package botauth

import (
	"context"
	"testing"
	"time"
)

type memRepo struct {
	tokens map[string]StoredToken
}

func newMemRepo() *memRepo {
	return &memRepo{tokens: map[string]StoredToken{}}
}

func (r *memRepo) Create(_ context.Context, input CreateTokenRecordInput) (StoredToken, error) {
	id := "11111111-1111-1111-1111-111111111111"
	out := StoredToken{
		ID:         id,
		BotUserID:  input.BotUserID,
		SecretHash: input.SecretHash,
		Scopes:     append([]string(nil), input.Scopes...),
		ChatIDs:    append([]string(nil), input.ChatIDs...),
		ExpiresAt:  input.ExpiresAt,
	}
	r.tokens[id] = out
	return out, nil
}

func (r *memRepo) FindActiveByID(_ context.Context, id string) (StoredToken, error) {
	out, ok := r.tokens[id]
	if !ok {
		return StoredToken{}, ErrTokenNotFound
	}
	if out.ExpiresAt != nil && out.ExpiresAt.Before(time.Now().UTC()) {
		return StoredToken{}, ErrTokenNotFound
	}
	return out, nil
}

func (r *memRepo) TouchLastUsed(context.Context, string, time.Time) error {
	return nil
}

func TestGenerateAndValidateTokenSuccess(t *testing.T) {
	repo := newMemRepo()
	svc, err := New(repo, "pepper-32-chars-minimum-value-here")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	generated, err := svc.GenerateToken(context.Background(), GenerateTokenInput{
		BotUserID: "bot-user-1",
		Scopes:    []string{"bot:messages:read"},
		ChatIDs:   []string{"*"},
	})
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if generated.Token == "" {
		t.Fatalf("expected generated token")
	}

	principal, err := svc.ValidateToken(context.Background(), generated.Token)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if principal.UserID != "bot-user-1" {
		t.Fatalf("unexpected user id: %s", principal.UserID)
	}
	if !principal.HasScope("bot:messages:read") {
		t.Fatalf("expected scope bot:messages:read")
	}
	if !principal.CanAccessChat("any-chat") {
		t.Fatalf("expected wildcard chat access")
	}
}

func TestValidateTokenInvalid(t *testing.T) {
	repo := newMemRepo()
	svc, err := New(repo, "pepper-32-chars-minimum-value-here")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	if _, err := svc.ValidateToken(context.Background(), "invalid"); err == nil {
		t.Fatalf("expected invalid token error")
	}
}
