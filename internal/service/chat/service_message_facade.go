package chat

import (
	"context"
	"time"
)

// MessageService owns message orchestration on a separate application boundary from chat/channel lifecycle.
type MessageService struct {
	chats         ChatRepository
	messages      MessageRepository
	publisher     MessageEventPublisher
	statusRepo    StatusRepository
	notifications NotificationRepository
	standalone    *StandaloneChannelMessagePolicyService
}

func NewMessageService(core *Service) *MessageService {
	if core == nil {
		return nil
	}
	return &MessageService{
		chats:         core.chats,
		messages:      core.messages,
		publisher:     core.publisher,
		statusRepo:    core.statusRepo,
		notifications: core.notifications,
		standalone:    core.standalone,
	}
}

func (s *MessageService) ensureChatMember(ctx context.Context, chatID, userID string) error {
	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return internal(err)
	}
	if !member {
		return forbidden("error.chat.forbidden")
	}
	return nil
}

func (s *MessageService) nowUTC() time.Time {
	return time.Now().UTC()
}

func (s *Service) CreateMessage(ctx context.Context, input CreateMessageInput) (Message, error) {
	return s.messageSvc.CreateMessage(ctx, input)
}

func (s *Service) ListMessages(ctx context.Context, input ListMessagesInput) (MessagePage, error) {
	return s.messageSvc.ListMessages(ctx, input)
}

func (s *Service) UpsertMessageStatus(ctx context.Context, input UpsertMessageStatusInput) (MessageStatus, error) {
	return s.messageSvc.UpsertMessageStatus(ctx, input)
}

func (s *Service) EditMessage(ctx context.Context, input EditMessageInput) (Message, error) {
	return s.messageSvc.EditMessage(ctx, input)
}

func (s *Service) MarkMessageReadByID(ctx context.Context, userID, messageID string) (MessageStatus, error) {
	return s.messageSvc.MarkMessageReadByID(ctx, userID, messageID)
}

func (s *Service) EditMessageByID(ctx context.Context, userID, messageID, content string) (Message, error) {
	return s.messageSvc.EditMessageByID(ctx, userID, messageID, content)
}

func (s *Service) DeleteMessageByID(ctx context.Context, userID, messageID string) error {
	return s.messageSvc.DeleteMessageByID(ctx, userID, messageID)
}

func (s *Service) ForwardMessage(ctx context.Context, input ForwardMessageInput) (Message, error) {
	return s.messageSvc.ForwardMessage(ctx, input)
}

func (s *Service) ToggleMessageReactionByID(ctx context.Context, userID, messageID, emoji string) ([]MessageReaction, string, error) {
	return s.messageSvc.ToggleMessageReactionByID(ctx, userID, messageID, emoji)
}
