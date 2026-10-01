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
	defaultEmailCodeTTL    = 10 * time.Minute
	defaultCallsTTL        = 12 * time.Hour
	defaultCallsMeshLimit  = 2
	defaultCallsMaxMembers = 200
)

type Config struct {
	App        AppConfig
	Auth       AuthConfig
	Bot        BotConfig
	Postgres   PostgresConfig
	Valkey     ValkeyConfig
	MinIO      MinIOConfig
	Migrations MigrationsConfig
	Calls      CallsConfig
	Translate  TranslateConfig
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
	EmailVerify   AuthEmailVerifyConfig
}

type AuthEmailVerifyConfig struct {
	Enabled      bool
	ResendAPIKey string
	ResendFrom   string
	ResendBase   string
	CodeTTL      time.Duration
	MaxAttempts  int
}

type ValkeyConfig struct {
	Addr     string
	Password string
	DB       int
}

type MinIOConfig struct {
	APIInternal  string
	PublicBase   string
	Bucket       string
	RootUser     string
	RootPassword string
	Secure       bool
	Region       string
	SSEMode      string
}

type MigrationsConfig struct {
	Enabled bool
	Path    string
}

// CallsConfig drives the WebRTC/signaling subsystem (internal/calls).
type CallsConfig struct {
	Enabled           bool
	STUNURLs          []string
	TURNURLs          []string
	TURNSharedSecret  string
	TURNCredentialTTL time.Duration
	MeshLimit         int
	MaxParticipants   int
	ICEPortMin        int
	ICEPortMax        int
	AllowLoopback     bool
}

type BotConfig struct {
	TokenPepper string
}

// TranslateConfig drives the R19 auto-translate engine. Empty EngineURL
// selects the MyMemory free endpoint (no key); a LibreTranslate-compatible
// base URL enables server-side auto-detect of the source language.
type TranslateConfig struct {
	EngineURL       string
	APIKey          string
	MyMemoryEmail   string
	Timeout         time.Duration
	CacheTTL        time.Duration
	RateLimitPerMin int
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
			EmailVerify: AuthEmailVerifyConfig{
				Enabled:      getBoolEnv("AUTH_EMAIL_VERIFY_ENABLED", false),
				ResendAPIKey: strings.TrimSpace(os.Getenv("RESEND_API_KEY")),
				ResendFrom:   strings.TrimSpace(os.Getenv("RESEND_FROM")),
				ResendBase:   strings.TrimSpace(os.Getenv("RESEND_BASE_URL")),
				CodeTTL:      getDurationEnv("AUTH_EMAIL_CODE_TTL", defaultEmailCodeTTL),
				MaxAttempts:  getIntEnv("AUTH_EMAIL_CODE_MAX_ATTEMPTS", 5),
			},
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
			PublicBase:   strings.TrimSpace(os.Getenv("MINIO_PUBLIC_BASE_URL")),
			Bucket:       strings.TrimSpace(os.Getenv("MINIO_BUCKET")),
			RootUser:     strings.TrimSpace(os.Getenv("MINIO_ROOT_USER")),
			RootPassword: strings.TrimSpace(os.Getenv("MINIO_ROOT_PASSWORD")),
			Secure:       getBoolEnv("MINIO_SECURE", false),
			Region:       getEnv("MINIO_REGION", "us-east-1"),
			SSEMode:      strings.ToLower(getEnv("MINIO_SSE_MODE", "s3")),
		},
		Migrations: MigrationsConfig{
			Enabled: getBoolEnv("MIGRATIONS_ENABLED", true),
			Path:    getEnv("MIGRATIONS_PATH", defaultMigrationsPath),
		},
		Calls: CallsConfig{
			Enabled:           getBoolEnv("CALLS_ENABLED", true),
			STUNURLs:          splitCSV(os.Getenv("CALLS_STUN_URLS")),
			TURNURLs:          splitCSV(os.Getenv("CALLS_TURN_URLS")),
			TURNSharedSecret:  strings.TrimSpace(os.Getenv("CALLS_TURN_SECRET")),
			TURNCredentialTTL: getDurationEnv("CALLS_TURN_CREDENTIAL_TTL", defaultCallsTTL),
			MeshLimit:         getIntEnv("CALLS_MESH_LIMIT", defaultCallsMeshLimit),
			MaxParticipants:   getIntEnv("CALLS_MAX_PARTICIPANTS", defaultCallsMaxMembers),
			ICEPortMin:        getIntEnv("CALLS_ICE_PORT_MIN", 0),
			ICEPortMax:        getIntEnv("CALLS_ICE_PORT_MAX", 0),
			AllowLoopback:     getBoolEnv("CALLS_ALLOW_LOOPBACK", false),
		},
		Translate: TranslateConfig{
			EngineURL:       strings.TrimSpace(os.Getenv("TRANSLATE_ENGINE_URL")),
			APIKey:          strings.TrimSpace(os.Getenv("TRANSLATE_API_KEY")),
			MyMemoryEmail:   strings.TrimSpace(os.Getenv("TRANSLATE_MYMEMORY_EMAIL")),
			Timeout:         getDurationEnv("TRANSLATE_TIMEOUT", 8*time.Second),
			CacheTTL:        getDurationEnv("TRANSLATE_CACHE_TTL", 24*time.Hour),
			RateLimitPerMin: getIntEnv("TRANSLATE_RATE_LIMIT_PER_MIN", 30),
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
	if c.Auth.EmailVerify.Enabled {
		if strings.TrimSpace(c.Auth.EmailVerify.ResendAPIKey) == "" {
			return errors.New("RESEND_API_KEY is required when AUTH_EMAIL_VERIFY_ENABLED=true")
		}
		if strings.TrimSpace(c.Auth.EmailVerify.ResendFrom) == "" {
			return errors.New("RESEND_FROM is required when AUTH_EMAIL_VERIFY_ENABLED=true")
		}
		if c.Auth.EmailVerify.CodeTTL <= 0 {
			return errors.New("AUTH_EMAIL_CODE_TTL must be positive")
		}
		if c.Auth.EmailVerify.MaxAttempts <= 0 {
			return errors.New("AUTH_EMAIL_CODE_MAX_ATTEMPTS must be positive")
		}
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
	if strings.EqualFold(strings.TrimSpace(c.MinIO.RootUser), "minioadmin") &&
		strings.TrimSpace(c.MinIO.RootPassword) == "minioadmin123" {
		return errors.New("default MinIO root credentials are forbidden; set MINIO_ROOT_USER and MINIO_ROOT_PASSWORD")
	}
	switch strings.TrimSpace(strings.ToLower(c.MinIO.SSEMode)) {
	case "", "s3":
	default:
		return errors.New("MINIO_SSE_MODE must be: s3")
	}

	if strings.TrimSpace(c.Bot.TokenPepper) == "" {
		return errors.New("BOT_TOKEN_PEPPER is required")
	}
	if c.Calls.Enabled {
		if len(c.Calls.TURNURLs) > 0 && c.Calls.TURNSharedSecret == "" {
			return errors.New("CALLS_TURN_SECRET is required when CALLS_TURN_URLS is set")
		}
		if c.Calls.MeshLimit < 1 {
			return errors.New("CALLS_MESH_LIMIT must be positive")
		}
		if c.Calls.MaxParticipants < 2 {
			return errors.New("CALLS_MAX_PARTICIPANTS must be at least 2")
		}
		if (c.Calls.ICEPortMin == 0) != (c.Calls.ICEPortMax == 0) {
			return errors.New("CALLS_ICE_PORT_MIN and CALLS_ICE_PORT_MAX must be set together")
		}
		if c.Calls.ICEPortMin > 0 && c.Calls.ICEPortMin > c.Calls.ICEPortMax {
			return errors.New("CALLS_ICE_PORT_MIN must not exceed CALLS_ICE_PORT_MAX")
		}
	}
	if c.Translate.Timeout <= 0 {
		return errors.New("TRANSLATE_TIMEOUT must be positive")
	}
	if c.Translate.CacheTTL <= 0 {
		return errors.New("TRANSLATE_CACHE_TTL must be positive")
	}
	if c.Translate.RateLimitPerMin <= 0 {
		return errors.New("TRANSLATE_RATE_LIMIT_PER_MIN must be positive")
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

// splitCSV turns a comma separated environment value into a trimmed list.
func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
