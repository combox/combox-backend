package botauth

import (
	"context"
	"testing"
)

func TestValidateTokenSuccess(t *testing.T) {
	svc, err := New([]TokenConfig{
		{
			Token:   "tok-1",
			UserID:  "bot-user-1",
			Scopes:  []string{"bot:messages:read"},
			ChatIDs: []string{"*"},
		},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	p, err := svc.ValidateToken(context.Background(), "tok-1")
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if p.UserID != "bot-user-1" {
		t.Fatalf("unexpected user id: %s", p.UserID)
	}
	if !p.HasScope("bot:messages:read") {
		t.Fatalf("expected read scope")
	}
	if !p.CanAccessChat("chat-any") {
		t.Fatalf("expected wildcard chat access")
	}
}

func TestValidateTokenInvalid(t *testing.T) {
	svc, err := New([]TokenConfig{
		{
			Token:   "tok-1",
			UserID:  "bot-user-1",
			Scopes:  []string{"bot:messages:read"},
			ChatIDs: []string{"chat-1"},
		},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	if _, err := svc.ValidateToken(context.Background(), "tok-2"); err == nil {
		t.Fatalf("expected invalid token error")
	}
}
