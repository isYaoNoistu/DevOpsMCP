package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetServerOverviewInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func overviewSQL() string {
	return `
SELECT
  @@version AS version,
  @@version_comment AS version_comment,
  @@hostname AS hostname,
  @@port AS port,
  @@datadir AS datadir,
  @@basedir AS basedir,
  @@read_only AS read_only,
  @@super_read_only AS super_read_only,
  @@offline_mode AS offline_mode,
  @@server_uuid AS server_uuid,
  @@server_id AS server_id,
  @@gtid_mode AS gtid_mode,
  @@enforce_gtid_consistency AS enforce_gtid_consistency,
  @@log_bin AS log_bin,
  @@binlog_format AS binlog_format,
  @@performance_schema AS performance_schema,
  @@innodb_buffer_pool_size AS innodb_buffer_pool_size,
  @@max_connections AS max_connections,
  DATABASE() AS current_database,
  CURRENT_USER() AS current_user,
  CONNECTION_ID() AS connection_id
`
}

func overviewStatusSQL() string {
	return `
SELECT VARIABLE_NAME AS name, VARIABLE_VALUE AS value
FROM performance_schema.global_status
WHERE VARIABLE_NAME IN (
  'Uptime','Uptime_since_flush_status',
  'Threads_connected','Threads_running','Threads_created','Max_used_connections',
  'Questions','Queries','Slow_queries','Aborted_clients','Aborted_connects',
  'Connections','Connection_errors_max_connections',
  'Innodb_buffer_pool_read_requests','Innodb_buffer_pool_reads',
  'Innodb_row_lock_waits','Innodb_deadlocks','Innodb_history_list_length'
)
ORDER BY VARIABLE_NAME
`
}

func (d Deps) GetServerOverview(ctx context.Context, _ *mcp.CallToolRequest, in GetServerOverviewInput) (*mcp.CallToolResult, any, error) {
	res, t, err := d.query(ctx, in.Target, overviewSQL(), nil, 1)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{
		"target": t.Name,
		"dbname": t.DBName,
	}
	if len(res.Rows) > 0 {
		out["overview"] = res.Rows[0]
	}
	status, serr := optionalSection(ctx, d, t, overviewStatusSQL(), nil, 50)
	if serr != "" {
		out["status_error"] = serr
		out["status"] = []any{}
	} else {
		putSection(out, "status", status)
	}
	trx, terr := optionalSection(ctx, d, t, `
SELECT COUNT(*) AS trx_count,
       SUM(CASE WHEN trx_state = 'LOCK WAIT' THEN 1 ELSE 0 END) AS lock_wait_count
FROM information_schema.innodb_trx
`, nil, 1)
	if terr != "" {
		out["trx_error"] = terr
	} else {
		putSection(out, "innodb_trx", trx)
	}
	replica, rerr := optionalSection(ctx, d, t, `
SELECT CHANNEL_NAME, SERVICE_STATE, LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE
FROM performance_schema.replication_connection_status
`, nil, 20)
	if rerr != "" {
		out["replication_error"] = rerr
	} else {
		putSection(out, "replication_channels", replica)
	}
	errors, eerr := optionalSection(ctx, d, t, errorLogSQL(""), nil, 10)
	if eerr != "" {
		out["error_log_error"] = eerr
	} else {
		putSection(out, "recent_errors", errors)
	}
	out["note"] = "search any variable with get_settings; any status counter with get_status; recent errors also in get_error_log"
	return jsonResult(out)
}

type GetDatabaseStatsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func (d Deps) GetDatabaseStats(ctx context.Context, _ *mcp.CallToolRequest, in GetDatabaseStatsInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	const sql = `
SELECT TABLE_SCHEMA AS schema_name,
       COUNT(*) AS table_count,
       SUM(TABLE_ROWS) AS table_rows,
       SUM(DATA_LENGTH) AS data_bytes,
       SUM(INDEX_LENGTH) AS index_bytes,
       SUM(DATA_LENGTH + INDEX_LENGTH) AS total_bytes,
       SUM(DATA_FREE) AS data_free_bytes
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')
GROUP BY TABLE_SCHEMA
ORDER BY total_bytes DESC
`
	schemas, err := d.Pool.Query(ctx, t, sql, nil, 200)
	if err != nil {
		return nil, nil, err
	}
	cur, _ := d.Pool.Query(ctx, t, `
SELECT DATABASE() AS current_database,
       COUNT(*) AS table_count,
       SUM(TABLE_ROWS) AS table_rows,
       SUM(DATA_LENGTH + INDEX_LENGTH) AS total_bytes
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = DATABASE()
`, nil, 1)
	out := map[string]any{
		"target": t.Name,
		"note":   "sizes come from information_schema.TABLES (approximate for InnoDB)",
	}
	putSection(out, "schemas", schemas)
	if cur != nil {
		putSection(out, "current_database", cur)
	}
	return jsonResult(out)
}

type GetSettingsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Query  string `json:"query,omitempty" jsonschema:"Optional substring filter on VARIABLE_NAME. Empty = curated ops variables."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 80, cap 200."`
}

var curatedSettings = []string{
	"max_connections", "wait_timeout", "interactive_timeout", "max_allowed_packet",
	"innodb_buffer_pool_size", "innodb_buffer_pool_instances", "innodb_redo_log_capacity",
	"innodb_log_file_size", "innodb_flush_log_at_trx_commit", "innodb_flush_method",
	"innodb_io_capacity", "innodb_io_capacity_max", "innodb_lock_wait_timeout",
	"lock_wait_timeout", "innodb_rollback_on_timeout", "innodb_deadlock_detect",
	"innodb_print_all_deadlocks", "innodb_file_per_table",
	"innodb_read_io_threads", "innodb_write_io_threads",
	"sync_binlog", "binlog_format", "binlog_row_image", "gtid_mode",
	"enforce_gtid_consistency", "log_bin", "log_replica_updates",
	"replica_parallel_workers", "replica_preserve_commit_order",
	"read_only", "super_read_only", "offline_mode", "sql_mode",
	"performance_schema", "table_open_cache", "table_definition_cache",
	"thread_cache_size", "tmp_table_size", "max_heap_table_size",
	"sort_buffer_size", "join_buffer_size",
	"slow_query_log", "long_query_time", "log_queries_not_using_indexes",
	"version", "hostname", "port", "datadir",
	"character_set_server", "collation_server", "time_zone",
	"max_execution_time", "transaction_isolation", "skip_name_resolve",
	"lower_case_table_names",
}

func (d Deps) GetSettings(ctx context.Context, _ *mcp.CallToolRequest, in GetSettingsInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 80, 200)
	q := strings.TrimSpace(in.Query)
	var sql string
	var args []any
	if q == "" {
		placeholders := strings.Repeat("?,", len(curatedSettings))
		placeholders = strings.TrimSuffix(placeholders, ",")
		sql = `
SELECT VARIABLE_NAME AS name, VARIABLE_VALUE AS value
FROM performance_schema.global_variables
WHERE VARIABLE_NAME IN (` + placeholders + `)
ORDER BY VARIABLE_NAME
`
		args = make([]any, len(curatedSettings))
		for i, n := range curatedSettings {
			args[i] = n
		}
	} else {
		sql = `
SELECT VARIABLE_NAME AS name, VARIABLE_VALUE AS value
FROM performance_schema.global_variables
WHERE VARIABLE_NAME LIKE ?
ORDER BY VARIABLE_NAME
`
		args = []any{"%" + q + "%"}
	}
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	note := "from performance_schema.global_variables (MySQL official); pass query to search all names"
	if q == "" {
		note = "curated operations variables; pass query to search all global variables (max_connections, innodb_*, gtid_mode, ...)"
	}
	return jsonResult(wrapRows(t, note, res))
}

type GetStatusInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Query  string `json:"query,omitempty" jsonschema:"Optional substring filter on VARIABLE_NAME. Empty = curated ops counters."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 80, cap 200."`
}

var curatedStatus = []string{
	"Uptime", "Threads_connected", "Threads_running", "Threads_created", "Max_used_connections",
	"Questions", "Queries", "Slow_queries", "Aborted_clients", "Aborted_connects",
	"Connections", "Connection_errors_max_connections",
	"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads", "Innodb_buffer_pool_pages_dirty",
	"Innodb_row_lock_current_waits", "Innodb_row_lock_time", "Innodb_row_lock_time_avg",
	"Innodb_row_lock_waits", "Innodb_deadlocks", "Innodb_history_list_length",
	"Innodb_rows_read", "Innodb_rows_inserted", "Innodb_rows_updated", "Innodb_rows_deleted",
	"Created_tmp_disk_tables", "Created_tmp_tables", "Select_full_join", "Select_scan",
	"Sort_merge_passes", "Table_locks_waited", "Open_tables", "Opened_tables",
	"Handler_read_rnd_next", "Com_select", "Com_insert", "Com_update", "Com_delete",
	"Com_commit", "Com_rollback", "Bytes_received", "Bytes_sent",
}

func (d Deps) GetStatus(ctx context.Context, _ *mcp.CallToolRequest, in GetStatusInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 80, 200)
	q := strings.TrimSpace(in.Query)
	var sql string
	var args []any
	if q == "" {
		placeholders := strings.Repeat("?,", len(curatedStatus))
		placeholders = strings.TrimSuffix(placeholders, ",")
		sql = `
SELECT VARIABLE_NAME AS name, VARIABLE_VALUE AS value
FROM performance_schema.global_status
WHERE VARIABLE_NAME IN (` + placeholders + `)
ORDER BY VARIABLE_NAME
`
		args = make([]any, len(curatedStatus))
		for i, n := range curatedStatus {
			args[i] = n
		}
	} else {
		sql = `
SELECT VARIABLE_NAME AS name, VARIABLE_VALUE AS value
FROM performance_schema.global_status
WHERE VARIABLE_NAME LIKE ?
ORDER BY VARIABLE_NAME
`
		args = []any{"%" + q + "%"}
	}
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	note := "from performance_schema.global_status; pass query to search all counters"
	if q == "" {
		note = "curated operations counters; pass query like innodb_ or Com_ to search all SHOW GLOBAL STATUS names"
	}
	return jsonResult(wrapRows(t, note, res))
}

type GetErrorLogInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Query  string `json:"query,omitempty" jsonschema:"Optional substring filter on error message or prio."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 40, cap 200."`
}

func errorLogSQL(query string) string {
	sql := `
SELECT LOGGED AS logged_at, THREAD_ID, PRIO, ERROR_CODE, SUBSYSTEM,
       LEFT(DATA, 400) AS data
FROM performance_schema.error_log
WHERE 1=1
`
	if query != "" {
		sql += " AND (DATA LIKE ? OR PRIO LIKE ? OR ERROR_CODE LIKE ? OR SUBSYSTEM LIKE ?)"
	}
	sql += " ORDER BY LOGGED DESC"
	return sql
}

func (d Deps) GetErrorLog(ctx context.Context, _ *mcp.CallToolRequest, in GetErrorLogInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 40, 200)
	q := strings.TrimSpace(in.Query)
	sql := errorLogSQL(q)
	var args []any
	if q != "" {
		pat := "%" + q + "%"
		args = []any{pat, pat, pat, pat}
	}
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		hint := missingHint(err,
			"performance_schema.error_log is not available",
			"Needs MySQL 8.0.22+ and SELECT on performance_schema.error_log. Use get_settings query=log_error for the file path instead.",
		)
		hint["target"] = in.Target
		return jsonResult(hint)
	}
	return jsonResult(wrapRows(t, "from performance_schema.error_log (MySQL 8.0.22+); not the on-disk error log file", res))
}
