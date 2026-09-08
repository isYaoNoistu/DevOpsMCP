package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListSessionsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	State  string `json:"state,omitempty" jsonschema:"Optional exact state filter, e.g. active, idle, idle in transaction."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 50, cap 200."`
}

func (d Deps) ListSessions(ctx context.Context, _ *mcp.CallToolRequest, in ListSessionsInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 50, 200)
	sql := `
SELECT pid, usename, application_name, client_addr::text AS client_addr,
       datname, state, wait_event_type, wait_event,
       xact_start, query_start, state_change,
       left(query, 240) AS query
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
`
	var args []any
	if in.State != "" {
		sql += " AND state = $1"
		args = append(args, in.State)
	}
	sql += " ORDER BY query_start NULLS LAST"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type ListLongTransactionsInput struct {
	Target     string `json:"target" jsonschema:"Stable target name from list_targets."`
	MinSeconds int    `json:"min_seconds,omitempty" jsonschema:"Minimum open transaction age in seconds. Default 30."`
	Limit      int    `json:"limit,omitempty" jsonschema:"Max rows, default 50, cap 200."`
}

func (d Deps) ListLongTransactions(ctx context.Context, _ *mcp.CallToolRequest, in ListLongTransactionsInput) (*mcp.CallToolResult, any, error) {
	minSec := in.MinSeconds
	if minSec <= 0 {
		minSec = 30
	}
	if minSec > 86400 {
		minSec = 86400
	}
	limit := clampLimit(in.Limit, 50, 200)
	sql := `
SELECT pid, usename, application_name, client_addr::text AS client_addr,
       datname, state, wait_event_type, wait_event,
       xact_start, now() - xact_start AS xact_age,
       query_start, left(query, 240) AS query
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND xact_start IS NOT NULL
  AND now() - xact_start > make_interval(secs => $1)
ORDER BY xact_start
`
	res, t, err := d.query(ctx, in.Target, sql, []any{minSec}, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type ListIdleTransactionsInput struct {
	Target     string `json:"target" jsonschema:"Stable target name from list_targets."`
	MinSeconds int    `json:"min_seconds,omitempty" jsonschema:"Minimum idle-in-transaction age in seconds. Default 5."`
	Limit      int    `json:"limit,omitempty" jsonschema:"Max rows, default 50, cap 200."`
}

func (d Deps) ListIdleTransactions(ctx context.Context, _ *mcp.CallToolRequest, in ListIdleTransactionsInput) (*mcp.CallToolResult, any, error) {
	minSec := in.MinSeconds
	if minSec <= 0 {
		minSec = 5
	}
	if minSec > 86400 {
		minSec = 86400
	}
	limit := clampLimit(in.Limit, 50, 200)
	sql := `
SELECT pid, usename, application_name, client_addr::text AS client_addr,
       datname, state, xact_start, now() - xact_start AS xact_age,
       state_change, now() - state_change AS idle_age,
       left(query, 240) AS last_query
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND state = 'idle in transaction'
  AND now() - state_change > make_interval(secs => $1)
ORDER BY state_change
`
	res, t, err := d.query(ctx, in.Target, sql, []any{minSec}, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type GetBlockingTreeInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func (d Deps) GetBlockingTree(ctx context.Context, _ *mcp.CallToolRequest, in GetBlockingTreeInput) (*mcp.CallToolResult, any, error) {
	const sql = `
SELECT blocked.pid AS blocked_pid,
       blocked.usename AS blocked_user,
       blocked.application_name AS blocked_app,
       blocked.state AS blocked_state,
       now() - blocked.xact_start AS blocked_xact_age,
       left(blocked.query, 240) AS blocked_query,
       blocking.pid AS blocking_pid,
       blocking.usename AS blocking_user,
       blocking.application_name AS blocking_app,
       blocking.state AS blocking_state,
       now() - blocking.xact_start AS blocking_xact_age,
       left(blocking.query, 240) AS blocking_query
FROM pg_stat_activity blocked
JOIN pg_stat_activity blocking ON blocking.pid = ANY (pg_blocking_pids(blocked.pid))
WHERE blocked.pid <> pg_backend_pid()
ORDER BY blocked.pid
`
	res, t, err := d.query(ctx, in.Target, sql, nil, 200)
	if err != nil {
		return nil, nil, err
	}
	note := ""
	if res.RowCount == 0 {
		note = "no blocking sessions right now"
	}
	return jsonResult(wrapRows(t, note, res))
}
