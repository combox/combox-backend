package chat

import (
	"context"
	"errors"
	"strings"
	"time"
)

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
	standalone    *StandaloneChannelMessagePolicyService
	messageSvc    *MessageService
}

var ErrChatNotFound = errors.New("chat not found")
var ErrMessageNotFound = errors.New("message not found")
var ErrInvalidAttachments = errors.New("invalid attachments")

const (
	avatarRefPrefix  = "s3key:"
	defaultAvatarTTL = time.Hour * 24 * 7
	defaultInviteTTL = time.Hour * 24 * 7
)

func New(chats ChatRepository, messages MessageRepository) (*Service, error) {
	if chats == nil {
		return nil, errors.New("chat repository is required")
	}
	if messages == nil {
		return nil, errors.New("message repository is required")
	}
	svc := &Service{chats: chats, messages: messages}
	svc.standalone = NewStandaloneChannelMessagePolicyService(chats, messages)
	svc.messageSvc = NewMessageService(svc)
	return svc, nil
}

func NewWithPublisher(chats ChatRepository, messages MessageRepository, publisher MessageEventPublisher) (*Service, error) {
	svc, err := New(chats, messages)
	if err != nil {
		return nil, err
	}
	svc.publisher = publisher
	if svc.messageSvc != nil {
		svc.messageSvc.publisher = publisher
	}
	return svc, nil
}

func NewWithPublisherAndStatusRepo(chats ChatRepository, messages MessageRepository, publisher MessageEventPublisher, statusRepo StatusRepository) (*Service, error) {
	svc, err := NewWithPublisher(chats, messages, publisher)
	if err != nil {
		return nil, err
	}
	svc.statusRepo = statusRepo
	if svc.messageSvc != nil {
		svc.messageSvc.statusRepo = statusRepo
	}
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
	if s.messageSvc != nil {
		s.messageSvc.notifications = repo
	}
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
