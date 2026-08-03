import type { AuditEvent } from './types';

const csvColumns = ['id', 'created_at', 'tool_name', 'action', 'decision', 'subject', 'plan_id', 'execution_id', 'request_id'] as const;

function escapeCSV(value: unknown): string {
  if (value === null || value === undefined) return '';
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  if (/[",\n\r]/.test(text)) {
    return `"${text.replace(/"/g, '""')}"`;
  }
  return text;
}

export function auditEventsToCSV(events: AuditEvent[]): string {
  const header = csvColumns.join(',');
  const rows = events.map((event) =>
    csvColumns
      .map((column) => escapeCSV(event[column as keyof AuditEvent] ?? ''))
      .join(','),
  );
  return [header, ...rows].join('\r\n');
}

export function downloadAuditCSV(events: AuditEvent[], filename = 'audit-events.csv'): void {
  const csv = auditEventsToCSV(events);
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}
