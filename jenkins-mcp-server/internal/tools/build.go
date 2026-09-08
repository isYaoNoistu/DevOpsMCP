package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// GetBuildInfoInput is the schema for get_build_info.
type GetBuildInfoInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
}

// buildInfoTree is the `tree` query selector for the build summary. Listed
// explicitly so the response is small and stable across Jenkins versions.
const buildInfoTree = "number,result,building,duration,estimatedDuration,timestamp,url," +
	"actions[parameters[name,value]]," +
	"changeSet[items[author[fullName],msg,commitId]]"

// GetBuildInfo returns the build's pretty-printed summary (result, duration,
// parameters, change set).
func (d Deps) GetBuildInfo(ctx context.Context, _ *mcp.CallToolRequest, in GetBuildInfoInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) + "/api/json"
	body, err := d.Client.Get(ctx, path, map[string]string{"tree": buildInfoTree})
	if err != nil {
		return nil, nil, err
	}
	var pretty any
	if err := json.Unmarshal(body, &pretty); err == nil {
		if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			return textResult(string(b)), nil, nil
		}
	}
	return textResult(string(body)), nil, nil
}

// GetBuildEnvironmentInput is the schema for get_build_environment.
type GetBuildEnvironmentInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number to inspect. Use 0 or omit for lastBuild."`
	NameFilter  string `json:"name_filter,omitempty" jsonschema:"Case-insensitive RE2 regex applied to injected env var names only."`
}

// buildEnvTree is the actions selector for cause + parameters in one
// /api/json call. Listed explicitly so the response is small and
// stable across Jenkins versions.
const buildEnvTree = "actions[" +
	"causes[shortDescription,upstreamProject,upstreamBuild,userId,userName]," +
	"parameters[name,value]]"

// GetBuildEnvironment renders three sections — Cause, Parameters, and
// Injected Env Vars — for a single build. Cause + parameters come from
// /api/json; the env-var section is provided by the EnvInject plugin and
// degrades gracefully when the plugin isn't installed (HTTP 404).
func (d Deps) GetBuildEnvironment(ctx context.Context, _ *mcp.CallToolRequest, in GetBuildEnvironmentInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	nameRe, err := compileFilter("name_filter", in.NameFilter)
	if err != nil {
		return nil, nil, err
	}

	buildRef := jenkins.BuildRef(in.BuildNumber)
	apiPath := jenkins.JobAPIPath(in.JobPath) + "/" + buildRef + "/api/json"
	body, err := d.Client.Get(ctx, apiPath, map[string]string{"tree": buildEnvTree})
	if err != nil {
		return nil, nil, fmt.Errorf("build env for %s build %s: %w", in.JobPath, buildRef, err)
	}
	var apiResp struct {
		Actions []struct {
			Causes []struct {
				ShortDescription string `json:"shortDescription"`
			} `json:"causes"`
			Parameters []struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"parameters"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("parse build env JSON: %w", err)
	}

	var out strings.Builder
	out.WriteString("Cause:\n")
	wroteCause := false
	for _, a := range apiResp.Actions {
		for _, c := range a.Causes {
			if c.ShortDescription != "" {
				fmt.Fprintf(&out, "  %s\n", c.ShortDescription)
				wroteCause = true
			}
		}
	}
	if !wroteCause {
		out.WriteString("  (none)\n")
	}

	out.WriteString("\nParameters:\n")
	wroteParam := false
	for _, a := range apiResp.Actions {
		for _, p := range a.Parameters {
			fmt.Fprintf(&out, "  %s=%s\n", p.Name, formatParamValue(p.Value))
			wroteParam = true
		}
	}
	if !wroteParam {
		out.WriteString("  (none)\n")
	}

	out.WriteString("\n")
	envPath := jenkins.JobAPIPath(in.JobPath) + "/" + buildRef + "/injectedEnvVars/api/json"
	envBody, envErr := d.Client.Get(ctx, envPath, nil)
	switch {
	case jenkins.IsHTTPStatus(envErr, http.StatusNotFound):
		out.WriteString("Injected Env Vars: EnvInject plugin not installed (HTTP 404 on injectedEnvVars/api/json)\n")
	case envErr != nil:
		fmt.Fprintf(&out, "Injected Env Vars: error fetching: %v\n", envErr)
	default:
		out.WriteString(renderInjectedEnvVars(envBody, nameRe, in.NameFilter))
	}

	return textResult(out.String()), nil, nil
}

// formatParamValue renders a parameter value for the report. Jenkins
// returns null for secret-typed parameters when the user lacks access —
// we surface that as `(masked)` so callers don't mistake it for empty.
func formatParamValue(v any) string {
	if v == nil {
		return "(masked)"
	}
	return fmt.Sprintf("%v", v)
}

// renderInjectedEnvVars formats the injected env-var section: sorted
// alphabetically, optionally filtered by RE2, with a header that reports
// both the unfiltered and filtered counts when a filter is applied.
func renderInjectedEnvVars(body []byte, nameRe *regexp.Regexp, rawFilter string) string {
	var resp struct {
		EnvMap map[string]string `json:"envMap"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Injected Env Vars: parse error: %v\n", err)
	}
	total := len(resp.EnvMap)
	keys := make([]string, 0, total)
	for k := range resp.EnvMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if nameRe != nil {
		filtered := keys[:0]
		for _, k := range keys {
			if nameRe.MatchString(k) {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}

	var out strings.Builder
	if nameRe != nil {
		fmt.Fprintf(&out, "Injected Env Vars (%d total, %d after filter):\n", total, len(keys))
	} else {
		fmt.Fprintf(&out, "Injected Env Vars (%d total):\n", total)
	}
	if len(keys) == 0 {
		if nameRe != nil {
			fmt.Fprintf(&out, "  (no matches for %q)\n", rawFilter)
		} else {
			out.WriteString("  (none)\n")
		}
		return out.String()
	}
	for _, k := range keys {
		fmt.Fprintf(&out, "  %s=%s\n", k, redactEnvValue(k, resp.EnvMap[k]))
	}
	return out.String()
}

func redactEnvValue(name, value string) string {
	n := strings.ToLower(name)
	for _, needle := range []string{
		"password", "passwd", "secret", "token", "api_key", "apikey", "private_key",
	} {
		if strings.Contains(n, needle) {
			return "(redacted)"
		}
	}
	return value
}
