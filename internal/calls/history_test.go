package calls

import (
	"context"
	"errors"
	"testing"
	"time"
)

type historyStoreStub struct {
	records   []CallRecord
	parts     map[string][]string
	lastLimit int
}

func (s *historyStoreStub) CreateCall(context.Context, CallRecord) error { return nil }

func (s *historyStoreStub) GetCall(context.Context, string) (CallRecord, error) {
	return CallRecord{}, ErrCallNotFound
}

func (s *historyStoreStub) GetActiveCallByChat(context.Context, string) (CallRecord, error) {
	return CallRecord{}, ErrCallNotFound
}

func (s *historyStoreStub) EndCall(context.Context, string, time.Time, string) error { return nil }

func (s *historyStoreStub) ListRecentCalls(_ context.Context, _ string, limit int) ([]CallRecord, error) {
	s.lastLimit = limit
	if limit > 0 && limit < len(s.records) {
		return s.records[:limit], nil
	}
	return s.records, nil
}

func (s *historyStoreStub) AddParticipant(context.Context, ParticipantRecord) error { return nil }

func (s *historyStoreStub) CloseParticipant(context.Context, string, string, string, time.Time, string) error {
	return nil
}

func (s *historyStoreStub) ListParticipants(_ context.Context, callID string) ([]ParticipantRecord, error) {
	out := make([]ParticipantRecord, 0, len(s.parts[callID]))
	for _, userID := range s.parts[callID] {
		out = append(out, ParticipantRecord{CallID: callID, UserID: userID})
	}
	return out, nil
}

type membersStub struct{ allow bool }

func (m membersStub) IsMember(context.Context, string, string) (bool, error) { return m.allow, nil }

func timePtr(value time.Time) *time.Time { return &value }

func TestListChatCallsProjectsDirectionMissedAndDuration(t *testing.T) {
	started := time.Date(2026, 9, 12, 12, 3, 0, 0, time.UTC)
	ended := started.Add(75 * time.Second)
	store := &historyStoreStub{
		records: []CallRecord{
			{ID: "call-1", ChatID: "chat-1", Kind: KindP2P, StartedBy: "u1", StartedAt: started, EndedAt: &ended},
			{ID: "call-2", ChatID: "chat-1", Kind: KindP2P, StartedBy: "u2", StartedAt: started.Add(-time.Hour), EndedAt: timePtr(started.Add(-time.Hour).Add(2 * time.Second))},
			{ID: "call-3", ChatID: "chat-1", Kind: KindGroup, StartedBy: "u1", StartedAt: started.Add(-2 * time.Hour)},
		},
		parts: map[string][]string{
			"call-1": {"u1", "u2", "u2"},
			"call-2": {"u2"},
			"call-3": {"u1"},
		},
	}
	svc := &Service{store: store, members: membersStub{allow: true}}

	items, err := svc.ListChatCalls(context.Background(), "u1", "chat-1", 50)
	if err != nil {
		t.Fatalf("ListChatCalls: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(items))
	}

	outgoing := items[0]
	if outgoing.Direction != DirectionOutgoing || outgoing.Missed || outgoing.DurationSeconds != 75 {
		t.Fatalf("unexpected answered outgoing call: %+v", outgoing)
	}
	if len(outgoing.Participants) != 2 {
		t.Fatalf("participants must be deduplicated, got %+v", outgoing.Participants)
	}

	missed := items[1]
	if missed.Direction != DirectionIncoming || !missed.Missed || missed.DurationSeconds != 2 {
		t.Fatalf("unexpected missed call: %+v", missed)
	}

	open := items[2]
	if open.Missed || open.DurationSeconds != 0 {
		t.Fatalf("an unfinished call is not missed and has no duration: %+v", open)
	}
}

func TestListChatCallsRejectsNonMember(t *testing.T) {
	svc := &Service{store: &historyStoreStub{}, members: membersStub{allow: false}}

	_, err := svc.ListChatCalls(context.Background(), "u1", "chat-1", 50)
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got %v", err)
	}
}

func TestListChatCallsClampsLimit(t *testing.T) {
	store := &historyStoreStub{records: []CallRecord{
		{ID: "call-1", ChatID: "chat-1", Kind: KindP2P, StartedBy: "u1", StartedAt: time.Now().UTC()},
	}}
	svc := &Service{store: store, members: membersStub{allow: true}}

	if _, err := svc.ListChatCalls(context.Background(), "u1", "chat-1", 1000); err != nil {
		t.Fatalf("ListChatCalls: %v", err)
	}
	if store.lastLimit != 200 {
		t.Fatalf("expected limit clamped to 200, got %d", store.lastLimit)
	}
	if _, err := svc.ListChatCalls(context.Background(), "u1", "chat-1", -5); err != nil {
		t.Fatalf("ListChatCalls: %v", err)
	}
	if store.lastLimit != 50 {
		t.Fatalf("expected default limit 50, got %d", store.lastLimit)
	}
	if _, err := svc.ListChatCalls(context.Background(), "u1", "", 10); !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember for an empty chat id, got %v", err)
	}
}

type deleteStoreStub struct {
	historyStoreStub
	deleted bool
	chatID  string
	callID  string
}

func (s *deleteStoreStub) DeleteCall(_ context.Context, chatID, callID string) (bool, error) {
	s.chatID, s.callID = chatID, callID
	return s.deleted, nil
}

func TestDeleteChatCallRemovesHistoryRow(t *testing.T) {
	store := &deleteStoreStub{deleted: true}
	svc := &Service{store: store, members: membersStub{allow: true}}

	if err := svc.DeleteChatCall(context.Background(), "u1", "chat-1", "call-9"); err != nil {
		t.Fatalf("DeleteChatCall: %v", err)
	}
	if store.chatID != "chat-1" || store.callID != "call-9" {
		t.Fatalf("store got chat=%q call=%q", store.chatID, store.callID)
	}
}

func TestDeleteChatCallReportsMissingRow(t *testing.T) {
	store := &deleteStoreStub{deleted: false}
	svc := &Service{store: store, members: membersStub{allow: true}}

	if err := svc.DeleteChatCall(context.Background(), "u1", "chat-1", "call-9"); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("expected ErrCallNotFound, got %v", err)
	}
}

func TestDeleteChatCallRejectsNonMember(t *testing.T) {
	svc := &Service{store: &deleteStoreStub{deleted: true}, members: membersStub{allow: false}}

	if err := svc.DeleteChatCall(context.Background(), "u1", "chat-1", "call-9"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got %v", err)
	}
}

func TestDeleteChatCallWithoutStoreSupport(t *testing.T) {
	svc := &Service{store: &historyStoreStub{}, members: membersStub{allow: true}}

	if err := svc.DeleteChatCall(context.Background(), "u1", "chat-1", "call-9"); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("expected ErrCallNotFound, got %v", err)
	}
}
