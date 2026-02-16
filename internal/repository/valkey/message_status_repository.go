package valkey

import (
	"context"
	"strconv"
	"strings"
	"time"

	chatsvc "combox-backend/internal/service/chat"

	"github.com/redis/go-redis/v9"
)

type MessageStatusRepository struct {
	c *Client
}

func NewMessageStatusRepository(c *Client) *MessageStatusRepository {
	return &MessageStatusRepository{c: c}
}

func statusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "read":
		return 2
	case "delivered":
		return 1
	default:
		return 0
	}
}

func statusKey(messageID, userID string) string {
	return "msgstatus:" + messageID + ":" + userID
}

func userIndexKey(userID string) string {
	return "msgstatus:user:" + userID
}

func (r *MessageStatusRepository) UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string, at time.Time) (chatsvc.MessageStatus, error) {
	if r == nil || r.c == nil {
		return chatsvc.MessageStatus{
			MessageID: strings.TrimSpace(messageID),
			ChatID:    strings.TrimSpace(chatID),
			UserID:    strings.TrimSpace(userID),
			Status:    strings.ToLower(strings.TrimSpace(status)),
			UpdatedAt: at.UTC(),
		}, nil
	}
	messageID = strings.TrimSpace(messageID)
	userID = strings.TrimSpace(userID)
	status = strings.ToLower(strings.TrimSpace(status))
	at = at.UTC()
	if messageID == "" || userID == "" || status == "" {
		return chatsvc.MessageStatus{}, nil
	}

	key := statusKey(messageID, userID)
	existingStatus, _ := r.c.Client().HGet(ctx, key, "status").Result()
	if statusRank(existingStatus) > statusRank(status) {
		status = strings.ToLower(strings.TrimSpace(existingStatus))
	}

	pipe := r.c.Client().Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"status":     status,
		"chat_id":    strings.TrimSpace(chatID),
		"updated_at": strconv.FormatInt(at.Unix(), 10),
	})
	pipe.ZAdd(ctx, userIndexKey(userID), redis.Z{Score: float64(at.Unix()), Member: messageID + ":" + strings.TrimSpace(chatID) + ":" + status})
	_, err := pipe.Exec(ctx)
	if err != nil {
		return chatsvc.MessageStatus{}, err
	}
	return chatsvc.MessageStatus{
		MessageID: messageID,
		ChatID:    strings.TrimSpace(chatID),
		UserID:    userID,
		Status:    status,
		UpdatedAt: at,
	}, nil
}
