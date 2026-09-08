package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// wfapiStage is one stage entry in /wfapi/describe.
type wfapiStage struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	DurationMillis  int64  `json:"durationMillis"`
	StartTimeMillis int64  `json:"startTimeMillis"`
	ExecNode        string `json:"execNode"`
}

// wfapiDescribe is the top-level shape of /wfapi/describe.
type wfapiDescribe struct {
	Status         string       `json:"status"`
	DurationMillis int64        `json:"durationMillis"`
	Stages         []wfapiStage `json:"stages"`
}

// wfapiLog is the shape returned by /execution/node/<id>/wfapi/log.
type wfapiLog struct {
	NodeID     string `json:"nodeId"`
	NodeStatus string `json:"nodeStatus"`
	Length     int    `json:"length"`
	HasMore    bool   `json:"hasMore"`
	Text       string `json:"text"`
	ConsoleURL string `json:"consoleUrl"`
}

var stageIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// GetPipelineStagesInput is the schema for get_pipeline_stages.
type GetPipelineStagesInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
}

// GetPipelineStages lists Declarative/Scripted Pipeline stages with status and
// duration via /wfapi/describe.
func (d Deps) GetPipelineStages(ctx context.Context, _ *mcp.CallToolRequest, in GetPipelineStagesInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) + "/wfapi/describe"
	body, err := d.Client.Get(ctx, path, nil)
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return textResult(
				"No pipeline stage data (HTTP 404 on /wfapi/describe). " +
					"This is not a Declarative/Scripted Pipeline build.",
			), nil, nil
		}
		return nil, nil, err
	}
	var desc wfapiDescribe
	if err := json.Unmarshal(body, &desc); err != nil {
		return nil, nil, fmt.Errorf("parse wfapi describe: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Pipeline status: %s (total %.1fs)\n\n", desc.Status, float64(desc.DurationMillis)/1000.0)
	fmt.Fprintf(&out, "%-4s  %-12s  %10s  %s\n", "id", "status", "duration", "name")
	fmt.Fprintf(&out, "%-4s  %-12s  %10s  %s\n", "----", "------------", "----------", "----")
	for _, s := range desc.Stages {
		fmt.Fprintf(&out, "%-4s  %-12s  %9.1fs  %s\n", s.ID, s.Status, float64(s.DurationMillis)/1000.0, s.Name)
	}
	out.WriteString(
		"\nUse get_stage_log with the stage id. If that log is empty, " +
			"use search_console_log or get_console_log (last 500 lines, max 2000).",
	)
	return textResult(out.String()), nil, nil
}

// GetStageLogInput is the schema for get_stage_log.
type GetStageLogInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
	StageID     string `json:"stage_id" jsonschema:"Stage id from get_pipeline_stages (e.g. \"188\")"`
}

// GetStageLog returns the log for a single pipeline stage.
func (d Deps) GetStageLog(ctx context.Context, _ *mcp.CallToolRequest, in GetStageLogInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" || in.StageID == "" {
		return nil, nil, fmt.Errorf("job_path and stage_id are required")
	}
	if !stageIDRe.MatchString(in.StageID) {
		return nil, nil, fmt.Errorf("invalid stage_id")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) +
		"/execution/node/" + in.StageID + "/wfapi/log"
	body, err := d.Client.Get(ctx, path, nil)
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return textResult(fmt.Sprintf(
				"No stage log at /execution/node/%s/wfapi/log (HTTP 404). "+
					"Stage id may be wrong or build is not a pipeline.",
				in.StageID,
			)), nil, nil
		}
		return nil, nil, err
	}
	var lg wfapiLog
	if err := json.Unmarshal(body, &lg); err != nil {
		return nil, nil, fmt.Errorf("parse wfapi log: %w", err)
	}
	if lg.Length == 0 {
		return textResult(fmt.Sprintf(
			"Stage %s (%s) has no captured log output (length=0). "+
				"This is common for declarative wrapper stages — "+
				"use search_console_log or get_console_log for the build console.",
			in.StageID, lg.NodeStatus,
		)), nil, nil
	}
	return textResult(fmt.Sprintf(
		"Stage %s log (%s, %d bytes, hasMore=%v):\n\n%s",
		in.StageID, lg.NodeStatus, lg.Length, lg.HasMore, lg.Text,
	)), nil, nil
}
