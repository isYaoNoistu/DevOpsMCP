# MySQL MCP 本机实验室

给测试同学一套**可丢弃的 Docker MySQL 8.4**，用来验收 `mysql-mcp-server` 的 21 个只读工具、SQL 护栏和凭据配置。不是生产库，口令写在仓库里也只允许用在这个容器上。

配置原理见 [mysql-mcp-server/docs/configuration.md](../../mysql-mcp-server/docs/configuration.md)。

## 需要准备什么

| 条件 | 说明 |
| --- | --- |
| Docker Compose | 能拉 `mysql:8.4` |
| Go 1.23+ | 编译 MCP 二进制 |
| MCP 客户端 | WorkBuddy 或 Cursor（stdio MCP，配好后重载） |
| 本机 3306 端口 | 被占用则改 `.env` 的 `MYSQL_PORT`，并同步改 targets / mysqlpass 的端口 |

## 目录里有什么

```text
lab/mysql/
├── README.md
├── docker-compose.yml
├── .env.example
├── init/
│   ├── 01-user.sql           mcp_ro 只读用户
│   └── 02-schema.sql         chaos_orders / chaos_payments 样例数据
├── targets.lab.json
├── mysqlpass.example
├── mcp.fragment.json
└── scripts/
    ├── install-local-config.ps1 / .sh
    └── make-blocking.ps1 / .sh
```

实验室约定（仅此容器）：

| 项 | 值 |
| --- | --- |
| 容器名 | `mysql-mcp-lab` |
| 库 | `mcp_lab` |
| 超级用户（只给 docker exec / 初始化） | `root` / `mcp-lab-super` |
| MCP 账号 | `mcp_ro` / `mcp-lab-readonly` |
| target `name` | `mcp-lab-local` |
| `sslmode` | `disable`（本机、非生产） |
| `credential_ref` | `mysql/mcp-lab-local` |

MCP **没有**写工具，不能 `KILL`。

## 完整测试流程

### 1. 启动数据库

```powershell
cd lab/mysql
copy .env.example .env
docker compose up -d
docker compose ps
```

第一次会跑 `init/*.sql`。若你改过 init 但容器是旧 volume，需要 `docker compose down -v` 后重建。

确认：

```powershell
docker exec mysql-mcp-lab mysql -uroot -pmcp-lab-super -e "SHOW GRANTS FOR 'mcp_ro'@'%';"
docker exec mysql-mcp-lab mysql -umcp_ro -pmcp-lab-readonly mcp_lab -e "SELECT COUNT(*) FROM chaos_orders;"
```

第二条应返回 `800`。

### 2. 编译 MCP

```powershell
cd ..\..\mysql-mcp-server
go test ./...
go build -o mysql-mcp-server.exe .
.\scripts\smoke-stdio.ps1
```

### 3. 安装本机 targets / mysqlpass

```powershell
cd ..\lab\mysql
.\scripts\install-local-config.ps1
```

Windows 也可再写入凭据管理器：

```powershell
cd ..\..\mysql-mcp-server
.\scripts\set-credential.ps1 -Target mcp-lab-local -User mcp_ro
```

口令：`mcp-lab-readonly`。

### 4. 接到 MCP 客户端

把 `mcp.fragment.json` 里的 `command` 和 `MYSQL_TARGETS_FILE` 改成绝对路径，粘进 `mcp.json`，重载 mysql 服务。

### 5. 工具验收清单

在对话里按顺序（都带 `target=mcp-lab-local`）：

1. `list_targets` query=`lab`
2. `get_server_overview`
3. `get_settings`（空 query）再 `query=innodb_buffer`
4. `get_status` query=`Threads`
5. `list_schemas` / `list_tables` / `describe_relation` relation=`chaos_orders`
6. `list_sessions`
7. `get_table_stats` / `get_index_stats`（`idx_orders_note_unused` 的 io_count 应很低）
8. `list_slow_queries`
9. `get_innodb_metrics`
10. `get_replication_status`（单机应无 replica 通道）
11. `query_mysql` `SELECT COUNT(*) FROM chaos_orders` → 800
12. `query_mysql` `SELECT GET_LOCK('x',1)` 应被拒绝
13. `explain_query` 对 `SELECT * FROM chaos_orders WHERE id=1`

可选锁等待：另开一个终端跑 `scripts/make-blocking.ps1`，再 `get_blocking_tree`。

## 清掉实验室

```powershell
docker compose down -v
```
