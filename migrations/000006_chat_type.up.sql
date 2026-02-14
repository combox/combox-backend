ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS chat_type TEXT NOT NULL DEFAULT 'standard';

CREATE INDEX IF NOT EXISTS idx_chats_type ON chats(chat_type);
