import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import MarkdownContent from './MarkdownContent.vue';

describe('MarkdownContent', () => {
  it('renders bold markdown as <strong>', () => {
    const wrapper = mount(MarkdownContent, { props: { content: '这是 **加粗** 文本' } });
    expect(wrapper.find('strong').exists()).toBe(true);
    expect(wrapper.text()).toContain('加粗');
  });

  it('renders a fenced code block as <pre><code>', () => {
    const md = '```\nconsole.log("hello")\n```';
    const wrapper = mount(MarkdownContent, { props: { content: md } });
    const pre = wrapper.find('pre');
    expect(pre.exists()).toBe(true);
    expect(pre.find('code').exists()).toBe(true);
    expect(pre.text()).toContain('console.log');
  });

  it('renders an unordered list', () => {
    const md = '- 第一项\n- 第二项';
    const wrapper = mount(MarkdownContent, { props: { content: md } });
    const ul = wrapper.find('ul');
    expect(ul.exists()).toBe(true);
    expect(ul.findAll('li')).toHaveLength(2);
  });

  it('renders a markdown link as <a> with href', () => {
    const md = '[文档](https://example.com)';
    const wrapper = mount(MarkdownContent, { props: { content: md } });
    const a = wrapper.find('a');
    expect(a.exists()).toBe(true);
    expect(a.attributes('href')).toBe('https://example.com');
  });

  it('strips <script> tags to prevent XSS', () => {
    const md = '正常文本\n<script>alert("xss")</script>\n后续文本';
    const wrapper = mount(MarkdownContent, { props: { content: md } });
    expect(wrapper.html()).not.toContain('<script>');
    expect(wrapper.html()).not.toContain('alert');
  });

  it('renders plain text without parsing when raw prop is true', () => {
    const wrapper = mount(MarkdownContent, { props: { content: '**not bold**', raw: true } });
    expect(wrapper.find('strong').exists()).toBe(false);
    // 原始文本保留，但不被解析为 HTML
    expect(wrapper.text()).toContain('**not bold**');
  });

  it('renders empty container for empty content', () => {
    const wrapper = mount(MarkdownContent, { props: { content: '' } });
    expect(wrapper.find('.markdown-content').exists()).toBe(true);
  });
});
