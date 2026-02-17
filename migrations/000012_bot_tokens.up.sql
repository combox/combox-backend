CREATE TABLE IF NOT EXISTS bot_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT,
    secret_hash TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    chat_ids TEXT[] NOT NULL,
    is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bot_tokens_user_id ON bot_tokens(bot_user_id);
CREATE INDEX IF NOT EXISTS idx_bot_tokens_active ON bot_tokens(is_revoked, expires_at);
