# Swagger Import Workbench Design

## Goal

Build the next product step for the Vue Capability Console: a Swagger import
workbench that turns a large existing middleware admin API surface into a
reviewable queue of AI capabilities.

The operator should not feel they are editing YAML one API at a time. They
should feel they are processing imported API assets through a governed pipeline:
classify, keep or ignore, complete mappings, test, publish, and verify with AI.

## Product Positioning

This feature makes the product identity clearer:

```text
Traditional middleware admin APIs
        |
        v
Swagger import workbench
        |
        v
Reviewed Capability drafts
        |
        v
Published AI-callable operations tools
```

The console is not a replacement for the existing middleware backend. It uses
that backend's API inventory and turns selected APIs into safe, observable AI
tools.

## Recommended Direction

Add an import result workbench inside the existing single-page Vue app.

Keep the current URL import action, but change what happens after import:

1. Imported OpenAPI operations are shown as an import batch.
2. Operations are grouped by inferred domain: MinIO, Kafka, GlusterFS, or other.
3. Each operation shows a publishability verdict and the reason.
4. Operators can mark operations as `保留` or `忽略`.
5. Kept operations become or remain Capability drafts for detailed review.
6. The existing detail panel handles parameter editing, testing, publishing,
   and AI preflight.

This avoids creating a heavy wizard while still giving bulk API onboarding a
clear shape.

## Alternatives Considered

### Keep Current Import Strip

Import URL, immediately insert drafts into the existing list, and rely on
search/filter.

Trade-off: lowest effort, but poor for real Swagger documents with dozens or
hundreds of operations. Users cannot distinguish "import inventory" from
"review queue".

### Full Multi-Step Wizard

Use separate screens for source, classification, mapping, testing, and publish.

Trade-off: very clear for first-time onboarding, but slower for repeated
operator work and requires more persisted import session state. It is better as
a later enterprise workflow.

### Import Result Workbench

Use the current page but add a first-class import batch panel with grouping,
bulk triage, and reasons.

Trade-off: not as guided as a wizard, but fits the current architecture and the
operator use case. This is the recommended path.

## UI Design

### Import Source Panel

The top intake area remains the starting point:

- Swagger/OpenAPI URL input.
- Backend Base URL input.
- Primary action: `导入 Swagger URL`.
- Example URL hints: `/v3/api-docs`, `/openapi.json`, `/swagger/doc.json`.
- Result summary: imported count, read count, write count, publishable count,
  ignored count.

After a successful import, the page should scroll or focus attention to the new
import batch panel, not silently mix everything into the main list.

### Import Batch Panel

Add a new panel between the import source and the capability inventory.

Content:

- Batch title: `本次导入`.
- Summary metrics:
  - total operations
  - safe read candidates
  - write or risky operations
  - missing mapping
  - selected to keep
- Domain tabs or segmented filters: `全部`, `MinIO`, `Kafka`, `GlusterFS`,
  `其他`.
- Operation rows with:
  - method and path
  - inferred capability name
  - operation type: read/write
  - risk
  - verdict: `可生成草稿`, `需补映射`, `暂不接入 AI`
  - reason text
  - keep/ignore toggle
  - open-in-review action

Rows should be compact and scannable. Long paths should wrap cleanly but not
stretch the layout.

### Main Capability Queue

Keep the existing capability table, including the new `下一步` signal.

Once an imported item is kept, it should appear in the normal capability queue
as a draft. Ignored operations do not need backend persistence in the first
version; they are only hidden from the current import batch.

### Review Panel

Keep the current right-side detail panel as the review editor.

For imported Swagger operations, add small source context near the top:

- `来自 Swagger 导入`.
- Original method and path.
- Inference notes such as `路径变量已自动生成测试参数`.

Do not add another editor. The existing detail panel remains the single place
for mappings, tests, publish checks, and AI preflight.

## Classification Rules

The first version can use deterministic heuristics:

- HTTP `GET` becomes `read`.
- HTTP `POST`, `PUT`, `PATCH`, and `DELETE` become `write`.
- Domains are inferred from path/name tokens:
  - `minio`, `bucket`, `object` -> MinIO
  - `kafka`, `topic`, `consumer`, `broker` -> Kafka
  - `gluster`, `glusterfs`, `volume`, `brick` -> GlusterFS
- Resource type is inferred from path tokens such as `bucket`, `topic`,
  `consumer_group`, `volume`.
- Read GET operations with valid backend base URL and at least one useful
  output mapping are strong candidates.
- Write operations are imported as drafts but marked `暂不接入 AI` for first
  version publishing.

The UI should show these as machine suggestions, not final truth. Operators can
edit details in the review panel.

## Data Flow

Use the current endpoint for the first implementation:

```text
Vue sends Swagger URL + backend base URL
        |
        v
POST /v1/capabilities/import/openapi-url
        |
        v
Go manager fetches OpenAPI and returns ManagedCapability drafts
        |
        v
Vue creates a local import batch view
        |
        v
Operator keeps/ignores candidates
        |
        v
Kept candidates appear in the capability queue
```

No new backend endpoint is required for the initial UI. If the backend already
writes all discovered drafts, ignored status is only a local UI triage state.
A later version can persist import sessions and ignored decisions.

## Error Handling

- Invalid Swagger URL shows the existing error alert and keeps the previous
  import batch visible.
- Empty import response shows `没有识别到可接入 API`.
- Duplicate capability names show `已有同名能力` and link to the existing row.
- Unsupported write operations show `第一版暂不发布写入能力`.
- Missing path parameters or output mappings show `需补映射`.
- Network failure keeps user input in place so the operator can retry.

## Testing

Vue tests should cover:

- Import batch panel appears after Swagger URL import.
- Batch summary counts total, read, write, and selected items.
- Domain filter narrows imported rows.
- Keep/ignore toggle hides ignored rows from the normal review queue.
- Opening an imported row selects it in the review panel.
- Existing import API payload remains `{ openapi_url, backend_base_url }`.
- Existing publish, test, and AI preflight tests continue to pass.

Go backend tests should remain focused on importer correctness:

- URL import rejects non-http schemes.
- OpenAPI URL import creates discovered drafts.
- Path variables become input schema fields where possible.
- Published runtime still only loads reviewed published files.

## Out Of Scope

- Persisted import history.
- Multi-user import sessions.
- Batch publishing.
- Publishing write capabilities to AI runtime.
- OAuth or secret management UI.
- Automatic LLM-based API classification.
- Replacing the existing YAML capability store.

## Acceptance Criteria

- After importing Swagger URL, users see a clear import batch instead of only a
  longer flat list.
- Users can quickly tell which imported APIs are good read candidates, which
  need mapping work, and which are not safe for first-version AI exposure.
- Users can keep or ignore imported operations without losing the existing
  detailed review workflow.
- Kept operations still flow through validation, test, publish, and AI
  preflight.
- The page remains dense and operational, with no marketing-style landing
  sections.
- `npm test`, `npm run build`, and relevant Go tests pass after implementation.
