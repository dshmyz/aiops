# Capability Console Pipeline UI Design

## Goal

Upgrade the Vue Capability Console from a plain capability editor into an API
onboarding pipeline for existing middleware management backends.

The console should make the main job obvious: import Swagger/OpenAPI, review
generated capability drafts, fix mappings, test safely, and publish only
reviewed read capabilities into the Copilot runtime.

## Product Principle

This is not a marketing page and not a general CRUD console. It is an operator
workbench for turning traditional middleware admin APIs into AI-callable,
governed capabilities.

The UI should feel dense, calm, and operational. It should prioritize scanning,
triage, and repeated review over decoration.

## Recommended Direction

Use a three-zone pipeline layout:

```text
Import rail
  Swagger URL + backend base URL + import result + readiness counts

API asset queue
  searchable capability inventory with stronger status/domain/source signals

Review workbench
  selected capability details, generated mapping, validation/test preview,
  publish checklist
```

This keeps the current single-page app and existing API calls, but changes the
information hierarchy so Swagger import is the first-class entry point.

## Alternatives Considered

### Cosmetic polish only

Improve colors, spacing, and card treatment while keeping the current
structure.

Trade-off: fastest, but it still reads as a generic settings screen. It does
not solve the product problem that users need to process many imported APIs.

### Full import wizard

Move Swagger import into a multi-step wizard with import, classify, map,
preview, and publish steps.

Trade-off: clearer for first-time users, but heavier to implement and slower
for repeated admin work. It also needs more backend state for import sessions.

### Pipeline workbench

Keep the current single-screen app, but make import, queue, and review visually
and behaviorally distinct.

Trade-off: less guided than a wizard, but much faster for experienced operators
and a better fit for bulk API onboarding. This is the recommended path.

## Visual System

Palette:

- `#101820` ink for primary text and dense panels.
- `#F4F7F8` cloud for page background.
- `#FFFFFF` surface for work areas.
- `#0F766E` teal for primary import and safe read signals.
- `#2563A8` blue for selected assets and review focus.
- `#B45309` amber for needs-review states.
- `#B42318` red for validation failures.

Typography:

- Use the current system sans stack for compatibility and speed.
- Treat labels and table metadata as utility text: 11-12px, uppercase only
  where it encodes state.
- Avoid hero-scale type inside the tool. The top title can remain compact.

Signature element:

- The import rail should look like an intake lane, not a normal form row. It
  uses a stronger left accent, numbered pipeline labels, and compact progress
  metrics: imported drafts, needs review, publishable, invalid.

## UI Changes

### 1. Import Rail

Replace the current simple `import-strip` with a stronger pipeline intake band.

Contents:

- Swagger/OpenAPI URL input.
- Backend Base URL input.
- Primary action: `导入 Swagger URL`.
- Import result line.
- Compact hint examples:
  - `/v3/api-docs`
  - `/openapi.json`
  - `/swagger/doc.json`

Behavior:

- The button continues to call `POST /v1/capabilities/import/openapi-url`.
- On success, imported drafts are inserted into the list and the first imported
  draft is selected.
- No automatic publishing.

### 2. API Asset Queue

Make the capability table read more like an asset queue:

- Add stronger state chips for `草稿`, `已发布`, and validation state.
- Emphasize domain/resource as the second line under the name.
- Keep method/path readable, but avoid letting long paths dominate the row.
- Keep current search and filters.
- Add a compact empty/loading treatment.

First implementation does not add true batch operations. It should prepare the
layout for batch controls later without inventing backend behavior now.

### 3. Review Workbench

Reframe the right-side editor as a selected asset review panel:

- Header shows selected capability name, source, operation, and validation
  state.
- Group order becomes:
  1. 识别结果
  2. 后端接口
  3. 输入参数
  4. 输出映射
  5. 发布检查
  6. 测试预览

The existing fields stay editable. The redesign should not remove manual
editing, validation, test, publish, or unpublish behaviors.

### 4. Publish Readiness

The publish checklist should feel like a gate, not another form group:

- Use clear pass/fail rows.
- Keep target file path visible.
- Keep the publish button disabled unless the selected item is publishable.
- Continue enforcing first-version read-only publishing rules.

## Data Flow

No new backend persistence is required.

```text
User enters Swagger URL
        |
        v
POST /v1/capabilities/import/openapi-url
        |
        v
Go manager fetches OpenAPI and writes discovered YAML drafts
        |
        v
Vue upserts returned ManagedCapability items
        |
        v
User reviews, validates, tests, and publishes safe reads
```

## Error Handling

- Invalid OpenAPI URL errors stay in the existing error alert.
- Import success should be visible near the import rail.
- Validation failures stay attached to the selected capability.
- Large imports must not break the response path; the backend already provides
  a larger capability management response cap.

## Testing

Update Vue tests to assert:

- The import rail renders as the primary intake area.
- Importing Swagger URL still sends `openapi_url` and `backend_base_url`.
- Imported drafts appear in the asset queue.
- The publish checklist remains visible after the layout change.

Existing Go tests for URL import and capability management response size remain
the backend coverage.

## Out Of Scope

- Multi-step import sessions.
- Batch save/publish actions.
- Real import history stored in a database.
- Automatic domain/risk editing across imported groups.
- Query/header parameter support.
- Publishing write capabilities into runtime.

## Acceptance Criteria

- The first viewport clearly communicates that Swagger URL import is the main
  entry point.
- The page still works as a dense operational backend, not a landing page.
- Existing capability list, edit, validate, test, publish, and unpublish flows
  keep working.
- The design remains responsive at desktop and mobile widths.
- Automated Vue tests and build pass.
