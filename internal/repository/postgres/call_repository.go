package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"combox-backend/internal/calls"

	"github.com/jackc/pgx/v5"
)

// CallRepository persists call sessions and their participants.
type CallRepository struct {
	client *Client
}

func NewCallRepository(client *Client) *CallRepository {
	return &CallRepository{client: client}
}

const callColumns = `id::text, chat_id::text, kind, topology, e2ee, started_by::text,
	started_at, ended_at, end_reason, max_participants`

func scanCall(row pgx.Row) (calls.CallRecord, error) {
	var rec calls.CallRecord
	err := row.Scan(
		&rec.ID, &rec.ChatID, &rec.Kind, &rec.Topology, &rec.E2EE, &rec.StartedBy,
		&rec.StartedAt, &rec.EndedAt, &rec.EndReason, &rec.MaxParticipants,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return calls.CallRecord{}, calls.ErrCallNotFound
	}
	if err != nil {
		return calls.CallRecord{}, err
	}
	return rec, nil
}

func (r *CallRepository) CreateCall(ctx context.Context, call calls.CallRecord) error {
	const q = `
		INSERT INTO call_sessions (id, chat_id, kind, topology, e2ee, started_by, started_at, max_participants)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8)
		ON CONFLICT (id) DO NOTHING`
	_, err := r.client.pool.Exec(ctx, q,
		call.ID, call.ChatID, string(call.Kind), string(call.Topology), call.E2EE,
		call.StartedBy, call.StartedAt, call.MaxParticipants,
	)
	return err
}

func (r *CallRepository) GetCall(ctx context.Context, callID string) (calls.CallRecord, error) {
	const q = `SELECT ` + callColumns + ` FROM call_sessions WHERE id = $1::uuid`
	return scanCall(r.client.pool.QueryRow(ctx, q, strings.TrimSpace(callID)))
}

func (r *CallRepository) GetActiveCallByChat(ctx context.Context, chatID string) (calls.CallRecord, error) {
	const q = `
		SELECT ` + callColumns + `
		FROM call_sessions
		WHERE chat_id = $1::uuid AND ended_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1`
	return scanCall(r.client.pool.QueryRow(ctx, q, strings.TrimSpace(chatID)))
}

func (r *CallRepository) EndCall(ctx context.Context, callID string, endedAt time.Time, reason string) error {
	const q = `
		UPDATE call_sessions
		SET ended_at = $2, end_reason = $3
		WHERE id = $1::uuid AND ended_at IS NULL`
	if _, err := r.client.pool.Exec(ctx, q, strings.TrimSpace(callID), endedAt, strings.TrimSpace(reason)); err != nil {
		return err
	}
	// A forced end can leave participants without left_at; close them so the
	// unique "active participant" index does not block a later rejoin.
	const parts = `
		UPDATE call_participants
		SET left_at = $2, leave_reason = $3
		WHERE call_id = $1::uuid AND left_at IS NULL`
	_, err := r.client.pool.Exec(ctx, parts, strings.TrimSpace(callID), endedAt, strings.TrimSpace(reason))
	return err
}

// DeleteCall removes a call row of the chat. Its participants go with it
// through the foreign key cascade, so the row cannot be resurrected by a
// later participant query.
func (r *CallRepository) DeleteCall(ctx context.Context, chatID, callID string) (bool, error) {
	const q = `
		DELETE FROM call_sessions
		WHERE id = $1::uuid AND chat_id = $2::uuid`
	tag, err := r.client.pool.Exec(ctx, q, strings.TrimSpace(callID), strings.TrimSpace(chatID))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *CallRepository) ListRecentCalls(ctx context.Context, chatID string, limit int) ([]calls.CallRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT ` + callColumns + `
		FROM call_sessions
		WHERE chat_id = $1::uuid
		ORDER BY started_at DESC
		LIMIT $2`
	rows, err := r.client.pool.Query(ctx, q, strings.TrimSpace(chatID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]calls.CallRecord, 0, limit)
	for rows.Next() {
		var rec calls.CallRecord
		if err := rows.Scan(
			&rec.ID, &rec.ChatID, &rec.Kind, &rec.Topology, &rec.E2EE, &rec.StartedBy,
			&rec.StartedAt, &rec.EndedAt, &rec.EndReason, &rec.MaxParticipants,
		); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ListRecentCallsWithParticipants fetches the newest calls of a chat together
// with the user ids that ever joined them, in two queries.
func (r *CallRepository) ListRecentCallsWithParticipants(ctx context.Context, chatID string, limit int) ([]calls.CallHistoryEntry, error) {
	records, err := r.ListRecentCalls(ctx, chatID, limit)
	if err != nil || len(records) == 0 {
		return nil, err
	}

	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	const q = `
		SELECT call_id::text, user_id::text
		FROM call_participants
		WHERE call_id = ANY($1::uuid[])
		ORDER BY joined_at ASC`
	rows, err := r.client.pool.Query(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCall := make(map[string][]string, len(records))
	for rows.Next() {
		var callID, userID string
		if err := rows.Scan(&callID, &userID); err != nil {
			return nil, err
		}
		byCall[callID] = append(byCall[callID], userID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries := make([]calls.CallHistoryEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, calls.CallHistoryEntry{Call: record, Participants: byCall[record.ID]})
	}
	return entries, nil
}

func (r *CallRepository) AddParticipant(ctx context.Context, rec calls.ParticipantRecord) error {
	const q = `
		INSERT INTO call_participants (call_id, user_id, device_id, role, joined_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT DO NOTHING`
	_, err := r.client.pool.Exec(ctx, q,
		rec.CallID, rec.UserID, strings.TrimSpace(rec.DeviceID), string(rec.Role), rec.JoinedAt,
	)
	return err
}

func (r *CallRepository) CloseParticipant(ctx context.Context, callID, userID, deviceID string, leftAt time.Time, reason string) error {
	const q = `
		UPDATE call_participants
		SET left_at = $4, leave_reason = $5
		WHERE call_id = $1::uuid
		  AND user_id = $2::uuid
		  AND left_at IS NULL
		  AND ($3 = '' OR device_id = $3)`
	_, err := r.client.pool.Exec(ctx, q,
		strings.TrimSpace(callID), strings.TrimSpace(userID), strings.TrimSpace(deviceID),
		leftAt, strings.TrimSpace(reason),
	)
	return err
}

func (r *CallRepository) ListParticipants(ctx context.Context, callID string) ([]calls.ParticipantRecord, error) {
	const q = `
		SELECT id::text, call_id::text, user_id::text, device_id, role, joined_at, left_at, leave_reason
		FROM call_participants
		WHERE call_id = $1::uuid
		ORDER BY joined_at ASC`
	rows, err := r.client.pool.Query(ctx, q, strings.TrimSpace(callID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]calls.ParticipantRecord, 0)
	for rows.Next() {
		var rec calls.ParticipantRecord
		if err := rows.Scan(&rec.ID, &rec.CallID, &rec.UserID, &rec.DeviceID, &rec.Role,
			&rec.JoinedAt, &rec.LeftAt, &rec.LeaveReason); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

var _ calls.Store = (*CallRepository)(nil)
var _ calls.HistoryStore = (*CallRepository)(nil)
