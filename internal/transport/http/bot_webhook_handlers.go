package http

import (
	"errors"
	"net/http"
	"strings"

	botwebhooksvc "combox-backend/internal/service/botwebhook"
)

const botScopeWebhooksWrite = "bot:webhooks:write"

type botCreateWebhookRequest struct {
	EndpointURL string   `json:"endpoint_url"`
	Events      []string `json:"events"`
}

func newPublicBotWebhooksHandler(svc BotWebhookService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		principal, ok := BotPrincipalFromContext(r.Context())
		if !ok || strings.TrimSpace(principal.UserID) == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.bot.invalid_token", nil, i18n, defaultLocale)
			return
		}
		if !principal.HasScope(botScopeWebhooksWrite) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.bot.missing_scope", map[string]string{"required_scope": botScopeWebhooksWrite}, i18n, defaultLocale)
			return
		}

		var req botCreateWebhookRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		created, err := svc.Create(r.Context(), botwebhooksvc.CreateInput{
			BotUserID:   principal.UserID,
			EndpointURL: req.EndpointURL,
			Events:      req.Events,
		})
		if err != nil {
			writeBotWebhookServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "bot.webhook.create.success"),
			"webhook": created,
		})
	}
}

func writeBotWebhookServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *botwebhooksvc.Error
	if errors.As(err, &svcErr) {
		status := http.StatusInternalServerError
		switch svcErr.Code {
		case botwebhooksvc.CodeInvalidArgument:
			status = http.StatusBadRequest
		case botwebhooksvc.CodeAlreadyExists:
			status = http.StatusConflict
		}
		writeAPIError(w, r, status, svcErr.Code, svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
		return
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
