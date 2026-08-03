import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import SchedulePresetPicker from './SchedulePresetPicker.vue';

describe('SchedulePresetPicker', () => {
  test('renders four preset options with labels', () => {
    const wrapper = mount(SchedulePresetPicker, {
      props: { modelValue: null },
    });

    const options = wrapper.findAll('[data-test="schedule-preset-option"]');
    expect(options).toHaveLength(4);
    const labels = options.map((option) => option.text());
    expect(labels.some((label) => label.includes('每 5 分钟'))).toBe(true);
    expect(labels.some((label) => label.includes('每小时'))).toBe(true);
    expect(labels.some((label) => label.includes('每天'))).toBe(true);
    expect(labels.some((label) => label.includes('每周一'))).toBe(true);
  });

  test('marks the selected option as active', () => {
    const wrapper = mount(SchedulePresetPicker, {
      props: { modelValue: 'daily' },
    });

    const options = wrapper.findAll('[data-test="schedule-preset-option"]');
    const activeValues = options.filter((option) => option.classes().includes('active')).map((option) => option.attributes('data-preset'));
    expect(activeValues).toEqual(['daily']);
  });

  test('does not mark any option active when modelValue is null', () => {
    const wrapper = mount(SchedulePresetPicker, {
      props: { modelValue: null },
    });

    const activeOptions = wrapper.findAll('[data-test="schedule-preset-option"].active');
    expect(activeOptions).toHaveLength(0);
  });

  test('emits update:modelValue with the chosen preset when an option is clicked', async () => {
    const wrapper = mount(SchedulePresetPicker, {
      props: { modelValue: null },
    });

    const options = wrapper.findAll('[data-test="schedule-preset-option"]');
    await options[2].trigger('click');

    const events = wrapper.emitted('update:modelValue');
    expect(events).toBeDefined();
    expect(events?.[0]).toEqual(['daily']);
  });

  test('keeps each option stable after clicking another option', async () => {
    const wrapper = mount(SchedulePresetPicker, {
      props: { modelValue: '5m' },
    });

    const weekly = wrapper.find('[data-test="schedule-preset-option"][data-preset="weekly"]');
    await weekly.trigger('click');

    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['weekly']);
    // modelValue is controlled by parent, so the component does not flip state
    // itself. We confirm via the prop-driven active class instead.
    expect(wrapper.findAll('[data-test="schedule-preset-option"].active')).toHaveLength(1);
  });
});
