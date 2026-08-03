# Eino-Ready Assistant Copilot Design

## Decision

Build the next AI step as an Eino-ready assistant endpoint, with a deterministic
planner as the default implementation. The endpoint accepts an operator
message, classifies it into a registered tool intent, runs the existing policy
layer, and either returns a read result or creates a pending action plan for a
write. Eino is introduced as the future Go-native LLM orchestration adapter,
not as an authority for permissions or execution.

## Scope

- Add `POST /v1/assistant/messages`.
- Support two intents in this phase:
  - cluster status read: `cluster.status.read`
  - topic retention write plan: `topic.retention.set`
- Keep JWT projection unchanged: only `sub`, `roles`, `allowed_environments`,
  and request ID are trusted.
- Reuse the static tool registry, policy evaluator, read-only executor, plan
  service, and audit store.
- Add a `Planner` interface with two intended implementations:
  - `DeterministicPlanner`: default local and CI implementation.
  - `EinoPlanner`: future implementation backed by Eino ChatModel/tool calling.
- Use SQLite for default local tests and MySQL for production/integration.

## Non-Goals

- No external model call in this phase.
- No Eino runtime dependency in the first implementation task unless it is
  hidden behind `EinoPlanner` and disabled by default.
- No RAG or document search.
- No real middleware write execution.
- No model-controlled tool metadata, permissions, SQL, shell, or raw HTTP.
- No L3 delete or bulk irreversible actions.

## Architecture

```text
React / API Client
  -> POST /v1/assistant/messages
  -> httpapi authenticates JWT
  -> assistant.Service calls Planner
  -> DeterministicPlanner now / EinoPlanner later
  -> policy.Evaluate resolves canonical tool and authorization
  -> read intent: execution.ReadOnlyService.ExecuteRead
  -> write intent: plans.Service.CreatePlan
  -> response contains answer or confirmation-required plan summary
```

`assistant.Service` is intentionally thin. It owns natural-language parsing and
response shaping, but it does not own permissions, execution, confirmation, or
tool definitions.

## Eino Boundary

Eino fits this project because it is a Go LLM application framework with
components such as ChatModel, Tool, Retriever, ChatTemplate, graph/chain
composition, callbacks, and extension packages for model providers. Those
capabilities are useful for later model planning and RAG.

The Eino integration boundary is deliberately narrow:

```go
type Planner interface {
    Plan(ctx context.Context, user identity.CurrentUser, message string) (Intent, error)
}

type Intent struct {
    ToolName    string
    Input       map[string]any
    Confidence  float64
    Explanation string
}
```

`EinoPlanner` may use Eino ChatModel, prompt templates, tool-calling schemas,
or retrievers to propose an `Intent`. The returned intent is untrusted
candidate data. `assistant.Service` must still call `tools.Lookup` and
`policy.Evaluate`; write intents must still go through `plans.CreatePlan`.

Do not register operational tools directly in Eino `ToolsNode` for production
actions. If Eino tool schemas are used later, they must describe candidate
planning only, not perform infrastructure changes.

## Message Contract

Request:

```json
{
  "message": "查看 prod 集群状态"
}
```

Response for allowed read:

```json
{
  "type": "answer",
  "tool": "cluster.status.read",
  "answer": {
    "status": "available",
    "environment": "prod"
  }
}
```

Response for write plan:

```json
{
  "type": "confirmation_required",
  "tool": "topic.retention.set",
  "plan_id": "uuid",
  "status": "pending_confirmation",
  "expires_at": "2026-07-21T12:10:00Z",
  "summary": "Set topic orders retention in prod to 72 hours."
}
```

Response for unclear text:

```json
{
  "type": "clarification_needed",
  "message": "I can help with cluster status or topic retention. Please include environment and required parameters."
}
```

## Intent Rules

- Cluster status read matches text containing `status`, `状态`, `health`, or
  `健康`, plus an environment token such as `prod`, `staging`, or `dev`.
- Topic retention write matches text containing `retention`, `保留`, or
  `留存`, plus environment, topic name, and hours.
- Environment extraction accepts only simple alphanumeric environment tokens.
- Topic extraction accepts only `[a-zA-Z0-9._-]+`.
- Hours extraction accepts positive integers and is still validated by the
  canonical tool schema and policy layer.
- Planner output is only a candidate. The policy layer remains authoritative.

## Error Handling

- Missing/invalid JWT returns `401`.
- Unknown or unclear intent returns `200` with `clarification_needed`.
- Policy denial returns `403` with the policy reason.
- Read runner failure returns `502`.
- Malformed JSON returns `400`.
- Assistant responses are capped by the existing HTTP response-size discipline.

## Testing

- Unit test intent parsing for English and Chinese phrases.
- HTTP test unauthenticated assistant request returns `401`.
- HTTP test viewer can ask for prod cluster status and receives an answer.
- HTTP test viewer asking for retention receives `403`.
- HTTP test admin asking for retention receives a pending confirmation plan,
  and the response includes no raw confirmation token.
- SQLite e2e test verifies a write message stores an `action_plans` row and
  audit event through the real SQL store.
- Contract test verifies an injected planner can return a forged write intent,
  but the assistant still creates a pending confirmation plan rather than
  executing it.

## Eino Integration Later

When this endpoint is stable, add `EinoPlanner` behind the same `Planner`
interface. Start with ChatModel plus a strict JSON output schema. Add Eino
Retriever/RAG only after the assistant endpoint has stable tests and audit
behavior. Eino output must remain a candidate intent only; the Go registry,
policy, plan service, and execution service remain authoritative.
