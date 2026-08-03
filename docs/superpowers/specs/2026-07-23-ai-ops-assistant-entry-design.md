# AI Ops Assistant Entry Design

## Goal

Add the missing user-facing entry to the Vue Capability Console so operators can
use the product as an AI operations assistant, not only as an API capability
management backend.

The default experience should answer a simple product question immediately:

> I can ask AI to inspect middleware through reviewed backend APIs.

Capability import, review, and publish remain available, but they become the
management entry rather than the first thing every user sees.

## Product Positioning

The product has two primary audiences:

- Operators and on-call users who want to ask questions in natural language.
- Platform admins who connect existing Swagger APIs and govern what AI may call.

The UI should make this split explicit:

```text
AI 运维助手
  Ask natural-language middleware questions
  See capability call, parameters, result, and missing-parameter prompts

能力接入管理
  Import Swagger
  Review Capability drafts
  Test, publish, and AI preflight
```

This keeps the current API onboarding workbench, but gives normal users a
clear first screen.

## Recommended Direction

Add a lightweight product shell inside the existing Vue app:

1. A left navigation rail with `AI 运维助手` and `能力接入管理`.
2. Make `AI 运维助手` the default active view.
3. Move the current Capability Console content under `能力接入管理`.
4. Build a first-version assistant workspace that calls the existing
   `POST /v1/assistant/messages` endpoint.

This avoids adding Vue Router or a separate app while the product shape is
still evolving. The first version can be one component file split later if the
view grows.

## Alternatives Considered

### Keep One Admin Page

Leave the current screen as the only entry and tell users to use the AI
preflight panel.

Trade-off: fastest, but wrong product model. AI preflight is for admins
checking published capabilities, not for daily operators.

### Separate User App

Build a new Vue app or reuse the older React console as the user-facing AI
assistant.

Trade-off: cleaner separation later, but premature now. It would split visual
language and slow down iteration while the capability model is still changing.

### Single Vue Shell With Two Views

Add navigation and two in-app views: assistant for users, management for admins.

Trade-off: the `App.vue` file grows in the short term, but it gives the product
the right first impression quickly and preserves all existing work. This is the
recommended path.

## UI Design

### Product Shell

The top-level page becomes a quiet operations app shell:

- Left rail:
  - product label: `AI 运维 Copilot`
  - navigation item: `AI 运维助手`
  - navigation item: `能力接入管理`
- Main region:
  - renders the active view
- Keep the existing dense, operational style.
- Do not create a marketing landing page.

The old header `现有后台 API 接入 AI` should move into the management view. The
assistant view should not lead with Swagger or Capability language.

### AI 运维助手 View

Use a three-zone layout:

```text
Scenario shortcuts       Conversation workspace        Call details
常用问题模板              用户问题 + AI 响应              调用能力 / 参数 / 结果
```

Left zone:

- Common prompt buttons:
  - `检查 prod minio archive bucket 容量`
  - `查看 prod kafka orders 消费延迟`
  - `检查 prod glusterfs data volume 状态`
- The buttons fill the input text or send directly. First version can fill the
  input and let the user submit.

Center zone:

- One natural-language input.
- Primary action: `发送`.
- Conversation transcript:
  - user message
  - assistant response summary
  - clarification prompt when parameters are missing
  - errors when the assistant endpoint fails

Right zone:

- Latest assistant call detail:
  - response type
  - selected tool/capability name
  - answer summary
  - raw JSON response
- If the response is `clarification_needed`, show the missing-parameter message
  as the important detail.

### 能力接入管理 View

Move the current console content into this view without changing behavior:

- Product flow
- Summary strip
- Swagger import source
- Import batch workbench
- Capability inventory
- Review editor
- Test parameter form
- Publish checklist
- AI preflight

The management view remains the admin workflow for connecting existing backend
APIs to AI.

## Data Flow

The user assistant view reuses the existing frontend API function:

```text
User enters natural language
        |
        v
sendAssistantMessage(message)
        |
        v
POST /v1/assistant/messages
        |
        v
Render response as answer / clarification / confirmation / execution result
```

No backend endpoint is required for this first version.

The management view continues to use the existing capability endpoints.

## State Model

Add simple local Vue state:

- `activeView: 'assistant' | 'management'`
- `assistantInput: string`
- `assistantMessages: Array<{ role: 'user' | 'assistant'; text: string; response?: AssistantConsoleResponse }>`
- `assistantLatestResponse: AssistantConsoleResponse | null`
- `assistantEntryLoading: boolean`
- `assistantEntryError: string`

The existing AI preflight state in the management view should remain separate.
Admin preflight and user assistant chat are different workflows even though
they call the same endpoint.

## Error Handling

- Empty user input does nothing or keeps focus in the input.
- Endpoint failure displays a visible inline error in the assistant view.
- `clarification_needed` renders as a user-facing request for missing
  parameters instead of raw JSON only.
- `confirmation_required` can render as `需要审批` in first version, but write
  approval UI remains out of scope.
- Unknown response shapes fall back to compact JSON.

## Testing

Vue tests should cover:

- The default active view is `AI 运维助手`.
- Navigation switches to `能力接入管理` and the existing Swagger import UI is
  still present.
- Scenario shortcut fills the assistant input.
- Sending a message calls `/v1/assistant/messages` with the typed text.
- Answer response renders tool/capability and summary.
- Clarification response renders the missing-parameter message.
- Existing capability import, test, publish, and AI preflight tests continue
  passing under the management view.

## Out Of Scope

- Vue Router.
- A new backend endpoint.
- Authentication or role-based menu hiding.
- Persisted chat history.
- Streaming responses.
- Approval execution UI for write operations.
- Replacing the existing React console.
- Moving the Capability Console into many components.

## Acceptance Criteria

- Opening `http://127.0.0.1:5174/` shows the AI assistant entry by default.
- A normal user can type a middleware question and see an assistant result
  without understanding Swagger, Capability YAML, or publish gates.
- Admins can switch to `能力接入管理` and keep using the existing Swagger import
  workbench.
- The first screen clearly communicates the product purpose: use AI to operate
  middleware through governed backend APIs.
- Existing tests still pass, and new tests cover assistant entry behavior.
