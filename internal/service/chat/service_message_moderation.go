package chat

import (
	"context"
	"errors"
	"strings"
)

func (s *Service) EditMessage(ctx context.Context, input EditMessageInput) (Message, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	messageID := strings.TrimSpace(input.MessageID)
	newContent := strings.TrimSpace(input.Content)
	if userID == "" || chatID == "" || messageID == "" || newContent == "" {
		return Message{}, invalidArg("error.message.invalid_input")
	}

	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return Message{}, err
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Message{}, mapChatOrMessageRepoError(err)
	}
	chatType, ok := normalizeChatType(chatMeta.Type)
	if !ok {
		return Message{}, invalidArg("error.chat.invalid_type")
	}
	if chatType != ChatTypeStandard {
		return Message{}, invalidArg("error.message.edit_not_allowed")
	}
	allowForeign := false
	if strings.TrimSpace(strings.ToLower(chatMeta.Kind)) == "public_channel" {
		role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
		if err != nil {
			return Message{}, internal(err)
		}
		if !canPostPublicChannelByRole(role) {
			return Message{}, forbidden("error.chat.forbidden")
		}
		allowForeign = true
	}

	updated, repoErr := s.messages.UpdateMessageContent(ctx, chatID, messageID, userID, newContent, input.AttachmentIDs, allowForeign)
	if repoErr != nil {
		return Message{}, mapChatOrMessageRepoError(repoErr)
	}

	if s.publisher != nil && updated.EditedAt != nil {
		members, err := s.chats.ListChatMemberIDs(ctx, chatID)
		if err == nil {
			for _, memberID := range members {
				_ = s.publisher.PublishMessageUpdated(ctx, MessageUpdatedEvent{
					MessageID:       updated.ID,
					ChatID:          updated.ChatID,
					EditorUserID:    userID,
					RecipientUserID: memberID,
					Content:         updated.Content,
					EditedAt:        *updated.EditedAt,
				})
			}
		}
	}
	return updated, nil
}

func (s *Service) MarkMessageReadByID(ctx context.Context, userID, messageID string) (MessageStatus, error) {
	userID = strings.TrimSpace(userID)
	messageID = strings.TrimSpace(messageID)
	if userID == "" || messageID == "" {
		return MessageStatus{}, invalidArg("error.message.invalid_input")
	}

	meta, err := s.messages.GetMessageMeta(ctx, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return MessageStatus{}, notFound("error.message.not_found", err)
		}
		return MessageStatus{}, internal(err)
	}

	return s.UpsertMessageStatus(ctx, UpsertMessageStatusInput{
		UserID:    userID,
		ChatID:    meta.ChatID,
		MessageID: meta.ID,
		Status:    "read",
	})
}

func (s *Service) EditMessageByID(ctx context.Context, userID, messageID, content string) (Message, error) {
	userID = strings.TrimSpace(userID)
	messageID = strings.TrimSpace(messageID)
	content = strings.TrimSpace(content)
	if userID == "" || messageID == "" || content == "" {
		return Message{}, invalidArg("error.message.invalid_input")
	}

	meta, err := s.messages.GetMessageMeta(ctx, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return Message{}, notFound("error.message.not_found", err)
		}
		return Message{}, internal(err)
	}

	return s.EditMessage(ctx, EditMessageInput{
		UserID:        userID,
		ChatID:        meta.ChatID,
		MessageID:     meta.ID,
		Content:       content,
		AttachmentIDs: nil,
	})
}

func (s *Service) DeleteMessageByID(ctx context.Context, userID, messageID string) error {
	userID = strings.TrimSpace(userID)
	messageID = strings.TrimSpace(messageID)
	if userID == "" || messageID == "" {
		return invalidArg("error.message.invalid_input")
	}

	meta, err := s.messages.GetMessageMeta(ctx, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return notFound("error.message.not_found", err)
		}
		return internal(err)
	}

	if err := s.ensureChatMember(ctx, meta.ChatID, userID); err != nil {
		return err
	}

	chatMeta, err := s.chats.GetChat(ctx, meta.ChatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	chatType, ok := normalizeChatType(chatMeta.Type)
	if !ok {
		return invalidArg("error.chat.invalid_type")
	}
	if chatType != ChatTypeStandard {
		return invalidArg("error.message.edit_not_allowed")
	}
	allowForeign := false
	if strings.TrimSpace(strings.ToLower(chatMeta.Kind)) == "public_channel" {
		role, err := s.chats.GetChatMemberRole(ctx, meta.ChatID, userID)
		if err != nil {
			return internal(err)
		}
		if !canPostPublicChannelByRole(role) {
			return forbidden("error.chat.forbidden")
		}
		allowForeign = true
	}

	if err := s.messages.SoftDeleteMessage(ctx, meta.ChatID, meta.ID, userID, allowForeign); err != nil {
		return mapChatOrMessageRepoError(err)
	}

	if s.publisher != nil {
		members, listErr := s.chats.ListChatMemberIDs(ctx, meta.ChatID)
		if listErr == nil {
			now := s.nowUTC()
			for _, memberID := range members {
				_ = s.publisher.PublishMessageDeleted(ctx, MessageDeletedEvent{
					MessageID:       meta.ID,
					ChatID:          meta.ChatID,
					ActorUserID:     userID,
					RecipientUserID: memberID,
					At:              now,
				})
			}
		}
	}

	return nil
}

func (s *Service) ForwardMessage(ctx context.Context, input ForwardMessageInput) (Message, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	sourceMessageID := strings.TrimSpace(input.SourceMessageID)
	if userID == "" || chatID == "" || sourceMessageID == "" {
		return Message{}, invalidArg("error.message.invalid_input")
	}

	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return Message{}, err
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Message{}, mapChatOrMessageRepoError(err)
	}
	chatType, ok := normalizeChatType(chatMeta.Type)
	if !ok {
		return Message{}, invalidArg("error.chat.invalid_type")
	}
	if chatType != ChatTypeStandard {
		return Message{}, invalidArg("error.message.e2e_not_allowed_in_standard")
	}

	created, repoErr := s.messages.CreateForwardedMessage(ctx, chatID, sourceMessageID, userID)
	if repoErr != nil {
		return Message{}, mapChatOrMessageRepoError(repoErr)
	}

	if s.publisher != nil {
		members, err := s.chats.ListChatMemberIDs(ctx, chatID)
		if err == nil {
			for _, memberID := range members {
				ev := UserMessageCreatedEvent{
					MessageID:       created.ID,
					ChatID:          created.ChatID,
					SenderUserID:    userID,
					RecipientUserID: memberID,
					CreatedAt:       created.CreatedAt,
				}
				_ = s.publisher.PublishUserMessageCreated(ctx, ev)
			}
		}
	}
	return created, nil
}

func (s *Service) ToggleMessageReactionByID(ctx context.Context, userID, messageID, emoji string) ([]MessageReaction, string, error) {
	userID = strings.TrimSpace(userID)
	messageID = strings.TrimSpace(messageID)
	emoji = strings.TrimSpace(emoji)
	if userID == "" || messageID == "" || emoji == "" {
		return nil, "", invalidArg("error.message.invalid_input")
	}

	meta, err := s.messages.GetMessageMeta(ctx, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return nil, "", notFound("error.message.not_found", err)
		}
		return nil, "", internal(err)
	}

	if err := s.ensureChatMember(ctx, meta.ChatID, userID); err != nil {
		return nil, "", err
	}
	chatMeta, chatErr := s.chats.GetChat(ctx, meta.ChatID)
	if chatErr != nil {
		return nil, "", mapChatOrMessageRepoError(chatErr)
	}
	if strings.TrimSpace(strings.ToLower(chatMeta.Kind)) == "public_channel" {
		role, roleErr := s.chats.GetChatMemberRole(ctx, meta.ChatID, userID)
		if roleErr != nil {
			if errors.Is(roleErr, ErrChatNotFound) {
				return nil, "", forbidden("error.chat.forbidden")
			}
			return nil, "", internal(roleErr)
		}
		if !canReactPublicChannelByRole(role) {
			return nil, "", forbidden("error.chat.forbidden")
		}
	}

	reactions, action, err := s.messages.ToggleMessageReaction(ctx, meta.ChatID, meta.ID, userID, emoji)
	if err != nil {
		return nil, "", mapChatOrMessageRepoError(err)
	}
	sanitized := sanitizeMessageReactionsForViewer(chatMeta, Message{
		ID:        meta.ID,
		ChatID:    meta.ChatID,
		Reactions: reactions,
	})
	if strings.TrimSpace(meta.ReplyToMessageID) != "" {
		sanitized.ReplyToMessageID = &meta.ReplyToMessageID
	}
	reactions = sanitized.Reactions

	if s.publisher != nil {
		members, listErr := s.chats.ListChatMemberIDs(ctx, meta.ChatID)
		if listErr == nil {
			now := s.nowUTC()
			for _, memberID := range members {
				_ = s.publisher.PublishMessageReaction(ctx, MessageReactionEvent{
					MessageID:       meta.ID,
					ChatID:          meta.ChatID,
					ActorUserID:     userID,
					RecipientUserID: memberID,
					Emoji:           emoji,
					Action:          action,
					Reactions:       reactions,
					At:              now,
				})
			}
		}
	}

	return reactions, action, nil
}
