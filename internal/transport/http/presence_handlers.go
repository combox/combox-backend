package http

import (
	"net/http"
	"strings"
	"time"

	vkrepo "combox-backend/internal/repository/valkey"
)

const presenceOnlineTTL = 90 * time.Second
const presenceOfflineTTL = 30 * 24 * time.Hour

type presenceResponseItem struct {
	UserID          string `json:"user_id"`
	Online          bool   `json:"online"`
	LastSeen        string `json:"last_seen,omitempty"`
	LastSeenVisible bool   `json:"last_seen_visible"`
}

func newPresenceHandler(presenceRepo *vkrepo.PresenceRepository, settingsRepo *vkrepo.ProfileSettingsRepository, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		raw := strings.TrimSpace(r.URL.Query().Get("user_ids"))
		if raw == "" {
			raw = strings.TrimSpace(r.URL.Query().Get("user_id"))
		}
		ids := make([]string, 0)
		for _, part := range strings.Split(raw, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.request.invalid_input", nil, i18n, defaultLocale)
			return
		}

		items, err := presenceRepo.GetPresence(r.Context(), ids)
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		out := make([]presenceResponseItem, 0, len(ids))
		for _, id := range ids {
			item := items[id]
			settings, _ := settingsRepo.Get(r.Context(), id)
			visible := settings.ShowLastSeen || id == userID
			resp := presenceResponseItem{
				UserID:          id,
				Online:          item.Online,
				LastSeenVisible: visible,
			}
			if visible && !item.LastSeen.IsZero() {
				resp.LastSeen = item.LastSeen.UTC().Format(time.RFC3339)
			}
			out = append(out, resp)
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"items":   out,
		})
	}
}

type profileSettingsRequest struct {
	ShowLastSeen *bool `json:"show_last_seen"`
}

func newProfileSettingsHandler(settingsRepo *vkrepo.ProfileSettingsRepository, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		switch r.Method {
		case http.MethodGet:
			settings, _ := settingsRepo.Get(r.Context(), userID)
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"settings": map[string]any{
					"show_last_seen": settings.ShowLastSeen,
				},
			})
		case http.MethodPatch:
			var req profileSettingsRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			if req.ShowLastSeen == nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.request.invalid_input", nil, i18n, defaultLocale)
				return
			}
			if err := settingsRepo.Set(r.Context(), userID, *req.ShowLastSeen); err != nil {
				writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"settings": map[string]any{
					"show_last_seen": *req.ShowLastSeen,
				},
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}
