package http

import (
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatsvc "combox-backend/internal/service/chat"
)

func pinPathHelpers(t *testing.T, chatID, rest string) string {
	t.Helper()
	return "/api/private/v1/chats/" + chatID + rest
}

func TestPinnedMessageRequiresAuthorization(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{}, nil)

	req := httptest.NewRequest(stdhttp.MethodGet, pinPathHelpers(t, "chat-1", "/pinned-message"), nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer, got %d", rr.Code)
	}
}

func TestPinnedMessageReturnsNullWhenNothingPinned(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{}, nil)
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(time.Minute).Unix())

	req := httptest.NewRequest(stdhttp.MethodGet, pinPathHelpers(t, "chat-1", "/pinned-message"), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var payload struct {
		Item *chatsvc.Message `json:"item"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Item != nil {
		t.Fatalf("expected null item, got %+v", payload.Item)
	}
}

func TestPinnedMessageReturnsPinnedItem(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{
		pinnedMessage: &chatsvc.Message{ID: "msg-9", ChatID: "chat-1", Content: "pinned text"},
	}, nil)
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(time.Minute).Unix())

	req := httptest.NewRequest(stdhttp.MethodGet, pinPathHelpers(t, "chat-1", "/pinned-message"), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var payload struct {
		Item *chatsvc.Message `json:"item"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Item == nil || payload.Item.ID != "msg-9" {
		t.Fatalf("expected item msg-9, got %+v", payload.Item)
	}
}

func TestPinMessageTogglesAndRejectsBadMethod(t *testing.T) {
	router := newCallHistoryRouter(t, stubChatService{}, nil)
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(time.Minute).Unix())

	req := httptest.NewRequest(stdhttp.MethodGet, pinPathHelpers(t, "chat-1", "/messages/msg-1/pin"), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET on pin path, got %d", rr.Code)
	}

	req = httptest.NewRequest(stdhttp.MethodPost, pinPathHelpers(t, "chat-1", "/messages/msg-1/pin"), strings.NewReader(`{"pinned":true}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var payload struct {
		Item *chatsvc.Message `json:"item"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Item == nil || payload.Item.ID != "msg-1" {
		t.Fatalf("expected item msg-1, got %+v", payload.Item)
	}

	req = httptest.NewRequest(stdhttp.MethodPost, pinPathHelpers(t, "chat-1", "/messages/msg-1/pin"), strings.NewReader(`{"pinned":false}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Item != nil {
		t.Fatalf("expected null item after unpin, got %+v", payload.Item)
	}

	req = httptest.NewRequest(stdhttp.MethodPost, pinPathHelpers(t, "chat-1", "/messages/msg-1/pin"), strings.NewReader(`{`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400 for broken json, got %d", rr.Code)
	}
}
