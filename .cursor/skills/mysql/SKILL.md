---
name: mysql
description: Use when querying MySQL through the mysql MCP tools, including sessions, InnoDB/metadata locks, slow queries, table/index stats, replication, global variables/status, and read-only SQL. Use when the user asks to inspect a MySQL production or UAT database, Sleep with open trx, blocking, history list, replica lag, or 查库 / MySQL 排障.
---

# MySQL 只读排障查询

## 核心原则

一个 MCP，多个 target。先 `list_targets` 解析稳定 `name`，再带 `target` 查库。不要猜库名，也不要为了一个新场景就要求加 Tool。

高频问题用专用诊断 Tool。没覆盖的问题走：`list_tables` → `describe_relation` → `query_mysql`。需要任意全局参数用 `get_settings`（`query` 搜索全部变量名）；需要任意状态计数用 `get_status`。`query_mysql` 是受控只读逃生口，不是万能写入口。

这组 MCP 工具只有读取能力。不要声称已 UPDATE、DELETE、KILL、FLUSH、OPTIMIZE 或 STOP replica。未在对话里明确要求「对 MySQL 做写操作」时，即使工具列表里出现写工具，也不要调用。本二进制默认也没有写工具。

需要参数细节时读取 [references/tool-reference.md](references/tool-reference.md)。查发版契约、主机、库名时优先检索本仓库或 `cicd/` 对应服务 README，不要改业务仓。

调用前先发现 MCP 工具 schema（Cursor 命名空间通常是 `user-mysql`），再按 schema 传参。密码只在本机凭据库或 mysqlpass，不要写入仓库或回复。`list_targets` / `get_target_info` 不会返回密码。本机文件怎么写见仓库 `mysql-mcp-server/docs/configuration.md`；Docker 实验室见 `lab/mysql/README.md`。

## 查询前先确定

1. 用户要查的是哪个业务库和哪个环境。
2. 用 `list_targets` 的 `query` 解析，得到稳定 `name`（如 `orders-prod`）。别名冲突时把候选列给用户，不要猜。
3. 之后每个工具都传这个 `target`。
4. 先用专用 Tool；专用 Tool 不够再用 `describe_relation` + `query_mysql`。任意 `innodb_*` / `max_connections` 一类参数直接 `get_settings`；`Threads_running` / `Innodb_deadlocks` 一类计数直接 `get_status`。

`name` 是机器稳定 ID，不要因为显示名变化就改它。

## 工具选择

| 目的 | 首选工具 | 使用要点 |
|---|---|---|
| 有哪些库 / 解析中文别名 | `list_targets` | `query` 如 `orders`；会按文件 mtime 自动 reload |
| 看一个 target 的连接信息 | `get_target_info` | 无密码 |
| 实例是否正常 | `get_server_overview` | 版本、只读、线程、开着的 trx、复制通道、最近 error_log |
| 库占用 | `get_database_stats` | `information_schema.TABLES` 按 schema 汇总 |
| 全局参数 | `get_settings` | 默认策展项；`query` 搜索全部 `global_variables` |
| 全局状态 | `get_status` | 默认策展计数；`query` 搜索全部 `global_status` |
| 错误日志 | `get_error_log` | `performance_schema.error_log`（8.0.22+） |
| 有哪些 schema / 表 | `list_schemas` / `list_tables` | 写兜底 SQL 前先看 |
| 列、索引、约束、分区 | `describe_relation` | `relation` 用 `schema.table` |
| 谁连着 | `list_sessions` | 可选 `state`（Query / Sleep / Lock wait） |
| 长事务 | `list_long_transactions` | 默认超过 30s 的 `innodb_trx` |
| Sleep 仍占事务 | `list_idle_transactions` | MySQL 没有 idle in transaction 这个状态名 |
| 谁堵谁 | `get_blocking_tree` | InnoDB `data_lock_waits` + `metadata_locks` |
| 慢 SQL | `list_slow_queries` | digest 汇总；没有就提示，不要再造 Tool |
| 表 / 索引统计 | `get_table_stats` / `get_index_stats` | 低 `io_count` 只是线索 |
| InnoDB / 历史链表 | `get_innodb_metrics` | 没有 VACUUM；看 history list 与 deadlock |
| 复制 | `get_replication_status` | P_S 复制表 + GTID；不要 STOP/RESET |
| 专用 Tool 没覆盖 | `query_mysql` | 单条 SELECT；先描述关系再写 SQL |
| 执行计划 | `explain_query` | 默认不 ANALYZE；生产禁止 ANALYZE |

未注册、不要调用：`execute_sql`、`run_sql`、任何更新/删除/kill/flush/optimize 工具。不要在 `query_mysql` 里写 `EXPLAIN ANALYZE`、`LOAD DATA`、`USE`、`SET`、多语句或 DML。

## 标准工作流

### 未知问题 / 专用 Tool 不够

1. `list_targets` 得到 `target`。
2. `list_tables` 或用户已给表名。
3. `describe_relation` 看列、索引、约束。
4. `query_mysql` 写一条 SELECT。需要计划时用 `explain_query`，不要 ANALYZE 生产库。

### 连接打满 / Sleep 很多

1. `get_server_overview`、`get_status`（`query=Threads` 或 `Connection`）。
2. `list_sessions`；若还要按 user/host 聚合，用 `query_mysql`：

```sql
SELECT PROCESSLIST_USER, PROCESSLIST_HOST, PROCESSLIST_COMMAND, COUNT(*) AS n
FROM performance_schema.threads
WHERE TYPE = 'FOREGROUND' AND PROCESSLIST_ID IS NOT NULL
GROUP BY PROCESSLIST_USER, PROCESSLIST_HOST, PROCESSLIST_COMMAND
ORDER BY n DESC;
```

不要为此再要求一个 `group_sessions_by_user` Tool。

### 锁等待 / 卡住

1. `get_blocking_tree`。
2. 不够再 `list_sessions`（`state=Query`）或 `get_innodb_metrics`。
3. 只报告阻塞链，不要声称已经 KILL 会话。

### 慢 / 全表扫描

1. `list_slow_queries`（无 digest 则说明缺口）。
2. `get_table_stats` / `get_index_stats`。
3. 需要某一张表细节时 `describe_relation`。

### 复制

1. `get_replication_status`。
2. 更深的 GTID / worker 错误用 `query_mysql` 查 Performance Schema。不要 STOP replica。

## 效率规则

- 已有精确 `target` 就直接下钻，否则先 `list_targets`。
- 不要为了“全面”把 21 个工具都调一遍。
- 不要原样贴整表统计或整段 SQL 结果。先结论，再留 target、process id、表名、少量行。
- 回复里不要出现密码、连接串里的口令、`credential_ref` 对应的秘密。

## 重要边界

- `MYSQL_MCP_READ_ONLY=true` 不是安全的全部。账号本身必须只读。
- `query_mysql` 仍只有一个领域：MySQL 只读查询。`GET_LOCK`、`LOAD_FILE`、`SLEEP`、`USE` 会被拒绝；不要把它描述成绝对无副作用。它会返回你 SELECT 到的业务行（可能含 PII）；优先用专用诊断工具。
- 改 targets 文件后若 JSON 无效，工具会报错，不会假装已经切到新库。
- 生产 target 默认 `sslmode=verify-full`，不允许 `prefer` / `disable` / `skip-verify`。
- 发现某个查询每天都在用，再考虑把它从 generic query「晋升」为专用 Tool。不要想到一个场景就加一个 Tool。
- 本机同时开夜莺 + Jenkins + PostgreSQL + MySQL 可能超过 Cursor 工具上限。查库时关掉暂不用的 MCP。
- 改 targets 文件后无需改 `mcp.json`；下一轮 `list_targets` 会 reload。改 MCP 二进制或 `mcp.json` 仍要重载 Cursor MCP。

## 回复规范

1. 先给结论：哪个 `target`、现象（长事务 / 阻塞 / 慢 SQL / 复制延迟等）。
2. 再给可核对证据：process id、user、表名、少量统计。
3. 分清「当前没有匹配」「权限不足」「Performance Schema 未开」「MCP 拒绝的写 SQL」。
4. 需要改业务 SQL 或授权时说明缺口，让开发或 DBA 改他们那边；运维只在 MCP 仓适配。

## 完整示例

用户问：“查 orders 生产库，为什么 Threads_running 这么高？”

1. `list_targets`：`{"query":"orders"}` → `orders-prod`
2. `get_status`：`{"target":"orders-prod","query":"Threads"}`
3. `list_sessions`：`{"target":"orders-prod","state":"Query"}`
4. 若有 Lock wait，再 `get_blocking_tree`。
5. 汇总：target、线程数、主要 user/host、是否锁等待。不要说已经杀掉会话。
