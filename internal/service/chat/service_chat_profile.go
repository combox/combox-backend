package chat

import (
	"context"
	"strings"
)

func canEditChatByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin", "moderator":
		return true
	default:
		return false
	}
}

func (s *Service) UpdateChat(ctx context.Context, input UpdateChatInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if !input.Title.Set && !input.AvatarDataURL.Set && !input.AvatarGradient.Set {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if target.IsDirect {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return Chat{}, internal(err)
	}
	if !canEditChatByRole(role) {
		return Chat{}, forbidden("error.chat.forbidden")
	}

	if input.Title.Set && input.Title.Value != nil {
		value := strings.TrimSpace(*input.Title.Value)
		if value == "" {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		input.Title.Value = &value
	}
	if input.AvatarDataURL.Set {
		if input.AvatarDataURL.Value != nil {
			value := strings.TrimSpace(*input.AvatarDataURL.Value)
			if value == "" {
				input.AvatarDataURL.Value = nil
			} else if strings.HasPrefix(strings.ToLower(value), "data:") {
				objectKey, err := s.uploadAvatarDataURL(ctx, value)
				if err != nil {
					return Chat{}, invalidArg("error.chat.invalid_input")
				}
				ref := avatarRefPrefix + objectKey
				input.AvatarDataURL.Value = &ref
			} else {
				input.AvatarDataURL.Value = &value
			}
		}
	}
	if input.AvatarGradient.Set && input.AvatarGradient.Value != nil {
		value := strings.TrimSpace(*input.AvatarGradient.Value)
		if value == "" {
			input.AvatarGradient.Value = nil
		} else {
			input.AvatarGradient.Value = &value
		}
	}

	updated, err := s.chats.UpdateChat(ctx, input)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	updated.AvatarURL = s.resolveAvatarURL(ctx, updated.AvatarURL)
	return updated, nil
}
