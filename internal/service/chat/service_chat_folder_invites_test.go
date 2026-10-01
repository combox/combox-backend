package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

// memFolderInviteRepo is an in-memory ChatFolderInviteRepository with the same
// single-active-invite rule as the postgres store.
type memFolderInviteRepo struct {
	invites []ChatFolderInvite
	uses    map[string]int
}

func newMemFolderInviteRepo() *memFolderInviteRepo {
	return &memFolderInviteRepo{uses: map[string]int{}}
}

func (m *memFolderInviteRepo) GetActiveChatFolderInvite(_ context.Context, folderID string) (ChatFolderInvite, error) {
	for _, item := range m.invites {
		if item.FolderID == folderID && item.RevokedAt == nil {
			return item, nil
		}
	}
	return ChatFolderInvite{}, ErrChatFolderInviteNotFound
}

func (m *memFolderInviteRepo) CreateChatFolderInvite(_ context.Context, folderID, createdBy, token string) (ChatFolderInvite, error) {
	for _, item := range m.invites {
		if item.Token == token || (item.FolderID == folderID && item.RevokedAt == nil) {
			return ChatFolderInvite{}, ErrChatFolderInviteTaken
		}
	}
	item := ChatFolderInvite{
		ID:        folderID + "-invite",
		FolderID:  folderID,
		Token:     token,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	m.invites = append(m.invites, item)
	return item, nil
}

func (m *memFolderInviteRepo) RevokeActiveChatFolderInvite(_ context.Context, folderID string) error {
	now := time.Now().UTC()
	for i := range m.invites {
		if m.invites[i].FolderID == folderID && m.invites[i].RevokedAt == nil {
			m.invites[i].RevokedAt = &now
			return nil
		}
	}
	return ErrChatFolderInviteNotFound
}

func (m *memFolderInviteRepo) GetChatFolderInviteByToken(_ context.Context, token string) (ChatFolderInvite, error) {
	for _, item := range m.invites {
		if item.Token == token {
			return item, nil
		}
	}
	return ChatFolderInvite{}, ErrChatFolderInviteNotFound
}

func (m *memFolderInviteRepo) IncrementChatFolderInviteUse(_ context.Context, inviteID string) error {
	m.uses[inviteID]++
	return nil
}

// memFolderInviteRepo needs the folder store for owner-agnostic loads; the
// test wires it through this wrapper.
type memFolderInviteRepoWithFolders struct {
	*memFolderInviteRepo
	folders *memFolderRepo
}

func (m *memFolderInviteRepoWithFolders) GetChatFolderByID(_ context.Context, folderID string) (ChatFolder, error) {
	for i := range m.folders.folders {
		if m.folders.folders[i].ID == folderID {
			return m.folders.folders[i], nil
		}
	}
	return ChatFolder{}, ErrChatFolderNotFound
}

func newInviteService(t *testing.T) (*Service, *memFolderRepo, *memChatRepo, *memFolderInviteRepo) {
	t.Helper()
	svc, folders, chats := newFolderService(t)
	invites := newMemFolderInviteRepo()
	svc.SetChatFolderInviteRepository(&memFolderInviteRepoWithFolders{memFolderInviteRepo: invites, folders: folders})
	return svc, folders, chats, invites
}

func TestCreateChatFolderInviteReturnsActiveToken(t *testing.T) {
	svc, _, _, _ := newInviteService(t)
	ctx := context.Background()

	created, err := svc.CreateChatFolder(ctx, CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	first, err := svc.CreateChatFolderInvite(ctx, folderUserID, created.ID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if len(first.Token) != 32 {
		t.Fatalf("token = %q, want 32 hex chars", first.Token)
	}
	for _, c := range first.Token {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("token = %q, want lowercase hex", first.Token)
		}
	}
	second, err := svc.CreateChatFolderInvite(ctx, folderUserID, created.ID)
	if err != nil {
		t.Fatalf("re-create invite: %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("second token = %q, want active %q", second.Token, first.Token)
	}
}

func TestCreateChatFolderInviteScopedToOwner(t *testing.T) {
	svc, _, _, _ := newInviteService(t)
	ctx := context.Background()

	created, err := svc.CreateChatFolder(ctx, CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if _, err := svc.CreateChatFolderInvite(ctx, folderOtherUserID, created.ID); err == nil {
		t.Fatalf("foreign user must not mint an invite")
	} else {
		wantCode(t, err, CodeNotFound)
	}
	if _, err := svc.CreateChatFolderInvite(ctx, folderUserID, "nope"); err == nil {
		t.Fatalf("malformed folder id must fail")
	} else {
		wantCode(t, err, CodeNotFound)
	}
}

func TestResolveChatFolderInviteReportsMembership(t *testing.T) {
	svc, _, chats, _ := newInviteService(t)
	ctx := context.Background()

	slug := "work-public"
	for i := range chats.chats {
		switch chats.chats[i].ID {
		case folderMemberChatID:
			chats.chats[i].Title = "Member chat"
			chats.chats[i].Kind = "group"
		case folderForeignChat:
			chats.chats[i].Title = "Foreign chat"
			chats.chats[i].Kind = "channel"
			chats.chats[i].PublicSlug = &slug
			if chats.members[folderForeignChat] == nil {
				chats.members[folderForeignChat] = map[string]bool{}
			}
			// The owner puts both chats into the folder; the resolver belongs
			// to only the first one.
			chats.members[folderForeignChat][folderUserID] = true
		}
	}

	created, err := svc.CreateChatFolder(ctx, CreateChatFolderInput{
		UserID:  folderUserID,
		Name:    "Work",
		Icon:    "💼",
		ChatIDs: []string{folderMemberChatID, folderForeignChat},
	})
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	invite, err := svc.CreateChatFolderInvite(ctx, folderUserID, created.ID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	resolved, err := svc.ResolveChatFolderInvite(ctx, folderOtherUserID, invite.Token)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.FolderName != "Work" || resolved.FolderEmoji != "💼" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if len(resolved.Chats) != 2 {
		t.Fatalf("chats = %+v", resolved.Chats)
	}
	if resolved.Chats[0].ID != folderMemberChatID || resolved.Chats[0].Title != "Member chat" || resolved.Chats[0].Kind != "group" {
		t.Fatalf("first chat = %+v", resolved.Chats[0])
	}
	if resolved.Chats[0].IsMember {
		t.Fatalf("resolver must not be a member of the first chat: %+v", resolved.Chats[0])
	}
	if resolved.Chats[1].ID != folderForeignChat || !resolved.Chats[1].IsMember {
		t.Fatalf("second chat = %+v", resolved.Chats[1])
	}
	if resolved.Chats[1].PublicSlug == nil || *resolved.Chats[1].PublicSlug != slug {
		t.Fatalf("public slug = %+v", resolved.Chats[1])
	}
}

func TestRevokeChatFolderInviteHidesToken(t *testing.T) {
	svc, _, _, _ := newInviteService(t)
	ctx := context.Background()

	created, err := svc.CreateChatFolder(ctx, CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	invite, err := svc.CreateChatFolderInvite(ctx, folderUserID, created.ID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := svc.RevokeChatFolderInvite(ctx, folderOtherUserID, created.ID); err == nil {
		t.Fatalf("foreign user must not revoke")
	} else {
		wantCode(t, err, CodeNotFound)
	}
	if err := svc.RevokeChatFolderInvite(ctx, folderUserID, created.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.ResolveChatFolderInvite(ctx, folderOtherUserID, invite.Token); err == nil {
		t.Fatalf("revoked token must not resolve")
	} else {
		wantCode(t, err, CodeNotFound)
	}
	if err := svc.RevokeChatFolderInvite(ctx, folderUserID, created.ID); err == nil {
		t.Fatalf("second revoke must report nothing to revoke")
	} else {
		wantCode(t, err, CodeNotFound)
	}
	if _, err := svc.ResolveChatFolderInvite(ctx, folderOtherUserID, "unknown-token"); err == nil {
		t.Fatalf("unknown token must not resolve")
	} else {
		wantCode(t, err, CodeNotFound)
	}
}
