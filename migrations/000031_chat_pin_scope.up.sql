ALTER TABLE chat_user_states
    ADD COLUMN IF NOT EXISTS pin_scope TEXT NOT NULL DEFAULT 'all',
    ADD COLUMN IF NOT EXISTS pin_order BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_chat_user_states_pinned
    ON chat_user_states(user_id, pin_scope, pin_order)
    WHERE pinned;
