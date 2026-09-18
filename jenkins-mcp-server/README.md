# jenkins-mcp-server

本机 stdio MCP，用来查 Jenkins 发版失败：Job、构建、Pipeline stage、控制台、队列、节点。进程跑在开发机，直连 Jenkins HTTP API。

源码从 [2001adarsh/jenkins-mcp-go](https://github.com/2001adarsh/jenkins-mcp-go) 裁过：只注册只读排障工具，去掉触发/停止/取消、Groovy、整包控制台路径。控制台默认最后 500 行、最多 2000 行。

## 默认范围

未在对话里明确要求「对 Jenkins 做写操作」时，Agent 不得调用任何写工具。本二进制也没有写工具。

`get_build_environment` 会对名称含 password/secret/token 等的注入环境变量打码。回复里不要原样贴 Token、密码或 `.env` 正文。

查询约定见 [Jenkins Skill](../.cursor/skills/jenkins/SKILL.md)。排障顺序：`list_jobs` → `get_build_info` → `get_pipeline_stages` → `get_stage_log` / `search_console_log`。

Job 路径用斜杠，例如 `team/prod/checkout-api`，对应 URL `/job/team/job/prod/job/checkout-api/`。

## 如何创建 Jenkins 只读用户和 API Token

MCP 用 **HTTP Basic**：用户名 + **API Token**（不是登录密码）访问 Jenkins REST。建议单独建账号（例如 `mcp-readonly`），不要把管理员密码或个人账号密码写进 `mcp.json`。

### 1. 管理员创建用户

1. 用管理员打开 Jenkins → **系统管理（Manage Jenkins）** → **用户（Users）**（有的版本在 **Security → Users**）。
2. **新建用户 / Create User**：用户名建议全小写英文（这就是 `JENKINS_USER`，不要填显示名）。设一个登录密码，仅供这个人登录 Web 去点「生成 Token」，MCP 不用这个密码。
3. 不要把该用户加成管理员。

### 2. 只给读权限（不要给 Build / Cancel / Configure）

本二进制没有触发/停止构建的工具，但 Jenkins 仍按账号鉴权。最小够用的权限：

| 权限 | 是否需要 | 说明 |
| --- | --- | --- |
| Overall/Read | 要 | 能进系统、调 REST |
| Job/Read、Job/Discover | 要 | 列 Job、看构建与控制台 |
| View/Read | 按需 | 你们用 View 时才需要；Folder 不是 View |
| Agent/Connect 或 Computer/Read | 按需 | 没有时 `list_nodes` / `get_node` 可能 403，不影响查 Job |
| Job/Build、Job/Cancel、Job/Configure、Overall/Administer、Run Scripts | **不要** | 防止误触发发版或跑 Groovy |
| PluginManager | **不要** | `health_check` 对 `/pluginManager` 返回 403 是预期 WARN |

授权方式因插件而异：

- **Matrix Authorization Strategy**：在全局矩阵里给该用户勾上表中的 Read/Discover。
- **Role-based Authorization Strategy**：建一个 `mcp-readonly` 角色，勾上同样权限，再把用户绑到角色。
- **Folder**：还要在业务 Folder（例如 `team`）上给 Read，否则根下看不见子 Job。

改完权限后用该用户登录 Web，确认能打开要排查的 Job 页面，但看不到「立即构建」或点了会被拒绝。

### 3. 该用户自己生成 API Token

API Token **必须由这个用户登录后生成**（管理员代替生成的方式因版本而异，以 Web 为准）：

1. 用 `mcp-readonly` 登录 Jenkins。
2. 点右上角用户名 → **Configure**（地址形如 `/user/mcp-readonly/configure`）。
3. 找到 **API Token** 一节 → **Add new Token** / **生成新的 Token**。
4. 起名 `cursor-mcp`，生成后 **立刻复制**。页面刷新后看不到完整值。
5. 填进本机 `mcp.json`：
   - `JENKINS_USER`：登录名（如 `mcp-readonly`）
   - `JENKINS_API_TOKEN`：刚复制的 Token
   - `JENKINS_URL`：Jenkins 根 URL，例如 `https://jenkins.example.com`（不要接到某个 `/job/...` 上）

Token 泄露：回到同一页 revoke 旧 Token，再发一颗并在所用客户端里重载 MCP。

本机快速验证：

```powershell
# 用户名:Token 走 Basic，不要把 Token 提交仓库
curl.exe -sS -u "mcp-readonly:<api-token>" "https://jenkins.example.com/api/json?tree=numJobs"
```

401：用户名或 Token 错。403：能登录但 Job 权限不够，回到第 2 步。然后把三项写进 `mcp.json`（见下一节），重载 jenkins。`health_check` 里插件列表 403 可忽略。

查询约定见 [Jenkins Skill](../.cursor/skills/jenkins/SKILL.md)。

## 能力

Cursor 里命名空间通常是 `user-jenkins`；WorkBuddy 等以该产品 MCP 面板里的名字为准。本二进制注册 18 个只读工具，均声明 `ReadOnlyHint=true`。

| 能力 | 工具 | 说明 |
| --- | --- | --- |
| 探测 Jenkins 是否可达 | `health_check` | 版本、账号、插件；`/pluginManager` 403 为预期 WARN |
| 查找 Job / Folder | `list_jobs` | `folder_path` 空=根；`recursive` + `name_filter`；最多 500 条 |
| 最近失败概览 | `find_recent_failures` | 限定 Folder；`since` 如 `24h` / `7d` |
| 查看排队 | `list_queue` | 可选 `job_path_prefix` |
| 列出 Agent | `list_nodes` | 在线 / 离线、执行器、标签 |
| 查看一个节点 | `get_node` | 控制器常用 `(built-in)` 或 `(master)` |
| 查看一次构建结果 | `get_build_info` | 结果、参数、变更集；`build_number` 省略或 `0` = lastBuild |
| 查看构建原因与环境 | `get_build_environment` | Cause、参数（秘密为 masked）、注入环境（敏感名 redacted） |
| 查看单次构建提交 | `get_scm_context` | commit 与改动路径 |
| 查找上次成功构建 | `last_green_build` | 对比失败构建的起点 |
| 成功之后改了什么 | `changes_since_last_green` | 与 `get_scm_context` 互补 |
| 对比两次构建 | `compare_builds` | `build_a` / `build_b` 必须为正整数且不同 |
| 查看 Pipeline 哪一步失败 | `get_pipeline_stages` | 先于控制台；记下失败 stage 的 `id` |
| 查看单个 stage 日志 | `get_stage_log` | 必须已有 `stage_id`；空日志改搜控制台 |
| 查看控制台末尾 | `get_console_log` | 默认最后 500 行，最多 2000；禁止负 `tail_lines` |
| 在控制台搜错误 | `search_console_log` | RE2，例如 `ERROR`、`FAILED`、`Caused by:` |
| 跟踪进行中的构建 | `tail_running_build` | 把返回的 `Next since_byte` 带回下一轮 |
| 查看 JUnit 报告 | `get_test_report` | 无报告时 404，不是权限问题 |

未注册、不要调用：`trigger_build`、`stop_build`、`cancel_queue_item`、`get_console_log_path`、`get_pipeline_script`、`run_groovy_script`。

## 构建

需要本机 Go 1.23+。

```bash
cd jenkins-mcp-server
go test ./...
go build -o jenkins-mcp-server.exe .
```

二进制不入库。日常请用仓库 [deploy/pack-windows.cmd](../deploy/README.md) 或 [deploy/pack-linux.sh](../deploy/README.md)。改源码后重新打包。

## 本机 MCP 配置

WorkBuddy 与 Cursor 用同一段 JSON。WorkBuddy 写入 `~/.workbuddy/mcp.json` 或在界面粘贴；Cursor 写入 `~/.cursor/mcp.json`。其它客户端见根 README [适配的智能体](../README.md#适配的智能体)。不要把 Token 提交到本仓库。用户和 Token 按上一节创建；账号只要 Overall/Read + Job/Read，不要给 Build / Cancel / Configure。完整样例见 [`examples/mcp.json.example`](../examples/mcp.json.example)。

```json
{
  "mcpServers": {
    "jenkins": {
      "type": "stdio",
      "command": "D:/project/CICD/cicd/mcp/jenkins-mcp-server/jenkins-mcp-server.exe",
      "args": [],
      "env": {
        "JENKINS_URL": "https://jenkins.example.com",
        "JENKINS_USER": "<readonly-user>",
        "JENKINS_API_TOKEN": "<jenkins-api-token>",
        "JENKINS_MCP_TIMEOUT": "90s"
      }
    }
  }
}
```

改配置后在所用客户端里重载或新开对话。`health_check` 对 `/pluginManager` 的 403 是只读账号无插件管理权限，预期 WARN，不影响查 Job。

## 本机验收

```powershell
# 不连 Jenkins，只核对接手和只读工具清单
.\scripts\smoke-stdio.ps1

# 连真实 Jenkins（Token / URL 走环境变量）
$env:JENKINS_URL = "https://jenkins.example.com"
$env:JENKINS_USER = "<user>"
$env:JENKINS_API_TOKEN = "<token>"
# 可选：指定一个你们环境里真实存在的 Folder / Job
$env:JENKINS_SAMPLE_FOLDER = "team"
$env:JENKINS_SAMPLE_JOB = "team/prod/checkout-api"
.\scripts\live-stdio.ps1
```

## 目录

```text
main.go               入口（stdio）
internal/jenkins/     HTTP 客户端与控制台缓存
internal/tools/       只读排障工具
scripts/              本机 smoke / live 验收
```

## Codex 接入与多环境配置

在用户级 `C:/Users/15509/.codex/config.toml`合并下面配置，替换程序和配置文件的绝对路径，保留原有设置；不要重复定义同名表。

```toml
[mcp_servers.jenkins]
command = "D:/project/CICD/cicd/mcp/jenkins-mcp-server/jenkins-mcp-server.exe"
args = []
enabled = true
startup_timeout_sec = 20
tool_timeout_sec = 120

[mcp_servers.jenkins.env]
JENKINS_URL = "https://jenkins.example.com"
JENKINS_USER = "<readonly-user>"
JENKINS_API_TOKEN = "<jenkins-api-token>"
JENKINS_MCP_TIMEOUT = "90s"
JENKINS_MCP_CACHE_DIR = "C:/Users/15509/AppData/Local/jenkins-mcp"
```

多个 Jenkins 环境分别注册 `jenkins-uat` / `jenkins-prod`，分别设置 URL、只读账号、Token 和独立的 `JENKINS_MCP_CACHE_DIR`；同一 Jenkins 内的环境通过 Folder / Job 路径区分。查询先指定实例并 `list_jobs`。

Token 占位符只在本机私有配置中替换；继承环境变量的写法见下方完整指南，不要把真实 Token 提交到仓库。

保存后重启对应 MCP 连接。CLI 可用 `codex mcp list` 检查配置、在会话中用 `/mcp` 核对连接；握手成功后再做小范围远端只读查询。

添加步骤、字段解释、凭据、环境切换和排障见 [Codex 完整指南](../CODEX.md)；可复制 [五服务 TOML](../examples/codex.toml.example) 或 [多环境 TOML](../examples/codex.multi-env.toml.example)。

## 对接 YluneMCPHub：凭据怎么添加

以下以 Linux Docker 部署月弦为例。先解压 `deploy/dist/devopsmcp-linux-amd64.tar.gz`，将所需二进制放入月弦宿主机的 `MCP_MOUNT_DIR`。默认挂载关系是 `/data/ylune-mcp` → 容器 `/opt/mcp`（只读）。如果使用了自定义挂载目录，以月弦实际 Compose 配置为准。`deploy` 只打包，不会自动复制文件、挂载或注册服务。

月弦「服务器」中新建 **STDIO** 服务，启动命令和文件环境变量一律填写**容器内路径**，不能填开发机 Windows 路径或容器不可见的宿主机路径。

启动命令：`/opt/mcp/jenkins-mcp-server`；参数留空。

在凭据中心新建例如 `jenkins-uat`，填写：

| 键 | 值示例 / 含义 |
| --- | --- |
| `JENKINS_URL` | `https://jenkins.example.com`，Jenkins 根地址 |
| `JENKINS_USER` | `mcp-readonly`，Jenkins 登录用户名 |
| `JENKINS_API_TOKEN` | 该账号生成的 API Token，直接在凭据中心填写真实值 |
| `JENKINS_MCP_TIMEOUT` | `90s` |

MCP 使用用户名加 API Token，不使用网页登录密码。不需要 targets 文件。连接后调用 `list_jobs` 验证权限。多个 Jenkins 实例若设置 `JENKINS_MCP_CACHE_DIR`，须分别指定不同的容器可写目录，不要指向只读 `/opt/mcp`。

### 在月弦绑定和授权

1. 管理员打开「凭据中心」，新建一条凭据，按上表添加键值。界面默认的 `HOST` / `PORT` / `TOKEN` 不是通用映射，必须使用表中的完整变量名。
2. 将凭据绑定到对应 MCP 服务器。建议在服务器环境变量中也声明这些变量：普通开关可填固定值，敏感项留空，由绑定的凭据覆盖。凭据中心注入的是进程环境变量，不会自动创建文件，也不会替换 targets JSON 内的 `${VAR}`。
3. 在凭据绑定处测试连接 / `listTools`。成功仅证明进程和工具列表可用，再到调试台执行下方的只读查询，验证上游凭据。
4. 在用户授权中勾选这个 MCP、允许的工具以及该 MCP 下绑定的凭据。只绑定凭据并不等于用户已获授权。客户端使用的是月弦签发的 Access Key，不是上游服务的 Token 或密码。

凭据中心加密存储需要月弦配置 `YLUNE_MASTER_KEY`。文件型密码仍由挂载文件保存，不会因为登记了文件路径就被月弦加密；应限制文件权限，并确保月弦容器运行用户可读。多个环境建议建独立服务器和独立凭据，分别指向各自的配置文件；同一进程可读取的 targets 不会因为凭据名称不同而自动隔离。

本服务的连接参数和 Token 已直接通过平台环境变量注入，不需要凭据文件。修改凭据后重新连接对应上游进程使新值生效。

更多平台说明见 YluneMCPHub 的凭据中心和部署文档。本节仅说明配置，不会修改现有月弦服务。
