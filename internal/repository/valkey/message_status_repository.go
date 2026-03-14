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

func latestStatusKey(messageID string) string {
	return "msgstatus:latest:" + messageID
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

	latestKey := latestStatusKey(messageID)
	latestStatus, _ := r.c.Client().HGet(ctx, latestKey, "status").Result()
	shouldUpdateLatest := statusRank(latestStatus) <= statusRank(status)

	pipe := r.c.Client().Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"status":     status,
		"chat_id":    strings.TrimSpace(chatID),
		"updated_at": strconv.FormatInt(at.Unix(), 10),
	})
	if shouldUpdateLatest {
		pipe.HSet(ctx, latestKey, map[string]any{
			"status":     status,
			"chat_id":    strings.TrimSpace(chatID),
			"user_id":    userID,
			"updated_at": strconv.FormatInt(at.Unix(), 10),
		})
	}
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

func (r *MessageStatusRepository) ListLatestMessageStatuses(ctx context.Context, messageIDs []string) ([]chatsvc.MessageStatus, error) {
	if r == nil || r.c == nil {
		return nil, nil
	}
	if len(messageIDs) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(messageIDs))
	ids := make([]string, 0, len(messageIDs))
	for _, raw := range messageIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	pipe := r.c.Client().Pipeline()
	cmds := make([]*redis.MapStringStringCmd, 0, len(ids))
	for _, id := range ids {
		cmds = append(cmds, pipe.HGetAll(ctx, latestStatusKey(id)))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	out := make([]chatsvc.MessageStatus, 0, len(ids))
	for i, cmd := range cmds {
		values, err := cmd.Result()
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(values["status"]))
		if status == "" {
			continue
		}
		chatID := strings.TrimSpace(values["chat_id"])
		userID := strings.TrimSpace(values["user_id"])
		updatedAtRaw := strings.TrimSpace(values["updated_at"])
		if chatID == "" {
			continue
		}
		updatedAt := time.Time{}
		if updatedAtRaw != "" {
			if unix, err := strconv.ParseInt(updatedAtRaw, 10, 64); err == nil {
				updatedAt = time.Unix(unix, 0).UTC()
			}
		}
		out = append(out, chatsvc.MessageStatus{
			MessageID: ids[i],
			ChatID:    chatID,
			UserID:    userID,
			Status:    status,
			UpdatedAt: updatedAt,
		})
	}
	return out, nil
}
