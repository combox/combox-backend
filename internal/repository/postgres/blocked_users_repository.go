package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"combox-backend/internal/service/blocks"

	"github.com/jackc/pgx/v5/pgconn"
)

// BlockedUsersRepository stores the directed block edges of migration
// 000046_blocked_users (table blocked_users).
type BlockedUsersRepository struct {
	client *Client
}

func NewBlockedUsersRepository(client *Client) *BlockedUsersRepository {
	return &BlockedUsersRepository{client: client}
}

// List returns every user blocked by owner, oldest first.
func (r *BlockedUsersRepository) List(ctx context.Context, ownerID string) ([]blocks.Entry, error) {
	const query = `
		SELECT blocked_id::text, created_at
		FROM blocked_users
		WHERE owner_id = $1::uuid
		ORDER BY created_at ASC
	`
	rows, err := r.client.pool.Query(ctx, query, strings.TrimSpace(ownerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]blocks.Entry, 0, 8)
	for rows.Next() {
		var entry blocks.Entry
		var createdAt time.Time
		if err := rows.Scan(&entry.UserID, &createdAt); err != nil {
			return nil, err
		}
		entry.CreatedAt = createdAt.UTC()
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Block inserts the edge. A missing target user surfaces as
// blocks.ErrTargetNotFound (foreign key); re-blocking the same user keeps the
// original created_at and returns the entry.
func (r *BlockedUsersRepository) Block(ctx context.Context, ownerID, blockedID string) (blocks.Entry, error) {
	const query = `
		INSERT INTO blocked_users (owner_id, blocked_id)
		VALUES ($1::uuid, $2::uuid)
		ON CONFLICT (owner_id, blocked_id) DO UPDATE
		SET created_at = blocked_users.created_at
		RETURNING blocked_id::text, created_at
	`
	var (
		entry     blocks.Entry
		createdAt time.Time
	)
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(ownerID), strings.TrimSpace(blockedID)).Scan(&entry.UserID, &createdAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return blocks.Entry{}, blocks.ErrTargetNotFound
		}
		return blocks.Entry{}, err
	}
	entry.CreatedAt = createdAt.UTC()
	return entry, nil
}

// Unblock deletes the edge; a missing edge is blocks.ErrNotBlocked.
func (r *BlockedUsersRepository) Unblock(ctx context.Context, ownerID, blockedID string) error {
	const query = `
		DELETE FROM blocked_users
		WHERE owner_id = $1::uuid AND blocked_id = $2::uuid
	`
	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(ownerID), strings.TrimSpace(blockedID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return blocks.ErrNotBlocked
	}
	return nil
}
