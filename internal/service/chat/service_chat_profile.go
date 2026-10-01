package chat

import (
	"context"
	"errors"
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

	if isStandaloneChannel(target) && target.IsPublic {
		role, roleErr := s.chats.GetChatMemberRole(ctx, chatID, userID)
		switch {
		case roleErr == nil:
			if strings.EqualFold(role, "banned") {
				return Chat{}, forbidden("error.chat.forbidden")
			}
			roleCopy := role
			target.ViewerRole = &roleCopy
		case errors.Is(roleErr, ErrChatNotFound):
			target.ViewerRole = nil
		default:
			return Chat{}, internal(roleErr)
		}
		// Subscriber rows are unique by PRIMARY KEY (chat_id, user_id), so a
		// plain COUNT(*) is already a unique-subscriber count. The client
		// (subscribe response included) renders this instead of locally
		// incrementing the counter.
		count, countErr := s.chats.CountChannelSubscribers(ctx, chatID)
		if countErr != nil {
			return Chat{}, internal(countErr)
		}
		countCopy := count
		target.SubscriberCount = &countCopy
		target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
		s.attachChatUserState(ctx, userID, &target)
		return target, nil
	}

	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return Chat{}, err
	}
	target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
	s.attachChatUserState(ctx, userID, &target)
	return target, nil
}

func (s *Service) UpdateChat(ctx context.Context, input UpdateChatInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if !input.Title.Set && !input.AvatarDataURL.Set && !input.AvatarGradient.Set && !input.CommentsEnabled.Set && !input.ReactionsEnabled.Set && !input.SignMessages.Set && !input.ShowAuthorsProfiles.Set && !input.AutoTranslate.Set && !input.SlowModeSeconds.Set && !input.DiscussionChatID.Set && !input.IsPublic.Set && !input.PublicSlug.Set && !input.SendPermission.Set && !input.Description.Set && !input.IconEmoji.Set && !input.ChannelType.Set {
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

	if input.SendPermission.Set {
		// "Who may post" only makes sense on a channel (a group topic).
		if strings.ToLower(strings.TrimSpace(target.Kind)) != "channel" {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		normalized := NormalizeSendPermission(derefString(input.SendPermission.Value))
		if normalized == "" {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		input.SendPermission.Value = &normalized
	}

	if input.SlowModeSeconds.Set && input.SlowModeSeconds.Value < 0 {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	if input.DiscussionChatID.Set {
		// An explicit empty string (or a nil value) clears the linked chat.
		value := derefString(input.DiscussionChatID.Value)
		if value == "" {
			input.DiscussionChatID.Value = nil
		} else {
			discussion, discussionErr := s.chats.GetChat(ctx, value)
			if discussionErr != nil {
				return Chat{}, invalidArg("error.chat.invalid_input")
			}
			kind := strings.ToLower(strings.TrimSpace(discussion.Kind))
			if kind != ChatKindGroup && kind != "standalone_channel" {
				return Chat{}, invalidArg("error.chat.invalid_input")
			}
			input.DiscussionChatID.Value = &value
		}
	}

	if input.Title.Set && input.Title.Value != nil {
		value := strings.TrimSpace(*input.Title.Value)
		if value == "" {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		input.Title.Value = &value
	}
	// description / icon_emoji are free text with a hard length cap: an empty
	// value clears the field, an absent or JSON null key leaves it unchanged.
	if input.Description.Set {
		value := derefString(input.Description.Value)
		if len([]rune(value)) > ChatDescriptionMaxLen {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		input.Description.Value = &value
	}
	if input.IconEmoji.Set {
		value := derefString(input.IconEmoji.Value)
		if len([]rune(value)) > ChatIconEmojiMaxLen {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		input.IconEmoji.Value = &value
	}
	// channel_type is an enum (text|voice) mirrored by a CHECK constraint; an
	// empty value means "keep the current type" instead of clearing it.
	if input.ChannelType.Set {
		value := strings.ToLower(strings.TrimSpace(derefString(input.ChannelType.Value)))
		switch value {
		case "":
			input.ChannelType = OptionalString{}
		case ChannelTypeText, ChannelTypeVoice:
			input.ChannelType.Value = &value
		default:
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
	}
	var uploadedAvatarKey string
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
				uploadedAvatarKey = objectKey
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
	if isStandaloneChannel(target) {
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
	// Journal the fields that really changed (raw snapshots, before the
	// avatar is presigned for this viewer).
	s.recordChatUpdateEvent(ctx, userID, target, updated)
	updated.AvatarURL = s.resolveAvatarURL(ctx, updated.AvatarURL)
	s.recordProfilePhoto(ctx, chatID, uploadedAvatarKey)
	eventChat := updated
	s.attachChatUserState(ctx, userID, &updated)
	s.publishChatUpdated(ctx, chatID, eventChat)
	return updated, nil
}

func (s *Service) publishChatUpdated(ctx context.Context, chatID string, snapshot Chat) {
	if s.publisher == nil {
		return
	}
	members, listErr := s.chats.ListChatMemberIDs(ctx, chatID)
	if listErr != nil {
		return
	}
	now := s.nowUTC()
	for _, memberID := range members {
		_ = s.publisher.PublishChatUpdated(ctx, ChatUpdatedEvent{
			ChatID:          snapshot.ID,
			RecipientUserID: memberID,
			Chat:            snapshot,
			UpdatedAt:       now,
		})
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
