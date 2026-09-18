package api

import (
	"context"
	"fmt"

	"github.com/n9e/n9e-mcp-server/pkg/client"
	"github.com/n9e/n9e-mcp-server/pkg/toolset"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultLogLimit caps the number of log lines returned per query.
const defaultLogLimit = 200
const maxLogLimit = 500

// maxLogRangeSeconds rejects log queries spanning more than 7 days to prevent runaway costs.
const maxLogRangeSeconds = 7 * 24 * 60 * 60

// RegisterLogsToolset registers the logs-query toolset.
// Backed by n9e's plugin-dispatched /api/n9e/logs-query (Loki/ES/OS).
func RegisterLogsToolset(group *toolset.ToolsetGroup, getClient client.GetClientFunc) {
	ts := toolset.NewToolset("logs", "Logs query tools (Loki/Elasticsearch/OpenSearch) via n9e logs-query")

	ts.AddReadTools(
		queryLogsTool(getClient),
		listLogIndicesTool(getClient),
		listLogFieldsTool(getClient),
	)

	group.AddToolset(ts)
}

type queryLogsInput struct {
	Body  map[string]any `json:"body"`
	Limit int            `json:"limit,omitempty"`
	Start int64          `json:"start,omitempty"`
	End   int64          `json:"end,omitempty"`
}

type logQueryMeta struct {
	Limit int   `json:"limit"`
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

func prepareLogQuery(body map[string]any, requestedLimit int) (map[string]any, logQueryMeta, error) {
	limit := requestedLimit
	if limit <= 0 {
		limit = defaultLogLimit
	}
	if limit > maxLogLimit {
		limit = maxLogLimit
	}
	if bodyLimit, ok := positiveInt(body["limit"]); ok && bodyLimit < limit {
		limit = bodyLimit
	}
	rawQueries, ok := body["query"].([]any)
	if !ok || len(rawQueries) == 0 {
		return nil, logQueryMeta{}, fmt.Errorf("body.query must be a non-empty array")
	}
	for i, raw := range rawQueries {
		query, ok := raw.(map[string]any)
		if !ok {
			return nil, logQueryMeta{}, fmt.Errorf("body.query[%d] must be an object", i)
		}
		if queryLimit, ok := positiveInt(query["limit"]); ok && queryLimit < limit {
			limit = queryLimit
		}
	}
	copyBody := make(map[string]any, len(body))
	for k, v := range body {
		copyBody[k] = v
	}
	queries := make([]any, len(rawQueries))
	meta := logQueryMeta{Limit: limit}
	for i, raw := range rawQueries {
		query, ok := raw.(map[string]any)
		if !ok {
			return nil, logQueryMeta{}, fmt.Errorf("body.query[%d] must be an object", i)
		}
		start, startOK := integerField(query["start"])
		end, endOK := integerField(query["end"])
		if !startOK || !endOK {
			return nil, logQueryMeta{}, fmt.Errorf("body.query[%d] must use supported Unix-second start/end fields", i)
		}
		if start <= 0 || end <= start {
			return nil, logQueryMeta{}, fmt.Errorf("body.query[%d] has invalid start/end", i)
		}
		if end-start > maxLogRangeSeconds {
			return nil, logQueryMeta{}, fmt.Errorf("time range exceeds max of %d seconds (~7 days)", maxLogRangeSeconds)
		}
		if i == 0 || start < meta.Start {
			meta.Start = start
		}
		if end > meta.End {
			meta.End = end
		}
		qcopy := make(map[string]any, len(query)+1)
		for k, v := range query {
			qcopy[k] = v
		}
		qcopy["limit"] = limit
		queries[i] = qcopy
	}
	if meta.End-meta.Start > maxLogRangeSeconds {
		return nil, logQueryMeta{}, fmt.Errorf("combined time range exceeds max of %d seconds (~7 days)", maxLogRangeSeconds)
	}
	copyBody["query"] = queries
	copyBody["limit"] = limit
	return copyBody, meta, nil
}

func positiveInt(value any) (int, bool) {
	v, ok := integerField(value)
	if !ok || v <= 0 || v > int64(^uint(0)>>1) {
		return 0, false
	}
	return int(v), true
}

func integerField(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
	}
	return 0, false
}

func queryLogsTool(getClient client.GetClientFunc) toolset.ServerTool {
	return toolset.NewServerTool(
		mcp.Tool{
			Name:        "query_logs",
			Description: "Query logs via n9e's plugin-dispatched /logs-query endpoint when the native body.query items use Unix-second start/end fields. Unrecognized time layouts are rejected explicitly. Each query is capped at 500 lines and time range > 7 days is rejected.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Query Logs",
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type:     "object",
				Required: []string{"body"},
				Properties: map[string]*jsonschema.Schema{
					"body":  {Type: "object", Description: "Log query body (matches n9e's /logs-query payload)"},
					"limit": {Type: "integer", Description: "Cap applied to every body.query item (default 200, max 500)"},
					"start": {Type: "integer", Description: "Optional Unix-second start; if provided, must match the actual body query window"},
					"end":   {Type: "integer", Description: "Optional Unix-second end; if provided, must match the actual body query window"},
				},
			},
		},
		toolset.MakeToolHandler(func(ctx context.Context, req *mcp.CallToolRequest, input queryLogsInput) (*mcp.CallToolResult, error) {
			if len(input.Body) == 0 {
				return toolset.NewToolResultError("body is required"), nil
			}
			body, meta, err := prepareLogQuery(input.Body, input.Limit)
			if err != nil {
				return toolset.NewToolResultError(err.Error()), nil
			}
			if (input.Start > 0 && input.Start != meta.Start) || (input.End > 0 && input.End != meta.End) {
				return toolset.NewToolResultError("outer start/end must match the actual body query window"), nil
			}

			c := getClient(ctx)
			if c == nil {
				return toolset.NewToolResultError("failed to get n9e client from context"), nil
			}
			result, err := client.DoPost[any](c, ctx, "/api/n9e/logs-query", body)
			if err != nil {
				return toolset.NewToolResultError(err.Error()), nil
			}
			return toolset.MarshalResult(map[string]any{
				"limit": meta.Limit,
				"start": meta.Start,
				"end":   meta.End,
				"data":  result,
			}), nil
		}),
	)
}

type logIndicesInput struct {
	Body   map[string]any `json:"body"`
	Engine string         `json:"engine,omitempty"` // "es" (default) or "os" (OpenSearch)
}

func listLogIndicesTool(getClient client.GetClientFunc) toolset.ServerTool {
	return toolset.NewServerTool(
		mcp.Tool{
			Name:        "list_log_indices",
			Description: "List indices for an Elasticsearch (default) or OpenSearch datasource. Body must include datasource_id.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Log Indices",
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type:     "object",
				Required: []string{"body"},
				Properties: map[string]*jsonschema.Schema{
					"body":   {Type: "object", Description: "Body (must include datasource_id)"},
					"engine": {Type: "string", Description: "'es' (default) or 'os' for OpenSearch"},
				},
			},
		},
		toolset.MakeToolHandler(func(ctx context.Context, req *mcp.CallToolRequest, input logIndicesInput) (*mcp.CallToolResult, error) {
			if len(input.Body) == 0 {
				return toolset.NewToolResultError("body is required"), nil
			}
			c := getClient(ctx)
			if c == nil {
				return toolset.NewToolResultError("failed to get n9e client from context"), nil
			}
			path := "/api/n9e/indices"
			if input.Engine == "os" {
				path = "/api/n9e/os-indices"
			}
			result, err := client.DoPost[any](c, ctx, path, input.Body)
			if err != nil {
				return toolset.NewToolResultError(err.Error()), nil
			}
			return toolset.MarshalResult(result), nil
		}),
	)
}

func listLogFieldsTool(getClient client.GetClientFunc) toolset.ServerTool {
	return toolset.NewServerTool(
		mcp.Tool{
			Name:        "list_log_fields",
			Description: "List fields for an Elasticsearch (default) or OpenSearch index. Body must include datasource_id and the index name.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Log Fields",
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type:     "object",
				Required: []string{"body"},
				Properties: map[string]*jsonschema.Schema{
					"body":   {Type: "object"},
					"engine": {Type: "string", Description: "'es' (default) or 'os' for OpenSearch"},
				},
			},
		},
		toolset.MakeToolHandler(func(ctx context.Context, req *mcp.CallToolRequest, input logIndicesInput) (*mcp.CallToolResult, error) {
			if len(input.Body) == 0 {
				return toolset.NewToolResultError("body is required"), nil
			}
			c := getClient(ctx)
			if c == nil {
				return toolset.NewToolResultError("failed to get n9e client from context"), nil
			}
			path := "/api/n9e/fields"
			if input.Engine == "os" {
				path = "/api/n9e/os-fields"
			}
			result, err := client.DoPost[any](c, ctx, path, input.Body)
			if err != nil {
				return toolset.NewToolResultError(err.Error()), nil
			}
			return toolset.MarshalResult(result), nil
		}),
	)
}
