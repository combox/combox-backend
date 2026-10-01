package translate

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache stores translated texts so repeated requests never hit the upstream
// engine. Only ephemeral data lives here (rules: Valkey is cache only,
// PostgreSQL stays the source of truth — translations need no migration).
type Cache interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

type memoryEntry struct {
	value     string
	expiresAt time.Time
}

// memoryCache is the fallback when Valkey is unavailable (local dev, tests).
// Bounded cleanup: expired entries are purged opportunistically on Set once
// the map grows past maxMemoryEntries.
type memoryCache struct {
	mu      sync.Mutex
	items   map[string]memoryEntry
	maxSize int
}

const maxMemoryEntries = 10000

func NewMemoryCache() Cache {
	return &memoryCache{items: make(map[string]memoryEntry), maxSize: maxMemoryEntries}
}

func (c *memoryCache) Get(_ context.Context, key string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return "", false, nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(c.items, key)
		return "", false, nil
	}
	return entry.value, true, nil
}

func (c *memoryCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.maxSize {
		now := time.Now()
		for k, entry := range c.items {
			if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
				delete(c.items, k)
			}
		}
	}
	expiresAt := time.Time{}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	c.items[key] = memoryEntry{value: value, expiresAt: expiresAt}
	return nil
}

// valkeyCache is the primary cache in every environment with Valkey.
type valkeyCache struct {
	rdb *redis.Client
}

// NewValkeyCache returns nil when rdb is nil so the service can fall back to
// the in-memory cache instead of failing.
func NewValkeyCache(rdb *redis.Client) Cache {
	if rdb == nil {
		return nil
	}
	return &valkeyCache{rdb: rdb}
}

func (c *valkeyCache) Get(ctx context.Context, key string) (string, bool, error) {
	value, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func (c *valkeyCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}
