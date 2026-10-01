package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "combox-backend/internal/service/auth"
)

// R11 regression: the playlist client sends `attachmentId` on every save
// (including removals of other tracks). decodeJSON uses
// DisallowUnknownFields, so an unknown `attachmentId` turned the whole
// PATCH /profile into 400 and track removal failed with
// "could not update the playlist".
type recordingProfileAuthStub struct {
	stubAuthService
	lastUpdate authsvc.UpdateProfileInput
	updateUser authsvc.User
}

func (s *recordingProfileAuthStub) UpdateProfile(_ context.Context, input authsvc.UpdateProfileInput) (authsvc.User, error) {
	s.lastUpdate = input
	if s.updateUser.ID == "" {
		s.updateUser = authsvc.User{ID: "u1", Email: "user@example.com", Username: "user", FirstName: "User"}
	}
	user := s.updateUser
	if input.SavedTracks.Set {
		user.SavedTracks = input.SavedTracks.Value
	}
	return user, nil
}

func patchProfileSavedTracks(t *testing.T, stub *recordingProfileAuthStub, body string) (int, map[string]any) {
	t.Helper()
	handler := newProfileHandler(stub, testTranslator(), "en")
	req := httptest.NewRequest(stdhttp.MethodPatch, "/api/private/v1/profile", strings.NewReader(body))
	req.Header.Set("X-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return rec.Code, payload
}

func TestPatchProfileAcceptsSavedTracksWithAttachmentID(t *testing.T) {
	stub := &recordingProfileAuthStub{}
	body := `{"saved_tracks":[{"id":"t1","title":"Song","artist":"A","duration":180,"fileSize":"3.1 MB","fileUrl":"https://cdn.example/u/u1/11111111-1111-1111-1111-111111111111/song.mp3","addedAt":"2026-09-30T00:00:00Z","attachmentId":"11111111-1111-1111-1111-111111111111"},{"id":"t2","title":"Old","artist":"","duration":0,"fileSize":"","fileUrl":"local://dead","addedAt":"2026-09-30T00:00:00Z"}]}`
	code, _ := patchProfileSavedTracks(t, stub, body)
	if code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200 (attachmentId must be a known field)", code)
	}
	if !stub.lastUpdate.SavedTracks.Set {
		t.Fatalf("SavedTracks was not marked as set")
	}
	if len(stub.lastUpdate.SavedTracks.Value) != 2 {
		t.Fatalf("tracks = %d, want 2", len(stub.lastUpdate.SavedTracks.Value))
	}
	if got := stub.lastUpdate.SavedTracks.Value[0].AttachmentID; got != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("attachmentId = %q, want the pinned id preserved", got)
	}
}

// Removing the last track sends an empty list; it must persist as [] (not 400).
func TestPatchProfileAcceptsEmptySavedTracks(t *testing.T) {
	stub := &recordingProfileAuthStub{}
	code, _ := patchProfileSavedTracks(t, stub, `{"saved_tracks":[]}`)
	if code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200 for empty saved_tracks", code)
	}
	if !stub.lastUpdate.SavedTracks.Set {
		t.Fatalf("SavedTracks was not marked as set")
	}
	if len(stub.lastUpdate.SavedTracks.Value) != 0 {
		t.Fatalf("tracks = %d, want 0", len(stub.lastUpdate.SavedTracks.Value))
	}
}
