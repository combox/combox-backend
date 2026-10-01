package chat

import (
	"context"
	"strings"
	"testing"
)

// withoutSavedChats filters the lazy Saved Messages self-chat out of a list
// so pre-saved tests keep asserting their own subject (archive/pin/delete)
// instead of the always-present self-chat.
func withoutSavedChats(chats []Chat) []Chat {
	out := make([]Chat, 0, len(chats))
	for _, c := range chats {
		if IsSavedChat(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func TestCreateSavedChatIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := svc.CreateChat(ctx, CreateChatInput{UserID: "u1", Title: "Saved Messages", Kind: ChatKindSaved})
	if err != nil {
		t.Fatalf("first CreateChat(saved): %v", err)
	}
	if !IsSavedChat(first) || first.IsDirect {
		t.Fatalf("saved chat = kind %q direct %v, want kind %q direct false", first.Kind, first.IsDirect, ChatKindSaved)
	}
	second, err := svc.CreateChat(ctx, CreateChatInput{UserID: "u1", Title: "Other title", Kind: ChatKindSaved})
	if err != nil {
		t.Fatalf("second CreateChat(saved): %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("saved chat not idempotent: %q vs %q", first.ID, second.ID)
	}
	other, err := svc.CreateChat(ctx, CreateChatInput{UserID: "u2", Kind: ChatKindSaved})
	if err != nil {
		t.Fatalf("other user CreateChat(saved): %v", err)
	}
	if other.ID == first.ID {
		t.Fatalf("saved chats shared across users: %q", first.ID)
	}
}

func TestCreateSavedChatRejectsSecretType(t *testing.T) {
	ctx := context.Background()
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := svc.CreateChat(ctx, CreateChatInput{UserID: "u1", Type: ChatTypeSecretE2E, Kind: ChatKindSaved}); err == nil {
		t.Fatalf("saved chat with secret type must fail")
	}
}

func TestListChatsEnsuresSavedChat(t *testing.T) {
	ctx := context.Background()
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	items, err := svc.ListChats(ctx, "u1")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	found := false
	for _, c := range items {
		if IsSavedChat(c) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListChats did not lazily create the saved chat")
	}
	count := 0
	again, err := svc.ListChats(ctx, "u1")
	if err != nil {
		t.Fatalf("second ListChats: %v", err)
	}
	for _, c := range again {
		if IsSavedChat(c) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("saved chats after two lists = %d, want 1", count)
	}
}

func TestAddMembersRejectsSavedChat(t *testing.T) {
	ctx := context.Background()
	repo := &memChatRepo{}
	svc, err := New(repo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	saved, err := svc.CreateChat(ctx, CreateChatInput{UserID: "u1", Kind: ChatKindSaved})
	if err != nil {
		t.Fatalf("CreateChat(saved): %v", err)
	}
	// Promote the mem role to owner like the postgres repo does.
	if repo.roles[saved.ID] == nil {
		repo.roles[saved.ID] = map[string]string{}
	}
	repo.roles[saved.ID]["u1"] = "owner"
	if _, err := svc.AddMembers(ctx, "u1", saved.ID, []string{"u2"}); err == nil {
		t.Fatalf("AddMembers into saved chat must fail")
	}
}

func TestLeaveSavedChatForbidden(t *testing.T) {
	ctx := context.Background()
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	saved, err := svc.CreateChat(ctx, CreateChatInput{UserID: "u1", Kind: ChatKindSaved})
	if err != nil {
		t.Fatalf("CreateChat(saved): %v", err)
	}
	if err := svc.LeaveChat(ctx, "u1", saved.ID); err == nil {
		t.Fatalf("LeaveChat(saved) must fail")
	} else if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("LeaveChat(saved) error = %v, want forbidden", err)
	}
}
