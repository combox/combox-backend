package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"combox-backend/internal/calls"
)

// callHistoryProvider is the history surface of the calls subsystem. It is a
// separate interface so the shared CallService contract stays untouched; the
// handler type-asserts against it.
type callHistoryProvider interface {
	ListChatCalls(ctx context.Context, userID, chatID string, limit int) ([]calls.ChatCall, error)
	DeleteChatCall(ctx context.Context, userID, chatID, callID string) error
}

// chatCallsFromPath extracts the chat id from GET /api/private/v1/chats/{chatID}/calls.
func chatCallsFromPath(path string) (string, bool) {
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
	if parts[0] == "" || parts[1] != "calls" {
		return "", false
	}
	return parts[0], true
}

// chatCallItemFromPath extracts the chat and call ids from
// DELETE /api/private/v1/chats/{chatID}/calls/{callID}.
func chatCallItemFromPath(path string) (string, string, bool) {
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
	if parts[0] == "" || parts[1] != "calls" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// newChatCallsHandler lists the past calls of a chat as rows for the feed.
func newChatCallsHandler(chat ChatService, svc CallService, i18n Translator, defaultLocale string) http.HandlerFunc {
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
		chatID, ok := chatCallsFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.chat.not_found", nil, i18n, defaultLocale)
			return
		}
		history, ok := svc.(callHistoryProvider)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}

		if _, err := chat.GetChat(r.Context(), userID, chatID); err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
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
		if limit <= 0 {
			limit = 50
		}
		if limit > 200 {
			limit = 200
		}

		items, err := history.ListChatCalls(r.Context(), userID, chatID, limit)
		if err != nil {
			if errors.Is(err, calls.ErrNotMember) {
				writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.chat.forbidden", nil, i18n, defaultLocale)
				return
			}
			slog.Default().Error("chat call history error",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Any("error", err),
			)
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"items":   items,
		})
	}
}

// newChatCallItemHandler handles per-row history actions. Deleting a row is
// what the client does to clear a service message (missed/incoming/outgoing
// call, live stream) from the chat feed.
func newChatCallItemHandler(chat ChatService, svc CallService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		chatID, callID, ok := chatCallItemFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}
		if r.Method != http.MethodDelete {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		history, ok := svc.(callHistoryProvider)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}
		if _, err := chat.GetChat(r.Context(), userID, chatID); err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		if err := history.DeleteChatCall(r.Context(), userID, chatID, callID); err != nil {
			if errors.Is(err, calls.ErrNotMember) {
				writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.chat.forbidden", nil, i18n, defaultLocale)
				return
			}
			if errors.Is(err, calls.ErrCallNotFound) {
				writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
				return
			}
			slog.Default().Error("chat call delete error",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Any("error", err),
			)
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
