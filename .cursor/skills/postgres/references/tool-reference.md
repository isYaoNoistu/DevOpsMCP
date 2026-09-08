# PostgreSQL MCP 工具参考

## 通用约定

- 除 `list_targets` 外，都要传 `target`（稳定 `name`，或能唯一命中的 alias / query）。
- `name` 是机器 ID，例如 `orders-prod`。不要把中文显示名当成 `target` 硬编码；先 `list_targets`。
- 工具结果是 JSON 文本。失败时把错误原文留给用户判断：解析失败、认证失败、权限不足、SQL 被 guard 拒绝。
- 行数默认约 50～100，上限 500。响应体积按 JSON 实际字节计算，上限约 256KiB。重复列名会改成 `col` / `col_2`，避免 map 覆盖。
- MCP 在 `BEGIN READ ONLY` 里跑查询，并设置 `statement_timeout`（默认 10s）和 `lock_timeout`（默认 2s）。连接归还池子前执行 `DISCARD ALL`。
- 密码不会出现在任何工具输出里。
- 生产 `sslmode` 必须是 `require` / `verify-ca` / `verify-full`；缺省是 `verify-full`。
- targets 文件 mtime 变化后若 JSON/字段不合法，工具返回错误，并保留上一份成功清单。

## Target

### `list_targets`

列出本机 `postgres-targets.json`。文件 mtime 变化则先 reload。解析失败会返回错误（保留上一份成功清单，但不继续当“已切库”）。

| 参数 | 说明 |
|---|---|
| `query` | 可选。匹配 name / aliases / tags / dbname / environment / host |

### `get_target_info`

| 参数 | 说明 |
|---|---|
| `target` | 稳定名或唯一别名 |

返回 host、port、dbname、user、`credential_ref`、tags。无密码。

## Overview

`get_server_overview`、`get_database_stats`：只要 `target`。

`get_settings`：`query` 为空时返回策展运维参数；有 `query` 则在全部 `pg_settings` 里搜 name / short_desc。

## Catalog

`list_schemas`：只要 `target`。

`list_tables`：`schema`、`query`（表名子串）、`limit` 可选。

`describe_relation`：`relation` 必须是 `table` 或 `schema.table`，仅允许普通标识符。多 schema 重名时返回候选。结果含列、索引、约束、`replica_identity`（`default` / `nothing` / `full` / `index`，不要把 `100` 当成有意义的值）。

## Session

`list_sessions`：可选 `state`（如 `active`、`idle in transaction`）。

`list_long_transactions`：`min_seconds` 默认 30。

`list_idle_transactions`：`min_seconds` 默认 5。

`get_blocking_tree`：基于 `pg_blocking_pids`。空列表表示当前没有阻塞。

## Performance / Maintenance

`list_slow_queries`：读 `pg_stat_statements`。扩展不存在时返回 `error` + `hint`，不是 MCP 缺工具。

`get_table_stats` / `get_index_stats`：可选 `schema`、`query`。

`get_vacuum_status`：按 `server_version_num` 选 PG16 / PG17 进度列；进度查询失败时返回 `progress_error`，不会装成“没有 vacuum”。

`get_replication_status`：是否 recovery；主库 `pg_current_wal_lsn`；standby 的 receive/replay LSN、`pg_stat_wal_receiver`（不含 `conninfo`）、`pg_stat_replication`、`pg_replication_slots`。不要建议 drop slot。

## Generic

### `query_postgres`

| 参数 | 说明 |
|---|---|
| `target` | 必填 |
| `sql` | 单条 SELECT / WITH，最多约 8000 字符 |
| `max_rows` | 默认 100，最大 500 |

拒绝：多语句、DML/DDL、`COPY`、`EXPLAIN`、`SET`，以及 `pg_read_file` / `dblink*` / `pg_advisory_lock` 等（含 `"dblink_exec"` 这种带引号写法）。字符串字面量和 dollar-quoted 字符串里的关键字可以通过。带引号的列名如 `"lock"` 可以通过。

### `explain_query`

对 subject SQL 做 `EXPLAIN (FORMAT JSON)`。不要自己写 EXPLAIN 前缀。

| 参数 | 说明 |
|---|---|
| `analyze` | 默认 false。true 会真正执行 SQL；生产 target（`environment` / `tags` / 名称后缀为 prod、production、prd）永久拒绝；非生产还要 `PG_MCP_ALLOW_EXPLAIN_ANALYZE=true` |

## 错误判读

| 现象 | 含义 |
|---|---|
| `no target matched` / `ambiguous` | 先 `list_targets`，不要猜 |
| `reload targets file` / `parse targets file` | targets JSON 坏了或字段不合法；先修文件，不要当已经切库 |
| `sslmode ... is not allowed` | 生产 target 用了 `prefer`/`allow`/`disable`，改成 `verify-full` 或 `require` |
| `must not store password` | targets 文件里出现了 password，删掉 |
| `no password for target` | 本机凭据或 pgpass 未配 |
| `password authentication failed` | 凭据错或用户不存在 |
| `permission denied` | `mcp_ro` 缺 SELECT / `pg_monitor` |
| `only a single SELECT` / `rejects keyword` | guard 拦住了非只读或危险 SQL |
| `pg_stat_statements is not available` | 扩展未装或未授权，改查 `pg_stat_activity` 或让 DBA 开扩展 |
