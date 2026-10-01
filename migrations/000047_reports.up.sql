-- User reports for the Report buttons in the UI.
--
-- One row per POST /api/private/v1/reports call: who reported what and why.
-- No idempotency key (task: not needed); spam is curbed by the service
-- per-reporter sliding-window limiter (10/min, same pattern as the
-- translate service userLimiter), not by a uniqueness constraint.
CREATE TABLE IF NOT EXISTS reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_type TEXT NOT NULL
        CONSTRAINT reports_target_type_check CHECK (target_type IN ('user', 'chat', 'photo', 'message')),
    target_id TEXT NOT NULL
        CONSTRAINT reports_target_id_check CHECK (char_length(target_id) BETWEEN 1 AND 256),
    reason TEXT NOT NULL
        CONSTRAINT reports_reason_check CHECK (char_length(reason) BETWEEN 1 AND 2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reports_reporter ON reports(reporter_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reports_target ON reports(target_type, target_id);
