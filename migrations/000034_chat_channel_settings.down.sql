DROP INDEX IF EXISTS idx_chats_discussion_chat_id;

ALTER TABLE chats
    DROP COLUMN IF EXISTS discussion_chat_id,
    DROP COLUMN IF EXISTS slow_mode_seconds,
    DROP COLUMN IF EXISTS auto_translate,
    DROP COLUMN IF EXISTS show_authors_profiles,
    DROP COLUMN IF EXISTS sign_messages,
    DROP COLUMN IF EXISTS reactions_enabled;
