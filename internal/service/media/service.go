package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeForbidden       = "forbidden"
	CodeInternal        = "internal"
	MaxAttachmentSize   = int64(5 * 1024 * 1024 * 1024) // 5 GiB
	playbackURLTTL      = 2 * time.Hour
)

type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

type Attachment struct {
	ID                 string     `json:"id"`
	UserID             string     `json:"user_id"`
	Filename           string     `json:"filename"`
	MimeType           string     `json:"mime_type"`
	Kind               string     `json:"kind"`
	Variant            string     `json:"variant"`
	IsClientCompressed bool       `json:"is_client_compressed"`
	SizeBytes          *int64     `json:"size_bytes,omitempty"`
	Width              *int       `json:"width,omitempty"`
	Height             *int       `json:"height,omitempty"`
	DurationMS         *int       `json:"duration_ms,omitempty"`
	Bucket             string     `json:"bucket"`
	ObjectKey          string     `json:"object_key"`
	UploadType         string     `json:"upload_type"`
	UploadID           *string    `json:"upload_id,omitempty"`
	ProcessingStatus   string     `json:"processing_status"`
	ProcessingError    *string    `json:"processing_error,omitempty"`
	PreviewObjectKey   *string    `json:"preview_object_key,omitempty"`
	HLSMasterObjectKey *string    `json:"hls_master_object_key,omitempty"`
	ProcessedAt        *time.Time `json:"processed_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type CreateAttachmentInput struct {
	UserID             string
	Filename           string
	MimeType           string
	Kind               string
	Variant            string
	IsClientCompressed bool
	SizeBytes          *int64
	Width              *int
	Height             *int
	DurationMS         *int
	PartsCount         int
}

type PartURLOutput struct {
	URL string `json:"url"`
}

type CompletePart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

type CreateAttachmentOutput struct {
	Attachment Attachment `json:"attachment"`
	Upload     struct {
		Type       string `json:"type"`
		UploadID   string `json:"upload_id"`
		PartsCount int    `json:"parts_count"`
	} `json:"upload"`
}

type MediaSession struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	AttachmentID  string     `json:"attachment_id"`
	Filename      string     `json:"filename"`
	MimeType      string     `json:"mime_type"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	PartsTotal    int        `json:"parts_total"`
	PartsUploaded int        `json:"parts_uploaded"`
	BytesTotal    *int64     `json:"bytes_total,omitempty"`
	BytesUploaded int64      `json:"bytes_uploaded"`
	PlaylistPath  *string    `json:"playlist_path,omitempty"`
	ErrorCode     *string    `json:"error_code,omitempty"`
	ErrorMessage  *string    `json:"error_message,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	FinalizedAt   *time.Time `json:"finalized_at,omitempty"`
}

type CreateSessionInput struct {
	UserID             string
	Filename           string
	MimeType           string
	Kind               string
	Variant            string
	IsClientCompressed bool
	SizeBytes          *int64
	Width              *int
	Height             *int
	DurationMS         *int
	PartsCount         int
}

type CreateSessionOutput struct {
	Session    MediaSession           `json:"session"`
	Attachment Attachment             `json:"attachment"`
	Upload     CreateAttachmentOutput `json:"upload"`
}

type Repository interface {
	CreateAttachment(ctx context.Context, a Attachment) (Attachment, error)
	GetAttachment(ctx context.Context, id string) (Attachment, error)
	CanUserAccessAttachment(ctx context.Context, userID, attachmentID string) (bool, error)
	SetAttachmentUploadID(ctx context.Context, id string, uploadID string) error
	SetProcessing(ctx context.Context, id string, status string, processingError *string, previewObjectKey *string, hlsMasterObjectKey *string, processedAt *time.Time) error
	CreateSession(ctx context.Context, s MediaSession) (MediaSession, error)
	GetSession(ctx context.Context, id string) (MediaSession, error)
	UpdateSessionProgress(ctx context.Context, id string, partsUploaded int, bytesUploaded int64) error
	MarkSessionFinalized(ctx context.Context, id string, status string, finalizedAt time.Time) error
	FinalizeSessionByAttachment(ctx context.Context, attachmentID string, status string, playlistPath *string, errorCode *string, errorMessage *string, finalizedAt time.Time) error
}

type ObjectStore interface {
	Bucket() string
	NewMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error)
	PresignUploadPart(ctx context.Context, objectKey, uploadID string, partNumber int, expires time.Duration) (string, error)
	CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []CompletePart, contentType string) error
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
	GetObject(ctx context.Context, objectKey string) (io.ReadCloser, error)
	PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error
	DeleteObject(ctx context.Context, objectKey string) error
}

type Service struct {
	repo  Repository
	store ObjectStore
}

var ErrAttachmentNotFound = errors.New("attachment not found")
var ErrMediaSessionNotFound = errors.New("media session not found")

var allowedStreamMIMEs = map[string]struct{}{
	"video/mp4":       {},
	"video/quicktime": {},
	"video/x-m4v":     {},
	"video/webm":      {},
	"video/ogg":       {},
	"audio/mpeg":      {},
	"audio/mp3":       {},
	"audio/aac":       {},
	"audio/mp4":       {},
	"audio/m4a":       {},
	"audio/ogg":       {},
	"audio/opus":      {},
	"audio/flac":      {},
	"audio/x-flac":    {},
	"audio/midi":      {},
	"audio/mid":       {},
	"audio/x-midi":    {},
	"audio/x-mid":     {},
	"audio/wav":       {},
	"audio/x-wav":     {},
	"audio/wave":      {},
	"audio/webm":      {},
	"application/ogg": {},
}

func New(repo Repository, store ObjectStore) (*Service, error) {
	if repo == nil {
		return nil, errors.New("media repository is required")
	}
	if store == nil {
		return nil, errors.New("object store is required")
	}
	return &Service{repo: repo, store: store}, nil
}

func (s *Service) CreateAttachment(ctx context.Context, input CreateAttachmentInput) (CreateAttachmentOutput, error) {
	userID := strings.TrimSpace(input.UserID)
	filename := strings.TrimSpace(input.Filename)
	mime := strings.TrimSpace(input.MimeType)
	kind := strings.TrimSpace(input.Kind)
	variant := strings.TrimSpace(input.Variant)
	if variant == "" {
		variant = "original"
	}
	if userID == "" || filename == "" || mime == "" || kind == "" {
		return CreateAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}
	if input.SizeBytes != nil && *input.SizeBytes > MaxAttachmentSize {
		return CreateAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input", Details: map[string]string{"size_bytes": "max_5_gb"}}
	}
	if (strings.HasPrefix(mime, "video/") || strings.HasPrefix(mime, "audio/") || mime == "application/ogg") && !strings.EqualFold(kind, "file") {
		if _, ok := allowedStreamMIMEs[strings.ToLower(mime)]; !ok {
			return CreateAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.unsupported_mime"}
		}
	}
	if input.PartsCount <= 0 || input.PartsCount > 10000 {
		return CreateAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	id := uuid.NewString()
	objectKey := "u/" + userID + "/" + id + "/" + filename

	uploadID, err := s.store.NewMultipartUpload(ctx, objectKey, mime)
	if err != nil {
		return CreateAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	a := Attachment{
		ID:                 id,
		UserID:             userID,
		Filename:           filename,
		MimeType:           mime,
		Kind:               kind,
		Variant:            variant,
		IsClientCompressed: input.IsClientCompressed,
		SizeBytes:          input.SizeBytes,
		Width:              input.Width,
		Height:             input.Height,
		DurationMS:         input.DurationMS,
		Bucket:             s.store.Bucket(),
		ObjectKey:          objectKey,
		UploadType:         "multipart",
		UploadID:           &uploadID,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
	created, err := s.repo.CreateAttachment(ctx, a)
	if err != nil {
		return CreateAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	var out CreateAttachmentOutput
	out.Attachment = created
	out.Upload.Type = "multipart"
	out.Upload.UploadID = uploadID
	out.Upload.PartsCount = input.PartsCount
	return out, nil
}

func (s *Service) PresignPart(ctx context.Context, requesterUserID, attachmentID, uploadID string, partNumber int) (PartURLOutput, error) {
	requesterUserID = strings.TrimSpace(requesterUserID)
	attachmentID = strings.TrimSpace(attachmentID)
	uploadID = strings.TrimSpace(uploadID)
	if requesterUserID == "" || attachmentID == "" || uploadID == "" || partNumber <= 0 {
		return PartURLOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	a, err := s.repo.GetAttachment(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, ErrAttachmentNotFound) {
			return PartURLOutput{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return PartURLOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if a.UserID != requesterUserID {
		return PartURLOutput{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}
	if a.UploadType != "multipart" || a.UploadID == nil || strings.TrimSpace(*a.UploadID) == "" {
		return PartURLOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}
	if strings.TrimSpace(*a.UploadID) != uploadID {
		return PartURLOutput{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}

	urlStr, err := s.store.PresignUploadPart(ctx, a.ObjectKey, uploadID, partNumber, 15*time.Minute)
	if err != nil {
		return PartURLOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	return PartURLOutput{URL: urlStr}, nil
}

func (s *Service) CompleteMultipart(ctx context.Context, requesterUserID, attachmentID, uploadID string, parts []CompletePart) (Attachment, error) {
	requesterUserID = strings.TrimSpace(requesterUserID)
	attachmentID = strings.TrimSpace(attachmentID)
	uploadID = strings.TrimSpace(uploadID)
	if requesterUserID == "" || attachmentID == "" || uploadID == "" || len(parts) == 0 {
		return Attachment{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	a, err := s.repo.GetAttachment(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, ErrAttachmentNotFound) {
			return Attachment{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return Attachment{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if a.UserID != requesterUserID {
		return Attachment{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}
	if a.UploadType != "multipart" || a.UploadID == nil || strings.TrimSpace(*a.UploadID) == "" {
		return Attachment{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}
	if strings.TrimSpace(*a.UploadID) != uploadID {
		return Attachment{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}

	if err := s.store.CompleteMultipartUpload(ctx, a.ObjectKey, uploadID, parts, a.MimeType); err != nil {
		return Attachment{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	go s.processPreviewAsync(context.Background(), a)
	return a, nil
}

type GetAttachmentOutput struct {
	Attachment Attachment `json:"attachment"`
	URL        string     `json:"url"`
	PreviewURL *string    `json:"preview_url,omitempty"`
}

type AttachmentDownloadOutput struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

func (s *Service) GetAttachment(ctx context.Context, requesterUserID, attachmentID string) (GetAttachmentOutput, error) {
	requesterUserID = strings.TrimSpace(requesterUserID)
	attachmentID = strings.TrimSpace(attachmentID)
	if requesterUserID == "" || attachmentID == "" {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	a, err := s.repo.GetAttachment(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, ErrAttachmentNotFound) {
			return GetAttachmentOutput{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if a.UserID != requesterUserID {
		allowed, accessErr := s.repo.CanUserAccessAttachment(ctx, requesterUserID, attachmentID)
		if accessErr != nil {
			return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: accessErr}
		}
		if !allowed {
			return GetAttachmentOutput{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
		}
	}

	urlStr := ""
	if (strings.EqualFold(a.Kind, "video") || strings.EqualFold(a.Kind, "audio")) && a.HLSMasterObjectKey != nil && strings.TrimSpace(*a.HLSMasterObjectKey) != "" {
		hlsURL, hlsErr := s.presignHLSPlaybackManifest(ctx, strings.TrimSpace(*a.HLSMasterObjectKey), playbackURLTTL)
		if hlsErr == nil && strings.TrimSpace(hlsURL) != "" {
			urlStr = hlsURL
		}
	}
	if strings.TrimSpace(urlStr) == "" {
		var err error
		urlStr, err = s.store.PresignGetObject(ctx, a.ObjectKey, playbackURLTTL)
		if err != nil {
			return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
		}
	}

	var previewURL *string
	if a.PreviewObjectKey != nil && strings.TrimSpace(*a.PreviewObjectKey) != "" {
		u, previewErr := s.store.PresignGetObject(ctx, strings.TrimSpace(*a.PreviewObjectKey), 15*time.Minute)
		if previewErr == nil {
			previewURL = &u
		}
	}

	return GetAttachmentOutput{Attachment: a, URL: urlStr, PreviewURL: previewURL}, nil
}

func (s *Service) presignHLSPlaybackManifest(ctx context.Context, masterKey string, ttl time.Duration) (string, error) {
	rc, err := s.store.GetObject(ctx, masterKey)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	baseDir := path.Dir(strings.TrimSpace(masterKey))
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var out strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#EXT-X-MAP:") {
			rewritten, rewriteErr := s.rewriteHLSMapLine(ctx, baseDir, line, ttl)
			if rewriteErr != nil {
				return "", rewriteErr
			}
			out.WriteString(rewritten)
			out.WriteByte('\n')
			continue
		}

		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			signed, signErr := s.signHLSReference(ctx, baseDir, trimmed, ttl)
			if signErr != nil {
				return "", signErr
			}
			out.WriteString(signed)
			out.WriteByte('\n')
			continue
		}

		out.WriteString(line)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	signedKey := path.Join(baseDir, "signed.m3u8")
	payload := out.String()
	if putErr := s.store.PutObject(ctx, signedKey, "application/vnd.apple.mpegurl", strings.NewReader(payload), int64(len(payload))); putErr != nil {
		return "", putErr
	}
	return s.store.PresignGetObject(ctx, signedKey, ttl)
}

func (s *Service) rewriteHLSMapLine(ctx context.Context, baseDir, line string, ttl time.Duration) (string, error) {
	const marker = `URI="`
	start := strings.Index(line, marker)
	if start < 0 {
		return line, nil
	}
	valueStart := start + len(marker)
	endRel := strings.Index(line[valueStart:], `"`)
	if endRel < 0 {
		return line, nil
	}
	valueEnd := valueStart + endRel
	ref := strings.TrimSpace(line[valueStart:valueEnd])
	if ref == "" {
		return line, nil
	}

	signed, err := s.signHLSReference(ctx, baseDir, ref, ttl)
	if err != nil {
		return "", err
	}
	return line[:valueStart] + signed + line[valueEnd:], nil
}

func (s *Service) signHLSReference(ctx context.Context, baseDir, ref string, ttl time.Duration) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref, nil
	}
	objectKey := path.Clean(path.Join(baseDir, ref))
	return s.store.PresignGetObject(ctx, objectKey, ttl)
}

func (s *Service) CreateSession(ctx context.Context, input CreateSessionInput) (CreateSessionOutput, error) {
	created, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		UserID:             input.UserID,
		Filename:           input.Filename,
		MimeType:           input.MimeType,
		Kind:               input.Kind,
		Variant:            input.Variant,
		IsClientCompressed: input.IsClientCompressed,
		SizeBytes:          input.SizeBytes,
		Width:              input.Width,
		Height:             input.Height,
		DurationMS:         input.DurationMS,
		PartsCount:         input.PartsCount,
	})
	if err != nil {
		return CreateSessionOutput{}, err
	}

	session := MediaSession{
		ID:            uuid.NewString(),
		UserID:        strings.TrimSpace(input.UserID),
		AttachmentID:  created.Attachment.ID,
		Filename:      created.Attachment.Filename,
		MimeType:      created.Attachment.MimeType,
		Kind:          created.Attachment.Kind,
		Status:        "uploading",
		PartsTotal:    input.PartsCount,
		BytesTotal:    input.SizeBytes,
		BytesUploaded: 0,
	}
	session, repoErr := s.repo.CreateSession(ctx, session)
	if repoErr != nil {
		return CreateSessionOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: repoErr}
	}

	return CreateSessionOutput{
		Session:    session,
		Attachment: created.Attachment,
		Upload:     created,
	}, nil
}

func (s *Service) PresignSessionPart(ctx context.Context, requesterUserID, sessionID string, partNumber int, _ string) (PartURLOutput, error) {
	session, attachment, err := s.getSessionOwned(ctx, requesterUserID, sessionID)
	if err != nil {
		return PartURLOutput{}, err
	}
	if session.Status != "uploading" {
		return PartURLOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}
	if attachment.UploadID == nil || strings.TrimSpace(*attachment.UploadID) == "" {
		return PartURLOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	out, err := s.PresignPart(ctx, requesterUserID, attachment.ID, strings.TrimSpace(*attachment.UploadID), partNumber)
	if err != nil {
		return PartURLOutput{}, err
	}
	partsUploaded := session.PartsUploaded
	if partNumber > partsUploaded {
		partsUploaded = partNumber
	}
	_ = s.repo.UpdateSessionProgress(ctx, session.ID, partsUploaded, session.BytesUploaded)
	return out, nil
}

func (s *Service) CompleteSession(ctx context.Context, requesterUserID, sessionID string, parts []CompletePart) (MediaSession, error) {
	session, attachment, err := s.getSessionOwned(ctx, requesterUserID, sessionID)
	if err != nil {
		return MediaSession{}, err
	}
	if attachment.UploadID == nil || strings.TrimSpace(*attachment.UploadID) == "" {
		return MediaSession{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}
	if _, err := s.CompleteMultipart(ctx, requesterUserID, attachment.ID, strings.TrimSpace(*attachment.UploadID), parts); err != nil {
		return MediaSession{}, err
	}
	finalStatus := "ready"
	if strings.EqualFold(session.Kind, "video") || strings.EqualFold(session.Kind, "audio") {
		finalStatus = "processing"
	}
	if repoErr := s.repo.MarkSessionFinalized(ctx, session.ID, finalStatus, time.Now().UTC()); repoErr != nil {
		return MediaSession{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: repoErr}
	}
	updated, repoErr := s.repo.GetSession(ctx, session.ID)
	if repoErr != nil {
		return MediaSession{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: repoErr}
	}
	return updated, nil
}

func (s *Service) GetSession(ctx context.Context, requesterUserID, sessionID string) (MediaSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	requesterUserID = strings.TrimSpace(requesterUserID)
	if sessionID == "" || requesterUserID == "" {
		return MediaSession{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}
	session, err := s.repo.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrMediaSessionNotFound) {
			return MediaSession{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return MediaSession{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if session.UserID != requesterUserID {
		return MediaSession{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}
	return session, nil
}

func (s *Service) getSessionOwned(ctx context.Context, requesterUserID, sessionID string) (MediaSession, Attachment, error) {
	session, err := s.GetSession(ctx, requesterUserID, sessionID)
	if err != nil {
		return MediaSession{}, Attachment{}, err
	}
	attachment, err := s.repo.GetAttachment(ctx, session.AttachmentID)
	if err != nil {
		if errors.Is(err, ErrAttachmentNotFound) {
			return MediaSession{}, Attachment{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return MediaSession{}, Attachment{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if attachment.UserID != requesterUserID {
		return MediaSession{}, Attachment{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}
	return session, attachment, nil
}
