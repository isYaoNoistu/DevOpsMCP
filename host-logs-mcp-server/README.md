# host-logs-mcp-server

本机 stdio MCP，读取允许目录内的主机日志。只提供列文件、检索、查看尾部等五个只读工具，没有通用 shell / exec、上传、删除或修改文件功能。

## 最简配置：两个文件

1. `C:/Users/15509/.codex/config.toml`：填写本目录 `.exe` 路径和 `HOST_LOGS_TARGETS_FILE`。
2. `C:/Users/15509/.cursor/host-logs-targets.json`：集中填写所有主机的地址、用户名、密码和允许读取的日志目录。

不需要单独的密钥文件，密码方式不依赖本机 OpenSSH。完整可复制配置见 [两文件配置指南](docs/configuration.md)。现有密钥连接继续兼容。

```json
{
  "targets": [
    {
      "name": "orders-prod",
      "host": "db.example.com",
      "user": "mcp_logs",
      "password": "<ssh-password>",
      "paths": [
        "/var/log/nginx"
      ]
    }
  ]
}
```

端口默认 22，别名、标签、环境描述都可不填。多个环境在 `targets` 数组中增加记录即可，见 [多环境样例](examples/host-logs-targets.multi-env.example.json)。真实密码仅保存在本机私有配置，仓库中的密码是占位符。

首次连接自动记录未知主机指纹，之后拒绝指纹变化。缓存由程序维护，无需手动配置第三个文件；也可在同一条主机配置中指定 `host_key_sha256` 固定主机身份。账号必须具备日志读取权限，远端需要 Bash 及 GNU 工具，详情见配置指南。

## 工具

| 工具 | 用途 |
| --- | --- |
| `list_targets` | 发现本机清单中的主机；不会返回密码 |
| `get_target_info` | 确认主机、认证方式和允许目录 |
| `list_log_files` | 列出 allowlist 内文件，最大深度 4 |
| `search_log` | 检索单个文件，可选行前缀时间窗和前后文 |
| `tail_log` | 查看单个文件末尾 |

先 `list_targets` 确认 `target`，再列文件并检索。远端正则是 `grep -E`，本地为 Go regexp；`fixed=true` 使用字面量。`.gz` 先解压。`start` / `end` 为整行字典序过滤，不解析时间戳。输出默认最多 80 行、最高 200 行，字节上限约 256KiB。`truncated=true` 表示结果受限，空结果只代表本次查询没有匹配。

`paths` 必须指向具体日志目录，如 `/var/log/nginx`，不允许 `/` 或 `/data`。程序会检查真实路径和敏感文件后缀；JSON 错误或目标有歧义时会报错。

## 更新与验收

主机清单修改后自动按文件修改时间重载；修改 MCP 启动配置或二进制后重启对应连接。

```powershell
.\scripts\smoke-stdio.ps1
```

该脚本验证本地 MCP 握手、五个工具及日志夹具，不连接真实 SSH 主机。密码认证另有本地回环 SSH 自动测试，真实环境仍需使用目标账号验证。

## 构建

源码构建需要 Go 1.26+（用于当前 SSH 依赖）；使用已打包 `.exe` 不需要安装 Go。

```powershell
go test ./...
go build -o host-logs-mcp-server.exe .
```

查询约定见 [主机日志 Skill](../.cursor/skills/host-logs/SKILL.md)。

## Codex 接入与多环境配置

在用户级 `C:/Users/15509/.codex/config.toml`合并下面配置，替换程序和配置文件的绝对路径，保留原有设置；不要重复定义同名表。

```toml
[mcp_servers.host-logs]
command = "D:/project/CICD/cicd/mcp/host-logs-mcp-server/host-logs-mcp-server.exe"
args = []
enabled = true
startup_timeout_sec = 20
tool_timeout_sec = 60

[mcp_servers.host-logs.env]
HOST_LOGS_TARGETS_FILE = "C:/Users/15509/.cursor/host-logs-targets.json"
HOST_LOGS_READ_ONLY = "true"
```

一份 targets 清单可配置 UAT / PROD 多台主机，每条分别设置 `name`、SSH 用户、密码和目录 `paths`。先 `list_targets` 再列文件，以明确的 `target` 和 allowlist 内路径检索；需要隔离时拆清单并注册两个实例。

可复制 [UAT / PROD targets 样例](examples/host-logs-targets.multi-env.example.json) 到本机后修改，并让上述 targets 环境变量指向它。主机密码直接写在这份本机清单；真实密码不提交仓库。

保存后重启对应 MCP 连接。CLI 可用 `codex mcp list` 检查配置、在会话中用 `/mcp` 核对连接；握手成功后再做小范围远端只读查询。

添加步骤、字段解释、凭据、环境切换和排障见 [Codex 完整指南](../CODEX.md)；可复制 [五服务 TOML](../examples/codex.toml.example) 或 [多环境 TOML](../examples/codex.multi-env.toml.example)。
