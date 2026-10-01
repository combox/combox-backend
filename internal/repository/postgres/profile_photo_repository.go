package postgres

import (
	"context"
	"strings"

	"combox-backend/internal/service/profilephoto"

	"github.com/google/uuid"
)

// ProfilePhotoRepository stores the avatar archive of users and chats.
type ProfilePhotoRepository struct {
	client *Client
}

func NewProfilePhotoRepository(client *Client) *ProfilePhotoRepository {
	return &ProfilePhotoRepository{client: client}
}

func (r *ProfilePhotoRepository) Add(ctx context.Context, ownerKind, ownerID, objectKey string) error {
	const query = `
		INSERT INTO profile_photos (id, owner_kind, owner_id, object_key)
		VALUES ($1::uuid, $2, $3::uuid, $4)
	`
	_, err := r.client.pool.Exec(
		ctx,
		query,
		uuid.NewString(),
		strings.TrimSpace(ownerKind),
		strings.TrimSpace(ownerID),
		strings.TrimSpace(objectKey),
	)
	return err
}

func (r *ProfilePhotoRepository) List(ctx context.Context, ownerKind, ownerID string, limit int) ([]profilephoto.Record, error) {
	if limit <= 0 {
		limit = profilephoto.DefaultListLimit
	}
	const query = `
		SELECT id::text, owner_kind, owner_id::text, object_key, created_at
		FROM profile_photos
		WHERE owner_kind = $1 AND owner_id = $2::uuid
		ORDER BY created_at DESC
		LIMIT $3
	`
	rows, err := r.client.pool.Query(ctx, query, strings.TrimSpace(ownerKind), strings.TrimSpace(ownerID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]profilephoto.Record, 0, 16)
	for rows.Next() {
		var rec profilephoto.Record
		if err := rows.Scan(&rec.ID, &rec.OwnerKind, &rec.OwnerID, &rec.ObjectKey, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
