package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	importURLTimeout  = 30 * time.Second
	maxImportURLBytes = int64(64 * 1024 * 1024)
)

type ImportFromURLInput struct {
	UserID    string
	SourceURL string
	Filename  string
}

func normalizeImportSourceURL(raw string) string {
	source := strings.TrimSpace(raw)
	if source == "" {
		return ""
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return source
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	if host == "giphy.com" || host == "www.giphy.com" {
		segments := strings.Split(strings.Trim(strings.TrimSpace(parsed.Path), "/"), "/")
		for idx := len(segments) - 1; idx >= 0; idx-- {
			part := strings.TrimSpace(segments[idx])
			if part == "" {
				continue
			}
			pieces := strings.Split(part, "-")
			id := strings.TrimSpace(pieces[len(pieces)-1])
			if id == "" {
				continue
			}
			return "https://media.giphy.com/media/" + id + "/giphy.gif"
		}
	}
	return source
}

func inferImportedKind(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		if _, ok := allowedStreamMIMEs[mimeType]; ok {
			return "video"
		}
		return "file"
	case strings.HasPrefix(mimeType, "audio/") || mimeType == "application/ogg":
		if _, ok := allowedStreamMIMEs[mimeType]; ok {
			return "audio"
		}
		return "file"
	default:
		return "file"
	}
}

func filenameFromURL(parsed *url.URL, fallback string) string {
	if parsed == nil {
		return fallback
	}
	name := strings.TrimSpace(filepath.Base(parsed.Path))
	if name == "" || name == "." || name == "/" {
		return fallback
	}
	return name
}

func extensionFromMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/gif":
		return ".gif"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		return ""
	}
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '.', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	safe := strings.Trim(strings.TrimSpace(string(out)), "._")
	if safe == "" {
		return ""
	}
	return safe
}

func (s *Service) ImportFromURL(ctx context.Context, input ImportFromURLInput) (GetAttachmentOutput, error) {
	userID := strings.TrimSpace(input.UserID)
	sourceURL := normalizeImportSourceURL(input.SourceURL)
	filename := strings.TrimSpace(input.Filename)
	if userID == "" || sourceURL == "" {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	parsedURL, err := url.Parse(sourceURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || strings.TrimSpace(parsedURL.Host) == "" {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	reqCtx, cancel := context.WithTimeout(ctx, importURLTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input", Cause: err}
	}
	req.Header.Set("User-Agent", "combox-backend-media-import/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	tmpPath, cleanup, err := tempPath(".import")
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	defer cleanup()

	tmp, err := os.Create(tmpPath)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	var total int64
	var sniff bytes.Buffer
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			total += int64(n)
			if total > maxImportURLBytes {
				_ = tmp.Close()
				return GetAttachmentOutput{}, &Error{
					Code:       CodeInvalidArgument,
					MessageKey: "error.media.invalid_input",
					Details:    map[string]string{"size_bytes": "max_64_mb"},
				}
			}
			if sniff.Len() < 512 {
				remain := 512 - sniff.Len()
				if remain > len(chunk) {
					remain = len(chunk)
				}
				_, _ = sniff.Write(chunk[:remain])
			}
			if _, writeErr := tmp.Write(chunk); writeErr != nil {
				_ = tmp.Close()
				return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: writeErr}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = tmp.Close()
			return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: readErr}
		}
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: closeErr}
	}
	if total <= 0 {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mimeType == "" {
		mimeType = strings.ToLower(strings.TrimSpace(http.DetectContentType(sniff.Bytes())))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	if filename == "" {
		filename = filenameFromURL(parsedURL, "")
	}
	filename = sanitizeFilename(filename)
	if filename == "" {
		filename = "imported_" + uuid.NewString()
	}
	if filepath.Ext(filename) == "" {
		if ext := extensionFromMime(mimeType); ext != "" {
			filename += ext
		}
	}

	kind := inferImportedKind(mimeType)
	id := uuid.NewString()
	objectKey := fmt.Sprintf("u/%s/%s/%s", userID, id, filename)

	file, err := os.Open(tmpPath)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	defer file.Close()
	if err := s.store.PutObject(ctx, objectKey, mimeType, file, total); err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	size := total
	created, err := s.repo.CreateAttachment(ctx, Attachment{
		ID:                 id,
		UserID:             userID,
		Filename:           filename,
		MimeType:           mimeType,
		Kind:               kind,
		Variant:            "original",
		IsClientCompressed: false,
		SizeBytes:          &size,
		Bucket:             s.store.Bucket(),
		ObjectKey:          objectKey,
		UploadType:         "server_import",
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	})
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	go s.processPreviewAsync(context.Background(), created)
	return s.GetAttachment(ctx, userID, created.ID)
}
