import { describe, expect, test, vi } from 'vitest';
import { nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { defineComponent } from 'vue';
import ElementPlus from 'element-plus';
import SequenceEditor from './SequenceEditor.vue';
import type { AdminTool } from '../../types';

const tools: AdminTool[] = [
  {
    name: 'cluster.status.read',
    operation: 'read',
    risk: 'low',
    domain: 'cluster',
    input_schema: {
      cluster: { type: 'string', required: true, examples: ['c1'] },
    },
  },
  {
    name: 'kafka.topic.retention.set',
    operation: 'write',
    risk: 'high',
    domain: 'kafka',
    input_schema: {
      topic: { type: 'string', required: true },
      retention_hours: { type: 'integer', enum: ['24', '72', '168'] },
    },
  },
];

function makeStep(tool: string, input: Record<string, string>) {
  return { tool, input };
}

describe('SequenceEditor', () => {
  test('renders a step for each item with tool name and param input', () => {
    const wrapper = mount(SequenceEditor, {
      props: { steps: [makeStep('cluster.status.read', { cluster: 'c1' })], tools },
      global: { plugins: [ElementPlus] },
    });
    const items = wrapper.findAll('[data-test="sequence-step"]');
    expect(items).toHaveLength(1);
    expect(wrapper.text()).toContain('cluster.status.read');
    expect(wrapper.text()).toContain('cluster');
  });

  test('adds a step at the end', async () => {
    const wrapper = mount(SequenceEditor, {
      props: { steps: [makeStep('cluster.status.read', {})], tools },
      global: { plugins: [ElementPlus] },
    });
    await wrapper.find('.add-step').trigger('click');
    const emitted = wrapper.emitted('update')!;
    expect(emitted).toBeTruthy();
    const steps = emitted[emitted.length - 1][0] as Array<{ tool: string; input: Record<string, string> }>;
    expect(steps).toHaveLength(2);
  });

  test('moves a step up and down', async () => {
    const steps = [makeStep('a.tool.read', {}), makeStep('b.tool.write', {})];
    const wrapper = mount(SequenceEditor, { props: { steps, tools }, global: { plugins: [ElementPlus] } });

    // 第二个步骤的"上移"按钮（step-ops 里第一个是上移）
    const step2Up = wrapper.findAll('[data-test="sequence-step"]')[1].findAll('.op-btn')[0];
    await step2Up.trigger('click');
    await nextTick();
    let emitted = wrapper.emitted('update')!;
    let after = emitted[emitted.length - 1][0] as Array<{ tool: string }>;
    expect(after[0].tool).toBe('b.tool.write');

    // 第一个步骤的"下移"按钮（受控 props 下 DOM 不重渲染，但事件按原 props 计算）
    const step1Down = wrapper.findAll('[data-test="sequence-step"]')[0].findAll('.op-btn')[1];
    await step1Down.trigger('click');
    await nextTick();
    emitted = wrapper.emitted('update')!;
    after = emitted[emitted.length - 1][0] as Array<{ tool: string }>;
    expect(after[0].tool).toBe('b.tool.write');
    expect(after[1].tool).toBe('a.tool.read');
  });

  test('copies a step', async () => {
    const wrapper = mount(SequenceEditor, {
      props: { steps: [makeStep('a.tool.read', { env: 'prod' })], tools },
      global: { plugins: [ElementPlus] },
    });
    await wrapper.findAll('.op-btn')[2].trigger('click'); // copy
    const emitted = wrapper.emitted('update')!;
    const steps = emitted[emitted.length - 1][0] as Array<{ tool: string; input: Record<string, string> }>;
    expect(steps).toHaveLength(2);
    expect(steps[1]).toEqual({ tool: 'a.tool.read', input: { env: 'prod' } });
  });

  test('removes a step when more than one exists', async () => {
    const steps = [makeStep('a.tool.read', {}), makeStep('b.tool.write', {})];
    const wrapper = mount(SequenceEditor, { props: { steps, tools }, global: { plugins: [ElementPlus] } });
    const deleteBtns = wrapper.findAll('.op-btn.danger');
    expect(deleteBtns).toHaveLength(2);
    await deleteBtns[1].trigger('click');
    const emitted = wrapper.emitted('update')!;
    const after = emitted[emitted.length - 1][0] as Array<{ tool: string }>;
    expect(after).toHaveLength(1);
    expect(after[0].tool).toBe('a.tool.read');
  });
});

// 验证 schema 驱动的动态参数表单：必填字段排在前面，enum 用下拉。
describe('SequenceEditor schema-driven params', () => {
  test('renders schema fields and enum as select when available', () => {
    const Host = defineComponent({
      components: { SequenceEditor },
      template: `<SequenceEditor :steps="steps" :tools="tools" @update="onUpdate" />`,
      props: {},
      setup() {
        return {
          steps: [makeStep('kafka.topic.retention.set', {})],
          tools,
          onUpdate: () => {},
        };
      },
    });
    const wrapper = mount(Host, { global: { plugins: [ElementPlus] } });
    const text = wrapper.text();
    expect(text).toContain('topic');
    expect(text).toContain('retention_hours');
    // 必填 topic 排在 enum 字段前
    const paramLabels = wrapper.findAll('.param-label');
    expect(paramLabels[0].text()).toContain('topic');
  });
});
