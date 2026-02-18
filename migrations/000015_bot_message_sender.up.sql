ALTER TABLE bots
    ALTER COLUMN actor_user_id DROP NOT NULL;

ALTER TABLE messages
    ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS sender_bot_id UUID REFERENCES bots(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_messages_sender_bot_id ON messages(sender_bot_id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'messages_sender_actor_check'
    ) THEN
        ALTER TABLE messages
            ADD CONSTRAINT messages_sender_actor_check
            CHECK (user_id IS NOT NULL OR sender_bot_id IS NOT NULL);
    END IF;
END $$;
