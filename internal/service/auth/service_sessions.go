package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ActiveSession is one row of the "Active sessions" list shown in the
// settings. Current marks the session the request itself came from.
type ActiveSession struct {
	ID        string    `json:"id"`
	UserAgent string    `json:"user_agent"`
	IPAddress string    `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

// ListSessions returns the still-valid sessions of a user, newest first.
// currentSessionID is the session embedded in the caller's access token; it may
// be empty when the token predates the sid claim.
func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID string) ([]ActiveSession, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, &Error{Code: CodeInvalidArgument, MessageKey: "error.auth.invalid_input"}
	}

	sessions, err := s.sessions.ListByUserID(ctx, userID)
	if err != nil {
		return nil, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	now := s.nowFn().UTC()
	out := make([]ActiveSession, 0, len(sessions))
	for _, session := range sessions {
		expiresAt := session.ExpiresAt.UTC()
		if !expiresAt.After(now) {
			continue
		}
		out = append(out, ActiveSession{
			ID:        session.ID,
			UserAgent: session.UserAgent,
			IPAddress: session.IPAddress,
			CreatedAt: session.CreatedAt.UTC(),
			ExpiresAt: expiresAt,
			Current:   session.ID == strings.TrimSpace(currentSessionID),
		})
	}
	if out == nil {
		out = []ActiveSession{}
	}
	return out, nil
}

// RevokeSession deletes one session of the caller. A session of a different
// user or a malformed id reads as not_found: the caller must not be able to
// probe other people's session ids.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	if userID == "" || sessionID == "" {
		return &Error{Code: CodeInvalidArgument, MessageKey: "error.auth.invalid_input"}
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return &Error{Code: CodeNotFound, MessageKey: "error.auth.session_not_found"}
	}
	if err := s.sessions.DeleteByUserIDAndID(ctx, userID, sessionID); err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return &Error{Code: CodeNotFound, MessageKey: "error.auth.session_not_found", Cause: err}
		}
		return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	return nil
}

// RevokeOtherSessions deletes every session of a user except keepSessionID.
// An empty keepSessionID revokes them all (including the caller's own).
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keepSessionID string) (int64, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return 0, &Error{Code: CodeInvalidArgument, MessageKey: "error.auth.invalid_input"}
	}
	keepSessionID = strings.TrimSpace(keepSessionID)
	if keepSessionID != "" {
		if _, err := uuid.Parse(keepSessionID); err != nil {
			return 0, &Error{Code: CodeNotFound, MessageKey: "error.auth.session_not_found"}
		}
	}
	revoked, err := s.sessions.DeleteOthersByUserID(ctx, userID, keepSessionID)
	if err != nil {
		return 0, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	return revoked, nil
}
