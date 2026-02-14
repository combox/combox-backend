//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"combox-backend/internal/app"
	"combox-backend/internal/i18n"
	pgrepo "combox-backend/internal/repository/postgres"
	vkrepo "combox-backend/internal/repository/valkey"
	authsvc "combox-backend/internal/service/auth"
	httptransport "combox-backend/internal/transport/http"
)

func TestAuthE2E_RegisterLoginRefreshLogout(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	pg, err := pgrepo.New(ctx, env.PostgresDSN)
	if err != nil {
		t.Fatalf("init postgres: %v", err)
	}
	t.Cleanup(pg.Close)

	vk := vkrepo.New(vkrepo.Config{Addr: env.ValkeyAddr})
	t.Cleanup(func() { _ = vk.Close() })

	if err := app.RunMigrations(ctx, logger, pg.Pool(), env.migrationsPath()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	catalog, err := i18n.LoadDir(env.stringsPath(), "en")
	if err != nil {
		t.Fatalf("load strings: %v", err)
	}

	authService, err := authsvc.New(authsvc.Config{
		Users:         pgrepo.NewAuthUserRepository(pg),
		Sessions:      pgrepo.NewAuthSessionRepository(pg),
		AccessSecret:  "test_access_secret_min_32_chars_xxxxxxxx",
		RefreshSecret: "test_refresh_secret_min_32_chars_yyyyyyyy",
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("init auth service: %v", err)
	}

	router := httptransport.NewRouter(httptransport.RouterDeps{
		Logger:        logger,
		Postgres:      pg,
		Valkey:        vk,
		ReadyTimeout:  2 * time.Second,
		I18n:          catalog,
		DefaultLocale: "en",
		Auth:          authService,
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	type registerResp struct {
		Message string `json:"message"`
		User    struct {
			ID       string `json:"id"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"user"`
		Tokens authsvc.Tokens `json:"tokens"`
	}

	gotRegister := doJSON[registerResp](t, srv.URL+"/api/private/v1/auth/register", http.MethodPost, map[string]string{
		"email":    "e2e@example.com",
		"username": "e2e-user",
		"password": "password12345",
	})
	if gotRegister.User.ID == "" || gotRegister.User.Email == "" || gotRegister.User.Username == "" {
		t.Fatalf("register: expected user fields, got %+v", gotRegister.User)
	}
	if gotRegister.Tokens.AccessToken == "" || gotRegister.Tokens.RefreshToken == "" || gotRegister.Tokens.ExpiresInSec <= 0 {
		t.Fatalf("register: expected tokens, got %+v", gotRegister.Tokens)
	}

	type loginResp registerResp
	gotLogin := doJSON[loginResp](t, srv.URL+"/api/private/v1/auth/login", http.MethodPost, map[string]string{
		"login":    "e2e@example.com",
		"password": "password12345",
	})
	if gotLogin.User.ID == "" || gotLogin.Tokens.AccessToken == "" || gotLogin.Tokens.RefreshToken == "" {
		t.Fatalf("login: expected user+tokens, got %+v", gotLogin)
	}

	type refreshResp struct {
		Message string         `json:"message"`
		Tokens  authsvc.Tokens `json:"tokens"`
	}
	gotRefresh := doJSON[refreshResp](t, srv.URL+"/api/private/v1/auth/refresh", http.MethodPost, map[string]string{
		"refresh_token": gotLogin.Tokens.RefreshToken,
	})
	if gotRefresh.Tokens.AccessToken == "" || gotRefresh.Tokens.RefreshToken == "" || gotRefresh.Tokens.ExpiresInSec <= 0 {
		t.Fatalf("refresh: expected tokens, got %+v", gotRefresh.Tokens)
	}

	type logoutResp struct {
		Message string `json:"message"`
	}
	_ = doJSON[logoutResp](t, srv.URL+"/api/private/v1/auth/logout", http.MethodPost, map[string]string{
		"refresh_token": gotRefresh.Tokens.RefreshToken,
	})

	// Refresh after logout should be rejected with the standard error envelope.
	status, errBody := doJSONExpectError(t, srv.URL+"/api/private/v1/auth/refresh", http.MethodPost, map[string]string{
		"refresh_token": gotRefresh.Tokens.RefreshToken,
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout refresh, got %d: %s", status, string(errBody))
	}
	var apiErr map[string]any
	_ = json.Unmarshal(errBody, &apiErr)
	if apiErr["code"] != "unauthorized" {
		t.Fatalf("expected code=unauthorized, got %v (%s)", apiErr["code"], string(errBody))
	}
	if s, _ := apiErr["request_id"].(string); s == "" {
		t.Fatalf("expected request_id in error, got %s", string(errBody))
	}
}

func doJSON[T any](t *testing.T, url, method string, payload any) T {
	t.Helper()

	status, body := doRaw(t, url, method, payload)
	if status < 200 || status >= 300 {
		t.Fatalf("expected 2xx, got %d: %s", status, string(body))
	}
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, string(body))
	}
	return out
}

func doJSONExpectError(t *testing.T, url, method string, payload any) (int, []byte) {
	t.Helper()

	status, body := doRaw(t, url, method, payload)
	return status, body
}

func doRaw(t *testing.T, url, method string, payload any) (int, []byte) {
	t.Helper()

	var buf bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			t.Fatalf("encode payload: %v", err)
		}
	}

	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}
