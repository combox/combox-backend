package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	authsvc "combox-backend/internal/service/auth"

	"github.com/jackc/pgx/v5"
)

type AuthSessionRepository struct {
	client *Client
}

// sessionColumns is the projection shared by every session read; the two
// nullable columns are coalesced so the struct can keep plain strings.
const sessionColumns = `id::text, user_id::text, refresh_token_hash,
		COALESCE(user_agent, '') AS user_agent,
		COALESCE(ip_address, '') AS ip_address,
		expires_at, created_at`

func NewAuthSessionRepository(client *Client) *AuthSessionRepository {
	return &AuthSessionRepository{client: client}
}

func (r *AuthSessionRepository) Create(ctx context.Context, input authsvc.CreateSessionInput) (authsvc.Session, error) {
	const query = `
		INSERT INTO sessions (id, user_id, refresh_token_hash, user_agent, ip_address, expires_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
		RETURNING ` + sessionColumns + `
	`

	var session authsvc.Session
	err := r.client.pool.QueryRow(
		ctx,
		query,
		input.ID,
		input.UserID,
		input.RefreshTokenHash,
		nullIfEmpty(input.UserAgent),
		nullIfEmpty(input.IPAddress),
		input.ExpiresAt,
	).Scan(
		&session.ID,
		&session.UserID,
		&session.RefreshTokenHash,
		&session.UserAgent,
		&session.IPAddress,
		&session.ExpiresAt,
		&session.CreatedAt,
	)
	if err != nil {
		return authsvc.Session{}, err
	}
	return session, nil
}

func (r *AuthSessionRepository) FindByID(ctx context.Context, sessionID string) (authsvc.Session, error) {
	const query = `
		SELECT ` + sessionColumns + `
		FROM sessions
		WHERE id = $1::uuid
		LIMIT 1
	`

	var session authsvc.Session
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(sessionID)).Scan(
		&session.ID,
		&session.UserID,
		&session.RefreshTokenHash,
		&session.UserAgent,
		&session.IPAddress,
		&session.ExpiresAt,
		&session.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authsvc.Session{}, authsvc.ErrSessionNotFound
		}
		return authsvc.Session{}, err
	}
	return session, nil
}

func (r *AuthSessionRepository) UpdateRefresh(ctx context.Context, sessionID, refreshTokenHash string, expiresAt time.Time) error {
	const query = `
		UPDATE sessions
		SET refresh_token_hash = $2, expires_at = $3
		WHERE id = $1::uuid
	`
	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(sessionID), refreshTokenHash, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return authsvc.ErrSessionNotFound
	}
	return nil
}

func (r *AuthSessionRepository) DeleteByID(ctx context.Context, sessionID string) error {
	const query = `DELETE FROM sessions WHERE id = $1::uuid`
	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(sessionID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return authsvc.ErrSessionNotFound
	}
	return nil
}

// ListByUserID returns every session of a user, newest first. Expired rows are
// returned too: the service is the single place that decides what "active"
// means.
func (r *AuthSessionRepository) ListByUserID(ctx context.Context, userID string) ([]authsvc.Session, error) {
	const query = `
		SELECT ` + sessionColumns + `
		FROM sessions
		WHERE user_id = $1::uuid
		ORDER BY created_at DESC, id
	`
	rows, err := r.client.pool.Query(ctx, query, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := []authsvc.Session{}
	for rows.Next() {
		var session authsvc.Session
		if err := rows.Scan(
			&session.ID,
			&session.UserID,
			&session.RefreshTokenHash,
			&session.UserAgent,
			&session.IPAddress,
			&session.ExpiresAt,
			&session.CreatedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// DeleteByUserIDAndID revokes one session; the user_id predicate means a
// session of another account is indistinguishable from a missing one.
func (r *AuthSessionRepository) DeleteByUserIDAndID(ctx context.Context, userID, sessionID string) error {
	const query = `
		DELETE FROM sessions
		WHERE id = $1::uuid AND user_id = $2::uuid
	`
	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(sessionID), strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return authsvc.ErrSessionNotFound
	}
	return nil
}

// DeleteOthersByUserID revokes every session of a user except keepSessionID.
// An empty keepSessionID keeps nothing. It returns how many rows were deleted.
func (r *AuthSessionRepository) DeleteOthersByUserID(ctx context.Context, userID, keepSessionID string) (int64, error) {
	keep := strings.TrimSpace(keepSessionID)
	query := `
		DELETE FROM sessions
		WHERE user_id = $1::uuid
	`
	args := []any{strings.TrimSpace(userID)}
	if keep != "" {
		query += ` AND id <> $2::uuid`
		args = append(args, keep)
	}
	tag, err := r.client.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func nullIfEmpty(v string) any {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
