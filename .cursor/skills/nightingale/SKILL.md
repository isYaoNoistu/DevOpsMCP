---
name: nightingale
description: Use when querying Nightingale monitoring data through the nightingale MCP tools, including active or historical alerts, alert rules, monitored targets, PromQL metrics, and Loki, Elasticsearch, or OpenSearch logs.
---

# Nightingale 数据查询

## 核心原则

按“先定位、再下钻、后验证、最后归纳”查询。先用列表工具和服务端过滤缩小范围，再对少量候选项查详情；从告警详情中复用 `datasource_id`、`group_id`、`rule_id`、`prom_ql`、标签和触发时间，最后才查询指标或日志。

这组 MCP 工具只有读取能力。不要声称已确认、关闭、屏蔽或修改任何告警与规则。

需要精确参数或返回字段时，读取 [references/tool-reference.md](references/tool-reference.md)。调用 `query_logs`、ES/OS 索引与字段工具前必须读取其中的日志约束。查 Elasticsearch 时再读 [references/elasticsearch-example.md](references/elasticsearch-example.md)：先发现数据源和索引，不要套用示例里的索引名。

## 查询前先确定

1. 明确用户要查的是当前状态、历史事件、规则配置、主机、指标还是日志。
2. 明确时间范围和时区。相对时间优先使用 `hours`；精确窗口转换为 Unix 秒。回复时再转换为用户可读的绝对时间。
3. 提取已有的事件 ID、规则 ID、业务组 ID、数据源 ID、主机标识、标签和查询表达式。
4. 选择能回答问题的最短调用链。缺少 ID 时先尝试从告警列表或详情获得；仍无法获得时再向用户索取，不要猜测。

## 工具选择

| 目的 | 首选工具 | 使用要点 |
|---|---|---|
| 查看正在触发的告警 | `list_active_alerts` | 用 `hours`、`severity`、`query`、`bgid`、`rid` 等尽早过滤 |
| 查看某条当前告警详情 | `get_active_alert` | 已有精确 `eid` 时直接调用 |
| 回溯已发生的告警 | `list_history_alerts` | 使用明确时间窗，按严重级别、恢复状态或业务组收敛 |
| 查看某条历史告警详情 | `get_history_alert` | 详情比列表多恢复状态和恢复时间 |
| 查看业务组中的规则 | `list_alert_rules` | 必须已有 `group_id`；按页取结果 |
| 查看规则配置 | `get_alert_rule` | 必须已有 `arid`，通常来自告警的 `rule_id` |
| 搜索主机或离线目标 | `list_targets` | `downtime` 单位是秒；`query` 匹配 ident/tags |
| 查询当前或某时刻指标值 | `query_instant` | 必须已有 Prometheus 数据源 ID 和 PromQL |
| 查询指标趋势 | `query_range` | 时间均为 Unix 秒；优先让工具自动计算 `step` |
| 查询 Loki/ES/OS 日志 | `query_logs` | `body` 是 Nightingale 原生透传请求体，禁止臆造字段 |
| 发现 ES/OS 索引 | `list_log_indices` | 只适用于 ES/OS；OpenSearch 必须传 `engine: "os"` |
| 发现 ES/OS 字段 | `list_log_fields` | 先确定索引；`body` 中提供数据源 ID 和索引名 |

## 标准工作流

### 告警调查

1. 调用 `list_active_alerts`，默认从小结果集开始，例如 `limit: 20, p: 1`。
2. 只对最相关的 1 至 3 条事件调用 `get_active_alert`。
3. 需要核对阈值、持续时间或启停状态时，用事件中的 `rule_id` 调用 `get_alert_rule`。
4. 需要判断影响主机时，用 `target_ident`、标签或 `group_id` 调用 `list_targets`。
5. 需要验证触发值或趋势时，复用详情中的 `datasource_id` 和 `prom_ql` 调用指标工具。
6. 需要寻找同一时段的错误日志时，以告警的 `trigger_time` 为中心构造窄时间窗，再使用已经确认的数据源和 Nightingale 日志请求体。

### 历史复盘

1. 先用 `list_history_alerts` 查询明确时间窗，不要一次拉取无限历史。
2. 根据 `total` 决定是否翻页；达到用户需要的证据量后停止。
3. 对代表性事件调用 `get_history_alert`，比较 `trigger_time`、`recover_time`、`severity`、标签和触发值。
4. 需要判断重复告警时，按 `rule_id`、`target_ident` 或稳定标签分组，不要只按展示名称判断。

### 指标查询

1. 用户问“现在、当前、某个时刻”时用 `query_instant`；问“趋势、峰值、何时开始、持续多久”时用 `query_range`。
2. 优先复用告警或规则返回的 PromQL。没有指标名、标签或数据源 ID 时，不要用猜测式查询消耗调用次数。
3. 在 PromQL 端先过滤和聚合：限定主机、集群或服务标签，并用 `sum by`、`avg by`、`max by`、`topk` 等控制序列基数。
4. `query_range` 通常省略 `step`，按分析所需设置合理 `max_points`；概览常用 200 至 500，细查再提高。
5. 读取返回的 `effective_step`。`truncated: true` 表示用户提供的步长过细而被增大。

### 日志查询

1. 先 `list_datasources` 确认引擎和 ID，不要猜测。ES/OS 不确定索引时 `list_log_indices`，不确定字段时 `list_log_fields`。Loki 不使用这两个工具。
2. `query_logs.body` 必须是目标 Nightingale 版本实际接受的 `/api/n9e/logs-query` 请求体。优先复用已验证结构或夜莺页面网络请求，不要把某一引擎的格式套到另一引擎。
3. 外层 `start`、`end` 只用于检查是否超过 7 天，不会写入 `body`。实际查询时间必须同时写入 `body.query[]`。
4. 对 Elasticsearch，实际命中条数上限位于 `body.query[].limit`。从 5 至 30 分钟的窄窗口和具体服务、主机、trace ID 或错误词开始。

## 效率规则

- 已有精确 ID 就直接使用 `get_*`，否则先 `list_*` 再查少量详情。
- 列表调用始终显式给出小 `limit` 和 `p: 1`。仅在 `p * limit < total` 且下一页仍有助于回答时翻页。
- 优先在工具参数中筛选，不要先拉取大量 JSON 再在上下文里过滤。
- 当前告警与历史告警分开查询。当前未恢复问题优先使用 `list_active_alerts`。
- 不要为了“全面”把所有工具都调一遍。不要原样输出大段时序点或日志。

## 重要边界

- 活跃告警的 `severity` 是逗号分隔字符串，例如 `"1,2"`；历史告警的 `severity` 是单个整数。
- 告警的 `hours` 与 `stime`/`etime` 互斥。时间戳单位是秒，不是毫秒。
- 当前实现会省略历史查询中的 `is_recovered: 0`，因此不能可靠地用它筛选“未恢复历史告警”。
- `list_alert_rules` 必须已有 `group_id`；可先用 `list_busi_groups`。
- 空列表或空向量只表示该查询没有匹配数据，不等于系统健康。
- `query_logs` 对引擎请求体不做结构校验；调用成功也不代表查询条件符合用户意图。

## 回复规范

先给结论，再给必要证据：数量、严重级别、对象、事件或规则 ID、触发/恢复时间。指标注明 PromQL 和数据源 ID；日志注明引擎、索引和窗口。分清「没有匹配数据」「查询失败」「缺少上下文」。关联性写成“时间相关”或“证据支持”，不要写成已证明的因果。

## 完整示例

用户问：“排查 api-01 最近 30 分钟的 CPU 告警，看看指标是否持续异常。”

1. `list_active_alerts`：`{"hours":1,"severity":"1,2","query":"api-01","limit":20,"p":1}`
2. 对最相关事件 `get_active_alert`。
3. 复用详情里的 `datasource_id` 与 PromQL，对用户指定窗口调用 `query_range`（省略 `step`）。
4. 汇总：告警是否仍活跃、窗口内指标范围、`effective_step`、事件 ID。无匹配序列时报告“该 PromQL 在该数据源和窗口内无匹配数据”，不要报告“CPU 正常”。
