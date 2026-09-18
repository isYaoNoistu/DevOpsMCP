package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetInnodbMetricsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max innodb_metrics rows, default 80, cap 200."`
}

func innodbMetricsSQL() string {
	return `
SELECT NAME AS name, SUBSYSTEM AS subsystem, COUNT AS count_value,
       MAX_COUNT AS max_count, AVG_COUNT AS avg_count, STATUS AS status,
       COMMENT AS comment
FROM information_schema.INNODB_METRICS
WHERE STATUS = 'enabled'
  AND (
    NAME IN (
      'lock_deadlocks','lock_timeouts','lock_row_lock_current_waits',
      'trx_rseg_history_len','trx_active_transactions',
      'buffer_pool_pages_total','buffer_pool_pages_dirty','buffer_pool_pages_free',
      'buffer_pool_read_requests','buffer_pool_reads','buffer_pool_wait_free',
      'log_lsn_current','log_lsn_checkpoint','log_lsn_buf_dirty_pages_added',
      'purge_trx_id_age','ddl_background_drop_indexes'
    )
    OR NAME LIKE 'lock_%'
    OR NAME LIKE 'buffer_pool_%'
    OR NAME LIKE 'trx_%'
    OR NAME LIKE 'log_%'
  )
ORDER BY SUBSYSTEM, NAME
`
}

func innodbBufferPoolSQL() string {
	return `
SELECT POOL_ID, POOL_SIZE, FREE_BUFFERS, DATABASE_PAGES, OLD_DATABASE_PAGES,
       MODIFIED_DB_PAGES, PENDING_DECOMPRESS, PENDING_READS,
       PENDING_FLUSH_LRU, PENDING_FLUSH_LIST,
       PAGES_READ, PAGES_WRITTEN, PAGES_CREATED,
       HIT_RATE, READ_AHEAD, READ_AHEAD_EVICTED
FROM information_schema.INNODB_BUFFER_POOL_STATS
`
}

func waitClassSQL() string {
	return `
SELECT EVENT_NAME AS event_name,
       COUNT_STAR AS wait_count,
       ROUND(SUM_TIMER_WAIT/1e12, 3) AS wait_sec,
       ROUND(AVG_TIMER_WAIT/1e12, 6) AS avg_sec
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE COUNT_STAR > 0
  AND EVENT_NAME NOT LIKE 'idle%'
ORDER BY SUM_TIMER_WAIT DESC
`
}

func (d Deps) GetInnodbMetrics(ctx context.Context, _ *mcp.CallToolRequest, in GetInnodbMetricsInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 80, 200)
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{"target": t.Name}

	metrics, merr := optionalSection(ctx, d, t, innodbMetricsSQL(), nil, limit)
	if merr != "" {
		out["metrics_error"] = merr
		out["metrics"] = []any{}
	} else {
		putSection(out, "metrics", metrics)
	}

	bp, berr := optionalSection(ctx, d, t, innodbBufferPoolSQL(), nil, 16)
	if berr != "" {
		out["buffer_pool_error"] = berr
	} else {
		putSection(out, "buffer_pool", bp)
	}

	trx, terr := optionalSection(ctx, d, t, `
SELECT trx_id, trx_state, trx_started,
       TIMESTAMPDIFF(SECOND, trx_started, NOW()) AS xact_age_sec,
       trx_mysql_thread_id AS id,
       trx_rows_locked, trx_rows_modified, trx_tables_in_use,
       trx_isolation_level, LEFT(trx_query, 200) AS trx_query
FROM information_schema.innodb_trx
ORDER BY trx_started
`, nil, 50)
	if terr != "" {
		out["trx_error"] = terr
	} else {
		putSection(out, "transactions", trx)
	}

	waits, werr := optionalSection(ctx, d, t, waitClassSQL(), nil, 30)
	if werr != "" {
		out["wait_error"] = werr
	} else {
		putSection(out, "top_waits", waits)
	}

	out["note"] = "InnoDB metrics, buffer pool, open trx, and top wait classes. MySQL has no VACUUM; watch trx_rseg_history_len / Innodb_history_list_length and lock_deadlocks. Do not KILL sessions from this MCP."
	return jsonResult(out)
}

type GetReplicationStatusInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func replicationVarsSQL() string {
	return `
SELECT VARIABLE_NAME AS name, VARIABLE_VALUE AS value
FROM performance_schema.global_variables
WHERE VARIABLE_NAME IN (
  'server_id','server_uuid','gtid_mode','enforce_gtid_consistency',
  'log_bin','binlog_format','binlog_row_image','sync_binlog',
  'log_replica_updates','read_only','super_read_only',
  'replica_parallel_workers','replica_preserve_commit_order',
  'source_uuid','rpl_semi_sync_master_enabled','rpl_semi_sync_replica_enabled'
)
ORDER BY VARIABLE_NAME
`
}

func (d Deps) GetReplicationStatus(ctx context.Context, _ *mcp.CallToolRequest, in GetReplicationStatusInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{"target": t.Name}

	vars, verr := optionalSection(ctx, d, t, replicationVarsSQL(), nil, 40)
	if verr != "" {
		out["variables_error"] = verr
	} else {
		putSection(out, "variables", vars)
	}

	gtid, gerr := optionalSection(ctx, d, t, `
SELECT @@global.gtid_executed AS gtid_executed,
       @@global.gtid_purged AS gtid_purged
`, nil, 1)
	if gerr != "" {
		out["gtid_error"] = gerr
	} else {
		putSection(out, "gtid", gtid)
	}

	conn, cerr := optionalSection(ctx, d, t, `
SELECT CHANNEL_NAME, GROUP_NAME, SOURCE_UUID, THREAD_ID, SERVICE_STATE,
       COUNT_RECEIVED_HEARTBEATS, LAST_HEARTBEAT_TIMESTAMP,
       LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE, LAST_ERROR_TIMESTAMP
FROM performance_schema.replication_connection_status
`, nil, 20)
	if cerr != "" {
		out["connection_status_error"] = cerr
		out["connection_status"] = []any{}
	} else {
		putSection(out, "connection_status", conn)
	}

	cfg, cfgerr := optionalSection(ctx, d, t, `
SELECT CHANNEL_NAME, HOST, PORT, USER, NETWORK_INTERFACE,
       AUTO_POSITION, SSL_ALLOWED, SSL_CA_FILE, HEARTBEAT_INTERVAL,
       COMPRESSION_ALGORITHM, GET_SOURCE_PUBLIC_KEY
FROM performance_schema.replication_connection_configuration
`, nil, 20)
	if cfgerr != "" {
		out["connection_configuration_error"] = cfgerr
	} else {
		putSection(out, "connection_configuration", cfg)
	}

	applier, aerr := optionalSection(ctx, d, t, `
SELECT CHANNEL_NAME, SERVICE_STATE, REMAINING_DELAY,
       COUNT_TRANSACTIONS_RETRIES
FROM performance_schema.replication_applier_status
`, nil, 20)
	if aerr != "" {
		out["applier_status_error"] = aerr
	} else {
		putSection(out, "applier_status", applier)
	}

	workers, werr := optionalSection(ctx, d, t, `
SELECT CHANNEL_NAME, WORKER_ID, THREAD_ID, SERVICE_STATE,
       LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE, LAST_ERROR_TIMESTAMP,
       LAST_APPLIED_TRANSACTION, APPLYING_TRANSACTION
FROM performance_schema.replication_applier_status_by_worker
`, nil, 50)
	if werr != "" {
		out["workers_error"] = werr
	} else {
		putSection(out, "workers", workers)
	}

	members, merr := optionalSection(ctx, d, t, `
SELECT CHANNEL_NAME, MEMBER_ID, MEMBER_HOST, MEMBER_PORT,
       MEMBER_STATE, MEMBER_ROLE, MEMBER_VERSION
FROM performance_schema.replication_group_members
`, nil, 20)
	if merr != "" {
		out["group_members_error"] = merr
	} else if len(members.Rows) > 0 || members.Truncated {
		putSection(out, "group_members", members)
	}

	out["note"] = "Uses Performance Schema replication tables (MySQL 8 official), not SHOW SLAVE STATUS. connection_configuration omits passwords. Do not STOP/RESET replica from this MCP."
	return jsonResult(out)
}
