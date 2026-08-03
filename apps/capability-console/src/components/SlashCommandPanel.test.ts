import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import SlashCommandPanel from './SlashCommandPanel.vue';
import type { SlashCommand } from './SlashCommandPanel.vue';

describe('SlashCommandPanel', () => {
  const commands: SlashCommand[] = [
    { name: '/minio', description: '查询 MinIO 存储状态' },
    { name: '/kafka', description: '查询 Kafka 消费延迟' },
    { name: '/glusterfs', description: '查询 GlusterFS 卷健康' },
  ];

  test('does not render when visible is false', () => {
    const wrapper = mount(SlashCommandPanel, {
      props: { commands, visible: false, selectedIndex: 0 },
    });

    expect(wrapper.find('[data-test="slash-panel"]').exists()).toBe(false);
  });

  test('renders command list when visible', () => {
    const wrapper = mount(SlashCommandPanel, {
      props: { commands, visible: true, selectedIndex: 0 },
    });

    const items = wrapper.findAll('[data-test="slash-command"]');
    expect(items).toHaveLength(3);
    expect(items[0].text()).toContain('/minio');
    expect(items[0].text()).toContain('查询 MinIO 存储状态');
  });

  test('renders command icons', () => {
    const wrapper = mount(SlashCommandPanel, {
      props: { commands, visible: true, selectedIndex: 0 },
    });

    const icons = wrapper.findAll('[data-test="slash-command-icon"]');
    expect(icons).toHaveLength(3);
  });

  test('highlights selected command', () => {
    const wrapper = mount(SlashCommandPanel, {
      props: { commands, visible: true, selectedIndex: 1 },
    });

    const items = wrapper.findAll('[data-test="slash-command"]');
    expect(items[0].classes()).not.toContain('selected');
    expect(items[1].classes()).toContain('selected');
  });

  test('emits select when a command is clicked', async () => {
    const wrapper = mount(SlashCommandPanel, {
      props: { commands, visible: true, selectedIndex: 0 },
    });

    await wrapper.findAll('[data-test="slash-command"]')[2].trigger('click');

    expect(wrapper.emitted('select')).toBeTruthy();
    expect(wrapper.emitted('select')?.[0]).toEqual([commands[2]]);
  });

  test('emits close when escape hint is clicked', async () => {
    const wrapper = mount(SlashCommandPanel, {
      props: { commands, visible: true, selectedIndex: 0 },
    });

    await wrapper.find('[data-test="slash-close"]').trigger('click');

    expect(wrapper.emitted('close')).toBeTruthy();
  });

  test('renders empty hint when commands list is empty', () => {
    const wrapper = mount(SlashCommandPanel, {
      props: { commands: [], visible: true, selectedIndex: 0 },
    });

    expect(wrapper.find('[data-test="slash-empty"]').exists()).toBe(true);
  });
});
