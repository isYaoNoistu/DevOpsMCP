# Jenkins MCP 工具参考

本仓库 MCP 名为 `jenkins`（Cursor 里通常是 `user-jenkins`）。stdio 二进制在 `jenkins-mcp-server/`，凭据只在本机 `~/.cursor/mcp.json`。

## 通用约定

- `job_path`：斜杠路径，不要写成 `/job/a/job/b/`。
- `build_number`：省略或 `0` = `lastBuild`。`compare_builds` 除外，必须两个正整数。
- `name_filter` / `path_filter`：RE2，大小写不敏感（工具侧会加 `(?i)` 的除外，以各工具说明为准）。`search_console_log` 的 `pattern` 是区分大小写的 RE2，需要忽略大小写时自己写 `(?i)ERROR`。
- 失败时可能是 MCP `isError`、JSON-RPC `-32602`（缺参）、或 Jenkins HTTP 403/404。
- 不要调用写工具：`trigger_build`、`stop_build`、`cancel_queue_item`。

## `list_jobs`

| 参数 | 类型 | 说明 |
|---|---|---|
| `folder_path` | string | 空 = 根；例 `team`、`team/prod` |
| `recursive` | bool | 默认 false；发版排查时对已知 Folder 用 true |
| `name_filter` | string | 匹配叶子 Job 名；Folder 仍会遍历 |

最多 500 条。返回表格：`type`（job/folder）、`status`（Jenkins color）、`last#`、`result`、`job_path`。

## `get_build_info`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `build_number` | integer | 可选 |

返回 JSON：`result`、`building`、`duration`、`timestamp`、`url`、`actions[].parameters`、变更集。先读这些字段，不要整包贴给用户。

## `get_build_environment`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `build_number` | integer | 可选 |
| `name_filter` | string | 只过滤注入环境变量名 |

含 Cause、Parameters（秘密类型为 `(masked)`）、EnvInject（插件缺失则提示 404）。名称含 password/secret/token 等的注入变量值为 `(redacted)`。

## `get_pipeline_stages`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `build_number` | integer | 可选 |

返回各 stage 的 `id`、`status`、`duration`、`name`。`id` 传给 `get_stage_log`。非 Pipeline 构建会 404 提示。

## `get_stage_log`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `stage_id` | string | 必填，来自 stages 表 |
| `build_number` | integer | 可选 |

Declarative 包装 stage 日志经常为空，改用 `search_console_log`。不要改调未注册的 `get_console_log_path`。

## `get_console_log`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `build_number` | integer | 可选 |
| `tail_lines` | integer | 默认最后 500 行，上限 2000；负数会报错。不返回本机缓存路径。 |

## `search_console_log`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `pattern` | string | 必填，RE2 |
| `build_number` | integer | 可选 |
| `context_lines` | integer | 默认 3 |
| `max_matches` | integer | 默认 50 |

发版失败常用：`ERROR|FAILED|Exception|Caused by:|Finished:`。

## `tail_running_build`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `build_number` | integer | 可选 |
| `since_byte` | integer | 下一页用上次 footer 的 `Next since_byte` |
| `max_bytes` | integer | 默认 65536，上限 1 MiB |

只用于仍在跑的构建。

## `find_recent_failures`

| 参数 | 类型 | 说明 |
|---|---|---|
| `folder_path` | string | 空 = 根 |
| `since` | string | 默认 `24h`；支持 Go duration 和 `Nd` |
| `result_filter` | string | `FAILURE`（默认）、`UNSTABLE`、`ABORTED`、`ANY_NON_SUCCESS` |
| `max_results` | integer | 默认 100，上限 500 |

每个 Job 只看最近 5 次构建。窗口大于 7 天可能漏掉更早的失败。

## `last_green_build` / `changes_since_last_green` / `get_scm_context`

- `last_green_build`：只要 `job_path`。
- `changes_since_last_green`：`job_path`，可选 `max_commits`、`path_filter`。
- `get_scm_context`：单次构建的 commit 与路径；可选 `build_number`、`max_commits`、`path_filter`。

## `compare_builds`

| 参数 | 类型 | 说明 |
|---|---|---|
| `job_path` | string | 必填 |
| `build_a` | integer | 必填，> 0，基线 |
| `build_b` | integer | 必填，> 0，且 ≠ `build_a` |
| `include_tests` | bool | 默认 true；大测试套件可 false |

## `get_test_report`

`job_path` 必填；可选 `build_number`、`stack_trace_lines`。无报告时提示 404。

## `list_queue` / `list_nodes` / `get_node` / `health_check`

- `list_queue`：可选 `job_path_prefix`（匹配 task URL 子串，如 `/job/team/`）。
- `list_nodes`：无参数。
- `get_node`：`name` 必填；本实例控制器为 `Built-In Node`，工具说明里也可用 `(built-in)`。
- `health_check`：无参数。插件 403 写成 WARN，不要据此判断 Pipeline 不可用。

## 错误判读

| 现象 | 含义 |
|---|---|
| `missing properties: ["build_number"]` | 旧二进制；应已可省略。仍出现则重载 MCP |
| HTTP 403 on `/pluginManager` | 只读账号无插件管理，忽略 |
| HTTP 404 on `/wfapi/describe` | 非 Pipeline 或无 stage 数据 |
| HTTP 404 on `/testReport` | 这次没发布测试报告 |
| `(no entries matched)` | Folder 下无匹配 Job，换 `folder_path` / `name_filter` |
| 空队列 / 无 FAILURE | 该查询无匹配，不是“系统正常”的证明 |
