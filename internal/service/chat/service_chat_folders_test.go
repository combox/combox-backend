package chat

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	folderUserID       = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	folderOtherUserID  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	folderMemberChatID = "11111111-1111-4111-8111-111111111111"
	folderForeignChat  = "22222222-2222-4222-8222-222222222222"
	folderMissingChat  = "33333333-3333-4333-8333-333333333333"
	folderOwnedID      = "44444444-4444-4444-8444-444444444444"
	folderForeignID    = "55555555-5555-4555-8555-555555555555"
)

// memFolderRepo is an in-memory ChatFolderRepository that keeps ownership so
// the user scoping rules of the real store are exercised too.
type memFolderRepo struct {
	folders   []ChatFolder
	owners    map[string]string
	createErr error
	seq       int
}

func newMemFolderRepo() *memFolderRepo {
	return &memFolderRepo{owners: map[string]string{}}
}

func (m *memFolderRepo) owned(userID, folderID string) (int, bool) {
	for i := range m.folders {
		if m.folders[i].ID == folderID && m.owners[folderID] == userID {
			return i, true
		}
	}
	return -1, false
}

func (m *memFolderRepo) ListChatFolders(_ context.Context, userID string) ([]ChatFolder, error) {
	out := []ChatFolder{}
	for i := range m.folders {
		if m.owners[m.folders[i].ID] != userID {
			continue
		}
		item := m.folders[i]
		item.ChatIDs = append([]string{}, item.ChatIDs...)
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

func (m *memFolderRepo) GetChatFolder(_ context.Context, userID, folderID string) (ChatFolder, error) {
	i, ok := m.owned(userID, folderID)
	if !ok {
		return ChatFolder{}, ErrChatFolderNotFound
	}
	return m.folders[i], nil
}

func (m *memFolderRepo) CreateChatFolder(_ context.Context, input CreateChatFolderInput, maxFolders int) (ChatFolder, error) {
	if m.createErr != nil {
		return ChatFolder{}, m.createErr
	}
	if len(m.folders) >= maxFolders {
		return ChatFolder{}, ErrChatFolderLimit
	}
	for i := range m.folders {
		if m.owners[m.folders[i].ID] == input.UserID && m.folders[i].Name == input.Name {
			return ChatFolder{}, ErrChatFolderNameTaken
		}
	}
	m.seq++
	item := ChatFolder{
		ID:        folderID(m.seq),
		Name:      input.Name,
		Icon:      input.Icon,
		Position:  len(m.folders),
		CreatedAt: time.Unix(int64(m.seq), 0).UTC(),
		ChatIDs:   append([]string{}, input.ChatIDs...),
	}
	m.folders = append(m.folders, item)
	m.owners[item.ID] = input.UserID
	return item, nil
}

func (m *memFolderRepo) UpdateChatFolderMeta(_ context.Context, userID, folderID string, name, icon *string) (ChatFolder, error) {
	i, ok := m.owned(userID, folderID)
	if !ok {
		return ChatFolder{}, ErrChatFolderNotFound
	}
	if name != nil {
		for j := range m.folders {
			if j != i && m.owners[m.folders[j].ID] == userID && m.folders[j].Name == *name {
				return ChatFolder{}, ErrChatFolderNameTaken
			}
		}
		m.folders[i].Name = *name
	}
	if icon != nil {
		m.folders[i].Icon = *icon
	}
	return m.folders[i], nil
}

func (m *memFolderRepo) MoveChatFolder(_ context.Context, userID, folderID string, position int) (ChatFolder, error) {
	i, ok := m.owned(userID, folderID)
	if !ok {
		return ChatFolder{}, ErrChatFolderNotFound
	}
	mine := []ChatFolder{}
	others := []ChatFolder{}
	for j := range m.folders {
		if m.owners[m.folders[j].ID] == userID {
			mine = append(mine, m.folders[j])
		} else {
			others = append(others, m.folders[j])
		}
	}
	sort.Slice(mine, func(a, b int) bool { return mine[a].Position < mine[b].Position })
	moving := mine[i]
	rest := append([]ChatFolder{}, mine[:i]...)
	rest = append(rest, mine[i+1:]...)
	target := position
	if target < 0 {
		target = 0
	}
	if target > len(rest) {
		target = len(rest)
	}
	ordered := append(append([]ChatFolder{}, rest[:target]...), moving)
	ordered = append(ordered, rest[target:]...)
	for idx := range ordered {
		ordered[idx].Position = idx
	}
	m.folders = append(others, ordered...)
	return moving, nil
}

func (m *memFolderRepo) ReplaceChatFolderChats(_ context.Context, userID, folderID string, chatIDs []string) (ChatFolder, error) {
	i, ok := m.owned(userID, folderID)
	if !ok {
		return ChatFolder{}, ErrChatFolderNotFound
	}
	m.folders[i].ChatIDs = append([]string{}, chatIDs...)
	return m.folders[i], nil
}

func (m *memFolderRepo) DeleteChatFolder(_ context.Context, userID, folderID string) error {
	i, ok := m.owned(userID, folderID)
	if !ok {
		return ErrChatFolderNotFound
	}
	m.folders = append(m.folders[:i], m.folders[i+1:]...)
	delete(m.owners, folderID)
	return nil
}

func folderID(seq int) string {
	return fmt.Sprintf("90000000-0000-4000-8000-%012d", seq)
}

// newFolderService builds a service with a chat the caller belongs to, a chat
// they do not belong to, and a folder store.
func newFolderService(t *testing.T) (*Service, *memFolderRepo, *memChatRepo) {
	t.Helper()
	chatRepo := &memChatRepo{
		chats: []Chat{
			{ID: folderMemberChatID},
			{ID: folderForeignChat},
		},
		members: map[string]map[string]bool{
			folderMemberChatID: {folderUserID: true},
			folderForeignChat:  {folderOtherUserID: true},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	repo := newMemFolderRepo()
	svc.SetChatFolderRepository(repo)
	return svc, repo, chatRepo
}

func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %q, got nil", code)
	}
	var svcErr *Error
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *Error with code %q, got %T: %v", code, err, err)
	}
	if svcErr.Code != code {
		t.Fatalf("code = %q, want %q (err %v)", svcErr.Code, code, err)
	}
	return svcErr
}

func TestCreateChatFolderRejectsBadName(t *testing.T) {
	svc, _, _ := newFolderService(t)
	for _, name := range []string{"", "   ", strings.Repeat("ы", 33)} {
		_, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
			UserID: folderUserID,
			Name:   name,
		})
		wantCode(t, err, CodeInvalidArgument)
	}
}

func TestCreateChatFolderRejectsLongIcon(t *testing.T) {
	svc, _, _ := newFolderService(t)
	_, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID: folderUserID,
		Name:   "Work",
		Icon:   strings.Repeat("🚀", chatFolderIconMaxRunes+1),
	})
	wantCode(t, err, CodeInvalidArgument)

	// The limit is generous for a single glyph run but still bounded.
	if _, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID: folderUserID,
		Name:   "Work",
		Icon:   strings.Repeat("🚀", chatFolderIconMaxRunes),
	}); err != nil {
		t.Fatalf("an icon of exactly %d runes must be accepted: %v", chatFolderIconMaxRunes, err)
	}
}

func TestCreateChatFolderRejectsMalformedChatID(t *testing.T) {
	svc, _, _ := newFolderService(t)
	_, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID:  folderUserID,
		Name:    "Work",
		ChatIDs: []string{"not-a-uuid"},
	})
	svcErr := wantCode(t, err, CodeInvalidArgument)
	if svcErr.Details["chat_id"] != "not-a-uuid" {
		t.Fatalf("details = %v", svcErr.Details)
	}
}

func TestCreateChatFolderRejectsForeignChat(t *testing.T) {
	svc, _, _ := newFolderService(t)
	_, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID:  folderUserID,
		Name:    "Work",
		ChatIDs: []string{folderForeignChat},
	})
	wantCode(t, err, CodeForbidden)
}

func TestCreateChatFolderRejectsMissingChat(t *testing.T) {
	svc, _, _ := newFolderService(t)
	_, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID:  folderUserID,
		Name:    "Work",
		ChatIDs: []string{folderMissingChat},
	})
	wantCode(t, err, CodeNotFound)
}

func TestCreateChatFolderRejectsTooManyChats(t *testing.T) {
	svc, _, chatRepo := newFolderService(t)
	ids := make([]string, 0, MaxChatFolderChats+1)
	for i := 0; i < MaxChatFolderChats+1; i++ {
		id := fmt.Sprintf("10000000-0000-4000-8000-%012x", i+1)
		ids = append(ids, id)
		chatRepo.chats = append(chatRepo.chats, Chat{ID: id})
		chatRepo.members[id] = map[string]bool{folderUserID: true}
	}
	_, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID:  folderUserID,
		Name:    "Work",
		ChatIDs: ids,
	})
	wantCode(t, err, CodeInvalidArgument)
}

func TestCreateChatFolderDeduplicatesChatIDs(t *testing.T) {
	svc, repo, _ := newFolderService(t)
	created, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID:  folderUserID,
		Name:    " Work ",
		ChatIDs: []string{" " + folderMemberChatID + " ", folderMemberChatID, folderMemberChatID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "Work" {
		t.Fatalf("name = %q, want it trimmed", created.Name)
	}
	if len(created.ChatIDs) != 1 || created.ChatIDs[0] != folderMemberChatID {
		t.Fatalf("chat_ids = %v, want one deduplicated entry", created.ChatIDs)
	}
	if len(repo.folders) != 1 {
		t.Fatalf("folders = %d, want 1", len(repo.folders))
	}
}

func TestCreateChatFolderMapsSentinels(t *testing.T) {
	svc, repo, _ := newFolderService(t)
	repo.createErr = ErrChatFolderNameTaken
	_, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	wantCode(t, err, CodeAlreadyExists)

	repo.createErr = ErrChatFolderLimit
	_, err = svc.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	svcErr := wantCode(t, err, CodeInvalidArgument)
	if svcErr.Details["limit"] != "12" {
		t.Fatalf("details = %v", svcErr.Details)
	}

	repo.createErr = errors.New("connection reset")
	_, err = svc.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	wantCode(t, err, CodeInternal)
	repo.createErr = nil
}

func TestListChatFoldersReturnsEmptySlice(t *testing.T) {
	svc, _, _ := newFolderService(t)
	items, err := svc.ListChatFolders(context.Background(), folderUserID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("items = %v, want a non-nil empty slice", items)
	}
}

func TestListChatFoldersRequiresUserID(t *testing.T) {
	svc, _, _ := newFolderService(t)
	if _, err := svc.ListChatFolders(context.Background(), "  "); err == nil {
		t.Fatalf("expected an error for an empty user id")
	}
}

func TestUpdateChatFolderScopedToOwner(t *testing.T) {
	svc, repo, _ := newFolderService(t)
	created, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Somebody else's folder.
	foreign, err := repo.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderOtherUserID, Name: "Secret"}, MaxChatFoldersPerUser)
	if err != nil {
		t.Fatalf("create foreign: %v", err)
	}

	name := "Renamed"
	_, err = svc.UpdateChatFolder(context.Background(), UpdateChatFolderInput{
		UserID:   folderUserID,
		FolderID: foreign.ID,
		Name:     OptionalString{Set: true, Value: &name},
	})
	wantCode(t, err, CodeNotFound)

	_, err = svc.UpdateChatFolder(context.Background(), UpdateChatFolderInput{
		UserID:   folderUserID,
		FolderID: folderForeignID,
		Name:     OptionalString{Set: true, Value: &name},
	})
	wantCode(t, err, CodeNotFound)

	updated, err := svc.UpdateChatFolder(context.Background(), UpdateChatFolderInput{
		UserID:   folderUserID,
		FolderID: created.ID,
		Name:     OptionalString{Set: true, Value: &name},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Fatalf("name = %q", updated.Name)
	}
}

func TestUpdateChatFolderRejectsBadInput(t *testing.T) {
	svc, _, _ := newFolderService(t)
	_, err := svc.UpdateChatFolder(context.Background(), UpdateChatFolderInput{
		UserID:   folderUserID,
		FolderID: "not-a-uuid",
	})
	wantCode(t, err, CodeNotFound)

	empty := ""
	_, err = svc.UpdateChatFolder(context.Background(), UpdateChatFolderInput{
		UserID:   folderUserID,
		FolderID: folderOwnedID,
		Name:     OptionalString{Set: true, Value: &empty},
	})
	wantCode(t, err, CodeInvalidArgument)
}

func TestSetChatFolderChatsValidatesAndReplaces(t *testing.T) {
	svc, _, _ := newFolderService(t)
	created, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{
		UserID:  folderUserID,
		Name:    "Work",
		ChatIDs: []string{folderMemberChatID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	item, err := svc.SetChatFolderChats(context.Background(), folderUserID, created.ID, nil)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(item.ChatIDs) != 0 {
		t.Fatalf("chat_ids = %v, want an empty list", item.ChatIDs)
	}

	_, err = svc.SetChatFolderChats(context.Background(), folderUserID, created.ID, []string{folderForeignChat})
	wantCode(t, err, CodeForbidden)

	_, err = svc.SetChatFolderChats(context.Background(), folderUserID, "nope", []string{})
	wantCode(t, err, CodeNotFound)
}

func TestDeleteChatFolderScopedToOwner(t *testing.T) {
	svc, repo, _ := newFolderService(t)
	created, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	foreign, err := repo.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderOtherUserID, Name: "Secret"}, MaxChatFoldersPerUser)
	if err != nil {
		t.Fatalf("create foreign: %v", err)
	}

	if err := svc.DeleteChatFolder(context.Background(), folderUserID, foreign.ID); err == nil {
		t.Fatalf("deleting somebody else's folder must fail")
	} else {
		wantCode(t, err, CodeNotFound)
	}
	if err := svc.DeleteChatFolder(context.Background(), folderUserID, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	items, err := svc.ListChatFolders(context.Background(), folderUserID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %v, want none", items)
	}
}

func TestChatFolderRepositoryIsRequired(t *testing.T) {
	svc, err := New(&memChatRepo{}, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := svc.ListChatFolders(context.Background(), folderUserID); err == nil {
		t.Fatalf("expected an error when the folder repository is not wired")
	} else {
		wantCode(t, err, CodeInternal)
	}
}

func TestUpdateChatFolderWithoutChangesReturnsCurrentFolder(t *testing.T) {
	svc, _, _ := newFolderService(t)
	created, err := svc.CreateChatFolder(context.Background(), CreateChatFolderInput{UserID: folderUserID, Name: "Work"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := svc.UpdateChatFolder(context.Background(), UpdateChatFolderInput{
		UserID:   folderUserID,
		FolderID: created.ID,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.ID != created.ID || updated.Name != "Work" {
		t.Fatalf("updated = %+v, want the current folder", updated)
	}
}
