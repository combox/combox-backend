package calls

import (
	"context"
	"strings"
	"time"
)

// CallHistoryEntry is a persisted call together with the user ids that ever
// took part in it (join order, duplicates from rejoins possible).
type CallHistoryEntry struct {
	Call         CallRecord
	Participants []string
}

// HistoryStore is the bulk history query the postgres repository implements.
// Stores without it fall back to per-call participant lookups.
type HistoryStore interface {
	ListRecentCallsWithParticipants(ctx context.Context, chatID string, limit int) ([]CallHistoryEntry, error)
}

// CallDeleter removes a finished call (participants go with it via cascade)
// so it disappears from the chat feed.
type CallDeleter interface {
	DeleteCall(ctx context.Context, chatID, callID string) (bool, error)
}

// Call direction as seen by the viewer of a chat.
const (
	DirectionOutgoing = "outgoing"
	DirectionIncoming = "incoming"
)

// ChatCall is the chat feed projection of a past call.
type ChatCall struct {
	ID              string     `json:"id"`
	ChatID          string     `json:"chat_id"`
	Kind            CallKind   `json:"kind"`
	StartedBy       string     `json:"started_by"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	DurationSeconds int64      `json:"duration_seconds"`
	EndReason       *string    `json:"end_reason,omitempty"`
	Direction       string     `json:"direction"`
	Missed          bool       `json:"missed"`
	Participants    []string   `json:"participants"`
}

// ListChatCalls returns the most recent calls of a chat, newest first, for a
// viewer allowed to see that chat. The caller authorizes the request; the
// member check below is defence in depth for future call sites.
func (s *Service) ListChatCalls(ctx context.Context, userID, chatID string, limit int) ([]ChatCall, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return nil, ErrNotMember
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if s.members != nil {
		member, err := s.members.IsMember(ctx, userID, chatID)
		if err != nil {
			return nil, err
		}
		if !member {
			return nil, ErrNotMember
		}
	}

	entries, err := s.callHistory(ctx, chatID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ChatCall, 0, len(entries))
	for _, entry := range entries {
		out = append(out, projectChatCall(entry, userID))
	}
	return out, nil
}

// DeleteChatCall removes one call-history row of a chat for a viewer allowed
// to see that chat, so the feed no longer shows the entry.
func (s *Service) DeleteChatCall(ctx context.Context, userID, chatID, callID string) error {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	callID = strings.TrimSpace(callID)
	if userID == "" || chatID == "" || callID == "" {
		return ErrCallNotFound
	}
	if s.members != nil {
		member, err := s.members.IsMember(ctx, userID, chatID)
		if err != nil {
			return err
		}
		if !member {
			return ErrNotMember
		}
	}
	deleter, ok := s.store.(CallDeleter)
	if !ok {
		return ErrCallNotFound
	}
	deleted, err := deleter.DeleteCall(ctx, chatID, callID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrCallNotFound
	}
	return nil
}

func (s *Service) callHistory(ctx context.Context, chatID string, limit int) ([]CallHistoryEntry, error) {
	if history, ok := s.store.(HistoryStore); ok {
		return history.ListRecentCallsWithParticipants(ctx, chatID, limit)
	}
	if s.store == nil {
		return nil, nil
	}
	records, err := s.store.ListRecentCalls(ctx, chatID, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]CallHistoryEntry, 0, len(records))
	for _, record := range records {
		parts, err := s.store.ListParticipants(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(parts))
		for _, part := range parts {
			ids = append(ids, part.UserID)
		}
		entries = append(entries, CallHistoryEntry{Call: record, Participants: ids})
	}
	return entries, nil
}

func projectChatCall(entry CallHistoryEntry, viewerID string) ChatCall {
	call := entry.Call
	participants := dedupeIDs(entry.Participants)

	direction := DirectionIncoming
	if call.StartedBy == viewerID {
		direction = DirectionOutgoing
	}

	var duration int64
	if call.EndedAt != nil {
		if seconds := call.EndedAt.Sub(call.StartedAt).Seconds(); seconds > 0 {
			duration = int64(seconds)
		}
	}

	// Missed: the call already ended and nobody but a single person was ever
	// present; multiparty calls with more than one participant never count.
	missed := call.EndedAt != nil && len(participants) <= 1

	return ChatCall{
		ID:              call.ID,
		ChatID:          call.ChatID,
		Kind:            call.Kind,
		StartedBy:       call.StartedBy,
		StartedAt:       call.StartedAt,
		EndedAt:         call.EndedAt,
		DurationSeconds: duration,
		EndReason:       call.EndReason,
		Direction:       direction,
		Missed:          missed,
		Participants:    participants,
	}
}

func dedupeIDs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		id := strings.TrimSpace(item)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
