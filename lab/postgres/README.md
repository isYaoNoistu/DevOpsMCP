# PostgreSQL MCP 本机实验室

给测试同学一套**可丢弃的 Docker PostgreSQL**，用来验收 `postgres-mcp-server` 的 19 个只读工具、SQL 护栏和凭据配置。不是生产库，口令写在仓库里也只允许用在这个容器上。

配置原理（targets 与密码为什么要分开）见 [postgres-mcp-server/docs/configuration.md](../../postgres-mcp-server/docs/configuration.md)。

## 需要准备什么

| 条件 | 说明 |
| --- | --- |
| Docker Compose | 能拉 `postgres:16` |
| Go 1.23+ | 编译 MCP 二进制 |
| MCP 客户端 | WorkBuddy 或 Cursor（stdio MCP，配好后重载） |
| 本机 5432 端口 | 被占用则改 `.env` 的 `POSTGRES_PORT`，并同步改 targets / pgpass 的端口 |

## 目录里有什么

```text
lab/postgres/
├── README.md                 本文件：流程 + 验收清单
├── docker-compose.yml        实验室数据库
├── .env.example              复制为 .env（可改端口/口令）
├── init/                     首次启动时建库对象（volume 已存在则不会重跑）
│   ├── 01-extension.sql      pg_stat_statements
│   ├── 02-role.sql           mcp_ro 只读角色
│   └── 03-schema.sql         chaos.orders / chaos.payments 样例数据
├── targets.lab.json          拷到本机的目标清单（无 password 字段）
├── pgpass.example            实验室 pgpass 一行
├── mcp.fragment.json         粘进 WorkBuddy / Cursor mcp.json 的 postgres 段
└── scripts/
    ├── install-local-config.ps1 / .sh   安装 lab 清单和 pgpass 行
    └── make-blocking.ps1 / .sh          可选：制造锁等待
```

实验室约定（仅此容器）：

| 项 | 值 |
| --- | --- |
| 容器名 | `postgres-mcp-lab` |
| 库 | `mcp_lab` |
| 超级用户（只给 docker exec / 初始化） | `postgres` / `mcp-lab-super` |
| MCP 账号 | `mcp_ro` / `mcp-lab-readonly` |
| target `name` | `mcp-lab-local` |
| `sslmode` | `disable`（本机、非生产） |
| `credential_ref` | `postgres/mcp-lab-local` |

`mcp_ro` 已设置 `default_transaction_read_only`、`statement_timeout=10s`、`lock_timeout=2s`。MCP **没有**写工具，不能 `pg_terminate_backend`。

## 完整测试流程

### 1. 启动数据库

在仓库根目录：

```powershell
cd lab/postgres
copy .env.example .env
docker compose up -d
docker compose ps
```

第一次会跑 `init/*.sql`。若你改过 init 但容器是旧 volume，需要 `docker compose down -v` 后重建（会清空实验室数据）。

确认：

```powershell
docker exec postgres-mcp-lab psql -U postgres -d mcp_lab -c "\du mcp_ro"
docker exec postgres-mcp-lab psql -U mcp_ro -d mcp_lab -c "SELECT count(*) FROM chaos.orders;"
```

第二条应返回 `800`。若 `mcp_ro` 连失败，检查 02-role.sql 是否执行过。

### 2. 编译 MCP

```powershell
cd ..\..\postgres-mcp-server
go test ./...
go build -o postgres-mcp-server.exe .
```

Linux / macOS：`go build -o postgres-mcp-server .`

不连库的握手检查（可选）：

```powershell
.\scripts\smoke-stdio.ps1
```

### 3. 安装本机清单和密码文件

**不要**覆盖你已有的生产 `postgres-targets.json`。实验室用单独文件。

```powershell
cd ..\lab\postgres
.\scripts\install-local-config.ps1
```

脚本会：

1. 写入 `%USERPROFILE%\.cursor\postgres-targets.lab.json`
2. 确保 `%APPDATA%\postgresql\pgpass.conf` 存在，并追加实验室那一行（已有 `mcp_lab` 行则跳过）

Linux / macOS：`chmod +x scripts/install-local-config.sh && ./scripts/install-local-config.sh`（`~/.pgpass`，权限 `600`）。

Windows 也可改用凭据管理器，效果与 pgpass 二选一即可（凭据优先）：

```powershell
cd ..\..\postgres-mcp-server
.\scripts\set-credential.ps1 -Target mcp-lab-local -User mcp_ro
# 提示时输入：mcp-lab-readonly
```

密码文件位置与匹配规则见 [configuration.md](../../postgres-mcp-server/docs/configuration.md)。

改了 `.env` 里的 `POSTGRES_PORT` 或 `MCP_RO_PASSWORD` 时：同步改 `targets.lab.json` 的 `port`、`init/02-role.sql`、`pgpass.example`，然后 `down -v` 重建，再跑安装脚本。

### 4. 配置 MCP 并重载

把 `mcp.fragment.json` 合并进所用客户端的 `mcp.json`（WorkBuddy：`~/.workbuddy/mcp.json` 或界面粘贴；Cursor：`~/.cursor/mcp.json`）：

- `command` 改成你编出来的二进制**绝对路径**（Windows 带 `.exe`）
- `PG_TARGETS_FILE` 改成安装脚本打印的 `postgres-targets.lab.json` 绝对路径（脚本默认写到 `~/.cursor/`，WorkBuddy 可把文件拷到别处，只要路径对上）
- 保留 `"PG_MCP_READ_ONLY": "true"`

在 MCP 面板重载 **postgres**。查库期间可先关掉夜莺 / Jenkins，避免工具数量顶到上限。

### 5. 在对话里按清单点名验收

对 Agent 说：用 target `mcp-lab-local` 按下面清单测 PostgreSQL MCP。每项应**只读**，不要声称已经 kill 会话或 VACUUM。

把结果记在你们的测试单上（通过 / 失败 / 截图或 JSON 摘要）。

## 验收清单

下列 `target` 一律为 `mcp-lab-local`。

### A. 配置是否接上

| # | 做什么 | 预期 |
| --- | --- | --- |
| A1 | `list_targets`，`query` 为 `lab` 或 `本地` | 命中 `mcp-lab-local`，**没有** password 字段 |
| A2 | `get_target_info` | host=`127.0.0.1`，dbname=`mcp_lab`，user=`mcp_ro`，sslmode=`disable` |
| A3 | `get_server_overview` | 能连上；版本 16.x；非 recovery |

连不上时先看 configuration.md「常见失败」：pgpass 四段、端口、是否重载了 MCP。

### B. 目录与统计

| # | 工具 | 预期 |
| --- | --- | --- |
| B1 | `list_schemas` | 有 `chaos` |
| B2 | `list_tables`，`schema=chaos` | `orders`、`payments` |
| B3 | `describe_relation`，`relation=chaos.orders` | 有列、主键、`idx_orders_note_unused`；`replica_identity` 为 `default` 这类标签，不要把原始 char 码当结论 |
| B4 | `get_database_stats` / `get_settings` | 有库统计；settings 能搜 `statement_timeout` |
| B5 | `get_table_stats`，schema=`chaos` | `orders` 约 800 行 live（ANALYZE 过后） |
| B6 | `get_index_stats` | `idx_orders_note_unused` 的 `idx_scan` 很低（线索，不是一定要删） |
| B7 | `list_slow_queries` | 扩展已装时应能返回（可能为空列表，只要不是「未安装」） |
| B8 | `get_vacuum_status` | 有表；不必有正在跑的 vacuum |
| B9 | `get_replication_status` | 单机实验室：不是 standby，slots 应为空 |

### C. 会话

| # | 工具 | 预期 |
| --- | --- | --- |
| C1 | `list_sessions` | 至少有当前 MCP 连接；不要把 pid 当可以 terminate 的入口 |
| C2 | `list_long_transactions` / `list_idle_transactions` | 空列表也算通过（表示当前没有） |
| C3 | `get_blocking_tree` | 默认空。可选：另开窗口跑 `scripts/make-blocking.ps1`，90 秒内再查应看到 waiter 被 blocker 堵住 |

### D. 受控 SQL 与护栏（必须测）

| # | 调用 | 预期 |
| --- | --- | --- |
| D1 | `query_postgres`：`SELECT count(*) FROM chaos.orders` | 800 |
| D2 | `query_postgres`：`SELECT 1; SELECT 2` | **拒绝**（多语句） |
| D3 | `query_postgres`：`INSERT INTO chaos.orders ...` 或 `DELETE ...` | **拒绝** |
| D4 | `query_postgres`：`SELECT pg_advisory_lock(1)` | **拒绝** |
| D5 | `explain_query`，`analyze=false`，SQL 为 `SELECT * FROM chaos.orders WHERE id = 1` | 返回 JSON 计划，不真正当业务写入 |
| D6 | `explain_query`，`analyze=true`（不要设 `PG_MCP_ALLOW_EXPLAIN_ANALYZE`） | **拒绝**或明确未开启。本实验室 `environment=dev`，即使打开 env 也只应在测试机短暂使用 |

生产 target（名字以 `-prod` 结尾等）上 `analyze=true` 会被硬拒绝，本实验室故意不用生产名，以免本机 `sslmode=disable` 无法加载清单。

### E. 反向验收（MCP 做不到的）

| # | 说明 |
| --- | --- |
| E1 | 工具列表里**没有** `execute_sql`、`VACUUM`、`pg_terminate_backend` |
| E2 | Agent 不得声称已经杀掉阻塞会话；清理阻塞可等 90 秒结束，或 `docker compose restart` |

## 测完怎么拆

```powershell
cd lab/postgres
docker compose down -v
```

本机的 `postgres-targets.lab.json` 和 pgpass 里那一行可手动删。不要误删生产 pgpass 的其它行。

## 安全

- `.env` 已在仓库根 `.gitignore` 中忽略；不要把改成生产口令的 `.env` 提交上来。
- `mcp-lab-super` / `mcp-lab-readonly` 只允许打在这个 Compose 上。
- 不要对客户内网 IP 做「实验室」文档截图后开源。
