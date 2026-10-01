DROP INDEX IF EXISTS idx_chat_user_states_pinned;

ALTER TABLE chat_user_states
    DROP COLUMN IF EXISTS pin_order,
    DROP COLUMN IF EXISTS pin_scope;
