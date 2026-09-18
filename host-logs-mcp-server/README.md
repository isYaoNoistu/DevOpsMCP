# host-logs-mcp-server

本机 stdio MCP，读取允许目录内的主机日志。只提供列文件、检索、查看尾部等五个只读工具，没有通用 shell / exec、上传、删除或修改文件功能。

## 最简配置：两个文件

1. `C:/Users/15509/.codex/config.toml`：填写本目录 `.exe` 路径和 `HOST_LOGS_TARGETS_FILE`。
2. `D:/project/CICD/.codex/host-logs-targets.json`：集中填写所有主机的地址、用户名、password 或 private_key 和允许读取的日志目录。

不需要单独的密钥文件，密码和内嵌私钥方式不依赖本机 OpenSSH。完整可复制配置见 [两文件配置指南](docs/configuration.md)。现有密钥连接继续兼容。

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

## 密码与密钥都支持

- 账号密码：填 `user` + `password`。
- 账号密钥：填 `user` + `private_key`；加密私钥再填 `private_key_passphrase`。见 [密钥样例](examples/host-logs-targets.key.example.json)。
- 两种同时填写：先密钥、后密码，任一种认证成功即可。已有 `identity_file` 文件方式继续兼容。

服务器创建用户后还要用 **`sudo passwd mcp_logs`** 设置登录密码；密钥账号需要把对应公钥装到 authorized_keys。创建用户、权限、公钥安装和 SSH 二选一认证配置见 [配置指南](docs/configuration.md)。

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

也可以直接通过 Codex 软件的 MCP 设置添加：按 [界面填写步骤](docs/configuration.md) 填写启动命令、空参数和两行环境变量；与下面手动编辑 TOML 的方式二选一即可。

在用户级 `C:/Users/15509/.codex/config.toml`合并下面配置，替换程序和配置文件的绝对路径，保留原有设置；不要重复定义同名表。

```toml
[mcp_servers.host-logs]
command = "D:/project/CICD/cicd/mcp/host-logs-mcp-server/host-logs-mcp-server.exe"
args = []
enabled = true
startup_timeout_sec = 20
tool_timeout_sec = 60

[mcp_servers.host-logs.env]
HOST_LOGS_TARGETS_FILE = "D:/project/CICD/.codex/host-logs-targets.json"
HOST_LOGS_READ_ONLY = "true"
```

一份 targets 清单可配置 UAT / PROD 多台主机，每条分别设置 `name`、SSH 用户、密码或私钥和目录 `paths`。先 `list_targets` 再列文件，以明确的 `target` 和 allowlist 内路径检索；需要隔离时拆清单并注册两个实例。

可复制 [UAT / PROD targets 样例](examples/host-logs-targets.multi-env.example.json) 到本机后修改，并让上述 targets 环境变量指向它。主机密码或私钥直接写在这份本机清单；真实凭据不提交仓库。

保存后重启对应 MCP 连接。CLI 可用 `codex mcp list` 检查配置、在会话中用 `/mcp` 核对连接；握手成功后再做小范围远端只读查询。

添加步骤、字段解释、凭据、环境切换和排障见 [Codex 完整指南](../CODEX.md)；可复制 [五服务 TOML](../examples/codex.toml.example) 或 [多环境 TOML](../examples/codex.multi-env.toml.example)。

## 对接 YluneMCPHub：平台凭据模式（不需要凭据文件）

平台模式通过月弦凭据中心注入环境变量，连接参数和凭据直接在内存解析，不需要 targets 文件、密码文件或密钥文件。原有文件模式继续供智能体本地直连使用。

1. 将 Linux 二进制放入月弦可执行的位置。默认 Docker 挂载 `/data/ylune-mcp` → `/opt/mcp`，这里只需放程序，不用放凭据文件。新建 STDIO 服务器，命令 `/opt/mcp/host-logs-mcp-server`，参数留空。平台原生部署则填实际二进制绝对路径。
2. 在「凭据中心」新建一条凭据，填写下面两个键，绑定该服务器：

| 凭据键 | 填写内容 |
| --- | --- |
| `HOST_LOGS_TARGETS_JSON` | 下方完整 JSON 文本，不是文件路径，也不用加外层引号 |
| `HOST_LOGS_READ_ONLY` | `true` |

```json
{
  "targets": [
    {
      "name": "uat",
      "host": "host.example.com",
      "port": 22,
      "user": "mcp_logs",
      "password": "<ssh-password>",
      "host_key_sha256": "SHA256:<verified-host-key-fingerprint>",
      "paths": [
        "/var/log/nginx"
      ]
    }
  ]
}
```

JSON 中的占位内容在凭据中心替换为真实值；不提交到仓库。月弦负责加密存储和运行时注入，需要正确配置 `YLUNE_MASTER_KEY`。默认的 HOST / PORT / TOKEN 字段不会自动映射，请使用表中精确变量名。

密钥认证时去掉 `password`，改填 `private_key`（完整 PEM/OpenSSH 私钥文本，JSON 内换行写 `\n`）；加密私钥再填 `private_key_passphrase`。密码和私钥可同时配置，保持原有双兼容行为。平台 SSH 模式不接受 `identity_file`，必须填 `host_key_sha256`，以免依赖本机 known_hosts。指纹须从可信渠道核验，例如在目标主机执行 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256` 并使用其 SHA256 指纹。不得直接信任未经核验的网络扫描结果。`paths` 是目标主机的日志白名单。

3. 在该凭据的绑定处测试工具列表，再用调试台调用 `list_targets` 和一次实际只读查询，分别验证配置及上游认证。仅列出目标/工具不能证明已连接上游。
4. 在月弦用户授权中勾选该 MCP、允许的工具和绑定凭据。客户端使用月弦 Access Key，上游密码不交给智能体。

### 与本地文件模式的兼容

- 未设置 `HOST_LOGS_TARGETS_JSON` 时，沿用 `HOST_LOGS_TARGETS_FILE` 和原有文件配置。
- 只要设置了 `HOST_LOGS_TARGETS_JSON`（即使为空），就优先采用平台模式；空值、格式错误、缺少必要字段均报错，不回退到文件。平台服务器无需再声明 `HOST_LOGS_TARGETS_FILE`。
- 配置是每个 MCP 进程启动时的快照。修改/轮换凭据后，在月弦重新连接或重启对应上游进程，使新环境变量生效；不会靠修改文件热更新平台凭据。
- 建议每个环境一台服务器、一条凭据。JSON 的 targets 可以有多个目标，但同一实例授权用户可访问该清单内的目标，不会按凭据名称自动细分权限。
- 平台 JSON 最多 1 MiB、最多 100 个目标；实际还受操作系统环境变量大小限制，大型清单应拆分为独立实例。敏感字段不会进入目标列表和配置错误文本，也不会由 MCP 写入临时凭据文件。
