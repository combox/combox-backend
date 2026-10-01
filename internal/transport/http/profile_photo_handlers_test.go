package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	profilephotosvc "combox-backend/internal/service/profilephoto"
)

type stubProfilePhotos struct {
	photos []profilephotosvc.Photo
	err    error

	deleteErr   error
	lastDelKind string
	lastDelID   string
	delCalls    int

	lastViewer string
	lastKind   string
	lastOwner  string
	calls      int
}

func (s *stubProfilePhotos) List(_ context.Context, viewerID, ownerKind, ownerID string) ([]profilephotosvc.Photo, error) {
	s.calls++
	s.lastViewer = viewerID
	s.lastKind = ownerKind
	s.lastOwner = ownerID
	if s.err != nil {
		return nil, s.err
	}
	return s.photos, nil
}

func (s *stubProfilePhotos) Delete(_ context.Context, viewerID, ownerKind, ownerID, photoID string) error {
	s.delCalls++
	s.lastViewer = viewerID
	s.lastDelKind = ownerKind
	s.lastOwner = ownerID
	s.lastDelID = photoID
	return s.deleteErr
}

func newProfilePhotoRouter(t *testing.T, photos ProfilePhotoList) (stdhttp.Handler, string) {
	t.Helper()
	const secret = "photo-test-secret"
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  secret,
		ProfilePhotos: photos,
	})
	return router, secret
}

func doPhotoRequest(t *testing.T, router stdhttp.Handler, secret, path string, tokenless bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(stdhttp.MethodGet, path, nil)
	if !tokenless {
		token := makeAccessToken(t, "viewer-1", secret, time.Now().UTC().Add(10*time.Minute).Unix())
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestProfilePhotoPathParsing(t *testing.T) {
	if got, ok := userPhotosFromPath("/api/private/v1/users/abc/photos"); !ok || got != "abc" {
		t.Fatalf("userPhotosFromPath = %q, %v", got, ok)
	}
	if _, ok := userPhotosFromPath("/api/private/v1/users/abc"); ok {
		t.Fatal("plain profile path must not parse as photos")
	}
	if _, ok := userPhotosFromPath("/api/private/v1/users/abc/photos/more"); ok {
		t.Fatal("deeper path must not parse as photos")
	}
	if got, ok := chatPhotosFromPath("/api/private/v1/chats/xyz/photos"); !ok || got != "xyz" {
		t.Fatalf("chatPhotosFromPath = %q, %v", got, ok)
	}
	if _, ok := chatPhotosFromPath("/api/private/v1/chats/xyz/members"); ok {
		t.Fatal("members path must not parse as photos")
	}
}

func TestUserPhotosRouteReturnsGallery(t *testing.T) {
	stamp := time.Date(2026, time.September, 17, 10, 30, 0, 0, time.UTC)
	photos := &stubProfilePhotos{photos: []profilephotosvc.Photo{
		{ID: "p2", URL: "https://cdn.test/avatars/two.jpg", CreatedAt: stamp},
		{ID: "p1", URL: "https://cdn.test/avatars/one.jpg", CreatedAt: stamp.Add(-24 * time.Hour)},
	}}
	router, secret := newProfilePhotoRouter(t, photos)

	rr := doPhotoRequest(t, router, secret, "/api/private/v1/users/owner-1/photos", false)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	if photos.lastViewer != "viewer-1" || photos.lastKind != profilephotosvc.OwnerUser || photos.lastOwner != "owner-1" {
		t.Fatalf("unexpected gate call: %+v", photos)
	}

	var payload struct {
		Photos []profilePhotoItem `json:"photos"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Photos) != 2 {
		t.Fatalf("expected 2 photos, got %d", len(payload.Photos))
	}
	if payload.Photos[0].ID != "p2" || payload.Photos[0].URL != "https://cdn.test/avatars/two.jpg" {
		t.Fatalf("unexpected first item: %+v", payload.Photos[0])
	}
	if payload.Photos[0].CreatedAt != "2026-09-17T10:30:00Z" {
		t.Fatalf("unexpected created_at: %q", payload.Photos[0].CreatedAt)
	}
}

func TestChatPhotosRouteReturnsGallery(t *testing.T) {
	photos := &stubProfilePhotos{photos: []profilephotosvc.Photo{}}
	router, secret := newProfilePhotoRouter(t, photos)

	rr := doPhotoRequest(t, router, secret, "/api/private/v1/chats/chat-9/photos", false)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	if photos.lastKind != profilephotosvc.OwnerChat || photos.lastOwner != "chat-9" {
		t.Fatalf("unexpected gate call: %+v", photos)
	}
	if !strings.Contains(rr.Body.String(), `"photos":[]`) {
		t.Fatalf("expected an empty gallery, got %s", rr.Body.String())
	}
}

func TestProfilePhotoRoutesRequireAuthorization(t *testing.T) {
	router, secret := newProfilePhotoRouter(t, &stubProfilePhotos{})

	for _, path := range []string{
		"/api/private/v1/users/owner-1/photos",
		"/api/private/v1/chats/chat-9/photos",
	} {
		rr := doPhotoRequest(t, router, secret, path, true)
		if rr.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", path, rr.Code)
		}
	}
}

func TestProfilePhotoRoutesMapGateErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"forbidden", profilephotosvc.ErrForbidden, stdhttp.StatusForbidden},
		{"not found", profilephotosvc.ErrNotFound, stdhttp.StatusNotFound},
		{"internal", errors.New("db down"), stdhttp.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, secret := newProfilePhotoRouter(t, &stubProfilePhotos{err: tc.err})
			rr := doPhotoRequest(t, router, secret, "/api/private/v1/users/owner-1/photos", false)
			if rr.Code != tc.status {
				t.Fatalf("expected %d, got %d; body=%s", tc.status, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestProfilePhotoRoutesRejectOtherMethods(t *testing.T) {
	photos := &stubProfilePhotos{}
	router, secret := newProfilePhotoRouter(t, photos)

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/users/owner-1/photos", nil)
	token := makeAccessToken(t, "viewer-1", secret, time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
	if photos.calls != 0 {
		t.Fatal("history must not be queried for a rejected method")
	}
}

func doPhotoDelete(t *testing.T, router stdhttp.Handler, secret, path string, tokenless bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(stdhttp.MethodDelete, path, nil)
	if !tokenless {
		token := makeAccessToken(t, "viewer-1", secret, time.Now().UTC().Add(10*time.Minute).Unix())
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestPhotoItemPathParsing(t *testing.T) {
	if userID, photoID, ok := userPhotoItemFromPath("/api/private/v1/users/abc/photos/p1"); !ok || userID != "abc" || photoID != "p1" {
		t.Fatalf("userPhotoItemFromPath = %q, %q, %v", userID, photoID, ok)
	}
	if _, _, ok := userPhotoItemFromPath("/api/private/v1/users/abc/photos"); ok {
		t.Fatal("collection path must not parse as an item")
	}
	if _, _, ok := userPhotoItemFromPath("/api/private/v1/users/abc/photos/"); ok {
		t.Fatal("empty photo id must not parse as an item")
	}
	if _, _, ok := userPhotoItemFromPath("/api/private/v1/users/abc/photos/p1/more"); ok {
		t.Fatal("deeper path must not parse as an item")
	}
	if chatID, photoID, ok := chatPhotoItemFromPath("/api/private/v1/chats/xyz/photos/p2"); !ok || chatID != "xyz" || photoID != "p2" {
		t.Fatalf("chatPhotoItemFromPath = %q, %q, %v", chatID, photoID, ok)
	}
	if _, _, ok := chatPhotoItemFromPath("/api/private/v1/chats/xyz/photos"); ok {
		t.Fatal("collection path must not parse as an item")
	}
}

func TestUserPhotoItemDelete(t *testing.T) {
	photos := &stubProfilePhotos{}
	router, secret := newProfilePhotoRouter(t, photos)

	rr := doPhotoDelete(t, router, secret, "/api/private/v1/users/viewer-1/photos/photo-9", false)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	if photos.delCalls != 1 || photos.lastDelKind != profilephotosvc.OwnerUser || photos.lastOwner != "viewer-1" || photos.lastDelID != "photo-9" {
		t.Fatalf("unexpected delete call: %+v", photos)
	}
	if !strings.Contains(rr.Body.String(), `"id":"photo-9"`) {
		t.Fatalf("expected the deleted id object, got %s", rr.Body.String())
	}
}

func TestChatPhotoItemDelete(t *testing.T) {
	photos := &stubProfilePhotos{}
	router, secret := newProfilePhotoRouter(t, photos)

	rr := doPhotoDelete(t, router, secret, "/api/private/v1/chats/chat-9/photos/photo-3", false)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	if photos.delCalls != 1 || photos.lastDelKind != profilephotosvc.OwnerChat || photos.lastOwner != "chat-9" || photos.lastDelID != "photo-3" {
		t.Fatalf("unexpected delete call: %+v", photos)
	}
}

func TestPhotoItemDeleteMapsErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"forbidden", profilephotosvc.ErrForbidden, stdhttp.StatusForbidden},
		{"not found", profilephotosvc.ErrNotFound, stdhttp.StatusNotFound},
		{"internal", errors.New("db down"), stdhttp.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, secret := newProfilePhotoRouter(t, &stubProfilePhotos{deleteErr: tc.err})
			rr := doPhotoDelete(t, router, secret, "/api/private/v1/users/viewer-1/photos/photo-9", false)
			if rr.Code != tc.status {
				t.Fatalf("expected %d, got %d; body=%s", tc.status, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPhotoItemRoutesRequireAuthorization(t *testing.T) {
	router, secret := newProfilePhotoRouter(t, &stubProfilePhotos{})

	for _, path := range []string{
		"/api/private/v1/users/viewer-1/photos/photo-9",
		"/api/private/v1/chats/chat-9/photos/photo-3",
	} {
		rr := doPhotoDelete(t, router, secret, path, true)
		if rr.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", path, rr.Code)
		}
	}
}

func TestPhotoItemRoutesRejectOtherMethods(t *testing.T) {
	photos := &stubProfilePhotos{}
	router, secret := newProfilePhotoRouter(t, photos)

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/users/viewer-1/photos/photo-9", nil)
	token := makeAccessToken(t, "viewer-1", secret, time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
	if photos.delCalls != 0 {
		t.Fatal("history must not be deleted for a rejected method")
	}
}
