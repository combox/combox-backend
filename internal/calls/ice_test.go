package calls

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func TestTurnCredential(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ttl := 12 * time.Hour

	username, password := TurnCredential("secret", "user-42", ttl, now)

	wantUser := fmt.Sprintf("%d:%s", now.Add(ttl).Unix(), "user-42")
	if username != wantUser {
		t.Fatalf("username = %q, want %q", username, wantUser)
	}

	mac := hmac.New(sha1.New, []byte("secret"))
	_, _ = mac.Write([]byte(username))
	wantPass := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if password != wantPass {
		t.Fatalf("password = %q, want %q", password, wantPass)
	}
}

func TestBuildICEServers(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfg := Config{
		STUNURLs:          []string{"stun:stun.example:3478"},
		TURNURLs:          []string{"turn:turn.example:3478?transport=udp", "turns:turn.example:5349?transport=tcp"},
		TURNSharedSecret:  "secret",
		TURNCredentialTTL: time.Hour,
	}

	servers := BuildICEServers(cfg, "user-1", now)
	if len(servers) != 2 {
		t.Fatalf("expected stun+turn, got %d servers", len(servers))
	}
	if servers[0].URLs[0] != "stun:stun.example:3478" || servers[0].Username != "" {
		t.Fatalf("unexpected stun server: %+v", servers[0])
	}
	if len(servers[1].URLs) != 2 || servers[1].Username == "" || servers[1].Credential == "" {
		t.Fatalf("unexpected turn server: %+v", servers[1])
	}
	if want := fmt.Sprintf("%d:user-1", now.Add(time.Hour).Unix()); servers[1].Username != want {
		t.Fatalf("turn username = %q, want %q", servers[1].Username, want)
	}
}

func TestBuildICEServersWithoutSecretSkipsTurn(t *testing.T) {
	servers := BuildICEServers(Config{
		STUNURLs: []string{"stun:stun.example:3478"},
		TURNURLs: []string{"turn:turn.example:3478"},
	}, "user-1", time.Now())
	if len(servers) != 1 {
		t.Fatalf("turn without secret must be skipped, got %d servers", len(servers))
	}
}
