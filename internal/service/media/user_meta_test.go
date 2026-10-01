package media

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeMetaRepo implements only the repository methods SetUserMeta touches;
// everything else panics through the embedded nil interface.
type fakeMetaRepo struct {
	Repository
	atts map[string]Attachment
}

func newFakeMetaRepo() *fakeMetaRepo {
	return &fakeMetaRepo{atts: map[string]Attachment{}}
}

func (f *fakeMetaRepo) GetAttachment(_ context.Context, id string) (Attachment, error) {
	att, ok := f.atts[id]
	if !ok {
		return Attachment{}, ErrAttachmentNotFound
	}
	return att, nil
}

func (f *fakeMetaRepo) CanUserAccessAttachment(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (f *fakeMetaRepo) SetAttachmentUserMeta(_ context.Context, userID, attachmentID string, meta map[string]any) error {
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

type fakeMetaStore struct {
	ObjectStore
}

func (fakeMetaStore) Bucket() string { return "media" }

func (fakeMetaStore) PresignGetObject(_ context.Context, objectKey string, _ time.Duration) (string, error) {
	return "https://minio.example/" + objectKey, nil
}

func newMetaService() *Service {
	repo := newFakeMetaRepo()
	repo.atts["att-1"] = Attachment{
		ID: "att-1", UserID: "u1", Kind: "audio", Variant: "voice",
		Bucket: "media", ObjectKey: "voice/att-1.ogg",
		ProcessingStatus: "ready",
		CreatedAt:        time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	return &Service{repo: repo, store: fakeMetaStore{}}
}

func TestSetUserMetaPersistsWaveform(t *testing.T) {
	svc := newMetaService()

	out, err := svc.SetUserMeta(context.Background(), SetUserMetaInput{
		UserID:       "u1",
		AttachmentID: "att-1",
		Meta: map[string]any{
			"waveform":    []any{float64(0), 12.5, 100},
			"round":       false,
			"duration_ms": float64(1500),
		},
	})
	if err != nil {
		t.Fatalf("set user meta: %v", err)
	}
	if out.Attachment.UserMeta["duration_ms"] != float64(1500) {
		t.Fatalf("meta not returned: %+v", out.Attachment.UserMeta)
	}
	if out.URL == "" {
		t.Fatal("output must still carry a playback url")
	}
}

func TestSetUserMetaRejectsUnknownKey(t *testing.T) {
	svc := newMetaService()
	_, err := svc.SetUserMeta(context.Background(), SetUserMetaInput{
		UserID: "u1", AttachmentID: "att-1",
		Meta: map[string]any{"wormhole": true},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	var svcErr *Error
	if !errors.As(err, &svcErr) || svcErr.Code != CodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}
}

func TestSetUserMetaValidatesWaveform(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
	}{
		{"empty waveform", map[string]any{"waveform": []any{}}},
		{"not a list", map[string]any{"waveform": "1,2,3"}},
		{"negative peak", map[string]any{"waveform": []any{float64(-1)}}},
		{"peak too large", map[string]any{"waveform": []any{float64(101)}}},
		{"non numeric peak", map[string]any{"waveform": []any{"loud"}}},
		{"too many peaks", func() map[string]any {
			peaks := make([]any, maxWaveformSamples+1)
			for i := range peaks {
				peaks[i] = float64(1)
			}
			return map[string]any{"waveform": peaks}
		}()},
		{"round not bool", map[string]any{"round": "yes"}},
		{"negative duration", map[string]any{"duration_ms": float64(-1)}},
		{"no keys", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMetaService()
			_, err := svc.SetUserMeta(context.Background(), SetUserMetaInput{
				UserID: "u1", AttachmentID: "att-1", Meta: tc.meta,
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			var svcErr *Error
			if !errors.As(err, &svcErr) || svcErr.Code != CodeInvalidArgument {
				t.Fatalf("expected invalid_argument, got %v", err)
			}
		})
	}
}

func TestSetUserMetaEnforcesOwnership(t *testing.T) {
	svc := newMetaService()
	_, err := svc.SetUserMeta(context.Background(), SetUserMetaInput{
		UserID: "u2", AttachmentID: "att-1",
		Meta: map[string]any{"round": true},
	})
	if err == nil {
		t.Fatal("expected an error for a foreign attachment")
	}
	var svcErr *Error
	if !errors.As(err, &svcErr) || svcErr.Code != CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}

	_, err = svc.SetUserMeta(context.Background(), SetUserMetaInput{
		UserID: "u1", AttachmentID: "missing",
		Meta: map[string]any{"round": true},
	})
	if !errors.As(err, &svcErr) || svcErr.Code != CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestSetUserMetaAcceptsMaxWaveformLength(t *testing.T) {
	svc := newMetaService()
	peaks := make([]any, maxWaveformSamples)
	for i := range peaks {
		peaks[i] = float64(i % (maxWaveformPeakUnit + 1))
	}
	if _, err := svc.SetUserMeta(context.Background(), SetUserMetaInput{
		UserID: "u1", AttachmentID: "att-1",
		Meta: map[string]any{"waveform": peaks},
	}); err != nil {
		t.Fatalf("max length waveform must be accepted: %v", err)
	}
}
