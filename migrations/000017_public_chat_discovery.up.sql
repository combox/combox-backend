ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS public_slug TEXT;

-- public_slug is a public @username-like handle. It must be unique when present.
CREATE UNIQUE INDEX IF NOT EXISTS idx_chats_public_slug_unique
    ON chats (public_slug)
    WHERE public_slug IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_chats_is_public ON chats(is_public);
