package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authsvc "combox-backend/internal/service/auth"
	chatsvc "combox-backend/internal/service/chat"

	"github.com/redis/go-redis/v9"
)

type stubPinger struct {
	err error
}

func (s stubPinger) Ping(context.Context) error {
	return s.err
}

func (stubPinger) Client() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
}

type mapTranslator struct {
	values map[string]map[string]string
}

func (m mapTranslator) Translate(requestLocale, key string) string {
	locale := strings.ToLower(requestLocale)
	if len(locale) >= 2 {
		locale = locale[:2]
	}
	if v, ok := m.values[locale][key]; ok {
		return v
	}
	if v, ok := m.values["en"][key]; ok {
		return v
	}
	return key
}

func TestHealthzLocalized(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/healthz", nil)
	req.Header.Set("Accept-Language", "ru-RU")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"status":"ок"`) {
		t.Fatalf("expected localized response, got %s", body)
	}
}

func TestReadyzDegradedWhenDependencyDown(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{err: errors.New("pg down")},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"degraded"`) {
		t.Fatalf("expected degraded status, got %s", rr.Body.String())
	}
}

func TestMiddlewareSetsRequestIDHeader(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	requestID := rr.Header().Get("X-Request-ID")
	if strings.TrimSpace(requestID) == "" {
		t.Fatalf("expected X-Request-ID header")
	}
}

func TestPrivateRouteRequiresAuthorizationHeader(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Chat:          stubChatService{},
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrivateRouteAllowsValidBearerToken(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Chat:          stubChatService{},
	})

	token := makeAccessToken(t, "u-test", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func makeAccessToken(t *testing.T, sub, secret string, exp int64) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(map[string]any{
		"sub": sub,
		"exp": exp,
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

func testTranslator() mapTranslator {
	return mapTranslator{values: map[string]map[string]string{
		"en": {
			"status.ok":                       "ok",
			"status.running":                  "running",
			"status.up":                       "up",
			"status.down":                     "down",
			"status.degraded":                 "degraded",
			"check.postgres":                  "postgres",
			"check.valkey":                    "valkey",
			"service.name":                    "combox-backend",
			"error.auth.missing_user_context": "missing user context",
			"chat.create.success":             "chat created",
		},
		"ru": {
			"status.ok": "ок",
		},
	}}
}

type stubChatService struct{}

func (stubChatService) CreateChat(context.Context, chatsvc.CreateChatInput) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: "chat-1", Title: "General"}, nil
}

func (stubChatService) ListChats(context.Context, string) ([]chatsvc.Chat, error) {
	return []chatsvc.Chat{{ID: "chat-1", Title: "General"}}, nil
}

func (stubChatService) CreateMessage(context.Context, chatsvc.CreateMessageInput) (chatsvc.Message, error) {
	return chatsvc.Message{}, nil
}

func (stubChatService) ListMessages(context.Context, chatsvc.ListMessagesInput) (chatsvc.MessagePage, error) {
	return chatsvc.MessagePage{}, nil
}

func (stubChatService) UpsertMessageStatus(context.Context, chatsvc.UpsertMessageStatusInput) (chatsvc.MessageStatus, error) {
	return chatsvc.MessageStatus{}, nil
}

func (stubChatService) EditMessage(context.Context, chatsvc.EditMessageInput) (chatsvc.Message, error) {
	return chatsvc.Message{}, nil
}

func (stubChatService) ForwardMessage(context.Context, chatsvc.ForwardMessageInput) (chatsvc.Message, error) {
	return chatsvc.Message{}, nil
}

type stubAuthService struct {
	registerErr error
}

func (s stubAuthService) Register(context.Context, authsvc.RegisterInput) (authsvc.User, authsvc.Tokens, error) {
	if s.registerErr != nil {
		return authsvc.User{}, authsvc.Tokens{}, s.registerErr
	}
	return authsvc.User{
			ID:       "u1",
			Email:    "user@example.com",
			Username: "user",
		}, authsvc.Tokens{
			AccessToken:  "a",
			RefreshToken: "r",
			ExpiresInSec: 900,
		}, nil
}

func (s stubAuthService) Login(context.Context, authsvc.LoginInput) (authsvc.User, authsvc.Tokens, error) {
	return authsvc.User{}, authsvc.Tokens{}, nil
}

func (s stubAuthService) Refresh(context.Context, authsvc.RefreshInput) (authsvc.Tokens, error) {
	return authsvc.Tokens{}, nil
}

func (s stubAuthService) Logout(context.Context, authsvc.LogoutInput) error {
	return nil
}

func TestRegisterRouteReturnsErrorEnvelope(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Auth: stubAuthService{
			registerErr: &authsvc.Error{
				Code:       authsvc.CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			},
		},
	})

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/auth/register", strings.NewReader(`{"email":"","username":"","password":""}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"code":"invalid_argument"`) {
		t.Fatalf("expected error code in envelope, got %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"request_id":"`) {
		t.Fatalf("expected request_id in envelope, got %s", rr.Body.String())
	}
}

func TestChatsRouteRequiresUserHeader(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("expected unauthorized code, got %s", rr.Body.String())
	}
}

func TestCreateChatRoute(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
	})

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/chats", strings.NewReader(`{"title":"General","member_ids":["u2"]}`))
	req.Header.Set("X-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"chat":{"id":"chat-1"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}
