package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	privacysvc "combox-backend/internal/service/privacy"
)

// privacySettingsPathPrefix anchors the {param} segment of
// PUT /api/private/v1/profile/privacy/{param}.
const privacySettingsPathPrefix = "/api/private/v1/profile/privacy/"

type privacySettingRequest struct {
	Rule     string   `json:"rule"`
	AllowIDs []string `json:"allow_ids"`
	DenyIDs  []string `json:"deny_ids"`
}

// newPrivacySettingsHandler serves
// GET /api/private/v1/profile/privacy -> every parameter with its rule,
// exception lists, counts, plus the default rule map.
func newPrivacySettingsHandler(privacy PrivacyService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		result, err := privacy.Get(r.Context(), userID)
		if err != nil {
			writePrivacyServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":  i18n.Translate(locale, "status.ok"),
			"settings": result.Settings,
			"defaults": result.Defaults,
		})
	}
}

// newPrivacySettingHandler serves
// PUT /api/private/v1/profile/privacy/{param} -> the updated setting.
func newPrivacySettingHandler(privacy PrivacyService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		param, ok := privacyParamFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusBadRequest, "error.validation", "error.request.invalid_input", map[string]string{"param": ""}, i18n, defaultLocale)
			return
		}

		var req privacySettingRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		setting, err := privacy.Set(r.Context(), userID, param, req.Rule, req.AllowIDs, req.DenyIDs)
		if err != nil {
			writePrivacyServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":     i18n.Translate(locale, "status.ok"),
			"param":       setting.Param,
			"rule":        setting.Rule,
			"allow_ids":   setting.AllowIDs,
			"deny_ids":    setting.DenyIDs,
			"allow_count": setting.AllowCount,
			"deny_count":  setting.DenyCount,
		})
	}
}

// privacyParamFromPath extracts the parameter segment of
// /api/private/v1/profile/privacy/{param}.
func privacyParamFromPath(path string) (string, bool) {
	trimmed := strings.TrimSpace(path)
	if !strings.HasPrefix(trimmed, privacySettingsPathPrefix) {
		return "", false
	}
	param := strings.Trim(strings.TrimPrefix(trimmed, privacySettingsPathPrefix), "/")
	if param == "" || strings.Contains(param, "/") {
		return "", false
	}
	return param, true
}

// writePrivacyServiceError maps the typed privacy error onto HTTP: 400 with
// the spec-mandated error.validation code for invalid params/rules/ids, 404
// for a missing resource, 500 for repository failures (which are logged).
func writePrivacyServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var serviceErr *privacysvc.Error
	if errors.As(err, &serviceErr) {
		switch serviceErr.Code {
		case privacysvc.CodeInvalidArgument:
			writeAPIError(w, r, http.StatusBadRequest, "error.validation", serviceErr.MessageKey, serviceErr.Details, i18n, defaultLocale)
			return
		case privacysvc.CodeNotFound:
			writeAPIError(w, r, http.StatusNotFound, "not_found", serviceErr.MessageKey, serviceErr.Details, i18n, defaultLocale)
			return
		default:
			logPrivacyError(r, serviceErr.Code, serviceErr.Cause)
		}
	} else {
		logPrivacyError(r, "", err)
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}

func logPrivacyError(r *http.Request, code string, cause error) {
	slog.Default().Error("privacy service error",
		slog.String("request_id", RequestIDFromContext(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("code", code),
		slog.Any("cause", cause),
	)
}

// PrivacyService is the settings + enforcement surface of the privacy
// service used by the HTTP layer.
type PrivacyService interface {
	Get(ctx context.Context, userID string) (privacysvc.Result, error)
	Set(ctx context.Context, userID, param, rule string, allowIDs, denyIDs []string) (privacysvc.Setting, error)
	Evaluate(ctx context.Context, viewerID, targetID, param string) (bool, error)
}
