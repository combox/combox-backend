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

func NewAuthSessionRepository(client *Client) *AuthSessionRepository {
	return &AuthSessionRepository{client: client}
}

func (r *AuthSessionRepository) Create(ctx context.Context, input authsvc.CreateSessionInput) (authsvc.Session, error) {
	const query = `
		INSERT INTO sessions (id, user_id, refresh_token_hash, user_agent, ip_address, expires_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
		RETURNING id::text, user_id::text, refresh_token_hash, expires_at
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
	).Scan(&session.ID, &session.UserID, &session.RefreshTokenHash, &session.ExpiresAt)
	if err != nil {
		return authsvc.Session{}, err
	}
	return session, nil
}

func (r *AuthSessionRepository) FindByID(ctx context.Context, sessionID string) (authsvc.Session, error) {
	const query = `
		SELECT id::text, user_id::text, refresh_token_hash, expires_at
		FROM sessions
		WHERE id = $1::uuid
		LIMIT 1
	`

	var session authsvc.Session
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(sessionID)).
		Scan(&session.ID, &session.UserID, &session.RefreshTokenHash, &session.ExpiresAt)
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

func nullIfEmpty(v string) any {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
