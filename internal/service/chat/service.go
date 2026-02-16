package chat

import (
	"context"
	"errors"
	"fmt"
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

type MessageMeta struct {
	ID     string
	ChatID string
	UserID string
	IsE2E  bool
}

func (s *Service) EditMessage(ctx context.Context, input EditMessageInput) (Message, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	messageID := strings.TrimSpace(input.MessageID)
	newContent := strings.TrimSpace(input.Content)
	if userID == "" || chatID == "" || messageID == "" || newContent == "" {
		return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input"}
	}

	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return Message{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if !member {
		return Message{}, &Error{Code: CodeForbidden, MessageKey: "error.chat.forbidden"}
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		if errors.Is(err, ErrChatNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: err}
		}
		return Message{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	chatType := strings.TrimSpace(chatMeta.Type)
	if chatType == "" {
		chatType = ChatTypeStandard
	}
	if chatType != ChatTypeStandard {
		return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.edit_not_allowed"}
	}

	updated, repoErr := s.messages.UpdateMessageContent(ctx, chatID, messageID, userID, newContent)
	if repoErr != nil {
		if errors.Is(repoErr, ErrMessageNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.message.not_found", Cause: repoErr}
		}
		if errors.Is(repoErr, ErrChatNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: repoErr}
		}
		return Message{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: repoErr}
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
		return MessageStatus{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input"}
	}

	meta, err := s.messages.GetMessageMeta(ctx, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return MessageStatus{}, &Error{Code: CodeNotFound, MessageKey: "error.message.not_found", Cause: err}
		}
		return MessageStatus{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	return s.UpsertMessageStatus(ctx, UpsertMessageStatusInput{
		UserID:    userID,
		ChatID:    meta.ChatID,
		MessageID: meta.ID,
		Status:    "read",
	})
}

func (s *Service) nowUTC() time.Time {
	return time.Now().UTC()
}

func (s *Service) EditMessageByID(ctx context.Context, userID, messageID, content string) (Message, error) {
	userID = strings.TrimSpace(userID)
	messageID = strings.TrimSpace(messageID)
	content = strings.TrimSpace(content)
	if userID == "" || messageID == "" || content == "" {
		return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input"}
	}

	meta, err := s.messages.GetMessageMeta(ctx, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.message.not_found", Cause: err}
		}
		return Message{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	return s.EditMessage(ctx, EditMessageInput{
		UserID:    userID,
		ChatID:    meta.ChatID,
		MessageID: meta.ID,
		Content:   content,
	})
}

func (s *Service) DeleteMessageByID(ctx context.Context, userID, messageID string) error {
	userID = strings.TrimSpace(userID)
	messageID = strings.TrimSpace(messageID)
	if userID == "" || messageID == "" {
		return &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input"}
	}

	meta, err := s.messages.GetMessageMeta(ctx, messageID)
	if err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return &Error{Code: CodeNotFound, MessageKey: "error.message.not_found", Cause: err}
		}
		return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	member, err := s.chats.IsChatMember(ctx, meta.ChatID, userID)
	if err != nil {
		return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if !member {
		return &Error{Code: CodeForbidden, MessageKey: "error.chat.forbidden"}
	}

	chatMeta, err := s.chats.GetChat(ctx, meta.ChatID)
	if err != nil {
		if errors.Is(err, ErrChatNotFound) {
			return &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: err}
		}
		return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	chatType := strings.TrimSpace(chatMeta.Type)
	if chatType == "" {
		chatType = ChatTypeStandard
	}
	if chatType != ChatTypeStandard {
		return &Error{Code: CodeInvalidArgument, MessageKey: "error.message.edit_not_allowed"}
	}

	if err := s.messages.SoftDeleteMessage(ctx, meta.ChatID, meta.ID, userID); err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return &Error{Code: CodeNotFound, MessageKey: "error.message.not_found", Cause: err}
		}
		if errors.Is(err, ErrChatNotFound) {
			return &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: err}
		}
		return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	return nil
}

func (s *Service) ForwardMessage(ctx context.Context, input ForwardMessageInput) (Message, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	sourceMessageID := strings.TrimSpace(input.SourceMessageID)
	if userID == "" || chatID == "" || sourceMessageID == "" {
		return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input"}
	}

	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return Message{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if !member {
		return Message{}, &Error{Code: CodeForbidden, MessageKey: "error.chat.forbidden"}
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		if errors.Is(err, ErrChatNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: err}
		}
		return Message{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	chatType := strings.TrimSpace(chatMeta.Type)
	if chatType == "" {
		chatType = ChatTypeStandard
	}
	if chatType != ChatTypeStandard {
		return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.e2e_not_allowed_in_standard"}
	}

	created, repoErr := s.messages.CreateForwardedMessage(ctx, chatID, sourceMessageID, userID)
	if repoErr != nil {
		if errors.Is(repoErr, ErrChatNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: repoErr}
		}
		if errors.Is(repoErr, ErrMessageNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.message.not_found", Cause: repoErr}
		}
		return Message{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: repoErr}
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

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

type Chat struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	IsDirect  bool      `json:"is_direct"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	ID        string      `json:"id"`
	ChatID    string      `json:"chat_id"`
	UserID    string      `json:"user_id"`
	Content   string      `json:"content"`
	IsE2E     bool        `json:"is_e2e"`
	E2E       *E2EPayload `json:"e2e,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	EditedAt  *time.Time  `json:"edited_at,omitempty"`
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

type CreateMessageInput struct {
	UserID         string
	ChatID         string
	Content        string
	AttachmentIDs  []string
	SenderDeviceID string
	Envelopes      []E2EEnvelope
}

type ListMessagesInput struct {
	UserID   string
	ChatID   string
	Limit    int
	Cursor   string
	DeviceID string
}

type ChatRepository interface {
	CreateChat(ctx context.Context, title string, memberIDs []string, creatorID string, chatType string) (Chat, error)
	ListChatsByUser(ctx context.Context, userID string) ([]Chat, error)
	GetChat(ctx context.Context, chatID string) (Chat, error)
	ListChatMemberIDs(ctx context.Context, chatID string) ([]string, error)
	IsChatMember(ctx context.Context, chatID, userID string) (bool, error)
}

type MessageRepository interface {
	CreateMessage(ctx context.Context, chatID, userID, content string) (Message, error)
	CreateMessageWithAttachments(ctx context.Context, chatID, userID, content string, attachmentIDs []string) (Message, error)
	CreateMessageE2E(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope) (Message, error)
	CreateMessageE2EWithAttachments(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, attachmentIDs []string) (Message, error)
	CreateForwardedMessage(ctx context.Context, chatID, sourceMessageID, userID string) (Message, error)
	ListMessages(ctx context.Context, chatID string, limit int, cursor string) (MessagePage, error)
	ListMessagesForDevice(ctx context.Context, chatID, deviceID string, limit int, cursor string) (MessagePage, error)
	UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string) (MessageStatus, error)
	UpdateMessageContent(ctx context.Context, chatID, messageID, editorUserID, newContent string) (Message, error)
	GetMessageMeta(ctx context.Context, messageID string) (MessageMeta, error)
	SoftDeleteMessage(ctx context.Context, chatID, messageID, deleterUserID string) error
}

type StatusRepository interface {
	UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string, at time.Time) (MessageStatus, error)
}

type MessageEventPublisher interface {
	PublishDeviceMessageCreated(ctx context.Context, ev DeviceMessageCreatedEvent) error
	PublishUserMessageCreated(ctx context.Context, ev UserMessageCreatedEvent) error
	PublishMessageStatus(ctx context.Context, ev MessageStatusEvent) error
	PublishMessageUpdated(ctx context.Context, ev MessageUpdatedEvent) error
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
	MessageID string
	ChatID    string
	UserID    string
	Status    string
	At        time.Time
}

type MessageUpdatedEvent struct {
	MessageID       string
	ChatID          string
	EditorUserID    string
	RecipientUserID string
	Content         string
	EditedAt        time.Time
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
	chats      ChatRepository
	messages   MessageRepository
	publisher  MessageEventPublisher
	statusRepo StatusRepository
}

var ErrChatNotFound = errors.New("chat not found")
var ErrMessageNotFound = errors.New("message not found")
var ErrInvalidAttachments = errors.New("invalid attachments")

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

func (s *Service) CreateChat(ctx context.Context, input CreateChatInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	title := strings.TrimSpace(input.Title)
	chatType := strings.TrimSpace(input.Type)
	if chatType == "" {
		chatType = ChatTypeStandard
	}
	if userID == "" || title == "" {
		return Chat{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.chat.invalid_input",
		}
	}
	if chatType != ChatTypeStandard && chatType != ChatTypeSecretE2E {
		return Chat{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.chat.invalid_type"}
	}

	uniqueMembers := dedupeMembers(append(input.MemberIDs, userID))
	if chatType == ChatTypeSecretE2E && len(uniqueMembers) != 2 {
		return Chat{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.chat.secret_must_be_direct"}
	}

	created, err := s.chats.CreateChat(ctx, title, uniqueMembers, userID, chatType)
	if err != nil {
		return Chat{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	return created, nil
}

func (s *Service) ListChats(ctx context.Context, userID string) ([]Chat, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.missing_user_context",
		}
	}
	chats, err := s.chats.ListChatsByUser(ctx, userID)
	if err != nil {
		return nil, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	return chats, nil
}

func (s *Service) CreateMessage(ctx context.Context, input CreateMessageInput) (Message, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	content := strings.TrimSpace(input.Content)
	senderDeviceID := strings.TrimSpace(input.SenderDeviceID)
	attachmentIDs := make([]string, 0, len(input.AttachmentIDs))
	for _, id := range input.AttachmentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		attachmentIDs = append(attachmentIDs, id)
	}

	if userID == "" || chatID == "" {
		return Message{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.message.invalid_input",
		}
	}

	isE2E := len(input.Envelopes) > 0 || senderDeviceID != ""
	if !isE2E {
		if content == "" && len(attachmentIDs) == 0 {
			return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input"}
		}
	}
	if isE2E {
		if senderDeviceID == "" || len(input.Envelopes) == 0 {
			return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_e2e_payload"}
		}
		for _, env := range input.Envelopes {
			if strings.TrimSpace(env.RecipientDeviceID) == "" || strings.TrimSpace(env.Alg) == "" || strings.TrimSpace(env.Header) == "" || strings.TrimSpace(env.Ciphertext) == "" {
				return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_e2e_payload"}
			}
		}
	}

	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return Message{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	if !member {
		return Message{}, &Error{
			Code:       CodeForbidden,
			MessageKey: "error.chat.forbidden",
		}
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		if errors.Is(err, ErrChatNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: err}
		}
		return Message{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	chatType := strings.TrimSpace(chatMeta.Type)
	if chatType == "" {
		chatType = ChatTypeStandard
	}
	if chatType == ChatTypeStandard && isE2E {
		return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.e2e_not_allowed_in_standard"}
	}
	if chatType == ChatTypeSecretE2E {
		if !chatMeta.IsDirect {
			return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.chat.secret_must_be_direct"}
		}
		if !isE2E {
			return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.plaintext_not_allowed_in_secret"}
		}
	}

	var message Message
	var repoErr error
	if isE2E {
		if len(attachmentIDs) > 0 {
			message, repoErr = s.messages.CreateMessageE2EWithAttachments(ctx, chatID, userID, senderDeviceID, input.Envelopes, attachmentIDs)
		} else {
			message, repoErr = s.messages.CreateMessageE2E(ctx, chatID, userID, senderDeviceID, input.Envelopes)
		}
	} else {
		if len(attachmentIDs) > 0 {
			message, repoErr = s.messages.CreateMessageWithAttachments(ctx, chatID, userID, content, attachmentIDs)
		} else {
			message, repoErr = s.messages.CreateMessage(ctx, chatID, userID, content)
		}
	}
	if repoErr != nil {
		if errors.Is(repoErr, ErrChatNotFound) {
			return Message{}, &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: repoErr}
		}
		if errors.Is(repoErr, ErrInvalidAttachments) {
			return Message{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input", Cause: repoErr}
		}
		return Message{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      repoErr,
		}
	}

	if isE2E && s.publisher != nil {
		for _, env := range input.Envelopes {
			ev := DeviceMessageCreatedEvent{
				MessageID:         message.ID,
				ChatID:            message.ChatID,
				SenderUserID:      userID,
				SenderDeviceID:    senderDeviceID,
				RecipientDeviceID: env.RecipientDeviceID,
				Alg:               env.Alg,
				Header:            env.Header,
				Ciphertext:        env.Ciphertext,
				CreatedAt:         message.CreatedAt,
			}
			_ = s.publisher.PublishDeviceMessageCreated(ctx, ev)
		}
	}
	if !isE2E && s.publisher != nil {
		members, err := s.chats.ListChatMemberIDs(ctx, chatID)
		if err == nil {
			for _, memberID := range members {
				ev := UserMessageCreatedEvent{
					MessageID:       message.ID,
					ChatID:          message.ChatID,
					SenderUserID:    userID,
					RecipientUserID: memberID,
					CreatedAt:       message.CreatedAt,
				}
				_ = s.publisher.PublishUserMessageCreated(ctx, ev)
			}
		}
	}
	return message, nil
}

func (s *Service) ListMessages(ctx context.Context, input ListMessagesInput) (MessagePage, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	deviceID := strings.TrimSpace(input.DeviceID)
	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	if userID == "" || chatID == "" {
		return MessagePage{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.chat.invalid_input",
		}
	}

	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return MessagePage{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	if !member {
		return MessagePage{}, &Error{
			Code:       CodeForbidden,
			MessageKey: "error.chat.forbidden",
		}
	}

	var page MessagePage
	var repoErr error
	if deviceID != "" {
		page, repoErr = s.messages.ListMessagesForDevice(ctx, chatID, deviceID, limit, strings.TrimSpace(input.Cursor))
	} else {
		page, repoErr = s.messages.ListMessages(ctx, chatID, limit, strings.TrimSpace(input.Cursor))
	}
	if repoErr != nil {
		return MessagePage{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      repoErr,
		}
	}
	return page, nil
}

func (s *Service) UpsertMessageStatus(ctx context.Context, input UpsertMessageStatusInput) (MessageStatus, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	messageID := strings.TrimSpace(input.MessageID)
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if userID == "" || chatID == "" || messageID == "" || status == "" {
		return MessageStatus{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_input"}
	}
	if status != "delivered" && status != "read" {
		return MessageStatus{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.message.invalid_status"}
	}

	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return MessageStatus{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if !member {
		return MessageStatus{}, &Error{Code: CodeForbidden, MessageKey: "error.chat.forbidden"}
	}

	var updated MessageStatus
	var repoErr error
	if s.statusRepo != nil {
		updated, repoErr = s.statusRepo.UpsertMessageStatus(ctx, chatID, messageID, userID, status, s.nowUTC())
	} else {
		updated, repoErr = s.messages.UpsertMessageStatus(ctx, chatID, messageID, userID, status)
	}
	if repoErr != nil {
		if errors.Is(repoErr, ErrChatNotFound) {
			return MessageStatus{}, &Error{Code: CodeNotFound, MessageKey: "error.chat.not_found", Cause: repoErr}
		}
		if errors.Is(repoErr, ErrMessageNotFound) {
			return MessageStatus{}, &Error{Code: CodeNotFound, MessageKey: "error.message.not_found", Cause: repoErr}
		}
		return MessageStatus{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: repoErr}
	}

	if s.publisher != nil {
		_ = s.publisher.PublishMessageStatus(ctx, MessageStatusEvent{
			MessageID: updated.MessageID,
			ChatID:    updated.ChatID,
			UserID:    updated.UserID,
			Status:    updated.Status,
			At:        updated.UpdatedAt,
		})
	}
	return updated, nil
}

func dedupeMembers(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
