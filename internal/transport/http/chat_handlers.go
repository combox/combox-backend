package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

type createChatRequest struct {
	Title     string   `json:"title"`
	MemberIDs []string `json:"member_ids"`
	Type      string   `json:"type"`
}

func messageEditFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] != "messages" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

type createMessageRequest struct {
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
	E2E           *struct {
		SenderDeviceID string                `json:"sender_device_id"`
		Envelopes      []chatsvc.E2EEnvelope `json:"envelopes"`
	} `json:"e2e"`
}

type upsertMessageStatusRequest struct {
	Status string `json:"status"`
}

type editMessageRequest struct {
	Content string `json:"content"`
}

type toggleReactionRequest struct {
	Emoji string `json:"emoji"`
}

func messageReadFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/messages/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "read" {
		return "", false
	}
	return parts[0], true
}

func messageIDFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/messages/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 1 {
		return "", false
	}
	if parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

func messageReactionFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/messages/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "reactions" {
		return "", false
	}
	return parts[0], true
}

func newMessagesByIDHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if r.Method == http.MethodPost {
			if messageID, ok := messageReadFromPath(r.URL.Path); ok {
				status, err := chat.MarkMessageReadByID(r.Context(), userID, messageID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "message.read.success"),
					"status":  status,
				})
				return
			}
			if messageID, ok := messageReactionFromPath(r.URL.Path); ok {
				var req toggleReactionRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				reactions, action, err := chat.ToggleMessageReactionByID(r.Context(), userID, messageID, req.Emoji)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message":   i18n.Translate(locale, "status.ok"),
					"action":    action,
					"reactions": reactions,
				})
				return
			}
		}

		messageID, ok := messageIDFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}

		switch r.Method {
		case http.MethodPatch:
			var req editMessageRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			updated, err := chat.EditMessageByID(r.Context(), userID, messageID, req.Content)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "message.update.success"),
				"item":    updated,
			})
		case http.MethodDelete:
			if err := chat.DeleteMessageByID(r.Context(), userID, messageID); err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "message.delete.success"),
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

func messageForwardFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] != "messages" || parts[2] == "" || parts[3] != "forward" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

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

func newChatMessagesHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if chatID, messageID, ok := messageForwardFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			created, err := chat.ForwardMessage(r.Context(), chatsvc.ForwardMessageInput{
				UserID:          userID,
				ChatID:          chatID,
				SourceMessageID: messageID,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusCreated, map[string]any{
				"message": i18n.Translate(locale, "message.create.success"),
				"item":    created,
			})
			return
		}

		if chatID, messageID, ok := messageStatusFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req upsertMessageStatusRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			updated, err := chat.UpsertMessageStatus(r.Context(), chatsvc.UpsertMessageStatusInput{
				UserID:    userID,
				ChatID:    chatID,
				MessageID: messageID,
				Status:    req.Status,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "message.status.upsert.success"),
				"status":  updated,
			})
			return
		}

		if chatID, messageID, ok := messageEditFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPatch {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req editMessageRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			updated, err := chat.EditMessage(r.Context(), chatsvc.EditMessageInput{
				UserID:    userID,
				ChatID:    chatID,
				MessageID: messageID,
				Content:   req.Content,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "message.update.success"),
				"item":    updated,
			})
			return
		}

		chatID, ok := chatIDFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.chat.not_found", nil, i18n, defaultLocale)
			return
		}

		switch r.Method {
		case http.MethodGet:
			deviceID := strings.TrimSpace(r.Header.Get("X-Device-ID"))
			limit := 50
			if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
				parsed, err := strconv.Atoi(rawLimit)
				if err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.chat.invalid_cursor", nil, i18n, defaultLocale)
					return
				}
				limit = parsed
			}

			page, err := chat.ListMessages(r.Context(), chatsvc.ListMessagesInput{
				UserID:   userID,
				ChatID:   chatID,
				Limit:    limit,
				Cursor:   r.URL.Query().Get("cursor"),
				DeviceID: deviceID,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message":     i18n.Translate(locale, "message.list.success"),
				"items":       page.Items,
				"next_cursor": page.NextCursor,
			})

		case http.MethodPost:
			var req createMessageRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			input := chatsvc.CreateMessageInput{
				UserID:        userID,
				ChatID:        chatID,
				Content:       req.Content,
				AttachmentIDs: req.AttachmentIDs,
			}
			if req.E2E != nil {
				input.SenderDeviceID = req.E2E.SenderDeviceID
				input.Envelopes = req.E2E.Envelopes
			}
			created, err := chat.CreateMessage(r.Context(), input)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusCreated, map[string]any{
				"message": i18n.Translate(locale, "message.create.success"),
				"item":    created,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

func chatIDFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/"
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

func messageStatusFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] != "messages" || parts[2] == "" || parts[3] != "status" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func writeChatServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *chatsvc.Error
	if errors.As(err, &svcErr) {
		status := http.StatusInternalServerError
		switch svcErr.Code {
		case chatsvc.CodeInvalidArgument:
			status = http.StatusBadRequest
		case chatsvc.CodeForbidden:
			status = http.StatusForbidden
		case chatsvc.CodeNotFound:
			status = http.StatusNotFound
		}
		writeAPIError(w, r, status, svcErr.Code, svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
		return
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
