package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	mediasvc "combox-backend/internal/service/media"
)

type createAttachmentRequest struct {
	Filename           string `json:"filename"`
	MimeType           string `json:"mime_type"`
	Kind               string `json:"kind"`
	Variant            string `json:"variant"`
	IsClientCompressed bool   `json:"is_client_compressed"`
	SizeBytes          *int64 `json:"size_bytes"`
	Width              *int   `json:"width"`
	Height             *int   `json:"height"`
	DurationMS         *int   `json:"duration_ms"`
	Multipart          *struct {
		PartsCount int `json:"parts_count"`
	} `json:"multipart"`
}

type partURLRequest struct {
	UploadID    string `json:"upload_id"`
	PartNumber  int    `json:"part_number"`
	ContentType string `json:"content_type"`
}

type completeMultipartRequest struct {
	UploadID string                  `json:"upload_id"`
	Parts    []mediasvc.CompletePart `json:"parts"`
}

func attachmentIDFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/media/attachments/"
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

func attachmentPartURLFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/media/attachments/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "multipart" || parts[2] != "part-url" {
		return "", false
	}
	return parts[0], true
}

func attachmentCompleteFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/media/attachments/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "multipart" || parts[2] != "complete" {
		return "", false
	}
	return parts[0], true
}

func newMediaAttachmentsHandler(svc MediaService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		var req createAttachmentRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		partsCount := 0
		if req.Multipart != nil {
			partsCount = req.Multipart.PartsCount
		}
		out, err := svc.CreateAttachment(r.Context(), mediasvc.CreateAttachmentInput{
			UserID:             userID,
			Filename:           req.Filename,
			MimeType:           req.MimeType,
			Kind:               req.Kind,
			Variant:            req.Variant,
			IsClientCompressed: req.IsClientCompressed,
			SizeBytes:          req.SizeBytes,
			Width:              req.Width,
			Height:             req.Height,
			DurationMS:         req.DurationMS,
			PartsCount:         partsCount,
		})
		if err != nil {
			writeMediaServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "media.attachment.create.success"),
			"result":  out,
		})
	}
}

func newMediaAttachmentByIDHandler(svc MediaService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if id, ok := attachmentPartURLFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req partURLRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			out, err := svc.PresignPart(r.Context(), userID, id, req.UploadID, req.PartNumber)
			if err != nil {
				writeMediaServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "media.multipart.part_url.success"),
				"url":     out.URL,
			})
			return
		}

		if id, ok := attachmentCompleteFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req completeMultipartRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			item, err := svc.CompleteMultipart(r.Context(), userID, id, req.UploadID, req.Parts)
			if err != nil {
				writeMediaServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message":    i18n.Translate(locale, "media.multipart.complete.success"),
				"attachment": item,
			})
			return
		}

		id, ok := attachmentIDFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}

		switch r.Method {
		case http.MethodGet:
			out, err := svc.GetAttachment(r.Context(), userID, id)
			if err != nil {
				writeMediaServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message":    i18n.Translate(locale, "media.attachment.get.success"),
				"attachment": out.Attachment,
				"url":        out.URL,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

type MediaService interface {
	CreateAttachment(ctx context.Context, input mediasvc.CreateAttachmentInput) (mediasvc.CreateAttachmentOutput, error)
	PresignPart(ctx context.Context, requesterUserID, attachmentID, uploadID string, partNumber int) (mediasvc.PartURLOutput, error)
	CompleteMultipart(ctx context.Context, requesterUserID, attachmentID, uploadID string, parts []mediasvc.CompletePart) (mediasvc.Attachment, error)
	GetAttachment(ctx context.Context, requesterUserID, attachmentID string) (mediasvc.GetAttachmentOutput, error)
}

func writeMediaServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *mediasvc.Error
	if errors.As(err, &svcErr) {
		status := http.StatusInternalServerError
		switch svcErr.Code {
		case mediasvc.CodeInvalidArgument:
			status = http.StatusBadRequest
		case mediasvc.CodeNotFound:
			status = http.StatusNotFound
		case mediasvc.CodeForbidden:
			status = http.StatusForbidden
		}
		writeAPIError(w, r, status, svcErr.Code, svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
		return
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}

func parseIntQuery(r *http.Request, key string) (int, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}
