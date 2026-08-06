# 生产上线 Checklist

本清单用于**首次生产落地**的逐步走查，粒度比 [OPERATIONS.md](./OPERATIONS.md) §9 的
checkbox 列表更细：每步给出具体配置值、命令与验证方式。建议按阶段顺序执行并在每一步
末尾验证，**上线前逐项打勾**。

- 前置阅读：[OPERATIONS.md](./OPERATIONS.md)（手册）、[configuration.md](./configuration.md)、
  [config.prod.yaml.example](../config.prod.yaml.example)
- 适用范围：单二进制托管前端（默认形态）或 容器/nginx 反代（可选形态），两者共用 env 配置。
- 约定：`<域名>` = 你的公网入口域名（如 `console.example.com`）；所有示例命令可复制到 bash。

---

## 阶段 0：版本与可重复构建

- [ ] 用 tag 标记本次镜像/二进制（示例做法：`git tag v0.3.0 && git push origin v0.3.0`），
      build 产物与源码 tag 一一对应，便于回滚定位。
- [ ] `make all-checks` 全绿（CI 的 `checks` job 在 PR / push main 自动跑）。
- [ ] **确认构建出的前端是真实产物而非占位页**：
  - `scripts/build.sh` 全量路径：web 先 build 并把 `apps/capability-console/dist` 拷入
    `internal/webui/dist`，随后 `go build` 内嵌。
  - 验证：`curl -s http://<host>:<port>/ | grep -i capability-console`（占位页标题是
    “AI Operations Copilot” / 空 HTML，真实产物标题是 “Capability Console”）。
  - ⚠️ 若用**根目录 Dockerfile** 构建镜像，它当前**不会**先跑 web build，也不会拷 dist——
    镜像内嵌的是占位页。需要单二进制前端时，先 `cd apps/capability-console && npm ci && npm run build`
    再 `cp -R dist/. <repo>/internal/webui/dist/`，最后 `docker build`；否则去掉 docker-compose 里
    独立的 capability-console（nginx 反代），统一走单二进制。

---

## 阶段 1：环境变量逐项

> 建议从 `.env.example` 复制为 `.env`，逐个填写。`main.go` 会自动加载 `.env`（已存在的
> 真实环境变量优先）。生产建议由外层进程 / Secret 注入，`.env` 不入库。

### 1.1 认证（最关键，先配）
- [ ] `COPILOT_AUTH_MODE`：`cas`（仅 SSO）或 `both`（CAS + JWT 共存）。内网工具一般 `cas`。
- [ ] `COPILOT_JWT_HMAC_SECRET`：**强随机**，`openssl rand -base64 32`。
      由它签发 CAS 会话 cookie 与 JWT。见阶段 3 轮换。
- [ ] `COPILOT_CAS_SERVER_URL`：真实 CAS 服务器根地址（**HTTPS**），形如
      `https://cas.example.com`。**禁用 cas-test mock**。
- [ ] `COPILOT_CAS_SERVICE_URL`：本服务公网地址（**HTTPS**），形如 `https://console.example.com`。
      回调路径由代码固定为 `ServiceURL + "/v1/auth/cas/callback"`，无需另行配置。
      ⚠️ cookie 的 `Secure` 标志由 `ServiceURL` 是否 `https://` 决定——**必须 HTTPS**，否则会话 cookie
      在浏览器 HTTPS 页面上不会被发送。
- [ ] `COPILOT_CAS_SESSION_TTL`：会话有效期（如 `8h`），按安全要求收紧（如 `30m`～`8h`）。
- [ ] `COPILOT_CAS_DEFAULT_ROLES`：CAS 未下发 `roles` 属性时的默认角色。**保持不含 `admin`**
      （默认 `["operator"]`）；admin 仅由 CAS 下发 `roles` 属性授予，见阶段 2。
- [ ] `COPILOT_CAS_DEFAULT_ENVIRONMENTS`：默认允许环境（默认 `["prod","staging","dev"]`）。
- [ ] `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=0`（生产强制）。

### 1.2 网络 / 安全
- [ ] `COPILOT_HTTP_ADDR`：监听地址（如 `:18080`）。对外经 Ingress / TLS 终止。
- [ ] `COPILOT_CORS_ALLOWED_ORIGINS`：**设为前端实际来源**，逗号分隔
      （如 `https://console.example.com`）。**留空 = 允许所有 `*`，生产禁用**。
- [ ] `COPILOT_ALERT_WEBHOOK_SECRET`：外部告警推送的 HMAC-SHA256 密钥。
      **不设则 webhook 路由 503（fail-closed）**——若上线要接告警，务必配好。

### 1.3 数据 / 存储
- [ ] `COPILOT_DATABASE_DRIVER=mysql`；`COPILOT_DATABASE_DSN` 指向 MySQL 8，
      DSN 形如 `copilot:密码@tcp(<db-host>:3306)/copilot?parseTime=true`。
      （Docker 形态见 [docker-compose.yml](../docker-compose.yml)：`copilot:copilot-password@tcp(mysql:3306)/copilot`。）
- [ ] `COPILOT_AUDIT_FALLBACK_DIR`：**挂持久卷**（审计 DB 写失败时本地 JSON 落盘 + 后台重放）。
- [ ] `COPILOT_DOCS_DIR`、`COPILOT_PROMPTS_DIR`：指向只读的线上文档 / prompt 模板目录。

### 1.4 能力 / 模型 / 知识
- [ ] `COPILOT_CAPABILITIES_DIR`：指向**审阅通过的已发布能力**父目录（目录内含 `published/` 子目录，
      程序会自动追加 `/published`）。形如 `./examples/capabilities`。
- [ ] `COPILOT_OPENAI_*`（或明确使用确定性 planner）：Provider / Key / Model / Timeout / Retry。
      API Key 走 Secret。
- [ ] 可选：`COPILOT_KNOWLEDGE_EMBEDDER_*`（RAG 向量化）；`COPILOT_FEISHU_WEBHOOK_URL`（待确认动作通知）；
      `COPILOT_OTEL_*`（见阶段 5）；`COPILOT_MCP_SERVERS`（外部 MCP，含内嵌 Secret 需谨慎）。

### 1.5 清单核对
- [ ] 用 `scripts/status.sh` 或直接 `ps` 确认启动；`scripts/logs.sh` 看启动日志无 warning/error。
- [ ] 健康检查：`curl -fsS http://127.0.0.1:18080/healthz` 与 `/readyz`（`readyz` 含 DB ping）。

---

## 阶段 2：CAS 注册与 admin 授权

- [ ] 在 CAS 服务器注册本应用：
  - 服务标识符（service URL）：`https://console.example.com/v1/auth/cas/callback`
    （**必须与 `COPILOT_CAS_SERVICE_URL + "/v1/auth/cas/callback"` 完全一致**，否则票据校验报
    INVALID_SERVICE——mock cas_test 对这一项是**严格比对**的）。
  - 回调必须走 HTTPS。
- [ ] admin 角色授予：CAS 登录返回的 `roles` 属性会被采纳为角色。
      - 需给某账号 admin：在 CAS 侧/用户目录为该账号配 `roles` 包含 `admin`；
      - 不要把 `admin` 放进 `COPILOT_CAS_DEFAULT_ROLES`（那是给**所有**默认用户的兜底角色）。
- [ ] 端到端验证（本地对 CAS 预设一个测试账号即可，不必动用户表）：
      浏览器打开 `https://console.example.com` → 未登录重定向到 CAS `/login` → 登录 → 回跳
      `.../v1/auth/cas/callback?ticket=ST-...` → 校验通过 → 种下 `copilot_cas_session` cookie →
      回到控制台并显示当前用户。
- [ ] 登出路径：确认 CAS `/logout` 与本地 `ClearSessionCookie`（过期 cookie）配合，退出后能重新登录。

---

## 阶段 3：密钥轮换与失效预期

- [ ] **`COPILOT_JWT_HMAC_SECRET` 轮换周期 ≤ 90 天**，纳入排班告警/日历提醒。
- [ ] 轮换流程（双写兼容或一次性切换都行，推荐前者）：
  - 短时窗口内新旧 secret 均被接受的实现（若代码支持）先双写 → 再移除旧值；
  - 否则：选低峰切换，重启服务。
- [ ] **失败预期要提前对齐**：轮换会**失效所有在途的 CAS 会话**（session cookie 由该 secret 签名）与
      JWT——切换后用户需重新登录。告知运维/用户，避免当成故障。
- [ ] 其它密钥同样评审：`COPILOT_ALERT_WEBHOOK_SECRET`、LLM API Key、`COPILOT_MCP_SERVERS` 内嵌 token
      的轮换与存放（一律进 Secret 管理器，不入镜像/库）。

---

## 阶段 4：数据库迁移与备份

- [ ] 迁移在**启动时自动应用**（`copilot_schema_migrations` 台账保证幂等）。确认进程工作目录可读到
      `migrations/*.sql`（容器已内置 `/app/migrations`）。
- [ ] **上线首个版本前对生产 MySQL 做全量快照/备份**（mysqldump 或托管快照）。
- [ ] 建立**定期自动备份**策略并验证恢复：
      - 每日 dump + 转储到异地/对象存储；保留 N 份。
      - **至少演练一次恢复**到干净的库，确认迁移台账 + 数据都回来。
- [ ] 运行期数据目录（MySQL 数据卷、`COPILOT_AUDIT_FALLBACK_DIR`）确认持久化，容器重建不丢。
- [ ] 若历史上有根目录的 `copilot-local.db`（sqlite demo 产物）躺进工作区，清理并确保不要随部署覆盖生产。

---

## 阶段 5：可观测性与告警

- [ ] 链路追踪：`COPILOT_OTEL_EXPORTER=otlp`、`COPILOT_OTEL_OTLP_ENDPOINT=<collector>`，
      接 `otel-collector` → Jaeger/Tempo。VITE/Jaeger 链接按环境调。
- [ ] 指标：Prometheus 抓 `http://<pod>:18080/metrics`。
- [ ] 日志：`COPILOT_LOG_LEVEL=info`（调试完勿留 `debug`）；收集到集中式日志。
- [ ] **可用性告警**（建议至少以下三类）：
      1. Liveness/Readiness：`/healthz`、`/readyz` 失败持续 N 分钟 → 通知；
      2. 错误率：API 5xx 比例 / 关键端点（如 CAS 回调、`/v1/*`）错误率超阈值 → 通知；
      3. 资源：CPU/内存/磁盘/DB 连接数。
- [ ] 审计可观测：开审计兜底（`COPILOT_AUDIT_FALLBACK_ENABLED=1`）+ 告警提示，避免静默丢失审计流水。

---

## 阶段 6：高可用与上线放行

- [ ] 副本 ≥ 2（K8s HPA 见 [OPERATIONS.md](./OPERATIONS.md) §8.1；裸进程/Systemd 至少 2 个实例 + 前置 LB）。
- [ ] Ingress / LB 层启用 TLS（`Secure` cookie 依赖，见阶段 1.1），证书自动续期（如 cert-manager / caddy）。
- [ ] 就绪探针用 `/readyz`（含 DB），滚动更新先等新实例 ready 再摘旧实例。
- [ ] 蓝绿或滚动发布，保留上一版本 tag 可随时回滚。
- [ ] **上线后 24–48h 观察**：登录成功率（CAS 回调错误日志）、webhook 接入量、昨日备份成功、
      磁盘/内存水位、审计流水完整。

---

## 附：常见坑速查

| 症状 | 根因 / 检查 |
|------|------------|
| CAS 回跳报 INVALID_SERVICE | `COPILOT_CAS_SERVICE_URL` 与 CAS 注册的 service URL 不一致（含 `/v1/auth/cas/callback` 拼接是否一致、http/https、尾斜杠） |
| 登录后 cookie 不生效 | `COPILOT_CAS_SERVICE_URL` 非 https → cookie 非 Secure，浏览器 HTTPS 页不发送 |
| webhook 一直 503 | `COPILOT_ALERT_WEBHOOK_SECRET` 未设置（fail-closed） |
| 前端是占位页 | 用根 Dockerfile 构建但没先 build web / 拷 dist，见阶段 0 |
| 轮换 secret 后全员掉线 | 正常预期：在途 CAS 会话/JWT 全部失效，需重新登录（见阶段 3） |
| capabilities 500 | `COPILOT_CAPABILITIES_DIR` 需指向**含 `published/` 的父目录**（程序自动追加 `/published`） |
| 生产用了 cas-test | cas-test 仅本地 mock，**严禁上生产** |
