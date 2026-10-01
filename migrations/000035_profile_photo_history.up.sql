-- Telegram-style avatar/photo history: every avatar object that was ever
-- written for a user or a chat keeps a row, so the fullscreen gallery can
-- replay the whole set instead of only the current picture.
--
-- owner_kind/owner_id are polymorphic (no FK on purpose): one table serves
-- both `users.id` and `chats.id`.
CREATE TABLE IF NOT EXISTS profile_photos (
    id UUID PRIMARY KEY,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('user','chat')),
    owner_id UUID NOT NULL,
    object_key TEXT NOT NULL,
    taken_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_profile_photos_owner ON profile_photos(owner_kind, owner_id, created_at DESC);
