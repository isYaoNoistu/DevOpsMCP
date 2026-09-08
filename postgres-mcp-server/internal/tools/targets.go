package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"postgres-mcp-server/internal/targets"
)

type ListTargetsInput struct {
	Query string `json:"query,omitempty" jsonschema:"Optional filter against name, aliases, tags, dbname, environment, host."`
}

func (d Deps) ListTargets(_ context.Context, _ *mcp.CallToolRequest, in ListTargetsInput) (*mcp.CallToolResult, any, error) {
	all, err := d.Reg.List()
	if err != nil {
		return nil, nil, err
	}
	if d.Pool != nil {
		d.Pool.Reap(all)
	}
	hits := targets.Filter(all, in.Query)
	views := make([]map[string]any, 0, len(hits))
	for _, t := range hits {
		views = append(views, t.PublicView())
	}
	return jsonResult(map[string]any{
		"targets_file": d.Reg.Path(),
		"query":        in.Query,
		"count":        len(views),
		"targets":      views,
		"note":         "name is the stable target id for other tools. Passwords are never stored here.",
	})
}

type GetTargetInfoInput struct {
	Target string `json:"target" jsonschema:"Stable target name, alias, or unique filter (e.g. orders-prod)."`
}

func (d Deps) GetTargetInfo(_ context.Context, _ *mcp.CallToolRequest, in GetTargetInfoInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	view := t.PublicView()
	view["note"] = "Password is resolved from Windows Credential Manager (credential_ref) or pgpass.conf. It is not returned here."
	return jsonResult(view)
}
