package valkey

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// presenceLastSeenTTL keeps the "last seen at" timestamp long after the short
// online marker expired, so an offline user still shows a timestamp.
const presenceLastSeenTTL = 30 * 24 * time.Hour

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

func presenceLastSeenKey(userID string) string {
	return "presence:last_seen:" + strings.TrimSpace(userID)
}

func parsePresenceUnix(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	unix, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}

// presenceStatusFrom merges the online hash with the durable last-seen value.
// The hash disappears once the online TTL lapses, so lastSeenFallback keeps
// the timestamp available for users who went fully offline.
func presenceStatusFrom(userID string, values map[string]string, lastSeenFallback string) PresenceStatus {
	status := PresenceStatus{UserID: userID}
	if len(values) == 0 {
		status.LastSeen = parsePresenceUnix(lastSeenFallback)
		return status
	}
	status.Online = strings.TrimSpace(values["online"]) == "1"
	status.LastSeen = parsePresenceUnix(values["last_seen"])
	if status.LastSeen.IsZero() {
		status.LastSeen = parsePresenceUnix(lastSeenFallback)
	}
	return status
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
	pipe.Set(ctx, presenceLastSeenKey(userID), strconv.FormatInt(now.Unix(), 10), presenceLastSeenTTL)
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
	pipe.Set(ctx, presenceLastSeenKey(userID), strconv.FormatInt(now.Unix(), 10), presenceLastSeenTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *PresenceRepository) GetPresence(ctx context.Context, userIDs []string) (map[string]PresenceStatus, error) {
	out := make(map[string]PresenceStatus, len(userIDs))
	if r == nil || r.rdb == nil {
		return out, nil
	}
	pipe := r.rdb.Pipeline()
	hashCmds := make(map[string]*redis.MapStringStringCmd, len(userIDs))
	lastSeenCmds := make(map[string]*redis.StringCmd, len(userIDs))
	for _, id := range userIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		hashCmds[id] = pipe.HGetAll(ctx, presenceKey(id))
		lastSeenCmds[id] = pipe.Get(ctx, presenceLastSeenKey(id))
	}
	// A missing last-seen key reports redis.Nil; that is an empty value, not a failure.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return out, err
	}
	for id, hashCmd := range hashCmds {
		var lastSeenRaw string
		if cmd := lastSeenCmds[id]; cmd != nil {
			lastSeenRaw, _ = cmd.Result()
		}
		out[id] = presenceStatusFrom(id, hashCmd.Val(), lastSeenRaw)
	}
	return out, nil
}
