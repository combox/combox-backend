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
	kind := strings.TrimSpace(strings.ToLower(input.Kind))
	if kind != "" && kind != ChatKindGroup && kind != ChatKindDirect {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	// An explicitly requested group must never be collapsed into a direct chat.
	forceGroup := kind == ChatKindGroup

	uniqueMembers := dedupeMembers(append(input.MemberIDs, userID))
	if chatType == ChatTypeSecretE2E && (len(uniqueMembers) != 2 || forceGroup) {
		return Chat{}, invalidArg("error.chat.secret_must_be_direct")
	}
	if !forceGroup && len(uniqueMembers) == 2 {
		existing, found, err := s.chats.FindDirectChatByMembers(ctx, uniqueMembers[0], uniqueMembers[1], chatType)
		if err != nil {
			return Chat{}, internal(err)
		}
		if found {
			return existing, nil
		}
	}

	created, err := s.chats.CreateChat(ctx, title, uniqueMembers, userID, chatType, kind)
	if err != nil {
		return Chat{}, internal(err)
	}
	return created, nil
}

func (s *Service) BanPublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error {
	actorUserID = strings.TrimSpace(actorUserID)
	channelChatID = strings.TrimSpace(channelChatID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorUserID == "" || channelChatID == "" || targetUserID == "" {
		return invalidArg("error.chat.invalid_input")
	}
	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(channel) {
		return invalidArg("error.chat.invalid_input")
	}
	role, err := s.chats.GetChatMemberRole(ctx, channelChatID, actorUserID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !canPostPublicChannelByRole(role) {
		return forbidden("error.chat.forbidden")
	}
	if err := s.chats.UpsertPublicChannelBan(ctx, channelChatID, targetUserID, actorUserID); err != nil {
		return internal(err)
	}
	s.recordChatEvent(ctx, channelChatID, actorUserID, targetUserID, ChatEventBanned, "")
	return nil
}

func (s *Service) UnbanPublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error {
	actorUserID = strings.TrimSpace(actorUserID)
	channelChatID = strings.TrimSpace(channelChatID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorUserID == "" || channelChatID == "" || targetUserID == "" {
		return invalidArg("error.chat.invalid_input")
	}
	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(channel) {
		return invalidArg("error.chat.invalid_input")
	}
	role, err := s.chats.GetChatMemberRole(ctx, channelChatID, actorUserID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !canPostPublicChannelByRole(role) {
		return forbidden("error.chat.forbidden")
	}
	if err := s.chats.DeletePublicChannelBan(ctx, channelChatID, targetUserID); err != nil {
		return internal(err)
	}
	s.recordChatEvent(ctx, channelChatID, actorUserID, targetUserID, ChatEventUnbanned, "")
	return nil
}

func (s *Service) MutePublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error {
	actorUserID = strings.TrimSpace(actorUserID)
	channelChatID = strings.TrimSpace(channelChatID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorUserID == "" || channelChatID == "" || targetUserID == "" {
		return invalidArg("error.chat.invalid_input")
	}
	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(channel) {
		return invalidArg("error.chat.invalid_input")
	}
	role, err := s.chats.GetChatMemberRole(ctx, channelChatID, actorUserID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !canPostPublicChannelByRole(role) {
		return forbidden("error.chat.forbidden")
	}
	if err := s.chats.UpsertPublicChannelMute(ctx, channelChatID, targetUserID, actorUserID); err != nil {
		return internal(err)
	}
	return nil
}

func (s *Service) UnmutePublicChannelUser(ctx context.Context, actorUserID, channelChatID, targetUserID string) error {
	actorUserID = strings.TrimSpace(actorUserID)
	channelChatID = strings.TrimSpace(channelChatID)
	targetUserID = strings.TrimSpace(targetUserID)
	if actorUserID == "" || channelChatID == "" || targetUserID == "" {
		return invalidArg("error.chat.invalid_input")
	}
	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(channel) {
		return invalidArg("error.chat.invalid_input")
	}
	role, err := s.chats.GetChatMemberRole(ctx, channelChatID, actorUserID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	if !canPostPublicChannelByRole(role) {
		return forbidden("error.chat.forbidden")
	}
	if err := s.chats.DeletePublicChannelMute(ctx, channelChatID, targetUserID); err != nil {
		return internal(err)
	}
	return nil
}

func (s *Service) ListPublicChannelBans(ctx context.Context, userID, channelChatID string, limit int) ([]PublicChannelModerationEntry, error) {
	userID = strings.TrimSpace(userID)
	channelChatID = strings.TrimSpace(channelChatID)
	if userID == "" || channelChatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(channel) {
		return nil, invalidArg("error.chat.invalid_input")
	}
	role, err := s.chats.GetChatMemberRole(ctx, channelChatID, userID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if !canPostPublicChannelByRole(role) {
		return nil, forbidden("error.chat.forbidden")
	}
	items, err := s.chats.ListPublicChannelBans(ctx, channelChatID, limit)
	if err != nil {
		return nil, internal(err)
	}
	return items, nil
}

func (s *Service) ListPublicChannelMutes(ctx context.Context, userID, channelChatID string, limit int) ([]PublicChannelModerationEntry, error) {
	userID = strings.TrimSpace(userID)
	channelChatID = strings.TrimSpace(channelChatID)
	if userID == "" || channelChatID == "" {
		return nil, invalidArg("error.chat.invalid_input")
	}
	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if !isStandaloneChannel(channel) {
		return nil, invalidArg("error.chat.invalid_input")
	}
	role, err := s.chats.GetChatMemberRole(ctx, channelChatID, userID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}
	if !canPostPublicChannelByRole(role) {
		return nil, forbidden("error.chat.forbidden")
	}
	items, err := s.chats.ListPublicChannelMutes(ctx, channelChatID, limit)
	if err != nil {
		return nil, internal(err)
	}
	return items, nil
}

func (s *Service) GetOrCreateCommentThread(ctx context.Context, userID, channelChatID, rootMessageID string) (string, error) {
	userID = strings.TrimSpace(userID)
	channelChatID = strings.TrimSpace(channelChatID)
	rootMessageID = strings.TrimSpace(rootMessageID)
	if userID == "" || channelChatID == "" || rootMessageID == "" {
		return "", invalidArg("error.chat.invalid_input")
	}

	channel, err := s.chats.GetChat(ctx, channelChatID)
	if err != nil {
		return "", mapChatOrMessageRepoError(err)
	}
	if !canHaveCommentThread(channel) {
		return "", invalidArg("error.chat.invalid_input")
	}
	if !channel.CommentsEnabled {
		return "", forbidden("error.chat.forbidden")
	}
	// Public channel comments should be readable without requiring a subscription.
	// Posting permissions are enforced at message creation time.
	if banned, banErr := s.chats.IsPublicChannelBanned(ctx, channelChatID, userID); banErr != nil {
		return "", internal(banErr)
	} else if banned {
		return "", forbidden("error.chat.forbidden")
	}

	meta, metaErr := s.messages.GetMessageMeta(ctx, rootMessageID)
	if metaErr != nil {
		return "", mapChatOrMessageRepoError(metaErr)
	}
	if strings.TrimSpace(meta.ChatID) != channelChatID {
		return "", invalidArg("error.chat.invalid_input")
	}
	// Root must be a top-level post.
	if strings.TrimSpace(meta.ReplyToMessageID) != "" {
		return "", invalidArg("error.chat.invalid_input")
	}

	threadID, err := s.chats.GetOrCreateCommentThread(ctx, channelChatID, rootMessageID, userID)
	if err != nil {
		return "", internal(err)
	}
	return strings.TrimSpace(threadID), nil
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
		created, err := s.chats.CreateChat(ctx, recipientID, []string{recipientID, userID}, userID, ChatTypeStandard, "")
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

	created, err := s.chats.CreateChat(ctx, recipientID, []string{recipientID, userID}, userID, ChatTypeStandard, "")
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

	group, err := s.chats.GetChat(ctx, groupChatID)
	if err != nil {
		return nil, mapChatOrMessageRepoError(err)
	}

	// Virtual General topic: messages live on the group chat itself, so it
	// mirrors the group row - the group's own title as its parent title and
	// the group's last message sender/timestamp.
	one := 1
	trueVal := true
	parentTitle := group.Title
	general := Chat{
		ID:                    groupChatID,
		Title:                 "General",
		Kind:                  "group",
		TopicNumber:           &one,
		IsGeneral:             &trueVal,
		ParentTitle:           &parentTitle,
		LastMessageSenderName: group.LastMessageSenderName,
		LastMessageAt:         group.LastMessageAt,
	}
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
	if isStandaloneChannel(target) {
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
	if !isStandaloneChannel(target) || !target.IsPublic {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	switch {
	case err == nil:
		if strings.EqualFold(role, "banned") {
			return Chat{}, forbidden("error.chat.forbidden")
		}
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "owner", "admin", "subscriber":
			// Already subscribed: idempotent, no write, so a repeated
			// subscribe never creates a duplicate row (pairs are unique by
			// PRIMARY KEY (chat_id, user_id) anyway) and the client flips
			// to Unsubscribe from the returned viewer_role.
			return s.GetChat(ctx, userID, chatID)
		default:
			// Legacy rows (e.g. boxchat ETL 'member') are channel members
			// but not a valid standalone_channel role: normalize to
			// subscriber so the client treats them as subscribed.
			if err := s.chats.UpdateChatMemberRole(ctx, chatID, userID, "subscriber"); err != nil {
				return Chat{}, internal(err)
			}
			return s.GetChat(ctx, userID, chatID)
		}
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

	// Resolve the avatar (and viewer state) through the service layer: the raw
	// repository row carries an unresolved storage key that the client would
	// render as a blank avatar.
	updated, err := s.GetChat(ctx, userID, chatID)
	if err != nil {
		return Chat{}, err
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
	if !isStandaloneChannel(target) || !target.IsPublic {
		return invalidArg("error.chat.invalid_input")
	}

	role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
	if err != nil {
		if errors.Is(err, ErrChatNotFound) {
			// Idempotent: not subscribed already reads as unsubscribed.
			return nil
		}
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
	// Manual additions through POST /chats/{id}/members must take effect
	// immediately. Personal invite DMs used to be sent here instead, which
	// left the chat untouched and flooded the actor with direct chats.
	if err := s.chats.AddChatMembers(ctx, chatID, nextMembers); err != nil {
		return nil, internal(err)
	}
	for _, memberID := range nextMembers {
		s.recordChatEvent(ctx, chatID, userID, memberID, ChatEventMemberJoined, "")
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
	switch {
	case isStandaloneChannel(target):
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
	s.recordChatEvent(ctx, chatID, actorUserID, targetUserID, ChatEventRoleChanged, "role="+role)
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
	s.recordChatEvent(ctx, chatID, actorUserID, targetUserID, ChatEventMemberRemoved, "")
	return s.ListMembers(ctx, actorUserID, chatID, false)
}
