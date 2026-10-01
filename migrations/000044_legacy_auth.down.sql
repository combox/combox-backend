-- Rollback of 000044_legacy_auth: drops the legacy-auth columns.
-- NOTE: rolling back after the ETL ran destroys the is_legacy_unverified
-- flags and the legacy_username audit trail; re-running the ETL is required.
ALTER TABLE users
    DROP COLUMN IF EXISTS legacy_username,
    DROP COLUMN IF EXISTS is_legacy_unverified;
