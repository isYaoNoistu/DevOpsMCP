package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

const (
	defaultRecentFailuresSince        = "24h"
	defaultRecentFailuresResultFilter = "FAILURE"
	defaultRecentFailuresMaxResults   = 100
	maxRecentFailuresMaxResults       = 500
	recentFailuresBuildPageSize       = 20
	recentFailuresMaxBuildsPerJob     = 100
	recentFailuresWideWindowThreshold = 7 * 24 * time.Hour
	recentFailuresJobPathWidth        = 50
	recentFailuresFinishedWidth       = 20
)

// recentFailuresBuildTree requests a bounded slice of newest-first builds.
func recentFailuresBuildTree(start, end int) string {
	return fmt.Sprintf("builds[number,result,timestamp,duration,url]{%d,%d}", start, end)
}

// validResultFilters is the closed set the tool accepts. ANY_NON_SUCCESS
// is a meta-filter that matches FAILURE, UNSTABLE, ABORTED.
var validResultFilters = map[string]bool{
	"FAILURE":         true,
	"UNSTABLE":        true,
	"ABORTED":         true,
	"ANY_NON_SUCCESS": true,
}

// FindRecentFailuresInput is the schema for find_recent_failures.
type FindRecentFailuresInput struct {
	FolderPath   string `json:"folder_path,omitempty" jsonschema:"Scope the search to a folder. Empty = Jenkins root."`
	Since        string `json:"since,omitempty" jsonschema:"Lookback window. Go duration like 24h, 30m, plus Nd for days. Default 24h."`
	ResultFilter string `json:"result_filter,omitempty" jsonschema:"One of FAILURE, UNSTABLE, ABORTED, ANY_NON_SUCCESS. Default FAILURE."`
	MaxResults   int    `json:"max_results,omitempty" jsonschema:"Cap on rows. Default 100, capped at 500."`
}

// recentFailureRow is one rendered row.
type recentFailureRow struct {
	JobPath     string
	BuildNumber int64
	Result      string
	Timestamp   int64 // ms since epoch
	Duration    int64 // ms
}

// recentFailureJobResult is the per-job outcome of the probe.
type recentFailureJobResult struct {
	JobPath    string
	Rows       []recentFailureRow
	Err        error
	Incomplete string
}

// FindRecentFailures surveys failed builds across the jobs under
// folder_path within the lookback window. Walks the job tree, fans out
// per-job /api/json probes (last N builds), filters by timestamp +
// result, and renders a sorted table.
func (d Deps) FindRecentFailures(ctx context.Context, _ *mcp.CallToolRequest, in FindRecentFailuresInput) (*mcp.CallToolResult, any, error) {
	sinceStr := in.Since
	if sinceStr == "" {
		sinceStr = defaultRecentFailuresSince
	}
	window, err := parseLookback(sinceStr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid since %q: %w", in.Since, err)
	}
	resultFilter := in.ResultFilter
	if resultFilter == "" {
		resultFilter = defaultRecentFailuresResultFilter
	}
	if !validResultFilters[resultFilter] {
		return nil, nil, fmt.Errorf("invalid result_filter %q (want FAILURE, UNSTABLE, ABORTED, or ANY_NON_SUCCESS)", in.ResultFilter)
	}
	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = defaultRecentFailuresMaxResults
	}
	if maxResults > maxRecentFailuresMaxResults {
		maxResults = maxRecentFailuresMaxResults
	}

	var entries []listingEntry
	hitCap := false
	if err := d.walkFolder(ctx, in.FolderPath, true, nil, &entries, &hitCap); err != nil {
		return nil, nil, err
	}
	leaves := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsFolder {
			leaves = append(leaves, e.JobPath)
		}
	}

	cutoffMs := time.Now().Add(-window).UnixMilli()
	results := fetchPerItem(leaves, func(job string) recentFailureJobResult {
		return d.probeOneJobForFailures(ctx, job, cutoffMs, resultFilter)
	})

	var rows []recentFailureRow
	var incomplete []recentFailureJobResult
	for _, r := range results {
		rows = append(rows, r.Rows...)
		if r.Err != nil || r.Incomplete != "" {
			incomplete = append(incomplete, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Timestamp > rows[j].Timestamp })

	truncated := false
	if len(rows) > maxResults {
		rows = rows[:maxResults]
		truncated = true
	}

	return textResult(renderRecentFailures(in.FolderPath, window, resultFilter, rows, len(leaves), incomplete, hitCap, truncated, maxResults)), nil, nil
}

func (d Deps) probeOneJobForFailures(ctx context.Context, jobPath string, cutoffMs int64, resultFilter string) recentFailureJobResult {
	res := recentFailureJobResult{JobPath: jobPath}
	path := jenkins.JobAPIPath(jobPath) + "/api/json"
	for start := 0; start < recentFailuresMaxBuildsPerJob; start += recentFailuresBuildPageSize {
		body, err := d.Client.Get(ctx, path, map[string]string{
			"tree": recentFailuresBuildTree(start, start+recentFailuresBuildPageSize),
		})
		if err != nil {
			res.Err = fmt.Errorf("%s: %w", jobPath, err)
			return res
		}
		var resp struct {
			Builds []struct {
				Number    int64  `json:"number"`
				Result    string `json:"result"`
				Timestamp int64  `json:"timestamp"`
				Duration  int64  `json:"duration"`
			} `json:"builds"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			res.Err = fmt.Errorf("parse builds for %s: %w", jobPath, err)
			return res
		}
		reachedCutoff := false
		for _, b := range resp.Builds {
			if b.Timestamp < cutoffMs {
				reachedCutoff = true
				continue
			}
			if resultMatches(b.Result, resultFilter) {
				res.Rows = append(res.Rows, recentFailureRow{
					JobPath: jobPath, BuildNumber: b.Number, Result: b.Result,
					Timestamp: b.Timestamp, Duration: b.Duration,
				})
			}
		}
		if reachedCutoff || len(resp.Builds) < recentFailuresBuildPageSize {
			return res
		}
	}
	res.Incomplete = fmt.Sprintf("build scan cap reached after %d builds", recentFailuresMaxBuildsPerJob)
	return res
}

func resultMatches(buildResult, filter string) bool {
	if filter == "ANY_NON_SUCCESS" {
		switch buildResult {
		case "FAILURE", "UNSTABLE", "ABORTED":
			return true
		}
		return false
	}
	return buildResult == filter
}

// parseLookback extends time.ParseDuration with day support ("7d" →
// 168h). Go's stdlib stops at hours because day length is ambiguous
// across calendar context; for this tool a "24-hour day" is fine.
func parseLookback(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("could not parse %q as days", s)
		}
		if days <= 0 || int64(days) > int64((1<<63-1)/(24*time.Hour)) {
			return 0, fmt.Errorf("days must be positive and fit in a duration")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, fmt.Errorf("lookback must be positive")
	}
	return duration, nil
}

func renderRecentFailures(folderPath string, window time.Duration, resultFilter string, rows []recentFailureRow, jobsScanned int, incomplete []recentFailureJobResult, listingIncomplete, truncated bool, maxResults int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Recent failures under %q (last %s, filter=%s):\n\n", folderPath, window, resultFilter)

	if len(rows) == 0 {
		if len(incomplete) == 0 && !listingIncomplete {
			out.WriteString("  (no matches)\n")
		} else {
			out.WriteString("  (no confirmed matches; scan incomplete)\n")
		}
	} else {
		fmt.Fprintf(&out, "  %s  %-6s  %-9s  %-20s  %s\n",
			padRight("job_path", recentFailuresJobPathWidth),
			"build", "result", "started", "duration")
		fmt.Fprintf(&out, "  %s  %s  %s  %s  %s\n",
			strings.Repeat("-", recentFailuresJobPathWidth),
			strings.Repeat("-", 6),
			strings.Repeat("-", 9),
			strings.Repeat("-", recentFailuresFinishedWidth),
			strings.Repeat("-", 8))
		for _, r := range rows {
			finished := time.UnixMilli(r.Timestamp).UTC().Format("2006-01-02 15:04 UTC")
			fmt.Fprintf(&out, "  %s  #%-5d  %-9s  %-20s  %s\n",
				padRight(truncate(r.JobPath, recentFailuresJobPathWidth), recentFailuresJobPathWidth),
				r.BuildNumber, r.Result, finished, formatBuildDuration(r.Duration))
		}
	}

	if truncated {
		fmt.Fprintf(&out, "\n(stopped at max_results=%d — narrow folder_path, since, or result_filter)\n", maxResults)
	}
	if listingIncomplete {
		fmt.Fprintf(&out, "\nScan incomplete: job listing cap of %d entries reached; narrow folder_path.\n", listJobsCap)
	}
	if window > recentFailuresWideWindowThreshold {
		out.WriteString("\n(wide window: builds were paged toward the cutoff; any per-job cap is reported as an incomplete scan)\n")
	}
	if len(incomplete) > 0 {
		out.WriteString("\nScan incomplete for:\n")
		for _, r := range incomplete {
			reason := r.Incomplete
			if r.Err != nil {
				reason = r.Err.Error()
			}
			fmt.Fprintf(&out, "  - %s: %s\n", r.JobPath, reason)
		}
	}
	if len(incomplete) == 0 {
		fmt.Fprintf(&out, "\n%d results across %d jobs scanned.\n", len(rows), jobsScanned)
	} else {
		fmt.Fprintf(&out, "\n%d results; %d jobs attempted, %d fully scanned.\n", len(rows), jobsScanned, jobsScanned-len(incomplete))
	}
	return out.String()
}
