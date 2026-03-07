package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	ChatTypeStandard  = "standard"
	ChatTypeSecretE2E = "secret_e2e"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
)

type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

type MessageMeta struct {
	ID          string
	ChatID      string
	UserID      string
	SenderBotID *string
	IsE2E       bool
}

type MessageReaction struct {
	Emoji   string   `json:"emoji"`
	UserIDs []string `json:"user_ids"`
}

type Chat struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	IsDirect           bool      `json:"is_direct"`
	Type               string    `json:"type"`
	Kind               string    `json:"kind"`
	ParentChatID       *string   `json:"parent_chat_id,omitempty"`
	ChannelType        *string   `json:"channel_type,omitempty"`
	TopicNumber        *int      `json:"topic_number,omitempty"`
	IsGeneral          *bool     `json:"is_general,omitempty"`
	BotID              *string   `json:"bot_id,omitempty"`
	PeerUserID         *string   `json:"peer_user_id,omitempty"`
	AvatarURL          *string   `json:"avatar_data_url,omitempty"`
	AvatarBg           *string   `json:"avatar_gradient,omitempty"`
	LastMessagePreview *string   `json:"last_message_preview,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type ChatMember struct {
	UserID   string    `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type ChatInvite struct {
	Token     string
	ChatID    string
	InviterID string
	InviteeID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Message struct {
	ID                       string            `json:"id"`
	ChatID                   string            `json:"chat_id"`
	UserID                   string            `json:"user_id"`
	SenderBotID              *string           `json:"sender_bot_id,omitempty"`
	Content                  string            `json:"content"`
	ReplyToMessageID         *string           `json:"reply_to_message_id,omitempty"`
	ReplyToMessagePreview    *string           `json:"reply_to_message_preview,omitempty"`
	ReplyToMessageSenderName *string           `json:"reply_to_message_sender_name,omitempty"`
	IsE2E                    bool              `json:"is_e2e"`
	E2E                      *E2EPayload       `json:"e2e,omitempty"`
	Reactions                []MessageReaction `json:"reactions,omitempty"`
	CreatedAt                time.Time         `json:"created_at"`
	EditedAt                 *time.Time        `json:"edited_at,omitempty"`
}

type E2EEnvelope struct {
	RecipientDeviceID string `json:"recipient_device_id"`
	Alg               string `json:"alg"`
	Header            string `json:"header"`
	Ciphertext        string `json:"ciphertext"`
}

type E2EPayload struct {
	SenderDeviceID string       `json:"sender_device_id"`
	Envelope       *E2EEnvelope `json:"envelope,omitempty"`
}

type MessagePage struct {
	Items      []Message `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type CreateChatInput struct {
	UserID    string
	Title     string
	MemberIDs []string
	Type      string
}

type CreateChannelInput struct {
	UserID      string
	GroupChatID string
	Title       string
	ChannelType string
}

type DeleteChannelInput struct {
	UserID        string
	GroupChatID   string
	ChannelChatID string
}

type OptionalString struct {
	Set   bool
	Value *string
}

type UpdateChatInput struct {
	UserID         string
	ChatID         string
	Title          OptionalString
	AvatarDataURL  OptionalString
	AvatarGradient OptionalString
}

type CreateMessageInput struct {
	UserID           string
	BotID            string
	ChatID           string
	Content          string
	ReplyToMessageID string
	AttachmentIDs    []string
	SenderDeviceID   string
	Envelopes        []E2EEnvelope
}

type ListMessagesInput struct {
	UserID   string
	ChatID   string
	Limit    int
	Cursor   string
	DeviceID string
}

type CreateDirectMessageInput struct {
	UserID           string
	RecipientUserID  string
	Content          string
	ReplyToMessageID string
	AttachmentIDs    []string
}

type ChatRepository interface {
	CreateChat(ctx context.Context, title string, memberIDs []string, creatorID string, chatType string) (Chat, error)
	CreateChannel(ctx context.Context, parentChatID, title, channelType, creatorID string) (Chat, error)
	DeleteChannel(ctx context.Context, parentChatID, channelChatID string) error
	FindDirectChatByMembers(ctx context.Context, userAID, userBID, chatType string) (Chat, bool, error)
	ListChatsByUser(ctx context.Context, userID string) ([]Chat, error)
	ListChannelsByParent(ctx context.Context, parentChatID, userID string) ([]Chat, error)
	GetChat(ctx context.Context, chatID string) (Chat, error)
	UpdateChat(ctx context.Context, input UpdateChatInput) (Chat, error)
	ListChatMembers(ctx context.Context, chatID string) ([]ChatMember, error)
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
	UpdateMessageContent(ctx context.Context, chatID, messageID, editorUserID, newContent string) (Message, error)
	GetMessageMeta(ctx context.Context, messageID string) (MessageMeta, error)
	SoftDeleteMessage(ctx context.Context, chatID, messageID, deleterUserID string) error
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

type UserMessageCreatedEvent struct {
	MessageID       string
	ChatID          string
	SenderUserID    string
	RecipientUserID string
	CreatedAt       time.Time
}

type DeviceMessageCreatedEvent struct {
	MessageID         string
	ChatID            string
	SenderUserID      string
	SenderDeviceID    string
	RecipientDeviceID string
	Alg               string
	Header            string
	Ciphertext        string
	CreatedAt         time.Time
}

type MessageStatus struct {
	MessageID string    `json:"message_id"`
	ChatID    string    `json:"chat_id"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MessageStatusEvent struct {
	MessageID       string
	ChatID          string
	UserID          string
	RecipientUserID string
	Status          string
	At              time.Time
}

type MessageUpdatedEvent struct {
	MessageID       string
	ChatID          string
	EditorUserID    string
	RecipientUserID string
	Content         string
	EditedAt        time.Time
}

type MessageReactionEvent struct {
	MessageID       string
	ChatID          string
	ActorUserID     string
	RecipientUserID string
	Emoji           string
	Action          string
	Reactions       []MessageReaction
	At              time.Time
}

type MessageDeletedEvent struct {
	MessageID       string
	ChatID          string
	ActorUserID     string
	RecipientUserID string
	At              time.Time
}

type EditMessageInput struct {
	UserID    string
	ChatID    string
	MessageID string
	Content   string
}

type ForwardMessageInput struct {
	UserID          string
	ChatID          string
	SourceMessageID string
}

type UpsertMessageStatusInput struct {
	UserID    string
	ChatID    string
	MessageID string
	Status    string
}

type Service struct {
	chats         ChatRepository
	messages      MessageRepository
	publisher     MessageEventPublisher
	statusRepo    StatusRepository
	notifications NotificationRepository
	avatars       AvatarStore
	avatarTTL     time.Duration
	invites       InviteRepository
	inviteTTL     time.Duration
}

var ErrChatNotFound = errors.New("chat not found")
var ErrMessageNotFound = errors.New("message not found")
var ErrInvalidAttachments = errors.New("invalid attachments")

const (
	avatarRefPrefix  = "s3key:"
	defaultAvatarTTL = time.Hour * 24 * 7
	defaultInviteTTL = time.Hour * 24 * 7
)

type AvatarStore interface {
	PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
}

func New(chats ChatRepository, messages MessageRepository) (*Service, error) {
	if chats == nil {
		return nil, errors.New("chat repository is required")
	}
	if messages == nil {
		return nil, errors.New("message repository is required")
	}
	return &Service{chats: chats, messages: messages}, nil
}

func NewWithPublisher(chats ChatRepository, messages MessageRepository, publisher MessageEventPublisher) (*Service, error) {
	svc, err := New(chats, messages)
	if err != nil {
		return nil, err
	}
	svc.publisher = publisher
	return svc, nil
}

func NewWithPublisherAndStatusRepo(chats ChatRepository, messages MessageRepository, publisher MessageEventPublisher, statusRepo StatusRepository) (*Service, error) {
	svc, err := NewWithPublisher(chats, messages, publisher)
	if err != nil {
		return nil, err
	}
	svc.statusRepo = statusRepo
	return svc, nil
}

func (s *Service) SetAvatarStore(store AvatarStore, ttl time.Duration) {
	s.avatars = store
	if ttl <= 0 {
		ttl = defaultAvatarTTL
	}
	s.avatarTTL = ttl
}

func (s *Service) SetNotificationRepository(repo NotificationRepository) {
	s.notifications = repo
}

func (s *Service) SetInviteRepository(repo InviteRepository, ttl time.Duration) {
	s.invites = repo
	if ttl <= 0 {
		ttl = defaultInviteTTL
	}
	s.inviteTTL = ttl
}

func (s *Service) resolveAvatarURL(ctx context.Context, raw *string) *string {
	if raw == nil {
		return nil
	}
	ref := strings.TrimSpace(*raw)
	if ref == "" {
		return nil
	}
	if !strings.HasPrefix(ref, avatarRefPrefix) {
		return &ref
	}
	if s.avatars == nil {
		return nil
	}
	objectKey := strings.TrimSpace(strings.TrimPrefix(ref, avatarRefPrefix))
	if objectKey == "" {
		return nil
	}
	ttl := s.avatarTTL
	if ttl <= 0 {
		ttl = defaultAvatarTTL
	}
	presigned, err := s.avatars.PresignGetObject(ctx, objectKey, ttl)
	if err != nil {
		return nil
	}
	return &presigned
}

func (s *Service) nowUTC() time.Time {
	return time.Now().UTC()
}
