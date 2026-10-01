package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"combox-backend/internal/service/privacy"
)

// PrivacyRepository stores the Telegram-style privacy settings of a user.
type PrivacyRepository struct {
	client *Client
}

func NewPrivacyRepository(client *Client) *PrivacyRepository {
	return &PrivacyRepository{client: client}
}

// GetAll returns every stored row of one user. Parameters without a row are
// absent on purpose: the service falls back to the documented default rule
// instead of materialising it.
func (r *PrivacyRepository) GetAll(ctx context.Context, userID string) ([]privacy.Row, error) {
	const query = `
		SELECT param,
		       rule,
		       array_to_json(allow_ids)::text,
		       array_to_json(deny_ids)::text
		FROM privacy_settings
		WHERE user_id = $1::uuid
		ORDER BY param
	`
	rows, err := r.client.pool.Query(ctx, query, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]privacy.Row, 0, 8)
	for rows.Next() {
		var (
			row       privacy.Row
			allowJSON string
			denyJSON  string
		)
		if err := rows.Scan(&row.Param, &row.Rule, &allowJSON, &denyJSON); err != nil {
			return nil, err
		}
		row.AllowIDs, err = decodeUUIDArray(allowJSON)
		if err != nil {
			return nil, err
		}
		row.DenyIDs, err = decodeUUIDArray(denyJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Upsert writes one (user, param) row, replacing the previous rule and both
// exception lists.
func (r *PrivacyRepository) Upsert(ctx context.Context, userID, param, rule string, allowIDs, denyIDs []string) error {
	const query = `
		INSERT INTO privacy_settings (user_id, param, rule, allow_ids, deny_ids)
		VALUES ($1::uuid, $2, $3, $4::text[]::uuid[], $5::text[]::uuid[])
		ON CONFLICT (user_id, param) DO UPDATE
		SET rule = EXCLUDED.rule,
		    allow_ids = EXCLUDED.allow_ids,
		    deny_ids = EXCLUDED.deny_ids,
		    updated_at = NOW()
	`
	if allowIDs == nil {
		allowIDs = []string{}
	}
	if denyIDs == nil {
		denyIDs = []string{}
	}
	_, err := r.client.pool.Exec(
		ctx,
		query,
		strings.TrimSpace(userID),
		strings.TrimSpace(param),
		strings.TrimSpace(rule),
		allowIDs,
		denyIDs,
	)
	return err
}

// AreDirectContacts reports whether the two users share a direct chat. This
// is the "my contacts" fallback: the codebase has no dedicated contact list,
// so a 1-on-1 chat (standard or secret) with no third member and no banned
// side counts as a contact relation.
func (r *PrivacyRepository) AreDirectContacts(ctx context.Context, userAID, userBID string) (bool, error) {
	const query = `
		SELECT EXISTS(
			SELECT 1
			FROM chats c
			JOIN chat_members cm_a ON cm_a.chat_id = c.id AND cm_a.user_id = $1::uuid
			JOIN chat_members cm_b ON cm_b.chat_id = c.id AND cm_b.user_id = $2::uuid
			WHERE c.is_direct = TRUE
			  AND cm_a.role <> 'banned'
			  AND cm_b.role <> 'banned'
			  AND NOT EXISTS (
			    SELECT 1
			    FROM chat_members cm_x
			    WHERE cm_x.chat_id = c.id
			      AND cm_x.user_id NOT IN ($1::uuid, $2::uuid)
			  )
		)
	`
	var exists bool
	if err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userAID), strings.TrimSpace(userBID)).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func decodeUUIDArray(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}
