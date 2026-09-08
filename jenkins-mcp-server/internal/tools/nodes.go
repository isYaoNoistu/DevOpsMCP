package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listNodesTree keeps the listing response compact and stable across Jenkins
// versions. monitorData is fetched whole because its keys vary by plugin set.
const listNodesTree = "computer[displayName,offline,temporarilyOffline,offlineCauseReason," +
	"numExecutors,idle,assignedLabels[name],monitorData]"

// getNodeTree adds per-executor idle state on top of the list selector so a
// caller can see exactly which slots are free.
const getNodeTree = "displayName,offline,temporarilyOffline,offlineCauseReason," +
	"numExecutors,idle,assignedLabels[name],monitorData,executors[idle,likelyStuck]"

type apiLabel struct {
	Name string `json:"name"`
}

type apiExecutor struct {
	Idle        bool `json:"idle"`
	LikelyStuck bool `json:"likelyStuck"`
}

type apiComputer struct {
	DisplayName        string         `json:"displayName"`
	Offline            bool           `json:"offline"`
	TemporarilyOffline bool           `json:"temporarilyOffline"`
	OfflineCauseReason string         `json:"offlineCauseReason"`
	NumExecutors       int            `json:"numExecutors"`
	Idle               bool           `json:"idle"`
	AssignedLabels     []apiLabel     `json:"assignedLabels"`
	MonitorData        map[string]any `json:"monitorData"`
	Executors          []apiExecutor  `json:"executors"`
}

type apiComputerListing struct {
	Computer []apiComputer `json:"computer"`
}

// ListNodes lists all Jenkins agents/nodes with status, executor counts, and
// monitor summaries — useful for diagnosing "why is this build still queued"
// when no executors match the requested label.
func (d Deps) ListNodes(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	body, err := d.Client.Get(ctx, "/computer/api/json", map[string]string{"tree": listNodesTree})
	if err != nil {
		return nil, nil, err
	}
	var listing apiComputerListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, nil, fmt.Errorf("parse /computer listing: %w", err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Nodes: %d total\n\n", len(listing.Computer))
	fmt.Fprintf(&out, "%-3s  %-30s  %-9s  %-3s  %-6s  %s\n",
		"#", "name", "status", "exe", "idle", "labels")
	fmt.Fprintf(&out, "%-3s  %-30s  %-9s  %-3s  %-6s  %s\n",
		"---", "------------------------------", "---------", "---", "------", "------")
	for i, c := range listing.Computer {
		fmt.Fprintf(&out, "%-3d  %s  %-9s  %-3d  %-6s  %s\n",
			i+1, padRight(truncate(c.DisplayName, 30), 30), nodeStatus(c), c.NumExecutors,
			fmt.Sprintf("%v", c.Idle), labelNames(c.AssignedLabels))
	}

	offlineWithReason := 0
	for _, c := range listing.Computer {
		if c.Offline && c.OfflineCauseReason != "" {
			offlineWithReason++
		}
	}
	if offlineWithReason > 0 {
		out.WriteString("\nOffline reasons:\n")
		for _, c := range listing.Computer {
			if c.Offline && c.OfflineCauseReason != "" {
				fmt.Fprintf(&out, "  %s: %s\n", c.DisplayName, c.OfflineCauseReason)
			}
		}
	}
	out.WriteString("\nUse get_node with a node name (e.g. \"(built-in)\" or \"(master)\" for the controller) " +
		"for per-executor detail and monitor data.\n")
	return textResult(out.String()), nil, nil
}

// GetNodeInput is the schema for get_node.
type GetNodeInput struct {
	Name string `json:"name" jsonschema:"Node name. Use \"(built-in)\" or \"(master)\" for the controller depending on Jenkins version."`
}

// GetNode returns a single node's status, executors, labels, and monitor data.
func (d Deps) GetNode(ctx context.Context, _ *mcp.CallToolRequest, in GetNodeInput) (*mcp.CallToolResult, any, error) {
	if in.Name == "" {
		return nil, nil, fmt.Errorf("name is required")
	}
	path := "/computer/" + url.PathEscape(in.Name) + "/api/json"
	body, err := d.Client.Get(ctx, path, map[string]string{"tree": getNodeTree})
	if err != nil {
		return nil, nil, err
	}
	var c apiComputer
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, nil, fmt.Errorf("parse node JSON: %w", err)
	}

	idleExecutors := 0
	for _, e := range c.Executors {
		if e.Idle {
			idleExecutors++
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Node: %s\n", c.DisplayName)
	fmt.Fprintf(&out, "Status:    %s\n", nodeStatus(c))
	fmt.Fprintf(&out, "Executors: %d total, %d idle\n", c.NumExecutors, idleExecutors)
	fmt.Fprintf(&out, "Labels:    %s\n", labelNames(c.AssignedLabels))
	if c.Offline && c.OfflineCauseReason != "" {
		fmt.Fprintf(&out, "Offline cause: %s\n", c.OfflineCauseReason)
	}
	if len(c.MonitorData) > 0 {
		out.WriteString("\nMonitor data:\n")
		for _, line := range formatMonitorData(c.MonitorData) {
			fmt.Fprintf(&out, "  %s\n", line)
		}
	}
	return textResult(out.String()), nil, nil
}

// nodeStatus collapses the offline/temporarilyOffline pair into a single label.
func nodeStatus(c apiComputer) string {
	switch {
	case c.TemporarilyOffline:
		return "temp-off"
	case c.Offline:
		return "offline"
	default:
		return "online"
	}
}

// labelNames returns a comma-joined sorted list, "-" when none.
func labelNames(labels []apiLabel) string {
	if len(labels) == 0 {
		return "-"
	}
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		if l.Name != "" {
			names = append(names, l.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// formatMonitorData renders a stable, single-line summary per monitor key.
// Keys are Jenkins-internal class names (e.g. "hudson.node_monitors.DiskSpaceMonitor")
// — we strip the package prefix and emit the value as compact JSON to stay
// version-agnostic across plugin sets.
func formatMonitorData(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		short := k
		if dot := strings.LastIndex(k, "."); dot >= 0 {
			short = k[dot+1:]
		}
		v := m[k]
		if v == nil {
			out = append(out, short+": null")
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out = append(out, short+": "+string(b))
	}
	return out
}
