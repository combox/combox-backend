package config

import (
	"testing"
)

func TestLoadSuccess(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
	t.Setenv("DEFAULT_LOCALE", "en")
	t.Setenv("STRINGS_PATH", "strings")
	t.Setenv("AUTH_ACCESS_SECRET", "access-secret")
	t.Setenv("AUTH_REFRESH_SECRET", "refresh-secret")
	t.Setenv("MINIO_API_INTERNAL", "http://minio:9000")
	t.Setenv("MINIO_BUCKET", "chat-media")
	t.Setenv("MINIO_ROOT_USER", "combox_admin")
	t.Setenv("MINIO_ROOT_PASSWORD", "combox_admin_change_me_123")
	t.Setenv("BOT_TOKEN_PEPPER", "bot-token-pepper-32-chars-min")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Postgres.DSN == "" {
		t.Fatalf("expected postgres dsn to be set")
	}
	if cfg.Valkey.Addr != "127.0.0.1:6379" {
		t.Fatalf("unexpected valkey addr: %s", cfg.Valkey.Addr)
	}
	if cfg.Bot.TokenPepper == "" {
		t.Fatalf("expected bot token pepper to be set")
	}
}

func TestLoadMissingPostgresDSN(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "")
	t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
	t.Setenv("DEFAULT_LOCALE", "en")
	t.Setenv("STRINGS_PATH", "strings")
	t.Setenv("AUTH_ACCESS_SECRET", "access-secret")
	t.Setenv("AUTH_REFRESH_SECRET", "refresh-secret")
	t.Setenv("MINIO_API_INTERNAL", "http://minio:9000")
	t.Setenv("MINIO_BUCKET", "chat-media")
	t.Setenv("MINIO_ROOT_USER", "combox_admin")
	t.Setenv("MINIO_ROOT_PASSWORD", "combox_admin_change_me_123")
	t.Setenv("BOT_TOKEN_PEPPER", "bot-token-pepper-32-chars-min")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for missing POSTGRES_DSN")
	}
}

func TestLoadInvalidReadyTimeoutFallsBack(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
	t.Setenv("DEFAULT_LOCALE", "en")
	t.Setenv("STRINGS_PATH", "strings")
	t.Setenv("READY_TIMEOUT", "invalid")
	t.Setenv("AUTH_ACCESS_SECRET", "access-secret")
	t.Setenv("AUTH_REFRESH_SECRET", "refresh-secret")
	t.Setenv("MINIO_API_INTERNAL", "http://minio:9000")
	t.Setenv("MINIO_BUCKET", "chat-media")
	t.Setenv("MINIO_ROOT_USER", "combox_admin")
	t.Setenv("MINIO_ROOT_PASSWORD", "combox_admin_change_me_123")
	t.Setenv("BOT_TOKEN_PEPPER", "bot-token-pepper-32-chars-min")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.App.ReadyTimeout.String() != "2s" {
		t.Fatalf("expected fallback timeout 2s, got %s", cfg.App.ReadyTimeout)
	}
}

func TestLoadMissingAuthSecrets(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
	t.Setenv("DEFAULT_LOCALE", "en")
	t.Setenv("STRINGS_PATH", "strings")
	t.Setenv("AUTH_ACCESS_SECRET", "")
	t.Setenv("AUTH_REFRESH_SECRET", "")
	t.Setenv("MINIO_API_INTERNAL", "http://minio:9000")
	t.Setenv("MINIO_BUCKET", "chat-media")
	t.Setenv("MINIO_ROOT_USER", "combox_admin")
	t.Setenv("MINIO_ROOT_PASSWORD", "combox_admin_change_me_123")
	t.Setenv("BOT_TOKEN_PEPPER", "bot-token-pepper-32-chars-min")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for missing auth secrets")
	}
}

func TestLoadMissingBotTokenPepper(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("VALKEY_ADDR", "127.0.0.1:6379")
	t.Setenv("DEFAULT_LOCALE", "en")
	t.Setenv("STRINGS_PATH", "strings")
	t.Setenv("AUTH_ACCESS_SECRET", "access-secret")
	t.Setenv("AUTH_REFRESH_SECRET", "refresh-secret")
	t.Setenv("MINIO_API_INTERNAL", "http://minio:9000")
	t.Setenv("MINIO_BUCKET", "chat-media")
	t.Setenv("MINIO_ROOT_USER", "combox_admin")
	t.Setenv("MINIO_ROOT_PASSWORD", "combox_admin_change_me_123")
	t.Setenv("BOT_TOKEN_PEPPER", "")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for missing BOT_TOKEN_PEPPER")
	}
}
