-- Two more Telegram-style chat settings that the UI exposes but nothing
-- persisted yet: a human readable description and a short icon shown in the
-- chat list / profile header.
--
-- description: free text, capped at 255 characters by the service layer.
-- icon_emoji:  up to 8 characters (any text, not emoji-validated) so a short
--              glyph such as "📣" or "[TV]" fits.
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS icon_emoji TEXT NOT NULL DEFAULT '';
