package media

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	downloadURLTTL      = 5 * time.Minute
	downloadCleanupWait = 20 * time.Minute
)

func (s *Service) CreateDownloadURL(ctx context.Context, requesterUserID, attachmentID string) (AttachmentDownloadOutput, error) {
	requesterUserID = strings.TrimSpace(requesterUserID)
	attachmentID = strings.TrimSpace(attachmentID)
	if requesterUserID == "" || attachmentID == "" {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	a, err := s.repo.GetAttachment(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, ErrAttachmentNotFound) {
			return AttachmentDownloadOutput{}, &Error{Code: CodeNotFound, MessageKey: "error.media.not_found", Cause: err}
		}
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if a.UserID != requesterUserID {
		allowed, accessErr := s.repo.CanUserAccessAttachment(ctx, requesterUserID, attachmentID)
		if accessErr != nil {
			return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: accessErr}
		}
		if !allowed {
			return AttachmentDownloadOutput{}, &Error{Code: CodeForbidden, MessageKey: "error.media.forbidden"}
		}
	}

	filename := strings.TrimSpace(a.Filename)
	if filename == "" {
		filename = "attachment"
	}

	if !(strings.EqualFold(a.Kind, "video") || strings.EqualFold(a.Kind, "audio")) || a.HLSMasterObjectKey == nil || strings.TrimSpace(*a.HLSMasterObjectKey) == "" {
		url, presignErr := s.store.PresignGetObject(ctx, a.ObjectKey, downloadURLTTL)
		if presignErr != nil {
			return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: presignErr}
		}
		return AttachmentDownloadOutput{URL: url, Filename: filename}, nil
	}

	manifestURL, manifestErr := s.presignHLSPlaybackManifest(ctx, strings.TrimSpace(*a.HLSMasterObjectKey), 15*time.Minute)
	if manifestErr != nil {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: manifestErr}
	}

	tmpFile, cleanup, tempErr := tempPath(filepath.Ext(filename))
	if tempErr != nil {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: tempErr}
	}
	defer cleanup()

	ffmpegPath, ffmpegErr := exec.LookPath("ffmpeg")
	if ffmpegErr != nil {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: ffmpegErr}
	}
	cmd := exec.CommandContext(
		ctx,
		ffmpegPath,
		"-y",
		"-protocol_whitelist", "file,http,https,tcp,tls,crypto",
		"-allowed_extensions", "ALL",
		"-tls_verify", "0",
		"-i", manifestURL,
		"-c", "copy",
		"-movflags", "+faststart",
		tmpFile,
	)
	if _, runErr := cmd.CombinedOutput(); runErr != nil {
		if url, presignErr := s.store.PresignGetObject(ctx, a.ObjectKey, downloadURLTTL); presignErr == nil {
			return AttachmentDownloadOutput{URL: url, Filename: filename}, nil
		}
		return AttachmentDownloadOutput{URL: manifestURL, Filename: filename}, nil
	}

	info, statErr := os.Stat(tmpFile)
	if statErr != nil {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: statErr}
	}
	file, openErr := os.Open(tmpFile)
	if openErr != nil {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: openErr}
	}
	defer file.Close()

	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	tempKey := fmt.Sprintf("tmp/downloads/%s/%s/%s%s", strings.TrimSpace(a.UserID), strings.TrimSpace(a.ID), uuid.NewString(), ext)
	contentType := detectContentTypeForDownload(ext, a.MimeType)
	if putErr := s.store.PutObject(ctx, tempKey, contentType, file, info.Size()); putErr != nil {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: putErr}
	}

	url, presignErr := s.store.PresignGetObject(ctx, tempKey, downloadURLTTL)
	if presignErr != nil {
		return AttachmentDownloadOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: presignErr}
	}

	go func(objectKey string) {
		time.Sleep(downloadCleanupWait)
		_ = s.store.DeleteObject(context.Background(), objectKey)
	}(tempKey)

	return AttachmentDownloadOutput{URL: url, Filename: filename}, nil
}

func detectContentTypeForDownload(ext, fallback string) string {
	if c := strings.TrimSpace(mime.TypeByExtension(ext)); c != "" {
		return c
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return "application/octet-stream"
}
