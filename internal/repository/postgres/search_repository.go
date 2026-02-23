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
			if err := rows.Scan(&item.ID, &item.Email, &item.Username, &item.FirstName, &item.LastName, &item.AvatarDataURL, &item.AvatarGradient); err != nil {
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
		if err := rows.Scan(&item.ID, &item.Email, &item.Username, &item.FirstName, &item.LastName, &item.AvatarDataURL, &item.AvatarGradient); err != nil {
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
			SELECT id::text,
			       title,
			       chat_kind,
			       public_slug
			FROM chats
			WHERE is_public = TRUE
			  AND chat_kind IN ('group', 'channel')
			  AND LOWER(COALESCE(public_slug, '')) LIKE $1
			ORDER BY created_at DESC
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
			if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.PublicSlug); err != nil {
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
		       title,
		       chat_kind,
		       public_slug
		FROM chats
		WHERE is_public = TRUE
		  AND chat_kind IN ('group', 'channel')
		  AND (
		       LOWER(title) LIKE $1
		       OR LOWER(COALESCE(public_slug, '')) LIKE $1
		  )
		ORDER BY created_at DESC
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
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.PublicSlug); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
