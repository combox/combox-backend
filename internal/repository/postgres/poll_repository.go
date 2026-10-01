package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"combox-backend/internal/service/chat"

	"github.com/jackc/pgx/v5"
)

// PollRepository stores polls and ballots (migration 000037_polls).
type PollRepository struct {
	client *Client
}

func NewPollRepository(client *Client) *PollRepository {
	return &PollRepository{client: client}
}

const pollColumns = `
	id::text,
	chat_id::text,
	message_id::text,
	question,
	description,
	options::text,
	show_who_voted,
	multiple,
	allow_add_options,
	allow_revoting,
	shuffle_options,
	array_to_json(correct_option_ids)::text,
	explanation,
	closes_at,
	hide_results,
	is_closed,
	created_by::text,
	created_at
`

type pollScanner interface {
	Scan(dest ...any) error
}

func scanPoll(row pollScanner) (chat.Poll, error) {
	var out chat.Poll
	var (
		description *string
		rawOptions  string
		rawCorrect  string
		explanation *string
		closesAt    *time.Time
		correctIDs  []string
	)
	if err := row.Scan(
		&out.ID,
		&out.ChatID,
		&out.MessageID,
		&out.Question,
		&description,
		&rawOptions,
		&out.ShowWhoVoted,
		&out.Multiple,
		&out.AllowAddOptions,
		&out.AllowRevoting,
		&out.ShuffleOptions,
		&rawCorrect,
		&explanation,
		&closesAt,
		&out.HideResults,
		&out.IsClosed,
		&out.CreatedBy,
		&out.CreatedAt,
	); err != nil {
		return chat.Poll{}, err
	}
	if err := json.Unmarshal([]byte(rawOptions), &out.Options); err != nil {
		return chat.Poll{}, err
	}
	if out.Options == nil {
		out.Options = []chat.PollOption{}
	}
	correctIDs, err := decodeUUIDArray(rawCorrect)
	if err != nil {
		return chat.Poll{}, err
	}
	out.CorrectOptionIDs = correctIDs
	out.Description = description
	out.Explanation = explanation
	out.ClosesAt = closesAt
	out.MyOptionIDs = []string{}
	return out, nil
}

func (r *PollRepository) CreatePoll(ctx context.Context, poll chat.Poll) (chat.Poll, error) {
	rawOptions, err := json.Marshal(poll.Options)
	if err != nil {
		return chat.Poll{}, err
	}
	if poll.CorrectOptionIDs == nil {
		poll.CorrectOptionIDs = []string{}
	}
	if poll.CreatedAt.IsZero() {
		poll.CreatedAt = time.Now().UTC()
	}
	const query = `
		INSERT INTO polls (
			chat_id, message_id, question, description, options,
			show_who_voted, multiple, allow_add_options, allow_revoting, shuffle_options,
			correct_option_ids, explanation, closes_at, hide_results, is_closed,
			created_by, created_at
		)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7, $8, $9, $10, $11::text[]::uuid[], $12, $13::timestamptz, $14, $15, $16::uuid, $17::timestamptz)
		RETURNING ` + pollColumns
	row := r.client.pool.QueryRow(ctx, query,
		strings.TrimSpace(poll.ChatID),
		strings.TrimSpace(poll.MessageID),
		poll.Question,
		poll.Description,
		string(rawOptions),
		poll.ShowWhoVoted,
		poll.Multiple,
		poll.AllowAddOptions,
		poll.AllowRevoting,
		poll.ShuffleOptions,
		poll.CorrectOptionIDs,
		poll.Explanation,
		poll.ClosesAt,
		poll.HideResults,
		poll.IsClosed,
		strings.TrimSpace(poll.CreatedBy),
		poll.CreatedAt,
	)
	created, err := scanPoll(row)
	if err != nil {
		return chat.Poll{}, err
	}
	return created, nil
}

func (r *PollRepository) GetPoll(ctx context.Context, pollID string) (chat.Poll, error) {
	const query = `SELECT ` + pollColumns + `
		FROM polls
		WHERE id = $1::uuid
		LIMIT 1
	`
	poll, err := scanPoll(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(pollID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.Poll{}, chat.ErrPollNotFound
		}
		return chat.Poll{}, err
	}
	return poll, nil
}

func (r *PollRepository) GetPollsByMessageIDs(ctx context.Context, messageIDs []string) ([]chat.Poll, error) {
	ids := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	const query = `SELECT ` + pollColumns + `
		FROM polls
		WHERE message_id::text = ANY($1::text[])
	`
	rows, err := r.client.pool.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]chat.Poll, 0, len(ids))
	for rows.Next() {
		poll, err := scanPoll(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, poll)
	}
	return out, rows.Err()
}

func (r *PollRepository) ListVotesForPolls(ctx context.Context, pollIDs []string) (map[string][]chat.PollVote, error) {
	out := make(map[string][]chat.PollVote, len(pollIDs))
	ids := make([]string, 0, len(pollIDs))
	for _, id := range pollIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	const query = `
		SELECT poll_id::text,
		       user_id::text,
		       array_to_json(option_ids)::text,
		       voted_at
		FROM poll_votes
		WHERE poll_id::text = ANY($1::text[])
	`
	rows, err := r.client.pool.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pollID string
		var vote chat.PollVote
		var rawOptions string
		if err := rows.Scan(&pollID, &vote.UserID, &rawOptions, &vote.VotedAt); err != nil {
			return nil, err
		}
		optionIDs, err := decodeUUIDArray(rawOptions)
		if err != nil {
			return nil, err
		}
		vote.OptionIDs = optionIDs
		out[pollID] = append(out[pollID], vote)
	}
	return out, rows.Err()
}

// UpsertVote stores one ballot per (poll, voter); a re-vote overwrites the
// previous choice.
func (r *PollRepository) UpsertVote(ctx context.Context, pollID, userID string, optionIDs []string, at time.Time) error {
	if optionIDs == nil {
		optionIDs = []string{}
	}
	const query = `
		INSERT INTO poll_votes (poll_id, user_id, option_ids, voted_at)
		VALUES ($1::uuid, $2::uuid, $3::text[]::uuid[], $4::timestamptz)
		ON CONFLICT (poll_id, user_id)
		DO UPDATE SET option_ids = EXCLUDED.option_ids, voted_at = EXCLUDED.voted_at
	`
	_, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(pollID), strings.TrimSpace(userID), optionIDs, at)
	return err
}

// ClosePoll marks the poll as stopped early (a past closes_at ends it lazily
// without touching this flag).
func (r *PollRepository) ClosePoll(ctx context.Context, pollID string) error {
	const query = `
		UPDATE polls
		SET is_closed = TRUE
		WHERE id = $1::uuid
	`
	_, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(pollID))
	return err
}
