package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memChatRepo struct {
	chats       []Chat
	members     map[string]map[string]bool
	roles       map[string]map[string]string
	inviteLinks []ChatInviteLink
	publicBans  map[string]map[string]PublicChannelModerationEntry
	publicMutes map[string]map[string]PublicChannelModerationEntry
	failList    bool
}

func (m *memChatRepo) CreateChat(_ context.Context, title string, memberIDs []string, creatorID string, chatType string) (Chat, error) {
	if strings.TrimSpace(chatType) == "" {
		chatType = ChatTypeStandard
	}
	kind := "group"
	isDirect := len(memberIDs) == 2
	if isDirect {
		kind = "direct"
	}
	created := Chat{
		ID:              "chat-1",
		Title:           title,
		IsDirect:        isDirect,
		Type:            chatType,
		Kind:            kind,
		CommentsEnabled: true,
		BotID:           nil,
		CreatedAt:       time.Now().UTC(),
	}
	m.chats = append(m.chats, created)
	if m.members == nil {
		m.members = map[string]map[string]bool{}
	}
	if m.roles == nil {
		m.roles = map[string]map[string]string{}
	}
	m.members[created.ID] = map[string]bool{}
	m.roles[created.ID] = map[string]string{}
	for _, memberID := range memberIDs {
		m.members[created.ID][memberID] = true
		if !isDirect && memberID == creatorID {
			m.roles[created.ID][memberID] = "owner"
		} else {
			m.roles[created.ID][memberID] = "member"
		}
	}
	return created, nil
}

func (m *memChatRepo) DeleteChannel(_ context.Context, parentChatID, channelChatID string) error {
	parentChatID = strings.TrimSpace(parentChatID)
	channelChatID = strings.TrimSpace(channelChatID)
	if parentChatID == "" || channelChatID == "" {
		return ErrChatNotFound
	}
	idx := -1
	for i := range m.chats {
		if m.chats[i].ID == channelChatID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrChatNotFound
	}
	chatRef := m.chats[idx]
	if chatRef.Kind != "channel" || chatRef.ParentChatID == nil || *chatRef.ParentChatID != parentChatID {
		return ErrChatNotFound
	}
	if chatRef.TopicNumber != nil && *chatRef.TopicNumber < 2 {
		return ErrChatNotFound
	}
	copy(m.chats[idx:], m.chats[idx+1:])
	m.chats = m.chats[:len(m.chats)-1]
	if m.members != nil {
		delete(m.members, channelChatID)
	}
	if m.roles != nil {
		delete(m.roles, channelChatID)
	}
	return nil
}

func (m *memChatRepo) DeleteChat(_ context.Context, chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ErrChatNotFound
	}
	idx := -1
	for i := range m.chats {
		if m.chats[i].ID == chatID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrChatNotFound
	}
	copy(m.chats[idx:], m.chats[idx+1:])
	m.chats = m.chats[:len(m.chats)-1]
	if m.members != nil {
		delete(m.members, chatID)
	}
	if m.roles != nil {
		delete(m.roles, chatID)
	}
	return nil
}

func (m *memChatRepo) CreateChannel(_ context.Context, parentChatID, title, channelType, _ string) (Chat, error) {
	if _, ok := m.members[parentChatID]; !ok {
		return Chat{}, ErrChatNotFound
	}
	if m.roles == nil {
		m.roles = map[string]map[string]string{}
	}
	created := Chat{
		ID:              "channel-1",
		Title:           title,
		IsDirect:        false,
		Type:            ChatTypeStandard,
		Kind:            "channel",
		ParentChatID:    &parentChatID,
		ChannelType:     &channelType,
		CommentsEnabled: true,
		CreatedAt:       time.Now().UTC(),
	}
	m.chats = append(m.chats, created)
	m.members[created.ID] = map[string]bool{}
	m.roles[created.ID] = map[string]string{}
	for userID, isMember := range m.members[parentChatID] {
		if !isMember {
			continue
		}
		m.members[created.ID][userID] = true
		m.roles[created.ID][userID] = m.roles[parentChatID][userID]
	}
	return created, nil
}

func (m *memChatRepo) CreatePublicChannel(_ context.Context, title, publicSlug, creatorID string, isPublic bool) (Chat, error) {
	if m.members == nil {
		m.members = map[string]map[string]bool{}
	}
	if m.roles == nil {
		m.roles = map[string]map[string]string{}
	}
	created := Chat{
		ID:              "public-channel-1",
		Title:           title,
		IsDirect:        false,
		Type:            ChatTypeStandard,
		Kind:            "standalone_channel",
		IsPublic:        isPublic,
		CommentsEnabled: true,
		CreatedAt:       time.Now().UTC(),
	}
	if strings.TrimSpace(publicSlug) != "" {
		created.PublicSlug = &publicSlug
	}
	m.chats = append(m.chats, created)
	m.members[created.ID] = map[string]bool{creatorID: true}
	m.roles[created.ID] = map[string]string{creatorID: "owner"}
	return created, nil
}

func (m *memChatRepo) FindDirectChatByMembers(_ context.Context, userAID, userBID, chatType string) (Chat, bool, error) {
	for _, c := range m.chats {
		if !c.IsDirect || c.Type != chatType {
			continue
		}
		members := m.members[c.ID]
		if members[userAID] && members[userBID] && len(members) == 2 {
			return c, true, nil
		}
	}
	return Chat{}, false, nil
}

func (m *memChatRepo) ListChatsByUser(_ context.Context, userID string) ([]Chat, error) {
	if m.failList {
		return nil, errors.New("list failed")
	}
	out := make([]Chat, 0, len(m.chats))
	for _, c := range m.chats {
		if m.members[c.ID][userID] {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memChatRepo) ListChannelsByParent(_ context.Context, parentChatID, userID string) ([]Chat, error) {
	out := make([]Chat, 0)
	for _, c := range m.chats {
		if c.Kind != "channel" || c.ParentChatID == nil || *c.ParentChatID != parentChatID {
			continue
		}
		if m.members[c.ID][userID] {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memChatRepo) GetChat(_ context.Context, chatID string) (Chat, error) {
	for _, c := range m.chats {
		if c.ID == chatID {
			return c, nil
		}
	}
	return Chat{}, ErrChatNotFound
}

func (m *memChatRepo) UpdateChat(_ context.Context, input UpdateChatInput) (Chat, error) {
	for i := range m.chats {
		if m.chats[i].ID != input.ChatID {
			continue
		}
		if input.Title.Set && input.Title.Value != nil {
			m.chats[i].Title = strings.TrimSpace(*input.Title.Value)
		}
		if input.AvatarDataURL.Set {
			m.chats[i].AvatarURL = input.AvatarDataURL.Value
		}
		if input.AvatarGradient.Set {
			m.chats[i].AvatarBg = input.AvatarGradient.Value
		}
		if input.CommentsEnabled.Set {
			m.chats[i].CommentsEnabled = input.CommentsEnabled.Value
		}
		if input.IsPublic.Set {
			m.chats[i].IsPublic = input.IsPublic.Value
		}
		if input.PublicSlug.Set {
			m.chats[i].PublicSlug = input.PublicSlug.Value
		}
		return m.chats[i], nil
	}
	return Chat{}, ErrChatNotFound
}

func (m *memChatRepo) ListChatInviteLinks(_ context.Context, chatID string) ([]ChatInviteLink, error) {
	out := make([]ChatInviteLink, 0)
	for _, item := range m.inviteLinks {
		if item.ChatID == chatID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *memChatRepo) CreateChatInviteLink(_ context.Context, chatID, createdBy, title string, isPrimary bool) (ChatInviteLink, error) {
	var titlePtr *string
	if strings.TrimSpace(title) != "" {
		trimmed := strings.TrimSpace(title)
		titlePtr = &trimmed
	}
	item := ChatInviteLink{
		ID:        "link-" + strings.TrimSpace(chatID) + "-" + time.Now().UTC().Format("150405.000"),
		ChatID:    chatID,
		CreatedBy: createdBy,
		Token:     "tok-" + strings.TrimSpace(chatID),
		Title:     titlePtr,
		IsPrimary: isPrimary,
		UseCount:  0,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if isPrimary {
		for i := range m.inviteLinks {
			if m.inviteLinks[i].ChatID == chatID {
				m.inviteLinks[i].IsPrimary = false
			}
		}
	}
	m.inviteLinks = append(m.inviteLinks, item)
	return item, nil
}

func (m *memChatRepo) GetOrCreateCommentThread(_ context.Context, channelChatID, rootMessageID, creatorUserID string) (string, error) {
	channelChatID = strings.TrimSpace(channelChatID)
	rootMessageID = strings.TrimSpace(rootMessageID)
	creatorUserID = strings.TrimSpace(creatorUserID)
	if channelChatID == "" || rootMessageID == "" || creatorUserID == "" {
		return "", ErrChatNotFound
	}
	return "thread-1", nil
}

func (m *memChatRepo) IsPublicChannelBanned(_ context.Context, channelChatID, userID string) (bool, error) {
	channelChatID = strings.TrimSpace(channelChatID)
	userID = strings.TrimSpace(userID)
	if m.publicBans == nil {
		return false, nil
	}
	if m.publicBans[channelChatID] == nil {
		return false, nil
	}
	_, ok := m.publicBans[channelChatID][userID]
	return ok, nil
}

func (m *memChatRepo) IsPublicChannelMuted(_ context.Context, channelChatID, userID string) (bool, error) {
	channelChatID = strings.TrimSpace(channelChatID)
	userID = strings.TrimSpace(userID)
	if m.publicMutes == nil {
		return false, nil
	}
	if m.publicMutes[channelChatID] == nil {
		return false, nil
	}
	_, ok := m.publicMutes[channelChatID][userID]
	return ok, nil
}

func (m *memChatRepo) UpsertPublicChannelBan(_ context.Context, channelChatID, userID, actorUserID string) error {
	channelChatID = strings.TrimSpace(channelChatID)
	userID = strings.TrimSpace(userID)
	actorUserID = strings.TrimSpace(actorUserID)
	if channelChatID == "" || userID == "" || actorUserID == "" {
		return ErrChatNotFound
	}
	if m.publicBans == nil {
		m.publicBans = map[string]map[string]PublicChannelModerationEntry{}
	}
	if m.publicBans[channelChatID] == nil {
		m.publicBans[channelChatID] = map[string]PublicChannelModerationEntry{}
	}
	m.publicBans[channelChatID][userID] = PublicChannelModerationEntry{UserID: userID, CreatedBy: actorUserID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return nil
}

func (m *memChatRepo) DeletePublicChannelBan(_ context.Context, channelChatID, userID string) error {
	channelChatID = strings.TrimSpace(channelChatID)
	userID = strings.TrimSpace(userID)
	if m.publicBans == nil || m.publicBans[channelChatID] == nil {
		return nil
	}
	delete(m.publicBans[channelChatID], userID)
	return nil
}

func (m *memChatRepo) UpsertPublicChannelMute(_ context.Context, channelChatID, userID, actorUserID string) error {
	channelChatID = strings.TrimSpace(channelChatID)
	userID = strings.TrimSpace(userID)
	actorUserID = strings.TrimSpace(actorUserID)
	if channelChatID == "" || userID == "" || actorUserID == "" {
		return ErrChatNotFound
	}
	if m.publicMutes == nil {
		m.publicMutes = map[string]map[string]PublicChannelModerationEntry{}
	}
	if m.publicMutes[channelChatID] == nil {
		m.publicMutes[channelChatID] = map[string]PublicChannelModerationEntry{}
	}
	m.publicMutes[channelChatID][userID] = PublicChannelModerationEntry{UserID: userID, CreatedBy: actorUserID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return nil
}

func (m *memChatRepo) DeletePublicChannelMute(_ context.Context, channelChatID, userID string) error {
	channelChatID = strings.TrimSpace(channelChatID)
	userID = strings.TrimSpace(userID)
	if m.publicMutes == nil || m.publicMutes[channelChatID] == nil {
		return nil
	}
	delete(m.publicMutes[channelChatID], userID)
	return nil
}

func (m *memChatRepo) ListPublicChannelBans(_ context.Context, channelChatID string, limit int) ([]PublicChannelModerationEntry, error) {
	channelChatID = strings.TrimSpace(channelChatID)
	if limit <= 0 {
		limit = 100
	}
	out := make([]PublicChannelModerationEntry, 0)
	if m.publicBans == nil || m.publicBans[channelChatID] == nil {
		return out, nil
	}
	for _, item := range m.publicBans[channelChatID] {
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memChatRepo) ListPublicChannelMutes(_ context.Context, channelChatID string, limit int) ([]PublicChannelModerationEntry, error) {
	channelChatID = strings.TrimSpace(channelChatID)
	if limit <= 0 {
		limit = 100
	}
	out := make([]PublicChannelModerationEntry, 0)
	if m.publicMutes == nil || m.publicMutes[channelChatID] == nil {
		return out, nil
	}
	for _, item := range m.publicMutes[channelChatID] {
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memChatRepo) GetChatInviteLinkByToken(_ context.Context, token string) (ChatInviteLink, error) {
	for _, item := range m.inviteLinks {
		if item.Token == token {
			return item, nil
		}
	}
	return ChatInviteLink{}, ErrChatNotFound
}

func (m *memChatRepo) IncrementChatInviteLinkUse(_ context.Context, linkID string) error {
	for i := range m.inviteLinks {
		if m.inviteLinks[i].ID == linkID {
			m.inviteLinks[i].UseCount++
			return nil
		}
	}
	return ErrChatNotFound
}

func (m *memChatRepo) ListChatMemberIDs(_ context.Context, chatID string) ([]string, error) {
	set, ok := m.members[chatID]
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out, nil
}

func (m *memChatRepo) ListChatMembers(_ context.Context, chatID string, includeBanned bool) ([]ChatMember, error) {
	set, ok := m.members[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	out := make([]ChatMember, 0, len(set))
	for userID, isMember := range set {
		if !isMember {
			continue
		}
		role := m.roles[chatID][userID]
		if !includeBanned && strings.EqualFold(role, "banned") {
			continue
		}
		out = append(out, ChatMember{
			UserID: userID,
			Role:   role,
		})
	}
	return out, nil
}

func (m *memChatRepo) AddChatMembers(_ context.Context, chatID string, memberIDs []string) error {
	if _, ok := m.members[chatID]; !ok {
		return ErrChatNotFound
	}
	for _, memberID := range memberIDs {
		if strings.TrimSpace(memberID) == "" {
			continue
		}
		m.members[chatID][memberID] = true
		if m.roles[chatID] == nil {
			m.roles[chatID] = map[string]string{}
		}
		if strings.TrimSpace(m.roles[chatID][memberID]) == "" {
			m.roles[chatID][memberID] = "member"
		}
	}
	return nil
}

func (m *memChatRepo) UpdateChatMemberRole(_ context.Context, chatID, userID, role string) error {
	if _, ok := m.members[chatID]; !ok {
		return ErrChatNotFound
	}
	if !m.members[chatID][userID] {
		return ErrChatNotFound
	}
	if m.roles[chatID] == nil {
		m.roles[chatID] = map[string]string{}
	}
	m.roles[chatID][userID] = role
	return nil
}

func (m *memChatRepo) RemoveChatMember(_ context.Context, chatID, userID string) error {
	if _, ok := m.members[chatID]; !ok {
		return ErrChatNotFound
	}
	delete(m.members[chatID], userID)
	if m.roles[chatID] != nil {
		delete(m.roles[chatID], userID)
	}
	return nil
}

func (m *memChatRepo) IsChatMember(_ context.Context, chatID, userID string) (bool, error) {
	if _, ok := m.members[chatID]; !ok {
		return false, nil
	}
	return m.members[chatID][userID], nil
}

func (m *memChatRepo) GetChatMemberRole(_ context.Context, chatID, userID string) (string, error) {
	if m.roles == nil {
		return "", nil
	}
	return m.roles[chatID][userID], nil
}

type memMsgRepo struct {
	items []Message
}

func (m *memMsgRepo) CreateMessage(_ context.Context, chatID, userID, content, _ string) (Message, error) {
	msg := Message{
		ID:        "msg-1",
		ChatID:    chatID,
		UserID:    userID,
		Content:   content,
		IsE2E:     false,
		CreatedAt: time.Now().UTC(),
	}
	m.items = append(m.items, msg)
	return msg, nil
}

func (m *memMsgRepo) CreateMessageAsBot(_ context.Context, chatID, botID, content, _ string) (Message, error) {
	msg := Message{
		ID:          "msg-bot-1",
		ChatID:      chatID,
		UserID:      "bot:" + botID,
		SenderBotID: &botID,
		Content:     content,
		IsE2E:       false,
		CreatedAt:   time.Now().UTC(),
	}
	m.items = append(m.items, msg)
	return msg, nil
}

func (m *memMsgRepo) CreateMessageWithAttachments(ctx context.Context, chatID, userID, content, replyToMessageID string, _ []string) (Message, error) {
	return m.CreateMessage(ctx, chatID, userID, content, replyToMessageID)
}

func (m *memMsgRepo) CreateMessageE2E(_ context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, _ string) (Message, error) {
	msg := Message{
		ID:        "msg-1",
		ChatID:    chatID,
		UserID:    userID,
		IsE2E:     true,
		E2E:       &E2EPayload{SenderDeviceID: senderDeviceID},
		CreatedAt: time.Now().UTC(),
	}
	if len(envelopes) > 0 {
		msg.E2E.Envelope = &envelopes[0]
	}
	m.items = append(m.items, msg)
	return msg, nil
}

func (m *memMsgRepo) CreateMessageE2EWithAttachments(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, replyToMessageID string, _ []string) (Message, error) {
	return m.CreateMessageE2E(ctx, chatID, userID, senderDeviceID, envelopes, replyToMessageID)
}

func (m *memMsgRepo) CreateForwardedMessage(ctx context.Context, chatID, sourceMessageID, userID string) (Message, error) {
	// For tests we only need to satisfy the interface.
	return m.CreateMessage(ctx, chatID, userID, "forward", "")
}

func (m *memMsgRepo) ListMessages(_ context.Context, chatID string, _ int, _ string) (MessagePage, error) {
	out := make([]Message, 0, len(m.items))
	for _, item := range m.items {
		if item.ChatID == chatID {
			out = append(out, item)
		}
	}
	return MessagePage{Items: out}, nil
}

func (m *memMsgRepo) ListMessagesForDevice(ctx context.Context, chatID, deviceID string, limit int, cursor string) (MessagePage, error) {
	return m.ListMessages(ctx, chatID, limit, cursor)
}

func (m *memMsgRepo) UpsertMessageStatus(_ context.Context, chatID, messageID, userID, status string) (MessageStatus, error) {
	return MessageStatus{
		MessageID: messageID,
		ChatID:    chatID,
		UserID:    userID,
		Status:    status,
		UpdatedAt: time.Now().UTC(),
	}, nil
}

func (m *memMsgRepo) UpdateMessageContent(_ context.Context, chatID, messageID, editorUserID, newContent string, _ []string, _ bool) (Message, error) {
	now := time.Now().UTC()
	msg := Message{
		ID:        messageID,
		ChatID:    chatID,
		UserID:    editorUserID,
		Content:   newContent,
		IsE2E:     false,
		CreatedAt: now,
		EditedAt:  &now,
	}
	return msg, nil
}

func (m *memMsgRepo) GetMessageMeta(_ context.Context, messageID string) (MessageMeta, error) {
	if strings.TrimSpace(messageID) == "" {
		return MessageMeta{}, ErrMessageNotFound
	}
	return MessageMeta{ID: messageID, ChatID: "chat-1", UserID: "u1", ReplyToMessageID: "", IsE2E: false}, nil
}

func (m *memMsgRepo) SoftDeleteMessage(_ context.Context, _ string, _ string, _ string, _ bool) error {
	return nil
}

func (m *memMsgRepo) ToggleMessageReaction(_ context.Context, _, _, _, emoji string) ([]MessageReaction, string, error) {
	return []MessageReaction{{Emoji: emoji, UserIDs: []string{"u1"}}}, "set", nil
}

func TestCreateChatAndMessageFlow(t *testing.T) {
	chatRepo := &memChatRepo{}
	msgRepo := &memMsgRepo{}

	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx := context.Background()
	createdChat, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "General",
		MemberIDs: []string{"u2"},
	})
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if createdChat.ID == "" {
		t.Fatalf("expected chat id")
	}

	createdMsg, err := svc.CreateMessage(ctx, CreateMessageInput{
		UserID:  "u1",
		ChatID:  createdChat.ID,
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if createdMsg.ID == "" {
		t.Fatalf("expected message id")
	}

	page, err := svc.ListMessages(ctx, ListMessagesInput{
		UserID: "u1",
		ChatID: createdChat.ID,
	})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one message, got %d", len(page.Items))
	}
}

func TestCreateMessageForbiddenForNonMember(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "chat-1", Title: "General"}},
		members: map[string]map[string]bool{
			"chat-1": {"u1": true},
		},
	}
	msgRepo := &memMsgRepo{}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	_, err = svc.CreateMessage(context.Background(), CreateMessageInput{
		UserID:  "u2",
		ChatID:  "chat-1",
		Content: "hello",
	})
	if err == nil {
		t.Fatalf("expected forbidden error")
	}
	var svcErr *Error
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected service error")
	}
	if svcErr.Code != CodeForbidden {
		t.Fatalf("unexpected error code: %s", svcErr.Code)
	}
}
