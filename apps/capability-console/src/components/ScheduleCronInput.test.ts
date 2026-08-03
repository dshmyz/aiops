import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ScheduleCronInput from './ScheduleCronInput.vue';

describe('ScheduleCronInput', () => {
  test('binds modelValue to the textarea', () => {
    const wrapper = mount(ScheduleCronInput, {
      props: { modelValue: '0 2 * * 1-5' },
    });

    const textarea = wrapper.find('[data-test="schedule-cron-input"]').element as HTMLTextAreaElement;
    expect(textarea.value).toBe('0 2 * * 1-5');
  });

  test('emits update:modelValue when textarea value changes', async () => {
    const wrapper = mount(ScheduleCronInput, {
      props: { modelValue: '' },
    });

    await wrapper.find('[data-test="schedule-cron-input"]').setValue('*/5 * * * *');

    const events = wrapper.emitted('update:modelValue');
    expect(events).toBeDefined();
    expect(events?.[0]).toEqual(['*/5 * * * *']);
  });

  test('shows next-run preview when cron expression is valid', async () => {
    const wrapper = mount(ScheduleCronInput, {
      props: { modelValue: '' },
    });

    await wrapper.find('[data-test="schedule-cron-input"]').setValue('0 2 * * *');

    // The preview text should mention "下次执行" and contain a formatted date.
    const preview = wrapper.find('[data-test="schedule-cron-preview"]');
    expect(preview.exists()).toBe(true);
    expect(preview.text()).toContain('下次执行');
    // Should not surface an error block for a valid expression.
    expect(wrapper.find('[data-test="schedule-cron-error"]').exists()).toBe(false);
  });

  test('emits valid=true when the expression parses', async () => {
    const wrapper = mount(ScheduleCronInput, {
      props: { modelValue: '' },
    });

    await wrapper.find('[data-test="schedule-cron-input"]').setValue('0 2 * * *');

    const validEvents = wrapper.emitted('valid');
    expect(validEvents).toBeDefined();
    const lastValid = validEvents?.[validEvents.length - 1];
    expect(lastValid).toEqual([true]);
  });

  test('shows red error and emits valid=false for invalid cron expression', async () => {
    const wrapper = mount(ScheduleCronInput, {
      props: { modelValue: '' },
    });

    await wrapper.find('[data-test="schedule-cron-input"]').setValue('not a cron');

    const errorBlock = wrapper.find('[data-test="schedule-cron-error"]');
    expect(errorBlock.exists()).toBe(true);
    expect(errorBlock.text()).not.toBe('');
    const validEvents = wrapper.emitted('valid');
    const lastValid = validEvents?.[validEvents.length - 1];
    expect(lastValid).toEqual([false]);
    // No preview should render while the expression is invalid.
    expect(wrapper.find('[data-test="schedule-cron-preview"]').exists()).toBe(false);
  });

  test('treats empty input as invalid with no preview', async () => {
    const wrapper = mount(ScheduleCronInput, {
      props: { modelValue: '0 2 * * *' },
    });

    await wrapper.find('[data-test="schedule-cron-input"]').setValue('');

    const validEvents = wrapper.emitted('valid');
    const lastValid = validEvents?.[validEvents.length - 1];
    expect(lastValid).toEqual([false]);
    expect(wrapper.find('[data-test="schedule-cron-preview"]').exists()).toBe(false);
  });

  test('reflects parent-driven modelValue updates in the textarea', async () => {
    const wrapper = mount(ScheduleCronInput, {
      props: { modelValue: '' },
    });

    await wrapper.setProps({ modelValue: '0 0 * * *' });

    const textarea = wrapper.find('[data-test="schedule-cron-input"]').element as HTMLTextAreaElement;
    expect(textarea.value).toBe('0 0 * * *');
  });
});
