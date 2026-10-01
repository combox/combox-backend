-- Saved Messages self-chat (Telegram-style "Избранное").
--
-- The saved chat reuses the chats table with chat_kind = 'saved' and a single
-- member (the owner, role 'owner'). There is no separate table and no new
-- column: the unique marker is (created_by + chat_kind = 'saved'), enforced by
-- a partial unique index so GetOrCreate stays idempotent under races (the
-- loser of a concurrent create hits the index and re-reads the winner).
--
-- ListChatsByUser needs no change: it already excludes only group topics
-- (chat_kind = 'channel' with a parent) and comment threads, so a 'saved'
-- row is returned like any other chat the user belongs to. Sending,
-- reactions, forwards and media work through the ordinary chat paths.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_chats_saved_per_user
    ON chats(created_by)
    WHERE chat_kind = 'saved';
