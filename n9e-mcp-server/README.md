# n9e-mcp-server

本机 stdio MCP，用来查夜莺（Nightingale / n9e）的告警、主机、数据源、PromQL 指标和日志。进程跑在开发机，直连夜莺 API。

基于 [n9e 官方开源 MCP](https://github.com/n9e/n9e-mcp-server) 二次开发（上游自称 *Nightingale's official MCP Server*）。本树只留 stdio 入口和这 6 个只读 toolset，去掉 HTTP 模式、写工具、用户/看板/屏蔽等未使用包。夜莺产品本身见 [ccfos/nightingale](https://github.com/ccfos/nightingale)。许可证与上游说明见仓库根目录 [NOTICE](../NOTICE)。

不做 Prometheus / Elasticsearch 专用 MCP：夜莺已经能同时看告警、指标和日志，把对应数据源接到夜莺即可，本进程只调夜莺 API。理由见根 README [为什么是夜莺](../README.md#为什么是夜莺而不是-prometheus--elasticsearch)。WorkBuddy、Cursor 及其它 stdio 客户端怎么接见 [适配的智能体](../README.md#适配的智能体)。尚在测试阶段，使用风险与写操作二开注意见 [测试阶段与免责](../README.md#测试阶段与免责)。

## 默认范围

本仓库只注册：

```text
alerts, targets, datasource, busi_groups, metrics, logs
```

`--read-only` 默认为 `true`。MCP 客户端配置再加一层：

```text
N9E_TOOLSETS=alerts,targets,datasource,busi_groups,metrics,logs
N9E_READ_ONLY=true
```

查询约定见 [夜莺 Skill](../.cursor/skills/nightingale/SKILL.md)。流程：列表过滤 → 少量详情 → 复用 ID / PromQL → 再查指标或日志。

未在对话里明确要求「对夜莺做写操作」时，Agent 不得调用任何写工具。本二进制默认也没有写工具。

`get_datasource` 可能带出鉴权字段，回复里不要原样贴密码或 Token。

## 如何拿到夜莺 Token

MCP 用请求头 `X-User-Token` 调夜莺 HTTP API，**不是**登录密码，也不是数据源（Prometheus / ES）自己的口令。Token 绑在**某一个夜莺用户**上，能看到的业务组、告警、数据源，就是这个用户在夜莺里被授权的范围。建议单独建一个只读值班账号（例如 `mcp-readonly`），不要用超级管理员的长期 Token。

### 1. 准备一个给 MCP 用的用户（管理员做）

管理员登录夜莺（Flashcat Nightingale）：

1. 打开 **人员组织 → 用户管理**（有的版本在 **系统设置 → 用户**）。
2. 添加用户：登录名、显示名、密码。不要勾选站点管理员，除非你们没有更细的角色。
3. 把该用户加进 MCP 需要排查的 **业务组**，并只给查看类权限（能看告警、主机、规则、数据源即可）。不要给改规则、屏蔽、发通知、管用户的权限。

若你们已经用个人账号排查、且权限本身只读，也可以跳过本步，直接用该账号发 Token。权限过大时，Agent 在 UI 里看不到的写入口，API 上仍可能看得到更多数据源配置，`get_datasource` 的回复里不要贴鉴权字段。

### 2. 用这个用户创建 Token（该用户自己登录）

1. 用上一步的账号登录夜莺 Web。
2. 点右上角头像 / 用户名，进入 **个人设置**（或 **个人中心**）。
3. 打开 **Token 管理**（有的版本叫 **Access Token** / **令牌**）。
4. **新建 Token**，备注写成 `cursor-mcp` 之类，便于以后吊销。
5. **只在创建成功那一次能看到完整 Token**。立刻复制，发给本机 MCP 配置里的 `N9E_TOKEN`（WorkBuddy：`~/.workbuddy/mcp.json`；Cursor：`~/.cursor/mcp.json`）。不要写入 Git、不要贴进群、不要截图进开源文档。

Token 泄露或人走了：回到同一页删除/禁用该 Token，再发一颗新的并在所用客户端里重载 MCP。

### 3. 填 `N9E_BASE_URL`

`N9E_BASE_URL` 是夜莺 **API 根地址**（本 MCP 会请求 `/api/n9e/...`），一般与 Web 同源，常见端口 `17000`，例如 `http://nightingale.example.com:17000`。不要加路径后缀，不要把 Prometheus 的地址填在这里。

本机验证（Token 走环境变量，不要写进命令历史文件）：

```powershell
$env:N9E_TOKEN = "<刚复制的 token>"
$env:N9E_BASE_URL = "http://nightingale.example.com:17000"
curl.exe -sS -H "X-User-Token: $env:N9E_TOKEN" "$env:N9E_BASE_URL/api/n9e/self/profile"
```

能返回当前用户 JSON 即可。401：Token 错或已删。403：用户没有该接口权限。然后把同样两项写进 MCP 配置（见下一节），重载 nightingale。

查询约定见 [夜莺 Skill](../.cursor/skills/nightingale/SKILL.md)。

## 能力

Cursor 里命名空间通常是 `user-nightingale`；WorkBuddy 等以该产品 MCP 面板里的名字为准。本二进制注册 16 个只读工具。

| 能力 | 工具 | 说明 |
| --- | --- | --- |
| 查看正在触发的告警 | `list_active_alerts` | 用 `hours`、`severity`、`query`、`bgid`、`rid` 等尽早过滤 |
| 查看一条当前告警 | `get_active_alert` | 必须已有事件 ID `eid` |
| 回溯已发生的告警 | `list_history_alerts` | 必须给时间窗；可按严重级别、恢复状态、业务组收敛 |
| 查看一条历史告警 | `get_history_alert` | 比列表多恢复状态和恢复时间 |
| 列出业务组 | `list_busi_groups` | 给后续 `list_alert_rules` 提供 `group_id` |
| 查看业务组内规则 | `list_alert_rules` | 必须已有 `group_id`；按页取 |
| 查看规则配置 | `get_alert_rule` | 必须已有 `arid`，通常来自告警的 `rule_id` |
| 搜索主机 / 离线目标 | `list_targets` | `downtime` 单位是秒；`query` 匹配 ident / tags |
| 列出数据源 | `list_datasources` | 摘要视图，不含鉴权秘密 |
| 查看一个数据源 | `get_datasource` | 插件类型与 URL；不要把返回里的 Token 贴进回复 |
| 列出数据源插件类型 | `list_datasource_plugins` | Prometheus、Loki、ES 等 |
| 查询当前或某时刻指标 | `query_instant` | 必须已有 Prometheus 数据源 ID 和 PromQL |
| 查询指标趋势 | `query_range` | 时间为 Unix 秒；优先省略 `step` 让工具自动算 |
| 查询 Loki / ES / OS 日志 | `query_logs` | `body` 必须是夜莺原生 `/logs-query` 结构，禁止臆造字段 |
| 发现 ES / OS 索引 | `list_log_indices` | 只适用于 ES / OS；`body` 必须带 `datasource_id` 和 `cate` |
| 发现 ES / OS 字段 | `list_log_fields` | 先确定索引；OpenSearch 传 `engine: "os"` |

未注册、不要调用：`create_*`、`update_*`、`import_*`、屏蔽、改规则、改通知、改用户、改看板。

## 构建

需要本机 Go 1.23+。

```bash
cd n9e-mcp-server
go test ./...
go build -o n9e-mcp-server.exe ./cmd/n9e-mcp-server/   # Windows
./n9e-mcp-server.exe version
```

二进制不入库。改源码后在本机重新 `go build`。

## 本机 MCP 配置

WorkBuddy 与 Cursor 用同一段 JSON（建议带 `"type": "stdio"`）。WorkBuddy 写入 `~/.workbuddy/mcp.json` 或在界面粘贴；Cursor 写入 `~/.cursor/mcp.json`。其它客户端见根 README [适配的智能体](../README.md#适配的智能体)。不要把 Token 提交到本仓库。完整样例见 [`examples/mcp.json.example`](../examples/mcp.json.example)。

```json
{
  "mcpServers": {
    "nightingale": {
      "type": "stdio",
      "command": "/ABS/PATH/DevOpsMCP/n9e-mcp-server/n9e-mcp-server",
      "args": ["stdio"],
      "env": {
        "N9E_TOKEN": "<nightingale-personal-token>",
        "N9E_BASE_URL": "http://nightingale.example.com:17000",
        "N9E_TOOLSETS": "alerts,targets,datasource,busi_groups,metrics,logs",
        "N9E_READ_ONLY": "true",
        "N9E_MCP_LOG_LEVEL": "info"
      }
    }
  }
}
```

Token 按上一节在夜莺「个人设置 → Token 管理」创建，只放本机 MCP 配置。改配置后在所用客户端里重载或新开对话。

查询日志时，`list_log_indices` / `query_logs` 的 `body` 必须带 `cate`（Elasticsearch 为 `elasticsearch`），只传 `datasource_id` 会返回 `cluster not exists`。

## 本机验收

```powershell
# 不连夜莺，只核对接手和工具清单
.\scripts\smoke-stdio.ps1

# 连真实夜莺（Token / Base URL 走环境变量）
$env:N9E_TOKEN = "<token>"
$env:N9E_BASE_URL = "http://nightingale.example.com:17000"
.\scripts\live-stdio.ps1
```

## 目录

```text
cmd/n9e-mcp-server/   入口（stdio）
internal/             stdio 服务
pkg/api/              六个只读 toolset
pkg/client/           夜莺 HTTP 客户端
pkg/toolset/          工具注册与只读开关
scripts/              本机 smoke / live 验收
```
