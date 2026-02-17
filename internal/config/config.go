package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddress     = ":8080"
	defaultReadTimeout     = 10 * time.Second
	defaultWriteTimeout    = 15 * time.Second
	defaultShutdownTimeout = 15 * time.Second
	defaultReadyTimeout    = 2 * time.Second
	defaultLocale          = "en"
	defaultStringsPath     = "strings"
	defaultMigrationsPath  = "migrations"
	defaultAccessTTL       = 15 * time.Minute
	defaultRefreshTTL      = 24 * time.Hour * 30
)

type Config struct {
	App        AppConfig
	Auth       AuthConfig
	Bot        BotConfig
	Postgres   PostgresConfig
	Valkey     ValkeyConfig
	MinIO      MinIOConfig
	Migrations MigrationsConfig
}

type AppConfig struct {
	Env             string
	HTTPAddress     string
	TLSEnabled      bool
	TLSCertFile     string
	TLSKeyFile      string
	TLSClientCAFile string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	ReadyTimeout    time.Duration
	DefaultLocale   string
	StringsPath     string
}

type PostgresConfig struct {
	DSN string
}

type AuthConfig struct {
	AccessSecret  string
	RefreshSecret string
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
}

type ValkeyConfig struct {
	Addr     string
	Password string
	DB       int
}

type MinIOConfig struct {
	APIInternal  string
	Bucket       string
	RootUser     string
	RootPassword string
	Secure       bool
	Region       string
}

type MigrationsConfig struct {
	Enabled bool
	Path    string
}

type BotConfig struct {
	TokenPepper string
}

func Load() (Config, error) {
	cfg := Config{
		App: AppConfig{
			Env:             getEnv("APP_ENV", "local"),
			HTTPAddress:     getEnv("HTTP_ADDRESS", defaultHTTPAddress),
			TLSEnabled:      getBoolEnv("TLS_ENABLED", false),
			TLSCertFile:     strings.TrimSpace(os.Getenv("TLS_CERT_FILE")),
			TLSKeyFile:      strings.TrimSpace(os.Getenv("TLS_KEY_FILE")),
			TLSClientCAFile: strings.TrimSpace(os.Getenv("TLS_CLIENT_CA_FILE")),
			ReadTimeout:     getDurationEnv("HTTP_READ_TIMEOUT", defaultReadTimeout),
			WriteTimeout:    getDurationEnv("HTTP_WRITE_TIMEOUT", defaultWriteTimeout),
			ShutdownTimeout: getDurationEnv("HTTP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout),
			ReadyTimeout:    getDurationEnv("READY_TIMEOUT", defaultReadyTimeout),
			DefaultLocale:   getEnv("DEFAULT_LOCALE", defaultLocale),
			StringsPath:     getEnv("STRINGS_PATH", defaultStringsPath),
		},
		Auth: AuthConfig{
			AccessSecret:  strings.TrimSpace(os.Getenv("AUTH_ACCESS_SECRET")),
			RefreshSecret: strings.TrimSpace(os.Getenv("AUTH_REFRESH_SECRET")),
			AccessTTL:     getDurationEnv("AUTH_ACCESS_TTL", defaultAccessTTL),
			RefreshTTL:    getDurationEnv("AUTH_REFRESH_TTL", defaultRefreshTTL),
		},
		Bot: BotConfig{
			TokenPepper: strings.TrimSpace(os.Getenv("BOT_TOKEN_PEPPER")),
		},
		Postgres: PostgresConfig{
			DSN: strings.TrimSpace(os.Getenv("POSTGRES_DSN")),
		},
		Valkey: ValkeyConfig{
			Addr:     getEnv("VALKEY_ADDR", "127.0.0.1:6379"),
			Password: os.Getenv("VALKEY_PASSWORD"),
			DB:       getIntEnv("VALKEY_DB", 0),
		},
		MinIO: MinIOConfig{
			APIInternal:  strings.TrimSpace(os.Getenv("MINIO_API_INTERNAL")),
			Bucket:       strings.TrimSpace(os.Getenv("MINIO_BUCKET")),
			RootUser:     strings.TrimSpace(os.Getenv("MINIO_ROOT_USER")),
			RootPassword: strings.TrimSpace(os.Getenv("MINIO_ROOT_PASSWORD")),
			Secure:       getBoolEnv("MINIO_SECURE", false),
			Region:       getEnv("MINIO_REGION", "us-east-1"),
		},
		Migrations: MigrationsConfig{
			Enabled: getBoolEnv("MIGRATIONS_ENABLED", true),
			Path:    getEnv("MIGRATIONS_PATH", defaultMigrationsPath),
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.Postgres.DSN == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	if strings.TrimSpace(c.Valkey.Addr) == "" {
		return errors.New("VALKEY_ADDR is required")
	}
	if c.App.HTTPAddress == "" {
		return errors.New("HTTP_ADDRESS is required")
	}
	if c.App.ReadyTimeout <= 0 {
		return fmt.Errorf("READY_TIMEOUT must be positive")
	}
	if strings.TrimSpace(c.App.DefaultLocale) == "" {
		return errors.New("DEFAULT_LOCALE is required")
	}
	if strings.TrimSpace(c.App.StringsPath) == "" {
		return errors.New("STRINGS_PATH is required")
	}
	if c.App.TLSEnabled {
		if strings.TrimSpace(c.App.TLSCertFile) == "" {
			return errors.New("TLS_CERT_FILE is required when TLS_ENABLED=true")
		}
		if strings.TrimSpace(c.App.TLSKeyFile) == "" {
			return errors.New("TLS_KEY_FILE is required when TLS_ENABLED=true")
		}
		if strings.TrimSpace(c.App.TLSClientCAFile) == "" {
			return errors.New("TLS_CLIENT_CA_FILE is required when TLS_ENABLED=true")
		}
	}
	if c.Auth.AccessSecret == "" {
		return errors.New("AUTH_ACCESS_SECRET is required")
	}
	if c.Auth.RefreshSecret == "" {
		return errors.New("AUTH_REFRESH_SECRET is required")
	}
	if c.Auth.AccessTTL <= 0 {
		return errors.New("AUTH_ACCESS_TTL must be positive")
	}
	if c.Auth.RefreshTTL <= 0 {
		return errors.New("AUTH_REFRESH_TTL must be positive")
	}
	if strings.TrimSpace(c.MinIO.APIInternal) == "" {
		return errors.New("MINIO_API_INTERNAL is required")
	}
	if strings.TrimSpace(c.MinIO.Bucket) == "" {
		return errors.New("MINIO_BUCKET is required")
	}
	if strings.TrimSpace(c.MinIO.RootUser) == "" {
		return errors.New("MINIO_ROOT_USER is required")
	}
	if strings.TrimSpace(c.MinIO.RootPassword) == "" {
		return errors.New("MINIO_ROOT_PASSWORD is required")
	}

	if strings.TrimSpace(c.Bot.TokenPepper) == "" {
		return errors.New("BOT_TOKEN_PEPPER is required")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getIntEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
