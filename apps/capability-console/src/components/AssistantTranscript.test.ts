import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import AssistantTranscript from './AssistantTranscript.vue';
import type { ConversationTurn } from '../types';

describe('AssistantTranscript', () => {
  const turns: ConversationTurn[] = [
    {
      id: 'turn-1',
      conversation_id: 'conv-1',
      role: 'user',
      content: '查看 集群状态',
      created_at: '2026-07-27T10:00:00Z',
    },
    {
      id: 'turn-2',
      conversation_id: 'conv-1',
      role: 'assistant',
      content: 'Volume data is healthy',
      response_type: 'answer',
      created_at: '2026-07-27T10:00:01Z',
    },
  ];

  test('renders empty hint when turns list is empty', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns: [], loading: false },
    });

    expect(wrapper.find('[data-test="assistant-transcript"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-transcript-empty"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('开始对话');
  });

  test('renders empty state icon', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns: [], loading: false },
    });

    expect(wrapper.find('[data-test="assistant-empty-icon"]').exists()).toBe(true);
  });

  test('renders turn items for each turn', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false },
    });

    expect(wrapper.find('[data-test="assistant-transcript-empty"]').exists()).toBe(false);
    const items = wrapper.findAll('[data-test="conversation-turn-item"]');
    expect(items).toHaveLength(2);
    expect(items[0].find('[data-test="conversation-turn-content"]').text()).toBe('查看 集群状态');
    expect(items[1].find('[data-test="conversation-turn-content"]').text()).toBe('Volume data is healthy');
  });

  test('renders loading indicator when loading is true', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns: [], loading: true },
    });

    expect(wrapper.find('[data-test="assistant-transcript-loading"]').exists()).toBe(true);
  });

  test('renders loading avatar', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns: [], loading: true },
    });

    expect(wrapper.find('[data-test="assistant-loading-avatar"]').exists()).toBe(true);
  });

  test('does not render empty hint when loading is true', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns: [], loading: true },
    });

    expect(wrapper.find('[data-test="assistant-transcript-empty"]').exists()).toBe(false);
  });

  test('renders turns and loading indicator simultaneously when waiting for response', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: true },
    });

    expect(wrapper.findAll('[data-test="conversation-turn-item"]').length).toBe(2);
    expect(wrapper.find('[data-test="assistant-transcript-loading"]').exists()).toBe(true);
  });

  test('omits load-more button when hasMore is not set', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false },
    });

    expect(wrapper.find('[data-test="assistant-load-more"]').exists()).toBe(false);
  });

  test('omits load-more button when hasMore is false', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false, hasMore: false },
    });

    expect(wrapper.find('[data-test="assistant-load-more"]').exists()).toBe(false);
  });

  test('renders load-more button when hasMore is true', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false, hasMore: true },
    });

    const button = wrapper.find('[data-test="assistant-load-more"]');
    expect(button.exists()).toBe(true);
    expect(button.text()).toBe('加载更多历史');
    expect(button.attributes('disabled')).toBeUndefined();
  });

  test('disables load-more button and shows loading text when loadingMore is true', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false, hasMore: true, loadingMore: true },
    });

    const button = wrapper.find('[data-test="assistant-load-more"]');
    expect(button.exists()).toBe(true);
    expect(button.text()).toBe('加载中...');
    expect(button.attributes('disabled')).toBeDefined();
  });

  test('emits load-more event when the load-more button is clicked', async () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false, hasMore: true },
    });

    await wrapper.find('[data-test="assistant-load-more"]').trigger('click');

    expect(wrapper.emitted('load-more')).toBeTruthy();
    expect(wrapper.emitted('load-more')?.length).toBe(1);
  });

  test('does not emit load-more when the button is disabled', async () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false, hasMore: true, loadingMore: true },
    });

    await wrapper.find('[data-test="assistant-load-more"]').trigger('click');

    expect(wrapper.emitted('load-more')).toBeFalsy();
  });

  test('forwards copy event from a turn item', async () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false },
    });

    await wrapper.findAll('[data-test="conversation-turn-copy"]')[0].trigger('click');

    expect(wrapper.emitted('copy')).toBeTruthy();
    expect(wrapper.emitted('copy')?.[0]).toEqual([turns[0].content]);
  });

  test('forwards regenerate event from the last assistant turn item', async () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false },
    });

    await wrapper.find('[data-test="conversation-turn-regenerate"]').trigger('click');

    expect(wrapper.emitted('regenerate')).toBeTruthy();
    expect(wrapper.emitted('regenerate')?.[0]).toEqual([turns[turns.length - 1]]);
  });

  test('inserts date divider when turns span different days', () => {
    const crossDayTurns: ConversationTurn[] = [
      {
        id: 'turn-day1-1',
        conversation_id: 'conv-1',
        role: 'user',
        content: '昨天的问题',
        created_at: '2026-07-26T10:00:00Z',
      },
      {
        id: 'turn-day2-1',
        conversation_id: 'conv-1',
        role: 'user',
        content: '今天的问题',
        created_at: '2026-07-27T10:00:00Z',
      },
    ];
    const wrapper = mount(AssistantTranscript, {
      props: { turns: crossDayTurns, loading: false },
    });

    const dividers = wrapper.findAll('[data-test="conversation-date-divider"]');
    expect(dividers.length).toBe(2);
  });

  test('does not insert date divider when all turns are on the same day', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: false },
    });

    const dividers = wrapper.findAll('[data-test="conversation-date-divider"]');
    expect(dividers.length).toBe(1);
  });

  test('hides standalone typing row when streaming turn is the last turn', () => {
    const streamingTurns: ConversationTurn[] = [
      ...turns,
      {
        id: 'local-assistant-streaming',
        conversation_id: 'conv-1',
        role: 'assistant',
        content: '',
        created_at: '2026-07-27T10:00:02Z',
      },
    ];
    const wrapper = mount(AssistantTranscript, {
      props: { turns: streamingTurns, loading: true },
    });

    // 流式 turn 存在时不渲染独立 typing 行
    expect(wrapper.find('[data-test="assistant-transcript-loading"]').exists()).toBe(false);
  });

  test('shows standalone typing row when loading but no streaming turn', () => {
    const wrapper = mount(AssistantTranscript, {
      props: { turns, loading: true },
    });

    expect(wrapper.find('[data-test="assistant-transcript-loading"]').exists()).toBe(true);
  });

  test('passes streaming prop to the last streaming turn', () => {
    const streamingTurns: ConversationTurn[] = [
      ...turns,
      {
        id: 'local-assistant-streaming',
        conversation_id: 'conv-1',
        role: 'assistant',
        content: '部分内容',
        created_at: '2026-07-27T10:00:02Z',
      },
    ];
    const wrapper = mount(AssistantTranscript, {
      props: { turns: streamingTurns, loading: true },
    });

    const items = wrapper.findAllComponents({ name: 'ConversationTurnItem' });
    const lastItem = items[items.length - 1];
    expect(lastItem.props('streaming')).toBe(true);
    // 前面的 turn 不应是 streaming
    expect(items[0].props('streaming')).toBe(false);
  });

  test('forwards retry event from an error turn item', async () => {
    const errorTurns: ConversationTurn[] = [
      ...turns,
      {
        id: 'local-assistant-error',
        conversation_id: 'conv-1',
        role: 'assistant',
        content: 'AI 助手请求失败：timeout',
        created_at: '2026-07-27T10:00:02Z',
        error: true,
      },
    ];
    const wrapper = mount(AssistantTranscript, {
      props: { turns: errorTurns, loading: false },
    });

    await wrapper.find('[data-test="conversation-turn-retry"]').trigger('click');

    expect(wrapper.emitted('retry')).toBeTruthy();
    expect(wrapper.emitted('retry')?.length).toBe(1);
  });
});
