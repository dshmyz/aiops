# Capability Importer Design

## Goal

Build a safe, semi-automatic API onboarding path for the Middleware Ops
Workbench. Existing middleware management APIs should be imported as capability
drafts, reviewed by humans, published into a registry, and then exposed to
Copilot through governed tools.

The importer must help teams onboard many APIs quickly without allowing newly
discovered APIs to become executable by AI automatically.

## Product Shape

The first version is file-based:

```text
OpenAPI / Swagger
        |
        v
capability importer
        |
        v
capabilities/discovered/*.yaml
        |
        v
human review
        |
        v
capabilities/published/*.yaml
        |
        v
runtime registry loader
        |
        v
Copilot tools + HTTP adapter
```

There is no review UI in the first version. Review happens by editing YAML and
moving or changing capability status. A UI can be added later on top of the same
files and validation rules.

## Scope

In scope:

- A versioned `capability.yaml` schema.
- An OpenAPI importer that generates draft capability files.
- A runtime loader that only loads `published` capabilities.
- Strict validation for read and write capabilities.
- A generic HTTP adapter for JSON APIs.
- Output normalization into workbench diagnostic/result shapes.
- Governance rules that prevent unsafe write APIs from loading.
- Tests for importer inference, schema validation, loader filtering, and HTTP
  adapter request/response behavior.

Out of scope:

- A graphical API review page.
- Live API gateway discovery.
- OAuth setup or external secret management UI.
- Non-HTTP adapters.
- Automatically publishing discovered write APIs.
- Letting Copilot call raw backend APIs.
- Replacing the existing action plan, approval, execution, or audit model.

## Capability File Layout

Capabilities are stored in two directories:

```text
capabilities/
  discovered/
    minio.bucket.capacity.read.yaml
  published/
    glusterfs.volume.health.read.yaml
```

`discovered` files are importer output and are not available to Copilot.
`published` files are reviewed capabilities that may be loaded if validation
passes.

Each capability has a status:

```yaml
status: discovered # discovered | needs_review | published | deprecated
```

The runtime loader only accepts `status: published` from
`capabilities/published`.

## Capability Schema

Example read capability:

```yaml
schema_version: 1
name: minio.bucket.capacity.read
status: needs_review

domain: minio
resource_type: bucket
operation: read
risk: low

backend:
  adapter: http
  method: GET
  path: /api/minio/clusters/{cluster}/buckets/{bucket}/capacity
  timeout_ms: 3000

input_schema:
  environment:
    type: string
    required: true
  cluster:
    type: string
    required: true
  bucket:
    type: string
    required: true

output:
  kind: observation
  severity_path: $.data.severity
  summary_template: "Bucket {bucket} usage is {usage_pct}%"
  fields:
    usage_bytes: $.data.usage_bytes
    quota_bytes: $.data.quota_bytes
    usage_pct: $.data.usage_pct
    object_count: $.data.object_count

auth:
  roles: [viewer, operator, admin]
  environment_scoped: true

ai:
  description: Query MinIO bucket capacity, quota, object count, and usage.
  examples:
    - prod archive bucket capacity
    - is the archive bucket almost full
```

Example write capability:

```yaml
schema_version: 1
name: kafka.topic.retention.update
status: needs_review

domain: kafka
resource_type: topic
operation: write
risk: medium

backend:
  adapter: http
  method: POST
  path: /api/kafka/clusters/{cluster}/topics/{topic}/retention
  timeout_ms: 5000

input_schema:
  environment:
    type: string
    required: true
  cluster:
    type: string
    required: true
  topic:
    type: string
    required: true
  retention_hours:
    type: integer
    required: true

governance:
  requires_action_plan: true
  requires_approval: true
  precheck_tools:
    - kafka.topic.config.read
  rollback:
    strategy: restore_previous_value
    source: previous_config.retention_hours

auth:
  roles: [admin]
  environment_scoped: true

ai:
  description: Update Kafka topic retention through an approved action plan.
  examples:
    - set orders topic retention to 72 hours
```

## Schema Rules

All capabilities require:

- `schema_version`.
- `name`.
- `status`.
- `domain`.
- `resource_type`.
- `operation`.
- `risk`.
- `backend.adapter`.
- `backend.method`.
- `backend.path`.
- `input_schema.environment`.
- `auth.roles`.
- `auth.environment_scoped`.

Read capabilities require:

- `operation: read`.
- `risk: low` or `risk: medium`.
- `output.kind`.
- At least one output field or summary template.

Write capabilities require:

- `operation: write`.
- `risk: medium` or `risk: high`.
- `governance.requires_action_plan: true`.
- `governance.requires_approval: true`.
- At least one `governance.precheck_tools` entry.
- `governance.rollback`.

The loader rejects:

- Non-published capabilities.
- Published files outside `capabilities/published`.
- Duplicate capability names.
- Unknown backend adapters.
- Write capabilities without action-plan governance.
- Capabilities with unknown input types.
- Paths that reference missing input parameters.
- Inputs that are not environment scoped.

## OpenAPI Importer

Command:

```bash
copilot capability import openapi ./middleware-openapi.yaml
```

Default output:

```text
capabilities/discovered/
```

The importer reads OpenAPI paths and operations, then generates capability
drafts with `status: needs_review`.

### Inference Rules

Operation:

- `GET` -> `read`.
- `HEAD` -> `read`.
- `POST`, `PUT`, `PATCH`, `DELETE` -> `write`.

Risk:

- Paths containing `health`, `status`, `metrics`, `config`, `list`, `capacity`
  -> low risk when read.
- Paths containing `restart`, `rebalance`, `heal`, `quota`, `retention`,
  `lifecycle` -> medium risk unless overridden.
- Paths containing `delete`, `drop`, `force`, `truncate`, `purge`, `format`,
  `remove` -> high risk.

Domain:

- Paths or tags containing `minio` -> `minio`.
- Paths or tags containing `gluster`, `glusterfs` -> `glusterfs`.
- Paths or tags containing `kafka` -> `kafka`.
- Otherwise `unknown`, requiring review before publish.

Resource type:

- `bucket` -> `bucket`.
- `volume` -> `volume`.
- `topic` -> `topic`.
- `consumer` or `consumer-group` -> `consumer_group`.
- `broker` -> `broker`.
- Otherwise `resource`.

Naming:

```text
{domain}.{resource_type}.{verb-or-subresource}.{read|update|delete|action}
```

Examples:

- `minio.bucket.capacity.read`
- `glusterfs.volume.health.read`
- `kafka.consumer_group.lag.read`
- `kafka.topic.retention.update`

Importer output is intentionally conservative. If confidence is low, the
capability remains `needs_review` with comments explaining what must be checked.

## Review And Publish

The human review checklist:

- Capability name is stable and semantic.
- Domain and resource type are correct.
- Operation is correctly classified as read or write.
- Risk level is correct.
- Input schema contains no unsafe free-form fields unless justified.
- Output mapping removes secrets and large payloads.
- Roles are least-privilege.
- Writes include precheck tools, approval, action plan, and rollback.
- AI description is useful but not authoritative.

Publishing is file based:

1. Edit the YAML.
2. Set `status: published`.
3. Move the file to `capabilities/published`.
4. Run capability validation.
5. Restart or reload the registry.

## Runtime Loader

The runtime loader reads `capabilities/published/*.yaml`, validates each file,
and registers the capability as a governed tool.

The loader produces:

- Tool name.
- Operation.
- Risk.
- Domain.
- Resource type.
- Input schema.
- Backend adapter metadata.
- Output mapping metadata.
- Authorization metadata.
- Governance metadata.

The loader must fail closed. Invalid published capabilities are rejected and
logged. A single invalid file should not silently become available to Copilot.
The first implementation may fail service startup on invalid published files;
later versions can quarantine invalid files and expose an admin warning.

## HTTP Adapter

The first adapter is a generic HTTP JSON adapter.

Responsibilities:

- Validate input schema.
- Reject unknown input fields.
- Build path parameters safely.
- Encode query/body parameters.
- Add internal auth headers.
- Apply timeout.
- Call the existing management backend API.
- Parse JSON response.
- Normalize output.
- Redact sensitive fields.
- Enforce response-size limits.
- Emit audit metadata.

Adapter interface:

```go
type Adapter interface {
    Execute(ctx context.Context, user identity.CurrentUser, cap Capability, input map[string]any) (NormalizedResult, error)
}
```

HTTP adapter config:

```yaml
backend:
  adapter: http
  method: GET
  path: /api/minio/clusters/{cluster}/buckets/{bucket}/capacity
  timeout_ms: 3000
```

## Output Normalization

The adapter never returns raw backend JSON directly to Copilot.

Read outputs normalize into one of:

- `ResourceSnapshot`.
- `Observation`.
- `MetricSummary`.
- `ConfigSnapshot`.
- `TaskStatus`.

Write outputs normalize into:

- `OperationResult`.
- `TaskStatus`.

Example normalized observation:

```json
{
  "kind": "observation",
  "resource": {
    "domain": "minio",
    "type": "bucket",
    "name": "archive",
    "environment": "prod"
  },
  "severity": "warning",
  "summary": "Bucket archive usage is 86%",
  "data": {
    "usage_pct": 86,
    "quota_bytes": 1000000000,
    "object_count": 235000
  }
}
```

## Write Governance

Write capabilities are never directly executable from Copilot chat.

Write flow:

```text
Copilot recommendation
        |
        v
candidate write capability + input
        |
        v
policy + schema + risk validation
        |
        v
immutable action plan
        |
        v
approval / countersign
        |
        v
HTTP adapter calls existing backend write API
        |
        v
execution record + audit
```

The adapter can execute writes only when called by the confirmed action-plan
execution path.

## Safety Defaults

- Discovered capabilities are not loaded.
- Writes are never auto-published.
- Unknown domains require review.
- Unknown risk means reject.
- Unknown adapter means reject.
- Missing rollback on write means reject.
- Missing precheck on write means reject.
- Raw backend output is never returned without normalization.
- Sensitive fields are redacted by default when names match common patterns:
  `password`, `secret`, `token`, `key`, `credential`, `authorization`.

## Testing

Importer tests:

- Imports OpenAPI GET path as read capability draft.
- Imports OpenAPI POST path as write capability draft.
- Infers domain/resource/risk from tags and paths.
- Marks unknown domain as `needs_review`.
- Generates deterministic file names.

Schema tests:

- Rejects missing required fields.
- Rejects duplicate names.
- Rejects write capability without action plan governance.
- Rejects write capability without rollback.
- Rejects path variables missing from input schema.
- Accepts a valid read capability.
- Accepts a valid governed write capability.

Loader tests:

- Loads only `published` capabilities from `capabilities/published`.
- Ignores or rejects discovered capabilities.
- Fails closed on invalid published files.
- Registers loaded capabilities as governed tools.

HTTP adapter tests:

- Builds path parameters safely.
- Rejects unknown input fields.
- Rejects missing required fields.
- Applies timeout.
- Normalizes JSON output.
- Redacts sensitive fields.
- Enforces response-size limits.
- Emits read audit metadata.

Governance tests:

- Read capability executes through read-only path.
- Write capability creates an action plan instead of executing directly.
- Confirmed action plan can execute a write capability through the adapter.
- Unpublished capability cannot be called.

## Acceptance Criteria

- An OpenAPI file can be imported into deterministic capability draft YAML.
- Draft capabilities are not available to Copilot.
- Published read capabilities can be loaded and executed through the HTTP
  adapter.
- Published write capabilities are rejected unless they define action plan,
  approval, precheck, and rollback governance.
- Copilot never receives raw backend API access.
- Backend API responses are normalized before reaching Copilot or UI.
- Existing action plan, approval, execution, and audit boundaries remain the
  authority for all writes.
