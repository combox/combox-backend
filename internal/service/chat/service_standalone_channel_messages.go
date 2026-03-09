package chat

import (
	"context"
	"errors"
	"strings"
)

type StandaloneChannelMessagePolicyService struct {
	chats    ChatRepository
	messages MessageRepository
}

func NewStandaloneChannelMessagePolicyService(chats ChatRepository, messages MessageRepository) *StandaloneChannelMessagePolicyService {
	return &StandaloneChannelMessagePolicyService{chats: chats, messages: messages}
}

func (s *StandaloneChannelMessagePolicyService) getChatRole(ctx context.Context, chatID, userID string) (string, bool, error) {
	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	switch {
	case err == nil:
		role = strings.TrimSpace(role)
		if role == "" {
			return "", false, nil
		}
		return role, true, nil
	case errors.Is(err, ErrChatNotFound):
		return "", false, nil
	default:
		return "", false, internal(err)
	}
}

func (s *StandaloneChannelMessagePolicyService) getAccess(ctx context.Context, chatMeta Chat, userID string) (standaloneChannelAccess, error) {
	role, hasRole, err := s.getChatRole(ctx, chatMeta.ID, userID)
	if err != nil {
		return standaloneChannelAccess{}, err
	}
	return resolveStandaloneChannelAccess(chatMeta, role, hasRole), nil
}

func (s *StandaloneChannelMessagePolicyService) sanitizeReactions(chatMeta Chat, item Message) Message {
	if !isStandaloneChannel(chatMeta) {
		return item
	}
	if item.ReplyToMessageID != nil && strings.TrimSpace(*item.ReplyToMessageID) != "" {
		return item
	}
	if len(item.Reactions) == 0 {
		return item
	}
	reactions := make([]MessageReaction, 0, len(item.Reactions))
	for _, reaction := range item.Reactions {
		count := reaction.Count
		if count <= 0 {
			count = len(reaction.UserIDs)
		}
		reactions = append(reactions, MessageReaction{
			Emoji:   reaction.Emoji,
			Count:   count,
			UserIDs: nil,
		})
	}
	item.Reactions = reactions
	return item
}

func (s *StandaloneChannelMessagePolicyService) ensureViewerAccess(ctx context.Context, chatMeta *Chat, userID string) error {
	if !isStandaloneChannel(*chatMeta) || userID == "" {
		return nil
	}
	if !isOpenStandaloneChannel(*chatMeta) {
		return nil
	}
	access, err := s.getAccess(ctx, *chatMeta, userID)
	if err != nil {
		return err
	}
	if access.IsBanned {
		return forbidden("error.chat.forbidden")
	}
	if access.HasRole {
		roleCopy := access.Role
		chatMeta.ViewerRole = &roleCopy
	} else {
		chatMeta.ViewerRole = nil
	}
	return nil
}

func (s *StandaloneChannelMessagePolicyService) ensureCreateAllowed(ctx context.Context, chatMeta Chat, userID, replyToMessageID string) error {
	if userID == "" || !isStandaloneChannel(chatMeta) {
		return nil
	}
	access, err := s.getAccess(ctx, chatMeta, userID)
	if err != nil {
		return err
	}
	if access.IsBanned {
		return forbidden("error.chat.forbidden")
	}
	if access.CanPost {
		return nil
	}
	if !isOpenStandaloneChannel(chatMeta) || !access.CanComment || strings.TrimSpace(replyToMessageID) == "" {
		return forbidden("error.chat.forbidden")
	}

	replyMeta, replyErr := s.messages.GetMessageMeta(ctx, replyToMessageID)
	if replyErr != nil {
		if errors.Is(replyErr, ErrMessageNotFound) {
			return invalidArg("error.message.invalid_input")
		}
		return internal(replyErr)
	}
	if strings.TrimSpace(replyMeta.ChatID) != chatMeta.ID || strings.TrimSpace(replyMeta.UserID) == "" {
		return invalidArg("error.message.invalid_input")
	}
	replyAuthorRole, hasReplyAuthorRole, roleErr := s.getChatRole(ctx, chatMeta.ID, strings.TrimSpace(replyMeta.UserID))
	if roleErr != nil {
		return roleErr
	}
	if !hasReplyAuthorRole || !canPostStandaloneChannelByRole(replyAuthorRole) {
		return forbidden("error.chat.forbidden")
	}
	return nil
}

func (s *StandaloneChannelMessagePolicyService) ensureEditOrDeleteAllowed(ctx context.Context, chatMeta Chat, userID string) (bool, error) {
	if !isStandaloneChannel(chatMeta) || userID == "" {
		return false, nil
	}
	access, err := s.getAccess(ctx, chatMeta, userID)
	if err != nil {
		return false, err
	}
	if access.IsBanned || !access.CanPost {
		return false, forbidden("error.chat.forbidden")
	}
	return true, nil
}

func (s *StandaloneChannelMessagePolicyService) ensureReactionAllowed(ctx context.Context, chatMeta Chat, userID string) error {
	if !isStandaloneChannel(chatMeta) || userID == "" {
		return nil
	}
	access, err := s.getAccess(ctx, chatMeta, userID)
	if err != nil {
		return err
	}
	if !access.CanReact {
		return forbidden("error.chat.forbidden")
	}
	return nil
}
