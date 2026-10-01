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

	"combox-backend/internal/calls"

	"github.com/gorilla/websocket"
)

type stubCallService struct {
	enabled  bool
	ice      []calls.ICEServer
	record   calls.CallRecord
	parts    []*calls.Participant
	joined   chan calls.Identity
	disabled bool
}

func (s *stubCallService) Enabled() bool { return s.enabled }

func (s *stubCallService) ICEServers(string) []calls.ICEServer { return s.ice }

func (s *stubCallService) ActiveCall(chatID string) (calls.CallRecord, []*calls.Participant, bool) {
	if s.record.ID == "" || s.record.ChatID != chatID {
		return calls.CallRecord{}, nil, false
	}
	return s.record, s.parts, true
}

func (s *stubCallService) HandleConn(_ context.Context, ident calls.Identity, conn calls.SignalConn) {
	if s.joined != nil {
		s.joined <- ident
	}
	_ = conn.Close()
}

func newCallsRouter(t *testing.T, svc CallService) stdhttp.Handler {
	t.Helper()
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Calls:         svc,
	})
}

func TestCallsICERequiresAuthorization(t *testing.T) {
	router := newCallsRouter(t, &stubCallService{
		enabled: true,
		ice:     []calls.ICEServer{{URLs: []string{"stun:stun.example:3478"}}},
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/calls/ice", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer, got %d", rr.Code)
	}

	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(time.Minute).Unix())
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/calls/ice", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		ICEServers []calls.ICEServer `json:"ice_servers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.ICEServers) != 1 || payload.ICEServers[0].URLs[0] != "stun:stun.example:3478" {
		t.Fatalf("unexpected ice servers: %+v", payload.ICEServers)
	}
}

func TestCallsActive(t *testing.T) {
	svc := &stubCallService{
		enabled: true,
		record: calls.CallRecord{
			ID:        "call-1",
			ChatID:    "chat-1",
			Kind:      calls.KindGroup,
			StartedAt: time.Now().UTC(),
		},
		parts: []*calls.Participant{{UserID: "u1"}},
	}
	router := newCallsRouter(t, svc)
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(time.Minute).Unix())

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/calls/active?chat_id=chat-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"call_id":"chat-1"`) && !strings.Contains(rr.Body.String(), `"chat_id":"chat-1"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}

	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/calls/active", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400 without chat_id, got %d", rr.Code)
	}

	// Unknown chat answers with a null call instead of an error.
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/calls/active?chat_id=nope", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK || !strings.Contains(rr.Body.String(), `"call":null`) {
		t.Fatalf("expected null call, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestCallsWSAuthenticatesQueryToken(t *testing.T) {
	svc := &stubCallService{enabled: true, joined: make(chan calls.Identity, 1)}
	router := newCallsRouter(t, svc)
	server := httptest.NewServer(router)
	defer server.Close()

	base := "ws" + strings.TrimPrefix(server.URL, "http")

	// Missing token is rejected before the upgrade.
	conn, resp, err := websocket.DefaultDialer.Dial(base+"/api/private/v1/calls/ws", nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected handshake without token to fail")
	}
	if resp == nil || resp.StatusCode != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401, got %+v", resp)
	}

	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(time.Minute).Unix())
	conn, resp, err = websocket.DefaultDialer.Dial(base+"/api/private/v1/calls/ws?access_token="+token+"&device_id=dev-1", nil)
	if err != nil {
		t.Fatalf("dial: %v (resp=%+v)", err, resp)
	}
	defer conn.Close()

	select {
	case ident := <-svc.joined:
		if ident.UserID != "u1" || ident.DeviceID != "dev-1" {
			t.Fatalf("unexpected identity: %+v", ident)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("signaling connection never reached the calls service")
	}
}

func TestCallsEndpointsMissingWhenDisabled(t *testing.T) {
	// A nil service means the routes are not registered at all.
	router := newCallsRouter(t, nil)
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(time.Minute).Unix())
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/calls/ice", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}
