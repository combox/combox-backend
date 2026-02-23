ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS chat_kind TEXT;

UPDATE chats
SET chat_kind = CASE
    WHEN is_direct THEN 'direct'
    ELSE 'group'
END
WHERE chat_kind IS NULL;

ALTER TABLE chats
    ALTER COLUMN chat_kind SET NOT NULL,
    ALTER COLUMN chat_kind SET DEFAULT 'group';

ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS bot_id UUID REFERENCES bots(id) ON DELETE SET NULL;

UPDATE chats
SET chat_kind = 'bot'
WHERE bot_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_chats_kind ON chats(chat_kind);
CREATE INDEX IF NOT EXISTS idx_chats_bot_id ON chats(bot_id);
