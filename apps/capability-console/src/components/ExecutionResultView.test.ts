import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ExecutionResultView from './ExecutionResultView.vue';
import type { ExecutionResult, VerificationResult } from '../types';

describe('ExecutionResultView', () => {
  test('renders basic execution info when verification is absent', () => {
    const result: ExecutionResult = {
      type: 'execution_result',
      plan_id: 'plan-1',
      execution_id: 'exec-1',
      status: 'succeeded',
      reused: false,
    };

    const wrapper = mount(ExecutionResultView, { props: { result } });

    expect(wrapper.find('[data-test="execution-result"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="verification-block"]').exists()).toBe(false);
  });

  test('renders runbook field when present (Runbook auto-execution)', () => {
    const result: ExecutionResult = {
      type: 'execution_result',
      plan_id: 'plan-rb-1',
      execution_id: 'exec-rb-1',
      status: 'succeeded',
      reused: false,
      runbook: 'retention-tweak',
    };

    const wrapper = mount(ExecutionResultView, { props: { result } });

    expect(wrapper.find('[data-test="execution-runbook"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="execution-runbook"]').text()).toContain('retention-tweak');
  });

  test('omits runbook row when absent', () => {
    const result: ExecutionResult = {
      type: 'execution_result',
      plan_id: 'plan-1',
      execution_id: 'exec-1',
      status: 'succeeded',
      reused: false,
    };

    const wrapper = mount(ExecutionResultView, { props: { result } });

    expect(wrapper.find('[data-test="execution-runbook"]').exists()).toBe(false);
  });

  test('renders verification success block with answer and elapsed time', () => {
    const verification: VerificationResult = {
      tool_name: 'kafka.topic.retention.read',
      status: 'success',
      answer: { retention_hours: 72 },
      elapsed_ms: 42,
    };
    const result: ExecutionResult = {
      type: 'execution_result',
      plan_id: 'plan-2',
      execution_id: 'exec-2',
      status: 'succeeded',
      reused: false,
      verification,
    };

    const wrapper = mount(ExecutionResultView, { props: { result } });

    const block = wrapper.find('[data-test="verification-block"]');
    expect(block.exists()).toBe(true);
    expect(block.find('[data-test="verification-status-success"]').exists()).toBe(true);
    expect(block.find('[data-test="verification-tool"]').text()).toContain('kafka.topic.retention.read');
    expect(block.find('[data-test="verification-elapsed"]').text()).toContain('42');
    expect(block.find('[data-test="verification-answer"]').text()).toContain('72');
    expect(block.find('[data-test="verification-error"]').exists()).toBe(false);
  });

  test('renders verification failed block with error message', () => {
    const verification: VerificationResult = {
      tool_name: 'kafka.topic.retention.read',
      status: 'failed',
      error: 'connection refused',
    };
    const result: ExecutionResult = {
      type: 'execution_result',
      plan_id: 'plan-3',
      execution_id: 'exec-3',
      status: 'succeeded',
      reused: false,
      verification,
    };

    const wrapper = mount(ExecutionResultView, { props: { result } });

    const block = wrapper.find('[data-test="verification-block"]');
    expect(block.exists()).toBe(true);
    expect(block.find('[data-test="verification-status-failed"]').exists()).toBe(true);
    expect(block.find('[data-test="verification-error"]').text()).toContain('connection refused');
    expect(block.find('[data-test="verification-answer"]').exists()).toBe(false);
  });

  test('renders verification denied block without error or answer', () => {
    const verification: VerificationResult = {
      tool_name: 'kafka.topic.retention.read',
      status: 'denied',
    };
    const result: ExecutionResult = {
      type: 'execution_result',
      plan_id: 'plan-4',
      execution_id: 'exec-4',
      status: 'succeeded',
      reused: false,
      verification,
    };

    const wrapper = mount(ExecutionResultView, { props: { result } });

    const block = wrapper.find('[data-test="verification-block"]');
    expect(block.exists()).toBe(true);
    expect(block.find('[data-test="verification-status-denied"]').exists()).toBe(true);
    expect(block.find('[data-test="verification-error"]').exists()).toBe(false);
    expect(block.find('[data-test="verification-answer"]').exists()).toBe(false);
  });
});
