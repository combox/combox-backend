package http

import (
	"net/http"
	"strings"

	chatsvc "combox-backend/internal/service/chat"
)

type createChatFolderRequest struct {
	Name    string   `json:"name"`
	Icon    string   `json:"icon"`
	ChatIDs []string `json:"chat_ids"`
}

type updateChatFolderRequest struct {
	Name     *string `json:"name"`
	Icon     *string `json:"icon"`
	Position *int    `json:"position"`
}

type setChatFolderChatsRequest struct {
	ChatIDs []string `json:"chat_ids"`
}

// newChatFoldersHandler serves the collection routes:
//
//	GET  /api/private/v1/chat-folders  -> every folder of the caller
//	POST /api/private/v1/chat-folders  -> the created folder
func newChatFoldersHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)

		switch r.Method {
		case http.MethodGet:
			items, err := chat.ListChatFolders(r.Context(), userID)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "chat.folder.list.success"),
				"items":   items,
			})
		case http.MethodPost:
			var req createChatFolderRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			created, err := chat.CreateChatFolder(r.Context(), chatsvc.CreateChatFolderInput{
				UserID:  userID,
				Name:    req.Name,
				Icon:    req.Icon,
				ChatIDs: req.ChatIDs,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{
				"message": i18n.Translate(locale, "chat.folder.create.success"),
				"item":    created,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

// newChatFolderItemHandler serves the per-folder routes:
//
//	PATCH  /api/private/v1/chat-folder/{folderID} -> the patched folder
//	DELETE /api/private/v1/chat-folder/{folderID} -> acknowledgement
func newChatFolderItemHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)
		folderID := strings.TrimSpace(r.PathValue("folderID"))

		switch r.Method {
		case http.MethodPatch:
			var req updateChatFolderRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			input := chatsvc.UpdateChatFolderInput{
				UserID:   userID,
				FolderID: folderID,
				Name:     chatsvc.OptionalString{Set: req.Name != nil, Value: req.Name},
				Icon:     chatsvc.OptionalString{Set: req.Icon != nil, Value: req.Icon},
				Position: chatsvc.OptionalInt{Set: req.Position != nil, Value: derefInt(req.Position)},
			}
			updated, err := chat.UpdateChatFolder(r.Context(), input)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "chat.folder.update.success"),
				"item":    updated,
			})
		case http.MethodDelete:
			if err := chat.DeleteChatFolder(r.Context(), userID, folderID); err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "chat.folder.delete.success"),
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

// newChatFolderChatsHandler serves:
//
//	PUT /api/private/v1/chat-folder/{folderID}/chats -> the folder with its new chat set
func newChatFolderChatsHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		var req setChatFolderChatsRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		updated, err := chat.SetChatFolderChats(r.Context(), userID, strings.TrimSpace(r.PathValue("folderID")), req.ChatIDs)
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(requestLocale(r, defaultLocale), "chat.folder.update.success"),
			"item":    updated,
		})
	}
}
