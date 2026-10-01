package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatsvc "combox-backend/internal/service/chat"
)

// folderChatService records what the handlers passed in and lets each test
// pick the error the service answers with.
type folderChatService struct {
	stubChatService
	folderErr   error
	folders     []chatsvc.ChatFolder
	created     chatsvc.ChatFolder
	updated     chatsvc.ChatFolder
	gotCreate   chatsvc.CreateChatFolderInput
	gotUpdate   chatsvc.UpdateChatFolderInput
	gotChats    []string
	gotFolderID string
	gotUserID   string
	deleteCalls int
}

func (s *folderChatService) ListChatFolders(_ context.Context, userID string) ([]chatsvc.ChatFolder, error) {
	s.gotUserID = userID
	if s.folderErr != nil {
		return nil, s.folderErr
	}
	if s.folders == nil {
		return []chatsvc.ChatFolder{}, nil
	}
	return s.folders, nil
}

func (s *folderChatService) CreateChatFolder(_ context.Context, input chatsvc.CreateChatFolderInput) (chatsvc.ChatFolder, error) {
	s.gotCreate = input
	if s.folderErr != nil {
		return chatsvc.ChatFolder{}, s.folderErr
	}
	created := s.created
	created.ID = "90000000-0000-4000-8000-000000000001"
	created.Name = input.Name
	created.Icon = input.Icon
	created.ChatIDs = input.ChatIDs
	if created.ChatIDs == nil {
		created.ChatIDs = []string{}
	}
	return created, nil
}

func (s *folderChatService) UpdateChatFolder(_ context.Context, input chatsvc.UpdateChatFolderInput) (chatsvc.ChatFolder, error) {
	s.gotUpdate = input
	if s.folderErr != nil {
		return chatsvc.ChatFolder{}, s.folderErr
	}
	item := s.updated
	item.ID = input.FolderID
	if input.Name.Set && input.Name.Value != nil {
		item.Name = *input.Name.Value
	}
	if input.Icon.Set && input.Icon.Value != nil {
		item.Icon = *input.Icon.Value
	}
	if input.Position.Set {
		item.Position = input.Position.Value
	}
	if item.ChatIDs == nil {
		item.ChatIDs = []string{}
	}
	return item, nil
}

func (s *folderChatService) SetChatFolderChats(_ context.Context, userID, folderID string, chatIDs []string) (chatsvc.ChatFolder, error) {
	s.gotUserID = userID
	s.gotFolderID = folderID
	s.gotChats = chatIDs
	if s.folderErr != nil {
		return chatsvc.ChatFolder{}, s.folderErr
	}
	return chatsvc.ChatFolder{
		ID:       folderID,
		Name:     "Work",
		Position: 0,
		ChatIDs:  append([]string{}, chatIDs...),
	}, nil
}

func (s *folderChatService) DeleteChatFolder(_ context.Context, userID, folderID string) error {
	s.gotUserID = userID
	s.gotFolderID = folderID
	s.deleteCalls++
	return s.folderErr
}

func newFolderRouter(svc ChatService) stdhttp.Handler {
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		Chat:          svc,
	})
}

// folderRequest runs one authenticated request against the router.
func folderRequest(t *testing.T, router stdhttp.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader = strings.NewReader(body)
	var req *stdhttp.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, reader)
	}
	req.Header.Set("Authorization", "Bearer "+makeAccessToken(t, "u-test", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix()))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	payload := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &payload)
	return rr.Code, payload
}

func TestListChatFoldersRoute(t *testing.T) {
	svc := &folderChatService{folders: []chatsvc.ChatFolder{{
		ID: "90000000-0000-4000-8000-000000000001", Name: "Work", Icon: "💼",
		Position: 0, CreatedAt: time.Unix(1, 0).UTC(), ChatIDs: []string{},
	}}}
	status, payload := folderRequest(t, newFolderRouter(svc), stdhttp.MethodGet, "/api/private/v1/chat-folders", "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotUserID != "u-test" {
		t.Fatalf("user id = %q", svc.gotUserID)
	}
	if payload["message"] != "chat.folder.list.success" {
		t.Fatalf("message = %v", payload["message"])
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %v", payload["items"])
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %v", items[0])
	}
	if first["chat_ids"] == nil {
		t.Fatalf("chat_ids must serialise as a JSON array, got %v", first["chat_ids"])
	}
}

func TestCreateChatFolderRoute(t *testing.T) {
	svc := &folderChatService{}
	body := `{"name":"Work","icon":"💼","chat_ids":["11111111-1111-4111-8111-111111111111"]}`
	status, payload := folderRequest(t, newFolderRouter(svc), stdhttp.MethodPost, "/api/private/v1/chat-folders", body)
	if status != stdhttp.StatusCreated {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotCreate.Name != "Work" || svc.gotCreate.Icon != "💼" {
		t.Fatalf("input = %+v", svc.gotCreate)
	}
	if len(svc.gotCreate.ChatIDs) != 1 {
		t.Fatalf("chat ids = %v", svc.gotCreate.ChatIDs)
	}
	item, ok := payload["item"].(map[string]any)
	if !ok {
		t.Fatalf("item = %v", payload["item"])
	}
	if item["chat_ids"] == nil {
		t.Fatalf("chat_ids must not be null: %v", item["chat_ids"])
	}
}

func TestCreateChatFolderRouteRejectsBadJSON(t *testing.T) {
	status, payload := folderRequest(t, newFolderRouter(&folderChatService{}),
		stdhttp.MethodPost, "/api/private/v1/chat-folders", `{"name":`)
	if status != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
}

func TestUpdateChatFolderRouteMapsOptionalFields(t *testing.T) {
	svc := &folderChatService{}
	folderID := "90000000-0000-4000-8000-000000000001"
	status, payload := folderRequest(t, newFolderRouter(svc), stdhttp.MethodPatch,
		"/api/private/v1/chat-folder/"+folderID, `{"name":"Personal","position":2}`)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if !svc.gotUpdate.Name.Set || svc.gotUpdate.Name.Value == nil || *svc.gotUpdate.Name.Value != "Personal" {
		t.Fatalf("name = %+v", svc.gotUpdate.Name)
	}
	if svc.gotUpdate.Icon.Set {
		t.Fatalf("icon must stay unset when absent from the body")
	}
	if !svc.gotUpdate.Position.Set || svc.gotUpdate.Position.Value != 2 {
		t.Fatalf("position = %+v", svc.gotUpdate.Position)
	}
	if svc.gotUpdate.FolderID != folderID || svc.gotUpdate.UserID != "u-test" {
		t.Fatalf("input = %+v", svc.gotUpdate)
	}
}

func TestUpdateChatFolderRouteKeepsFieldWhenNull(t *testing.T) {
	svc := &folderChatService{}
	folderID := "90000000-0000-4000-8000-000000000001"
	status, _ := folderRequest(t, newFolderRouter(svc), stdhttp.MethodPatch,
		"/api/private/v1/chat-folder/"+folderID, `{"name":null,"icon":null}`)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if svc.gotUpdate.Name.Set || svc.gotUpdate.Icon.Set {
		t.Fatalf("a null field must be treated as absent: %+v", svc.gotUpdate)
	}
}

func TestSetChatFolderChatsRoute(t *testing.T) {
	svc := &folderChatService{}
	folderID := "90000000-0000-4000-8000-000000000001"
	chatID := "11111111-1111-4111-8111-111111111111"
	status, payload := folderRequest(t, newFolderRouter(svc), stdhttp.MethodPut,
		"/api/private/v1/chat-folder/"+folderID+"/chats", `{"chat_ids":["`+chatID+`"]}`)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotFolderID != folderID || svc.gotUserID != "u-test" {
		t.Fatalf("folder=%q user=%q", svc.gotFolderID, svc.gotUserID)
	}
	if len(svc.gotChats) != 1 || svc.gotChats[0] != chatID {
		t.Fatalf("chat ids = %v", svc.gotChats)
	}
}

func TestDeleteChatFolderRoute(t *testing.T) {
	svc := &folderChatService{}
	folderID := "90000000-0000-4000-8000-000000000001"
	status, payload := folderRequest(t, newFolderRouter(svc), stdhttp.MethodDelete,
		"/api/private/v1/chat-folder/"+folderID, "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.deleteCalls != 1 {
		t.Fatalf("delete calls = %d", svc.deleteCalls)
	}
}

func TestChatFolderErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		code string
		want int
	}{
		{"not found", chatsvc.CodeNotFound, stdhttp.StatusNotFound},
		{"already exists", chatsvc.CodeAlreadyExists, stdhttp.StatusConflict},
		{"limit", chatsvc.CodeInvalidArgument, stdhttp.StatusBadRequest},
		{"forbidden", chatsvc.CodeForbidden, stdhttp.StatusForbidden},
		{"internal", chatsvc.CodeInternal, stdhttp.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &chatsvc.Error{Code: tc.code, MessageKey: "error.chat.folder." + tc.name}
			svc := &folderChatService{folderErr: err}
			status, payload := folderRequest(t, newFolderRouter(svc), stdhttp.MethodGet, "/api/private/v1/chat-folders", "")
			if status != tc.want {
				t.Fatalf("status = %d, want %d; body=%v", status, tc.want, payload)
			}
			if payload["code"] != tc.code {
				t.Fatalf("code = %v, want %q", payload["code"], tc.code)
			}
		})
	}
}

func TestChatFolderRoutesRejectAnonymous(t *testing.T) {
	router := newFolderRouter(&folderChatService{})
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/chat-folders", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestChatFolderRoutesRejectWrongMethod(t *testing.T) {
	status, _ := folderRequest(t, newFolderRouter(&folderChatService{}),
		stdhttp.MethodDelete, "/api/private/v1/chat-folders", "")
	if status != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", status)
	}
}
