package http

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

type revokeOtherSessionsRequest struct {
	KeepSessionID string `json:"keep_session_id"`
}

// newAuthSessionsHandler serves GET /api/private/v1/auth/sessions.
func newAuthSessionsHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
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
		items, err := auth.ListSessions(r.Context(), userID, strings.TrimSpace(r.Header.Get("X-Session-ID")))
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(requestLocale(r, defaultLocale), "auth.sessions.list.success"),
			"items":   items,
		})
	}
}

// newAuthSessionItemHandler serves DELETE /api/private/v1/auth/sessions/{sessionID}.
func newAuthSessionItemHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		if err := auth.RevokeSession(r.Context(), userID, r.PathValue("sessionID")); err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(requestLocale(r, defaultLocale), "auth.session.revoke.success"),
		})
	}
}

// newAuthRevokeOthersHandler serves POST /api/private/v1/auth/sessions/revoke-others.
//
// The session to keep is taken from the body first, then from the session id
// the access token was minted for, and when neither is present every session
// of the user is revoked.
func newAuthRevokeOthersHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		var req revokeOtherSessionsRequest
		if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		keep := strings.TrimSpace(req.KeepSessionID)
		if keep == "" {
			keep = strings.TrimSpace(r.Header.Get("X-Session-ID"))
		}

		revoked, err := auth.RevokeOtherSessions(r.Context(), userID, keep)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(requestLocale(r, defaultLocale), "auth.sessions.revoke_others.success"),
			"revoked": revoked,
		})
	}
}
