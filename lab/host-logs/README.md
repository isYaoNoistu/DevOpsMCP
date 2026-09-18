# 主机日志 MCP 本机实验室

给测试同学一套**可丢弃的夹具日志**，用来验收 `host-logs-mcp-server` 的 5 个只读工具、路径 allowlist 和 stdio 握手。不 SSH、不连生产主机。夹具里的 IP / 库名都是文档占位（`203.0.113.10`、`shop_prod`）。

配置原理（targets 与密钥为什么要分开、为什么没有 exec）见 [host-logs-mcp-server/docs/configuration.md](../../host-logs-mcp-server/docs/configuration.md)。

## 需要准备什么

| 条件 | 说明 |
| --- | --- |
| Go 1.23+ | 编译 MCP 二进制 |
| MCP 客户端 | WorkBuddy 或 Cursor（stdio MCP，配好后重载） |
| OpenSSH | 本实验室走 `transport=local`，**不需要** ssh。接真实主机时才要 |

## 目录里有什么

```text
lab/host-logs/
├── README.md                 本文件：流程 + 验收清单
├── fixtures/                 PostgreSQL 风格样例日志（.log.sample）
├── targets.lab.json          文档用占位清单（路径是 /ABS/PATH，不要直接当生产用）
├── mcp.fragment.json         粘进 WorkBuddy / Cursor mcp.json 的 host-logs 段
└── scripts/
    └── install-local-config.ps1 / .sh   写成带本机绝对路径的 lab 清单
```

实验室约定：

| 项 | 值 |
| --- | --- |
| target `name` | `mcp-lab-host-logs` |
| `transport` | `local` |
| 夹具文件 | `fixtures/postgresql-16-main-2026-09-17.log.sample` |
| 示例命中 | `unexpected EOF`、`app=shop-web`、pid `688365` |

MCP **没有** exec / 写文件工具。

## 完整测试流程

### 1. 安装本机清单

```powershell
cd lab\host-logs\scripts
.\install-local-config.ps1
```

会写入 `%USERPROFILE%\.cursor\host-logs-targets.lab.json`，`paths` 指向本仓库 `lab/host-logs/fixtures` 的绝对路径。

### 2. 编译 MCP

```powershell
cd ..\..\..\host-logs-mcp-server
go test ./...
go build -o host-logs-mcp-server.exe .
.\scripts\smoke-stdio.ps1
```

`SMOKE_OK` 表示握手、5 个只读工具、夹具 search/tail、越界路径拒绝都过了。

### 3. 接到客户端

把 [mcp.fragment.json](mcp.fragment.json) 里的 `command` 和 `HOST_LOGS_TARGETS_FILE` 改成上一步打印的绝对路径（Windows 用 `.exe`），粘进 `mcp.json`，重载 **host-logs**。

### 4. 工具验收清单

在客户端对话里让 Agent 做（或你手动调工具）：

| # | 动作 | 期望 |
| --- | --- | --- |
| 1 | `list_targets` query=`lab` | 出现 `mcp-lab-host-logs`，无私钥全文 |
| 2 | `get_target_info` target=`mcp-lab-host-logs` | `transport=local`，paths 含 fixtures |
| 3 | `list_log_files` | 能看到 `postgresql-16-main-2026-09-17.log.sample` |
| 4 | `search_log` pattern=`unexpected EOF` fixed=true，start/end 包住 05:40 | 一行含 `shop-web` 与 pid 688365 |
| 5 | `tail_log` lines=2 | 最后两行，含 checkpoint complete 或 EOF 行 |
| 6 | `search_log` path 指到 fixtures **以外** | 报 outside allowlist |
| 7 | 工具列表 | 只有 5 个工具；没有 exec / write_file |

不要在实验室里把 `paths` 改成 `C:\` 或 `/`。

## 和真实 SSH 的差别

实验室用 `local` 读开发机文件。生产请用 `transport=ssh` + `mcp_logs` 密钥，路径写成目标机 Unix 目录（如 `/data/postgresql/log`），见服务 README。月弦 Docker 里默认**不要**注册本服务：容器通常没有你们的 SSH 私钥和 `ssh` 客户端。
