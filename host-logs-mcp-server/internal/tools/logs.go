package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"host-logs-mcp-server/internal/limits"
	"host-logs-mcp-server/internal/logop"
	"host-logs-mcp-server/internal/quote"
	"host-logs-mcp-server/internal/targets"
)

type Deps struct {
	Reg     *targets.Registry
	SSH     logop.SSHConfig
	Version string
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(b)), v, nil
}

func (d Deps) resolve(query string) (targets.Target, error) {
	t, hits, err := d.Reg.Resolve(query)
	if err != nil {
		if len(hits) > 0 {
			names := make([]string, 0, len(hits))
			for _, h := range hits {
				names = append(names, h.Name)
			}
			return targets.Target{}, fmt.Errorf("%w; candidates: %s", err, strings.Join(names, ", "))
		}
		return targets.Target{}, err
	}
	return *t, nil
}

type ListTargetsInput struct {
	Query string `json:"query,omitempty" jsonschema:"Optional filter against name, aliases, tags, host, paths."`
}

func (d Deps) ListTargets(_ context.Context, _ *mcp.CallToolRequest, in ListTargetsInput) (*mcp.CallToolResult, any, error) {
	all, err := d.Reg.List()
	if err != nil {
		return nil, nil, err
	}
	hits := targets.Filter(all, in.Query)
	views := make([]map[string]any, 0, len(hits))
	for _, t := range hits {
		views = append(views, t.PublicView())
	}
	return jsonResult(map[string]any{
		"targets_file": d.Reg.Path(),
		"query":        in.Query,
		"count":        len(views),
		"targets":      views,
		"note":         "name is the stable target id. SSH passwords and keys are not returned.",
	})
}

type GetTargetInfoInput struct {
	Target string `json:"target" jsonschema:"Stable target name or unique alias."`
}

func (d Deps) GetTargetInfo(_ context.Context, _ *mcp.CallToolRequest, in GetTargetInfoInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	view := t.PublicView()
	view["note"] = "Only allowlisted paths can be listed or searched. No exec, no writes."
	return jsonResult(view)
}

type ListLogFilesInput struct {
	Target string `json:"target" jsonschema:"Stable target name."`
	Path   string `json:"path,omitempty" jsonschema:"Absolute directory or file under the allowlist. Empty = all allowlisted roots."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max files, default 200."`
}

func (d Deps) ListLogFiles(ctx context.Context, _ *mcp.CallToolRequest, in ListLogFilesInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	if t.IsLocal() {
		files, err := logop.ListLocal(t, in.Path, in.Limit)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{
			"target": t.Name,
			"count":  len(files),
			"files":  files,
		})
	}
	out, err := logop.ListRemote(ctx, t, d.SSH, in.Path, in.Limit)
	if err != nil {
		return nil, nil, err
	}
	lines := splitNonEmpty(out)
	files := make([]map[string]string, 0, len(lines))
	for _, l := range lines {
		files = append(files, map[string]string{"path": l})
	}
	return jsonResult(map[string]any{
		"target": t.Name,
		"count":  len(files),
		"files":  files,
	})
}

type SearchLogInput struct {
	Target        string `json:"target" jsonschema:"Stable target name."`
	Path          string `json:"path" jsonschema:"Absolute log file under the allowlist. Not a directory."`
	Pattern       string `json:"pattern" jsonschema:"Text to match. Default regex: remote grep -E (POSIX ERE, use [0-9] not \\d); local Go regexp. Set fixed=true for literal."`
	Fixed         bool   `json:"fixed,omitempty" jsonschema:"If true, match as literal substring (grep -F)."`
	Start         string `json:"start,omitempty" jsonschema:"Inclusive lexicographic whole-line prefix, e.g. 2026-09-17 05:38:00. Not a parsed timestamp."`
	End           string `json:"end,omitempty" jsonschema:"Exclusive lexicographic whole-line prefix, e.g. 2026-09-17 05:45:00. Not a parsed timestamp."`
	MaxLines      int    `json:"max_lines,omitempty" jsonschema:"Max matching lines, default 80, cap 200."`
	ContextBefore int    `json:"context_before,omitempty" jsonschema:"Lines before each match, 0-5."`
	ContextAfter  int    `json:"context_after,omitempty" jsonschema:"Lines after each match, 0-5."`
}

func (d Deps) SearchLog(ctx context.Context, _ *mcp.CallToolRequest, in SearchLogInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.Path) == "" {
		return nil, nil, fmt.Errorf("path is required")
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return nil, nil, fmt.Errorf("pattern is required")
	}
	if err := quote.CheckRemoteArg("pattern", in.Pattern, limits.MaxPattern); err != nil {
		return nil, nil, err
	}
	if err := quote.CheckRemoteArg("start", in.Start, 128); err != nil {
		return nil, nil, err
	}
	if err := quote.CheckRemoteArg("end", in.End, 128); err != nil {
		return nil, nil, err
	}
	opt := logop.SearchOpts{
		Path:          in.Path,
		Pattern:       in.Pattern,
		Fixed:         in.Fixed,
		Start:         in.Start,
		End:           in.End,
		MaxLines:      in.MaxLines,
		ContextBefore: in.ContextBefore,
		ContextAfter:  in.ContextAfter,
	}
	if t.IsLocal() {
		hits, truncated, err := logop.SearchLocal(t, opt)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{
			"target":    t.Name,
			"path":      in.Path,
			"count":     len(hits),
			"truncated": truncated,
			"lines":     hits,
		})
	}
	out, err := logop.SearchRemote(ctx, t, d.SSH, opt)
	if err != nil {
		return nil, nil, err
	}
	lines := splitKeep(out)
	n := limits.ClampLines(in.MaxLines, limits.DefaultLines, limits.MaxLines)
	truncated := len(lines) >= n || strings.Contains(out, "...[truncated]")
	return jsonResult(map[string]any{
		"target":    t.Name,
		"path":      in.Path,
		"count":     len(lines),
		"truncated": truncated,
		"lines":     lines,
	})
}

type TailLogInput struct {
	Target string `json:"target" jsonschema:"Stable target name."`
	Path   string `json:"path" jsonschema:"Absolute log file under the allowlist."`
	Lines  int    `json:"lines,omitempty" jsonschema:"Last N lines, default 80, cap 200."`
}

func (d Deps) TailLog(ctx context.Context, _ *mcp.CallToolRequest, in TailLogInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.Path) == "" {
		return nil, nil, fmt.Errorf("path is required")
	}
	if t.IsLocal() {
		lines, err := logop.TailLocal(t, in.Path, in.Lines)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{
			"target": t.Name,
			"path":   in.Path,
			"count":  len(lines),
			"lines":  lines,
		})
	}
	out, err := logop.TailRemote(ctx, t, d.SSH, in.Path, in.Lines)
	if err != nil {
		return nil, nil, err
	}
	lines := splitKeep(out)
	return jsonResult(map[string]any{
		"target": t.Name,
		"path":   in.Path,
		"count":  len(lines),
		"lines":  lines,
	})
}

func splitNonEmpty(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitKeep(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

func ParseDuration(env string, def time.Duration) time.Duration {
	v := strings.TrimSpace(env)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
