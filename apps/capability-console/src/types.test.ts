import { describe, expect, test } from 'vitest';
import type { AuditEvent } from './types';

describe('AuditEvent type', () => {
  test('supports an optional trace_id field', () => {
    const event: AuditEvent = {
      id: 'evt-1',
      plan_id: 'plan-1',
      subject: 'admin-1',
      tool_name: 'kafka.topic.retention.set',
      action: 'plan_created',
      decision: 'permitted',
      trace_id: '4bf92f3577b34da6a3ce929d0e0e4736',
      created_at: '2026-07-25T10:00:00Z',
    };
    // The field is present on the interface (compile-checked by assignment)
    // and holds the value at runtime.
    expect(event.trace_id).toBe('4bf92f3577b34da6a3ce929d0e0e4736');
  });

  test('trace_id is optional and is undefined when omitted', () => {
    const event: AuditEvent = {
      id: 'evt-2',
      plan_id: 'plan-1',
      subject: 'admin-1',
      tool_name: 'kafka.topic.retention.set',
      action: 'plan_created',
      decision: 'permitted',
      created_at: '2026-07-25T10:00:00Z',
    };
    // Omitting trace_id still satisfies the interface, and the field reads back
    // as undefined rather than throwing.
    expect(event.trace_id).toBeUndefined();
  });
});
