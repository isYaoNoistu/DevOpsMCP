# Kafka production trial implementation plan

> For agentic workers: use subagent-driven-development or executing-plans task by task.

**Goal:** Complete the approved bounded read-only diagnostic workflow and publish a traceable development build.

**Architecture:** Preserve stdio, fresh target configuration and read-only Kafka clients. Add explicit group topic scope, bounded discovery, safe structured errors and a per-target process-local concurrency budget. No new service or connection pool.

**Tech stack:** Existing Go MCP SDK, franz-go/kadm/kmsg, kfake tests, PowerShell packaging, Python stdio smoke.

## Constraints
- Work in DevOpsMCP only; binaries in workspace dist, no cicd migration or commits.
- No production writes, payload opt-in remains unchanged; fixtures create data only in local kfake.
- Every broker operation remains allowlisted, bounded and cancellable. Default target concurrency 2, immediate busy response.
- New lists return only allowed names, sorted, default 50 / maximum 200 results, exclusive after-name cursor. Each call is a fresh non-atomic snapshot, not broker-side pagination; partial discovery is explicitly marked.
- Group topics optional, maximum 20 exact names, overrides implicit offset topic scope when supplied; missing commits stay -1 with explicit no_committed_offset status. No automatic lag inference.

## Tasks
- [x] Broker regression tests then fixes: empty config keys; explicit group topics; structured resource/broker error and retryability; scoped partition limits.
- [x] Discovery backend and tests: topic metadata without auto-create, group list shards, allowlist filtering before output/paging, partial errors, broker metadata bounds.
- [x] MCP inputs and tests: ten tools; validate all scopes before dialing; target semaphore survives concurrent requests and releases on failure/cancellation; safe top-level error details.
- [x] Docs/skills/examples: arguments, output semantics, payload transmission vs output, scan cost and unsupported cases.
- [x] Packaging: version, revision, dirty marker, build timestamp; SHA-256, module docs and licenses outside repository.
- [x] Run tests/vet and stdio smoke. Run new binary against current explicitly confirmed test target with only read-only requests; verify dedicated group offsets unchanged around metadata-only peek. Record gaps honestly, including missing active group, authenticated real clusters and CLI comparison if unavailable.
- [x] Review final changes, address actionable findings, rebuild and verify artifact checksum.
