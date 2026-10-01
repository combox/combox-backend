package http

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	settingssvc "combox-backend/internal/service/settings"
)

type updateUserSettingsRequest struct {
	// Settings is a pointer so a body without the field at all can be told
	// apart from an intentional empty patch.
	Settings *map[string]any `json:"settings"`
}

// newUserSettingsHandler serves the global app settings:
//
//	GET /api/private/v1/profile/user-settings -> every whitelisted key
//	PUT /api/private/v1/profile/user-settings -> every whitelisted key after the patch
func newUserSettingsHandler(settings UserSettingsService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)

		switch r.Method {
		case http.MethodGet:
			current, err := settings.GetUserSettings(r.Context(), userID)
			if err != nil {
				writeUserSettingsError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message":  i18n.Translate(locale, "user_settings.get.success"),
				"settings": current,
			})
		case http.MethodPut:
			var req updateUserSettingsRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			if req.Settings == nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.user_settings.invalid_input", nil, i18n, defaultLocale)
				return
			}
			patch := make(map[string]string, len(*req.Settings))
			for key, value := range *req.Settings {
				patch[key] = normalizeUserSettingValue(value)
			}
			updated, err := settings.UpdateUserSettings(r.Context(), userID, patch)
			if err != nil {
				writeUserSettingsError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message":  i18n.Translate(locale, "user_settings.update.success"),
				"settings": updated,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

// normalizeUserSettingValue flattens a decoded JSON value onto the string form
// the storage uses. Booleans become "true"/"false"; anything else is passed
// through as its Go rendering so the service rejects it with the canonical
// invalid_value error instead of a handler-specific one.
func normalizeUserSettingValue(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return strings.TrimSpace(v)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// writeUserSettingsError maps the settings service codes onto HTTP, mirroring
// writeAuthServiceError.
func writeUserSettingsError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *settingssvc.Error
	if errors.As(err, &svcErr) {
		status := http.StatusInternalServerError
		if svcErr.Code == settingssvc.CodeInvalidArgument {
			status = http.StatusBadRequest
		}
		writeAPIError(w, r, status, svcErr.Code, svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
		return
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
