-- Normalize legacy chat_kind values.
UPDATE chats
SET chat_kind = 'standalone_channel'
WHERE chat_kind = 'public_channel';

