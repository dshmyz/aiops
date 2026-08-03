CREATE TABLE IF NOT EXISTS copilot_assistant_conversations (
    id CHAR(36) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL DEFAULT '',
    last_message_preview VARCHAR(500) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_active_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    archived_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY copilot_assistant_conversations_subject_last_active_idx (subject, last_active_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS copilot_assistant_turns (
    id CHAR(36) NOT NULL,
    conversation_id CHAR(36) NOT NULL,
    parent_turn_id CHAR(36) NULL,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    response_type VARCHAR(32) NULL,
    response_payload JSON NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY copilot_assistant_turns_conversation_created_idx (conversation_id, created_at),
    CONSTRAINT copilot_assistant_turns_conversation_id_fk
        FOREIGN KEY (conversation_id) REFERENCES copilot_assistant_conversations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
