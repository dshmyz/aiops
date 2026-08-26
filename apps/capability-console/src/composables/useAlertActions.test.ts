import { describe, expect, test, vi, beforeEach } from 'vitest';
import { useAlertActions } from './useAlertActions';

const ruleA = {
  name: 'kafka-high-lag',
  description: 'Kafka 消费滞后',
  enabled: true,
  alert_match: { severity: 'critical', domain: 'kafka' },
  tool_sequence: [{ tool: 'cluster.status.read', input: {} }],
};
const ruleB = {
  name: 'minio-down',
  description: 'MinIO 不可用',
  enabled: false,
  alert_match: { severity: 'critical', domain: 'minio' },
  tool_sequence: [{ tool: 'cluster.status.read', input: {} }],
};

function jsonResponse(body: unknown, ok = true) {
  return {
    ok,
    status: ok ? 200 : 500,
    headers: new Headers(),
    text: () => Promise.resolve(JSON.stringify(body)),
    json: () => Promise.resolve(body),
  };
}

describe('useAlertActions', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.stubGlobal('ElMessage', { success: vi.fn(), error: vi.fn() });
  });

  test('loads rules and derives filtered list by search', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ configured: true, rules: [ruleA, ruleB], count: 2 }));
    vi.stubGlobal('fetch', fetchMock);
    const state = useAlertActions();
    await state.load();

    expect(state.rules.value).toHaveLength(2);
    expect(state.filteredRules.value).toHaveLength(2);

    state.search.value = 'minio';
    expect(state.filteredRules.value).toHaveLength(1);
    expect(state.filteredRules.value[0].name).toBe('minio-down');

    state.search.value = 'kafka';
    expect(state.filteredRules.value).toHaveLength(1);
    expect(state.filteredRules.value[0].name).toBe('kafka-high-lag');
  });

  test('startCreate resets the form and enters new mode', () => {
    const state = useAlertActions();
    state.startCreate();
    expect(state.editing.value).toBe('new');
    expect(state.editForm.value.name).toBe('');
    expect(state.editForm.value.tool_sequence).toHaveLength(1);
    expect(state.editForm.value.enabled).toBe(true);
  });

  test('startEdit deep-copies the rule so editing does not mutate the list item', () => {
    const state = useAlertActions();
    state.startEdit(ruleA);
    expect(state.editing.value).toEqual(ruleA);
    // 修改 form 不应影响原对象
    state.editForm.value.name = 'changed';
    expect(ruleA.name).toBe('kafka-high-lag');
  });

  test('save posts the form and reloads', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ status: 'created', name: 'kafka-high-lag' })) // POST
      .mockResolvedValueOnce(jsonResponse({ configured: true, rules: [ruleA] })); // reload GET
    vi.stubGlobal('fetch', fetchMock);

    const state = useAlertActions();
    state.startCreate();
    state.editForm.value.name = 'kafka-high-lag';
    state.editForm.value.tool_sequence = [{ tool: 'cluster.status.read', input: {} }];
    await state.save();

    const postCall = fetchMock.mock.calls.find((c) => c[1]?.method === 'POST');
    expect(postCall).toBeTruthy();
    expect(JSON.parse(String(postCall![1]?.body))).toMatchObject({ name: 'kafka-high-lag' });
    expect(state.editing.value).toBeNull();
    expect(state.rules.value).toHaveLength(1);
  });

  test('save rejects empty name or empty tool sequence without fetching', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const state = useAlertActions();
    state.startCreate();
    await state.save();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  test('toggleEnabled PATCHes the opposite value and updates the rule in place', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ status: 'updated', name: 'kafka-high-lag', enabled: false })));

    const state = useAlertActions();
    state.rules.value = [{ ...ruleA, enabled: true }];
    const target = state.rules.value[0];
    await state.toggleEnabled(target);
    expect(target.enabled).toBe(false); // ruleA 初始 true → next=false
    const calls = vi.mocked(fetch).mock.calls;
    const patch = calls.find((c) => c[1]?.method === 'PATCH');
    expect(patch).toBeTruthy();
    expect(JSON.parse(String(patch![1]?.body))).toEqual({ enabled: false });
  });

  test('remove deletes the rule', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ status: 'deleted' })) // DELETE
      .mockResolvedValueOnce(jsonResponse({ configured: true, rules: [] })); // reload
    vi.stubGlobal('fetch', fetchMock);
    const state = useAlertActions();
    await state.remove('kafka-high-lag');
    const del = fetchMock.mock.calls.find((c) => c[1]?.method === 'DELETE');
    expect(del).toBeTruthy();
    expect(state.rules.value).toHaveLength(0);
  });
});
