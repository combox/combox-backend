package http

import (
	"net/http"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

type createPublicChannelRequest struct {
	Title      string `json:"title"`
	PublicSlug string `json:"public_slug"`
	IsPublic   *bool  `json:"is_public"`
}

func publicChannelIDOnlyFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/public-channels/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 1 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

func publicChannelActionFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/public-channels/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func newPublicChannelsHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		var req createPublicChannelRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		created, err := chat.CreatePublicChannel(r.Context(), chatsvc.CreatePublicChannelInput{
			UserID:     userID,
			Title:      req.Title,
			PublicSlug: req.PublicSlug,
			IsPublic:   req.IsPublic == nil || *req.IsPublic,
		})
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"chat":    created,
		})
	}
}

func newPublicChannelByIDHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if chatID, action, ok := publicChannelActionFromPath(r.URL.Path); ok {
			switch {
			case action == "subscribe" && r.Method == http.MethodPost:
				updated, err := chat.SubscribePublicChannel(r.Context(), userID, chatID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"chat":    updated,
				})
				return
			case action == "unsubscribe" && r.Method == http.MethodPost:
				if err := chat.UnsubscribePublicChannel(r.Context(), userID, chatID); err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
				})
				return
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
		}

		if _, ok := publicChannelIDOnlyFromPath(r.URL.Path); ok {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
	}
}
