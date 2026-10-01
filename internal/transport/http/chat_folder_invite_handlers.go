package http

import (
	"net/http"
	"strings"
)

// newChatFolderInviteHandler serves the per-folder share-link routes (the
// folder id is the singular /chat-folder/ prefix, like the other per-folder
// routes, so the patterns never collide with the resolve route below):
//
//	POST   /api/private/v1/chat-folder/{folderID}/invite -> {message, token}
//	DELETE /api/private/v1/chat-folder/{folderID}/invite -> {message}
func newChatFolderInviteHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)
		folderID := strings.TrimSpace(r.PathValue("folderID"))

		switch r.Method {
		case http.MethodPost:
			invite, err := chat.CreateChatFolderInvite(r.Context(), userID, folderID)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{
				"message": i18n.Translate(locale, "chat.folder.invite.create.success"),
				"token":   invite.Token,
			})
		case http.MethodDelete:
			if err := chat.RevokeChatFolderInvite(r.Context(), userID, folderID); err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "chat.folder.invite.revoke.success"),
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

// newChatFolderInviteResolveHandler serves the collection level preview route
// (plural /chat-folders/ prefix, next to the folder collection route):
//
//	GET /api/private/v1/chat-folders/invite/{token} -> {message, folder_name, folder_emoji, chats}
//
// A missing or revoked token reads as 404. The server never auto-joins: the
// client renders an import dialog from the payload.
func newChatFolderInviteResolveHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)
		resolved, err := chat.ResolveChatFolderInvite(r.Context(), userID, strings.TrimSpace(r.PathValue("token")))
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message":      i18n.Translate(locale, "chat.folder.invite.resolve.success"),
			"folder_name":  resolved.FolderName,
			"folder_emoji": resolved.FolderEmoji,
			"chats":        resolved.Chats,
		})
	}
}
