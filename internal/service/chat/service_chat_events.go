package chat

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Chat event types stored in the per-chat "recent actions" journal
// (migration 000040_chat_events, table chat_events).
const (
	ChatEventMemberJoined       = "member_joined"
	ChatEventMemberLeft         = "member_left"
	ChatEventMemberRemoved      = "member_removed"
	ChatEventRoleChanged        = "role_changed"
	ChatEventTitleChanged       = "title_changed"
	ChatEventAvatarChanged      = "avatar_changed"
	ChatEventDescriptionChanged = "description_changed"
	ChatEventIconChanged        = "icon_changed"
	ChatEventSettingsChanged    = "settings_changed"
	ChatEventBanned             = "banned"
	ChatEventUnbanned           = "unbanned"
)

const (
	// defaultChatEventsLimit / maxChatEventsLimit bound ?limit= on the
	// GET /chats/{chatID}/events endpoint.
	defaultChatEventsLimit = 50
	maxChatEventsLimit     = 200
	// chatEventPayloadLimit keeps one payload a single short line.
	chatEventPayloadLimit = 160
)

// ChatEvent is one journal row: who did what to a chat and when.
type ChatEvent struct {
	ID           string    `json:"id"`
	ChatID       string    `json:"chat_id"`
	ActorUserID  *string   `json:"actor_user_id,omitempty"`
	TargetUserID *string   `json:"target_user_id,omitempty"`
	EventType    string    `json:"event_type"`
	Payload      string    `json:"payload"`
	CreatedAt    time.Time `json:"created_at"`
}

// ChatEventRepository stores the journal. It is optional: while it is unset
// recording is a no-op and the read endpoint returns an empty list.
type ChatEventRepository interface {
	RecordChatEvent(ctx context.Context, event ChatEvent) error
	ListChatEvents(ctx context.Context, chatID string, limit int) ([]ChatEvent, error)
}

// SetChatEventRepository wires the recent-actions journal (see
// postgres.NewChatEventRepository).
func (s *Service) SetChatEventRepository(repo ChatEventRepository) {
	s.events = repo
}

// recordChatEvent appends one journal entry. The journal must never break the
// operation it describes, so every failure (including a missing repository, an
// empty chat id or an empty actor) is swallowed silently.
func (s *Service) recordChatEvent(ctx context.Context, chatID, actorUserID, targetUserID, eventType, payload string) {
	if s.events == nil {
		return
	}
	chatID = strings.TrimSpace(chatID)
	eventType = strings.TrimSpace(eventType)
	if chatID == "" || eventType == "" {
		return
	}
	event := ChatEvent{
		ChatID:    chatID,
		EventType: eventType,
		Payload:   chatEventPayload(payload),
	}
	if actor := strings.TrimSpace(actorUserID); actor != "" {
		event.ActorUserID = &actor
	}
	if target := strings.TrimSpace(targetUserID); target != "" {
		event.TargetUserID = &target
	}
	_ = s.events.RecordChatEvent(ctx, event)
}

// ListChatEvents returns the newest journal entries of a chat for a viewer who
// may read it (members, plus anyone for a public standalone channel).
func (s *Service) ListChatEvents(ctx context.Context, userID, chatID string, limit int) ([]ChatEvent, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatReadableForViewer(ctx, chatID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultChatEventsLimit
	}
	if limit > maxChatEventsLimit {
		limit = maxChatEventsLimit
	}
	if s.events == nil {
		return []ChatEvent{}, nil
	}
	items, err := s.events.ListChatEvents(ctx, chatID, limit)
	if err != nil {
		return nil, internal(err)
	}
	if items == nil {
		items = []ChatEvent{}
	}
	return items, nil
}

// chatEventPayload collapses a payload to one short line without newlines.
func chatEventPayload(payload string) string {
	collapsed := strings.Join(strings.Fields(payload), " ")
	runes := []rune(collapsed)
	if len(runes) > chatEventPayloadLimit {
		return string(runes[:chatEventPayloadLimit]) + "…"
	}
	return collapsed
}

// recordChatUpdateEvent journals what actually changed while updating a chat:
// the title, the avatar, the description and the icon get their own entries,
// every other persisted setting is summarised as one settings_changed row
// carrying "field=value" pairs.
func (s *Service) recordChatUpdateEvent(ctx context.Context, actorUserID string, before, after Chat) {
	chatID := strings.TrimSpace(after.ID)
	if chatID == "" {
		return
	}
	if before.Title != after.Title {
		s.recordChatEvent(ctx, chatID, actorUserID, "", ChatEventTitleChanged, after.Title)
	}
	if derefString(before.AvatarURL) != derefString(after.AvatarURL) ||
		derefString(before.AvatarBg) != derefString(after.AvatarBg) {
		s.recordChatEvent(ctx, chatID, actorUserID, "", ChatEventAvatarChanged, "")
	}
	if before.Description != after.Description {
		s.recordChatEvent(ctx, chatID, actorUserID, "", ChatEventDescriptionChanged, after.Description)
	}
	if before.IconEmoji != after.IconEmoji {
		s.recordChatEvent(ctx, chatID, actorUserID, "", ChatEventIconChanged, after.IconEmoji)
	}
	if diff := chatSettingsDiff(before, after); len(diff) > 0 {
		s.recordChatEvent(ctx, chatID, actorUserID, "", ChatEventSettingsChanged, strings.Join(diff, ", "))
	}
}

// chatSettingsDiff lists the shared settings whose stored value changed.
func chatSettingsDiff(before, after Chat) []string {
	diff := make([]string, 0, 8)
	add := func(field, value string) {
		diff = append(diff, field+"="+value)
	}
	if optText(before.ChannelType) != optText(after.ChannelType) {
		add("channel_type", optText(after.ChannelType))
	}
	if optText(before.DiscussionChatID) != optText(after.DiscussionChatID) {
		add("discussion_chat_id", optText(after.DiscussionChatID))
	}
	if optText(before.SendPermission) != optText(after.SendPermission) {
		add("send_permission", optText(after.SendPermission))
	}
	if before.CommentsEnabled != after.CommentsEnabled {
		add("comments_enabled", fmt.Sprintf("%t", after.CommentsEnabled))
	}
	if optFlag(before.ReactionsEnabled) != optFlag(after.ReactionsEnabled) {
		add("reactions_enabled", fmt.Sprintf("%t", optFlag(after.ReactionsEnabled)))
	}
	if optFlag(before.SignMessages) != optFlag(after.SignMessages) {
		add("sign_messages", fmt.Sprintf("%t", optFlag(after.SignMessages)))
	}
	if optFlag(before.ShowAuthorsProfiles) != optFlag(after.ShowAuthorsProfiles) {
		add("show_authors_profiles", fmt.Sprintf("%t", optFlag(after.ShowAuthorsProfiles)))
	}
	if optFlag(before.AutoTranslate) != optFlag(after.AutoTranslate) {
		add("auto_translate", fmt.Sprintf("%t", optFlag(after.AutoTranslate)))
	}
	if optCount(before.SlowModeSeconds) != optCount(after.SlowModeSeconds) {
		add("slow_mode_seconds", fmt.Sprintf("%d", optCount(after.SlowModeSeconds)))
	}
	if before.IsPublic != after.IsPublic {
		add("is_public", fmt.Sprintf("%t", after.IsPublic))
	}
	if optText(before.PublicSlug) != optText(after.PublicSlug) {
		add("public_slug", optText(after.PublicSlug))
	}
	return diff
}

func optText(value *string) string {
	return derefString(value)
}

func optFlag(value *bool) bool {
	if value == nil {
		return false
	}
	return *value
}

func optCount(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
