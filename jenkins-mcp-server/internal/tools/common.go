// Package tools implements the MCP tool handlers exposed by the server.
//
// Each file groups related tools. All handlers share a Deps value carrying
// the Jenkins client and the on-disk console-log cache.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// Deps is the shared dependency set passed to every tool handler.
type Deps struct {
	Client   *jenkins.Client
	Cache    *jenkins.ConsoleCache
	Version  string
	ReadOnly bool
}

// textResult wraps a string as an mcp.CallToolResult.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// compileFilter compiles a case-insensitive RE2 regex used by the *_filter
// tool inputs. Returns (nil, nil) when expr is empty so callers can guard
// with a plain nil check. paramName is the JSON field name and is woven
// into the error so the caller sees which filter failed to parse.
func compileFilter(paramName, expr string) (*regexp.Regexp, error) {
	if expr == "" {
		return nil, nil
	}
	re, err := regexp.Compile("(?i)" + expr)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", paramName, err)
	}
	return re, nil
}

// formatBuildDuration renders Jenkins' millisecond duration as a compact
// Go-style string truncated to whole seconds (e.g. "2m15s", "1h2m3s").
func formatBuildDuration(millis int64) string {
	return (time.Duration(millis) * time.Millisecond).Truncate(time.Second).String()
}

// truncate shortens s to at most n runes, replacing the trailing rune with
// "…" when truncation happens. Rune-aware so it never splits a multi-byte
// codepoint and so callers get a predictable display width.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// padRight pads s with spaces on the right so its rune-counted width
// equals width. Use this for tabular columns whose contents may include
// multi-byte runes — Go's %-Ns format pads by byte count, which under-pads
// non-ASCII or the "…" returned by truncate.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// perBuildFetchConcurrency caps in-flight per-build requests so a 50-build
// sample doesn't open 50 sockets at once against the Jenkins controller.
// Shared by tools that walk recent builds (get_flaky_candidates,
// get_test_history).
const perBuildFetchConcurrency = 6

// completedBuildsBuffer extends the build-list fetch beyond sample_size so
// in-progress builds at the head don't shrink the effective sample.
const completedBuildsBuffer = 5

// completedBuildListTreeFmt is the `tree` selector for the latest N builds
// with just number + result. result tells us whether the build is
// completed; the {0,N} range avoids dragging in 1000-entry build histories.
const completedBuildListTreeFmt = "builds[number,result]{0,%d}"

// completedBuild is one entry returned by discoverCompletedBuildsWithResult.
type completedBuild struct {
	Number int64
	Result string
}

// discoverCompletedBuilds returns the latest sample_size completed build
// numbers under jobPath. Convenience wrapper over
// discoverCompletedBuildsWithResult for callers that only need numbers.
func (d Deps) discoverCompletedBuilds(ctx context.Context, jobPath string, sampleSize int) ([]int64, error) {
	builds, err := d.discoverCompletedBuildsWithResult(ctx, jobPath, sampleSize)
	if err != nil {
		return nil, err
	}
	nums := make([]int64, len(builds))
	for i, b := range builds {
		nums[i] = b.Number
	}
	return nums, nil
}

// discoverCompletedBuildsWithResult returns the latest sample_size completed
// builds under jobPath, with each build's result (SUCCESS/FAILURE/UNSTABLE/
// ABORTED). In-progress builds at the head are filtered out, so the fetch
// asks for sample_size + completedBuildsBuffer to leave headroom.
func (d Deps) discoverCompletedBuildsWithResult(ctx context.Context, jobPath string, sampleSize int) ([]completedBuild, error) {
	apiPath := jenkins.JobAPIPath(jobPath) + "/api/json"
	tree := fmt.Sprintf(completedBuildListTreeFmt, sampleSize+completedBuildsBuffer)
	body, err := d.Client.Get(ctx, apiPath, map[string]string{"tree": tree})
	if err != nil {
		return nil, fmt.Errorf("list builds for %s: %w", jobPath, err)
	}
	var listing struct {
		Builds []struct {
			Number int64  `json:"number"`
			Result string `json:"result"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("parse build listing: %w", err)
	}
	out := make([]completedBuild, 0, sampleSize)
	for _, b := range listing.Builds {
		if b.Result == "" {
			continue
		}
		out = append(out, completedBuild{Number: b.Number, Result: b.Result})
		if len(out) >= sampleSize {
			break
		}
	}
	return out, nil
}

// fetchPerItem runs fetchFn against each item concurrently, capped at
// perBuildFetchConcurrency, and returns results in input order. Used to
// fan out per-build (int64-keyed) or per-job (string-keyed) HTTP requests
// against Jenkins without opening N sockets at once.
func fetchPerItem[K, T any](items []K, fetchFn func(K) T) []T {
	results := make([]T, len(items))
	sem := make(chan struct{}, perBuildFetchConcurrency)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(idx int, k K) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = fetchFn(k)
		}(i, item)
	}
	wg.Wait()
	return results
}
