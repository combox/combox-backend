package media

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakePinRepo is an in-memory Repository for the pin flow; access models chat
// membership (CanUserAccessAttachment), so flipping it simulates the source
// chat being deleted or the saver leaving it.
type fakePinRepo struct {
	Repository
	atts   map[string]Attachment
	access map[string]bool
}

func newFakePinRepo() *fakePinRepo {
	return &fakePinRepo{atts: map[string]Attachment{}, access: map[string]bool{}}
}

func (f *fakePinRepo) GetAttachment(_ context.Context, id string) (Attachment, error) {
	att, ok := f.atts[strings.TrimSpace(id)]
	if !ok {
		return Attachment{}, ErrAttachmentNotFound
	}
	return att, nil
}

func (f *fakePinRepo) CreateAttachment(_ context.Context, a Attachment) (Attachment, error) {
	f.atts[a.ID] = a
	return a, nil
}

func (f *fakePinRepo) CanUserAccessAttachment(_ context.Context, userID, attachmentID string) (bool, error) {
	return f.access[userID+"\x00"+attachmentID], nil
}

func (f *fakePinRepo) SetAttachmentUserMeta(_ context.Context, userID, attachmentID string, meta map[string]any) error {
	att, ok := f.atts[attachmentID]
	if !ok {
		return ErrAttachmentNotFound
	}
	if att.UserID != userID {
		return ErrAttachmentNotFound
	}
	att.UserMeta = meta
	f.atts[attachmentID] = att
	return nil
}

type fakePinStore struct {
	ObjectStore
	objects map[string]string
	copies  int
}

func newFakePinStore() *fakePinStore {
	return &fakePinStore{objects: map[string]string{}}
}

func (f *fakePinStore) Bucket() string { return "media" }

func (f *fakePinStore) PresignGetObject(_ context.Context, objectKey string, _ time.Duration) (string, error) {
	return "https://cdn.example/" + objectKey, nil
}

func (f *fakePinStore) CopyObject(_ context.Context, srcKey, dstKey string) error {
	body, ok := f.objects[srcKey]
	if !ok {
		return errors.New("NoSuchKey: the specified key does not exist")
	}
	f.objects[dstKey] = body
	f.copies++
	return nil
}

func newPinService() (*Service, *fakePinRepo, *fakePinStore) {
	repo := newFakePinRepo()
	store := newFakePinStore()
	duration := 210000
	size := int64(4200000)
	repo.atts["src-1"] = Attachment{
		ID: "src-1", UserID: "u1",
		Filename: "song.mp3", MimeType: "audio/mpeg", Kind: "audio", Variant: "original",
		SizeBytes: &size, DurationMS: &duration,
		Bucket: "media", ObjectKey: "u/u1/src-1/song.mp3",
		UploadType: "multipart", ProcessingStatus: "ready",
		UserMeta:  map[string]any{"voice": true, "duration_ms": float64(210000)},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	store.objects["u/u1/src-1/song.mp3"] = "audio-bytes"
	return &Service{repo: repo, store: store}, repo, store
}

func pinErrCode(err error) string {
	var svcErr *Error
	if !errors.As(err, &svcErr) {
		return ""
	}
	return svcErr.Code
}

func TestPinAttachmentCopiesToOwnedKey(t *testing.T) {
	svc, repo, store := newPinService()
	repo.access["u2\x00src-1"] = true

	out, err := svc.PinAttachment(context.Background(), "u2", "src-1")
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	got := out.Attachment
	if got.ID == "" || got.ID == "src-1" {
		t.Fatalf("pin must mint a fresh attachment id, got %q", got.ID)
	}
	if got.UserID != "u2" {
		t.Fatalf("pinned copy must be owned by the saver, got %q", got.UserID)
	}
	if !strings.HasPrefix(got.ObjectKey, "u/u2/"+got.ID+"/") {
		t.Fatalf("pinned key must live under the saver prefix, got %q", got.ObjectKey)
	}
	if out.URL == "" {
		t.Fatal("pin must return a fresh playback url")
	}
	if store.objects[got.ObjectKey] != "audio-bytes" {
		t.Fatal("object bytes were not server-side copied")
	}
	// The source row is untouched: deleting it later cannot affect the pin.
	src, err := repo.GetAttachment(context.Background(), "src-1")
	if err != nil || src.UserID != "u1" {
		t.Fatalf("source row must survive pinning: %+v %v", src, err)
	}
	if got.Filename != "song.mp3" || got.MimeType != "audio/mpeg" || got.Kind != "audio" {
		t.Fatalf("metadata not carried over: %+v", got)
	}
	if got.SizeBytes == nil || *got.SizeBytes != 4200000 {
		t.Fatalf("size not carried over: %+v", got.SizeBytes)
	}
	if got.DurationMS == nil || *got.DurationMS != 210000 {
		t.Fatalf("duration not carried over: %+v", got.DurationMS)
	}
	if got.UserMeta["voice"] != true {
		t.Fatalf("user meta (voice flag) not carried over: %+v", got.UserMeta)
	}
}

func TestPinAttachmentForbiddenWithoutAccess(t *testing.T) {
	svc, _, store := newPinService()

	_, err := svc.PinAttachment(context.Background(), "u2", "src-1")
	if pinErrCode(err) != CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if store.copies != 0 {
		t.Fatal("forbidden pin must not copy any object")
	}
}

func TestPinAttachmentMissingSource(t *testing.T) {
	svc, _, _ := newPinService()

	if _, err := svc.PinAttachment(context.Background(), "u2", "nope"); pinErrCode(err) != CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
	if _, err := svc.PinAttachment(context.Background(), "", "src-1"); pinErrCode(err) != CodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}
}

func TestPinAttachmentMissingObjectMapsToNotFound(t *testing.T) {
	svc, repo, _ := newPinService()
	repo.access["u2\x00src-1"] = true
	// Row exists but the object is already gone (e.g. uploader wiped storage):
	// the pin must report not_found, not a phantom row.
	src := repo.atts["src-1"]
	src.ObjectKey = "u/u1/src-1/gone.mp3"
	repo.atts["src-1"] = src

	if _, err := svc.PinAttachment(context.Background(), "u2", "src-1"); pinErrCode(err) != CodeNotFound {
		t.Fatalf("expected not_found for a missing object, got %v", err)
	}
}

// TestPinSurvivesSourceAccessLoss is the R16 delete chain: after pinning,
// the saver loses every path to the source (chat hard-deleted, membership
// dropped, source message soft-deleted — all of them surface as
// CanUserAccessAttachment=false while rows/objects stay put). The pinned
// copy must keep playing through ownership; the source id honestly 403s.
func TestPinSurvivesSourceAccessLoss(t *testing.T) {
	svc, repo, _ := newPinService()
	repo.access["u2\x00src-1"] = true

	out, err := svc.PinAttachment(context.Background(), "u2", "src-1")
	if err != nil {
		t.Fatalf("pin: %v", err)
	}

	// Simulate "исходное сообщение удалено": soft delete keeps rows/objects
	// (no GC exists) but chat-level access is gone.
	repo.access["u2\x00src-1"] = false

	again, err := svc.GetAttachment(context.Background(), "u2", out.Attachment.ID)
	if err != nil {
		t.Fatalf("pinned copy must resolve after source access loss: %v", err)
	}
	if again.URL == "" {
		t.Fatal("pinned copy must carry a playback url")
	}
	if _, err := svc.GetAttachment(context.Background(), "u2", "src-1"); pinErrCode(err) != CodeForbidden {
		t.Fatalf("source id must honestly deny access now, got %v", err)
	}
}

func TestPinOwnAttachmentReturnsSameID(t *testing.T) {
	svc, _, store := newPinService()

	out, err := svc.PinAttachment(context.Background(), "u1", "src-1")
	if err != nil {
		t.Fatalf("pin own: %v", err)
	}
	if out.Attachment.ID != "src-1" {
		t.Fatalf("own pin must not duplicate, got %q", out.Attachment.ID)
	}
	if store.copies != 0 {
		t.Fatal("own pin must not copy any object")
	}
}
