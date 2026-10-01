package postgres

import (
	"context"
	"strings"

	settingsvc "combox-backend/internal/service/settings"
)

// UserSettingsRepository stores the global app settings of one user
// (migration 000042_user_settings, table user_settings).
type UserSettingsRepository struct {
	client *Client
}

func NewUserSettingsRepository(client *Client) *UserSettingsRepository {
	return &UserSettingsRepository{client: client}
}

// GetAll returns the stored key/value rows of one user. Keys the user never
// touched are absent on purpose: the service falls back to the documented
// default instead of materialising it.
func (r *UserSettingsRepository) GetAll(ctx context.Context, userID string) (map[string]string, error) {
	const query = `
		SELECT key, value
		FROM user_settings
		WHERE user_id = $1::uuid
		ORDER BY key
	`
	rows, err := r.client.pool.Query(ctx, query, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string, 8)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertMany writes every pair of values in one statement, so a partial patch
// is never half applied.
func (r *UserSettingsRepository) UpsertMany(ctx context.Context, userID string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	const query = `
		INSERT INTO user_settings (user_id, key, value)
		SELECT $1::uuid, x.key, x.value
		FROM unnest($2::text[], $3::text[]) AS x(key, value)
		ON CONFLICT (user_id, key) DO UPDATE
		SET value = EXCLUDED.value,
		    updated_at = NOW()
	`
	keys := make([]string, 0, len(values))
	stored := make([]string, 0, len(values))
	for _, key := range settingsvc.Keys() {
		value, ok := values[key]
		if !ok {
			continue
		}
		keys = append(keys, key)
		stored = append(stored, value)
	}
	if len(keys) == 0 {
		return nil
	}
	_, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(userID), keys, stored)
	return err
}
