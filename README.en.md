<div align="center">

<h1>DevOpsMCP</h1>

**Read-only MCP for on-call ops** — Nightingale alerts · Jenkins deploys · PostgreSQL troubleshooting

<p><a href="README.md">简体中文</a> · <b>English</b></p>

Ask in natural language from **WorkBuddy** or **Cursor**. Stop hopping between the monitoring UI, Jenkins, and a SQL client.  
The three binaries are standard **MCP stdio**: build once, reuse the same `command` / `args` / `env`, and only change where each product stores config.  
The process runs on **your laptop** and talks to systems you already have. Credentials stay local. This repo has no tokens, passwords, or real hostnames.

<p>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?labelColor=1f2937" alt="Apache 2.0"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white&labelColor=1f2937" alt="Go 1.23+"></a>
  <a href="https://modelcontextprotocol.io/"><img src="https://img.shields.io/badge/MCP-stdio-7c3aed?labelColor=1f2937" alt="MCP stdio"></a>
  <img src="https://img.shields.io/badge/default-read--only-059669?labelColor=1f2937" alt="read-only">
</p>

<p>
  <b><a href="#quick-start">Quick start</a></b> ·
  <a href="#compatible-agents">Compatible agents</a> ·
  <a href="#what-it-does">What it does</a> ·
  <a href="#why-nightingale-not-prometheus--elasticsearch">Why Nightingale</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#the-three-servers">Servers</a> ·
  <a href="#security-model">Security</a> ·
  <a href="#preview-status-and-disclaimer">Preview &amp; disclaimer</a> ·
  <a href="#when-to-use--when-not-to">Fit</a> ·
  <a href="#docs">Docs</a>
</p>

</div>

---

When alerts fire, a deploy goes red, or the database stalls, the time sink is rarely “not knowing how to look.” It is **scattered UIs, dropped context, and being one click too late**. DevOpsMCP hands three read-only diagnosis APIs to the agent already watching the alert group: list then detail, stage then Console, `target` then SQL.

**The tools are portable.** Nightingale, Jenkins, and PostgreSQL speak MCP, not a particular chat product. WorkBuddy and Cursor use almost the same JSON: fill in absolute paths and credentials in [`examples/mcp.json.example`](examples/mcp.json.example) and both can launch the same binaries. Codex, Claude Code, Trae, Lingma, and any other MCP stdio client reuse the same `command` + `env`. You do not write a new MCP per IDE.

| Situation | Without MCP | With DevOpsMCP |
| --- | --- | --- |
| Alerts | Nightingale list → rule → host → PromQL → jump to the log UI | Chat: current alert → reuse `rule_id` / PromQL → metrics and logs |
| Failed deploy | Open Jenkins, scroll thousands of Console lines, paste into chat | Job → failed stage → search `ERROR`, bring the build number back |
| Database stuck | Ping a DBA, or connect to the wrong DB and run SQL you should not | Per-`target` read-only sessions, blocking trees, slow SQL |

## What it does

- **Nightingale MCP** — Fork of the [official n9e MCP](https://github.com/n9e/n9e-mcp-server): stdio plus six read-only toolsets (16 tools) for active/history alerts, rules, hosts, PromQL, and Loki / Elasticsearch / OpenSearch logs. Upstream HTTP mode, write tools, and unused packages (users, dashboards, mutes, …) are stripped.
- **Jenkins MCP** — 18 read-only tools: jobs, failed builds, Pipeline stages, console tail and search, queue, nodes. No trigger, no stop, no Groovy.
- **PostgreSQL MCP** — 19 read-only tools: one process, many instances via a local `target` file; sessions / locks / stats / replication plus one guarded `SELECT` escape hatch. More databases means editing local JSON, not more Tools.
- **Reusable query order** — [Skills](.cursor/skills/) and [Rules](.cursor/rules/) ship for Cursor. WorkBuddy and others can paste the same order into their own Skills. The binaries register no write tools, so safety does not depend on one vendor’s rules file.
- **Local lab** — `lab/postgres` brings up a disposable Docker PostgreSQL and a 19-tool checklist, including the SQL guardrails.

This is not an MCP Hub, not another HTTP gateway, and not a CMDB. Frequent questions get dedicated Tools. PostgreSQL uses a guarded `query_postgres` instead of one Tool per system view.

## Why Nightingale, not Prometheus / Elasticsearch

On-call usually needs **aligned alerts, metrics, and logs** — not “one Prometheus” or “one Elasticsearch.” Which host did this rule fire on, what is the PromQL, and do logs at the same time show the same error? Nightingale (夜莺) is already that aggregation layer: alerts live there, PromQL is queried there, and Loki / Elasticsearch / OpenSearch can be attached as datasources.

So this repo ships a **Nightingale MCP**, not a separate MCP for Prometheus, Elasticsearch, Loki, or VictoriaMetrics:

- Each storage backend would mean another process, another auth story, another field dialect — and the agent would still have to join alert IDs, metric labels, and log indexes. Nightingale already did that join.
- Add new backends **in Nightingale** (Prometheus, VictoriaMetrics, Elasticsearch, Loki, OpenSearch, and the many others it supports). After that, this MCP still uses the same read-only tools: alerts, hosts, datasource list, `query_instant` / `query_range`, `query_logs`. You do not open a new MCP per backend.
- The MCP talks only to the Nightingale API, never to the underlying stores. The token is a Nightingale user token; business groups and datasources match that account’s Nightingale ACL.

Jenkins and PostgreSQL have no equivalent on-call aggregation plane, so those two MCPs talk to the platforms directly. Monitoring already has Nightingale — do not stack another MCP layer on the raw stores.

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
  │  nightingale · jenkins · postgres          │
  │  Build once; clients only differ in config │
  └────────────────────────────────────────────┘
         │              │               │
         ▼              ▼               ▼
    Nightingale API  Jenkins REST     PostgreSQL
    X-User-Token     user + API token   mcp_ro + pgpass / Credential Manager
```

- **Secrets stay off git**: `mcp.json` / `config.toml` is local only; the PostgreSQL targets JSON **rejects** a `password` field.
- **Binaries register no write tools**: no `trigger_build`, no alert-rule edits, no `VACUUM` / `pg_terminate_backend`.
- **Platform accounts are still the floor**: Jenkins needs Overall/Read + Job/Read; PostgreSQL uses `mcp_ro` plus role-level `READ ONLY`.
- **Editing targets does not require an MCP reload**: PostgreSQL hot-reloads on file mtime. After changing the binary path or env vars, reload that server in whichever client you use.

With all three servers enabled, some clients (Cursor Community is a common example) cap tools around 40. Disable the two you are not using for that incident.

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
| **Any other MCP client** | See that product’s docs | If it can spawn a local process over stdio, point it at these three binaries |

**Tools travel; chat habits do not.** Each product owns Skills / system prompts: Cursor uses the repo Skills; WorkBuddy gets the same query order as its own Skill; Codex uses `AGENTS.md` and friends. After switching clients, copy “list then detail; no writes unless explicitly asked,” or the agent may spray every tool. The binaries still have no write tools.

Config paths change with product versions — trust the vendor docs. After wiring, confirm all three servers are green in that product’s MCP panel.

## Quick start

Needs **Go 1.23+**. Binaries are not committed; you build them once for WorkBuddy and Cursor.

```bash
git clone https://github.com/isYaoNoistu/DevOpsMCP.git
cd DevOpsMCP

# Windows
go build -o n9e-mcp-server/n9e-mcp-server.exe ./n9e-mcp-server/cmd/n9e-mcp-server/
go build -o jenkins-mcp-server/jenkins-mcp-server.exe ./jenkins-mcp-server/
go build -o postgres-mcp-server/postgres-mcp-server.exe ./postgres-mcp-server/

# Linux / macOS: drop the .exe suffix
```

1. Copy [`examples/mcp.json.example`](examples/mcp.json.example).
2. Set `command` to an **absolute path** (Windows points at `.exe`). Replace URLs and tokens. `PG_TARGETS_FILE` may be any local path; it does not have to live under `.cursor`.
3. **WorkBuddy**: paste into MCP settings or write `~/.workbuddy/mcp.json`. **Cursor**: write `~/.cursor/mcp.json`. Both can point at the same binaries.
4. Codex: copy [`examples/codex.toml.example`](examples/codex.toml.example) into `config.toml`.

| Server | Credentials you need | How to get them |
| --- | --- | --- |
| Nightingale | `N9E_BASE_URL` + `N9E_TOKEN` | User login → Profile → Token management. Steps: [n9e README](n9e-mcp-server/README.md#如何拿到夜莺-token) (Chinese) |
| Jenkins | `JENKINS_URL` + read-only user + API Token | Create a user → grant Read only → generate a token on that user’s Configure page. [jenkins README](jenkins-mcp-server/README.md#如何创建-jenkins-只读用户和-api-token) (Chinese) |
| PostgreSQL | `PG_TARGETS_FILE` + `mcp_ro` password | Superuser runs the role SQL; password goes in Credential Manager or pgpass, **not** in targets. [postgres README](postgres-mcp-server/README.md#用-postgres-超级用户创建-mcp_ro) · [configuration](postgres-mcp-server/docs/configuration.md) (Chinese) |

After editing config or `go build`, **reload** the matching MCP in the client you use. For repo Skills, open this repo in Cursor; if the binaries live in another ops repo, copy `.cursor/skills/{nightingale,jenkins,postgres}` there.

Handshake only, no real systems: each directory’s `scripts/smoke-stdio.ps1`. PostgreSQL from scratch, 19 tools: [lab/postgres](lab/postgres/README.md) (a separate `postgres-targets.lab.json`, so your production list is not overwritten). The lab steps are written for Cursor; in WorkBuddy paste the same `mcp.fragment.json` into your `mcp.json`.

Per-server READMEs, Skills, and the lab checklist are currently **Chinese**. This English file covers the overview, clients, security, and disclaimer.

## The three servers

| | Nightingale | Jenkins | PostgreSQL |
| --- | --- | --- | --- |
| Directory | [n9e-mcp-server](n9e-mcp-server/README.md) | [jenkins-mcp-server](jenkins-mcp-server/README.md) | [postgres-mcp-server](postgres-mcp-server/README.md) |
| Config name | `nightingale` | `jenkins` | `postgres` |
| Tool count | 16 | 18 | 19 |
| Auth | `X-User-Token` | HTTP Basic (username + API Token) | `mcp_ro` + Credential Manager / pgpass |
| Typical order | alert list → detail → reuse PromQL / log body | `list_jobs` → build → stage → search Console | `list_targets` → dedicated diagnostics → `query_postgres` only if needed |
| Query order (Cursor Skill) | [nightingale](.cursor/skills/nightingale/SKILL.md) | [jenkins](.cursor/skills/jenkins/SKILL.md) | [postgres](.cursor/skills/postgres/SKILL.md) |

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

Extra PostgreSQL constraints:

- A `password` field in targets → load fails
- Production targets (`environment` / `tags` / name suffix `prod`) default to `sslmode=verify-full`; `prefer` / `disable` are rejected
- `query_postgres` accepts a single `SELECT` / `WITH`, blocks `dblink*`, advisory locks, and multi-statement; `DISCARD ALL` before returning a connection to the pool
- `explain_query analyze=true` is refused on production targets

The agent must not claim it retried a build, muted an alert, killed a session, or changed data. Cursor rules: [`.cursor/rules/devops-mcp.mdc`](.cursor/rules/devops-mcp.mdc). Other clients should carry the same contract in their own Skills / system prompts.

## Preview status and disclaimer

This repository is in **preview**. APIs, tool lists, Skills, and the default read-only boundary may change. It is provided **AS IS** under [Apache License 2.0](LICENSE) and **is not a warranty or commitment for any production environment**.

After you build, configure, and point these servers at real Nightingale / Jenkins / PostgreSQL, any query failure, misdiagnosis, data leak, or business impact from misuse, bad credentials, agent hallucination, upstream API changes, network faults, or platform outages is **your responsibility. Authors and contributors are not liable.** Before production, use read-only accounts and validate in your own environment. When something breaks, check local MCP config, platform ACLs, and the upstream service first — do not assume this repo is at fault.

This release **does not register write tools**. If you fork this tree or upstream and put writes back (edit alert rules, mutes, trigger builds, Groovy, `VACUUM`, kill sessions, …):

- You must design **least privilege** (dedicated accounts, only the roles you need, never a long-lived superadmin token)
- You must wire **approval and audit** (who changed what, through which agent, at what time)
- You must own the blast radius of an agent calling a write tool by mistake; a production write is **your** change, not this repo’s default behavior

“It compiles” is not “it is safe to write production.” Changes go through your existing change process. Do not hand write access to MCP.

## When to use · when not to

**Use it when**: Nightingale + Jenkins + PostgreSQL are already running; on-call needs natural language to join alerts / red builds / lock waits; you want the agent to **inspect production, not mutate it**; WorkBuddy, Cursor, or another MCP client can run stdio.

**Skip it when**: those three systems are not there yet; you need MCP to click “Build now” or edit alert rules (use your change process); the question is short and not about live state (no MCP needed); the client only speaks remote HTTP MCP and cannot spawn a local process (this tree dropped Nightingale HTTP mode).

Vs. a home-grown poller: scripts fit fixed checks; on-call questions change every time. MCP gives the agent a read-only client, and query order keeps a whole Console dump out of one turn.

## Docs

Service READMEs below are Chinese.

| Start here | Then |
| --- | --- |
| [Compatible agents](#compatible-agents) | [mcp.json example](examples/mcp.json.example) · [Codex TOML example](examples/codex.toml.example) |
| [Nightingale: get a token](n9e-mcp-server/README.md#如何拿到夜莺-token) | [Nightingale tools and mcp.json](n9e-mcp-server/README.md) |
| [Jenkins: user + API token](jenkins-mcp-server/README.md#如何创建-jenkins-只读用户和-api-token) | [Jenkins tools and diagnosis order](jenkins-mcp-server/README.md) |
| [PostgreSQL: create mcp_ro as postgres](postgres-mcp-server/README.md#用-postgres-超级用户创建-mcp_ro) | [targets / pgpass](postgres-mcp-server/docs/configuration.md) |
| [Docker lab checklist](lab/postgres/README.md) | [Skill: postgres](.cursor/skills/postgres/SKILL.md) |
| [no-secrets rule](.cursor/rules/no-secrets.mdc) | |

## Layout

```text
n9e-mcp-server/          Nightingale read-only MCP (fork of official n9e MCP)
jenkins-mcp-server/      Jenkins read-only MCP (trimmed jenkins-mcp-go, MIT)
postgres-mcp-server/     Multi-target PostgreSQL read-only MCP
lab/postgres/            Local Docker lab + checklist
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
```

Issues / PRs welcome: new read-only diagnosis Tools, clearer Skills, corrections to other clients’ config paths. Do not commit production tokens, passwords, private IPs, real job names, or customer database names. Passwords marked as one-shot Docker-only under `lab/` may stay. After a behavior change, update the matching README and Skill. Root overview: keep [README.md](README.md) (Chinese, default) and [README.en.md](README.en.md) (English) in sync.

## License

The repo is [Apache License 2.0](LICENSE). `jenkins-mcp-server` keeps upstream [MIT](jenkins-mcp-server/LICENSE). Third-party provenance: [NOTICE](NOTICE).
