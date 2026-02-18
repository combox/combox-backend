CREATE TABLE IF NOT EXISTS bots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL DEFAULT 'user',
    name TEXT,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_bots_owner_kind ON bots(owner_user_id, kind) WHERE owner_user_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_bots_system_true ON bots(is_system) WHERE is_system = TRUE;
CREATE INDEX IF NOT EXISTS idx_bots_actor_user_id ON bots(actor_user_id);

ALTER TABLE bot_tokens
    ADD COLUMN IF NOT EXISTS bot_id UUID;

INSERT INTO bots (owner_user_id, actor_user_id, kind, name, is_system)
SELECT DISTINCT bt.bot_user_id, bt.bot_user_id, 'user', 'User Bot', FALSE
FROM bot_tokens bt
LEFT JOIN bots b
  ON b.owner_user_id = bt.bot_user_id AND b.kind = 'user'
WHERE b.id IS NULL;

UPDATE bot_tokens bt
SET bot_id = b.id
FROM bots b
WHERE bt.bot_id IS NULL
  AND b.owner_user_id = bt.bot_user_id
  AND b.kind = 'user';

ALTER TABLE bot_tokens
    ALTER COLUMN bot_id SET NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.table_constraints
        WHERE table_name = 'bot_tokens'
          AND constraint_type = 'FOREIGN KEY'
          AND constraint_name = 'bot_tokens_bot_user_id_fkey'
    ) THEN
        ALTER TABLE bot_tokens DROP CONSTRAINT bot_tokens_bot_user_id_fkey;
    END IF;
END $$;

ALTER TABLE bot_tokens
    ADD CONSTRAINT bot_tokens_bot_id_fkey FOREIGN KEY (bot_id) REFERENCES bots(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS idx_bot_tokens_user_id;
CREATE INDEX IF NOT EXISTS idx_bot_tokens_bot_id ON bot_tokens(bot_id);

ALTER TABLE bot_tokens
    DROP COLUMN IF EXISTS bot_user_id;
