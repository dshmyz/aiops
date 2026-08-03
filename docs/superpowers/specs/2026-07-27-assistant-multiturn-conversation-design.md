# AI 运维助手多轮对话上下文设计

- 日期：2026-07-27
- 范围：AI 运维助手 / Capability Console
- 状态：已完成（2026-08-02，/v1/assistant/conversations + 前端 ConversationSidebar 均已落地）

## 背景与动机

当前 `POST /v1/assistant/messages` 完全无状态：

- 后端 `Planner.Plan(ctx, user, message)` 仅接收当前消息字符串，不接收任何历史
- 后端 `Service.HandleMessage`、`Router.serveAssistant` 均无会话状态字段
- 前端 `assistantMessages` 数组仅用于 UI 渲染，每次请求只发 `{message}`，不回传任何上下文
- 无任何 `conversations` / `sessions` / `messages` 表

由此产生两个明显痛点：

1. **clarification 补参割裂**：用户得到 `clarification_needed` 后，必须重新输入完整消息；后端重新做 token 匹配，丢弃上一轮已选候选与已提取参数；用户若只补缺失参数本身（如 `topic=orders`），仍会因缺少 environment 等上下文再次触发 clarification。
2. **无指代理解**：用户问"那 prod kafka 呢"或"同 environment 再查一个"，规则引擎无法理解，LLM 即使启用也无历史可参考。

产品主线已闭合（Swagger 导入 → 能力评审发布 → AI plan+confirmation → 执行 → 审计查询），但 AI 助手本身仍是"一问一答"模式，跨轮上下文缺失。

## 目标

- AI 助手能理解跨轮指代（"刚才那个 capability"、"同 environment 再查一个"）
- clarification 后用户可只补缺失参数，后端能合并上一轮 Selection.Extracted 重试
- 对话跨浏览器刷新保留，用户可切换/恢复近期会话
- 不破坏现有 API 与无 LLM 部署下的功能

## 非目标

- 不做消息推送/WebSocket 流式响应
- 不做多用户协作会话（同一会话仅一个 subject）
- 不做物理删除 API（归档即可，物理删除走 DBA）
- 不做跨设备实时同步（DB 持久化已能保证切换设备后能查到，但不推送实时更新）

## 架构总览

```
┌────────────────────────────┐
│  前端 ConversationSidebar  │  ← 列出近期会话
│  + AssistantTranscript     │  ← 渲染历史 turns
│  + 输入框                   │
└──────────┬─────────────────┘
           │  POST /v1/assistant/messages
           │     {message, conversation_id?, parent_turn_id?}
           ▼
┌────────────────────────────┐
│  Router.serveAssistant     │
│  - Authenticate            │
│  - 校验 conversation_id    │
│    归属当前 user.subject   │
└──────────┬─────────────────┘
           ▼
┌────────────────────────────────────────┐
│  Service.HandleMessage                 │
│  - 取/创建 Conversation                │
│  - 拉取最近 N 轮 turns                 │
│  - 调 Planner.Plan(msg, history)       │
│  - 执行 Read / 创建 Plan 等            │
│  - 持久化本轮 user + assistant turn    │
│  - 更新 Conversation.last_active_at    │
└──────────┬─────────────────────────────┘
           ▼
┌────────────────────────────────────────┐
│  CapabilityAwarePlanner                │
│  - resolveDynamicCapability(msg)       │
│    命中 → 直接返回 Intent              │
│    未命中 + history 有上一轮           │
│      clarification_needed → 合并重试   │
│  - fallback = EinoPlanner(msg, history) │
│    把 history 拼进 LLM messages        │
└────────────────────────────────────────┘
```

## 数据模型

新建 migration `migrations/004_assistant_conversations.sql`：

```sql
CREATE TABLE IF NOT EXISTS copilot_assistant_conversations (
    id CHAR(36) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    last_message_preview VARCHAR(500) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_active_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    archived_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY copilot_assistant_conversations_subject_last_active_idx (subject, last_active_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS copilot_assistant_turns (
    id CHAR(36) NOT NULL,
    conversation_id CHAR(36) NOT NULL,
    parent_turn_id CHAR(36) NULL,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    response_type VARCHAR(32) NULL,
    response_payload JSON NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY copilot_assistant_turns_conversation_created_idx (conversation_id, created_at),
    CONSTRAINT copilot_assistant_turns_conversation_id_fk
        FOREIGN KEY (conversation_id) REFERENCES copilot_assistant_conversations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
```

### 字段说明

- **`copilot_assistant_conversations`**
  - `subject`：会话归属用户，用于鉴权隔离
  - `title`：会话标题，MVP 用首轮用户消息前 50 字截断生成；未来可让 LLM 总结
  - `last_message_preview`：最后一句用户消息前 500 字，避免每次列表都 join turns
  - `last_active_at`：最新 turn 写入时更新；列表按此字段倒序
  - `archived_at`：软归档时间戳，NULL 表示未归档
- **`copilot_assistant_turns`**
  - `parent_turn_id`：分叉/重试时指向父 turn；MVP 不强制使用，schema 预留
  - `role`：`user` 或 `assistant`
  - `content`：用户原文 或 AI 摘要文本
  - `response_type`：仅 assistant 角色，对应 Response.Type（answer/clarification_needed/confirmation_required/execution_result）
  - `response_payload`：完整 Response 对象 JSON，用于回放/审计/调试

### 内存实现

在 `internal/store/assistant_conversations.go` 提供 `MemoryAssistantConversationStore`，与现有 `MemoryActionPlanStore` 风格一致，用于单测与本地开发。

```go
type AssistantConversationStore interface {
    CreateConversation(ctx context.Context, subject, title, preview string) (Conversation, error)
    AppendTurn(ctx context.Context, turn Turn) error
    ListConversations(ctx context.Context, filter ConversationFilter) (ConversationPage, error)
    GetConversation(ctx context.Context, id, subject string) (Conversation, error)
    ListTurns(ctx context.Context, conversationID string, limit int, beforeTurnID string) (TurnPage, error)
    ArchiveConversation(ctx context.Context, id, subject string) error
}
```

## API 设计

### 1. `POST /v1/assistant/messages`（扩展，向后兼容）

**请求体：**

```json
{
  "message": "string",
  "conversation_id": "uuid?",
  "parent_turn_id": "uuid?"
}
```

**响应体：**

```json
{
  "type": "answer|clarification_needed|confirmation_required|execution_result",
  "conversation_id": "uuid",
  "turn_id": "uuid",
  ...原有字段（tool/answer/plan_id/trace 等）
}
```

- 旧客户端不传 `conversation_id` → 后端创建新会话 → 响应仍返回 `conversation_id`/`turn_id`，旧客户端忽略即可
- 若 `conversation_id` 存在但不属于当前 user.subject → 返回 403

### 2. `GET /v1/assistant/conversations`

```
GET /v1/assistant/conversations?limit=20&archived=false
```

响应：

```json
{
  "conversations": [
    {
      "id": "uuid",
      "title": "minio archive 容量查询",
      "last_message_preview": "检查 prod minio archive bucket 容量",
      "last_active_at": "2026-07-27T10:00:00Z",
      "created_at": "2026-07-27T09:30:00Z",
      "archived": false
    }
  ]
}
```

- 强制按当前 user.subject 过滤
- 默认 `archived=false`，可传 `archived=true` 查看归档会话
- `limit` 默认 20，上限 100

### 3. `GET /v1/assistant/conversations/{id}`

```
GET /v1/assistant/conversations/{id}?limit=50&before_turn_id=...
```

响应：

```json
{
  "conversation": {
    "id": "uuid",
    "title": "...",
    "last_active_at": "...",
    "archived": false
  },
  "turns": [
    {
      "id": "uuid",
      "role": "user",
      "content": "检查 prod minio archive bucket 容量",
      "response_type": null,
      "created_at": "..."
    },
    {
      "id": "uuid",
      "role": "assistant",
      "content": "Bucket archive usage is 77%",
      "response_type": "answer",
      "response_payload": { /* 完整 Response */ },
      "created_at": "..."
    }
  ],
  "next_cursor": "uuid?"
}
```

- 鉴权：若会话 `subject` ≠ 当前用户 → 404（避免泄露存在性）
- turns 按 `created_at desc` 排序（最新在前）
- `next_cursor` 是更老 turn 的 id；前端可基于此拉取更早历史

### 4. `POST /v1/assistant/conversations/{id}/archive`

软归档：设置 `archived_at = NOW()`。响应 204 No Content。

### 5. 不实现 `DELETE`

YAGNI——归档已足够。物理删除走 DBA。

### 鉴权与隔离

- 所有 `/v1/assistant/conversations*` 路由复用现有 `auth.Authenticate` 中间件
- Conversation 的 `subject` 字段强制 = 当前用户 subject
- 列表/详情查询强制按 `subject` 过滤

## Planner 接口扩展

### 当前接口

```go
type Planner interface {
    Plan(ctx context.Context, user identity.CurrentUser, message string) (Intent, error)
}
```

### 扩展后接口

```go
type Turn struct {
    Role         string
    Content      string
    ResponseType string
    Intent       *Intent
}

type Planner interface {
    Plan(ctx context.Context, user identity.CurrentUser, message string, history []Turn) (Intent, error)
}
```

### EinoPlanner 实现

- 把 `history` 转成 LLM SDK 的 `messages` 数组：
  - `role=user` → `user` 消息
  - `role=assistant` → `assistant` 消息（用 `Content` 字段）
- 当前消息拼到末尾
- System prompt 保持不变（仍含可用能力清单）
- 限制最近 N 轮（如 10 轮）以控制 token 成本

### DeterministicPlanner 实现（保持兼容）

- **忽略 history 参数**——规则引擎无法理解指代
- 行为完全保持现状：每次只看当前 message 做 token 匹配
- 无 LLM 部署下功能不退化

### CapabilityAwarePlanner 装饰器

- 仍是 fallback 装饰器：先尝试 `resolveDynamicCapability`（规则匹配），失败则走 fallback Planner
- **关键改动**：当本轮规则匹配失败但 history 中上一轮 assistant 是 `clarification_needed` 且携带 `Intent.Selection`，则把上一轮 `Selection.Extracted` 与本轮 message 合并重试一次
- 这保证了"用户只补缺失参数"场景在无 LLM 时也能工作（双保险）

### CapabilityAwarePlanner 与 EinoPlanner 协同

当前 main.go 装配保持不变：

```
CapabilityAwarePlanner
  ├─ resolveDynamicCapability (规则匹配)
  └─ fallback = EinoPlanner (LLM)
```

所有 Planner 实现都接受 `history` 参数。CapabilityAwarePlanner 在规则匹配命中时直接返回，无需调用 LLM；未命中时把 history 透传给 EinoPlanner，让 LLM 兜底。

## 前端组件

按"使用子组件"原则拆分：

- **`ConversationSidebar.vue`**：左侧会话列表，显示近期会话标题 + 最后一句预览 + 最后活跃时间；点击切换会话；顶部"新会话"按钮；每项右侧"归档"按钮
- **`ConversationTurnItem.vue`**：单条对话气泡渲染（user/assistant 不同样式 + 状态徽章）
- **`AssistantTranscript.vue`**：把现有 transcript 区域抽出来，接受 `turns: Turn[]` prop；遍历 turns 用 `ConversationTurnItem`

### App.vue 状态扩展

```ts
const conversationId = ref<string | null>(null);
const conversations = ref<ConversationSummary[]>([]);
const conversationsLoading = ref(false);
```

- `sendAssistantEntryMessage` 改为：调 `sendAssistantMessage(message, conversationId.value)` → 从响应取 `conversation_id` / `turn_id` 存到本地
- `loadConversation(id)` 切换会话：调 `GET /v1/assistant/conversations/{id}` → 把 turns 映射为 transcript 渲染
- `startNewConversation()` 重置：清空 `conversationId` 与 `assistantMessages`

### api.ts 扩展

```ts
export async function sendAssistantMessage(
  message: string,
  conversationId?: string | null,
): Promise<AssistantConsoleResponse>

export async function listConversations(
  opts?: { limit?: number; archived?: boolean },
): Promise<ConversationSummary[]>

export async function getConversation(
  id: string,
  opts?: { limit?: number; beforeTurnID?: string },
): Promise<ConversationDetail>

export async function archiveConversation(id: string): Promise<void>
```

### UI 布局

```
┌──────────────────────────────────────────────────────────────┐
│  AI 运维助手                                                  │
├────────────────┬─────────────────────────────────────────────┤
│ + 新会话        │  对话区                                     │
│ ──────────     │  ┌──────────────────────────────────────┐   │
│ ○ minio 容量   │  │ user: 检查 prod minio archive 容量  │   │
│ ○ kafka 配置   │  │ assistant: Bucket usage is 77%      │   │
│ ○ 昨天的会话   │  └──────────────────────────────────────┘   │
│                │  ┌──────────────────────────────────────┐   │
│                │  │ user: 那同 environment 再查 kafka   │   │
│                │  │ assistant: ...                      │   │
│                │  └──────────────────────────────────────┘   │
│                │  ┌──────────────────────────────────────┐   │
│                │  │ [输入框]                            │   │
│                │  └──────────────────────────────────────┘   │
└────────────────┴─────────────────────────────────────────────┘
```

## TDD 测试计划

### 后端

**`internal/store/assistant_conversations_test.go`**

- `TestMemoryConversationStoreCreateAndList`：创建会话、列出会话
- `TestMemoryConversationStoreAppendTurnsAndList`：追加 turn、按时间倒序列出
- `TestMemoryConversationStoreFiltersBySubject`：跨用户隔离
- `TestMemoryConversationStoreArchiveSoftDeletes`：归档后默认列表不可见，`?archived=true` 可见
- `TestSQLConversationStoreListByKeysetCursor`：游标分页

**`internal/assistant/service_test.go`（扩展）**

- `TestServiceHandleMessageCreatesConversationWhenIDMissing`：传空 conversation_id → 新建会话
- `TestServiceHandleMessagePersistsTurns`：成功响应后 turns 表有 1 user + 1 assistant
- `TestServiceHandleMessageLoadsHistoryForPlanner`：mock Planner 收到 history
- `TestServiceHandleMessageRejectsForeignConversation`：他人会话 ID 返回 403

**`internal/assistant/planner_test.go`（扩展）**

- `TestDeterministicPlannerIgnoresHistory`：history 不影响规则匹配结果
- `TestEinoPlannerPassesHistoryToLLM`：mock LLM client 收到完整 messages

**`internal/httpapi/router_test.go`（扩展）**

- `TestAssistantMessagesEndpointReturnsConversationID`：响应包含 conversation_id/turn_id
- `TestListConversationsEndpointFiltersBySubject`
- `TestGetConversationEndpointAppliesKeysetPagination`

### 前端

**`apps/capability-console/src/components/ConversationSidebar.test.ts`**

- 渲染会话列表 + 点击切换
- "新会话"按钮 emit 事件
- 归档按钮 emit 事件

**`apps/capability-console/src/components/AssistantTranscript.test.ts`**

- 渲染 user/assistant 不同样式
- 状态徽章正确显示

**`apps/capability-console/src/App.test.ts`（扩展）**

- 首次发消息后 conversationId 被填充
- 切换会话后 transcript 更新
- "新会话"按钮重置状态

### E2E

**`tests/e2e/assistant_multiturn_test.go`**

- 发"检查 prod minio archive 容量" → answer
- 同会话发"那 prod kafka 呢" → AI 应能基于上下文回答（依赖 LLM provider 集成测试）

## 迁移与风险

- **DB migration**：`004_assistant_conversations.sql` 走现有 migration 流程
- **配置变更**：无（不需要新环境变量）
- **回滚策略**：删表即可，旧 API 行为完全兼容
- **风险**：
  1. LLM 调用延迟可能上升（带历史时 prompt 变长）→ 限制最近 N 轮（如 10 轮）
  2. 历史过长导致 token 成本增加 → 同上限制
  3. 用户切换会话时本地未保存的输入丢失 → UI 加未保存提醒

## 实现顺序

1. **DB schema + Memory Store + TDD**（基础设施层）
2. **Planner 接口扩展 + DeterministicPlanner/EinoPlanner/CapabilityAwarePlanner 实现 + TDD**（领域层）
3. **Service.HandleMessage 扩展 + TDD**（应用层）
4. **Router 新增 `/v1/assistant/conversations*` + 扩展 `/v1/assistant/messages` 响应 + TDD**（接口层）
5. **前端 api.ts 扩展 + types.ts 扩展**
6. **前端 ConversationSidebar / ConversationTurnItem / AssistantTranscript 组件 + TDD**
7. **App.vue 集成 + E2E 测试**
8. **全量验证**：`go vet` / `go build` / `go test ./...` / `vitest` / `make dev-verify-trace`
