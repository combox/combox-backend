package http

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crypto/hmac"
	"crypto/sha256"
)

func makeMigrToken(t *testing.T, sub, secret string, exp int64) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(map[string]any{
		"sub":  sub,
		"exp":  exp,
		"migr": true,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)

	unsigned := header + "." + payload
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(unsigned))
	sig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return unsigned + "." + sig
}

func newMigrTestRouter() stdhttp.Handler {
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Auth:          stubAuthService{},
		Chat:          stubChatService{},
	})
}

func doWithToken(router stdhttp.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestMigrTokenBlockedOutsideAllowlist(t *testing.T) {
	router := newMigrTestRouter()
	migr := makeMigrToken(t, "u-legacy", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	for _, target := range []struct{ method, path string }{
		{stdhttp.MethodGet, "/api/private/v1/chats"},
		{stdhttp.MethodPost, "/api/private/v1/chats"},
		{stdhttp.MethodGet, "/api/private/v1/search?q=x"},
		{stdhttp.MethodPatch, "/api/private/v1/profile"},
		{stdhttp.MethodPost, "/api/private/v1/profile/password"},
		{stdhttp.MethodPost, "/api/private/v1/auth/sessions/revoke-others"},
	} {
		rr := doWithToken(router, target.method, target.path, migr, "")
		if rr.Code != stdhttp.StatusForbidden {
			t.Fatalf("%s %s: expected 403, got %d; body=%s", target.method, target.path, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"code":"EMAIL_BINDING_REQUIRED"`) {
			t.Fatalf("%s %s: expected EMAIL_BINDING_REQUIRED code, got %s", target.method, target.path, rr.Body.String())
		}
	}
}

func TestMigrTokenAllowlistPasses(t *testing.T) {
	router := newMigrTestRouter()
	migr := makeMigrToken(t, "u-legacy", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	// Read-only self/session reads pass the gate (stubs answer 200).
	for _, path := range []string{"/api/private/v1/profile", "/api/private/v1/auth/sessions"} {
		rr := doWithToken(router, stdhttp.MethodGet, path, migr, "")
		if rr.Code != stdhttp.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d; body=%s", path, rr.Code, rr.Body.String())
		}
	}

	// Bind endpoints are registered and pass the gate (they fail later on
	// the missing OTP store / payload, never with 403).
	rr := doWithToken(router, stdhttp.MethodPost, "/api/private/v1/auth/legacy/bind-email/request", migr, `{"email":"new@example.com"}`)
	if rr.Code == stdhttp.StatusForbidden {
		t.Fatalf("bind request hit the migr gate: %s", rr.Body.String())
	}
	rr = doWithToken(router, stdhttp.MethodPost, "/api/private/v1/auth/legacy/bind-email/verify", migr, `{"email":"new@example.com","code":"123456"}`)
	if rr.Code == stdhttp.StatusForbidden {
		t.Fatalf("bind verify hit the migr gate: %s", rr.Body.String())
	}
}

func TestFullTokenUnaffectedByMigrGate(t *testing.T) {
	router := newMigrTestRouter()
	full := makeAccessToken(t, "u-regular", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())

	rr := doWithToken(router, stdhttp.MethodGet, "/api/private/v1/chats", full, "")
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}
