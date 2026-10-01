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
	Translate      TranslateService
	Privacy        PrivacyService
	ProfilePhotos  ProfilePhotoList
	GIF            GIFService
	Media          MediaService
	E2E            E2EService
	Calls          CallService
	BotAuth        BotAuthService
	BotTokens      BotTokenService
	BotWebhooks    BotWebhookService
	PresenceRepo   *vkrepo.PresenceRepository
	ProfileRepo    *vkrepo.ProfileSettingsRepository
	EmailChange    *vkrepo.EmailChangeRepository
	EmailChangeTTL time.Duration
	// LegacyBind serves the boxchat email-binding OTPs (migration 000044,
	// key "legacy-bind:<userID>", 10 minute TTL). LegacyMailSender sends
	// the OTP mails; nil means dev mode (code goes to the server log).
	LegacyBind *vkrepo.LegacyBindRepository
	LegacyTTL  time.Duration
	// LegacyMailSender is the Resend-backed mailer shared with the email-code
	// service; nil disables real sending (dev log mode in the handler).
	LegacyMailSender emailcodesvc.Sender
	// UserSettings serves the global app settings of the "Notifications and
	// Sounds" and "Data and Storage" sections.
	UserSettings UserSettingsService
	// Blocked serves the "Blocked users" list of Privacy and Security.
	Blocked BlockService
	// Reports serves POST /api/private/v1/reports (the Report buttons).
	Reports ReportService
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
		// Active sessions: the literal "revoke-others" pattern is more specific
		// than {sessionID}, so both can share the /auth/sessions prefix.
		mux.HandleFunc("/api/private/v1/auth/sessions", newAuthSessionsHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/sessions/revoke-others", newAuthRevokeOthersHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/sessions/{sessionID}", newAuthSessionItemHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile", newProfileHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/password", newProfilePasswordHandler(deps.Auth, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/users/", newUserByIDHandler(deps.Auth, deps.Privacy, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/start", newProfileEmailChangeStartHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/verify-old", newProfileEmailChangeVerifyOldHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/send-new", newProfileEmailChangeSendNewHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/email/change/confirm", newProfileEmailChangeConfirmHandler(deps.Auth, deps.EmailCode, deps.EmailChange, deps.EmailChangeTTL, deps.I18n, deps.DefaultLocale))
		// Boxchat legacy email binding (migration 000044): reachable with a
		// migr-limited token, see migrAllowed in middleware.go.
		mux.HandleFunc("/api/private/v1/auth/legacy/bind-email/request", newLegacyBindRequestHandler(deps.Auth, deps.LegacyBind, deps.LegacyMailSender, deps.Logger, deps.LegacyTTL, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/auth/legacy/bind-email/verify", newLegacyBindVerifyHandler(deps.Auth, deps.LegacyBind, deps.Logger, deps.I18n, deps.DefaultLocale))
	}
	if deps.Chat != nil {
		mux.HandleFunc("/api/private/v1/chats", newChatsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/direct", newDirectChatHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/direct/messages", newDirectMessageHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/", newChatMessagesHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		// Registered as their own patterns so they win over the /chats/ subtree.
		mux.HandleFunc("/api/private/v1/chats/{chatID}/clear", newChatClearHistoryHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/{chatID}/export", newChatExportHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/{chatID}/wallpaper", newChatWallpaperHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/{chatID}/polls", newChatPollsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/polls/{pollID}", newPollsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/polls/{pollID}/vote", newPollsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/polls/{pollID}/close", newPollsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/messages/", newMessagesByIDHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/standalone-channels", newPublicChannelsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/standalone-channels/", newPublicChannelByIDHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		// Chat folders: the collection is a plural literal, the per-folder
		// routes are wildcards so they never collide with it.
		mux.HandleFunc("/api/private/v1/chat-folders", newChatFoldersHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chat-folder/{folderID}", newChatFolderItemHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chat-folder/{folderID}/chats", newChatFolderChatsHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		// Folder share links: the per-folder mint/revoke routes stay on the
		// singular /chat-folder/ prefix (like the other per-folder routes)
		// while resolve lives on the plural collection prefix, so the
		// /{folderID}/invite and /invite/{token} patterns can never overlap
		// in ServeMux.
		mux.HandleFunc("/api/private/v1/chat-folder/{folderID}/invite", newChatFolderInviteHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chat-folders/invite/{token}", newChatFolderInviteResolveHandler(deps.Chat, deps.I18n, deps.DefaultLocale))
	}
	if deps.Search != nil {
		mux.HandleFunc("/api/private/v1/search", newSearchHandler(deps.Search, deps.I18n, deps.DefaultLocale))
	}
	if deps.Translate != nil {
		mux.HandleFunc("/api/private/v1/translate", newTranslateHandler(deps.Translate, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/translate/languages", newTranslateLanguagesHandler(deps.Translate, deps.I18n, deps.DefaultLocale))
	}
	if deps.ProfilePhotos != nil {
		// ServeMux ranks these wildcard patterns above the /users/ and
		// /chats/ subtree patterns, so registration order is irrelevant.
		mux.HandleFunc("/api/private/v1/users/{userID}/photos", newUserPhotosHandler(deps.ProfilePhotos, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/{chatID}/photos", newChatPhotosHandler(deps.ProfilePhotos, deps.I18n, deps.DefaultLocale))
		// Single-row deletes: the two-segment collection patterns above and
		// these three-segment item patterns never overlap in ServeMux.
		mux.HandleFunc("/api/private/v1/users/{userID}/photos/{photoID}", newUserPhotoItemHandler(deps.ProfilePhotos, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/chats/{chatID}/photos/{photoID}", newChatPhotoItemHandler(deps.ProfilePhotos, deps.I18n, deps.DefaultLocale))
	}
	if deps.GIF != nil {
		mux.HandleFunc("/api/private/v1/gifs/search", newGifsSearchHandler(deps.GIF, deps.I18n, deps.DefaultLocale))
		if deps.ProfileRepo != nil {
			mux.HandleFunc("/api/private/v1/gifs/recent", newGifsRecentHandler(deps.ProfileRepo, deps.I18n, deps.DefaultLocale))
		}
	}
	if deps.Privacy != nil {
		// /profile/privacy (exact) and /profile/privacy/{param} (one wildcard
		// segment) never collide with the other literal /profile/... routes.
		mux.HandleFunc("/api/private/v1/profile/privacy", newPrivacySettingsHandler(deps.Privacy, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/privacy/{param}", newPrivacySettingHandler(deps.Privacy, deps.I18n, deps.DefaultLocale))
	}
	if deps.PresenceRepo != nil && deps.ProfileRepo != nil {
		mux.HandleFunc("/api/private/v1/presence", newPresenceHandler(deps.PresenceRepo, deps.ProfileRepo, deps.Privacy, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/settings", newProfileSettingsHandler(deps.ProfileRepo, deps.I18n, deps.DefaultLocale))
	}
	if deps.UserSettings != nil {
		// Separate from /profile/settings on purpose: that one is the per-user
		// profile privacy/appearance block, this one is the app-wide toggles.
		mux.HandleFunc("/api/private/v1/profile/user-settings", newUserSettingsHandler(deps.UserSettings, deps.I18n, deps.DefaultLocale))
	}
	if deps.Blocked != nil {
		// The exact collection pattern and the {userID} item pattern never
		// collide with the /profile/privacy subtree (different prefix).
		mux.HandleFunc("/api/private/v1/profile/blocked", newBlockedListHandler(deps.Blocked, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/profile/blocked/{userID}", newBlockedItemHandler(deps.Blocked, deps.I18n, deps.DefaultLocale))
	}
	if deps.Reports != nil {
		mux.HandleFunc("/api/private/v1/reports", newReportsHandler(deps.Reports, deps.I18n, deps.DefaultLocale))
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
		mux.HandleFunc("/api/private/v1/ws", newWSHandler(deps.Valkey, wsDeps{ChatService: deps.Chat, SearchService: deps.Search, ProfileRepo: deps.ProfileRepo, Privacy: deps.Privacy}, deps.AccessSecret, deps.I18n, deps.DefaultLocale))
	}
	if deps.E2E != nil {
		mux.HandleFunc("/api/private/v1/e2e/devices/", newE2EDeviceKeysHandler(deps.E2E, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/e2e/users/", newE2EUsersHandler(deps.E2E, deps.I18n, deps.DefaultLocale))
	}
	if deps.Calls != nil {
		mux.HandleFunc("/api/private/v1/calls/ws", newCallsWSHandler(deps.Calls, deps.AccessSecret, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/calls/ice", newCallsICEHandler(deps.Calls, deps.I18n, deps.DefaultLocale))
		mux.HandleFunc("/api/private/v1/calls/active", newCallsActiveHandler(deps.Calls, deps.I18n, deps.DefaultLocale))
		if deps.Chat != nil {
			mux.HandleFunc("/api/private/v1/chats/{chatID}/calls", newChatCallsHandler(deps.Chat, deps.Calls, deps.I18n, deps.DefaultLocale))
			mux.HandleFunc("/api/private/v1/chats/{chatID}/calls/{callID}", newChatCallItemHandler(deps.Chat, deps.Calls, deps.I18n, deps.DefaultLocale))
		}
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
	// Active sessions, see service/auth/service_sessions.go. currentSessionID
	// is the sid claim of the caller's access token.
	ListSessions(ctx context.Context, userID, currentSessionID string) ([]authsvc.ActiveSession, error)
	RevokeSession(ctx context.Context, userID, sessionID string) error
	RevokeOtherSessions(ctx context.Context, userID, keepSessionID string) (int64, error)
	// Boxchat legacy migration (000044): IsLegacyUnverified reports the
	// users.is_legacy_unverified flag for a login; CompleteLegacyBind stores
	// the verified email, clears the flag (keeping legacy_username) and
	// issues a full session.
	IsLegacyUnverified(ctx context.Context, login string) (bool, error)
	CompleteLegacyBind(ctx context.Context, userID, email, userAgent, ipAddress string) (authsvc.User, authsvc.Tokens, error)
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

// UserSettingsService is the whitelist-backed store behind
// /api/private/v1/profile/user-settings. Values are always the strings
// "true"/"false"; the service fills in the documented defaults for keys the
// user never touched.
type UserSettingsService interface {
	GetUserSettings(ctx context.Context, userID string) (map[string]string, error)
	UpdateUserSettings(ctx context.Context, userID string, patch map[string]string) (map[string]string, error)
}

type ChatService interface {
	CreateChat(ctx context.Context, input chat.CreateChatInput) (chat.Chat, error)
	CreateChannel(ctx context.Context, input chat.CreateChannelInput) (chat.Chat, error)
	CreatePublicChannel(ctx context.Context, input chat.CreatePublicChannelInput) (chat.Chat, error)
	DeleteChannel(ctx context.Context, input chat.DeleteChannelInput) error
	DeleteChat(ctx context.Context, userID, chatID string) error
	DeleteChatForEveryone(ctx context.Context, userID, chatID string) error
	SubscribePublicChannel(ctx context.Context, userID, chatID string) (chat.Chat, error)
	UnsubscribePublicChannel(ctx context.Context, userID, chatID string) error
	GetChat(ctx context.Context, userID, chatID string) (chat.Chat, error)
	UpdateChat(ctx context.Context, input chat.UpdateChatInput) (chat.Chat, error)
	ListInviteLinks(ctx context.Context, userID, chatID string) ([]chat.ChatInviteLink, error)
	CreateInviteLink(ctx context.Context, input chat.CreateInviteLinkInput) (chat.ChatInviteLink, error)
	AcceptInviteLink(ctx context.Context, userID, token string) (chat.Chat, error)
	ListChannels(ctx context.Context, userID, groupChatID string) ([]chat.Chat, error)
	ListMembers(ctx context.Context, userID, chatID string, includeBanned bool) ([]chat.ChatMember, error)
	// ListChatEvents returns the newest "recent actions" journal entries of a
	// chat the viewer may read; limit <= 0 means the service default (50).
	ListChatEvents(ctx context.Context, userID, chatID string, limit int) ([]chat.ChatEvent, error)
	AddMembers(ctx context.Context, userID, chatID string, memberIDs []string) ([]chat.ChatMember, error)
	UpdateMemberRole(ctx context.Context, actorUserID, chatID, targetUserID, role string) ([]chat.ChatMember, error)
	RemoveMember(ctx context.Context, actorUserID, chatID, targetUserID string) ([]chat.ChatMember, error)
	AcceptInvite(ctx context.Context, userID, token string) (chat.Chat, error)
	LeaveChat(ctx context.Context, userID, chatID string) error
	ArchiveChat(ctx context.Context, userID, chatID string, archived bool) (chat.Chat, error)
	PinChat(ctx context.Context, userID, chatID string, pinned bool, pinScope string, pinOrder int64) (chat.Chat, error)
	MarkChatRead(ctx context.Context, userID, chatID string) error
	// ClearChatHistory hides the caller's copy of the history and reports how
	// many messages they could still see.
	ClearChatHistory(ctx context.Context, userID, chatID string) (int, error)
	ExportChatHistory(ctx context.Context, userID, chatID string) (chat.ChatExport, error)
	SetChatWallpaper(ctx context.Context, userID, chatID, wallpaperKind, wallpaperValue string) (chat.Chat, error)
	CreatePoll(ctx context.Context, input chat.CreatePollInput) (chat.Message, error)
	GetPoll(ctx context.Context, userID, pollID string) (chat.Poll, error)
	VotePoll(ctx context.Context, input chat.VotePollInput) (chat.Poll, error)
	ClosePoll(ctx context.Context, userID, pollID string) (chat.Poll, error)
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
	GetPinnedMessage(ctx context.Context, userID, chatID string) (*chat.Message, error)
	PinMessage(ctx context.Context, input chat.PinMessageInput) (*chat.Message, error)
	GetOrCreateCommentThread(ctx context.Context, userID, channelChatID, rootMessageID string) (string, error)
	BanPublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error
	UnbanPublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error
	MutePublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error
	UnmutePublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error
	ListPublicChannelBans(ctx context.Context, userID, channelChatID string, limit int) ([]chat.PublicChannelModerationEntry, error)
	ListPublicChannelMutes(ctx context.Context, userID, channelChatID string, limit int) ([]chat.PublicChannelModerationEntry, error)
	// Chat folders (the "Folders" dialog filters), see
	// service/chat/service_chat_folders.go.
	ListChatFolders(ctx context.Context, userID string) ([]chat.ChatFolder, error)
	CreateChatFolder(ctx context.Context, input chat.CreateChatFolderInput) (chat.ChatFolder, error)
	UpdateChatFolder(ctx context.Context, input chat.UpdateChatFolderInput) (chat.ChatFolder, error)
	SetChatFolderChats(ctx context.Context, userID, folderID string, chatIDs []string) (chat.ChatFolder, error)
	DeleteChatFolder(ctx context.Context, userID, folderID string) error
	// Folder share links (the "share folder" invites), see
	// service/chat/service_chat_folder_invites.go. Mint and revoke are owner
	// only; resolve previews the folder for any signed-in holder of the
	// token and never auto-joins.
	CreateChatFolderInvite(ctx context.Context, userID, folderID string) (chat.ChatFolderInvite, error)
	RevokeChatFolderInvite(ctx context.Context, userID, folderID string) error
	ResolveChatFolderInvite(ctx context.Context, userID, token string) (chat.ResolvedFolderInvite, error)
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
