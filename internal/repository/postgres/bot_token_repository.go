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

func (r *BotTokenRepository) EnsureUserBot(ctx context.Context, ownerUserID, name string) (botauthsvc.Bot, error) {
	const query = `
		INSERT INTO bots (owner_user_id, actor_user_id, kind, name, is_system)
		VALUES ($1::uuid, $1::uuid, 'user', NULLIF($2, ''), FALSE)
		ON CONFLICT (owner_user_id, kind)
		DO UPDATE SET
			name = COALESCE(NULLIF(EXCLUDED.name, ''), bots.name),
			updated_at = NOW()
		RETURNING id::text, owner_user_id::text, actor_user_id::text
	`
	var out botauthsvc.Bot
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(ownerUserID), strings.TrimSpace(name)).
		Scan(&out.ID, &out.OwnerUserID, &out.ActorUserID)
	if err != nil {
		return botauthsvc.Bot{}, err
	}
	return out, nil
}

func (r *BotTokenRepository) Create(ctx context.Context, input botauthsvc.CreateTokenRecordInput) (botauthsvc.StoredToken, error) {
	const query = `
		INSERT INTO bot_tokens (bot_id, name, secret_hash, scopes, chat_ids, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		RETURNING id::text, bot_id::text, secret_hash, scopes, chat_ids, is_revoked, expires_at
	`

	var out botauthsvc.StoredToken
	err := r.client.pool.QueryRow(
		ctx,
		query,
		strings.TrimSpace(input.BotID),
		nullIfEmpty(input.Name),
		strings.TrimSpace(input.SecretHash),
		input.Scopes,
		input.ChatIDs,
		input.ExpiresAt,
	).Scan(
		&out.ID,
		&out.BotID,
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
		SELECT bt.id::text,
		       bt.bot_id::text,
		       COALESCE(b.owner_user_id::text, ''),
		       COALESCE(b.actor_user_id::text, ''),
		       bt.secret_hash,
		       bt.scopes,
		       bt.chat_ids,
		       bt.is_revoked,
		       bt.expires_at
		FROM bot_tokens bt
		INNER JOIN bots b ON b.id = bt.bot_id
		WHERE bt.id = $1::uuid
		  AND bt.is_revoked = FALSE
		  AND (bt.expires_at IS NULL OR bt.expires_at > NOW())
		LIMIT 1
	`

	var out botauthsvc.StoredToken
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(id)).Scan(
		&out.ID,
		&out.BotID,
		&out.OwnerUserID,
		&out.ActorUserID,
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
