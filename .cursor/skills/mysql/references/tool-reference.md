# MySQL MCP 工具参考

## 通用约定

- 除 `list_targets` 外，都要传 `target`（稳定 `name`，或能唯一命中的 alias / query）。
- `name` 是机器 ID，例如 `orders-prod`。不要把中文显示名当成 `target` 硬编码；先 `list_targets`。
- 工具结果是 JSON 文本。失败时把错误原文留给用户判断：解析失败、认证失败、权限不足、SQL 被 guard 拒绝。
- 行数默认约 50～100，上限 500。响应体积按 JSON 实际字节计算，上限约 256KiB。重复列名会改成 `col` / `col_2`。
- MCP 在 `START TRANSACTION READ ONLY` 里跑查询，并设置 `max_execution_time`（默认 10s）和 lock wait（默认 2s）。
- 密码不会出现在任何工具输出里。
- 生产 `sslmode` 必须是 `require` / `verify-ca` / `verify-full`；缺省是 `verify-full`。
- targets 文件 mtime 变化后若 JSON/字段不合法，工具返回错误，并保留上一份成功清单。

诊断数据来自 MySQL 8 官方目录：`performance_schema.global_variables` / `global_status`、`threads`、`events_statements_summary_by_digest`、`data_lock_waits`、`metadata_locks`、复制表、`information_schema.innodb_trx` / `INNODB_METRICS`、`performance_schema.error_log`。

## Target

### `list_targets`

列出本机 `mysql-targets.json`。文件 mtime 变化则先 reload。

| 参数 | 说明 |
|---|---|
| `query` | 可选。匹配 name / aliases / tags / dbname / environment / host |

### `get_target_info`

| 参数 | 说明 |
|---|---|
| `target` | 稳定名或唯一别名 |

返回 host、port、dbname、user、`credential_ref`、tags。无密码。

## Overview

`get_server_overview`：版本、`read_only` / GTID / binlog、线程与 trx 摘要、复制通道、最近 `error_log`。

`get_database_stats`：`information_schema.TABLES` 按 schema 汇总（InnoDB 行数/体积是估计值）。

`get_settings`：`query` 为空时返回策展运维变量；有 `query` 则在全部 `performance_schema.global_variables` 里按 `VARIABLE_NAME LIKE`。

`get_status`：同样模式，表是 `performance_schema.global_status`。

`get_error_log`：`performance_schema.error_log`（8.0.22+）。表不存在时返回 hint，指向 `get_settings query=log_error`。

## Catalog

`list_schemas`：只要 `target`。

`list_tables`：`schema`、`query`（表名子串）、`limit` 可选。

`describe_relation`：`relation` 必须是 `table` 或 `schema.table`，仅允许普通标识符。多 schema 重名时返回候选。结果含列、索引、约束、分区。

## Session

`list_sessions`：可选 `state`（如 `Query`、`Sleep`、`Lock wait`），匹配 command 或 state。

`list_long_transactions`：`min_seconds` 默认 30，读 `innodb_trx`。

`list_idle_transactions`：`min_seconds` 默认 5。COMMAND=`Sleep` 且仍有 `innodb_trx`。

`get_blocking_tree`：`innodb_locks`（`data_lock_waits`）和 `metadata_locks`。空列表表示当前没有阻塞。

## Performance / Engine

`list_slow_queries`：`events_statements_summary_by_digest`。不可用时返回 `error` + `hint`。

`get_table_stats` / `get_index_stats`：可选 `schema`、`query`。

`get_innodb_metrics`：`INNODB_METRICS`、buffer pool、当前 trx、top wait classes。不要建议从这个 MCP 去 KILL。

`get_replication_status`：P_S 复制 connection/applier/worker、GTID、可选 Group Replication members。`connection_configuration` 不含密码。不要 STOP/RESET replica。

## Generic

### `query_mysql`

| 参数 | 说明 |
|---|---|
| `target` | 必填 |
| `sql` | 单条 SELECT / WITH，最多约 8000 字符 |
| `max_rows` | 默认 100，最大 500 |

拒绝：多语句、DML/DDL、`USE`、`SET`、`LOAD`、`EXPLAIN`、`INTO OUTFILE`、`FOR UPDATE` / `FOR SHARE`，以及 `LOAD_FILE` / `GET_LOCK` / `SLEEP` / `BENCHMARK` 等（含 `` `GET_LOCK` ``）。字符串字面量里的关键字可以通过。带反引号的列名如 `` `lock` `` 可以通过。这是数据面只读：查业务表会返回业务行（可能含 PII），不要整表导出。

### `explain_query`

对 subject SQL 做 `EXPLAIN FORMAT=JSON`。不要自己写 EXPLAIN 前缀。

| 参数 | 说明 |
|---|---|
| `analyze` | 默认 false。true 会真正执行 SQL；生产 target 永久拒绝；非生产还要 `MYSQL_MCP_ALLOW_EXPLAIN_ANALYZE=true` |

## 错误判读

| 现象 | 含义 |
|---|---|
| `no target matched` / `ambiguous` | 先 `list_targets`，不要猜 |
| `reload targets file` / `parse targets file` | targets JSON 坏了或字段不合法；先修文件 |
| `sslmode ... is not allowed` | 生产 target 用了 `prefer`/`disable`/`skip-verify` |
| `must not store password` | targets 文件里出现了 password，删掉 |
| `no password for target` | 本机凭据或 mysqlpass 未配 |
| `Access denied` | 凭据错或用户不存在 |
| `denied` / Error 1142 | `mcp_ro` 缺 SELECT / PROCESS |
| `only a single SELECT` / `rejects keyword` | guard 拦住了非只读或危险 SQL |
| digest / error_log is not available | 扩展/表未开或未授权，改查 `list_sessions` 或让 DBA 授权 |
