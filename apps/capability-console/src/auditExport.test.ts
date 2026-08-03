import { describe, expect, test, vi } from 'vitest';
import { auditEventsToCSV, downloadAuditCSV } from './auditExport';
import type { AuditEvent } from './types';

describe('auditExport', () => {
  const events: AuditEvent[] = [
    {
      id: 'evt-1',
      plan_id: 'plan-1',
      subject: 'operator-1',
      tool_name: 'kafka.topic.retention.set',
      action: 'plan_created',
      decision: 'permitted',
      request_id: 'req-1',
      metadata: { risk: 'medium' },
      created_at: '2026-07-25T10:00:00Z',
    },
    {
      id: 'evt-2',
      plan_id: 'plan-1',
      execution_id: 'exec-1',
      subject: 'admin-1',
      tool_name: 'kafka.topic.retention.set',
      action: 'execution_started,plan_confirmed',
      decision: 'permitted',
      created_at: '2026-07-25T10:05:00Z',
    },
  ];

  test('serializes events to CSV with header and rows', () => {
    const csv = auditEventsToCSV(events);
    const lines = csv.split('\r\n');
    expect(lines[0]).toBe('id,created_at,tool_name,action,decision,subject,plan_id,execution_id,request_id');
    expect(lines[1]).toContain('evt-1');
    expect(lines[1]).toContain('kafka.topic.retention.set');
    expect(lines[1]).toContain('operator-1');
    expect(lines[1]).toContain('req-1');
    expect(lines[2]).toContain('evt-2');
    expect(lines[2]).toContain('exec-1');
  });

  test('escapes commas in values by quoting the field', () => {
    const csv = auditEventsToCSV(events);
    const lines = csv.split('\r\n');
    expect(lines[2]).toContain('"execution_started,plan_confirmed"');
  });

  test('omits metadata column (kept in detail view)', () => {
    const csv = auditEventsToCSV(events);
    expect(csv).not.toContain('metadata');
    expect(csv).not.toContain('medium');
  });

  test('downloadAuditCSV creates an anchor and triggers a click', () => {
    const createObjectURL = vi.fn(() => 'blob:url');
    const revokeObjectURL = vi.fn();
    vi.stubGlobal('URL', { createObjectURL, revokeObjectURL });
    const clickSpy = vi.fn();
    const removeChildSpy = vi.fn();
    const appendChildSpy = vi.fn();
    const fakeLink = {
      href: '',
      download: '',
      click: clickSpy,
    } as unknown as HTMLAnchorElement;
    const createElementSpy = vi.fn(() => fakeLink);
    const fakeBody = {
      appendChild: appendChildSpy,
      removeChild: removeChildSpy,
    } as unknown as HTMLElement;
    vi.stubGlobal('document', { createElement: createElementSpy, body: fakeBody });
    vi.stubGlobal('Blob', vi.fn());

    downloadAuditCSV(events, 'audit.csv');

    expect(createObjectURL).toHaveBeenCalledOnce();
    expect(appendChildSpy).toHaveBeenCalledWith(fakeLink);
    expect(clickSpy).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:url');
    expect(removeChildSpy).toHaveBeenCalledWith(fakeLink);
    expect(fakeLink.download).toBe('audit.csv');

    vi.unstubAllGlobals();
  });
});
