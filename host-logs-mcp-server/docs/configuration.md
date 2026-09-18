# 主机日志：两份文件完成配置

账号密码方式只需手动配置两份文件。MCP 配置负责启动程序；主机清单集中填写地址、用户名、密码和日志目录。**不需要单独的私钥文件，也不需要安装 OpenSSH 客户端。**

## 1. Codex 配置

在 `C:/Users/15509/.codex/config.toml` 合并下面段落，已有同名段落就修改，不要重复添加：

```toml
[mcp_servers.host-logs]
command = "D:/project/CICD/cicd/mcp/host-logs-mcp-server/host-logs-mcp-server.exe"
args = []
startup_timeout_sec = 20
tool_timeout_sec = 60

[mcp_servers.host-logs.env]
HOST_LOGS_TARGETS_FILE = "C:/Users/15509/.cursor/host-logs-targets.json"
HOST_LOGS_READ_ONLY = "true"
```

Cursor / WorkBuddy 使用 `mcp.json` 的 `command`、`args` 和 `env`，参数相同，见 [客户端样例](../../examples/mcp.json.example)。五服务和多环境的 Codex 配置见 [完整指南](../../CODEX.md)。

## 2. 主机清单

创建 `C:/Users/15509/.cursor/host-logs-targets.json`，只填这五项：

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

把示例地址、账号、密码和目录换成自己的配置。`name` 可以写容易辨认的名称（如“生产环境”），同一份清单内不能重复。密码原样使用，包括空格；JSON 中的反斜杠和双引号要按 JSON 规则转义。真实密码只写在这份本机私有文件中，不提交仓库，不发到对话。

端口默认 `22`，只有非默认端口才加 `"port": 2222`。增加环境就向 `targets` 数组再加一条记录，参考 [UAT / PROD 样例](../examples/host-logs-targets.multi-env.example.json)。不需要增加 MCP 实例，也不用每台主机配置一套环境变量。

## 3. 开始查询

保存后重启客户端中的 host-logs MCP。先 `list_targets` 确认名称，再 `list_log_files` 查看允许目录下的文件，最后 `search_log` / `tail_log`。例如：“查主机日志 `orders-prod` 最近 15 分钟的 timeout，先列出日志文件”。时间参数按日志行前缀的字典序过滤，并非时间戳解析。

修改主机清单（包括密码）后，下一次调用会检查文件修改时间并重新加载；解析或校验失败会报错，不会用新旧信息混连。修改 Codex 配置或更换二进制后需要重启对应 MCP。

## 4. 默认行为与可选项

| 字段 | 说明 |
| --- | --- |
| `name` | 必填，唯一目标名称 |
| `host` | 必填，真实主机名或 IP；密码方式直连，不解析 `~/.ssh/config` 的别名、ProxyJump 或代理规则 |
| `user` | 必填，具有日志读取权限的 SSH 用户 |
| `password` | 账号密码方式必填，远端必须允许 SSH password 认证；不支持验证码或交互式 MFA |
| `paths` | 必填，允许读取的具体日志目录；不能是 `/`、`/data`、`/var/log` 等过宽目录 |
| `port` | 可选，默认 22 |
| `host_key_sha256` | 可选，密码方式可固定管理员提供的 `SHA256:...` 主机指纹；不需要额外文件 |
| `identity_file` | 可选，兼容原有私钥文件方式；与 `password` 二选一 |
| `aliases` / `description` / `environment` / `tags` | 可选，仅帮助检索和展示；最小配置不用填 |
| `transport` | 默认 `ssh`；`local` 仅用于读取 MCP 本机夹具，不允许填写密码 |

密码方式首次连接会自动将未知主机指纹写入本机 `~/.ssh/known_hosts`，之后拒绝密钥变化。这个文件由程序维护，不需要你额外填写配置。已有指纹仍会校验，未知主机证书不会自动信任。需要事先固定主机身份时，在同一条主机记录里填 `host_key_sha256`；使用指纹固定时不依赖 known_hosts 文件。

| 环境变量 | 默认与用途 |
| --- | --- |
| `HOST_LOGS_TIMEOUT` | `20s`；连接、认证和查询总超时 |
| `HOST_LOGS_STRICT_HOST_KEY` | 未填时密码方式为 `accept-new`，旧密钥方式仍为 `yes`；显式 `yes` 要求预先信任主机，`accept-new` 允许首次记录，其他值按 `yes` |
| `HOST_LOGS_KNOWN_HOSTS` | 可选，自定义 known_hosts 路径；密码方式默认使用用户目录下的 `.ssh/known_hosts` |
| `HOST_LOGS_READ_ONLY` | 必须为 `true`；实际权限仍由 SSH 账号和目录 allowlist 限制 |

`list_targets` / `get_target_info` 只返回认证方式 `auth`，不返回密码；密码不会传进进程命令行，错误和远端输出会脱敏。密码保存在本机配置文件中，并非加密凭据库。

## 5. 兼容原来的密钥连接

已有密钥配置可以保持原样：省略 `password`，使用 `identity_file` 或本机 `~/.ssh/config`。该模式仍调用 OpenSSH，要求本机有 `ssh`，使用 `BatchMode=yes`，不会弹出密码框。不要同时填 `password` 和 `identity_file`。

## 6. 远端要求和查询范围

远端账号需要能读取配置的日志目录，并有可执行命令的 login shell（可以是 `/bin/sh`，不能是 `nologin`）。远端需安装 Bash、支持相关选项的 GNU find / grep / awk / tail / cat / test / readlink / gzip。密码方式不要求在服务器安装 MCP。

程序只提供 list / search / tail 固定模板，无通用 exec、上传、删除或改配置工具。路径先通过 allowlist 和真实路径校验；管道用 `bash -o pipefail -c` 保留读取、解压和过滤错误。grep 无匹配返回空结果，非法正则或文件读取失败会报错。输出默认 80 行、最高 200 行，约 256KiB 字节上限；限流持续消费管道，避免 SIGPIPE 假错误。

## 7. 常见问题

| 现象 | 处理 |
| --- | --- |
| `handshake/authentication` | 检查账号密码以及远端是否允许 password 认证；不需要改填私钥 |
| `choose password or identity_file` | 密码和私钥二选一 |
| `host key verification failed` / `fingerprint mismatch` | 主机身份与记录不符，先核实是否换机或重装，不要直接删除记录绕过校验 |
| `host key is unknown` | 当前显式使用严格模式；确认指纹后配置 `host_key_sha256` 或 known_hosts |
| `host key trust file` | 本机信任缓存不可读写，检查权限或自定义路径 |
| `allowlist path is too broad` | `paths` 写到具体目录，例如 `/var/log/nginx` |
| `outside this target allowlist` | 先列文件，使用允许目录内的路径 |
| `looks like a secret file` | 不能读取 `.env`、密钥等敏感文件 |
| 超时 | 检查网络、端口和服务响应；`HOST_LOGS_TIMEOUT` 默认 20 秒 |

本地握手与夹具验收用 `scripts/smoke-stdio.ps1`；它不验证真实主机的网络和密码。
