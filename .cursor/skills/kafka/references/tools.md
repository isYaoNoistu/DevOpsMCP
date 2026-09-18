# 生产试用版本工具参考

| 工具 | 何时使用 | 核心参数 |
| --- | --- | --- |
| list_targets | 找本地目标 | query 可选 |
| get_target_info | 核对访问和 payload 范围 | target |
| kafka_capabilities | 核对 broker/协议能力 | target |
| kafka_topics_list | 发现白名单内 topic | target；可选 query、after、limit、include_internal |
| kafka_groups_list | 发现白名单内消费组 | target；可选 query、after、limit |
| kafka_configs_query | 查看配置和来源 | target、resource_type=topic/broker、resource_names；可选 config_keys、include_synonyms |
| kafka_topic_inspect | 查看分区 leader/replicas/ISR | target、topic |
| kafka_group_inspect | 查看组成员、分配和提交位置 | target、group；可选 topics（最多 20 个精确白名单名称） |
| kafka_offsets_query | 查看分区 offset 边界 | target、topic、partition；可选 timestamp_ms |
| kafka_records_peek | 有界抽样 | target、topic、partition；start_offset 与 timestamp_ms 二选一 |

configs 的 resource_names 为 1–10 个精确名称，broker 资源名为数字 ID，config_keys 最多 30；省略或空列表均查询全部可见配置键。工具中的精确 topic/group 参数不接受通配查询；通配符只属于本地 target 白名单配置。对象发现使用两个 list 工具。

两个列表的 limit 默认 50、最大 200；query 是区分大小写的名称子串，最多 256 UTF-8 字节。after 使用返回的 next_after，是上一页最后一个名称的排他游标。先过滤白名单再排序和分页；每次请求是新的非原子快照，不保证翻页期间资源不变。topics 的 include_internal 默认 false，打开后仍遵守白名单。列表不是 broker 端分页：topic 发现获取全 topic 元数据，组发现最多扫描 100 个 broker、最多 4 路并发，单 broker 超时 3 秒、发现整体超时 12 秒。单个 broker 响应读取限制为 8 MiB，超限会失败或给出部分结果；limit 不是网络流量或内存上限。检查 has_more/next_after、partial/truncated、broker_errors，不把返回条目当作全量清单。

group 的 topics 显式提供时覆盖从当前分配或精确白名单推断的 offset 范围；不会查询这些 topic 之外的提交位置，也不表示完整历史订阅。无已提交位置时 committed_offset=-1、commit_status=no_committed_offset，不是 offset=0 或 lag=0。范围过大时缩小 topics，不退回无界 offset 查询。

消费组名称允许冒号、斜杠等字符，不采用 Topic 的字符限制。group_type 区分 classic/consumer 协议；查询会按协调器能力兼容新版接口。capabilities 只代表一个可达 broker 的协议能力，不能推断所有节点版本一致。

peek 的 max_records 默认 5、最大 20；max_bytes 默认 16384、最大 65536，限制记录 JSON 总字节数（含元数据与已启用的 payload），不是网络流量或内存总额。include_value 默认 false，同时控制输出 key/headers/value；关闭它时原始记录仍通过 Fetch 进入本地进程。内容以可逆 base64 返回，不是脱敏或加密；单条原始内容超过 16384 字节会省略。isolation_level 默认 read_committed，可选 read_uncommitted。不加入组、不自动提交、不创建 topic。整体 broker 查询超时为 20 秒。同一进程同一 target 最多 2 个并发 broker 操作，超额立即返回 busy；等待现有请求完成后再考虑重试，不紧循环。

示例参数：`{"target":"local-dev","topic":"demo-events","partition":0,"timestamp_ms":1700000000000}`。peek 可在同一参数中加入 `"max_records":5,"include_value":false`；不要再同时提供 start_offset。

响应外层含 target、operation、sampled_at、scope、status、truncated、data。继续检查 data 内资源/分区错误和截断信息；API 不支持与权限不足分开报告。响应过大需缩小资源/config_keys，不重试全量扫描。目标文件重载失败不会保留旧权限。

部署和本地配置仅参考 [模块 README](../../../../kafka-mcp-server/README.md)。不把真实 brokers、密码或消息样本写入仓库，也不自动安装/修改活动 MCP 配置。
