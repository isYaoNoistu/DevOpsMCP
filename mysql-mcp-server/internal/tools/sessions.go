package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListSessionsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	State  string `json:"state,omitempty" jsonschema:"Optional filter on PROCESSLIST_COMMAND or PROCESSLIST_STATE, e.g. Query, Sleep, Lock wait."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 50, cap 200."`
}

func sessionsSQL() string {
	return `
SELECT t.PROCESSLIST_ID AS id,
       t.PROCESSLIST_USER AS user_name,
       t.PROCESSLIST_HOST AS host,
       t.PROCESSLIST_DB AS db_name,
       t.PROCESSLIST_COMMAND AS command,
       t.PROCESSLIST_TIME AS time_sec,
       t.PROCESSLIST_STATE AS state,
       LEFT(t.PROCESSLIST_INFO, 240) AS info,
       t.THREAD_ID AS thread_id,
       trx.trx_id,
       trx.trx_state,
       trx.trx_started,
       trx.trx_rows_locked,
       trx.trx_rows_modified,
       trx.trx_isolation_level,
       LEFT(esc.SQL_TEXT, 240) AS current_sql
FROM performance_schema.threads t
LEFT JOIN information_schema.innodb_trx trx
  ON trx.trx_mysql_thread_id = t.PROCESSLIST_ID
LEFT JOIN performance_schema.events_statements_current esc
  ON esc.THREAD_ID = t.THREAD_ID
WHERE t.TYPE = 'FOREGROUND'
  AND t.PROCESSLIST_ID IS NOT NULL
  AND t.PROCESSLIST_ID <> CONNECTION_ID()
`
}

func (d Deps) ListSessions(ctx context.Context, _ *mcp.CallToolRequest, in ListSessionsInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 50, 200)
	sql := sessionsSQL()
	var args []any
	if strings.TrimSpace(in.State) != "" {
		sql += " AND (t.PROCESSLIST_COMMAND = ? OR t.PROCESSLIST_STATE = ?)"
		args = append(args, in.State, in.State)
	}
	sql += " ORDER BY t.PROCESSLIST_TIME DESC"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "from performance_schema.threads + innodb_trx (official MySQL 8 processlist)", res))
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
SELECT trx.trx_id,
       trx.trx_mysql_thread_id AS id,
       trx.trx_state,
       trx.trx_started,
       TIMESTAMPDIFF(SECOND, trx.trx_started, NOW()) AS xact_age_sec,
       trx.trx_wait_started,
       trx.trx_rows_locked,
       trx.trx_rows_modified,
       trx.trx_tables_in_use,
       trx.trx_isolation_level,
       t.PROCESSLIST_USER AS user_name,
       t.PROCESSLIST_HOST AS host,
       t.PROCESSLIST_DB AS db_name,
       t.PROCESSLIST_COMMAND AS command,
       t.PROCESSLIST_TIME AS time_sec,
       LEFT(trx.trx_query, 240) AS trx_query,
       LEFT(t.PROCESSLIST_INFO, 240) AS info
FROM information_schema.innodb_trx trx
LEFT JOIN performance_schema.threads t
  ON t.PROCESSLIST_ID = trx.trx_mysql_thread_id
WHERE TIMESTAMPDIFF(SECOND, trx.trx_started, NOW()) >= ?
ORDER BY trx.trx_started
`
	res, t, err := d.query(ctx, in.Target, sql, []any{minSec}, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "from information_schema.innodb_trx; empty means no matching open InnoDB transactions", res))
}

type ListIdleTransactionsInput struct {
	Target     string `json:"target" jsonschema:"Stable target name from list_targets."`
	MinSeconds int    `json:"min_seconds,omitempty" jsonschema:"Minimum Sleep-with-open-trx age in seconds. Default 5."`
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
SELECT trx.trx_id,
       trx.trx_mysql_thread_id AS id,
       trx.trx_state,
       trx.trx_started,
       TIMESTAMPDIFF(SECOND, trx.trx_started, NOW()) AS xact_age_sec,
       t.PROCESSLIST_COMMAND AS command,
       t.PROCESSLIST_TIME AS idle_sec,
       t.PROCESSLIST_USER AS user_name,
       t.PROCESSLIST_HOST AS host,
       t.PROCESSLIST_DB AS db_name,
       LEFT(trx.trx_query, 240) AS last_trx_query,
       LEFT(t.PROCESSLIST_INFO, 240) AS info
FROM information_schema.innodb_trx trx
JOIN performance_schema.threads t
  ON t.PROCESSLIST_ID = trx.trx_mysql_thread_id
WHERE t.PROCESSLIST_COMMAND = 'Sleep'
  AND t.PROCESSLIST_TIME >= ?
ORDER BY t.PROCESSLIST_TIME DESC
`
	res, t, err := d.query(ctx, in.Target, sql, []any{minSec}, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "MySQL equivalent of idle-in-transaction: COMMAND=Sleep with an open innodb_trx", res))
}

type GetBlockingTreeInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func innodbLockWaitSQL() string {
	return `
SELECT
  r.trx_id AS waiting_trx_id,
  r.trx_mysql_thread_id AS waiting_id,
  LEFT(r.trx_query, 240) AS waiting_query,
  TIMESTAMPDIFF(SECOND, r.trx_wait_started, NOW()) AS wait_sec,
  b.trx_id AS blocking_trx_id,
  b.trx_mysql_thread_id AS blocking_id,
  LEFT(b.trx_query, 240) AS blocking_query,
  b.trx_started AS blocking_trx_started,
  dl.OBJECT_SCHEMA AS object_schema,
  dl.OBJECT_NAME AS object_name,
  dl.INDEX_NAME AS index_name,
  dl.LOCK_TYPE AS lock_type,
  dl.LOCK_MODE AS lock_mode,
  dl.LOCK_STATUS AS lock_status
FROM performance_schema.data_lock_waits w
JOIN performance_schema.data_locks dl
  ON dl.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
JOIN information_schema.innodb_trx b
  ON b.trx_id = w.BLOCKING_ENGINE_TRANSACTION_ID
JOIN information_schema.innodb_trx r
  ON r.trx_id = w.REQUESTING_ENGINE_TRANSACTION_ID
ORDER BY r.trx_wait_started
`
}

func metadataLockWaitSQL() string {
	return `
SELECT
  waiting.OBJECT_SCHEMA AS object_schema,
  waiting.OBJECT_NAME AS object_name,
  waiting.LOCK_TYPE AS lock_type,
  waiting.LOCK_DURATION AS lock_duration,
  waiting.OWNER_THREAD_ID AS waiting_thread_id,
  wt.PROCESSLIST_ID AS waiting_id,
  LEFT(wt.PROCESSLIST_INFO, 240) AS waiting_query,
  blocking.OWNER_THREAD_ID AS blocking_thread_id,
  bt.PROCESSLIST_ID AS blocking_id,
  LEFT(bt.PROCESSLIST_INFO, 240) AS blocking_query,
  blocking.LOCK_STATUS AS blocking_lock_status
FROM performance_schema.metadata_locks waiting
JOIN performance_schema.metadata_locks blocking
  ON waiting.OBJECT_SCHEMA <=> blocking.OBJECT_SCHEMA
 AND waiting.OBJECT_NAME <=> blocking.OBJECT_NAME
 AND waiting.OWNER_THREAD_ID <> blocking.OWNER_THREAD_ID
 AND waiting.LOCK_STATUS = 'PENDING'
 AND blocking.LOCK_STATUS = 'GRANTED'
JOIN performance_schema.threads wt ON wt.THREAD_ID = waiting.OWNER_THREAD_ID
JOIN performance_schema.threads bt ON bt.THREAD_ID = blocking.OWNER_THREAD_ID
ORDER BY waiting.OBJECT_SCHEMA, waiting.OBJECT_NAME
`
}

func (d Deps) GetBlockingTree(ctx context.Context, _ *mcp.CallToolRequest, in GetBlockingTreeInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{"target": t.Name}
	innodb, ierr := optionalSection(ctx, d, t, innodbLockWaitSQL(), nil, 200)
	if ierr != "" {
		out["innodb_lock_error"] = ierr
		out["innodb_locks"] = []any{}
	} else {
		putSection(out, "innodb_locks", innodb)
	}
	meta, merr := optionalSection(ctx, d, t, metadataLockWaitSQL(), nil, 200)
	if merr != "" {
		out["metadata_lock_error"] = merr
		out["metadata_locks"] = []any{}
	} else {
		putSection(out, "metadata_locks", meta)
	}
	note := "InnoDB row/table locks from performance_schema.data_lock_waits; metadata locks from performance_schema.metadata_locks. Errors or truncation mean the affected section is incomplete."
	if len(innodb.Rows) == 0 && len(meta.Rows) == 0 && !innodb.Truncated && !meta.Truncated && ierr == "" && merr == "" {
		note = "no blocking sessions right now"
	}
	out["note"] = note
	return jsonResult(out)
}
