package chat

import (
	"context"
	"errors"
	"strings"
)

func canManageInviteLinksByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func (s *Service) ListInviteLinks(ctx context.Context, userID, chatID string) ([]ChatInviteLink, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return nil, err
	}
	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return nil, internal(err)
	}
	if !canManageInviteLinksByRole(role) {
		return nil, forbidden("error.chat.forbidden")
	}

	links, err := s.chats.ListChatInviteLinks(ctx, chatID)
	if err != nil {
		return nil, internal(err)
	}
	if len(links) == 0 {
		created, err := s.chats.CreateChatInviteLink(ctx, chatID, userID, "", true)
		if err != nil {
			return nil, internal(err)
		}
		links = []ChatInviteLink{created}
	}
	return links, nil
}

func (s *Service) CreateInviteLink(ctx context.Context, input CreateInviteLinkInput) (ChatInviteLink, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	title := strings.TrimSpace(input.Title)
	if userID == "" || chatID == "" {
		return ChatInviteLink{}, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return ChatInviteLink{}, err
	}
	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return ChatInviteLink{}, internal(err)
	}
	if !canManageInviteLinksByRole(role) {
		return ChatInviteLink{}, forbidden("error.chat.forbidden")
	}

	item, err := s.chats.CreateChatInviteLink(ctx, chatID, userID, title, false)
	if err != nil {
		return ChatInviteLink{}, internal(err)
	}
	return item, nil
}

func (s *Service) AcceptInviteLink(ctx context.Context, userID, token string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	token = strings.TrimSpace(token)
	if userID == "" || token == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	link, err := s.chats.GetChatInviteLinkByToken(ctx, token)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	target, err := s.chats.GetChat(ctx, link.ChatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if target.IsDirect {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if isStandaloneChannel(target) && target.IsPublic {
		role, roleErr := s.chats.GetChatMemberRole(ctx, target.ID, userID)
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
		target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
		_ = s.chats.IncrementChatInviteLinkUse(ctx, link.ID)
		return target, nil
	}

	role, err := s.chats.GetChatMemberRole(ctx, target.ID, userID)
	switch {
	case err == nil:
		if strings.EqualFold(role, "banned") {
			return Chat{}, forbidden("error.chat.forbidden")
		}
	case errors.Is(err, ErrChatNotFound):
	default:
		return Chat{}, internal(err)
	}

	isMember, err := s.chats.IsChatMember(ctx, target.ID, userID)
	if err != nil {
		return Chat{}, internal(err)
	}
	if !isMember {
		if err := s.chats.AddChatMembers(ctx, target.ID, []string{userID}); err != nil {
			return Chat{}, internal(err)
		}
		if isStandaloneChannel(target) {
			if err := s.chats.UpdateChatMemberRole(ctx, target.ID, userID, "subscriber"); err != nil {
				return Chat{}, internal(err)
			}
		}
	}
	_ = s.chats.IncrementChatInviteLinkUse(ctx, link.ID)
	target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
	return target, nil
}
