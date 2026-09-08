package tools

import (
	"context"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"postgres-mcp-server/internal/targets"
)

type GetVacuumStatusInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max table rows, default 40, cap 200."`
}

func vacuumProgressSQL(versionNum int) string {
	if versionNum >= 170000 {
		return `
SELECT pid, datname, relid::regclass::text AS relation,
       phase, heap_blks_total, heap_blks_scanned, heap_blks_vacuumed,
       index_vacuum_count, max_dead_tuple_bytes, dead_tuple_bytes, num_dead_item_ids
FROM pg_stat_progress_vacuum
`
	}
	return `
SELECT pid, datname, relid::regclass::text AS relation,
       phase, heap_blks_total, heap_blks_scanned, heap_blks_vacuumed,
       index_vacuum_count, max_dead_tuples, num_dead_tuples
FROM pg_stat_progress_vacuum
`
}

func (d Deps) GetVacuumStatus(ctx context.Context, _ *mcp.CallToolRequest, in GetVacuumStatusInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 40, 200)
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{
		"target": t.Name,
	}
	versionNum, verr := queryServerVersionNum(ctx, d, t)
	if verr != nil {
		out["progress_error"] = verr.Error()
		out["progress"] = []any{}
	} else {
		progress, err := d.Pool.Query(ctx, t, vacuumProgressSQL(versionNum), nil, 50)
		if err != nil {
			out["progress_error"] = err.Error()
			out["progress"] = []any{}
		} else {
			out["progress"] = progress.Rows
			out["server_version_num"] = versionNum
		}
	}
	tableSQL := `
SELECT schemaname, relname,
       n_live_tup, n_dead_tup,
       CASE WHEN n_live_tup + n_dead_tup > 0
            THEN round(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 2)
            ELSE 0 END AS dead_pct,
       last_vacuum, last_autovacuum, last_analyze, last_autoanalyze,
       vacuum_count, autovacuum_count
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC NULLS LAST
`
	tables, err := d.Pool.Query(ctx, t, tableSQL, nil, limit)
	if err != nil {
		return nil, nil, err
	}
	out["tables"] = tables.Rows
	return jsonResult(out)
}

func queryServerVersionNum(ctx context.Context, d Deps, t targets.Target) (int, error) {
	res, err := d.Pool.Query(ctx, t, `SELECT current_setting('server_version_num') AS version_num`, nil, 1)
	if err != nil {
		return 0, err
	}
	if len(res.Rows) == 0 {
		return 0, fmt.Errorf("server_version_num is empty")
	}
	return asInt(res.Rows[0]["version_num"])
}

func asInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("server_version_num %q: %w", n, err)
		}
		return i, nil
	case []byte:
		i, err := strconv.Atoi(string(n))
		if err != nil {
			return 0, fmt.Errorf("server_version_num %q: %w", n, err)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("server_version_num has unexpected type %T", v)
	}
}

type GetReplicationStatusInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
}

func replicationOverviewSQL() string {
	return `
SELECT pg_is_in_recovery() AS in_recovery,
       CASE WHEN NOT pg_is_in_recovery() THEN pg_current_wal_lsn()::text END AS current_wal_lsn,
       CASE WHEN NOT pg_is_in_recovery() THEN pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0') END AS current_wal_lsn_bytes,
       CASE WHEN pg_is_in_recovery() THEN pg_last_wal_receive_lsn()::text END AS last_wal_receive_lsn,
       CASE WHEN pg_is_in_recovery() THEN pg_last_wal_replay_lsn()::text END AS last_wal_replay_lsn,
       CASE WHEN pg_is_in_recovery() THEN pg_wal_lsn_diff(pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn()) END AS replay_lag_bytes,
       CASE WHEN pg_is_in_recovery() THEN pg_last_xact_replay_timestamp() END AS last_xact_replay_timestamp
`
}

func walReceiverSQL() string {
	return `
SELECT pid, status,
       receive_start_lsn::text AS receive_start_lsn,
       received_lsn::text AS received_lsn,
       latest_end_lsn::text AS latest_end_lsn,
       last_msg_send_time, last_msg_receipt_time, latest_end_time,
       slot_name, sender_host, sender_port
FROM pg_stat_wal_receiver
`
}

func (d Deps) GetReplicationStatus(ctx context.Context, _ *mcp.CallToolRequest, in GetReplicationStatusInput) (*mcp.CallToolResult, any, error) {
	t, err := d.resolve(in.Target)
	if err != nil {
		return nil, nil, err
	}
	overview, err := d.Pool.Query(ctx, t, replicationOverviewSQL(), nil, 1)
	if err != nil {
		return nil, nil, err
	}
	repl, err := d.Pool.Query(ctx, t, `
SELECT pid, usename, application_name, client_addr::text AS client_addr,
       state, sync_state, sent_lsn::text, write_lsn::text, flush_lsn::text, replay_lsn::text,
       write_lag, flush_lag, replay_lag
FROM pg_stat_replication
`, nil, 50)
	if err != nil {
		return nil, nil, err
	}
	slots, err := d.Pool.Query(ctx, t, `
SELECT slot_name, plugin, slot_type, database, active,
       restart_lsn::text, confirmed_flush_lsn::text
FROM pg_replication_slots
`, nil, 100)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{
		"target":   t.Name,
		"replicas": repl.Rows,
		"slots":    slots.Rows,
	}
	if len(overview.Rows) > 0 {
		out["overview"] = overview.Rows[0]
	}
	receiver, rerr := d.Pool.Query(ctx, t, walReceiverSQL(), nil, 10)
	if rerr != nil {
		out["wal_receiver_error"] = rerr.Error()
		out["wal_receiver"] = []any{}
	} else {
		out["wal_receiver"] = receiver.Rows
	}
	note := "slots are included here; promote to a dedicated tool only if this is queried every day. On standby, overview includes receive/replay LSN and pg_stat_wal_receiver."
	if t.IsProduction() && len(slots.Rows) > 0 {
		note += " Inactive slots on prod can retain WAL; do not drop them from this MCP."
	}
	out["note"] = note
	return jsonResult(out)
}
