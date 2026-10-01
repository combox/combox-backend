package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authsvc "combox-backend/internal/service/auth"
)

// sessionAuthService records the arguments the session handlers forward.
type sessionAuthService struct {
	stubAuthService
	sessions        []authsvc.ActiveSession
	listErr         error
	revokeErr       error
	revokedCount    int64
	gotUserID       string
	gotCurrentID    string
	gotRevokeID     string
	gotKeepID       string
	revokeCalls     int
	revokeOtherCall int
}

func (s *sessionAuthService) ListSessions(_ context.Context, userID, currentSessionID string) ([]authsvc.ActiveSession, error) {
	s.gotUserID = userID
	s.gotCurrentID = currentSessionID
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.sessions == nil {
		return []authsvc.ActiveSession{}, nil
	}
	return s.sessions, nil
}

func (s *sessionAuthService) RevokeSession(_ context.Context, userID, sessionID string) error {
	s.gotUserID = userID
	s.gotRevokeID = sessionID
	s.revokeCalls++
	return s.revokeErr
}

func (s *sessionAuthService) RevokeOtherSessions(_ context.Context, userID, keepSessionID string) (int64, error) {
	s.gotUserID = userID
	s.gotKeepID = keepSessionID
	s.revokeOtherCall++
	if s.revokeErr != nil {
		return 0, s.revokeErr
	}
	return s.revokedCount, nil
}

// makeSessionToken mirrors newAccessToken: a signed token carrying sub, exp and
// (optionally) the sid claim the auth middleware promotes to X-Session-ID.
func makeSessionToken(t *testing.T, sub, sid, secret string, exp int64) string {
	t.Helper()
	claims := map[string]any{"sub": sub, "exp": exp}
	if sid != "" {
		claims["sid"] = sid
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	unsigned := header + "." + payload
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func newSessionRouter(svc AuthService) stdhttp.Handler {
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Auth:          svc,
	})
}

func sessionRequest(t *testing.T, router stdhttp.Handler, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	var req *stdhttp.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	payload := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &payload)
	return rr.Code, payload
}

func TestListSessionsPassesCurrentSessionFromToken(t *testing.T) {
	svc := &sessionAuthService{sessions: []authsvc.ActiveSession{{
		ID:        "sess-1",
		UserAgent: "curl/8",
		IPAddress: "10.0.0.1",
		CreatedAt: time.Unix(1, 0).UTC(),
		ExpiresAt: time.Unix(2000000000, 0).UTC(),
		Current:   true,
	}}}
	router := newSessionRouter(svc)
	token := makeSessionToken(t, "u-test", "sess-1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	status, payload := sessionRequest(t, router, stdhttp.MethodGet, "/api/private/v1/auth/sessions", token, "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotUserID != "u-test" || svc.gotCurrentID != "sess-1" {
		t.Fatalf("user=%q current=%q", svc.gotUserID, svc.gotCurrentID)
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %v", payload["items"])
	}
	first := items[0].(map[string]any)
	if first["current"] != true {
		t.Fatalf("current = %v", first["current"])
	}
	for _, field := range []string{"id", "user_agent", "ip_address", "created_at", "expires_at"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("session payload is missing %q: %v", field, first)
		}
	}
}

func TestAuthMiddlewareIgnoresForgedSessionHeader(t *testing.T) {
	svc := &sessionAuthService{}
	router := newSessionRouter(svc)
	token := makeSessionToken(t, "u-test", "", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/auth/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Session-ID", "attacker-chosen")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if svc.gotCurrentID != "" {
		t.Fatalf("a client supplied X-Session-ID leaked through: %q", svc.gotCurrentID)
	}
}

func TestRevokeSessionRoute(t *testing.T) {
	svc := &sessionAuthService{}
	router := newSessionRouter(svc)
	token := makeSessionToken(t, "u-test", "sess-1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	status, payload := sessionRequest(t, router, stdhttp.MethodDelete,
		"/api/private/v1/auth/sessions/sess-2", token, "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotRevokeID != "sess-2" || svc.gotUserID != "u-test" || svc.revokeCalls != 1 {
		t.Fatalf("revoke id=%q user=%q calls=%d", svc.gotRevokeID, svc.gotUserID, svc.revokeCalls)
	}
}

func TestRevokeSessionRouteNotFound(t *testing.T) {
	svc := &sessionAuthService{revokeErr: &authsvc.Error{
		Code: authsvc.CodeNotFound, MessageKey: "error.auth.session_not_found",
	}}
	router := newSessionRouter(svc)
	token := makeSessionToken(t, "u-test", "sess-1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	status, payload := sessionRequest(t, router, stdhttp.MethodDelete,
		"/api/private/v1/auth/sessions/sess-9", token, "")
	if status != stdhttp.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%v", status, payload)
	}
	if payload["code"] != "not_found" {
		t.Fatalf("code = %v", payload["code"])
	}
}

func TestRevokeOthersPrefersBodyThenTokenThenNothing(t *testing.T) {
	now := time.Now().UTC().Add(10 * time.Minute).Unix()

	// 1. body wins over the token session id
	svc := &sessionAuthService{revokedCount: 3}
	router := newSessionRouter(svc)
	token := makeSessionToken(t, "u-test", "sess-token", "test-secret", now)
	status, payload := sessionRequest(t, router, stdhttp.MethodPost,
		"/api/private/v1/auth/sessions/revoke-others", token, `{"keep_session_id":"sess-body"}`)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotKeepID != "sess-body" {
		t.Fatalf("keep = %q, want the body value", svc.gotKeepID)
	}
	if payload["revoked"] != float64(3) {
		t.Fatalf("revoked = %v", payload["revoked"])
	}

	// 2. no body -> the sid claim of the access token
	svc = &sessionAuthService{revokedCount: 1}
	router = newSessionRouter(svc)
	status, _ = sessionRequest(t, router, stdhttp.MethodPost,
		"/api/private/v1/auth/sessions/revoke-others", token, "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if svc.gotKeepID != "sess-token" {
		t.Fatalf("keep = %q, want the token session id", svc.gotKeepID)
	}

	// 3. no body and no sid -> revoke everything
	svc = &sessionAuthService{revokedCount: 5}
	router = newSessionRouter(svc)
	tokenWithoutSid := makeSessionToken(t, "u-test", "", "test-secret", now)
	status, _ = sessionRequest(t, router, stdhttp.MethodPost,
		"/api/private/v1/auth/sessions/revoke-others", tokenWithoutSid, `{}`)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if svc.gotKeepID != "" {
		t.Fatalf("keep = %q, want an empty keep id (revoke all)", svc.gotKeepID)
	}
}

func TestSessionRoutesRejectWrongMethod(t *testing.T) {
	router := newSessionRouter(&sessionAuthService{})
	token := makeSessionToken(t, "u-test", "", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	status, _ := sessionRequest(t, router, stdhttp.MethodPost, "/api/private/v1/auth/sessions", token, `{}`)
	if status != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", status)
	}
	status, _ = sessionRequest(t, router, stdhttp.MethodGet, "/api/private/v1/auth/sessions/revoke-others", token, "")
	if status != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", status)
	}
	status, _ = sessionRequest(t, router, stdhttp.MethodPatch, "/api/private/v1/auth/sessions/sess-1", token, `{}`)
	if status != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", status)
	}
}

func TestSessionRoutesRejectAnonymous(t *testing.T) {
	router := newSessionRouter(&sessionAuthService{})
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/auth/sessions", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}
