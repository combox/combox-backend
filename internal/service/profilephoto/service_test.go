package profilephoto

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	records   []Record
	added     []Record
	deleted   []Record
	listErr   error
	deleteErr error
	lastKind  string
	lastOwner string
	lastLimit int
}

func (f *fakeStore) Add(_ context.Context, ownerKind, ownerID, objectKey string) error {
	f.added = append(f.added, Record{OwnerKind: ownerKind, OwnerID: ownerID, ObjectKey: objectKey})
	return nil
}

func (f *fakeStore) Delete(_ context.Context, ownerKind, ownerID, photoID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, Record{OwnerKind: ownerKind, OwnerID: ownerID, ID: photoID})
	return nil
}

func (f *fakeStore) List(_ context.Context, ownerKind, ownerID string, limit int) ([]Record, error) {
	f.lastKind = ownerKind
	f.lastOwner = ownerID
	f.lastLimit = limit
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.records, nil
}

type fakePresigner struct{}

func (fakePresigner) PresignGetObject(_ context.Context, objectKey string, _ time.Duration) (string, error) {
	if objectKey == "broken" {
		return "", errors.New("presign failed")
	}
	return "https://cdn.test/" + objectKey, nil
}

type stubUserAccess struct{ err error }

func (s stubUserAccess) CanViewUserPhotos(context.Context, string, string) error { return s.err }

type stubChatAccess struct {
	err       error
	deleteErr error
}

func (s stubChatAccess) CanViewChatPhotos(context.Context, string, string) error { return s.err }
func (s stubChatAccess) CanDeleteChatPhotos(context.Context, string, string) error {
	return s.deleteErr
}

func newTestService(t *testing.T, store Store, user UserAccess, chat ChatAccess) *Service {
	t.Helper()
	svc, err := New(Config{
		Store:      store,
		Avatars:    fakePresigner{},
		UserAccess: user,
		ChatAccess: chat,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return svc
}

func TestNewRequiresStorageAndPresigner(t *testing.T) {
	if _, err := New(Config{Avatars: fakePresigner{}}); err == nil {
		t.Fatal("expected an error without a store")
	}
	if _, err := New(Config{Store: &fakeStore{}}); err == nil {
		t.Fatal("expected an error without a presigner")
	}
}

func TestRecordStoresArchiveRow(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, stubUserAccess{}, stubChatAccess{})

	if err := svc.Record(context.Background(), OwnerUser, "user-1", "avatars/one.jpg"); err != nil {
		t.Fatalf("Record() failed: %v", err)
	}
	if len(store.added) != 1 {
		t.Fatalf("expected 1 stored row, got %d", len(store.added))
	}
	if store.added[0].OwnerKind != OwnerUser || store.added[0].OwnerID != "user-1" || store.added[0].ObjectKey != "avatars/one.jpg" {
		t.Fatalf("unexpected row: %+v", store.added[0])
	}
}

func TestRecordRejectsUnknownOwner(t *testing.T) {
	svc := newTestService(t, &fakeStore{}, stubUserAccess{}, stubChatAccess{})

	if err := svc.Record(context.Background(), "group", "user-1", "avatars/one.jpg"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if err := svc.Record(context.Background(), OwnerUser, "", "avatars/one.jpg"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for an empty owner, got %v", err)
	}
	if err := svc.Record(context.Background(), OwnerUser, "user-1", " "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for an empty object key, got %v", err)
	}
}

func TestListFailsClosedWithoutGate(t *testing.T) {
	svc := newTestService(t, &fakeStore{}, nil, nil)

	if _, err := svc.List(context.Background(), "viewer", OwnerUser, "owner"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden without a user gate, got %v", err)
	}
	if _, err := svc.List(context.Background(), "viewer", OwnerChat, "chat-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden without a chat gate, got %v", err)
	}
}

func TestListPropagatesGateErrors(t *testing.T) {
	userSvc := newTestService(t, &fakeStore{}, stubUserAccess{err: ErrForbidden}, stubChatAccess{})
	if _, err := userSvc.List(context.Background(), "viewer", OwnerUser, "owner"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}

	chatSvc := newTestService(t, &fakeStore{}, stubUserAccess{}, stubChatAccess{err: ErrNotFound})
	if _, err := chatSvc.List(context.Background(), "viewer", OwnerChat, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListRejectsUnknownOwnerKindAndViewer(t *testing.T) {
	svc := newTestService(t, &fakeStore{}, stubUserAccess{}, stubChatAccess{})

	if _, err := svc.List(context.Background(), "viewer", "group", "chat-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := svc.List(context.Background(), "", OwnerUser, "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an empty viewer, got %v", err)
	}
}

func TestListPresignsNewestFirstAndSkipsBrokenRows(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{records: []Record{
		{ID: "3", OwnerKind: OwnerChat, OwnerID: "chat-1", ObjectKey: "chat-avatars/three.jpg", CreatedAt: now},
		{ID: "2", OwnerKind: OwnerChat, OwnerID: "chat-1", ObjectKey: "broken", CreatedAt: now.Add(-time.Hour)},
		{ID: "1", OwnerKind: OwnerChat, OwnerID: "chat-1", ObjectKey: "chat-avatars/one.jpg", CreatedAt: now.Add(-2 * time.Hour)},
	}}
	svc := newTestService(t, store, stubUserAccess{}, stubChatAccess{})

	photos, err := svc.List(context.Background(), "viewer", OwnerChat, "chat-1")
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(photos) != 2 {
		t.Fatalf("expected 2 photos (broken row skipped), got %d", len(photos))
	}
	if photos[0].ID != "3" || photos[0].URL != "https://cdn.test/chat-avatars/three.jpg" {
		t.Fatalf("unexpected first photo: %+v", photos[0])
	}
	if !photos[0].CreatedAt.Equal(now) {
		t.Fatalf("createdAt not preserved: %v", photos[0].CreatedAt)
	}
	if store.lastKind != OwnerChat || store.lastOwner != "chat-1" {
		t.Fatalf("unexpected store query: kind=%q owner=%q", store.lastKind, store.lastOwner)
	}
	if store.lastLimit != DefaultListLimit {
		t.Fatalf("expected the default limit, got %d", store.lastLimit)
	}
}

func TestDeleteOwnUserPhoto(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, stubUserAccess{}, stubChatAccess{})

	if err := svc.Delete(context.Background(), "user-1", OwnerUser, "user-1", "photo-9"); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected 1 deleted row, got %d", len(store.deleted))
	}
	got := store.deleted[0]
	if got.OwnerKind != OwnerUser || got.OwnerID != "user-1" || got.ID != "photo-9" {
		t.Fatalf("unexpected deleted row: %+v", got)
	}
}

func TestDeleteUserPhotoForbidsStrangers(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, stubUserAccess{}, stubChatAccess{})

	if err := svc.Delete(context.Background(), "viewer-2", OwnerUser, "user-1", "photo-9"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatal("forbidden delete must not reach the store")
	}
}

func TestDeleteChatPhotoNeedsEditGate(t *testing.T) {
	store := &fakeStore{}
	allowed := newTestService(t, store, stubUserAccess{}, stubChatAccess{})
	if err := allowed.Delete(context.Background(), "admin-1", OwnerChat, "chat-1", "photo-9"); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0].OwnerKind != OwnerChat {
		t.Fatalf("unexpected deleted rows: %+v", store.deleted)
	}

	deniedStore := &fakeStore{}
	denied := newTestService(t, deniedStore, stubUserAccess{}, stubChatAccess{deleteErr: ErrForbidden})
	if err := denied.Delete(context.Background(), "member-1", OwnerChat, "chat-1", "photo-9"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if len(deniedStore.deleted) != 0 {
		t.Fatal("forbidden delete must not reach the store")
	}
}

func TestDeleteRejectsBadArguments(t *testing.T) {
	svc := newTestService(t, &fakeStore{}, stubUserAccess{}, stubChatAccess{})

	for _, args := range [][4]string{
		{"", OwnerUser, "user-1", "photo-9"},
		{"user-1", "group", "user-1", "photo-9"},
		{"user-1", OwnerUser, "", "photo-9"},
		{"user-1", OwnerUser, "user-1", ""},
	} {
		if err := svc.Delete(context.Background(), args[0], args[1], args[2], args[3]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Delete(%q) = %v, want ErrNotFound", args, err)
		}
	}
}

func TestDeletePropagatesStoreNotFound(t *testing.T) {
	store := &fakeStore{deleteErr: ErrNotFound}
	svc := newTestService(t, store, stubUserAccess{}, stubChatAccess{})

	if err := svc.Delete(context.Background(), "user-1", OwnerUser, "user-1", "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
