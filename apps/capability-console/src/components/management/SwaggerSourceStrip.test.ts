import { mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { describe, expect, test, vi } from 'vitest';
import SwaggerSourceStrip from './SwaggerSourceStrip.vue';

function mountStrip(props: Record<string, unknown> = {}) {
  return mount(SwaggerSourceStrip, {
    props: { url: '', baseUrl: '', loading: false, ...props },
    global: { plugins: [ElementPlus] },
  });
}

describe('SwaggerSourceStrip', () => {
  test('渲染 URL 和 Base URL 输入框', () => {
    const wrapper = mountStrip({ url: 'http://example/v3/api-docs', baseUrl: 'https://mw.example.com' });
    expect(wrapper.find('[data-test="openapi-url-input"]').element).toBeDefined();
    expect((wrapper.find('[data-test="openapi-url-input"]').element as HTMLInputElement).value).toBe('http://example/v3/api-docs');
    expect((wrapper.find('[data-test="backend-base-url-input"]').element as HTMLInputElement).value).toBe('https://mw.example.com');
  });

  test('输入 URL 时触发 update:url 和 clear-preview', async () => {
    const wrapper = mountStrip();
    await wrapper.find('[data-test="openapi-url-input"]').setValue('http://new-url');
    expect(wrapper.emitted('update:url')?.[0]).toEqual(['http://new-url']);
    expect(wrapper.emitted('clear-preview')).toBeTruthy();
  });

  test('输入 Base URL 时触发 update:baseUrl 和 clear-preview', async () => {
    const wrapper = mountStrip();
    await wrapper.find('[data-test="backend-base-url-input"]').setValue('https://new-mw');
    expect(wrapper.emitted('update:baseUrl')?.[0]).toEqual(['https://new-mw']);
    expect(wrapper.emitted('clear-preview')).toBeTruthy();
  });

  test('点击预览按钮触发 preview 事件', async () => {
    const wrapper = mountStrip();
    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    expect(wrapper.emitted('preview')).toBeTruthy();
  });

  test('点击载入内置示例按钮触发 load-example 事件', async () => {
    const wrapper = mountStrip();
    await wrapper.find('[data-test="load-builtin-example"]').trigger('click');
    expect(wrapper.emitted('load-example')).toBeTruthy();
  });

  test('loading 时载入内置示例按钮禁用', () => {
    const wrapper = mountStrip({ loading: true });
    expect((wrapper.find('[data-test="load-builtin-example"]').element as HTMLButtonElement).disabled).toBe(true);
  });

  test('loading 为 true 时预览按钮显示加载态', () => {
    const wrapper = mountStrip({ loading: true });
    const button = wrapper.find('[data-test="preview-openapi-url"]');
    expect(button.classes()).toContain('is-loading');
  });

  test('buttonText 控制按钮文字', () => {
    const wrapper = mountStrip({ buttonText: '重新预览' });
    expect(wrapper.find('[data-test="preview-openapi-url"]').text()).toBe('重新预览');
  });

  test('默认按钮文字为"预览 API"', () => {
    const wrapper = mountStrip();
    expect(wrapper.find('[data-test="preview-openapi-url"]').text()).toBe('预览 API');
  });
});
