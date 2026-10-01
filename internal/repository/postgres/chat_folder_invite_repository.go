package postgres

import (
	"context"
	"errors"
	"strings"

	"combox-backend/internal/service/chat"

	"github.com/jackc/pgx/v5"
)

const chatFolderInviteSelect = `
	SELECT id::text,
	       folder_id::text,
	       token,
	       created_by::text,
	       created_at,
	       revoked_at,
	       use_count
	FROM chat_folder_invites
`

// GetActiveChatFolderInvite returns the live (revoked_at IS NULL) invite of a
// folder, or chat.ErrChatFolderInviteNotFound when there is none.
func (r *ChatFolderRepository) GetActiveChatFolderInvite(ctx context.Context, folderID string) (chat.ChatFolderInvite, error) {
	query := chatFolderInviteSelect + `WHERE folder_id = $1::uuid AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 1`
	item, err := scanChatFolderInvite(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(folderID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.ChatFolderInvite{}, chat.ErrChatFolderInviteNotFound
		}
		return chat.ChatFolderInvite{}, err
	}
	return item, nil
}

// CreateChatFolderInvite mints a live invite for a folder. A UNIQUE collision
// (token reuse, or a concurrent create racing the single-active-invite partial
// index) surfaces as chat.ErrChatFolderInviteTaken so the service can re-read
// the active invite or retry with a fresh token.
func (r *ChatFolderRepository) CreateChatFolderInvite(ctx context.Context, folderID, createdBy, token string) (chat.ChatFolderInvite, error) {
	const query = `
		INSERT INTO chat_folder_invites (folder_id, created_by, token)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text, folder_id::text, token, created_by::text, created_at, revoked_at, use_count
	`
	item, err := scanChatFolderInvite(r.client.pool.QueryRow(
		ctx,
		query,
		strings.TrimSpace(folderID),
		strings.TrimSpace(createdBy),
		strings.TrimSpace(token),
	))
	if err != nil {
		if isUniqueViolation(err) {
			return chat.ChatFolderInvite{}, chat.ErrChatFolderInviteTaken
		}
		return chat.ChatFolderInvite{}, err
	}
	return item, nil
}

// RevokeActiveChatFolderInvite stamps revoked_at=now() on the live invite of a
// folder. With no live invite it reports chat.ErrChatFolderInviteNotFound.
func (r *ChatFolderRepository) RevokeActiveChatFolderInvite(ctx context.Context, folderID string) error {
	const query = `
		UPDATE chat_folder_invites
		SET revoked_at = now()
		WHERE folder_id = $1::uuid AND revoked_at IS NULL
	`
	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(folderID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return chat.ErrChatFolderInviteNotFound
	}
	return nil
}

// GetChatFolderInviteByToken loads an invite by token regardless of owner or
// revocation state; the service maps revoked rows to 404.
func (r *ChatFolderRepository) GetChatFolderInviteByToken(ctx context.Context, token string) (chat.ChatFolderInvite, error) {
	query := chatFolderInviteSelect + `WHERE token = $1 LIMIT 1`
	item, err := scanChatFolderInvite(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(token)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.ChatFolderInvite{}, chat.ErrChatFolderInviteNotFound
		}
		return chat.ChatFolderInvite{}, err
	}
	return item, nil
}

// GetChatFolderByID loads a folder by id without an owner scope, for invite
// resolve (the resolver is usually not the owner). A missing folder reports
// chat.ErrChatFolderNotFound.
func (r *ChatFolderRepository) GetChatFolderByID(ctx context.Context, folderID string) (chat.ChatFolder, error) {
	query := chatFolderSelect + `
		WHERE f.id = $1::uuid
		GROUP BY f.id
	`
	item, err := scanChatFolder(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(folderID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.ChatFolder{}, chat.ErrChatFolderNotFound
		}
		return chat.ChatFolder{}, err
	}
	return item, nil
}

// IncrementChatFolderInviteUse bumps use_count; resolve calls it best effort.
func (r *ChatFolderRepository) IncrementChatFolderInviteUse(ctx context.Context, inviteID string) error {
	const query = `UPDATE chat_folder_invites SET use_count = use_count + 1 WHERE id = $1::uuid`
	_, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(inviteID))
	return err
}

func scanChatFolderInvite(row rowScanner) (chat.ChatFolderInvite, error) {
	var item chat.ChatFolderInvite
	if err := row.Scan(&item.ID, &item.FolderID, &item.Token, &item.CreatedBy, &item.CreatedAt, &item.RevokedAt, &item.UseCount); err != nil {
		return chat.ChatFolderInvite{}, err
	}
	return item, nil
}
