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
	botauthsvc "combox-backend/internal/service/botauth"
	botwebhooksvc "combox-backend/internal/service/botwebhook"
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

func TestPrivateBotTokenRouteRequiresAuth(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		BotTokens:     stubBotTokenService{},
	})

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/bot/tokens", strings.NewReader(`{"scopes":["bot:messages:read"],"chat_ids":["*"]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPrivateBotTokenRouteCreate(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		BotTokens:     stubBotTokenService{},
	})

	token := makeAccessToken(t, "u-test", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())
	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/bot/tokens", strings.NewReader(`{"name":"My bot","scopes":["bot:messages:read"],"chat_ids":["*"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicBotRouteRequiresBearerToken(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
		BotAuth:       stubBotAuthService{},
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/public/v1/bot/chats/chat-1/messages", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicBotRouteRejectsMissingScope(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
		BotAuth: stubBotAuthService{principal: botauthsvc.Principal{
			UserID:          "u-bot",
			Scopes:          map[string]struct{}{"bot:messages:write": {}},
			AllowedChatIDs:  map[string]struct{}{"chat-1": {}},
			AllowAllChatIDs: false,
		}},
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/public/v1/bot/chats/chat-1/messages", nil)
	req.Header.Set("Authorization", "Bearer tok-ok")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusForbidden {
		t.Fatalf("expected 403, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicBotRouteRejectsChatOutOfScope(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
		BotAuth: stubBotAuthService{principal: botauthsvc.Principal{
			UserID:          "u-bot",
			Scopes:          map[string]struct{}{"bot:messages:read": {}},
			AllowedChatIDs:  map[string]struct{}{"chat-1": {}},
			AllowAllChatIDs: false,
		}},
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/public/v1/bot/chats/chat-2/messages", nil)
	req.Header.Set("Authorization", "Bearer tok-ok")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusForbidden {
		t.Fatalf("expected 403, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicBotRouteAllowsScopedAccess(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
		BotAuth: stubBotAuthService{principal: botauthsvc.Principal{
			UserID:          "u-bot",
			Scopes:          map[string]struct{}{"bot:messages:read": {}},
			AllowedChatIDs:  map[string]struct{}{"chat-1": {}},
			AllowAllChatIDs: false,
		}},
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/public/v1/bot/chats/chat-1/messages", nil)
	req.Header.Set("Authorization", "Bearer tok-ok")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicBotWebhookRouteRejectsMissingScope(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
		BotAuth: stubBotAuthService{principal: botauthsvc.Principal{
			UserID:          "u-bot",
			Scopes:          map[string]struct{}{"bot:messages:read": {}},
			AllowedChatIDs:  map[string]struct{}{"chat-1": {}},
			AllowAllChatIDs: false,
		}},
		BotWebhooks: stubBotWebhookService{},
	})

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/public/v1/bot/webhooks", strings.NewReader(`{"endpoint_url":"https://example.com/hook","events":["message.created"]}`))
	req.Header.Set("Authorization", "Bearer tok-ok")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusForbidden {
		t.Fatalf("expected 403, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicBotWebhookRouteCreatesWebhook(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
		BotAuth: stubBotAuthService{principal: botauthsvc.Principal{
			UserID:          "u-bot",
			Scopes:          map[string]struct{}{"bot:webhooks:write": {}},
			AllowedChatIDs:  map[string]struct{}{"*": {}},
			AllowAllChatIDs: true,
		}},
		BotWebhooks: stubBotWebhookService{},
	})

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/public/v1/bot/webhooks", strings.NewReader(`{"endpoint_url":"https://example.com/hook","events":["message.created","message.read"]}`))
	req.Header.Set("Authorization", "Bearer tok-ok")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicBotWebhookRouteReturnsConflictOnDuplicate(t *testing.T) {
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		Chat:          stubChatService{},
		BotAuth: stubBotAuthService{principal: botauthsvc.Principal{
			UserID:          "u-bot",
			Scopes:          map[string]struct{}{"bot:webhooks:write": {}},
			AllowedChatIDs:  map[string]struct{}{"*": {}},
			AllowAllChatIDs: true,
		}},
		BotWebhooks: stubBotWebhookService{
			err: &botwebhooksvc.Error{Code: botwebhooksvc.CodeAlreadyExists, MessageKey: "error.bot.webhook_already_exists"},
		},
	})

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/public/v1/bot/webhooks", strings.NewReader(`{"endpoint_url":"https://example.com/hook","events":["message.created"]}`))
	req.Header.Set("Authorization", "Bearer tok-ok")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusConflict {
		t.Fatalf("expected 409, got %d; body=%s", rr.Code, rr.Body.String())
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
			"status.ok":                        "ok",
			"status.running":                   "running",
			"status.up":                        "up",
			"status.down":                      "down",
			"status.degraded":                  "degraded",
			"check.postgres":                   "postgres",
			"check.valkey":                     "valkey",
			"service.name":                     "combox-backend",
			"error.auth.missing_user_context":  "missing user context",
			"error.bot.invalid_token":          "invalid bot token",
			"bot.token.create.success":         "bot token created",
			"error.bot.invalid_token_input":    "invalid bot token input",
			"error.bot.missing_scope":          "missing required bot scope",
			"error.bot.chat_not_allowed":       "bot token has no access to this chat",
			"bot.webhook.create.success":       "bot webhook created",
			"error.bot.invalid_webhook_input":  "invalid bot webhook input",
			"error.bot.invalid_webhook_url":    "invalid bot webhook url",
			"error.bot.invalid_webhook_event":  "invalid bot webhook event",
			"error.bot.webhook_already_exists": "bot webhook already exists",
			"chat.create.success":              "chat created",
			"bot.message.list.success":         "bot messages fetched",
		},
		"ru": {
			"status.ok": "ок",
		},
	}}
}

type stubChatService struct {
	pinnedMessage *chatsvc.Message
}

func (stubChatService) CreateChat(context.Context, chatsvc.CreateChatInput) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: "chat-1", Title: "General", Type: "standard", Kind: "group"}, nil
}

func (stubChatService) CreateChannel(context.Context, chatsvc.CreateChannelInput) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: "channel-1", Title: "General", Type: "standard", Kind: "channel"}, nil
}

func (stubChatService) CreatePublicChannel(context.Context, chatsvc.CreatePublicChannelInput) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: "public-channel-1", Title: "News", Type: "standard", Kind: "standalone_channel", IsPublic: true}, nil
}

func (stubChatService) OpenDirectChat(_ context.Context, input chatsvc.OpenDirectChatInput) (chatsvc.Chat, error) {
	return chatsvc.Chat{
		ID:         "direct-1",
		Title:      input.RecipientUserID,
		Type:       "standard",
		Kind:       "direct",
		IsDirect:   true,
		PeerUserID: &input.RecipientUserID,
	}, nil
}

func (stubChatService) DeleteChannel(context.Context, chatsvc.DeleteChannelInput) error {
	return nil
}

func (stubChatService) DeleteChat(context.Context, string, string) error {
	return nil
}

func (stubChatService) DeleteChatForEveryone(context.Context, string, string) error {
	return nil
}

func (stubChatService) UpdateChat(_ context.Context, input chatsvc.UpdateChatInput) (chatsvc.Chat, error) {
	title := "General"
	if input.Title.Set && input.Title.Value != nil {
		title = *input.Title.Value
	}
	return chatsvc.Chat{ID: input.ChatID, Title: title, Type: "standard", Kind: "group"}, nil
}

func (stubChatService) GetChat(_ context.Context, _ string, chatID string) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: chatID, Title: "News", Type: "standard", Kind: "standalone_channel", IsPublic: true}, nil
}

func (stubChatService) ListChannels(context.Context, string, string) ([]chatsvc.Chat, error) {
	return []chatsvc.Chat{{ID: "chat-2", Title: "General / text", Type: "standard", Kind: "channel"}}, nil
}

func (stubChatService) ListMembers(context.Context, string, string, bool) ([]chatsvc.ChatMember, error) {
	return []chatsvc.ChatMember{
		{UserID: "u1", Role: "owner"},
		{UserID: "u2", Role: "member"},
	}, nil
}

func (stubChatService) ListChatEvents(context.Context, string, string, int) ([]chatsvc.ChatEvent, error) {
	return []chatsvc.ChatEvent{}, nil
}

func (stubChatService) AddMembers(context.Context, string, string, []string) ([]chatsvc.ChatMember, error) {
	return []chatsvc.ChatMember{
		{UserID: "u1", Role: "owner"},
		{UserID: "u2", Role: "member"},
		{UserID: "u3", Role: "member"},
	}, nil
}

func (stubChatService) UpdateMemberRole(context.Context, string, string, string, string) ([]chatsvc.ChatMember, error) {
	return []chatsvc.ChatMember{
		{UserID: "u1", Role: "owner"},
		{UserID: "u2", Role: "admin"},
	}, nil
}

func (stubChatService) RemoveMember(context.Context, string, string, string) ([]chatsvc.ChatMember, error) {
	return []chatsvc.ChatMember{
		{UserID: "u1", Role: "owner"},
	}, nil
}

func (stubChatService) SubscribePublicChannel(context.Context, string, string) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: "public-channel-1", Title: "News", Type: "standard", Kind: "standalone_channel", IsPublic: true}, nil
}

func (stubChatService) UnsubscribePublicChannel(context.Context, string, string) error {
	return nil
}

func (stubChatService) AcceptInvite(context.Context, string, string) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: "chat-1", Title: "General", Type: "standard", Kind: "group"}, nil
}

func (stubChatService) ListInviteLinks(context.Context, string, string) ([]chatsvc.ChatInviteLink, error) {
	return []chatsvc.ChatInviteLink{{
		ID:        "link-1",
		ChatID:    "public-channel-1",
		CreatedBy: "u1",
		Token:     "tok-1",
		IsPrimary: true,
		UseCount:  0,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}}, nil
}

func (stubChatService) CreateInviteLink(context.Context, chatsvc.CreateInviteLinkInput) (chatsvc.ChatInviteLink, error) {
	return chatsvc.ChatInviteLink{
		ID:        "link-1",
		ChatID:    "public-channel-1",
		CreatedBy: "u1",
		Token:     "tok-1",
		IsPrimary: false,
		UseCount:  0,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (stubChatService) AcceptInviteLink(context.Context, string, string) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: "public-channel-1", Title: "News", Type: "standard", Kind: "standalone_channel", IsPublic: true}, nil
}

func (stubChatService) LeaveChat(context.Context, string, string) error {
	return nil
}

func (stubChatService) ArchiveChat(_ context.Context, _ string, chatID string, archived bool) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: chatID, Title: "General", Type: "standard", Kind: "group", Archived: archived}, nil
}

func (stubChatService) PinChat(_ context.Context, _ string, chatID string, pinned bool, pinScope string, pinOrder int64) (chatsvc.Chat, error) {
	return chatsvc.Chat{ID: chatID, Title: "General", Type: "standard", Kind: "group", Pinned: pinned, PinScope: pinScope, PinOrder: pinOrder}, nil
}

func (stubChatService) MarkChatRead(context.Context, string, string) error {
	return nil
}

func (stubChatService) ClearChatHistory(context.Context, string, string) (int, error) {
	return 0, nil
}

func (stubChatService) ExportChatHistory(_ context.Context, _ string, chatID string) (chatsvc.ChatExport, error) {
	return chatsvc.ChatExport{
		Chat:     chatsvc.Chat{ID: chatID, Title: "General", Type: "standard", Kind: "group"},
		Messages: []chatsvc.ChatExportMessage{},
	}, nil
}

func (stubChatService) SetChatWallpaper(_ context.Context, _ string, chatID, wallpaperKind, wallpaperValue string) (chatsvc.Chat, error) {
	item := chatsvc.Chat{ID: chatID, Title: "General", Type: "standard", Kind: "group"}
	kind := strings.TrimSpace(wallpaperKind)
	if kind == "" {
		kind = chatsvc.WallpaperKindNone
	}
	item.WallpaperKind = &kind
	if value := strings.TrimSpace(wallpaperValue); value != "" {
		item.WallpaperValue = &value
	}
	return item, nil
}

func (stubChatService) CreatePoll(_ context.Context, input chatsvc.CreatePollInput) (chatsvc.Message, error) {
	poll := chatsvc.Poll{
		ID:          "poll-1",
		ChatID:      input.ChatID,
		MessageID:   "msg-1",
		Question:    input.Question,
		Options:     []chatsvc.PollOption{},
		MyOptionIDs: []string{},
		CreatedBy:   input.UserID,
	}
	message := chatsvc.Message{ID: "msg-1", ChatID: input.ChatID, UserID: input.UserID, Content: input.Question, Poll: &poll}
	return message, nil
}

func (stubChatService) GetPoll(context.Context, string, string) (chatsvc.Poll, error) {
	return chatsvc.Poll{ID: "poll-1", MyOptionIDs: []string{}}, nil
}

func (stubChatService) VotePoll(context.Context, chatsvc.VotePollInput) (chatsvc.Poll, error) {
	return chatsvc.Poll{ID: "poll-1", MyOptionIDs: []string{}}, nil
}

func (stubChatService) ClosePoll(context.Context, string, string) (chatsvc.Poll, error) {
	return chatsvc.Poll{ID: "poll-1", IsClosed: true, Closed: true, MyOptionIDs: []string{}}, nil
}

func (stubChatService) ListChats(context.Context, string) ([]chatsvc.Chat, error) {
	return []chatsvc.Chat{{ID: "chat-1", Title: "General", Type: "standard", Kind: "group"}}, nil
}

func (stubChatService) CreateMessage(context.Context, chatsvc.CreateMessageInput) (chatsvc.Message, error) {
	return chatsvc.Message{}, nil
}

func (stubChatService) CreateDirectMessage(context.Context, chatsvc.CreateDirectMessageInput) (chatsvc.Message, chatsvc.Chat, error) {
	return chatsvc.Message{}, chatsvc.Chat{}, nil
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

func (stubChatService) EditMessageByID(context.Context, string, string, string) (chatsvc.Message, error) {
	return chatsvc.Message{}, nil
}

func (stubChatService) ForwardMessage(context.Context, chatsvc.ForwardMessageInput) (chatsvc.Message, error) {
	return chatsvc.Message{}, nil
}

func (stubChatService) DeleteMessageByID(context.Context, string, string) error {
	return nil
}

func (stubChatService) MarkMessageReadByID(context.Context, string, string) (chatsvc.MessageStatus, error) {
	return chatsvc.MessageStatus{Status: "read"}, nil
}

func (stubChatService) ToggleMessageReactionByID(context.Context, string, string, string) ([]chatsvc.MessageReaction, string, error) {
	return nil, "set", nil
}

func (s stubChatService) GetPinnedMessage(context.Context, string, string) (*chatsvc.Message, error) {
	return s.pinnedMessage, nil
}

func (s stubChatService) PinMessage(_ context.Context, input chatsvc.PinMessageInput) (*chatsvc.Message, error) {
	if !input.Pinned {
		return nil, nil
	}
	if s.pinnedMessage != nil {
		return s.pinnedMessage, nil
	}
	return &chatsvc.Message{ID: input.MessageID, ChatID: input.ChatID, Content: "pinned"}, nil
}

func (stubChatService) GetOrCreateCommentThread(context.Context, string, string, string) (string, error) {
	return "thread-1", nil
}

func (stubChatService) BanPublicChannelUser(context.Context, string, string, string) error {
	return nil
}

func (stubChatService) UnbanPublicChannelUser(context.Context, string, string, string) error {
	return nil
}

func (stubChatService) MutePublicChannelUser(context.Context, string, string, string) error {
	return nil
}

func (stubChatService) UnmutePublicChannelUser(context.Context, string, string, string) error {
	return nil
}

func (stubChatService) ListPublicChannelBans(context.Context, string, string, int) ([]chatsvc.PublicChannelModerationEntry, error) {
	return nil, nil
}

func (stubChatService) ListPublicChannelMutes(context.Context, string, string, int) ([]chatsvc.PublicChannelModerationEntry, error) {
	return nil, nil
}

func (stubChatService) ListChatFolders(context.Context, string) ([]chatsvc.ChatFolder, error) {
	return []chatsvc.ChatFolder{}, nil
}

func (stubChatService) CreateChatFolder(context.Context, chatsvc.CreateChatFolderInput) (chatsvc.ChatFolder, error) {
	return chatsvc.ChatFolder{}, nil
}

func (stubChatService) UpdateChatFolder(context.Context, chatsvc.UpdateChatFolderInput) (chatsvc.ChatFolder, error) {
	return chatsvc.ChatFolder{}, nil
}

func (stubChatService) SetChatFolderChats(context.Context, string, string, []string) (chatsvc.ChatFolder, error) {
	return chatsvc.ChatFolder{}, nil
}

func (stubChatService) DeleteChatFolder(context.Context, string, string) error {
	return nil
}

func (stubChatService) CreateChatFolderInvite(context.Context, string, string) (chatsvc.ChatFolderInvite, error) {
	return chatsvc.ChatFolderInvite{}, nil
}

func (stubChatService) RevokeChatFolderInvite(context.Context, string, string) error {
	return nil
}

func (stubChatService) ResolveChatFolderInvite(context.Context, string, string) (chatsvc.ResolvedFolderInvite, error) {
	return chatsvc.ResolvedFolderInvite{Chats: []chatsvc.ResolvedFolderInviteChat{}}, nil
}

type stubAuthService struct {
	registerErr error
}

type stubBotAuthService struct {
	principal botauthsvc.Principal
	err       error
}

func (s stubBotAuthService) ValidateToken(context.Context, string) (botauthsvc.Principal, error) {
	if s.err != nil {
		return botauthsvc.Principal{}, s.err
	}
	if strings.TrimSpace(s.principal.UserID) == "" {
		return botauthsvc.Principal{}, errors.New("invalid token")
	}
	return s.principal, nil
}

type stubBotWebhookService struct {
	err error
}

func (s stubBotWebhookService) Create(context.Context, botwebhooksvc.CreateInput) (botwebhooksvc.Webhook, error) {
	if s.err != nil {
		return botwebhooksvc.Webhook{}, s.err
	}
	return botwebhooksvc.Webhook{
		ID:          "wh-1",
		BotUserID:   "u-bot",
		EndpointURL: "https://example.com/hook",
		Events:      []string{"message.created"},
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
	}, nil
}

type stubBotTokenService struct {
	err error
}

func (s stubBotTokenService) GenerateToken(context.Context, botauthsvc.GenerateTokenInput) (botauthsvc.GeneratedToken, error) {
	if s.err != nil {
		return botauthsvc.GeneratedToken{}, s.err
	}
	return botauthsvc.GeneratedToken{
		ID:          "11111111-1111-1111-1111-111111111111",
		Name:        "My bot",
		BotID:       "22222222-2222-2222-2222-222222222222",
		OwnerUserID: "u-test",
		Scopes:      []string{"bot:messages:read"},
		ChatIDs:     []string{"*"},
		Token:       "bt_11111111-1111-1111-1111-111111111111.secret",
	}, nil
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

func (s stubAuthService) EmailExists(context.Context, string) (bool, error) {
	return false, nil
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

func (s stubAuthService) GetProfile(context.Context, string) (authsvc.User, error) {
	return authsvc.User{
		ID:        "u1",
		Email:     "user@example.com",
		Username:  "user",
		FirstName: "User",
	}, nil
}

func (s stubAuthService) UpdateProfile(context.Context, authsvc.UpdateProfileInput) (authsvc.User, error) {
	return authsvc.User{
		ID:        "u1",
		Email:     "user@example.com",
		Username:  "user",
		FirstName: "User",
	}, nil
}

func (s stubAuthService) UpdateSessionIdleTTL(context.Context, string, *int64) (authsvc.User, error) {
	return authsvc.User{
		ID:        "u1",
		Email:     "user@example.com",
		Username:  "user",
		FirstName: "User",
	}, nil
}

func (s stubAuthService) ChangePassword(context.Context, string, string, string) error {
	return nil
}

func (s stubAuthService) UpdateEmail(context.Context, string, string) (authsvc.User, error) {
	return authsvc.User{
		ID:        "u1",
		Email:     "user2@example.com",
		Username:  "user",
		FirstName: "User",
	}, nil
}

func (s stubAuthService) ListSessions(context.Context, string, string) ([]authsvc.ActiveSession, error) {
	return []authsvc.ActiveSession{}, nil
}

func (s stubAuthService) RevokeSession(context.Context, string, string) error {
	return nil
}

func (s stubAuthService) RevokeOtherSessions(context.Context, string, string) (int64, error) {
	return 0, nil
}

func (s stubAuthService) IsLegacyUnverified(context.Context, string) (bool, error) {
	return false, nil
}

func (s stubAuthService) CompleteLegacyBind(context.Context, string, string, string, string) (authsvc.User, authsvc.Tokens, error) {
	return authsvc.User{}, authsvc.Tokens{}, nil
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
		AccessSecret:  "test-secret",
		Chat:          stubChatService{},
	})

	req := httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/chats", strings.NewReader(`{"title":"General","member_ids":["u2"]}`))
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
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

func TestUpdateChatRoute(t *testing.T) {
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

	req := httptest.NewRequest(stdhttp.MethodPatch, "/api/private/v1/chats/chat-1", strings.NewReader(`{"title":"Renamed"}`))
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"chat":{"id":"chat-1","title":"Renamed"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}

// recordingChatService captures the input the handler builds so tests can
// assert that PATCH payloads survive the HTTP layer.
type recordingChatService struct {
	stubChatService
	lastUpdateInput chatsvc.UpdateChatInput
	lastEventsCall  *chatEventsCall
}

type chatEventsCall struct {
	chatID string
	limit  int
	events []chatsvc.ChatEvent
}

func (s *recordingChatService) UpdateChat(ctx context.Context, input chatsvc.UpdateChatInput) (chatsvc.Chat, error) {
	s.lastUpdateInput = input
	return s.stubChatService.UpdateChat(ctx, input)
}

func (s *recordingChatService) ListChatEvents(_ context.Context, _ string, chatID string, limit int) ([]chatsvc.ChatEvent, error) {
	if s.lastEventsCall == nil {
		s.lastEventsCall = &chatEventsCall{}
	}
	s.lastEventsCall.chatID = chatID
	s.lastEventsCall.limit = limit
	return s.lastEventsCall.events, nil
}

func TestUpdateChatRoutePassesSendPermissionAndSettings(t *testing.T) {
	chatStub := &recordingChatService{}
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Chat:          chatStub,
	})

	body := `{
		"send_permission": "admins",
		"reactions_enabled": false,
		"sign_messages": true,
		"show_authors_profiles": true,
		"auto_translate": true,
		"slow_mode_seconds": 30,
		"discussion_chat_id": "chat-2"
	}`
	req := httptest.NewRequest(stdhttp.MethodPatch, "/api/private/v1/chats/chat-1", strings.NewReader(body))
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}

	got := chatStub.lastUpdateInput
	if !got.SendPermission.Set || got.SendPermission.Value == nil || *got.SendPermission.Value != "admins" {
		t.Fatalf("send_permission was dropped: %+v", got.SendPermission)
	}
	if !got.ReactionsEnabled.Set || got.ReactionsEnabled.Value {
		t.Fatalf("reactions_enabled was dropped: %+v", got.ReactionsEnabled)
	}
	if !got.SignMessages.Set || !got.SignMessages.Value {
		t.Fatalf("sign_messages was dropped: %+v", got.SignMessages)
	}
	if !got.ShowAuthorsProfiles.Set || !got.ShowAuthorsProfiles.Value {
		t.Fatalf("show_authors_profiles was dropped: %+v", got.ShowAuthorsProfiles)
	}
	if !got.AutoTranslate.Set || !got.AutoTranslate.Value {
		t.Fatalf("auto_translate was dropped: %+v", got.AutoTranslate)
	}
	if !got.SlowModeSeconds.Set || got.SlowModeSeconds.Value != 30 {
		t.Fatalf("slow_mode_seconds was dropped: %+v", got.SlowModeSeconds)
	}
	if !got.DiscussionChatID.Set || got.DiscussionChatID.Value == nil || *got.DiscussionChatID.Value != "chat-2" {
		t.Fatalf("discussion_chat_id was dropped: %+v", got.DiscussionChatID)
	}
}

func TestUpdateChatRoutePassesDescriptionIconAndChannelType(t *testing.T) {
	chatStub := &recordingChatService{}
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Chat:          chatStub,
	})

	body := `{"description":"About the team","icon_emoji":"📌","channel_type":"voice"}`
	req := httptest.NewRequest(stdhttp.MethodPatch, "/api/private/v1/chats/chat-1", strings.NewReader(body))
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}

	got := chatStub.lastUpdateInput
	if !got.Description.Set || got.Description.Value == nil || *got.Description.Value != "About the team" {
		t.Fatalf("description was dropped: %+v", got.Description)
	}
	if !got.IconEmoji.Set || got.IconEmoji.Value == nil || *got.IconEmoji.Value != "📌" {
		t.Fatalf("icon_emoji was dropped: %+v", got.IconEmoji)
	}
	if !got.ChannelType.Set || got.ChannelType.Value == nil || *got.ChannelType.Value != "voice" {
		t.Fatalf("channel_type was dropped: %+v", got.ChannelType)
	}

	// JSON null must map to "leave unchanged" rather than a cleared value.
	req = httptest.NewRequest(stdhttp.MethodPatch, "/api/private/v1/chats/chat-1", strings.NewReader(`{"description":null,"icon_emoji":null,"channel_type":null}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200 for null values, got %d; body=%s", rr.Code, rr.Body.String())
	}
	got = chatStub.lastUpdateInput
	if got.Description.Set || got.IconEmoji.Set || got.ChannelType.Set {
		t.Fatalf("JSON null should leave the fields unchanged: %+v %+v %+v", got.Description, got.IconEmoji, got.ChannelType)
	}
}

func TestListChatEventsRoute(t *testing.T) {
	chatStub := &recordingChatService{
		lastEventsCall: &chatEventsCall{events: []chatsvc.ChatEvent{{
			ID:        "evt-1",
			ChatID:    "chat-1",
			EventType: chatsvc.ChatEventSettingsChanged,
			Payload:   "reactions_enabled=false",
		}}},
	}
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Chat:          chatStub,
	})

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/events?limit=25", nil)
	token := makeAccessToken(t, "u1", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	if chatStub.lastEventsCall == nil || chatStub.lastEventsCall.chatID != "chat-1" || chatStub.lastEventsCall.limit != 25 {
		t.Fatalf("events call was not routed correctly: %+v", chatStub.lastEventsCall)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"event_type":"settings_changed"`) || !strings.Contains(body, `"reactions_enabled=false"`) {
		t.Fatalf("unexpected body: %s", body)
	}

	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200 without a limit, got %d", rr.Code)
	}
	if chatStub.lastEventsCall.limit != 0 {
		t.Fatalf("expected the default limit (0) to reach the service, got %d", chatStub.lastEventsCall.limit)
	}

	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chats/chat-1/events?limit=abc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed limit, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"code":"invalid_argument"`) {
		t.Fatalf("expected invalid_argument, got %s", rr.Body.String())
	}

	req = httptest.NewRequest(stdhttp.MethodPost, "/api/private/v1/chats/chat-1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", rr.Code)
	}
}
