import { describe, expect, test, vi, beforeEach } from 'vitest';
import { useConversations } from './useConversations';
import type { ConversationTurn } from '../types';

// 流式期间仅内存中累积、后端持久化 turn 不含的瞬时字段：
// refreshTurns 后端回读时若直接硬替换，这些"输出过程中的东西"会消失。
// 本组测试锁定 refreshTurns 必须把瞬时过程内容合并回刷新后的持久化 turn。
const turn = (id: string, role: 'user' | 'assistant', content: string): ConversationTurn =>
  ({
    id,
    conversation_id: 'conv-1',
    role,
    content,
    response_type: role === 'assistant' ? 'chat' : undefined,
    created_at: `2026-08-16T00:00:0${id.length % 10}Z`,
  }) as ConversationTurn;

vi.mock('../api', () => ({
  getConversation: vi.fn(),
  listConversations: vi.fn(async () => ({ conversations: [] })),
  archiveConversation: vi.fn(async () => undefined),
}));

describe('useConversations refreshTurns 保留瞬时过程内容', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('刷新后 steps/tool_calls/progress_stages/thinking 合并回持久化 turn', async () => {
    const { getConversation } = await import('../api');
    const viGet = vi.mocked(getConversation);

    const { conversationTurns, refreshTurns } = useConversations();

    // 流式期间的实时列表：user 提问 + assistant 携带过程内容但内容尚未落库
    conversationTurns.value = [
      turn('u1', 'user', 'kafka 健康吗'),
      Object.assign(turn('last-assistant', 'assistant', 'Kafka 集群健康，3 节点在线'), {
        steps: [{ tool: 'cluster.status.read', step_index: 0, status: 'done', summary: 'green' }],
        tool_calls: [{ tool: 'cluster.status.read' }],
        progress_stages: [{ stage: 'tool_executing', detail: 'cluster.status.read', started_at: 't' }],
        thinking: '先查集群状态…',
      }),
    ];

    // 后端已持久化同一对话（assistant turn 换成真实 id，但仍以同一 user+assistant 对结尾）
    viGet.mockResolvedValueOnce({
      id: 'conv-1',
      subject: 't',
      title: 't',
      last_message_preview: '',
      created_at: '2026-08-16T00:00:00Z',
      last_active_at: '2026-08-16T00:00:00Z',
      turns: [
        turn('a-persisted-2', 'assistant', 'Kafka 集群健康，3 节点在线'),
        turn('u1', 'user', 'kafka 健康吗'),
      ],
      next_turn_cursor: null,
    });

    await refreshTurns('conv-1');

    expect(conversationTurns.value).toHaveLength(2);
    const [userTurn, assistantTurn] = conversationTurns.value;
    expect(userTurn.id).toBe('u1');
    expect(assistantTurn.id).toBe('a-persisted-2');
    expect(assistantTurn.content).toBe('Kafka 集群健康，3 节点在线');
    // 瞬时过程内容已合并回刷新后的 assistant turn
    expect(assistantTurn.steps).toHaveLength(1);
    expect(assistantTurn.tool_calls).toHaveLength(1);
    expect(assistantTurn.progress_stages).toHaveLength(1);
    expect(assistantTurn.thinking).toContain('先查集群状态');
  });

  test('无瞬时内容的普通刷新不受影响（不合并、不报错）', async () => {
    const { getConversation } = await import('../api');
    const viGet = vi.mocked(getConversation);

    const { conversationTurns, refreshTurns } = useConversations();
    conversationTurns.value = [turn('u1', 'user', 'hi')];

    viGet.mockResolvedValueOnce({
      id: 'conv-1',
      subject: 't',
      title: 't',
      last_message_preview: '',
      created_at: '2026-08-16T00:00:00Z',
      last_active_at: '2026-08-16T00:00:00Z',
      turns: [turn('a1', 'assistant', '你好'), turn('u1', 'user', 'hi')],
      next_turn_cursor: null,
    });

    await refreshTurns('conv-1');

    expect(conversationTurns.value).toHaveLength(2);
    expect(conversationTurns.value[0]).toMatchObject({ id: 'u1', role: 'user', content: 'hi' });
    expect(conversationTurns.value[1]).toMatchObject({ id: 'a1', role: 'assistant', content: '你好' });
  });

  test('刷新失败时保留现有列表（含瞬时内容），不抛错', async () => {
    const { getConversation } = await import('../api');
    const viGet = vi.mocked(getConversation);

    const { conversationTurns, refreshTurns } = useConversations();
    const live = Object.assign(turn('a-live', 'assistant', ''), { thinking: '进行中…' });
    conversationTurns.value = [turn('u1', 'user', 'hi'), live];

    viGet.mockRejectedValueOnce(new Error('network down'));

    await refreshTurns('conv-1');

    expect(conversationTurns.value).toHaveLength(2);
    expect(conversationTurns.value[1].thinking).toBe('进行中…');
  });

  test('刷新后从 response_payload.process 水合思考与步骤（后端已落库）', async () => {
    const { getConversation } = await import('../api');
    const viGet = vi.mocked(getConversation);

    const { conversationTurns, refreshTurns } = useConversations();

    // 页面刷新：内存瞬态已清空，只能靠后端持久化的过程证据（response_payload.process）
    // 复原生成时的思考与工具调用步骤。
    viGet.mockResolvedValueOnce({
      id: 'conv-1',
      subject: 't',
      title: 't',
      last_message_preview: '',
      created_at: '2026-08-16T00:00:00Z',
      last_active_at: '2026-08-16T00:00:00Z',
      turns: [
        Object.assign(turn('a-persisted-2', 'assistant', 'Kafka 集群健康，3 节点在线'), {
          response_payload: {
            type: 'answer',
            process: {
              thinking: '先查 lag 再查 topic…',
              steps: [
                { tool: 'kafka.consumer_lag.read', step_index: 0, status: 'done', summary: 'lag 12ms' },
                { tool: 'kafka.topic.read', step_index: 1, status: 'done', summary: 'topic ok' },
              ],
              progress_stages: [
                { stage: 'planning', received_at: '2026-08-03T10:00:00Z' },
                { stage: 'tool_executing', received_at: '2026-08-03T10:00:01Z' },
              ],
            },
          },
        }),
        turn('u1', 'user', 'kafka 健康吗'),
      ],
      next_turn_cursor: null,
    });

    await refreshTurns('conv-1');

    const assistantTurn = conversationTurns.value[1];
    expect(assistantTurn.thinking).toContain('先查 lag');
    expect(assistantTurn.steps).toHaveLength(2);
    expect(assistantTurn.steps?.[0]).toMatchObject({
      tool: 'kafka.consumer_lag.read',
      step_index: 0,
      status: 'done',
    });
    expect(assistantTurn.steps?.[1].summary).toContain('topic ok');
    // progress_stages 同样从持久化 process 水合，供回放重建进度面板相位骨架
    expect(assistantTurn.progress_stages).toHaveLength(2);
    expect(assistantTurn.progress_stages?.[0]).toMatchObject({ stage: 'planning' });
    expect(assistantTurn.progress_stages?.[1]).toMatchObject({ stage: 'tool_executing' });
    // response_payload 原样保留，水合只补瞬态字段、不动持久化数据
    expect(assistantTurn.response_payload).toMatchObject({ type: 'answer' });
  });

  test('无 process payload 的旧 turn 不被水合', async () => {
    const { getConversation } = await import('../api');
    const viGet = vi.mocked(getConversation);

    const { conversationTurns, refreshTurns } = useConversations();

    viGet.mockResolvedValueOnce({
      id: 'conv-1',
      subject: 't',
      title: 't',
      last_message_preview: '',
      created_at: '2026-08-16T00:00:00Z',
      last_active_at: '2026-08-16T00:00:00Z',
      turns: [
        // 修复前落库的 turn：response_payload 只有 answer，无 process。
        Object.assign(turn('a-old', 'assistant', '已执行完成'), {
          response_payload: { type: 'answer' },
        }),
        turn('u1', 'user', '查一下'),
      ],
      next_turn_cursor: null,
    });

    await refreshTurns('conv-1');

    const assistantTurn = conversationTurns.value[1];
    expect(assistantTurn.thinking).toBeUndefined();
    expect(assistantTurn.steps).toBeUndefined();
  });

  test('持久化的 error turn（response_type=error）回放时还原为错误气泡', async () => {
    const { getConversation } = await import('../api');
    const viGet = vi.mocked(getConversation);

    const { conversationTurns, refreshTurns } = useConversations();

    viGet.mockResolvedValueOnce({
      id: 'conv-1',
      subject: 't',
      title: 't',
      last_message_preview: '',
      created_at: '2026-08-16T00:00:00Z',
      last_active_at: '2026-08-16T00:00:00Z',
      turns: [
        // 后端持久化的失败 turn：response_type=error，content 是错误文案。
        Object.assign(turn('a-err', 'assistant', 'Get http://x: connection refused'), {
          response_type: 'error',
          response_payload: { type: 'error', message: 'Get http://x: connection refused' },
        }),
        turn('u1', 'user', '查一下'),
      ],
      next_turn_cursor: null,
    });

    await refreshTurns('conv-1');

    const [userTurn, errTurn] = conversationTurns.value;
    expect(userTurn.id).toBe('u1');
    expect(userTurn.error).toBeFalsy();
    expect(errTurn.id).toBe('a-err');
    expect(errTurn.error).toBe(true);
    expect(errTurn.content).toContain('connection refused');
  });
});