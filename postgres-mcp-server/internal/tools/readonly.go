package tools

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReadOnlyTool is the shared registration for every postgres MCP tool.
func ReadOnlyTool(name, title, desc string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{
			Title:        title,
			ReadOnlyHint: true,
		},
		OutputSchema: &jsonschema.Schema{Type: "object"},
	}
}
