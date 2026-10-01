package chat

import (
	"context"
	"errors"
	"strconv"
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
	userStates  map[string]map[string]ChatUserState
	pinnedMsgs  map[string]string
	failList    bool
}

func (m *memChatRepo) userState(userID, chatID string) ChatUserState {
	if m.userStates == nil {
		return ChatUserState{}
	}
	return m.userStates[userID][chatID]
}

func (m *memChatRepo) mutateUserState(userID, chatID string, mutate func(*ChatUserState)) {
	if m.userStates == nil {
		m.userStates = map[string]map[string]ChatUserState{}
	}
	if m.userStates[userID] == nil {
		m.userStates[userID] = map[string]ChatUserState{}
	}
	state := m.userStates[userID][chatID]
	mutate(&state)
	m.userStates[userID][chatID] = state
}

func (m *memChatRepo) GetChatUserState(_ context.Context, userID, chatID string) (ChatUserState, error) {
	return m.userState(userID, chatID), nil
}

func (m *memChatRepo) ListChatUserStates(_ context.Context, userID string) (map[string]ChatUserState, error) {
	out := make(map[string]ChatUserState, len(m.userStates[userID]))
	for chatID, state := range m.userStates[userID] {
		out[chatID] = state
	}
	return out, nil
}

func (m *memChatRepo) SetChatArchived(_ context.Context, userID, chatID string, archived bool) error {
	m.mutateUserState(userID, chatID, func(state *ChatUserState) { state.Archived = archived })
	return nil
}

func (m *memChatRepo) SetChatPinned(_ context.Context, userID, chatID string, pinned bool, pinScope string, pinOrder int64) error {
	m.mutateUserState(userID, chatID, func(state *ChatUserState) {
		state.Pinned = pinned
		state.PinScope = pinScope
		state.PinOrder = pinOrder
	})
	return nil
}

func (m *memChatRepo) SetChatPinnedMessage(_ context.Context, chatID, messageID string) error {
	if m.pinnedMsgs == nil {
		m.pinnedMsgs = map[string]string{}
	}
	if strings.TrimSpace(messageID) == "" {
		delete(m.pinnedMsgs, chatID)
		return nil
	}
	m.pinnedMsgs[chatID] = messageID
	return nil
}

func (m *memChatRepo) GetChatPinnedMessageID(_ context.Context, chatID string) (string, error) {
	return m.pinnedMsgs[chatID], nil
}

func (m *memChatRepo) SetChatWallpaper(_ context.Context, chatID, wallpaperKind, wallpaperValue string) error {
	kind := strings.TrimSpace(strings.ToLower(wallpaperKind))
	value := strings.TrimSpace(wallpaperValue)
	if kind == "" {
		kind = WallpaperKindNone
	}
	if kind == WallpaperKindNone {
		value = ""
	}
	for i := range m.chats {
		if m.chats[i].ID != chatID {
			continue
		}
		kindCopy := kind
		m.chats[i].WallpaperKind = &kindCopy
		if value == "" {
			m.chats[i].WallpaperValue = nil
		} else {
			valueCopy := value
			m.chats[i].WallpaperValue = &valueCopy
		}
		return nil
	}
	return ErrChatNotFound
}

func (m *memChatRepo) SetChatHistoryCleared(_ context.Context, userID, chatID string, at time.Time) error {
	m.mutateUserState(userID, chatID, func(state *ChatUserState) {
		value := at
		state.HistoryClearedAt = &value
	})
	return nil
}

func (m *memChatRepo) DeleteChatUserState(_ context.Context, userID, chatID string) error {
	if m.userStates == nil {
		return nil
	}
	delete(m.userStates[userID], chatID)
	return nil
}

// applyChatSettingDefaults mirrors the SQL column defaults from
// migration 000034 for chats created through the in-memory repository.
func applyChatSettingDefaults(c Chat) Chat {
	reactionsEnabled := true
	signMessages := false
	showAuthorsProfiles := false
	autoTranslate := false
	slowModeSeconds := 0
	c.ReactionsEnabled = &reactionsEnabled
	c.SignMessages = &signMessages
	c.ShowAuthorsProfiles = &showAuthorsProfiles
	c.AutoTranslate = &autoTranslate
	c.SlowModeSeconds = &slowModeSeconds
	return c
}

func (m *memChatRepo) CreateChat(_ context.Context, title string, memberIDs []string, creatorID string, chatType string, kind string) (Chat, error) {
	if strings.TrimSpace(chatType) == "" {
		chatType = ChatTypeStandard
	}
	kind = strings.TrimSpace(strings.ToLower(kind))
	createdID := "chat-1"
	if len(m.chats) > 0 {
		createdID = "chat-" + strconv.Itoa(len(m.chats)+1)
	}
	created := Chat{
		ID:              createdID,
		Title:           title,
		Type:            chatType,
		CommentsEnabled: true,
		BotID:           nil,
		CreatedAt:       time.Now().UTC(),
	}
	switch kind {
	case ChatKindGroup:
		created.IsDirect = false
		created.Kind = ChatKindGroup
	default:
		created.IsDirect = len(memberIDs) == 2
		created.Kind = "group"
		if created.IsDirect {
			created.Kind = "direct"
		}
	}
	created = applyChatSettingDefaults(created)
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
		if !created.IsDirect && memberID == creatorID {
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
	created = applyChatSettingDefaults(created)
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
	created = applyChatSettingDefaults(created)
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
			state := m.userState(userID, c.ID)
			c.Archived = state.Archived
			c.Pinned = state.Pinned
			c.PinScope = state.PinScope
			c.PinOrder = state.PinOrder
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
			if parent, ok := m.chatByID(parentChatID); ok {
				parentTitle := parent.Title
				c.ParentTitle = &parentTitle
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memChatRepo) chatByID(chatID string) (Chat, bool) {
	for _, c := range m.chats {
		if c.ID == chatID {
			return c, true
		}
	}
	return Chat{}, false
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
		if input.ReactionsEnabled.Set {
			value := input.ReactionsEnabled.Value
			m.chats[i].ReactionsEnabled = &value
		}
		if input.SignMessages.Set {
			value := input.SignMessages.Value
			m.chats[i].SignMessages = &value
		}
		if input.ShowAuthorsProfiles.Set {
			value := input.ShowAuthorsProfiles.Value
			m.chats[i].ShowAuthorsProfiles = &value
		}
		if input.AutoTranslate.Set {
			value := input.AutoTranslate.Value
			m.chats[i].AutoTranslate = &value
		}
		if input.SlowModeSeconds.Set {
			value := input.SlowModeSeconds.Value
			m.chats[i].SlowModeSeconds = &value
		}
		if input.DiscussionChatID.Set {
			m.chats[i].DiscussionChatID = nil
			if input.DiscussionChatID.Value != nil {
				if value := strings.TrimSpace(*input.DiscussionChatID.Value); value != "" {
					m.chats[i].DiscussionChatID = &value
				}
			}
		}
		if input.SendPermission.Set {
			m.chats[i].SendPermission = input.SendPermission.Value
		}
		if input.Description.Set {
			m.chats[i].Description = derefString(input.Description.Value)
		}
		if input.IconEmoji.Set {
			m.chats[i].IconEmoji = derefString(input.IconEmoji.Value)
		}
		if input.ChannelType.Set {
			m.chats[i].ChannelType = input.ChannelType.Value
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

func (m *memChatRepo) CountChannelSubscribers(_ context.Context, chatID string) (int, error) {
	n := 0
	for userID, isMember := range m.members[chatID] {
		if !isMember {
			continue
		}
		if strings.EqualFold(m.roles[chatID][userID], "banned") {
			continue
		}
		n++
	}
	return n, nil
}

type memMsgRepo struct {
	items                  []Message
	meta                   *MessageMeta
	attachments            map[string][]AttachmentSummary
	softDeleteCalled       bool
	softDeleteAllowForeign bool
	softDeleteDeleterID    string
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

func (m *memMsgRepo) GetMessageByID(_ context.Context, chatID, messageID string) (Message, error) {
	for _, item := range m.items {
		if item.ID == messageID && item.ChatID == chatID {
			return item, nil
		}
	}
	if m.meta != nil && m.meta.ID == messageID {
		chatIDCopy := chatID
		if strings.TrimSpace(m.meta.ChatID) != "" {
			chatIDCopy = m.meta.ChatID
		}
		return Message{
			ID:     messageID,
			ChatID: chatIDCopy,
			UserID: m.meta.UserID,
		}, nil
	}
	return Message{}, ErrMessageNotFound
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
	if m.meta != nil {
		meta := *m.meta
		if meta.ID == "" {
			meta.ID = messageID
		}
		return meta, nil
	}
	return MessageMeta{ID: messageID, ChatID: "chat-1", UserID: "u1", ReplyToMessageID: "", IsE2E: false}, nil
}

func (m *memMsgRepo) SoftDeleteMessage(_ context.Context, _ string, _ string, deleterID string, allowForeign bool) error {
	m.softDeleteCalled = true
	m.softDeleteAllowForeign = allowForeign
	m.softDeleteDeleterID = deleterID
	ownerID := "u1"
	if m.meta != nil {
		ownerID = m.meta.UserID
	}
	if !allowForeign && deleterID != ownerID {
		return ErrMessageNotFound
	}
	return nil
}

func (m *memMsgRepo) ToggleMessageReaction(_ context.Context, _, _, _, emoji string) ([]MessageReaction, string, error) {
	return []MessageReaction{{Emoji: emoji, UserIDs: []string{"u1"}}}, "set", nil
}

func (m *memMsgRepo) CountVisibleMessages(_ context.Context, chatID string, visibleAfter *time.Time) (int, error) {
	count := 0
	for _, item := range m.items {
		if item.ChatID != chatID {
			continue
		}
		if visibleAfter != nil && !item.CreatedAt.After(*visibleAfter) {
			continue
		}
		count++
	}
	return count, nil
}

func (m *memMsgRepo) ListAttachmentSummaries(_ context.Context, messageIDs []string) (map[string][]AttachmentSummary, error) {
	out := make(map[string][]AttachmentSummary, len(messageIDs))
	if m.attachments == nil {
		return out, nil
	}
	for _, id := range messageIDs {
		if items, ok := m.attachments[id]; ok && len(items) > 0 {
			out[id] = items
		}
	}
	return out, nil
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

func TestCreateChatWithKindGroupCreatesNewGroup(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "direct-1", Title: "peer", IsDirect: true, Type: ChatTypeStandard, Kind: "direct"}},
		members: map[string]map[string]bool{
			"direct-1": {"u1": true, "u2": true},
		},
		roles: map[string]map[string]string{
			"direct-1": {"u1": "member", "u2": "member"},
		},
	}
	msgRepo := &memMsgRepo{}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	// Legacy behaviour (no kind): creator + one participant reuses the direct chat.
	legacy, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Peer",
		MemberIDs: []string{"u2"},
	})
	if err != nil {
		t.Fatalf("create chat without kind: %v", err)
	}
	if legacy.ID != "direct-1" || !legacy.IsDirect {
		t.Fatalf("expected legacy call to reuse direct chat, got id=%q isDirect=%v", legacy.ID, legacy.IsDirect)
	}

	// kind=group: a new group chat must be created even with a single participant.
	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Team",
		MemberIDs: []string{"u2"},
		Type:      ChatTypeStandard,
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create group chat: %v", err)
	}
	if group.ID == "direct-1" {
		t.Fatalf("expected a new chat instead of the existing direct chat")
	}
	if group.IsDirect || group.Kind != ChatKindGroup {
		t.Fatalf("expected group chat, got isDirect=%v kind=%q", group.IsDirect, group.Kind)
	}
	role, err := chatRepo.GetChatMemberRole(ctx, group.ID, "u1")
	if err != nil {
		t.Fatalf("creator role: %v", err)
	}
	if role != "owner" {
		t.Fatalf("expected creator to be owner of the group, got %q", role)
	}

	// kind=direct keeps the legacy member-count behaviour.
	direct, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Peer again",
		MemberIDs: []string{"u2"},
		Kind:      ChatKindDirect,
	})
	if err != nil {
		t.Fatalf("create direct chat: %v", err)
	}
	if direct.ID != "direct-1" || !direct.IsDirect {
		t.Fatalf("expected direct kind to reuse direct chat, got id=%q isDirect=%v", direct.ID, direct.IsDirect)
	}
}

func TestCreateGroupWithoutMembersCreatesGroup(t *testing.T) {
	chatRepo := &memChatRepo{}
	msgRepo := &memMsgRepo{}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID: "u1",
		Title:  "Empty group",
		Kind:   ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create empty group: %v", err)
	}
	if group.IsDirect || group.Kind != ChatKindGroup {
		t.Fatalf("expected group chat, got isDirect=%v kind=%q", group.IsDirect, group.Kind)
	}
	isMember, err := chatRepo.IsChatMember(ctx, group.ID, "u1")
	if err != nil {
		t.Fatalf("membership lookup: %v", err)
	}
	if !isMember {
		t.Fatalf("expected creator to be a member of the new group")
	}
	role, err := chatRepo.GetChatMemberRole(ctx, group.ID, "u1")
	if err != nil {
		t.Fatalf("creator role: %v", err)
	}
	if role != "owner" {
		t.Fatalf("expected creator role owner, got %q", role)
	}

	if _, err := svc.CreateChat(ctx, CreateChatInput{
		UserID: "u1",
		Title:  "Broken",
		Kind:   "chat",
	}); err == nil {
		t.Fatalf("expected invalid kind to be rejected")
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

func TestChatListActionsArePerViewer(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "chat-1", Title: "General", Kind: "group", CommentsEnabled: true}},
		members: map[string]map[string]bool{
			"chat-1": {"u1": true, "u2": true},
		},
		roles: map[string]map[string]string{
			"chat-1": {"u1": "owner", "u2": "member"},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	archived, err := svc.ArchiveChat(ctx, "u1", "chat-1", true)
	if err != nil {
		t.Fatalf("archive chat: %v", err)
	}
	if !archived.Archived {
		t.Fatalf("expected archived chat for viewer")
	}

	peer, err := svc.GetChat(ctx, "u2", "chat-1")
	if err != nil {
		t.Fatalf("get chat for peer: %v", err)
	}
	if peer.Archived {
		t.Fatalf("archive state must not leak to another viewer")
	}

	pinned, err := svc.PinChat(ctx, "u2", "chat-1", true, "all", 0)
	if err != nil {
		t.Fatalf("pin chat: %v", err)
	}
	if !pinned.Pinned {
		t.Fatalf("expected pinned chat for viewer")
	}
	if pinned.Archived {
		t.Fatalf("pin must not change archive state of the same viewer unexpectedly")
	}
	if _, err := svc.PinChat(ctx, "u1", "chat-1", true, "all", 0); err != nil {
		t.Fatalf("pin chat for first viewer: %v", err)
	}

	if _, err := svc.ClearChatHistory(ctx, "u1", "chat-1"); err != nil {
		t.Fatalf("clear history: %v", err)
	}
	state, err := chatRepo.GetChatUserState(ctx, "u1", "chat-1")
	if err != nil {
		t.Fatalf("get user state: %v", err)
	}
	if state.HistoryClearedAt == nil {
		t.Fatalf("expected history watermark for viewer")
	}

	if err := svc.MarkChatRead(ctx, "u1", "chat-1"); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	if _, err := svc.ArchiveChat(ctx, "u3", "chat-1", true); err == nil {
		t.Fatalf("expected error for non member")
	}

	chats, err := svc.ListChats(ctx, "u1")
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected one chat, got %d", len(chats))
	}
	if !chats[0].Archived || !chats[0].Pinned {
		t.Fatalf("expected archived and pinned flags in chat list, got archived=%v pinned=%v", chats[0].Archived, chats[0].Pinned)
	}
}

func TestDeleteDirectChatOnlyForCaller(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "chat-1", Title: "peer", IsDirect: true, Type: "standard", Kind: "direct"}},
		members: map[string]map[string]bool{
			"chat-1": {"u1": true, "u2": true},
		},
		roles: map[string]map[string]string{
			"chat-1": {"u1": "member", "u2": "member"},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	if err := chatRepo.SetChatArchived(ctx, "u1", "chat-1", true); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if err := svc.DeleteChat(ctx, "u1", "chat-1"); err != nil {
		t.Fatalf("delete direct chat: %v", err)
	}

	stillMember, err := chatRepo.IsChatMember(ctx, "chat-1", "u1")
	if err != nil {
		t.Fatalf("check caller membership: %v", err)
	}
	if stillMember {
		t.Fatalf("expected caller to be removed from direct chat")
	}
	peerMember, err := chatRepo.IsChatMember(ctx, "chat-1", "u2")
	if err != nil {
		t.Fatalf("check peer membership: %v", err)
	}
	if !peerMember {
		t.Fatalf("expected peer to keep the direct chat")
	}

	callerChats, err := svc.ListChats(ctx, "u1")
	if err != nil {
		t.Fatalf("list chats for caller: %v", err)
	}
	if len(callerChats) != 0 {
		t.Fatalf("expected no chats for caller, got %d", len(callerChats))
	}
	peerChats, err := svc.ListChats(ctx, "u2")
	if err != nil {
		t.Fatalf("list chats for peer: %v", err)
	}
	if len(peerChats) != 1 {
		t.Fatalf("expected peer to keep one chat, got %d", len(peerChats))
	}

	state, err := chatRepo.GetChatUserState(ctx, "u1", "chat-1")
	if err != nil {
		t.Fatalf("read caller state: %v", err)
	}
	if state.Archived {
		t.Fatalf("expected caller state to be dropped with the chat")
	}

	if _, err := svc.ArchiveChat(ctx, "u1", "chat-1", true); err == nil {
		t.Fatalf("expected removed viewer to lose access")
	}
}

func TestDeleteForeignMessageInDirectChat(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{
			{ID: "chat-1", Title: "peer", IsDirect: true, Type: ChatTypeStandard, Kind: "direct"},
			{ID: "chat-2", Title: "General", IsDirect: false, Type: ChatTypeStandard, Kind: "group"},
		},
		members: map[string]map[string]bool{
			"chat-1": {"u1": true, "u2": true},
			"chat-2": {"u1": true, "u2": true},
		},
		roles: map[string]map[string]string{
			"chat-1": {"u1": "member", "u2": "member"},
			"chat-2": {"u1": "owner", "u2": "member"},
		},
	}
	msgRepo := &memMsgRepo{
		meta: &MessageMeta{ID: "msg-1", ChatID: "chat-1", UserID: "u1", IsE2E: false},
	}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	if err := svc.DeleteMessageByID(ctx, "u2", "msg-1"); err != nil {
		t.Fatalf("delete foreign message in direct chat: %v", err)
	}
	if !msgRepo.softDeleteCalled {
		t.Fatalf("expected soft delete to be called")
	}
	if !msgRepo.softDeleteAllowForeign {
		t.Fatalf("expected allowForeign to be true for direct chat")
	}
	if msgRepo.softDeleteDeleterID != "u2" {
		t.Fatalf("unexpected deleter id: %q", msgRepo.softDeleteDeleterID)
	}

	msgRepo.softDeleteCalled = false
	msgRepo.softDeleteAllowForeign = false
	msgRepo.meta = &MessageMeta{ID: "msg-2", ChatID: "chat-2", UserID: "u1", IsE2E: false}
	if err := svc.DeleteMessageByID(ctx, "u2", "msg-2"); err == nil {
		t.Fatalf("expected foreign delete to fail in group chat")
	}
	if !msgRepo.softDeleteCalled || msgRepo.softDeleteAllowForeign {
		t.Fatalf("expected group chat delete with allowForeign=false")
	}

	msgRepo.softDeleteCalled = false
	msgRepo.meta = &MessageMeta{ID: "msg-3", ChatID: "chat-1", UserID: "u1", IsE2E: false}
	if err := svc.DeleteMessageByID(ctx, "u3", "msg-3"); err == nil {
		t.Fatalf("expected forbidden error for non member")
	}
	if msgRepo.softDeleteCalled {
		t.Fatalf("repo must not be called for non members")
	}
}

func expectChatErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	var svcErr *Error
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected service error, got %v", err)
	}
	if svcErr.Code != code {
		t.Fatalf("expected code %s, got %s", code, svcErr.Code)
	}
}

func TestPinMessageLifecycle(t *testing.T) {
	chatRepo := &memChatRepo{
		chats:   []Chat{{ID: "chat-1", Title: "General"}},
		members: map[string]map[string]bool{"chat-1": {"u1": true, "u2": true}},
	}
	msgRepo := &memMsgRepo{items: []Message{
		{ID: "msg-1", ChatID: "chat-1", UserID: "u2", Content: "hello", CreatedAt: time.Now().UTC()},
	}}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	pinned, err := svc.GetPinnedMessage(ctx, "u1", "chat-1")
	if err != nil {
		t.Fatalf("get pinned: %v", err)
	}
	if pinned != nil {
		t.Fatalf("expected no pinned message initially, got %v", pinned.ID)
	}

	_, err = svc.PinMessage(ctx, PinMessageInput{UserID: "u9", ChatID: "chat-1", MessageID: "msg-1", Pinned: true})
	expectChatErrorCode(t, err, CodeForbidden)

	item, err := svc.PinMessage(ctx, PinMessageInput{UserID: "u1", ChatID: "chat-1", MessageID: "msg-1", Pinned: true})
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	if item == nil || item.ID != "msg-1" {
		t.Fatalf("expected pinned item msg-1, got %+v", item)
	}

	pinned, err = svc.GetPinnedMessage(ctx, "u2", "chat-1")
	if err != nil {
		t.Fatalf("get pinned: %v", err)
	}
	if pinned == nil || pinned.ID != "msg-1" {
		t.Fatalf("expected pinned msg-1, got %+v", pinned)
	}

	_, err = svc.PinMessage(ctx, PinMessageInput{UserID: "u1", ChatID: "chat-1", MessageID: "msg-missing", Pinned: true})
	expectChatErrorCode(t, err, CodeNotFound)

	item, err = svc.PinMessage(ctx, PinMessageInput{UserID: "u1", ChatID: "chat-1", MessageID: "msg-1", Pinned: false})
	if err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if item != nil {
		t.Fatalf("expected nil item after unpin, got %+v", item)
	}
	pinned, err = svc.GetPinnedMessage(ctx, "u2", "chat-1")
	if err != nil {
		t.Fatalf("get pinned: %v", err)
	}
	if pinned != nil {
		t.Fatalf("expected no pinned message after unpin, got %+v", pinned.ID)
	}

	_, err = svc.GetPinnedMessage(ctx, "u9", "chat-1")
	expectChatErrorCode(t, err, CodeForbidden)
}

func TestForwardMessageRequiresSourceReadable(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "chat-1"}, {ID: "chat-2"}, {ID: "chat-3"}},
		members: map[string]map[string]bool{
			"chat-1": {"u1": true},
			"chat-2": {"u1": true},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{meta: &MessageMeta{ID: "src-1", ChatID: "chat-3", UserID: "u9"}})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	_, err = svc.ForwardMessage(ctx, ForwardMessageInput{
		UserID:          "u1",
		ChatID:          "chat-1",
		SourceMessageID: "src-1",
	})
	expectChatErrorCode(t, err, CodeForbidden)

	svc.messages.(*memMsgRepo).meta = &MessageMeta{ID: "src-2", ChatID: "chat-2", UserID: "u1"}
	forwarded, err := svc.ForwardMessage(ctx, ForwardMessageInput{
		UserID:          "u1",
		ChatID:          "chat-1",
		SourceMessageID: "src-2",
	})
	if err != nil {
		t.Fatalf("forward from readable chat: %v", err)
	}
	if forwarded.ChatID != "chat-1" {
		t.Fatalf("expected forward target chat-1, got %s", forwarded.ChatID)
	}
}

func TestForwardMessageRequiresPostPermission(t *testing.T) {
	channel := Chat{ID: "chan-1", Title: "news", Type: ChatTypeStandard, Kind: "standalone_channel", IsPublic: true}
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "src-chat", Title: "team", Type: ChatTypeStandard, Kind: "group"}, channel},
		members: map[string]map[string]bool{
			"src-chat": {"u1": true, "u2": true},
			"chan-1":   {"u1": true, "u2": true},
		},
		roles: map[string]map[string]string{
			"chan-1": {"u1": "subscriber", "u2": "admin"},
		},
	}
	msgRepo := &memMsgRepo{meta: &MessageMeta{ID: "src-1", ChatID: "src-chat", UserID: "u1"}}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	// A subscriber may read the channel but must not be able to post into it.
	_, err = svc.ForwardMessage(ctx, ForwardMessageInput{
		UserID:          "u1",
		ChatID:          "chan-1",
		SourceMessageID: "src-1",
	})
	expectChatErrorCode(t, err, CodeForbidden)

	// An admin of the same channel may forward.
	forwarded, err := svc.ForwardMessage(ctx, ForwardMessageInput{
		UserID:          "u2",
		ChatID:          "chan-1",
		SourceMessageID: "src-1",
	})
	if err != nil {
		t.Fatalf("admin forward: %v", err)
	}
	if forwarded.ChatID != "chan-1" {
		t.Fatalf("expected forward target chan-1, got %s", forwarded.ChatID)
	}
}

func TestPinChatScopeIsPerCategory(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "chat-1", Title: "peer", IsDirect: true, Type: "standard", Kind: "direct"}},
		members: map[string]map[string]bool{
			"chat-1": {"u1": true, "u2": true},
		},
		roles: map[string]map[string]string{
			"chat-1": {"u1": "member", "u2": "member"},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	pinned, err := svc.PinChat(ctx, "u1", "chat-1", true, "direct", 3)
	if err != nil {
		t.Fatalf("pin into direct scope: %v", err)
	}
	if !pinned.Pinned || pinned.PinScope != "direct" || pinned.PinOrder != 3 {
		t.Fatalf("expected pinned direct/3, got pinned=%v scope=%q order=%d", pinned.Pinned, pinned.PinScope, pinned.PinOrder)
	}

	chats, err := svc.ListChats(ctx, "u1")
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 1 || !chats[0].Pinned || chats[0].PinScope != "direct" || chats[0].PinOrder != 3 {
		t.Fatalf("expected list to carry pin scope direct/3, got %+v", chats)
	}

	moved, err := svc.PinChat(ctx, "u1", "chat-1", true, "group", 1)
	if err != nil {
		t.Fatalf("move pin to group scope: %v", err)
	}
	if moved.PinScope != "group" || moved.PinOrder != 1 {
		t.Fatalf("expected scope group/1 after move, got %q/%d", moved.PinScope, moved.PinOrder)
	}

	fallback, err := svc.PinChat(ctx, "u1", "chat-1", true, "not-a-scope", 0)
	if err != nil {
		t.Fatalf("pin with unknown scope: %v", err)
	}
	if fallback.PinScope != DefaultPinScope {
		t.Fatalf("expected unknown scope to fall back to %q, got %q", DefaultPinScope, fallback.PinScope)
	}

	unpinned, err := svc.PinChat(ctx, "u1", "chat-1", false, "all", 7)
	if err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if unpinned.Pinned || unpinned.PinOrder != 0 {
		t.Fatalf("expected unpin to reset order, got pinned=%v order=%d", unpinned.Pinned, unpinned.PinOrder)
	}

	other, err := svc.GetChat(ctx, "u2", "chat-1")
	if err != nil {
		t.Fatalf("peer view: %v", err)
	}
	if other.Pinned {
		t.Fatalf("pin state must stay per viewer, peer got pinned=%v", other.Pinned)
	}
}

func TestDeleteSystemBotChat(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "chat-bot", Title: "Combox service notifications", IsDirect: false, Type: "standard", Kind: "bot"}},
		members: map[string]map[string]bool{
			"chat-bot": {"u1": true},
		},
		roles: map[string]map[string]string{
			"chat-bot": {"u1": "member"},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	if err := svc.DeleteChat(ctx, "u1", "chat-bot"); err != nil {
		t.Fatalf("delete bot chat: %v", err)
	}
	member, err := chatRepo.IsChatMember(ctx, "chat-bot", "u1")
	if err != nil {
		t.Fatalf("membership lookup: %v", err)
	}
	if member {
		t.Fatal("expected caller membership removed from bot chat")
	}
	chats, err := svc.ListChats(ctx, "u1")
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 0 {
		t.Fatalf("expected bot chat to disappear from the list, got %d", len(chats))
	}
}

func TestDeleteDirectChatForEveryone(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{
			{ID: "chat-1", Title: "peer", IsDirect: true, Type: "standard", Kind: "direct"},
			{ID: "chat-group", Title: "group", IsDirect: false, Type: "standard", Kind: "group"},
		},
		members: map[string]map[string]bool{
			"chat-1":     {"u1": true, "u2": true},
			"chat-group": {"u1": true},
		},
		roles: map[string]map[string]string{
			"chat-1":     {"u1": "member", "u2": "member"},
			"chat-group": {"u1": "owner"},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	if err := svc.DeleteChatForEveryone(ctx, "u1", "chat-1"); err != nil {
		t.Fatalf("delete direct chat for everyone: %v", err)
	}
	if _, err := chatRepo.GetChat(ctx, "chat-1"); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("expected chat-1 to be removed for both peers, got err=%v", err)
	}

	err = svc.DeleteChatForEveryone(ctx, "u1", "chat-group")
	if err == nil {
		t.Fatal("expected group chats to be rejected by delete-for-everyone")
	}
}

type memInviteRepo struct {
	created []ChatInvite
}

func (m *memInviteRepo) Create(_ context.Context, chatID, inviterID, inviteeID string, ttl time.Duration) (ChatInvite, error) {
	invite := ChatInvite{
		Token:     "tok-" + inviteeID,
		ChatID:    chatID,
		InviterID: inviterID,
		InviteeID: inviteeID,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	m.created = append(m.created, invite)
	return invite, nil
}

func (m *memInviteRepo) Consume(_ context.Context, token string) (ChatInvite, bool, error) {
	for _, item := range m.created {
		if item.Token == token {
			return item, true, nil
		}
	}
	return ChatInvite{}, false, nil
}

func TestAddMembersAddsImmediatelyWithInviteRepository(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{{ID: "chat-group", Title: "group", IsDirect: false, Type: ChatTypeStandard, Kind: "group"}},
		members: map[string]map[string]bool{
			"chat-group": {"u1": true},
		},
		roles: map[string]map[string]string{
			"chat-group": {"u1": "owner"},
		},
	}
	msgRepo := &memMsgRepo{}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()
	invites := &memInviteRepo{}
	svc.SetInviteRepository(invites, time.Hour)

	members, err := svc.AddMembers(ctx, "u1", "chat-group", []string{"u2", "u3"})
	if err != nil {
		t.Fatalf("add members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}
	for _, memberID := range []string{"u2", "u3"} {
		isMember, err := chatRepo.IsChatMember(ctx, "chat-group", memberID)
		if err != nil {
			t.Fatalf("membership lookup for %s: %v", memberID, err)
		}
		if !isMember {
			t.Fatalf("expected %s to be added to the group immediately", memberID)
		}
	}
	if len(chatRepo.chats) != 1 {
		t.Fatalf("expected no extra chats to be created, got %d", len(chatRepo.chats))
	}
	if len(msgRepo.items) != 0 {
		t.Fatalf("expected no invite direct messages, got %d", len(msgRepo.items))
	}
	if len(invites.created) != 0 {
		t.Fatalf("expected no per-member invite tokens for manual adds, got %d", len(invites.created))
	}
}

func strPtr(value string) *string { return &value }

func intPtr(value int) *int { return &value }

func TestUpdateChatPersistsChannelSettings(t *testing.T) {
	chatRepo := &memChatRepo{}
	msgRepo := &memMsgRepo{}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Team",
		MemberIDs: []string{"u2", "u3"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	discussion, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Ideas",
		MemberIDs: []string{"u2"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create discussion group: %v", err)
	}

	updated, err := svc.UpdateChat(ctx, UpdateChatInput{
		UserID:              "u1",
		ChatID:              group.ID,
		CommentsEnabled:     OptionalBool{Set: true, Value: true},
		ReactionsEnabled:    OptionalBool{Set: true, Value: false},
		SignMessages:        OptionalBool{Set: true, Value: true},
		ShowAuthorsProfiles: OptionalBool{Set: true, Value: true},
		AutoTranslate:       OptionalBool{Set: true, Value: true},
		SlowModeSeconds:     OptionalInt{Set: true, Value: 30},
		DiscussionChatID:    OptionalString{Set: true, Value: &discussion.ID},
	})
	if err != nil {
		t.Fatalf("update chat: %v", err)
	}
	if updated.ReactionsEnabled == nil || *updated.ReactionsEnabled {
		t.Fatalf("expected reactions_enabled=false, got %v", updated.ReactionsEnabled)
	}
	if updated.SignMessages == nil || !*updated.SignMessages {
		t.Fatalf("expected sign_messages=true, got %v", updated.SignMessages)
	}
	if updated.ShowAuthorsProfiles == nil || !*updated.ShowAuthorsProfiles {
		t.Fatalf("expected show_authors_profiles=true, got %v", updated.ShowAuthorsProfiles)
	}
	if updated.AutoTranslate == nil || !*updated.AutoTranslate {
		t.Fatalf("expected auto_translate=true, got %v", updated.AutoTranslate)
	}
	if updated.SlowModeSeconds == nil || *updated.SlowModeSeconds != 30 {
		t.Fatalf("expected slow_mode_seconds=30, got %v", updated.SlowModeSeconds)
	}
	if updated.DiscussionChatID == nil || *updated.DiscussionChatID != discussion.ID {
		t.Fatalf("expected discussion_chat_id=%s, got %v", discussion.ID, updated.DiscussionChatID)
	}

	fetched, err := svc.GetChat(ctx, "u1", group.ID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if fetched.ReactionsEnabled == nil || *fetched.ReactionsEnabled {
		t.Fatalf("expected persisted reactions_enabled=false, got %v", fetched.ReactionsEnabled)
	}
	if fetched.SlowModeSeconds == nil || *fetched.SlowModeSeconds != 30 {
		t.Fatalf("expected persisted slow_mode_seconds=30, got %v", fetched.SlowModeSeconds)
	}
	if fetched.DiscussionChatID == nil || *fetched.DiscussionChatID != discussion.ID {
		t.Fatalf("expected persisted discussion_chat_id=%s, got %v", discussion.ID, fetched.DiscussionChatID)
	}

	empty := ""
	cleared, err := svc.UpdateChat(ctx, UpdateChatInput{
		UserID:           "u1",
		ChatID:           group.ID,
		DiscussionChatID: OptionalString{Set: true, Value: &empty},
	})
	if err != nil {
		t.Fatalf("clear discussion chat: %v", err)
	}
	if cleared.DiscussionChatID != nil {
		t.Fatalf("expected discussion chat to be cleared, got %v", *cleared.DiscussionChatID)
	}
}

func TestUpdateChatSettingsValidation(t *testing.T) {
	chatRepo := &memChatRepo{
		chats: []Chat{
			{ID: "group-1", Title: "General", Type: ChatTypeStandard, Kind: ChatKindGroup},
			{ID: "channel-1", Title: "News", Type: ChatTypeStandard, Kind: "channel", ParentChatID: strPtr("group-1")},
			{ID: "direct-1", Title: "peer", IsDirect: true, Type: ChatTypeStandard, Kind: "direct"},
		},
		members: map[string]map[string]bool{
			"group-1":   {"u1": true, "u2": true},
			"channel-1": {"u1": true, "u2": true},
			"direct-1":  {"u1": true, "u2": true},
		},
		roles: map[string]map[string]string{
			"group-1":   {"u1": "owner", "u2": "member"},
			"channel-1": {"u1": "owner", "u2": "member"},
			"direct-1":  {"u1": "member", "u2": "member"},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	cases := []struct {
		name     string
		chatID   string
		userID   string
		input    UpdateChatInput
		wantCode string
	}{
		{
			name:     "direct chat is rejected",
			chatID:   "direct-1",
			userID:   "u1",
			input:    UpdateChatInput{Title: OptionalString{Set: true, Value: strPtr("Renamed")}},
			wantCode: CodeInvalidArgument,
		},
		{
			name:     "plain member cannot edit settings",
			chatID:   "group-1",
			userID:   "u2",
			input:    UpdateChatInput{ReactionsEnabled: OptionalBool{Set: true, Value: false}},
			wantCode: CodeForbidden,
		},
		{
			name:     "negative slow mode is rejected",
			chatID:   "group-1",
			userID:   "u1",
			input:    UpdateChatInput{SlowModeSeconds: OptionalInt{Set: true, Value: -1}},
			wantCode: CodeInvalidArgument,
		},
		{
			name:     "send permission requires a channel",
			chatID:   "group-1",
			userID:   "u1",
			input:    UpdateChatInput{SendPermission: OptionalString{Set: true, Value: strPtr(SendPermissionAdmins)}},
			wantCode: CodeInvalidArgument,
		},
		{
			name:     "empty payload is rejected",
			chatID:   "group-1",
			userID:   "u1",
			input:    UpdateChatInput{},
			wantCode: CodeInvalidArgument,
		},
		{
			name:   "send permission accepted on a channel",
			chatID: "channel-1",
			userID: "u1",
			input:  UpdateChatInput{SendPermission: OptionalString{Set: true, Value: strPtr(SendPermissionAdmins)}},
		},
		{
			name:   "settings accepted on a group",
			chatID: "group-1",
			userID: "u1",
			input: UpdateChatInput{
				ReactionsEnabled: OptionalBool{Set: true, Value: true},
				SlowModeSeconds:  OptionalInt{Set: true, Value: 10},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			input.UserID = tc.userID
			input.ChatID = tc.chatID

			updated, err := svc.UpdateChat(ctx, input)
			if tc.wantCode != "" {
				expectChatErrorCode(t, err, tc.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("update chat: %v", err)
			}
			if tc.input.SendPermission.Set {
				if updated.SendPermission == nil || *updated.SendPermission != SendPermissionAdmins {
					t.Fatalf("expected send_permission=%s, got %v", SendPermissionAdmins, updated.SendPermission)
				}
			}
			if tc.input.SlowModeSeconds.Set {
				if updated.SlowModeSeconds == nil || *updated.SlowModeSeconds != tc.input.SlowModeSeconds.Value {
					t.Fatalf("expected slow_mode_seconds=%d, got %v", tc.input.SlowModeSeconds.Value, updated.SlowModeSeconds)
				}
			}
		})
	}
}

func TestUpdateChatDiscussionChatValidation(t *testing.T) {
	chatRepo := &memChatRepo{}
	msgRepo := &memMsgRepo{}
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Team",
		MemberIDs: []string{"u2", "u3"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	direct, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "peer",
		MemberIDs: []string{"u2"},
	})
	if err != nil {
		t.Fatalf("create direct chat: %v", err)
	}
	if !direct.IsDirect {
		t.Fatalf("expected a direct chat fixture")
	}

	cases := []struct {
		name     string
		value    string
		wantCode string
	}{
		{name: "group target is accepted", value: group.ID},
		{name: "direct target is rejected", value: direct.ID, wantCode: CodeInvalidArgument},
		{name: "missing target is rejected", value: "missing-chat", wantCode: CodeInvalidArgument},
		{name: "explicit empty value clears the link", value: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, err := svc.UpdateChat(ctx, UpdateChatInput{
				UserID:           "u1",
				ChatID:           group.ID,
				DiscussionChatID: OptionalString{Set: true, Value: &tc.value},
			})
			if tc.wantCode != "" {
				expectChatErrorCode(t, err, tc.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("update chat: %v", err)
			}
			if tc.value == "" {
				if updated.DiscussionChatID != nil {
					t.Fatalf("expected cleared discussion chat, got %v", *updated.DiscussionChatID)
				}
				return
			}
			if updated.DiscussionChatID == nil || *updated.DiscussionChatID != tc.value {
				t.Fatalf("expected discussion_chat_id=%s, got %v", tc.value, updated.DiscussionChatID)
			}
		})
	}
}

func TestListChannelsGeneralMirrorsGroup(t *testing.T) {
	senderName := "Alice"
	sentAt := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	chatRepo := &memChatRepo{
		chats: []Chat{
			{
				ID:                    "group-1",
				Title:                 "Weekend Trip",
				Type:                  ChatTypeStandard,
				Kind:                  ChatKindGroup,
				LastMessageSenderName: &senderName,
				LastMessageAt:         &sentAt,
			},
			{
				ID:           "channel-1",
				Title:        "Plans",
				Type:         ChatTypeStandard,
				Kind:         "channel",
				ParentChatID: strPtr("group-1"),
				TopicNumber:  intPtr(2),
			},
		},
		members: map[string]map[string]bool{
			"group-1":   {"u1": true},
			"channel-1": {"u1": true},
		},
		roles: map[string]map[string]string{
			"group-1":   {"u1": "owner"},
			"channel-1": {"u1": "owner"},
		},
	}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	items, err := svc.ListChannels(context.Background(), "u1", "group-1")
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(items))
	}

	general := items[0]
	if general.IsGeneral == nil || !*general.IsGeneral {
		t.Fatalf("expected the first topic to be the virtual General topic")
	}
	if general.ID != "group-1" || general.Title != "General" {
		t.Fatalf("unexpected general topic: id=%q title=%q", general.ID, general.Title)
	}
	if general.ParentTitle == nil || *general.ParentTitle != "Weekend Trip" {
		t.Fatalf("expected general parent title to mirror the group title, got %v", general.ParentTitle)
	}
	if general.LastMessageSenderName == nil || *general.LastMessageSenderName != "Alice" {
		t.Fatalf("expected general sender to mirror the group, got %v", general.LastMessageSenderName)
	}
	if general.LastMessageAt == nil || !general.LastMessageAt.Equal(sentAt) {
		t.Fatalf("expected general last message at to mirror the group, got %v", general.LastMessageAt)
	}

	topic := items[1]
	if topic.IsGeneral != nil {
		t.Fatalf("expected topic rows to keep is_general unset")
	}
	if topic.ParentTitle == nil || *topic.ParentTitle != "Weekend Trip" {
		t.Fatalf("expected topic parent title, got %v", topic.ParentTitle)
	}
}
