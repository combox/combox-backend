-- Post-migration cleanup for the boxchat -> combox transition.
--
-- Run MANUALLY with psql AFTER the transition period is over, e.g.:
--   psql -tAc "$(cat tools/legacy_cleanup.sql)"
-- (quote the whole file: piping via `cat file | psql` breaks on some setups).
--
-- This script is intentionally conservative:
--   1. It first prints an audit (remaining unverified users, placeholder emails).
--      If unverified users remain, stop here and let them bind an email first —
--      the script aborts the transaction in that case (see the guard below).
--      To force-run anyway, delete the guard block.
--   2. It drops the ETL bookkeeping table `legacy_id_map` (idempotency map).
--      Re-running the ETL afterwards would re-insert rows, so only run this
--      once no more ETL runs are planned.
--   3. It does NOT touch user data: `users.legacy_username` is kept for audit,
--      `users.is_legacy_unverified` stays as-is (verified users already have
--      FALSE thanks to bind-email/verify). Disabling username-only login is a
--      code change, not covered here.
--
-- Safe to run multiple times (all statements are IF EXISTS / guarded).

BEGIN;

-- 1. Audit: how many legacy users never bound an email?
DO $$
DECLARE
  remaining integer;
BEGIN
  SELECT count(*) INTO remaining
  FROM users
  WHERE is_legacy_unverified IS TRUE;
  RAISE NOTICE 'legacy_cleanup: remaining unverified users = %', remaining;
  IF remaining > 0 THEN
    RAISE EXCEPTION 'legacy_cleanup: % user(s) still unverified — bind emails first, aborting', remaining;
  END IF;
END
$$;

-- 2. Sanity: any leftover placeholder emails on verified accounts?
DO $$
DECLARE
  placeholders integer;
BEGIN
  SELECT count(*) INTO placeholders
  FROM users
  WHERE is_legacy_unverified IS NOT TRUE
    AND email LIKE '%@legacy.invalid';
  RAISE NOTICE 'legacy_cleanup: verified accounts still on placeholder email = %', placeholders;
END
$$;

-- 3. Drop ETL bookkeeping (idempotency map). No FKs point to it (verified:
--    only cmd/boxchat-migrate reads/writes it).
DROP TABLE IF EXISTS legacy_id_map;

COMMIT;

-- 4. Post-check (outside the transaction so it always runs).
SELECT 'legacy_id_map present?' AS check, count(*) AS tables
FROM information_schema.tables
WHERE table_name = 'legacy_id_map';
