package chat

import (
	"context"
	"errors"
	"strings"
	"time"
)

type StandaloneChannelService struct {
	chats     ChatRepository
	avatars   AvatarStore
	avatarTTL time.Duration
}

func NewStandaloneChannelService(chats ChatRepository) (*StandaloneChannelService, error) {
	if chats == nil {
		return nil, errors.New("chat repository is required")
	}
	return &StandaloneChannelService{chats: chats}, nil
}

func (s *StandaloneChannelService) SetAvatarStore(store AvatarStore, ttl time.Duration) {
	s.avatars = store
	if ttl <= 0 {
		ttl = defaultAvatarTTL
	}
	s.avatarTTL = ttl
}

func (s *StandaloneChannelService) resolveAvatarURL(ctx context.Context, raw *string) *string {
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

func (s *StandaloneChannelService) ensureChannelMember(ctx context.Context, chatID, userID string) error {
	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return internal(err)
	}
	if !member {
		return forbidden("error.chat.forbidden")
	}
	return nil
}

func (s *StandaloneChannelService) getChatRole(ctx context.Context, chatID, userID string) (string, bool, error) {
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

func (s *StandaloneChannelService) getAccess(ctx context.Context, chatMeta Chat, userID string) (standaloneChannelAccess, error) {
	role, hasRole, err := s.getChatRole(ctx, chatMeta.ID, userID)
	if err != nil {
		return standaloneChannelAccess{}, err
	}

	resolved := resolveStandaloneChannelAccess(chatMeta, role, hasRole)
	return resolved, nil
}

func resolveStandaloneChannelAccess(chatMeta Chat, role string, hasRole bool) standaloneChannelAccess {
	resolved := resolveStandaloneChannelPolicy(chatMeta, role, hasRole)
	return standaloneChannelAccess{
		Role:       resolved.Role,
		HasRole:    resolved.HasRole,
		IsBanned:   resolved.IsBanned,
		CanPost:    resolved.CanPost,
		CanComment: resolved.CanComment,
		CanReact:   resolved.CanReact,
	}
}

func (s *StandaloneChannelService) CreateStandaloneChannel(ctx context.Context, input CreateStandaloneChannelInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	title := strings.TrimSpace(input.Title)
	publicSlug := normalizePublicSlug(input.PublicSlug)
	if userID == "" || title == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if input.IsPublic && publicSlug == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if !input.IsPublic {
		publicSlug = ""
	}

	created, err := s.chats.CreateStandaloneChannel(ctx, title, publicSlug, userID, input.IsPublic)
	if err != nil {
		return Chat{}, internal(err)
	}
	created.AvatarURL = s.resolveAvatarURL(ctx, created.AvatarURL)
	return created, nil
}

func (s *StandaloneChannelService) GetChannel(ctx context.Context, userID, chatID string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(target) {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	if isOpenStandaloneChannel(target) {
		access, accessErr := s.getAccess(ctx, target, userID)
		if accessErr != nil {
			return Chat{}, accessErr
		}
		if access.IsBanned {
			return Chat{}, forbidden("error.chat.forbidden")
		}
		if access.HasRole {
			roleCopy := access.Role
			target.ViewerRole = &roleCopy
		} else {
			target.ViewerRole = nil
		}
		target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
		return target, nil
	}

	if err := s.ensureChannelMember(ctx, chatID, userID); err != nil {
		return Chat{}, err
	}
	target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
	return target, nil
}

func (s *StandaloneChannelService) UpdateChannel(ctx context.Context, input UpdateChatInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if !input.Title.Set && !input.AvatarDataURL.Set && !input.AvatarGradient.Set && !input.CommentsEnabled.Set && !input.ReactionsEnabled.Set && !input.IsPublic.Set && !input.PublicSlug.Set {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(target) || target.IsDirect {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return Chat{}, internal(err)
	}
	if !canEditChatByRole(role) {
		return Chat{}, forbidden("error.chat.forbidden")
	}

	if input.Title.Set && input.Title.Value != nil {
		value := strings.TrimSpace(*input.Title.Value)
		if value == "" {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		input.Title.Value = &value
	}
	if input.AvatarDataURL.Set && input.AvatarDataURL.Value != nil {
		value := strings.TrimSpace(*input.AvatarDataURL.Value)
		if value == "" {
			input.AvatarDataURL.Value = nil
		} else if strings.HasPrefix(strings.ToLower(value), "data:") {
			objectKey, err := uploadAvatarDataURL(ctx, s.avatars, value)
			if err != nil {
				return Chat{}, invalidArg("error.chat.invalid_input")
			}
			ref := avatarRefPrefix + objectKey
			input.AvatarDataURL.Value = &ref
		} else {
			input.AvatarDataURL.Value = &value
		}
	}
	if input.AvatarGradient.Set && input.AvatarGradient.Value != nil {
		value := strings.TrimSpace(*input.AvatarGradient.Value)
		if value == "" {
			input.AvatarGradient.Value = nil
		} else {
			input.AvatarGradient.Value = &value
		}
	}
	if input.IsPublic.Set {
		if !input.IsPublic.Value {
			input.PublicSlug = OptionalString{Set: true, Value: nil}
		} else if !input.PublicSlug.Set {
			existingSlug := normalizePublicSlug(derefString(target.PublicSlug))
			if existingSlug == "" {
				return Chat{}, invalidArg("error.chat.invalid_input")
			}
			input.PublicSlug = OptionalString{Set: true, Value: &existingSlug}
		}
	}
	if input.PublicSlug.Set {
		if input.PublicSlug.Value != nil {
			value := normalizePublicSlug(*input.PublicSlug.Value)
			if value == "" {
				input.PublicSlug.Value = nil
			} else {
				input.PublicSlug.Value = &value
			}
		}
		nextPublic := target.IsPublic
		if input.IsPublic.Set {
			nextPublic = input.IsPublic.Value
		}
		if nextPublic && input.PublicSlug.Value == nil {
			return Chat{}, invalidArg("error.chat.invalid_input")
		}
		if !nextPublic {
			input.PublicSlug.Value = nil
		}
	}

	updated, err := s.chats.UpdateChat(ctx, input)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	updated.AvatarURL = s.resolveAvatarURL(ctx, updated.AvatarURL)
	return updated, nil
}

func (s *StandaloneChannelService) ListMembers(ctx context.Context, userID, chatID string, includeBanned bool) ([]ChatMember, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(target) {
		return nil, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChannelMember(ctx, chatID, userID); err != nil {
		return nil, err
	}
	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return nil, internal(err)
	}
	if !canViewStandaloneChannelMembersByRole(role) {
		return nil, forbidden("error.chat.forbidden")
	}
	items, err := s.chats.ListChatMembers(ctx, chatID, includeBanned)
	if err != nil {
		return nil, internal(err)
	}
	return items, nil
}

func (s *StandaloneChannelService) SubscribeChannel(ctx context.Context, userID, chatID string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(target) || !target.IsPublic {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	switch {
	case err == nil:
		if strings.EqualFold(role, "banned") {
			return Chat{}, forbidden("error.chat.forbidden")
		}
		target.AvatarURL = s.resolveAvatarURL(ctx, target.AvatarURL)
		return target, nil
	case errors.Is(err, ErrChatNotFound):
	default:
		return Chat{}, internal(err)
	}

	if err := s.chats.AddChatMembers(ctx, chatID, []string{userID}); err != nil {
		return Chat{}, internal(err)
	}
	if err := s.chats.UpdateChatMemberRole(ctx, chatID, userID, "subscriber"); err != nil {
		return Chat{}, internal(err)
	}

	updated, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	updated.AvatarURL = s.resolveAvatarURL(ctx, updated.AvatarURL)
	return updated, nil
}

func (s *StandaloneChannelService) UnsubscribeChannel(ctx context.Context, userID, chatID string) error {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(target) || !target.IsPublic {
		return invalidArg("error.chat.invalid_input")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return internal(err)
	}
	if strings.EqualFold(role, "owner") {
		return forbidden("error.chat.forbidden")
	}
	if err := s.chats.RemoveChatMember(ctx, chatID, userID); err != nil {
		return internal(err)
	}
	return nil
}

func (s *StandaloneChannelService) UpdateMemberRole(ctx context.Context, actorUserID, chatID, targetUserID, role string) ([]ChatMember, error) {
	actorUserID = strings.TrimSpace(actorUserID)
	chatID = strings.TrimSpace(chatID)
	targetUserID = strings.TrimSpace(targetUserID)
	role = strings.TrimSpace(strings.ToLower(role))
	if actorUserID == "" || chatID == "" || targetUserID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	switch role {
	case "subscriber", "admin", "banned":
	default:
		return nil, invalidArg("error.chat.invalid_input")
	}
	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(target) {
		return nil, invalidArg("error.chat.invalid_input")
	}
	actorRole, err := s.chats.GetChatMemberRole(ctx, chatID, actorUserID)
	if err != nil {
		return nil, internal(err)
	}
	if !canManageRolesByRole(actorRole) {
		return nil, forbidden("error.chat.forbidden")
	}
	targetRole, err := s.chats.GetChatMemberRole(ctx, chatID, targetUserID)
	if err != nil {
		return nil, internal(err)
	}
	if strings.EqualFold(targetRole, "owner") {
		return nil, forbidden("error.chat.forbidden")
	}
	if err := s.chats.UpdateChatMemberRole(ctx, chatID, targetUserID, role); err != nil {
		return nil, internal(err)
	}
	return s.ListMembers(ctx, actorUserID, chatID, false)
}

func (s *StandaloneChannelService) RemoveMember(ctx context.Context, actorUserID, chatID, targetUserID string) ([]ChatMember, error) {
	actorUserID = strings.TrimSpace(actorUserID)
	chatID = strings.TrimSpace(chatID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorUserID == "" || chatID == "" || targetUserID == "" || actorUserID == targetUserID {
		return nil, invalidArg("error.chat.invalid_input")
	}
	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(target) {
		return nil, invalidArg("error.chat.invalid_input")
	}
	actorRole, err := s.chats.GetChatMemberRole(ctx, chatID, actorUserID)
	if err != nil {
		return nil, internal(err)
	}
	if !canManageMembersByRole(actorRole) {
		return nil, forbidden("error.chat.forbidden")
	}
	targetRole, err := s.chats.GetChatMemberRole(ctx, chatID, targetUserID)
	if err != nil {
		return nil, internal(err)
	}
	if strings.EqualFold(targetRole, "owner") {
		return nil, forbidden("error.chat.forbidden")
	}
	if err := s.chats.RemoveChatMember(ctx, chatID, targetUserID); err != nil {
		return nil, internal(err)
	}
	return s.ListMembers(ctx, actorUserID, chatID, false)
}
