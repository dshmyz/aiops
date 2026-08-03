CREATE TABLE IF NOT EXISTS copilot_assistant_feedback (
    id CHAR(36) NOT NULL,
    conversation_id CHAR(36) NOT NULL,
    turn_id CHAR(36) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    rating TINYINT NOT NULL DEFAULT 0,
    correction TEXT NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY copilot_assistant_feedback_turn_idx (turn_id),
    KEY copilot_assistant_feedback_conversation_idx (conversation_id, subject, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
