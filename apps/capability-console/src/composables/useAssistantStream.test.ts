import { describe, expect, test, vi, afterEach } from 'vitest';
import { streamAssistantMessage, friendlyStreamError, sseErrorMessage } from './useAssistantStream';
import type { AssistantStep } from '../types';

// SSE 事件流按 \n\n 分隔，逐事件喂给 reader。
function sseStream(...eventChunks: string[]): string {
  return eventChunks.join('\n\n') + '\n\n';
}

function mockFetchStream(body: string) {
  const encoder = new TextEncoder();
  return vi.stubGlobal('fetch', vi.fn(async () => {
    return {
      ok: true,
      body: new ReadableStream({
        start(controller) {
          controller.enqueue(encoder.encode(body));
          controller.close();
        },
      }),
    } as unknown as Response;
  }));
}

describe('useAssistantStream step 事件解析', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('解析 event: step 并回调 onStep，且不触发 onDelta', async () => {
    mockFetchStream(
      sseStream(
        'event: step\ndata: {"tool":"cluster.status.read","step_index":0,"status":"done","summary":"green","input":{"cluster":"c1"},"output":{"status":"green"}}',
      ),
    );

    const steps: AssistantStep[] = [];
    const deltas: string[] = [];
    await streamAssistantMessage({ message: 'hi' }, new AbortController().signal, {
      onDelta: (c) => deltas.push(c),
      onThinking: () => {},
      onToolCall: () => {},
      onStep: (s) => steps.push(s),
      onProgress: () => {},
      onFinal: () => {},
      onError: (m) => {
        throw new Error(`unexpected stream error: ${m}`);
      },
    });

    expect(steps).toHaveLength(1);
    expect(steps[0]).toMatchObject({
      tool: 'cluster.status.read',
      step_index: 0,
      status: 'done',
      summary: 'green',
    });
    expect(steps[0].input).toEqual({ cluster: 'c1' });
    expect(steps[0].output).toEqual({ status: 'green' });
    expect(deltas).toHaveLength(0);
  });

  test('过滤噪声事件（thinking/progress/done 不触发 onStep）', async () => {
    mockFetchStream(
      sseStream(
        'event: thinking\ndata: {"thinking":"planning..."}',
        'event: progress\ndata: {"stage":"tool_executing","detail":"cluster.status.read"}',
        'event: step\ndata: {"tool":"cluster.status.read","step_index":0,"status":"done"}',
        'event: done\ndata: {"done":true}',
      ),
    );

    const steps: AssistantStep[] = [];
    await streamAssistantMessage({ message: 'hi' }, new AbortController().signal, {
      onDelta: () => {},
      onThinking: () => {},
      onToolCall: () => {},
      onStep: (s) => steps.push(s),
      onProgress: () => {},
      onFinal: () => {},
      onError: () => {},
    });

    expect(steps).toHaveLength(1);
    expect(steps[0].tool).toBe('cluster.status.read');
  });
});

describe('friendlyStreamError 状态码映射', () => {
  test('429 映射为配额友好文案', () => {
    const r = friendlyStreamError(429, '{"error":"429 Too Many Requests: quota exhausted"}');
    expect(r.message).toContain('配额');
    expect(r.detail).toContain('quota');
  });

  test('500 映射为服务不可用', () => {
    const r = friendlyStreamError(502, '');
    expect(r.message).toContain('暂时不可用');
  });

  test('401/403 映射为权限提示', () => {
    expect(friendlyStreamError(401, '').message).toContain('权限');
    expect(friendlyStreamError(403, '').message).toContain('权限');
  });

  test('0/无状态映射为网络错误', () => {
    expect(friendlyStreamError(0, '').message).toContain('网络');
  });

  test('其他状态码且后端有可读 error 时透出原文', () => {
    const r = friendlyStreamError(400, '{"error":"消息不能为空"}');
    expect(r.message).toBe('消息不能为空');
  });

  test('其他状态码无 error 体时保留通用文案', () => {
    expect(friendlyStreamError(418, '').message).toBe('请求失败 (418)');
  });

  test('HTTP 429 响应经 streamAssistantMessage 触发友好文案', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false,
      status: 429,
      text: async () => '{"error":"429 quota exhausted"}',
    } as unknown as Response)));
    const errors: string[] = [];
    await streamAssistantMessage({ message: 'hi' }, new AbortController().signal, {
      onDelta: () => {},
      onThinking: () => {},
      onToolCall: () => {},
      onStep: () => {},
      onProgress: () => {},
      onFinal: () => {},
      onError: (m) => errors.push(m),
    });
    expect(errors).toHaveLength(1);
    expect(errors[0]).toContain('配额');
  });
});

describe('sseErrorMessage SSE 错误文案映射', () => {
  test('识别 quota/429 关键词', () => {
    expect(sseErrorMessage('LLM generate: 429 Too Many Requests')).toContain('配额');
    expect(sseErrorMessage('quota exhausted for project')).toContain('配额');
  });

  test('识别超时类关键词', () => {
    expect(sseErrorMessage('context deadline exceeded')).toContain('超时');
  });

  test('识别认证失败', () => {
    expect(sseErrorMessage('invalid api key provided')).toContain('认证');
  });

  test('普通错误原样透出', () => {
    expect(sseErrorMessage('上游返回了意外格式')).toBe('上游返回了意外格式');
    expect(sseErrorMessage('')).toBe('流式响应错误');
  });
});
