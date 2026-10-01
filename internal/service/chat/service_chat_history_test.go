package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestClearChatHistoryOnlyAffectsTheCaller(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)

	for _, content := range []string{"one", "two", "three"} {
		if _, err := svc.CreateMessage(ctx, CreateMessageInput{UserID: "u1", ChatID: chatID, Content: content}); err != nil {
			t.Fatalf("create message: %v", err)
		}
	}

	before, err := svc.ListMessages(ctx, ListMessagesInput{UserID: "u2", ChatID: chatID})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(before.Items) != 3 {
		t.Fatalf("expected 3 visible messages, got %d", len(before.Items))
	}

	cleared, err := svc.ClearChatHistory(ctx, "u1", chatID)
	if err != nil {
		t.Fatalf("clear history: %v", err)
	}
	if cleared != 3 {
		t.Fatalf("expected the caller to have seen 3 messages, got %d", cleared)
	}

	mine, err := svc.ListMessages(ctx, ListMessagesInput{UserID: "u1", ChatID: chatID})
	if err != nil {
		t.Fatalf("list messages after clear: %v", err)
	}
	if len(mine.Items) != 0 {
		t.Fatalf("expected an empty feed for the caller, got %d", len(mine.Items))
	}

	theirs, err := svc.ListMessages(ctx, ListMessagesInput{UserID: "u2", ChatID: chatID})
	if err != nil {
		t.Fatalf("list messages for peer: %v", err)
	}
	if len(theirs.Items) != 3 {
		t.Fatalf("another member must keep their history, got %d", len(theirs.Items))
	}

	// Clearing again reports nothing new.
	again, err := svc.ClearChatHistory(ctx, "u1", chatID)
	if err != nil {
		t.Fatalf("clear history twice: %v", err)
	}
	if again != 0 {
		t.Fatalf("expected 0 cleared on the second call, got %d", again)
	}
}

func TestClearChatHistoryHidesThePinnedBanner(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)

	message, err := svc.CreateMessage(ctx, CreateMessageInput{UserID: "u1", ChatID: chatID, Content: "pinned note"})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if _, err := svc.PinMessage(ctx, PinMessageInput{UserID: "u1", ChatID: chatID, MessageID: message.ID, Pinned: true}); err != nil {
		t.Fatalf("pin message: %v", err)
	}
	pinned, err := svc.GetPinnedMessage(ctx, "u1", chatID)
	if err != nil {
		t.Fatalf("get pinned: %v", err)
	}
	if pinned == nil {
		t.Fatalf("expected a pinned message before clearing")
	}

	if _, err := svc.ClearChatHistory(ctx, "u1", chatID); err != nil {
		t.Fatalf("clear history: %v", err)
	}
	after, err := svc.GetPinnedMessage(ctx, "u1", chatID)
	if err != nil {
		t.Fatalf("get pinned after clear: %v", err)
	}
	if after != nil {
		t.Fatalf("the pinned banner must follow the viewer's clear watermark")
	}

	peer, err := svc.GetPinnedMessage(ctx, "u2", chatID)
	if err != nil {
		t.Fatalf("get pinned for peer: %v", err)
	}
	if peer == nil {
		t.Fatalf("another member must still see the pin")
	}
}

func TestExportChatHistoryIncludesPollsAndAttachments(t *testing.T) {
	svc, _, msgRepo, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)

	pollMessage := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question: "Ship it?",
		Options:  []string{"Yes", "No"},
	})
	msgRepo.items = append(msgRepo.items, Message{
		ID:        "msg-2",
		ChatID:    chatID,
		UserID:    "u2",
		Content:   "here is the deck",
		CreatedAt: time.Now().UTC(),
	})
	msgRepo.attachments = map[string][]AttachmentSummary{
		"msg-2": {{ID: "att-1", Filename: "deck.pdf", MIMEType: "application/pdf", Kind: "file"}},
	}

	export, err := svc.ExportChatHistory(ctx, "u1", chatID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if export.Chat.ID != chatID || export.Chat.Title != "Polls" {
		t.Fatalf("unexpected chat meta: %+v", export.Chat)
	}
	if export.MessageCount != 2 || len(export.Messages) != 2 {
		t.Fatalf("expected 2 exported messages, got count=%d len=%d", export.MessageCount, len(export.Messages))
	}
	if export.Truncated {
		t.Fatalf("small export must not be truncated")
	}
	if export.ExportedAt.IsZero() {
		t.Fatalf("expected an export timestamp")
	}

	byID := map[string]ChatExportMessage{}
	for _, item := range export.Messages {
		byID[item.ID] = item
	}
	pollExported, ok := byID[pollMessage.ID]
	if !ok {
		t.Fatalf("poll message missing from the export: %+v", byID)
	}
	if pollExported.Poll == nil || pollExported.Poll.Question != "Ship it?" {
		t.Fatalf("poll results must ride along with the export, got %+v", pollExported.Poll)
	}
	if pollExported.Text != "Ship it?" || pollExported.SenderUserID != "u1" {
		t.Fatalf("unexpected export row: %+v", pollExported)
	}

	plain := byID["msg-2"]
	if plain.Text != "here is the deck" || plain.SenderUserID != "u2" {
		t.Fatalf("unexpected plain export row: %+v", plain)
	}
	if len(plain.Attachments) != 1 || plain.Attachments[0].Filename != "deck.pdf" {
		t.Fatalf("expected attachment summaries, got %+v", plain.Attachments)
	}
}

func TestExportChatHistoryRejectsNonMembers(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	chatID := pollTestChat(t, svc)
	if _, err := svc.ExportChatHistory(context.Background(), "stranger", chatID); err == nil {
		t.Fatalf("expected a non member export to fail")
	} else {
		assertPollErrorCode(t, err, CodeForbidden)
	}
}

func TestSetChatWallpaperEnforcesRoles(t *testing.T) {
	svc, chatRepo, _, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)
	chatRepo.roles = map[string]map[string]string{
		chatID: {"u1": "owner", "u2": "member"},
	}

	updated, err := svc.SetChatWallpaper(ctx, "u1", chatID, "preset", "grid-dark")
	if err != nil {
		t.Fatalf("owner sets wallpaper: %v", err)
	}
	if updated.WallpaperKind == nil || *updated.WallpaperKind != WallpaperKindPreset {
		t.Fatalf("expected the preset kind on the returned chat, got %+v", updated.WallpaperKind)
	}
	if updated.WallpaperValue == nil || *updated.WallpaperValue != "grid-dark" {
		t.Fatalf("expected the preset value, got %+v", updated.WallpaperValue)
	}

	stored, err := chatRepo.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if stored.WallpaperKind == nil || *stored.WallpaperKind != WallpaperKindPreset {
		t.Fatalf("wallpaper must be persisted on the chat row, got %+v", stored.WallpaperKind)
	}

	if _, err := svc.SetChatWallpaper(ctx, "u2", chatID, "preset", "grid-light"); err == nil {
		t.Fatalf("a plain member must not change the wallpaper")
	} else {
		assertPollErrorCode(t, err, CodeForbidden)
	}
	if _, err := svc.SetChatWallpaper(ctx, "stranger", chatID, "preset", "grid-light"); err == nil {
		t.Fatalf("a non member must not change the wallpaper")
	}

	if _, err := svc.SetChatWallpaper(ctx, "u1", chatID, "sparkles", "x"); err == nil {
		t.Fatalf("unknown wallpaper kind must be rejected")
	}
	if _, err := svc.SetChatWallpaper(ctx, "u1", chatID, "preset", "not a preset!"); err == nil {
		t.Fatalf("a malformed preset id must be rejected")
	}
	if _, err := svc.SetChatWallpaper(ctx, "u1", chatID, "image", "javascript:alert(1)"); err == nil {
		t.Fatalf("a non image reference must be rejected")
	}
	oversized := "data:image/png;base64," + strings.Repeat("a", maxWallpaperValueLen)
	if _, err := svc.SetChatWallpaper(ctx, "u1", chatID, "image", oversized); err == nil {
		t.Fatalf("an oversized image must be rejected")
	}
	image, err := svc.SetChatWallpaper(ctx, "u1", chatID, "image", "data:image/png;base64,AAAA")
	if err != nil {
		t.Fatalf("image wallpaper: %v", err)
	}
	if image.WallpaperKind == nil || *image.WallpaperKind != WallpaperKindImage {
		t.Fatalf("expected image kind, got %+v", image.WallpaperKind)
	}

	cleared, err := svc.SetChatWallpaper(ctx, "u1", chatID, "none", "")
	if err != nil {
		t.Fatalf("clear wallpaper: %v", err)
	}
	if cleared.WallpaperKind == nil || *cleared.WallpaperKind != WallpaperKindNone {
		t.Fatalf("expected none kind after clearing, got %+v", cleared.WallpaperKind)
	}
	if cleared.WallpaperValue != nil {
		t.Fatalf("clearing must drop the value, got %+v", cleared.WallpaperValue)
	}
}

func TestSetChatWallpaperOnDirectChatAllowsParticipants(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	ctx := context.Background()
	created, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Direct",
		MemberIDs: []string{"u2"},
	})
	if err != nil {
		t.Fatalf("create direct chat: %v", err)
	}
	if !created.IsDirect {
		t.Fatalf("setup: expected a direct chat")
	}

	updated, err := svc.SetChatWallpaper(ctx, "u1", created.ID, "preset", "grid")
	if err != nil {
		t.Fatalf("participant sets wallpaper: %v", err)
	}
	if updated.WallpaperKind == nil || *updated.WallpaperKind != WallpaperKindPreset {
		t.Fatalf("expected the wallpaper on the returned chat, got %+v", updated.WallpaperKind)
	}

	if _, err := svc.SetChatWallpaper(ctx, "stranger", created.ID, "preset", "grid"); err == nil {
		t.Fatalf("a stranger must not set the wallpaper of a direct chat")
	}
}

func TestChatDTOAlwaysSerialisesWallpaperFields(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	chatID := pollTestChat(t, svc)
	chat, err := svc.GetChat(context.Background(), "u1", chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.WallpaperKind != nil || chat.WallpaperValue != nil {
		t.Fatalf("expected an unset wallpaper before it is configured, got %+v / %+v", chat.WallpaperKind, chat.WallpaperValue)
	}
	raw, err := json.Marshal(chat)
	if err != nil {
		t.Fatalf("marshal chat: %v", err)
	}
	if !strings.Contains(string(raw), `"wallpaper_kind":null`) || !strings.Contains(string(raw), `"wallpaper_value":null`) {
		t.Fatalf("wallpaper keys must always be present, got %s", string(raw))
	}
}
