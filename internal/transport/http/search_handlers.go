package http

import (
	"net/http"
	"strconv"
	"strings"
)

func newSearchHandler(svc SearchService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		q := strings.TrimSpace(r.URL.Query().Get("q"))
		scope := strings.TrimSpace(r.URL.Query().Get("scope"))
		limitRaw := strings.TrimSpace(r.URL.Query().Get("limit"))
		limit := 20
		if limitRaw != "" {
			if parsed, err := strconv.Atoi(limitRaw); err == nil {
				limit = parsed
			}
		}

		items, err := svc.Search(r.Context(), q, scope, limit)
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}
		// Never return the requesting user in directory people results.
		filteredUsers := items.Users[:0]
		for _, user := range items.Users {
			if strings.TrimSpace(user.ID) == userID {
				continue
			}
			filteredUsers = append(filteredUsers, user)
		}
		items.Users = filteredUsers

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "search.success"),
			"items":   items,
		})
	}
}
