# Capability Console AI Preflight Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an AI preflight panel to the Vue Capability Console so operators can test whether a published Capability is reachable through the built-in assistant.

**Architecture:** Keep the existing single-page Vue workbench. Add a small API wrapper for `POST /v1/assistant/messages`, derive a natural-language prompt from the selected Capability metadata and input JSON, and display the assistant response next to the publish/test workflow. Do not add backend endpoints or bypass the existing assistant runtime, policy, or read execution path.

**Tech Stack:** Vue 3, Element Plus, Vitest, existing Go assistant endpoint.

## Global Constraints

- Only call the existing `/v1/assistant/messages` assistant route.
- The panel must not expose raw backend URLs as assistant tools.
- The first version targets published/read Capability verification; draft testing stays in the existing direct Capability test panel.
- The UI remains a dense operational workbench, not a landing page.
- Write tests before production code changes.

---

### Task 1: Assistant API Wrapper

**Files:**
- Modify: `apps/capability-console/src/types.ts`
- Modify: `apps/capability-console/src/api.ts`
- Test: `apps/capability-console/src/App.test.ts`

**Interfaces:**
- Produces: `AssistantConsoleResponse`
- Produces: `sendAssistantMessage(message: string): Promise<AssistantConsoleResponse>`

- [ ] **Step 1: Write the failing test**

Add a Vue test that clicks the new AI preflight button and expects a `POST /v1/assistant/messages` call with `{ "message": "查询 prod m1 archive bucket 的 minio 容量" }`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/capability-console && npm test -- --run App.test.ts`

Expected: FAIL because the AI preflight controls and API wrapper do not exist.

- [ ] **Step 3: Add types and API function**

Add a minimal assistant response union and `sendAssistantMessage` wrapper that reuses the existing JSON request helper.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd apps/capability-console && npm test -- --run App.test.ts`

Expected: PASS.

### Task 2: AI Preflight Panel

**Files:**
- Modify: `apps/capability-console/src/App.vue`
- Modify: `apps/capability-console/src/styles.css`
- Test: `apps/capability-console/src/App.test.ts`

**Interfaces:**
- Consumes: `sendAssistantMessage(message: string): Promise<AssistantConsoleResponse>`
- Produces: UI state for prompt text, assistant response preview, running/error status, and a published-read readiness gate.

- [ ] **Step 1: Write failing UI tests**

Cover:
- The panel renders for the selected capability.
- The default prompt is derived from selected Capability and test input JSON.
- The button is disabled until the selected item is published and read-only.
- A successful assistant answer is rendered.
- A clarification response is rendered without treating it as a thrown error.

- [ ] **Step 2: Run tests to verify failure**

Run: `cd apps/capability-console && npm test -- --run App.test.ts`

Expected: FAIL because the panel is not implemented.

- [ ] **Step 3: Implement minimal Vue state and markup**

Add the AI preflight panel below publish readiness and before direct test input. Generate the prompt from `environment`, first non-environment input value, `resource_type`, `domain`, and an operation keyword derived from the Capability name.

- [ ] **Step 4: Add focused CSS**

Style the panel as a compact operational gate with status chips and fixed-height JSON preview. Keep the existing palette.

- [ ] **Step 5: Run tests and build**

Run:

```bash
cd apps/capability-console && npm test -- --run App.test.ts
cd apps/capability-console && npm run build
```

Expected: PASS.

