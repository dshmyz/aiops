# AI Operations Copilot

Go backend foundation for an AI-assisted middleware operations console.

> **上手/上线请看：[docs/OPERATIONS.md](docs/OPERATIONS.md)（使用手册）** — 覆盖本地与生产启动、
> 配置与安全、HTTP API 与 RBAC、Web 控制台、核心工作流、测试与构建、部署上线与上线前检查清单。

## What Exists

- Go HTTP service with `GET /healthz`.
- Governed tool registry: **5 static platform meta-tools** — `cluster.status.read`, `system.posture.read` (系统态势), `alert.query` (告警), `event.query` (审计事件), `task.query` (定时任务) — plus dynamic tools from capabilities and external MCP servers. Middleware capability tools (`topic.retention.set` (write), `glusterfs.volume.health.read`, `minio.bucket.health.read`, `kafka.consumer_lag.read`) are **not** hardcoded in Go: they are declared as published YAML capabilities (`examples/capabilities/published/`) and executed over the configured HTTP middleware backend (see `docs/assistant.md` → "Assistant Boundary").
- Tool execution layer: server-side authorization (static allowlist + role/environment/input/risk policy), a **per-read timeout** (default 5s, `ReadOnlyService.WithTimeout`), and audit on every call.
- External MCP integration: `COPILOT_MCP_SERVERS` startup registration + `/v1/mcp/servers` hot CRUD/reload + health checks.
- Assistant endpoints: `POST /v1/assistant/messages` (one-shot) and `POST /v1/assistant/stream` (SSE: delta/thinking/tool_call/progress/response), multiturn conversations (`/v1/assistant/conversations*`), and page-context support.
- Write governance: immutable action plans, dry-run preview (`risk_notice` block), inline confirm, **low-risk Runbook auto-execution** returning `execution_result` with the runbook slug + informational blocks, post-execution verification, and a `/v1/executions` query API.
- Alert layer: `/v1/alerts/webhook` and `/v1/alerts/alertmanager` (both HMAC-SHA256; the latter ingests Prometheus Alertmanager's native payload), `copilot_alerts` table, `alert.query` tool.
- Audit layer: `/v1/audit-events` (filters + keyset + `final_result_only`) + `/v1/audit-events/search` natural-language query; scheduled tasks `/v1/scheduled-tasks*`; prompt registry `/v1/admin/prompts*`; knowledge base; feedback; rate limiting; auth modes (jwt / cas / both).
- Vue 3 capability console in `apps/capability-console`.
- JWT projection uses only `sub`, `roles`, `allowed_environments`, and request ID.
- Production persistence targets MySQL 8; local tests use SQLite for real SQL constraints, foreign keys, and idempotency behavior without Docker.

## Eval

The EinoPlanner evaluation suite is isolated behind a build tag. Run it with:

```bash
make eval
# or: go test -tags=eval ./internal/assistant/eval/...
```

It regenerates `internal/assistant/eval/report.md` with per-category pass rates (tool category 100%, others ≥90%).

## Prompts

`prompts/` holds system prompt templates (e.g. `planning.md` for Eino intent planning). `COPILOT_PROMPTS_DIR` points the prompt registry at such a directory for hot-reload without redeploy.

## Assistant Boundary

`POST /v1/assistant/messages` currently uses a deterministic planner for local
and CI stability. It can classify simple cluster status and topic retention
requests, then hands the candidate intent to the existing Go safety boundary.

Eino is available behind the assistant planner boundary through `EinoPlanner`.
The checked-in adapter uses Eino core interfaces and can wrap a provider-backed
ChatModel. It is not enabled by default and does not call an external model in
local tests. Eino output remains untrusted candidate data: the backend still
resolves canonical tools with the static registry, calls policy, creates pending
plans for writes, and writes audit records. Eino must not directly execute
operational tools, SQL, shell commands, or raw middleware APIs.

Enable an OpenAI-compatible Eino planner:

```bash
COPILOT_ASSISTANT_PROVIDER='eino-openai'
COPILOT_OPENAI_API_KEY='...'
COPILOT_OPENAI_MODEL='...'
COPILOT_OPENAI_BASE_URL='https://api.openai.com/v1'
```

If `COPILOT_ASSISTANT_PROVIDER` is unset, the service uses the deterministic
planner.

### Autonomous agent loop

With an LLM planner (`eino-openai`), the assistant can run an **autonomous agent
loop**: plan → execute a read-only tool → feed the result back → replan, up to
`COPILOT_ASSISTANT_MAX_STEPS` (default 8). It emits an SSE `step` event per
executed tool so the console renders an independent "steps performed" timeline,
and persists each intermediate tool result as a `tool_step` conversation turn
(full step-level audit, chained via `parent_turn_id`).

Security boundary: the loop only **reads** autonomously. Writes always stop at
plan creation + `confirmation_required` for human approval — the loop never
auto-executes a write, and low-risk Runbook auto-execution is disabled inside
the loop. See [docs/assistant.md](docs/assistant.md) for details.

## Middleware Diagnostics

The assistant can return a structured diagnostic package for middleware health
questions. The first rollout order is GlusterFS, then MinIO, then Kafka.

Example prompts:

```text
检查 prod glusterfs data volume 健康
检查 prod minio archive bucket 健康
检查 prod kafka payments consumer lag
```

Diagnostic output includes resources, observations, findings, and
recommendations. Recommendations are not execution authority. Any write action
still resolves to a registered tool, creates an immutable action plan, and must
pass approval, execution, and audit.

## Capability Importer

Import OpenAPI paths into reviewed capability drafts:

```bash
go run ./cmd/capability-importer import openapi ./middleware-openapi.yaml ./capabilities/discovered
```

Files under `capabilities/discovered` are drafts only. Review and edit the
generated YAML manually or through the Capability Console workflow, including
its input and output mappings, backend endpoint, roles, environment scope, and
governance. Publishing moves an approved read capability to
`capabilities/published` with `status: published`; unpublishing moves it back
to `capabilities/discovered` with `status: needs_review`. Only published files
are loaded by Copilot at startup:

```bash
COPILOT_CAPABILITIES_DIR='./capabilities' go run ./cmd/copilot-api
```

The first importer and HTTP adapter version supports GET-only JSON read
capabilities with OpenAPI `in: path` parameters. Query and header parameters
are not imported or sent to the backend; reviewers must model those needs
manually in a later capability version, or wait for explicit support.

The loader validates published writes, including action-plan, approval,
precheck, and rollback governance, but published writes are not exposed to the
Copilot runtime until a capability-aware confirmed executor exists. Published
reads execute only through the governed read-only service, which enforces the
canonical tool registry, role and environment policy, input validation, HTTP
adapter restrictions, output normalization, and audit logging. Discovered
files are not loaded, raw backend access is not returned, and direct model
execution is not runtime authority.

When `COPILOT_CAPABILITIES_DIR` is set, the API also exposes capability
management endpoints for an admin UI:

```text
GET    /v1/capabilities
GET    /v1/capabilities/{name}
POST   /v1/capabilities/drafts
PUT    /v1/capabilities/drafts/{name}
POST   /v1/capabilities/validate
POST   /v1/capabilities/test
POST   /v1/capabilities/{name}/publish
POST   /v1/capabilities/{name}/unpublish
```

`list` and `get` are available to viewer/operator/admin roles. Draft writes,
validation, backend test calls, publish, and unpublish require admin. The test
endpoint executes through the HTTP adapter and returns only normalized
Copilot-visible output.

## Local Verification

Run the default test suite:

```bash
go test ./...
go vet ./...
```

The default suite includes SQLite-backed store tests. It does not require
Docker or MySQL.

Run live MySQL migration checks when Docker is available:

```bash
make test-integration
```

## Run Locally

Start the API with local SQLite:

```bash
COPILOT_DATABASE_DRIVER='sqlite' \
COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN='1' \
COPILOT_JWT_HMAC_SECRET='dev-secret' \
go run ./cmd/copilot-api
```

The local SQLite database defaults to `copilot-local.db` in the working
directory. Production still targets MySQL:

```bash
docker compose up -d mysql

COPILOT_DATABASE_DSN='root:copilot-root-password@tcp(127.0.0.1:3306)/copilot?parseTime=true' \
COPILOT_JWT_HMAC_SECRET='dev-secret' \
go run ./cmd/copilot-api
```

The read endpoint expects an `HS256` bearer JWT. Claims are projected into a
request identity; permissions are never accepted from the token.

Start the web console:

```bash
cd apps/capability-console
npm install
npm run dev
```

The API defaults to `http://127.0.0.1:18080`. The console defaults to
`http://localhost:5174` and proxies `/v1` to `http://127.0.0.1:18080` with a
dev admin token.

`COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=1` is only for local demos. It lets the
console receive the one-time confirmation token required by the confirm API.
Do not enable it in production.
