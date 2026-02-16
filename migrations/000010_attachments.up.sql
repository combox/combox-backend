CREATE TABLE IF NOT EXISTS attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    kind TEXT NOT NULL,
    variant TEXT NOT NULL,
    is_client_compressed BOOLEAN NOT NULL DEFAULT FALSE,
    size_bytes BIGINT,
    width INT,
    height INT,
    duration_ms INT,
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    upload_type TEXT NOT NULL,
    upload_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_attachments_bucket_key ON attachments(bucket, object_key);
CREATE INDEX IF NOT EXISTS idx_attachments_user_id_created ON attachments(user_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS message_attachments (
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    attachment_id UUID NOT NULL REFERENCES attachments(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, attachment_id)
);

CREATE INDEX IF NOT EXISTS idx_message_attachments_attachment_id ON message_attachments(attachment_id);
