package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	blocksvc "combox-backend/internal/service/blocks"

	"github.com/google/uuid"
)

type blockedStub struct {
	entries    []blocksvc.Entry
	gotOwner   string
	gotTarget  string
	listErr    error
	blockErr   error
	unblockErr error
}

func (s *blockedStub) List(_ context.Context, ownerID string) ([]blocksvc.Entry, error) {
	s.gotOwner = ownerID
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.entries == nil {
		return []blocksvc.Entry{}, nil
	}
	return s.entries, nil
}

func (s *blockedStub) Block(_ context.Context, ownerID, targetID string) (blocksvc.Entry, error) {
	s.gotOwner = ownerID
	s.gotTarget = targetID
	if s.blockErr != nil {
		return blocksvc.Entry{}, s.blockErr
	}
	return blocksvc.Entry{UserID: targetID, CreatedAt: time.Now().UTC()}, nil
}

func (s *blockedStub) Unblock(_ context.Context, ownerID, targetID string) error {
	s.gotOwner = ownerID
	s.gotTarget = targetID
	return s.unblockErr
}

func newBlockedRouter(svc BlockService) stdhttp.Handler {
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Blocked:       svc,
	})
}

func blockedRequest(t *testing.T, router stdhttp.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *stdhttp.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+makeAccessToken(t, uuid.NewString(), "test-secret", time.Now().UTC().Add(10*time.Minute).Unix()))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	payload := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &payload)
	return rr.Code, payload
}

func TestBlockedListRoute(t *testing.T) {
	target := uuid.NewString()
	svc := &blockedStub{entries: []blocksvc.Entry{{UserID: target, CreatedAt: time.Now().UTC()}}}
	status, payload := blockedRequest(t, newBlockedRouter(svc), stdhttp.MethodGet, "/api/private/v1/profile/blocked", "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	items, ok := payload["blocked"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("blocked = %v", payload["blocked"])
	}
	if payload["count"] != float64(1) {
		t.Fatalf("count = %v", payload["count"])
	}
}

func TestBlockedPostRoute(t *testing.T) {
	target := uuid.NewString()
	svc := &blockedStub{}
	status, payload := blockedRequest(t, newBlockedRouter(svc), stdhttp.MethodPost,
		"/api/private/v1/profile/blocked", `{"user_id":"`+target+`"}`)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotTarget != target {
		t.Fatalf("target = %q, want %q", svc.gotTarget, target)
	}
	entry, ok := payload["blocked"].(map[string]any)
	if !ok || entry["user_id"] != target {
		t.Fatalf("blocked = %v", payload["blocked"])
	}
}

func TestBlockedDeleteRoute(t *testing.T) {
	target := uuid.NewString()
	svc := &blockedStub{}
	status, payload := blockedRequest(t, newBlockedRouter(svc), stdhttp.MethodDelete,
		"/api/private/v1/profile/blocked/"+target, "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotTarget != target {
		t.Fatalf("target = %q, want %q", svc.gotTarget, target)
	}
	if payload["revoked"] != true {
		t.Fatalf("revoked = %v", payload["revoked"])
	}
}

func TestBlockedRoutesMapServiceErrors(t *testing.T) {
	selfErr := &blocksvc.Error{Code: blocksvc.CodeInvalidArgument, MessageKey: "error.blocked.self_block"}
	status, payload := blockedRequest(t,
		newBlockedRouter(&blockedStub{blockErr: selfErr}),
		stdhttp.MethodPost, "/api/private/v1/profile/blocked", `{"user_id":"x"}`)
	if status != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400; payload=%v", status, payload)
	}
	if payload["code"] != "error.validation" {
		t.Fatalf("code = %v", payload["code"])
	}

	missingErr := &blocksvc.Error{Code: blocksvc.CodeNotFound, MessageKey: "error.blocked.not_found"}
	status, payload = blockedRequest(t,
		newBlockedRouter(&blockedStub{unblockErr: missingErr}),
		stdhttp.MethodDelete, "/api/private/v1/profile/blocked/"+uuid.NewString(), "")
	if status != stdhttp.StatusNotFound {
		t.Fatalf("status = %d, want 404; payload=%v", status, payload)
	}
	if payload["code"] != "not_found" {
		t.Fatalf("code = %v", payload["code"])
	}
}

func TestBlockedRoutesRejectAnonymous(t *testing.T) {
	router := newBlockedRouter(&blockedStub{})
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/profile/blocked", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}
