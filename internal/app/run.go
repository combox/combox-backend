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

	"combox-backend/internal/config"
	"combox-backend/internal/i18n"
	resendintegration "combox-backend/internal/integration/resend"
	systembotintegration "combox-backend/internal/integration/systembot"
	"combox-backend/internal/observability"
	miniorepo "combox-backend/internal/repository/minio"
	pgrepo "combox-backend/internal/repository/postgres"
	vkrepo "combox-backend/internal/repository/valkey"
	authsvc "combox-backend/internal/service/auth"
	botauthsvc "combox-backend/internal/service/botauth"
	botwebhooksvc "combox-backend/internal/service/botwebhook"
	chatsvc "combox-backend/internal/service/chat"
	e2esvc "combox-backend/internal/service/e2e"
	emailcodesvc "combox-backend/internal/service/emailcode"
	gifsvc "combox-backend/internal/service/gif"
	mediasvc "combox-backend/internal/service/media"
	searchsvc "combox-backend/internal/service/search"
	httptransport "combox-backend/internal/transport/http"
)

type chatPublisherAdapter struct {
	p      *vkrepo.EventPublisher
	logger *slog.Logger
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
	})
	_ = a.p.PublishNotification(ctx, vkrepo.NotificationEvent{
		UserID:    ev.RecipientUserID,
		Kind:      "message.created",
		Payload:   map[string]string{"chat_id": ev.ChatID, "message_id": ev.MessageID, "sender_user_id": ev.SenderUserID},
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
	publisher := vkrepo.NewEventPublisher(valkeyClient)
	statusRepo := vkrepo.NewMessageStatusRepository(valkeyClient)
	presenceRepo := vkrepo.NewPresenceRepository(valkeyClient)
	profileRepo := vkrepo.NewProfileSettingsRepository(valkeyClient)
	emailChangeRepo := vkrepo.NewEmailChangeRepository(valkeyClient)
	chatInviteRepo := vkrepo.NewChatInviteRepository(valkeyClient)

	chatPublisher := &chatPublisherAdapter{p: publisher, logger: logger}
	chatSvc, err := chatsvc.NewWithPublisherAndStatusRepo(chatRepo, msgRepo, chatPublisher, statusRepo)
	if err != nil {
		return fmt.Errorf("init chat service: %w", err)
	}
	chatSvc.SetAvatarStore(minioClient, 0)
	chatSvc.SetNotificationRepository(profileRepo)
	chatSvc.SetInviteRepository(chatInviteStoreAdapter{r: chatInviteRepo}, 0)
	messageSvc := chatsvc.NewMessageService(chatSvc)

	standaloneSvc, err := chatsvc.NewStandaloneChannelService(chatRepo)
	if err != nil {
		return fmt.Errorf("init standalone channel service: %w", err)
	}
	standaloneSvc.SetAvatarStore(minioClient, 0)

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

	botTokenRepo := pgrepo.NewBotTokenRepository(postgresClient)
	botAuthService, err := botauthsvc.New(botTokenRepo, cfg.Bot.TokenPepper)
	if err != nil {
		return fmt.Errorf("init bot auth service: %w", err)
	}
	botWebhookService := botwebhooksvc.New()

	var emailCodeService *emailcodesvc.Service
	if cfg.Auth.EmailVerify.Enabled {
		resendSender, err := resendintegration.New(resendintegration.Config{
			APIKey:  cfg.Auth.EmailVerify.ResendAPIKey,
			From:    cfg.Auth.EmailVerify.ResendFrom,
			BaseURL: cfg.Auth.EmailVerify.ResendBase,
		})
		if err != nil {
			return fmt.Errorf("init resend sender: %w", err)
		}

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
		Messages:       messageSvc,
		Standalone:     standaloneSvc,
		Search:         searchService,
		GIF:            gifService,
		Media:          mediaService,
		E2E:            e2eService,
		BotAuth:        botAuthService,
		BotTokens:      botAuthService,
		BotWebhooks:    botWebhookService,
		PresenceRepo:   presenceRepo,
		ProfileRepo:    profileRepo,
		EmailChange:    emailChangeRepo,
		EmailChangeTTL: cfg.Auth.EmailVerify.CodeTTL,
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

	logger.Info("combox-backend stopped", slog.Duration("shutdown_timeout", cfg.App.ShutdownTimeout), slog.Time("stopped_at", time.Now().UTC()))
	return nil
}
