<div align="center">

<h1>DevOpsMCP</h1>

**Read-only MCP for on-call ops** — Nightingale alerts · Jenkins deploys · PostgreSQL / MySQL troubleshooting · host logs

<p><a href="README.md">简体中文</a> · <b>English</b></p>

Ask in natural language from **WorkBuddy** or **Cursor**. Stop hopping between the monitoring UI, Jenkins, and a SQL client.  
The five binaries are standard **MCP stdio**: build once, reuse the same `command` / `args` / `env`, and only change where each product stores config.  
The process runs on **your laptop** and talks to systems you already have. Credentials stay local. This repo has no tokens, passwords, or real hostnames.

<p>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?labelColor=1f2937" alt="Apache 2.0"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white&labelColor=1f2937" alt="Go 1.26+"></a>
  <a href="https://modelcontextprotocol.io/"><img src="https://img.shields.io/badge/MCP-stdio-7c3aed?labelColor=1f2937" alt="MCP stdio"></a>
  <img src="https://img.shields.io/badge/default-read--only-059669?labelColor=1f2937" alt="read-only">
</p>

<p>
  <b><a href="#quick-start">Quick start</a></b> ·
  <a href="#compatible-agents">Compatible agents</a> ·
  <a href="#what-it-does">What it does</a> ·
  <a href="#why-nightingale-not-prometheus--elasticsearch">Why Nightingale</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#the-five-servers">Servers</a> ·
  <a href="#security-model">Security</a> ·
  <a href="#preview-status-and-disclaimer">Preview &amp; disclaimer</a> ·
  <a href="#when-to-use--when-not-to">Fit</a> ·
  <a href="#docs">Docs</a>
</p>

</div>

---

When alerts fire, a deploy goes red, or the database stalls, the time sink is rarely “not knowing how to look.” It is **scattered UIs, dropped context, and being one click too late**. DevOpsMCP hands five read-only diagnosis APIs to the agent already watching the alert group: list then detail, stage then Console, `target` then SQL, allowlisted host logs.

**The tools are portable.** Nightingale, Jenkins, PostgreSQL, MySQL, and host logs speak MCP, not a particular chat product. WorkBuddy and Cursor use almost the same JSON: fill in absolute paths and credentials in [`examples/mcp.json.example`](examples/mcp.json.example) and both can launch the same binaries. Codex, Claude Code, Trae, Lingma, and any other MCP stdio client reuse the same `command` + `env`. You do not write a new MCP per IDE.

| Situation | Without MCP | With DevOpsMCP |
| --- | --- | --- |
| Alerts | Nightingale list → rule → host → PromQL → jump to the log UI | Chat: current alert → reuse `rule_id` / PromQL → metrics and logs |
| Failed deploy | Open Jenkins, scroll thousands of Console lines, paste into chat | Job → failed stage → search `ERROR`, bring the build number back |
| Database stuck | Ping a DBA, or connect to the wrong DB and run SQL you should not | Per-`target` read-only sessions, blocking trees, slow SQL |
| Host logs | SSH in and grep, or paste a whole file into chat | Per-`target` allowlisted list / search / tail; no exec |

## What it does

- **Nightingale MCP** — Fork of the [official n9e MCP](https://github.com/n9e/n9e-mcp-server): stdio plus six read-only toolsets (16 tools) for active/history alerts, rules, hosts, PromQL, and Loki / Elasticsearch / OpenSearch logs. Upstream HTTP mode, write tools, and unused packages (users, dashboards, mutes, …) are stripped.
- **Jenkins MCP** — 18 read-only tools: jobs, failed builds, Pipeline stages, console tail and search, queue, nodes. No trigger, no stop, no Groovy.
- **PostgreSQL MCP** — 19 read-only tools: one process, many instances via a local `target` file; sessions / locks / stats / replication plus one guarded `SELECT` escape hatch. More databases means editing local JSON, not more Tools.
- **MySQL MCP** — 21 read-only tools with the same multi-`target` model: sessions, InnoDB/metadata locks, statement digest, global variables/status, replication, plus a guarded `SELECT`. Oracle's official MySQL MCP targets HeatWave/AI; this one is on-call diagnosis from Performance Schema and sys.
- **Host logs MCP** — 5 read-only tools: one process, many hosts via a local `target` file; list / search / tail under a path allowlist. Not SFTP and not SSH exec. The surface was narrowed after MCP filesystem allowlists, MCP Roots (not a sandbox), and OWASP MCP05 command injection.
- **Reusable query order** — [Skills](.cursor/skills/) and [Rules](.cursor/rules/) ship for Cursor. WorkBuddy and others can paste the same order into their own Skills. The binaries register no write tools, so safety does not depend on one vendor’s rules file.
- **Local lab** — `lab/postgres` and `lab/mysql` bring up disposable Docker databases; `lab/host-logs` uses fixture files (no SSH) to check the path guardrails.

This is not an MCP Hub, not another HTTP gateway, and not a CMDB. Frequent questions get dedicated Tools. PostgreSQL and MySQL use guarded `query_postgres` / `query_mysql` instead of one Tool per system view. Host logs have no generic `exec`.

## Why Nightingale, not Prometheus / Elasticsearch

On-call usually needs **aligned alerts, metrics, and logs** — not “one Prometheus” or “one Elasticsearch.” Which host did this rule fire on, what is the PromQL, and do logs at the same time show the same error? Nightingale (夜莺) is already that aggregation layer: alerts live there, PromQL is queried there, and Loki / Elasticsearch / OpenSearch can be attached as datasources.

So this repo ships a **Nightingale MCP**, not a separate MCP for Prometheus, Elasticsearch, Loki, or VictoriaMetrics:

- Each storage backend would mean another process, another auth story, another field dialect — and the agent would still have to join alert IDs, metric labels, and log indexes. Nightingale already did that join.
- Add new backends **in Nightingale** (Prometheus, VictoriaMetrics, Elasticsearch, Loki, OpenSearch, and the many others it supports). After that, this MCP still uses the same read-only tools: alerts, hosts, datasource list, `query_instant` / `query_range`, `query_logs`. You do not open a new MCP per backend.
- The MCP talks only to the Nightingale API, never to the underlying stores. The token is a Nightingale user token; business groups and datasources match that account’s Nightingale ACL.

Jenkins, PostgreSQL, and MySQL have no equivalent on-call aggregation plane, so those MCPs talk to the platforms directly. Monitoring already has Nightingale — do not stack another MCP layer on the raw stores.

## How it works

```
  On-call
       │
       ▼
  WorkBuddy · Cursor · Codex · Claude Code · Trae · Lingma · …
       │  Natural language: "charging prod is red" / "idle in transaction"
       ▼
  ┌────────────────────────────────────────────┐
  │  Local stdio MCP (your laptop, not the DC) │
  │  nightingale · jenkins · postgres · mysql · host-logs  │
  │  Build once; clients only differ in config │
  └────────────────────────────────────────────┘
         │              │               │
         ▼              ▼               ▼
    Nightingale API  Jenkins REST     PostgreSQL      MySQL         host OpenSSH
    X-User-Token     user + API token   mcp_ro + pgpass mcp_ro + mysqlpass  mcp_logs + key
```

- **Secrets stay off git**: `mcp.json` / `config.toml` is local only; PostgreSQL / MySQL targets JSON rejects `password`; host-log passwords may be kept in the private local targets file.
- **Binaries register no write tools**: no `trigger_build`, no alert-rule edits, no `VACUUM` / `pg_terminate_backend`, no host `exec`.
- **Platform accounts are still the floor**: Jenkins needs Overall/Read + Job/Read; databases use `mcp_ro`; host logs use a logs-only SSH user.
- **Editing targets does not require an MCP reload**: PostgreSQL / MySQL / host logs hot-reload on file mtime. After changing the binary path or env vars, reload that server in whichever client you use.

With several servers enabled, the five binaries expose about 79 tools. Some clients (Cursor Community is a common example) cap tools around 40. Disable the ones you are not using for that incident. A Community-sized subset is [examples/mcp.json.cursor-community.example.json](examples/mcp.json.cursor-community.example.json) (Nightingale + host logs). The full five-server file is [examples/mcp.json.example](examples/mcp.json.example).

## Compatible agents

**WorkBuddy + Cursor** are the primary path: both use `mcpServers` → `command` / `args` / `env`. Drop the same example into each product’s config file. Other MCP stdio clients are already compatible at the protocol layer; only the file path or encoding changes.

| Client | Config location | How to connect |
| --- | --- | --- |
| **WorkBuddy** (Tencent Cloud CodeBuddy) | User `~/.workbuddy/mcp.json`, or project `.workbuddy/mcp.json`; or paste in UI (Plugins → MCP servers) | **Same JSON** as the example. Prefer `"type": "stdio"` (already in the sample). [WorkBuddy MCP docs](https://www.codebuddy.cn/docs/workbuddy/From-Beginner-to-Expert-Guide/Function-Description/MCP-Guide) |
| **Cursor** | User `~/.cursor/mcp.json`, or project `.cursor/mcp.json` | Same JSON. Opening this repo enables `.cursor/skills` and `.cursor/rules` for the workspace |
| **Codex** (OpenAI) | `~/.codex/config.toml` | **Not JSON.** Map the same `command` / `args` / `env` into `[mcp_servers.nightingale]` tables: [`examples/codex.toml.example`](examples/codex.toml.example) |
| **Claude Code** | `claude mcp add` or user-level MCP JSON (`mcpServers`) | Same `command` + `env` |
| **Trae** | User/project MCP config (often `.trae/mcp.json`) | Same `mcpServers` JSON |
| **Lingma / Qoder** | IDE “MCP services” or a config file | STDIO: command, args, env; or paste isomorphic JSON |
| **VS Code Copilot** | User MCP config | Often `"servers"` instead of `"mcpServers"`; `command` / `args` / `env` mean the same |
| **Any other MCP client** | See that product’s docs | If it can spawn a local process over stdio, point it at these binaries |

**Tools travel; chat habits do not.** Each product owns Skills / system prompts: Cursor uses the repo Skills; WorkBuddy gets the same query order as its own Skill; Codex uses `AGENTS.md` and friends. After switching clients, copy “list then detail; no writes unless explicitly asked,” or the agent may spray every tool. The binaries still have no write tools.

Config paths change with product versions — trust the vendor docs. After wiring, confirm the servers you enabled are green in that product’s MCP panel.

## Quick start

Needs **Go 1.26+** (pack scripts use a golang Docker image if Go is missing). Binaries are not committed. Build once for WorkBuddy and Cursor.

```bash
git clone https://github.com/isYaoNoistu/DevOpsMCP.git
cd DevOpsMCP/deploy
```

**Windows (cmd):**

```bat
pack-windows.cmd
```

Output: `deploy\dist\devopsmcp-windows-amd64\` (five `.exe` files + examples).

**Linux:**

```bash
chmod +x pack-linux.sh
./pack-linux.sh
```

Output: `deploy/dist/devopsmcp-linux-amd64/`.

1. Open `examples/mcp.json.example` inside the pack (same file as the repo root).
2. Set `command` to an **absolute path of a binary in that folder** (Windows: `.exe`). Replace URLs and tokens. `PG_TARGETS_FILE` / `MYSQL_TARGETS_FILE` / `HOST_LOGS_TARGETS_FILE` may be any local path.
3. **WorkBuddy**: paste into MCP settings or write `~/.workbuddy/mcp.json`. **Cursor**: write `~/.cursor/mcp.json`.
4. Codex: copy [`examples/codex.toml.example`](examples/codex.toml.example) into `config.toml`.

Attaching to a running YLune (Docker mount + API register): [deploy/README.md](deploy/README.md) section 3.

You can still `go build` by hand in each module. Day-to-day, use `deploy/pack-*`.

| Server | Credentials you need | How to get them |
| --- | --- | --- |
| Nightingale | `N9E_BASE_URL` + `N9E_TOKEN` | User login → Profile → Token management. Steps: [n9e README](n9e-mcp-server/README.md#如何拿到夜莺-token) (Chinese) |
| Jenkins | `JENKINS_URL` + read-only user + API Token | Create a user → grant Read only → generate a token on that user’s Configure page. [jenkins README](jenkins-mcp-server/README.md#如何创建-jenkins-只读用户和-api-token) (Chinese) |
| PostgreSQL | `PG_TARGETS_FILE` + `mcp_ro` password | Superuser runs the role SQL; password goes in Credential Manager or pgpass, **not** in targets. [postgres README](postgres-mcp-server/README.md#用-postgres-超级用户创建-mcp_ro) · [configuration](postgres-mcp-server/docs/configuration.md) (Chinese) |
| MySQL | `MYSQL_TARGETS_FILE` + `mcp_ro` password | Admin runs the user SQL; password goes in Credential Manager or mysqlpass, **not** in targets. [mysql README](mysql-mcp-server/README.md#用-mysql-管理员创建-mcp_ro) · [configuration](mysql-mcp-server/docs/configuration.md) (Chinese) |
| Host logs | `HOST_LOGS_TARGETS_FILE` + SSH password or key | Dedicated logs-only user on the host; passwords may be stored in the private local targets file; key-based login remains supported. [host-logs README](host-logs-mcp-server/README.md) · [configuration](host-logs-mcp-server/docs/configuration.md) (Chinese) |

After editing config or `go build`, **reload** the matching MCP in the client you use. For repo Skills, open this repo in Cursor; if the binaries live in another ops repo, copy `.cursor/skills/{nightingale,jenkins,postgres,mysql,host-logs}` there.

Handshake only, no real systems: each directory’s `scripts/smoke-stdio.ps1`. Labs: [lab/postgres](lab/postgres/README.md), [lab/mysql](lab/mysql/README.md), [lab/host-logs](lab/host-logs/README.md). The lab steps are written for Cursor; in WorkBuddy paste the same `mcp.fragment.json` into your `mcp.json`.

Per-server READMEs, Skills, and the lab checklist are currently **Chinese**. This English file covers the overview, clients, security, and disclaimer.

## The five servers

| | Nightingale | Jenkins | PostgreSQL | MySQL | Host logs |
| --- | --- | --- | --- | --- | --- |
| Directory | [n9e-mcp-server](n9e-mcp-server/README.md) | [jenkins-mcp-server](jenkins-mcp-server/README.md) | [postgres-mcp-server](postgres-mcp-server/README.md) | [mysql-mcp-server](mysql-mcp-server/README.md) | [host-logs-mcp-server](host-logs-mcp-server/README.md) |
| Config name | `nightingale` | `jenkins` | `postgres` | `mysql` | `host-logs` |
| Tool count | 16 | 18 | 19 | 21 | 5 |
| Auth | `X-User-Token` | HTTP Basic (username + API Token) | `mcp_ro` + Credential Manager / pgpass | `mcp_ro` + Credential Manager / mysqlpass | SSH password/key + path allowlist |
| Typical order | alert list → detail → reuse PromQL / log body | `list_jobs` → build → stage → search Console | `list_targets` → dedicated diagnostics → `query_postgres` only if needed | `list_targets` → dedicated diagnostics → `query_mysql` only if needed | `list_targets` → `list_log_files` → `search_log` |
| Query order (Cursor Skill) | [nightingale](.cursor/skills/nightingale/SKILL.md) | [jenkins](.cursor/skills/jenkins/SKILL.md) | [postgres](.cursor/skills/postgres/SKILL.md) | [mysql](.cursor/skills/mysql/SKILL.md) | [host-logs](.cursor/skills/host-logs/SKILL.md) |

Nightingale MCP is a fork of [n9e/n9e-mcp-server](https://github.com/n9e/n9e-mcp-server) (Flashcat Nightingale’s official open-source MCP), read-only toolsets only. Jenkins is trimmed from [jenkins-mcp-go](https://github.com/2001adarsh/jenkins-mcp-go): no trigger/stop/Groovy/full-console paths (console defaults to the last 500 lines, cap 2000). Licenses and upstream notes: [NOTICE](NOTICE).

Job names, database names, and index names in Skills are **fictional** (`team/prod/checkout-api`, `orders-prod`, `app-logs-*`). After you connect your environment, discover with `list_*`. Do not guess IDs.

## Security model

```
Conversation grant (no explicit write request → do not call write tools)
        +
Binaries register no write tools by default
        +
Least-privilege platform accounts
        +
Repo forbids tokens / passwords / real hostnames
```

Extra PostgreSQL / MySQL / host-log constraints:

- A `password` field in database targets → load fails; host-log targets support passwords
- Production database targets (`environment` / `tags` / name suffix `prod`) default to `sslmode=verify-full`; `prefer` / `disable` / `skip-verify` are rejected
- `query_postgres` / `query_mysql` accept a single `SELECT` / `WITH` and block side-effect functions and multi-statement. They are **data-plane reads**: querying application tables returns those rows (possibly PII). Prefer dedicated diagnostic tools; do not dump large business tables.
- `explain_query analyze=true` is refused on production targets
- Host logs: overly broad `paths` (`/` , `/data`) are rejected; no `exec`; POSIX-quoted templates; built-in SSH for passwords, OpenSSH `BatchMode` for keys

The agent must not claim it retried a build, muted an alert, killed a session, changed data, or ran a command on a host. Cursor rules: [`.cursor/rules/devops-mcp.mdc`](.cursor/rules/devops-mcp.mdc). Other clients should carry the same contract in their own Skills / system prompts.

## Preview status and disclaimer

This repository is in **preview**. APIs, tool lists, Skills, and the default read-only boundary may change. It is provided **AS IS** under [Apache License 2.0](LICENSE) and **is not a warranty or commitment for any production environment**.

After you build, configure, and point these servers at real Nightingale / Jenkins / PostgreSQL / MySQL / host SSH, any query failure, misdiagnosis, data leak, or business impact from misuse, bad credentials, agent hallucination, upstream API changes, network faults, or platform outages is **your responsibility. Authors and contributors are not liable.** Before production, use read-only accounts and validate in your own environment. When something breaks, check local MCP config, platform ACLs, and the upstream service first — do not assume this repo is at fault.

This release **does not register write tools**. If you fork this tree or upstream and put writes back (edit alert rules, mutes, trigger builds, Groovy, `VACUUM`, `KILL`, kill sessions, remote exec, …):

- You must design **least privilege** (dedicated accounts, only the roles you need, never a long-lived superadmin token)
- You must wire **approval and audit** (who changed what, through which agent, at what time)
- You must own the blast radius of an agent calling a write tool by mistake; a production write is **your** change, not this repo’s default behavior

“It compiles” is not “it is safe to write production.” Changes go through your existing change process. Do not hand write access to MCP.

## When to use · when not to

**Use it when**: Nightingale + Jenkins + PostgreSQL / MySQL are already running and you also need to grep host text logs; on-call needs natural language to join alerts / red builds / lock waits / files; you want the agent to **inspect production, not mutate it**; WorkBuddy, Cursor, or another MCP client can run stdio.

**Skip it when**: those systems are not there yet; you need MCP to click “Build now”, edit alert rules, or exec on a host (use your change process); the question is short and not about live state (no MCP needed); the client only speaks remote HTTP MCP and cannot spawn a local process (this tree dropped Nightingale HTTP mode).

Vs. a home-grown poller: scripts fit fixed checks; on-call questions change every time. MCP gives the agent a read-only client, and query order keeps a whole Console dump out of one turn.

## Docs

Service READMEs below are Chinese.

| Start here | Then |
| --- | --- |
| [Pack / attach to YLune](deploy/README.md) | Linux/Windows default packs; Docker hub mount + register |
| [Compatible agents](#compatible-agents) | [mcp.json example](examples/mcp.json.example) · [Community subset](examples/mcp.json.cursor-community.example.json) · [Codex TOML example](examples/codex.toml.example) |
| [Nightingale: get a token](n9e-mcp-server/README.md#如何拿到夜莺-token) | [Nightingale tools and mcp.json](n9e-mcp-server/README.md) |
| [Jenkins: user + API token](jenkins-mcp-server/README.md#如何创建-jenkins-只读用户和-api-token) | [Jenkins tools and diagnosis order](jenkins-mcp-server/README.md) |
| [PostgreSQL: create mcp_ro as postgres](postgres-mcp-server/README.md#用-postgres-超级用户创建-mcp_ro) | [targets / pgpass](postgres-mcp-server/docs/configuration.md) |
| [MySQL: create mcp_ro](mysql-mcp-server/README.md#用-mysql-管理员创建-mcp_ro) | [targets / mysqlpass](mysql-mcp-server/docs/configuration.md) |
| [Host logs: two-file setup + allowlist](host-logs-mcp-server/README.md) | [targets / SSH authentication](host-logs-mcp-server/docs/configuration.md) |
| [Docker labs: PostgreSQL](lab/postgres/README.md) · [MySQL](lab/mysql/README.md) · [host-log fixtures](lab/host-logs/README.md) | [Skill: postgres](.cursor/skills/postgres/SKILL.md) · [mysql](.cursor/skills/mysql/SKILL.md) · [host-logs](.cursor/skills/host-logs/SKILL.md) |
| [no-secrets rule](.cursor/rules/no-secrets.mdc) | |

## Layout

```text
n9e-mcp-server/          Nightingale read-only MCP (fork of official n9e MCP)
jenkins-mcp-server/      Jenkins read-only MCP (trimmed jenkins-mcp-go, MIT)
postgres-mcp-server/     Multi-target PostgreSQL read-only MCP
mysql-mcp-server/        Multi-target MySQL read-only MCP
host-logs-mcp-server/    Multi-target host-log MCP (allowlist, no exec)
deploy/                  Linux/Windows packs; attach to YLune
lab/postgres/            Local Docker PostgreSQL lab + checklist
lab/mysql/               Local Docker MySQL lab + checklist
lab/host-logs/           Local fixture logs + checklist
examples/                mcp.json and Codex TOML samples
.cursor/skills/          Cursor query skills (other clients can restate the same order)
.cursor/rules/           Cursor read-only and no-secrets rules
README.md                Chinese (GitHub default)
README.en.md             English
```

Each service README covers: purpose, how to get credentials, tool scope, build, local MCP config. Upstream marketing, Docker images, and real environment configs stay out of this repo.

## Contributing

```bash
git clone https://github.com/isYaoNoistu/DevOpsMCP.git
cd n9e-mcp-server && go test ./...
cd ../jenkins-mcp-server && go test ./...
cd ../postgres-mcp-server && go test ./...
cd ../mysql-mcp-server && go test ./...
cd ../host-logs-mcp-server && go test ./...
```

Issues / PRs welcome: new read-only diagnosis Tools, clearer Skills, corrections to other clients’ config paths. Do not commit production tokens, passwords, private IPs, real job names, or customer database names. Passwords marked as one-shot Docker-only under `lab/` may stay. After a behavior change, update the matching README and Skill. Root overview: keep [README.md](README.md) (Chinese, default) and [README.en.md](README.en.md) (English) in sync.

## License

The repo is [Apache License 2.0](LICENSE). `jenkins-mcp-server` keeps upstream [MIT](jenkins-mcp-server/LICENSE). Third-party provenance: [NOTICE](NOTICE).

## Codex setup and multiple environments

See the [Codex setup guide (Chinese)](CODEX.md) for installation, TOML fields, credentials, UAT/PROD instances, target registries, and verification. Each service README includes its own TOML block. Copy [single-environment](examples/codex.toml.example) or [multi-environment](examples/codex.multi-env.toml.example) settings as needed.

## Kafka MCP (production trial candidate)

The independent Kafka module provides 10 read-only tools for local targets, allowlisted topic/group discovery, capabilities, configuration, group commits with explicit topic scope, offsets and bounded record samples. Trial builds include version, Git revision/dirty marker, build time, SHA-256 and a build manifest; the module is separate from the existing five-service packaging. Code remains in this repository with no cicd migration. See the [Kafka README](kafka-mcp-server/README.md) and [Kafka Skill](.cursor/skills/kafka/SKILL.md). Successful offline tests and packaging do not establish production acceptance.
