import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ConversationTurnItem from './ConversationTurnItem.vue';
import type { ConversationTurn } from '../types';

describe('ConversationTurnItem', () => {
  const baseTurn: ConversationTurn = {
    id: 'turn-1',
    conversation_id: 'conv-1',
    role: 'user',
    content: '查看 prod 集群状态',
    created_at: '2026-07-27T10:00:00Z',
  };

  test('renders user turn with user label', () => {
    const wrapper = mount(ConversationTurnItem, { props: { turn: baseTurn } });

    expect(wrapper.find('[data-test="conversation-turn-item"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="conversation-turn-item"]').classes()).toContain('user');
    expect(wrapper.find('[data-test="conversation-turn-role"]').text()).toBe('你');
    expect(wrapper.find('[data-test="conversation-turn-content"]').text()).toBe('查看 prod 集群状态');
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
        input: { environment: 'prod' },
        result: { status: 'green' },
      },
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    // 步骤区块渲染，且工具名来自 payload
    expect(wrapper.find('[data-test="assistant-steps"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-step-item-0"]').text()).toContain('cluster.status.read');
    // 不回放文字气泡（避免与步骤摘要重复）
    expect(wrapper.find('[data-test="conversation-turn-content"]').text()).not.toContain('cluster.status.read：green');
  });

  test('user turn never renders steps block', () => {
    const turn: ConversationTurn = {
      ...baseTurn,
      role: 'user',
      content: '检查 prod 集群',
      steps: [{ tool: 'cluster.status.read', step_index: 0, status: 'done' }],
    };

    const wrapper = mount(ConversationTurnItem, { props: { turn } });

    expect(wrapper.find('[data-test="assistant-steps"]').exists()).toBe(false);
  });
});
