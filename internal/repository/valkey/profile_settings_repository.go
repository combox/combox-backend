package valkey

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type ProfileSettings struct {
	ShowLastSeen bool
}

type ChatNotifications struct {
	MutedChatIDs []string
	UnreadByChat map[string]int
}

type ProfileSettingsRepository struct {
	c *Client
}

type RecentGIF struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	PreviewURL string `json:"preview_url"`
	URL        string `json:"url"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

func NewProfileSettingsRepository(c *Client) *ProfileSettingsRepository {
	return &ProfileSettingsRepository{c: c}
}

func profileSettingsKey(userID string) string {
	return "profile:settings:" + strings.TrimSpace(userID)
}

func profileRecentGIFsKey(userID string) string {
	return "profile:gifs:recent:" + strings.TrimSpace(userID)
}

func mutedChatsKey(userID string) string {
	return "profile:chats:muted:" + strings.TrimSpace(userID)
}

func unreadChatsKey(userID string) string {
	return "profile:chats:unread:" + strings.TrimSpace(userID)
}

func (r *ProfileSettingsRepository) Get(ctx context.Context, userID string) (ProfileSettings, error) {
	if r == nil || r.c == nil || strings.TrimSpace(userID) == "" {
		return ProfileSettings{ShowLastSeen: true}, nil
	}
	val, err := r.c.Client().HGet(ctx, profileSettingsKey(userID), "show_last_seen").Result()
	if err != nil {
		return ProfileSettings{ShowLastSeen: true}, nil
	}
	return ProfileSettings{ShowLastSeen: strings.TrimSpace(val) != "0"}, nil
}

func (r *ProfileSettingsRepository) Set(ctx context.Context, userID string, showLastSeen bool) error {
	if r == nil || r.c == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	val := "1"
	if !showLastSeen {
		val = "0"
	}
	pipe := r.c.Client().Pipeline()
	pipe.HSet(ctx, profileSettingsKey(userID), "show_last_seen", val)
	pipe.Expire(ctx, profileSettingsKey(userID), 365*24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *ProfileSettingsRepository) AddRecentGIF(ctx context.Context, userID string, item RecentGIF, limit int) error {
	if r == nil || r.c == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	id := strings.TrimSpace(item.ID)
	url := strings.TrimSpace(item.URL)
	if id == "" || url == "" {
		return nil
	}
	if limit <= 0 {
		limit = 400
	}
	if limit > 400 {
		limit = 400
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}

	key := profileRecentGIFsKey(userID)
	items, _ := r.c.Client().LRange(ctx, key, 0, int64(limit*2)).Result()
	for _, row := range items {
		var existing RecentGIF
		if err := json.Unmarshal([]byte(row), &existing); err != nil {
			continue
		}
		if strings.TrimSpace(existing.ID) == id || strings.TrimSpace(existing.URL) == url {
			_ = r.c.Client().LRem(ctx, key, 0, row).Err()
		}
	}

	pipe := r.c.Client().Pipeline()
	pipe.LPush(ctx, key, raw)
	pipe.LTrim(ctx, key, 0, int64(limit-1))
	pipe.Expire(ctx, key, 365*24*time.Hour)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *ProfileSettingsRepository) ListRecentGIFs(ctx context.Context, userID string, limit int) ([]RecentGIF, error) {
	if r == nil || r.c == nil || strings.TrimSpace(userID) == "" {
		return []RecentGIF{}, nil
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 400 {
		limit = 400
	}
	rows, err := r.c.Client().LRange(ctx, profileRecentGIFsKey(userID), 0, int64(limit-1)).Result()
	if err != nil {
		return []RecentGIF{}, nil
	}
	out := make([]RecentGIF, 0, len(rows))
	for _, row := range rows {
		var item RecentGIF
		if err := json.Unmarshal([]byte(row), &item); err != nil {
			continue
		}
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.URL) == "" {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *ProfileSettingsRepository) SetChatMuted(ctx context.Context, userID, chatID string, muted bool) error {
	if r == nil || r.c == nil {
		return nil
	}
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil
	}

	key := mutedChatsKey(userID)
	if muted {
		pipe := r.c.Client().Pipeline()
		pipe.SAdd(ctx, key, chatID)
		pipe.Expire(ctx, key, 365*24*time.Hour)
		_, err := pipe.Exec(ctx)
		return err
	}
	return r.c.Client().SRem(ctx, key, chatID).Err()
}

func (r *ProfileSettingsRepository) ListMutedChatIDs(ctx context.Context, userID string) ([]string, error) {
	if r == nil || r.c == nil || strings.TrimSpace(userID) == "" {
		return []string{}, nil
	}
	items, err := r.c.Client().SMembers(ctx, mutedChatsKey(userID)).Result()
	if err != nil {
		return []string{}, nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

func (r *ProfileSettingsRepository) IncrementChatUnread(ctx context.Context, userID, chatID string, delta int) (int, error) {
	if r == nil || r.c == nil {
		return 0, nil
	}
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" || delta == 0 {
		return 0, nil
	}
	val, err := r.c.Client().HIncrBy(ctx, unreadChatsKey(userID), chatID, int64(delta)).Result()
	if err != nil {
		return 0, err
	}
	if val < 0 {
		_ = r.c.Client().HSet(ctx, unreadChatsKey(userID), chatID, 0).Err()
		return 0, nil
	}
	_ = r.c.Client().Expire(ctx, unreadChatsKey(userID), 365*24*time.Hour).Err()
	return int(val), nil
}

func (r *ProfileSettingsRepository) ResetChatUnread(ctx context.Context, userID, chatID string) error {
	if r == nil || r.c == nil {
		return nil
	}
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil
	}
	return r.c.Client().HDel(ctx, unreadChatsKey(userID), chatID).Err()
}

func (r *ProfileSettingsRepository) GetChatNotifications(ctx context.Context, userID string) (ChatNotifications, error) {
	out := ChatNotifications{
		MutedChatIDs: []string{},
		UnreadByChat: map[string]int{},
	}
	if r == nil || r.c == nil || strings.TrimSpace(userID) == "" {
		return out, nil
	}

	muted, _ := r.ListMutedChatIDs(ctx, userID)
	out.MutedChatIDs = muted

	raw, err := r.c.Client().HGetAll(ctx, unreadChatsKey(userID)).Result()
	if err != nil {
		return out, nil
	}
	for chatID, value := range raw {
		id := strings.TrimSpace(chatID)
		if id == "" {
			continue
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(value))
		if convErr != nil || n <= 0 {
			continue
		}
		out.UnreadByChat[id] = n
	}
	return out, nil
}
