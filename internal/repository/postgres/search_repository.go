package postgres

import (
	"context"
	"strings"

	searchsvc "combox-backend/internal/service/search"
)

type SearchRepository struct {
	client *Client
}

func NewSearchRepository(client *Client) *SearchRepository {
	return &SearchRepository{client: client}
}

func (r *SearchRepository) SearchUsers(ctx context.Context, q string, limit int) ([]searchsvc.UserResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if strings.HasPrefix(q, "@") {
		handle := strings.TrimSpace(strings.TrimPrefix(q, "@"))
		if handle == "" {
			return nil, nil
		}
		if limit <= 0 {
			limit = 20
		}
		if limit > 50 {
			limit = 50
		}
		pattern := strings.ToLower(handle) + "%"
		const query = `
			SELECT id::text,
			       email,
			       username,
			       COALESCE(first_name, ''),
			       last_name,
			       birth_date::text,
			       avatar_data_url,
			       avatar_gradient
			FROM users
			WHERE LOWER(username) LIKE $1
			ORDER BY username ASC
			LIMIT $2
		`

		rows, err := r.client.pool.Query(ctx, query, pattern, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		out := make([]searchsvc.UserResult, 0)
		for rows.Next() {
			var item searchsvc.UserResult
			if err := rows.Scan(&item.ID, &item.Email, &item.Username, &item.FirstName, &item.LastName, &item.BirthDate, &item.AvatarDataURL, &item.AvatarGradient); err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return out, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	pattern := "%" + strings.ToLower(q) + "%"
	const query = `
		SELECT id::text,
		       email,
		       username,
		       COALESCE(first_name, ''),
		       last_name,
		       birth_date::text,
		       avatar_data_url,
		       avatar_gradient
		FROM users
		WHERE LOWER(username) LIKE $1
		   OR LOWER(email) LIKE $1
		   OR LOWER(COALESCE(first_name, '')) LIKE $1
		   OR LOWER(COALESCE(last_name, '')) LIKE $1
		ORDER BY username ASC
		LIMIT $2
	`

	rows, err := r.client.pool.Query(ctx, query, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]searchsvc.UserResult, 0)
	for rows.Next() {
		var item searchsvc.UserResult
		if err := rows.Scan(&item.ID, &item.Email, &item.Username, &item.FirstName, &item.LastName, &item.BirthDate, &item.AvatarDataURL, &item.AvatarGradient); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *SearchRepository) SearchPublicChats(ctx context.Context, q string, limit int) ([]searchsvc.ChatResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if strings.HasPrefix(q, "@") {
		handle := strings.TrimSpace(strings.TrimPrefix(q, "@"))
		if handle == "" {
			return nil, nil
		}
		if limit <= 0 {
			limit = 20
		}
		if limit > 50 {
			limit = 50
		}
		pattern := strings.ToLower(handle) + "%"
		const query = `
			SELECT c.id::text,
			       COALESCE(pc.title, c.title),
			       CASE WHEN pc.chat_id IS NOT NULL THEN 'standalone_channel' ELSE c.chat_kind END,
			       COALESCE(pc.public_slug, c.public_slug),
			       COALESCE(pc.avatar_data_url, c.avatar_data_url),
			       COALESCE(pc.avatar_gradient, c.avatar_gradient)
			FROM chats c
			LEFT JOIN standalone_channels pc ON pc.chat_id = c.id
			WHERE (
			       (pc.chat_id IS NOT NULL AND pc.is_public = TRUE)
			       OR (pc.chat_id IS NULL AND c.is_public = TRUE AND c.chat_kind IN ('group', 'channel'))
			      )
			  AND LOWER(COALESCE(pc.public_slug, c.public_slug, '')) LIKE $1
			ORDER BY c.created_at DESC
			LIMIT $2
		`

		rows, err := r.client.pool.Query(ctx, query, pattern, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		out := make([]searchsvc.ChatResult, 0)
		for rows.Next() {
			var item searchsvc.ChatResult
			if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.PublicSlug, &item.AvatarDataURL, &item.AvatarGradient); err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return out, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	pattern := "%" + strings.ToLower(q) + "%"
	const query = `
		SELECT c.id::text,
		       COALESCE(pc.title, c.title),
		       CASE WHEN pc.chat_id IS NOT NULL THEN 'standalone_channel' ELSE c.chat_kind END,
		       COALESCE(pc.public_slug, c.public_slug),
		       COALESCE(pc.avatar_data_url, c.avatar_data_url),
		       COALESCE(pc.avatar_gradient, c.avatar_gradient)
		FROM chats c
		LEFT JOIN standalone_channels pc ON pc.chat_id = c.id
		WHERE (
		       (pc.chat_id IS NOT NULL AND pc.is_public = TRUE)
		       OR (pc.chat_id IS NULL AND c.is_public = TRUE AND c.chat_kind IN ('group', 'channel'))
		      )
		  AND (
		       LOWER(COALESCE(pc.title, c.title)) LIKE $1
		       OR LOWER(COALESCE(pc.public_slug, c.public_slug, '')) LIKE $1
		  )
		ORDER BY c.created_at DESC
		LIMIT $2
	`

	rows, err := r.client.pool.Query(ctx, query, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]searchsvc.ChatResult, 0)
	for rows.Next() {
		var item searchsvc.ChatResult
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.PublicSlug, &item.AvatarDataURL, &item.AvatarGradient); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
