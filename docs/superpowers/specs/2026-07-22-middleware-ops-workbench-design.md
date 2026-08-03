# Middleware Ops Workbench Design

## Goal

Turn the current AI Operations Copilot into a chat-first middleware operations
workbench. The first version provides a common diagnostic and governance model
for multiple middleware domains, starting with GlusterFS, then MinIO, then Kafka.

The workbench should let an operator ask natural-language questions, inspect
structured diagnostic evidence, review Copilot findings and recommendations, and
route all write operations through the existing action plan, approval, execution,
and audit boundary.

## Product Shape

The primary experience is a Chat-first Middleware Ops Workbench.

The operator starts from Copilot chat with requests such as:

- Check storage health in prod.
- Show capacity risk for a MinIO bucket.
- Explain why Kafka consumer lag is high.
- Prepare a safe remediation plan for this volume.

Copilot identifies the middleware domain and resource, selects a runbook, gathers
read-only evidence, and returns a structured diagnostic package alongside its
natural-language response. The console renders that package in a workbench area
next to the conversation.

The first version keeps chat as the main entry point. It reserves room for
resource-centric and alert-centric workflows, but it does not build a full
resource catalog or incident system yet.

## Scope

In scope:

- A unified diagnostic result model for resources, observations, findings, and
  recommendations.
- A domain registry for declaring supported middleware domains, resource types,
  diagnostic runbooks, read tools, write tools, and risk metadata.
- Structured diagnostic packages returned from `POST /v1/assistant/messages`.
- A console workbench that renders diagnostic packages, action plans, approvals,
  execution results, and audit timeline data in one flow.
- GlusterFS as the first complete domain sample.
- MinIO as a lightweight read-only diagnostic domain.
- Kafka integration through the existing topic operations, adapted into the
  shared model.
- Existing action plan, risk-tiered countersign, execution, and audit boundaries
  remain authoritative for all write actions.

Out of scope:

- A complete CMDB or inventory database.
- A complete alert ingestion and incident lifecycle system.
- Real-time updates through WebSocket or SSE.
- External approval connectors such as Slack, email, or ticketing.
- Full production implementations for every GlusterFS, MinIO, and Kafka write
  action.
- Allowing Copilot, domain adapters, or UI clients to execute writes directly.

## Core Model

### Domain

A domain represents one middleware family.

Examples:

- `glusterfs`
- `minio`
- `kafka`

Each domain declares its resource types, diagnostic runbooks, tools, and risk
metadata through a server-side registry.

### Resource

A resource is an object that can be diagnosed or operated on.

Examples:

- GlusterFS volume, brick, node.
- MinIO bucket, erasure set, node.
- Kafka topic, consumer group, broker.

Suggested shape:

```go
type ResourceRef struct {
    Domain      string            `json:"domain"`
    Type        string            `json:"type"`
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Environment string            `json:"environment"`
    Labels      map[string]string `json:"labels,omitempty"`
}
```

### Observation

An observation is read-only diagnostic evidence collected by tools or runbook
steps.

Examples:

- Volume capacity usage.
- Brick health.
- Bucket quota and lifecycle state.
- Consumer lag.
- Broker ISR status.

Suggested shape:

```go
type Observation struct {
    ID         string                 `json:"id"`
    Resource   ResourceRef            `json:"resource"`
    Kind       string                 `json:"kind"`
    Severity   string                 `json:"severity"`
    Summary    string                 `json:"summary"`
    Data       map[string]any         `json:"data,omitempty"`
    CollectedAt time.Time             `json:"collected_at"`
}
```

### Finding

A finding is Copilot's interpreted conclusion from one or more observations.
Findings are candidate analysis, not authority.

Examples:

- Capacity growth is close to quota.
- A GlusterFS brick appears unhealthy.
- Kafka lag is concentrated in one consumer group.

Suggested shape:

```go
type Finding struct {
    ID             string   `json:"id"`
    Severity       string   `json:"severity"`
    Summary        string   `json:"summary"`
    EvidenceIDs    []string `json:"evidence_ids"`
    Confidence     string   `json:"confidence"`
}
```

### Recommendation

A recommendation is a proposed next step. It can be read-only, manual, or
eligible to become an action plan.

Examples:

- Inspect the unhealthy brick.
- Trigger GlusterFS heal.
- Adjust MinIO lifecycle policy.
- Change Kafka topic retention.

Suggested shape:

```go
type Recommendation struct {
    ID            string         `json:"id"`
    Summary       string         `json:"summary"`
    Rationale     string         `json:"rationale"`
    Risk          tools.RiskLevel `json:"risk"`
    Actionable    bool           `json:"actionable"`
    ToolName      string         `json:"tool_name,omitempty"`
    CandidateInput map[string]any `json:"candidate_input,omitempty"`
}
```

Actionable recommendations do not execute directly. They are converted into
immutable action plans and sent through approval, execution, and audit.

### Diagnostic Package

A diagnostic package groups the result of one Copilot diagnostic turn.

Suggested shape:

```go
type DiagnosticPackage struct {
    ID              string           `json:"id"`
    Environment     string           `json:"environment"`
    Domains         []string         `json:"domains"`
    Resources       []ResourceRef    `json:"resources"`
    Observations    []Observation    `json:"observations"`
    Findings        []Finding        `json:"findings"`
    Recommendations []Recommendation `json:"recommendations"`
    PlanIDs         []string         `json:"plan_ids,omitempty"`
    CreatedAt       time.Time        `json:"created_at"`
}
```

The package may be persisted immediately or returned inline first, with durable
storage added when audit and replay needs require it. The API contract should be
stable either way.

## Diagnostic Flow

The chat-first diagnostic flow is:

1. User sends a natural-language request.
2. Assistant planner identifies candidate domain, environment, resources, and
   intent.
3. Backend validates identity, environment access, registered domains, and
   allowed read tools.
4. A diagnostic runbook selects read-only tool calls.
5. Read tools produce observations.
6. Planner interprets observations into findings and recommendations.
7. Backend resolves any actionable recommendation to a canonical registered
   write tool.
8. If a write is requested, backend creates an immutable action plan.
9. Console renders the diagnostic package, pending approvals, execution records,
   and audit events.

Copilot output remains untrusted candidate data. The backend continues to own
tool resolution, authorization, risk policy, action plan creation, approval,
execution, and audit.

## Domain Registry

The registry extends the existing static tool boundary instead of replacing it.

Each domain declaration should include:

- Domain name.
- Resource types.
- Read tools.
- Write tools.
- Runbooks.
- Recommendation-to-tool mappings.
- Risk metadata.

The registry is server-side only. Clients can render domain metadata returned by
the API, but they cannot register tools or change risk policy.

## Domain Rollout

### Phase 1: GlusterFS Complete Sample

GlusterFS proves the shared model.

Read diagnostics:

- Volume health.
- Brick status.
- Capacity and growth summary.
- Heal and rebalance status.
- Node-level warnings.

Recommendations:

- Inspect or isolate an unhealthy brick.
- Trigger or continue heal.
- Start or review rebalance.
- Plan capacity expansion.

Write actions may start as simulated or narrowly controlled operations. The
important part is validating the governance path: recommendation to action plan,
approval, execution, and audit.

### Phase 2: MinIO Lightweight Diagnostics

MinIO starts read-only.

Read diagnostics:

- Bucket capacity.
- Object growth.
- Quota state.
- Lifecycle policy state.
- Cluster or erasure-set health summary.

Recommendations can suggest lifecycle, quota, or capacity work, but the first
version does not need real MinIO write execution.

### Phase 3: Kafka Integration

Kafka adapts existing topic operations into the shared registry.

Read diagnostics:

- Topic configuration.
- Consumer lag.
- Broker health.
- ISR or replication warnings.

Write actions:

- Keep the existing topic retention change as the first Kafka governed write
  sample.

## API Design

### Assistant Messages

Extend:

```http
POST /v1/assistant/messages
```

Response includes the current text response plus optional diagnostic package and
action plan references.

```json
{
  "message": "The prod storage volume is healthy, but capacity is near the warning threshold.",
  "diagnostic": {
    "id": "diag-123",
    "environment": "prod",
    "domains": ["glusterfs"],
    "resources": [],
    "observations": [],
    "findings": [],
    "recommendations": [],
    "plan_ids": []
  },
  "plans": []
}
```

Existing clients can ignore `diagnostic`.

### Diagnostic Detail

Add when diagnostics are persisted:

```http
GET /v1/diagnostics/{id}
```

The endpoint returns one diagnostic package if the authenticated user can access
the package environment.

### Resource List

Optional first-version endpoint:

```http
GET /v1/resources?domain=glusterfs&environment=prod
```

This endpoint can be backed by read tools rather than a database. It is useful
for resource-centric navigation later but is not required for the chat-first
MVP.

## Console Design

The console remains a work-focused operations surface.

Suggested layout:

- Left: Copilot chat.
- Center: current diagnostic package with resource snapshot, observations,
  findings, and recommendations.
- Right: action plans, approval progress, execution result, and audit timeline.

Important states:

- No diagnostic selected.
- Diagnostic loading.
- Partial diagnostic with read-tool errors.
- No findings.
- Recommendations only.
- Pending approval.
- Approved and executing.
- Executed.
- Rejected or cancelled.

The UI should render all middleware domains through the same components. Domain
specifics appear as labels, resource types, observation kinds, and structured
details, not as separate one-off page implementations.

## Authorization And Safety

- JWT identity projection remains limited to trusted claims.
- Server-side policy remains authoritative for role, environment, tool, input,
  and risk decisions.
- Read diagnostics only return resources in allowed environments.
- Unknown domains, tools, or resources fail closed.
- Write recommendations are converted to immutable action plans before any
  execution.
- Risk-tiered countersign applies to every domain's write tools.
- Audit events correlate chat requests, diagnostic packages, action plans,
  approvals, execution records, and failures.

## Error Handling

- Missing or invalid JWT: `401`.
- Domain not registered: `400`.
- Unsupported diagnostic request: `400`.
- Resource not found or hidden by policy: `404` or `403`, matching existing
  plan visibility behavior.
- Read tool failure: diagnostic package is returned with a warning when partial
  evidence is still useful.
- Store or tool infrastructure failure: existing JSON error envelope with `502`.
- Write attempt outside action plan flow: denied by construction and covered by
  tests.

## Testing

Backend tests:

- Assistant messages can return a diagnostic package.
- Unknown domains fail closed.
- Diagnostics only use registered read tools.
- Actionable recommendations resolve to canonical registered write tools.
- Write recommendations create action plans and do not execute directly.
- Users cannot see diagnostics or resources outside their allowed environments.
- GlusterFS sample runbook returns resources, observations, findings, and at
  least one recommendation.
- Kafka retention remains governed through the existing action plan path.

Frontend tests:

- Renders a diagnostic package from the assistant response.
- Shows observations, findings, and recommendations in stable domain-neutral
  components.
- Shows pending action plans and approval progress next to the diagnostic.
- Handles partial diagnostic errors.
- Handles an assistant response with no diagnostic.

E2E tests:

- A chat request for a GlusterFS health check returns structured diagnostics.
- A chat request that proposes a write creates a pending action plan.
- Approval and execution remain auditable and environment-restricted.

## Acceptance Criteria

- Operators can start from chat and receive a structured middleware diagnostic
  package.
- The console renders that package without domain-specific page rewrites.
- GlusterFS is the first complete sample domain.
- MinIO has lightweight read-only diagnostics.
- Kafka topic operations fit the shared domain model.
- No write action can bypass action plans, risk-tiered approval, execution
  records, or audit.
- The design leaves clear extension points for future resource and alert driven
  workflows.
