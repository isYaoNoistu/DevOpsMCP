package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"postgres-mcp-server/internal/sqlguard"
)

type QueryPostgresInput struct {
	Target  string `json:"target" jsonschema:"Stable target name from list_targets."`
	SQL     string `json:"sql" jsonschema:"Single SELECT/WITH statement. No DML/DDL, COPY, or multiple statements."`
	MaxRows int    `json:"max_rows,omitempty" jsonschema:"Row cap, default 100, max 500."`
}

func (d Deps) QueryPostgres(ctx context.Context, _ *mcp.CallToolRequest, in QueryPostgresInput) (*mcp.CallToolResult, any, error) {
	sql, err := sqlguard.CheckReadQuery(in.SQL)
	if err != nil {
		return nil, nil, err
	}
	res, t, err := d.query(ctx, in.Target, sql, nil, in.MaxRows)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "escape-hatch read-only query; prefer a dedicated tool when one already covers this", res))
}

type ExplainQueryInput struct {
	Target  string `json:"target" jsonschema:"Stable target name from list_targets."`
	SQL     string `json:"sql" jsonschema:"Subject SELECT/WITH. Do not prefix EXPLAIN yourself."`
	Analyze bool   `json:"analyze,omitempty" jsonschema:"If true, EXPLAIN ANALYZE actually runs the SQL. Default false. Forbidden on production targets. Non-prod also needs PG_MCP_ALLOW_EXPLAIN_ANALYZE=true."`
}

func (d Deps) ExplainQuery(ctx context.Context, _ *mcp.CallToolRequest, in ExplainQueryInput) (*mcp.CallToolResult, any, error) {
	subject, err := sqlguard.CheckExplainSubject(in.SQL)
	if err != nil {
		return nil, nil, err
	}
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	analyze := in.Analyze
	if analyze {
		if t.IsProduction() {
			return nil, nil, fmt.Errorf("EXPLAIN ANALYZE is forbidden on production targets")
		}
		if !d.Pool.Config().AllowAnalyze {
			return nil, nil, fmt.Errorf("EXPLAIN ANALYZE is disabled; set PG_MCP_ALLOW_EXPLAIN_ANALYZE=true only for non-prod")
		}
	}
	opts := "FORMAT JSON"
	if analyze {
		opts = "ANALYZE, FORMAT JSON"
	}
	sql := "EXPLAIN (" + opts + ") " + subject
	res, err := d.Pool.Query(ctx, t, sql, nil, 20)
	if err != nil {
		return nil, nil, err
	}
	note := "EXPLAIN without ANALYZE does not execute the statement"
	if analyze {
		note = "EXPLAIN ANALYZE executed the statement on a non-prod target"
	}
	return jsonResult(wrapRows(t, note, res))
}
