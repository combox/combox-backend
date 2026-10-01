package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	profilephotosvc "combox-backend/internal/service/profilephoto"
)

// ProfilePhotoList is the read surface of the avatar/photo history gallery.
// The privacy gates live behind it, so the handler only maps errors.
type ProfilePhotoList interface {
	List(ctx context.Context, viewerID, ownerKind, ownerID string) ([]profilephotosvc.Photo, error)
}

type profilePhotoItem struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	// Migrated marks rows the boxchat ETL backfilled: CreatedAt is the
	// migration moment, not the original install moment (legacy stores no
	// avatar timestamp), so the viewer labels them "from boxchat".
	Migrated bool `json:"migrated"`
}

func profilePhotoItems(photos []profilephotosvc.Photo) []profilePhotoItem {
	out := make([]profilePhotoItem, 0, len(photos))
	for _, photo := range photos {
		out = append(out, profilePhotoItem{
			ID:        photo.ID,
			URL:       photo.URL,
			CreatedAt: photo.CreatedAt.UTC().Format(time.RFC3339),
			Migrated:  photo.Migrated,
		})
	}
	return out
}

// userPhotosFromPath extracts the user id from
// GET /api/private/v1/users/{userID}/photos.
func userPhotosFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/users/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "photos" {
		return "", false
	}
	return parts[0], true
}

// chatPhotosFromPath extracts the chat id from
// GET /api/private/v1/chats/{chatID}/photos.
func chatPhotosFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/chats/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "photos" {
		return "", false
	}
	return parts[0], true
}

func writeProfilePhotoError(w http.ResponseWriter, r *http.Request, err error, notFoundKey, forbiddenKey string, i18n Translator, defaultLocale string) {
	switch {
	case errors.Is(err, profilephotosvc.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, "not_found", notFoundKey, nil, i18n, defaultLocale)
	case errors.Is(err, profilephotosvc.ErrForbidden):
		writeAPIError(w, r, http.StatusForbidden, "forbidden", forbiddenKey, nil, i18n, defaultLocale)
	default:
		slog.Default().Error("profile photo history error",
			slog.String("request_id", RequestIDFromContext(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Any("error", err),
		)
		writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
	}
}

// newUserPhotosHandler lists the photo history of GET /users/{userID}/photos.
// Visibility mirrors the profile itself: whoever may open the profile may
// open the history (the gate lives in the service).
func newUserPhotosHandler(photos ProfilePhotoList, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		viewerID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if viewerID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		userID, ok := userPhotosFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}

		items, err := photos.List(r.Context(), viewerID, profilephotosvc.OwnerUser, userID)
		if err != nil {
			writeProfilePhotoError(w, r, err, "error.request.not_found", "error.media.forbidden", i18n, defaultLocale)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"photos": profilePhotoItems(items),
		})
	}
}

// newChatPhotosHandler lists the photo history of
// GET /chats/{chatID}/photos. Members only, enforced through the same
// membership semantics as reading the chat itself.
func newChatPhotosHandler(photos ProfilePhotoList, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		viewerID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if viewerID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		chatID, ok := chatPhotosFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.chat.not_found", nil, i18n, defaultLocale)
			return
		}

		items, err := photos.List(r.Context(), viewerID, profilephotosvc.OwnerChat, chatID)
		if err != nil {
			writeProfilePhotoError(w, r, err, "error.chat.not_found", "error.chat.forbidden", i18n, defaultLocale)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"photos": profilePhotoItems(items),
		})
	}
}
