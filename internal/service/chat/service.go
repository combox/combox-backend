package chat

import (
	"context"
	"errors"
	"strings"
)

const (
	maxMessageContentLen = 4000
	maxE2EEnvelopeLen    = 1 << 16
)

func sanitizeMessageReactionsForViewer(chatMeta Chat, item Message) Message {
	if !isStandaloneChannel(chatMeta) {
		return item
	}
	if item.ReplyToMessageID != nil && strings.TrimSpace(*item.ReplyToMessageID) != "" {
		return item
	}
	if len(item.Reactions) == 0 {
		return item
	}
	reactions := make([]MessageReaction, 0, len(item.Reactions))
	for _, reaction := range item.Reactions {
		count := reaction.Count
		if count <= 0 {
			count = len(reaction.UserIDs)
		}
		reactions = append(reactions, MessageReaction{
			Emoji:   reaction.Emoji,
			Count:   count,
			UserIDs: nil,
		})
	}
	item.Reactions = reactions
	return item
}

func canReactPublicChannelByRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "owner", "admin", "subscriber":
		return true
	default:
		return false
	}
}

// canPostIntoChannel reports whether the viewer may write into a channel whose
// "who can send messages" setting is restricted to admins.
func (s *Service) canPostIntoChannel(ctx context.Context, target Chat, userID string) (bool, error) {
	if NormalizeSendPermission(derefString(target.SendPermission)) != SendPermissionAdmins {
		return true, nil
	}
	role, err := s.chats.GetChatMemberRole(ctx, target.ID, userID)
	if err != nil {
		return false, internal(err)
	}
	return canCreateChannelByRole(role), nil
}

// ensureCanPostIntoChat enforces the destination-side posting rules for an
// incoming message. Forwarding used to skip these, so a subscriber could push
// messages into an admin-only channel.
func (s *Service) ensureCanPostIntoChat(ctx context.Context, target Chat, userID string) error {
	chatID := strings.TrimSpace(target.ID)
	chatKind := strings.TrimSpace(strings.ToLower(target.Kind))
	switch {
	case isStandaloneChannel(target):
		if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
			return err
		}
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, chatID, userID); banErr != nil {
			return internal(banErr)
		} else if banned {
			return forbidden("error.chat.forbidden")
		}
		if muted, muteErr := s.chats.IsPublicChannelMuted(ctx, chatID, userID); muteErr != nil {
			return internal(muteErr)
		} else if muted {
			return forbidden("error.chat.forbidden")
		}
		role, roleErr := s.chats.GetChatMemberRole(ctx, chatID, userID)
		if roleErr != nil {
			return internal(roleErr)
		}
		if !canPostPublicChannelByRole(role) {
			return forbidden("error.chat.forbidden")
		}
	case chatKind == "comment_thread":
		parentID := ""
		if target.ParentChatID != nil {
			parentID = strings.TrimSpace(*target.ParentChatID)
		}
		if parentID == "" {
			return forbidden("error.chat.forbidden")
		}
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, parentID, userID); banErr != nil {
			return internal(banErr)
		} else if banned {
			return forbidden("error.chat.forbidden")
		}
		if muted, muteErr := s.chats.IsPublicChannelMuted(ctx, parentID, userID); muteErr != nil {
			return internal(muteErr)
		} else if muted {
			return forbidden("error.chat.forbidden")
		}
		role, roleErr := s.chats.GetChatMemberRole(ctx, parentID, userID)
		if roleErr != nil {
			return internal(roleErr)
		}
		if !canPostPublicChannelByRole(role) {
			return forbidden("error.chat.forbidden")
		}
	default:
		if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
			return err
		}
		if chatKind == "channel" {
			if allowed, permErr := s.canPostIntoChannel(ctx, target, userID); permErr != nil {
				return permErr
			} else if !allowed {
				return forbidden("error.chat.forbidden")
			}
		}
	}
	return nil
}

func (s *Service) CreateMessage(ctx context.Context, input CreateMessageInput) (Message, error) {
	userID := strings.TrimSpace(input.UserID)
	botID := strings.TrimSpace(input.BotID)
	chatID := strings.TrimSpace(input.ChatID)
	content := strings.TrimSpace(input.Content)
	replyToMessageID := strings.TrimSpace(input.ReplyToMessageID)
	senderDeviceID := strings.TrimSpace(input.SenderDeviceID)

	attachmentIDs := make([]string, 0, len(input.AttachmentIDs))
	for _, id := range input.AttachmentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		attachmentIDs = append(attachmentIDs, id)
	}

	if chatID == "" || (userID == "" && botID == "") || (userID != "" && botID != "") {
		return Message{}, invalidArg("error.message.invalid_input")
	}

	isE2E := len(input.Envelopes) > 0 || senderDeviceID != ""
	if !isE2E && content == "" && len(attachmentIDs) == 0 {
		return Message{}, invalidArg("error.message.invalid_input")
	}
	if isE2E {
		if replyToMessageID != "" || senderDeviceID == "" || len(input.Envelopes) == 0 {
			return Message{}, invalidArg("error.message.invalid_e2e_payload")
		}
		for _, env := range input.Envelopes {
			if strings.TrimSpace(env.RecipientDeviceID) == "" || strings.TrimSpace(env.Alg) == "" || strings.TrimSpace(env.Header) == "" || strings.TrimSpace(env.Ciphertext) == "" {
				return Message{}, invalidArg("error.message.invalid_e2e_payload")
			}
			if len(env.Header) > maxE2EEnvelopeLen || len(env.Ciphertext) > maxE2EEnvelopeLen {
				return Message{}, invalidArg("error.message.invalid_e2e_payload")
			}
		}
	}

	if !isE2E && len([]rune(content)) > maxMessageContentLen {
		return Message{}, invalidArg("error.message.invalid_input")
	}

	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return Message{}, mapChatOrMessageRepoError(err)
	}
	chatKind := strings.TrimSpace(strings.ToLower(chatMeta.Kind))
	if userID != "" {
		switch {
		case isStandaloneChannel(chatMeta):
			// Membership required for posting to standalone channels (comments handled via comment_thread).
			if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
				return Message{}, err
			}
		case chatKind == "comment_thread":
			parentID := ""
			if chatMeta.ParentChatID != nil {
				parentID = strings.TrimSpace(*chatMeta.ParentChatID)
			}
			if parentID == "" {
				return Message{}, forbidden("error.chat.forbidden")
			}
			if banned, banErr := s.chats.IsPublicChannelBanned(ctx, parentID, userID); banErr != nil {
				return Message{}, internal(banErr)
			} else if banned {
				return Message{}, forbidden("error.chat.forbidden")
			}
			if muted, muteErr := s.chats.IsPublicChannelMuted(ctx, parentID, userID); muteErr != nil {
				return Message{}, internal(muteErr)
			} else if muted {
				return Message{}, forbidden("error.chat.forbidden")
			}
			role, roleErr := s.chats.GetChatMemberRole(ctx, parentID, userID)
			if roleErr != nil {
				return Message{}, internal(roleErr)
			}
			if !canPostPublicChannelByRole(role) {
				return Message{}, forbidden("error.chat.forbidden")
			}
		default:
			if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
				return Message{}, err
			}
			if chatKind == "channel" {
				if allowed, permErr := s.canPostIntoChannel(ctx, chatMeta, userID); permErr != nil {
					return Message{}, permErr
				} else if !allowed {
					return Message{}, forbidden("error.chat.forbidden")
				}
			}
		}
	}
	chatType, ok := normalizeChatType(chatMeta.Type)
	if !ok {
		return Message{}, invalidArg("error.chat.invalid_type")
	}
	if chatType == ChatTypeStandard && isE2E {
		return Message{}, invalidArg("error.message.e2e_not_allowed_in_standard")
	}
	if chatType == ChatTypeSecretE2E {
		if !chatMeta.IsDirect {
			return Message{}, invalidArg("error.chat.secret_must_be_direct")
		}
		if !isE2E {
			return Message{}, invalidArg("error.message.plaintext_not_allowed_in_secret")
		}
	}
	if userID != "" && isStandaloneChannel(chatMeta) {
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, chatID, userID); banErr != nil {
			return Message{}, internal(banErr)
		} else if banned {
			return Message{}, forbidden("error.chat.forbidden")
		}
		if muted, muteErr := s.chats.IsPublicChannelMuted(ctx, chatID, userID); muteErr != nil {
			return Message{}, internal(muteErr)
		} else if muted {
			return Message{}, forbidden("error.chat.forbidden")
		}
		role, err := s.chats.GetChatMemberRole(ctx, chatID, userID)
		if err != nil {
			return Message{}, internal(err)
		}
		if !canPostPublicChannelByRole(role) {
			if !chatMeta.CommentsEnabled || replyToMessageID == "" || !strings.EqualFold(role, "subscriber") {
				return Message{}, forbidden("error.chat.forbidden")
			}
			replyMeta, replyErr := s.messages.GetMessageMeta(ctx, replyToMessageID)
			if replyErr != nil {
				if errors.Is(replyErr, ErrMessageNotFound) {
					return Message{}, invalidArg("error.message.invalid_input")
				}
				return Message{}, internal(replyErr)
			}
			if strings.TrimSpace(replyMeta.ChatID) != chatID || strings.TrimSpace(replyMeta.UserID) == "" {
				return Message{}, invalidArg("error.message.invalid_input")
			}
			replyAuthorRole, roleErr := s.chats.GetChatMemberRole(ctx, chatID, strings.TrimSpace(replyMeta.UserID))
			if roleErr != nil {
				return Message{}, internal(roleErr)
			}
			if !canPostPublicChannelByRole(replyAuthorRole) {
				return Message{}, forbidden("error.chat.forbidden")
			}
		}
	}

	if replyToMessageID != "" {
		replyMeta, replyErr := s.messages.GetMessageMeta(ctx, replyToMessageID)
		if replyErr != nil {
			if errors.Is(replyErr, ErrMessageNotFound) {
				return Message{}, invalidArg("error.message.invalid_input")
			}
			return Message{}, internal(replyErr)
		}
		if strings.TrimSpace(replyMeta.ChatID) != chatID {
			return Message{}, invalidArg("error.message.invalid_input")
		}
	}

	var message Message
	var repoErr error
	if isE2E {
		if userID == "" {
			return Message{}, invalidArg("error.message.invalid_e2e_payload")
		}
		if len(attachmentIDs) > 0 {
			message, repoErr = s.messages.CreateMessageE2EWithAttachments(ctx, chatID, userID, senderDeviceID, input.Envelopes, replyToMessageID, attachmentIDs)
		} else {
			message, repoErr = s.messages.CreateMessageE2E(ctx, chatID, userID, senderDeviceID, input.Envelopes, replyToMessageID)
		}
	} else {
		switch {
		case botID != "":
			if len(attachmentIDs) > 0 {
				return Message{}, invalidArg("error.message.invalid_input")
			}
			message, repoErr = s.messages.CreateMessageAsBot(ctx, chatID, botID, content, replyToMessageID)
		case len(attachmentIDs) > 0:
			message, repoErr = s.messages.CreateMessageWithAttachments(ctx, chatID, userID, content, replyToMessageID, attachmentIDs)
		default:
			message, repoErr = s.messages.CreateMessage(ctx, chatID, userID, content, replyToMessageID)
		}
	}
	if repoErr != nil {
		return Message{}, mapChatOrMessageRepoError(repoErr)
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
			senderID := userID
			if strings.TrimSpace(senderID) == "" && message.SenderBotID != nil {
				senderID = "bot:" + strings.TrimSpace(*message.SenderBotID)
			}
			for _, memberID := range members {
				ev := UserMessageCreatedEvent{
					MessageID:       message.ID,
					ChatID:          message.ChatID,
					SenderUserID:    senderID,
					RecipientUserID: memberID,
					CreatedAt:       message.CreatedAt,
					Preview:         messagePreviewHead(message.Content, 200),
				}
				_ = s.publisher.PublishUserMessageCreated(ctx, ev)
				if s.notifications != nil && userID != "" && memberID != userID {
					_, _ = s.notifications.IncrementChatUnread(ctx, memberID, chatID, 1)
				}
			}
		}
	}

	if isE2E && s.notifications != nil && userID != "" {
		if members, err := s.chats.ListChatMemberIDs(ctx, chatID); err == nil {
			for _, memberID := range members {
				if memberID == userID {
					continue
				}
				_, _ = s.notifications.IncrementChatUnread(ctx, memberID, chatID, 1)
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
		return MessagePage{}, invalidArg("error.chat.invalid_input")
	}
	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return MessagePage{}, mapChatOrMessageRepoError(err)
	}
	kind := strings.TrimSpace(strings.ToLower(chatMeta.Kind))
	if isStandaloneChannel(chatMeta) && chatMeta.IsPublic {
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, chatID, userID); banErr != nil {
			return MessagePage{}, internal(banErr)
		} else if banned {
			return MessagePage{}, forbidden("error.chat.forbidden")
		}
		role, roleErr := s.chats.GetChatMemberRole(ctx, chatID, userID)
		switch {
		case roleErr == nil:
			if strings.EqualFold(role, "banned") {
				return MessagePage{}, forbidden("error.chat.forbidden")
			}
			roleCopy := role
			chatMeta.ViewerRole = &roleCopy
		case errors.Is(roleErr, ErrChatNotFound):
			chatMeta.ViewerRole = nil
		default:
			return MessagePage{}, internal(roleErr)
		}
	} else if kind == "comment_thread" {
		parentID := ""
		if chatMeta.ParentChatID != nil {
			parentID = strings.TrimSpace(*chatMeta.ParentChatID)
		}
		if parentID == "" {
			return MessagePage{}, forbidden("error.chat.forbidden")
		}
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, parentID, userID); banErr != nil {
			return MessagePage{}, internal(banErr)
		} else if banned {
			return MessagePage{}, forbidden("error.chat.forbidden")
		}
		// No membership requirement for reading.
	} else {
		if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
			return MessagePage{}, err
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
		return MessagePage{}, internal(repoErr)
	}
	// Per-user history clear: a member's own watermark hides everything at or
	// before it from that member only; other members are untouched.
	state, stateErr := s.chats.GetChatUserState(ctx, userID, chatID)
	if stateErr != nil {
		return MessagePage{}, internal(stateErr)
	}
	if state.HistoryClearedAt != nil {
		watermark := *state.HistoryClearedAt
		visible := page.Items[:0]
		for _, item := range page.Items {
			if item.CreatedAt.After(watermark) {
				visible = append(visible, item)
			}
		}
		page.Items = visible
	}
	for idx := range page.Items {
		page.Items[idx] = sanitizeMessageReactionsForViewer(chatMeta, page.Items[idx])
	}
	s.applyForwardPrivacyToMessages(ctx, userID, page.Items)
	if err := s.attachPollsToMessages(ctx, userID, page.Items); err != nil {
		return MessagePage{}, err
	}
	if s.statusRepo != nil && len(page.Items) > 0 {
		messageIDs := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			if id := strings.TrimSpace(item.ID); id != "" {
				messageIDs = append(messageIDs, id)
			}
		}
		if len(messageIDs) > 0 {
			if statuses, err := s.statusRepo.ListLatestMessageStatuses(ctx, messageIDs); err == nil && len(statuses) > 0 {
				page.Statuses = statuses
			}
		}
	}
	return page, nil
}

func (s *Service) UpsertMessageStatus(ctx context.Context, input UpsertMessageStatusInput) (MessageStatus, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	messageID := strings.TrimSpace(input.MessageID)
	status, ok := normalizeStatus(input.Status)
	if userID == "" || chatID == "" || messageID == "" {
		return MessageStatus{}, invalidArg("error.message.invalid_input")
	}
	if !ok {
		return MessageStatus{}, invalidArg("error.message.invalid_status")
	}

	if err := s.ensureChatMember(ctx, chatID, userID); err != nil {
		return MessageStatus{}, err
	}

	var updated MessageStatus
	var repoErr error
	if s.statusRepo != nil {
		updated, repoErr = s.statusRepo.UpsertMessageStatus(ctx, chatID, messageID, userID, status, s.nowUTC())
	} else {
		updated, repoErr = s.messages.UpsertMessageStatus(ctx, chatID, messageID, userID, status)
	}
	if repoErr != nil {
		return MessageStatus{}, mapChatOrMessageRepoError(repoErr)
	}

	if s.publisher != nil {
		if members, listErr := s.chats.ListChatMemberIDs(ctx, chatID); listErr == nil {
			for _, memberID := range members {
				_ = s.publisher.PublishMessageStatus(ctx, MessageStatusEvent{
					MessageID:       updated.MessageID,
					ChatID:          updated.ChatID,
					UserID:          updated.UserID,
					RecipientUserID: memberID,
					Status:          updated.Status,
					At:              updated.UpdatedAt,
				})
			}
		}
	}
	if status == "read" && s.notifications != nil {
		_ = s.notifications.ResetChatUnread(ctx, userID, chatID)
	}
	return updated, nil
}
