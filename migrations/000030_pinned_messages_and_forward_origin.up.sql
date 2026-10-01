-- Pinned chat messages and forward provenance for forwarded messages.
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS pinned_message_id UUID REFERENCES messages(id) ON DELETE SET NULL;

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS forward_origin_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_chats_pinned_message
    ON chats(pinned_message_id)
    WHERE pinned_message_id IS NOT NULL;
