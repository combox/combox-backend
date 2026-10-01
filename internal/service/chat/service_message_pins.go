package chat

import (
	"context"
	"errors"
	"strings"
)

// ensureChatReadableForViewer mirrors the read-access rules used by
// ListMessages: public channels can be read by anyone who is not banned,
// everything else requires membership.
func (s *Service) ensureChatReadableForViewer(ctx context.Context, chatID, userID string) error {
	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	kind := strings.TrimSpace(strings.ToLower(chatMeta.Kind))
	switch {
	case isStandaloneChannel(chatMeta) && chatMeta.IsPublic:
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, chatID, userID); banErr != nil {
			return internal(banErr)
		} else if banned {
			return forbidden("error.chat.forbidden")
		}
		role, roleErr := s.chats.GetChatMemberRole(ctx, chatID, userID)
		switch {
		case roleErr == nil:
			if strings.EqualFold(role, "banned") {
				return forbidden("error.chat.forbidden")
			}
		case errors.Is(roleErr, ErrChatNotFound):
			// Public viewers are allowed without membership.
		default:
			return internal(roleErr)
		}
		return nil
	case kind == "comment_thread":
		parentID := ""
		if chatMeta.ParentChatID != nil {
			parentID = strings.TrimSpace(*chatMeta.ParentChatID)
		}
		if parentID == "" {
			return forbidden("error.chat.forbidden")
		}
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, parentID, userID); banErr != nil {
			return internal(banErr)
		} else if banned {
			return forbidden("error.chat.forbidden")
		}
		return nil
	default:
		return s.ensureChatMember(ctx, chatID, userID)
	}
}

// GetPinnedMessage returns the currently pinned message of a chat, or nil when
// nothing is pinned (or the pinned row is already gone).
func (s *Service) GetPinnedMessage(ctx context.Context, userID, chatID string) (*Message, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil, invalidArg("error.message.invalid_input")
	}

	if err := s.ensureChatReadableForViewer(ctx, chatID, userID); err != nil {
		return nil, err
	}

	pinnedID, err := s.chats.GetChatPinnedMessageID(ctx, chatID)
	if err != nil {
		return nil, internal(err)
	}
	if strings.TrimSpace(pinnedID) == "" {
		return nil, nil
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}

	item, err := s.messages.GetMessageByID(ctx, chatID, pinnedID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return nil, nil
		}
		return nil, mapChatOrMessageRepoError(err)
	}
	// The pin banner follows the viewer's own history clear watermark.
	if state, stateErr := s.chats.GetChatUserState(ctx, userID, chatID); stateErr == nil && state.HistoryClearedAt != nil {
		if !item.CreatedAt.After(*state.HistoryClearedAt) {
			return nil, nil
		}
	}
	sanitized := sanitizeMessageReactionsForViewer(chatMeta, item)
	s.applyForwardPrivacyToMessage(ctx, userID, &sanitized)
	if err := s.attachPollToMessage(ctx, userID, &sanitized); err != nil {
		return nil, err
	}
	return &sanitized, nil
}

// PinMessage pins (or unpins) a message. Unpinning returns a nil message.
func (s *Service) PinMessage(ctx context.Context, input PinMessageInput) (*Message, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	messageID := strings.TrimSpace(input.MessageID)
	if userID == "" || chatID == "" || messageID == "" {
		return nil, invalidArg("error.message.invalid_input")
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	chatType, ok := normalizeChatType(chatMeta.Type)
	if !ok {
		return nil, invalidArg("error.chat.invalid_type")
	}
	if chatType != ChatTypeStandard {
		return nil, invalidArg("error.message.edit_not_allowed")
	}
	if isStandaloneChannel(chatMeta) {
		role, roleErr := s.chats.GetChatMemberRole(ctx, chatID, userID)
		if roleErr != nil && !errors.Is(roleErr, ErrChatNotFound) {
			return nil, internal(roleErr)
		}
		if !canPostPublicChannelByRole(role) {
			return nil, forbidden("error.chat.forbidden")
		}
	} else if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return nil, err
	}

	target := messageID
	if !input.Pinned {
		target = ""
	} else if _, err := s.messages.GetMessageByID(ctx, chatID, messageID); err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}

	if err := s.chats.SetChatPinnedMessage(ctx, chatID, target); err != nil {
		return nil, internal(err)
	}
	if !input.Pinned {
		return nil, nil
	}

	item, err := s.messages.GetMessageByID(ctx, chatID, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return nil, nil
		}
		return nil, mapChatOrMessageRepoError(err)
	}
	sanitized := sanitizeMessageReactionsForViewer(chatMeta, item)
	s.applyForwardPrivacyToMessage(ctx, userID, &sanitized)
	return &sanitized, nil
}
