# 在 Codex 中使用五个运维 MCP

适用于夜莺、Jenkins、PostgreSQL、MySQL 和主机日志。五个程序都是本机 stdio 服务：Codex 启动二进制，通过标准输入输出调用工具，不需要为程序填写 HTTP MCP 地址。


本机路径说明：五个 `.exe` 已位于 `D:/project/CICD/cicd/mcp/` 对应服务目录；现有 PostgreSQL 清单为 `C:/Users/15509/.cursor/postgres-targets.json`。MySQL 清单示例放在同一 `.cursor` 目录；主机日志使用当前界面配置的 `D:/project/CICD/.codex/host-logs-targets.json`。使用前核对各清单是否存在并填写真实目标。多环境拆分的清单、SSH 示例密钥也尚不存在，不能直接照抄后就查询。Jenkins 默认缓存位于 `C:/Users/15509/AppData/Local/jenkins-mcp`，多环境使用带环境后缀的独立目录。

## 1. 准备与添加

1. 准备对应系统的二进制。Windows 使用 `.exe`；Linux / macOS 使用对应平台产物，不能直接使用 Windows 包。
2. 编辑用户级 `C:/Users/15509/.codex/config.toml`。保留原配置，只合并需要的 `[mcp_servers.<名称>]` 段，不能重复定义同名表。
3. 参考 [单环境完整样例](examples/codex.toml.example)，程序路径已按本机目录填写；核对 targets 文件是否存在，再填写服务地址及凭据。Windows TOML 路径推荐使用 `/`，例如 `D:/project/CICD/cicd/mcp/mysql-mcp-server/mysql-mcp-server.exe`。不要把 Cursor 的 `mcpServers` JSON 直接粘进 TOML。
4. 保存后在客户端的 MCP 设置中重启对应连接；必要时重新打开客户端或新建会话。已有对话不一定立即获得新工具。

也可以使用可信项目内的 `.codex/config.toml`，但不要把真实 Token 写进项目配置并提交。仅有 Cursor 的 `~/.cursor/mcp.json` 不会自动完成 Codex 配置。

安装了 Codex CLI 时，可以添加一个不含密码的数据库实例：

```powershell
codex mcp add mysql --env MYSQL_TARGETS_FILE=C:/Users/15509/.cursor/mysql-targets.json --env MYSQL_MCP_READ_ONLY=true -- D:/project/CICD/cicd/mcp/mysql-mcp-server/mysql-mcp-server.exe
codex mcp list
```

CLI 和手动编辑是两种替代方式，不要对同名服务重复添加。没有 CLI 时直接编辑 TOML。CLI 会话中用 `/mcp` 查看连接状态；桌面端或 IDE 扩展可在 MCP 设置中查看状态。这里只读程序使用 Token、数据库凭据或 SSH 密码/密钥，不需要执行 OAuth `mcp login`。

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

其他四个 MCP 也可按相同方式在界面添加：`command` 对应“启动命令”，`args` 每个元素对应一条“参数”，`env` 的每个键值对应一行“环境变量”。夜莺需要一条 `stdio` 参数，Jenkins、PostgreSQL、MySQL 和 Host-logs 不需要参数；具体变量见 [五服务样例](examples/codex.toml.example)。

## 2. 配置字段和凭据

| 字段 | 含义 |
| --- | --- |
| `[mcp_servers.mysql]` | Codex 内唯一实例名；多实例使用不同名称 |
| `command` | 程序绝对路径，不要把参数拼进路径 |
| `args` | 夜莺为 `["stdio"]`；其余四个为 `[]` |
| `enabled` | `false` 暂时关闭此实例；开启需要改回 `true` 并重载 |
| `startup_timeout_sec` | 启动握手超时，单位秒 |
| `tool_timeout_sec` | 单次工具调用超时，单位秒；Jenkins 示例设为 120，以覆盖服务内 90 秒超时 |
| `[mcp_servers.<名称>.env]` | 该子进程的环境变量，值写为字符串 |
| `env_vars` | 透传启动 Codex 的进程已有的环境变量；它不负责读取 `.env` 或重命名变量 |

夜莺需要 `N9E_BASE_URL`、`N9E_TOKEN`，以及示例中的只读开关与 toolsets。Jenkins 需要 `JENKINS_URL`、`JENKINS_USER`、`JENKINS_API_TOKEN`。单环境时，可从 TOML 的 `.env` 删除 Token 行，在对应服务器表内加入 `env_vars = ["N9E_TOKEN"]` 或 `env_vars = ["JENKINS_API_TOKEN"]`，再由本机凭据注入机制提供变量。已运行的 GUI 不会自动获得后来在另一个终端设置的变量。

TOML 中的 `"${N9E_TOKEN}"` 是字符串，不是本项目提供的变量展开功能。多个环境需要不同 Token 时，按下文给每个实例单独设置 `.env`；不要把不同名称的变量直接传入程序，程序只认规定的变量名。真实 Token 只保存在本机私有配置或凭据机制中，不写到仓库样例，也不要放入命令行历史。

PostgreSQL / MySQL 的 TOML 只指向 targets 文件。数据库密码通过 Windows 凭据管理器或 pgpass / mysqlpass 提供；targets 禁止 `password`。`credential_ref` 是凭据的查找名称，不是密码。主机日志可直接在本机 targets 中填写 `password` 或 `private_key`，只需 Codex 配置加主机清单两份文件；也兼容已有的 SSH 私钥方式。

## 3. 多环境：先区分两种配置方式

| MCP | 多环境方式 | 每次查询如何选择 |
| --- | --- | --- |
| 夜莺 | 每个独立夜莺地址注册一个实例，例如 `nightingale-uat` / `nightingale-prod` | 指定实例，再发现该实例的数据源、索引；不同实例的 ID 不能混用 |
| Jenkins | 每个独立 Jenkins 地址注册一个实例，例如 `jenkins-uat` / `jenkins-prod` | 指定实例，再发现 Folder / Job；同一个 Jenkins 内可通过 Job 路径区分环境 |
| PostgreSQL | 一个实例指向一份含多个数据库的 targets 清单 | `list_targets` 后使用明确的 `target` |
| MySQL | 一个实例指向一份含多个数据库的 targets 清单 | `list_targets` 后使用明确的 `target` |
| 主机日志 | 一个实例指向一份含多台主机和目录 allowlist 的清单 | 明确 `target`，再发现允许读取的文件 |

同一个夜莺已经汇集多个环境时，可以继续用一个 MCP，按数据源、索引和标签区分；不需要为了标签不同而重复启动服务。夜莺和 Jenkins 当前没有数据库式的 targets 路由，不能只加一个 `environment=prod` 就切换后端地址。

### 夜莺与 Jenkins：独立实例

[多环境 TOML 样例](examples/codex.multi-env.toml.example) 提供 UAT / PROD 两组夜莺和 Jenkins，以及三种 targets 服务。它是单环境样例的替代方案，按需合并，避免把同一环境重复接入。生产实例示例默认 `enabled = false`；准备好地址和只读凭据后再按需开启。

Jenkins 各环境必须使用不同的 `JENKINS_MCP_CACHE_DIR`。控制台缓存不能共用一个默认目录，以免同名 Job、相同构建号的不同环境混在一起。服务实例名不会自动改缓存目录。

给 Codex 的查询可以写成：“使用 `jenkins-uat` 查看 `team/uat/checkout-api` 最近失败构建”或“使用 `nightingale-prod`，先列出数据源，再查订单服务最近 15 分钟 timeout”。先确认 `/mcp` 中实例已启用；具体工具显示名称由客户端决定。

### 数据库与主机日志：targets 清单

可复制下面的 UAT / PROD 清单到本机，再按实际情况替换地址、数据库、只读账号、主机密码和允许目录：

- [PostgreSQL 多环境 targets](postgres-mcp-server/examples/postgres-targets.multi-env.example.json)
- [MySQL 多环境 targets](mysql-mcp-server/examples/mysql-targets.multi-env.example.json)
- [主机日志多环境 targets](host-logs-mcp-server/examples/host-logs-targets.multi-env.example.json)

将 TOML 的 `PG_TARGETS_FILE`、`MYSQL_TARGETS_FILE`、`HOST_LOGS_TARGETS_FILE` 分别指向复制后的本机文件。清单根结构必须是 `{"targets": [...]}`，每条记录使用唯一、明确的 `name`，例如 `orders-uat` / `orders-prod`。`environment`、`tags` 和 `aliases` 帮助识别目标，不代替连接地址或权限隔离；避免两个环境共用模糊别名“订单库”。

数据库按环境分别配置 `credential_ref`，例如 `mysql/orders-uat` 与 `mysql/orders-prod`，并为每个引用写入对应的只读账号凭据。生产配置使用 `verify-full`，需要证书信任和主机名校验正确；不要通过降低生产 TLS 校验来解决连接失败。

主机日志为每个环境填写 `host`、`user`、`password` 或 `private_key` 以及具体日志目录 `paths`，端口默认 22。密码/内嵌私钥方式无需额外私钥文件或本机 OpenSSH；首次连接自动记录未知主机指纹，后续拒绝变化，也可通过同一条记录中的 `host_key_sha256` 固定指纹。远端需要 Bash 和文档列出的 GNU 工具。完整两文件示例见 [主机日志配置](host-logs-mcp-server/docs/configuration.md)。

查询示例：“用 MySQL 的 `list_targets` 确认 `orders-uat`，再查该 target 的长事务”；“使用主机日志 target `orders-prod` 列文件，只检索最近 15 分钟的 timeout”。主机日志路径必须来自该 target 的 allowlist。

### 必须隔离环境时

如果不希望一个 MCP 能看到全部环境，将 targets 拆成两份，再注册两个实例。以下是 PostgreSQL 示例，MySQL / 主机日志同理替换程序和变量名：

```toml
[mcp_servers.postgres-uat]
command = "D:/project/CICD/cicd/mcp/postgres-mcp-server/postgres-mcp-server.exe"
args = []
enabled = true
[mcp_servers.postgres-uat.env]
PG_TARGETS_FILE = "C:/Users/15509/.cursor/postgres-uat.json"
PG_MCP_READ_ONLY = "true"

[mcp_servers.postgres-prod]
command = "D:/project/CICD/cicd/mcp/postgres-mcp-server/postgres-mcp-server.exe"
args = []
enabled = false
[mcp_servers.postgres-prod.env]
PG_TARGETS_FILE = "C:/Users/15509/.cursor/postgres-prod.json"
PG_MCP_READ_ONLY = "true"
```

实例拆分和 `enabled` 是客户端管理方式；真正的访问边界仍是数据库账号、SSH 账号、凭据和 allowlist。不要把仅标了 `environment` 的混合清单视为权限隔离。

## 4. 验收、更新和排障

1. `codex mcp list` 或 MCP 设置中确认服务配置存在，再在会话中确认连接和工具清单。仅看见配置不代表已完成握手。
2. 每个服务的 `scripts/smoke-stdio.ps1` 可验证交付程序的本地握手；这不证明远端地址和凭据可用。
3. 数据库和主机日志先 `list_targets`，核对目标后再做一次小范围只读查询。夜莺先 `list_datasources`；Jenkins 先小范围 `list_jobs`。一次成功握手不代表已经连通这些后端。
4. 修改 TOML、URL、Token、程序路径或启动参数后重启该 MCP 连接。仅修改已有路径下的 targets 内容，当前实现会检查文件修改时间并重载；用 `list_targets` 确认新内容，解析失败时按错误提示修复。
5. “启动失败”先查路径、平台、夜莺 `stdio` 参数、必填环境变量和 targets JSON；“查询失败”再区分授权、网络、证书、SSH 指纹和目录权限。无匹配日志不等于服务健康。

Skills 与 MCP 连接分别配置。备份 Skill 文件不会自动注册服务；服务连接成功也不意味着客户端已安装对应 Skill。源码仓库技能在 `.cursor/skills`，交付仓库备份在 `mcp/skills`；安装时复制完整子目录及 references，并核对实际二进制的工具清单。

Codex 配置语法依据：[OpenAI 官方 MCP 文档](https://developers.openai.com/codex/mcp/)。服务环境变量和多目标行为以本仓库实现为准。
