// 作用：本机 stdio MCP，只读查询本机清单里的 PostgreSQL 实例
// 运行主机：开发机（Cursor 拉起）
// 调用方：Cursor MCP（~/.cursor/mcp.json）
// 大概流程：
//   1) 读 PG_TARGETS_FILE（mtime 变化则下次调用自动 reload）
//   2) 密码走 Windows Credential Manager / pgpass，不读 targets 里的 password
//   3) 注册发现 / 诊断 / 受控 query_postgres 只读工具
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

	"postgres-mcp-server/internal/pg"
	"postgres-mcp-server/internal/targets"
	"postgres-mcp-server/internal/tools"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	path := strings.TrimSpace(os.Getenv("PG_TARGETS_FILE"))
	reg, err := targets.New(path)
	if err != nil {
		return err
	}

	readOnly := strings.TrimSpace(os.Getenv("PG_MCP_READ_ONLY"))
	if readOnly == "" {
		readOnly = "true"
	}
	if !parseBool(readOnly) {
		return fmt.Errorf("PG_MCP_READ_ONLY must stay true; this binary has no write tools")
	}

	cfg := pg.Config{
		ConnectTimeout:   parseDur("PG_MCP_CONNECT_TIMEOUT", 5*time.Second),
		StatementTimeout: parseDur("PG_MCP_STATEMENT_TIMEOUT", 10*time.Second),
		LockTimeout:      parseDur("PG_MCP_LOCK_TIMEOUT", 2*time.Second),
		AllowAnalyze:     parseBool(os.Getenv("PG_MCP_ALLOW_EXPLAIN_ANALYZE")),
	}
	pool := pg.NewPooler(cfg)
	defer pool.Close()

	deps := tools.Deps{
		Reg:     reg,
		Pool:    pool,
		Version: version,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "postgres",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "Read-only PostgreSQL MCP. Resolve a target from the local registry, " +
			"then use discovery and diagnostics tools. For uncovered questions use query_postgres " +
			"(single SELECT only). Do not claim to have written, updated, or deleted any data. " +
			"Passwords never appear in tool output.",
	})

	log.Printf("postgres-mcp %s (read-only) targets=%s", version, path)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_targets", "List targets",
		"List local PostgreSQL targets (name, aliases, host, dbname). Reloads the targets file if it changed; parse errors are returned instead of silently keeping the old list. Filter with query. No passwords."), deps.ListTargets)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_target_info", "Get target info",
		"One target from the local registry. target is the stable name or a unique alias. No password is returned."), deps.GetTargetInfo)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_server_overview", "Server overview",
		"Version, current database, session counts, recovery flag. Requires target."), deps.GetServerOverview)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_database_stats", "Database stats",
		"pg_stat_database for the connected database. Requires target."), deps.GetDatabaseStats)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_settings", "Settings",
		"Curated pg_settings, or search with query. Requires target."), deps.GetSettings)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_schemas", "List schemas",
		"Schemas on the connected database. Requires target."), deps.ListSchemas)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_tables", "List tables",
		"User tables/views. Optional schema and name query. Requires target."), deps.ListTables)

	mcp.AddTool(srv, tools.ReadOnlyTool("describe_relation", "Describe relation",
		"Columns, indexes, constraints, replica identity for schema.table. Use before writing query_postgres."), deps.DescribeRelation)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_sessions", "List sessions",
		"pg_stat_activity rows (query text truncated). Optional state filter. Requires target."), deps.ListSessions)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_long_transactions", "Long transactions",
		"Sessions with an open transaction older than min_seconds (default 30). Requires target."), deps.ListLongTransactions)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_idle_transactions", "Idle transactions",
		"idle in transaction sessions older than min_seconds (default 5). Requires target."), deps.ListIdleTransactions)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_blocking_tree", "Blocking tree",
		"Blocked vs blocking sessions via pg_blocking_pids. Empty means no waits now."), deps.GetBlockingTree)

	mcp.AddTool(srv, tools.ReadOnlyTool("list_slow_queries", "Slow queries",
		"Top statements from pg_stat_statements. If the extension is missing, returns a hint instead of inventing a new tool."), deps.ListSlowQueries)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_table_stats", "Table stats",
		"pg_stat_user_tables: scans, tuples, last vacuum/analyze. Optional schema/query."), deps.GetTableStats)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_index_stats", "Index stats",
		"pg_stat_user_indexes. Low idx_scan is a hint, not a drop instruction."), deps.GetIndexStats)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_vacuum_status", "Vacuum status",
		"In-progress vacuum (PG16 and PG17 column names) plus tables ordered by dead tuples. Progress errors are returned, not hidden."), deps.GetVacuumStatus)

	mcp.AddTool(srv, tools.ReadOnlyTool("get_replication_status", "Replication status",
		"Recovery flag, primary WAL LSN or standby receive/replay LSN, pg_stat_wal_receiver, pg_stat_replication, and slots. Do not drop slots from this MCP."), deps.GetReplicationStatus)

	mcp.AddTool(srv, tools.ReadOnlyTool("query_postgres", "Query Postgres",
		"Controlled read-only escape hatch: one SELECT/WITH, READ ONLY transaction, row/size/timeout limits. Rejects dblink, advisory locks, and quoted-identifier bypasses. Not for EXPLAIN ANALYZE, DML, or DDL."), deps.QueryPostgres)

	mcp.AddTool(srv, tools.ReadOnlyTool("explain_query", "Explain query",
		"EXPLAIN (FORMAT JSON) without executing. analyze=true is off by default and forbidden on production targets (environment/tags/name)."), deps.ExplainQuery)

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
