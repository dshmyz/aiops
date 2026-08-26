import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ConversationSidebar from './ConversationSidebar.vue';
import type { ConversationSummary } from '../types';

describe('ConversationSidebar', () => {
  const conversations: ConversationSummary[] = [
    {
      id: 'conv-1',
      subject: 'viewer-1',
      title: 'minio 容量查询',
      last_message_preview: '检查 minio archive bucket 容量',
      created_at: '2026-07-27T10:00:00Z',
      last_active_at: '2026-07-27T10:05:00Z',
      archived_at: null,
    },
    {
      id: 'conv-2',
      subject: 'viewer-1',
      title: 'kafka 消费延迟',
      last_message_preview: '查看 kafka orders 消费延迟',
      created_at: '2026-07-27T09:00:00Z',
      last_active_at: '2026-07-27T09:30:00Z',
      archived_at: null,
    },
  ];

  const baseProps = {
    conversations: [] as ConversationSummary[],
    activeConversationID: null as string | null,
    loading: false,
    searchQuery: '',
    archivedView: 'active' as const,
  };

  test('renders new conversation button', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    expect(wrapper.find('[data-test="conversation-new"]').exists()).toBe(true);
  });

  test('emits new event when new conversation button is clicked', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    await wrapper.find('[data-test="conversation-new"]').trigger('click');

    expect(wrapper.emitted('new')).toBeTruthy();
    expect(wrapper.emitted('new')?.[0]).toEqual([]);
  });

  test('renders conversation items', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations },
    });

    const items = wrapper.findAll('[data-test="conversation-item"]');
    expect(items).toHaveLength(2);
    expect(items[0].text()).toContain('minio 容量查询');
    expect(items[1].text()).toContain('kafka 消费延迟');
  });

  test('renders empty hint when conversations list is empty', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    expect(wrapper.find('[data-test="conversation-sidebar-empty"]').exists()).toBe(true);
  });

  test('highlights active conversation', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations, activeConversationID: 'conv-1' },
    });

    const activeItem = wrapper.find('[data-test="conversation-item"].active');
    expect(activeItem.exists()).toBe(true);
    expect(activeItem.attributes('data-conversation-id')).toBe('conv-1');
  });

  test('emits select event when conversation item is clicked', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations },
    });

    await wrapper.find('[data-test="conversation-item"]').trigger('click');

    expect(wrapper.emitted('select')).toBeTruthy();
    expect(wrapper.emitted('select')?.[0]).toEqual(['conv-1']);
  });

  test('emits archive event when archive button is clicked', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations },
    });

    await wrapper.find('[data-test="conversation-archive"]').trigger('click');

    expect(wrapper.emitted('archive')).toBeTruthy();
    expect(wrapper.emitted('archive')?.[0]).toEqual(['conv-1']);
  });

  test('renders loading indicator when loading is true', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, loading: true },
    });

    expect(wrapper.find('[data-test="conversation-sidebar-loading"]').exists()).toBe(true);
  });

  test('does not render loading indicator when loading is false', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations },
    });

    expect(wrapper.find('[data-test="conversation-sidebar-loading"]').exists()).toBe(false);
  });

  test('renders search input', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    expect(wrapper.find('[data-test="conversation-search"]').exists()).toBe(true);
  });

  test('emits update:searchQuery when typing in search input', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    await wrapper.find('[data-test="conversation-search"]').setValue('minio');

    expect(wrapper.emitted('update:searchQuery')).toBeTruthy();
    expect(wrapper.emitted('update:searchQuery')?.[0]).toEqual(['minio']);
  });

  test('renders active and archived tabs', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    expect(wrapper.find('[data-test="conversation-tab-active"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="conversation-tab-archived"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="conversation-tab-active"]').classes()).toContain('active');
  });

  test('emits update:archivedView when archived tab is clicked', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    await wrapper.find('[data-test="conversation-tab-archived"]').trigger('click');

    expect(wrapper.emitted('update:archivedView')).toBeTruthy();
    expect(wrapper.emitted('update:archivedView')?.[0]).toEqual(['archived']);
  });

  test('hides archive button in archived view', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations, archivedView: 'archived' },
    });

    expect(wrapper.find('[data-test="conversation-archive"]').exists()).toBe(false);
  });

  test('conversation items are keyboard focusable with listbox semantics', () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations, activeConversationID: 'conv-1' },
    });

    const list = wrapper.find('.conversation-list');
    expect(list.attributes('role')).toBe('listbox');

    const items = wrapper.findAll('[data-test="conversation-item"]');
    expect(items[0].attributes('tabindex')).toBe('0');
    expect(items[0].attributes('role')).toBe('option');
    expect(items[0].attributes('aria-selected')).toBe('true');
    expect(items[1].attributes('aria-selected')).toBe('false');
  });

  test('Enter on a focused conversation item selects it', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations },
    });

    await wrapper.findAll('[data-test="conversation-item"]')[1].trigger('keydown', { key: 'Enter' });
    expect(wrapper.emitted('select')?.[0]).toEqual(['conv-2']);

    // Space 也应触发选择
    await wrapper.findAll('[data-test="conversation-item"]')[0].trigger('keydown', { key: ' ' });
    expect(wrapper.emitted('select')?.[1]).toEqual(['conv-1']);
  });

  // 注意：focus() 只对已挂到文档中的元素生效，这两个方向键测试需 attachTo
  test('ArrowDown moves keyboard focus to next conversation (wraps around)', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations },
      attachTo: document.body,
    });
    const items = wrapper.findAll('[data-test="conversation-item"]');

    await items[0].trigger('keydown', { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[1].element);

    await items[1].trigger('keydown', { key: 'ArrowDown' }); // 末项后回绕到第一项
    expect(document.activeElement).toBe(items[0].element);
    wrapper.unmount();
  });

  test('ArrowUp moves keyboard focus to previous conversation', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps, conversations },
      attachTo: document.body,
    });
    const items = wrapper.findAll('[data-test="conversation-item"]');

    await items[1].trigger('keydown', { key: 'ArrowUp' });
    expect(document.activeElement).toBe(items[0].element);
    wrapper.unmount();
  });

  test('tab arrow keys switch between active and archived views', async () => {
    const wrapper = mount(ConversationSidebar, {
      props: { ...baseProps },
    });

    const tablist = wrapper.find('[role="tablist"]');
    await tablist.trigger('keydown', { key: 'ArrowRight' });
    expect(wrapper.emitted('update:archivedView')?.[0]).toEqual(['archived']);

    wrapper.setProps({ archivedView: 'archived' });
    await wrapper.vm.$nextTick();
    await tablist.trigger('keydown', { key: 'ArrowLeft' });
    expect(wrapper.emitted('update:archivedView')?.[1]).toEqual(['active']);
  });
});
