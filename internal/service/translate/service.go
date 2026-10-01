package translate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// R19 auto-translate engine.
//
// Provider selection (probed 2026-09-30 from the prod host):
//   - TRANSLATE_ENGINE_URL set → LibreTranslate-compatible HTTP API
//     (self-hosted LibreTranslate or a compatible gateway; supports
//     server-side auto-detect via /detect).
//   - otherwise → MyMemory free anonymous endpoint (no key). The public
//     https://libretranslate.com instance is NOT used as a default: it
//     answers 403 behind Cloudflare, while
//     https://api.mymemory.translated.net/get returns 200.
//
// No new tables or migrations: translations are cached externally (Valkey,
// in-memory TTL fallback) keyed by hash(text + source + target).

const (
	// MaxTextRunes caps a single request (task limit: 2000 characters).
	MaxTextRunes = 2000
	// CacheKeyPrefix namespaces translate keys in the shared Valkey.
	CacheKeyPrefix = "translate:v1:"
	// DefaultCacheTTL is the translation cache lifetime.
	DefaultCacheTTL = 24 * time.Hour
	// DefaultRateLimitPerMin is the per-user request budget. There is no
	// global rate-limit middleware in the codebase (only per-endpoint caps
	// like search limit<=100), so the service enforces its own in-memory
	// sliding window to protect the free upstream quota.
	DefaultRateLimitPerMin = 30
	// DefaultTimeout bounds every upstream call (matches the GIF service).
	DefaultTimeout = 8 * time.Second

	// DefaultLibreTranslateURL documents the public instance shape for
	// TRANSLATE_ENGINE_URL. It is intentionally NOT the default: probed
	// 2026-09-30 → HTTP 403 (Cloudflare), so the default engine is MyMemory.
	DefaultLibreTranslateURL = "https://libretranslate.com"
)

const (
	CodeInvalidArgument     = "invalid_argument"
	CodeUnsupportedLanguage = "unsupported_language"
	CodeDetectNotSupported  = "detect_not_supported"
	CodeRateLimited         = "rate_limited"
	CodeUpstream            = "upstream_failed"
	CodeInternal            = "internal"
)

// Error is the typed service error; the HTTP layer maps Code onto the
// stable error envelope (code, message, details, request_id).
type Error struct {
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return "translate: " + e.Code + ": " + e.Message }

// Language is one entry of GET /translate/languages.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Result is the POST /translate response payload.
type Result struct {
	Text   string `json:"text"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// staticLanguages is the verified fallback list (served when the provider
// has no language API, e.g. MyMemory) and the validation set for
// source/target codes.
var staticLanguages = []Language{
	{Code: "en", Name: "English"},
	{Code: "ru", Name: "Русский"},
	{Code: "uk", Name: "Українська"},
	{Code: "be", Name: "Беларуская"},
	{Code: "de", Name: "Deutsch"},
	{Code: "fr", Name: "Français"},
	{Code: "es", Name: "Español"},
	{Code: "it", Name: "Italiano"},
	{Code: "pt", Name: "Português"},
	{Code: "pl", Name: "Polski"},
	{Code: "nl", Name: "Nederlands"},
	{Code: "ro", Name: "Română"},
	{Code: "cs", Name: "Čeština"},
	{Code: "sv", Name: "Svenska"},
	{Code: "el", Name: "Ελληνικά"},
	{Code: "tr", Name: "Türkçe"},
	{Code: "kk", Name: "Қазақша"},
	{Code: "ar", Name: "العربية"},
	{Code: "zh", Name: "中文"},
	{Code: "ja", Name: "日本語"},
	{Code: "ko", Name: "한국어"},
	{Code: "hi", Name: "हिन्दी"},
}

var supportedSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(staticLanguages))
	for _, lang := range staticLanguages {
		set[lang.Code] = struct{}{}
	}
	return set
}()

// IsSupported reports whether code is a known translatable language.
func IsSupported(code string) bool {
	_, ok := supportedSet[NormalizeLang(code)]
	return ok
}

// NormalizeLang folds "en-US"/"en_US"/"EN" into the primary subtag "en".
func NormalizeLang(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "_", "-")
	if idx := strings.Index(value, "-"); idx >= 0 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

func copyLanguages(src []Language) []Language {
	out := make([]Language, len(src))
	copy(out, src)
	return out
}

// CacheKeyFor derives the cache key from hash(text + source + target).
// Normalizes the language codes so "EN|ru" and "en|RU" share an entry.
func CacheKeyFor(text, source, target string) string {
	sum := sha256.Sum256([]byte(
		strings.ToLower(strings.TrimSpace(source)) + "\x00" +
			strings.ToLower(strings.TrimSpace(target)) + "\x00" +
			text,
	))
	return CacheKeyPrefix + hex.EncodeToString(sum[:])
}

// cacheValue is the stored envelope: the detected source must survive the
// round-trip so auto-detect hits (alias key with empty source) still return
// a complete Result.
type cacheValue struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

// userLimiter is an in-memory per-user sliding-window limiter
// (limit requests per minute). It guards the free upstream quota; Valkey
// is deliberately not used so translate never depends on cache health.
type userLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newUserLimiter(limit int) *userLimiter {
	if limit <= 0 {
		limit = DefaultRateLimitPerMin
	}
	return &userLimiter{hits: make(map[string][]time.Time), limit: limit, window: time.Minute}
}

// allow reports whether userID may proceed and, when not, how long to wait.
func (l *userLimiter) allow(userID string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	kept := l.hits[userID][:0]
	for _, at := range l.hits[userID] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.limit {
		return false, kept[0].Add(l.window).Sub(now)
	}
	l.hits[userID] = append(kept, now)
	return true, 0
}

// Config wires the service. Zero values get sane defaults; tests inject
// Provider/Cache/HTTPClient doubles.
type Config struct {
	// EngineURL selects the LibreTranslate-compatible engine; empty means
	// the MyMemory free endpoint (no key needed).
	EngineURL string
	// APIKey is forwarded as api_key to the LibreTranslate-compatible engine.
	APIKey string
	// MyMemoryEmail is sent as &de= to raise the free MyMemory quota.
	MyMemoryEmail string
	Timeout       time.Duration
	CacheTTL      time.Duration
	// RateLimitPerMin is the per-user budget (default 30).
	RateLimitPerMin int
	HTTPClient      *http.Client
	Provider        Provider
	Cache           Cache
}

// Service is the translate use-case: validate → rate-limit → cache → engine.
type Service struct {
	provider Provider
	cache    Cache
	cacheTTL time.Duration
	limiter  *userLimiter
}

func New(cfg Config) (*Service, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	provider := cfg.Provider
	if provider == nil {
		if strings.TrimSpace(cfg.EngineURL) != "" {
			provider = NewLibreTranslateProvider(cfg.EngineURL, cfg.APIKey, client)
		} else {
			provider = NewMyMemoryProvider(cfg.MyMemoryEmail, client)
		}
	}
	cache := cfg.Cache
	if cache == nil {
		cache = NewMemoryCache()
	}
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Service{
		provider: provider,
		cache:    cache,
		cacheTTL: ttl,
		limiter:  newUserLimiter(cfg.RateLimitPerMin),
	}, nil
}

// ProviderName exposes the selected engine for logs/health ("mymemory" by
// default, "libretranslate" with TRANSLATE_ENGINE_URL).
func (s *Service) ProviderName() string {
	if s == nil || s.provider == nil {
		return ""
	}
	return s.provider.Name()
}

// Translate renders text into target, auto-detecting source through the
// engine API when source is empty and the engine supports it.
func (s *Service) Translate(ctx context.Context, userID, text, source, target string) (Result, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Result{}, &Error{Code: CodeInvalidArgument, Message: "text is required"}
	}
	if utf8.RuneCountInString(trimmed) > MaxTextRunes {
		return Result{}, &Error{Code: CodeInvalidArgument, Message: "text exceeds 2000 characters"}
	}
	dst := NormalizeLang(target)
	if dst == "" {
		return Result{}, &Error{Code: CodeInvalidArgument, Message: "target is required"}
	}
	if !IsSupported(dst) {
		return Result{}, &Error{Code: CodeUnsupportedLanguage, Message: "unsupported target language: " + dst}
	}
	src := NormalizeLang(source)
	if src != "" && !IsSupported(src) {
		return Result{}, &Error{Code: CodeUnsupportedLanguage, Message: "unsupported source language: " + src}
	}
	if src != "" && src == dst {
		return Result{Text: trimmed, Source: src, Target: dst}, nil
	}

	bucket := strings.TrimSpace(userID)
	if bucket == "" {
		bucket = "anonymous"
	}
	if ok, retryAfter := s.limiter.allow(bucket, time.Now()); !ok {
		return Result{}, &Error{Code: CodeRateLimited, Message: "translation rate limit exceeded", RetryAfter: retryAfter}
	}

	lookupKey := CacheKeyFor(trimmed, src, dst)
	if value, found, err := s.cache.Get(ctx, lookupKey); err == nil && found {
		var decoded cacheValue
		if json.Unmarshal([]byte(value), &decoded) == nil && strings.TrimSpace(decoded.Text) != "" {
			resolved := NormalizeLang(decoded.Source)
			if resolved == "" {
				resolved = src
			}
			return Result{Text: decoded.Text, Source: resolved, Target: dst}, nil
		}
	}

	if err := ctx.Err(); err != nil {
		return Result{}, &Error{Code: CodeInternal, Message: err.Error()}
	}
	translated, detected, err := s.provider.Translate(ctx, trimmed, src, dst)
	if err != nil {
		return Result{}, err
	}
	detected = NormalizeLang(detected)
	if detected == "" {
		detected = src
	}
	stored, _ := json.Marshal(cacheValue{Source: detected, Text: translated})
	_ = s.cache.Set(ctx, CacheKeyFor(trimmed, detected, dst), string(stored), s.cacheTTL)
	if src == "" {
		// Alias under the empty-source key so the next auto-detect request
		// for the same text+target is served from cache.
		_ = s.cache.Set(ctx, lookupKey, string(stored), s.cacheTTL)
	}
	return Result{Text: translated, Source: detected, Target: dst}, nil
}

// Languages returns the sorted picker list: live from the engine when it
// has a language API, otherwise the verified static list.
func (s *Service) Languages(ctx context.Context) ([]Language, error) {
	items, err := s.provider.Languages(ctx)
	if err != nil {
		return nil, err
	}
	out := copyLanguages(items)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}
