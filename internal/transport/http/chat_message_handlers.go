package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

func newMessagesByIDHandler(messages MessageService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if r.Method == http.MethodPost {
			if messageID, ok := messageReadFromPath(r.URL.Path); ok {
				status, err := messages.MarkMessageReadByID(r.Context(), userID, messageID)
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
				reactions, action, err := messages.ToggleMessageReactionByID(r.Context(), userID, messageID, req.Emoji)
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
			updated, err := messages.EditMessageByID(r.Context(), userID, messageID, req.Content)
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
			if err := messages.DeleteMessageByID(r.Context(), userID, messageID); err != nil {
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

func newChatMessagesHandler(chat ChatService, messages MessageService, i18n Translator, defaultLocale string) http.HandlerFunc {
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
			if r.Method == http.MethodGet {
				item, err := chat.GetChat(r.Context(), userID, targetChatID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"chat": item})
				return
			}
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
				UserID:           userID,
				ChatID:           targetChatID,
				Title:            chatsvc.OptionalString{Set: req.Title != nil, Value: req.Title},
				AvatarDataURL:    chatsvc.OptionalString{Set: req.AvatarDataURL != nil, Value: req.AvatarDataURL},
				AvatarGradient:   chatsvc.OptionalString{Set: req.AvatarGradient != nil, Value: req.AvatarGradient},
				CommentsEnabled:  chatsvc.OptionalBool{Set: req.CommentsEnabled != nil, Value: req.CommentsEnabled != nil && *req.CommentsEnabled},
				ReactionsEnabled: chatsvc.OptionalBool{Set: req.ReactionsEnabled != nil, Value: req.ReactionsEnabled != nil && *req.ReactionsEnabled},
				IsPublic:         chatsvc.OptionalBool{Set: req.IsPublic != nil, Value: req.IsPublic != nil && *req.IsPublic},
				PublicSlug:       chatsvc.OptionalString{Set: req.PublicSlug != nil, Value: req.PublicSlug},
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

		if inviteToken, ok := inviteLinkAcceptFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			accepted, err := chat.AcceptInviteLink(r.Context(), userID, inviteToken)
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

		if targetChatID, ok := inviteLinksFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodGet:
				items, err := chat.ListInviteLinks(r.Context(), userID, targetChatID)
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
				var req createInviteLinkRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				item, err := chat.CreateInviteLink(r.Context(), chatsvc.CreateInviteLinkInput{
					UserID: userID,
					ChatID: targetChatID,
					Title:  req.Title,
				})
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusCreated, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"item":    item,
				})
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
			}
			return
		}

		if targetChatID, ok := membersFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodGet:
				includeBanned := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_banned")), "1") ||
					strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_banned")), "true")
				items, err := chat.ListMembers(r.Context(), userID, targetChatID, includeBanned)
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
			created, err := messages.ForwardMessage(r.Context(), chatsvc.ForwardMessageInput{
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
			updated, err := messages.UpsertMessageStatus(r.Context(), chatsvc.UpsertMessageStatusInput{
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
			updated, err := messages.EditMessage(r.Context(), chatsvc.EditMessageInput{
				UserID:        userID,
				ChatID:        chatID,
				MessageID:     messageID,
				Content:       req.Content,
				AttachmentIDs: req.AttachmentIDs,
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

			page, err := messages.ListMessages(r.Context(), chatsvc.ListMessagesInput{
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
			created, err := messages.CreateMessage(r.Context(), input)
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
