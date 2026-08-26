import { describe, expect, test, vi, afterEach } from 'vitest';
import { streamAssistantMessage } from './useAssistantStream';
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
