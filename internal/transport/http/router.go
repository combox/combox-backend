package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	authsvc "combox-backend/internal/service/auth"
	botauthsvc "combox-backend/internal/service/botauth"
	botwebhooksvc "combox-backend/internal/service/botwebhook"
	"combox-backend/internal/service/chat"
	e2esvc "combox-backend/internal/service/e2e"

	"github.com/redis/go-redis/v9"
)

type PostgresPinger interface {
	Ping(ctx context.Context) error
}

type ValkeyPinger interface {
	Ping(ctx context.Context) error
}

type ValkeyClient interface {
	Ping(ctx context.Context) error
	Client() *redis.Client
}

type RouterDeps struct {
	Logger        *slog.Logger
	Postgres      PostgresPinger
	Valkey        ValkeyClient
	ReadyTimeout  time.Duration
	I18n          Translator
	DefaultLocale string
	AccessSecret  string
	Auth          AuthService
	Chat          ChatService
	Media         MediaService
	E2E           E2EService
	BotAuth       BotAuthService
	BotWebhooks   BotWebhookService
}

type Translator interface {
	Translate(requestLocale, key string) string
}

func NewRouter(deps RouterDeps) http.Handler {
	if deps.I18n == nil {
		deps.I18n = passthroughTranslator{}
	}
	if strings.TrimSpace(deps.DefaultLocale) == "" {
		deps.DefaultLocale = "en"
	}

	mux := http.NewServeMux()

	if deps.Auth != nil {
		mux.HandleFunc("/api/private/v1/auth/register", newRegisterHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/login", newLoginHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/refresh", newRefreshHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/logout", newLogoutHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
	}
	if deps.Chat != nil {
		mux.HandleFunc("/api/private/v1/chats", newChatsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/", newChatMessagesHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/messages/", newMessagesByIDHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
	}
	if deps.Media != nil {
		mux.HandleFunc("/api/private/v1/media/attachments", newMediaAttachmentsHandler(deps.Media, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/media/attachments/", newMediaAttachmentByIDHandler(deps.Media, deps.I18n, deps.DefaultLocale))
	}
	if deps.Chat != nil && deps.BotAuth != nil {
		mux.HandleFunc("/api/public/v1/bot/messages", newPublicBotMessagesHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/public/v1/bot/chats/", newPublicBotChatMessagesHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		if deps.BotWebhooks != nil {
			mux.HandleFunc("/api/public/v1/bot/webhooks", newPublicBotWebhooksHandler(deps.BotWebhooks, deps.I18n, deps.DefaultLocale))
		}
	}
	if deps.Valkey != nil {
		mux.HandleFunc("/api/private/v1/ws", newWSHandler(deps.Valkey, deps.AccessSecret, deps.I18n, deps.DefaultLocale))
	}
	if deps.E2E != nil {
		mux.HandleFunc("/api/private/v1/e2e/devices/", newE2EDeviceKeysHandler(deps.E2E, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/e2e/users/", newE2EUsersHandler(deps.E2E, deps.I18n, deps.DefaultLocale))
	}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		locale := requestLocale(r, deps.DefaultLocale)
		writeJSON(w, http.StatusOK, map[string]string{
			"status": deps.I18n.Translate(locale, "status.ok"),
		})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		locale := requestLocale(r, deps.DefaultLocale)
		ctx, cancel := context.WithTimeout(r.Context(), deps.ReadyTimeout)
		defer cancel()

		var checks []map[string]string
		hasFailures := false

		if err := deps.Postgres.Ping(ctx); err != nil {
			hasFailures = true
			checks = append(checks, map[string]string{
				"name":   deps.I18n.Translate(locale, "check.postgres"),
				"status": deps.I18n.Translate(locale, "status.down"),
				"error":  err.Error(),
			})
		} else {
			checks = append(checks, map[string]string{
				"name":   deps.I18n.Translate(locale, "check.postgres"),
				"status": deps.I18n.Translate(locale, "status.up"),
			})
		}

		if err := deps.Valkey.Ping(ctx); err != nil {
			hasFailures = true
			checks = append(checks, map[string]string{
				"name":   deps.I18n.Translate(locale, "check.valkey"),
				"status": deps.I18n.Translate(locale, "status.down"),
				"error":  err.Error(),
			})
		} else {
			checks = append(checks, map[string]string{
				"name":   deps.I18n.Translate(locale, "check.valkey"),
				"status": deps.I18n.Translate(locale, "status.up"),
			})
		}

		if hasFailures {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": deps.I18n.Translate(locale, "status.degraded"),
				"checks": checks,
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status": deps.I18n.Translate(locale, "status.ok"),
			"checks": checks,
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		locale := requestLocale(r, deps.DefaultLocale)
		writeJSON(w, http.StatusOK, map[string]string{
			"service": deps.I18n.Translate(locale, "service.name"),
			"status":  deps.I18n.Translate(locale, "status.running"),
		})
	})

	return chain(mux,
		RequestIDMiddleware,
		BotAuthMiddleware(deps.BotAuth, deps.I18n, deps.DefaultLocale),
		AuthMiddleware(deps.AccessSecret, deps.I18n, deps.DefaultLocale),
		RecoverMiddleware(deps.Logger),
		AccessLogMiddleware(deps.Logger),
	)
}

type AuthService interface {
	Register(ctx context.Context, input authsvc.RegisterInput) (authsvc.User, authsvc.Tokens, error)
	Login(ctx context.Context, input authsvc.LoginInput) (authsvc.User, authsvc.Tokens, error)
	Refresh(ctx context.Context, input authsvc.RefreshInput) (authsvc.Tokens, error)
	Logout(ctx context.Context, input authsvc.LogoutInput) error
}

type ChatService interface {
	CreateChat(ctx context.Context, input chat.CreateChatInput) (chat.Chat, error)
	ListChats(ctx context.Context, userID string) ([]chat.Chat, error)
	CreateMessage(ctx context.Context, input chat.CreateMessageInput) (chat.Message, error)
	ListMessages(ctx context.Context, input chat.ListMessagesInput) (chat.MessagePage, error)
	UpsertMessageStatus(ctx context.Context, input chat.UpsertMessageStatusInput) (chat.MessageStatus, error)
	EditMessage(ctx context.Context, input chat.EditMessageInput) (chat.Message, error)
	EditMessageByID(ctx context.Context, userID, messageID, content string) (chat.Message, error)
	ForwardMessage(ctx context.Context, input chat.ForwardMessageInput) (chat.Message, error)
	DeleteMessageByID(ctx context.Context, userID, messageID string) error
	MarkMessageReadByID(ctx context.Context, userID, messageID string) (chat.MessageStatus, error)
}

type E2EService interface {
	UpsertDeviceKeys(ctx context.Context, input e2esvc.UpsertDeviceKeysInput) (e2esvc.Device, error)
	ListUserDevices(ctx context.Context, userID string) ([]e2esvc.DeviceSummary, error)
	ClaimPreKeyBundle(ctx context.Context, userID, deviceID string) (e2esvc.PreKeyBundle, error)
	UpsertUserKeyBackup(ctx context.Context, input e2esvc.UpsertUserKeyBackupInput) (e2esvc.UserKeyBackup, error)
	GetUserKeyBackup(ctx context.Context, userID string) (e2esvc.UserKeyBackup, error)
}

type BotAuthService interface {
	ValidateToken(ctx context.Context, token string) (botauthsvc.Principal, error)
}

type BotWebhookService interface {
	Create(ctx context.Context, input botwebhooksvc.CreateInput) (botwebhooksvc.Webhook, error)
}

func chain(next http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		next = middlewares[i](next)
	}
	return next
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(payload)
}

func requestLocale(r *http.Request, fallback string) string {
	value := strings.TrimSpace(r.Header.Get("Accept-Language"))
	if value == "" {
		return fallback
	}
	return value
}

type passthroughTranslator struct{}

func (passthroughTranslator) Translate(_ string, key string) string {
	return key
}
