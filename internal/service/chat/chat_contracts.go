package chat

import (
	"context"
	"io"
	"time"
)

type ChatRepository interface {
	CreateChat(ctx context.Context, title string, memberIDs []string, creatorID string, chatType string) (Chat, error)
	CreateChannel(ctx context.Context, parentChatID, title, channelType, creatorID string) (Chat, error)
	CreateStandaloneChannel(ctx context.Context, title, publicSlug, creatorID string, isPublic bool) (Chat, error)
	DeleteChannel(ctx context.Context, parentChatID, channelChatID string) error
	FindDirectChatByMembers(ctx context.Context, userAID, userBID, chatType string) (Chat, bool, error)
	ListChatsByUser(ctx context.Context, userID string) ([]Chat, error)
	ListChannelsByParent(ctx context.Context, parentChatID, userID string) ([]Chat, error)
	GetChat(ctx context.Context, chatID string) (Chat, error)
	UpdateChat(ctx context.Context, input UpdateChatInput) (Chat, error)
	ListChatInviteLinks(ctx context.Context, chatID string) ([]ChatInviteLink, error)
	CreateChatInviteLink(ctx context.Context, chatID, createdBy, title string, isPrimary bool) (ChatInviteLink, error)
	GetChatInviteLinkByToken(ctx context.Context, token string) (ChatInviteLink, error)
	IncrementChatInviteLinkUse(ctx context.Context, linkID string) error
	ListChatMembers(ctx context.Context, chatID string, includeBanned bool) ([]ChatMember, error)
	AddChatMembers(ctx context.Context, chatID string, memberIDs []string) error
	UpdateChatMemberRole(ctx context.Context, chatID, userID, role string) error
	RemoveChatMember(ctx context.Context, chatID, userID string) error
	ListChatMemberIDs(ctx context.Context, chatID string) ([]string, error)
	GetChatMemberRole(ctx context.Context, chatID, userID string) (string, error)
	IsChatMember(ctx context.Context, chatID, userID string) (bool, error)
}

type MessageRepository interface {
	CreateMessage(ctx context.Context, chatID, userID, content, replyToMessageID string) (Message, error)
	CreateMessageAsBot(ctx context.Context, chatID, botID, content, replyToMessageID string) (Message, error)
	CreateMessageWithAttachments(ctx context.Context, chatID, userID, content, replyToMessageID string, attachmentIDs []string) (Message, error)
	CreateMessageE2E(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, replyToMessageID string) (Message, error)
	CreateMessageE2EWithAttachments(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, replyToMessageID string, attachmentIDs []string) (Message, error)
	CreateForwardedMessage(ctx context.Context, chatID, sourceMessageID, userID string) (Message, error)
	ListMessages(ctx context.Context, chatID string, limit int, cursor string) (MessagePage, error)
	ListMessagesForDevice(ctx context.Context, chatID, deviceID string, limit int, cursor string) (MessagePage, error)
	UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string) (MessageStatus, error)
	UpdateMessageContent(ctx context.Context, chatID, messageID, editorUserID, newContent string, attachmentIDs []string, allowForeign bool) (Message, error)
	GetMessageMeta(ctx context.Context, messageID string) (MessageMeta, error)
	SoftDeleteMessage(ctx context.Context, chatID, messageID, deleterUserID string, allowForeign bool) error
	ToggleMessageReaction(ctx context.Context, chatID, messageID, userID, emoji string) ([]MessageReaction, string, error)
}

type StatusRepository interface {
	UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string, at time.Time) (MessageStatus, error)
}

type MessageEventPublisher interface {
	PublishDeviceMessageCreated(ctx context.Context, ev DeviceMessageCreatedEvent) error
	PublishUserMessageCreated(ctx context.Context, ev UserMessageCreatedEvent) error
	PublishMessageStatus(ctx context.Context, ev MessageStatusEvent) error
	PublishMessageUpdated(ctx context.Context, ev MessageUpdatedEvent) error
	PublishMessageDeleted(ctx context.Context, ev MessageDeletedEvent) error
	PublishMessageReaction(ctx context.Context, ev MessageReactionEvent) error
}

type NotificationRepository interface {
	IncrementChatUnread(ctx context.Context, userID, chatID string, delta int) (int, error)
	ResetChatUnread(ctx context.Context, userID, chatID string) error
}

type InviteRepository interface {
	Create(ctx context.Context, chatID, inviterID, inviteeID string, ttl time.Duration) (ChatInvite, error)
	Consume(ctx context.Context, token string) (ChatInvite, bool, error)
}

type AvatarStore interface {
	PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
}
