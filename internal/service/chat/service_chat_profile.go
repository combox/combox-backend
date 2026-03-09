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

func (s *Service) GetChat(ctx context.Context, userID, chatID string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}

	if err := s.standalone.ensureViewerAccess(ctx, &target, userID); err != nil {
		return Chat{}, err
	}
	if isOpenStandaloneChannel(target) {
		target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
		return target, nil
	}

	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return Chat{}, err
	}
	target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
	return target, nil
}

func (s *Service) UpdateChat(ctx context.Context, input UpdateChatInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if !input.Title.Set && !input.AvatarDataURL.Set && !input.AvatarGradient.Set && !input.CommentsEnabled.Set && !input.ReactionsEnabled.Set && !input.IsPublic.Set && !input.PublicSlug.Set {
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
	if target.Kind == "standalone_channel" {
		if input.IsPublic.Set {
			if !input.IsPublic.Value {
				input.PublicSlug = OptionalString{Set: true, Value: nil}
			} else if !input.PublicSlug.Set {
				existingSlug := normalizePublicSlug(derefString(target.PublicSlug))
				if existingSlug == "" {
					return Chat{}, invalidArg("error.chat.invalid_input")
				}
				input.PublicSlug = OptionalString{Set: true, Value: &existingSlug}
			}
		}
		if input.PublicSlug.Set {
			if input.PublicSlug.Value != nil {
				value := normalizePublicSlug(*input.PublicSlug.Value)
				if value == "" {
					input.PublicSlug.Value = nil
				} else {
					input.PublicSlug.Value = &value
				}
			}
			nextPublic := target.IsPublic
			if input.IsPublic.Set {
				nextPublic = input.IsPublic.Value
			}
			if nextPublic && input.PublicSlug.Value == nil {
				return Chat{}, invalidArg("error.chat.invalid_input")
			}
			if !nextPublic {
				input.PublicSlug.Value = nil
			}
		}
	} else {
		input.IsPublic = OptionalBool{}
		input.PublicSlug = OptionalString{}
	}

	updated, err := s.chats.UpdateChat(ctx, input)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	updated.AvatarURL = s.resolveAvatarURL(ctx, updated.AvatarURL)
	return updated, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
