DROP INDEX IF EXISTS idx_poll_votes_poll_id;

DROP TABLE IF EXISTS poll_votes;

DROP INDEX IF EXISTS idx_polls_closes_at;
DROP INDEX IF EXISTS idx_polls_chat_id;
DROP INDEX IF EXISTS idx_polls_message_id;

DROP TABLE IF EXISTS polls;
