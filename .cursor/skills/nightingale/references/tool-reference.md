# Nightingale MCP 工具参考

## 目录

- [通用约定](#通用约定)
- [告警工具](#告警工具)
- [目标工具](#目标工具)
- [指标工具](#指标工具)
- [日志工具](#日志工具)
- [错误与结果判读](#错误与结果判读)

## 通用约定

- 所有时间戳均为 Unix 秒。
- 严重级别：`1` 严重、`2` 警告、`3` 提示。
- 常见分页结果为 `{"list": [...], "total": N}`。页码 `p` 从 1 开始，默认 `limit=20`、`p=1`。
- `limit` 与 `p` 只能是非负整数；传 `0` 表示使用服务端或工具默认值。
- 工具结果是 JSON 文本。工具调用失败时 MCP 结果带 `isError=true`，错误文本可能来自参数校验、HTTP 状态或 Nightingale 响应中的 `err`。

## 告警工具

### `list_active_alerts`

列出当前活跃告警，返回 `{"list": AlertCurEvent[], "total": N}`。

| 参数 | 类型 | 说明 |
|---|---|---|
| `hours` | integer | 回看小时数；与 `stime`/`etime` 互斥 |
| `stime` | integer | 开始时间 |
| `etime` | integer | 结束时间；同时提供时必须大于 `stime` |
| `severity` | string | 可用 `"1"` 或 `"1,2"`；仅接受 1、2、3 |
| `query` | string | 匹配规则名或标签的关键词 |
| `cate` | string | 常见值 `prometheus`、`host`、`elasticsearch`、`loki`、`$all` |
| `rule_prods` | string | 逗号分隔的 `host`、`metric`、`loki`、`anomaly` |
| `datasource_ids` | string | 逗号分隔的数据源 ID |
| `rid` | integer | 告警规则 ID |
| `bgid` | integer | 业务组 ID |
| `limit` | integer | 页大小，默认 20 |
| `p` | integer | 页码，从 1 开始 |

虽然内部输入结构还定义了 `event_ids` 和 `my_groups`，当前 MCP schema 与请求构造没有暴露它们，不要传入。

`AlertCurEvent` 中最适合后续下钻的字段：

| 字段 | 用途 |
|---|---|
| `id` | 传给 `get_active_alert.eid` |
| `rule_id` | 传给 `get_alert_rule.arid` |
| `group_id`, `group_name` | 业务范围与规则列表 |
| `datasource_id` | 指标或日志数据源上下文 |
| `prom_ql` | 可复用的指标表达式 |
| `target_ident` | 主机/目标定位 |
| `severity` | 1/2/3 严重级别 |
| `trigger_time`, `first_trigger_time` | 当前与首次触发时间 |
| `trigger_value`, `trigger_values` | 触发值 |
| `tags`, `tags_map`, `annotations` | 服务、实例、环境等关联条件 |
| `cate`, `rule_prod`, `rule_algo` | 告警类型与算法 |

### `get_active_alert`

输入：

```json
{"eid":98123}
```

`eid` 必须为正整数。返回单个 `AlertCurEvent`，不存在或无权限时返回工具错误。

### `list_history_alerts`

列出历史告警，返回 `{"list": AlertHisEvent[], "total": N}`。

| 参数 | 类型 | 说明 |
|---|---|---|
| `hours` | integer | 回看小时数；与 `stime`/`etime` 互斥 |
| `stime` | integer | 开始时间 |
| `etime` | integer | 结束时间 |
| `severity` | integer | `-1` 全部、`1` 严重、`2` 警告、`3` 提示；一次只能传一个 |
| `is_recovered` | integer | schema 含义为 `-1` 全部、`0` 未恢复、`1` 已恢复；当前实现只转发非零值 |
| `query` | string | 搜索关键词 |
| `cate` | string | 告警类别 |
| `rule_prods` | string | 逗号分隔产品类型 |
| `datasource_ids` | string | 逗号分隔数据源 ID |
| `bgid` | integer | 业务组 ID |
| `limit` | integer | 页大小，默认 20 |
| `p` | integer | 页码，从 1 开始 |

当前实现没有对历史告警的 `severity` 和 `is_recovered` 做枚举校验。仍只使用上述合法值。由于 `is_recovered: 0` 会被省略，查未恢复事件应改用 `list_active_alerts`，或获取历史列表后根据返回值过滤。

`AlertHisEvent` 包含活跃事件的全部字段，另有：

- `is_recovered`: 是否恢复。
- `recover_time`: 恢复时间 Unix 秒。

### `get_history_alert`

输入：

```json
{"eid":87654}
```

返回单个 `AlertHisEvent`。

### `list_alert_rules`

输入参数：

| 参数 | 类型 | 必需 | 说明 |
|---|---|---|---|
| `group_id` | integer | 是 | 正整数业务组 ID |
| `limit` | integer | 否 | 页大小，默认 20 |
| `p` | integer | 否 | 页码，从 1 开始 |

Nightingale API 原始返回整个业务组规则数组，MCP 在本地切页并返回 `{"list": AlertRule[], "total": N}`。因此小页有助于减少模型输出，但不会减少上游 API 返回量。

规则常用字段：`id`、`group_id`、`name`、`note`、`cate`、`prod`、`datasource_ids`、`datasource_queries`、`prom_ql`、`algorithm`、`algo_params`、`severity`、`disabled`、`prom_for_duration`、`prom_eval_interval`、`recover_duration`、`enable_stime`、`enable_etime`、`enable_days_of_week`、`annotations`、`runbook_url`。

### `get_alert_rule`

输入：

```json
{"arid":456}
```

`arid` 必须为正整数，返回单个 `AlertRule`。

## 目标工具

### `list_targets`

返回 `{"list": Target[], "total": N}`。

| 参数 | 类型 | 说明 |
|---|---|---|
| `gids` | string | 逗号分隔业务组 ID |
| `query` | string | 匹配目标 `ident` 或标签 |
| `limit` | integer | 页大小，默认 20 |
| `p` | integer | 页码，从 1 开始 |
| `downtime` | integer | 未上报时长，单位秒；例如 5 分钟传 300 |
| `datasource_ids` | string | 逗号分隔数据源 ID |

返回常用字段：

- 标识：`id`、`ident`、`note`、`host_ip`。
- 分组：`group_id`、`group_ids`、`group_objs`。
- 标签：`tags`、`tags_map`。
- 状态：`target_up`、`unix_time`、`update_at`、`offset`。
- 环境：`agent_version`、`engine_name`、`os`、`arch`、`remote_addr`、`cpu_num`、`mem_size`。

不要仅凭 `target_up` 字段名推测语义；结合 `downtime` 查询、最近更新时间和用户环境解释在线状态。

## 指标工具

### `query_instant`

| 参数 | 类型 | 必需 | 说明 |
|---|---|---|---|
| `ds_id` | integer | 是 | 正整数 Prometheus 兼容数据源 ID |
| `query` | string | 是 | 非空 PromQL |
| `time` | integer | 否 | 求值时间 Unix 秒；省略或非正数时工具使用当前时间 |

示例：

```json
{"ds_id":7,"query":"up{ident=\"api-01\"}"}
```

工具向 Nightingale 批量接口提交一条查询，并解包第一条结果。常见返回为序列数组，每项包含 `metric` 标签和 `value: [timestamp, value]`。批接口没有结果项时可能返回 `null`。

### `query_range`

| 参数 | 类型 | 必需 | 说明 |
|---|---|---|---|
| `ds_id` | integer | 是 | 正整数数据源 ID |
| `query` | string | 是 | 非空 PromQL |
| `start` | integer | 是 | 开始时间 Unix 秒，必须为正 |
| `end` | integer | 是 | 结束时间 Unix 秒，必须大于 `start` |
| `step` | integer | 否 | 步长秒数；省略或非正数时自动计算 |
| `max_points` | integer | 否 | 每条序列点数上限，默认 1000 |

自动步长为 `ceil((end-start)/max_points)`，最小 1 秒。若显式 `step` 会产生超过 `max_points` 的点数，工具会把步长增大并标记 `truncated: true`。

返回：

```json
{
  "effective_step": 10,
  "max_points": 180,
  "truncated": false,
  "data": [
    {
      "metric": {"ident": "api-01"},
      "values": [[1785810600, "73.5"]]
    }
  ]
}
```

`data` 是批查询第一条结果的解包值；可能为数组、`null` 或上游返回的其他 JSON 结构。

## 日志工具

日志接口把 `body` 原样转发给 Nightingale 插件。这个 MCP 项目没有定义 Loki、Elasticsearch 或 OpenSearch 的统一请求体 schema，因此以下工具只能保证外层契约。

Elasticsearch 请求体骨架、发现步骤与示例见 [elasticsearch-example.md](elasticsearch-example.md)。字段名、索引前缀以你环境的 `list_log_indices` / `list_log_fields` 为准，不要套用别人的索引。

### `query_logs`

| 参数 | 类型 | 必需 | 说明 |
|---|---|---|---|
| `body` | object | 是 | Nightingale `/api/n9e/logs-query` 原生请求体；ES 根对象含 `cate`、`datasource_id` 和单数键 `query` 数组 |
| `limit` | integer | 否 | 外层返回条数提示，默认 200 |
| `start` | integer | 否 | 只用于 MCP 的 7 天跨度校验 |
| `end` | integer | 否 | 只用于 MCP 的 7 天跨度校验 |

约束和细节：

- 同时提供外层 `start` 与 `end` 且跨度超过 604800 秒时，工具拒绝查询。
- 外层 `start`/`end` 不会注入 `body`，所以不能代替引擎请求体中的实际时间范围。
- MCP 在根级 `body.limit` 缺失时会注入外层 `limit`，但 Nightingale ES 的 `QueryParam` 不读取这个根级字段。ES 的真实限制必须写在 `body.query[].limit`。
- 返回形状为 `{"limit": N, "data": <Nightingale 插件返回值>}`。
- 对 ES，把外层 `limit` 与每个 `body.query[].limit` 设为相同值。返回顶层 `limit` 只是 MCP 元数据，不证明上游实际返回了同样数量。
- ES 查询对象中的 `page` 是从零开始的结果偏移量，不是页码。例如 `limit: 50` 时依次使用 `page: 0`、`page: 50`、`page: 100`。

### `list_log_indices`

输入外层：

| 参数 | 类型 | 必需 | 说明 |
|---|---|---|---|
| `body` | object | 是 | ES 至少使用 `{"cate":"elasticsearch","datasource_id":<实际 ID>}` |
| `engine` | string | 否 | 省略或 `"es"` 使用 ES；精确 `"os"` 使用 OpenSearch |

代码只对 `engine == "os"` 切换 OpenSearch 路径，其他任意值都会走 ES。只使用省略、`"es"` 或 `"os"`。

### `list_log_fields`

外层参数与 `list_log_indices` 相同，但 ES 的 `body` 还必须包含 `index`，例如 `{"cate":"elasticsearch","datasource_id":<实际 ID>,"index":"<your-logs-*>"}`。返回字段结构由 ES/OS 插件决定。

## 错误与结果判读

| 现象 | 含义与处理 |
|---|---|
| `invalid input` | 修正时间互斥、严重级别或分页参数后重试 |
| `eid/arid/group_id/ds_id ... required` | 缺少或传入了非正整数 ID |
| `client error: 401/403` | Token 无效或权限不足；不要改写查询来掩盖权限问题 |
| `client error: 404` | API 路径与 Nightingale 版本不兼容，或对象不存在 |
| `rate limited (429)` | 客户端已重试仍受限；缩减调用频率和范围 |
| `server error: 5xx` | Nightingale 或数据源服务端错误；已自动重试 |
| Nightingale `err` 文本 | 业务错误，例如数据源不存在；按原文报告 |
| `[]`、`{"list":[],"total":0}` | 查询成功但没有匹配项 |
| `null` 指标结果 | 批接口没有可解包的第一条结果，不可解释为健康 |
| 日志 `data` 为空 | 当前请求体与窗口无匹配日志；先核对引擎时间字段、索引和过滤条件 |
| 日志错误 `no data` | Nightingale ES 查询可能用该错误表示零命中；先按“无匹配日志”处理并核对窗口、索引、字段和过滤条件，不要直接判为基础设施故障 |
