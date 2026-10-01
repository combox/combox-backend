package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	profilephotosvc "combox-backend/internal/service/profilephoto"

	"github.com/google/uuid"
)

// ProfilePhotoRecorder archives every avatar object that gets written for a
// chat, so the fullscreen gallery can replay the whole photo history.
type ProfilePhotoRecorder interface {
	Record(ctx context.Context, ownerKind, ownerID, objectKey string) error
}

// SetProfilePhotoRecorder wires the avatar history archive.
func (s *Service) SetProfilePhotoRecorder(recorder ProfilePhotoRecorder) {
	s.photoHistory = recorder
}

// recordProfilePhoto archives a freshly uploaded chat avatar object. The
// avatar itself is already stored at this point, so a history failure never
// fails the chat update that produced it. Clearing the avatar or leaving it
// untouched never produces an object key, hence no record.
func (s *Service) recordProfilePhoto(ctx context.Context, chatID, objectKey string) {
	if s.photoHistory == nil {
		return
	}
	chatID = strings.TrimSpace(chatID)
	objectKey = strings.TrimSpace(objectKey)
	if chatID == "" || objectKey == "" {
		return
	}
	_ = s.photoHistory.Record(ctx, profilephotosvc.OwnerChat, chatID, objectKey)
}

func (s *Service) uploadAvatarDataURL(ctx context.Context, raw string) (string, error) {
	return uploadAvatarDataURL(ctx, s.avatars, raw)
}

func uploadAvatarDataURL(ctx context.Context, store AvatarStore, raw string) (string, error) {
	if store == nil {
		return "", errors.New("avatar store is not configured")
	}
	contentType, payload, err := decodeDataURL(raw)
	if err != nil {
		return "", err
	}
	objectKey := fmt.Sprintf("chat-avatars/%s%s", uuid.NewString(), extensionByContentType(contentType))
	if err := store.PutObject(ctx, objectKey, contentType, bytes.NewReader(payload), int64(len(payload))); err != nil {
		return "", err
	}
	return objectKey, nil
}

func decodeDataURL(raw string) (string, []byte, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return "", nil, errors.New("avatar must be data url")
	}
	commaIdx := strings.Index(value, ",")
	if commaIdx <= 5 {
		return "", nil, errors.New("invalid data url")
	}
	meta := value[5:commaIdx]
	dataPart := value[commaIdx+1:]
	parts := strings.Split(meta, ";")
	contentType := strings.TrimSpace(parts[0])
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	isBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}
	if isBase64 {
		payload, err := base64.StdEncoding.DecodeString(dataPart)
		if err != nil {
			return "", nil, err
		}
		return contentType, payload, nil
	}
	decoded, err := url.QueryUnescape(dataPart)
	if err != nil {
		return "", nil, err
	}
	return contentType, []byte(decoded), nil
}

func extensionByContentType(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}
