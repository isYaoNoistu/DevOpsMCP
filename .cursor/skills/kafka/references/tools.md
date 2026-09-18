# V1 工具参考

| 工具 | 何时使用 | 核心参数 |
| --- | --- | --- |
| list_targets | 找本地目标 | query 可选 |
| get_target_info | 核对访问和 payload 范围 | target |
| kafka_capabilities | 核对 broker/协议能力 | target |
| kafka_configs_query | 查看配置和来源 | target、resource_type=topic/broker、resource_names；可选 config_keys、include_synonyms |
| kafka_topic_inspect | 查看分区 leader/replicas/ISR | target、topic |
| kafka_group_inspect | 查看组成员、分配和提交位置 | target、group |
| kafka_offsets_query | 查看分区 offset 边界 | target、topic、partition；可选 timestamp_ms |
| kafka_records_peek | 有界抽样 | target、topic、partition；start_offset 与 timestamp_ms 二选一 |

configs 的 resource_names 为 1–10 个精确名称，broker 资源名为数字 ID，config_keys 最多 30。工具不支持任意 topic/group 通配扫描；通配符只属于本地 target 白名单配置。

消费组名称允许冒号、斜杠等字符，不采用 Topic 的字符限制。group_type 区分 classic/consumer 协议；查询会按协调器能力兼容新版接口。capabilities 只代表一个可达 broker 的协议能力，不能推断所有节点版本一致。

peek 的 max_records 默认 5、最大 20；max_bytes 默认 16384、最大 65536，限制记录 JSON 总字节数（含元数据与已启用的 payload），不是网络流量总额。include_value 默认 false，同时控制 key/headers/value，内容以 base64 返回；单条原始内容超过 16384 字节会省略。isolation_level 默认 read_committed，可选 read_uncommitted。不加入组、不自动提交、不创建 topic。整体 broker 查询超时为 20 秒。

示例参数：`{"target":"local-dev","topic":"demo-events","partition":0,"timestamp_ms":1700000000000}`。peek 可在同一参数中加入 `"max_records":5,"include_value":false`；不要再同时提供 start_offset。

响应外层含 target、operation、sampled_at、scope、status、truncated、data。继续检查 data 内资源/分区错误和截断信息；API 不支持与权限不足分开报告。响应过大需缩小资源/config_keys，不重试全量扫描。目标文件重载失败不会保留旧权限。

部署和本地配置仅参考 [模块 README](../../../../kafka-mcp-server/README.md)。不把真实 brokers、密码或消息样本写入仓库，也不自动安装/修改活动 MCP 配置。
