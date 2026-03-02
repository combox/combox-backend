package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"combox-backend/internal/service/chat"

	"github.com/jackc/pgx/v5"
)

type ChatRepository struct {
	client *Client
}

func (r *MessageRepository) GetMessageMeta(ctx context.Context, messageID string) (chat.MessageMeta, error) {
	const query = `
		SELECT id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, is_e2e
		FROM messages
		WHERE id = $1::uuid
		  AND deleted_at IS NULL
		LIMIT 1
	`

	var meta chat.MessageMeta
	if err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(messageID)).Scan(&meta.ID, &meta.ChatID, &meta.UserID, &meta.SenderBotID, &meta.IsE2E); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.MessageMeta{}, chat.ErrMessageNotFound
		}
		return chat.MessageMeta{}, err
	}
	return meta, nil
}

func (r *MessageRepository) SoftDeleteMessage(ctx context.Context, chatID, messageID, deleterUserID string) error {
	const query = `
		UPDATE messages
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid
		  AND chat_id = $2::uuid
		  AND user_id = $3::uuid
		  AND is_e2e = FALSE
		  AND deleted_at IS NULL
	`

	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(messageID), strings.TrimSpace(chatID), strings.TrimSpace(deleterUserID))
	if err != nil {
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return chat.ErrChatNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return chat.ErrMessageNotFound
	}
	return nil
}

func NewChatRepository(client *Client) *ChatRepository {
	return &ChatRepository{client: client}
}

func (r *ChatRepository) CreateChat(ctx context.Context, title string, memberIDs []string, creatorID string, chatType string) (chat.Chat, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.Chat{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const insertChat = `
		INSERT INTO chats (title, is_direct, created_by, chat_type, chat_kind)
		VALUES ($1, $2, $3::uuid, $4, $5)
		RETURNING id::text, title, is_direct, chat_type, chat_kind, bot_id::text, created_at
	`

	var created chat.Chat
	isDirect := len(memberIDs) == 2
	chatKind := "group"
	if isDirect {
		chatKind = "direct"
	}
	err = tx.QueryRow(ctx, insertChat, title, isDirect, creatorID, chatType, chatKind).
		Scan(&created.ID, &created.Title, &created.IsDirect, &created.Type, &created.Kind, &created.BotID, &created.CreatedAt)
	if err != nil {
		return chat.Chat{}, fmt.Errorf("insert chat: %w", err)
	}

	const insertMember = `
		INSERT INTO chat_members (chat_id, user_id)
		VALUES ($1::uuid, $2::uuid)
		ON CONFLICT (chat_id, user_id) DO NOTHING
	`
	for _, memberID := range memberIDs {
		if _, err := tx.Exec(ctx, insertMember, created.ID, memberID); err != nil {
			return chat.Chat{}, fmt.Errorf("insert member: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return chat.Chat{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

func (r *ChatRepository) FindDirectChatByMembers(ctx context.Context, userAID, userBID, chatType string) (chat.Chat, bool, error) {
	const query = `
		SELECT c.id::text, c.title, c.is_direct, c.chat_type, c.chat_kind, c.bot_id::text, c.created_at
		FROM chats c
		JOIN chat_members cm_a ON cm_a.chat_id = c.id AND cm_a.user_id = $1::uuid
		JOIN chat_members cm_b ON cm_b.chat_id = c.id AND cm_b.user_id = $2::uuid
		WHERE c.is_direct = TRUE
		  AND c.chat_type = $3
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_members cm_extra
		    WHERE cm_extra.chat_id = c.id
		      AND cm_extra.user_id NOT IN ($1::uuid, $2::uuid)
		  )
		ORDER BY c.created_at ASC
		LIMIT 1
	`
	var found chat.Chat
	if err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userAID), strings.TrimSpace(userBID), strings.TrimSpace(chatType)).
		Scan(&found.ID, &found.Title, &found.IsDirect, &found.Type, &found.Kind, &found.BotID, &found.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.Chat{}, false, nil
		}
		return chat.Chat{}, false, err
	}
	return found, true, nil
}

func (r *ChatRepository) ListChatsByUser(ctx context.Context, userID string) ([]chat.Chat, error) {
	const query = `
		SELECT c.id::text,
		       CASE
		         WHEN c.is_direct THEN COALESCE(NULLIF(TRIM(CONCAT_WS(' ', peer.first_name, peer.last_name)), ''), peer.username, c.title)
		         ELSE c.title
		       END AS display_title,
		       c.is_direct,
		       c.chat_type,
		       c.chat_kind,
		       c.bot_id::text,
		       peer.id::text,
		       peer.avatar_data_url,
		       peer.avatar_gradient,
		       c.created_at
		FROM chats c
		INNER JOIN chat_members cm ON cm.chat_id = c.id
		LEFT JOIN LATERAL (
			SELECT u.id, u.username, u.first_name, u.last_name, u.avatar_data_url, u.avatar_gradient
			FROM chat_members cm_peer
			INNER JOIN users u ON u.id = cm_peer.user_id
			WHERE cm_peer.chat_id = c.id
			  AND cm_peer.user_id <> $1::uuid
			ORDER BY cm_peer.joined_at ASC
			LIMIT 1
		) peer ON c.is_direct = TRUE
		WHERE cm.user_id = $1::uuid
		  AND (c.is_direct = FALSE OR peer.id IS NOT NULL)
		ORDER BY c.created_at DESC
	`
	rows, err := r.client.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []chat.Chat
	for rows.Next() {
		var item chat.Chat
		if err := rows.Scan(&item.ID, &item.Title, &item.IsDirect, &item.Type, &item.Kind, &item.BotID, &item.PeerUserID, &item.AvatarURL, &item.AvatarBg, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ChatRepository) GetChat(ctx context.Context, chatID string) (chat.Chat, error) {
	const query = `
		SELECT id::text, title, is_direct, chat_type, chat_kind, bot_id::text, created_at
		FROM chats
		WHERE id = $1::uuid
		LIMIT 1
	`
	var item chat.Chat
	if err := r.client.pool.QueryRow(ctx, query, chatID).Scan(&item.ID, &item.Title, &item.IsDirect, &item.Type, &item.Kind, &item.BotID, &item.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.Chat{}, chat.ErrChatNotFound
		}
		return chat.Chat{}, err
	}
	return item, nil
}

func (r *ChatRepository) ListChatMemberIDs(ctx context.Context, chatID string) ([]string, error) {
	const query = `
		SELECT user_id::text
		FROM chat_members
		WHERE chat_id = $1::uuid
		ORDER BY joined_at ASC
	`
	rows, err := r.client.pool.Query(ctx, query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		out = append(out, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ChatRepository) IsChatMember(ctx context.Context, chatID, userID string) (bool, error) {
	const query = `
		SELECT EXISTS(
			SELECT 1 FROM chat_members
			WHERE chat_id = $1::uuid AND user_id = $2::uuid
		)
	`
	var exists bool
	if err := r.client.pool.QueryRow(ctx, query, chatID, userID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

type MessageRepository struct {
	client *Client
}

func NewMessageRepository(client *Client) *MessageRepository {
	return &MessageRepository{client: client}
}

func (r *MessageRepository) CreateMessage(ctx context.Context, chatID, userID, content string) (chat.Message, error) {
	const query = `
		INSERT INTO messages (chat_id, user_id, content)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, content, is_e2e, created_at, edited_at
	`
	var msg chat.Message
	err := r.client.pool.QueryRow(ctx, query, chatID, userID, content).
		Scan(&msg.ID, &msg.ChatID, &msg.UserID, &msg.SenderBotID, &msg.Content, &msg.IsE2E, &msg.CreatedAt, &msg.EditedAt)
	if err != nil {
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return chat.Message{}, chat.ErrChatNotFound
		}
		return chat.Message{}, err
	}
	return msg, nil
}

func (r *MessageRepository) CreateMessageAsBot(ctx context.Context, chatID, botID, content string) (chat.Message, error) {
	const query = `
		INSERT INTO messages (chat_id, sender_bot_id, content)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, content, is_e2e, created_at, edited_at
	`
	var msg chat.Message
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(chatID), strings.TrimSpace(botID), content).
		Scan(&msg.ID, &msg.ChatID, &msg.UserID, &msg.SenderBotID, &msg.Content, &msg.IsE2E, &msg.CreatedAt, &msg.EditedAt)
	if err != nil {
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return chat.Message{}, chat.ErrChatNotFound
		}
		return chat.Message{}, err
	}
	if strings.TrimSpace(msg.UserID) == "" && msg.SenderBotID != nil {
		msg.UserID = "bot:" + strings.TrimSpace(*msg.SenderBotID)
	}
	return msg, nil
}

func (r *MessageRepository) CreateMessageWithAttachments(ctx context.Context, chatID, userID, content string, attachmentIDs []string) (chat.Message, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.Message{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if len(attachmentIDs) > 0 {
		const validate = `
			SELECT COUNT(*)
			FROM attachments
			WHERE id = ANY($1::uuid[])
			  AND user_id = $2::uuid
		`
		var cnt int
		if err := tx.QueryRow(ctx, validate, attachmentIDs, strings.TrimSpace(userID)).Scan(&cnt); err != nil {
			return chat.Message{}, err
		}
		if cnt != len(attachmentIDs) {
			return chat.Message{}, chat.ErrInvalidAttachments
		}
	}

	const insertMsg = `
		INSERT INTO messages (chat_id, user_id, content)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, content, is_e2e, created_at, edited_at
	`
	var msg chat.Message
	if err := tx.QueryRow(ctx, insertMsg, strings.TrimSpace(chatID), strings.TrimSpace(userID), content).
		Scan(&msg.ID, &msg.ChatID, &msg.UserID, &msg.SenderBotID, &msg.Content, &msg.IsE2E, &msg.CreatedAt, &msg.EditedAt); err != nil {
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return chat.Message{}, chat.ErrChatNotFound
		}
		return chat.Message{}, err
	}

	if len(attachmentIDs) > 0 {
		const link = `
			INSERT INTO message_attachments (message_id, attachment_id)
			SELECT $1::uuid, unnest($2::uuid[])
			ON CONFLICT (message_id, attachment_id) DO NOTHING
		`
		if _, err := tx.Exec(ctx, link, strings.TrimSpace(msg.ID), attachmentIDs); err != nil {
			return chat.Message{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return chat.Message{}, err
	}
	return msg, nil
}

func (r *MessageRepository) CreateForwardedMessage(ctx context.Context, chatID, sourceMessageID, userID string) (chat.Message, error) {
	// Snapshot forward: copy the current content into a new message row.
	// Forwarded message must be a standard (non-e2e) message.
	const selectQuery = `
		SELECT content, is_e2e
		FROM messages
		WHERE id = $1::uuid
		  AND chat_id = $2::uuid
		  AND deleted_at IS NULL
		LIMIT 1
	`

	var content *string
	var isE2E bool
	if err := r.client.pool.QueryRow(ctx, selectQuery, sourceMessageID, chatID).Scan(&content, &isE2E); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.Message{}, chat.ErrMessageNotFound
		}
		return chat.Message{}, err
	}
	if isE2E || content == nil {
		return chat.Message{}, chat.ErrMessageNotFound
	}

	return r.CreateMessage(ctx, chatID, userID, *content)
}

func (r *MessageRepository) UpdateMessageContent(ctx context.Context, chatID, messageID, editorUserID, newContent string) (chat.Message, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.Message{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const selectMsg = `
		SELECT id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, content, is_e2e
		FROM messages
		WHERE id = $1::uuid AND chat_id = $2::uuid AND deleted_at IS NULL
		LIMIT 1
	`
	var existing chat.Message
	if err := tx.QueryRow(ctx, selectMsg, messageID, chatID).Scan(&existing.ID, &existing.ChatID, &existing.UserID, &existing.SenderBotID, &existing.Content, &existing.IsE2E); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.Message{}, chat.ErrMessageNotFound
		}
		return chat.Message{}, err
	}
	if existing.IsE2E {
		return chat.Message{}, chat.ErrMessageNotFound
	}
	if existing.UserID != strings.TrimSpace(editorUserID) {
		return chat.Message{}, chat.ErrMessageNotFound
	}

	const insertEdit = `
		INSERT INTO message_edits (message_id, editor_user_id, old_content, new_content)
		VALUES ($1::uuid, $2::uuid, $3, $4)
	`
	if _, err := tx.Exec(ctx, insertEdit, messageID, editorUserID, existing.Content, newContent); err != nil {
		return chat.Message{}, err
	}

	const updateMsg = `
		UPDATE messages
		SET content = $3,
		    edited_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1::uuid AND chat_id = $2::uuid AND deleted_at IS NULL
		RETURNING id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, content, is_e2e, created_at, edited_at
	`
	var out chat.Message
	if err := tx.QueryRow(ctx, updateMsg, messageID, chatID, newContent).Scan(&out.ID, &out.ChatID, &out.UserID, &out.SenderBotID, &out.Content, &out.IsE2E, &out.CreatedAt, &out.EditedAt); err != nil {
		return chat.Message{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return chat.Message{}, err
	}
	return out, nil
}

func (r *MessageRepository) UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string) (chat.MessageStatus, error) {
	const query = `
		INSERT INTO message_statuses (message_id, user_id, status, updated_at)
		SELECT m.id, $3::uuid, $4, NOW()
		FROM messages m
		WHERE m.id = $1::uuid AND m.chat_id = $2::uuid AND m.deleted_at IS NULL
		ON CONFLICT (message_id, user_id) DO UPDATE
		SET status = EXCLUDED.status,
		    updated_at = NOW()
		RETURNING message_id::text, $2::text, user_id::text, status, updated_at
	`

	var out chat.MessageStatus
	if err := r.client.pool.QueryRow(ctx, query, messageID, chatID, userID, status).Scan(
		&out.MessageID,
		&out.ChatID,
		&out.UserID,
		&out.Status,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.MessageStatus{}, chat.ErrMessageNotFound
		}
		return chat.MessageStatus{}, err
	}
	return out, nil
}

func (r *MessageRepository) CreateMessageE2E(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []chat.E2EEnvelope) (chat.Message, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.Message{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertMessage = `
		INSERT INTO messages (chat_id, user_id, content, is_e2e, e2e_sender_device_id)
		VALUES ($1::uuid, $2::uuid, NULL, TRUE, $3::uuid)
		RETURNING id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, content, is_e2e, e2e_sender_device_id::text, created_at
	`

	var msg chat.Message
	var senderID string
	if err := tx.QueryRow(ctx, insertMessage, chatID, userID, senderDeviceID).
		Scan(&msg.ID, &msg.ChatID, &msg.UserID, &msg.SenderBotID, &msg.Content, &msg.IsE2E, &senderID, &msg.CreatedAt); err != nil {
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return chat.Message{}, chat.ErrChatNotFound
		}
		return chat.Message{}, fmt.Errorf("insert message: %w", err)
	}

	msg.E2E = &chat.E2EPayload{SenderDeviceID: senderID}

	const insertEnvelope = `
		INSERT INTO message_envelopes (message_id, recipient_device_id, alg, header, ciphertext)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT (message_id, recipient_device_id) DO UPDATE
		SET alg = EXCLUDED.alg,
		    header = EXCLUDED.header,
		    ciphertext = EXCLUDED.ciphertext
	`
	for _, env := range envelopes {
		if _, err := tx.Exec(ctx, insertEnvelope, msg.ID, env.RecipientDeviceID, env.Alg, env.Header, env.Ciphertext); err != nil {
			return chat.Message{}, fmt.Errorf("insert envelope: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return chat.Message{}, fmt.Errorf("commit tx: %w", err)
	}
	return msg, nil
}

func (r *MessageRepository) CreateMessageE2EWithAttachments(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []chat.E2EEnvelope, attachmentIDs []string) (chat.Message, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.Message{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if len(attachmentIDs) > 0 {
		const validate = `
			SELECT COUNT(*)
			FROM attachments
			WHERE id = ANY($1::uuid[])
			  AND user_id = $2::uuid
		`
		var cnt int
		if err := tx.QueryRow(ctx, validate, attachmentIDs, strings.TrimSpace(userID)).Scan(&cnt); err != nil {
			return chat.Message{}, err
		}
		if cnt != len(attachmentIDs) {
			return chat.Message{}, chat.ErrInvalidAttachments
		}
	}

	const insertMessage = `
		INSERT INTO messages (chat_id, user_id, content, is_e2e, e2e_sender_device_id)
		VALUES ($1::uuid, $2::uuid, NULL, TRUE, $3::uuid)
		RETURNING id::text, chat_id::text, COALESCE(user_id::text, ''), sender_bot_id::text, content, is_e2e, e2e_sender_device_id::text, created_at
	`

	var msg chat.Message
	var senderID string
	if err := tx.QueryRow(ctx, insertMessage, chatID, userID, senderDeviceID).
		Scan(&msg.ID, &msg.ChatID, &msg.UserID, &msg.SenderBotID, &msg.Content, &msg.IsE2E, &senderID, &msg.CreatedAt); err != nil {
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return chat.Message{}, chat.ErrChatNotFound
		}
		return chat.Message{}, fmt.Errorf("insert message: %w", err)
	}

	msg.E2E = &chat.E2EPayload{SenderDeviceID: senderID}

	const insertEnvelope = `
		INSERT INTO message_envelopes (message_id, recipient_device_id, alg, header, ciphertext)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT (message_id, recipient_device_id) DO UPDATE
		SET alg = EXCLUDED.alg,
		    header = EXCLUDED.header,
		    ciphertext = EXCLUDED.ciphertext
	`
	for _, env := range envelopes {
		if _, err := tx.Exec(ctx, insertEnvelope, msg.ID, env.RecipientDeviceID, env.Alg, env.Header, env.Ciphertext); err != nil {
			return chat.Message{}, fmt.Errorf("insert envelope: %w", err)
		}
	}

	if len(attachmentIDs) > 0 {
		const link = `
			INSERT INTO message_attachments (message_id, attachment_id)
			SELECT $1::uuid, unnest($2::uuid[])
			ON CONFLICT (message_id, attachment_id) DO NOTHING
		`
		if _, err := tx.Exec(ctx, link, strings.TrimSpace(msg.ID), attachmentIDs); err != nil {
			return chat.Message{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return chat.Message{}, fmt.Errorf("commit tx: %w", err)
	}
	return msg, nil
}

func (r *MessageRepository) ListMessages(ctx context.Context, chatID string, limit int, cursor string) (chat.MessagePage, error) {
	const baseQuery = `
		SELECT id::text,
		       chat_id::text,
		       COALESCE(user_id::text, ''),
		       sender_bot_id::text,
		       content,
		       is_e2e,
		       e2e_sender_device_id::text,
		       created_at,
		       edited_at,
		       COALESCE((
		         SELECT json_agg(row_to_json(x))
		         FROM (
		           SELECT mr.emoji, array_agg(mr.user_id::text ORDER BY mr.updated_at DESC) AS user_ids
		           FROM message_reactions mr
		           WHERE mr.message_id = messages.id
		           GROUP BY mr.emoji
		           ORDER BY max(mr.updated_at) DESC
		         ) AS x
		       ), '[]'::json) AS reactions_json
		FROM messages
		WHERE chat_id = $1::uuid
		  AND deleted_at IS NULL
		  %s
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`

	args := []any{chatID, limit}
	condition := ""

	if strings.TrimSpace(cursor) != "" {
		cursorTS, cursorID, err := parseMessageCursor(cursor)
		if err != nil {
			return chat.MessagePage{}, err
		}
		condition = "AND (created_at, id) < ($3::timestamptz, $4::uuid)"
		args = append(args, cursorTS, cursorID)
	}

	query := fmt.Sprintf(baseQuery, condition)
	rows, err := r.client.pool.Query(ctx, query, args...)
	if err != nil {
		return chat.MessagePage{}, err
	}
	defer rows.Close()

	out := make([]chat.Message, 0, limit)
	for rows.Next() {
		var item chat.Message
		var senderDeviceID *string
		var reactionsJSON []byte
		if err := rows.Scan(&item.ID, &item.ChatID, &item.UserID, &item.SenderBotID, &item.Content, &item.IsE2E, &senderDeviceID, &item.CreatedAt, &item.EditedAt, &reactionsJSON); err != nil {
			return chat.MessagePage{}, err
		}
		item.Reactions = parseMessageReactionsJSON(reactionsJSON)
		if strings.TrimSpace(item.UserID) == "" && item.SenderBotID != nil {
			item.UserID = "bot:" + strings.TrimSpace(*item.SenderBotID)
		}
		if item.IsE2E {
			item.E2E = &chat.E2EPayload{SenderDeviceID: strings.TrimSpace(derefString(senderDeviceID))}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return chat.MessagePage{}, err
	}

	page := chat.MessagePage{Items: out}
	if len(out) == limit {
		last := out[len(out)-1]
		page.NextCursor = formatMessageCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func (r *MessageRepository) ListMessagesForDevice(ctx context.Context, chatID, deviceID string, limit int, cursor string) (chat.MessagePage, error) {
	const baseQuery = `
		SELECT m.id::text, m.chat_id::text, COALESCE(m.user_id::text, ''), m.sender_bot_id::text, m.content,
		       m.is_e2e, m.e2e_sender_device_id::text,
		       e.recipient_device_id::text, e.alg, e.header, e.ciphertext,
		       m.created_at, m.edited_at,
		       COALESCE((
		         SELECT json_agg(row_to_json(x))
		         FROM (
		           SELECT mr.emoji, array_agg(mr.user_id::text ORDER BY mr.updated_at DESC) AS user_ids
		           FROM message_reactions mr
		           WHERE mr.message_id = m.id
		           GROUP BY mr.emoji
		           ORDER BY max(mr.updated_at) DESC
		         ) AS x
		       ), '[]'::json) AS reactions_json
		FROM messages m
		LEFT JOIN message_envelopes e
		  ON e.message_id = m.id
		 AND e.recipient_device_id = $2::uuid
		WHERE m.chat_id = $1::uuid
		  AND m.deleted_at IS NULL
		  AND (m.is_e2e = FALSE OR e.recipient_device_id IS NOT NULL)
		  %s
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $3
	`

	args := []any{chatID, deviceID, limit}
	condition := ""
	if strings.TrimSpace(cursor) != "" {
		cursorTS, cursorID, err := parseMessageCursor(cursor)
		if err != nil {
			return chat.MessagePage{}, err
		}
		condition = "AND (m.created_at, m.id) < ($4::timestamptz, $5::uuid)"
		args = append(args, cursorTS, cursorID)
	}

	query := fmt.Sprintf(baseQuery, condition)
	rows, err := r.client.pool.Query(ctx, query, args...)
	if err != nil {
		return chat.MessagePage{}, err
	}
	defer rows.Close()

	out := make([]chat.Message, 0, limit)
	for rows.Next() {
		var item chat.Message
		var senderDeviceID *string
		var senderBotID *string
		var recDeviceID *string
		var alg, header, ciphertext *string
		var editedAt *time.Time
		var reactionsJSON []byte
		if err := rows.Scan(
			&item.ID,
			&item.ChatID,
			&item.UserID,
			&senderBotID,
			&item.Content,
			&item.IsE2E,
			&senderDeviceID,
			&recDeviceID,
			&alg,
			&header,
			&ciphertext,
			&item.CreatedAt,
			&editedAt,
			&reactionsJSON,
		); err != nil {
			return chat.MessagePage{}, err
		}
		item.Reactions = parseMessageReactionsJSON(reactionsJSON)
		item.EditedAt = editedAt
		item.SenderBotID = senderBotID
		if strings.TrimSpace(item.UserID) == "" && senderBotID != nil {
			item.UserID = "bot:" + strings.TrimSpace(*senderBotID)
		}

		if item.IsE2E {
			payload := &chat.E2EPayload{SenderDeviceID: strings.TrimSpace(derefString(senderDeviceID))}
			if recDeviceID != nil && alg != nil && header != nil && ciphertext != nil {
				payload.Envelope = &chat.E2EEnvelope{
					RecipientDeviceID: *recDeviceID,
					Alg:               *alg,
					Header:            *header,
					Ciphertext:        *ciphertext,
				}
			}
			item.E2E = payload
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return chat.MessagePage{}, err
	}

	page := chat.MessagePage{Items: out}
	if len(out) == limit {
		last := out[len(out)-1]
		page.NextCursor = formatMessageCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func formatMessageCursor(createdAt time.Time, id string) string {
	return strconv.FormatInt(createdAt.UTC().UnixNano(), 10) + ":" + id
}

func parseMessageCursor(cursor string) (time.Time, string, error) {
	parts := strings.Split(strings.TrimSpace(cursor), ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return time.Time{}, "", errors.New("invalid cursor format")
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", err
	}
	return time.Unix(0, nanos).UTC(), parts[1], nil
}

func parseMessageReactionsJSON(raw []byte) []chat.MessageReaction {
	if len(raw) == 0 {
		return nil
	}
	var parsed []chat.MessageReaction
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	return parsed
}

func (r *MessageRepository) ToggleMessageReaction(ctx context.Context, chatID, messageID, userID, emoji string) ([]chat.MessageReaction, string, error) {
	const checkMessage = `
		SELECT 1
		FROM messages
		WHERE id = $1::uuid
		  AND chat_id = $2::uuid
		  AND deleted_at IS NULL
		LIMIT 1
	`
	var exists int
	if err := r.client.pool.QueryRow(ctx, checkMessage, strings.TrimSpace(messageID), strings.TrimSpace(chatID)).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", chat.ErrMessageNotFound
		}
		return nil, "", err
	}

	const getCurrent = `
		SELECT emoji
		FROM message_reactions
		WHERE message_id = $1::uuid
		  AND user_id = $2::uuid
		LIMIT 1
	`
	var current string
	currentErr := r.client.pool.QueryRow(ctx, getCurrent, strings.TrimSpace(messageID), strings.TrimSpace(userID)).Scan(&current)
	if currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows) {
		return nil, "", currentErr
	}

	action := "set"
	if currentErr == nil && strings.TrimSpace(current) == strings.TrimSpace(emoji) {
		const deleteQuery = `
			DELETE FROM message_reactions
			WHERE message_id = $1::uuid
			  AND user_id = $2::uuid
		`
		if _, err := r.client.pool.Exec(ctx, deleteQuery, strings.TrimSpace(messageID), strings.TrimSpace(userID)); err != nil {
			return nil, "", err
		}
		action = "removed"
	} else {
		const upsert = `
			INSERT INTO message_reactions (message_id, user_id, emoji)
			VALUES ($1::uuid, $2::uuid, $3)
			ON CONFLICT (message_id, user_id)
			DO UPDATE SET emoji = EXCLUDED.emoji, updated_at = NOW()
		`
		if _, err := r.client.pool.Exec(ctx, upsert, strings.TrimSpace(messageID), strings.TrimSpace(userID), strings.TrimSpace(emoji)); err != nil {
			return nil, "", err
		}
	}

	const listQuery = `
		SELECT COALESCE((
		  SELECT json_agg(row_to_json(x))
		  FROM (
		    SELECT mr.emoji, array_agg(mr.user_id::text ORDER BY mr.updated_at DESC) AS user_ids
		    FROM message_reactions mr
		    WHERE mr.message_id = $1::uuid
		    GROUP BY mr.emoji
		    ORDER BY max(mr.updated_at) DESC
		  ) AS x
		), '[]'::json)
	`
	var raw []byte
	if err := r.client.pool.QueryRow(ctx, listQuery, strings.TrimSpace(messageID)).Scan(&raw); err != nil {
		return nil, "", err
	}
	return parseMessageReactionsJSON(raw), action, nil
}
