---
name: jenkins
description: Use when querying Jenkins CI/CD through the jenkins MCP tools, including jobs, folders, build status, pipeline stages, console logs, queue, and nodes. Use when a deploy or pipeline fails, a build is stuck, the user asks to inspect Jenkins history, or mentions 发版失败、流水线、构建日志、排队、Jenkins Job.
---

# Jenkins 发版排障查询

## 核心原则

按“先定位 Job、再确认构建、再看 stage、最后搜控制台”查询。先用 `list_jobs` / `find_recent_failures` 缩小范围，再对 1 至 3 个构建下钻。不要一上来拉整包 Console。

这组 MCP 工具只有读取能力。不要声称已触发、停止、取消或修改任何 Job / 构建。未在对话里明确要求「对 Jenkins 做写操作」时，即使工具列表里出现 `trigger_build`、`stop_build`、`cancel_queue_item`，也不要调用。

需要精确参数时读取 [references/tool-reference.md](references/tool-reference.md)。Folder / Job 路径写法见 [references/job-paths.md](references/job-paths.md)。真实路径用 `list_jobs` 发现，不要使用文档里的示例名当真值。

调用前先发现 MCP 工具 schema（Cursor 命名空间通常是 `user-jenkins`），再按 schema 传参。Token 只在本机 `~/.cursor/mcp.json`，不要写入仓库或回复。

## 查询前先确定

1. 用户要查的是失败原因、进行中卡住、最近是否成功，还是对比两次构建。
2. 已有的 Job 名、Folder、构建号、参数。没有 `job_path` 时先 `list_jobs`，不要猜路径。
3. `job_path` 用斜杠，例如 `team/prod/checkout-api`，对应 URL `/job/team/job/prod/job/checkout-api/`。
4. `build_number` 可省略或传 `0`，表示 `lastBuild`。对比两次构建时必须给两个正整数。

## 工具选择

| 目的 | 首选工具 | 使用要点 |
|---|---|---|
| 找 Job / Folder | `list_jobs` | 先给 `folder_path`；`recursive: true`；可用 `name_filter` |
| 最近失败概览 | `find_recent_failures` | 限定 Folder；`since` 如 `24h` / `7d` |
| 某次构建结果与参数 | `get_build_info` | 已有 `job_path`；看 `result`、`building`、参数 |
| 构建原因 / 注入环境 | `get_build_environment` | 密钥会打码；不要把秘密值贴进回复 |
| Pipeline 哪一步失败 | `get_pipeline_stages` | 先于控制台；记下失败 stage 的 `id` |
| 单个 stage 日志 | `get_stage_log` | 必须已有 `stage_id`；空日志则改搜控制台 |
| 控制台末尾 | `get_console_log` | 默认最后 500 行，最多 2000；禁止负 `tail_lines` |
| 控制台搜错 | `search_console_log` | RE2，如 `ERROR|FAILED|Caused by:` |
| 进行中的日志 | `tail_running_build` | 把返回的 `Next since_byte` 带回下一轮 |
| 上次成功 | `last_green_build` | 和失败构建对比的起点 |
| 成功之后改了什么 | `changes_since_last_green` | 与 `get_scm_context` 互补 |
| 两次构建差异 | `compare_builds` | `build_a` / `build_b` 必须 > 0 |
| 测试报告 | `get_test_report` | 无 JUnit 时 404 提示，不是权限问题 |
| 排队未开始 | `list_queue` | 可用 `job_path_prefix` |
| Agent 是否在线 | `list_nodes` / `get_node` | 控制器名常用 `(built-in)` |
| 连通与账号 | `health_check` | 插件列表 403 为预期，不影响查 Job |

未注册、不要调用：`trigger_build`、`stop_build`、`cancel_queue_item`、`get_console_log_path`、`get_pipeline_script`、`run_groovy_script`。

## 标准工作流

### 发版 / Pipeline 失败

1. 用户给出服务名时，`list_jobs`：`folder_path` 取业务 Folder，`name_filter` 取服务关键字。
2. 对命中的 `job_path` 调用 `get_build_info`。确认 `result`、`building`、构建号、参数。
3. 调用 `get_pipeline_stages`。优先看 `FAILED` / `ABORTED` 的 stage。
4. 对该 stage 调 `get_stage_log`。长度为 0 时改用 `search_console_log`（`ERROR|FAILED|Exception|Caused by:`）。
5. 仍不够时 `get_console_log` 取最后 80～200 行，不要整包。
6. 需要对比上次成功时：`last_green_build` → `compare_builds` 或 `changes_since_last_green`。

### 构建还在跑或一直排队

1. `get_build_info` 看 `building`。
2. 仍在跑：`get_pipeline_stages` + `tail_running_build`。
3. 还没编号或一直排队：`list_queue`，必要时 `list_nodes`。

### 最近谁红了

1. `find_recent_failures`，`folder_path` 收窄，`since` 用用户给的窗口（默认 24h）。
2. 只对最相关的 1 至 3 条走上面的失败工作流。

## 效率规则

- 已有精确 `job_path` 和构建号就直接下钻，否则先 `list_jobs`。
- 根目录 `recursive: true` 会列出大量条目；能按 Folder 就按 Folder。
- 不要为了“全面”把 18 个工具都调一遍。
- 不要原样贴整段 Console 或完整 `get_build_info` JSON。先结论，再留构建号、失败 stage、几行关键日志。
- 参数里的凭据 **ID** 可以点名；密码、Token、`.env` 正文不要输出。

## 重要边界

- Folder 与 View 不是一回事。Folder 用 Job 路径。
- `health_check` 对 `/pluginManager` 的 403 是只读账号无插件管理权限，预期 WARN。
- `get_pipeline_stages` 404 表示该构建不是 Pipeline 或无 wfapi，改看控制台。
- `get_test_report` 404 表示这次构建没发 JUnit，不等于测试都过了。
- 空队列、无最近 FAILURE 只表示该查询无匹配，不等于 Jenkins 健康。
- 回复时把时间戳转成用户可读的本地时间。

## 回复规范

1. 先给结论：哪个 `job_path`、哪次构建、结果、失败 stage 或卡住原因。
2. 再给可核对证据：构建号、参数、stage 名/id、少量日志行、Jenkins URL。
3. 分清「构建失败」「还在跑」「在排队」「没有匹配 Job」「工具/权限错误」。
4. 需要改业务代码时说明缺口，让开发改他们的仓；不要声称已经重跑或修好。

## 完整示例

用户问：“checkout 生产发布失败了，看一下。”

1. `list_jobs`：`{"folder_path":"team","recursive":true,"name_filter":"checkout"}`
2. 命中 `team/prod/checkout-api` 后 `get_build_info`。
3. `get_pipeline_stages`；失败 stage 再 `get_stage_log`。
4. 汇总：构建号、关键参数、失败 stage、错误行。不要说已经重跑。
