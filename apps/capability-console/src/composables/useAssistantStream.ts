import type { AssistantConsoleResponse, AssistantStep, ProgressStage } from '../types';

export interface ToolCallInfo {
  tool: string;
  input?: Record<string, unknown>;
  raw_response?: Record<string, unknown>;
  done: boolean;
}

export interface StreamCallbacks {
  onDelta: (chunk: string) => void;
  onThinking: (chunk: string) => void;
  onToolCall: (info: ToolCallInfo) => void;
  onStep: (step: AssistantStep) => void;
  onProgress: (stage: ProgressStage) => void;
  onFinal: (response: AssistantConsoleResponse) => void;
  onError: (message: string) => void;
}

export interface PageContext {
  domain?: string;
  environment?: string;
  resource_type?: string;
  resource_name?: string;
}

export interface StreamParams {
  message: string;
  conversationID?: string;
  environment?: string;
  pageContext?: PageContext;
}

/**
 * Stream an assistant message via SSE (POST /v1/assistant/stream).
 *
 * Uses fetch + ReadableStream instead of EventSource because EventSource
 * cannot send custom headers (Authorization) or POST bodies.
 *
 * Delta chunks are coalesced via requestAnimationFrame to avoid per-character
 * re-renders — the callback fires at most once per animation frame.
 */
export async function streamAssistantMessage(
  params: StreamParams,
  signal: AbortSignal,
  callbacks: StreamCallbacks,
): Promise<void> {
  const payload: Record<string, unknown> = { message: params.message };
  if (params.conversationID) {
    payload.conversation_id = params.conversationID;
  }
  // PageContext 是结构化页面上下文（缺口-3），优先于 legacy environment 字段。
  // 当 pageContext 非空时发送 page_context；否则回退到 environment 字段以保持兼容。
  if (params.pageContext && (params.pageContext.domain || params.pageContext.environment || params.pageContext.resource_type || params.pageContext.resource_name)) {
    payload.page_context = params.pageContext;
  } else if (params.environment && params.environment !== 'none') {
    payload.environment = params.environment;
  }

  const response = await fetch('/v1/assistant/stream', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal,
  });

  if (!response.ok) {
    const text = await response.text().catch(() => '');
    let msg = `请求失败 (${response.status})`;
    if (text) {
      try {
        const body = JSON.parse(text);
        if (body.error) msg = body.error;
      } catch {
        msg = text;
      }
    }
    callbacks.onError(msg);
    return;
  }

  if (!response.body) {
    callbacks.onError('浏览器不支持流式响应');
    return;
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  // Coalesce delta chunks via rAF so the UI updates at most once per frame.
  let pendingDelta = '';
  let pendingThinking = '';
  let rafScheduled = false;
  function flushDelta() {
    rafScheduled = false;
    if (pendingDelta) {
      callbacks.onDelta(pendingDelta);
      pendingDelta = '';
    }
    if (pendingThinking) {
      callbacks.onThinking(pendingThinking);
      pendingThinking = '';
    }
  }

  function scheduleFlush() {
    if (!rafScheduled) {
      rafScheduled = true;
      requestAnimationFrame(flushDelta);
    }
  }

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      // SSE events are separated by \n\n. Process complete events.
      let sepIndex: number;
      while ((sepIndex = buffer.indexOf('\n\n')) >= 0) {
        const rawEvent = buffer.slice(0, sepIndex);
        buffer = buffer.slice(sepIndex + 2);
        processSSEEvent(rawEvent, callbacks, (delta, thinking) => {
          if (delta) {
            pendingDelta += delta;
          }
          if (thinking) {
            pendingThinking += thinking;
          }
          scheduleFlush();
        });
      }
    }
    // Flush any remaining buffered delta before returning.
    flushDelta();
  } finally {
    reader.releaseLock();
  }
}

function processSSEEvent(
  rawEvent: string,
  callbacks: StreamCallbacks,
  appendDelta: (delta: string | undefined, thinking: string | undefined) => void,
) {
  let eventType = '';
  let dataLines: string[] = [];

  for (const line of rawEvent.split('\n')) {
    if (line.startsWith('event:')) {
      eventType = line.slice(6).trim();
    } else if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trim());
    }
  }

  const data = dataLines.join('');
  if (!data) return;

  try {
    const parsed = JSON.parse(data);

    if (eventType === 'error') {
      callbacks.onError(parsed.message || '流式响应错误');
      return;
    }

    if (eventType === 'response') {
      callbacks.onFinal(parsed as AssistantConsoleResponse);
      return;
    }

    if (eventType === 'done') {
      return;
    }

    if (eventType === 'thinking') {
      appendDelta(undefined, parsed.thinking);
      return;
    }

    if (eventType === 'tool_call') {
      callbacks.onToolCall(parsed as ToolCallInfo);
      return;
    }

    if (eventType === 'step') {
      // 后端 assistant.StepEvent: { tool, step_index, status, summary, input, output }.
      callbacks.onStep(parsed as AssistantStep);
      return;
    }

    if (eventType === 'progress') {
      // 后端 assistant.ProgressEvent: { stage, detail? }. 前端补 received_at
      // 用于时间线展示，避免依赖 SSE 抓包时间。
      const stage: ProgressStage = {
        stage: parsed.stage,
        detail: parsed.detail,
        received_at: new Date().toISOString(),
      };
      callbacks.onProgress(stage);
      return;
    }

    // Default: data event with delta.
    if (parsed.delta) {
      appendDelta(parsed.delta, undefined);
    }
  } catch {
    // Ignore malformed JSON lines — SSE is line-oriented and resilient.
  }
}
