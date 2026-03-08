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
	emailcodesvc "combox-backend/internal/service/emailcode"
	searchsvc "combox-backend/internal/service/search"

	vkrepo "combox-backend/internal/repository/valkey"

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
	Logger         *slog.Logger
	Postgres       PostgresPinger
	Valkey         ValkeyClient
	ReadyTimeout   time.Duration
	I18n           Translator
	DefaultLocale  string
	AccessSecret   string
	Auth           AuthService
	EmailCode      EmailCodeService
	Chat           ChatService
	Search         SearchService
	GIF            GIFService
	Media          MediaService
	E2E            E2EService
	BotAuth        BotAuthService
	BotTokens      BotTokenService
	BotWebhooks    BotWebhookService
	PresenceRepo   *vkrepo.PresenceRepository
	ProfileRepo    *vkrepo.ProfileSettingsRepository
	EmailChange    *vkrepo.EmailChangeRepository
	EmailChangeTTL time.Duration
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
		mux.HandleFunc("/api/private/v1/auth/email-exists", newEmailExistsHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/email-code/send", newEmailCodeSendHandler(deps.EmailCode, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/email-code/verify", newEmailCodeVerifyHandler(deps.EmailCode, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/register", newRegisterHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/login", newLoginHandler(deps.Auth, deps.EmailCode, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/refresh", newRefreshHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/logout", newLogoutHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile", newProfileHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/password", newProfilePasswordHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/users/", newUserByIDHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/start", newProfileEmailChangeStartHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/verify-old", newProfileEmailChangeVerifyOldHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/send-new", newProfileEmailChangeSendNewHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/confirm", newProfileEmailChangeConfirmHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
	}
	if deps.Chat != nil {
		mux.HandleFunc("/api/private/v1/chats", newChatsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/direct", newDirectChatHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/direct/messages", newDirectMessageHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/", newChatMessagesHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/messages/", newMessagesByIDHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/public-channels", newPublicChannelsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/public-channels/", newPublicChannelByIDHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
	}
	if deps.Search != nil {
		mux.HandleFunc("/api/private/v1/search", newSearchHandler(deps.Search, deps.I18n, deps.DefaultLocale))
	}
	if deps.GIF != nil {
		mux.HandleFunc("/api/private/v1/gifs/search", newGifsSearchHandler(deps.GIF, deps.I18n, deps.DefaultLocale))
		if deps.ProfileRepo != nil {
			mux.HandleFunc("/api/private/v1/gifs/recent", newGifsRecentHandler(deps.ProfileRepo, deps.I18n, deps.DefaultLocale))
		}
	}
	if deps.PresenceRepo != nil && deps.ProfileRepo != nil {
		mux.HandleFunc("/api/private/v1/presence", newPresenceHandler(deps.PresenceRepo, deps.ProfileRepo, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/settings", newProfileSettingsHandler(deps.ProfileRepo, deps.I18n, deps.DefaultLocale))
	}
	if deps.Media != nil {
		mux.HandleFunc("/api/private/v1/media/attachments", newMediaAttachmentsHandler(deps.Media, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/media/attachments/", newMediaAttachmentByIDHandler(deps.Media, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/media/sessions", newMediaSessionsHandler(deps.Media, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/media/sessions/", newMediaSessionByIDHandler(deps.Media, deps.I18n, deps.DefaultLocale))
	}
	if deps.BotTokens != nil {
		mux.HandleFunc("/api/private/v1/bot/tokens", newPrivateBotTokensHandler(deps.BotTokens, deps.I18n, deps.DefaultLocale))
	}
	if deps.Chat != nil && deps.BotAuth != nil {
		mux.HandleFunc("/api/public/v1/bot/messages", newPublicBotMessagesHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/public/v1/bot/chats/", newPublicBotChatMessagesHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		if deps.BotWebhooks != nil {
			mux.HandleFunc("/api/public/v1/bot/webhooks", newPublicBotWebhooksHandler(deps.BotWebhooks, deps.I18n, deps.DefaultLocale))
		}
	}
	if deps.Valkey != nil {
		mux.HandleFunc("/api/private/v1/ws", newWSHandler(deps.Valkey, wsDeps{ChatService: deps.Chat, SearchService: deps.Search}, deps.AccessSecret, deps.I18n, deps.DefaultLocale))
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
		PresenceHeartbeatMiddleware(deps.Valkey),
		RecoverMiddleware(deps.Logger),
		AccessLogMiddleware(deps.Logger),
	)
}

type AuthService interface {
	EmailExists(ctx context.Context, email string) (bool, error)
	Register(ctx context.Context, input authsvc.RegisterInput) (authsvc.User, authsvc.Tokens, error)
	Login(ctx context.Context, input authsvc.LoginInput) (authsvc.User, authsvc.Tokens, error)
	Refresh(ctx context.Context, input authsvc.RefreshInput) (authsvc.Tokens, error)
	Logout(ctx context.Context, input authsvc.LogoutInput) error
	GetProfile(ctx context.Context, userID string) (authsvc.User, error)
	UpdateProfile(ctx context.Context, input authsvc.UpdateProfileInput) (authsvc.User, error)
	UpdateSessionIdleTTL(ctx context.Context, userID string, sessionIdleTTLSeconds *int64) (authsvc.User, error)
	ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error
	UpdateEmail(ctx context.Context, userID, email string) (authsvc.User, error)
}

type EmailCodeService interface {
	SendCode(ctx context.Context, email, locale string) error
	SendCodeEmailOnly(ctx context.Context, email, locale string) error
	VerifyCode(ctx context.Context, email, code string) (bool, error)
	ConsumeVerified(ctx context.Context, email string) (bool, error)
	IssueLoginKey(ctx context.Context, email string) (string, error)
	ValidateLoginKey(ctx context.Context, email, key string) (bool, error)
	ConsumeLoginKey(ctx context.Context, email, key string) (bool, error)
}

type SearchService interface {
	Search(ctx context.Context, q string, scope string, limit int) (searchsvc.Results, error)
}

type ChatService interface {
	CreateChat(ctx context.Context, input chat.CreateChatInput) (chat.Chat, error)
	CreateChannel(ctx context.Context, input chat.CreateChannelInput) (chat.Chat, error)
	CreatePublicChannel(ctx context.Context, input chat.CreatePublicChannelInput) (chat.Chat, error)
	DeleteChannel(ctx context.Context, input chat.DeleteChannelInput) error
	SubscribePublicChannel(ctx context.Context, userID, chatID string) (chat.Chat, error)
	UnsubscribePublicChannel(ctx context.Context, userID, chatID string) error
	GetChat(ctx context.Context, userID, chatID string) (chat.Chat, error)
	UpdateChat(ctx context.Context, input chat.UpdateChatInput) (chat.Chat, error)
	ListInviteLinks(ctx context.Context, userID, chatID string) ([]chat.ChatInviteLink, error)
	CreateInviteLink(ctx context.Context, input chat.CreateInviteLinkInput) (chat.ChatInviteLink, error)
	AcceptInviteLink(ctx context.Context, userID, token string) (chat.Chat, error)
	ListChannels(ctx context.Context, userID, groupChatID string) ([]chat.Chat, error)
	ListMembers(ctx context.Context, userID, chatID string, includeBanned bool) ([]chat.ChatMember, error)
	AddMembers(ctx context.Context, userID, chatID string, memberIDs []string) ([]chat.ChatMember, error)
	UpdateMemberRole(ctx context.Context, actorUserID, chatID, targetUserID, role string) ([]chat.ChatMember, error)
	RemoveMember(ctx context.Context, actorUserID, chatID, targetUserID string) ([]chat.ChatMember, error)
	AcceptInvite(ctx context.Context, userID, token string) (chat.Chat, error)
	LeaveChat(ctx context.Context, userID, chatID string) error
	ListChats(ctx context.Context, userID string) ([]chat.Chat, error)
	CreateMessage(ctx context.Context, input chat.CreateMessageInput) (chat.Message, error)
	CreateDirectMessage(ctx context.Context, input chat.CreateDirectMessageInput) (chat.Message, chat.Chat, error)
	OpenDirectChat(ctx context.Context, input chat.OpenDirectChatInput) (chat.Chat, error)
	ListMessages(ctx context.Context, input chat.ListMessagesInput) (chat.MessagePage, error)
	UpsertMessageStatus(ctx context.Context, input chat.UpsertMessageStatusInput) (chat.MessageStatus, error)
	EditMessage(ctx context.Context, input chat.EditMessageInput) (chat.Message, error)
	EditMessageByID(ctx context.Context, userID, messageID, content string) (chat.Message, error)
	ForwardMessage(ctx context.Context, input chat.ForwardMessageInput) (chat.Message, error)
	DeleteMessageByID(ctx context.Context, userID, messageID string) error
	MarkMessageReadByID(ctx context.Context, userID, messageID string) (chat.MessageStatus, error)
	ToggleMessageReactionByID(ctx context.Context, userID, messageID, emoji string) ([]chat.MessageReaction, string, error)
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

type BotTokenService interface {
	GenerateToken(ctx context.Context, input botauthsvc.GenerateTokenInput) (botauthsvc.GeneratedToken, error)
}

type BotWebhookService interface {
	Create(ctx context.Context, input botwebhooksvc.CreateInput) (botwebhooksvc.Webhook, error)
}

var _ EmailCodeService = (*emailcodesvc.Service)(nil)

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
	if value := strings.TrimSpace(r.Header.Get("X-Client-Locale")); value != "" {
		return value
	}
	if value := strings.TrimSpace(r.Header.Get("Accept-Language")); value != "" {
		return value
	}
	if c, err := r.Cookie("language"); err == nil {
		if value := strings.TrimSpace(c.Value); value != "" {
			return value
		}
	}
	return fallback
}

type passthroughTranslator struct{}

func (passthroughTranslator) Translate(_ string, key string) string {
	return key
}
