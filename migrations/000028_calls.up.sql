-- Calls subsystem: persisted call sessions and their participants.
CREATE TABLE IF NOT EXISTS call_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    topology TEXT NOT NULL,
    e2ee BOOLEAN NOT NULL DEFAULT FALSE,
    started_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at TIMESTAMPTZ,
    end_reason TEXT,
    max_participants INT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_call_sessions_chat_started
    ON call_sessions(chat_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_call_sessions_chat_active
    ON call_sessions(chat_id, started_at DESC)
    WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS call_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id UUID NOT NULL REFERENCES call_sessions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at TIMESTAMPTZ,
    leave_reason TEXT
);

CREATE INDEX IF NOT EXISTS idx_call_participants_call
    ON call_participants(call_id, joined_at);
CREATE INDEX IF NOT EXISTS idx_call_participants_user
    ON call_participants(user_id, joined_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_call_participants_active
    ON call_participants(call_id, user_id)
    WHERE left_at IS NULL;

-- Client supplied media metadata for voice messages and video notes
-- (waveform peaks, round flag, recorded duration).
ALTER TABLE attachments
    ADD COLUMN IF NOT EXISTS user_meta JSONB NOT NULL DEFAULT '{}'::jsonb;
