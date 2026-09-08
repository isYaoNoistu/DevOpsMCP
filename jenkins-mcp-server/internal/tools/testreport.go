package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

type junitCase struct {
	ClassName       string  `json:"className"`
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	Duration        float64 `json:"duration"`
	ErrorDetails    *string `json:"errorDetails"`
	ErrorStackTrace *string `json:"errorStackTrace"`
}

type junitSuite struct {
	Name  string      `json:"name"`
	Cases []junitCase `json:"cases"`
}

type junitReport struct {
	FailCount int          `json:"failCount"`
	PassCount int          `json:"passCount"`
	SkipCount int          `json:"skipCount"`
	Duration  float64      `json:"duration"`
	Suites    []junitSuite `json:"suites"`
}

// GetTestReportInput is the schema for get_test_report.
type GetTestReportInput struct {
	JobPath         string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber     int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
	StackTraceLines int    `json:"stack_trace_lines,omitempty" jsonschema:"Lines of head+tail to show from each failed case's stack trace (default 30 head + 30 tail)"`
}

// HeadTail returns the first n + last n lines of s, joining them with an
// elision marker when s is longer than 2n lines. Exported because the test
// suite covers it directly.
func HeadTail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 2*n {
		return s
	}
	head := strings.Join(lines[:n], "\n")
	tail := strings.Join(lines[len(lines)-n:], "\n")
	return head + "\n... (" + strconv.Itoa(len(lines)-2*n) + " lines elided) ...\n" + tail
}

// GetTestReport returns the structured JUnit results from /testReport/api/json.
// Failed cases are returned with name, duration, error details, and a
// head+tail snippet of the stack trace.
func (d Deps) GetTestReport(ctx context.Context, _ *mcp.CallToolRequest, in GetTestReportInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	stLines := in.StackTraceLines
	if stLines == 0 {
		stLines = 30
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) + "/testReport/api/json"
	body, err := d.Client.Get(ctx, path, nil)
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return textResult(
				"No test report published for this build (HTTP 404 on /testReport/api/json). " +
					"The job may not have a JUnit publisher configured — " +
					"use get_ginkgo_failure_summary against the console log instead.",
			), nil, nil
		}
		return nil, nil, err
	}
	var report junitReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, nil, fmt.Errorf("parse test report JSON: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Test Report — build %s of %s\n", jenkins.BuildRef(in.BuildNumber), in.JobPath)
	fmt.Fprintf(&out, "Counts: %d passed, %d failed, %d skipped (total %d)\n",
		report.PassCount, report.FailCount, report.SkipCount,
		report.PassCount+report.FailCount+report.SkipCount)
	fmt.Fprintf(&out, "Total duration: %.1fs\n\n", report.Duration)

	failIdx := 0
	for _, suite := range report.Suites {
		for _, c := range suite.Cases {
			if c.Status != "FAILED" && c.Status != "REGRESSION" {
				continue
			}
			failIdx++
			fmt.Fprintf(&out, "=== Failed test %d of %d ===\n", failIdx, report.FailCount)
			fmt.Fprintf(&out, "Suite:    %s\n", suite.Name)
			fmt.Fprintf(&out, "Class:    %s\n", c.ClassName)
			fmt.Fprintf(&out, "Name:     %s\n", c.Name)
			fmt.Fprintf(&out, "Duration: %.2fs\n", c.Duration)
			fmt.Fprintf(&out, "Status:   %s\n", c.Status)
			if c.ErrorDetails != nil && *c.ErrorDetails != "" {
				fmt.Fprintf(&out, "\nerrorDetails:\n  %s\n",
					strings.ReplaceAll(*c.ErrorDetails, "\n", "\n  "))
			}
			if c.ErrorStackTrace != nil && *c.ErrorStackTrace != "" {
				fmt.Fprintf(&out, "\nerrorStackTrace (first %d + last %d lines):\n%s\n",
					stLines, stLines, HeadTail(*c.ErrorStackTrace, stLines))
			}
			out.WriteString("\n")
		}
	}
	if failIdx == 0 {
		out.WriteString("No FAILED cases in report.\n")
	}
	return textResult(out.String()), nil, nil
}
