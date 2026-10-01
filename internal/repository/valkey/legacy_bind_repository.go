package valkey

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// LegacyBindRepository stores the boxchat email-binding OTPs
// (migration 000044_legacy_auth) in valkey. One entry per user:
//
//	key:   legacy-bind:<userID>
//	value: hash {email, salt, hash, attempts} with a 10 minute TTL.
//
// The code itself is never stored: only sha256(salt:code) is kept, and the
// comparison is constant-time. All methods are nil-client safe (they no-op or
// report "missing" when valkey is unavailable) like EmailChangeRepository.
type LegacyBindRepository struct {
	rdb *Client
}

// LegacyBindMaxAttempts caps OTP guesses per issued code; the entry is
// deleted once the cap is hit.
const LegacyBindMaxAttempts = 5

type LegacyBindEntry struct {
	UserID   string
	Email    string
	Attempts int
}

func NewLegacyBindRepository(c *Client) *LegacyBindRepository {
	if c == nil {
		return &LegacyBindRepository{}
	}
	return &LegacyBindRepository{rdb: c}
}

func legacyBindKey(userID string) string {
	return "legacy-bind:" + strings.TrimSpace(userID)
}

func legacyBindCodeHash(salt, code string) string {
	sum := sha256.Sum256([]byte(salt + ":" + code))
	return hex.EncodeToString(sum[:])
}

// Save stores a fresh OTP for userID, resetting attempts and the TTL.
func (r *LegacyBindRepository) Save(ctx context.Context, userID, email, salt, codeHash string, ttl time.Duration) error {
	if r == nil || r.rdb == nil || r.rdb.Client() == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	key := legacyBindKey(userID)
	pipe := r.rdb.Client().Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"email":    strings.TrimSpace(strings.ToLower(email)),
		"salt":     salt,
		"hash":     codeHash,
		"attempts": "0",
	})
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// HashCode is the helper handlers use to hash a freshly generated OTP before
// calling Save. It is split out so unit tests can cover the scheme without
// valkey.
func HashLegacyBindCode(salt, code string) string {
	return legacyBindCodeHash(strings.TrimSpace(salt), strings.TrimSpace(code))
}

// Get loads the entry plus the remaining TTL. ok=false means no live entry.
func (r *LegacyBindRepository) Get(ctx context.Context, userID string) (entry LegacyBindEntry, ttl time.Duration, ok bool, err error) {
	entry.UserID = strings.TrimSpace(userID)
	if r == nil || r.rdb == nil || r.rdb.Client() == nil || entry.UserID == "" {
		return entry, 0, false, nil
	}
	key := legacyBindKey(userID)
	values, err := r.rdb.Client().HGetAll(ctx, key).Result()
	if err != nil {
		return entry, 0, false, err
	}
	if len(values) == 0 {
		return entry, 0, false, nil
	}
	entry.Email = strings.TrimSpace(values["email"])
	if attempts, convErr := strconv.Atoi(strings.TrimSpace(values["attempts"])); convErr == nil {
		entry.Attempts = attempts
	}
	left, err := r.rdb.Client().TTL(ctx, key).Result()
	if err != nil {
		return entry, 0, false, err
	}
	if left <= 0 {
		return entry, 0, false, nil
	}
	return entry, left, true, nil
}

// Verify checks email + code in constant time. A wrong code bumps attempts
// (deleting the entry at the cap); a right one leaves the entry for the
// caller to Delete after completing the bind.
func (r *LegacyBindRepository) Verify(ctx context.Context, userID, email, code string) (bool, error) {
	if r == nil || r.rdb == nil || r.rdb.Client() == nil || strings.TrimSpace(userID) == "" {
		return false, nil
	}
	key := legacyBindKey(userID)
	values, err := r.rdb.Client().HGetAll(ctx, key).Result()
	if err != nil || len(values) == 0 {
		return false, err
	}
	if !strings.EqualFold(strings.TrimSpace(values["email"]), strings.TrimSpace(email)) {
		return false, nil
	}
	attempts, _ := strconv.Atoi(strings.TrimSpace(values["attempts"]))
	if attempts >= LegacyBindMaxAttempts {
		_ = r.rdb.Client().Del(ctx, key).Err()
		return false, nil
	}
	expected := strings.TrimSpace(values["hash"])
	got := legacyBindCodeHash(strings.TrimSpace(values["salt"]), strings.TrimSpace(code))
	match := expected != "" && got != "" &&
		subtle.ConstantTimeCompare([]byte(expected), []byte(got)) == 1
	if !match {
		attempts++
		if attempts >= LegacyBindMaxAttempts {
			_ = r.rdb.Client().Del(ctx, key).Err()
			return false, nil
		}
		_ = r.rdb.Client().HSet(ctx, key, "attempts", strconv.Itoa(attempts)).Err()
		return false, nil
	}
	return true, nil
}

// Delete removes the entry (after a successful bind).
func (r *LegacyBindRepository) Delete(ctx context.Context, userID string) error {
	if r == nil || r.rdb == nil || r.rdb.Client() == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	return r.rdb.Client().Del(ctx, legacyBindKey(userID)).Err()
}
