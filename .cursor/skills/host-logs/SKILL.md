---
name: host-logs
description: Use when searching host log files through the host-logs MCP tools, including PostgreSQL text logs, Nginx access/error, listing allowlisted files, and tailing. Use when the user asks to grep a server log, 查主机日志, unexpected EOF, or a path under /data/postgresql/log after database MCP is not enough.
---

# 主机日志只读检索

## 核心原则

一个 MCP，多个 target。先 `list_targets` 解析稳定 `name`，再带 `target` + **allowlist 内的绝对路径** 查文件。不要猜主机，也不要为了一个新 grep 场景就要求加 `exec` Tool。

这组 MCP 工具只有读取能力。不要声称已改文件、重启服务、chmod、或在机器上执行了任意命令。未在对话里明确要求「对主机做写操作」时，即使工具列表里出现 `exec` / `run_command`，也不要调用。本二进制默认也没有这些工具。

需要参数细节时读取 [references/tool-reference.md](references/tool-reference.md)。查发版契约、主机、日志目录时优先检索本仓库或 `cicd/` 对应服务 README，不要改业务仓。

调用前先发现 MCP 工具 schema（Cursor 命名空间通常是 `user-host-logs`），再按 schema 传参。私钥和密码不要写入仓库或回复。`list_targets` / `get_target_info` 不会返回私钥路径全文（最多 basename）。本机文件怎么写见仓库 `host-logs-mcp-server/docs/configuration.md`；无 SSH 夹具见 `lab/host-logs/README.md`。

## 连接配置

客户端 MCP 配置加一份本机 targets 即可：每台主机填 name、host、user、paths，再填 password 或 private_key，port 默认 22。加密私钥使用 private_key_passphrase。密码和密钥可混用，也可同一记录共存：先密钥后密码。私钥正文与 identity_file 二选一；旧密钥文件方式继续兼容。凭据不回显，不要求额外凭据文件。内置认证首次记录未知主机指纹，变化时拒绝；可设 host_key_sha256 固定指纹。创建用户后需要 passwd 设置密码，密钥用户需安装对应公钥，具体步骤见服务配置指南。

## 查询前先确定

1. 用户要查的是哪台机器上的哪类日志（PostgreSQL 文本日志 / Nginx / 其它），哪个环境。
2. 用 `list_targets` 的 `query` 解析，得到稳定 `name`（如 `orders-pg-prod`）。别名冲突时把候选列给用户，不要猜。
3. 之后每个工具都传这个 `target`。
4. 先 `list_log_files` 拿到真实文件名，再 `search_log` / `tail_log`。不要对目录做 search。

`name` 是机器稳定 ID，不要因为显示名变化就改它。文档里的 `orders-pg-prod` 只是示例。

## 工具选择

| 目的 | 首选工具 | 使用要点 |
|---|---|---|
| 有哪些主机 / 解析中文别名 | `list_targets` | `query` 如 `orders` 或 `postgres`；会按文件 mtime 自动 reload |
| 看一个 target 的 host / allowlist | `get_target_info` | 无私钥全文 |
| 目录里有哪些日志文件 | `list_log_files` | `path` 可空（列出该 target 全部 roots）；maxdepth 4 |
| 按关键字 / 正则搜一个文件 | `search_log` | 必填 `path` + `pattern`；可选 `start`/`end` 行前缀（字典序，非解析时间）；`fixed=true` 为字面量。远程 `grep -E` 不要用 `\d`，用 `[0-9]`。`.gz` 会解压 |
| 看文件尾 | `tail_log` | 默认 80 行，封顶 200 |

未注册、不要调用：`exec`、`run_command`、任意路径 `read_file`、`write_file`、删除或上传。不要要求用户把 allowlist 扩成 `/` 或 `/data`。

## 标准工作流

### PostgreSQL 长事务 / 断连（库 MCP 看不到当时 SQL）

1. `list_targets`：`query` 用库名或 `postgres`。
2. `list_log_files`：确认当天 `postgresql-*-YYYY-MM-DD.log`（文件名以现场为准）。
3. `search_log`：`path` 用上一步的绝对路径。常用字面量：`unexpected EOF`、`Connection timed out`、客户端 IP、`app=shop-web`、backend pid。
4. 有时间窗时传 `start` / `end`（与 `log_line_prefix=%m` 同一形式，如 `2026-09-17 05:38:00`）。
5. 需要前后几行时用 `context_before` / `context_after`（0–5）。
6. 汇报：时间、pid、`app=`、客户端、日志原文一两行。不要说已经改参数或杀会话。

### 未知问题 / 只知道大概目录

1. `list_targets` 得到 `target`。
2. `list_log_files`。
3. 对**单个文件** `search_log` 或 `tail_log`。
4. 没有命中就说没有命中，不要改去搜 allowlist 外的 `/var/log/messages`。

## 效率规则

- 已有精确 `target` 和文件路径就直接 `search_log`，否则先 list。
- 不要为了“全面”把目录下每个文件都 tail 一遍。
- 不要原样贴几百行。先结论，再留 target、路径、pid、少量行。
- 回复里不要出现私钥、`identity_file` 全路径、known_hosts 内容。

## 重要边界

- `HOST_LOGS_READ_ONLY=true` 不是安全的全部。SSH 用户本身必须只能读日志。
- allowlist 过宽（`/`、`/data`）在加载清单时会被拒绝。
- 这不是夜莺 `query_logs`（那是 Loki/ES），也不是 PostgreSQL MCP。
- 改 targets 文件后若 JSON 无效，工具会报错，不会假装已经切到新主机。
- 本机同时开多个 MCP 可能超过 Cursor 工具上限。查日志时关掉暂不用的 MCP。
- 改 targets 文件后无需改 `mcp.json`；下一轮 `list_targets` 会 reload。改二进制或 `mcp.json` 仍要重载 Cursor MCP。

## 回复规范

1. 先给结论：哪个 `target`、哪个文件、搜到了什么（EOF / timeout / 无匹配）。
2. 再给可核对证据：时间、pid、`application_name` / `app=`、客户端、一两行原文。
3. 分清「当前没有匹配」「路径不在 allowlist」「SSH 失败」「MCP 拒绝的敏感路径」。
4. 需要扩 allowlist、加 SSH 用户或开 `log_connections` 时说明缺口，让运维改主机，不要从 MCP 去改。

## 完整示例

用户问：“订单生产库凌晨有 unexpected EOF，帮我在主机日志里对一下时间。”

1. `list_targets`：`{"query":"orders"}` → 得到稳定 `name`（示例为 `orders-pg-prod`）
2. `list_log_files`：带上该 `target`
3. `search_log`：`path` 为当天日志绝对路径，`pattern`=`unexpected EOF`，`fixed`=true，加上 `start`/`end`
4. 汇总命中时间、pid、`app=`。不要说已经修改 PostgreSQL 配置。
