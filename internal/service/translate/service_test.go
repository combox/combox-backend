package translate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeLang(t *testing.T) {
	for raw, want := range map[string]string{
		"en":    "en",
		"EN":    "en",
		"en-US": "en",
		"en_US": "en",
		" RU ":  "ru",
		"pt-BR": "pt",
		"":      "",
	} {
		if got := NormalizeLang(raw); got != want {
			t.Fatalf("NormalizeLang(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestCacheKeyFor(t *testing.T) {
	a := CacheKeyFor("Hello", "en", "ru")
	b := CacheKeyFor("Hello", "en", "ru")
	if a != b {
		t.Fatalf("cache key is not deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, CacheKeyPrefix) {
		t.Fatalf("cache key misses prefix: %q", a)
	}
	for _, other := range []string{
		CacheKeyFor("Hello!", "en", "ru"),
		CacheKeyFor("Hello", "de", "ru"),
		CacheKeyFor("Hello", "en", "de"),
		CacheKeyFor("Hello", "", "ru"),
	} {
		if other == a {
			t.Fatalf("cache key collision: %q", other)
		}
	}
	// Case-insensitive language part: EN|ru and en|RU share the entry.
	if got := CacheKeyFor("Hello", "EN", "RU"); got != a {
		t.Fatalf("expected normalized key %q, got %q", a, got)
	}
}

func TestParseMyMemoryPayloadOK(t *testing.T) {
	body := `{"responseData":{"translatedText":"Здравствуйте","match":0.99},"quotaFinished":false,"responseDetails":"","responseStatus":200}`
	got, err := parseMyMemoryPayload([]byte(body), "en", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Здравствуйте" {
		t.Fatalf("unexpected translation: %q", got)
	}
}

func TestParseMyMemoryPayloadEscapes(t *testing.T) {
	// Live wire shape: translatedText arrives \u-escaped; encoding/json
	// must decode it to readable text.
	body := `{"responseData":{"translatedText":"\u0417\u0434\u0440\u0430\u0432\u0441\u0442\u0432\u0443\u0439\u0442\u0435"},"quotaFinished":false,"responseDetails":"","responseStatus":200}`
	got, err := parseMyMemoryPayload([]byte(body), "en", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Здравствуйте" {
		t.Fatalf("unexpected translation: %q", got)
	}
}

func TestParseMyMemoryPayloadErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "invalid pair",
			body: `{"responseData":{"translatedText":"INVALID LANGUAGE PAIR SPECIFIED. EXAMPLE: LANGPAIR=EN|IT"},"responseDetails":"'AUTO' IS AN INVALID SOURCE LANGUAGE","responseStatus":403}`,
			code: CodeUnsupportedLanguage,
		},
		{
			name: "quota finished",
			body: `{"responseData":{"translatedText":""},"quotaFinished":true,"responseDetails":"","responseStatus":200}`,
			code: CodeUpstream,
		},
		{
			name: "too many requests",
			body: `{"responseData":{"translatedText":""},"quotaFinished":false,"responseDetails":"","responseStatus":429}`,
			code: CodeUpstream,
		},
		{
			name: "query length",
			body: `{"responseData":{"translatedText":"QUERY LENGTH LIMIT EXCEEDED"},"quotaFinished":false,"responseDetails":"QUERY LENGTH LIMIT EXCEEDED","responseStatus":403}`,
			code: CodeInvalidArgument,
		},
		{
			name: "garbage",
			body: `not json`,
			code: CodeUpstream,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseMyMemoryPayload([]byte(tc.body), "en", "ru")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var svcErr *Error
			if !errors.As(err, &svcErr) {
				t.Fatalf("expected *Error, got %T", err)
			}
			if svcErr.Code != tc.code {
				t.Fatalf("expected code %q, got %q (%s)", tc.code, svcErr.Code, svcErr.Message)
			}
		})
	}
}

func TestLibreDetectedLanguageShapes(t *testing.T) {
	if got := libreDetectedLanguage(map[string]any{"language": "EN", "confidence": 0.9}); got != "en" {
		t.Fatalf("object shape: got %q", got)
	}
	if got := libreDetectedLanguage("fr"); got != "fr" {
		t.Fatalf("string shape: got %q", got)
	}
	if got := libreDetectedLanguage(42); got != "" {
		t.Fatalf("unexpected shape: got %q", got)
	}
}

// stubProvider is the engine double for service-level tests.
type stubProvider struct {
	name      string
	auto      bool
	translate func(ctx context.Context, text, source, target string) (string, string, error)
	languages []Language
	calls     int
}

func (s *stubProvider) Name() string             { return s.name }
func (s *stubProvider) SupportsAutoDetect() bool { return s.auto }
func (s *stubProvider) Translate(ctx context.Context, text, source, target string) (string, string, error) {
	s.calls++
	return s.translate(ctx, text, source, target)
}
func (s *stubProvider) Languages(_ context.Context) ([]Language, error) {
	return copyLanguages(s.languages), nil
}

func newTestService(t *testing.T, provider Provider, rateLimit int) *Service {
	t.Helper()
	svc, err := New(Config{Provider: provider, Cache: NewMemoryCache(), RateLimitPerMin: rateLimit})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return svc
}

func TestTranslateValidation(t *testing.T) {
	svc := newTestService(t, &stubProvider{
		name: "stub",
		translate: func(_ context.Context, text, source, target string) (string, string, error) {
			return "x", source, nil
		},
	}, 100)
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		text   string
		source string
		target string
		code   string
	}{
		{"empty text", "  ", "en", "ru", CodeInvalidArgument},
		{"missing target", "hi", "en", "", CodeInvalidArgument},
		{"bad target", "hi", "en", "xx", CodeUnsupportedLanguage},
		{"bad source", "hi", "xx", "ru", CodeUnsupportedLanguage},
		{"too long", strings.Repeat("a", MaxTextRunes+1), "en", "ru", CodeInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Translate(ctx, "user-1", tc.text, tc.source, tc.target)
			var svcErr *Error
			if !errors.As(err, &svcErr) || svcErr.Code != tc.code {
				t.Fatalf("expected code %q, got %v", tc.code, err)
			}
		})
	}
}

func TestTranslateSameLanguageShortCircuit(t *testing.T) {
	stub := &stubProvider{name: "stub", translate: func(_ context.Context, text, source, target string) (string, string, error) {
		return "SHOULD NOT BE CALLED", source, nil
	}}
	svc := newTestService(t, stub, 100)
	got, err := svc.Translate(context.Background(), "user-1", "Привет", "ru", "ru-RU")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Text != "Привет" || got.Source != "ru" || got.Target != "ru" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if stub.calls != 0 {
		t.Fatalf("provider must not be called for identical languages")
	}
}

func TestTranslateCachesByKey(t *testing.T) {
	stub := &stubProvider{name: "stub", translate: func(_ context.Context, text, source, target string) (string, string, error) {
		return "Здравствуйте", source, nil
	}}
	svc := newTestService(t, stub, 100)
	ctx := context.Background()

	first, err := svc.Translate(ctx, "user-1", "Hello", "en", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := svc.Translate(ctx, "user-1", "Hello", "EN", "RU")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Fatalf("cache mismatch: %+v vs %+v", first, second)
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 provider call, got %d", stub.calls)
	}
}

func TestTranslateAutoDetectAliasCached(t *testing.T) {
	stub := &stubProvider{name: "stub", auto: true, translate: func(_ context.Context, text, source, target string) (string, string, error) {
		if source != "" {
			t.Errorf("expected empty source for auto, got %q", source)
		}
		return "Здравствуйте", "fr", nil
	}}
	svc := newTestService(t, stub, 100)
	ctx := context.Background()

	first, err := svc.Translate(ctx, "user-1", "Bonjour", "", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.Source != "fr" || first.Text != "Здравствуйте" || first.Target != "ru" {
		t.Fatalf("unexpected result: %+v", first)
	}
	second, err := svc.Translate(ctx, "user-1", "Bonjour", "", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second != first {
		t.Fatalf("alias cache mismatch: %+v vs %+v", first, second)
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 provider call, got %d", stub.calls)
	}
}

func TestTranslateRateLimit(t *testing.T) {
	stub := &stubProvider{name: "stub", translate: func(_ context.Context, text, source, target string) (string, string, error) {
		return text, source, nil
	}}
	svc := newTestService(t, stub, 2)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := svc.Translate(ctx, "user-1", "msg", "en", "ru"); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
	_, err := svc.Translate(ctx, "user-1", "other", "en", "ru")
	var svcErr *Error
	if !errors.As(err, &svcErr) || svcErr.Code != CodeRateLimited {
		t.Fatalf("expected rate_limited, got %v", err)
	}
	if svcErr.RetryAfter <= 0 {
		t.Fatalf("expected positive retry-after, got %v", svcErr.RetryAfter)
	}
	// Another user is unaffected.
	if _, err := svc.Translate(ctx, "user-2", "other", "en", "ru"); err != nil {
		t.Fatalf("other user must not be limited: %v", err)
	}
}

func TestTranslateUpstreamErrorPassThrough(t *testing.T) {
	stub := &stubProvider{name: "stub", translate: func(_ context.Context, text, source, target string) (string, string, error) {
		return "", "", &Error{Code: CodeUpstream, Message: "boom"}
	}}
	svc := newTestService(t, stub, 100)
	_, err := svc.Translate(context.Background(), "user-1", "Hello", "en", "ru")
	var svcErr *Error
	if !errors.As(err, &svcErr) || svcErr.Code != CodeUpstream {
		t.Fatalf("expected upstream_failed, got %v", err)
	}
}

func TestLanguagesSorted(t *testing.T) {
	svc := newTestService(t, &stubProvider{
		name:      "stub",
		languages: []Language{{Code: "ru", Name: "Русский"}, {Code: "en", Name: "English"}},
		translate: func(_ context.Context, text, source, target string) (string, string, error) {
			return text, source, nil
		},
	}, 100)
	items, err := svc.Languages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 || items[0].Code != "en" || items[1].Code != "ru" {
		t.Fatalf("expected sorted [en ru], got %+v", items)
	}
}

func TestStaticLanguagesCoverMinimum(t *testing.T) {
	for _, want := range []string{"en", "ru"} {
		if !IsSupported(want) {
			t.Fatalf("static list must contain %q", want)
		}
	}
	if len(staticLanguages) < 10 {
		t.Fatalf("static list too small: %d", len(staticLanguages))
	}
}

func TestMemoryCacheExpiry(t *testing.T) {
	cache := NewMemoryCache()
	ctx := context.Background()
	if err := cache.Set(ctx, "k", "v", 20*time.Millisecond); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if value, found, _ := cache.Get(ctx, "k"); !found || value != "v" {
		t.Fatalf("expected hit, got %q %v", value, found)
	}
	time.Sleep(50 * time.Millisecond)
	if _, found, _ := cache.Get(ctx, "k"); found {
		t.Fatalf("expected expiry")
	}
}

func TestNewSelectsMyMemoryByDefault(t *testing.T) {
	svc, err := New(Config{})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if svc.ProviderName() != "mymemory" {
		t.Fatalf("expected default mymemory provider, got %q", svc.ProviderName())
	}
}

func TestNewSelectsLibreWhenEngineURLSet(t *testing.T) {
	svc, err := New(Config{EngineURL: "https://translate.example.com"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if svc.ProviderName() != "libretranslate" {
		t.Fatalf("expected libretranslate provider, got %q", svc.ProviderName())
	}
}
