package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// HealthCheckInput is the (empty) input schema for health_check.
type HealthCheckInput struct{}

const (
	statusOK    = "OK   "
	statusWarn  = "WARN "
	statusError = "ERROR"
)

// healthRow is one line in the rendered health report.
type healthRow struct {
	Name   string
	Status string
	Detail string
}

// pluginCheck names a Jenkins plugin to probe by its `shortName`.
// The label is what we render to the user; the shortName is what Jenkins
// reports in /pluginManager/api/json.
type pluginCheck struct {
	Label     string
	ShortName string
}

// pluginsToCheck is the small set of plugins health_check probes. Pipeline
// powers get_pipeline_stages/get_stage_log; JUnit powers get_test_report.
var pluginsToCheck = []pluginCheck{
	{"Pipeline plugin", "workflow-aggregator"},
	{"JUnit plugin", "junit"},
}

// clockSkewWarn is the absolute threshold above which a server/local clock
// difference is surfaced as WARN. Jenkins' build timestamps and the agent's
// reasoning about "when did this happen" both lean on these clocks staying
// close.
const clockSkewWarn = 60 * time.Second

// HealthCheck runs a fixed battery of read-only probes against the configured
// Jenkins and renders a one-line-per-check report. Each check fails open: a
// single subcheck error becomes a row, not an aborted tool call. Use this to
// validate a fresh install or to debug "the agent says it can't see Jenkins".
func (d Deps) HealthCheck(ctx context.Context, _ *mcp.CallToolRequest, _ HealthCheckInput) (*mcp.CallToolResult, any, error) {
	rows := make([]healthRow, 0, 8)
	var serverDate time.Time

	// 1) Reachability + Jenkins version (X-Jenkins header on any /api/json).
	_, headers, err := d.Client.GetWithHeaders(ctx, "/api/json", map[string]string{"tree": "mode"})
	switch {
	case err != nil:
		rows = append(rows, healthRow{"Jenkins reachable", statusError, err.Error()})
	default:
		if v := headers.Get("X-Jenkins"); v != "" {
			rows = append(rows, healthRow{"Jenkins reachable", statusOK, "version " + v})
		} else {
			rows = append(rows, healthRow{"Jenkins reachable", statusWarn,
				"no X-Jenkins header (reverse proxy stripping it?)"})
		}
		if dh := headers.Get("Date"); dh != "" {
			if t, perr := http.ParseTime(dh); perr == nil {
				serverDate = t
			}
		}
	}

	// 2) Authenticated user (/me/api/json).
	rows = append(rows, checkAuth(ctx, d.Client))

	// 3) CSRF crumb issuer — 404 means disabled. Fine for this GET-only MCP.
	rows = append(rows, checkCrumb(ctx, d.Client))

	// 4) Plugins that back specific tools.
	rows = append(rows, checkPlugins(ctx, d.Client, pluginsToCheck)...)

	// 5) Online vs offline executor counts.
	rows = append(rows, checkNodes(ctx, d.Client))

	// 6) Clock skew, only when we have a server Date to compare against.
	if !serverDate.IsZero() {
		rows = append(rows, checkClockSkew(serverDate))
	}

	return textResult(renderHealth(rows, d)), nil, nil
}

func checkAuth(ctx context.Context, c *jenkins.Client) healthRow {
	body, err := c.Get(ctx, "/me/api/json", map[string]string{"tree": "fullName,id"})
	if err != nil {
		return healthRow{"Authenticated", statusError, err.Error()}
	}
	var me struct {
		FullName string `json:"fullName"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return healthRow{"Authenticated", statusWarn, "parse /me: " + err.Error()}
	}
	detail := me.ID
	if detail == "" {
		detail = me.FullName
	} else if me.FullName != "" && me.FullName != me.ID {
		detail = fmt.Sprintf("%s (%s)", me.ID, me.FullName)
	}
	if detail == "" {
		detail = "anonymous"
	}
	return healthRow{"Authenticated", statusOK, detail}
}

func checkCrumb(ctx context.Context, c *jenkins.Client) healthRow {
	if _, err := c.Get(ctx, "/crumbIssuer/api/json", nil); err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return healthRow{"CSRF crumb issuer", statusWarn,
				"disabled (GET-only MCP does not POST)"}
		}
		return healthRow{"CSRF crumb issuer", statusError, err.Error()}
	}
	return healthRow{"CSRF crumb issuer", statusOK, "enabled"}
}

func checkPlugins(ctx context.Context, c *jenkins.Client, checks []pluginCheck) []healthRow {
	rows := make([]healthRow, 0, len(checks))
	body, err := c.Get(ctx, "/pluginManager/api/json", map[string]string{
		"tree":  "plugins[shortName,active,version]",
		"depth": "1",
	})
	if err != nil {
		msg := "plugin status unknown (need PluginManager permission)"
		if jenkins.IsHTTPStatus(err, http.StatusForbidden) {
			msg = "plugin status unknown (HTTP 403; read-only account has no PluginManager access, expected)"
		} else if jenkins.IsHTTPStatus(err, http.StatusUnauthorized) {
			msg = "plugin status unknown (HTTP 401)"
		}
		for _, ch := range checks {
			rows = append(rows, healthRow{ch.Label, statusWarn, msg})
		}
		return rows
	}
	var resp struct {
		Plugins []struct {
			ShortName string `json:"shortName"`
			Active    bool   `json:"active"`
			Version   string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		for _, ch := range checks {
			rows = append(rows, healthRow{ch.Label, statusError,
				"parse /pluginManager: " + err.Error()})
		}
		return rows
	}
	have := make(map[string]struct {
		active  bool
		version string
	}, len(resp.Plugins))
	for _, p := range resp.Plugins {
		have[p.ShortName] = struct {
			active  bool
			version string
		}{p.Active, p.Version}
	}
	for _, ch := range checks {
		h, ok := have[ch.ShortName]
		switch {
		case !ok:
			rows = append(rows, healthRow{ch.Label, statusWarn, "not installed"})
		case !h.active:
			rows = append(rows, healthRow{ch.Label, statusWarn,
				"installed but inactive (" + h.version + ")"})
		default:
			rows = append(rows, healthRow{ch.Label, statusOK,
				"installed (" + h.version + ")"})
		}
	}
	return rows
}

func checkNodes(ctx context.Context, c *jenkins.Client) healthRow {
	body, err := c.Get(ctx, "/computer/api/json", map[string]string{
		"tree": "computer[offline,temporarilyOffline]",
	})
	if err != nil {
		return healthRow{"Nodes", statusError, err.Error()}
	}
	var listing struct {
		Computer []struct {
			Offline            bool `json:"offline"`
			TemporarilyOffline bool `json:"temporarilyOffline"`
		} `json:"computer"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return healthRow{"Nodes", statusError, "parse /computer: " + err.Error()}
	}
	online, offline := 0, 0
	for _, n := range listing.Computer {
		if n.Offline || n.TemporarilyOffline {
			offline++
		} else {
			online++
		}
	}
	status := statusOK
	if online == 0 {
		status = statusWarn
	}
	return healthRow{"Nodes", status, fmt.Sprintf("%d online, %d offline", online, offline)}
}

func checkClockSkew(serverDate time.Time) healthRow {
	skew := time.Since(serverDate)
	abs := skew
	if abs < 0 {
		abs = -abs
	}
	status := statusOK
	if abs > clockSkewWarn {
		status = statusWarn
	}
	return healthRow{"Clock skew", status, formatSkew(skew)}
}

func formatSkew(d time.Duration) string {
	abs := d
	if abs < 0 {
		abs = -abs
	}
	abs = abs.Truncate(time.Millisecond)
	if d >= 0 {
		return fmt.Sprintf("server is %s behind local time", abs)
	}
	return fmt.Sprintf("server is %s ahead of local time", abs)
}

func renderHealth(rows []healthRow, d Deps) string {
	var out strings.Builder
	out.WriteString("Jenkins MCP health check\n\n")
	for _, r := range rows {
		fmt.Fprintf(&out, "  [%s] %-20s  %s\n", r.Status, r.Name, r.Detail)
	}

	version := d.Version
	if version == "" {
		version = "dev"
	}
	out.WriteString("\nEffective configuration\n")
	fmt.Fprintf(&out, "  jenkins-mcp version: %s\n", version)
	fmt.Fprintf(&out, "  read-only mode:      %v\n", d.ReadOnly)
	fmt.Fprintf(&out, "  cache dir:           %s\n", d.Cache.Dir)
	fmt.Fprintf(&out, "  cache max bytes:     %d\n", d.Cache.MaxBytes)
	fmt.Fprintf(&out, "  http timeout:        %s\n", d.Client.Timeout())
	return out.String()
}
