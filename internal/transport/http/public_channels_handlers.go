package http

import (
	"net/http"
	"strconv"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

type createPublicChannelRequest struct {
	Title      string `json:"title"`
	PublicSlug string `json:"public_slug"`
	IsPublic   *bool  `json:"is_public"`
}

func publicChannelThreadFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/standalone-channels/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] != "threads" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func publicChannelThreadCommentsFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/standalone-channels/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] != "threads" || parts[2] == "" || parts[3] != "comments" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

type createCommentRequest struct {
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
}

type moderationTargetRequest struct {
	UserID string `json:"user_id"`
}

func publicChannelModerationFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/standalone-channels/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] != "moderation" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func publicChannelIDOnlyFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/standalone-channels/"
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
	const prefix = "/api/private/v1/standalone-channels/"
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

		// Moderation.
		if channelID, action, ok := publicChannelModerationFromPath(r.URL.Path); ok {
			switch {
			case action == "ban" && r.Method == http.MethodPost:
				var req moderationTargetRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				if err := chat.BanPublicChannelUser(r.Context(), userID, channelID, req.UserID); err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{"message": i18n.Translate(locale, "status.ok")})
				return
			case action == "unban" && r.Method == http.MethodPost:
				var req moderationTargetRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				if err := chat.UnbanPublicChannelUser(r.Context(), userID, channelID, req.UserID); err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{"message": i18n.Translate(locale, "status.ok")})
				return
			case action == "mute" && r.Method == http.MethodPost:
				var req moderationTargetRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				if err := chat.MutePublicChannelUser(r.Context(), userID, channelID, req.UserID); err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{"message": i18n.Translate(locale, "status.ok")})
				return
			case action == "unmute" && r.Method == http.MethodPost:
				var req moderationTargetRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				if err := chat.UnmutePublicChannelUser(r.Context(), userID, channelID, req.UserID); err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{"message": i18n.Translate(locale, "status.ok")})
				return
			case action == "banned" && r.Method == http.MethodGet:
				limit := 200
				if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
					if n, convErr := strconv.Atoi(raw); convErr == nil {
						limit = n
					}
				}
				items, err := chat.ListPublicChannelBans(r.Context(), userID, channelID, limit)
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
			case action == "muted" && r.Method == http.MethodGet:
				limit := 200
				if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
					if n, convErr := strconv.Atoi(raw); convErr == nil {
						limit = n
					}
				}
				items, err := chat.ListPublicChannelMutes(r.Context(), userID, channelID, limit)
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
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
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

		// Comment threads.
		if channelID, rootMessageID, ok := publicChannelThreadCommentsFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodGet:
				threadChatID, err := chat.GetOrCreateCommentThread(r.Context(), userID, channelID, rootMessageID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				limit := 60
				if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
					if n, convErr := strconv.Atoi(raw); convErr == nil {
						limit = n
					}
				}
				cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
				page, err := chat.ListMessages(r.Context(), chatsvc.ListMessagesInput{UserID: userID, ChatID: threadChatID, Limit: limit, Cursor: cursor})
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message":        i18n.Translate(locale, "status.ok"),
					"thread_chat_id": threadChatID,
					"items":          page.Items,
					"next_cursor":    page.NextCursor,
				})
				return
			case http.MethodPost:
				threadChatID, err := chat.GetOrCreateCommentThread(r.Context(), userID, channelID, rootMessageID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				var req createCommentRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				created, err := chat.CreateMessage(r.Context(), chatsvc.CreateMessageInput{
					UserID:        userID,
					ChatID:        threadChatID,
					Content:       req.Content,
					AttachmentIDs: req.AttachmentIDs,
				})
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusCreated, map[string]any{
					"message": i18n.Translate(locale, "status.ok"),
					"item":    created,
				})
				return
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
		}

		if channelID, rootMessageID, ok := publicChannelThreadFromPath(r.URL.Path); ok {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			threadChatID, err := chat.GetOrCreateCommentThread(r.Context(), userID, channelID, rootMessageID)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message":        i18n.Translate(locale, "status.ok"),
				"thread_chat_id": threadChatID,
			})
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
