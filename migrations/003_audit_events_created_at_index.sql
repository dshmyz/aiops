ALTER TABLE copilot_audit_events
    ADD KEY copilot_audit_events_created_at_idx (created_at);

ALTER TABLE copilot_audit_events
    ADD KEY copilot_audit_events_actor_subject_idx (actor_subject);
