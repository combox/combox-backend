CREATE TABLE IF NOT EXISTS e2e_user_key_backups (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    alg TEXT NOT NULL,
    kdf TEXT NOT NULL,
    salt TEXT NOT NULL,
    params JSONB NOT NULL,
    ciphertext TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_e2e_user_key_backups_updated_at ON e2e_user_key_backups(updated_at);
