-- Channel/group settings that the UI exposes but nothing persisted yet.
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS reactions_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS sign_messages BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS show_authors_profiles BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS auto_translate BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS slow_mode_seconds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS discussion_chat_id UUID REFERENCES chats(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_chats_discussion_chat_id ON chats(discussion_chat_id);
