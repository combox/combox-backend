package chat

import (
	"context"
	"errors"
	"testing"
)

// Regression for POST /api/private/v1/chats/{id}/invite-links -> 500 and
// GET .../invite-links -> 500 on private groups/channels.
//
// Root cause: postgres ListChatInviteLinks/CreateChatInviteLink/
// GetChatInviteLinkByToken selected raw timestamptz (revoked_at, created_at)
// while ChatInviteLink carries them as string/*string, so pgx failed to scan
// every row and the service mapped the error to internal -> HTTP 500.
// The fix casts both columns to text in SQL (same idiom as
// ListPublicChannelBans); these tests pin the service-level contract for
// private groups, group channels and private standalone channels.
func TestInviteLinksPrivateGroupCreateAndList(t *testing.T) {
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "owner-1",
		Title:     "Private team",
		MemberIDs: []string{"owner-1", "member-1"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create private group: %v", err)
	}

	created, err := svc.CreateInviteLink(ctx, CreateInviteLinkInput{
		UserID: "owner-1",
		ChatID: group.ID,
		Title:  "for friends",
	})
	if err != nil {
		t.Fatalf("owner create invite link: %v", err)
	}
	if created.ChatID != group.ID || created.Token == "" {
		t.Fatalf("bad link: %+v", created)
	}
	if created.Title == nil || *created.Title != "for friends" {
		t.Fatalf("expected title preserved, got %+v", created.Title)
	}
	if created.CreatedAt == "" {
		t.Fatalf("expected created_at to be set")
	}

	items, err := svc.ListInviteLinks(ctx, "owner-1", group.ID)
	if err != nil {
		t.Fatalf("owner list invite links: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least the created link")
	}
	found := false
	for _, item := range items {
		if item.Token == created.Token {
			found = true
		}
	}
	if !found {
		t.Fatalf("created link missing from list: %+v", items)
	}

	// Plain members must not manage links.
	if _, err := svc.CreateInviteLink(ctx, CreateInviteLinkInput{
		UserID: "member-1",
		ChatID: group.ID,
	}); err == nil {
		t.Fatalf("expected member create to fail")
	} else {
		wantCode(t, err, CodeForbidden)
	}
	if _, err := svc.ListInviteLinks(ctx, "member-1", group.ID); err == nil {
		t.Fatalf("expected member list to fail")
	} else {
		wantCode(t, err, CodeForbidden)
	}
}

func TestInviteLinksPrivateGroupChannelCreateAndList(t *testing.T) {
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "owner-1",
		Title:     "Private team",
		MemberIDs: []string{"owner-1", "member-1"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create private group: %v", err)
	}
	channel, err := svc.CreateChannel(ctx, CreateChannelInput{
		UserID:      "owner-1",
		GroupChatID: group.ID,
		Title:       "voice-room",
		ChannelType: "voice",
	})
	if err != nil {
		t.Fatalf("create private group channel: %v", err)
	}

	created, err := svc.CreateInviteLink(ctx, CreateInviteLinkInput{
		UserID: "owner-1",
		ChatID: channel.ID,
	})
	if err != nil {
		t.Fatalf("owner create channel invite link: %v", err)
	}
	if created.ChatID != channel.ID || created.Token == "" || created.CreatedAt == "" {
		t.Fatalf("bad channel link: %+v", created)
	}

	items, err := svc.ListInviteLinks(ctx, "owner-1", channel.ID)
	if err != nil {
		t.Fatalf("owner list channel invite links: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least the created channel link")
	}
}

func TestInviteLinksPrivateStandaloneChannelCreateAndList(t *testing.T) {
	repo := &memChatRepo{}
	svc, err := New(repo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	private, err := repo.CreatePublicChannel(ctx, "Secret news", "", "owner-1", false)
	if err != nil {
		t.Fatalf("create private standalone channel: %v", err)
	}
	if private.IsPublic {
		t.Fatalf("expected a private standalone channel")
	}

	created, err := svc.CreateInviteLink(ctx, CreateInviteLinkInput{
		UserID: "owner-1",
		ChatID: private.ID,
		Title:  "join us",
	})
	if err != nil {
		t.Fatalf("owner create private channel invite link: %v", err)
	}
	if created.ChatID != private.ID || created.Token == "" || created.CreatedAt == "" {
		t.Fatalf("bad private channel link: %+v", created)
	}

	items, err := svc.ListInviteLinks(ctx, "owner-1", private.ID)
	if err != nil {
		t.Fatalf("owner list private channel invite links: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least the created private channel link")
	}
}

func TestInviteLinksListAutoCreatesPrimary(t *testing.T) {
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "owner-1",
		Title:     "Private team",
		MemberIDs: []string{"owner-1"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create private group: %v", err)
	}

	first, err := svc.ListInviteLinks(ctx, "owner-1", group.ID)
	if err != nil {
		t.Fatalf("first list (auto-create primary): %v", err)
	}
	if len(first) != 1 || !first[0].IsPrimary {
		t.Fatalf("expected exactly one primary link, got %+v", first)
	}

	second, err := svc.ListInviteLinks(ctx, "owner-1", group.ID)
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if len(second) != 1 || second[0].Token != first[0].Token {
		t.Fatalf("expected stable primary link, first=%+v second=%+v", first, second)
	}
}

// racyInviteRepo fails the first primary auto-create (simulating a lost
// race on idx_chat_invite_links_primary_unique) while the winner's row is
// already visible to List. ListInviteLinks must serve the winner instead
// of 500ing with internal.
type racyInviteRepo struct {
	*memChatRepo
	failNextCreate bool
}

func (r *racyInviteRepo) CreateChatInviteLink(ctx context.Context, chatID, createdBy, title string, isPrimary bool) (ChatInviteLink, error) {
	if r.failNextCreate {
		r.failNextCreate = false
		seeded, seedErr := r.memChatRepo.CreateChatInviteLink(ctx, chatID, "owner-1", "", true)
		if seedErr != nil {
			return ChatInviteLink{}, seedErr
		}
		_ = seeded
		return ChatInviteLink{}, errors.New(`duplicate key value violates unique constraint "idx_chat_invite_links_primary_unique"`)
	}
	return r.memChatRepo.CreateChatInviteLink(ctx, chatID, createdBy, title, isPrimary)
}

func TestInviteLinksListPrimaryRaceServesWinner(t *testing.T) {
	base := &memChatRepo{}
	svc, err := New(&racyInviteRepo{memChatRepo: base, failNextCreate: true}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "owner-1",
		Title:     "Private team",
		MemberIDs: []string{"owner-1"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create private group: %v", err)
	}

	items, err := svc.ListInviteLinks(ctx, "owner-1", group.ID)
	if err != nil {
		t.Fatalf("list under primary race must not fail: %v", err)
	}
	if len(items) != 1 || !items[0].IsPrimary {
		t.Fatalf("expected the winner primary link, got %+v", items)
	}
}
