---
name: postgres
description: Use when querying PostgreSQL through the postgres MCP tools, including sessions, locks, slow queries, table/index stats, replication, schemas, and read-only SQL. Use when the user asks to inspect a production or UAT database, idle in transaction, blocking, vacuum, replication slots, or 查库 / PostgreSQL 排障.
---

# PostgreSQL 只读排障查询

## 核心原则

一个 MCP，多个 target。先 `list_targets` 解析稳定 `name`，再带 `target` 查库。不要猜库名，也不要为了一个新场景就要求加 Tool。

高频问题用专用诊断 Tool。没覆盖的问题走：`list_tables` → `describe_relation` → `query_postgres`。`query_postgres` 是受控只读逃生口，不是万能写入口。

这组 MCP 工具只有读取能力。不要声称已 UPDATE、DELETE、VACUUM、DROP slot 或杀掉会话。未在对话里明确要求「对 PostgreSQL 做写操作」时，即使工具列表里出现写工具，也不要调用。本二进制默认也没有写工具。

需要参数细节时读取 [references/tool-reference.md](references/tool-reference.md)。

调用前先发现 MCP 工具 schema（Cursor 命名空间通常是 `user-postgres`），再按 schema 传参。密码只在本机凭据库或 `pgpass`，不要写入仓库或回复。`list_targets` / `get_target_info` 不会返回密码。本机文件怎么写、密码默认路径见仓库 `postgres-mcp-server/docs/configuration.md`；Docker 实验室见 `lab/postgres/README.md`。

## 查询前先确定

1. 用户要查的是哪个业务库、哪个环境。
2. 用 `list_targets` 的 `query` 解析，得到稳定 `name`（如 `orders-prod`）。别名冲突时把候选列给用户，不要猜。
3. 之后每个工具都传这个 `target`。
4. 先用专用 Tool；专用 Tool 不够再用 `describe_relation` + `query_postgres`。

`name` 是机器稳定 ID，不要因为显示名变化就改它。文档里的 `orders-prod` 只是示例。

## 工具选择

| 目的 | 首选工具 | 使用要点 |
|---|---|---|
| 有哪些库 / 解析中文别名 | `list_targets` | `query` 如 `orders`；会按文件 mtime 自动 reload |
| 看一个 target 的连接信息 | `get_target_info` | 无密码 |
| 实例是否正常 | `get_server_overview` | 版本、会话数、是否 recovery |
| 当前库统计 | `get_database_stats` | `pg_stat_database` |
| 参数 | `get_settings` | 默认策展项；`query` 搜索全部 |
| 有哪些 schema / 表 | `list_schemas` / `list_tables` | 写兜底 SQL 前先看 |
| 列、索引、约束、replica identity | `describe_relation` | `relation` 用 `schema.table` |
| 谁连着 | `list_sessions` | 可选 `state` |
| 长事务 | `list_long_transactions` | 默认超过 30s |
| idle in transaction | `list_idle_transactions` | 默认超过 5s |
| 谁堵谁 | `get_blocking_tree` | 空结果表示当前没有阻塞 |
| 慢 SQL | `list_slow_queries` | 需要 `pg_stat_statements`；没有就提示，不要再造 Tool |
| 表 / 索引统计 | `get_table_stats` / `get_index_stats` | 低 `idx_scan` 只是线索 |
| vacuum | `get_vacuum_status` | 含进行中进度 |
| 复制与 slot | `get_replication_status` | 含 standby LSN、slots；不要建议从这个 MCP 去 drop |
| 专用 Tool 没覆盖 | `query_postgres` | 单条 SELECT；先描述关系再写 SQL |
| 执行计划 | `explain_query` | 默认不 ANALYZE；生产禁止 ANALYZE |

未注册、不要调用：`execute_sql`、`run_sql`、任何更新/删除/vacuum/kill/drop 工具。不要在 `query_postgres` 里写 `EXPLAIN ANALYZE`、`COPY`、多语句或 DML。

## 标准工作流

### 未知问题 / 专用 Tool 不够

1. `list_targets` 得到 `target`。
2. `list_tables` 或用户已给表名。
3. `describe_relation` 看列、索引、约束。
4. `query_postgres` 写一条 SELECT。需要计划时用 `explain_query`，不要 ANALYZE 生产库。

### idle in transaction 很多

1. `list_idle_transactions`。
2. 若还要按 `application_name` 聚合，用 `query_postgres` 查 `pg_stat_activity`。
3. 不要声称已经杀掉会话。

### 锁等待 / 卡住

1. `get_blocking_tree`。
2. 不够再 `list_sessions`（`state=active`）或 `query_postgres` 查 `pg_locks`。
3. 只报告阻塞链。

### 慢 / 膨胀

1. `list_slow_queries`（无扩展则说明缺口）。
2. `get_table_stats` / `get_index_stats` / `get_vacuum_status`。
3. 某一张表细节用 `describe_relation`。

### 复制

1. `get_replication_status`（含 slot）。
2. 更深的问题用 `query_postgres` 查系统视图。不要 drop slot。

## 效率规则

- 已有精确 `target` 就直接下钻，否则先 `list_targets`。
- 不要为了“全面”把 19 个工具都调一遍。
- 不要原样贴整表统计。先结论，再留 target、pid、表名、少量行。
- 回复里不要出现密码、连接串口令、`credential_ref` 对应的秘密。

## 重要边界

- `PG_MCP_READ_ONLY=true` 不是安全的全部。账号本身必须只读。
- `query_postgres` 会拒绝带引号的 `dblink_exec`、advisory lock 等，不要把它描述成绝对无副作用。
- 改 targets 文件后若 JSON 无效，工具会报错，不会假装已经切到新库。
- 生产 target 默认 `sslmode=verify-full`，不允许 `prefer`。
- 本机同时开夜莺 + Jenkins + PostgreSQL 可能超过 Cursor 工具上限。查库时关掉暂不用的 MCP。
- 改 targets 文件后无需改 `mcp.json`；下一轮 `list_targets` 会 reload。改二进制或 `mcp.json` 仍要重载 Cursor MCP。

## 回复规范

1. 先给结论：哪个 `target`、现象（长事务 / 阻塞 / 慢 SQL / 复制延迟等）。
2. 再给可核对证据：pid、`application_name`、表名、少量统计。
3. 分清「当前没有匹配」「权限不足」「扩展未装」「MCP 拒绝的写 SQL」。
4. 需要改业务 SQL 或授权时说明缺口，让开发或 DBA 处理。

## 完整示例

用户问：“查订单生产库，idle in transaction 为什么这么多？”

1. `list_targets`：`{"query":"orders"}` → 得到稳定 `name`（示例为 `orders-prod`）
2. `list_idle_transactions`：带上该 `target`
3. 若要看应用分布，再 `query_postgres` 按 `application_name` 聚合
4. 汇总数量、主要应用、最长空闲时间。不要说已经杀掉会话。
