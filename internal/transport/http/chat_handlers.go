package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

type createChatRequest struct {
	Title     string   `json:"title"`
	MemberIDs []string `json:"member_ids"`
	Type      string   `json:"type"`
	Kind      string   `json:"kind"`
}

type createChannelRequest struct {
	Title       string `json:"title"`
	ChannelType string `json:"channel_type"`
}

type updateChatRequest struct {
	Title               *string `json:"title"`
	AvatarDataURL       *string `json:"avatar_data_url"`
	AvatarGradient      *string `json:"avatar_gradient"`
	CommentsEnabled     *bool   `json:"comments_enabled"`
	ReactionsEnabled    *bool   `json:"reactions_enabled"`
	SignMessages        *bool   `json:"sign_messages"`
	ShowAuthorsProfiles *bool   `json:"show_authors_profiles"`
	AutoTranslate       *bool   `json:"auto_translate"`
	SlowModeSeconds     *int    `json:"slow_mode_seconds"`
	DiscussionChatID    *string `json:"discussion_chat_id"`
	IsPublic            *bool   `json:"is_public"`
	PublicSlug          *string `json:"public_slug"`
	SendPermission      *string `json:"send_permission"`
	Description         *string `json:"description"`
	IconEmoji           *string `json:"icon_emoji"`
	// ChannelType accepts "text" or "voice"; an empty value keeps the current
	// type. The CHECK constraint lives in migration 000021.
	ChannelType *string `json:"channel_type"`
}

type createInviteLinkRequest struct {
	Title string `json:"title"`
}

// updateChatInputFromRequest maps the PATCH body onto the service input,
// treating a JSON null/absent key as "leave unchanged".
func updateChatInputFromRequest(userID, chatID string, req updateChatRequest) chatsvc.UpdateChatInput {
	return chatsvc.UpdateChatInput{
		UserID:              userID,
		ChatID:              chatID,
		Title:               chatsvc.OptionalString{Set: req.Title != nil, Value: req.Title},
		AvatarDataURL:       chatsvc.OptionalString{Set: req.AvatarDataURL != nil, Value: req.AvatarDataURL},
		AvatarGradient:      chatsvc.OptionalString{Set: req.AvatarGradient != nil, Value: req.AvatarGradient},
		CommentsEnabled:     chatsvc.OptionalBool{Set: req.CommentsEnabled != nil, Value: req.CommentsEnabled != nil && *req.CommentsEnabled},
		ReactionsEnabled:    chatsvc.OptionalBool{Set: req.ReactionsEnabled != nil, Value: req.ReactionsEnabled != nil && *req.ReactionsEnabled},
		SignMessages:        chatsvc.OptionalBool{Set: req.SignMessages != nil, Value: req.SignMessages != nil && *req.SignMessages},
		ShowAuthorsProfiles: chatsvc.OptionalBool{Set: req.ShowAuthorsProfiles != nil, Value: req.ShowAuthorsProfiles != nil && *req.ShowAuthorsProfiles},
		AutoTranslate:       chatsvc.OptionalBool{Set: req.AutoTranslate != nil, Value: req.AutoTranslate != nil && *req.AutoTranslate},
		SlowModeSeconds:     chatsvc.OptionalInt{Set: req.SlowModeSeconds != nil, Value: derefInt(req.SlowModeSeconds)},
		DiscussionChatID:    chatsvc.OptionalString{Set: req.DiscussionChatID != nil, Value: req.DiscussionChatID},
		IsPublic:            chatsvc.OptionalBool{Set: req.IsPublic != nil, Value: req.IsPublic != nil && *req.IsPublic},
		PublicSlug:          chatsvc.OptionalString{Set: req.PublicSlug != nil, Value: req.PublicSlug},
		SendPermission:      chatsvc.OptionalString{Set: req.SendPermission != nil, Value: req.SendPermission},
		Description:         chatsvc.OptionalString{Set: req.Description != nil, Value: req.Description},
		IconEmoji:           chatsvc.OptionalString{Set: req.IconEmoji != nil, Value: req.IconEmoji},
		ChannelType:         chatsvc.OptionalString{Set: req.ChannelType != nil, Value: req.ChannelType},
	}
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
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

type openDirectChatRequest struct {
	RecipientUserID string `json:"recipient_user_id"`
}

// pinChatRequest optionally carries the sidebar category a chat is pinned
// into plus its position inside that category's pinned block.
type pinChatRequest struct {
	Scope *string `json:"scope"`
	Order *int64  `json:"order"`
}

// decodeOptionalJSON decodes a request body, treating an empty body as a
// no-op so callers can keep the legacy no-body request shape.
func decodeOptionalJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// deleteForEveryone reports whether the caller asked to remove the chat for
// every participant instead of only for themselves.
func deleteForEveryone(r *http.Request) bool {
	raw := strings.TrimSpace(r.URL.Query().Get("for_everyone"))
	return raw == "1" || strings.EqualFold(raw, "true")
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

// chatEventsFromPath matches GET /api/private/v1/chats/{chatID}/events, the
// per-chat "recent actions" journal.
func chatEventsFromPath(path string) (string, bool) {
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
	if parts[0] == "" || parts[1] != "events" {
		return "", false
	}
	return parts[0], true
}

func inviteLinksFromPath(path string) (string, bool) {
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
	if parts[0] == "" || parts[1] != "invite-links" {
		return "", false
	}
	return parts[0], true
}

func inviteLinkAcceptFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/invite-links/"
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

// chatListActionFromPath matches per-viewer chat list actions:
// archive, unarchive, pin, unpin, read and history.
func chatListActionFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	if parts[0] == "" {
		return "", "", false
	}
	switch parts[1] {
	case "archive", "unarchive", "pin", "unpin", "read", "history":
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
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
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
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

func pinnedMessageFromPath(path string) (string, bool) {
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
	if parts[0] == "" || parts[1] != "pinned-message" {
		return "", false
	}
	return parts[0], true
}

func messagePinFromPath(path string) (string, string, bool) {
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
	if parts[0] == "" || parts[1] != "messages" || parts[2] == "" || parts[3] != "pin" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

type pinMessageRequest struct {
	Pinned *bool `json:"pinned"`
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
				Kind:      req.Kind,
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
			if r.Method == http.MethodGet {
				item, err := chat.GetChat(r.Context(), userID, targetChatID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"chat": item})
				return
			}
			if r.Method == http.MethodDelete {
				var err error
				if deleteForEveryone(r) {
					err = chat.DeleteChatForEveryone(r.Context(), userID, targetChatID)
				} else {
					err = chat.DeleteChat(r.Context(), userID, targetChatID)
				}
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
			if r.Method != http.MethodPatch {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req updateChatRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			updated, err := chat.UpdateChat(r.Context(), updateChatInputFromRequest(userID, targetChatID, req))
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

		if targetChatID, ok := pinnedMessageFromPath(r.URL.Path); ok {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			item, err := chat.GetPinnedMessage(r.Context(), userID, targetChatID)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"item":    item,
			})
			return
		}

		if targetChatID, messageID, ok := messagePinFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req pinMessageRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			pinned := req.Pinned == nil || *req.Pinned
			item, err := chat.PinMessage(r.Context(), chatsvc.PinMessageInput{
				UserID:    userID,
				ChatID:    targetChatID,
				MessageID: messageID,
				Pinned:    pinned,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"item":    item,
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

		if targetChatID, ok := chatEventsFromPath(r.URL.Path); ok {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			limit := 0
			if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
				parsed, err := strconv.Atoi(rawLimit)
				if err != nil || parsed < 0 {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.chat.invalid_input", nil, i18n, defaultLocale)
					return
				}
				limit = parsed
			}
			items, err := chat.ListChatEvents(r.Context(), userID, targetChatID, limit)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"items":   items,
			})
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

		if targetChatID, action, ok := chatListActionFromPath(r.URL.Path); ok {
			locale := requestLocale(r, defaultLocale)
			switch action {
			case "archive", "unarchive":
				if r.Method != http.MethodPost {
					writeMethodNotAllowed(w, r, i18n, defaultLocale)
					return
				}
				updated, err := chat.ArchiveChat(r.Context(), userID, targetChatID, action == "archive")
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"chat":    updated,
				})
			case "pin", "unpin":
				if r.Method != http.MethodPost {
					writeMethodNotAllowed(w, r, i18n, defaultLocale)
					return
				}
				var req pinChatRequest
				if err := decodeOptionalJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				scope := chatsvc.DefaultPinScope
				if req.Scope != nil {
					scope = strings.TrimSpace(*req.Scope)
				}
				var order int64
				if req.Order != nil {
					order = *req.Order
				}
				updated, err := chat.PinChat(r.Context(), userID, targetChatID, action == "pin", scope, order)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"chat":    updated,
				})
			case "read":
				if r.Method != http.MethodPost {
					writeMethodNotAllowed(w, r, i18n, defaultLocale)
					return
				}
				if err := chat.MarkChatRead(r.Context(), userID, targetChatID); err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
				})
			case "history":
				if r.Method != http.MethodDelete {
					writeMethodNotAllowed(w, r, i18n, defaultLocale)
					return
				}
				cleared, err := chat.ClearChatHistory(r.Context(), userID, targetChatID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"cleared": cleared,
				})
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
			}
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
			if limit <= 0 {
				limit = 50
			}
			if limit > 500 {
				limit = 500
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
				"statuses":    page.Statuses,
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
		if svcErr.Code == chatsvc.CodeInternal {
			slog.Default().Error("chat service internal error",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Any("cause", svcErr.Cause),
			)
		}
		status := http.StatusInternalServerError
		switch svcErr.Code {
		case chatsvc.CodeInvalidArgument:
			status = http.StatusBadRequest
		case chatsvc.CodeForbidden:
			status = http.StatusForbidden
		case chatsvc.CodeNotFound:
			status = http.StatusNotFound
		case chatsvc.CodeConflict:
			status = http.StatusConflict
		case chatsvc.CodeAlreadyExists:
			status = http.StatusConflict
		}
		writeAPIError(w, r, status, svcErr.Code, svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
		return
	}
	slog.Default().Error("chat handler unknown error",
		slog.String("request_id", RequestIDFromContext(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	)
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}

// chatSubpath matches /api/private/v1/chats/{chatID}/{action} and returns the
// chat id. The explicit patterns registered for these actions win over the
// /chats/ subtree in ServeMux, so the action segment is always the last one.
func chatSubpath(path, action string) (string, bool) {
	const prefix = "/api/private/v1/chats/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || rest == "" {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != action {
		return "", false
	}
	return parts[0], true
}

// newChatClearHistoryHandler serves POST /api/private/v1/chats/{chatID}/clear.
func newChatClearHistoryHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
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
		targetChatID, ok := chatSubpath(r.URL.Path, "clear")
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.chat.not_found", nil, i18n, defaultLocale)
			return
		}
		cleared, err := chat.ClearChatHistory(r.Context(), userID, targetChatID)
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(requestLocale(r, defaultLocale), "status.ok"),
			"cleared": cleared,
		})
	}
}

// newChatExportHandler serves GET /api/private/v1/chats/{chatID}/export and
// streams the archive as a downloadable JSON file.
func newChatExportHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		targetChatID, ok := chatSubpath(r.URL.Path, "export")
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.chat.not_found", nil, i18n, defaultLocale)
			return
		}
		export, err := chat.ExportChatHistory(r.Context(), userID, targetChatID)
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		filename := "chat-export-" + exportFilenameSlug(export.Chat) + ".json"
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(export)
	}
}

// exportFilenameSlug turns a chat title into a filesystem-safe slug.
func exportFilenameSlug(item chatsvc.Chat) string {
	source := item.Title
	if strings.TrimSpace(source) == "" {
		source = item.ID
	}
	var b strings.Builder
	for _, r := range strings.ToLower(source) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "chat"
	}
	return slug
}

// newChatWallpaperHandler serves PUT (set) and DELETE (clear) on
// /api/private/v1/chats/{chatID}/wallpaper.
func newChatWallpaperHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		targetChatID, ok := chatSubpath(r.URL.Path, "wallpaper")
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.chat.not_found", nil, i18n, defaultLocale)
			return
		}
		kind := chatsvc.WallpaperKindNone
		value := ""
		switch r.Method {
		case http.MethodPut, http.MethodPost:
			var req wallpaperRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			kind = strings.TrimSpace(req.Kind)
			if req.Value != nil {
				value = strings.TrimSpace(*req.Value)
			}
		case http.MethodDelete:
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		updated, err := chat.SetChatWallpaper(r.Context(), userID, targetChatID, kind, value)
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(requestLocale(r, defaultLocale), "status.ok"),
			"chat":    updated,
		})
	}
}

type wallpaperRequest struct {
	Kind  string  `json:"kind"`
	Value *string `json:"value"`
}
