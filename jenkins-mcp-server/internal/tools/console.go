package tools

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// ---- get_console_log ----

// GetConsoleLogInput is the schema for get_console_log.
type GetConsoleLogInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path, e.g. 'folder/subfolder/job-name'"`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
	TailLines   int    `json:"tail_lines,omitempty" jsonschema:"Lines from the end of the log to return. 0 (default) = last 500. Max 2000. Negative values are rejected."`
}

// GetConsoleLog returns the build's console output, tailed to TailLines.
func (d Deps) GetConsoleLog(ctx context.Context, _ *mcp.CallToolRequest, in GetConsoleLogInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	if in.TailLines < 0 {
		return nil, nil, fmt.Errorf("tail_lines must be >= 0; use search_console_log instead of requesting the full log")
	}
	tail := in.TailLines
	if tail == 0 {
		tail = 500
	}
	if tail > 2000 {
		tail = 2000
	}
	body, _, err := d.Cache.Fetch(ctx, in.JobPath, in.BuildNumber)
	if err != nil {
		return nil, nil, err
	}
	text := string(body)
	totalLines := 1 + strings.Count(string(body), "\n")
	if tail > 0 {
		lines := strings.Split(text, "\n")
		if len(lines) > tail {
			text = strings.Join(lines[len(lines)-tail:], "\n")
			text += fmt.Sprintf("\n\n--- last %d of ~%d lines; use search_console_log for the rest ---\n", tail, totalLines)
		}
	}
	return textResult(text), nil, nil
}

// ---- search_console_log ----

// SearchConsoleLogInput is the schema for search_console_log.
type SearchConsoleLogInput struct {
	JobPath      string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber  int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
	Pattern      string `json:"pattern" jsonschema:"Go regexp pattern (RE2 syntax)"`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"Lines of context before and after each match (default 3)"`
	MaxMatches   int    `json:"max_matches,omitempty" jsonschema:"Cap on matches returned (default 50)"`
}

// SearchConsoleLog runs an RE2 regex over the log and returns matches with
// surrounding context lines.
func (d Deps) SearchConsoleLog(ctx context.Context, _ *mcp.CallToolRequest, in SearchConsoleLogInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" || in.Pattern == "" {
		return nil, nil, fmt.Errorf("job_path and pattern are required")
	}
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid pattern: %w", err)
	}
	ctxLines := in.ContextLines
	if ctxLines == 0 {
		ctxLines = 3
	}
	maxMatches := in.MaxMatches
	if maxMatches == 0 {
		maxMatches = 50
	}

	body, _, err := d.Cache.Fetch(ctx, in.JobPath, in.BuildNumber)
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(string(body), "\n")

	var out strings.Builder
	matches := 0
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		matches++
		start := i - ctxLines
		if start < 0 {
			start = 0
		}
		end := i + ctxLines + 1
		if end > len(lines) {
			end = len(lines)
		}
		fmt.Fprintf(&out, "── match %d (line %d) ──\n", matches, i+1)
		for n := start; n < end; n++ {
			marker := "  "
			if n == i {
				marker = "→ "
			}
			fmt.Fprintf(&out, "%s%d: %s\n", marker, n+1, lines[n])
		}
		out.WriteString("\n")
		if matches >= maxMatches {
			fmt.Fprintf(&out, "(stopped at max_matches=%d)\n", maxMatches)
			break
		}
	}
	if matches == 0 {
		return textResult(fmt.Sprintf(
			"No matches for /%s/ in this build's console log.",
			in.Pattern,
		)), nil, nil
	}
	return textResult(out.String()), nil, nil
}

// ---- tail_running_build ----

const (
	defaultTailMaxBytes = 65536
	maxTailMaxBytes     = 1048576 // 1 MB hard cap on per-call payload
)

// TailRunningBuildInput is the schema for tail_running_build.
type TailRunningBuildInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path."`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build to tail. Use 0 or omit for lastBuild."`
	SinceByte   int64  `json:"since_byte,omitempty" jsonschema:"Start byte offset (echo back from the previous call's footer)."`
	MaxBytes    int    `json:"max_bytes,omitempty" jsonschema:"Cap on bytes returned per call. Default 65536, max 1048576."`
}

// TailRunningBuild streams a capped slice of an in-flight build's console
// via /<job>/<build>/logText/progressiveText. The tool never writes to
// the on-disk cache — the cache invariant (only finished builds) holds.
// Callers echo the footer's `Next since_byte` back on the follow-up call
// to advance through the log without re-fetching the prefix.
func (d Deps) TailRunningBuild(ctx context.Context, _ *mcp.CallToolRequest, in TailRunningBuildInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	if in.SinceByte < 0 {
		return nil, nil, fmt.Errorf("since_byte must be >= 0")
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultTailMaxBytes
	}
	if maxBytes > maxTailMaxBytes {
		maxBytes = maxTailMaxBytes
	}

	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) + "/logText/progressiveText"
	body, headers, err := d.Client.GetWithHeaders(ctx, path, map[string]string{
		"start": strconv.FormatInt(in.SinceByte, 10),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("progressiveText for %s build %s: %w",
			in.JobPath, jenkins.BuildRef(in.BuildNumber), err)
	}

	textSize, _ := strconv.ParseInt(headers.Get("X-Text-Size"), 10, 64)
	buildRunning := strings.EqualFold(headers.Get("X-More-Data"), "true")

	truncated := false
	nextByte := textSize
	if int64(len(body)) > int64(maxBytes) {
		body = body[:maxBytes]
		nextByte = in.SinceByte + int64(maxBytes)
		truncated = true
	}
	// "more" means "the agent should call again" — true if either the
	// build hasn't finished or we truncated this chunk.
	more := buildRunning || truncated

	var out strings.Builder
	if len(body) == 0 {
		if buildRunning {
			out.WriteString("(no new bytes; build still running)\n")
		} else {
			out.WriteString("(no new bytes; build finished)\n")
		}
	} else {
		out.Write(body)
		if body[len(body)-1] != '\n' {
			out.WriteByte('\n')
		}
	}

	out.WriteString("--- ")
	fmt.Fprintf(&out, "bytes %d..%d (more=%v", in.SinceByte, nextByte, more)
	if !buildRunning {
		out.WriteString("; build finished")
	}
	out.WriteString("). ")
	if more {
		fmt.Fprintf(&out, "Next since_byte=%d", nextByte)
	} else {
		out.WriteString("Use get_console_log or search_console_log for the completed log.")
	}
	out.WriteString(" ---\n")
	return textResult(out.String()), nil, nil
}
