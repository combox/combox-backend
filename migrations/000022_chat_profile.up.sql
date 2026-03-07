ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS avatar_data_url TEXT,
    ADD COLUMN IF NOT EXISTS avatar_gradient TEXT;
