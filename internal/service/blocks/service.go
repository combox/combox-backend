// Package blocks implements the "Blocked users" list of the Telegram-style
// Privacy screen: one directed (owner -> blocked) edge per row. Storage and
// list API only; no message/call filtering is enforced here (that is a later
// step, see the blocked_users table comment in migration 000046).
package blocks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
)

// Sentinel validation errors, unwrappable through Error.Cause.
var (
	// ErrInvalidUser is returned for an empty owner id.
	ErrInvalidUser = errors.New("invalid user id")
	// ErrInvalidTarget is returned for an empty or malformed blocked id.
	ErrInvalidTarget = errors.New("invalid blocked user id")
	// ErrSelfBlock is returned when a user tries to block themselves.
	ErrSelfBlock = errors.New("cannot block yourself")
	// ErrTargetNotFound is returned when the blocked id is a well-formed
	// UUID but matches no user (foreign-key violation on insert).
	ErrTargetNotFound = errors.New("blocked user not found")
	// ErrNotBlocked is returned when unblocking a user that is not blocked.
	ErrNotBlocked = errors.New("user is not blocked")
)

// Error is the typed error of the blocks service. Handlers map
// CodeInvalidArgument onto HTTP 400, CodeNotFound onto HTTP 404 and
// everything else onto HTTP 500.
type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func invalidArg(key string, cause error, details map[string]string) *Error {
	return &Error{Code: CodeInvalidArgument, MessageKey: key, Details: details, Cause: cause}
}

func notFound(key string, cause error, details map[string]string) *Error {
	return &Error{Code: CodeNotFound, MessageKey: key, Details: details, Cause: cause}
}

func internalErr(cause error) *Error {
	return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: cause}
}

// Entry is one row of the owner's block list.
type Entry struct {
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Repository is the persistence of the blocked_users table.
type Repository interface {
	// List returns every user blocked by owner, oldest first.
	List(ctx context.Context, ownerID string) ([]Entry, error)
	// Block inserts the edge; it reports ErrTargetNotFound when blockedID
	// matches no user. Re-blocking is a no-op that returns the entry.
	Block(ctx context.Context, ownerID, blockedID string) (Entry, error)
	// Unblock deletes the edge; it reports ErrNotBlocked when there is none.
	Unblock(ctx context.Context, ownerID, blockedID string) error
}

// Service serves the blocked-users list API.
type Service struct {
	repo Repository
}

// New builds the service.
func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("blocked users repository is required")
	}
	return &Service{repo: repo}, nil
}

func cleanIDs(ownerID, targetID string) (string, string) {
	return strings.TrimSpace(ownerID), strings.TrimSpace(targetID)
}

func checkPair(ownerID, targetID string) (string, string, error) {
	owner, target := cleanIDs(ownerID, targetID)
	if owner == "" {
		return "", "", invalidArg("error.blocked.invalid_input", ErrInvalidUser, map[string]string{"user_id": ownerID})
	}
	if _, err := uuid.Parse(owner); err != nil {
		return "", "", invalidArg("error.blocked.invalid_input", ErrInvalidUser, map[string]string{"user_id": ownerID})
	}
	if target == "" {
		return "", "", invalidArg("error.blocked.invalid_input", ErrInvalidTarget, map[string]string{"user_id": targetID})
	}
	if _, err := uuid.Parse(target); err != nil {
		return "", "", invalidArg("error.blocked.invalid_input", ErrInvalidTarget, map[string]string{"user_id": targetID})
	}
	if owner == target {
		return "", "", invalidArg("error.blocked.self_block", ErrSelfBlock, map[string]string{"user_id": target})
	}
	return owner, target, nil
}

// List returns the owner's block list (never nil, so the JSON shape is []).
func (s *Service) List(ctx context.Context, ownerID string) ([]Entry, error) {
	owner := strings.TrimSpace(ownerID)
	if owner == "" {
		return nil, invalidArg("error.blocked.invalid_input", ErrInvalidUser, map[string]string{"user_id": ownerID})
	}
	if _, err := uuid.Parse(owner); err != nil {
		return nil, invalidArg("error.blocked.invalid_input", ErrInvalidUser, map[string]string{"user_id": ownerID})
	}
	entries, err := s.repo.List(ctx, owner)
	if err != nil {
		return nil, internalErr(err)
	}
	if entries == nil {
		entries = []Entry{}
	}
	return entries, nil
}

// Block adds targetID to the owner's list and returns the entry.
func (s *Service) Block(ctx context.Context, ownerID, targetID string) (Entry, error) {
	owner, target, err := checkPair(ownerID, targetID)
	if err != nil {
		return Entry{}, err
	}
	entry, err := s.repo.Block(ctx, owner, target)
	if err != nil {
		if errors.Is(err, ErrTargetNotFound) {
			return Entry{}, notFound("error.blocked.target_not_found", err, map[string]string{"user_id": target})
		}
		return Entry{}, internalErr(err)
	}
	return entry, nil
}

// Unblock removes targetID from the owner's list.
func (s *Service) Unblock(ctx context.Context, ownerID, targetID string) error {
	owner, target, err := checkPair(ownerID, targetID)
	if err != nil {
		return err
	}
	if err := s.repo.Unblock(ctx, owner, target); err != nil {
		if errors.Is(err, ErrNotBlocked) {
			return notFound("error.blocked.not_found", err, map[string]string{"user_id": target})
		}
		return internalErr(err)
	}
	return nil
}

// IsBlocked reports whether targetID is on the owner's list. It backs future
// enforcement points (message/call filters); the HTTP list API does not use it.
func (s *Service) IsBlocked(ctx context.Context, ownerID, targetID string) (bool, error) {
	owner, target := cleanIDs(ownerID, targetID)
	if owner == "" || target == "" || owner == target {
		return false, nil
	}
	if _, err := uuid.Parse(owner); err != nil {
		return false, nil
	}
	if _, err := uuid.Parse(target); err != nil {
		return false, nil
	}
	entries, err := s.repo.List(ctx, owner)
	if err != nil {
		return false, internalErr(err)
	}
	for _, entry := range entries {
		if entry.UserID == target {
			return true, nil
		}
	}
	return false, nil
}
