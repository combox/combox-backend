package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"combox-backend/internal/config"
	"combox-backend/internal/i18n"
	"combox-backend/internal/observability"
	pgrepo "combox-backend/internal/repository/postgres"
	vkrepo "combox-backend/internal/repository/valkey"
	authsvc "combox-backend/internal/service/auth"
	chatsvc "combox-backend/internal/service/chat"
	e2esvc "combox-backend/internal/service/e2e"
	httptransport "combox-backend/internal/transport/http"
)

type chatPublisherAdapter struct{ p *vkrepo.EventPublisher }

func (a chatPublisherAdapter) PublishDeviceMessageCreated(ctx context.Context, ev chatsvc.DeviceMessageCreatedEvent) error {
	return a.p.PublishDeviceMessageCreated(ctx, vkrepo.DeviceMessageCreatedEvent{
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
}

func (a chatPublisherAdapter) PublishUserMessageCreated(ctx context.Context, ev chatsvc.UserMessageCreatedEvent) error {
	return a.p.PublishUserMessageCreated(ctx, vkrepo.UserMessageCreatedEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		SenderUserID:    ev.SenderUserID,
		RecipientUserID: ev.RecipientUserID,
		CreatedAt:       ev.CreatedAt,
	})
}

func (a chatPublisherAdapter) PublishMessageStatus(ctx context.Context, ev chatsvc.MessageStatusEvent) error {
	return a.p.PublishMessageStatus(ctx, vkrepo.MessageStatusEvent{
		MessageID: ev.MessageID,
		ChatID:    ev.ChatID,
		UserID:    ev.UserID,
		Status:    ev.Status,
		At:        ev.At,
	})
}

func (a chatPublisherAdapter) PublishMessageUpdated(ctx context.Context, ev chatsvc.MessageUpdatedEvent) error {
	return a.p.PublishMessageUpdated(ctx, vkrepo.MessageUpdatedEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		EditorUserID:    ev.EditorUserID,
		RecipientUserID: ev.RecipientUserID,
		Content:         ev.Content,
		EditedAt:        ev.EditedAt,
	})
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

	authService, err := authsvc.New(authsvc.Config{
		Users:         pgrepo.NewAuthUserRepository(postgresClient),
		Sessions:      pgrepo.NewAuthSessionRepository(postgresClient),
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

	chatPublisher := &chatPublisherAdapter{p: publisher}
	chatSvc, err := chatsvc.NewWithPublisher(chatRepo, msgRepo, chatPublisher)
	if err != nil {
		return fmt.Errorf("init chat service: %w", err)
	}

	e2eService, err := e2esvc.New(pgrepo.NewE2ERepository(postgresClient))
	if err != nil {
		return fmt.Errorf("init e2e service: %w", err)
	}

	router := httptransport.NewRouter(httptransport.RouterDeps{
		Logger:        logger,
		Postgres:      postgresClient,
		Valkey:        valkeyClient,
		ReadyTimeout:  cfg.App.ReadyTimeout,
		I18n:          catalog,
		DefaultLocale: cfg.App.DefaultLocale,
		AccessSecret:  cfg.Auth.AccessSecret,
		Auth:          authService,
		Chat:          chatSvc,
		E2E:           e2eService,
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
