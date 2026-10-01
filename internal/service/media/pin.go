package media

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// pinnedObjectKey reuses the upload key convention (u/<owner>/<id>/<file>)
// so presigned URLs of pinned copies keep resolving through the same
// attachment-id extraction the playlist already relies on.
func pinnedObjectKey(userID, attachmentID, filename string) string {
	name := sanitizeFilename(filename)
	if name == "" {
		name = "track"
	}
	return "u/" + userID + "/" + attachmentID + "/" + name
}

// PinAttachment gives the caller an owned server-side copy of an attachment
// they are allowed to read (owner or chat member via the [[att:]] token).
// The copy lives under the caller's own key prefix with a fresh attachment
// row, so deleting the source message — or the whole source chat — never
// touches it: playback resolves through ownership, not chat membership.
//
// There is no refcount/GC to bump: message deletes are soft (deleted_at) and
// nothing ever sweeps attachments or MinIO objects, so a pin flag would guard
// against a collector that does not exist. Ownership of a copy is the whole
// mechanism.
func (s *Service) PinAttachment(ctx context.Context, requesterUserID, attachmentID string) (GetAttachmentOutput, error) {
	requesterUserID = strings.TrimSpace(requesterUserID)
	attachmentID = strings.TrimSpace(attachmentID)
	if requesterUserID == "" || attachmentID == "" {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	src, err := s.repo.GetAttachment(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, ErrAttachmentNotFound) {
			return GetAttachmentOutput{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	// Pinning an already owned copy is a no-op returning a fresh URL.
	if src.UserID == requesterUserID {
		return s.GetAttachment(ctx, requesterUserID, src.ID)
	}
	allowed, accessErr := s.repo.CanUserAccessAttachment(ctx, requesterUserID, src.ID)
	if accessErr != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: accessErr}
	}
	if !allowed {
		return GetAttachmentOutput{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
	}

	now := time.Now().UTC()
	pinned := Attachment{
		ID:                 uuid.NewString(),
		UserID:             requesterUserID,
		Filename:           strings.TrimSpace(src.Filename),
		MimeType:           strings.TrimSpace(src.MimeType),
		Kind:               strings.TrimSpace(src.Kind),
		Variant:            strings.TrimSpace(src.Variant),
		IsClientCompressed: src.IsClientCompressed,
		SizeBytes:          src.SizeBytes,
		Width:              src.Width,
		Height:             src.Height,
		DurationMS:         src.DurationMS,
		Bucket:             s.store.Bucket(),
		UploadType:         "pin",
		// Processing status/preview/HLS keys are intentionally left zero: the
		// INSERT keeps DB defaults and playback uses the direct object URL.
		UserMeta:  src.UserMeta,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if pinned.Filename == "" {
		pinned.Filename = "track"
	}
	if pinned.Variant == "" {
		pinned.Variant = "original"
	}
	pinned.ObjectKey = pinnedObjectKey(requesterUserID, pinned.ID, pinned.Filename)

	if err := s.store.CopyObject(ctx, strings.TrimSpace(src.ObjectKey), pinned.ObjectKey); err != nil {
		if isMissingObjectError(err) {
			return GetAttachmentOutput{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	// NOTE: the attachments INSERT only carries the core columns (preview and
	// HLS keys keep their DB defaults), so no preview/HLS state is copied.
	// The pinned row plays through the direct object URL, which GetAttachment
	// already prefers when no HLS master key is set. Audio (the playlist
	// case) never has HLS anyway.

	created, err := s.repo.CreateAttachment(ctx, pinned)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	// The INSERT keeps DB defaults for user_meta, so carry voice/video-note
	// metadata (waveform, round flag) over explicitly; the row is owned by
	// the requester, which is exactly what SetAttachmentUserMeta requires.
	if len(src.UserMeta) > 0 {
		if metaErr := s.repo.SetAttachmentUserMeta(ctx, requesterUserID, created.ID, src.UserMeta); metaErr != nil {
			return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: metaErr}
		}
	}
	return s.GetAttachment(ctx, requesterUserID, created.ID)
}

// isMissingObjectError spots a copy from a source object that is already gone
// (e.g. a video original removed after HLS transcoding, or an uploader whose
// account deletion cascaded the row but left the request racing).
func isMissingObjectError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"nosuchkey", "no such key", "notfound", "not found", "does not exist"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
