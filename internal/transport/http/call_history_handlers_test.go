package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"combox-backend/internal/calls"
	chatsvc "combox-backend/internal/service/chat"
)

type stubCallHistoryService struct {
	stubCallService
	items  []calls.ChatCall
	err    error
	userID string
	chatID string
	limit  int

	deleteErr  error
	deletedBy  string
	deletedFor string
	deletedID  string
}

func (s *stubCallHistoryService) ListChatCalls(_ context.Context, userID, chatID string, limit int) ([]calls.ChatCall, error) {
	s.userID = userID
	s.chatID = chatID
	s.limit = limit
	return s.items, s.err
}

func (s *stubCallHistoryService) DeleteChatCall(_ context.Context, userID, chatID, callID string) error {
	s.deletedBy = userID
	s.deletedFor = chatID
	s.deletedID = callID
	return s.deleteErr
}

type forbiddenChatStub struct {
	stubChatService
}

func (forbiddenChatStub) GetChat(context.Context, string, string) (chatsvc.Chat, error) {
	return chatsvc.Chat{}, &chatsvc.Error{Code: chatsvc.CodeForbidden, MessageKey: "error.chat.forbidden"}
}

func newCallHistoryRouter(t *testing.T, chat ChatService, svc CallService) stdhttp.Handler {
	t.Helper()
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Chat:          chat,
		Calls:         svc,
	})
}

func callHistoryToken(t *testing.T, userID string) string {
	t.Helper()
	return makeAccessToken(t, userID, "test-secret", time.Now().UTC().Add(time.Minute).Unix())
}

func TestChatCallsRequiresAuthorization(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{}, &stubCallHistoryService{})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/calls", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer, got %d", rr.Code)
	}
}

func TestChatCallsListsHistoryForViewer(t *testing.T) {
	started := time.Date(2026, 9, 12, 12, 3, 0, 0, time.UTC)
	ended := started.Add(75 * time.Second)
	svc := &stubCallHistoryService{items: []calls.ChatCall{
		{
			ID:              "call-1",
			ChatID:          "chat-1",
			Kind:            calls.KindP2P,
			StartedBy:       "u1",
			StartedAt:       started,
			EndedAt:         &ended,
			DurationSeconds: 75,
			Direction:       calls.DirectionOutgoing,
			Participants:    []string{"u1", "u2"},
		},
		{
			ID:              "call-2",
			ChatID:          "chat-1",
			Kind:            calls.KindP2P,
			StartedBy:       "u2",
			StartedAt:       started.Add(-time.Hour),
			Direction:       calls.DirectionIncoming,
			Missed:          true,
			Participants:    []string{"u1"},
			DurationSeconds: 0,
		},
	}}
	router := newCallHistoryRouter(t, stubChatService{}, svc)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/calls?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.userID != "u1" || svc.chatID != "chat-1" || svc.limit != 10 {
		t.Fatalf("unexpected call args: user=%q chat=%q limit=%d", svc.userID, svc.chatID, svc.limit)
	}

	var payload struct {
		Items []struct {
			ID              string   `json:"id"`
			Direction       string   `json:"direction"`
			Missed          bool     `json:"missed"`
			DurationSeconds int64    `json:"duration_seconds"`
			Participants    []string `json:"participants"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(payload.Items))
	}
	if payload.Items[0].Direction != "outgoing" || payload.Items[0].Missed {
		t.Fatalf("unexpected first item: %+v", payload.Items[0])
	}
	if payload.Items[0].DurationSeconds != 75 || len(payload.Items[0].Participants) != 2 {
		t.Fatalf("unexpected first item payload: %+v", payload.Items[0])
	}
	if payload.Items[1].Direction != "incoming" || !payload.Items[1].Missed || payload.Items[1].DurationSeconds != 0 {
		t.Fatalf("unexpected second item: %+v", payload.Items[1])
	}
}

func TestChatCallsClampsAndRejectsLimit(t *testing.T) {
	svc := &stubCallHistoryService{}
	router := newCallHistoryRouter(t, stubChatService{}, svc)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/calls?limit=1000", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK || svc.limit != 200 {
		t.Fatalf("expected 200 with clamped limit, got %d limit=%d", rr.Code, svc.limit)
	}

	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/calls?limit=abc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400 for invalid limit, got %d", rr.Code)
	}

	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/calls", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK || svc.limit != 50 {
		t.Fatalf("expected default limit 50, got status %d limit=%d", rr.Code, svc.limit)
	}
}

func TestChatCallsForbiddenWhenChatHidden(t *testing.T) {
	svc := &stubCallHistoryService{}
	router := newCallHistoryRouter(t, forbiddenChatStub{}, svc)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/calls", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.userID != "" {
		t.Fatalf("history must not be queried for a forbidden chat")
	}
}

func TestChatCallsRejectsNonGetMethods(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{}, &stubCallHistoryService{})
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/chats/chat-1/calls", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestChatCallItemDeleteRemovesRow(t *testing.T) {
	svc := &stubCallHistoryService{}
	router := newCallHistoryRouter(t, stubChatService{}, svc)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodDelete, "/api/private/v1/chats/chat-1/calls/call-9", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.deletedBy != "u1" || svc.deletedFor != "chat-1" || svc.deletedID != "call-9" {
		t.Fatalf("unexpected delete args: user=%q chat=%q call=%q", svc.deletedBy, svc.deletedFor, svc.deletedID)
	}
}

func TestChatCallItemDeleteReportsMissingRow(t *testing.T) {
	svc := &stubCallHistoryService{deleteErr: calls.ErrCallNotFound}
	router := newCallHistoryRouter(t, stubChatService{}, svc)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodDelete, "/api/private/v1/chats/chat-1/calls/call-9", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatCallItemDeleteRequiresAuthorization(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{}, &stubCallHistoryService{})

	req := httptest.NewRequest(stdhttp.MethodDelete, "/api/private/v1/chats/chat-1/calls/call-9", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer, got %d", rr.Code)
	}
}

func TestChatCallItemDeleteRejectsOtherMethods(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{}, &stubCallHistoryService{})
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/calls/call-9", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestChatCallItemDeleteForbiddenWhenChatHidden(t *testing.T) {
	svc := &stubCallHistoryService{}
	router := newCallHistoryRouter(t, forbiddenChatStub{}, svc)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodDelete, "/api/private/v1/chats/chat-1/calls/call-9", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.deletedID != "" {
		t.Fatalf("history must not be mutated for a forbidden chat")
	}
}
