package valkey

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ChatInvite struct {
	Token     string    `json:"token"`
	ChatID    string    `json:"chat_id"`
	InviterID string    `json:"inviter_user_id"`
	InviteeID string    `json:"invitee_user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ChatInviteRepository struct {
	c *Client
}

func NewChatInviteRepository(c *Client) *ChatInviteRepository {
	return &ChatInviteRepository{c: c}
}

func chatInviteKey(token string) string {
	return "chat:invite:" + strings.TrimSpace(token)
}

func (r *ChatInviteRepository) Create(ctx context.Context, chatID, inviterID, inviteeID string, ttl time.Duration) (ChatInvite, error) {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	now := time.Now().UTC()
	item := ChatInvite{
		Token:     uuid.NewString(),
		ChatID:    strings.TrimSpace(chatID),
		InviterID: strings.TrimSpace(inviterID),
		InviteeID: strings.TrimSpace(inviteeID),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return ChatInvite{}, err
	}
	if err := r.c.Client().Set(ctx, chatInviteKey(item.Token), raw, ttl).Err(); err != nil {
		return ChatInvite{}, err
	}
	return item, nil
}

func (r *ChatInviteRepository) Consume(ctx context.Context, token string) (ChatInvite, bool, error) {
	clean := strings.TrimSpace(token)
	if r == nil || r.c == nil || clean == "" {
		return ChatInvite{}, false, nil
	}
	key := chatInviteKey(clean)
	raw, err := r.c.Client().GetDel(ctx, key).Result()
	if err != nil {
		return ChatInvite{}, false, nil
	}
	var item ChatInvite
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return ChatInvite{}, false, err
	}
	if strings.TrimSpace(item.Token) == "" {
		item.Token = clean
	}
	return item, true, nil
}
