package valkey

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestGetPresenceNilRepository(t *testing.T) {
	repo := NewPresenceRepository(nil)
	out, err := repo.GetPresence(context.Background(), []string{"user-1"})
	if err != nil {
		t.Fatalf("GetPresence() error = %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("GetPresence() = %#v, want empty map", out)
	}
	if err := repo.SetOnline(context.Background(), "user-1", time.Now(), time.Minute); err != nil {
		t.Fatalf("SetOnline() on nil repo error = %v", err)
	}
	if err := repo.SetOffline(context.Background(), "user-1", time.Now(), time.Minute); err != nil {
		t.Fatalf("SetOffline() on nil repo error = %v", err)
	}
}

func TestPresenceStatusFromUsesHashWhenPresent(t *testing.T) {
	status := presenceStatusFrom("u1", map[string]string{
		"online":    "1",
		"last_seen": "1700000000",
	}, "")
	if !status.Online {
		t.Fatalf("Online = false, want true")
	}
	if want := time.Unix(1700000000, 0).UTC(); !status.LastSeen.Equal(want) {
		t.Fatalf("LastSeen = %v, want %v", status.LastSeen, want)
	}
}

func TestPresenceStatusFromFallsBackWhenHashGone(t *testing.T) {
	status := presenceStatusFrom("u1", nil, "1690000000")
	if status.Online {
		t.Fatalf("Online = true, want false")
	}
	if want := time.Unix(1690000000, 0).UTC(); !status.LastSeen.Equal(want) {
		t.Fatalf("LastSeen = %v, want %v", status.LastSeen, want)
	}
}

func TestPresenceStatusFromEmpty(t *testing.T) {
	status := presenceStatusFrom("u1", nil, "")
	if status.Online || !status.LastSeen.IsZero() || status.UserID != "u1" {
		t.Fatalf("presenceStatusFrom() = %#v, want offline zero-value status", status)
	}
}

func TestPresenceEventOmitsLastSeenVisibleWhenNil(t *testing.T) {
	raw, err := json.Marshal(PresenceEvent{UserID: "u1", Online: true})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, ok := decoded["last_seen_visible"]; ok {
		t.Fatalf("presence event %s must omit last_seen_visible when nil", raw)
	}
}

func TestPresenceEventIncludesLastSeenVisible(t *testing.T) {
	visible := false
	raw, err := json.Marshal(PresenceEvent{UserID: "u1", Online: false, LastSeenVisible: &visible})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	value, ok := decoded["last_seen_visible"].(bool)
	if !ok || value {
		t.Fatalf("presence event %s last_seen_visible = %#v, want false", raw, decoded["last_seen_visible"])
	}
}
