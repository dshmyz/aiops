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
  resource_type?: string;
  resource_name?: string;
}

/**
 * Map HTTP status / backend error payloads to user-friendly messages.
 * Raw technical details stay available via the returned detail field
 * (shown in a collapsible "查看详情" section by the caller).
 */
export function friendlyStreamError(
  status: number,
  rawBody: string,
): { message: string; detail: string } {
  const detail = rawBody.trim().slice(0, 500);
  // 后端 JSON 错误体：{ error: "..." } —— 常见业务错误（如澄清失败）直接透出，
  // 但 429/5xx 这类基础设施错误仍映射为友好文案。
  let backendError = '';
  if (rawBody) {
    try {
      const parsed = JSON.parse(rawBody);
      if (parsed && typeof parsed.error === 'string') backendError = parsed.error;
    } catch {
      /* 非 JSON 体，忽略 */
    }
  }
  if (status === 429) {
    return {
      message: '模型调用配额已达上限，请稍后再试。若持续出现，请联系管理员调整配额或更换模型。',
      detail: backendError || detail,
    };
  }
  if (status === 401 || status === 403) {
    return {
      message: '没有访问 AI 助手的权限，请确认登录状态或联系管理员开通。',
      detail: backendError || detail,
    };
  }
  if (status === 404) {
    return {
      message: 'AI 助手服务未部署或地址已变更，请联系管理员检查服务配置。',
      detail: backendError || detail,
    };
  }
  if (status >= 500) {
    return {
      message: 'AI 服务暂时不可用，请稍后重试。',
      detail: backendError || detail,
    };
  }
  if (status === 0 || !status) {
    return {
      message: '网络连接失败，请检查网络后重试。',
      detail: backendError || detail,
    };
  }
  // 其他业务错误：后端有可读 error 就透出，否则给通用文案
  if (backendError) {
    return { message: backendError, detail };
  }
  return { message: `请求失败 (${status})`, detail };
}

export interface StreamParams {
  message: string;
  conversationID?: string;
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
  // PageContext 是结构化页面上下文，非空时发送 page_context。
  if (params.pageContext && (params.pageContext.domain || params.pageContext.resource_type || params.pageContext.resource_name)) {
    payload.page_context = params.pageContext;
  }

  const response = await fetch('/v1/assistant/stream', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal,
  });

  if (!response.ok) {
    const text = await response.text().catch(() => '');
    const friendly = friendlyStreamError(response.status, text);
    callbacks.onError(friendly.message);
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

/**
 * SSE error 事件文案映射：后端把 LLM 上游错误（429/quota/timeout 等）
 * 包装进 message 透传。按关键词识别常见类别给友好提示，其余原样透出。
 */
export function sseErrorMessage(raw: string): string {
  const text = raw || '流式响应错误';
  const lower = text.toLowerCase();
  if (lower.includes('429') || lower.includes('quota') || lower.includes('rate limit') || lower.includes('too many requests')) {
    return '模型调用配额已达上限，请稍后再试。若持续出现，请联系管理员调整配额或更换模型。';
  }
  if (lower.includes('timeout') || lower.includes('deadline') || lower.includes('canceled') || lower.includes('context canceled')) {
    return '模型响应超时，请稍后重试或换个简短的问题。';
  }
  if (lower.includes('unauthorized') || lower.includes('invalid api key') || lower.includes('authentication')) {
    return '模型服务认证失败，请检查 API Key 配置。';
  }
  return text;
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
      // SSE 流中途出错：后端 message 若是配额/限流类技术错误，映射为友好文案
      const raw = typeof parsed.message === 'string' ? parsed.message : '';
      callbacks.onError(sseErrorMessage(raw));
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
