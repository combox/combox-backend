package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type stubForwardPrivacy struct {
	allowed    map[string]bool
	err        error
	memoCalls  int
	evalCalls  int
	lastViewer string
	lastTarget string
	lastParam  string
}

func (s *stubForwardPrivacy) WithMemo(ctx context.Context) context.Context {
	s.memoCalls++
	return ctx
}

func (s *stubForwardPrivacy) Evaluate(_ context.Context, viewerID, targetID, param string) (bool, error) {
	s.evalCalls++
	s.lastViewer = viewerID
	s.lastTarget = targetID
	s.lastParam = param
	if s.err != nil {
		return false, s.err
	}
	return s.allowed[targetID], nil
}

type stubForwardOriginProfiles struct {
	avatar string
	err    error
	calls  int
}

func (s *stubForwardOriginProfiles) GetAvatarDataURL(_ context.Context, userID string) (*string, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	avatar := s.avatar + userID
	return &avatar, nil
}

func originMessage(userID, name string) Message {
	return Message{
		ID:                  "m1",
		ChatID:              "c1",
		UserID:              "forwarder",
		Content:             "hello",
		ForwardOriginUserID: &userID,
		ForwardOriginName:   &name,
	}
}

func TestForwardPrivacyAllowedKeepsOriginAndAddsAvatar(t *testing.T) {
	privacy := &stubForwardPrivacy{allowed: map[string]bool{"owner-1": true}}
	profiles := &stubForwardOriginProfiles{avatar: "data:"}
	svc := &Service{}
	svc.SetForwardPrivacy(privacy)
	svc.SetForwardOriginProfiles(profiles)

	msg := originMessage("owner-1", "Alice")
	svc.applyForwardPrivacyToMessage(context.Background(), "viewer-1", &msg)

	if msg.ForwardOriginUserID == nil || *msg.ForwardOriginUserID != "owner-1" {
		t.Fatalf("forward_origin_user_id = %#v", msg.ForwardOriginUserID)
	}
	if msg.ForwardOriginName == nil || *msg.ForwardOriginName != "Alice" {
		t.Fatalf("forward_origin_name = %#v", msg.ForwardOriginName)
	}
	if msg.ForwardOriginRedacted {
		t.Fatalf("forward_origin_redacted = true, want false")
	}
	if msg.ForwardOriginAvatarURL == nil || *msg.ForwardOriginAvatarURL != "data:owner-1" {
		t.Fatalf("forward_origin_avatar_data_url = %#v", msg.ForwardOriginAvatarURL)
	}
	if privacy.lastParam != "forwarded_messages" {
		t.Fatalf("evaluated param = %q, want forwarded_messages", privacy.lastParam)
	}
	if privacy.lastViewer != "viewer-1" || privacy.lastTarget != "owner-1" {
		t.Fatalf("evaluated viewer/target = %q/%q", privacy.lastViewer, privacy.lastTarget)
	}
}

func TestForwardPrivacyDeniedRedactsOrigin(t *testing.T) {
	privacy := &stubForwardPrivacy{allowed: map[string]bool{"owner-1": false}}
	profiles := &stubForwardOriginProfiles{avatar: "data:"}
	svc := &Service{}
	svc.SetForwardPrivacy(privacy)
	svc.SetForwardOriginProfiles(profiles)

	msg := originMessage("owner-1", "Alice")
	svc.applyForwardPrivacyToMessage(context.Background(), "viewer-1", &msg)

	if msg.ForwardOriginName == nil || *msg.ForwardOriginName != "" {
		t.Fatalf("forward_origin_name = %#v, want empty", msg.ForwardOriginName)
	}
	if msg.ForwardOriginUserID != nil {
		t.Fatalf("forward_origin_user_id = %#v, want nil", msg.ForwardOriginUserID)
	}
	if msg.ForwardOriginAvatarURL != nil {
		t.Fatalf("forward_origin_avatar_data_url = %#v, want nil", msg.ForwardOriginAvatarURL)
	}
	if !msg.ForwardOriginRedacted {
		t.Fatalf("forward_origin_redacted = false, want true")
	}
	if profiles.calls != 0 {
		t.Fatalf("avatar resolved %d times for a denied origin", profiles.calls)
	}
}

func TestForwardPrivacyFailsClosedOnError(t *testing.T) {
	privacy := &stubForwardPrivacy{err: errors.New("db down")}
	profiles := &stubForwardOriginProfiles{avatar: "data:"}
	svc := &Service{}
	svc.SetForwardPrivacy(privacy)
	svc.SetForwardOriginProfiles(profiles)

	msg := originMessage("owner-1", "Alice")
	svc.applyForwardPrivacyToMessage(context.Background(), "viewer-1", &msg)

	if !msg.ForwardOriginRedacted || msg.ForwardOriginUserID != nil || msg.ForwardOriginName == nil || *msg.ForwardOriginName != "" {
		t.Fatalf("message not fail-closed: %#v", msg)
	}
}

func TestForwardPrivacyWithoutServiceLeavesMessageUntouched(t *testing.T) {
	svc := &Service{}

	msg := originMessage("owner-1", "Alice")
	svc.applyForwardPrivacyToMessage(context.Background(), "viewer-1", &msg)

	if msg.ForwardOriginUserID == nil || *msg.ForwardOriginUserID != "owner-1" {
		t.Fatalf("forward_origin_user_id = %#v", msg.ForwardOriginUserID)
	}
	if msg.ForwardOriginName == nil || *msg.ForwardOriginName != "Alice" {
		t.Fatalf("forward_origin_name = %#v", msg.ForwardOriginName)
	}
	if msg.ForwardOriginRedacted {
		t.Fatalf("forward_origin_redacted = true, want false")
	}
}

func TestForwardPrivacyIgnoresMessagesWithoutOriginOrViewer(t *testing.T) {
	privacy := &stubForwardPrivacy{allowed: map[string]bool{}}
	svc := &Service{}
	svc.SetForwardPrivacy(privacy)

	msg := Message{ID: "m1", Content: "hi"}
	svc.applyForwardPrivacyToMessage(context.Background(), "viewer-1", &msg)
	if privacy.evalCalls != 0 {
		t.Fatalf("Evaluate called %d times for a message without origin", privacy.evalCalls)
	}

	msg = originMessage("owner-1", "Alice")
	privacy.memoCalls = 0
	svc.applyForwardPrivacyToMessage(context.Background(), "  ", &msg)
	if privacy.evalCalls != 0 || privacy.memoCalls != 0 {
		t.Fatalf("empty viewer still evaluated: evals=%d memos=%d", privacy.evalCalls, privacy.memoCalls)
	}
	if msg.ForwardOriginRedacted {
		t.Fatalf("message was redacted without a viewer")
	}
}

func TestForwardPrivacyBatchInstallsOneMemo(t *testing.T) {
	privacy := &stubForwardPrivacy{allowed: map[string]bool{"owner-1": true}}
	svc := &Service{}
	svc.SetForwardPrivacy(privacy)

	page := []Message{}
	for i := 0; i < 5; i++ {
		page = append(page, originMessage("owner-1", "Alice"))
	}
	svc.applyForwardPrivacyToMessages(context.Background(), "viewer-1", page)

	if privacy.memoCalls != 1 {
		t.Fatalf("WithMemo calls = %d, want 1", privacy.memoCalls)
	}
	if privacy.evalCalls != 5 {
		t.Fatalf("Evaluate calls = %d, want 5", privacy.evalCalls)
	}
	for i := range page {
		if page[i].ForwardOriginRedacted {
			t.Fatalf("page[%d] redacted", i)
		}
	}
}

func TestForwardPrivacyJSONFieldNames(t *testing.T) {
	privacy := &stubForwardPrivacy{allowed: map[string]bool{"owner-1": false}}
	svc := &Service{}
	svc.SetForwardPrivacy(privacy)

	msg := originMessage("owner-1", "Alice")
	svc.applyForwardPrivacyToMessage(context.Background(), "viewer-1", &msg)

	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"forward_origin_redacted":true`, `"forward_origin_name":""`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("payload %s missing %s", payload, want)
		}
	}
	for _, forbidden := range []string{`"forward_origin_user_id"`, `"forward_origin_avatar_data_url"`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("payload %s must not contain %s", payload, forbidden)
		}
	}

	allowed := &stubForwardPrivacy{allowed: map[string]bool{"owner-1": true}}
	profiles := &stubForwardOriginProfiles{avatar: "data:"}
	allowedSvc := &Service{}
	allowedSvc.SetForwardPrivacy(allowed)
	allowedSvc.SetForwardOriginProfiles(profiles)

	visible := originMessage("owner-1", "Alice")
	allowedSvc.applyForwardPrivacyToMessage(context.Background(), "viewer-1", &visible)
	payload, err = json.Marshal(visible)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"forward_origin_avatar_data_url":"data:owner-1"`) {
		t.Fatalf("payload %s missing forward_origin_avatar_data_url", payload)
	}
	if strings.Contains(string(payload), `"forward_origin_redacted":true`) {
		t.Fatalf("payload %s must not be redacted", payload)
	}
}
