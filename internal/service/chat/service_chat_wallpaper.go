package chat

import (
	"context"
	"errors"
	"strings"
)

const maxWallpaperValueLen = 512 * 1024

// SetChatWallpaper stores the chat's own background.
//
// Permissions reuse the existing role checks: owner/admin/moderator for
// groups and channels, any participant for direct chats.
func (s *Service) SetChatWallpaper(ctx context.Context, userID, chatID, wallpaperKind, wallpaperValue string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	kind, value, err := normalizeWallpaper(wallpaperKind, wallpaperValue)
	if err != nil {
		return Chat{}, err
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}

	role, roleErr := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if roleErr != nil && !errors.Is(roleErr, ErrChatNotFound) {
		return Chat{}, internal(roleErr)
	}
	if target.IsDirect {
		// A private conversation is owned by its participants.
		if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
			return Chat{}, err
		}
	} else if !canEditChatByRole(role) {
		return Chat{}, forbidden("error.chat.forbidden")
	}

	if err := s.chats.SetChatWallpaper(ctx, chatID, kind, value); err != nil {
		return Chat{}, internal(err)
	}

	updated, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	updated.AvatarURL = s.resolveAvatarURL(ctx, updated.AvatarURL)
	eventChat := updated
	s.attachChatUserState(ctx, userID, &updated)
	s.publishChatUpdated(ctx, chatID, eventChat)
	return updated, nil
}

// normalizeWallpaper validates and canonicalises the pair; an empty kind
// means "no wallpaper" and always clears the value.
func normalizeWallpaper(rawKind, rawValue string) (string, string, error) {
	kind := strings.TrimSpace(strings.ToLower(rawKind))
	value := strings.TrimSpace(rawValue)
	switch kind {
	case "", WallpaperKindNone:
		return WallpaperKindNone, "", nil
	case WallpaperKindPreset:
		if !isWallpaperPresetID(value) {
			return "", "", invalidArg("error.chat.invalid_input")
		}
		return WallpaperKindPreset, value, nil
	case WallpaperKindImage:
		if value == "" || len(value) > maxWallpaperValueLen || !isWallpaperImageRef(value) {
			return "", "", invalidArg("error.chat.invalid_input")
		}
		return WallpaperKindImage, value, nil
	default:
		return "", "", invalidArg("error.chat.invalid_input")
	}
}

func isWallpaperPresetID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func isWallpaperImageRef(value string) bool {
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "data:image/"), strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "blob:"), strings.HasPrefix(lower, avatarRefPrefix):
		return true
	default:
		return false
	}
}
