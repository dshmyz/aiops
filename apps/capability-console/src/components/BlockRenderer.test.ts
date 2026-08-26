import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import BlockRenderer from './BlockRenderer.vue';
import type { Block } from '../types';

describe('BlockRenderer', () => {
  test('renders empty when blocks is empty', () => {
    const wrapper = mount(BlockRenderer, {
      props: { blocks: [] },
    });

    expect(wrapper.find('[data-test="block-renderer"]').exists()).toBe(false);
  });

  test('renders incident_card block with title and content', () => {
    const blocks: Block[] = [
      {
        type: 'incident_card',
        title: 'Kafka 集群延迟告警',
        content: 'consumer_group 延迟超过阈值',
      },
    ];
    const wrapper = mount(BlockRenderer, {
      props: { blocks },
    });

    expect(wrapper.find('[data-test="block-renderer"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="block-incident_card"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('Kafka 集群延迟告警');
    expect(wrapper.text()).toContain('consumer_group 延迟超过阈值');
  });

  test('renders risk_notice block with risk level', () => {
    const blocks: Block[] = [
      {
        type: 'risk_notice',
        title: '风险提示',
        content: '此操作可能导致服务短暂不可用',
        payload: { risk_level: 'write', impact: '服务中断约 30 秒' },
      },
    ];
    const wrapper = mount(BlockRenderer, {
      props: { blocks },
    });

    expect(wrapper.find('[data-test="block-risk_notice"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('服务中断约 30 秒');
  });

  test('renders risk_notice block with dry-run preview and suggested strategy (借鉴-3)', () => {
    const blocks: Block[] = [
      {
        type: 'risk_notice',
        title: '操作预演 (Dry-Run)',
        content: '将把 topic orders 的消息保留时间设置为 72 小时。',
        payload: {
          affected_resources: ['topic:orders@prod'],
          commands: ['kafka-configs --alter --add-config retention.hours=72'],
          warnings: ['缩短保留时间可能导致历史消息被删除'],
          suggested_strategy: {
            timeout: 60_000_000_000, // 60s（纳秒）
            retry: 0,
            concurrency: 1,
            risk_level: 'medium',
          },
        },
      },
    ];
    const wrapper = mount(BlockRenderer, {
      props: { blocks },
    });

    expect(wrapper.find('[data-test="block-risk_notice"]').exists()).toBe(true);
    // 影响资源
    expect(wrapper.find('[data-test="risk-resources"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('topic:orders@prod');
    // 命令
    expect(wrapper.find('[data-test="risk-commands"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('kafka-configs --alter');
    // 警告
    expect(wrapper.find('[data-test="risk-warnings"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('缩短保留时间可能导致历史消息被删除');
    // 执行策略（借鉴-3）
    expect(wrapper.find('[data-test="risk-strategy"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('medium');
    expect(wrapper.text()).toContain('60s');
    expect(wrapper.text()).toContain('并发度');
  });

  test('renders multiple blocks in order', () => {
    const blocks: Block[] = [
      { type: 'incident_card', title: '告警摘要' },
      { type: 'evidence_timeline', title: '证据时间线' },
      { type: 'query_suggestion', content: 'logql query here' },
    ];
    const wrapper = mount(BlockRenderer, {
      props: { blocks },
    });

    const rendered = wrapper.findAll('[data-test^="block-"]:not([data-test="block-renderer"])');
    expect(rendered).toHaveLength(3);
    expect(rendered[0].attributes('data-test')).toBe('block-incident_card');
    expect(rendered[1].attributes('data-test')).toBe('block-evidence_timeline');
    expect(rendered[2].attributes('data-test')).toBe('block-query_suggestion');
  });

  test('renders query_suggestion with code block', () => {
    const blocks: Block[] = [
      {
        type: 'query_suggestion',
        title: '建议的 LogQL 查询',
        content: '{service="order-center", level="error"} |= "timeout"',
        payload: { language: 'logql', time_range: '15m' },
      },
    ];
    const wrapper = mount(BlockRenderer, {
      props: { blocks },
    });

    expect(wrapper.find('[data-test="block-query_suggestion"]').exists()).toBe(true);
    expect(wrapper.find('code').text()).toContain('order-center');
    expect(wrapper.text()).toContain('logql');
    expect(wrapper.text()).toContain('15m');
  });

  test('renders approval_form with form fields', () => {
    const blocks: Block[] = [
      {
        type: 'approval_form',
        title: '请确认执行参数',
        payload: {
          action_code: 'middleware.diagnose',
          fields: [
            { name: 'cluster', type: 'select', required: true, options: ['m1', 'm2'] },
            { name: 'service', type: 'text', required: true },
          ],
        },
      },
    ];
    const wrapper = mount(BlockRenderer, {
      props: { blocks },
    });

    expect(wrapper.find('[data-test="block-approval_form"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('cluster');
    expect(wrapper.text()).toContain('service');
    expect(wrapper.text()).toContain('m1');
  });

  test('renders evidence_timeline with events', () => {
    const blocks: Block[] = [
      {
        type: 'evidence_timeline',
        title: '证据时间线',
        payload: {
          events: [
            { time: '2026-08-01T10:00:00Z', type: 'alert', description: '告警触发' },
            { time: '2026-08-01T10:05:00Z', type: 'log', description: '错误日志激增' },
          ],
        },
      },
    ];
    const wrapper = mount(BlockRenderer, {
      props: { blocks },
    });

    expect(wrapper.find('[data-test="block-evidence_timeline"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('告警触发');
    expect(wrapper.text()).toContain('错误日志激增');
  });
});
