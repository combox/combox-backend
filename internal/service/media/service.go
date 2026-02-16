package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeForbidden       = "forbidden"
	CodeInternal        = "internal"
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

type Repository interface {
	CreateAttachment(ctx context.Context, a Attachment) (Attachment, error)
	GetAttachment(ctx context.Context, id string) (Attachment, error)
	SetAttachmentUploadID(ctx context.Context, id string, uploadID string) error
}

type ObjectStore interface {
	Bucket() string
	NewMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error)
	PresignUploadPart(ctx context.Context, objectKey, uploadID string, partNumber int, expires time.Duration) (string, error)
	CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []CompletePart, contentType string) error
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
}

type Service struct {
	repo  Repository
	store ObjectStore
}

var ErrAttachmentNotFound = errors.New("attachment not found")

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
	return a, nil
}

type GetAttachmentOutput struct {
	Attachment Attachment `json:"attachment"`
	URL        string     `json:"url"`
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
		return GetAttachmentOutput{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}

	urlStr, err := s.store.PresignGetObject(ctx, a.ObjectKey, 15*time.Minute)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	return GetAttachmentOutput{Attachment: a, URL: urlStr}, nil
}
