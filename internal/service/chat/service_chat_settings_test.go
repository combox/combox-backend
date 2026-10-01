package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// memEventRepo is an in-memory ChatEventRepository that returns entries newest
// first, mirroring the postgres journal (created_at DESC, id DESC).
type memEventRepo struct {
	events []ChatEvent
}

func (m *memEventRepo) RecordChatEvent(_ context.Context, event ChatEvent) error {
	if event.ID == "" {
		event.ID = fmt.Sprintf("evt-%d", len(m.events)+1)
	}
	m.events = append(m.events, event)
	return nil
}

func (m *memEventRepo) ListChatEvents(_ context.Context, chatID string, limit int) ([]ChatEvent, error) {
	out := make([]ChatEvent, 0, len(m.events))
	for i := len(m.events) - 1; i >= 0; i-- {
		if len(out) == limit {
			break
		}
		if m.events[i].ChatID == chatID {
			out = append(out, m.events[i])
		}
	}
	return out, nil
}

func newSettingsTestService(t *testing.T, events ChatEventRepository) (*Service, *memChatRepo, string) {
	t.Helper()
	chatRepo := &memChatRepo{}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if events != nil {
		svc.SetChatEventRepository(events)
	}
	group, err := svc.CreateChat(context.Background(), CreateChatInput{
		UserID:    "u1",
		Title:     "Team",
		MemberIDs: []string{"u2"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	return svc, chatRepo, group.ID
}

func TestUpdateChatPersistsDescriptionIconAndChannelType(t *testing.T) {
	svc, _, chatID := newSettingsTestService(t, nil)
	ctx := context.Background()

	updated, err := svc.UpdateChat(ctx, UpdateChatInput{
		UserID:      "u1",
		ChatID:      chatID,
		Description: OptionalString{Set: true, Value: strPtr("Читаем регламент")},
		IconEmoji:   OptionalString{Set: true, Value: strPtr("📌")},
		ChannelType: OptionalString{Set: true, Value: strPtr(ChannelTypeVoice)},
	})
	if err != nil {
		t.Fatalf("update chat: %v", err)
	}
	if updated.Description != "Читаем регламент" {
		t.Fatalf("expected description to be persisted, got %q", updated.Description)
	}
	if updated.IconEmoji != "📌" {
		t.Fatalf("expected icon_emoji to be persisted, got %q", updated.IconEmoji)
	}
	if updated.ChannelType == nil || *updated.ChannelType != ChannelTypeVoice {
		t.Fatalf("expected channel_type=voice, got %v", updated.ChannelType)
	}

	fetched, err := svc.GetChat(ctx, "u1", chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if fetched.Description != "Читаем регламент" || fetched.IconEmoji != "📌" {
		t.Fatalf("expected settings to round trip, got description=%q icon=%q", fetched.Description, fetched.IconEmoji)
	}
	if fetched.ChannelType == nil || *fetched.ChannelType != ChannelTypeVoice {
		t.Fatalf("expected channel_type=voice after fetch, got %v", fetched.ChannelType)
	}

	// An explicit empty value clears description/icon but keeps the type.
	cleared, err := svc.UpdateChat(ctx, UpdateChatInput{
		UserID:      "u1",
		ChatID:      chatID,
		Description: OptionalString{Set: true, Value: strPtr("")},
		IconEmoji:   OptionalString{Set: true, Value: strPtr("")},
		ChannelType: OptionalString{Set: true, Value: strPtr("")},
	})
	if err != nil {
		t.Fatalf("clear settings: %v", err)
	}
	if cleared.Description != "" || cleared.IconEmoji != "" {
		t.Fatalf("expected description and icon to be cleared, got %q / %q", cleared.Description, cleared.IconEmoji)
	}
	if cleared.ChannelType == nil || *cleared.ChannelType != ChannelTypeVoice {
		t.Fatalf("expected empty channel_type to keep the current type, got %v", cleared.ChannelType)
	}

	// A JSON null (nil value with Set=false) leaves everything unchanged.
	unchanged, err := svc.UpdateChat(ctx, UpdateChatInput{
		UserID: "u1",
		ChatID: chatID,
		Title:  OptionalString{Set: true, Value: strPtr("Team")},
	})
	if err != nil {
		t.Fatalf("update title only: %v", err)
	}
	if unchanged.Description != "" || unchanged.IconEmoji != "" {
		t.Fatalf("expected untouched settings, got %q / %q", unchanged.Description, unchanged.IconEmoji)
	}
}

func TestUpdateChatValidatesDescriptionIconAndChannelType(t *testing.T) {
	svc, _, chatID := newSettingsTestService(t, nil)
	ctx := context.Background()

	cases := []struct {
		name  string
		input UpdateChatInput
	}{
		{
			name:  "description over the 255 rune cap is rejected",
			input: UpdateChatInput{Description: OptionalString{Set: true, Value: strPtr(strings.Repeat("a", ChatDescriptionMaxLen+1))}},
		},
		{
			name:  "icon over the 8 rune cap is rejected",
			input: UpdateChatInput{IconEmoji: OptionalString{Set: true, Value: strPtr(strings.Repeat("🙂", ChatIconEmojiMaxLen+1))}},
		},
		{
			name:  "unknown channel type is rejected",
			input: UpdateChatInput{ChannelType: OptionalString{Set: true, Value: strPtr("email")}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			input.UserID = "u1"
			input.ChatID = chatID
			_, err := svc.UpdateChat(ctx, input)
			expectChatErrorCode(t, err, CodeInvalidArgument)
		})
	}

	// Boundaries are accepted.
	if _, err := svc.UpdateChat(ctx, UpdateChatInput{
		UserID:      "u1",
		ChatID:      chatID,
		Description: OptionalString{Set: true, Value: strPtr(strings.Repeat("a", ChatDescriptionMaxLen))},
		IconEmoji:   OptionalString{Set: true, Value: strPtr(strings.Repeat("🙂", ChatIconEmojiMaxLen))},
		ChannelType: OptionalString{Set: true, Value: strPtr(ChannelTypeText)},
	}); err != nil {
		t.Fatalf("boundary values should be accepted: %v", err)
	}
}

func TestChatSettingsChangesAreJournaled(t *testing.T) {
	events := &memEventRepo{}
	svc, _, chatID := newSettingsTestService(t, events)
	ctx := context.Background()

	if _, err := svc.UpdateChat(ctx, UpdateChatInput{
		UserID:           "u1",
		ChatID:           chatID,
		Title:            OptionalString{Set: true, Value: strPtr("Team renamed")},
		Description:      OptionalString{Set: true, Value: strPtr("New blurb")},
		IconEmoji:        OptionalString{Set: true, Value: strPtr("📌")},
		ReactionsEnabled: OptionalBool{Set: true, Value: false},
	}); err != nil {
		t.Fatalf("update chat: %v", err)
	}

	items, err := svc.ListChatEvents(ctx, "u1", chatID, 50)
	if err != nil {
		t.Fatalf("list chat events: %v", err)
	}
	byType := map[string]string{}
	for _, item := range items {
		if item.ChatID != chatID {
			t.Fatalf("event %s carries chat_id=%q, want %q", item.EventType, item.ChatID, chatID)
		}
		if item.ActorUserID == nil || *item.ActorUserID != "u1" {
			t.Fatalf("event %s should be attributed to u1, got %v", item.EventType, item.ActorUserID)
		}
		if _, seen := byType[item.EventType]; seen {
			t.Fatalf("duplicate event type %q", item.EventType)
		}
		byType[item.EventType] = item.Payload
	}
	if byType[ChatEventTitleChanged] != "Team renamed" {
		t.Fatalf("expected title_changed payload %q, got %q", "Team renamed", byType[ChatEventTitleChanged])
	}
	if byType[ChatEventDescriptionChanged] != "New blurb" {
		t.Fatalf("expected description_changed payload %q, got %q", "New blurb", byType[ChatEventDescriptionChanged])
	}
	if byType[ChatEventIconChanged] != "📌" {
		t.Fatalf("expected icon_changed payload %q, got %q", "📌", byType[ChatEventIconChanged])
	}
	if settings := byType[ChatEventSettingsChanged]; !strings.Contains(settings, "reactions_enabled=false") {
		t.Fatalf("expected settings_changed payload to mention reactions_enabled=false, got %q", settings)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 journaled events, got %d (%v)", len(items), items)
	}

	// A rejected update journals nothing.
	before := len(events.events)
	_, err = svc.UpdateChat(ctx, UpdateChatInput{
		UserID:      "u1",
		ChatID:      chatID,
		ChannelType: OptionalString{Set: true, Value: strPtr("email")},
	})
	expectChatErrorCode(t, err, CodeInvalidArgument)
	if len(events.events) != before {
		t.Fatalf("expected no events for a rejected update, got %d new", len(events.events)-before)
	}

	// The journal is readable by members only.
	if _, err := svc.ListChatEvents(ctx, "u3", chatID, 50); err == nil {
		t.Fatalf("expected a non member to be denied access to the journal")
	}
}

func TestListChatEventsHonoursTheLimit(t *testing.T) {
	events := &memEventRepo{}
	svc, _, chatID := newSettingsTestService(t, events)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.UpdateChat(ctx, UpdateChatInput{
			UserID:      "u1",
			ChatID:      chatID,
			Description: OptionalString{Set: true, Value: strPtr(fmt.Sprintf("blurb %d", i))},
		}); err != nil {
			t.Fatalf("update chat: %v", err)
		}
	}
	items, err := svc.ListChatEvents(ctx, "u1", chatID, 2)
	if err != nil {
		t.Fatalf("list chat events: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected the limit to cap the page at 2, got %d", len(items))
	}
	if items[0].Payload != "blurb 2" {
		t.Fatalf("expected the newest entry first, got %q", items[0].Payload)
	}
}
