// Package reports implements the minimal POST /api/private/v1/reports
// endpoint behind the Report buttons in the UI.
//
// Contract: {target_type: user|chat|photo|message, target_id, reason} -> 201
// with the stored report. No idempotency (task). Anti-spam is an in-memory
// per-reporter sliding window (10/min), the same pattern as the translate
// service userLimiter: the codebase has no global rate-limit middleware
// (only per-endpoint caps like search limit<=100), so each service enforces
// its own. Valkey is deliberately not used so reports never depend on cache
// health.
package reports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeRateLimited     = "rate_limited"
	CodeInternal        = "internal"

	// DefaultRateLimitPerMin is the per-reporter budget.
	DefaultRateLimitPerMin = 10
	// MaxReasonRunes caps the reason text (matches the DB CHECK 1..2000).
	MaxReasonRunes = 2000
	// MaxTargetIDLen caps target_id (matches the DB CHECK 1..256 chars).
	MaxTargetIDLen = 256
)

// Error is the typed service error; the HTTP layer maps CodeInvalidArgument
// onto 400, CodeRateLimited onto 429 and everything else onto 500.
type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return "reports: " + e.Code
	}
	return fmt.Sprintf("reports: %s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

func invalidArg(key string, cause error, details map[string]string) *Error {
	return &Error{Code: CodeInvalidArgument, MessageKey: key, Details: details, Cause: cause}
}

func rateLimited(retryAfter time.Duration) *Error {
	return &Error{
		Code:       CodeRateLimited,
		MessageKey: "error.reports.rate_limited",
		Details:    map[string]string{"retry_after_sec": fmt.Sprintf("%d", int(retryAfter/time.Second))},
		RetryAfter: retryAfter,
	}
}

func internalErr(cause error) *Error {
	return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: cause}
}

// Entry is one stored report row.
type Entry struct {
	ID         string    `json:"id"`
	ReporterID string    `json:"reporter_id"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// Repository persists report rows (postgres/reports_repository.go).
type Repository interface {
	Create(ctx context.Context, reporterID, targetType, targetID, reason string) (Entry, error)
}

// userLimiter is the translate-service sliding-window limiter, copied:
// limit requests per minute per reporter, in memory, no Valkey dependency.
type userLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newUserLimiter(limit int) *userLimiter {
	if limit <= 0 {
		limit = DefaultRateLimitPerMin
	}
	return &userLimiter{hits: make(map[string][]time.Time), limit: limit, window: time.Minute}
}

func (l *userLimiter) allow(userID string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	kept := l.hits[userID][:0]
	for _, at := range l.hits[userID] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.limit {
		return false, kept[0].Add(l.window).Sub(now)
	}
	l.hits[userID] = append(kept, now)
	return true, 0
}

// Service serves the reports API.
type Service struct {
	repo    Repository
	limiter *userLimiter
	nowFn   func() time.Time
}

// New builds the service.
func New(repo Repository) (*Service, error) {
	return NewWithLimit(repo, DefaultRateLimitPerMin)
}

// NewWithLimit builds the service with a custom per-minute budget (tests).
func NewWithLimit(repo Repository, limitPerMin int) (*Service, error) {
	if repo == nil {
		return nil, errors.New("reports repository is required")
	}
	return &Service{repo: repo, limiter: newUserLimiter(limitPerMin), nowFn: time.Now}, nil
}

func validTargetType(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "user", "chat", "photo", "message":
		return true
	default:
		return false
	}
}

// Create stores one report. targetType must be user|chat|photo|message,
// targetID 1..256 chars, reason 1..2000 runes (trimmed).
func (s *Service) Create(ctx context.Context, reporterID, targetType, targetID, reason string) (Entry, error) {
	reporter := strings.TrimSpace(reporterID)
	if reporter == "" {
		return Entry{}, invalidArg("error.reports.invalid_input", errors.New("invalid reporter id"), map[string]string{"reporter_id": reporterID})
	}
	if _, err := uuid.Parse(reporter); err != nil {
		return Entry{}, invalidArg("error.reports.invalid_input", errors.New("invalid reporter id"), map[string]string{"reporter_id": reporterID})
	}
	tt := strings.ToLower(strings.TrimSpace(targetType))
	if !validTargetType(tt) {
		return Entry{}, invalidArg("error.reports.invalid_input", errors.New("invalid target_type"), map[string]string{"target_type": targetType})
	}
	tid := strings.TrimSpace(targetID)
	if tid == "" || len(tid) > MaxTargetIDLen {
		return Entry{}, invalidArg("error.reports.invalid_input", errors.New("invalid target_id"), map[string]string{"target_id": targetID})
	}
	rsn := strings.TrimSpace(reason)
	if rsn == "" || len([]rune(rsn)) > MaxReasonRunes {
		return Entry{}, invalidArg("error.reports.invalid_input", errors.New("invalid reason"), map[string]string{"reason": "1..2000 chars"})
	}

	now := time.Now().UTC()
	if s.nowFn != nil {
		now = s.nowFn().UTC()
	}
	if ok, retryAfter := s.limiter.allow(reporter, now); !ok {
		return Entry{}, rateLimited(retryAfter)
	}

	entry, err := s.repo.Create(ctx, reporter, tt, tid, rsn)
	if err != nil {
		return Entry{}, internalErr(err)
	}
	return entry, nil
}
