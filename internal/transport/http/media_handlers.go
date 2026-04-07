package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
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

type createMediaSessionRequest struct {
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

type sessionPartURLRequest struct {
	PartNumber  int    `json:"part_number"`
	ContentType string `json:"content_type"`
}

type completeSessionRequest struct {
	Parts []mediasvc.CompletePart `json:"parts"`
}

type importAttachmentURLRequest struct {
	SourceURL string `json:"source_url"`
	Filename  string `json:"filename"`
}

func sanitizeImportAttachmentSourceURL(raw string) (string, error) {
	source := strings.TrimSpace(raw)
	if source == "" {
		return "", errors.New("empty source_url")
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("unsupported scheme")
	}
	if parsed.User != nil {
		return "", errors.New("userinfo is not allowed")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(strings.ToLower(host), ".local") {
		return "", errors.New("prohibited host")
	}
	if net.ParseIP(host) != nil {
		return "", errors.New("ip literal host is not allowed")
	}
	return source, nil
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

func attachmentDownloadURLFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/media/attachments/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "download-url" {
		return "", false
	}
	return parts[0], true
}

func mediaSessionIDFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/media/sessions/"
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

func mediaSessionPartURLFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/media/sessions/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "part-url" {
		return "", false
	}
	return parts[0], true
}

func mediaSessionCompleteFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/media/sessions/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "complete" {
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
		if strings.TrimSpace(r.URL.Path) == "/api/private/v1/media/attachments/import-url" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req importAttachmentURLRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			sourceURL, err := sanitizeImportAttachmentSourceURL(req.SourceURL)
			if err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.media.invalid_input", nil, i18n, defaultLocale)
				return
			}
			out, err := svc.ImportFromURL(r.Context(), mediasvc.ImportFromURLInput{
				UserID:    userID,
				SourceURL: sourceURL,
				Filename:  req.Filename,
			})
			if err != nil {
				writeMediaServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusCreated, map[string]any{
				"message":     i18n.Translate(locale, "media.attachment.create.success"),
				"attachment":  out.Attachment,
				"url":         out.URL,
				"preview_url": out.PreviewURL,
			})
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

		if id, ok := attachmentDownloadURLFromPath(r.URL.Path); ok {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			out, err := svc.CreateDownloadURL(r.Context(), userID, id)
			if err != nil {
				writeMediaServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message":  i18n.Translate(locale, "media.attachment.get.success"),
				"url":      out.URL,
				"filename": out.Filename,
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
				"message":     i18n.Translate(locale, "media.attachment.get.success"),
				"attachment":  out.Attachment,
				"url":         out.URL,
				"preview_url": out.PreviewURL,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

func newMediaSessionsHandler(svc MediaService, i18n Translator, defaultLocale string) http.HandlerFunc {
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

		var req createMediaSessionRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		partsCount := 0
		if req.Multipart != nil {
			partsCount = req.Multipart.PartsCount
		}
		out, err := svc.CreateSession(r.Context(), mediasvc.CreateSessionInput{
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

func newMediaSessionByIDHandler(svc MediaService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if sessionID, ok := mediaSessionPartURLFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req sessionPartURLRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			out, err := svc.PresignSessionPart(r.Context(), userID, sessionID, req.PartNumber, req.ContentType)
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

		if sessionID, ok := mediaSessionCompleteFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			var req completeSessionRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			session, err := svc.CompleteSession(r.Context(), userID, sessionID, req.Parts)
			if err != nil {
				writeMediaServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "media.multipart.complete.success"),
				"session": session,
			})
			return
		}

		sessionID, ok := mediaSessionIDFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}

		switch r.Method {
		case http.MethodGet:
			session, err := svc.GetSession(r.Context(), userID, sessionID)
			if err != nil {
				writeMediaServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "media.attachment.get.success"),
				"session": session,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

type MediaService interface {
	CreateAttachment(ctx context.Context, input mediasvc.CreateAttachmentInput) (mediasvc.CreateAttachmentOutput, error)
	ImportFromURL(ctx context.Context, input mediasvc.ImportFromURLInput) (mediasvc.GetAttachmentOutput, error)
	PresignPart(ctx context.Context, requesterUserID, attachmentID, uploadID string, partNumber int) (mediasvc.PartURLOutput, error)
	CompleteMultipart(ctx context.Context, requesterUserID, attachmentID, uploadID string, parts []mediasvc.CompletePart) (mediasvc.Attachment, error)
	GetAttachment(ctx context.Context, requesterUserID, attachmentID string) (mediasvc.GetAttachmentOutput, error)
	CreateDownloadURL(ctx context.Context, requesterUserID, attachmentID string) (mediasvc.AttachmentDownloadOutput, error)
	CreateSession(ctx context.Context, input mediasvc.CreateSessionInput) (mediasvc.CreateSessionOutput, error)
	PresignSessionPart(ctx context.Context, requesterUserID, sessionID string, partNumber int, contentType string) (mediasvc.PartURLOutput, error)
	CompleteSession(ctx context.Context, requesterUserID, sessionID string, parts []mediasvc.CompletePart) (mediasvc.MediaSession, error)
	GetSession(ctx context.Context, requesterUserID, sessionID string) (mediasvc.MediaSession, error)
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
