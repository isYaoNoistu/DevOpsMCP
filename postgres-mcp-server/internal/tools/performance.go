package tools

import (
	"context"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListSlowQueriesInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 20, cap 100."`
}

func (d Deps) ListSlowQueries(ctx context.Context, _ *mcp.CallToolRequest, in ListSlowQueriesInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 20, 100)
	modern := `
SELECT queryid::text AS queryid,
       calls,
       round(total_exec_time::numeric, 2) AS total_exec_ms,
       round(mean_exec_time::numeric, 2) AS mean_exec_ms,
       rows,
       left(query, 300) AS query
FROM pg_stat_statements
ORDER BY mean_exec_time DESC
`
	res, t, err := d.query(ctx, in.Target, modern, nil, limit)
	if err == nil {
		return jsonResult(wrapRows(t, "from pg_stat_statements; missing extension is a server config issue, not an MCP gap", res))
	}
	msg := strings.ToLower(err.Error())
	legacy := `
SELECT queryid::text AS queryid,
       calls,
       round(total_time::numeric, 2) AS total_exec_ms,
       round(mean_time::numeric, 2) AS mean_exec_ms,
       rows,
       left(query, 300) AS query
FROM pg_stat_statements
ORDER BY mean_time DESC
`
	if strings.Contains(msg, "total_exec_time") || strings.Contains(msg, "mean_exec_time") || strings.Contains(msg, "column") {
		res, t, err = d.query(ctx, in.Target, legacy, nil, limit)
		if err == nil {
			return jsonResult(wrapRows(t, "from pg_stat_statements (legacy column names)", res))
		}
		msg = strings.ToLower(err.Error())
	}
	if strings.Contains(msg, "pg_stat_statements") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "permission denied") {
		return jsonResult(map[string]any{
			"target": in.Target,
			"error":  "pg_stat_statements is not available on this database",
			"hint":   "Enable the extension and grant SELECT to mcp_ro, or use query_postgres against pg_stat_activity for currently running queries.",
			"detail": err.Error(),
		})
	}
	return nil, nil, err
}

type GetTableStatsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Schema string `json:"schema,omitempty" jsonschema:"Optional schema filter."`
	Query  string `json:"query,omitempty" jsonschema:"Optional table name substring."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 50, cap 200."`
}

func (d Deps) GetTableStats(ctx context.Context, _ *mcp.CallToolRequest, in GetTableStatsInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 50, 200)
	sql := `
SELECT schemaname, relname,
       seq_scan, seq_tup_read, idx_scan, idx_tup_fetch,
       n_tup_ins, n_tup_upd, n_tup_del, n_live_tup, n_dead_tup,
       last_vacuum, last_autovacuum, last_analyze, last_autoanalyze
FROM pg_stat_user_tables
WHERE 1=1
`
	var args []any
	n := 1
	if in.Schema != "" {
		if err := checkIdent(in.Schema); err != nil {
			return nil, nil, err
		}
		sql += " AND schemaname = $1"
		args = append(args, in.Schema)
		n++
	}
	if in.Query != "" {
		sql += " AND relname ILIKE $" + strconv.Itoa(n)
		args = append(args, "%"+in.Query+"%")
	}
	sql += " ORDER BY n_live_tup DESC NULLS LAST"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type GetIndexStatsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Schema string `json:"schema,omitempty" jsonschema:"Optional schema filter."`
	Query  string `json:"query,omitempty" jsonschema:"Optional table or index name substring."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 50, cap 200."`
}

func (d Deps) GetIndexStats(ctx context.Context, _ *mcp.CallToolRequest, in GetIndexStatsInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 50, 200)
	sql := `
SELECT schemaname, relname, indexrelname,
       idx_scan, idx_tup_read, idx_tup_fetch
FROM pg_stat_user_indexes
WHERE 1=1
`
	var args []any
	n := 1
	if in.Schema != "" {
		if err := checkIdent(in.Schema); err != nil {
			return nil, nil, err
		}
		sql += " AND schemaname = $1"
		args = append(args, in.Schema)
		n++
	}
	if in.Query != "" {
		sql += " AND (relname ILIKE $" + strconv.Itoa(n) + " OR indexrelname ILIKE $" + strconv.Itoa(n) + ")"
		args = append(args, "%"+in.Query+"%")
	}
	sql += " ORDER BY idx_scan ASC, relname"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "low idx_scan may indicate unused indexes; confirm before asking anyone to DROP", res))
}
