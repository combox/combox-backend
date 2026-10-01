-- Telegram-style Privacy settings ("Privacy and Security -> Privacy").
--
-- One row per (user, param). A MISSING row means "the user never touched
-- this parameter" and the service falls back to the documented default, so
-- defaults are NOT materialised here. See internal/service/privacy for the
-- authoritative default map:
--   phone_number      -> contacts
--   forwarded_messages-> contacts
--   everything else   -> everybody
--
-- rule: everybody | contacts | nobody (the three Telegram rules).
-- allow_ids: "Always share with" exceptions (user ids; chat ids are accepted
--            too and stored in the same UUID domain).
-- deny_ids:  "Never share with" exceptions.
CREATE TABLE IF NOT EXISTS privacy_settings (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    param     TEXT NOT NULL CHECK (param IN (
        'phone_number',
        'last_seen',
        'profile_photos',
        'forwarded_messages',
        'calls',
        'voice_messages',
        'messages',
        'birthday',
        'gifts',
        'bio',
        'saved_music',
        'invites'
    )),
    rule      TEXT NOT NULL DEFAULT 'everybody' CHECK (rule IN ('everybody','contacts','nobody')),
    allow_ids UUID[] NOT NULL DEFAULT '{}',
    deny_ids  UUID[] NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, param)
);

CREATE INDEX IF NOT EXISTS idx_privacy_settings_user ON privacy_settings(user_id);
