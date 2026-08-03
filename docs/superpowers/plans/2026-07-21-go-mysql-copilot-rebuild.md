# Go + MySQL AI 运维副驾驶重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 以 Go 和 MySQL 重建安全的 AI 运维副驾驶后端基础，替换现有 TypeScript 原型。

**Architecture:** 一个模块化 Go HTTP 服务承载身份、工具白名单、策略、计划、执行与审计；MySQL 存储不可变计划和幂等执行记录。React 保留到后续 API 稳定后接入。

**Tech Stack:** Go 1.24、chi、sqlc、goose、go-sql-driver/mysql、github.com/mattn/go-sqlite3、testify、MySQL 8。

## Global Constraints

- 删除 TypeScript 后端和 PostgreSQL 原型，不保留双写或双服务。
- JWT 仅投影 subject、roles、allowed environments、request ID。
- 模型和 JWT 均不能决定工具权限；服务端 role-to-permission 映射是唯一来源。
- 写操作必须经过计划、确认、乐观锁和幂等键；确认后输入、工具和风险不可变。
- 禁止注册 L3 删除或批量不可逆动作。
- 生产持久化目标是 MySQL 8；本地默认测试使用 SQLite 覆盖真实 SQL 约束、外键和幂等路径，MySQL 通过 `make test-integration` 单独验证。

---

## File Structure

- `cmd/copilot-api/main.go`：服务入口与依赖组装。
- `internal/identity/identity.go`：JWT 投影。
- `internal/tools/registry.go`：工具 allowlist。
- `internal/policy/policy.go`：角色权限和环境校验。
- `internal/plans/service.go`：计划状态机。
- `internal/store/*.sql`：sqlc 查询。
- `migrations/001_copilot.sql`：MySQL 表、索引与约束。
- `internal/store/db.go`：MySQL 迁移入口与 SQLite 本地测试迁移入口。
- `internal/*/*_test.go`：模块测试。
- `docker-compose.yml`：MySQL 8 集成测试环境。

### Task 1: 清理原型并建立 Go/MySQL 基座

**Files:**
- Delete: `apps/copilot-api`, `packages`, `infra/postgres`, `scripts/migrate-postgres.mjs`, Node 工作区配置与 Node 测试
- Create: `go.mod`, `cmd/copilot-api/main.go`, `docker-compose.yml`, `migrations/001_copilot.sql`, `internal/store/db.go`
- Test: `internal/store/db_test.go`

- [ ] **Step 1: 写失败迁移测试**

```go
func TestMigrationsCreateActionPlans(t *testing.T) {
  db := testMySQL(t)
  require.NoError(t, ApplyMigrations(db))
  require.True(t, tableExists(db, "action_plans"))
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/store -run TestMigrationsCreateActionPlans`
Expected: FAIL，因为 MySQL 存储尚不存在。

- [ ] **Step 3: 实现最小 Go 模块和迁移**

迁移创建 `action_plans`、`tool_executions`、`copilot_audit_events`；`tool_executions.idempotency_key` 唯一。计划表包含 `version`、`input_hash`、`confirmation_token_hash` 和状态字段。

- [ ] **Step 4: 运行 GREEN**

Run: `go test ./internal/store && go vet ./...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "feat: bootstrap Go copilot storage"
```

### Task 2: 身份、工具白名单和策略

**Files:**
- Create: `internal/identity/identity.go`, `internal/tools/registry.go`, `internal/policy/policy.go`
- Test: `internal/identity/identity_test.go`, `internal/tools/registry_test.go`, `internal/policy/policy_test.go`

**Interfaces:**
- Produces: `CurrentUser{Subject, Roles, AllowedEnvironments, RequestID}`、`Evaluate(user, tool, input) Decision`。

- [ ] **Step 1: 写失败测试**

```go
func TestEvaluateRejectsRoleWithoutToolPermission(t *testing.T) {
  d := Evaluate(user("viewer", "prod"), writeTool, map[string]any{"environment":"prod"})
  require.Equal(t, PermissionDenied, d.Reason)
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/policy -run TestEvaluateRejectsRoleWithoutToolPermission`
Expected: FAIL，因为策略未实现。

- [ ] **Step 3: 实现最小策略**

工具输入以 JSON Schema 或 Go validator 校验；写工具必须有风险与回滚说明。角色映射在 Go 常量/配置中维护，环境、工具和风险逐项拒绝。

- [ ] **Step 4: 运行 GREEN**

Run: `go test ./internal/identity ./internal/tools ./internal/policy`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal
git commit -m "feat: add Go identity and policy controls"
```

### Task 3: 不可变计划、确认和幂等执行

**Files:**
- Create: `internal/plans/service.go`, `internal/execution/service.go`, `internal/audit/service.go`
- Test: `internal/plans/service_test.go`, `internal/execution/service_test.go`

**Interfaces:**
- Produces: `CreatePlan`、`ConfirmPlan`、`ExecuteConfirmedPlan`。

- [ ] **Step 1: 写失败测试**

```go
func TestConfirmedPlanCannotChangeInput(t *testing.T) {
  plan := createAndConfirm(t)
  err := service.ExecuteConfirmedPlan(ctx, plan.ID, map[string]any{"partitions": 16})
  require.ErrorContains(t, err, "immutable")
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/plans -run TestConfirmedPlanCannotChangeInput`
Expected: FAIL，因为计划服务未实现。

- [ ] **Step 3: 实现事务状态机**

创建计划时哈希规范化输入并设 10 分钟过期；确认使用 `WHERE status='pending_confirmation' AND version=?` 条件更新；执行只从数据库快照读取输入，并以 `plan:<id>:<inputHash>` 写入唯一幂等键和审计事件。

- [ ] **Step 4: 运行 GREEN**

Run: `go test ./internal/plans ./internal/execution ./internal/audit`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal migrations
git commit -m "feat: add immutable Go action plans"
```

### Task 4: 只读工具 API 与验收环境

**Files:**
- Create: `internal/httpapi/router.go`, `internal/execution/readonly.go`, `tests/e2e/readonly_test.go`, `README.md`
- Test: `internal/httpapi/router_test.go`

- [ ] **Step 1: 写失败 HTTP 测试**

```go
func TestReadToolRequiresAuthenticatedRole(t *testing.T) {
  r := newRouter(fakeService)
  res := httptest.NewRecorder()
  r.ServeHTTP(res, requestWithoutJWT())
  require.Equal(t, http.StatusUnauthorized, res.Code)
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/httpapi -run TestReadToolRequiresAuthenticatedRole`
Expected: FAIL，因为路由未实现。

- [ ] **Step 3: 实现最小 API**

提供 `POST /v1/tools/:name/read`；路由验证 JWT，调用策略层，执行只读 allowlist 工具，设置 5 秒超时、10 KB 响应上限并写审计。README 写明 `docker compose up -d mysql`、迁移和测试命令。

- [ ] **Step 4: 运行验收**

Run: `go test ./... && go vet ./...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add cmd internal tests README.md docker-compose.yml
git commit -m "feat: expose governed Go read tools"
```

## Plan Self-Review

- 覆盖 Go 替换、MySQL 迁移、角色权限、工具白名单、不可变计划、幂等、审计和只读 API。
- AI 编排、RAG 与 React 确认界面在安全后端稳定后另建计划，避免与基础重构混合。
