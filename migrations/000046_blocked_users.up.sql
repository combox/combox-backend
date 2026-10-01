-- Blocked users ("Privacy and Security -> Blocked users").
--
-- One row per (owner, blocked): owner never receives DMs/calls from blocked
-- and sees none of their presence. No other table references it; enforcement
-- (message/call filtering) is a later step, this migration is storage + list
-- API only.
--
-- A MISSING row means "not blocked". Self-blocks are rejected by the CHECK.
CREATE TABLE IF NOT EXISTS blocked_users (
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, blocked_id),
    CONSTRAINT blocked_users_no_self CHECK (owner_id <> blocked_id)
);

CREATE INDEX IF NOT EXISTS idx_blocked_users_owner ON blocked_users(owner_id);
