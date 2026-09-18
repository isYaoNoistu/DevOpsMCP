package tools

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
