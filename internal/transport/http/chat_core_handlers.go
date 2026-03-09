package http

import (
	"net/http"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

func newChatsHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		switch r.Method {
		case http.MethodGet:
			items, err := chat.ListChats(r.Context(), userID)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "chat.list.success"),
				"items":   items,
			})
		case http.MethodPost:
			var req createChatRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			created, err := chat.CreateChat(r.Context(), chatsvc.CreateChatInput{
				UserID:    userID,
				Title:     req.Title,
				MemberIDs: req.MemberIDs,
				Type:      req.Type,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusCreated, map[string]any{
				"message": i18n.Translate(locale, "chat.create.success"),
				"chat":    created,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

func newDirectMessageHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		var req createDirectMessageRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		if strings.TrimSpace(req.RecipientUserID) == userID {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.message.invalid_input", nil, i18n, defaultLocale)
			return
		}
		created, createdChat, err := chat.CreateDirectMessage(r.Context(), chatsvc.CreateDirectMessageInput{
			UserID:           userID,
			RecipientUserID:  req.RecipientUserID,
			Content:          req.Content,
			ReplyToMessageID: req.ReplyToMessageID,
			AttachmentIDs:    req.AttachmentIDs,
		})
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "message.create.success"),
			"item":    created,
			"chat":    createdChat,
		})
	}
}

func newDirectChatHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		var req openDirectChatRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		opened, err := chat.OpenDirectChat(r.Context(), chatsvc.OpenDirectChatInput{
			UserID:          userID,
			RecipientUserID: req.RecipientUserID,
		})
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"chat":    opened,
		})
	}
}
