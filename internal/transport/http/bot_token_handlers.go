package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	botauthsvc "combox-backend/internal/service/botauth"
)

type createBotTokenRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ChatIDs   []string `json:"chat_ids"`
	ExpiresAt string   `json:"expires_at"`
}

func newPrivateBotTokensHandler(svc BotTokenService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		var req createBotTokenRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		var expiresAt *time.Time
		if raw := strings.TrimSpace(req.ExpiresAt); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.bot.invalid_token_input", nil, i18n, defaultLocale)
				return
			}
			value := parsed.UTC()
			expiresAt = &value
		}

		out, err := svc.GenerateToken(r.Context(), botauthsvc.GenerateTokenInput{
			OwnerUserID: userID,
			Name:        req.Name,
			Scopes:      req.Scopes,
			ChatIDs:     req.ChatIDs,
			ExpiresAt:   expiresAt,
		})
		if err != nil {
			writeBotTokenServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "bot.token.create.success"),
			"token":   out,
		})
	}
}

func writeBotTokenServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *botauthsvc.Error
	if errors.As(err, &svcErr) {
		status := http.StatusInternalServerError
		switch svcErr.Code {
		case botauthsvc.CodeInvalidArgument:
			status = http.StatusBadRequest
		case botauthsvc.CodeNotFound:
			status = http.StatusNotFound
		}
		writeAPIError(w, r, status, svcErr.Code, svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
		return
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
