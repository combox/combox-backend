ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS parent_chat_id UUID REFERENCES chats(id) ON DELETE CASCADE;

ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS channel_type TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'chk_chats_channel_type'
    ) THEN
        ALTER TABLE chats
            ADD CONSTRAINT chk_chats_channel_type
            CHECK (channel_type IS NULL OR channel_type IN ('text', 'voice'));
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_chats_parent_chat_id ON chats(parent_chat_id);
CREATE INDEX IF NOT EXISTS idx_chats_parent_channel_type ON chats(parent_chat_id, channel_type);
