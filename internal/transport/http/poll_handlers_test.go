package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chatsvc "combox-backend/internal/service/chat"
)

// pollChatService lets the route tests assert what the handlers pass in
// and pick the responses (and errors) they return.
type pollChatService struct {
	stubChatService
	pollErr      error
	clearCount   int
	wallpaperOut chatsvc.Chat
	exportTitle  string
	gotChatID    string
	gotPollID    string
	gotUserID    string
	gotKind      string
	gotValue     string
}

func (s *pollChatService) CreatePoll(_ context.Context, input chatsvc.CreatePollInput) (chatsvc.Message, error) {
	s.gotChatID = input.ChatID
	poll := chatsvc.Poll{
		ID:          "poll-1",
		ChatID:      input.ChatID,
		MessageID:   "msg-1",
		Question:    input.Question,
		HideResults: input.HideResults,
		Options:     []chatsvc.PollOption{},
		MyOptionIDs: []string{},
		CreatedBy:   input.UserID,
	}
	return chatsvc.Message{ID: "msg-1", ChatID: input.ChatID, UserID: input.UserID, Content: input.Question, Poll: &poll}, nil
}

func (s *pollChatService) VotePoll(_ context.Context, input chatsvc.VotePollInput) (chatsvc.Poll, error) {
	s.gotPollID = input.PollID
	s.gotUserID = input.UserID
	if s.pollErr != nil {
		return chatsvc.Poll{}, s.pollErr
	}
	return chatsvc.Poll{ID: input.PollID, MyOptionIDs: input.OptionIDs}, nil
}

func (s *pollChatService) ClearChatHistory(_ context.Context, userID, chatID string) (int, error) {
	s.gotUserID = userID
	s.gotChatID = chatID
	return s.clearCount, nil
}

func (s *pollChatService) ExportChatHistory(_ context.Context, userID, chatID string) (chatsvc.ChatExport, error) {
	s.gotUserID = userID
	s.gotChatID = chatID
	title := s.exportTitle
	if title == "" {
		title = "General"
	}
	return chatsvc.ChatExport{
		Chat:         chatsvc.Chat{ID: chatID, Title: title, Type: "standard", Kind: "group"},
		MessageCount: 1,
		Messages:     []chatsvc.ChatExportMessage{{ID: "msg-1", SenderUserID: userID, Text: "hello"}},
	}, nil
}

func (s *pollChatService) SetChatWallpaper(_ context.Context, userID, chatID, kind, value string) (chatsvc.Chat, error) {
	s.gotUserID = userID
	s.gotChatID = chatID
	s.gotKind = kind
	s.gotValue = value
	item := s.wallpaperOut
	if item.ID == "" {
		item = chatsvc.Chat{ID: chatID, Title: "General", Type: "standard", Kind: "group"}
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = chatsvc.WallpaperKindNone
	}
	item.WallpaperKind = &kind
	if value != "" {
		item.WallpaperValue = &value
	} else {
		item.WallpaperValue = nil
	}
	return item, nil
}

func newPollTestRouter(t *testing.T, chat ChatService) stdhttp.Handler {
	t.Helper()
	return newCallHistoryRouter(t, chat, &stubCallHistoryService{})
}

func doAuthenticated(t *testing.T, router stdhttp.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+callHistoryToken(t, "u1"))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestPollRoutesRequireAuthorization(t *testing.T) {
	router := newPollTestRouter(t, &pollChatService{})
	for _, tc := range []struct{ method, path string }{
		{stdhttp.MethodGet, "/api/private/v1/polls/poll-1"},
		{stdhttp.MethodPost, "/api/private/v1/polls/poll-1/vote"},
		{stdhttp.MethodPost, "/api/private/v1/polls/poll-1/close"},
		{stdhttp.MethodPost, "/api/private/v1/chats/chat-1/polls"},
		{stdhttp.MethodPost, "/api/private/v1/chats/chat-1/clear"},
		{stdhttp.MethodGet, "/api/private/v1/chats/chat-1/export"},
		{stdhttp.MethodPut, "/api/private/v1/chats/chat-1/wallpaper"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401, got %d", tc.method, tc.path, rr.Code)
		}
	}
}

func TestCreatePollRouteReturnsPollMessageAndLimit(t *testing.T) {
	router := newPollTestRouter(t, &pollChatService{})
	rr := doAuthenticated(t, router, stdhttp.MethodPost, "/api/private/v1/chats/chat-1/polls",
		`{"question":"Best?","options":["A","B"],"hide_results":true}`)
	if rr.Code != stdhttp.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Poll       *chatsvc.Poll    `json:"poll"`
		Item       *chatsvc.Message `json:"item"`
		MaxOptions int              `json:"max_options"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Poll == nil || payload.Item == nil {
		t.Fatalf("expected poll and item in the response, got %s", rr.Body.String())
	}
	if payload.MaxOptions != chatsvc.PollMaxOptions {
		t.Fatalf("expected max_options=%d, got %d", chatsvc.PollMaxOptions, payload.MaxOptions)
	}
	if payload.Poll.Question != "Best?" || !payload.Poll.HideResults {
		t.Fatalf("unexpected poll payload: %+v", payload.Poll)
	}
	if payload.Item.Poll == nil {
		t.Fatalf("the message payload must carry the poll")
	}
}

func TestGetPollRouteReturnsViewerScopedPoll(t *testing.T) {
	router := newPollTestRouter(t, &pollChatService{})
	rr := doAuthenticated(t, router, stdhttp.MethodGet, "/api/private/v1/polls/poll-1", "")
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Poll       *chatsvc.Poll `json:"poll"`
		MaxOptions int           `json:"max_options"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Poll == nil || payload.MaxOptions != chatsvc.PollMaxOptions {
		t.Fatalf("unexpected payload: %s", rr.Body.String())
	}
}

func TestVotePollConflictMapsTo409(t *testing.T) {
	svc := &pollChatService{pollErr: &chatsvc.Error{Code: chatsvc.CodeConflict, MessageKey: "error.poll.already_voted"}}
	router := newPollTestRouter(t, svc)
	rr := doAuthenticated(t, router, stdhttp.MethodPost, "/api/private/v1/polls/poll-9/vote", `{"option_ids":["opt-1"]}`)
	if rr.Code != stdhttp.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Code != chatsvc.CodeConflict {
		t.Fatalf("expected code %q, got %q", chatsvc.CodeConflict, payload.Code)
	}
	if svc.gotPollID != "poll-9" || svc.gotUserID != "u1" {
		t.Fatalf("unexpected args: poll=%q user=%q", svc.gotPollID, svc.gotUserID)
	}
}

func TestVotePollInvalidBodyMapsTo400(t *testing.T) {
	router := newPollTestRouter(t, &pollChatService{})
	rr := doAuthenticated(t, router, stdhttp.MethodPost, "/api/private/v1/polls/poll-1/vote", `{"nope":1}`)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown field, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClosePollRoute(t *testing.T) {
	router := newPollTestRouter(t, &pollChatService{})
	rr := doAuthenticated(t, router, stdhttp.MethodPost, "/api/private/v1/polls/poll-1/close", "")
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Poll *chatsvc.Poll `json:"poll"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Poll == nil || !payload.Poll.Closed {
		t.Fatalf("expected a closed poll, got %s", rr.Body.String())
	}
}

func TestClearChatHistoryRouteReportsClearedCount(t *testing.T) {
	svc := &pollChatService{clearCount: 7}
	router := newPollTestRouter(t, svc)
	rr := doAuthenticated(t, router, stdhttp.MethodPost, "/api/private/v1/chats/chat-1/clear", "")
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Cleared != 7 {
		t.Fatalf("expected cleared=7, got %d", payload.Cleared)
	}
	if svc.gotChatID != "chat-1" || svc.gotUserID != "u1" {
		t.Fatalf("unexpected args: chat=%q user=%q", svc.gotChatID, svc.gotUserID)
	}

	wrong := doAuthenticated(t, router, stdhttp.MethodGet, "/api/private/v1/chats/chat-1/clear", "")
	if wrong.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", wrong.Code)
	}
}

func TestExportChatHistoryRouteSendsAttachmentHeaders(t *testing.T) {
	router := newPollTestRouter(t, &pollChatService{exportTitle: "Weekend Trip"})
	rr := doAuthenticated(t, router, stdhttp.MethodGet, "/api/private/v1/chats/chat-1/export", "")
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if disposition := rr.Header().Get("Content-Disposition"); !strings.Contains(disposition, `attachment; filename="chat-export-weekend-trip.json"`) {
		t.Fatalf("unexpected content disposition: %q", disposition)
	}
	var payload chatsvc.ChatExport
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.MessageCount != 1 || len(payload.Messages) != 1 || payload.Messages[0].Text != "hello" {
		t.Fatalf("unexpected export payload: %+v", payload)
	}
	if payload.Chat.Title != "Weekend Trip" {
		t.Fatalf("expected chat meta in the export, got %+v", payload.Chat)
	}
}

func TestChatWallpaperRoute(t *testing.T) {
	svc := &pollChatService{}
	router := newPollTestRouter(t, svc)

	rr := doAuthenticated(t, router, stdhttp.MethodPut, "/api/private/v1/chats/chat-1/wallpaper", `{"kind":"preset","value":"grid-dark"}`)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.gotKind != "preset" || svc.gotValue != "grid-dark" {
		t.Fatalf("unexpected args: kind=%q value=%q", svc.gotKind, svc.gotValue)
	}
	var payload struct {
		Chat *chatsvc.Chat `json:"chat"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Chat == nil || payload.Chat.WallpaperKind == nil || *payload.Chat.WallpaperKind != "preset" {
		t.Fatalf("expected the updated chat, got %s", rr.Body.String())
	}

	cleared := doAuthenticated(t, router, stdhttp.MethodDelete, "/api/private/v1/chats/chat-1/wallpaper", "")
	if cleared.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", cleared.Code, cleared.Body.String())
	}
	if svc.gotKind != chatsvc.WallpaperKindNone || svc.gotValue != "" {
		t.Fatalf("delete must clear the wallpaper, got kind=%q value=%q", svc.gotKind, svc.gotValue)
	}

	unsupported := doAuthenticated(t, router, stdhttp.MethodPatch, "/api/private/v1/chats/chat-1/wallpaper", `{"kind":"preset"}`)
	if unsupported.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", unsupported.Code)
	}
}
