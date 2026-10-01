package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	reportsvc "combox-backend/internal/service/reports"
)

// ReportService is the reports surface used by the HTTP layer.
type ReportService interface {
	Create(ctx context.Context, reporterID, targetType, targetID, reason string) (reportsvc.Entry, error)
}

type reportRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
}

// newReportsHandler serves POST /api/private/v1/reports -> 201 with the
// stored report. Minimal contract for the Report buttons in the UI:
// {target_type: user|chat|photo|message, target_id, reason}.
func newReportsHandler(reports ReportService, i18n Translator, defaultLocale string) http.HandlerFunc {
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
		if reports == nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var req reportRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		entry, err := reports.Create(r.Context(), userID, req.TargetType, req.TargetID, req.Reason)
		if err != nil {
			writeReportsServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"report":  entry,
		})
	}
}

// writeReportsServiceError maps the typed reports error onto HTTP: 400 for
// bad target/reason, 429 for the per-reporter sliding window, 500 otherwise.
func writeReportsServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *reportsvc.Error
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case reportsvc.CodeInvalidArgument:
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
			return
		case reportsvc.CodeRateLimited:
			details := map[string]string{}
			if svcErr.RetryAfter > 0 {
				details["retry_after_seconds"] = strconv.FormatInt(int64((svcErr.RetryAfter+time.Second-1)/time.Second), 10)
			}
			writeAPIError(w, r, http.StatusTooManyRequests, "rate_limited", svcErr.MessageKey, details, i18n, defaultLocale)
			return
		}
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
