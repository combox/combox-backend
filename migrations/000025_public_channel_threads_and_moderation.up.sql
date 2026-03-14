CREATE TABLE IF NOT EXISTS comment_threads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    root_message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    thread_chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_comment_threads_channel_root_unique
    ON comment_threads(channel_chat_id, root_message_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_comment_threads_thread_chat_unique
    ON comment_threads(thread_chat_id);

CREATE INDEX IF NOT EXISTS idx_comment_threads_channel_created
    ON comment_threads(channel_chat_id, created_at DESC);

CREATE TABLE IF NOT EXISTS public_channel_bans (
    channel_chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_chat_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_public_channel_bans_channel_created
    ON public_channel_bans(channel_chat_id, created_at DESC);

CREATE TABLE IF NOT EXISTS public_channel_mutes (
    channel_chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_chat_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_public_channel_mutes_channel_created
    ON public_channel_mutes(channel_chat_id, created_at DESC);
