ALTER TABLE copilot_audit_events
    ADD COLUMN tool_execution_id CHAR(36) NULL AFTER action_plan_id;

ALTER TABLE copilot_audit_events
    ADD COLUMN decision VARCHAR(64) NOT NULL DEFAULT '' AFTER action;

ALTER TABLE copilot_audit_events
    ADD KEY copilot_audit_events_tool_execution_id_idx (tool_execution_id);

ALTER TABLE copilot_audit_events
    ADD CONSTRAINT copilot_audit_events_tool_execution_id_fk
        FOREIGN KEY (tool_execution_id) REFERENCES tool_executions (id);
