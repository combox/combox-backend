package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	authsvc "combox-backend/internal/service/auth"
)

type playlistPrivacyAuthStub struct {
	stubAuthService
	user authsvc.User
}

func (s playlistPrivacyAuthStub) GetProfile(context.Context, string) (authsvc.User, error) {
	return s.user, nil
}

func fetchUserForPlaylistPrivacy(t *testing.T, targetID, requesterID string, public bool) []authsvc.SavedTrack {
	t.Helper()

	handler := newUserByIDHandler(playlistPrivacyAuthStub{
		user: authsvc.User{
			ID:               targetID,
			Username:         "target",
			PlaylistTitle:    "Target mix",
			PlaylistIsPublic: public,
			SavedTracks:      []authsvc.SavedTrack{{ID: "t1", Title: "Song", FileURL: "local://track/song"}},
		},
	}, nil, testTranslator(), "en")

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/users/"+targetID, nil)
	req.Header.Set("X-User-ID", requesterID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		User struct {
			PlaylistIsPublic bool                 `json:"playlist_is_public"`
			PlaylistTitle    string               `json:"playlist_title"`
			SavedTracks      []authsvc.SavedTrack `json:"saved_tracks"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.User.PlaylistTitle != "Target mix" {
		t.Fatalf("playlist_title = %q", payload.User.PlaylistTitle)
	}
	if payload.User.PlaylistIsPublic != public {
		t.Fatalf("playlist_is_public = %v, want %v", payload.User.PlaylistIsPublic, public)
	}
	return payload.User.SavedTracks
}

func TestUserByIDHandlerHidesPrivatePlaylistFromOthers(t *testing.T) {
	tracks := fetchUserForPlaylistPrivacy(t, "target-user", "someone-else", false)
	if len(tracks) != 0 {
		t.Fatalf("private playlist leaked %d tracks to another user", len(tracks))
	}
}

func TestUserByIDHandlerKeepsPrivatePlaylistForOwner(t *testing.T) {
	tracks := fetchUserForPlaylistPrivacy(t, "target-user", "target-user", false)
	if len(tracks) != 1 {
		t.Fatalf("owner sees %d tracks, want 1", len(tracks))
	}
}

func TestUserByIDHandlerKeepsPublicPlaylistForOthers(t *testing.T) {
	tracks := fetchUserForPlaylistPrivacy(t, "target-user", "someone-else", true)
	if len(tracks) != 1 {
		t.Fatalf("public playlist shows %d tracks, want 1", len(tracks))
	}
}
