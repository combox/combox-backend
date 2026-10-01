package postgres

import (
	"strings"
	"testing"
)

func intRef(v int) *int { return &v }

func TestResolveChannelTopicAllocation(t *testing.T) {
	cases := []struct {
		name      string
		stored    *int
		max       *int
		allocated int
		newNext   int
	}{
		// Fresh group, no channels: first topic is 2.
		{name: "fresh nil/nil", stored: nil, max: nil, allocated: 2, newNext: 3},
		// Stale prod case: next_topic_number NULL but channels up to 12 exist
		// (e.g. TheComBox.). Old code allocated 2 -> unique violation -> 500.
		{name: "stale null next with max 12", stored: nil, max: intRef(12), allocated: 13, newNext: 14},
		{name: "stale null next with max 2", stored: nil, max: intRef(2), allocated: 3, newNext: 4},
		// Healthy counters keep working.
		{name: "healthy next 3 max 2", stored: intRef(3), max: intRef(2), allocated: 3, newNext: 4},
		{name: "stale next 3 max 12", stored: intRef(3), max: intRef(12), allocated: 13, newNext: 14},
		{name: "ahead next 10 max 3", stored: intRef(10), max: intRef(3), allocated: 10, newNext: 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, next := resolveChannelTopicAllocation(tc.stored, tc.max)
			if got != tc.allocated || next != tc.newNext {
				t.Fatalf("got (%d,%d), want (%d,%d)", got, next, tc.allocated, tc.newNext)
			}
			if got < 2 {
				t.Fatalf("allocated topic must be >=2, got %d", got)
			}
		})
	}
}

func TestIsChannelTopicConflict(t *testing.T) {
	if !isChannelTopicConflict(errTestContains("duplicate key value violates unique constraint \"idx_channel_topic_number_unique\"")) {
		t.Fatalf("expected unique topic conflict to be detected")
	}
	if isChannelTopicConflict(errTestContains("connection reset")) {
		t.Fatalf("non-conflict error must not match")
	}
	if isChannelTopicConflict(nil) {
		t.Fatalf("nil must not match")
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

func errTestContains(s string) error { return testErr(s) }

func TestAllocTopicSQLSelfHeals(t *testing.T) {
	// The SQL text must clamp to MAX(topic_number): without GREATEST/MAX the
	// stale-NULL prod rows allocate 2 again and POST /channels 500s.
	// This pins the query shape (logic itself is covered above).
	const want1 = "GREATEST"
	const want2 = "MAX(topic_number)"
	// Read the source file to avoid duplicating the query string here.
	// If this ever fails, update allocTopic in chat_repository.go.
	src := allocTopicSQLForTest()
	if !strings.Contains(src, want1) || !strings.Contains(src, want2) {
		t.Fatalf("allocTopic must contain %q and %q, got: %s", want1, want2, src)
	}
}
