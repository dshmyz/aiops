import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import McpServerList from './McpServerList.vue';
import type { MCPServer } from '../types';

function makeServer(overrides: Partial<MCPServer> = {}): MCPServer {
  return {
    id: 'srv-1',
    name: 'grafana',
    command: 'mcp-grafana',
    args: ['--port', '3000'],
    env: { API_KEY: 'xxx' },
    url: '',
    enabled: true,
    created_at: '2026-08-02T10:00:00Z',
    updated_at: '2026-08-02T10:00:00Z',
    ...overrides,
  };
}

describe('McpServerList', () => {
  test('渲染服务器列表（name / command 或 url / 启用状态）', () => {
    const servers = [
      makeServer(),
      makeServer({
        id: 'srv-2',
        name: 'loki',
        command: '',
        url: 'http://loki:3100/mcp',
        enabled: false,
      }),
    ];
    const wrapper = mount(McpServerList, { props: { servers } });

    expect(wrapper.find('[data-test="mcp-server-list"]').exists()).toBe(true);
    const rows = wrapper.findAll('[data-test="mcp-server-row"]');
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain('grafana');
    expect(rows[0].text()).toContain('mcp-grafana');
    expect(rows[1].text()).toContain('loki');
    expect(rows[1].text()).toContain('http://loki:3100/mcp');
  });

  test('空列表显示占位', () => {
    const wrapper = mount(McpServerList, { props: { servers: [] } });
    expect(wrapper.find('[data-test="mcp-server-empty"]').exists()).toBe(true);
  });

  test('enabled 开关：点击 emit toggle-enabled', async () => {
    const servers = [makeServer({ id: 'srv-1', enabled: true })];
    const wrapper = mount(McpServerList, { props: { servers } });

    const toggle = wrapper.find('[data-test="mcp-server-toggle"]');
    await toggle.setValue(false);

    const emitted = wrapper.emitted('toggle-enabled');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual(['srv-1', false]);
  });

  test('编辑按钮 emit edit', async () => {
    const servers = [makeServer()];
    const wrapper = mount(McpServerList, { props: { servers } });

    await wrapper.find('[data-test="mcp-server-edit"]').trigger('click');
    const emitted = wrapper.emitted('edit');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual([servers[0]]);
  });

  test('删除按钮 emit delete', async () => {
    const servers = [makeServer({ id: 'srv-1' })];
    const wrapper = mount(McpServerList, { props: { servers } });

    await wrapper.find('[data-test="mcp-server-delete"]').trigger('click');
    const emitted = wrapper.emitted('delete');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual(['srv-1']);
  });

  test('连接方式优先显示 command，无 command 时显示 url', () => {
    const servers = [
      makeServer({ id: 's1', command: 'mcp-grafana', url: '' }),
      makeServer({ id: 's2', command: '', url: 'http://loki/mcp' }),
    ];
    const wrapper = mount(McpServerList, { props: { servers } });
    const rows = wrapper.findAll('[data-test="mcp-server-row"]');
    expect(rows[0].text()).toContain('mcp-grafana');
    expect(rows[1].text()).toContain('http://loki/mcp');
  });
});
