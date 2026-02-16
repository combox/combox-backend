ALTER TABLE users
    ADD COLUMN IF NOT EXISTS session_idle_ttl_seconds BIGINT;

CREATE INDEX IF NOT EXISTS idx_users_session_idle_ttl_seconds ON users(session_idle_ttl_seconds);
