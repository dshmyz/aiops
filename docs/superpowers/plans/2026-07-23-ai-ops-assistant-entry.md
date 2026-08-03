# AI Ops Assistant Entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a user-facing AI operations assistant entry to the Vue Capability Console while preserving the existing capability management workflow under an admin view.

**Architecture:** Keep a single Vue app and avoid Vue Router for this first version. Add local view state in `App.vue` for a two-entry product shell, reuse the existing `sendAssistantMessage` API helper for user chat, and keep all existing capability import/review/publish code inside the management view.

**Tech Stack:** Vue 3 `<script setup>`, TypeScript, Vitest, Vue Test Utils, Element Plus, existing `/v1/assistant/messages` and capability management APIs.

## Global Constraints

- Do not add Vue Router.
- Do not add a new backend endpoint.
- Do not add authentication or role-based menu hiding.
- Do not add persisted chat history.
- Do not add streaming responses.
- Do not add approval execution UI for write operations.
- Do not replace the existing React console.
- Do not move the Capability Console into many components.
- Opening `http://127.0.0.1:5174/` shows the AI assistant entry by default.
- Admins can switch to `能力接入管理` and keep using the existing Swagger import workbench.
- Existing capability import, test, publish, unpublish, and AI preflight behavior must keep working.
- Use TDD: every behavior change starts with a failing test.

---

## File Structure

- Modify `apps/capability-console/src/App.vue`
  - Adds product-shell view state.
  - Adds AI assistant entry state and handlers.
  - Wraps existing capability console content in the management view.
- Modify `apps/capability-console/src/App.test.ts`
  - Adds tests for default assistant entry, navigation, shortcuts, assistant send, answer rendering, and clarification rendering.
  - Updates existing management tests to switch into `能力接入管理` before asserting management-only UI.
- Modify `apps/capability-console/src/styles.css`
  - Adds app shell, nav rail, assistant layout, prompt shortcut, transcript, and detail panel styles.
  - Preserves existing management styles.
- Modify `examples/README.md`
  - Adds a short demo note that the Vue console now opens on the AI assistant entry and management lives under `能力接入管理`.

No backend files should be modified.

---

### Task 1: Product Shell Navigation

**Files:**
- Modify: `apps/capability-console/src/App.test.ts`
- Modify: `apps/capability-console/src/App.vue`
- Modify: `apps/capability-console/src/styles.css`

**Interfaces:**
- Produces: `type ActiveView = 'assistant' | 'management'`
- Produces: `const activeView = ref<ActiveView>('assistant')`
- Produces: shell nav buttons with selectors:
  - `data-test="nav-assistant"`
  - `data-test="nav-management"`
- Produces: assistant temporary section with `data-test="assistant-entry"`
- Produces: management section wrapper with `data-test="management-entry"`

- [ ] **Step 1: Write failing shell tests**

Modify `apps/capability-console/src/App.test.ts`.

Add a helper after `mountApp()`:

```ts
async function openManagement(wrapper: ReturnType<typeof mountApp>) {
  await wrapper.find('[data-test="nav-management"]').trigger('click');
  await flushPromises();
}
```

Add a new test near the top of the suite:

```ts
  test('opens on the AI assistant entry and keeps capability management behind navigation', async () => {
    const wrapper = mountApp();
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-entry"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-entry"]').text()).toContain('AI 运维助手');
    expect(wrapper.find('[data-test="management-entry"]').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('现有后台 API 接入 AI');

    await openManagement(wrapper);

    expect(wrapper.find('[data-test="assistant-entry"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="management-entry"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="management-entry"]').text()).toContain('现有后台 API 接入 AI');
    expect(wrapper.find('[data-test="swagger-url-import"]').exists()).toBe(true);
  });
```

Update existing management-only tests by adding `await openManagement(wrapper);` after their first `await flushPromises();` before they look for:

- `product-flow`
- `stat-published`
- `swagger-url-import`
- `new-draft`
- `capability-search`
- `edit-*`
- `publish-*`
- `test-input`
- `ai-preflight`
- `validate-capability`
- `test-capability`

For example:

```ts
    const wrapper = mountApp();
    await flushPromises();
    await openManagement(wrapper);
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts
```

Expected: FAIL because `nav-management`, `assistant-entry`, and `management-entry` do not exist.

- [ ] **Step 3: Add shell state and temporary assistant view**

Modify the script section in `apps/capability-console/src/App.vue`.

Add near the imports:

```ts
type ActiveView = 'assistant' | 'management';
```

Add near the existing refs:

```ts
const activeView = ref<ActiveView>('assistant');
```

Wrap the current template content in a product shell:

```vue
<template>
  <main class="capability-console app-shell">
    <aside class="app-nav" aria-label="产品入口">
      <div class="app-brand">
        <strong>AI 运维 Copilot</strong>
        <span>中间件智能运维</span>
      </div>
      <button
        data-test="nav-assistant"
        class="nav-item"
        :class="{ active: activeView === 'assistant' }"
        @click="activeView = 'assistant'"
      >
        AI 运维助手
      </button>
      <button
        data-test="nav-management"
        class="nav-item"
        :class="{ active: activeView === 'management' }"
        @click="activeView = 'management'"
      >
        能力接入管理
      </button>
    </aside>

    <section class="app-main">
      <section v-if="activeView === 'assistant'" data-test="assistant-entry" class="assistant-entry">
        <header class="assistant-hero">
          <p class="eyebrow">AI 运维助手</p>
          <h1>问 AI 排查中间件状态</h1>
          <p>用自然语言查询 MinIO、Kafka、GlusterFS，AI 会通过已发布能力调用现有后台 API。</p>
        </header>
      </section>

      <section v-else data-test="management-entry" class="management-entry">
        <!-- Move the existing header, alerts, product flow, import strip, import batch, and workspace here. -->
      </section>
    </section>
  </main>
</template>
```

Move the existing topbar, alert, product flow, summary strip, import strip, import batch, and workspace inside the `management-entry` section. Do not change their internals in this task.

- [ ] **Step 4: Add shell styles**

Add to `apps/capability-console/src/styles.css` near the top-level layout styles:

```css
.app-shell {
  display: grid;
  gap: 0;
  grid-template-columns: 220px minmax(0, 1fr);
  padding: 0;
}

.app-nav {
  background: #101820;
  color: #eef6f5;
  min-height: 100vh;
  padding: 20px 14px;
}

.app-brand {
  border-bottom: 1px solid rgba(238, 246, 245, 0.16);
  display: grid;
  gap: 4px;
  margin-bottom: 18px;
  padding-bottom: 16px;
}

.app-brand strong {
  font-size: 15px;
}

.app-brand span {
  color: #a8bcbd;
  font-size: 12px;
}

.nav-item {
  background: transparent;
  border: 1px solid transparent;
  border-radius: 8px;
  color: #c8d8d9;
  cursor: pointer;
  display: block;
  font-weight: 800;
  margin-bottom: 8px;
  padding: 10px 12px;
  text-align: left;
  width: 100%;
}

.nav-item.active {
  background: #1d6f68;
  border-color: #2c8f85;
  color: #ffffff;
}

.app-main {
  min-width: 0;
  padding: 24px;
}

.management-entry,
.assistant-entry {
  min-width: 0;
}

.assistant-hero {
  margin: 0 auto 16px;
  max-width: 1440px;
}

.assistant-hero p:last-child {
  color: #4e6268;
  font-size: 14px;
  line-height: 1.5;
  margin: 8px 0 0;
}
```

Update the existing `.capability-console` style so padding is controlled by `.app-main`:

```css
.capability-console {
  min-height: 100vh;
  background: #edf1f3;
}
```

Add inside the existing `@media (max-width: 980px)` block:

```css
  .app-shell {
    grid-template-columns: 1fr;
  }

  .app-nav {
    min-height: auto;
  }

  .app-main {
    padding: 14px;
  }
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts
```

Expected: PASS for the updated App tests.

- [ ] **Step 6: Run frontend build**

Run:

```bash
cd apps/capability-console
npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

```bash
git add apps/capability-console/src/App.vue apps/capability-console/src/App.test.ts apps/capability-console/src/styles.css
git commit -m "feat: add ai ops product shell"
```

---

### Task 2: Assistant Entry Chat Workflow

**Files:**
- Modify: `apps/capability-console/src/App.test.ts`
- Modify: `apps/capability-console/src/App.vue`
- Modify: `apps/capability-console/src/styles.css`

**Interfaces:**
- Consumes: existing `sendAssistantMessage(message: string): Promise<AssistantConsoleResponse>`
- Produces refs:
  - `assistantInput = ref('')`
  - `assistantMessages = ref<AssistantEntryMessage[]>([])`
  - `assistantLatestResponse = ref<AssistantConsoleResponse | null>(null)`
  - `assistantEntryLoading = ref(false)`
  - `assistantEntryError = ref('')`
- Produces helper type:
  - `interface AssistantEntryMessage { role: 'user' | 'assistant'; text: string; response?: AssistantConsoleResponse }`
- Produces functions:
  - `fillAssistantPrompt(prompt: string): void`
  - `sendAssistantEntryMessage(): Promise<void>`
  - `assistantResponseSummary(response: AssistantConsoleResponse): string`
  - `assistantDetailText(response: AssistantConsoleResponse | null): string`
- Produces selectors:
  - `data-test="assistant-input"`
  - `data-test="assistant-send"`
  - `data-test="assistant-shortcut-minio"`
  - `data-test="assistant-shortcut-kafka"`
  - `data-test="assistant-shortcut-glusterfs"`
  - `data-test="assistant-transcript"`
  - `data-test="assistant-latest-detail"`

- [ ] **Step 1: Write failing assistant entry tests**

Add these tests near the shell navigation test in `apps/capability-console/src/App.test.ts`:

```ts
  test('fills the assistant input from a scenario shortcut', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-shortcut-minio"]').trigger('click');

    expect((wrapper.find('[data-test="assistant-input"]').element as HTMLTextAreaElement).value).toBe('检查 prod minio archive bucket 容量');
  });

  test('sends a user question through the assistant entry and renders an answer', async () => {
    const fetchMock = vi.mocked(fetch);
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('检查 prod glusterfs data volume 状态');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    const assistantCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    expect(JSON.parse(String(assistantCall?.[1]?.body))).toEqual({
      message: '检查 prod glusterfs data volume 状态',
    });
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('检查 prod glusterfs data volume 状态');
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('Volume data is healthy');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('glusterfs.volume.health.read');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('Volume data is healthy');
  });

  test('renders assistant clarification in the user entry', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('查询 prod glusterfs');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('缺少参数: cluster, name');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('需要补充参数');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('缺少参数: cluster, name');
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts
```

Expected: FAIL because assistant input, shortcuts, send button, transcript, and latest detail panel do not exist.

- [ ] **Step 3: Add assistant entry state and handlers**

Modify the script in `apps/capability-console/src/App.vue`.

Add near `type ActiveView`:

```ts
interface AssistantEntryMessage {
  role: 'user' | 'assistant';
  text: string;
  response?: AssistantConsoleResponse;
}
```

Add refs near `activeView`:

```ts
const assistantInput = ref('');
const assistantMessages = ref<AssistantEntryMessage[]>([]);
const assistantLatestResponse = ref<AssistantConsoleResponse | null>(null);
const assistantEntryLoading = ref(false);
const assistantEntryError = ref('');
```

Add computed:

```ts
const assistantLatestDetailText = computed(() => assistantDetailText(assistantLatestResponse.value));
```

Add functions near the existing AI preflight functions:

```ts
function fillAssistantPrompt(prompt: string) {
  assistantInput.value = prompt;
}

async function sendAssistantEntryMessage() {
  const message = assistantInput.value.trim();
  if (!message || assistantEntryLoading.value) {
    return;
  }
  assistantEntryLoading.value = true;
  assistantEntryError.value = '';
  assistantMessages.value.push({ role: 'user', text: message });
  assistantInput.value = '';
  try {
    const response = await sendAssistantMessage(message);
    assistantLatestResponse.value = response;
    assistantMessages.value.push({
      role: 'assistant',
      text: assistantResponseSummary(response),
      response,
    });
  } catch (err) {
    assistantEntryError.value = err instanceof Error ? err.message : 'AI 助手请求失败';
  } finally {
    assistantEntryLoading.value = false;
  }
}

function assistantResponseSummary(response: AssistantConsoleResponse): string {
  if (response.type === 'answer') {
    return stringValue(response.answer.summary) || JSON.stringify(response.answer);
  }
  if (response.type === 'clarification_needed') {
    return response.message;
  }
  if (response.type === 'confirmation_required') {
    return response.summary || '需要审批';
  }
  if (response.type === 'execution_result') {
    return `执行状态：${response.status}`;
  }
  return JSON.stringify(response);
}

function assistantDetailText(response: AssistantConsoleResponse | null): string {
  if (!response) {
    return '暂无调用详情';
  }
  return JSON.stringify(response, null, 2);
}
```

- [ ] **Step 4: Add assistant entry template**

Replace the temporary assistant view from Task 1 with:

```vue
<section v-if="activeView === 'assistant'" data-test="assistant-entry" class="assistant-entry">
  <header class="assistant-hero">
    <p class="eyebrow">AI 运维助手</p>
    <h1>问 AI 排查中间件状态</h1>
    <p>用自然语言查询 MinIO、Kafka、GlusterFS，AI 会通过已发布能力调用现有后台 API。</p>
  </header>

  <section class="assistant-workspace">
    <aside class="assistant-shortcuts" aria-label="常用问题模板">
      <div class="group-title">
        <h2>常用问题</h2>
        <span>点击填入输入框</span>
      </div>
      <button data-test="assistant-shortcut-minio" class="shortcut-button" @click="fillAssistantPrompt('检查 prod minio archive bucket 容量')">
        检查 prod minio archive bucket 容量
      </button>
      <button data-test="assistant-shortcut-kafka" class="shortcut-button" @click="fillAssistantPrompt('查看 prod kafka orders 消费延迟')">
        查看 prod kafka orders 消费延迟
      </button>
      <button data-test="assistant-shortcut-glusterfs" class="shortcut-button" @click="fillAssistantPrompt('检查 prod glusterfs data volume 状态')">
        检查 prod glusterfs data volume 状态
      </button>
    </aside>

    <section class="assistant-chat" aria-label="AI 运维对话">
      <div data-test="assistant-transcript" class="assistant-transcript">
        <div v-if="assistantMessages.length === 0" class="empty">输入一个中间件问题，AI 会选择已发布能力执行查询。</div>
        <article v-for="(message, index) in assistantMessages" :key="index" class="assistant-message" :class="message.role">
          <strong>{{ message.role === 'user' ? '你' : 'AI 助手' }}</strong>
          <p>{{ message.text }}</p>
        </article>
      </div>
      <el-alert v-if="assistantEntryError" class="assistant-error" type="error" :title="assistantEntryError" show-icon />
      <label class="assistant-input-label">
        <span>自然语言请求</span>
        <textarea
          data-test="assistant-input"
          v-model="assistantInput"
          class="assistant-input"
          rows="4"
        />
      </label>
      <button data-test="assistant-send" class="primary-inline" :disabled="assistantEntryLoading || assistantInput.trim() === ''" @click="sendAssistantEntryMessage">
        {{ assistantEntryLoading ? '发送中' : '发送' }}
      </button>
    </section>

    <aside class="assistant-detail" aria-label="本次能力调用">
      <div class="group-title">
        <h2>本次调用</h2>
        <span>{{ assistantLatestResponse?.type === 'clarification_needed' ? '需要补充参数' : assistantLatestResponse?.type || '等待请求' }}</span>
      </div>
      <pre data-test="assistant-latest-detail">{{ assistantLatestDetailText }}</pre>
    </aside>
  </section>
</section>
```

- [ ] **Step 5: Add assistant entry styles**

Add to `apps/capability-console/src/styles.css` near the assistant shell styles:

```css
.assistant-workspace {
  display: grid;
  gap: 16px;
  grid-template-columns: minmax(220px, 0.7fr) minmax(420px, 1.25fr) minmax(280px, 0.85fr);
  margin: 0 auto;
  max-width: 1440px;
}

.assistant-shortcuts,
.assistant-chat,
.assistant-detail {
  background: rgba(255, 255, 255, 0.94);
  border: 1px solid #c9d8d6;
  border-radius: 8px;
  box-shadow: 0 10px 22px rgba(25, 47, 49, 0.08);
  min-width: 0;
  padding: 16px;
}

.shortcut-button {
  background: #f6f9fa;
  border: 1px solid #d9e4e7;
  border-radius: 8px;
  color: #1c3338;
  cursor: pointer;
  display: block;
  font-weight: 800;
  line-height: 1.45;
  margin-bottom: 8px;
  padding: 10px 12px;
  text-align: left;
  width: 100%;
}

.shortcut-button:hover {
  border-color: #1d6f68;
}

.assistant-transcript {
  background: #f8fafb;
  border: 1px solid #d8e2e5;
  border-radius: 8px;
  display: grid;
  gap: 10px;
  margin-bottom: 12px;
  min-height: 260px;
  padding: 12px;
}

.assistant-message {
  border-radius: 8px;
  display: grid;
  gap: 4px;
  padding: 10px 12px;
}

.assistant-message.user {
  background: #e8f1f7;
}

.assistant-message.assistant {
  background: #e9f3f2;
}

.assistant-message strong {
  color: #1f4d55;
  font-size: 12px;
}

.assistant-message p {
  margin: 0;
}

.assistant-error {
  margin-bottom: 10px;
}

.assistant-input-label {
  color: #344a48;
  display: grid;
  font-size: 12px;
  font-weight: 800;
  gap: 6px;
  margin-bottom: 10px;
}

.assistant-input {
  background: #fff;
  border: 1px solid #cbd8dc;
  border-radius: 8px;
  color: #1c2430;
  min-height: 92px;
  padding: 10px;
  resize: vertical;
}

.assistant-input:focus {
  border-color: #1f756f;
  outline: 2px solid rgba(31, 117, 111, 0.16);
}

.assistant-detail pre {
  max-height: 420px;
}
```

Add inside the existing `@media (max-width: 980px)` block:

```css
  .assistant-workspace {
    grid-template-columns: 1fr;
  }
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts
```

Expected: PASS.

- [ ] **Step 7: Run frontend checks**

Run:

```bash
cd apps/capability-console
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

```bash
git add apps/capability-console/src/App.vue apps/capability-console/src/App.test.ts apps/capability-console/src/styles.css
git commit -m "feat: add ai ops assistant entry"
```

---

### Task 3: Demo Documentation and Final Verification

**Files:**
- Modify: `examples/README.md`

**Interfaces:**
- Consumes: existing demo instructions in `examples/README.md`
- Produces: demo note that the default Vue view is `AI 运维助手` and Swagger import lives under `能力接入管理`.

- [ ] **Step 1: Add documentation expectation**

Run:

```bash
rg -n "AI 运维助手|能力接入管理" examples/README.md
```

Expected: If these phrases are absent from the Vue demo section, update the README in Step 2.

- [ ] **Step 2: Update demo README**

In `examples/README.md`, near the console startup/open instructions, add:

```md
The Vue console opens on `AI 运维助手` by default. Use this entry for natural
language middleware questions. Switch to `能力接入管理` when you need to import
Swagger, review generated Capability drafts, or publish reviewed read tools.
```

- [ ] **Step 3: Verify documentation text**

Run:

```bash
rg -n "AI 运维助手|能力接入管理" examples/README.md
```

Expected: both phrases are found in the Vue console demo section.

- [ ] **Step 4: Run final full checks**

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

- [ ] **Step 5: Commit Task 3**

```bash
git add examples/README.md
git commit -m "docs: describe ai ops assistant entry"
```

---

## Self-Review

### Spec Coverage

- Product shell: Task 1 adds the navigation rail and default assistant view.
- Default assistant entry: Task 1 sets `activeView` to `assistant`; Task 2 fills the assistant workspace.
- Management entry: Task 1 moves existing console content under `management-entry` and updates tests to navigate there.
- Assistant shortcuts: Task 2 adds MinIO, Kafka, and GlusterFS prompt buttons.
- Conversation workflow: Task 2 adds assistant input, send handler, transcript, latest response detail, and endpoint integration.
- Clarification rendering: Task 2 adds explicit `clarification_needed` transcript and detail expectations.
- Existing workflows: Task 1 updates existing tests to run under management; Task 2 and Task 3 rerun all Vue tests and build.
- Documentation: Task 3 updates demo instructions.

### Red-Flag Term Scan

No incomplete-work markers are intentionally present. Every task lists concrete files, selectors, functions, commands, and expected outcomes.

### Type Consistency

`ActiveView`, `AssistantEntryMessage`, `assistantInput`, `assistantMessages`, `assistantLatestResponse`, `assistantEntryLoading`, `assistantEntryError`, `assistantLatestDetailText`, `fillAssistantPrompt`, `sendAssistantEntryMessage`, `assistantResponseSummary`, and `assistantDetailText` are introduced in Tasks 1-2 and used with matching names in the template and tests.
