package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	botauthsvc "combox-backend/internal/service/botauth"

	"github.com/jackc/pgx/v5"
)

type BotTokenRepository struct {
	client *Client
}

func NewBotTokenRepository(client *Client) *BotTokenRepository {
	return &BotTokenRepository{client: client}
}

func (r *BotTokenRepository) Create(ctx context.Context, input botauthsvc.CreateTokenRecordInput) (botauthsvc.StoredToken, error) {
	const query = `
		INSERT INTO bot_tokens (bot_user_id, name, secret_hash, scopes, chat_ids, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		RETURNING id::text, bot_user_id::text, secret_hash, scopes, chat_ids, is_revoked, expires_at
	`

	var out botauthsvc.StoredToken
	err := r.client.pool.QueryRow(
		ctx,
		query,
		strings.TrimSpace(input.BotUserID),
		nullIfEmpty(input.Name),
		strings.TrimSpace(input.SecretHash),
		input.Scopes,
		input.ChatIDs,
		input.ExpiresAt,
	).Scan(
		&out.ID,
		&out.BotUserID,
		&out.SecretHash,
		&out.Scopes,
		&out.ChatIDs,
		&out.IsRevoked,
		&out.ExpiresAt,
	)
	if err != nil {
		return botauthsvc.StoredToken{}, err
	}
	return out, nil
}

func (r *BotTokenRepository) FindActiveByID(ctx context.Context, id string) (botauthsvc.StoredToken, error) {
	const query = `
		SELECT id::text, bot_user_id::text, secret_hash, scopes, chat_ids, is_revoked, expires_at
		FROM bot_tokens
		WHERE id = $1::uuid
		  AND is_revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
		LIMIT 1
	`

	var out botauthsvc.StoredToken
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(id)).Scan(
		&out.ID,
		&out.BotUserID,
		&out.SecretHash,
		&out.Scopes,
		&out.ChatIDs,
		&out.IsRevoked,
		&out.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return botauthsvc.StoredToken{}, botauthsvc.ErrTokenNotFound
		}
		return botauthsvc.StoredToken{}, err
	}
	return out, nil
}

func (r *BotTokenRepository) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	const query = `
		UPDATE bot_tokens
		SET last_used_at = $2
		WHERE id = $1::uuid
	`
	_, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(id), at.UTC())
	return err
}
