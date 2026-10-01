package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	translatesvc "combox-backend/internal/service/translate"
)

// TranslateService is the R19 auto-translate use-case behind
// POST /api/private/v1/translate and GET /api/private/v1/translate/languages.
// The channel auto_translate toggle already exists; these endpoints provide
// the target-language picker data and the actual translation engine.
type TranslateService interface {
	Translate(ctx context.Context, userID, text, source, target string) (translatesvc.Result, error)
	Languages(ctx context.Context) ([]translatesvc.Language, error)
}

type translateRequest struct {
	Text   string `json:"text"`
	Target string `json:"target"`
	Source string `json:"source"`
}

func newTranslateHandler(svc TranslateService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if svc == nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var req translateRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		result, err := svc.Translate(r.Context(), userID, req.Text, req.Source, req.Target)
		if err != nil {
			writeTranslateError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"text":    result.Text,
			"source":  result.Source,
			"target":  result.Target,
		})
	}
}

func newTranslateLanguagesHandler(svc TranslateService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if svc == nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		items, err := svc.Languages(r.Context())
		if err != nil {
			writeTranslateError(w, r, err, i18n, defaultLocale)
			return
		}
		if items == nil {
			items = []translatesvc.Language{}
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":   i18n.Translate(locale, "status.ok"),
			"languages": items,
		})
	}
}

func writeTranslateError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *translatesvc.Error
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case translatesvc.CodeInvalidArgument,
			translatesvc.CodeUnsupportedLanguage,
			translatesvc.CodeDetectNotSupported:
			writeAPIError(w, r, http.StatusBadRequest, svcErr.Code, "error.request.invalid_input", nil, i18n, defaultLocale)
			return
		case translatesvc.CodeRateLimited:
			details := map[string]string{}
			if svcErr.RetryAfter > 0 {
				details["retry_after_seconds"] = strconv.FormatInt(int64((svcErr.RetryAfter+time.Second-1)/time.Second), 10)
			}
			writeAPIError(w, r, http.StatusTooManyRequests, "rate_limited", "error.translate.rate_limited", details, i18n, defaultLocale)
			return
		case translatesvc.CodeUpstream:
			writeAPIError(w, r, http.StatusBadGateway, "upstream_failed", "error.translate.upstream_unavailable", nil, i18n, defaultLocale)
			return
		}
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
