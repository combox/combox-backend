package chat

import (
	"context"
	"errors"
	"strings"
)

func (s *Service) CreateChat(ctx context.Context, input CreateChatInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	title := strings.TrimSpace(input.Title)
	if userID == "" || title == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	chatType, ok := normalizeChatType(input.Type)
	if !ok {
		return Chat{}, invalidArg("error.chat.invalid_type")
	}

	uniqueMembers := dedupeMembers(append(input.MemberIDs, userID))
	if chatType == ChatTypeSecretE2E && len(uniqueMembers) != 2 {
		return Chat{}, invalidArg("error.chat.secret_must_be_direct")
	}
	if len(uniqueMembers) == 2 {
		existing, found, err := s.chats.FindDirectChatByMembers(ctx, uniqueMembers[0], uniqueMembers[1], chatType)
		if err != nil {
			return Chat{}, internal(err)
		}
		if found {
			return existing, nil
		}
	}

	created, err := s.chats.CreateChat(ctx, title, uniqueMembers, userID, chatType)
	if err != nil {
		return Chat{}, internal(err)
	}
	return created, nil
}

func (s *Service) CreateDirectMessage(ctx context.Context, input CreateDirectMessageInput) (Message, Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	recipientID := strings.TrimSpace(input.RecipientUserID)
	content := strings.TrimSpace(input.Content)
	if userID == "" || recipientID == "" {
		return Message{}, Chat{}, invalidArg("error.message.invalid_input")
	}
	if userID == recipientID {
		return Message{}, Chat{}, invalidArg("error.message.invalid_input")
	}
	if content == "" && len(input.AttachmentIDs) == 0 {
		return Message{}, Chat{}, invalidArg("error.message.invalid_input")
	}

	existing, found, err := s.chats.FindDirectChatByMembers(ctx, userID, recipientID, ChatTypeStandard)
	if err != nil {
		return Message{}, Chat{}, internal(err)
	}

	chatRef := existing
	if !found {
		created, err := s.chats.CreateChat(ctx, recipientID, []string{recipientID, userID}, userID, ChatTypeStandard)
		if err != nil {
			return Message{}, Chat{}, internal(err)
		}
		chatRef = created
	}

	msg, err := s.CreateMessage(ctx, CreateMessageInput{
		UserID:           userID,
		ChatID:           chatRef.ID,
		Content:          content,
		ReplyToMessageID: strings.TrimSpace(input.ReplyToMessageID),
		AttachmentIDs:    input.AttachmentIDs,
	})
	if err != nil {
		return Message{}, Chat{}, err
	}
	return msg, chatRef, nil
}

func (s *Service) ListChats(ctx context.Context, userID string) ([]Chat, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, invalidArg("error.auth.missing_user_context")
	}
	chats, err := s.chats.ListChatsByUser(ctx, userID)
	if err != nil {
		return nil, internal(err)
	}
	for i := range chats {
		chats[i].AvatarURL = s.resolveAvatarURL(ctx, chats[i].AvatarURL)
	}
	return chats, nil
}

func canCreateChannelByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin", "moderator":
		return true
	default:
		return false
	}
}

func canPostPublicChannelByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func canViewPublicChannelMembersByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func canManageMembersByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin", "moderator":
		return true
	default:
		return false
	}
}

func canManageRolesByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func normalizePublicSlug(raw string) string {
	slug := strings.TrimSpace(strings.ToLower(raw))
	slug = strings.TrimPrefix(slug, "@")
	return slug
}

func (s *Service) CreatePublicChannel(ctx context.Context, input CreatePublicChannelInput) (Chat, error) {
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

	created, err := s.chats.CreatePublicChannel(ctx, title, publicSlug, userID, input.IsPublic)
	if err != nil {
		return Chat{}, internal(err)
	}
	return created, nil
}

func (s *Service) OpenDirectChat(ctx context.Context, input OpenDirectChatInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	recipientID := strings.TrimSpace(input.RecipientUserID)
	if userID == "" || recipientID == "" || userID == recipientID {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	existing, found, err := s.chats.FindDirectChatByMembers(ctx, userID, recipientID, ChatTypeStandard)
	if err != nil {
		return Chat{}, internal(err)
	}
	if found {
		return existing, nil
	}

	created, err := s.chats.CreateChat(ctx, recipientID, []string{recipientID, userID}, userID, ChatTypeStandard)
	if err != nil {
		return Chat{}, internal(err)
	}
	return created, nil
}

func (s *Service) CreateChannel(ctx context.Context, input CreateChannelInput) (Chat, error) {
	userID := strings.TrimSpace(input.UserID)
	groupChatID := strings.TrimSpace(input.GroupChatID)
	title := strings.TrimSpace(input.Title)
	channelType := strings.TrimSpace(strings.ToLower(input.ChannelType))

	if userID == "" || groupChatID == "" || title == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if channelType == "" {
		channelType = "text"
	}
	if channelType != "text" && channelType != "voice" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	group, err := s.chats.GetChat(ctx, groupChatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if strings.TrimSpace(strings.ToLower(group.Kind)) != "group" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	if err := s.ensureChatMember(ctx, groupChatID, userID); err != nil {
		return Chat{}, err
	}

	role, err := s.chats.GetChatMemberRole(ctx, groupChatID, userID)
	if err != nil {
		return Chat{}, internal(err)
	}
	if !canCreateChannelByRole(role) {
		return Chat{}, forbidden("error.chat.forbidden")
	}

	created, err := s.chats.CreateChannel(ctx, groupChatID, title, channelType, userID)
	if err != nil {
		return Chat{}, internal(err)
	}
	return created, nil
}

func (s *Service) DeleteChannel(ctx context.Context, input DeleteChannelInput) error {
	userID := strings.TrimSpace(input.UserID)
	groupChatID := strings.TrimSpace(input.GroupChatID)
	channelChatID := strings.TrimSpace(input.ChannelChatID)
	if userID == "" || groupChatID == "" || channelChatID == "" {
		return invalidArg("error.chat.invalid_input")
	}

	// General is the group root.
	if channelChatID == groupChatID {
		return forbidden("error.chat.forbidden")
	}

	group, err := s.chats.GetChat(ctx, groupChatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if strings.TrimSpace(strings.ToLower(group.Kind)) != "group" {
		return invalidArg("error.chat.invalid_input")
	}

	if err := s.ensureChatMember(ctx, groupChatID, userID); err != nil {
		return err
	}

	role, err := s.chats.GetChatMemberRole(ctx, groupChatID, userID)
	if err != nil {
		return internal(err)
	}
	if !canCreateChannelByRole(role) {
		return forbidden("error.chat.forbidden")
	}

	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if strings.TrimSpace(strings.ToLower(channel.Kind)) != "channel" {
		return invalidArg("error.chat.invalid_input")
	}
	if channel.ParentChatID == nil || strings.TrimSpace(*channel.ParentChatID) != groupChatID {
		return invalidArg("error.chat.invalid_input")
	}
	if channel.TopicNumber == nil || *channel.TopicNumber < 2 {
		return forbidden("error.chat.forbidden")
	}

	if err := s.chats.DeleteChannel(ctx, groupChatID, channelChatID); err != nil {
		return mapChatOrMessageRepoError(err)
	}
	return nil
}

func (s *Service) ListChannels(ctx context.Context, userID, groupChatID string) ([]Chat, error) {
	userID = strings.TrimSpace(userID)
	groupChatID = strings.TrimSpace(groupChatID)
	if userID == "" || groupChatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}

	if err := s.ensureChatMember(ctx, groupChatID, userID); err != nil {
		return nil, err
	}

	items, err := s.chats.ListChannelsByParent(ctx, groupChatID, userID)
	if err != nil {
		return nil, internal(err)
	}

	// Virtual General topic: messages live on the group chat itself.
	one := 1
	trueVal := true
	general := Chat{ID: groupChatID, Title: "General", Kind: "group", TopicNumber: &one, IsGeneral: &trueVal}
	for i := range items {
		items[i].IsGeneral = nil
	}
	return append([]Chat{general}, items...), nil
}

func (s *Service) ListMembers(ctx context.Context, userID, chatID string, includeBanned bool) ([]ChatMember, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return nil, err
	}
	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if strings.TrimSpace(strings.ToLower(target.Kind)) == "public_channel" {
		role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
		if err != nil {
			return nil, internal(err)
		}
		if !canViewPublicChannelMembersByRole(role) {
			return nil, forbidden("error.chat.forbidden")
		}
	}

	items, err := s.chats.ListChatMembers(ctx, chatID, includeBanned)
	if err != nil {
		return nil, internal(err)
	}
	return items, nil
}

func (s *Service) SubscribePublicChannel(ctx context.Context, userID, chatID string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, mapChatOrMessageRepoError(err)
	}
	if strings.TrimSpace(strings.ToLower(target.Kind)) != "public_channel" || !target.IsPublic {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	switch {
	case err == nil:
		if strings.EqualFold(role, "banned") {
			return Chat{}, forbidden("error.chat.forbidden")
		}
		return target, nil
	case errors.Is(err, ErrChatNotFound):
		// continue
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
	return updated, nil
}

func (s *Service) UnsubscribePublicChannel(ctx context.Context, userID, chatID string) error {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return invalidArg("error.chat.invalid_input")
	}

	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if strings.TrimSpace(strings.ToLower(target.Kind)) != "public_channel" || !target.IsPublic {
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

func (s *Service) AddMembers(ctx context.Context, userID, chatID string, memberIDs []string) ([]ChatMember, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if target.IsDirect {
		return nil, invalidArg("error.chat.invalid_input")
	}
	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		return nil, internal(err)
	}
	if !canManageMembersByRole(role) {
		return nil, forbidden("error.chat.forbidden")
	}
	existingMemberIDs, err := s.chats.ListChatMemberIDs(ctx, chatID)
	if err != nil {
		return nil, internal(err)
	}
	existingSet := make(map[string]struct{}, len(existingMemberIDs))
	for _, memberID := range existingMemberIDs {
		memberID = strings.TrimSpace(memberID)
		if memberID != "" {
			existingSet[memberID] = struct{}{}
		}
	}
	nextMembers := make([]string, 0, len(memberIDs))
	for _, memberID := range dedupeMembers(memberIDs) {
		memberID = strings.TrimSpace(memberID)
		if memberID == "" || memberID == userID {
			continue
		}
		if _, exists := existingSet[memberID]; exists {
			continue
		}
		nextMembers = append(nextMembers, memberID)
	}
	if len(nextMembers) == 0 {
		return nil, invalidArg("error.chat.invalid_input")
	}
	if s.invites != nil {
		ttl := s.inviteTTL
		if ttl <= 0 {
			ttl = defaultInviteTTL
		}
		for _, memberID := range nextMembers {
			invite, err := s.invites.Create(ctx, chatID, userID, memberID, ttl)
			if err != nil {
				return nil, internal(err)
			}
			_, _, _ = s.CreateDirectMessage(ctx, CreateDirectMessageInput{
				UserID:          userID,
				RecipientUserID: memberID,
				Content:         "You were invited to chat \"" + target.Title + "\"\nhttps://app.combox.local/#invite:" + invite.Token,
			})
		}
		return s.ListMembers(ctx, userID, chatID, false)
	}
	if err := s.chats.AddChatMembers(ctx, chatID, nextMembers); err != nil {
		return nil, internal(err)
	}
	return s.ListMembers(ctx, userID, chatID, false)
}

func (s *Service) UpdateMemberRole(ctx context.Context, actorUserID, chatID, targetUserID, role string) ([]ChatMember, error) {
	actorUserID = strings.TrimSpace(actorUserID)
	chatID = strings.TrimSpace(chatID)
	targetUserID = strings.TrimSpace(targetUserID)
	role = strings.TrimSpace(strings.ToLower(role))
	if actorUserID == "" || chatID == "" || targetUserID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	target, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	validRole := false
	switch strings.TrimSpace(strings.ToLower(target.Kind)) {
	case "public_channel":
		switch role {
		case "subscriber", "admin", "banned":
			validRole = true
		}
	default:
		switch role {
		case "member", "moderator", "admin":
			validRole = true
		}
	}
	if !validRole {
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

func (s *Service) RemoveMember(ctx context.Context, actorUserID, chatID, targetUserID string) ([]ChatMember, error) {
	actorUserID = strings.TrimSpace(actorUserID)
	chatID = strings.TrimSpace(chatID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorUserID == "" || chatID == "" || targetUserID == "" || actorUserID == targetUserID {
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
