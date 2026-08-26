import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ConversationTurnItem from './ConversationTurnItem.vue';
import type { ConversationTurn } from '../types';

describe('ConversationTurnItem', () => {
  const baseTurn: ConversationTurn = {
    id: 'turn-1',
    conversation_id: 'conv-1',
    role: 'user',
    content: '查看 集群状态',
    created_at: '2026-07-27T10:00:00Z',
  };

  test('renders user turn with user label', () => {
    const wrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });

    expect(wrapper.find('[data-test="conversation-turn-item"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="conversation-turn-item"]').classes()).toContain('user');
    expect(wrapper.find('[data-test="conversation-turn-role"]').text()).toBe('你');
    expect(wrapper.find('[data-test="conversation-turn-content"]').text()).toBe('查看 集群状态');
    expect(wrapper.find('[data-test="conversation-turn-response-type"]').exists()).toBe(false);
  });

  test('renders assistant turn with assistant label', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-2',
      role: 'assistant',
      content: 'Volume data is healthy',
      response_type: 'answer',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="conversation-turn-item"]').classes()).toContain('assistant');
    expect(wrapper.find('[data-test="conversation-turn-role"]').text()).toBe('AI 助手');
    expect(wrapper.find('[data-test="conversation-turn-content"]').text()).toBe('Volume data is healthy');
  });

  test('renders avatar for each role', () => {
    const userWrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });
    expect(userWrapper.find('[data-test="conversation-turn-avatar"]').exists()).toBe(true);

    const assistantTurn: ConversationTurn = { ...baseTurn, role: 'assistant', content: 'hi' };
    const assistantWrapper = mount(ConversationTurnItem, { props: { turn: assistantTurn } });
    expect(assistantWrapper.find('[data-test="conversation-turn-avatar"]').exists()).toBe(true);
  });

  test('renders response_type badge with localized label and answer variant class', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-2',
      role: 'assistant',
      content: 'Volume data is healthy',
      response_type: 'answer',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    const badge = wrapper.find('[data-test="conversation-turn-response-type"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('答案');
    expect(badge.classes()).toContain('variant-answer');
  });

  test('renders clarification_needed badge with clarification variant class', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-3',
      role: 'assistant',
      content: '缺少 cluster 参数',
      response_type: 'clarification_needed',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    const badge = wrapper.find('[data-test="conversation-turn-response-type"]');
    expect(badge.text()).toBe('待补充参数');
    expect(badge.classes()).toContain('variant-clarification');
  });

  test('renders confirmation_required badge with confirmation variant class', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-4',
      role: 'assistant',
      content: '需要审批',
      response_type: 'confirmation_required',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    const badge = wrapper.find('[data-test="conversation-turn-response-type"]');
    expect(badge.text()).toBe('待审批');
    expect(badge.classes()).toContain('variant-confirmation');
  });

  test('renders execution_result badge with execution variant class', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-5',
      role: 'assistant',
      content: '执行完成',
      response_type: 'execution_result',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    const badge = wrapper.find('[data-test="conversation-turn-response-type"]');
    expect(badge.text()).toBe('执行结果');
    expect(badge.classes()).toContain('variant-execution');
  });

  test('renders unknown response_type with default variant class', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-6',
      role: 'assistant',
      content: '自定义响应',
      response_type: 'custom_kind',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    const badge = wrapper.find('[data-test="conversation-turn-response-type"]');
    expect(badge.text()).toBe('custom_kind');
    expect(badge.classes()).toContain('variant-default');
  });

  test('omits response_type badge when response_type is empty', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-7',
      role: 'assistant',
      content: '回答内容',
      response_type: '',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="conversation-turn-response-type"]').exists()).toBe(false);
  });

  test('renders timestamp with datetime attribute and title for absolute time', () => {
    const wrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });

    const time = wrapper.find('[data-test="conversation-turn-time"]');
    expect(time.exists()).toBe(true);
    expect(time.attributes('datetime')).toBe('2026-07-27T10:00:00Z');
    // title should contain the local-formatted absolute time (locale-dependent,
    // so we just check it is non-empty and includes the year).
    const title = time.attributes('title') ?? '';
    expect(title.length).toBeGreaterThan(0);
    expect(title).toContain('2026');
  });

  test('applies role class for styling', () => {
    const wrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });

    expect(wrapper.find('[data-test="conversation-turn-item"].user').exists()).toBe(true);
  });

  test('applies assistant class for assistant turns', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      role: 'assistant',
      content: '回答',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="conversation-turn-item"].assistant').exists()).toBe(true);
  });

  test('renders copy button for user and assistant turns', () => {
    const userWrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });
    expect(userWrapper.find('[data-test="conversation-turn-copy"]').exists()).toBe(true);

    const assistantTurn: ConversationTurn = { ...baseTurn, role: 'assistant', content: 'hi' };
    const assistantWrapper = mount(ConversationTurnItem, { props: { turn: assistantTurn } });
    expect(assistantWrapper.find('[data-test="conversation-turn-copy"]').exists()).toBe(true);
  });

  test('emits copy event with content when copy button is clicked', async () => {
    const wrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });

    await wrapper.find('[data-test="conversation-turn-copy"]').trigger('click');

    expect(wrapper.emitted('copy')).toBeTruthy();
    expect(wrapper.emitted('copy')?.[0]).toEqual([baseTurn.content]);
  });

  test('does not render regenerate button for user turns', () => {
    const wrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });

    expect(wrapper.find('[data-test="conversation-turn-regenerate"]').exists()).toBe(false);
  });

  test('does not render regenerate button for non-last assistant turns', () => {
    const turn: ConversationTurn = { ...baseTurn, role: 'assistant', content: 'hi' };
    const wrapper = mount(ConversationTurnItem, { props: { turn, isLast: false } });

    expect(wrapper.find('[data-test="conversation-turn-regenerate"]').exists()).toBe(false);
  });

  test('renders regenerate button for last assistant turn', () => {
    const turn: ConversationTurn = { ...baseTurn, role: 'assistant', content: 'hi' };
    const wrapper = mount(ConversationTurnItem, { props: { turn, isLast: true } });

    expect(wrapper.find('[data-test="conversation-turn-regenerate"]').exists()).toBe(true);
  });

  test('emits regenerate event with turn when regenerate button is clicked', async () => {
    const turn: ConversationTurn = { ...baseTurn, role: 'assistant', content: 'hi' };
    const wrapper = mount(ConversationTurnItem, { props: { turn, isLast: true } });

    await wrapper.find('[data-test="conversation-turn-regenerate"]').trigger('click');

    expect(wrapper.emitted('regenerate')).toBeTruthy();
    expect(wrapper.emitted('regenerate')?.[0]).toEqual([turn]);
  });

  test('renders event.query structured answer from response_payload', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-evt',
      role: 'assistant',
      content: '事件列表',
      response_type: 'answer',
      response_payload: {
        type: 'answer',
        tool: 'event.query',
        answer: { events: [{ id: 'evt-9', action: 'plan_confirmed' }], count: 1 },
      },
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="tool-answer-view"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="tool-answer-row"]').text()).toContain('evt-9');
  });

  test('renders blocks from response_payload for execution_result turns', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-rb',
      role: 'assistant',
      content: '执行结果',
      response_type: 'execution_result',
      response_payload: {
        type: 'execution_result',
        blocks: [{ type: 'risk_notice', title: '操作预演', content: '保留 72h' }],
      },
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="conversation-turn-blocks"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="block-risk_notice"]').exists()).toBe(true);
  });

  test('does not render blocks or tool answer for user turns', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      role: 'user',
      content: '查看事件',
      response_payload: {
        type: 'answer',
        tool: 'event.query',
        answer: { events: [{ id: 'evt-9' }], count: 1 },
      },
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="tool-answer-view"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="conversation-turn-blocks"]').exists()).toBe(false);
  });

  test('renders live agent-loop steps as an independent block', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-steps',
      role: 'assistant',
      content: 'prod 集群健康，无异常',
      response_type: 'answer',
      steps: [
        { tool: 'cluster.status.read', step_index: 0, status: 'done', summary: 'cluster.status.read：green' },
        { tool: 'kafka.cluster.health.read', step_index: 1, status: 'done', summary: 'kafka.cluster.health.read：healthy' },
      ],
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="assistant-steps"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-steps"]').text()).toContain('已执行 2 个步骤');
    expect(wrapper.find('[data-test="assistant-step-item-0"]').text()).toContain('cluster.status.read');
    expect(wrapper.find('[data-test="assistant-step-item-1"]').text()).toContain('kafka.cluster.health.read');
  });

  test('replays full progress stream (stage skeleton + tool sub-items) from persisted evidence', () => {
    // 方案 B 回放场景：刷新后 progress_stages 从 response_payload.process 水合、
    // steps 亦然；进度面板须据此重建"阶段骨架 + 工具子项"完整流，而非只剩骨架或
    // 只靠 tool_calls（主路径根本不产 tool_calls）。
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-replay-progress',
      role: 'assistant',
      content: 'prod 集群健康，无异常',
      response_type: 'answer',
      progress_stages: [
        { stage: 'planning', received_at: '2026-08-03T10:00:00Z' },
        { stage: 'tool_executing', received_at: '2026-08-03T10:00:01Z' },
        { stage: 'formatting', received_at: '2026-08-03T10:00:02Z' },
      ],
      steps: [
        { tool: 'cluster.status.read', step_index: 0, status: 'done', summary: 'cluster.status.read：green' },
        { tool: 'kafka.cluster.health.read', step_index: 1, status: 'done', summary: 'kafka.cluster.health.read：healthy' },
      ],
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn, streaming: false } });

    expect(wrapper.find('[data-test="progress-timeline"]').exists()).toBe(true);
    const labels = wrapper.findAll('.progress-item-label').map((n) => n.text());
    // 阶段骨架仍在
    expect(labels).toContain('识别并拆解任务');
    expect(labels).toContain('查询平台事实');
    expect(labels).toContain('整合输出');
    // 工具子项由 steps 重建（此前只从 tool_calls 取，主路径会缺）；工具原文在
    // detail 里保留（.read 后缀仅从人话化 action 文案剥除）
    expect(labels).toContain('查询 cluster.status');
    expect(labels).toContain('查询 kafka.cluster.health');
    const details = wrapper.findAll('.progress-item-detail').map((n) => n.text());
    expect(details).toContain('cluster.status.read');
    expect(details).toContain('kafka.cluster.health.read');
  });

  test('keeps tool sub-items hidden when only a stage skeleton exists without steps', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-skeleton-only',
      role: 'assistant',
      content: '正在分析',
      progress_stages: [{ stage: 'planning', received_at: '2026-08-03T10:00:00Z' }],
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn, streaming: true } });

    expect(wrapper.find('[data-test="progress-timeline"]').exists()).toBe(true);
    expect(wrapper.find('.progress-item-label').text()).toBe('正在识别并拆解任务');
  });

  test('renders a persisted tool_step turn as its own steps block, not a text bubble', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-toolstep',
      role: 'assistant',
      response_type: 'tool_step',
      content: 'cluster.status.read：green',
      response_payload: {
        type: 'tool_step',
        tool: 'cluster.status.read',
        step_index: 0,
        summary: 'cluster.status.read：green',
        input: { cluster: 'c1' },
        result: { status: 'green' },
      },
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    // 步骤区块渲染，且工具名来自 payload
    expect(wrapper.find('[data-test="assistant-steps"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-step-item-0"]').text()).toContain('cluster.status.read');
    // 不回放文字气泡（避免与步骤摘要重复）：气泡外壳整体不渲染，而非渲染一个空气泡
    expect(wrapper.find('[data-test="conversation-turn-content"]').exists()).toBe(false);
  });

  test('renders a denied persisted tool_step turn with error and failed status', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-toolstep-denied',
      role: 'assistant',
      response_type: 'tool_step',
      content: '工具执行失败：policy denied: action_not_allowed',
      response_payload: {
        type: 'tool_step',
        tool: 'minio.bucket.health.read',
        step_index: 0,
        status: 'failed',
        error: 'policy denied: action_not_allowed',
        summary: '工具执行失败：policy denied: action_not_allowed',
        input: { bucket: 'archive' },
      },
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    const item = wrapper.find('[data-test="assistant-step-item-0"]');
    expect(item.exists()).toBe(true);
    expect(item.classes()).toContain('status-failed');
    expect(item.text()).toContain('minio.bucket.health.read');
    // denied 归入 failed 展示，文案为"已拒绝"并显示原始错误
    expect(item.text()).toContain('已拒绝');
    expect(item.text()).toContain('policy denied: action_not_allowed');
  });

  test('user turn never renders steps block', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      role: 'user',
      content: '检查集群',
      steps: [{ tool: 'cluster.status.read', step_index: 0, status: 'done' }],
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="assistant-steps"]').exists()).toBe(false);
  });

  test('thinking auto-expands while streaming, stays expandable after', async () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-thinking',
      role: 'assistant',
      content: '分析中',
      thinking: '先查 lag 再查 topic…',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn, streaming: true } });

    const section = wrapper.find('[data-test="conversation-turn-thinking"]');
    expect(section.exists()).toBe(true);
    // 流式期间：thinking 自动展开，无需手动点击即可看到推理过程。
    // 用 aria-expanded 断言折叠状态：v-show 的 display:none 在 jsdom 下无法
    // 被 isVisible() 稳定识别，aria-expanded 是组件暴露的可靠状态信号。
    expect(wrapper.find('.thinking-toggle').attributes('aria-expanded')).toBe('true');
  });

  test('thinking stays collapsed for replayed turns (no streaming)', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-thinking-replay',
      role: 'assistant',
      content: '分析中',
      thinking: '先查 lag 再查 topic…',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn, streaming: false } });

    expect(wrapper.find('[data-test="conversation-turn-thinking"]').exists()).toBe(true);
    // 非流式回放：保持默认收起，不打扰阅读
    expect(wrapper.find('.thinking-toggle').attributes('aria-expanded')).toBe('false');
  });

  test('thinking respects manual toggle after stream ends', async () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      id: 'turn-thinking-manual',
      role: 'assistant',
      content: '分析完成',
      thinking: '先查 lag…',
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn, streaming: true } });
    // 流式时自动展开，随后用户手动收起，流式结束后保持收起
    expect(wrapper.find('.thinking-toggle').attributes('aria-expanded')).toBe('true');
    await wrapper.find('.thinking-toggle').trigger('click');
    expect(wrapper.find('.thinking-toggle').attributes('aria-expanded')).toBe('false');

    await wrapper.setProps({ streaming: false });
    expect(wrapper.find('.thinking-toggle').attributes('aria-expanded')).toBe('false');
  });
});
