# Elasticsearch 日志查询（示例）

本文件说明夜莺 MCP 查 **Elasticsearch** 时的通用约束。索引名、字段名、时区以**你环境**的 `list_datasources` / `list_log_indices` / `list_log_fields` 为准。

下面的 `app-logs-YYYY.MM.DD`、`app-api` 只是占位，不要直接拿去查别人的集群。

## 发现顺序

1. `list_datasources`：记下 Elasticsearch 的 `id` 和 `cate`（应为 `elasticsearch`）。
2. `list_log_indices`：`body` 至少 `{"cate":"elasticsearch","datasource_id":<id>}`。
3. `list_log_fields`：`body` 再加 `index`。
4. `query_logs`：`body` 用夜莺 `/api/n9e/logs-query` 原生结构。

Loki 不要走索引/字段这两个工具。OpenSearch 传 `engine: "os"`。

## 请求体骨架

```json
{
  "cate": "elasticsearch",
  "datasource_id": 12,
  "query": [
    {
      "index": "app-logs-2026.09.08",
      "filter": "service.name:\"app-api\" AND log.level:ERROR",
      "start": 1788810000,
      "end": 1788811800,
      "limit": 50,
      "page": 0,
      "ascending": false
    }
  ]
}
```

要点：

- 必须带 `cate`。只传 `datasource_id` 常会得到 `cluster not exists`。
- 外层 MCP 的 `start`/`end` **不会**写入 `body`。查询时间写在 `body.query[]`。
- ES 真正的条数上限是 `body.query[].limit`，不是根上的 `body.limit`。
- `page` 是从 0 开始的偏移（`limit=50` 时下一页 `page=50`），不是页码。
- 窄窗口优先用精确日索引；日期未知再用通配 `app-logs-*` 这类模式（以你们 ILM 为准）。

## 过滤

字段是否存在先看 `list_log_fields`。常见（**不保证你们集群也有**）：

- 时间：`@timestamp`
- 服务：`service.name` / `app`
- 级别：`log.level`
- 正文：`message`
- 主机：`host.name`

从具体服务 + ERROR + 5～30 分钟窗口开始，结果不足再放宽。

## 判读

- 空 `data` 或夜莺返回 `no data`：先当「当前条件无命中」，核对索引、时间字段、时区、过滤，不要直接判基础设施故障。
- 索引不存在 和 索引存在但零命中 必须分开说。
- 不要把 Loki 的 LogQL 或 OpenSearch 专用字段套到 ES 上。
