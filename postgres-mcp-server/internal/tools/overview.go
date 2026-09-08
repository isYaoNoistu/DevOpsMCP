package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetServerOverviewInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func (d Deps) GetServerOverview(ctx context.Context, _ *mcp.CallToolRequest, in GetServerOverviewInput) (*mcp.CallToolResult, any, error) {
	const sql = `
SELECT
  version() AS version,
  current_database() AS current_database,
  current_user AS current_user,
  inet_server_addr()::text AS server_addr,
  inet_server_port() AS server_port,
  pg_postmaster_start_time() AS postmaster_start,
  pg_is_in_recovery() AS in_recovery,
  (SELECT count(*) FROM pg_stat_activity) AS sessions,
  (SELECT count(*) FROM pg_stat_activity WHERE state = 'active') AS active_sessions,
  (SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction') AS idle_in_transaction
`
	res, t, err := d.query(ctx, in.Target, sql, nil, 1)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type GetDatabaseStatsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func (d Deps) GetDatabaseStats(ctx context.Context, _ *mcp.CallToolRequest, in GetDatabaseStatsInput) (*mcp.CallToolResult, any, error) {
	const sql = `
SELECT datname, numbackends, xact_commit, xact_rollback,
       blks_hit, blks_read, tup_returned, tup_fetched,
       tup_inserted, tup_updated, tup_deleted,
       conflicts, temp_files, temp_bytes, deadlocks,
       stats_reset
FROM pg_stat_database
WHERE datname = current_database()
`
	res, t, err := d.query(ctx, in.Target, sql, nil, 5)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "", res))
}

type GetSettingsInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Query  string `json:"query,omitempty" jsonschema:"Optional substring filter on setting name or short_desc. Empty = curated ops settings."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 80, cap 200."`
}

var curatedSettings = []string{
	"max_connections", "shared_buffers", "work_mem", "maintenance_work_mem",
	"effective_cache_size", "listen_addresses", "port", "max_wal_size",
	"checkpoint_timeout", "wal_level", "hot_standby", "archive_mode",
	"default_transaction_read_only", "statement_timeout", "lock_timeout",
	"idle_in_transaction_session_timeout", "log_min_duration_statement",
	"shared_preload_libraries", "max_replication_slots", "max_wal_senders",
	"autovacuum", "synchronous_commit", "TimeZone", "server_version",
}

func (d Deps) GetSettings(ctx context.Context, _ *mcp.CallToolRequest, in GetSettingsInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 80, 200)
	q := strings.TrimSpace(in.Query)
	var sql string
	var args []any
	if q == "" {
		sql = `
SELECT name, setting, unit, vartype, context, source, short_desc
FROM pg_settings
WHERE name = ANY(string_to_array($1, ','))
ORDER BY name
`
		args = []any{strings.Join(curatedSettings, ",")}
	} else {
		sql = `
SELECT name, setting, unit, vartype, context, source, short_desc
FROM pg_settings
WHERE name ILIKE $1 OR short_desc ILIKE $1
ORDER BY name
`
		args = []any{"%" + q + "%"}
	}
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	note := ""
	if q == "" {
		note = "curated operations settings; pass query to search all pg_settings"
	}
	return jsonResult(wrapRows(t, note, res))
}
