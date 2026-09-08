<div align="center">

# DevOpsMCP

**值班运维的只读 MCP** — 夜莺告警 · Jenkins 发版 · PostgreSQL 排障

<p><b>简体中文</b> · <a href="README.en.md">English</a></p>

在 Cursor 里用自然语言查，不必在监控台、Jenkins 和控制台之间来回切。  
进程跑在**你的电脑**上，直连已有系统。凭据留在本机，仓库里没有 Token、没有密码、没有真实主机名。

<p>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?labelColor=1f2937" alt="Apache 2.0"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white&labelColor=1f2937" alt="Go 1.23+"></a>
  <a href="https://modelcontextprotocol.io/"><img src="https://img.shields.io/badge/MCP-stdio-7c3aed?labelColor=1f2937" alt="MCP stdio"></a>
  <img src="https://img.shields.io/badge/default-read--only-059669?labelColor=1f2937" alt="read-only">
</p>

<p>
  <b><a href="#快速开始">快速开始</a></b> ·
  <a href="#它做什么">它做什么</a> ·
  <a href="#为什么是夜莺而不是-prometheus--elasticsearch">为什么是夜莺</a> ·
  <a href="#怎么工作">怎么工作</a> ·
  <a href="#三个服务">三个服务</a> ·
  <a href="#安全模型">安全</a> ·
  <a href="#测试阶段与免责">测试与免责</a> ·
  <a href="#什么时候用--什么时候不用">适用边界</a> ·
  <a href="#文档">文档</a>
</p>

</div>

---

告警炸了、发版红了、库卡住了：真正耗时间的往往不是「不会看」，而是**入口散、上下文丢、手慢一步**。DevOpsMCP 把三套只读诊断 API 交给已经在看告警群的 Agent，并用 Skill 把调用顺序钉死——先列表后详情，先 stage 后 Console，先 target 再查库。

| 现场 | 没有 MCP | 有 DevOpsMCP |
| --- | --- | --- |
| 告警 | 夜莺列表 → 规则 → 主机 → PromQL → 再切日志平台 | 对话：当前告警 → 复用 `rule_id` / PromQL → 指标和日志 |
| 发版失败 | 打开 Jenkins，翻几千行 Console，群里贴日志 | 定位 Job → 失败 stage → 搜 `ERROR`，结论带回构建号 |
| 数据库卡住 | 找 DBA，或连错库、跑了不该跑的 SQL | 按 target 只读看会话、阻塞树、慢 SQL |

## 它做什么

- **夜莺 MCP** — 基于 [n9e 官方开源 MCP](https://github.com/n9e/n9e-mcp-server) 二次开发：只留 stdio 与 6 个只读 toolset（16 个工具），查活跃/历史告警、规则、主机、PromQL、Loki / Elasticsearch / OpenSearch 日志。去掉上游的 HTTP 模式、写工具、用户/看板/屏蔽等未使用包。
- **Jenkins MCP** — 18 个只读工具：Job、失败构建、Pipeline stage、控制台尾部与搜索、队列、节点。不触发、不停止、不跑 Groovy。
- **PostgreSQL MCP** — 19 个只读工具：一个进程、多实例 `target` 清单；会话/锁/统计/复制 + 一条受控 `SELECT` 逃生口。库变多只改本机 JSON，不增加 Tool。
- **Cursor Skill + Rule** — 仓库自带查询技能和硬性只读约定，换一个值班的人，Agent 也不会把所有工具打一遍。
- **本机实验室** — `lab/postgres` 用 Docker 拉起一次性 PostgreSQL，按清单验收 19 个工具和 SQL 护栏。

不是 MCP Hub，不是再部署一套 HTTP 网关，也不是 CMDB。高频问题有专用 Tool；PostgreSQL 用 guard 过的 `query_postgres`，而不是为每个系统视图再造一个接口。

## 为什么是夜莺，而不是 Prometheus / Elasticsearch

值班要看的往往不是「某一个 Prometheus」或「某一个 Elasticsearch」，而是**已经对齐过的告警、指标、日志**：这条规则打在哪台机器上、PromQL 是什么、同一时刻日志里有没有对应错误。夜莺本身就是这块的汇聚层——告警在它上面，PromQL 在它上面查，Loki / Elasticsearch / OpenSearch 也可以作为数据源接进去。

所以这里做的是**夜莺 MCP**，不是再给 Prometheus、Elasticsearch、Loki、VictoriaMetrics 各写一套 MCP：

- 根上每接一种存储就多一个进程、一套鉴权、一套字段方言；Agent 还要自己把告警 ID、指标标签、日志索引对上。夜莺已经做过这层对齐。
- 新数据源优先在**夜莺里接入**（Prometheus、VictoriaMetrics、Elasticsearch、Loki、OpenSearch 等，夜莺支持的种类很多）。接好之后，本 MCP 仍走同一套只读工具：告警、主机、数据源列表、`query_instant` / `query_range`、`query_logs`。不必为每种后端再开一个 MCP。
- MCP 只调夜莺 API，不直连底层库。Token 是夜莺用户 Token，能看到的业务组和数据源范围跟这个账号在夜莺里的授权一致。

Jenkins、PostgreSQL 没有等价的「值班汇聚台」，所以另外两套 MCP 才直连平台本身。监控侧已经有夜莺，就不要在根上再堆一套。

## 怎么工作

```
  值班同学  →  Cursor / 任意 MCP 客户端
                    │  自然语言：「charging 生产红了」「查 idle in transaction」
                    ▼
         ┌──────────────────────────────────────────┐
         │  本机 stdio MCP（你的电脑，不进机房）      │
         │  nightingale · jenkins · postgres        │
         │  Skill 规定顺序 · Rule 禁止声称已写过      │
         └──────────────────────────────────────────┘
              │              │               │
              ▼              ▼               ▼
         夜莺 API        Jenkins REST      PostgreSQL
         X-User-Token    用户名+API Token    mcp_ro + pgpass / 凭据管理器
```

- **凭据不出仓**：`mcp.json` 只放本机；PostgreSQL 的 targets JSON **禁止** `password` 字段。
- **二进制默认无写工具**：没有 `trigger_build`、没有改告警规则、没有 `VACUUM` / `pg_terminate_backend`。
- **平台账号仍是底线**：Jenkins 只要 Overall/Read + Job/Read；PostgreSQL 用 `mcp_ro` + 角色级 `READ ONLY`。
- **改 targets 不用重载 MCP**：PostgreSQL 按文件 mtime 热加载；改二进制或 `mcp.json` 仍要在 Cursor 面板重载。

本机同时开三个服务时，工具数可能超过 Cursor 社区版常见上限（约 40）。查某一类问题时，关掉另外两个即可。

## 快速开始

需要 **Go 1.23+**。二进制不入库，自己编。

```bash
git clone https://github.com/isYaoNoistu/DevOpsMCP.git
cd DevOpsMCP

# Windows
go build -o n9e-mcp-server/n9e-mcp-server.exe ./n9e-mcp-server/cmd/n9e-mcp-server/
go build -o jenkins-mcp-server/jenkins-mcp-server.exe ./jenkins-mcp-server/
go build -o postgres-mcp-server/postgres-mcp-server.exe ./postgres-mcp-server/

# Linux / macOS：去掉 .exe
```

拷贝 [`examples/mcp.json.example`](examples/mcp.json.example) 到用户级 `~/.cursor/mcp.json`，把 `command` 换成**绝对路径**（Windows 指向 `.exe`），把 URL / Token 换成你们环境的。

| 服务 | 你要准备的凭据 | 怎么拿 |
| --- | --- | --- |
| 夜莺 | `N9E_BASE_URL` + `N9E_TOKEN` | 用户登录 → 个人设置 → Token 管理。步骤：[n9e README](n9e-mcp-server/README.md#如何拿到夜莺-token) |
| Jenkins | `JENKINS_URL` + 只读用户 + API Token | 建用户 → 只给 Read → 该用户 Configure 里生成 Token。[jenkins README](jenkins-mcp-server/README.md#如何创建-jenkins-只读用户和-api-token) |
| PostgreSQL | `PG_TARGETS_FILE` + `mcp_ro` 口令 | 超级用户执行建角色 SQL；口令进凭据管理器或 pgpass，**不要**写进 targets。[postgres README](postgres-mcp-server/README.md#用-postgres-超级用户创建-mcp_ro) · [配置详解](postgres-mcp-server/docs/configuration.md) |

用 Cursor **打开本仓库**后，`.cursor/skills` 与 `.cursor/rules` 会跟着工作区生效。MCP 若装在别的运维仓，把 `.cursor/skills/{nightingale,jenkins,postgres}` 拷过去。

改完 `mcp.json` 或重新 `go build` 后，在 MCP 面板 **重载** 对应服务。

不连真实系统、只核对接手：各目录 `scripts/smoke-stdio.ps1`。PostgreSQL 从零验收 19 个工具：[lab/postgres](lab/postgres/README.md)（单独一份 `postgres-targets.lab.json`，不覆盖你已有的生产清单）。

## 三个服务

| | 夜莺 | Jenkins | PostgreSQL |
| --- | --- | --- | --- |
| 目录 | [n9e-mcp-server](n9e-mcp-server/README.md) | [jenkins-mcp-server](jenkins-mcp-server/README.md) | [postgres-mcp-server](postgres-mcp-server/README.md) |
| Cursor 名 | `nightingale` | `jenkins` | `postgres` |
| 工具数 | 16 | 18 | 19 |
| 鉴权 | `X-User-Token` | HTTP Basic（用户名 + API Token） | `mcp_ro` + 凭据管理器 / pgpass |
| 典型顺序 | 告警列表 → 详情 → 复用 PromQL / 日志 body | `list_jobs` → 构建 → stage → 搜 Console | `list_targets` → 专用诊断 → 不够再用 `query_postgres` |
| Skill | [nightingale](.cursor/skills/nightingale/SKILL.md) | [jenkins](.cursor/skills/jenkins/SKILL.md) | [postgres](.cursor/skills/postgres/SKILL.md) |

夜莺 MCP 基于 [n9e/n9e-mcp-server](https://github.com/n9e/n9e-mcp-server)（Flashcat Nightingale 官方开源 MCP）二次开发，只留只读 toolset。Jenkins 从 [jenkins-mcp-go](https://github.com/2001adarsh/jenkins-mcp-go) 裁过，去掉触发/停止/Groovy/整包控制台路径（控制台默认最后 500 行、最多 2000 行）。许可证与上游说明见 [NOTICE](NOTICE)。

Skill 里的 Job 名、库名、索引名都是**虚构示例**（`team/prod/checkout-api`、`orders-prod`、`app-logs-*`）。接到你们环境后用 `list_*` 发现，不要猜 ID。

## 安全模型

```
对话授权（未明确要求写操作 → 不调用写工具）
        +
二进制默认不注册写工具
        +
平台账号最小权限
        +
仓库禁止 Token / 密码 / 真实主机名
```

PostgreSQL 额外约束：

- targets 出现 `password` 字段 → 直接加载失败
- 生产 target（`environment` / `tags` / 名称后缀为 prod）默认要求 `sslmode=verify-full`，禁止 `prefer` / `disable`
- `query_postgres` 只接受单条 `SELECT` / `WITH`，拦截 `dblink*`、advisory lock、多语句；连接还池前 `DISCARD ALL`
- 生产上 `explain_query analyze=true` 会被拒绝

Agent 不得声称已经重跑构建、屏蔽告警、杀掉会话或改过数据。规则见 [`.cursor/rules/devops-mcp.mdc`](.cursor/rules/devops-mcp.mdc)。

## 测试阶段与免责

本仓库尚在**测试阶段**：接口、工具范围、Skill、默认只读边界都可能改。按 [Apache License 2.0](LICENSE) 以「按现状」提供，**不构成对任何生产环境的承诺或担保**。

你自行编译、配置、接入真实夜莺 / Jenkins / PostgreSQL 之后，因误用、凭据配错、Agent 幻觉、上游 API 变更、网络或平台故障导致的查询失败、误判、数据泄露或业务影响，**由使用者自行承担，与作者及贡献者无关**。上生产前请用只读账号、在你们自己的环境做验收；出了问题先查本机 `mcp.json`、平台权限和上游服务，而不是默认是本仓库的锅。

本发行版**默认不注册写工具**。若你基于本仓库或上游再做二次开发、把写操作加回去（改告警规则、屏蔽、触发构建、跑 Groovy、`VACUUM`、杀会话等）：

- 必须自己设计**最小权限**（独立账号、只开需要的角色，不要用超管 Token）
- 必须自己接**审批与审计**（谁在何时通过 Agent 改了什么，要能追到人）
- 必须自己评估 Agent 误调用写工具的后果；写坏生产是你的变更，不是本仓库默认行为

不要把「仓库里能编过」理解成「可以放心写生产」。需要变更时走你们现有的变更流程，而不是把写权限交给 MCP。

## 什么时候用 · 什么时候不用

**适合**：夜莺 + Jenkins + PostgreSQL 已经在跑；值班要用自然语言把「告警 / 红构建 / 锁等待」串起来；希望 Agent **查得了、动不了** 生产。

**不适合**：还没有这三套系统；需要 MCP 去点「立即构建」或改告警规则（请走你们现有的变更流程）；短问答、不涉及线上状态（用不着 MCP）。

和自己写巡检脚本的差别：脚本适合固定巡检；值班问题每次不一样。MCP 把只读客户端交给 Agent，并用 Skill 避免一次对话灌进整包 Console。不想用 Cursor 时，三个二进制仍是标准 stdio MCP，可挂到任何 MCP 客户端。

## 文档

| 先看这个 | 再往下 |
| --- | --- |
| [夜莺：拿 Token](n9e-mcp-server/README.md#如何拿到夜莺-token) | [夜莺工具表与 mcp.json](n9e-mcp-server/README.md) |
| [Jenkins：建用户 + API Token](jenkins-mcp-server/README.md#如何创建-jenkins-只读用户和-api-token) | [Jenkins 工具表与排障顺序](jenkins-mcp-server/README.md) |
| [PostgreSQL：用 postgres 创建 mcp_ro](postgres-mcp-server/README.md#用-postgres-超级用户创建-mcp_ro) | [targets / pgpass 原理](postgres-mcp-server/docs/configuration.md) |
| [Docker 实验室验收清单](lab/postgres/README.md) | [Skill：postgres](.cursor/skills/postgres/SKILL.md) |
| [mcp.json 样例](examples/mcp.json.example) | [脱敏约定](.cursor/rules/no-secrets.mdc) |

## 仓库布局

```text
n9e-mcp-server/          夜莺只读 MCP（基于 n9e 官方开源 MCP 二开）
jenkins-mcp-server/      Jenkins 只读 MCP（裁自 jenkins-mcp-go，MIT）
postgres-mcp-server/     PostgreSQL 多 target 只读 MCP
lab/postgres/            本机 Docker 实验室 + 验收清单
examples/                脱敏后的 mcp.json 样例
.cursor/skills/          给 Agent 的查询技能
.cursor/rules/           只读与脱敏硬性约定
README.md                中文（GitHub 默认）
README.en.md             English
```

各服务 README 写清：作用、如何拿凭据、工具范围、构建、Cursor 配置。上游宣传材料、Docker 镜像、真实环境配置不进本仓库。

## 贡献

```bash
git clone https://github.com/isYaoNoistu/DevOpsMCP.git
cd n9e-mcp-server && go test ./...
cd ../jenkins-mcp-server && go test ./...
cd ../postgres-mcp-server && go test ./...
```

欢迎 Issue / PR：新的只读诊断 Tool、更清楚的 Skill、跨平台文档。请不要提交生产 Token、密码、内网 IP、真实 Job 名或客户库名。`lab/` 里标明仅用于一次性 Docker 的口令可以保留。改行为后同步更新对应 README 与 Skill。根目录改说明时同时改 [README.md](README.md)（中文，默认）和 [README.en.md](README.en.md)（英文）。

## 许可证

仓库整体为 [Apache License 2.0](LICENSE)。`jenkins-mcp-server` 仍保留上游 [MIT](jenkins-mcp-server/LICENSE)。第三方来源见 [NOTICE](NOTICE)。
