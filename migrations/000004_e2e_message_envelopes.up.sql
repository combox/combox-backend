CREATE TABLE IF NOT EXISTS message_envelopes (
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    recipient_device_id UUID NOT NULL REFERENCES e2e_devices(device_id) ON DELETE CASCADE,
    alg TEXT NOT NULL,
    header TEXT NOT NULL,
    ciphertext TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, recipient_device_id)
);

CREATE INDEX IF NOT EXISTS idx_message_envelopes_recipient_created
    ON message_envelopes(recipient_device_id, created_at DESC);
