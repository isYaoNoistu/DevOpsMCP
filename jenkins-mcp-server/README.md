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

Cursor 里命名空间通常是 `user-jenkins`；WorkBuddy 等以该产品 MCP 面板里的名字为准。本二进制注册 18 个只读工具。

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

二进制不入库。改源码后在本机重新 `go build`。

## 本机 MCP 配置

WorkBuddy 与 Cursor 用同一段 JSON。WorkBuddy 写入 `~/.workbuddy/mcp.json` 或在界面粘贴；Cursor 写入 `~/.cursor/mcp.json`。其它客户端见根 README [适配的智能体](../README.md#适配的智能体)。不要把 Token 提交到本仓库。用户和 Token 按上一节创建；账号只要 Overall/Read + Job/Read，不要给 Build / Cancel / Configure。完整样例见 [`examples/mcp.json.example`](../examples/mcp.json.example)。

```json
{
  "mcpServers": {
    "jenkins": {
      "type": "stdio",
      "command": "/ABS/PATH/DevOpsMCP/jenkins-mcp-server/jenkins-mcp-server",
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
