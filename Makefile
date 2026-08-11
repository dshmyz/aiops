.PHONY: test test-integration lint vet cover build docker-build web-build web-test web-typecheck web-check e2e all-checks dev-up dev-down dev-logs dev-verify-trace dev-verify-verification eval gen-token compose-up compose-down compose-logs scripts-build scripts-start scripts-stop scripts-status scripts-logs scripts-health scripts-token scripts-nginx dev-enrich

test:
	go test -race ./...

# Starts the compose MySQL service and runs the migration assertions against a
# fresh database. It fails if Docker cannot provide a usable MySQL instance.
test-integration:
	docker compose up -d --wait mysql
	COPILOT_REQUIRE_MYSQL=1 COPILOT_TEST_MYSQL_DSN='root:copilot-root-password@tcp(127.0.0.1:3306)/copilot?parseTime=true' go test -count=1 -v ./internal/store -run '^TestMySQLMigrations'

# === Code quality ===

lint:
	@which golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Install it:"; \
		echo "  macOS:   brew install golangci-lint"; \
		echo "  Other:   https://golangci-lint.run/usage/install/"; \
		exit 1; }
	golangci-lint run ./...

vet:
	go vet ./...

# EinoPlanner 评估套件（build tag 隔离，手动跑）：生成 internal/assistant/eval/report.md
eval:
	go test -tags=eval ./internal/assistant/eval/...

cover:
	go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

# === Build ===

build:
	go build -o copilot-api ./cmd/copilot-api

docker-build:
	docker build -t copilot-api .

web-build:
	cd apps/capability-console && npm run build

# === Operational tooling ===

# Generate a 24h admin JWT for dev/联调. Sources ./.env so COPILOT_JWT_HMAC_SECRET
# is picked up; the token is only meant for local testing, not production identity.
gen-token:
	@if [ -f .env ]; then set -a; . ./.env; set +a; else echo "WARN: no ./.env — use COPILOT_JWT_HMAC_SECRET env"; fi; \
	go run ./gen_token.go

# dev-enrich: 检查「导入流程 LLM 辅助」是否已配好（贴 Swagger 后自动补参数说明/
# 示例/枚举）。需要一个 eino LLM planner 才会富化；纯规则/确定性 planner 下富化不
# 生效（但不影响能力导入）。逐项告警，让你一眼看出少了哪个，避免静默 no-op。
dev-enrich:
	@echo "== 导入 LLM 辅助（v0.7.0+）配置检查 =="; \
	set -a; [ -f .env ] && . ./.env; set +a; \
	if [ "$${COPILOT_ASSISTANT_PROVIDER:-}" = "eino-openai" ]; then echo "[ok] COPILOT_ASSISTANT_PROVIDER=eino-openai (LLM planner)"; else echo "[缺] COPILOT_ASSISTANT_PROVIDER 应为 eino-openai（否则走纯规则，不富化）"; fi; \
	if [ -n "$${COPILOT_OPENAI_API_KEY:-}" ]; then echo "[ok] COPILOT_OPENAI_API_KEY 已设置"; else echo "[缺] COPILOT_OPENAI_API_KEY（LLM 调用需要）"; fi; \
	if [ -n "$${COPILOT_OPENAI_MODEL:-}" ]; then echo "[ok] COPILOT_OPENAI_MODEL=$${COPILOT_OPENAI_MODEL}"; else echo "[缺] COPILOT_OPENAI_MODEL（默认可用但建议显式）"; fi; \
	if [ -n "$${COPILOT_CAPABILITIES_DIR:-}" ]; then echo "[ok] COPILOT_CAPABILITIES_DIR=$${COPILOT_CAPABILITIES_DIR}"; else echo "[缺] COPILOT_CAPABILITIES_DIR（能力管理需此目录）"; fi; \
	echo "== 全部 [ok] 即富化开启；有 [缺] 则按提示补齐后重启后端。 =="

# Build & run the full containerized stack (mysql + copilot-api + capability-console)
# via docker-compose. Requires the two Dockerfiles and a JWT secret.
compose-up:
	docker compose up -d --build

compose-down:
	docker compose down

compose-logs:
	docker compose logs -f

# === Shell 运维脚本（线上无 Docker 时用 scripts/*.sh）===
# 透传到 scripts/*.sh；详见 docs/OPERATIONS.md「纯脚本部署」。
scripts-build:
	bash scripts/build.sh

scripts-start:
	bash scripts/start.sh

scripts-stop:
	bash scripts/stop.sh

scripts-status:
	bash scripts/status.sh

scripts-logs:
	bash scripts/logs.sh

scripts-health:
	bash scripts/health.sh

scripts-token:
	bash scripts/gen-token.sh

scripts-nginx:
	bash scripts/nginx.sh generate

# Standalone Vue typecheck (vue-tsc --noEmit). `web-build` also runs it, but this
# target isolates the check without emitting a production bundle.
web-typecheck:
	cd apps/capability-console && npx vue-tsc --noEmit

web-test:
	cd apps/capability-console && npx vitest run

# Frontend gate: typecheck + unit tests — the full verification run used before
# committing console changes.
web-check: web-typecheck web-test
	@echo "console typecheck + tests passed"

# === End-to-end ===

e2e:
	go test ./tests/e2e/...

# === Aggregated checks ===

# Complete local gate before push: Go lint+vet+race tests, then the full
# console front-end gate (typecheck + unit tests).
all-checks: vet lint test web-check
	@echo "all checks passed"

# === Local full-stack dev environment ===
# Starts mock middleware API (19090), Copilot API (18080, dev tokens on) and
# the Vue console (5173) in the background. Use `make dev-down` to stop.
DEV_ADMIN_TOKEN ?= dev-admin-fallback-token
DEV_PID_DIR := /tmp/copilot-dev-pids

dev-up:
	@mkdir -p $(DEV_PID_DIR)
	@for name in mock api web; do [ -f $(DEV_PID_DIR)/$$name.pid ] && kill -0 $$(cat $(DEV_PID_DIR)/$$name.pid) 2>/dev/null || rm -f $(DEV_PID_DIR)/$$name.pid; done
	@if [ -f $(DEV_PID_DIR)/mock.pid ] && kill -0 $$(cat $(DEV_PID_DIR)/mock.pid) 2>/dev/null; then echo "mock already running"; else \
		nohup node examples/mock-middleware-api.js > $(DEV_PID_DIR)/mock.log 2>&1 < /dev/null & echo $$! > $(DEV_PID_DIR)/mock.pid; \
		echo "mock middleware API started on http://127.0.0.1:19090 (pid $$(cat $(DEV_PID_DIR)/mock.pid))"; fi
	@if [ -f $(DEV_PID_DIR)/api.pid ] && kill -0 $$(cat $(DEV_PID_DIR)/api.pid) 2>/dev/null; then echo "copilot-api already running"; else \
		if [ -f .env ]; then set -a; . ./.env; set +a; else echo "WARN: no ./.env — copy .env.example to .env or set env vars manually"; fi; \
		nohup go run ./cmd/copilot-api > $(DEV_PID_DIR)/api.log 2>&1 < /dev/null & echo $$! > $(DEV_PID_DIR)/api.pid; \
		echo "copilot-api starting on http://127.0.0.1:18080 (pid $$(cat $(DEV_PID_DIR)/api.pid))"; fi
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		curl --noproxy '*' -sf -o /dev/null http://127.0.0.1:18080/v1/capabilities -H "Authorization: Bearer $(shell awk -F= '/^VITE_DEV_ADMIN_TOKEN=/{print $$2}' .env 2>/dev/null)" && break; \
		echo "waiting for copilot-api... ($$i)"; sleep 1; done
	@if [ -f $(DEV_PID_DIR)/web.pid ] && kill -0 $$(cat $(DEV_PID_DIR)/web.pid) 2>/dev/null; then echo "console already running"; else \
		cd apps/capability-console && nohup npm run dev -- --port 5173 > $(DEV_PID_DIR)/web.log 2>&1 < /dev/null & echo $$! > $(DEV_PID_DIR)/web.pid; \
		echo "capability console starting on http://127.0.0.1:5173 (pid $$(cat $(DEV_PID_DIR)/web.pid))"; fi
	@echo ""
	@echo "=== 全栈已启动 ==="
	@echo "  Vue 控制台:    http://127.0.0.1:5173"
	@echo "  Copilot API:   http://127.0.0.1:18080"
	@echo "  Mock 中间件:    http://127.0.0.1:19090"
	@echo "  日志目录:       $(DEV_PID_DIR)/{mock,api,web}.log"
	@echo "  停止:           make dev-down"
	@echo "  验证 trace:     make dev-verify-trace"
	@echo "  验证 verification: make dev-verify-verification"

dev-down:
	@for name in web api mock; do \
		if [ -f $(DEV_PID_DIR)/$$name.pid ]; then \
			pid=$$(cat $(DEV_PID_DIR)/$$name.pid); \
			if kill -0 $$pid 2>/dev/null; then kill $$pid; echo "stopped $$name (pid $$pid)"; fi; \
			rm -f $(DEV_PID_DIR)/$$name.pid; \
		fi; \
	done

dev-logs:
	@tail -f $(DEV_PID_DIR)/mock.log $(DEV_PID_DIR)/api.log $(DEV_PID_DIR)/web.log

# Calls the assistant endpoint and prints the trace field so you can verify
# AI 调用链透明化 without opening the browser.
dev-verify-trace:
	@echo "--- 1) Read 场景 (cluster.status.read) ---"
	@curl --noproxy '*' -sS -X POST http://127.0.0.1:18080/v1/assistant/messages \
		-H "Authorization: Bearer $(DEV_ADMIN_TOKEN)" \
		-H "Content-Type: application/json" \
		-d '{"message":"prod 集群状态"}' | jq '.trace'
	@echo ""
	@echo "--- 2) Write 场景 (topic.retention.set) ---"
	@curl --noproxy '*' -sS -X POST http://127.0.0.1:18080/v1/assistant/messages \
		-H "Authorization: Bearer $(DEV_ADMIN_TOKEN)" \
		-H "Content-Type: application/json" \
		-d '{"message":"prod topic orders retention 48 hours"}' | jq '{type, tool, plan_id, summary, trace}'
	@echo ""
	@echo "--- 3) Clarification 场景 (缺少 environment) ---"
	@curl --noproxy '*' -sS -X POST http://127.0.0.1:18080/v1/assistant/messages \
		-H "Authorization: Bearer $(DEV_ADMIN_TOKEN)" \
		-H "Content-Type: application/json" \
		-d '{"message":"hello"}' | jq '{type, message, trace}'

# Runs the full post-execution verification pipeline against the dev stack:
# 1) assistant creates a write plan via the dynamic capability,
# 2) confirm executes the plan,
# 3) the response surfaces the verification field so you can confirm
#    the read-back capability ran and matched the applied value.
dev-verify-verification:
	@response=$$(curl --noproxy '*' -sS -X POST http://127.0.0.1:18080/v1/assistant/messages \
		-H "Authorization: Bearer $(DEV_ADMIN_TOKEN)" \
		-H "Content-Type: application/json" \
		-d '{"message":"set prod k1 orders topic retention_hours=72"}'); \
	plan_id=$$(echo "$$response" | jq -r '.plan_id // empty'); \
	token=$$(echo "$$response" | jq -r '.confirmation_token // empty'); \
	if [ -z "$$plan_id" ] || [ -z "$$token" ]; then \
		echo "--- 1) 触发写能力 plan 失败 ---"; echo "$$response" | jq '.'; exit 1; \
	fi; \
	echo "--- 1) 触发写能力 plan (kafka.topic.retention.set) ---"; \
	echo "plan_id=$$plan_id"; \
	echo ""; \
	echo "--- 2) 确认并执行 plan (期望 verification.status=success) ---"; \
	curl --noproxy '*' -sS -X POST http://127.0.0.1:18080/v1/action-plans/$$plan_id/confirm \
		-H "Authorization: Bearer $(DEV_ADMIN_TOKEN)" \
		-H "Content-Type: application/json" \
		-d "{\"expected_version\":1,\"confirmation_token\":\"$$token\"}" | jq '{type, plan_id, execution_id, status, verification}'
