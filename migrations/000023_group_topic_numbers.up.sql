ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS topic_number INTEGER;

ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS next_topic_number INTEGER;

UPDATE chats
SET next_topic_number = 2
WHERE chat_kind = 'group'
  AND next_topic_number IS NULL;

-- Move legacy "General" channel messages back into the group root.
WITH general_channels AS (
    SELECT c.id AS channel_id, c.parent_chat_id AS group_id
    FROM chats c
    WHERE c.chat_kind = 'channel'
      AND c.parent_chat_id IS NOT NULL
      AND LOWER(TRIM(c.title)) = 'general'
)
UPDATE messages m
SET chat_id = gc.group_id,
    updated_at = NOW()
FROM general_channels gc
WHERE m.chat_id = gc.channel_id;

-- Remove legacy "General" channel rows.
DELETE FROM chats c
USING (
    SELECT id
    FROM chats
    WHERE chat_kind = 'channel'
      AND parent_chat_id IS NOT NULL
      AND LOWER(TRIM(title)) = 'general'
) g
WHERE c.id = g.id;

-- Assign topic numbers to remaining channels (2..n), ordered by creation time.
WITH numbered AS (
    SELECT
        c.id,
        c.parent_chat_id,
        (ROW_NUMBER() OVER (PARTITION BY c.parent_chat_id ORDER BY c.created_at ASC, c.id ASC) + 1) AS topic_number
    FROM chats c
    WHERE c.chat_kind = 'channel'
      AND c.parent_chat_id IS NOT NULL
)
UPDATE chats c
SET topic_number = n.topic_number
FROM numbered n
WHERE c.id = n.id
  AND c.topic_number IS NULL;

-- Set group's next_topic_number = max(topic_number) + 1 (at least 2)
WITH max_topics AS (
    SELECT parent_chat_id AS group_id, COALESCE(MAX(topic_number), 1) AS max_topic
    FROM chats
    WHERE chat_kind = 'channel'
      AND parent_chat_id IS NOT NULL
    GROUP BY parent_chat_id
)
UPDATE chats g
SET next_topic_number = GREATEST(2, mt.max_topic + 1)
FROM max_topics mt
WHERE g.id = mt.group_id
  AND g.chat_kind = 'group';

-- Enforce uniqueness of topic numbers within a group.
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_topic_number_unique
    ON chats(parent_chat_id, topic_number)
    WHERE chat_kind = 'channel' AND parent_chat_id IS NOT NULL AND topic_number IS NOT NULL;

-- Basic sanity: channel topics are >= 2.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'chk_channel_topic_number'
    ) THEN
        ALTER TABLE chats
            ADD CONSTRAINT chk_channel_topic_number
            CHECK (chat_kind <> 'channel' OR topic_number IS NULL OR topic_number >= 2);
    END IF;
END
$$;
