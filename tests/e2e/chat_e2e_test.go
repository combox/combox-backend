//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"combox-backend/internal/app"
	"combox-backend/internal/i18n"
	pgrepo "combox-backend/internal/repository/postgres"
	vkrepo "combox-backend/internal/repository/valkey"
	authsvc "combox-backend/internal/service/auth"
	chatsvc "combox-backend/internal/service/chat"
	httptransport "combox-backend/internal/transport/http"

	"github.com/gorilla/websocket"
)

type chatPublisherAdapter struct{ p *vkrepo.EventPublisher }

func (a chatPublisherAdapter) PublishDeviceMessageCreated(ctx context.Context, ev chatsvc.DeviceMessageCreatedEvent) error {
	return a.p.PublishDeviceMessageCreated(ctx, vkrepo.DeviceMessageCreatedEvent{
		MessageID:         ev.MessageID,
		ChatID:            ev.ChatID,
		SenderUserID:      ev.SenderUserID,
		SenderDeviceID:    ev.SenderDeviceID,
		RecipientDeviceID: ev.RecipientDeviceID,
		Alg:               ev.Alg,
		Header:            ev.Header,
		Ciphertext:        ev.Ciphertext,
		CreatedAt:         ev.CreatedAt,
	})
}

func (a chatPublisherAdapter) PublishUserMessageCreated(ctx context.Context, ev chatsvc.UserMessageCreatedEvent) error {
	return a.p.PublishUserMessageCreated(ctx, vkrepo.UserMessageCreatedEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		SenderUserID:    ev.SenderUserID,
		RecipientUserID: ev.RecipientUserID,
		CreatedAt:       ev.CreatedAt,
	})
}

func (a chatPublisherAdapter) PublishMessageStatus(ctx context.Context, ev chatsvc.MessageStatusEvent) error {
	return a.p.PublishMessageStatus(ctx, vkrepo.MessageStatusEvent{
		MessageID: ev.MessageID,
		ChatID:    ev.ChatID,
		UserID:    ev.UserID,
		DeviceID:  ev.DeviceID,
		Status:    ev.Status,
		At:        ev.At,
	})
}

func (a chatPublisherAdapter) PublishMessageUpdated(ctx context.Context, ev chatsvc.MessageUpdatedEvent) error {
	return a.p.PublishMessageUpdated(ctx, vkrepo.MessageUpdatedEvent{
		MessageID:       ev.MessageID,
		ChatID:          ev.ChatID,
		EditorUserID:    ev.EditorUserID,
		RecipientUserID: ev.RecipientUserID,
		Content:         ev.Content,
		EditedAt:        ev.EditedAt,
	})
}

func TestChatE2E_StandardFlow(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	pg, err := pgrepo.New(ctx, env.PostgresDSN)
	if err != nil {
		t.Fatalf("init postgres: %v", err)
	}
	t.Cleanup(pg.Close)

	vk := vkrepo.New(vkrepo.Config{Addr: env.ValkeyAddr})
	t.Cleanup(func() { _ = vk.Close() })

	if err := app.RunMigrations(ctx, logger, pg.Pool(), env.migrationsPath()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	catalog, err := i18n.LoadDir(env.stringsPath(), "en")
	if err != nil {
		t.Fatalf("load strings: %v", err)
	}

	authService, err := authsvc.New(authsvc.Config{
		Users:         pgrepo.NewAuthUserRepository(pg),
		Sessions:      pgrepo.NewAuthSessionRepository(pg),
		AccessSecret:  "test_access_secret_min_32_chars_xxxxxxxx",
		RefreshSecret: "test_refresh_secret_min_32_chars_yyyyyyyy",
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("init auth service: %v", err)
	}

	chatRepo := pgrepo.NewChatRepository(pg)
	msgRepo := pgrepo.NewMessageRepository(pg)
	publisher := vkrepo.NewEventPublisher(vk)
	statusRepo := vkrepo.NewMessageStatusRepository(vk)

	chatSvc, err := chatsvc.NewWithPublisherAndStatusRepo(chatRepo, msgRepo, chatPublisherAdapter{p: publisher}, statusRepo)
	if err != nil {
		t.Fatalf("init chat service: %v", err)
	}

	router := httptransport.NewRouter(httptransport.RouterDeps{
		Logger:        logger,
		Postgres:      pg,
		Valkey:        vk,
		ReadyTimeout:  2 * time.Second,
		I18n:          catalog,
		DefaultLocale: "en",
		AccessSecret:  "test_access_secret_min_32_chars_xxxxxxxx",
		Auth:          authService,
		Chat:          chatSvc,
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// register and obtain access token
	type registerResp struct {
		Message string `json:"message"`
		User    struct {
			ID string `json:"id"`
		} `json:"user"`
		Tokens authsvc.Tokens `json:"tokens"`
	}
	reg := doJSON[registerResp](t, srv.URL+"/api/private/v1/auth/register", http.MethodPost, map[string]string{
		"email":    "chat-e2e@example.com",
		"username": "chat-e2e-user",
		"password": "password12345",
	})
	if reg.Tokens.AccessToken == "" {
		t.Fatalf("expected access token")
	}

	reg2 := doJSON[registerResp](t, srv.URL+"/api/private/v1/auth/register", http.MethodPost, map[string]string{
		"email":    "chat-e2e-2@example.com",
		"username": "chat-e2e-user-2",
		"password": "password12345",
	})
	if reg2.User.ID == "" {
		t.Fatalf("expected second user id")
	}

	// create chat
	type createChatResp struct {
		Message string       `json:"message"`
		Chat    chatsvc.Chat `json:"chat"`
	}
	createdChat := doJSONAuth[createChatResp](t, srv.URL+"/api/private/v1/chats", http.MethodPost, map[string]any{
		"title":      "General",
		"member_ids": []string{reg2.User.ID},
	}, reg.Tokens.AccessToken)
	if strings.TrimSpace(createdChat.Chat.ID) == "" {
		t.Fatalf("expected chat id")
	}

	// connect WS for realtime assertions (in prod this is wss://, but for httptest server it's ws://)
	wsURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	wsURL.Scheme = "ws"
	wsURL.Path = "/api/private/v1/ws"
	q := wsURL.Query()
	q.Set("access_token", reg.Tokens.AccessToken)
	wsURL.RawQuery = q.Encode()

	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = wsConn.Close() }()

	// create message
	type createMsgResp struct {
		Message string          `json:"message"`
		Item    chatsvc.Message `json:"item"`
	}
	createdMsg := doJSONAuth[createMsgResp](t, srv.URL+"/api/private/v1/chats/"+createdChat.Chat.ID+"/messages", http.MethodPost, map[string]any{
		"content": "hello",
	}, reg.Tokens.AccessToken)
	if strings.TrimSpace(createdMsg.Item.ID) == "" {
		t.Fatalf("expected message id")
	}

	// ws should receive message.created event for this user
	_ = wsConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("ws payload json: %v (%s)", err, string(payload))
	}
	if ev["type"] != "message.created" {
		t.Fatalf("expected ws event type message.created, got %v (%s)", ev["type"], string(payload))
	}
	if ev["message_id"] != createdMsg.Item.ID {
		t.Fatalf("expected ws event message_id=%s, got %v (%s)", createdMsg.Item.ID, ev["message_id"], string(payload))
	}

	// list messages
	type listMsgResp struct {
		Message string            `json:"message"`
		Items   []chatsvc.Message `json:"items"`
	}
	list1 := doJSONAuth[listMsgResp](t, srv.URL+"/api/private/v1/chats/"+createdChat.Chat.ID+"/messages", http.MethodGet, nil, reg.Tokens.AccessToken)
	if len(list1.Items) != 1 {
		t.Fatalf("expected 1 message, got %d", len(list1.Items))
	}

	// edit message by id (no chat_id in URL)
	type editMsgResp struct {
		Message string          `json:"message"`
		Item    chatsvc.Message `json:"item"`
	}
	edited := doJSONAuth[editMsgResp](t, srv.URL+"/api/private/v1/messages/"+createdMsg.Item.ID, http.MethodPatch, map[string]any{
		"content": "hello2",
	}, reg.Tokens.AccessToken)
	if edited.Item.Content != "hello2" {
		t.Fatalf("expected edited content, got %q", edited.Item.Content)
	}

	// mark as read
	type readResp struct {
		Message string                `json:"message"`
		Status  chatsvc.MessageStatus `json:"status"`
	}
	read := doJSONAuth[readResp](t, srv.URL+"/api/private/v1/messages/"+createdMsg.Item.ID+"/read", http.MethodPost, nil, reg.Tokens.AccessToken)
	if read.Status.Status != "read" {
		t.Fatalf("expected status=read, got %s", read.Status.Status)
	}

	// assert Valkey got status
	vkKey := "msgstatus:" + createdMsg.Item.ID + ":" + reg.User.ID
	stored, err := vk.Client().HGet(ctx, vkKey, "status").Result()
	if err != nil {
		t.Fatalf("valkey hget status: %v", err)
	}
	if stored != "read" {
		t.Fatalf("expected valkey status=read, got %s", stored)
	}

	// delete message
	type deleteResp struct {
		Message string `json:"message"`
	}
	_ = doJSONAuth[deleteResp](t, srv.URL+"/api/private/v1/messages/"+createdMsg.Item.ID, http.MethodDelete, nil, reg.Tokens.AccessToken)

	// list messages after delete
	list2 := doJSONAuth[listMsgResp](t, srv.URL+"/api/private/v1/chats/"+createdChat.Chat.ID+"/messages", http.MethodGet, nil, reg.Tokens.AccessToken)
	if len(list2.Items) != 0 {
		t.Fatalf("expected 0 messages after delete, got %d", len(list2.Items))
	}
}

func doJSONAuth[T any](t *testing.T, url, method string, payload any, accessToken string) T {
	t.Helper()
	status, body := doRawAuth(t, url, method, payload, accessToken)
	if status < 200 || status >= 300 {
		t.Fatalf("expected 2xx, got %d: %s", status, string(body))
	}
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, string(body))
	}
	return out
}

func doRawAuth(t *testing.T, url, method string, payload any, accessToken string) (int, []byte) {
	t.Helper()

	var buf bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			t.Fatalf("encode payload: %v", err)
		}
	}

	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}
