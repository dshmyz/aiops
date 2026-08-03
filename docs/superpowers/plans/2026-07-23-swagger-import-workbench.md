# Swagger Import Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-version Swagger import result workbench to the Vue Capability Console so operators can triage imported middleware API operations before reviewing and publishing capabilities.

**Architecture:** Keep the existing single-page Vue app and current `/v1/capabilities/import/openapi-url` endpoint. Add a small pure TypeScript import-batch module for deterministic verdicts, filters, and keep/ignore state, then render the batch panel in `App.vue` while preserving the existing capability queue and review panel.

**Tech Stack:** Vue 3 `<script setup>`, TypeScript, Vitest, Vue Test Utils, Element Plus, existing Go capability management API.

## Global Constraints

- Do not add a new backend import-session endpoint in this iteration.
- Use the existing `POST /v1/capabilities/import/openapi-url` request payload: `{ openapi_url, backend_base_url }`.
- Ignored operations are local UI triage state only; backend-discovered YAML behavior remains unchanged.
- Keep publishing read-only in first version: AI runtime publish still requires discovered + read + GET + valid + no same-name published twin.
- Keep the UI dense and operational, not marketing-style.
- Existing validate, test, publish, unpublish, and AI preflight flows must continue working.
- Use TDD: every behavior change starts with a failing test.

---

## File Structure

- Create `apps/capability-console/src/importBatch.ts`
  - Owns import-batch derivation, verdict labels, stats, and filtering.
  - Does not import Vue.
- Create `apps/capability-console/src/importBatch.test.ts`
  - Tests import-batch logic without mounting the app.
- Modify `apps/capability-console/src/App.vue`
  - Adds import batch state.
  - Calls `createImportBatch` after Swagger URL import.
  - Renders batch metrics, domain filter, keep/ignore toggle, and open-in-review action.
- Modify `apps/capability-console/src/App.test.ts`
  - Covers the import batch panel, filtering, keep/ignore behavior, and review selection.
- Modify `apps/capability-console/src/styles.css`
  - Adds compact operational styling for the import batch panel.

---

### Task 1: Pure Import Batch Model

**Files:**
- Create: `apps/capability-console/src/importBatch.ts`
- Create: `apps/capability-console/src/importBatch.test.ts`

**Interfaces:**
- Consumes: `ManagedCapability` from `apps/capability-console/src/types.ts`
- Produces:
  - `type ImportVerdict = 'draft_ready' | 'needs_mapping' | 'not_ai_ready' | 'duplicate'`
  - `interface ImportBatchItem`
  - `interface ImportBatch`
  - `function createImportBatch(items: ManagedCapability[], existing: ManagedCapability[]): ImportBatch`
  - `function setImportItemIgnored(batch: ImportBatch, name: string, ignored: boolean): ImportBatch`
  - `function filterImportBatchItems(batch: ImportBatch, domain: string): ImportBatchItem[]`

- [ ] **Step 1: Write the failing import-batch tests**

Create `apps/capability-console/src/importBatch.test.ts`:

```ts
import { describe, expect, test } from 'vitest';
import { normalizeCapability } from './capability';
import {
  createImportBatch,
  filterImportBatchItems,
  setImportItemIgnored,
} from './importBatch';
import type { ManagedCapability } from './types';

function capability(partial: Partial<ManagedCapability>): ManagedCapability {
  return normalizeCapability({
    status: 'needs_review',
    source: 'discovered',
    validation: { valid: true },
    backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
    output: {
      kind: 'observation',
      severity_path: '',
      summary_template: 'Bucket {bucket} usage is {usage_pct}%',
      fields: { usage_pct: '$.data.usage_pct' },
    },
    ...partial,
  });
}

describe('import batch', () => {
  test('classifies imported operations and computes summary stats', () => {
    const batch = createImportBatch(
      [
        capability({ name: 'minio.bucket.capacity.read', domain: 'minio', operation: 'read' }),
        capability({
          name: 'kafka.topic.retention.update',
          domain: 'kafka',
          operation: 'write',
          risk: 'medium',
          backend: { method: 'POST', base_url: 'https://middleware.example.com', path: '/api/kafka/{cluster}/topics/{topic}/retention' },
        }),
        capability({
          name: 'glusterfs.volume.status.read',
          domain: 'glusterfs',
          operation: 'read',
          output: { kind: 'observation', severity_path: '', summary_template: '', fields: {} },
        }),
      ],
      [],
    );

    expect(batch.stats).toEqual({
      total: 3,
      read: 2,
      write: 1,
      selected: 3,
      ignored: 0,
      needsMapping: 1,
      notAIReady: 1,
    });
    expect(batch.items.map((item) => [item.name, item.verdict, item.reason])).toEqual([
      ['minio.bucket.capacity.read', 'draft_ready', '可生成草稿'],
      ['kafka.topic.retention.update', 'not_ai_ready', '第一版暂不发布写入能力'],
      ['glusterfs.volume.status.read', 'needs_mapping', '需补输出映射'],
    ]);
  });

  test('marks duplicate imported operations against existing capabilities', () => {
    const batch = createImportBatch(
      [capability({ name: 'minio.bucket.capacity.read', domain: 'minio', operation: 'read' })],
      [capability({ name: 'minio.bucket.capacity.read', source: 'published', status: 'published' })],
    );

    expect(batch.items[0].verdict).toBe('duplicate');
    expect(batch.items[0].reason).toBe('已有同名能力');
  });

  test('filters by domain and tracks ignored items immutably', () => {
    const batch = createImportBatch(
      [
        capability({ name: 'minio.bucket.capacity.read', domain: 'minio' }),
        capability({ name: 'kafka.consumer_group.lag.read', domain: 'kafka', resource_type: 'consumer_group' }),
      ],
      [],
    );

    const next = setImportItemIgnored(batch, 'minio.bucket.capacity.read', true);

    expect(filterImportBatchItems(next, 'kafka').map((item) => item.name)).toEqual(['kafka.consumer_group.lag.read']);
    expect(next.stats.selected).toBe(1);
    expect(next.stats.ignored).toBe(1);
    expect(batch.stats.selected).toBe(2);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/capability-console
npm test -- --run src/importBatch.test.ts
```

Expected: FAIL because `./importBatch` does not exist.

- [ ] **Step 3: Implement the pure import-batch module**

Create `apps/capability-console/src/importBatch.ts`:

```ts
import type { ManagedCapability } from './types';

export type ImportVerdict = 'draft_ready' | 'needs_mapping' | 'not_ai_ready' | 'duplicate';

export interface ImportBatchItem {
  name: string;
  domain: string;
  method: string;
  path: string;
  operation: string;
  risk: string;
  capability: ManagedCapability;
  verdict: ImportVerdict;
  verdictLabel: string;
  reason: string;
  ignored: boolean;
}

export interface ImportBatchStats {
  total: number;
  read: number;
  write: number;
  selected: number;
  ignored: number;
  needsMapping: number;
  notAIReady: number;
}

export interface ImportBatch {
  items: ImportBatchItem[];
  domains: string[];
  stats: ImportBatchStats;
}

export function createImportBatch(items: ManagedCapability[], existing: ManagedCapability[]): ImportBatch {
  const existingNames = new Set(existing.map((item) => item.name));
  return buildBatch(
    items.map((capability) => {
      const verdict = classifyCapability(capability, existingNames);
      return {
        name: capability.name,
        domain: capability.domain || 'other',
        method: capability.backend.method || 'GET',
        path: capability.backend.path || '/',
        operation: capability.operation,
        risk: capability.risk,
        capability,
        verdict: verdict.verdict,
        verdictLabel: verdict.label,
        reason: verdict.reason,
        ignored: false,
      };
    }),
  );
}

export function setImportItemIgnored(batch: ImportBatch, name: string, ignored: boolean): ImportBatch {
  return buildBatch(batch.items.map((item) => (item.name === name ? { ...item, ignored } : item)));
}

export function filterImportBatchItems(batch: ImportBatch, domain: string): ImportBatchItem[] {
  if (domain === 'all') {
    return batch.items;
  }
  return batch.items.filter((item) => item.domain === domain);
}

function buildBatch(items: ImportBatchItem[]): ImportBatch {
  const domains = Array.from(new Set(items.map((item) => item.domain).filter(Boolean))).sort();
  const stats = {
    total: items.length,
    read: items.filter((item) => item.operation === 'read').length,
    write: items.filter((item) => item.operation === 'write').length,
    selected: items.filter((item) => !item.ignored).length,
    ignored: items.filter((item) => item.ignored).length,
    needsMapping: items.filter((item) => item.verdict === 'needs_mapping').length,
    notAIReady: items.filter((item) => item.verdict === 'not_ai_ready').length,
  };
  return { items, domains, stats };
}

function classifyCapability(
  capability: ManagedCapability,
  existingNames: Set<string>,
): { verdict: ImportVerdict; label: string; reason: string } {
  if (existingNames.has(capability.name)) {
    return { verdict: 'duplicate', label: '已有同名能力', reason: '已有同名能力' };
  }
  if (capability.operation !== 'read' || capability.backend.method !== 'GET') {
    return { verdict: 'not_ai_ready', label: '暂不接入 AI', reason: '第一版暂不发布写入能力' };
  }
  if (!capability.output.summary_template && Object.keys(capability.output.fields).length === 0) {
    return { verdict: 'needs_mapping', label: '需补映射', reason: '需补输出映射' };
  }
  return { verdict: 'draft_ready', label: '可生成草稿', reason: '可生成草稿' };
}
```

- [ ] **Step 4: Run tests to verify Task 1 passes**

Run:

```bash
cd apps/capability-console
npm test -- --run src/importBatch.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add apps/capability-console/src/importBatch.ts apps/capability-console/src/importBatch.test.ts
git commit -m "feat: model swagger import batches"
```

---

### Task 2: App State and Import Batch Interaction

**Files:**
- Modify: `apps/capability-console/src/App.test.ts`
- Modify: `apps/capability-console/src/App.vue`

**Interfaces:**
- Consumes: `createImportBatch`, `setImportItemIgnored`, `filterImportBatchItems`, `ImportBatch`
- Produces:
  - `importBatch` Vue ref
  - `importDomainFilter` Vue ref
  - `visibleImportBatchItems` computed
  - `toggleImportIgnored(name: string, ignored: boolean): void`
  - `openImportedCapability(name: string): void`

- [ ] **Step 1: Write failing app tests for import batch behavior**

Append these assertions to the existing `imports Swagger URL into discovered drafts` test in `apps/capability-console/src/App.test.ts` after the existing import result assertions:

```ts
expect(wrapper.find('[data-test="import-batch"]').text()).toContain('本次导入');
expect(wrapper.find('[data-test="import-batch"]').text()).toContain('minio.bucket.capacity.read.imported');
expect(wrapper.find('[data-test="import-batch-stat-total"]').text()).toContain('1');
expect(wrapper.find('[data-test="import-batch-stat-selected"]').text()).toContain('1');

await wrapper.find('[data-test="ignore-import-minio.bucket.capacity.read.imported"]').setValue(true);
expect(wrapper.find('[data-test="import-batch-stat-selected"]').text()).toContain('0');
expect(wrapper.find('[data-test="import-batch-stat-ignored"]').text()).toContain('1');

await wrapper.find('[data-test="ignore-import-minio.bucket.capacity.read.imported"]').setValue(false);
await wrapper.find('[data-test="open-import-minio.bucket.capacity.read.imported"]').trigger('click');
expect((wrapper.find('[data-test="capability-name"]').find('input').element as HTMLInputElement).value).toBe('minio.bucket.capacity.read.imported');
```

Add a second app test for domain filtering:

```ts
test('filters the latest Swagger import batch by domain', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    if (String(input) === '/v1/capabilities') {
      return ok({ capabilities: [] });
    }
    if (String(input) === '/v1/capabilities/import/openapi-url') {
      return ok({
        capabilities: [
          {
            name: 'minio.bucket.capacity.read',
            status: 'needs_review',
            source: 'discovered',
            domain: 'minio',
            resource_type: 'bucket',
            operation: 'read',
            risk: 'low',
            backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
            validation: { valid: true },
          },
          {
            name: 'kafka.topic.retention.update',
            status: 'needs_review',
            source: 'discovered',
            domain: 'kafka',
            resource_type: 'topic',
            operation: 'write',
            risk: 'medium',
            backend: { method: 'POST', base_url: 'https://middleware.example.com', path: '/api/kafka/{cluster}/topics/{topic}/retention' },
            validation: { valid: true },
          },
        ],
      });
    }
    return ok({});
  }));

  const wrapper = mountApp();
  await flushPromises();

  await wrapper.find('[data-test="import-openapi-url"]').trigger('click');
  await flushPromises();

  expect(wrapper.find('[data-test="import-batch"]').text()).toContain('minio.bucket.capacity.read');
  expect(wrapper.find('[data-test="import-batch"]').text()).toContain('kafka.topic.retention.update');

  await wrapper.find('[data-test="import-domain-filter"]').setValue('kafka');

  expect(wrapper.find('[data-test="import-batch"]').text()).not.toContain('minio.bucket.capacity.read');
  expect(wrapper.find('[data-test="import-batch"]').text()).toContain('kafka.topic.retention.update');
  expect(wrapper.find('[data-test="import-batch"]').text()).toContain('暂不接入 AI');
});
```

- [ ] **Step 2: Run app tests to verify they fail**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts
```

Expected: FAIL because `import-batch` UI and interaction handlers are absent.

- [ ] **Step 3: Add import batch state to `App.vue`**

Modify the imports in `apps/capability-console/src/App.vue`:

```ts
import { createImportBatch, filterImportBatchItems, setImportItemIgnored } from './importBatch';
import type { ImportBatch } from './importBatch';
```

Add state after the current import refs:

```ts
const importBatch = ref<ImportBatch | null>(null);
const importDomainFilter = ref('all');
```

Add computed state near `availableDomains`:

```ts
const visibleImportBatchItems = computed(() => {
  if (!importBatch.value) {
    return [];
  }
  return filterImportBatchItems(importBatch.value, importDomainFilter.value);
});
```

Modify `importSwaggerURL` after `const imported = await importOpenAPIURL(...)`:

```ts
importBatch.value = createImportBatch(imported, capabilities.value);
importDomainFilter.value = 'all';
```

Keep the existing `upsert`, first imported selection, and import message logic.

Add handlers near `publishSelected`:

```ts
function toggleImportIgnored(name: string, ignored: boolean) {
  if (!importBatch.value) {
    return;
  }
  importBatch.value = setImportItemIgnored(importBatch.value, name, ignored);
}

function openImportedCapability(name: string) {
  const item = capabilities.value.find((capability) => capability.name === name);
  if (item) {
    selectCapability(item);
  }
}
```

- [ ] **Step 4: Run app tests to verify state still fails only on missing template**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts
```

Expected: FAIL because the import batch state exists but no template renders `data-test="import-batch"`.

- [ ] **Step 5: Commit Task 2 state changes only if tests still fail for missing UI**

Do not commit a failing state. Continue directly to Task 3 if Task 2 is being executed in one session. If using subagents, Task 2 and Task 3 should be assigned together to keep the branch green.

---

### Task 3: Import Batch Panel UI

**Files:**
- Modify: `apps/capability-console/src/App.vue`
- Modify: `apps/capability-console/src/styles.css`
- Test: `apps/capability-console/src/App.test.ts`

**Interfaces:**
- Consumes: `visibleImportBatchItems`, `importBatch`, `importDomainFilter`, `toggleImportIgnored`, `openImportedCapability`
- Produces: Rendered batch panel with `data-test` selectors used by Task 2 tests.

- [ ] **Step 1: Add the batch panel template**

Insert this section immediately after the existing `swagger-url-import` section:

```vue
<section v-if="importBatch" data-test="import-batch" class="import-batch-panel" aria-label="本次 Swagger 导入">
  <div class="section-heading">
    <div>
      <h2>本次导入</h2>
      <span>先批量判断哪些 API 值得接入 AI，再进入右侧评审</span>
    </div>
    <select data-test="import-domain-filter" v-model="importDomainFilter" class="filter-select">
      <option value="all">全部领域</option>
      <option v-for="domain in importBatch.domains" :key="domain" :value="domain">{{ domain }}</option>
    </select>
  </div>
  <div class="import-batch-stats">
    <div data-test="import-batch-stat-total"><span>导入接口</span><strong>{{ importBatch.stats.total }}</strong></div>
    <div><span>读取候选</span><strong>{{ importBatch.stats.read }}</strong></div>
    <div><span>写入/风险</span><strong>{{ importBatch.stats.write }}</strong></div>
    <div><span>需补映射</span><strong>{{ importBatch.stats.needsMapping }}</strong></div>
    <div data-test="import-batch-stat-selected"><span>保留</span><strong>{{ importBatch.stats.selected }}</strong></div>
    <div data-test="import-batch-stat-ignored"><span>忽略</span><strong>{{ importBatch.stats.ignored }}</strong></div>
  </div>
  <div v-if="visibleImportBatchItems.length === 0" class="empty">当前领域没有导入项</div>
  <div v-else class="import-batch-list">
    <article v-for="item in visibleImportBatchItems" :key="item.name" class="import-batch-row" :class="{ ignored: item.ignored }">
      <div class="method-pill">{{ item.method }}</div>
      <div class="import-batch-main">
        <button class="link-button" :data-test="`open-import-${item.name}`" @click="openImportedCapability(item.name)">
          {{ item.name }}
        </button>
        <small>{{ item.domain }} / {{ item.operation }} / {{ item.path }}</small>
      </div>
      <div class="verdict-cell">
        <strong>{{ item.verdictLabel }}</strong>
        <small>{{ item.reason }}</small>
      </div>
      <label class="keep-toggle">
        <input
          :data-test="`ignore-import-${item.name}`"
          type="checkbox"
          :checked="item.ignored"
          @change="toggleImportIgnored(item.name, ($event.target as HTMLInputElement).checked)"
        />
        忽略
      </label>
    </article>
  </div>
</section>
```

- [ ] **Step 2: Add compact import batch styles**

Append these styles near the import-strip styles in `apps/capability-console/src/styles.css`:

```css
.import-batch-panel {
  background: #ffffff;
  border: 1px solid #cbd6da;
  border-left: 4px solid #2563a8;
  border-radius: 8px;
  margin: 0 auto 16px;
  max-width: 1440px;
  padding: 14px;
}

.import-batch-panel .section-heading {
  margin-bottom: 10px;
}

.import-batch-stats {
  display: grid;
  gap: 0;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  margin-bottom: 10px;
  overflow: hidden;
}

.import-batch-stats div {
  background: #f6f9fa;
  border: 1px solid #dbe5e8;
  display: grid;
  gap: 3px;
  min-height: 54px;
  padding: 9px 10px;
}

.import-batch-stats div + div {
  border-left: 0;
}

.import-batch-stats span {
  color: #5c6f75;
  font-size: 11px;
  font-weight: 800;
}

.import-batch-stats strong {
  color: #162b35;
  font-size: 22px;
  line-height: 1;
}

.import-batch-list {
  display: grid;
  gap: 8px;
}

.import-batch-row {
  align-items: center;
  background: #f9fbfb;
  border: 1px solid #d9e4e7;
  border-radius: 8px;
  display: grid;
  gap: 10px;
  grid-template-columns: 64px minmax(180px, 1fr) minmax(140px, 0.5fr) 72px;
  padding: 10px;
}

.import-batch-row.ignored {
  opacity: 0.62;
}

.method-pill {
  background: #e8f1f7;
  border: 1px solid #c7dce8;
  border-radius: 6px;
  color: #255c7a;
  font-size: 12px;
  font-weight: 900;
  padding: 6px 8px;
  text-align: center;
}

.import-batch-main {
  min-width: 0;
}

.import-batch-main small,
.verdict-cell small {
  color: #65777d;
  display: block;
  font-size: 12px;
  margin-top: 3px;
  word-break: break-word;
}

.verdict-cell strong {
  color: #205d58;
  font-size: 13px;
}

.keep-toggle {
  align-items: center;
  color: #344a48;
  display: flex;
  font-size: 12px;
  font-weight: 800;
  gap: 6px;
  justify-content: flex-end;
}
```

Add responsive rules inside the existing `@media (max-width: 980px)` block:

```css
.import-batch-stats,
.import-batch-row {
  grid-template-columns: 1fr;
}

.import-batch-stats div + div {
  border-left: 1px solid #dbe5e8;
}

.keep-toggle {
  justify-content: flex-start;
}
```

- [ ] **Step 3: Run app tests**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts
```

Expected: PASS.

- [ ] **Step 4: Run full frontend checks**

Run:

```bash
cd apps/capability-console
npm test
npm run build
```

Expected: PASS for all Vue tests and production build.

- [ ] **Step 5: Commit Task 2 and Task 3 together**

```bash
git add apps/capability-console/src/App.vue apps/capability-console/src/App.test.ts apps/capability-console/src/styles.css
git commit -m "feat: add swagger import workbench"
```

---

### Task 4: End-to-End Regression and Documentation Note

**Files:**
- Modify: `examples/README.md`

**Interfaces:**
- Consumes: Existing demo commands from `examples/README.md`
- Produces: Updated demo instructions that mention the import batch workbench.

- [ ] **Step 1: Add a failing documentation expectation**

Run:

```bash
rg -n "本次导入|import batch|Swagger import workbench" examples/README.md
```

Expected: no matches.

- [ ] **Step 2: Update demo documentation**

In `examples/README.md`, under the Swagger import test section, add:

```md
After importing from `http://127.0.0.1:19090/openapi.json`, the Capability
Console shows a `本次导入` workbench. Use it to filter imported operations by
domain, inspect the publishability verdict, ignore operations that should not
enter the current review pass, and open kept operations in the review panel.
```

- [ ] **Step 3: Verify documentation text exists**

Run:

```bash
rg -n "本次导入|publishability verdict" examples/README.md
```

Expected: both phrases are found.

- [ ] **Step 4: Run full project checks**

Run:

```bash
cd apps/capability-console
npm test
npm run build
cd ../..
go test -count=1 ./...
go vet ./...
git diff --check
```

Expected: all commands pass.

- [ ] **Step 5: Commit documentation**

```bash
git add examples/README.md
git commit -m "docs: describe swagger import workbench demo"
```

---

## Self-Review

### Spec Coverage

- Import source panel: Task 3 renders the batch panel immediately after URL import and preserves the existing import source action.
- Import batch panel: Task 1 models batch items and stats; Task 3 renders metrics, domain filter, verdicts, keep/ignore, and open-in-review.
- Capability queue: Task 2 keeps using `upsert` so kept imports remain in the normal queue.
- Review panel: Task 2 adds open-in-review behavior; existing detail panel remains the editor.
- Classification rules: Task 1 implements read/write, duplicate, missing output mapping, and draft-ready verdicts.
- Data flow: Task 2 continues to use the current import endpoint and builds a local batch from returned capabilities.
- Error handling: existing import errors remain in `error`; empty batch shows a visible empty state in Task 3.
- Testing: Tasks 1-4 include unit, component, build, Go, vet, and whitespace checks.

### Red-Flag Term Scan

The plan contains no incomplete-work markers. Every task has concrete files, selectors, function names, commands, and expected outcomes.

### Type Consistency

`ImportBatch`, `ImportBatchItem`, `ImportVerdict`, `createImportBatch`, `setImportItemIgnored`, and `filterImportBatchItems` are defined in Task 1 and consumed by Tasks 2-3 with matching names.
