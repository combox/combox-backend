package http

import (
	"net/http"
	"strconv"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

const (
	botScopeMessagesRead  = "bot:messages:read"
	botScopeMessagesWrite = "bot:messages:write"
)

type botCreateMessageRequest struct {
	ChatID        string   `json:"chat_id"`
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
}

func newPublicBotMessagesHandler(messages MessageService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		principal, ok := BotPrincipalFromContext(r.Context())
		if !ok || strings.TrimSpace(principal.BotID) == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.bot.invalid_token", nil, i18n, defaultLocale)
			return
		}
		if !principal.HasScope(botScopeMessagesWrite) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.bot.missing_scope", map[string]string{"required_scope": botScopeMessagesWrite}, i18n, defaultLocale)
			return
		}

		var req botCreateMessageRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		chatID := strings.TrimSpace(req.ChatID)
		if chatID == "" {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.message.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if !principal.CanAccessChat(chatID) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.bot.chat_not_allowed", nil, i18n, defaultLocale)
			return
		}

		created, err := messages.CreateMessage(r.Context(), chatsvc.CreateMessageInput{
			BotID:         principal.BotID,
			ChatID:        chatID,
			Content:       req.Content,
			AttachmentIDs: req.AttachmentIDs,
		})
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "bot.message.create.success"),
			"item":    created,
		})
	}
}

func botChatIDFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/public/v1/bot/chats/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "messages" {
		return "", false
	}
	return parts[0], true
}

func newPublicBotChatMessagesHandler(messages MessageService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		principal, ok := BotPrincipalFromContext(r.Context())
		if !ok || strings.TrimSpace(principal.UserID) == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.bot.invalid_token", nil, i18n, defaultLocale)
			return
		}
		if !principal.HasScope(botScopeMessagesRead) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.bot.missing_scope", map[string]string{"required_scope": botScopeMessagesRead}, i18n, defaultLocale)
			return
		}

		chatID, ok := botChatIDFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}
		if !principal.CanAccessChat(chatID) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.bot.chat_not_allowed", nil, i18n, defaultLocale)
			return
		}

		limit := 50
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.chat.invalid_cursor", nil, i18n, defaultLocale)
				return
			}
			limit = parsed
		}

		page, err := messages.ListMessages(r.Context(), chatsvc.ListMessagesInput{
			UserID: principal.UserID,
			ChatID: chatID,
			Limit:  limit,
			Cursor: r.URL.Query().Get("cursor"),
		})
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":     i18n.Translate(locale, "bot.message.list.success"),
			"items":       page.Items,
			"next_cursor": page.NextCursor,
		})
	}
}
