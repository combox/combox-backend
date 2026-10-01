package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	blocksvc "combox-backend/internal/service/blocks"
)

// BlockService is the blocked-users surface used by the HTTP layer.
type BlockService interface {
	List(ctx context.Context, ownerID string) ([]blocksvc.Entry, error)
	Block(ctx context.Context, ownerID, targetID string) (blocksvc.Entry, error)
	Unblock(ctx context.Context, ownerID, targetID string) error
}

type blockRequest struct {
	UserID string `json:"user_id"`
}

// newBlockedListHandler serves
// GET /api/private/v1/profile/blocked -> the owner's block list, oldest first.
// POST /api/private/v1/profile/blocked {user_id} -> the created block entry.
func newBlockedListHandler(blocks BlockService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)

		switch r.Method {
		case http.MethodGet:
			entries, err := blocks.List(r.Context(), userID)
			if err != nil {
				writeBlockedServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"blocked": entries,
				"count":   len(entries),
			})
		case http.MethodPost:
			var req blockRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			entry, err := blocks.Block(r.Context(), userID, req.UserID)
			if err != nil {
				writeBlockedServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"blocked": entry,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

// newBlockedItemHandler serves
// DELETE /api/private/v1/profile/blocked/{userID} -> removes one block edge.
func newBlockedItemHandler(blocks BlockService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		targetID := strings.TrimSpace(r.PathValue("userID"))
		if targetID == "" {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.request.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if err := blocks.Unblock(r.Context(), userID, targetID); err != nil {
			writeBlockedServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"revoked": true,
		})
	}
}

// writeBlockedServiceError maps the typed blocks error onto HTTP: 400 for bad
// ids and self-blocks, 404 for unknown targets and missing edges, 500 for
// repository failures.
func writeBlockedServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *blocksvc.Error
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case blocksvc.CodeInvalidArgument:
			writeAPIError(w, r, http.StatusBadRequest, "error.validation", svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
			return
		case blocksvc.CodeNotFound:
			writeAPIError(w, r, http.StatusNotFound, "not_found", svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
			return
		}
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
