package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listQueueTree is the `tree` selector for /queue/api/json. Kept explicit so
// the response stays small and stable across Jenkins versions.
const listQueueTree = "items[id,task[name,url],inQueueSince,why,stuck,blocked,buildable,params]"

type apiQueueTask struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type apiQueueItem struct {
	ID           int64        `json:"id"`
	Task         apiQueueTask `json:"task"`
	InQueueSince int64        `json:"inQueueSince"`
	Why          string       `json:"why"`
	Stuck        bool         `json:"stuck"`
	Blocked      bool         `json:"blocked"`
	Buildable    bool         `json:"buildable"`
	Params       string       `json:"params"`
}

type apiQueueListing struct {
	Items []apiQueueItem `json:"items"`
}

// ListQueueInput is the schema for list_queue.
type ListQueueInput struct {
	JobPathPrefix string `json:"job_path_prefix,omitempty" jsonschema:"Optional case-sensitive substring matched against each item's task URL — useful for narrowing to one folder."`
}

// ListQueue lists pending Jenkins queue items with the block reason for each.
func (d Deps) ListQueue(ctx context.Context, _ *mcp.CallToolRequest, in ListQueueInput) (*mcp.CallToolResult, any, error) {
	body, err := d.Client.Get(ctx, "/queue/api/json", map[string]string{"tree": listQueueTree})
	if err != nil {
		return nil, nil, err
	}
	var listing apiQueueListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, nil, fmt.Errorf("parse /queue listing: %w", err)
	}

	var out strings.Builder
	matched := 0
	now := time.Now()
	for _, it := range listing.Items {
		if in.JobPathPrefix != "" && !strings.Contains(it.Task.URL, in.JobPathPrefix) {
			continue
		}
		matched++
		waited := "-"
		if it.InQueueSince > 0 {
			waited = formatDuration(now.Sub(time.UnixMilli(it.InQueueSince)))
		}
		flags := queueFlags(it)
		fmt.Fprintf(&out, "[id=%d] %s\n", it.ID, it.Task.Name)
		fmt.Fprintf(&out, "  url:     %s\n", it.Task.URL)
		fmt.Fprintf(&out, "  waited:  %s\n", waited)
		fmt.Fprintf(&out, "  state:   %s\n", flags)
		if it.Why != "" {
			fmt.Fprintf(&out, "  why:     %s\n", it.Why)
		}
		if it.Params != "" {
			fmt.Fprintf(&out, "  params:  %s\n", strings.TrimSpace(it.Params))
		}
		out.WriteString("\n")
	}

	header := fmt.Sprintf("Queue items: %d total", len(listing.Items))
	if in.JobPathPrefix != "" {
		header += fmt.Sprintf(", %d matched job_path_prefix=%q", matched, in.JobPathPrefix)
	}
	header += "\n\n"
	if matched == 0 {
		return textResult(header + "(no items)\n"), nil, nil
	}
	return textResult(header + out.String()), nil, nil
}

func queueFlags(it apiQueueItem) string {
	flags := make([]string, 0, 3)
	if it.Buildable {
		flags = append(flags, "buildable")
	}
	if it.Blocked {
		flags = append(flags, "blocked")
	}
	if it.Stuck {
		flags = append(flags, "stuck")
	}
	if len(flags) == 0 {
		return "-"
	}
	return strings.Join(flags, ",")
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
