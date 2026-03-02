package valkey

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type PresenceStatus struct {
	UserID   string
	Online   bool
	LastSeen time.Time
}

type PresenceRepository struct {
	rdb *redis.Client
}

func NewPresenceRepository(c *Client) *PresenceRepository {
	if c == nil {
		return &PresenceRepository{}
	}
	return &PresenceRepository{rdb: c.Client()}
}

func NewPresenceRepositoryFromRedis(rdb *redis.Client) *PresenceRepository {
	return &PresenceRepository{rdb: rdb}
}

func presenceKey(userID string) string {
	return "presence:user:" + strings.TrimSpace(userID)
}

func (r *PresenceRepository) SetOnline(ctx context.Context, userID string, now time.Time, ttl time.Duration) error {
	if r == nil || r.rdb == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	key := presenceKey(userID)
	pipe := r.rdb.Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"online":    "1",
		"last_seen": strconv.FormatInt(now.Unix(), 10),
	})
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *PresenceRepository) SetOffline(ctx context.Context, userID string, now time.Time, ttl time.Duration) error {
	if r == nil || r.rdb == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	key := presenceKey(userID)
	pipe := r.rdb.Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"online":    "0",
		"last_seen": strconv.FormatInt(now.Unix(), 10),
	})
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *PresenceRepository) GetPresence(ctx context.Context, userIDs []string) (map[string]PresenceStatus, error) {
	out := make(map[string]PresenceStatus, len(userIDs))
	if r == nil || r.rdb == nil {
		return out, nil
	}
	pipe := r.rdb.Pipeline()
	cmds := make(map[string]*redis.MapStringStringCmd, len(userIDs))
	for _, id := range userIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		cmds[id] = pipe.HGetAll(ctx, presenceKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return out, err
	}
	for id, cmd := range cmds {
		values := cmd.Val()
		if len(values) == 0 {
			out[id] = PresenceStatus{UserID: id, Online: false}
			continue
		}
		lastSeenRaw := strings.TrimSpace(values["last_seen"])
		lastSeen := time.Time{}
		if lastSeenRaw != "" {
			if unix, err := strconv.ParseInt(lastSeenRaw, 10, 64); err == nil && unix > 0 {
				lastSeen = time.Unix(unix, 0).UTC()
			}
		}
		online := strings.TrimSpace(values["online"]) == "1"
		out[id] = PresenceStatus{UserID: id, Online: online, LastSeen: lastSeen}
	}
	return out, nil
}
