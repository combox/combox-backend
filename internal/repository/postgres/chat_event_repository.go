package postgres

import (
	"context"
	"strings"

	"combox-backend/internal/service/chat"
)

// ChatEventRepository stores the per-chat recent-actions journal
// (migration 000040_chat_events, table chat_events).
type ChatEventRepository struct {
	client *Client
}

func NewChatEventRepository(client *Client) *ChatEventRepository {
	return &ChatEventRepository{client: client}
}

// nullableUUID maps an optional user id onto a query argument: an empty value
// becomes a SQL NULL so the column stays nullable.
func nullableUUID(value *string) any {
	if value == nil {
		return nil
	}
	if id := strings.TrimSpace(*value); id != "" {
		return id
	}
	return nil
}

// RecordChatEvent appends one journal row. created_at comes from the database
// clock so the journal always sorts consistently with the index.
func (r *ChatEventRepository) RecordChatEvent(ctx context.Context, event chat.ChatEvent) error {
	const query = `
		INSERT INTO chat_events (chat_id, actor_user_id, target_user_id, event_type, payload)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5)
	`
	_, err := r.client.pool.Exec(ctx, query,
		strings.TrimSpace(event.ChatID),
		nullableUUID(event.ActorUserID),
		nullableUUID(event.TargetUserID),
		event.EventType,
		event.Payload,
	)
	return err
}

// ListChatEvents returns the newest rows first; id breaks created_at ties so
// paging stays stable inside one statement.
func (r *ChatEventRepository) ListChatEvents(ctx context.Context, chatID string, limit int) ([]chat.ChatEvent, error) {
	const query = `
		SELECT id::text, chat_id::text, actor_user_id::text, target_user_id::text, event_type, payload, created_at
		FROM chat_events
		WHERE chat_id = $1::uuid
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`
	rows, err := r.client.pool.Query(ctx, query, strings.TrimSpace(chatID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]chat.ChatEvent, 0)
	for rows.Next() {
		var item chat.ChatEvent
		var actorUserID, targetUserID *string
		if err := rows.Scan(
			&item.ID,
			&item.ChatID,
			&actorUserID,
			&targetUserID,
			&item.EventType,
			&item.Payload,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.ActorUserID = actorUserID
		item.TargetUserID = targetUserID
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
