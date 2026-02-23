package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"combox-backend/internal/service/media"

	"github.com/jackc/pgx/v5"
)

type MediaRepository struct {
	client *Client
}

func NewMediaRepository(client *Client) *MediaRepository {
	return &MediaRepository{client: client}
}

func (r *MediaRepository) CreateAttachment(ctx context.Context, a media.Attachment) (media.Attachment, error) {
	const q = `
		INSERT INTO attachments (
			id, user_id, filename, mime_type, kind, variant, is_client_compressed,
			size_bytes, width, height, duration_ms,
			bucket, object_key, upload_type, upload_id
		)
		VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, $6, $7,
			$8, $9, $10, $11,
			$12, $13, $14, $15
		)
		RETURNING id::text, user_id::text, filename, mime_type, kind, variant, is_client_compressed,
			size_bytes, width, height, duration_ms,
			bucket, object_key, upload_type, upload_id,
			processing_status, processing_error, preview_object_key, hls_master_object_key, processed_at,
			created_at, updated_at
	`

	var out media.Attachment
	var uploadID *string
	if strings.TrimSpace(a.UploadType) == "multipart" {
		uploadID = a.UploadID
	}
	err := r.client.pool.QueryRow(
		ctx,
		q,
		strings.TrimSpace(a.ID),
		strings.TrimSpace(a.UserID),
		strings.TrimSpace(a.Filename),
		strings.TrimSpace(a.MimeType),
		strings.TrimSpace(a.Kind),
		strings.TrimSpace(a.Variant),
		a.IsClientCompressed,
		a.SizeBytes,
		a.Width,
		a.Height,
		a.DurationMS,
		strings.TrimSpace(a.Bucket),
		strings.TrimSpace(a.ObjectKey),
		strings.TrimSpace(a.UploadType),
		uploadID,
	).Scan(
		&out.ID,
		&out.UserID,
		&out.Filename,
		&out.MimeType,
		&out.Kind,
		&out.Variant,
		&out.IsClientCompressed,
		&out.SizeBytes,
		&out.Width,
		&out.Height,
		&out.DurationMS,
		&out.Bucket,
		&out.ObjectKey,
		&out.UploadType,
		&out.UploadID,
		&out.ProcessingStatus,
		&out.ProcessingError,
		&out.PreviewObjectKey,
		&out.HLSMasterObjectKey,
		&out.ProcessedAt,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return media.Attachment{}, err
	}
	return out, nil
}

func (r *MediaRepository) GetAttachment(ctx context.Context, id string) (media.Attachment, error) {
	const q = `
		SELECT id::text, user_id::text, filename, mime_type, kind, variant, is_client_compressed,
			size_bytes, width, height, duration_ms,
			bucket, object_key, upload_type, upload_id,
			processing_status, processing_error, preview_object_key, hls_master_object_key, processed_at,
			created_at, updated_at
		FROM attachments
		WHERE id = $1::uuid
		LIMIT 1
	`

	var out media.Attachment
	id = strings.TrimSpace(id)
	if err := r.client.pool.QueryRow(ctx, q, id).Scan(
		&out.ID,
		&out.UserID,
		&out.Filename,
		&out.MimeType,
		&out.Kind,
		&out.Variant,
		&out.IsClientCompressed,
		&out.SizeBytes,
		&out.Width,
		&out.Height,
		&out.DurationMS,
		&out.Bucket,
		&out.ObjectKey,
		&out.UploadType,
		&out.UploadID,
		&out.ProcessingStatus,
		&out.ProcessingError,
		&out.PreviewObjectKey,
		&out.HLSMasterObjectKey,
		&out.ProcessedAt,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return media.Attachment{}, media.ErrAttachmentNotFound
		}
		return media.Attachment{}, err
	}
	return out, nil
}

func (r *MediaRepository) CanUserAccessAttachment(ctx context.Context, userID, attachmentID string) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1
			FROM attachments a
			INNER JOIN messages m
				ON m.content LIKE ('%[[att:' || a.id::text || '|%')
			INNER JOIN chat_members cm
				ON cm.chat_id = m.chat_id
			WHERE a.id = $1::uuid
			  AND cm.user_id = $2::uuid
		)
	`
	var allowed bool
	if err := r.client.pool.QueryRow(ctx, q, strings.TrimSpace(attachmentID), strings.TrimSpace(userID)).Scan(&allowed); err != nil {
		return false, err
	}
	return allowed, nil
}

func (r *MediaRepository) SetAttachmentUploadID(ctx context.Context, id string, uploadID string) error {
	const q = `
		UPDATE attachments
		SET upload_id = $2, updated_at = NOW()
		WHERE id = $1::uuid
	`
	_, err := r.client.pool.Exec(ctx, q, strings.TrimSpace(id), strings.TrimSpace(uploadID))
	return err
}

func (r *MediaRepository) SetProcessing(ctx context.Context, id string, status string, processingError *string, previewObjectKey *string, processedAt *time.Time) error {
	const q = `
		UPDATE attachments
		SET processing_status = $2,
		    processing_error = $3,
		    preview_object_key = $4,
		    processed_at = $5,
		    updated_at = NOW()
		WHERE id = $1::uuid
	`
	_, err := r.client.pool.Exec(
		ctx,
		q,
		strings.TrimSpace(id),
		strings.TrimSpace(status),
		processingError,
		previewObjectKey,
		processedAt,
	)
	return err
}
