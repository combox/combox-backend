-- BoxChat legacy migration (boxchat -> combox ETL): the ETL inserts migrated
-- users with a placeholder email and flags them for the forced email-binding
-- flow (POST /api/private/v1/auth/legacy/bind-email/*).
--
-- is_legacy_unverified: the SINGLE criterion that marks a migrated user.
--   TRUE  -> legacy user, password checked against the werkzeug scrypt hash,
--            login issues a migr=true limited token until the email is bound.
--   FALSE -> regular combox user, flow unchanged (bcrypt).
-- legacy_username: original boxchat username, kept for audit. It is NEVER
--   cleared by the bind flow (UPDATE ... SET email, is_legacy_unverified only).
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_legacy_unverified BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS legacy_username TEXT;
