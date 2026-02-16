package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memChatRepo struct {
	chats    []Chat
	members  map[string]map[string]bool
	failList bool
}

func (m *memChatRepo) CreateChat(_ context.Context, title string, memberIDs []string, _ string, chatType string) (Chat, error) {
	if strings.TrimSpace(chatType) == "" {
		chatType = ChatTypeStandard
	}
	created := Chat{
		ID:        "chat-1",
		Title:     title,
		IsDirect:  len(memberIDs) == 2,
		Type:      chatType,
		CreatedAt: time.Now().UTC(),
	}
	m.chats = append(m.chats, created)
	if m.members == nil {
		m.members = map[string]map[string]bool{}
	}
	m.members[created.ID] = map[string]bool{}
	for _, memberID := range memberIDs {
		m.members[created.ID][memberID] = true
	}
	return created, nil
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

func (m *memChatRepo) GetChat(_ context.Context, chatID string) (Chat, error) {
	for _, c := range m.chats {
		if c.ID == chatID {
			return c, nil
		}
	}
	return Chat{}, ErrChatNotFound
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

func (m *memChatRepo) IsChatMember(_ context.Context, chatID, userID string) (bool, error) {
	if _, ok := m.members[chatID]; !ok {
		return false, nil
	}
	return m.members[chatID][userID], nil
}

type memMsgRepo struct {
	items []Message
}

func (m *memMsgRepo) CreateMessage(_ context.Context, chatID, userID, content string) (Message, error) {
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

func (m *memMsgRepo) CreateMessageWithAttachments(ctx context.Context, chatID, userID, content string, _ []string) (Message, error) {
	return m.CreateMessage(ctx, chatID, userID, content)
}

func (m *memMsgRepo) CreateMessageE2E(_ context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope) (Message, error) {
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

func (m *memMsgRepo) CreateMessageE2EWithAttachments(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, _ []string) (Message, error) {
	return m.CreateMessageE2E(ctx, chatID, userID, senderDeviceID, envelopes)
}

func (m *memMsgRepo) CreateForwardedMessage(ctx context.Context, chatID, sourceMessageID, userID string) (Message, error) {
	// For tests we only need to satisfy the interface.
	return m.CreateMessage(ctx, chatID, userID, "forward")
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

func (m *memMsgRepo) UpdateMessageContent(_ context.Context, chatID, messageID, editorUserID, newContent string) (Message, error) {
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
	return MessageMeta{ID: messageID, ChatID: "chat-1", UserID: "u1", IsE2E: false}, nil
}

func (m *memMsgRepo) SoftDeleteMessage(_ context.Context, _ string, _ string, _ string) error {
	return nil
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
