package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"combox-backend/internal/calls"
	"combox-backend/internal/config"
	"combox-backend/internal/i18n"
	resendintegration "combox-backend/internal/integration/resend"
	systembotintegration "combox-backend/internal/integration/systembot"
	"combox-backend/internal/observability"
	miniorepo "combox-backend/internal/repository/minio"
	pgrepo "combox-backend/internal/repository/postgres"
	vkrepo "combox-backend/internal/repository/valkey"
	authsvc "combox-backend/internal/service/auth"
	blocksvc "combox-backend/internal/service/blocks"
	botauthsvc "combox-backend/internal/service/botauth"
	botwebhooksvc "combox-backend/internal/service/botwebhook"
	chatsvc "combox-backend/internal/service/chat"
	e2esvc "combox-backend/internal/service/e2e"
	emailcodesvc "combox-backend/internal/service/emailcode"
	gifsvc "combox-backend/internal/service/gif"
	mediasvc "combox-backend/internal/service/media"
	privacysvc "combox-backend/internal/service/privacy"
	profilephotosvc "combox-backend/internal/service/profilephoto"
	reportsvc "combox-backend/internal/service/reports"
	searchsvc "combox-backend/internal/service/search"
	settingssvc "combox-backend/internal/service/settings"
	translatesvc "combox-backend/internal/service/translate"
	httptransport "combox-backend/internal/transport/http"
)

type chatPublisherAdapter struct {
	p        *vkrepo.EventPublisher
	settings *vkrepo.ProfileSettingsRepository
	logger   *slog.Logger
}

// callMemberChecker lets the calls hub ask the chat service whether a user
// belongs to a chat; only members may join or stay in its calls.
type callMemberChecker struct {
	chat *chatsvc.Service
}

func (c callMemberChecker) IsMember(ctx context.Context, userID, chatID string) (bool, error) {
	if c.chat == nil {
		return false, errors.New("chat service is not configured")
	}
	_, err := c.chat.GetChat(ctx, userID, chatID)
	if err == nil {
		return true, nil
	}
	var svcErr *chatsvc.Error
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case chatsvc.CodeForbidden, chatsvc.CodeNotFound:
			return false, nil
		}
	}
	return false, err
}

// CanPublishStream reports whether the user may start a channel live stream.
// Channels reuse their moderation roles: only the owner/admin may publish,
// everybody else may only watch. Outside of channels every member may publish.
func (c callMemberChecker) CanPublishStream(ctx context.Context, userID, chatID string) (bool, error) {
	if c.chat == nil {
		return false, errors.New("chat service is not configured")
	}
	chat, err := c.chat.GetChat(ctx, userID, chatID)
	if err != nil {
		return false, err
	}
	if !isStandaloneChannelChat(chat) {
		return true, nil
	}
	if role := strings.ToLower(strings.TrimSpace(derefString(chat.ViewerRole))); role != "" {
		return role == "owner" || role == "admin", nil
	}
	// Private channels do not expose the viewer role on the chat payload; the
	// member list is the remaining role source and is itself limited to the
	// members that are allowed to see it (owner/admin on channels).
	members, err := c.chat.ListMembers(ctx, userID, chatID, false)
	if err != nil {
		var svcErr *chatsvc.Error
		if errors.As(err, &svcErr) && (svcErr.Code == chatsvc.CodeForbidden || svcErr.Code == chatsvc.CodeNotFound) {
			return false, nil
		}
		return false, err
	}
	for _, member := range members {
		if member.UserID != userID {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(member.Role))
		return role == "owner" || role == "admin", nil
	}
	return false, nil
}

func isStandaloneChannelChat(chat chatsvc.Chat) bool {
	switch strings.ToLower(strings.TrimSpace(chat.Kind)) {
	case "standalone_channel":
		return true
	case "channel":
		parent := ""
		if chat.ParentChatID != nil {
			parent = strings.TrimSpace(*chat.ParentChatID)
		}
		return parent == ""
	default:
		return false
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// callNotifier pushes call lifecycle events onto the realtime channel so chat
// members that are not in the signaling session yet can ring / dismiss.
type callNotifier struct {
	chat   *chatsvc.Service
	pub    *vkrepo.EventPublisher
	logger *slog.Logger
}

func (n callNotifier) CallStarted(ctx context.Context, call calls.CallRecord) {
	n.broadcast(ctx, call, nil)
}

func (n callNotifier) CallEnded(ctx context.Context, call calls.CallRecord, reason string) {
	n.broadcast(ctx, call, &reason)
}

func (n callNotifier) broadcast(ctx context.Context, call calls.CallRecord, reason *string) {
	if n.chat == nil || n.pub == nil {
		return
	}
	members, err := n.chat.ListMembers(ctx, call.StartedBy, call.ChatID, false)
	if err != nil {
		n.logger.Error("calls: list members for call notification",
			slog.String("call_id", call.ID),
			slog.String("chat_id", call.ChatID),
			slog.String("error", err.Error()))
		return
	}
	for _, member := range members {
		if member.UserID == "" || member.UserID == call.StartedBy {
			continue
		}
		ev := vkrepo.CallEvent{
			UserID:    member.UserID,
			CallID:    call.ID,
			ChatID:    call.ChatID,
			Kind:      string(call.Kind),
			E2EE:      call.E2EE,
			StartedBy: call.StartedBy,
			StartedAt: call.StartedAt,
		}
		var pubErr error
		if reason == nil {
			pubErr = n.pub.PublishCallStarted(ctx, ev)
		} else {
			ev.Reason = *reason
			pubErr = n.pub.PublishCallEnded(ctx, ev)
		}
		if pubErr != nil {
			n.logger.Error("calls: publish call notification",
				slog.String("call_id", call.ID),
				slog.String("user_id", member.UserID),
				slog.String("error", pubErr.Error()))
		}
	}
}

func (a chatPublisherAdapter) PublishDeviceMessageCreated(ctx context.Context, ev chatsvc.DeviceMessageCreatedEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	err := a.p.PublishDeviceMessageCreated(ctx, vkrepo.DeviceMessageCreatedEvent{
		MessageID:         ev.MessageID,
		ChatID:            ev.ChatID,
		SenderUserID:      ev.SenderUserID,
		SenderDeviceID:    ev.SenderDeviceID,
		RecipientDeviceID: ev.RecipientDeviceID,
		Alg:               ev.Alg,
		Header:            ev.Header,
		Ciphertext:        ev.Ciphertext,
		CreatedAt:         ev.CreatedAt,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "message.created.device"),
			slog.String("chat_id", ev.ChatID),
			slog.String("message_id", ev.MessageID),
			slog.String("recipient_device_id", ev.RecipientDeviceID),
			slog.String("error", err.Error()))
	}
	return err
}

func (a chatPublisherAdapter) PublishUserMessageCreated(ctx context.Context, ev chatsvc.UserMessageCreatedEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	err := a.p.PublishUserMessageCreated(ctx, vkrepo.UserMessageCreatedEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		SenderUserID:    ev.SenderUserID,
		RecipientUserID: ev.RecipientUserID,
		CreatedAt:       ev.CreatedAt,
		Preview:         ev.Preview,
	})
	// We always publish notification events, but mark muted chats so clients can suppress
	// sound/desktop notifications while still showing unread counters.
	muted := false
	if a.settings != nil {
		if isMuted, checkErr := a.settings.IsChatMuted(ctx, ev.RecipientUserID, ev.ChatID); checkErr == nil && isMuted {
			muted = true
		}
	}
	_ = a.p.PublishNotification(ctx, vkrepo.NotificationEvent{
		UserID: ev.RecipientUserID,
		Kind:   "message.created",
		Muted:  muted,
		Payload: map[string]string{
			"chat_id":        ev.ChatID,
			"message_id":     ev.MessageID,
			"sender_user_id": ev.SenderUserID,
			"preview":        ev.Preview,
		},
		CreatedAt: ev.CreatedAt,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "message.created"),
			slog.String("chat_id", ev.ChatID),
			slog.String("message_id", ev.MessageID),
			slog.String("recipient_user_id", ev.RecipientUserID),
			slog.String("error", err.Error()))
	}
	return err
}

func (a chatPublisherAdapter) PublishMessageStatus(ctx context.Context, ev chatsvc.MessageStatusEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	err := a.p.PublishMessageStatus(ctx, vkrepo.MessageStatusEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		UserID:          ev.UserID,
		RecipientUserID: ev.RecipientUserID,
		Status:          ev.Status,
		At:              ev.At,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "message.status"),
			slog.String("chat_id", ev.ChatID),
			slog.String("message_id", ev.MessageID),
			slog.String("recipient_user_id", ev.RecipientUserID),
			slog.String("error", err.Error()))
	}
	return err
}

func (a chatPublisherAdapter) PublishMessageUpdated(ctx context.Context, ev chatsvc.MessageUpdatedEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	err := a.p.PublishMessageUpdated(ctx, vkrepo.MessageUpdatedEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		EditorUserID:    ev.EditorUserID,
		RecipientUserID: ev.RecipientUserID,
		Content:         ev.Content,
		EditedAt:        ev.EditedAt,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "message.updated"),
			slog.String("chat_id", ev.ChatID),
			slog.String("message_id", ev.MessageID),
			slog.String("recipient_user_id", ev.RecipientUserID),
			slog.String("error", err.Error()))
	}
	return err
}

func (a chatPublisherAdapter) PublishMessageDeleted(ctx context.Context, ev chatsvc.MessageDeletedEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	err := a.p.PublishMessageDeleted(ctx, vkrepo.MessageDeletedEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		ActorUserID:     ev.ActorUserID,
		RecipientUserID: ev.RecipientUserID,
		At:              ev.At,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "message.deleted"),
			slog.String("chat_id", ev.ChatID),
			slog.String("message_id", ev.MessageID),
			slog.String("recipient_user_id", ev.RecipientUserID),
			slog.String("error", err.Error()))
	}
	return err
}

func (a chatPublisherAdapter) PublishMessageReaction(ctx context.Context, ev chatsvc.MessageReactionEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	reactions := make([]vkrepo.MessageReaction, 0, len(ev.Reactions))
	for _, reaction := range ev.Reactions {
		reactions = append(reactions, vkrepo.MessageReaction{
			Emoji:   reaction.Emoji,
			UserIDs: reaction.UserIDs,
		})
	}
	err := a.p.PublishMessageReaction(ctx, vkrepo.MessageReactionEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		ActorUserID:     ev.ActorUserID,
		RecipientUserID: ev.RecipientUserID,
		Emoji:           ev.Emoji,
		Action:          ev.Action,
		Reactions:       reactions,
		At:              ev.At,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "message.reaction"),
			slog.String("chat_id", ev.ChatID),
			slog.String("message_id", ev.MessageID),
			slog.String("recipient_user_id", ev.RecipientUserID),
			slog.String("error", err.Error()))
	}
	return err
}

func (a chatPublisherAdapter) PublishChatUpdated(ctx context.Context, ev chatsvc.ChatUpdatedEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	err := a.p.PublishChatUpdated(ctx, vkrepo.ChatUpdatedEvent{
		ChatID:          ev.ChatID,
		RecipientUserID: ev.RecipientUserID,
		Chat:            ev.Chat,
		UpdatedAt:       ev.UpdatedAt,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "chat.updated"),
			slog.String("chat_id", ev.ChatID),
			slog.String("recipient_user_id", ev.RecipientUserID),
			slog.String("error", err.Error()))
	}
	return err
}

type profilePublisherAdapter struct {
	p      *vkrepo.EventPublisher
	logger *slog.Logger
}

func (a profilePublisherAdapter) PublishProfileUpdate(ctx context.Context, ev authsvc.ProfileUpdatedEvent) error {
	if a.p == nil {
		return errors.New("valkey event publisher is nil")
	}
	err := a.p.PublishProfileUpdate(ctx, vkrepo.ProfileUpdateEvent{
		UserID:          ev.UserID,
		RecipientUserID: ev.RecipientUserID,
		ID:              ev.UserID,
		Email:           ev.Email,
		Username:        ev.Username,
		FirstName:       ev.FirstName,
		LastName:        ev.LastName,
		BirthDate:       ev.BirthDate,
		AvatarDataURL:   ev.AvatarDataURL,
		AvatarGradient:  ev.AvatarGradient,
	})
	if err != nil && a.logger != nil {
		a.logger.Error("publish ws event failed",
			slog.String("event", "profile.update"),
			slog.String("user_id", ev.UserID),
			slog.String("recipient_user_id", ev.RecipientUserID),
			slog.String("error", err.Error()))
	}
	return err
}

// profilePhotoUserAccess gates a user's photo history on the owner's
// profile_photos privacy rule: the owner always sees their own, everybody
// else needs Evaluate(viewer, owner, "profile_photos") to allow them.
type profilePhotoUserAccess struct {
	auth    *authsvc.Service
	privacy *privacysvc.Service
}

func (a profilePhotoUserAccess) CanViewUserPhotos(ctx context.Context, viewerID, ownerID string) error {
	viewerID = strings.TrimSpace(viewerID)
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return profilephotosvc.ErrNotFound
	}
	if viewerID == ownerID {
		return nil
	}
	if a.auth == nil {
		return profilephotosvc.ErrForbidden
	}
	if _, err := a.auth.GetProfile(ctx, ownerID); err != nil {
		var svcErr *authsvc.Error
		if errors.As(err, &svcErr) && (svcErr.Code == authsvc.CodeUnauthorized || svcErr.Code == authsvc.CodeInvalidArgument) {
			// Nobody's business that a missing account ever existed.
			return profilephotosvc.ErrNotFound
		}
		return err
	}
	if a.privacy == nil {
		return nil
	}
	allowed, err := a.privacy.Evaluate(ctx, viewerID, ownerID, privacysvc.ParamProfilePhotos)
	if err != nil {
		return err
	}
	if !allowed {
		return profilephotosvc.ErrForbidden
	}
	return nil
}

// forwardOriginProfiles resolves the origin user's avatar for the forward
// card. GetProfile already presigns the avatar object key, so the chat
// service gets a ready to use data URL.
type forwardOriginProfiles struct {
	auth *authsvc.Service
}

func (a forwardOriginProfiles) GetAvatarDataURL(ctx context.Context, userID string) (*string, error) {
	if a.auth == nil {
		return nil, errors.New("auth service is not configured")
	}
	user, err := a.auth.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	return user.AvatarDataURL, nil
}

// profilePhotoChatAccess reuses GetChat, which already runs ensureChatMember:
// only chat members (or, for a public channel, whoever may open it) see the
// chat's photo history.
type profilePhotoChatAccess struct {
	chat *chatsvc.Service
}

func (a profilePhotoChatAccess) CanViewChatPhotos(ctx context.Context, viewerID, chatID string) error {
	if a.chat == nil {
		return profilephotosvc.ErrForbidden
	}
	if strings.TrimSpace(viewerID) == "" || strings.TrimSpace(chatID) == "" {
		return profilephotosvc.ErrNotFound
	}
	if _, err := a.chat.GetChat(ctx, viewerID, chatID); err != nil {
		var svcErr *chatsvc.Error
		if errors.As(err, &svcErr) {
			switch svcErr.Code {
			case chatsvc.CodeNotFound:
				return profilephotosvc.ErrNotFound
			case chatsvc.CodeForbidden:
				return profilephotosvc.ErrForbidden
			}
		}
		return err
	}
	return nil
}

// CanDeleteChatPhotos mirrors the chat avatar edit gate (owner / admin /
// moderator): whoever may set the chat avatar may prune its history. A
// plain member or an outsider gets ErrForbidden.
func (a profilePhotoChatAccess) CanDeleteChatPhotos(ctx context.Context, viewerID, chatID string) error {
	if a.chat == nil {
		return profilephotosvc.ErrForbidden
	}
	if strings.TrimSpace(viewerID) == "" || strings.TrimSpace(chatID) == "" {
		return profilephotosvc.ErrNotFound
	}
	target, err := a.chat.GetChat(ctx, viewerID, chatID)
	if err != nil {
		var svcErr *chatsvc.Error
		if errors.As(err, &svcErr) {
			switch svcErr.Code {
			case chatsvc.CodeNotFound:
				return profilephotosvc.ErrNotFound
			case chatsvc.CodeForbidden:
				return profilephotosvc.ErrForbidden
			}
		}
		return err
	}
	role := ""
	if target.ViewerRole != nil {
		role = strings.ToLower(strings.TrimSpace(*target.ViewerRole))
	}
	switch role {
	case "owner", "admin", "moderator":
		return nil
	default:
		return profilephotosvc.ErrForbidden
	}
}

type sharedChatAudience struct {
	chats chatsvc.ChatRepository
}

// ListSharedChatMemberIDs returns every distinct user sharing at least one
// chat with userID, excluding userID itself. Ordering is not guaranteed.
func (a sharedChatAudience) ListSharedChatMemberIDs(ctx context.Context, userID string) ([]string, error) {
	cleanUserID := strings.TrimSpace(userID)
	if a.chats == nil || cleanUserID == "" {
		return nil, nil
	}
	chats, err := a.chats.ListChatsByUser(ctx, cleanUserID)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	memberIDs := make([]string, 0, 16)
	for _, chat := range chats {
		chatMemberIDs, listErr := a.chats.ListChatMemberIDs(ctx, chat.ID)
		if listErr != nil {
			return nil, listErr
		}
		for _, memberID := range chatMemberIDs {
			memberID = strings.TrimSpace(memberID)
			if memberID == "" || memberID == cleanUserID {
				continue
			}
			if _, exists := seen[memberID]; exists {
				continue
			}
			seen[memberID] = struct{}{}
			memberIDs = append(memberIDs, memberID)
		}
	}
	return memberIDs, nil
}

type mediaStoreAdapter struct{ c *miniorepo.Client }

func (a mediaStoreAdapter) Bucket() string {
	return a.c.Bucket()
}

func (a mediaStoreAdapter) NewMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error) {
	return a.c.NewMultipartUpload(ctx, objectKey, contentType)
}

func (a mediaStoreAdapter) PresignUploadPart(ctx context.Context, objectKey, uploadID string, partNumber int, expires time.Duration) (string, error) {
	return a.c.PresignUploadPart(ctx, objectKey, uploadID, partNumber, expires)
}

func (a mediaStoreAdapter) CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []mediasvc.CompletePart, contentType string) error {
	converted := make([]miniorepo.CompletePart, 0, len(parts))
	for _, p := range parts {
		converted = append(converted, miniorepo.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	return a.c.CompleteMultipartUpload(ctx, objectKey, uploadID, converted, contentType)
}

func (a mediaStoreAdapter) PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error) {
	return a.c.PresignGetObject(ctx, objectKey, expires)
}

func (a mediaStoreAdapter) GetObject(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	return a.c.GetObject(ctx, objectKey)
}

func (a mediaStoreAdapter) PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error {
	return a.c.PutObject(ctx, objectKey, contentType, body, size)
}

func (a mediaStoreAdapter) DeleteObject(ctx context.Context, objectKey string) error {
	return a.c.DeleteObject(ctx, objectKey)
}

func (a mediaStoreAdapter) CopyObject(ctx context.Context, srcKey, dstKey string) error {
	return a.c.CopyObject(ctx, srcKey, dstKey)
}

type chatInviteStoreAdapter struct {
	r *vkrepo.ChatInviteRepository
}

func (a chatInviteStoreAdapter) Create(ctx context.Context, chatID, inviterID, inviteeID string, ttl time.Duration) (chatsvc.ChatInvite, error) {
	item, err := a.r.Create(ctx, chatID, inviterID, inviteeID, ttl)
	if err != nil {
		return chatsvc.ChatInvite{}, err
	}
	return chatsvc.ChatInvite{
		Token:     item.Token,
		ChatID:    item.ChatID,
		InviterID: item.InviterID,
		InviteeID: item.InviteeID,
		CreatedAt: item.CreatedAt,
		ExpiresAt: item.ExpiresAt,
	}, nil
}

func (a chatInviteStoreAdapter) Consume(ctx context.Context, token string) (chatsvc.ChatInvite, bool, error) {
	item, found, err := a.r.Consume(ctx, token)
	if err != nil || !found {
		return chatsvc.ChatInvite{}, found, err
	}
	return chatsvc.ChatInvite{
		Token:     item.Token,
		ChatID:    item.ChatID,
		InviterID: item.InviterID,
		InviteeID: item.InviteeID,
		CreatedAt: item.CreatedAt,
		ExpiresAt: item.ExpiresAt,
	}, true, nil
}

func Run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := observability.NewLogger(cfg.App.Env)
	logger.Info("starting combox-backend", slog.String("env", cfg.App.Env), slog.String("http_address", cfg.App.HTTPAddress))

	catalog, err := i18n.LoadDir(cfg.App.StringsPath, cfg.App.DefaultLocale)
	if err != nil {
		return fmt.Errorf("load strings catalog: %w", err)
	}

	postgresClient, err := pgrepo.New(ctx, cfg.Postgres.DSN)
	if err != nil {
		return fmt.Errorf("init postgres: %w", err)
	}
	defer postgresClient.Close()

	valkeyClient := vkrepo.New(vkrepo.Config{
		Addr:     cfg.Valkey.Addr,
		Password: cfg.Valkey.Password,
		DB:       cfg.Valkey.DB,
	})
	{
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := valkeyClient.Ping(pingCtx); err != nil {
			return fmt.Errorf("init valkey: %w", err)
		}
	}
	defer func() {
		if closeErr := valkeyClient.Close(); closeErr != nil {
			logger.Error("close valkey", slog.String("error", closeErr.Error()))
		}
	}()

	if cfg.Migrations.Enabled {
		if err := RunMigrations(ctx, logger, postgresClient.Pool(), cfg.Migrations.Path); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}

	minioClient, err := miniorepo.New(cfg.MinIO)
	if err != nil {
		return fmt.Errorf("init minio: %w", err)
	}
	{
		ensureCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := minioClient.EnsureBucket(ensureCtx); err != nil {
			return fmt.Errorf("ensure minio bucket: %w", err)
		}
	}

	authService, err := authsvc.New(authsvc.Config{
		Users:         pgrepo.NewAuthUserRepository(postgresClient),
		Sessions:      pgrepo.NewAuthSessionRepository(postgresClient),
		Avatars:       minioClient,
		AccessSecret:  cfg.Auth.AccessSecret,
		RefreshSecret: cfg.Auth.RefreshSecret,
		AccessTTL:     cfg.Auth.AccessTTL,
		RefreshTTL:    cfg.Auth.RefreshTTL,
	})
	if err != nil {
		return fmt.Errorf("init auth service: %w", err)
	}

	chatRepo := pgrepo.NewChatRepository(postgresClient)
	msgRepo := pgrepo.NewMessageRepository(postgresClient)
	pollRepo := pgrepo.NewPollRepository(postgresClient)
	chatEventRepo := pgrepo.NewChatEventRepository(postgresClient)
	publisher := vkrepo.NewEventPublisher(valkeyClient)
	statusRepo := vkrepo.NewMessageStatusRepository(valkeyClient)
	presenceRepo := vkrepo.NewPresenceRepository(valkeyClient)
	profileRepo := vkrepo.NewProfileSettingsRepository(valkeyClient)
	emailChangeRepo := vkrepo.NewEmailChangeRepository(valkeyClient)
	legacyBindRepo := vkrepo.NewLegacyBindRepository(valkeyClient)
	chatInviteRepo := vkrepo.NewChatInviteRepository(valkeyClient)

	privacyRepo := pgrepo.NewPrivacyRepository(postgresClient)
	privacySvc, err := privacysvc.New(privacyRepo)
	if err != nil {
		return fmt.Errorf("init privacy service: %w", err)
	}

	chatPublisher := &chatPublisherAdapter{p: publisher, settings: profileRepo, logger: logger}
	chatSvc, err := chatsvc.NewWithPublisherAndStatusRepo(chatRepo, msgRepo, chatPublisher, statusRepo)
	if err != nil {
		return fmt.Errorf("init chat service: %w", err)
	}
	chatSvc.SetAvatarStore(minioClient, 0)
	chatSvc.SetPollRepository(pollRepo)
	chatSvc.SetChatEventRepository(chatEventRepo)
	chatSvc.SetNotificationRepository(profileRepo)
	chatSvc.SetInviteRepository(chatInviteStoreAdapter{r: chatInviteRepo}, 0)
	chatSvc.SetPublicAppBaseURL(os.Getenv("PUBLIC_APP_BASE_URL"))
	// Message serialisation enforces the origin owner's forwarded_messages
	// rule and attaches the forward card avatar when the origin is visible.
	chatSvc.SetForwardPrivacy(privacySvc)
	chatSvc.SetForwardOriginProfiles(forwardOriginProfiles{auth: authService})

	// Dialog filter folders ("Folders" in the settings) and the global app
	// toggles of "Notifications and Sounds" / "Data and Storage".
	chatSvc.SetChatFolderRepository(pgrepo.NewChatFolderRepository(postgresClient))
	chatSvc.SetChatFolderInviteRepository(pgrepo.NewChatFolderRepository(postgresClient))
	userSettingsSvc, err := settingssvc.New(pgrepo.NewUserSettingsRepository(postgresClient))
	if err != nil {
		return fmt.Errorf("init user settings service: %w", err)
	}

	// Blocked users ("Privacy and Security -> Blocked users", 000046).
	blockedSvc, err := blocksvc.New(pgrepo.NewBlockedUsersRepository(postgresClient))
	if err != nil {
		return fmt.Errorf("init blocked users service: %w", err)
	}

	// User reports for the Report buttons (POST /api/private/v1/reports,
	// migration 000047). Anti-spam is the in-service 10/min sliding window.
	reportsSvc, err := reportsvc.New(pgrepo.NewReportsRepository(postgresClient))
	if err != nil {
		return fmt.Errorf("init reports service: %w", err)
	}

	authService.SetProfileEventPublisher(profilePublisherAdapter{p: publisher, logger: logger})
	authService.SetProfileAudienceResolver(sharedChatAudience{chats: chatRepo})

	profilePhotoRepo := pgrepo.NewProfilePhotoRepository(postgresClient)
	profilePhotoSvc, err := profilephotosvc.New(profilephotosvc.Config{
		Store:      profilePhotoRepo,
		Avatars:    minioClient,
		UserAccess: profilePhotoUserAccess{auth: authService, privacy: privacySvc},
		ChatAccess: profilePhotoChatAccess{chat: chatSvc},
	})
	if err != nil {
		return fmt.Errorf("init profile photo service: %w", err)
	}
	authService.SetProfilePhotoRecorder(profilePhotoSvc)
	chatSvc.SetProfilePhotoRecorder(profilePhotoSvc)

	e2eService, err := e2esvc.New(pgrepo.NewE2ERepository(postgresClient))
	if err != nil {
		return fmt.Errorf("init e2e service: %w", err)
	}

	mediaService, err := mediasvc.New(pgrepo.NewMediaRepository(postgresClient), mediaStoreAdapter{c: minioClient})
	if err != nil {
		return fmt.Errorf("init media service: %w", err)
	}

	searchService := searchsvc.New(pgrepo.NewSearchRepository(postgresClient))
	if searchService != nil {
		searchService.SetAvatarStore(minioClient, 0)
	}
	gifService := gifsvc.New(strings.TrimSpace(os.Getenv("GIPHY_API_KEY")))
	if !gifService.Enabled() {
		gifService = nil
	}

	// R19 auto-translate engine. Empty TRANSLATE_ENGINE_URL selects the
	// MyMemory free endpoint (no key); a LibreTranslate-compatible base URL
	// enables server-side auto-detect of the source language. Translations
	// are cached in Valkey (memory fallback lives inside the service).
	translateService, err := translatesvc.New(translatesvc.Config{
		EngineURL:       cfg.Translate.EngineURL,
		APIKey:          cfg.Translate.APIKey,
		MyMemoryEmail:   cfg.Translate.MyMemoryEmail,
		Timeout:         cfg.Translate.Timeout,
		CacheTTL:        cfg.Translate.CacheTTL,
		RateLimitPerMin: cfg.Translate.RateLimitPerMin,
		Cache:           translatesvc.NewValkeyCache(valkeyClient.Client()),
	})
	if err != nil {
		return fmt.Errorf("init translate service: %w", err)
	}
	logger.Info("translate engine ready", slog.String("provider", translateService.ProviderName()))

	var callsService *calls.Service
	{
		// Fail-fast on out-of-range UDP ports: CallsConfig carries ints
		// parsed via Atoi (see config.getIntEnv), so a value like 70000
		// would silently truncate on uint16() (CodeQL
		// go/incorrect-integer-conversion) and pin the SFU to the wrong
		// range. Ports are critical, so reject instead of clamping.
		if cfg.Calls.ICEPortMin < 0 || cfg.Calls.ICEPortMin > 65535 {
			return fmt.Errorf("invalid CALLS_ICE_PORT_MIN %d: must be 0..65535 (0 = OS ephemeral, set together with CALLS_ICE_PORT_MAX)", cfg.Calls.ICEPortMin)
		}
		if cfg.Calls.ICEPortMax < 0 || cfg.Calls.ICEPortMax > 65535 {
			return fmt.Errorf("invalid CALLS_ICE_PORT_MAX %d: must be 0..65535 (0 = OS ephemeral, set together with CALLS_ICE_PORT_MIN)", cfg.Calls.ICEPortMax)
		}
		icePortMin := uint16(cfg.Calls.ICEPortMin)
		icePortMax := uint16(cfg.Calls.ICEPortMax)
		buildErr := error(nil)
		callsService, buildErr = calls.New(calls.Config{
			Enabled:           cfg.Calls.Enabled,
			STUNURLs:          cfg.Calls.STUNURLs,
			TURNURLs:          cfg.Calls.TURNURLs,
			TURNSharedSecret:  cfg.Calls.TURNSharedSecret,
			TURNCredentialTTL: cfg.Calls.TURNCredentialTTL,
			MeshLimit:         cfg.Calls.MeshLimit,
			MaxParticipants:   cfg.Calls.MaxParticipants,
			ICEPortMin:        icePortMin,
			ICEPortMax:        icePortMax,
			AllowLoopback:     cfg.Calls.AllowLoopback,
			Logger:            logger,
		}, pgrepo.NewCallRepository(postgresClient), callMemberChecker{chat: chatSvc})
		if buildErr != nil {
			return fmt.Errorf("init calls service: %w", buildErr)
		}
		callsService.SetNotifier(callNotifier{chat: chatSvc, pub: publisher, logger: logger})
	}

	botTokenRepo := pgrepo.NewBotTokenRepository(postgresClient)
	botAuthService, err := botauthsvc.New(botTokenRepo, cfg.Bot.TokenPepper)
	if err != nil {
		return fmt.Errorf("init bot auth service: %w", err)
	}
	botWebhookService := botwebhooksvc.New()

	var emailCodeService *emailcodesvc.Service
	var legacyMail emailcodesvc.Sender
	if cfg.Auth.EmailVerify.Enabled {
		resendSender, err := resendintegration.New(resendintegration.Config{
			APIKey:  cfg.Auth.EmailVerify.ResendAPIKey,
			From:    cfg.Auth.EmailVerify.ResendFrom,
			BaseURL: cfg.Auth.EmailVerify.ResendBase,
		})
		if err != nil {
			return fmt.Errorf("init resend sender: %w", err)
		}
		// The same Resend mailer doubles as the sender for the boxchat
		// legacy email-binding OTPs; nil (verify disabled) means the
		// bind handler runs in dev-log mode.
		legacyMail = resendSender

		emailCodeService, err = emailcodesvc.New(emailcodesvc.Config{
			Sender:      resendSender,
			Notifier:    systembotintegration.New(postgresClient.Pool(), chatSvc),
			I18n:        catalog,
			CodeTTL:     cfg.Auth.EmailVerify.CodeTTL,
			VerifiedTTL: cfg.Auth.EmailVerify.CodeTTL,
			MaxAttempts: cfg.Auth.EmailVerify.MaxAttempts,
		})
		if err != nil {
			return fmt.Errorf("init email code service: %w", err)
		}
	}

	var emailCodeAPI httptransport.EmailCodeService
	if emailCodeService != nil {
		emailCodeAPI = emailCodeService
	}

	router := httptransport.NewRouter(httptransport.RouterDeps{
		Logger:         logger,
		Postgres:       postgresClient,
		Valkey:         valkeyClient,
		ReadyTimeout:   cfg.App.ReadyTimeout,
		I18n:           catalog,
		DefaultLocale:  cfg.App.DefaultLocale,
		AccessSecret:   cfg.Auth.AccessSecret,
		Auth:           authService,
		EmailCode:      emailCodeAPI,
		Chat:           chatSvc,
		Search:         searchService,
		Translate:      translateService,
		Privacy:        privacySvc,
		ProfilePhotos:  profilePhotoSvc,
		GIF:            gifService,
		Media:          mediaService,
		E2E:            e2eService,
		Calls:          callsService,
		BotAuth:        botAuthService,
		BotTokens:      botAuthService,
		BotWebhooks:    botWebhookService,
		PresenceRepo:   presenceRepo,
		ProfileRepo:    profileRepo,
		EmailChange:    emailChangeRepo,
		EmailChangeTTL: cfg.Auth.EmailVerify.CodeTTL,
		// Boxchat legacy binding: OTP store + shared Resend mailer (nil when
		// email verification is disabled -> dev-log mode in the handler).
		LegacyBind:       legacyBindRepo,
		LegacyTTL:        cfg.Auth.EmailVerify.CodeTTL,
		LegacyMailSender: legacyMail,
		UserSettings:     userSettingsSvc,
		Blocked:          blockedSvc,
		Reports:          reportsSvc,
	})

	httpServer := &http.Server{
		Addr:         cfg.App.HTTPAddress,
		Handler:      router,
		ReadTimeout:  cfg.App.ReadTimeout,
		WriteTimeout: cfg.App.WriteTimeout,
	}

	var tlsCertFile string
	var tlsKeyFile string
	if cfg.App.TLSEnabled {
		caPEM, err := os.ReadFile(cfg.App.TLSClientCAFile)
		if err != nil {
			return fmt.Errorf("read tls client ca file: %w", err)
		}
		clientCAs := x509.NewCertPool()
		if ok := clientCAs.AppendCertsFromPEM(caPEM); !ok {
			return fmt.Errorf("parse tls client ca file")
		}

		httpServer.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ClientCAs:  clientCAs,
			ClientAuth: tls.RequireAndVerifyClientCert,
		}
		tlsCertFile = cfg.App.TLSCertFile
		tlsKeyFile = cfg.App.TLSKeyFile
	}

	shutdownCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", slog.String("addr", cfg.App.HTTPAddress), slog.Bool("tls", cfg.App.TLSEnabled))
		var err error
		if cfg.App.TLSEnabled {
			err = httpServer.ListenAndServeTLS(tlsCertFile, tlsKeyFile)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-shutdownCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server failed: %w", err)
		}
		return nil
	}

	gracefulCtx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(gracefulCtx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}

	if callsService != nil {
		callsService.Close(gracefulCtx)
	}

	logger.Info("combox-backend stopped", slog.Duration("shutdown_timeout", cfg.App.ShutdownTimeout), slog.Time("stopped_at", time.Now().UTC()))
	return nil
}
