# Kafka MCP 0.2.0-trial

本次验证结果及尚未覆盖的生产条件见 [验收记录](VALIDATION.md)。

独立 Go stdio MCP，提供 10 个只读 Kafka 诊断工具。源码、示例和技能均在 DevOpsMCP；本模块不迁移到 cicd，不修改正在使用的 MCP 配置。没有生产消息、提交/重置 offset、创建/删除 topic、修改配置或 ACL 的工具。当前为生产试用候选版本，发布包不代表已通过生产验收。

## 构建与离线验收

日常发布使用仓库 deploy：Linux 执行 `bash pack-linux.sh`，Windows 执行 `pack-windows.cmd`，均不传参数；六服务压缩包输出到 `deploy/dist/`。详见 [打包说明](../deploy/README.md)。下方模块脚本用于单模块开发验证。

需要 Go 1.26 或更新版本；首次构建需要下载 go.mod 中的依赖。在模块目录执行：

```powershell
go test ./...
go vet ./...
./scripts/build.ps1
py -3 ./scripts/smoke.py
```

构建脚本从模块路径推导仓库外输出目录，本工作区为 `D:/project/CICD/dist/devopsmcp-windows-amd64/kafka-mcp-server.exe`，关闭 CGO，只构建当前模块，不清理其他产物。先构建独立临时文件，再替换默认路径，不生成 `.exe~` 备份；默认文件被客户端占用时保留原文件并输出带版本名的新文件，不终止现有进程。此时 smoke 使用 `--binary <新文件路径>` 指定新产物。

`--version` 输出版本、Git 短 revision（工作区有修改时附 `-dirty`）和 UTC 构建时间。随包包含可核对的 `<二进制文件名>.sha256`、`kafka-mcp-server.build-info.json`（二进制哈希、完整 commit、dirty 标记、模块源文件哈希、Go/平台及构建参数）、`kafka-mcp-licenses/` 和 `kafka-mcp-docs/`。文档包保留模块 README、公开示例和 Kafka Skill；不会复制私有 target 或凭据。dirty 构建需结合源文件哈希识别实际内容，commit 本身不足以复现未提交修改。

smoke 使用临时 localhost 配置，执行 `--version`、MCP initialize、tools/list（10 个只读工具）和 list_targets；不会连接 Kafka。离线成功只说明本地加载和 stdio 协议可用。

自动化测试使用 kfake 协议模拟服务，覆盖不自动建 Topic、不加入消费组、不提交 offset、保留部分查询结果，以及模拟事务提交/回滚后 `read_committed` 排除回滚消息。它不等于真实 Kafka 验收。试用前需在明确获准的目标上验证工具调用，并在 metadata-only peek 前后核对专用消费组提交位置不变；认证、ACL、TLS、事务隔离、活跃消费组及实际 broker 兼容性需分别记录证据。没有相应记录时不能声称这些能力已完成真实环境验收。

明确获准后，可对已存在的专用测试对象运行只读验收。以下名称均为示例，私有 target 文件必须已映射到获准环境；消费组在整个验收期间必须空闲，topic 必须有可读的已提交记录，配置测试 topic 的 retention.ms 必须等于指定期望值。脚本不会创建测试对象、生产消息、提交 offset 或修改配置：

```powershell
py -3 ./scripts/acceptance.py `
  --binary D:/project/CICD/dist/devopsmcp-windows-amd64/kafka-mcp-server.exe `
  --targets D:/project/CICD/.codex/kafka-targets.json `
  --target local-uat --topic mcp-events --group mcp-lag-group `
  --config-topic mcp-config-test --expected-retention-ms 21600000
```

它核对列表和翻页、配置查询的省略/空 config_keys 结果相同、显式 topic 范围的提交位置，以及 metadata-only peek 前后专用组 offset 不变。另查询随机生成的 `mcp-missing-readonly-*` 不存在 Topic，验证错误和禁止自动创建；测试白名单须允许该前缀。成功只覆盖本次目标和对象，不包含独立 Kafka CLI 对照或活跃消费组验收。分发包的 `kafka-mcp-docs/kafka-mcp-server/scripts/` 附带 smoke.py 和 acceptance.py；在包内运行 smoke 时须显式给出 `--binary`，构建和 Go 测试命令则在源码模块目录运行。

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
| Command | `D:/project/CICD/dist/devopsmcp-windows-amd64/kafka-mcp-server.exe` |
| KAFKA_TARGETS_FILE | `D:/project/CICD/.codex/kafka-targets.json` |
| KAFKA_MCP_READ_ONLY | `true` |

对应 TOML：

```toml
[mcp_servers.kafka-dev]
command = "D:/project/CICD/dist/devopsmcp-windows-amd64/kafka-mcp-server.exe"

[mcp_servers.kafka-dev.env]
KAFKA_TARGETS_FILE = "D:/project/CICD/.codex/kafka-targets.json"
KAFKA_MCP_READ_ONLY = "true"
```

本地文件模式下 KAFKA_TARGETS_FILE 必填；平台模式改用 KAFKA_TARGETS_JSON，见下方平台凭据章节。KAFKA_MCP_READ_ONLY 缺省即 true，设置其他值会拒绝启动，不能借此打开写能力。诊断日志写 stderr；stdout 保留给 MCP。`--version` 可单独查询版本。

## 工具与边界

| 工具 | 输入和用途 |
| --- | --- |
| list_targets | 可选 query，按名称或描述筛选本地 target；不连接 broker |
| get_target_info | target；检查本地白名单和 payload 策略 |
| kafka_capabilities | target；查询集群元数据和协议能力；支持某 API 不代表有调用权限 |
| kafka_topics_list | target；可选 query、after、limit（默认 50，最多 200）、include_internal（默认 false）；只返回白名单内 topic |
| kafka_groups_list | target；可选 query、after、limit（默认 50，最多 200）；只返回白名单内消费组 |
| kafka_configs_query | target、resource_type（topic/broker）、resource_names（1–10 个精确名称）；broker 用数字节点 ID；可选 config_keys（最多 30）和 include_synonyms；返回配置来源，隐藏敏感值 |
| kafka_topic_inspect | target、topic；指定 topic 的 leader、replicas、ISR；不自动创建 topic |
| kafka_group_inspect | target、group；可选 topics（最多 20 个白名单内精确 topic）；成员、分区分配和已提交 offset；不暴露应用处理位置、线程栈或业务代码 |
| kafka_offsets_query | target、topic、partition；查询 earliest、high watermark、last stable offset；可选 timestamp_ms |
| kafka_records_peek | target、topic、partition；start_offset 或 timestamp_ms 二选一；有界手动分区采样，不加入消费组、不提交 offset |

工具参数示例见 [tool-calls.json](examples/tool-calls.json)。所有时间戳参数为 **Unix 毫秒**，不是秒。HW 是 high watermark，LSO 是 last stable offset；二者都不能称为 LEO（leader 本地日志末端）。offset 差值也不是精确消息条数：压缩、删除、事务与记录空洞会影响解释。committed offset 表示消费组提交边界，不等于此刻应用已经处理的位置。

列表先按白名单过滤，再按名称排序、query 筛选和分页；query 为区分大小写的名称子串，最多 256 UTF-8 字节。`after` 为上一页最后一个名称（返回的 next_after），采用严格大于该名称的排他游标。每次调用重新发现，是非原子的新快照，资源变化会影响后续页；这不是 broker 端分页。limit 只限制返回条目，发现仍需请求全 topic 元数据或多个 broker 的组列表，不能据此推断网络流量或内存上限。消费组发现最多扫描元数据中的 100 个 broker，最多 4 路并发，单 broker 最多 3 秒、发现整体最多 12 秒；超出扫描范围会标记 partial/truncated。单个 broker 响应读取上限为 8 MiB，超过时会失败或标记部分发现；部分结果、截断和错误不等于全量清单。include_internal 只影响 topic 列表，仍受白名单限制。

group 的 topics 省略时由当前分配或精确 topic 白名单推断 offset 查询范围；显式提供时覆盖这个隐式范围，不自动扩展到其他 topic。它不是完整历史订阅查询。存在分区但没有已提交位置时保留 `committed_offset: -1`，并返回 `commit_status: no_committed_offset`；不能按 offset 0 或 lag 0 解读，也不会自动推导 lag。分区范围超限时应缩小 topics。

peek 默认 max_records=5（上限 20）、max_bytes=16384（上限 65536）、isolation_level=read_committed；可显式选择 read_uncommitted。max_bytes 限制序列化后的记录 JSON 总字节数（包含元数据和已启用的 payload），不是 Kafka 网络流量或进程内存上限。即使 include_value=false，Kafka Fetch 仍会将原始记录传入本地 MCP 进程；此开关只控制工具输出中的 key/headers/value。单条原始 key/headers/value 合计超过 16384 字节时省略内容；启用的内容以 base64 返回。base64 可逆，不是脱敏或加密。默认不输出 payload；空采样不能证明 topic 无数据。

每次 broker 工具调用有 20 秒整体超时，同一 MCP 进程对同一 target 最多同时执行 2 个 broker 操作，超额立即返回 busy，不排队；不同 MCP 进程不共享此限制。响应超过 256 KiB 会返回 response_too_large，需缩小查询。这些输出预算不能保证总网络流量或进程内存的数值上限。

响应包含 target、operation、sampled_at（UTC）、scope、status、truncated 和 data。读取 data 中的分区/资源错误以及截断信息，不能只看外层 status。权限不足、超时、目标不可用与不支持 API 都不是业务系统正常的证据。诊断按“证据 → 字段含义 → 分析”组织：先用现有夜莺指标确认时间窗，再定位 group/topic/config/offset，最后关联获准的主机和应用日志。

能力查询会在连接失败时切换到其他可用 broker，返回的是一个可达 broker 的协议能力，不代表所有节点版本一致。消费组查询兼容 classic 和新版 consumer 协议，结果中的 `group_type` 区分两者；classic 查询返回 Dead 或组不存在时，先检查协调器是否支持新版接口，再补充查询。权限错误或连接故障不会被当作组不存在。

## 对接 YluneMCPHub：平台凭据模式（不需要凭据文件）

平台模式通过月弦凭据中心注入环境变量，连接参数和凭据直接在内存解析，不需要 targets 文件、密码文件或密钥文件。原有文件模式继续供智能体本地直连使用。

1. 将 Linux 二进制放入月弦可执行的位置。默认 Docker 挂载 `/data/ylune-mcp` → `/opt/mcp`，这里只需放程序，不用放凭据文件。新建 STDIO 服务器，命令 `/opt/mcp/kafka-mcp-server`，参数留空。平台原生部署则填实际二进制绝对路径。
2. 在「凭据中心」新建一条凭据，填写下面两个键，绑定该服务器：

| 凭据键 | 填写内容 |
| --- | --- |
| `KAFKA_TARGETS_JSON` | 下方完整 JSON 文本，不是文件路径，也不用加外层引号 |
| `KAFKA_MCP_READ_ONLY` | `true` |

```json
{
  "targets": [
    {
      "name": "uat",
      "brokers": [
        "kafka.example.com:9092"
      ],
      "topics": [
        "*"
      ],
      "groups": [
        "*"
      ],
      "allow_payload": false
    }
  ]
}
```

JSON 中的占位内容在凭据中心替换为真实值；不提交到仓库。月弦负责加密存储和运行时注入，需要正确配置 `YLUNE_MASTER_KEY`。默认的 HOST / PORT / TOKEN 字段不会自动映射，请使用表中精确变量名。

无认证 Kafka 使用上面示例即可。启用认证时，在同一 target 增加：

```json
"sasl": {"mechanism": "SCRAM-SHA-512", "username": "mcp_ro", "password": "<kafka-password>"},
"tls": {"enabled": true, "server_name": "kafka.example.com"}
```

上述是需要合并到 target 的字段片段。私有 CA 可填 `tls.ca_pem`；双向 TLS 再填 `tls.cert_pem` 和 `tls.key_pem`，均为完整 PEM 文本（JSON 内换行写 `\n`）。使用系统信任 CA 时省略 `ca_pem`。平台模式不接受 `ca_file` / `cert_file` / `key_file`，不关闭证书验证。

3. 在该凭据的绑定处测试工具列表，再用调试台调用 `list_targets` 和一次实际只读查询，分别验证配置及上游认证。仅列出目标/工具不能证明已连接上游。
4. 在月弦用户授权中勾选该 MCP、允许的工具和绑定凭据。客户端使用月弦 Access Key，上游密码不交给智能体。

### 与本地文件模式的兼容

- 未设置 `KAFKA_TARGETS_JSON` 时，沿用 `KAFKA_TARGETS_FILE` 和原有文件配置。
- 只要设置了 `KAFKA_TARGETS_JSON`（即使为空），就优先采用平台模式；空值、格式错误、缺少必要字段均报错，不回退到文件。平台服务器无需再声明 `KAFKA_TARGETS_FILE`。
- 配置是每个 MCP 进程启动时的快照。修改/轮换凭据后，在月弦重新连接或重启对应上游进程，使新环境变量生效；不会靠修改文件热更新平台凭据。
- 建议每个环境一台服务器、一条凭据。JSON 的 targets 可以有多个目标，但同一实例授权用户可访问该清单内的目标，不会按凭据名称自动细分权限。
- 平台 JSON 最多 1 MiB、最多 100 个目标；实际还受操作系统环境变量大小限制，大型清单应拆分为独立实例。敏感字段不会进入目标列表和配置错误文本，也不会由 MCP 写入临时凭据文件。
