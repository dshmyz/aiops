# Go + MySQL AI 运维副驾驶重构设计

## 决策

后端统一改为 Go，数据库统一改为 MySQL 8。React 管理后台保留。此前创建的 Node.js/TypeScript 后端、pnpm 工作区和 PostgreSQL 迁移属于原型，将在重构实施时移除，不再继续扩展。

## 目标架构

```text
React 管理后台
  ↓
Go Copilot API
  ├─ identity：JWT 验证与角色投影
  ├─ tools：运维 API 工具白名单
  ├─ policy：角色映射、环境/参数/风险校验
  ├─ plans：计划、确认、状态机与幂等执行
  ├─ assistant：模型工具调用与知识检索
  └─ audit：审计和脱敏
  ↓
MySQL 8
  ↓
既有中间件运维 API / 知识文档库
```

## 技术选择

- Go 1.24、`chi`、`sqlc`、`goose`、`go-sql-driver/mysql`。
- JWT 使用既有网关的 JWKS 或签名密钥验证；只投影 `subject`、`roles`、允许环境和请求 ID。
- 模型通过 OpenAI-compatible HTTP client 接入；模型不能直接访问数据库、网络或原始运维 API。
- 工具通过人工维护的 allowlist 注册；OpenAPI 仅用于生成候选客户端，不自动扩大权限。

## MySQL 数据设计与安全语义

- `action_plans`：计划 JSON、输入哈希、风险、状态、确认令牌哈希、确认人、过期时间。
- `tool_executions`：幂等键唯一索引、执行状态、关联计划、脱敏结果摘要。
- `copilot_audit_events`：请求 ID、用户、工具、动作、脱敏元数据和时间。
- 写操作先创建 `pending_confirmation` 计划；确认以条件更新把计划改为 `confirmed`；执行读取已确认快照，绝不接受客户端再次提交的参数。
- 所有状态更新采用 `WHERE id=? AND status IN (...) AND version=?` 乐观锁；确认后禁止更新工具名、输入、输入哈希和风险。
- MySQL 事务负责计划确认、执行记录创建和审计写入；幂等键避免重复调用外部 API。

## 角色与权限

JWT 只提供角色。Go 服务维护不可由 JWT 或模型覆盖的 `role → tool permission` 映射。策略层依次校验：工具已登记、角色权限、环境范围、输入 schema、资源数量、风险等级和审批要求。

## 分阶段重构

1. 初始化 Go 模块和 MySQL 迁移；删除 TypeScript 后端原型。
2. 重建身份、工具注册表、策略和计划服务，并以 Go 测试覆盖安全状态机。
3. 接入只读运维 API 与审计。
4. 接入 AI 编排、知识检索和确认界面。
5. 在测试/预发验证后灰度开放 L1 写操作；L2 生产动作另行设计双人审批。

## 非目标

- 无人值守生产变更；
- L3 删除、批量不可逆操作；
- 模型直接访问 MySQL、Shell 或未登记 API；
- 在本期引入多 Agent 自主执行。

