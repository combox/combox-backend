package calls

import (
	"context"
	"time"
)

// Store persists call sessions and their participants. The production
// implementation lives in internal/repository/postgres.
type Store interface {
	CreateCall(ctx context.Context, call CallRecord) error
	GetCall(ctx context.Context, callID string) (CallRecord, error)
	GetActiveCallByChat(ctx context.Context, chatID string) (CallRecord, error)
	EndCall(ctx context.Context, callID string, endedAt time.Time, reason string) error
	ListRecentCalls(ctx context.Context, chatID string, limit int) ([]CallRecord, error)

	AddParticipant(ctx context.Context, rec ParticipantRecord) error
	CloseParticipant(ctx context.Context, callID, userID, deviceID string, leftAt time.Time, reason string) error
	ListParticipants(ctx context.Context, callID string) ([]ParticipantRecord, error)
}

// noopStore is used when persistence is unavailable so calls keep working in
// memory only (mirrors the "degraded but usable" behaviour of the realtime layer).
type noopStore struct{}

func (noopStore) CreateCall(context.Context, CallRecord) error { return nil }
func (noopStore) GetCall(context.Context, string) (CallRecord, error) {
	return CallRecord{}, ErrCallNotFound
}
func (noopStore) GetActiveCallByChat(context.Context, string) (CallRecord, error) {
	return CallRecord{}, ErrCallNotFound
}
func (noopStore) EndCall(context.Context, string, time.Time, string) error           { return nil }
func (noopStore) ListRecentCalls(context.Context, string, int) ([]CallRecord, error) { return nil, nil }
func (noopStore) AddParticipant(context.Context, ParticipantRecord) error            { return nil }
func (noopStore) CloseParticipant(context.Context, string, string, string, time.Time, string) error {
	return nil
}
func (noopStore) ListParticipants(context.Context, string) ([]ParticipantRecord, error) {
	return nil, nil
}
