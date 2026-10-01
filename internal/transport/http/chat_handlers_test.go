package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chatsvc "combox-backend/internal/service/chat"
)

type deleteRouteStub struct {
	stubChatService
	plain       bool
	forEveryone bool
	pinPinned   bool
	pinScope    string
	pinOrder    int64
}

func (s *deleteRouteStub) DeleteChat(context.Context, string, string) error {
	s.plain = true
	return nil
}

func (s *deleteRouteStub) DeleteChatForEveryone(context.Context, string, string) error {
	s.forEveryone = true
	return nil
}

func (s *deleteRouteStub) PinChat(_ context.Context, _ string, chatID string, pinned bool, pinScope string, pinOrder int64) (chatsvc.Chat, error) {
	s.pinPinned = pinned
	s.pinScope = pinScope
	s.pinOrder = pinOrder
	return chatsvc.Chat{ID: chatID, Pinned: pinned, PinScope: pinScope, PinOrder: pinOrder}, nil
}

func TestChatDeleteRoutesToRequestedScope(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantPlain    bool
		wantEveryone bool
	}{
		{name: "default removes only my membership", query: "", wantPlain: true},
		{name: "for_everyone removes the chat for both peers", query: "?for_everyone=1", wantEveryone: true},
		{name: "for_everyone accepts an explicit true", query: "?for_everyone=true", wantEveryone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &deleteRouteStub{}
			router := newCallHistoryRouter(t, stub, nil)
			token := callHistoryToken(t, "u1")

			req := httptest.NewRequest(stdhttp.MethodDelete, "/api/private/v1/chats/chat-1"+tt.query, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != stdhttp.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
			}
			if stub.plain != tt.wantPlain || stub.forEveryone != tt.wantEveryone {
				t.Fatalf("got plain=%v forEveryone=%v, want plain=%v forEveryone=%v", stub.plain, stub.forEveryone, tt.wantPlain, tt.wantEveryone)
			}
		})
	}
}

func TestChatPinAcceptsScopeAndOrder(t *testing.T) {
	stub := &deleteRouteStub{}
	router := newCallHistoryRouter(t, stub, nil)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/chats/chat-1/pin", strings.NewReader(`{"scope":"group","order":5}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !stub.pinPinned || stub.pinScope != "group" || stub.pinOrder != 5 {
		t.Fatalf("expected pinned group/5, got pinned=%v scope=%q order=%d", stub.pinPinned, stub.pinScope, stub.pinOrder)
	}

	var payload struct {
		Chat chatsvc.Chat `json:"chat"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Chat.PinScope != "group" || payload.Chat.PinOrder != 5 {
		t.Fatalf("expected response to echo pin scope, got %+v", payload.Chat)
	}
}

func TestChatPinAcceptsEmptyBody(t *testing.T) {
	stub := &deleteRouteStub{}
	router := newCallHistoryRouter(t, stub, nil)
	token := callHistoryToken(t, "u1")

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/chats/chat-1/pin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected legacy no-body pin to stay compatible, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !stub.pinPinned || stub.pinScope != chatsvc.DefaultPinScope || stub.pinOrder != 0 {
		t.Fatalf("expected default %q/0, got pinned=%v scope=%q order=%d", chatsvc.DefaultPinScope, stub.pinPinned, stub.pinScope, stub.pinOrder)
	}
}
