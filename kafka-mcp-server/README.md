# Kafka MCP V1

独立 Go stdio MCP，提供 8 个只读 Kafka 诊断工具。源码、示例和技能均在 DevOpsMCP；本模块不迁移到 cicd，不修改正在使用的 MCP 配置。没有生产消息、提交/重置 offset、创建/删除 topic、修改配置或 ACL 的工具。

## 构建与离线验收

需要 Go 1.26 或更新版本；首次构建需要下载 go.mod 中的依赖。在模块目录执行：

```powershell
go test ./...
go vet ./...
./scripts/build.ps1
py -3 ./scripts/smoke.py
```

构建脚本从模块路径推导仓库外输出目录，本工作区为 `D:/project/CICD/dist/devopsmcp-dev-windows-amd64/kafka-mcp-server.exe`，关闭 CGO 并附带许可证，只构建当前模块，不清理其他产物。smoke 使用临时的 localhost 配置，执行 MCP initialize、tools/list 和 list_targets；不会连接 Kafka。离线成功只说明本地加载和 stdio 协议可用。

自动化测试使用 kfake 协议模拟服务，覆盖不自动建 Topic、不加入消费组、不提交 offset、保留部分查询结果，以及实际模拟事务提交/回滚后 `read_committed` 排除回滚消息。它不等于真实 Kafka 验收。当前环境 Docker daemon 不可用，尚无真实 Kafka 集群运行验收；认证、ACL、TLS、事务隔离和实际 broker 兼容性仍需在获准环境验证。

## 本地 target 文件

参考 [明文开发示例](examples/targets.dev.json) 或 [TLS + SASL SCRAM 示例](examples/targets.tls-scram.json)。实际文件自行保存在仓库外，例如 `D:/project/CICD/.codex/kafka-targets.json`；这些说明不会创建该文件。SASL 密码只在本机私有 JSON 中填写，不提交，不粘贴到聊天。示例密码是不可用的占位符，不支持环境变量插值。限制文件的操作系统读取权限。

- 顶层为 `{"targets": [...]}`，最多 100 个 target、文件最多 1 MiB。name 唯一，使用字母、数字、点、下划线和连字符。
- brokers 为 host:port；TLS 支持 ca_file、cert_file/key_file 和 server_name，证书与私钥必须成对。证书路径建议使用绝对路径；未配置 CA 时使用系统信任。不会关闭证书验证。
- SASL 支持 `PLAIN`、`SCRAM-SHA-256`、`SCRAM-SHA-512`。远程凭据连接应使用 TLS。
- topics 和 groups 都必须是非空白名单，缺省或空数组会拒绝配置；不需要限制范围时，两项均填写 `["*"]`，无需逐个列出名称。它们是 MCP 本地查询范围，与 Kafka 是否启用账号认证无关。
- Topic 名称使用字母、数字、点、下划线和连字符；消费组名称独立校验，支持 `reader:uat`、`team/reader` 等名称（非空、最多 255 UTF-8 字节、不含控制字符）。白名单只有 `*` 表示通配；消费组中的 `?`、`[]` 等其他符号按字面匹配，不表示字符集合。
- allow_payload 默认 false。即使本地设为 true，每次采样仍须显式 `include_value: true`，此开关同时暴露 key、headers 和 value，不只是 value。
- 每次工具调用重新读取 target 文件；无效更新会拒绝查询，不回退到旧权限。公开信息不返回 SASL 用户名、密码或证书路径。broker ACL 仍决定服务端权限，本地白名单不是 broker 权限替代品。

## Codex 开发接入示例

在 Codex 的 MCP 设置中新增 stdio 服务 `kafka-dev`，Command 填写以下二进制绝对路径，Arguments 留空，环境变量按下表填写；保存后重新连接并先调用 list_targets。界面名称可能随版本变化。这里仅给出配置示例，不安装技能或修改当前工作区配置。

| 字段 | 值 |
| --- | --- |
| Command | `D:/project/CICD/dist/devopsmcp-dev-windows-amd64/kafka-mcp-server.exe` |
| KAFKA_TARGETS_FILE | `D:/project/CICD/.codex/kafka-targets.json` |
| KAFKA_MCP_READ_ONLY | `true` |

对应 TOML：

```toml
[mcp_servers.kafka-dev]
command = "D:/project/CICD/dist/devopsmcp-dev-windows-amd64/kafka-mcp-server.exe"

[mcp_servers.kafka-dev.env]
KAFKA_TARGETS_FILE = "D:/project/CICD/.codex/kafka-targets.json"
KAFKA_MCP_READ_ONLY = "true"
```

KAFKA_TARGETS_FILE 必填；KAFKA_MCP_READ_ONLY 缺省即 true，设置其他值会拒绝启动，不能借此打开写能力。诊断日志写 stderr；stdout 保留给 MCP。`--version` 可单独查询版本。

## 工具与边界

| 工具 | 输入和用途 |
| --- | --- |
| list_targets | 可选 query，按名称或描述筛选本地 target；不连接 broker |
| get_target_info | target；检查本地白名单和 payload 策略 |
| kafka_capabilities | target；查询集群元数据和协议能力；支持某 API 不代表有调用权限 |
| kafka_configs_query | target、resource_type（topic/broker）、resource_names（1–10 个精确名称）；broker 用数字节点 ID；可选 config_keys（最多 30）和 include_synonyms；返回配置来源，隐藏敏感值 |
| kafka_topic_inspect | target、topic；指定 topic 的 leader、replicas、ISR；不自动创建 topic |
| kafka_group_inspect | target、group；成员、分区分配和已提交 offset；不暴露应用处理位置、线程栈或业务代码 |
| kafka_offsets_query | target、topic、partition；查询 earliest、high watermark、last stable offset；可选 timestamp_ms |
| kafka_records_peek | target、topic、partition；start_offset 或 timestamp_ms 二选一；有界手动分区采样，不加入消费组、不提交 offset |

工具参数示例见 [tool-calls.json](examples/tool-calls.json)。所有时间戳参数为 **Unix 毫秒**，不是秒。HW 是 high watermark，LSO 是 last stable offset；二者都不能称为 LEO（leader 本地日志末端）。offset 差值也不是精确消息条数：压缩、删除、事务与记录空洞会影响解释。committed offset 表示消费组提交边界，不等于此刻应用已经处理的位置。

peek 默认 max_records=5（上限 20）、max_bytes=16384（上限 65536）、isolation_level=read_committed；可显式选择 read_uncommitted。max_bytes 限制序列化后的记录 JSON 总字节数（包含元数据和已启用的 payload），不是 Kafka 网络流量上限。单条原始 key/headers/value 合计超过 16384 字节时省略内容；启用的内容以 base64 返回。默认只看元数据，隐藏 key/headers/value；空采样不能证明 topic 无数据。每次 broker 工具调用有 20 秒整体超时，响应超过 256 KiB 会返回 response_too_large，需缩小查询。

响应包含 target、operation、sampled_at（UTC）、scope、status、truncated 和 data。读取 data 中的分区/资源错误以及截断信息，不能只看外层 status。权限不足、超时、目标不可用与不支持 API 都不是业务系统正常的证据。诊断按“证据 → 字段含义 → 分析”组织：先用现有夜莺指标确认时间窗，再定位 group/topic/config/offset，最后关联获准的主机和应用日志。

能力查询会在连接失败时切换到其他可用 broker，返回的是一个可达 broker 的协议能力，不代表所有节点版本一致。消费组查询兼容 classic 和新版 consumer 协议，结果中的 `group_type` 区分两者；classic 查询返回 Dead 或组不存在时，先检查协调器是否支持新版接口，再补充查询。权限错误或连接故障不会被当作组不存在。
