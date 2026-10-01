-- Who is allowed to post into a channel:
--   'all'   -> every member may send messages (default)
--   'admins'-> only owner/admin/moderator may send messages
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS send_permission TEXT NOT NULL DEFAULT 'all';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'chk_chats_send_permission'
    ) THEN
        ALTER TABLE chats
            ADD CONSTRAINT chk_chats_send_permission
            CHECK (send_permission IN ('all', 'admins'));
    END IF;
END
$$;
