# mysql-mcp-server

本机 stdio MCP，用来查本机清单里的 MySQL 8 实例：会话、InnoDB / 元数据锁、慢 SQL、表/索引统计、复制、全局参数与状态、以及一条受控只读 SQL 逃生口。进程跑在开发机，按 `target` 连到对应库。

这不是 MCP Hub，也不是「一库一个 MCP」。数据库变多，只改本机 `mysql-targets.json`，不增加 MCP 进程，也不增加 Tool 数量。

官方 Oracle [MySQL MCP](https://github.com/oracle/mcp/tree/main/src/mysql-mcp-server) 面向 HeatWave / MySQL AI，不是值班排障。本服务按 [MySQL 8.4 sys Schema](https://dev.mysql.com/doc/refman/8.4/en/sys-schema.html)、[Performance Schema](https://dev.mysql.com/doc/refman/8.4/en/performance-schema.html)、[InnoDB 锁信息](https://dev.mysql.com/doc/refman/8.4/en/innodb-information-schema-examples.html)、[复制 Performance Schema 表](https://dev.mysql.com/doc/refman/8.4/en/performance-schema-replication-tables.html) 做只读诊断，模型与仓库里的 PostgreSQL MCP 相同。

## 默认范围

未在对话里明确要求「对 MySQL 做写操作」时，Agent 不得执行任何 DML / DDL / `KILL` / `FLUSH`。本二进制也没有写工具。`MYSQL_MCP_READ_ONLY` 必须为 `true`，它只是程序自己的约束。

真正的安全边界是数据库账号：单独建 **MCP 专用只读用户**（建议名 `mcp_ro`），不要给 `SUPER` / `SYSTEM_VARIABLES_ADMIN` / `CONNECTION_ADMIN` / `REPLICATION_SLAVE_ADMIN` / `FILE` / `RELOAD`，也不要把业务应用账号配进 MCP。

### 用 MySQL 管理员创建 `mcp_ro`

在实例上用管理员（常见 `root`）执行。`CREATE USER` 是**整个实例一份**；业务库的 `GRANT SELECT` 按 MCP 要连的**每个库**各做一次。口令只写在执行现场，随后放进凭据管理器或 mysqlpass，**不要**写进 `mysql-targets.json`，也不要提交仓库。

```sql
-- 1) 创建登录用户（实例级，做一次）。把 'replace-me' 换成现场口令。
CREATE USER 'mcp_ro'@'%' IDENTIFIED BY 'replace-me';

-- 2) 诊断权限（官方：PROCESS 看会话/InnoDB 事务；REPLICATION CLIENT 看复制状态）
GRANT PROCESS, REPLICATION CLIENT, SHOW DATABASES, SHOW VIEW ON *.* TO 'mcp_ro'@'%';

-- 3) Performance Schema / sys（官方排障视图）
GRANT SELECT ON performance_schema.* TO 'mcp_ro'@'%';
GRANT SELECT ON sys.* TO 'mcp_ro'@'%';

-- 4) 业务库只读（每个库一行，把 yourdb 换成真实库名）
GRANT SELECT ON yourdb.* TO 'mcp_ro'@'%';

FLUSH PRIVILEGES;
```

不要 `GRANT SELECT ON mysql.*`（`mysql.user` 含哈希）。不要 `GRANT ALL`。

核对：

```sql
SHOW GRANTS FOR 'mcp_ro'@'%';
SELECT USER(), CURRENT_USER();
```

再用 `mcp_ro` 连 `yourdb` 跑一条 `SELECT 1`。口令配置见下方「凭据与配置文件」。

查询约定见 [MySQL Skill](../.cursor/skills/mysql/SKILL.md)。

高频问题用专用 Tool；没覆盖的问题先 `describe_relation`，再 `query_mysql`。任意全局参数走 `get_settings`（`query` 搜全部 `performance_schema.global_variables`），任意状态计数走 `get_status`。不要为每个 sys 视图再造一个 Tool。

需要 **MySQL 8.0+**（复制表、digest、`error_log` 按 8.0.22+ / 8.4 官方表来）。更早版本部分工具会返回权限/表不存在的 hint，而不是假装有数据。

## 能力

Cursor 里命名空间通常是 `user-mysql`；WorkBuddy 等以该产品 MCP 面板里的名字为准。本二进制注册 **21** 个只读工具。

| 层 | 能力 | 工具 |
| --- | --- | --- |
| Target | 列出 / 解析本机目标 | `list_targets`、`get_target_info` |
| Overview | 实例、库大小、参数、状态、错误日志 | `get_server_overview`、`get_database_stats`、`get_settings`、`get_status`、`get_error_log` |
| Catalog | schema / 表 / 列 | `list_schemas`、`list_tables`、`describe_relation` |
| Session | 会话与阻塞 | `list_sessions`、`list_long_transactions`、`list_idle_transactions`、`get_blocking_tree` |
| Performance | 慢 SQL 与统计 | `list_slow_queries`、`get_table_stats`、`get_index_stats` |
| Engine | InnoDB / 复制 | `get_innodb_metrics`、`get_replication_status` |
| Generic | 受控只读 SQL / 计划 | `query_mysql`、`explain_query` |

未注册、不要调用：任意 `INSERT`/`UPDATE`/`DELETE`/`KILL`/`FLUSH`/`OPTIMIZE` 类工具。`query_mysql` 只接受单条 `SELECT`/`WITH`，在 `START TRANSACTION READ ONLY` 里执行，有 statement/lock/行数/体积限制。扫描器会检查反引号标识符（`` `GET_LOCK` ``）以及 `LOAD_FILE` / `SLEEP` / `USE` / `SET` 等有副作用的写法。不要在里面写 `EXPLAIN ANALYZE`；看计划用 `explain_query`（默认不执行 SQL）。生产 target（`environment`/`tags` 为 prod/production/prd，或 name 以 `-prod` / `_prod` 结尾）上 `analyze=true` 会被拒绝。`query_mysql` 是数据面只读：查业务表会返回业务行（可能含 PII）。优先用专用诊断工具，不要整表导出。

`list_targets` / `get_target_info` 每次调用会检查 targets 文件 mtime。JSON 错误、重复 name、缺字段时会向调用方报错，并继续保留上一份成功清单，不会假装已经切库。改文件后下一轮即可看到新库，不用重载 MCP，也不用改客户端配置。删除或改名的 target 会立刻关掉对应连接池。

`name` 是稳定机器 ID（如 `orders-prod`）。人类可读名称放 `aliases` / `tags` / `description`。除 `list_targets` 外，其它工具都要带 `target`。别名冲突时返回候选，不猜。

`list_slow_queries` 依赖 `performance_schema.events_statements_summary_by_digest`。消费者未开或没授权时会明确提示，不要因此再造一个 Tool。

MySQL 没有 VACUUM：历史链表 / 长事务看 `get_innodb_metrics`（`trx_rseg_history_len`）和 `list_long_transactions`。

## 凭据与配置文件

完整字段表、密码文件路径、匹配规则和原理见 **[docs/configuration.md](docs/configuration.md)**。本机 Docker 联调步骤与 21 个工具验收清单见仓库 **[lab/mysql/](../lab/mysql/README.md)**。

三份本机文件，职责分开：

| 文件 | 放哪 | 写什么 |
| --- | --- | --- |
| 客户端 `mcp.json` | WorkBuddy：`~/.workbuddy/mcp.json`；Cursor：`~/.cursor/mcp.json`（其它见根 README） | 二进制路径 + `MYSQL_TARGETS_FILE`（绝对路径）+ `MYSQL_MCP_READ_ONLY=true`。**不写密码** |
| targets JSON | `MYSQL_TARGETS_FILE` 指向的任意本机路径 | `name` / `host` / `dbname` / `user` / `sslmode` / `credential_ref`。**禁止 `password` 字段** |
| 密码 | Windows：凭据管理器 Generic 目标 = `credential_ref`；或 mysqlpass | 见下 |

取密码顺序：Windows 上先查 `credential_ref` 对应的 Generic 凭据；没有再读 mysqlpass。Linux / macOS 只有 mysqlpass。不读 `MYSQL_PWD`，也不解析官方混淆过的 `~/.mylogin.cnf`。

mysqlpass 默认位置（格式与 pgpass 相同：`hostname:port:database:username:password`）：

- Windows：`%APPDATA%\mysql\mysqlpass.conf`
- Linux / macOS：`~/.mysqlpass`（必须 `chmod 600`）
- 可用环境变量 `MYSQL_PASSFILE` 覆盖

Windows 写入凭据管理器（不回显）：

```powershell
cd mysql-mcp-server
.\scripts\set-credential.ps1 -Target orders-prod -User mcp_ro
```

`mcp.json` 只告诉 MCP 清单在哪，不要为每个库加一个环境变量密码。

## 构建

需要本机 Go 1.23+。

```bash
cd mysql-mcp-server
go test ./...
go build -o mysql-mcp-server.exe .
```

二进制不入库。日常请用仓库 [deploy/pack-windows.cmd](../deploy/README.md) 或 [deploy/pack-linux.sh](../deploy/README.md)。改源码后重新打包。

## 本机 MCP 配置

WorkBuddy 与 Cursor 用同一段 JSON。完整 `mcp.json` 见 [`examples/mcp.json.example`](../examples/mcp.json.example)。

```json
{
  "mcpServers": {
    "mysql": {
      "type": "stdio",
      "command": "D:/project/CICD/cicd/mcp/mysql-mcp-server/mysql-mcp-server.exe",
      "args": [],
      "env": {
        "MYSQL_TARGETS_FILE": "C:/Users/15509/.cursor/mysql-targets.json",
        "MYSQL_MCP_READ_ONLY": "true"
      }
    }
  }
}
```

未写 `sslmode` 时默认 `verify-full`。生产 target 不允许 `prefer` / `disable` / `skip-verify`。本机或没有可信证书的非生产库可以显式写 `disable` 或 `prefer`。

可选：`MYSQL_MCP_STATEMENT_TIMEOUT`（默认 `10s`）、`MYSQL_MCP_LOCK_TIMEOUT`（默认 `2s`）、`MYSQL_MCP_CONNECT_TIMEOUT`（默认 `5s`）。`MYSQL_MCP_ALLOW_EXPLAIN_ANALYZE` 默认关；即使打开，生产 target 仍禁止 ANALYZE。

21 个工具都声明了 `ReadOnlyHint=true` 和 `OutputSchema`。

本机同时开着夜莺 + Jenkins + PostgreSQL + MySQL 时，有的客户端（例如 Cursor 社区版）工具数上限大约 40。查库时在 MCP 面板关掉暂不用的服务。

## 本机验收

```powershell
# 不连 MySQL，只核对接手和只读工具清单
.\scripts\smoke-stdio.ps1
```

接真实或实验室库：按 [docs/configuration.md](docs/configuration.md) 配好三份文件后，在所用客户端里 `list_targets` → `get_server_overview`。从零搭 Docker 实验室并按 21 个工具打勾：[lab/mysql/README.md](../lab/mysql/README.md)。

## 目录

```text
main.go                 入口（stdio）
internal/targets/       本机目标清单，mtime reload
internal/cred/          Windows Credential Manager / mysqlpass
internal/sqlguard/      query_mysql 只读校验
internal/mysql/         每 target 连接池，READ ONLY 事务
internal/tools/         21 个只读工具
docs/configuration.md   targets / mcp.json / 密码文件怎么写
examples/               targets 样例（无密码、无真实主机）
scripts/                smoke / 写入本机凭据
```

## Codex 接入与多环境配置

在用户级 `C:/Users/15509/.codex/config.toml`合并下面配置，替换程序和配置文件的绝对路径，保留原有设置；不要重复定义同名表。

```toml
[mcp_servers.mysql]
command = "D:/project/CICD/cicd/mcp/mysql-mcp-server/mysql-mcp-server.exe"
args = []
enabled = true
startup_timeout_sec = 20
tool_timeout_sec = 60

[mcp_servers.mysql.env]
MYSQL_TARGETS_FILE = "C:/Users/15509/.cursor/mysql-targets.json"
MYSQL_MCP_READ_ONLY = "true"
```

一份 targets 清单可列出 UAT / PROD 多个库，每条使用唯一 `name`、环境标识和独立 `credential_ref`。先 `list_targets`，再用明确的 `target` 查询。需要隔离时拆清单并注册两个 MCP 实例。

可复制 [UAT / PROD targets 样例](examples/mysql-targets.multi-env.example.json) 到本机后修改，并让上述 targets 环境变量指向它。数据库密码或 SSH 私钥不写入清单。

保存后重启对应 MCP 连接。CLI 可用 `codex mcp list` 检查配置、在会话中用 `/mcp` 核对连接；握手成功后再做小范围远端只读查询。

添加步骤、字段解释、凭据、环境切换和排障见 [Codex 完整指南](../CODEX.md)；可复制 [五服务 TOML](../examples/codex.toml.example) 或 [多环境 TOML](../examples/codex.multi-env.toml.example)。
