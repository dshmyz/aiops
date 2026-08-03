import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import McpServerForm from './McpServerForm.vue';
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

describe('McpServerForm', () => {
  test('新建模式：字段为空，enabled 默认勾选', () => {
    const wrapper = mount(McpServerForm, { props: { server: null } });
    expect(wrapper.find('[data-test="mcp-server-form-name"]').text()).toContain('');
    const enabledCheckbox = wrapper.find('[data-test="mcp-server-form-enabled"]');
    expect((enabledCheckbox.element as HTMLInputElement).checked).toBe(true);
  });

  test('编辑模式：传入 server 时字段预填', () => {
    const server = makeServer();
    const wrapper = mount(McpServerForm, { props: { server } });

    const nameInput = wrapper.find('[data-test="mcp-server-form-name"]').element as HTMLInputElement;
    expect(nameInput.value).toBe('grafana');
    const commandInput = wrapper.find('[data-test="mcp-server-form-command"]').element as HTMLInputElement;
    expect(commandInput.value).toBe('mcp-grafana');
    const argsTextarea = wrapper.find('[data-test="mcp-server-form-args"]').element as HTMLTextAreaElement;
    expect(argsTextarea.value).toContain('--port');
    const envTextarea = wrapper.find('[data-test="mcp-server-form-env"]').element as HTMLTextAreaElement;
    expect(envTextarea.value).toContain('API_KEY');
  });

  test('name 和 command/url 至少一项为空时禁止提交', async () => {
    const wrapper = mount(McpServerForm, { props: { server: null } });
    const submit = wrapper.find('[data-test="mcp-server-form-submit"]');
    expect((submit.element as HTMLButtonElement).disabled).toBe(true);

    await wrapper.find('[data-test="mcp-server-form-name"]').setValue('grafana');
    // 仍无 command 和 url，应禁用
    expect((submit.element as HTMLButtonElement).disabled).toBe(true);

    await wrapper.find('[data-test="mcp-server-form-url"]').setValue('http://grafana/mcp');
    expect((submit.element as HTMLButtonElement).disabled).toBe(false);
  });

  test('args JSON 非法时禁止提交', async () => {
    const wrapper = mount(McpServerForm, { props: { server: null } });
    await wrapper.find('[data-test="mcp-server-form-name"]').setValue('grafana');
    await wrapper.find('[data-test="mcp-server-form-command"]').setValue('mcp-grafana');
    await wrapper.find('[data-test="mcp-server-form-args"]').setValue('not-json');
    const submit = wrapper.find('[data-test="mcp-server-form-submit"]');
    expect((submit.element as HTMLButtonElement).disabled).toBe(true);
  });

  test('env JSON 非法时禁止提交', async () => {
    const wrapper = mount(McpServerForm, { props: { server: null } });
    await wrapper.find('[data-test="mcp-server-form-name"]').setValue('grafana');
    await wrapper.find('[data-test="mcp-server-form-command"]').setValue('mcp-grafana');
    await wrapper.find('[data-test="mcp-server-form-env"]').setValue('{invalid}');
    const submit = wrapper.find('[data-test="mcp-server-form-submit"]');
    expect((submit.element as HTMLButtonElement).disabled).toBe(true);
  });

  test('提交 emit submit payload（含解析后的 args/env）', async () => {
    const wrapper = mount(McpServerForm, { props: { server: null } });
    await wrapper.find('[data-test="mcp-server-form-name"]').setValue('loki');
    await wrapper.find('[data-test="mcp-server-form-command"]').setValue('mcp-loki');
    await wrapper.find('[data-test="mcp-server-form-args"]').setValue('["--debug"]');
    await wrapper.find('[data-test="mcp-server-form-env"]').setValue('{"TOKEN":"abc"}');
    await wrapper.find('[data-test="mcp-server-form-enabled"]').setValue(false);

    await wrapper.find('[data-test="mcp-server-form-submit"]').trigger('click');

    const emitted = wrapper.emitted('submit');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual([
      {
        name: 'loki',
        command: 'mcp-loki',
        args: ['--debug'],
        env: { TOKEN: 'abc' },
        url: '',
        enabled: false,
      },
    ]);
  });

  test('取消按钮 emit cancel', async () => {
    const wrapper = mount(McpServerForm, { props: { server: null } });
    await wrapper.find('[data-test="mcp-server-form-cancel"]').trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });
});
