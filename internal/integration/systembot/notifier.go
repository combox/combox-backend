package systembot

import (
	"context"
	"fmt"
	"strings"
	"time"

	chatsvc "combox-backend/internal/service/chat"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	systemFirstName = "Combox"
	systemChatTitle = "Combox Service Notifications"
)

type Notifier struct {
	pool     *pgxpool.Pool
	messages messageSender
}

type messageSender interface {
	CreateMessage(ctx context.Context, input chatsvc.CreateMessageInput) (chatsvc.Message, error)
}

func New(pool *pgxpool.Pool, messages messageSender) *Notifier {
	return &Notifier{pool: pool, messages: messages}
}

func (n *Notifier) NotifyLoginCode(ctx context.Context, email, code string, expiresAt time.Time, locale string) (bool, error) {
	if n == nil || n.pool == nil || n.messages == nil {
		return false, nil
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || strings.TrimSpace(code) == "" {
		return false, nil
	}

	tx, err := n.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	userID, err := findUserIDByEmail(ctx, tx, email)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	hasSessions, err := hasActiveSessions(ctx, tx, userID, time.Now().UTC())
	if err != nil {
		return false, err
	}
	if !hasSessions {
		return false, nil
	}

	systemID, err := ensureSystemUser(ctx, tx)
	if err != nil {
		return false, err
	}

	chatID, err := ensureDirectChat(ctx, tx, systemID, userID)
	if err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}

	content := buildCodeMessage(locale, code, expiresAt)
	if _, err := n.messages.CreateMessage(ctx, chatsvc.CreateMessageInput{
		BotID:   systemID,
		ChatID:  chatID,
		Content: content,
	}); err != nil {
		return false, err
	}

	return true, nil
}

func findUserIDByEmail(ctx context.Context, tx pgx.Tx, email string) (string, error) {
	var userID string
	err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1 LIMIT 1`, email).Scan(&userID)
	return userID, err
}

func hasActiveSessions(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM sessions
			WHERE user_id = $1::uuid
			  AND expires_at > $2::timestamptz
			LIMIT 1
		)
	`, userID, now).Scan(&exists)
	return exists, err
}

func ensureSystemUser(ctx context.Context, tx pgx.Tx) (string, error) {
	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO bots (owner_user_id, actor_user_id, kind, name, is_system)
		VALUES (NULL, NULL, 'system', $1, TRUE)
		ON CONFLICT (is_system) WHERE is_system = TRUE
		DO UPDATE SET name = EXCLUDED.name, updated_at = NOW()
		RETURNING id::text
	`, systemFirstName).Scan(&userID); err != nil {
		return "", err
	}
	return userID, nil
}

func ensureDirectChat(ctx context.Context, tx pgx.Tx, systemID, userID string) (string, error) {
	var chatID string
	err := tx.QueryRow(ctx, `
		SELECT c.id::text
		FROM chats c
		INNER JOIN chat_members m1 ON m1.chat_id = c.id AND m1.user_id = $1::uuid
		WHERE c.is_direct = FALSE
		  AND c.title = $2
		  AND c.created_by = $1::uuid
		ORDER BY c.created_at ASC
		LIMIT 1
	`, userID, systemChatTitle).Scan(&chatID)
	if err == nil {
		return chatID, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO chats (title, is_direct, created_by, chat_type, chat_kind, bot_id)
		VALUES ($1, FALSE, $2::uuid, 'standard', 'bot', $3::uuid)
		RETURNING id::text
	`, systemChatTitle, userID, systemID).Scan(&chatID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat_members (chat_id, user_id, role)
		VALUES ($1::uuid, $2::uuid, 'member')
		ON CONFLICT (chat_id, user_id) DO NOTHING
	`, chatID, userID); err != nil {
		return "", err
	}
	return chatID, nil
}

func buildCodeMessage(locale, code string, expiresAt time.Time) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "ru") {
		return fmt.Sprintf("Код входа: %s\nНикому не сообщайте этот код.\nДействителен до %s UTC.", code, expiresAt.UTC().Format("15:04"))
	}
	return fmt.Sprintf("Login code: %s\nDo not share this code.\nValid until %s UTC.", code, expiresAt.UTC().Format("15:04"))
}
