# Swagger Import Wizard Design

## Goal

Build a first-version Swagger import wizard for the Vue Capability Console so a
platform admin can connect a large existing middleware backend without creating
draft pollution up front.

The wizard should answer the admin's core question:

> Which APIs from this Swagger document are good AI operation candidates, and
> which ones should become Capability drafts now?

This is the next step after the current import workbench. The current workbench
already helps triage imported drafts, but the backend writes drafts as soon as
the URL import succeeds. The new wizard moves triage before draft creation.

## User Decisions

The first version uses a mixed mode:

- The system recommends candidate APIs automatically.
- The admin can override recommendation decisions before saving anything.
- The backend creates Capability drafts only after the admin confirms.

The first version uses the standard preview flow:

- Preview Swagger first.
- Show candidates without saving drafts.
- Commit selected candidates later.

## Product Model

```text
Traditional middleware admin backend
        |
        v
Swagger / OpenAPI URL
        |
        v
Preview candidates without saving drafts
        |
        v
Admin selects and adjusts candidates
        |
        v
Create Capability drafts
        |
        v
Review / test / publish read tools
        |
        v
AI 运维助手 calls published capabilities
```

This keeps the product language clear:

- `AI 运维助手` is the user entry for natural-language operations.
- `能力接入管理` is the admin factory for turning existing backend APIs into
  governed AI-callable capabilities.
- `Swagger 接入向导` is the intake flow inside that factory.

## Recommended Direction

Add a two-phase import flow:

1. `POST /v1/capabilities/import/openapi-url/preview`
   - Fetches and parses the OpenAPI document.
   - Infers candidate Capability data.
   - Classifies candidates as recommended, needs adjustment, or not recommended.
   - Does not write draft files.
2. `POST /v1/capabilities/import/openapi-url/commit`
   - Receives the same source information plus selected candidate IDs and
     admin overrides.
   - Re-fetches and re-parses the OpenAPI document for a stateless first
     implementation.
   - Saves only selected candidates as discovered Capability drafts.
   - Returns the saved `ManagedCapability` items.

This avoids introducing database-backed import sessions in the first version.
The trade-off is that commit may fetch the Swagger document twice. That is
acceptable for v1 because it keeps the server simple and avoids session cleanup,
but the API shape can later grow a persisted import session if needed.

## Alternatives Considered

### Keep Import-As-Draft

Continue using `POST /v1/capabilities/import/openapi-url`, immediately writing
all discovered drafts, then let the UI hide ignored items.

Trade-off: fastest, but wrong for large real API catalogs. It pollutes the
draft queue and makes the admin clean up after the tool.

### Persist Import Sessions

Create a backend import session table or filesystem session, then commit by
`preview_id`.

Trade-off: best for very large imports and long-running review sessions, but it
adds storage, expiration, and cleanup concerns before the product needs them.

### Stateless Preview And Commit

Preview returns deterministic candidates. Commit re-parses the same Swagger URL
and saves selected candidates by deterministic candidate ID.

Trade-off: the document is fetched twice, and commit should detect if the source
changed. The implementation is simpler and gives the desired no-draft-preview
experience. This is the recommended first version.

## Backend API

### Preview Swagger URL

Endpoint:

```http
POST /v1/capabilities/import/openapi-url/preview
```

Request:

```json
{
  "openapi_url": "http://127.0.0.1:19090/v3/api-docs",
  "backend_base_url": "http://127.0.0.1:19090"
}
```

Response:

```json
{
  "source": {
    "openapi_url": "http://127.0.0.1:19090/v3/api-docs",
    "backend_base_url": "http://127.0.0.1:19090",
    "fingerprint": "sha256:..."
  },
  "stats": {
    "total": 42,
    "recommended": 18,
    "needs_adjustment": 9,
    "not_recommended": 15,
    "read": 24,
    "write": 18
  },
  "candidates": [
    {
      "id": "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
      "method": "GET",
      "path": "/api/minio/{cluster}/buckets/{bucket}/capacity",
      "operation_id": "getMinioBucketCapacity",
      "capability": {
        "name": "minio.bucket.capacity.read",
        "domain": "minio",
        "resource_type": "bucket",
        "operation": "read",
        "risk": "low"
      },
      "recommendation": "recommended",
      "reasons": ["GET read operation", "known middleware domain"],
      "warnings": []
    }
  ]
}
```

Notes:

- `fingerprint` is computed from the fetched OpenAPI document bytes. Commit can
  reject or warn if the document changes between preview and commit.
- `candidate.id` must be deterministic from method + normalized path. If
  `operationId` exists, it is metadata, not the primary identity.
- `capability` in preview may include the full inferred `Capability` object in
  implementation, but the UI should present only the fields admins need to
  review first.

### Commit Selected Candidates

Endpoint:

```http
POST /v1/capabilities/import/openapi-url/commit
```

Request:

```json
{
  "openapi_url": "http://127.0.0.1:19090/v3/api-docs",
  "backend_base_url": "http://127.0.0.1:19090",
  "fingerprint": "sha256:...",
  "selections": [
    {
      "candidate_id": "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
      "overrides": {
        "name": "minio.bucket.capacity.read",
        "domain": "minio",
        "resource_type": "bucket",
        "operation": "read",
        "risk": "low"
      }
    }
  ]
}
```

Response:

```json
{
  "capabilities": [
    {
      "name": "minio.bucket.capacity.read",
      "source": "discovered",
      "status": "needs_review"
    }
  ],
  "skipped": [
    {
      "candidate_id": "POST /api/kafka/{cluster}/topics/{topic}/retention",
      "reason": "not selected"
    }
  ]
}
```

Commit behavior:

- Only `admin` role can preview or commit, matching the current import endpoint.
- Commit saves selected candidates as discovered drafts.
- Commit reuses existing `SaveDraft` and validation rules.
- Commit should reject unsupported URL schemes with the current URL validation.
- If the fetched document fingerprint differs from the preview fingerprint,
  return a clear error: `Swagger 文档已变化，请重新预览`.
- Existing `POST /v1/capabilities/import/openapi-url` remains for compatibility
  and tests, but the Vue wizard should use preview + commit.

## Candidate Recommendation Model

Recommendation values:

- `recommended`
  - Safe-looking read candidate.
  - Usually `GET`.
  - Domain and resource type inferred with reasonable confidence.
  - No duplicate published capability name.
- `needs_adjustment`
  - Candidate may be useful, but mapping or naming needs admin attention.
  - Missing output summary mapping.
  - Ambiguous domain/resource.
  - Duplicate draft name or weak operation ID.
- `not_recommended`
  - Write operation in v1.
  - High-risk endpoint.
  - Auth/session/admin endpoints that should not be exposed to AI.
  - Upload/download or broad list endpoints that are too generic for first pass.

First-version deterministic rules:

- HTTP `GET` defaults to `read`.
- `POST`, `PUT`, `PATCH`, and `DELETE` default to `write`.
- `minio`, `bucket`, `object` suggest domain `minio`.
- `kafka`, `topic`, `consumer`, `consumer-groups`, `broker` suggest domain
  `kafka`.
- `gluster`, `glusterfs`, `volume`, `brick` suggest domain `glusterfs`.
- Paths containing `delete`, `update`, `set`, `retention`, `create`, `restart`,
  `rebalance`, or `repair` increase risk or mark as not recommended.
- Known safe read paths can still require adjustment if output fields cannot be
  inferred.

The UI should present recommendations as suggestions, not final truth.

## UI Design

The wizard lives inside `能力接入管理`.

Replace the current single import strip with a compact stepper:

```text
1. 来源
2. 候选 API
3. 调整选择
4. 生成草稿
```

### Step 1: 来源

Fields:

- `Swagger / OpenAPI 地址`
- `中间件后台 Base URL`

Primary action:

- `预览 API`

Supporting text:

- Show examples: `/v3/api-docs`, `/openapi.json`, `/swagger/doc.json`.
- Keep the copy operational, not marketing-like.

### Step 2: 候选 API

Show preview stats:

- 全部接口
- 推荐接入
- 需要调整
- 暂不接入
- 读取
- 写入

Filters:

- Recommendation filter: `全部`, `推荐接入`, `需要调整`, `暂不接入`.
- Domain filter: `全部领域`, `minio`, `kafka`, `glusterfs`, `其他`.
- Search by method, path, capability name, operation ID.

Rows:

- Method pill.
- Path.
- Suggested capability name.
- Domain/resource/operation/risk.
- Recommendation label and reasons.
- Selection checkbox.

Default selection:

- `recommended` candidates selected by default.
- `needs_adjustment` and `not_recommended` unselected by default.

### Step 3: 调整选择

The admin can edit selected candidates before draft creation:

- Capability name.
- Domain.
- Resource type.
- Operation type.
- Risk.

Do not build a full output mapping editor here. Output mapping remains in the
existing review panel after draft creation. The wizard should only collect the
minimum data needed to decide whether a candidate should become a draft.

### Step 4: 生成草稿

Show confirmation summary:

- Number of selected candidates.
- How many are reads.
- How many are writes or risky candidates.
- Warnings that write candidates can be drafted but cannot be published to AI
  in v1.

Primary action:

- `生成 Capability 草稿`

After commit:

- Upsert returned drafts into the existing capability queue.
- Create the current import batch from saved drafts so the existing workbench
  still works.
- Select the first saved draft.
- Show result: `已生成 N 个待评审草稿`.

## Frontend State

Add local wizard state in the Vue console:

- `importWizardStep: 'source' | 'candidates' | 'adjust' | 'commit'`
- `importPreview: ImportPreview | null`
- `importPreviewLoading: boolean`
- `importCommitLoading: boolean`
- `candidateSelections: Record<string, boolean>`
- `candidateOverrides: Record<string, CandidateOverride>`
- `candidateFilters`

The current `ImportBatch` module can be reused or generalized:

- Keep current verdict concepts for post-commit drafts.
- Add a separate preview model for pre-draft candidates.
- Do not force preview candidates into `ManagedCapability` until commit.

## Error Handling

- Invalid Swagger URL: show error and keep source inputs unchanged.
- Network failure: show retryable error; do not clear previous preview.
- Empty OpenAPI paths: show `没有识别到可接入 API`.
- Commit with zero selections: disable `生成 Capability 草稿`.
- Fingerprint mismatch: show `Swagger 文档已变化，请重新预览`.
- Duplicate capability names: mark candidate `needs_adjustment` and show the
  existing capability name conflict.
- Commit partial failure: return saved drafts plus per-candidate errors; UI
  shows saved count and failed rows.

## Testing

Backend tests:

- Preview endpoint rejects non-http URLs.
- Preview endpoint parses OpenAPI URL and returns candidates without writing
  discovered drafts.
- Preview response includes deterministic candidate IDs and source fingerprint.
- Commit endpoint saves only selected candidates.
- Commit endpoint rejects changed fingerprint.
- Existing import-as-draft endpoint still works for compatibility.

Frontend tests:

- Management view renders the import wizard source step.
- `预览 API` sends `openapi_url` and `backend_base_url` to the preview endpoint.
- Preview candidates render grouped recommendation counts.
- Recommended candidates are selected by default.
- Admin can deselect a recommended candidate and edit selected candidate
  metadata.
- Commit sends selected candidate IDs and overrides.
- Saved drafts appear in the existing capability queue and import batch.
- Zero selected candidates disables commit.
- Existing review, validate, test, publish, unpublish, and AI preflight tests
  continue passing.

## Migration And Compatibility

- Keep `POST /v1/capabilities/import/openapi-url`.
- Existing CLI importer remains unchanged.
- Existing example mock Swagger URL remains valid.
- Existing docs can present the wizard as the default path, while retaining the
  old endpoint as a compatibility API.
- No database migration is required for the stateless first version.

## Out Of Scope

- Persisted import history.
- Multi-user collaborative import sessions.
- Batch publish.
- Publishing write capabilities into AI runtime.
- LLM-based classification.
- OAuth or secret management UI.
- Full output mapping inside the wizard.
- Replacing the existing review editor.
- Vue Router.

## Acceptance Criteria

- Previewing a Swagger URL does not create any Capability draft files.
- The admin can see recommended, needs-adjustment, and not-recommended API
  candidates before saving.
- Recommended candidates are selected by default, but the admin can change the
  selection and metadata.
- Committing creates drafts only for selected candidates.
- Saved drafts continue through the existing review, test, publish, and AI
  preflight workflow.
- The UI makes the product story clear: existing backend APIs become reviewed
  AI-callable capabilities, which the AI operations assistant can use.
