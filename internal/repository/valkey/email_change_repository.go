package valkey

import (
	"context"
	"strings"
	"time"
)

type EmailChangeState struct {
	UserID      string
	OldEmail    string
	NewEmail    string
	OldVerified bool
}

type EmailChangeRepository struct {
	rdb *Client
}

func NewEmailChangeRepository(c *Client) *EmailChangeRepository {
	if c == nil {
		return &EmailChangeRepository{}
	}
	return &EmailChangeRepository{rdb: c}
}

func emailChangeKey(userID string) string {
	return "profile:email_change:" + strings.TrimSpace(userID)
}

func (r *EmailChangeRepository) Get(ctx context.Context, userID string) (EmailChangeState, error) {
	state := EmailChangeState{UserID: strings.TrimSpace(userID)}
	if r == nil || r.rdb == nil {
		return state, nil
	}
	key := emailChangeKey(userID)
	values, err := r.rdb.Client().HGetAll(ctx, key).Result()
	if err != nil {
		return state, err
	}
	state.OldEmail = strings.TrimSpace(values["old_email"])
	state.NewEmail = strings.TrimSpace(values["new_email"])
	state.OldVerified = strings.TrimSpace(values["old_verified"]) == "1"
	return state, nil
}

func (r *EmailChangeRepository) MarkOldVerified(ctx context.Context, userID, oldEmail string, ttl time.Duration) error {
	if r == nil || r.rdb == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	key := emailChangeKey(userID)
	pipe := r.rdb.Client().Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"old_verified": "1",
		"old_email":    strings.TrimSpace(strings.ToLower(oldEmail)),
	})
	pipe.HDel(ctx, key, "new_email")
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *EmailChangeRepository) SetNewEmail(ctx context.Context, userID, newEmail string, ttl time.Duration) error {
	if r == nil || r.rdb == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	key := emailChangeKey(userID)
	pipe := r.rdb.Client().Pipeline()
	pipe.HSet(ctx, key, "new_email", strings.TrimSpace(strings.ToLower(newEmail)))
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *EmailChangeRepository) Clear(ctx context.Context, userID string) error {
	if r == nil || r.rdb == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	return r.rdb.Client().Del(ctx, emailChangeKey(userID)).Err()
}
