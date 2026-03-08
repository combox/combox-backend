ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS comments_enabled BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE IF NOT EXISTS chat_invite_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL,
    title TEXT,
    is_primary BOOLEAN NOT NULL DEFAULT FALSE,
    use_count INTEGER NOT NULL DEFAULT 0,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_invite_links_token_unique
    ON chat_invite_links(token);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_invite_links_primary_unique
    ON chat_invite_links(chat_id)
    WHERE is_primary = TRUE AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_chat_invite_links_chat_id
    ON chat_invite_links(chat_id, created_at DESC);
