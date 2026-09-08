package api

import (
	"context"

	"github.com/n9e/n9e-mcp-server/pkg/client"
	"github.com/n9e/n9e-mcp-server/pkg/toolset"
	"github.com/n9e/n9e-mcp-server/pkg/types"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListDatasourcesInput represents datasources list query parameters
type ListDatasourcesInput struct {
	Limit int `json:"limit,omitempty"`
	Page  int `json:"p,omitempty"`
}

// RegisterDatasourceToolset registers datasource read tools.
func RegisterDatasourceToolset(group *toolset.ToolsetGroup, getClient client.GetClientFunc) {
	ts := toolset.NewToolset("datasource", "Datasource lookup (Prometheus/VictoriaMetrics/Loki/ES/...)")

	ts.AddReadTools(
		listDatasourcesTool(getClient),
		getDatasourceTool(getClient),
		listDatasourcePluginsTool(getClient),
	)

	group.AddToolset(ts)
}

func listDatasourcesTool(getClient client.GetClientFunc) toolset.ServerTool {
	return toolset.NewServerTool(
		mcp.Tool{
			Name:        "list_datasources",
			Description: "List all available datasources (sanitized brief view — no auth secrets)",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Datasources",
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"limit": {Type: "integer", Description: "Page size (default 20)"},
					"p":     {Type: "integer", Description: "Page number (starts from 1)"},
				},
			},
		},
		toolset.MakeToolHandler(func(ctx context.Context, req *mcp.CallToolRequest, input ListDatasourcesInput) (*mcp.CallToolResult, error) {
			c := getClient(ctx)
			if c == nil {
				return toolset.NewToolResultError("failed to get n9e client from context"), nil
			}

			result, err := client.DoGet[[]types.Datasource](c, ctx, "/api/n9e/datasource/brief", nil)
			if err != nil {
				return toolset.NewToolResultError(err.Error()), nil
			}

			items, total := toolset.SlicePage(result, input.Page, input.Limit)
			return toolset.MarshalResult(types.PageResp[types.Datasource]{List: items, Total: total}), nil
		}),
	)
}

type getDatasourceInput struct {
	Id int64 `json:"id"`
}

func getDatasourceTool(getClient client.GetClientFunc) toolset.ServerTool {
	return toolset.NewServerTool(
		mcp.Tool{
			Name:        "get_datasource",
			Description: "Get one datasource by ID (plugin type, URL). Do not echo auth secrets from the response.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Datasource",
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type:     "object",
				Required: []string{"id"},
				Properties: map[string]*jsonschema.Schema{
					"id": {Type: "integer", Description: "Datasource ID"},
				},
			},
		},
		toolset.MakeToolHandler(func(ctx context.Context, req *mcp.CallToolRequest, input getDatasourceInput) (*mcp.CallToolResult, error) {
			if input.Id <= 0 {
				return toolset.NewToolResultError("id is required and must be positive"), nil
			}
			c := getClient(ctx)
			if c == nil {
				return toolset.NewToolResultError("failed to get n9e client from context"), nil
			}
			result, err := client.DoPost[any](c, ctx, "/api/n9e/datasource/desc", map[string]any{"id": input.Id})
			if err != nil {
				return toolset.NewToolResultError(err.Error()), nil
			}
			return toolset.MarshalResult(result), nil
		}),
	)
}

func listDatasourcePluginsTool(getClient client.GetClientFunc) toolset.ServerTool {
	return toolset.NewServerTool(
		mcp.Tool{
			Name:        "list_datasource_plugins",
			Description: "List supported datasource plugin types (Prometheus, Loki, ES, Tencent CLS, ...).",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Datasource Plugins",
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{Type: "object"},
		},
		toolset.MakeToolHandler(func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, error) {
			c := getClient(ctx)
			if c == nil {
				return toolset.NewToolResultError("failed to get n9e client from context"), nil
			}
			result, err := client.DoPost[any](c, ctx, "/api/n9e/datasource/plugin/list", map[string]any{})
			if err != nil {
				return toolset.NewToolResultError(err.Error()), nil
			}
			return toolset.MarshalResult(result), nil
		}),
	)
}
