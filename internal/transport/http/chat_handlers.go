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

type createChannelRequest struct {
	Title       string `json:"title"`
	ChannelType string `json:"channel_type"`
}

type updateChatRequest struct {
	Title          *string `json:"title"`
	AvatarDataURL  *string `json:"avatar_data_url"`
	AvatarGradient *string `json:"avatar_gradient"`
}

type addMembersRequest struct {
	MemberIDs []string `json:"member_ids"`
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
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
	Content          string   `json:"content"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	AttachmentIDs    []string `json:"attachment_ids"`
	E2E              *struct {
		SenderDeviceID string                `json:"sender_device_id"`
		Envelopes      []chatsvc.E2EEnvelope `json:"envelopes"`
	} `json:"e2e"`
}

type createDirectMessageRequest struct {
	RecipientUserID  string   `json:"recipient_user_id"`
	Content          string   `json:"content"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	AttachmentIDs    []string `json:"attachment_ids"`
}

func channelsFromPath(path string) (string, bool) {
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
	if parts[0] == "" || parts[1] != "channels" {
		return "", false
	}
	return parts[0], true
}

func channelFromPath(path string) (string, string, bool) {
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
	if parts[0] == "" || parts[1] != "channels" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func membersFromPath(path string) (string, bool) {
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
	if parts[0] == "" || parts[1] != "members" {
		return "", false
	}
	return parts[0], true
}

func chatIDOnlyFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/"
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

func inviteAcceptFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/invites/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "accept" {
		return "", false
	}
	return parts[0], true
}

func leaveFromPath(path string) (string, bool) {
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
	if parts[0] == "" || parts[1] != "leave" {
		return "", false
	}
	return parts[0], true
}

func memberByUserFromPath(path string) (string, string, bool) {
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
	if parts[0] == "" || parts[1] != "members" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
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

func newChatMessagesHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if groupChatID, ok := channelsFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodGet:
				items, err := chat.ListChannels(r.Context(), userID, groupChatID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"items":   items,
				})
			case http.MethodPost:
				var req createChannelRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				created, err := chat.CreateChannel(r.Context(), chatsvc.CreateChannelInput{
					UserID:      userID,
					GroupChatID: groupChatID,
					Title:       req.Title,
					ChannelType: req.ChannelType,
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
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
			}
			return
		}

		if groupChatID, channelChatID, ok := channelFromPath(r.URL.Path); ok {
			if r.Method != http.MethodDelete {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			err := chat.DeleteChannel(r.Context(), chatsvc.DeleteChannelInput{UserID: userID, GroupChatID: groupChatID, ChannelChatID: channelChatID})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
			})
			return
		}

		if targetChatID, ok := chatIDOnlyFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPatch {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req updateChatRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			updated, err := chat.UpdateChat(r.Context(), chatsvc.UpdateChatInput{
				UserID:         userID,
				ChatID:         targetChatID,
				Title:          chatsvc.OptionalString{Set: req.Title != nil, Value: req.Title},
				AvatarDataURL:  chatsvc.OptionalString{Set: req.AvatarDataURL != nil, Value: req.AvatarDataURL},
				AvatarGradient: chatsvc.OptionalString{Set: req.AvatarGradient != nil, Value: req.AvatarGradient},
			})
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
		}

		if inviteToken, ok := inviteAcceptFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			accepted, err := chat.AcceptInvite(r.Context(), userID, inviteToken)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"chat":    accepted,
			})
			return
		}

		if targetChatID, ok := leaveFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			if err := chat.LeaveChat(r.Context(), userID, targetChatID); err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
			})
			return
		}

		if targetChatID, ok := membersFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodGet:
				items, err := chat.ListMembers(r.Context(), userID, targetChatID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"items":   items,
				})
			case http.MethodPost:
				var req addMembersRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				items, err := chat.AddMembers(r.Context(), userID, targetChatID, req.MemberIDs)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"items":   items,
				})
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
			}
			return
		}

		if targetChatID, targetUserID, ok := memberByUserFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodPatch:
				var req updateMemberRoleRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				items, err := chat.UpdateMemberRole(r.Context(), userID, targetChatID, targetUserID, req.Role)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"items":   items,
				})
			case http.MethodDelete:
				items, err := chat.RemoveMember(r.Context(), userID, targetChatID, targetUserID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"items":   items,
				})
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
			}
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
				UserID:           userID,
				ChatID:           chatID,
				Content:          req.Content,
				ReplyToMessageID: req.ReplyToMessageID,
				AttachmentIDs:    req.AttachmentIDs,
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
