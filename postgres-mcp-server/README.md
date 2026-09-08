# postgres-mcp-server

本机 stdio MCP，用来查本机清单里的 PostgreSQL 实例：会话、锁等待、慢 SQL、表/索引统计、复制、以及一条受控只读 SQL 逃生口。进程跑在开发机，按 `target` 连到对应库。

这不是 MCP Hub，也不是「一库一个 MCP」。数据库变多，只改本机 `postgres-targets.json`，不增加 MCP 进程，也不增加 Tool 数量。

## 默认范围

未在对话里明确要求「对 PostgreSQL 做写操作」时，Agent 不得执行任何 DML / DDL。本二进制也没有写工具。`PG_MCP_READ_ONLY` 必须为 `true`，它只是程序自己的约束。

真正的安全边界是数据库账号：单独建 **MCP 专用只读角色**（建议名 `mcp_ro`），不要给 `SUPERUSER` / `CREATEDB` / `CREATEROLE`，也不要把业务应用账号配进 MCP。

### 用 postgres 超级用户创建 `mcp_ro`

在实例上用超级用户（常见角色名 `postgres`）执行。`CREATE ROLE` 是**整个实例一份**；`GRANT CONNECT` 按 MCP 要连的**每个库**各做一次。口令只写在执行现场，随后放进凭据管理器或 pgpass，**不要**写进 `postgres-targets.json`，也不要提交仓库。

```sql
-- 1) 创建登录角色（实例级，做一次）
CREATE ROLE mcp_ro
  LOGIN
  PASSWORD 'replace-me'
  NOSUPERUSER
  NOCREATEDB
  NOCREATEROLE
  INHERIT
  NOREPLICATION;

-- 2) 允许连上 MCP 要查的库（每个库一行，把 yourdb 换成真实库名）
GRANT CONNECT ON DATABASE yourdb TO mcp_ro;

-- 3) 读数据 + 看会话/统计/复制（PostgreSQL 14+）
GRANT pg_read_all_data TO mcp_ro;
GRANT pg_monitor TO mcp_ro;

-- 4) 会话默认只读，并加上超时，避免 Agent 把库拖死
ALTER ROLE mcp_ro SET default_transaction_read_only = on;
ALTER ROLE mcp_ro SET statement_timeout = '10s';
ALTER ROLE mcp_ro SET lock_timeout = '2s';
ALTER ROLE mcp_ro SET idle_in_transaction_session_timeout = '10s';
```

`pg_read_all_data` 覆盖当前库里已有和以后新建的表（仍要先有该库的 `CONNECT`）。`pg_monitor` 覆盖 `pg_stat_activity`、锁、复制、vacuum 进度等诊断视图。

可选：慢 SQL 工具 `list_slow_queries` 需要扩展（超级用户装一次，一般已有 `pg_monitor` 即可读）：

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

`shared_preload_libraries` 里要有 `pg_stat_statements`，改完需重启实例。没有扩展时 MCP 会明确提示，不是缺工具。

PostgreSQL 13 及更早没有 `pg_read_all_data` 时，在**每个目标库**里改用（`\c yourdb` 之后）：

```sql
GRANT USAGE ON SCHEMA public TO mcp_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO mcp_ro;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO mcp_ro;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO mcp_ro;
-- 业务表不在 public 时，对每个 schema 重复 USAGE / SELECT / DEFAULT PRIVILEGES
```

核对：

```sql
\du mcp_ro
SELECT rolname, rolsuper, rolcreatedb, rolcanlogin
FROM pg_roles WHERE rolname = 'mcp_ro';
```

应看到可以 LOGIN，且不是超级用户。再用 `mcp_ro` 连 `yourdb` 跑一条 `SELECT 1`。口令配置见下方「凭据与配置文件」。

查询约定见 [PostgreSQL Skill](../.cursor/skills/postgres/SKILL.md)。

高频问题用专用 Tool；没覆盖的问题先 `describe_relation`，再 `query_postgres`。不要为每个系统视图再造一个 Tool。

## 能力

Cursor 命名空间通常是 `user-postgres`。本二进制注册 19 个只读工具。

| 层 | 能力 | 工具 |
| --- | --- | --- |
| Target | 列出 / 解析本机目标 | `list_targets`、`get_target_info` |
| Overview | 实例与当前库 | `get_server_overview`、`get_database_stats`、`get_settings` |
| Catalog | schema / 表 / 列 | `list_schemas`、`list_tables`、`describe_relation` |
| Session | 会话与阻塞 | `list_sessions`、`list_long_transactions`、`list_idle_transactions`、`get_blocking_tree` |
| Performance | 慢 SQL 与统计 | `list_slow_queries`、`get_table_stats`、`get_index_stats` |
| Maintenance | vacuum / 复制 | `get_vacuum_status`、`get_replication_status` |
| Generic | 受控只读 SQL / 计划 | `query_postgres`、`explain_query` |

未注册、不要调用：任意 `INSERT`/`UPDATE`/`DELETE`/`VACUUM`/`DROP` 类工具。`query_postgres` 只接受单条 `SELECT`/`WITH`，在 `BEGIN READ ONLY` 里执行，有 statement/lock/行数/体积限制。扫描器会检查带引号的标识符（`public."dblink_exec"`）以及 `pg_advisory_lock` / `dblink*` 等有副作用的函数。连接归还池子前会 `DISCARD ALL`，避免会话级锁留在连接上。不要在里面写 `EXPLAIN ANALYZE`；看计划用 `explain_query`（默认不执行 SQL）。生产 target（`environment`/`tags` 为 prod/production/prd，或 name 以 `-prod` / `_prod` 结尾）上 `analyze=true` 会被拒绝。

`list_targets` / `get_target_info` 每次调用会检查 targets 文件 mtime。JSON 错误、重复 name、缺字段时会向调用方报错，并继续保留上一份成功清单，不会假装已经切库。改文件后下一轮即可看到新库，不用重载 Cursor MCP，也不用改 `mcp.json`。删除或改名的 target 会立刻关掉对应连接池。

`name` 是稳定机器 ID（如 `orders-prod`）。人类可读名称放 `aliases` / `tags` / `description`。除 `list_targets` 外，其它工具都要带 `target`。别名冲突时返回候选，不猜。

`list_slow_queries` 依赖 `pg_stat_statements`。扩展不存在或没授权时会明确提示，不要因此再造一个 Tool。

## 凭据与配置文件

完整字段表、密码文件路径、匹配规则和原理见 **[docs/configuration.md](docs/configuration.md)**。本机 Docker 联调步骤与 19 个工具验收清单见仓库 **[lab/postgres/](../lab/postgres/README.md)**。

三份本机文件，职责分开：

| 文件 | 放哪 | 写什么 |
| --- | --- | --- |
| `~/.cursor/mcp.json` | Cursor 用户配置 | 二进制路径 + `PG_TARGETS_FILE`（绝对路径）+ `PG_MCP_READ_ONLY=true`。**不写密码** |
| targets JSON | `PG_TARGETS_FILE` 指向的任意本机路径 | `name` / `host` / `dbname` / `user` / `sslmode` / `credential_ref`。**禁止 `password` 字段** |
| 密码 | Windows：凭据管理器 Generic 目标 = `credential_ref`；或 pgpass | 见下 |

取密码顺序：Windows 上先查 `credential_ref` 对应的 Generic 凭据；没有再读 pgpass。Linux / macOS 只有 pgpass。

pgpass 默认位置：

- Windows：`%APPDATA%\postgresql\pgpass.conf`（`C:\Users\<用户>\AppData\Roaming\postgresql\pgpass.conf`）
- Linux / macOS：`~/.pgpass`（必须 `chmod 600`）
- 可用环境变量 `PGPASSFILE` 覆盖

格式：`hostname:port:database:username:password`（与 libpq 相同）。库名/用户名大小写敏感；密码不去掉首尾空格。

Windows 写入凭据管理器（不回显）：

```powershell
cd postgres-mcp-server
.\scripts\set-credential.ps1 -Target orders-prod -User mcp_ro
```

`mcp.json` 只告诉 MCP 清单在哪，不要为每个库加一个环境变量密码。

## 构建

需要本机 Go 1.23+。

```bash
cd postgres-mcp-server
go test ./...
go build -o postgres-mcp-server.exe .
```

二进制不入库。改源码后在本机重新 `go build`。

## 本机 Cursor 配置

写在用户级 `~/.cursor/mcp.json`。targets 文件放在本机（例如 `~/.cursor/postgres-targets.json`），**不入库**。样例见 `examples/postgres-targets.example.json`。完整 `mcp.json` 见 [`examples/mcp.json.example`](../examples/mcp.json.example)。

```json
{
  "mcpServers": {
    "postgres": {
      "command": "/ABS/PATH/DevOpsMCP/postgres-mcp-server/postgres-mcp-server",
      "args": [],
      "env": {
        "PG_TARGETS_FILE": "/ABS/PATH/.cursor/postgres-targets.json",
        "PG_MCP_READ_ONLY": "true"
      }
    }
  }
}
```

未写 `sslmode` 时默认 `verify-full`（校验服务器证书）。生产 target 不允许 `prefer` / `allow` / `disable`。本机或没有可信证书的非生产库可以显式写 `prefer` 或 `require`。若本机 `postgres-targets.json` 里生产库仍是 `prefer`，启动或 reload 会失败，需要改成 `verify-full`（或至少 `require`）。

可选：`PG_MCP_STATEMENT_TIMEOUT`（默认 `10s`）、`PG_MCP_LOCK_TIMEOUT`（默认 `2s`）、`PG_MCP_CONNECT_TIMEOUT`（默认 `5s`）。`PG_MCP_ALLOW_EXPLAIN_ANALYZE` 默认关；即使打开，生产 target 仍禁止 ANALYZE。

19 个工具都声明了 `ReadOnlyHint=true` 和 `OutputSchema`。

本机同时开着夜莺 + Jenkins + PostgreSQL 时，可能超过 Cursor 社区常见的约 40 个工具会话上限。查库时在 MCP 面板关掉暂不用的服务。

## 本机验收

```powershell
# 不连 PostgreSQL，只核对接手和只读工具清单
.\scripts\smoke-stdio.ps1
```

接真实或实验室库：按 [docs/configuration.md](docs/configuration.md) 配好三份文件后，Cursor 里 `list_targets` → `get_server_overview`。从零搭 Docker 实验室并按 19 个工具打勾：[lab/postgres/README.md](../lab/postgres/README.md)。

## 目录

```text
main.go                 入口（stdio）
internal/targets/       本机目标清单，mtime reload
internal/cred/          Windows Credential Manager / pgpass
internal/sqlguard/      query_postgres 只读校验
internal/pg/            每 target 连接池，READ ONLY 事务
internal/tools/         19 个只读工具
docs/configuration.md   targets / mcp.json / 密码文件怎么写
examples/               targets 样例（无密码、无真实主机）
scripts/                smoke / 写入本机凭据
```
