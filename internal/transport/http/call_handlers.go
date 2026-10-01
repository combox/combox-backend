package http

import (
	"context"
	"net/http"
	"strings"

	"combox-backend/internal/calls"
)

// CallService is the transport facing surface of the calls subsystem.
type CallService interface {
	Enabled() bool
	ICEServers(userID string) []calls.ICEServer
	ActiveCall(chatID string) (calls.CallRecord, []*calls.Participant, bool)
	HandleConn(ctx context.Context, ident calls.Identity, conn calls.SignalConn)
}

// newCallsWSHandler upgrades the signaling channel. Browsers cannot set an
// Authorization header on a WebSocket handshake, so the access token travels
// in the query string (same convention as /api/private/v1/ws).
func newCallsWSHandler(svc CallService, accessSecret string, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || !svc.Enabled() {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}
		token := strings.TrimSpace(r.URL.Query().Get("access_token"))
		if token == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		userID, err := verifyAccessToken(token, accessSecret)
		if err != nil {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.invalid_credentials", nil, i18n, defaultLocale)
			return
		}
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.HandleConn(r.Context(), calls.Identity{UserID: userID, DeviceID: deviceID}, conn)
	}
}

// newCallsICEHandler hands out STUN/TURN servers for the pre-join screen.
func newCallsICEHandler(svc CallService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || !svc.Enabled() {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ice_servers": svc.ICEServers(userID),
		})
	}
}

// newCallsActiveHandler reports the live call of a chat (for the call banner
// restored after a reload).
func newCallsActiveHandler(svc CallService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || !svc.Enabled() {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		chatID := strings.TrimSpace(r.URL.Query().Get("chat_id"))
		if chatID == "" {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.chat.invalid_input", nil, i18n, defaultLocale)
			return
		}
		record, participants, ok := svc.ActiveCall(chatID)
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"call": nil})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"call":         record,
			"participants": participants,
		})
	}
}
