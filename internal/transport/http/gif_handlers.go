package http

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	vkrepo "combox-backend/internal/repository/valkey"
	gifsvc "combox-backend/internal/service/gif"
)

type GIFService interface {
	SearchGIFs(ctx context.Context, q, pos string, limit int) (gifsvc.SearchResult, error)
}

type gifRecentRequest struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	PreviewURL string `json:"preview_url"`
	URL        string `json:"url"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

func newGifsSearchHandler(svc GIFService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		limit := 24
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				limit = parsed
			}
		}

		result, err := svc.SearchGIFs(
			r.Context(),
			strings.TrimSpace(r.URL.Query().Get("q")),
			strings.TrimSpace(r.URL.Query().Get("pos")),
			limit,
		)
		if err != nil {
			writeAPIError(w, r, http.StatusBadGateway, "upstream_failed", "error.internal", nil, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":  i18n.Translate(locale, "status.ok"),
			"items":    result.Items,
			"next_pos": result.NextPos,
		})
	}
}

func newGifsRecentHandler(repo *vkrepo.ProfileSettingsRepository, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		switch r.Method {
		case http.MethodGet:
			limit := 30
			if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil {
					limit = parsed
				}
			}
			items, err := repo.ListRecentGIFs(r.Context(), userID, limit)
			if err != nil {
				writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"items":   items,
			})
		case http.MethodPost:
			var req gifRecentRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.URL) == "" {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.request.invalid_input", nil, i18n, defaultLocale)
				return
			}
			err := repo.AddRecentGIF(r.Context(), userID, vkrepo.RecentGIF{
				ID:         strings.TrimSpace(req.ID),
				Title:      strings.TrimSpace(req.Title),
				PreviewURL: strings.TrimSpace(req.PreviewURL),
				URL:        strings.TrimSpace(req.URL),
				Width:      req.Width,
				Height:     req.Height,
			}, 400)
			if err != nil {
				writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}
