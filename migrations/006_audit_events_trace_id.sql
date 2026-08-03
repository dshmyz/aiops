-- 006_audit_events_trace_id.sql
-- Correlate audit events with OpenTelemetry traces so an operator can jump
-- from an audit log entry to the distributed trace that produced it.
ALTER TABLE copilot_audit_events
    ADD COLUMN trace_id CHAR(32) NULL AFTER decision;

ALTER TABLE copilot_audit_events
    ADD KEY copilot_audit_events_trace_id_idx (trace_id);
