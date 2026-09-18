# 主机日志 MCP 工具参考

## 通用约定

- 除 `list_targets` 外，都要传 `target`（稳定 `name`，或能唯一命中的 alias / query）。
- `name` 是机器 ID，例如 `orders-pg-prod`。不要把中文显示名当成 `target` 硬编码；先 `list_targets`。
- `path` 必须是该 target `paths` 下的 Unix 绝对路径（实验室 `local` 则是 MCP 本机绝对路径）。
- 工具结果是 JSON 文本。失败时把错误原文留给用户判断：解析失败、SSH 失败、路径越界、敏感后缀。
- 行数默认 80，上限 200。响应体积上限约 256KiB。列目录 maxdepth 4，最多 200 个文件。
- `.gz` 会先解压再 search/tail。远程 search 打到行数上限时 `truncated=true`。
- 远程正则是 `grep -E`（POSIX ERE：`[0-9]` 不是 `\d`）。本机 `local` 才是 Go regexp。
- `start`/`end` 是整行前缀字典序，不是解析后的时间戳。
- 私钥不会以全文出现在任何工具输出里（`identity_file` 最多 basename）。
- targets 文件 mtime 变化后若 JSON/字段不合法，工具返回错误，并保留上一份成功清单。

## Target

### `list_targets`

列出本机 `host-logs-targets.json`。文件 mtime 变化则先 reload。解析失败会返回错误（保留上一份成功清单）。

| 参数 | 说明 |
|---|---|
| `query` | 可选。匹配 name / aliases / tags / host / paths / environment |

### `get_target_info`

| 参数 | 说明 |
|---|---|
| `target` | 稳定名或唯一别名 |

返回 host、port、user、transport、paths、identity_file 的 basename。无密码；`auth` 显示 password / private_key / private_key_or_password / key_or_ssh_config / local。

## Files

### `list_log_files`

| 参数 | 说明 |
|---|---|
| `target` | 必填 |
| `path` | 可选。空则列出该 target 全部 allowlist 根下的文件 |
| `limit` | 默认 200 |

每项至少含 `path`；本机 `local` 还会给 `size`。

## Search / Tail

### `search_log`

| 参数 | 说明 |
|---|---|
| `target` | 必填 |
| `path` | 必填，必须是文件 |
| `pattern` | 必填，最长 256 字节 |
| `fixed` | 默认 false（正则）。true 为字面量子串 |
| `start` | 可选。整行 `>= start`（适合 `%m` 时间戳前缀） |
| `end` | 可选。整行 `< end` |
| `max_lines` | 默认 80，最大 200 |
| `context_before` / `context_after` | 0–5 |

远程实现是固定 `readlink` + `gzip`/`cat` + `grep`/`awk` 模板，不是用户 shell。无匹配时返回空列表，不要当成 MCP 坏了。`\d` 在远程不会匹配数字。

### `tail_log`

| 参数 | 说明 |
|---|---|
| `target` | 必填 |
| `path` | 必填，必须是文件 |
| `lines` | 默认 80，最大 200 |

本机 `local` 从文件末尾最多回看约 2MiB 再取最后 N 行。

## 错误判读

| 现象 | 含义 |
|---|---|
| `no target matched` / `ambiguous` | 先 `list_targets`，不要猜 |
| `reload targets file` / `parse targets file` | targets JSON 坏了或字段不合法；先修文件，不要当已经切主机 |
| `choose private_key or identity_file` | 两种密钥来源二选一；可同时填 password 作为回退 |
| `invalid private key` / `private_key_passphrase` | 私钥正文或解密口令无效，连接前失败 |
| `allowlist path is too broad` | `paths` 写到具体目录，不要 `/` 或 `/data` |
| `outside this target allowlist` | `path` 不在该 target 的 `paths` 下 |
| `looks like a secret file` | 命中 `.env` / 密钥类后缀 |
| `path is required` / `path must be a file` | 先 `list_log_files` 再 search/tail |
| `ssh: …` | SSH 连接失败：检查认证方式、密码或密钥、主机指纹、用户和网络 |
| `HOST_LOGS_READ_ONLY must stay true` | 不要关掉只读开关 |
