package chat

import (
	"context"
	"strings"
)

func (s *Service) DeleteChat(ctx context.Context, userID, chatID string) error {
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

	kind := strings.TrimSpace(strings.ToLower(target.Kind))
	if kind == "channel" && target.ParentChatID != nil && strings.TrimSpace(*target.ParentChatID) != "" {
		return invalidArg("error.chat.invalid_input")
	}
	if kind != "group" && kind != "standalone_channel" && kind != "channel" {
		return invalidArg("error.chat.invalid_input")
	}
	if kind == "channel" {
		// Only allow deleting the virtual General topic through group deletion.
		return forbidden("error.chat.forbidden")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return internal(err)
	}
	if !strings.EqualFold(role, "owner") {
		return forbidden("error.chat.forbidden")
	}

	if err := s.chats.DeleteChat(ctx, chatID); err != nil {
		return mapChatOrMessageRepoError(err)
	}
	return nil
}

