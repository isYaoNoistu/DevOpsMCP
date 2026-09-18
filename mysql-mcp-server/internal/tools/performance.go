package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListSlowQueriesInput struct {
	Target string `json:"target" jsonschema:"Stable target name from list_targets."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows, default 20, cap 100."`
}

func slowQuerySQL() string {
	return `
SELECT SCHEMA_NAME AS schema_name,
       DIGEST AS digest,
       LEFT(DIGEST_TEXT, 300) AS query_text,
       COUNT_STAR AS calls,
       ROUND(SUM_TIMER_WAIT/1e12, 3) AS total_sec,
       ROUND(AVG_TIMER_WAIT/1e12, 3) AS avg_sec,
       ROUND(MAX_TIMER_WAIT/1e12, 3) AS max_sec,
       ROUND(SUM_LOCK_TIME/1e12, 3) AS lock_sec,
       SUM_ROWS_EXAMINED AS rows_examined,
       SUM_ROWS_SENT AS rows_sent,
       SUM_CREATED_TMP_DISK_TABLES AS tmp_disk_tables,
       SUM_NO_INDEX_USED AS no_index_used,
       SUM_NO_GOOD_INDEX_USED AS no_good_index_used,
       FIRST_SEEN AS first_seen,
       LAST_SEEN AS last_seen
FROM performance_schema.events_statements_summary_by_digest
WHERE SCHEMA_NAME IS NULL OR SCHEMA_NAME NOT IN ('mysql','performance_schema','sys')
ORDER BY AVG_TIMER_WAIT DESC
`
}

func (d Deps) ListSlowQueries(ctx context.Context, _ *mcp.CallToolRequest, in ListSlowQueriesInput) (*mcp.CallToolResult, any, error) {
	limit := clampLimit(in.Limit, 20, 100)
	res, t, err := d.query(ctx, in.Target, slowQuerySQL(), nil, limit)
	if err == nil {
		return jsonResult(wrapRows(t, "from performance_schema.events_statements_summary_by_digest; missing consumers is a server config issue, not an MCP gap", res))
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "events_statements_summary_by_digest") || strings.Contains(msg, "denied") || strings.Contains(msg, "doesn't exist") {
		hint := missingHint(err,
			"events_statements_summary_by_digest is not available",
			"Enable performance_schema statement digest consumers and GRANT SELECT on performance_schema.* to mcp_ro. Currently running SQL is still in list_sessions.",
		)
		hint["target"] = in.Target
		return jsonResult(hint)
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
SELECT t.TABLE_SCHEMA AS schema_name,
       t.TABLE_NAME AS table_name,
       t.ENGINE AS engine,
       t.TABLE_ROWS AS table_rows,
       t.DATA_LENGTH AS data_bytes,
       t.INDEX_LENGTH AS index_bytes,
       t.DATA_FREE AS data_free_bytes,
       t.AUTO_INCREMENT AS auto_increment,
       t.UPDATE_TIME AS update_time,
       io.COUNT_STAR AS io_count,
       io.COUNT_READ AS io_read,
       io.COUNT_WRITE AS io_write,
       ROUND(io.SUM_TIMER_WAIT/1e12, 3) AS io_wait_sec
FROM information_schema.TABLES t
LEFT JOIN performance_schema.table_io_waits_summary_by_table io
  ON io.OBJECT_SCHEMA = t.TABLE_SCHEMA AND io.OBJECT_NAME = t.TABLE_NAME
WHERE t.TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')
`
	var args []any
	if in.Schema != "" {
		if err := checkIdent(in.Schema); err != nil {
			return nil, nil, err
		}
		sql += " AND t.TABLE_SCHEMA = ?"
		args = append(args, in.Schema)
	}
	if in.Query != "" {
		sql += " AND t.TABLE_NAME LIKE ?"
		args = append(args, "%"+in.Query+"%")
	}
	sql += " ORDER BY t.DATA_LENGTH DESC"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(wrapRows(t, "information_schema.TABLES plus table_io_waits_summary_by_table when permitted", res))
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
SELECT OBJECT_SCHEMA AS schema_name,
       OBJECT_NAME AS table_name,
       INDEX_NAME AS index_name,
       COUNT_STAR AS io_count,
       COUNT_READ AS io_read,
       COUNT_WRITE AS io_write,
       ROUND(SUM_TIMER_WAIT/1e12, 3) AS io_wait_sec
FROM performance_schema.table_io_waits_summary_by_index_usage
WHERE OBJECT_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')
`
	var args []any
	if in.Schema != "" {
		if err := checkIdent(in.Schema); err != nil {
			return nil, nil, err
		}
		sql += " AND OBJECT_SCHEMA = ?"
		args = append(args, in.Schema)
	}
	if in.Query != "" {
		sql += " AND (OBJECT_NAME LIKE ? OR INDEX_NAME LIKE ?)"
		args = append(args, "%"+in.Query+"%", "%"+in.Query+"%")
	}
	sql += " ORDER BY COUNT_STAR ASC, OBJECT_NAME"
	res, t, err := d.query(ctx, in.Target, sql, args, limit)
	if err != nil {
		return jsonResult(func() map[string]any {
			h := missingHint(err,
				"table_io_waits_summary_by_index_usage is not available",
				"GRANT SELECT on performance_schema.* to mcp_ro. describe_relation still lists indexes from information_schema.STATISTICS.",
			)
			h["target"] = in.Target
			return h
		}())
	}
	return jsonResult(wrapRows(t, "low io_count on a named index may mean unused; confirm before asking anyone to DROP. INDEX_NAME NULL is table scan I/O.", res))
}
