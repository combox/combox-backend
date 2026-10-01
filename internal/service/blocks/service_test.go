package blocks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memBlocksRepo struct {
	edges map[string]map[string]Entry
}

func newMemBlocksRepo() *memBlocksRepo {
	return &memBlocksRepo{edges: map[string]map[string]Entry{}}
}

func (m *memBlocksRepo) List(_ context.Context, ownerID string) ([]Entry, error) {
	set := m.edges[ownerID]
	out := make([]Entry, 0, len(set))
	for _, entry := range set {
		out = append(out, entry)
	}
	return out, nil
}

func (m *memBlocksRepo) Block(_ context.Context, ownerID, blockedID string) (Entry, error) {
	if blockedID == "" {
		return Entry{}, ErrTargetNotFound
	}
	if m.edges[ownerID] == nil {
		m.edges[ownerID] = map[string]Entry{}
	}
	if entry, ok := m.edges[ownerID][blockedID]; ok {
		return entry, nil
	}
	entry := Entry{UserID: blockedID, CreatedAt: time.Now().UTC()}
	m.edges[ownerID][blockedID] = entry
	return entry, nil
}

func (m *memBlocksRepo) Unblock(_ context.Context, ownerID, blockedID string) error {
	set := m.edges[ownerID]
	if _, ok := set[blockedID]; !ok {
		return ErrNotBlocked
	}
	delete(set, blockedID)
	return nil
}

func testUUIDs(t *testing.T) (owner, target, other string) {
	t.Helper()
	return uuid.NewString(), uuid.NewString(), uuid.NewString()
}

func TestBlockAndList(t *testing.T) {
	repo := newMemBlocksRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	owner, target, other := testUUIDs(t)
	ctx := context.Background()

	if _, err := svc.Block(ctx, owner, target); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := svc.Block(ctx, owner, other); err != nil {
		t.Fatalf("block: %v", err)
	}
	entries, err := svc.List(ctx, owner)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("list size = %d, want 2", len(entries))
	}

	// Re-blocking is idempotent, not a duplicate.
	if _, err := svc.Block(ctx, owner, target); err != nil {
		t.Fatalf("re-block: %v", err)
	}
	entries, err = svc.List(ctx, owner)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("list size after re-block = %d, want 2", len(entries))
	}
}

func TestBlockRejectsBadIDsAndSelf(t *testing.T) {
	svc, err := New(newMemBlocksRepo())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	owner, target, _ := testUUIDs(t)
	ctx := context.Background()

	for name, pair := range map[string][2]string{
		"empty owner":  {"", target},
		"bad owner":    {"not-a-uuid", target},
		"empty target": {owner, ""},
		"bad target":   {owner, "nope"},
		"self":         {owner, owner},
	} {
		if _, err := svc.Block(ctx, pair[0], pair[1]); err == nil {
			t.Fatalf("%s: expected an error", name)
		} else {
			var svcErr *Error
			if !errors.As(err, &svcErr) || svcErr.Code != CodeInvalidArgument {
				t.Fatalf("%s: want invalid_argument, got %v", name, err)
			}
		}
	}
}

func TestUnblockMissingEdgeIsNotFound(t *testing.T) {
	repo := newMemBlocksRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	owner, target, _ := testUUIDs(t)
	ctx := context.Background()

	if err := svc.Unblock(ctx, owner, target); err == nil {
		t.Fatalf("expected not_found for a missing edge")
	} else {
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != CodeNotFound {
			t.Fatalf("want not_found, got %v", err)
		}
		if !errors.Is(err, ErrNotBlocked) {
			t.Fatalf("error should unwrap to ErrNotBlocked")
		}
	}

	if _, err := svc.Block(ctx, owner, target); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := svc.Unblock(ctx, owner, target); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	entries, err := svc.List(ctx, owner)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("list size = %d, want 0", len(entries))
	}
}

func TestListEmptyOwnerIsEmptySlice(t *testing.T) {
	svc, err := New(newMemBlocksRepo())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	entries, err := svc.List(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if entries == nil || len(entries) != 0 {
		t.Fatalf("empty list must be [], got %v", entries)
	}
}

func TestListRejectsEmptyOwner(t *testing.T) {
	svc, err := New(newMemBlocksRepo())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := svc.List(context.Background(), "  "); err == nil {
		t.Fatalf("expected an error for an empty owner")
	}
}

func TestNewRequiresRepository(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatalf("expected an error when the repository is missing")
	}
}
