package chat

import (
	"context"
	"strings"
)

func (s *Service) AcceptInvite(ctx context.Context, userID, token string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	token = strings.TrimSpace(token)
	if userID == "" || token == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if s.invites == nil {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	invite, found, err := s.invites.Consume(ctx, token)
	if err != nil {
		return Chat{}, internal(err)
	}
	if !found {
		return Chat{}, notFound("error.chat.not_found", ErrChatNotFound)
	}
	if strings.TrimSpace(invite.InviteeID) != userID {
		return Chat{}, forbidden("error.chat.forbidden")
	}

	target, err := s.chats.GetChat(ctx, invite.ChatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if target.IsDirect {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	isMember, err := s.chats.IsChatMember(ctx, invite.ChatID, userID)
	if err != nil {
		return Chat{}, internal(err)
	}
	if !isMember {
		if err := s.chats.AddChatMembers(ctx, invite.ChatID, []string{userID}); err != nil {
			return Chat{}, internal(err)
		}
	}

	target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
	return target, nil
}

func (s *Service) LeaveChat(ctx context.Context, userID, chatID string) error {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if target.IsDirect {
		return invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return err
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return internal(err)
	}
	if strings.EqualFold(role, "owner") {
		members, err := s.chats.ListChatMembers(ctx, chatID, false)
		if err != nil {
			return internal(err)
		}
		var replacement string
		for _, member := range members {
			if strings.TrimSpace(member.UserID) == "" || member.UserID == userID {
				continue
			}
			replacement = member.UserID
			break
		}
		if replacement == "" {
			return forbidden("error.chat.forbidden")
		}
		if err := s.chats.UpdateChatMemberRole(ctx, chatID, replacement, "owner"); err != nil {
			return internal(err)
		}
	}
	if err := s.chats.RemoveChatMember(ctx, chatID, userID); err != nil {
		return internal(err)
	}
	return nil
}
