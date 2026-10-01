-- Telegram-style polls.
--
-- A poll is carried by an ordinary chat message: `message_id` points at the
-- message whose content holds the question, so every permission, notification
-- and preview path keeps working untouched. The poll row stores the options
-- and the aggregate state that a message cannot express.
--
-- options: JSONB array of {"id":"<uuid>","text":"..."} (2..13 entries).
-- correct_option_ids: option ids of the quiz answers ({} for a regular poll).
-- is_closed: the poll was stopped early; a past closes_at also ends the poll
--            lazily (no cron), see internal/service/chat/service_polls.go.
-- hide_results: viewers who have not voted must not see the tallies until the
--               poll is closed.
CREATE TABLE IF NOT EXISTS polls (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    question TEXT NOT NULL CHECK (length(btrim(question)) BETWEEN 1 AND 300),
    description TEXT,
    options JSONB NOT NULL CHECK (jsonb_array_length(options) BETWEEN 2 AND 13),
    show_who_voted BOOLEAN NOT NULL DEFAULT FALSE,
    multiple BOOLEAN NOT NULL DEFAULT FALSE,
    allow_add_options BOOLEAN NOT NULL DEFAULT FALSE,
    allow_revoting BOOLEAN NOT NULL DEFAULT FALSE,
    shuffle_options BOOLEAN NOT NULL DEFAULT FALSE,
    correct_option_ids UUID[] NOT NULL DEFAULT '{}',
    explanation TEXT,
    closes_at TIMESTAMPTZ,
    hide_results BOOLEAN NOT NULL DEFAULT FALSE,
    is_closed BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_polls_message_id ON polls(message_id);
CREATE INDEX IF NOT EXISTS idx_polls_chat_id ON polls(chat_id);
CREATE INDEX IF NOT EXISTS idx_polls_closes_at ON polls(closes_at) WHERE closes_at IS NOT NULL;

-- One vote row per (poll, voter): re-voting overwrites the previous choice,
-- which is how allow_revoting is implemented (a rejected re-vote never reaches
-- this table).
CREATE TABLE IF NOT EXISTS poll_votes (
    poll_id UUID NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    option_ids UUID[] NOT NULL CHECK (cardinality(option_ids) > 0),
    voted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (poll_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_poll_votes_poll_id ON poll_votes(poll_id);
