CREATE TABLE IF NOT EXISTS media_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    attachment_id UUID NOT NULL REFERENCES attachments(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'uploading',
    parts_total INT NOT NULL DEFAULT 0,
    parts_uploaded INT NOT NULL DEFAULT 0,
    bytes_total BIGINT,
    bytes_uploaded BIGINT NOT NULL DEFAULT 0,
    playlist_path TEXT,
    error_code TEXT,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_media_sessions_user_created ON media_sessions(user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_media_sessions_attachment_id ON media_sessions(attachment_id);
