// 作用：本机 stdio MCP，只读查询本机清单里的 MySQL 实例
// 运行主机：开发机（Cursor / WorkBuddy 拉起）
// 调用方：MCP 客户端（~/.cursor/mcp.json 或 ~/.workbuddy/mcp.json）
// 大概流程：
//   1) 读 MYSQL_TARGETS_FILE（mtime 变化则下次调用自动 reload）
//   2) 密码走 Windows Credential Manager / mysqlpass，不读 targets 里的 password
//   3) 注册发现 / 诊断 / 受控 query_mysql 只读工具
//   4) 走 stdio MCP
// 勿放密钥：密码不进仓库、不进 targets JSON、不进本进程日志
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mysqldb "mysql-mcp-server/internal/mysql"
	"mysql-mcp-server/internal/targets"
	"mysql-mcp-server/internal/tools"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	path := strings.TrimSpace(os.Getenv("MYSQL_TARGETS_FILE"))
	reg, err := targets.New(path)
	if err != nil {
		return err
	}

	readOnly := strings.TrimSpace(os.Getenv("MYSQL_MCP_READ_ONLY"))
	if readOnly == "" {
		readOnly = "true"
	}
	if !parseBool(readOnly) {
		return fmt.Errorf("MYSQL_MCP_READ_ONLY must stay true; this binary has no write tools")
	}

	cfg := mysqldb.Config{
		ConnectTimeout:   parseDur("MYSQL_MCP_CONNECT_TIMEOUT", 5*time.Second),
		StatementTimeout: parseDur("MYSQL_MCP_STATEMENT_TIMEOUT", 10*time.Second),
		LockTimeout:      parseDur("MYSQL_MCP_LOCK_TIMEOUT", 2*time.Second),
		AllowAnalyze:     parseBool(os.Getenv("MYSQL_MCP_ALLOW_EXPLAIN_ANALYZE")),
	}
	pool := mysqldb.NewPooler(cfg)
	defer pool.Close()

	deps := tools.Deps{
		Reg:     reg,
		Pool:    pool,
		Version: version,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "mysql",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "Read-only MySQL MCP. Resolve a target from the local registry, " +
			"then use discovery and diagnostics tools. For uncovered questions use query_mysql " +
			"(single SELECT only). Do not claim to have written, updated, killed, or flushed anything. " +
			"Passwords never appear in tool output.",
	})

	log.Printf("mysql-mcp %s (read-only) targets=%s", version, path)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_targets", "List targets",
		"List local MySQL targets (name, aliases, host, dbname). Reloads the targets file if it changed; parse errors are returned instead of silently keeping the old list. Filter with query. No passwords."), deps.ListTargets)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_target_info", "Get target info",
		"One target from the local registry. target is the stable name or a unique alias. No password is returned."), deps.GetTargetInfo)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_server_overview", "Server overview",
		"Version, read_only, GTID/binlog flags, thread counts, open trx, replication channels, recent error_log. Requires target."), deps.GetServerOverview)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_database_stats", "Database stats",
		"information_schema.TABLES sizes grouped by schema. Requires target."), deps.GetDatabaseStats)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_settings", "Settings",
		"Curated performance_schema.global_variables, or search all names with query. Requires target."), deps.GetSettings)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_status", "Status",
		"Curated performance_schema.global_status, or search all counters with query. Requires target."), deps.GetStatus)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_error_log", "Error log",
		"Recent rows from performance_schema.error_log (MySQL 8.0.22+). Optional query filter. Requires target."), deps.GetErrorLog)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_schemas", "List schemas",
		"information_schema.SCHEMATA. Requires target."), deps.ListSchemas)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_tables", "List tables",
		"User tables/views with engine and estimated size. Optional schema and name query. Requires target."), deps.ListTables)

	mcp.AddTool(srv, tools.ReadOnlyTool("describe_relation", "Describe relation",
		"Columns, indexes, constraints, partitions for schema.table. Use before writing query_mysql."), deps.DescribeRelation)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_sessions", "List sessions",
		"performance_schema.threads plus innodb_trx (query text truncated). Optional command/state filter. Requires target."), deps.ListSessions)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_long_transactions", "Long transactions",
		"Open innodb_trx older than min_seconds (default 30). Requires target."), deps.ListLongTransactions)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_idle_transactions", "Idle transactions",
		"Sleep sessions that still hold an InnoDB transaction, older than min_seconds (default 5). Requires target."), deps.ListIdleTransactions)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_blocking_tree", "Blocking tree",
		"InnoDB data_lock_waits plus metadata_locks. Empty means no waits now."), deps.GetBlockingTree)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_slow_queries", "Slow queries",
		"Top statements from events_statements_summary_by_digest. If consumers are off, returns a hint instead of inventing a new tool."), deps.ListSlowQueries)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_table_stats", "Table stats",
		"information_schema.TABLES plus table I/O waits. Optional schema/query."), deps.GetTableStats)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_index_stats", "Index stats",
		"table_io_waits_summary_by_index_usage. Low io_count is a hint, not a drop instruction."), deps.GetIndexStats)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_innodb_metrics", "InnoDB metrics",
		"INNODB_METRICS, buffer pool, open trx, top wait classes. History list length is the VACUUM analog. Do not KILL from this MCP."), deps.GetInnodbMetrics)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_replication_status", "Replication status",
		"Performance Schema replication connection/applier/worker tables, GTID, Group Replication members when present. Do not STOP/RESET replica from this MCP."), deps.GetReplicationStatus)

	mcp.AddTool(srv, tools.ReadOnlyTool("query_mysql", "Query MySQL",
		"Controlled read-only escape hatch: one SELECT/WITH, READ ONLY transaction, row/size/timeout limits. Rejects LOAD_FILE, GET_LOCK, SLEEP, USE, SET, and quoted-identifier bypasses. Not for EXPLAIN ANALYZE, DML, or DDL. This is a full data-plane read: querying application tables returns those rows (possibly PII). Prefer dedicated diagnostic tools; do not dump large business tables."), deps.QueryMySQL)

	mcp.AddTool(srv, tools.ReadOnlyTool("explain_query", "Explain query",
		"EXPLAIN FORMAT=JSON without executing. analyze=true is off by default and forbidden on production targets (environment/tags/name)."), deps.ExplainQuery)

	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseDur(env string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(env))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("WARN %s=%q invalid, using %s", env, v, def)
		return def
	}
	return d
}
