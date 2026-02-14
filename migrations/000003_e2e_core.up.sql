CREATE TABLE IF NOT EXISTS e2e_devices (
    device_id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    identity_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_e2e_devices_user_id ON e2e_devices(user_id);

CREATE TABLE IF NOT EXISTS e2e_signed_prekeys (
    device_id UUID NOT NULL REFERENCES e2e_devices(device_id) ON DELETE CASCADE,
    key_id INTEGER NOT NULL,
    public_key TEXT NOT NULL,
    signature TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (device_id, key_id)
);

CREATE TABLE IF NOT EXISTS e2e_one_time_prekeys (
    device_id UUID NOT NULL REFERENCES e2e_devices(device_id) ON DELETE CASCADE,
    key_id INTEGER NOT NULL,
    public_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at TIMESTAMPTZ,
    PRIMARY KEY (device_id, key_id)
);

CREATE INDEX IF NOT EXISTS idx_e2e_one_time_prekeys_device_consumed ON e2e_one_time_prekeys(device_id, consumed_at);

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS is_e2e BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS e2e_alg TEXT,
    ADD COLUMN IF NOT EXISTS e2e_ciphertext TEXT,
    ADD COLUMN IF NOT EXISTS e2e_header TEXT,
    ADD COLUMN IF NOT EXISTS e2e_sender_device_id UUID,
    ADD COLUMN IF NOT EXISTS e2e_recipient_device_id UUID;

ALTER TABLE messages
    ALTER COLUMN content DROP NOT NULL;
