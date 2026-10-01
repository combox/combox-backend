-- "Recent actions" log for the chat settings panel: who joined, who was
-- removed, which role/setting changed. One row per action, newest first.
--
-- event_type: member_joined | member_left | member_removed | role_changed |
--             title_changed | avatar_changed | description_changed |
--             icon_changed | settings_changed | banned | unbanned
-- payload:    short human readable detail (e.g. "role=admin", "reactions_enabled=false");
--             never carries secrets or whole data URLs.
--
-- Writes are best effort (the service swallows failures) so the journal can
-- never break the operation it describes.
CREATE TABLE IF NOT EXISTS chat_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    actor_user_id UUID,
    target_user_id UUID,
    event_type TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chat_events_chat_created_at ON chat_events (chat_id, created_at DESC);
