# 主机日志：两份文件完成配置

账号密码或内嵌私钥方式都只需手动配置两份文件。MCP 配置负责启动程序；主机清单集中填写地址、用户名、密码或私钥和日志目录。**不需要单独的私钥文件，也不需要安装 OpenSSH 客户端。**

## 1. Codex 配置

### 在 Codex 软件界面中添加（Host-logs 示例）

在 Codex 的设置中打开 MCP 服务器配置，添加本地 stdio 服务；如果已经存在 Host-logs，直接打开它的更新页面。下面按当前软件界面的字段填写：

| 界面字段 | 填写内容 |
| --- | --- |
| 名称（新增时） | `host-logs` |
| 启动命令 | `D:/project/CICD/cicd/mcp/host-logs-mcp-server/host-logs-mcp-server.exe` |
| 参数 | 不添加参数；已有空白参数行可点右侧垃圾桶删除。不要填写 `[]`、`stdio` 或引号 |
| 环境变量第 1 行：名称 | `HOST_LOGS_TARGETS_FILE` |
| 环境变量第 1 行：值 | `D:/project/CICD/.codex/host-logs-targets.json` |
| 环境变量第 2 行：名称 | `HOST_LOGS_READ_ONLY` |
| 环境变量第 2 行：值 | `true` |

点“添加环境变量”逐行填写，左边是变量名，右边是值。界面输入框直接填写表格内容，不带反引号或额外的双引号，也不用填写 `KEY=value`。

保存（或更新）后重启该 MCP 连接；必要时重新打开 Codex 或新建会话。先调用 `list_targets` 确认清单加载，再调用 `list_log_files` 验证实际 SSH 连接。仅在设置中看见服务，并不代表已经连上主机。

主机地址、用户名、`password` 或 `private_key` 仍填写在 `D:/project/CICD/.codex/host-logs-targets.json` 中，不填到“参数”栏。密码和密钥登录使用相同的界面配置；新增主机或环境，只需在该文件的 `targets` 数组中增加记录。

通过软件界面添加与手动编辑 `config.toml` 是替代方式，选一种即可，不必再手动重复添加同一服务。当前截图中的“更新 Host-logs MCP”页面可以直接按上表修改，无需卸载重建；只有确实要切换 MCP 服务器类型时，才按界面提示处理。

### 手动编辑 config.toml（另一种方式）

在 `C:/Users/15509/.codex/config.toml` 合并下面段落，已有同名段落就修改，不要重复添加：

```toml
[mcp_servers.host-logs]
command = "D:/project/CICD/cicd/mcp/host-logs-mcp-server/host-logs-mcp-server.exe"
args = []
startup_timeout_sec = 20
tool_timeout_sec = 60

[mcp_servers.host-logs.env]
HOST_LOGS_TARGETS_FILE = "D:/project/CICD/.codex/host-logs-targets.json"
HOST_LOGS_READ_ONLY = "true"
```

Cursor / WorkBuddy 使用 `mcp.json` 的 `command`、`args` 和 `env`，参数相同，见 [客户端样例](../../examples/mcp.json.example)。五服务和多环境的 Codex 配置见 [完整指南](../../CODEX.md)。

## 2. 主机清单

创建 `D:/project/CICD/.codex/host-logs-targets.json`，只填这五项：

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

### 用户名 + 密钥（同样只配置两个文件）

把主机记录中的 `password` 换成 `private_key` 即可。这里填**私钥正文**，不是 `.pub` 公钥，也不是文件路径；支持 OpenSSH / PEM 私钥。下面仅为占位，必须替换为有效私钥：

```json
{
  "targets": [
    {
      "name": "orders-key-prod",
      "host": "db.example.com",
      "user": "mcp_logs",
      "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n<private-key-body>\n-----END OPENSSH PRIVATE KEY-----\n",
      "paths": [
        "/var/log/nginx"
      ]
    }
  ]
}
```

JSON 的 `\n` 表示私钥中的换行。私钥有加密口令时，在同一条记录增加 `"private_key_passphrase": "<key-passphrase>"`；未加密的私钥省略该字段。没有加密私钥时不要把 SSH 登录密码填到这里。

为了避免手工转义，可在本机 PowerShell 里把已有私钥文件导入已有的主机记录；修改 `orders-key-prod` 为自己的目标名。该命令不输出私钥：

```powershell
$targetsPath = "D:/project/CICD/.codex/host-logs-targets.json"
$targetsConfig = Get-Content -LiteralPath $targetsPath -Raw -Encoding UTF8 | ConvertFrom-Json
$entry = @($targetsConfig.targets | Where-Object { $_.name -eq "orders-key-prod" })
if ($entry.Count -ne 1) { throw "需要恰好一条对应名称的主机记录" }
$keyPath = Read-Host "现有私钥文件的完整路径"
$keyText = Get-Content -LiteralPath $keyPath -Raw -Encoding UTF8
$entry[0] | Add-Member -NotePropertyName private_key -NotePropertyValue $keyText -Force
$entry[0].PSObject.Properties.Remove("identity_file")
[IO.File]::WriteAllText($targetsPath, ($targetsConfig | ConvertTo-Json -Depth 10), (New-Object System.Text.UTF8Encoding($false)))
```

导入后 MCP 运行时不再依赖原私钥文件；是否保留原文件用于备份由你管理。已有 `password` 会保留为回退方式；只想用密钥时删掉该字段。真实密码、私钥、私钥口令仅存本机，不进入仓库样例。

### 两种方式共存与优先级

同一份清单可以同时放密码主机和密钥主机；同一条记录也可同时填 `private_key`（或 `identity_file`）与 `password`。程序先尝试密钥，密钥被服务器拒绝后再尝试密码，任一种成功即可。不提供密码时不会尝试空密码。

无效私钥、错误私钥口令或不可读密钥文件属于本机配置错误，会在连接前报错，不会静默绕过。`private_key` 与 `identity_file` 是两种密钥来源，不能同时填写。服务器本身要求 MFA/多因素时仍需满足服务器规则，本客户端不支持交互式验证码。

## 创建用户、设置密码和安装公钥

以下由管理员在**目标 Linux 主机**执行，MCP 本身不创建用户或修改 SSH 服务。已有 `mcp_logs` 用户可以跳过 `useradd`。

```bash
# 创建带 home 和可执行命令 shell 的专用用户
sudo useradd -m -s /bin/sh mcp_logs

# 设置该账号的登录密码；按提示输入，不把密码写在命令行
sudo passwd mcp_logs

# 确认账号没有锁定或过期
sudo passwd -S mcp_logs
sudo chage -l mcp_logs
```

**`passwd mcp_logs` 设置的就是 targets 中的 `password`。** 创建用户本身不会替你设好可用的登录密码。不要再执行 `passwd -l mcp_logs`，否则可能使密码认证失败，部分系统也会拒绝该账号的密钥登录。该账号不要加入 sudo 组；shell 不要设为 `nologin`。

授予目标日志的读取权限，例如服务器支持 POSIX ACL 时：

```bash
# 目录和现有文件只读/可遍历；替换为实际日志目录
sudo setfacl -R -m u:mcp_logs:rX /var/log/nginx
# 新建日志的默认 ACL；轮转策略也应保留读取权限
sudo setfacl -m d:u:mcp_logs:rX /var/log/nginx
sudo -u mcp_logs test -r /var/log/nginx/access.log
```

如果父目录不可遍历，管理员还需对必要父目录授予遍历权限。文件尚未创建时 `test -r` 会失败，使用已存在的日志验证。也可使用现有只读日志组替代 ACL，但不要给账号业务写权限。

### 使用密钥的账号

私钥留在客户端；服务器只安装对应的**公钥**。已有密钥可直接复用。需要新密钥时，在客户端运行 `ssh-keygen -t ed25519`，按提示设置保存位置和可选私钥口令。私钥口令用于解密私钥，不是服务器登录密码。

管理员将公钥追加到目标账号的 `authorized_keys`，保留已经存在的公钥：

```bash
sudo install -d -m 700 -o mcp_logs -g "$(id -gn mcp_logs)" /home/mcp_logs/.ssh
sudo touch /home/mcp_logs/.ssh/authorized_keys
# 编辑并追加一整行公钥，例如 ssh-ed25519 AAAA... comment；不要粘贴私钥
sudoedit /home/mcp_logs/.ssh/authorized_keys
sudo chown mcp_logs:"$(id -gn mcp_logs)" /home/mcp_logs/.ssh/authorized_keys
sudo chmod 600 /home/mcp_logs/.ssh/authorized_keys
```

上述 home 路径对应前面的 `useradd -m`；已有账号用 `getent passwd mcp_logs` 核对真实 home。采用集中公钥管理时按现场策略安装，不必另造一个 `authorized_keys`。

### 服务器允许密码或密钥任一种成功

需要两种认证共存的账号，管理员应核对最终生效的 SSH 配置。相关配置示意如下，不能直接覆盖整份 `sshd_config`：

```text
Match User mcp_logs
    PubkeyAuthentication yes
    PasswordAuthentication yes
    AuthenticationMethods any
Match all
```

`AuthenticationMethods any` 表示任一已启用方式成功即可；`publickey,password` 则要求两种连续成功，不是二选一。仅允许密钥的账号可保持 `PasswordAuthentication no`，MCP 只填 `private_key` 仍可连接。参见 [OpenSSH 官方认证配置](https://man.openbsd.org/sshd_config#AuthenticationMethods)。

修改 SSH 配置后先执行 `sudo sshd -t` 校验；由管理员按系统实际服务名重载 `ssh` 或 `sshd`。`AllowUsers`、`AllowGroups`、`Match`、账号过期和 PAM 策略也会影响登录，不能只检查一个开关。

## 3. 开始查询

保存后重启客户端中的 host-logs MCP。先 `list_targets` 确认名称，再 `list_log_files` 查看允许目录下的文件，最后 `search_log` / `tail_log`。例如：“查主机日志 `orders-prod` 最近 15 分钟的 timeout，先列出日志文件”。时间参数按日志行前缀的字典序过滤，并非时间戳解析。

修改主机清单（包括密码）后，下一次调用会检查文件修改时间并重新加载；解析或校验失败会报错，不会用新旧信息混连。修改 Codex 配置或更换二进制后需要重启对应 MCP。

## 4. 默认行为与可选项

| 字段 | 说明 |
| --- | --- |
| `name` | 必填，唯一目标名称 |
| `host` | 必填，真实主机名或 IP；密码/内嵌私钥方式直连，不解析 `~/.ssh/config` 的别名、ProxyJump 或代理规则 |
| `user` | 必填，具有日志读取权限的 SSH 用户 |
| `password` | 账号密码方式必填，远端必须允许 SSH password 认证；不支持验证码或交互式 MFA |
| `paths` | 必填，允许读取的具体日志目录；不能是 `/`、`/data`、`/var/log` 等过宽目录 |
| `port` | 可选，默认 22 |
| `host_key_sha256` | 可选，内置 SSH 认证可固定管理员提供的 `SHA256:...` 主机指纹；不需要额外文件 |
| `private_key` | 可选，OpenSSH / PEM 私钥正文，与 password 至少提供一种（也兼容旧密钥文件方式） |
| `private_key_passphrase` | 可选，加密私钥的解密口令；不是账号登录密码 |
| `identity_file` | 可选，已有私钥文件路径；与 private_key 二选一，可与 password 共存 |
| `aliases` / `description` / `environment` / `tags` | 可选，仅帮助检索和展示；最小配置不用填 |
| `transport` | 默认 `ssh`；`local` 仅用于读取 MCP 本机夹具，不允许填写密码或私钥 |

内置 SSH 认证首次连接会自动将未知主机指纹写入本机 `~/.ssh/known_hosts`，之后拒绝密钥变化。这个文件由程序维护，不需要你额外填写配置。已有指纹仍会校验，未知主机证书不会自动信任。需要事先固定主机身份时，在同一条主机记录里填 `host_key_sha256`；使用指纹固定时不依赖 known_hosts 文件。

| 环境变量 | 默认与用途 |
| --- | --- |
| `HOST_LOGS_TIMEOUT` | `20s`；连接、认证和查询总超时 |
| `HOST_LOGS_STRICT_HOST_KEY` | 未填时密码/内嵌私钥方式为 `accept-new`，仅密钥文件的 OpenSSH 方式仍为 `yes`；显式 `yes` 要求预先信任主机，`accept-new` 允许首次记录，其他值按 `yes` |
| `HOST_LOGS_KNOWN_HOSTS` | 可选，自定义 known_hosts 路径；内置 SSH 默认使用用户目录下的 `.ssh/known_hosts` |
| `HOST_LOGS_READ_ONLY` | 必须为 `true`；实际权限仍由 SSH 账号和目录 allowlist 限制 |

`list_targets` / `get_target_info` 只返回认证方式 `auth`，不返回密码、私钥或私钥口令；这些凭据不传进进程命令行，错误和远端输出会脱敏。凭据保存在本机配置文件中，并非加密凭据库。

## 5. 兼容原来的密钥连接

已有仅密钥文件配置可以保持原样：省略 `password`，使用 `identity_file` 或本机 `~/.ssh/config`。未设置私钥口令或指纹固定时，该模式继续调用 OpenSSH，要求本机有 `ssh`，使用 `BatchMode=yes`。`identity_file` 与 password、private_key_passphrase 或 host_key_sha256 配合使用时改走内置 SSH，直接连接配置的 host，不解析 ssh_config 的跳板机规则。

## 6. 远端要求和查询范围

远端账号需要能读取配置的日志目录，并有可执行命令的 login shell（可以是 `/bin/sh`，不能是 `nologin`）。远端需安装 Bash、支持相关选项的 GNU find / grep / awk / tail / cat / test / readlink / gzip。密码方式不要求在服务器安装 MCP。

程序只提供 list / search / tail 固定模板，无通用 exec、上传、删除或改配置工具。路径先通过 allowlist 和真实路径校验；管道用 `bash -o pipefail -c` 保留读取、解压和过滤错误。grep 无匹配返回空结果，非法正则或文件读取失败会报错。输出默认 80 行、最高 200 行，约 256KiB 字节上限；限流持续消费管道，避免 SIGPIPE 假错误。

## 7. 常见问题

| 现象 | 处理 |
| --- | --- |
| `handshake/authentication` | 检查账号、所选凭据，以及服务器是否允许对应 password / publickey 认证 |
| `choose private_key or identity_file` | 私钥正文和私钥文件路径二选一；两者任意一种均可与 password 共存 |
| `invalid private key` / `private_key_passphrase` | 检查是否填写完整私钥正文、换行及正确的私钥解密口令 |
| `host key verification failed` / `fingerprint mismatch` | 主机身份与记录不符，先核实是否换机或重装，不要直接删除记录绕过校验 |
| `host key is unknown` | 当前显式使用严格模式；确认指纹后配置 `host_key_sha256` 或 known_hosts |
| `host key trust file` | 本机信任缓存不可读写，检查权限或自定义路径 |
| `allowlist path is too broad` | `paths` 写到具体目录，例如 `/var/log/nginx` |
| `outside this target allowlist` | 先列文件，使用允许目录内的路径 |
| `looks like a secret file` | 不能读取 `.env`、密钥等敏感文件 |
| 超时 | 检查网络、端口和服务响应；`HOST_LOGS_TIMEOUT` 默认 20 秒 |

本地握手与夹具验收用 `scripts/smoke-stdio.ps1`；它不验证真实主机的网络和密码。
