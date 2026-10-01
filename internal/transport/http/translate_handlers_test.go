package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	translatesvc "combox-backend/internal/service/translate"
)

type translateStub struct {
	result    translatesvc.Result
	languages []translatesvc.Language
	err       error
	gotUserID string
	gotText   string
	gotSource string
	gotTarget string
}

func (s *translateStub) Translate(_ context.Context, userID, text, source, target string) (translatesvc.Result, error) {
	s.gotUserID, s.gotText, s.gotSource, s.gotTarget = userID, text, source, target
	if s.err != nil {
		return translatesvc.Result{}, s.err
	}
	return s.result, nil
}

func (s *translateStub) Languages(_ context.Context) ([]translatesvc.Language, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.languages, nil
}

func newTranslateRouter(svc TranslateService) stdhttp.Handler {
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Translate:     svc,
	})
}

func doTranslateRequest(t *testing.T, router stdhttp.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *stdhttp.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+makeAccessToken(t, "u-test", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix()))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	payload := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &payload)
	return rr.Code, payload
}

func TestTranslateRouteContract(t *testing.T) {
	svc := &translateStub{result: translatesvc.Result{Text: "Здравствуйте", Source: "en", Target: "ru"}}
	status, payload := doTranslateRequest(t, newTranslateRouter(svc), stdhttp.MethodPost, "/api/private/v1/translate", `{"text":"Hello","target":"ru","source":"en"}`)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if payload["text"] != "Здравствуйте" || payload["source"] != "en" || payload["target"] != "ru" {
		t.Fatalf("payload = %v", payload)
	}
	if svc.gotUserID != "u-test" || svc.gotText != "Hello" || svc.gotSource != "en" || svc.gotTarget != "ru" {
		t.Fatalf("service got user=%q text=%q source=%q target=%q", svc.gotUserID, svc.gotText, svc.gotSource, svc.gotTarget)
	}
}

func TestTranslateRouteErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid", &translatesvc.Error{Code: translatesvc.CodeInvalidArgument}, stdhttp.StatusBadRequest, translatesvc.CodeInvalidArgument},
		{"unsupported", &translatesvc.Error{Code: translatesvc.CodeUnsupportedLanguage}, stdhttp.StatusBadRequest, translatesvc.CodeUnsupportedLanguage},
		{"no autodetect", &translatesvc.Error{Code: translatesvc.CodeDetectNotSupported}, stdhttp.StatusBadRequest, translatesvc.CodeDetectNotSupported},
		{"rate limited", &translatesvc.Error{Code: translatesvc.CodeRateLimited, RetryAfter: 30 * time.Second}, stdhttp.StatusTooManyRequests, "rate_limited"},
		{"upstream", &translatesvc.Error{Code: translatesvc.CodeUpstream}, stdhttp.StatusBadGateway, "upstream_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &translateStub{err: tc.err}
			status, payload := doTranslateRequest(t, newTranslateRouter(svc), stdhttp.MethodPost, "/api/private/v1/translate", `{"text":"Hello","target":"ru"}`)
			if status != tc.status {
				t.Fatalf("status = %d, want %d; body=%v", status, tc.status, payload)
			}
			if payload["code"] != tc.code {
				t.Fatalf("code = %v, want %q", payload["code"], tc.code)
			}
		})
	}
}

func TestTranslateRouteRejectsBadJSON(t *testing.T) {
	svc := &translateStub{result: translatesvc.Result{Text: "x", Source: "en", Target: "ru"}}
	status, payload := doTranslateRequest(t, newTranslateRouter(svc), stdhttp.MethodPost, "/api/private/v1/translate", `{"text":`)
	if status != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", status, payload)
	}
	if payload["code"] != "invalid_json" {
		t.Fatalf("code = %v", payload["code"])
	}
}

func TestTranslateLanguagesRoute(t *testing.T) {
	svc := &translateStub{languages: []translatesvc.Language{{Code: "en", Name: "English"}, {Code: "ru", Name: "Русский"}}}
	status, payload := doTranslateRequest(t, newTranslateRouter(svc), stdhttp.MethodGet, "/api/private/v1/translate/languages", "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	items, ok := payload["languages"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("languages = %v", payload["languages"])
	}
	first, _ := items[0].(map[string]any)
	if first["code"] != "en" || first["name"] != "English" {
		t.Fatalf("first = %v", first)
	}
}

func TestTranslateRoutesRequireMethod(t *testing.T) {
	svc := &translateStub{result: translatesvc.Result{Text: "x", Source: "en", Target: "ru"}}
	router := newTranslateRouter(svc)
	if status, _ := doTranslateRequest(t, router, stdhttp.MethodGet, "/api/private/v1/translate", ""); status != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("GET /translate status = %d, want 405", status)
	}
	if status, _ := doTranslateRequest(t, router, stdhttp.MethodPost, "/api/private/v1/translate/languages", `{"text":"x"}`); status != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("POST /translate/languages status = %d, want 405", status)
	}
}
