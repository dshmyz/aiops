# AI Capability Runtime Design

## Goal

Connect the imported and published middleware capabilities to the built-in AI
assistant without letting the model call raw Swagger APIs directly.

The assistant should resolve user messages into reviewed tools from the
canonical registry, execute safe read capabilities through the existing
read-only path, and route write capabilities into action plans and approvals.
MCP can be added later as an export surface over the same governed registry.

## Core Principle

Capability is the product boundary. MCP is only a possible protocol boundary.

```text
Traditional middleware API
        |
        v
Swagger/OpenAPI import
        |
        v
Capability draft
        |
        v
Human review and publish
        |
        v
Canonical tool registry
        |
        v
Assistant planner / resolver
        |
        v
Policy + read execution or action plan
```

AI clients must not receive raw backend URLs, raw Swagger operations, or direct
HTTP adapter access. They receive only published tool names, input schemas, and
safe descriptions derived from reviewed capabilities.

## Current Foundation

The codebase already has most of the runtime spine:

- `capabilities.RegisterPublished` loads published capability YAML files and
  registers reviewed read capabilities as dynamic tools.
- `tools.Lookup`, `tools.All`, and `tools.ValidateInput` already support
  dynamic tools.
- `policy.Evaluate` is the authority for roles, environments, tool operation,
  and risk.
- `CapabilityReadRunner` executes published read capabilities through the HTTP
  adapter and returns normalized results.
- `assistant.Service` already turns planner output into either a read answer
  or a pending write action plan.
- `plans.Service` and `execution.Service` already own confirmation and
  confirmed execution.

The missing piece is dynamic intent resolution: the assistant planner still
knows mostly static tools and diagnostic runbooks. It needs a capability-aware
resolver that can match user requests to the published dynamic tool registry.

## Alternatives Considered

### Direct MCP over Swagger

Expose Swagger operations directly as MCP tools.

Trade-off: fast demo, unsafe product. It bypasses review, output
normalization, write governance, role checks, and audit semantics. This is not
acceptable for middleware operations.

### MCP over Capability first

Build an MCP server now, backed by the published capability registry.

Trade-off: useful for external clients, but it adds protocol work before the
internal assistant has a stable runtime contract. It also splits testing across
two entry points too early.

### Built-in Capability Tool Runtime first

Teach the existing assistant/resolver to discover and use published
capabilities from the canonical registry, then add MCP export later.

Trade-off: less ecosystem-facing in the first step, but it closes the product
loop fastest and keeps governance central. This is the recommended path.

## Recommended Architecture

Add a capability-aware resolver behind the existing assistant planner
boundary.

```text
POST /v1/assistant/messages
        |
        v
assistant.Service
        |
        v
Planner / CapabilityResolver
        |
        v
candidate assistant.Intent
        |
        v
tools.Lookup
        |
        v
policy.Evaluate
        |
        +--> read: execution.ReadOnlyService.ExecuteRead
        |
        +--> write: plans.Service.CreatePlan
```

The resolver is not allowed to execute tools. It only returns a candidate
`assistant.Intent`.

## Resolver Behavior

The first capability-aware resolver can be deterministic and metadata-driven.

Inputs:

- User message text.
- Current user, for environment hints only. Authorization remains in policy.
- Published tools from `tools.All()`.
- Dynamic input schemas already registered by published capabilities.

Matching signals:

- Tool name tokens: `minio.bucket.capacity.read`.
- Domain and resource type: `minio`, `bucket`, `kafka`, `topic`,
  `glusterfs`, `volume`.
- Operation words:
  - read: `read`, `query`, `查看`, `查询`, `状态`, `容量`, `健康`.
  - write: `set`, `update`, `调整`, `修改`, `设置`.
- Input field names from the dynamic schema.
- Simple parameter extraction from `key=value`, `key: value`, and common
  natural-language patterns.

Output:

```go
assistant.Intent{
    ToolName: "minio.bucket.capacity.read",
    Input: map[string]any{
        "environment": "prod",
        "cluster": "m1",
        "bucket": "archive",
    },
    Confidence: 0.7,
    Explanation: "matched published capability by domain, resource, and input tokens",
}
```

If required inputs are missing, the resolver returns clarification rather than
guessing.

## Read Flow

For a published read capability:

1. User asks a question such as `查一下 prod m1 archive bucket 的容量`.
2. Resolver returns a candidate dynamic tool intent.
3. `assistant.Service` calls `tools.Lookup`.
4. `policy.Evaluate` verifies role, environment, operation, and input schema.
5. `execution.ReadOnlyService` calls the read runner.
6. `CapabilityReadRunner` executes the HTTP adapter.
7. Normalized result is returned as the assistant answer.

The model never sees the backend base URL. The user gets normalized,
Copilot-visible data.

## Write Flow

For write capabilities:

1. Resolver may match a published write capability once write runtime support
   exists.
2. `assistant.Service` must not execute it directly.
3. `policy.Evaluate` and `plans.Service.CreatePlan` create a pending action
   plan.
4. Existing confirmation and execution endpoints handle approval and audit.

In the current implementation phase, publishing write capabilities into runtime
remains out of scope. The design keeps the path explicit so the resolver does
not need to change shape later.

## MCP Position

MCP should be a later export layer over the same runtime contract:

```text
MCP client
   |
   v
MCP server exposes published capabilities as tools
   |
   v
same tool registry + policy + execution/plan services
```

MCP tools should be generated only from reviewed, published capabilities. MCP
must not import Swagger directly and must not bypass role, environment, input,
approval, or audit rules.

## Error Handling

- No matching tool: return `clarification_needed`.
- Multiple similarly scored tools: return `clarification_needed` with the
  candidate capability names.
- Missing required parameter: return `clarification_needed` naming the missing
  fields.
- Policy denied: keep existing assistant policy-denial behavior.
- Read adapter failure: keep existing read service failure behavior.
- Write request without write runtime support: clarify that the capability is
  not currently executable by AI.

## Testing

Add tests for:

- Published dynamic read capability is visible to the resolver.
- Natural-language request resolves to the dynamic capability intent.
- Missing required dynamic input returns clarification.
- Forged or unregistered tool names are still rejected by `tools.Lookup` and
  policy.
- Allowed user can receive an assistant answer from a dynamic read capability.
- Disallowed environment or role is denied before execution.
- Write capability candidates do not execute directly.

Existing tests for capability loading, dynamic tool registration, policy,
read-only execution, and action plan confirmation remain part of the safety
net.

## Out Of Scope

- Building the MCP server in this phase.
- Direct Swagger-to-MCP export.
- LLM provider integration for dynamic tool calling.
- Vector search or embeddings for capability matching.
- Runtime publication of write capabilities.
- Secret management or OAuth setup.
- Raw backend JSON exposure to the assistant.

## Acceptance Criteria

- A user can ask the built-in assistant to use a published read capability
  imported from Swagger.
- The assistant response goes through the canonical registry, policy layer, and
  read-only execution path.
- Missing parameters produce a clarification instead of a guessed API call.
- Raw backend URLs and raw Swagger operations are not exposed as assistant
  tools.
- The design leaves a clear, later MCP export path without making MCP the core
  runtime.
