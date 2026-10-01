-- Per-chat wallpaper (Telegram style background).
--
-- The chat's own background, stored on the chat row so that it is fetched with
-- the chat list in the same query (no extra round trip).
--
-- wallpaper_kind: NULL/'none' = no wallpaper, 'preset' = built-in preset id,
--                 wallpaper_value = "<preset-id>".
--                 'image' = custom background, wallpaper_value = data URL,
--                 http(s) URL or object ("s3key:...") reference.
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS wallpaper_kind TEXT CHECK (wallpaper_kind IS NULL OR wallpaper_kind IN ('none', 'preset', 'image')),
    ADD COLUMN IF NOT EXISTS wallpaper_value TEXT;
