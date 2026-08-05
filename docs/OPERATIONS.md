# AI Operations Copilot — 使用手册

面向**上线 / 运维 / 二次开发**的使用手册。覆盖总体架构、本地与生产启动、配置与安全、
HTTP API 与角色权限、Web 控制台、核心工作流、测试与构建、部署上线，以及上线前检查清单。

> 本手册是对各专项文档的**总入口**。深入某一块时请跳转：
> [配置](configuration.md) · [Assistant 与自治 Agent 循环](assistant.md) ·
> [告警接入](alerting.md) · [能力市场](capability-marketplace.md) ·
> [能力 CI](capability-ci.md)。

---

## 1. 项目概览

系统由两大部分组成：

```
┌─────────────────────────────────────────────┐
│  apps/capability-console  (Vue 3 + TS SPA)   │  操作控制台 / Web UI
│  13 个视图，/v1 代理到后端                   │
└──────────────────────────┬──────────────────┘
                           │ HTTP /v1（JWT）
┌──────────────────────────▼──────────────────┐
│  cmd/copilot-api  (Go)                       │  copilot-api 服务（:18080）
│  - Assistant（确定性 planner / Eino LLM）    │
│  - 工具注册表 + 能力(YAML) + 外部 MCP        │
│  - 写治理（action plan，人工确认）           │
│  - Runbook 低风险自动执行 + 反馈自演化        │
│  - 告警接入 / 审计 / 定时巡检 / 知识库(RAG)   │
│  - 统一 RBAC（viewer/operator/admin）        │
└───────────────┬─────────────────────────────┘
                │
        ┌───────┴────────┐
        │  MySQL / SQLite │  持久化（生产 MySQL 8）
        └────────────────┘
        ┌────────────────┐
        │ 中间件能力后端    │  经 HTTPAdapter 调用的外部中间件 API
        │ (examples/mock) │
        └────────────────┘
```

**角色（RBAC，来自 JWT claims）**：

| 角色 | 说明 | 典型权限边界 |
|------|------|--------------|
| `viewer` | 只读 | 查看能力/市场/计划/审计/巡检 |
| `operator` | 运维操作 | 只读 + 确认 action plan 执行、反馈评分 |
| `admin` | 管理 | 全部读 + 所有写 / 管理端点（prompt、知识、runbook 草稿、MCP、执行历史、能力发布） |

> 权限**只来自服务端对 JWT claims 的投影**，绝不接受 token 里自带的权限声明。

**写治理模型**：任何写操作（如 `topic.retention.set`）都不会被模型直接执行——
先创建**不可变 action plan**（含 dry-run 预览），人工确认后才执行，全过程审计。

---

## 2. 快速启动（本地开发）

依赖：Go ≥ 1.25、Node ≥ 20、可选 Docker。

### 2.1 一键启动全栈（推荐）

```bash
make dev-up        # mock 中间件(:19090) + copilot-api(:18080) + 控制台(:5173)
make dev-logs      # 跟踪三个进程日志（/tmp/copilot-dev-pids/*.log）
make dev-down      # 停止
```

验证 AI 调用链透明化 / 执行后验证（可选）：

```bash
make dev-verify-trace          # read/write/clarification 三类场景 trace
make dev-verify-verification   # 写 plan → 确认 → 执行后 verification 回读
```

> `dev-up` 会读取根目录 `.env`（存在且变量非空时优先于注入）。首次可 `cp .env.example .env`。

### 2.2 手动分步启动

后端（SQLite，本地演示）：

```bash
COPILOT_DATABASE_DRIVER=sqlite \
COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=1 \
COPILOT_JWT_HMAC_SECRET=dev-secret \
go run ./cmd/copilot-api
```

> `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=1` **仅本地演示**：让前端能拿到一次性确认 token。
> **生产必须保持 `0`。**

前端：

```bash
cd apps/capability-console && npm install && npm run dev   # :5173，/v1 代理到 :18080
```

### 2.3 健康检查

```bash
curl -s http://127.0.0.1:18080/healthz   # 200 OK
curl -s http://127.0.0.1:18080/readyz    # 依赖就绪（含 DB ping）
curl -s http://127.0.0.1:18080/metrics   # Prometheus 指标
```

---

## 3. 配置与安全

服务**完全通过环境变量配置**，启动时自动加载 `.env`（已存在的真实环境变量优先）。

### 3.1 核心配置分组

| 分组 | 关键变量 | 说明 |
|------|----------|------|
| 数据库 | `COPILOT_DATABASE_DRIVER` | `sqlite`（默认/开发）或 `mysql`（生产） |
| 数据库 | `COPILOT_DATABASE_DSN` | SQLite 文件路径或 MySQL DSN |
| HTTP | `COPILOT_HTTP_ADDR` | 监听地址，默认 `:18080` |
| CORS | `COPILOT_CORS_ALLOWED_ORIGINS` | 逗号分隔；**留空=允许所有 `*`，生产必须限定前端域名** |
| 鉴权 | `COPILOT_JWT_HMAC_SECRET` | HS256 签名密钥，**生产必设，定期轮换** |
| 鉴权 | `COPILOT_AUTH_MODE` | `jwt`（默认）/ `cas` / `both` |
| LLM | `COPILOT_ASSISTANT_PROVIDER` | `eino-openai` 或空（确定性 planner） |
| LLM | `COPILOT_OPENAI_BASE_URL` / `_API_KEY` / `_MODEL` | OpenAI 兼容接口 |
| LLM | `COPILOT_OPENAI_TIMEOUT` / `_RETRY` / `_RETRY_BACKOFF` | 超时/重试/退避 |
| Prompt | `COPILOT_PROMPTS_DIR` | prompt 模板目录，热加载无需重启 |
| 能力 | `COPILOT_CAPABILITIES_DIR` | YAML 已发布能力目录；留空则不启用能力管理 |
| 文档 | `COPILOT_DOCS_DIR` | 「使用手册」markdown 目录；留空取工作目录 `docs/`，生产可挂到只读卷设绝对路径 |
| 能力市场 | （迁移自动建表） | `/v1/marketplace/capabilities` 团队共享注册表 |
| MCP | `COPILOT_MCP_SERVERS` | JSON 数组，启动发现外部 MCP 工具 |
| 告警 | `COPILOT_ALERT_WEBHOOK_SECRET` | webhook HMAC 密钥；**未设则 webhook 路由 503 fail-closed** |
| 通知 | `COPILOT_FEISHU_WEBHOOK_URL` | 飞书机器人；留空仅日志 |
| 审计 | `COPILOT_AUDIT_FALLBACK_ENABLED` / `_DIR` | DB 写入失败本地落盘+重放，默认开启 |
| 知识库 | `COPILOT_KNOWLEDGE_EMBEDDER_BASE_URL` / `_API_KEY` / `_MODEL` | RAG 向量化；配好则执行结果自动入库 |
| 可观测 | `COPILOT_OTEL_EXPORTER` / `COPILOT_OTEL_OTLP_ENDPOINT` | OTLP 链路追踪 |
| 限流 | `COPILOT_RATE_LIMIT_SUBJECT` / `_IP` | 默认 30 req/min·主体、60 req/min·IP |

完整变量与默认值见 [`.env.example`](../.env.example) 与 [configuration.md](configuration.md)。

### 3.2 生成密钥 / 开发 token

```bash
openssl rand -base64 32   # JWT HMAC 密钥
openssl rand -hex 32      # webhook 密钥

export COPILOT_JWT_HMAC_SECRET="<your-secret>"
go run gen_token.go       # 生成 24h 有效的 admin JWT（含 prod/staging/dev 环境白名单）
```

> `gen_token.go` 生成一个 `roles:["admin"]` 的 JWT，仅用于开发/联调，**不要用于生产身份体系**。

### 3.3 生产安全注意

- `COPILOT_JWT_HMAC_SECRET`、`COPILOT_OPENAI_API_KEY`、`COPILOT_KNOWLEDGE_EMBEDDER_API_KEY` 等**严禁入库**，经 K8s Secret / 密钥管理注入。
- `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN` 必须为 `0`。`VITE_DEV_ADMIN_TOKEN` 只用于 dev 代理，勿提交真实 token。
- `COPILOT_CORS_ALLOWED_ORIGINS` 生产限定前端源。
- 能力市场/能力列表读端点对 viewer/operator/admin 开放，**所有写端点仅 admin**；`GET /v1/executions`（含敏感输入/错误）**仅 admin**，operator/viewer 用 `/v1/audit-events`。

---

## 4. HTTP API 与权限总览

鉴权：`Authorization: Bearer <JWT>`（HS256/HMAC），`X-Request-ID` 可透传（否则自动生成）。
除 webhook / CAS / auth-config 外均需登录。

### 4.1 端点速查（路由前缀 → 可用角色）

| 端点 | 角色 |
|------|------|
| `POST /v1/assistant/messages` · `POST /v1/assistant/stream` | 任一登录用户 |
| `GET/POST /v1/assistant/conversations*` | 任一登录用户（归 subject） |
| `POST/GET /v1/assistant/feedback` | 任一登录用户；admin 可按 subject 过滤 |
| `GET /v1/action-plans` · `GET /v1/action-plans/{id}` | viewer/operator/admin（+环境白名单） |
| `POST /v1/action-plans/{id}/confirm` | viewer/operator/admin + 策略/environment |
| `GET /v1/audit-events` · `/v1/audit-events/search` | viewer/operator/admin |
| `GET /v1/executions` | **admin only** |
| `GET /v1/scheduled-tasks*` | 任一登录用户（写/触发=admin） |
| `GET /v1/inspection-reports*` | 任一登录用户 |
| `GET/POST /v1/marketplace/capabilities*` | 读=任一；发布/评分写=admin |
| `GET /v1/capabilities{/name}` | viewer/operator/admin（写=admin） |
| `POST /v1/tools/{name}/read` | 任一登录用户（策略/read-runner 强制） |
| `/v1/auth/config` · `/v1/auth/cas/*` | 无鉴权（登录/OAuth 流程） |
| `POST /v1/alerts/webhook` · `/v1/alerts/alertmanager` | 无 JWT，HMAC `X-Webhook-Signature` 门 |
| `GET/PUT /v1/admin/prompts*` | **admin** |
| `GET/POST /v1/admin/knowledge*` · `GET /v1/admin/knowledge/status` | **admin** |
| `GET/POST /v1/admin/runbook-drafts*` | **admin** |
| `GET /v1/docs/{name}` | **admin**（读取 docs/ 目录 markdown，供「使用手册」视图；白名单文件名 + 防路径穿越） |
| `GET/POST/PUT/DELETE /v1/mcp/servers*` · `POST .../reload` | **admin** |

未匹配 → `404`。部分 admin 端点未配置时返回 `{configured:false}`（prompts / runbook-drafts /
knowledge/status），其余（feedback/knowledge/MCP/marketplace/webhook）返回 **503**。

### 4.2 运维与生产端点（非 /v1）

- `GET /healthz` — 存活探针
- `GET /readyz` — 就绪探针（含 DB ping）
- `GET /metrics` — Prometheus 指标

---

## 5. Web 控制台（capability-console）

单页应用（无路由库，`ref<ActiveView>` 切换），共 **14 个视图**，除 `management` 外均懒加载。

**侧栏「运维」**：

| 视图 | 快捷键 | 说明 |
|------|--------|------|
| AI 运维助手 | Cmd+1 | 与 copilot 对话（SSE 流式 + 自治 agent loop 步骤时间线） |
| 能力接入管理 | Cmd+2 | 能力 YAML 草稿/校验/测试/发布/下架 |
| 待确认计划 | Cmd+3 | 待人工确认的写 action plan（带未决角标） |
| 定时巡检 | Cmd+4 | 定时任务 CRUD/触发/运行历史（带失败数角标） |
| 巡检报告 | — | 巡检报告列表/详情 |

**侧栏「管理配置」**：

| 视图 | 快捷键 | 说明 |
|------|--------|------|
| 审计记录 | Cmd+5 | 审计事件过滤 + 自然语言搜索 |
| 执行历史 | — | **admin**，含敏感输入/错误、plan→审计跳转 |
| 告警全景 | — | incident 只读全景（5 条证据源软匹配 join） |
| 能力市场 | — | 团队共享能力注册表：发布/版本/评分/下载 |
| Prompt 管理 | Cmd+6 | prompt 模板 hot-reload |
| 知识库 | Cmd+7 | RAG 文档列表/摄入/状态 |
| 用户反馈 | Cmd+8 | 评分/纠正 + 改进建议聚合 + **runbook 草稿生成/启用** |
| MCP 服务器 | Cmd+9 | MCP 服务器 热 CRUD/reload |
| 使用手册 | — | 渲染 docs/OPERATIONS.md（admin 只读，`GET /v1/docs/OPERATIONS.md`） |

**鉴权**：开发环境下 Vite 代理注入固定 `VITE_DEV_ADMIN_TOKEN`；生产由前端直接带真实用户
JWT（CAS 登录跳转 `/v1/auth/config` + `/v1/auth/cas/*`）。

---

## 6. 核心工作流

### 6.1 写治理（action plan → 人工确认 → 审计）

```
用户/模型提议写操作
  → 解析为合法注册工具
  → 策略（角色/environment/输入/风险）校验
  → 创建不可变 action plan（dry-run 预览 + risk_notice）
  → 人工确认（POST /v1/action-plans/{id}/confirm）
  → 执行 + 执行后 verification 回读
  → 全程 audit + trace
```

- **低风险 Runbook**（如保留策略调整）可自动执行，返回 `execution_result` + 信息块。
- **自治 agent loop 只读自转**：多步循环中写操作永远停在 plan 创建 + `confirmation_required`，
  从不自动执行写（loop 内低风险 Runbook 自动执行也禁用）。
- 详见 [assistant.md](assistant.md)。

### 6.2 Runbook 自演化（反馈 → 草稿 → 确认启用）

`用户反馈`视图把负向反馈按关键词聚合为主题，**确定性规则**生成候选 runbook 草稿
（意图匹配 / 工具序列 / 风险等级），操作员**确认后**经 `CreateRunbook(IsEnabled=true)` 写入
SQL 注册表，`RunbookRouter` **即时命中**——无需改代码。

- 可落 runbook 主题：`retention`、`capability-call`、`latency` 等（映射到真实可读工具名白名单）。
- 不可落主题（如 `format`、未归类）跳过并在 UI 标注原因。
- 端点：`POST /v1/admin/runbook-drafts/infer`、`GET /v1/admin/runbook-drafts`、
  `POST /v1/admin/runbook-drafts/{id}/activate`。

### 6.3 告警接入

外部监控（Prometheus Alertmanager 原生 payload + 通用 webhook）经 HMAC 签名推入 →
归一化 → 去重（source+external_id）→ 落 `copilot_alerts` → 供 `alert.query` / incident 全景读取。
详见 [alerting.md](alerting.md)。

### 6.4 能力接入与发布

- **能力**（YAML）：草稿 → 校验/测试（走 HTTPAdapter，只返回归一化结果）→ `publish` → 运行时按
  `status: published` 加载。写能力默认不自动执行，走确认 executor。
- **能力市场**：团队共享的**版本化**注册表，支持发布/版本回溯/下载/评分/依赖管理与 CI 校验。
  详见 [capability-marketplace.md](capability-marketplace.md)、[capability-ci.md](capability-ci.md)。

---

## 7. 测试与构建

代码仓库内已有一级 Makefile 目标（见 `Makefile` 顶部 `.PHONY` 列表，`make <target>` 直接调用）。

```bash
# 后端
make test              # go test -race ./...
make vet               # go vet ./...
make lint              # golangci-lint run ./...
make test-integration  # Docker MySQL 迁移断言（需 Docker）
make eval              # EinoPlanner 评估套件（build tag 隔离，产出 eval/report.md）
make cover             # 覆盖率

# 前端
make web-typecheck     # vue-tsc --noEmit（独立类型检查）
make web-test          # npx vitest run
make web-check         # 类型检查 + 单测（提交前端改动前的完整门禁）

# 全量
make all-checks        # vet + lint + test + web-check
make build             # go build -o copilot-api
make web-build         # vue-tsc + vite build
```

> 说明：`make web-test`/`all-checks` 整仓并行跑时，个别 5s 超时用例（如
> `ReviewStage.test.ts`）可能偶发抖动——那是并行负载下的测试时限问题，单文件重跑即过，
> 与业务代码无涉。

---

## 8. 部署上线

### 8.1 生产运行形态

目前**生产持久化目标为 MySQL 8**（迁移在启动时应用，`copilot_schema_migrations` 台账保证
幂等；Docker MySQL 初始化钩子先行应用同名 DDL 也会被容忍）。SQLite 仅供本地/测试。

推荐生产拓扑：

```
        ┌───────────────┐
用户 →  │ Ingress / TLS │ → capability-console（静态 / 代理）
        └───────┬───────┘
                │ /v1 (JWT / CAS)
        ┌───────▼──────────┐        ┌──────────────┐
        │  copilot-api(s)   │ ─────→ │  MySQL 8     │
        │  (HPA ≥2 副本)    │        └──────────────┘
        └───────┬──────────┘
                │           │ OTLP → otel-collector → Jaeger/Tempo
                ├─ HTTP → 中间件能力后端
                └─ 外部 MCP servers / 飞书 / webhook
```

### 8.2 暂未就绪的容器化项（上线前需补齐）

- **根目录 `Dockerfile`（copilot-api）不存在**：`docker-compose.yml` 中 `copilot-api` 与
  `capability-console` 的 `build:` 均标了 "requires Dockerfile (not yet created)"。上线要么
  走裸进程/Systemd/K8s 直接部署二进制，要么先补两个 Dockerfile。
- `docker-compose.yml` 提供 `mysql`、`otel-collector`、`mock-middleware` 等开发辅助服务；
  `copilot-api` / `capability-console` 容器化后即可一键编排。
- CI（`.github/workflows/capability-validation.yml`）目前**仅校验能力 YAML**（语法/schema/必填/
  机密扫描/dry-run + PR 评论），**尚未跑 Go 测试与前端测试**——建议上线前把 `make all-checks`
  并入 CI。

### 8.3 启动说明要点

- 迁移在**启动时自动应用**，无需手工 `migrate` 命令；需保证进程工作目录可读到
  `migrations/*.sql`。
- 审计兜底默认开启：DB 写失败时本地 JSON 落盘 + 后台重放，务必为 `COPILOT_AUDIT_FALLBACK_DIR`
  挂持久卷。
- 就绪探针 `GET /readyz`（含 DB ping）用于部署健康检查；`/metrics` 供 Prometheus 抓取。

---

## 9. 上线前检查清单

**配置/安全**
- [ ] `COPILOT_JWT_HMAC_SECRET` 设为强随机（`openssl rand -base64 32`），并设定轮换机制（建议 ≤90 天）
- [ ] `COPILOT_AUTH_MODE` 确定（`jwt` / `cas` / `both`），CAS 场景配好 `_SERVER_URL`/`_SERVICE_URL`
- [ ] `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=0`
- [ ] `COPILOT_CORS_ALLOWED_ORIGINS` 限定前端域名（勿留 `*`）
- [ ] 密钥/API Key 经 Secret 注入，**不入库**
- [ ] `COPILOT_ALERT_WEBHOOK_SECRET` 已设（未设则告警 webhook 直接 503）

**数据/存储**
- [ ] 使用 MySQL 8（`COPILOT_DATABASE_DRIVER=mysql`），不建议 SQLite
- [ ] `COPILOT_AUDIT_FALLBACK_DIR` 挂持久卷
- [ ] 数据库定期备份

**能力/模型**
- [ ] `COPILOT_CAPABILITIES_DIR` 指向审阅通过的已发布能力，确认写能力走确认 executor
- [ ] LLM 配置就绪（`COPILOT_OPENAI_*`），或明确使用确定性 planner
- [ ] `COPILOT_PROMPTS_DIR` 指向线上 prompt 模板
- [ ] `COPILOT_DOCS_DIR` 指向「使用手册」只读卷（留空则取工作目录 `docs/`）
- [ ] 可选：知识库 RAG（`COPILOT_KNOWLEDGE_EMBEDDER_*`）

**可观测/高可用**
- [ ] OTLP 链路追踪（`COPILOT_OTEL_*`）接通 collector
- [ ] Prometheus 抓 `/metrics`，就绪探针用 `/readyz`
- [ ] 副本 ≥2，Ingress 处启用 TLS

**质量门禁**
- [ ] `make all-checks` 全绿
- [ ] （建议）将 `make all-checks` 并入 CI 主链路

---

## 10. 已知限制与注意事项

- **容器镜像未就绪**：`copilot-api` 与 `capability-console` 尚无 Dockerfile；docker-compose 容器化是上线前置项。
- **CI 覆盖不完整**：现有 CI 只校验能力 YAML，Go/前端测试不在 CI。
- **`.env` 自动加载**：`main.go` 会自动加载 `.env`（已存在的真实环境变量优先），与
  `.env.example` 顶部注释"服务不会自动加载 .env"不一致——以 `.env` 文件描述为准。
- **SQLite 迁移内嵌列表到 `014`**，市场（`015` 能力市场）在 SQLite 经 `internal/store/db.go`
  内联 SQL 补齐；**生产走 MySQL 时读取 `migrations/*.sql` 文件**。
- **runbook 草稿存储为内存态**，重启即清（已启用 runbook 落 SQL 持久化）；如需草稿持久化需另行迭代。
- **连接池参数尚未环境化**（硬编码），需要更细调优时需改源码。

---

## 11. 目录速查

| 路径 | 说明 |
|------|------|
| `cmd/copilot-api/` | 后端主程序（`main.go`），启动组装 |
| `cmd/capability-importer/` `cmd/capability-validator/` | 能力导入 / CI 校验 CLI |
| `internal/httpapi/` | HTTP 路由与 handler（`router.go` + 各领域 handler） |
| `internal/store/` | SQL（MySQL/SQLite）持久化 + 迁移 |
| `internal/assistant/` | planner、runbook router、agent loop |
| `internal/tools/` | 平台元工具注册表与执行链 |
| `internal/policy/` `internal/audit/` `internal/plans/` | 权限 / 审计 / 计划 |
| `internal/marketplace/` `internal/capabilities/` `internal/mcp/` | 市场 / 能力 / MCP |
| `internal/scheduler/` `internal/diagnostics/` `internal/execution/` | 巡检 / 诊断 / 执行 |
| `apps/capability-console/` | Web 控制台（Vue 3 + TS） |
| `migrations/` | MySQL 迁移 SQL |
| `examples/capabilities/` | 能力示例（discovered / published） |
| `prompts/` | prompt 模板 |
| `docs/` | 专项文档 |
| `tests/e2e/` | Go e2e 测试 |
