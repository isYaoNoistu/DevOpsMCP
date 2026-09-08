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
	recentFailuresPerJobBuilds        = 5
	recentFailuresWideWindowThreshold = 7 * 24 * time.Hour
	recentFailuresJobPathWidth        = 50
	recentFailuresFinishedWidth       = 20
)

// recentFailuresPerJobTree fetches just enough per-job to filter and
// render. {0,N} keeps the response bounded — the issue spec settled on
// 5 since 24h windows are the common case.
var recentFailuresPerJobTree = fmt.Sprintf(
	"builds[number,result,timestamp,duration,url]{0,%d}",
	recentFailuresPerJobBuilds,
)

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
	Rows []recentFailureRow
	Err  error
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
	for _, r := range results {
		if r.Err != nil {
			return nil, nil, r.Err
		}
		rows = append(rows, r.Rows...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Timestamp > rows[j].Timestamp })

	truncated := false
	if len(rows) > maxResults {
		rows = rows[:maxResults]
		truncated = true
	}

	return textResult(renderRecentFailures(in.FolderPath, window, resultFilter, rows, len(leaves), truncated, maxResults)), nil, nil
}

func (d Deps) probeOneJobForFailures(ctx context.Context, jobPath string, cutoffMs int64, resultFilter string) recentFailureJobResult {
	res := recentFailureJobResult{}
	path := jenkins.JobAPIPath(jobPath) + "/api/json"
	body, err := d.Client.Get(ctx, path, map[string]string{"tree": recentFailuresPerJobTree})
	if err != nil {
		// Probe failures (404 etc.) just contribute no rows; the job is
		// still counted as scanned in the footer.
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
	for _, b := range resp.Builds {
		if b.Timestamp < cutoffMs {
			continue
		}
		if !resultMatches(b.Result, resultFilter) {
			continue
		}
		res.Rows = append(res.Rows, recentFailureRow{
			JobPath: jobPath, BuildNumber: b.Number, Result: b.Result,
			Timestamp: b.Timestamp, Duration: b.Duration,
		})
	}
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
		if days < 0 {
			return 0, fmt.Errorf("negative days: %d", days)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func renderRecentFailures(folderPath string, window time.Duration, resultFilter string, rows []recentFailureRow, jobsScanned int, truncated bool, maxResults int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Recent failures under %q (last %s, filter=%s):\n\n", folderPath, window, resultFilter)

	if len(rows) == 0 {
		out.WriteString("  (no matches)\n")
	} else {
		fmt.Fprintf(&out, "  %s  %-6s  %-9s  %-20s  %s\n",
			padRight("job_path", recentFailuresJobPathWidth),
			"build", "result", "finished", "duration")
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
	if window > recentFailuresWideWindowThreshold {
		fmt.Fprintf(&out,
			"\n(wide window: since > 7d — only the last %d builds per job were inspected; older failures in the window may be missed)\n",
			recentFailuresPerJobBuilds)
	}
	fmt.Fprintf(&out, "\n%d results across %d jobs scanned.\n", len(rows), jobsScanned)
	return out.String()
}
