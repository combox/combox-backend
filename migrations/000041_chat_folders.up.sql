-- Telegram style chat folders (dialog filters): a user groups chats into
-- named folders that show up as tabs above the chat list.
--
-- name:     1..32 characters after trim (service layer), unique per user.
-- icon:     optional short glyph up to 8 characters; '' when never set.
-- position: order inside the folder bar, 0 based, assigned by the service.
--           Reordering rewrites the whole (user_id, position) sequence so
--           positions stay a dense permutation.
--
-- chat_folder_chats keeps the chats of one folder in their own order; the
-- same chat may belong to several folders of the same user.
CREATE TABLE IF NOT EXISTS chat_folders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    icon TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chat_folders_user_name_key UNIQUE (user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_chat_folders_user_position ON chat_folders (user_id, position);

CREATE TABLE IF NOT EXISTS chat_folder_chats (
    folder_id UUID NOT NULL REFERENCES chat_folders(id) ON DELETE CASCADE,
    chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (folder_id, chat_id)
);

CREATE INDEX IF NOT EXISTS idx_chat_folder_chats_chat_id ON chat_folder_chats (chat_id);
