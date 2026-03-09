CREATE TABLE IF NOT EXISTS standalone_channels (
    chat_id UUID PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    public_slug TEXT,
    comments_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    reactions_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    avatar_data_url TEXT,
    avatar_gradient TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_standalone_channels_public_slug_unique
    ON standalone_channels(public_slug)
    WHERE public_slug IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_standalone_channels_is_public
    ON standalone_channels(is_public);

CREATE TABLE IF NOT EXISTS standalone_channel_members (
    chat_id UUID NOT NULL REFERENCES standalone_channels(chat_id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'subscriber',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (chat_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_standalone_channel_members_user_id
    ON standalone_channel_members(user_id);

INSERT INTO standalone_channels (
    chat_id,
    title,
    created_by,
    is_public,
    public_slug,
    comments_enabled,
    reactions_enabled,
    avatar_data_url,
    avatar_gradient,
    created_at,
    updated_at
)
SELECT c.id,
       c.title,
       c.created_by,
       c.is_public,
       c.public_slug,
       c.comments_enabled,
       COALESCE(c.reactions_enabled, TRUE),
       c.avatar_data_url,
       c.avatar_gradient,
       c.created_at,
       c.updated_at
FROM chats c
WHERE c.chat_kind = 'standalone_channel'
ON CONFLICT (chat_id) DO NOTHING;

INSERT INTO standalone_channel_members (chat_id, user_id, role, joined_at)
SELECT cm.chat_id,
       cm.user_id,
       CASE
         WHEN LOWER(TRIM(cm.role)) IN ('owner', 'admin', 'subscriber', 'banned') THEN LOWER(TRIM(cm.role))
         ELSE 'subscriber'
       END,
       cm.joined_at
FROM chat_members cm
INNER JOIN chats c ON c.id = cm.chat_id
WHERE c.chat_kind = 'standalone_channel'
ON CONFLICT (chat_id, user_id) DO NOTHING;

DELETE FROM chat_members cm
USING chats c
WHERE c.id = cm.chat_id
  AND c.chat_kind = 'standalone_channel';
