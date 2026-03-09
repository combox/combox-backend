package http

import (
	"net/http"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

type createStandaloneChannelRequest struct {
	Title      string `json:"title"`
	PublicSlug string `json:"public_slug"`
	IsPublic   *bool  `json:"is_public"`
}

var channelRoutePrefixes = []string{
	"/api/private/v1/channels/",
	"/api/private/v1/public-channels/",
}

func trimChannelRoutePrefix(path string) (string, bool) {
	path = strings.TrimSpace(path)
	for _, prefix := range channelRoutePrefixes {
		if strings.HasPrefix(path, prefix) {
			return strings.TrimPrefix(path, prefix), true
		}
	}
	return "", false
}

func channelIDOnlyFromPath(path string) (string, bool) {
	rest, ok := trimChannelRoutePrefix(path)
	if !ok {
		return "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 1 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

func channelActionFromPath(path string) (string, string, bool) {
	rest, ok := trimChannelRoutePrefix(path)
	if !ok {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func channelMembersFromPath(path string) (string, bool) {
	rest, ok := trimChannelRoutePrefix(path)
	if !ok {
		return "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "members" {
		return "", false
	}
	return parts[0], true
}

func channelMemberByUserFromPath(path string) (string, string, bool) {
	rest, ok := trimChannelRoutePrefix(path)
	if !ok {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "members" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func newChannelCollectionHandler(channels StandaloneChannelService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		var req createStandaloneChannelRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		created, err := channels.CreateStandaloneChannel(r.Context(), chatsvc.CreateStandaloneChannelInput{
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

func newChannelByIDHandler(channels StandaloneChannelService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if chatID, ok := channelMembersFromPath(r.URL.Path); ok {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			includeBanned := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_banned")), "1") ||
				strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_banned")), "true")
			items, err := channels.ListMembers(r.Context(), userID, chatID, includeBanned)
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

		if chatID, action, ok := channelActionFromPath(r.URL.Path); ok {
			switch {
			case action == "subscribe" && r.Method == http.MethodPost:
				updated, err := channels.SubscribeChannel(r.Context(), userID, chatID)
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
				if err := channels.UnsubscribeChannel(r.Context(), userID, chatID); err != nil {
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

		if chatID, targetUserID, ok := channelMemberByUserFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodPatch:
				var req updateMemberRoleRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				items, err := channels.UpdateMemberRole(r.Context(), userID, chatID, targetUserID, req.Role)
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
				items, err := channels.RemoveMember(r.Context(), userID, chatID, targetUserID)
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

		if chatID, ok := channelIDOnlyFromPath(r.URL.Path); ok {
			switch r.Method {
			case http.MethodGet:
				item, err := channels.GetChannel(r.Context(), userID, chatID)
				if err != nil {
					writeChatServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"chat": item})
			case http.MethodPatch:
				var req updateChatRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				updated, err := channels.UpdateChannel(r.Context(), chatsvc.UpdateChatInput{
					UserID:           userID,
					ChatID:           chatID,
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
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
			}
			return
		}

		writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
	}
}
