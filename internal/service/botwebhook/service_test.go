package botwebhook

import (
	"context"
	"testing"
)

func TestCreateWebhookSuccess(t *testing.T) {
	svc := New()
	out, err := svc.Create(context.Background(), CreateInput{
		BotUserID:   "bot-user-1",
		EndpointURL: "https://example.com/webhook",
		Events:      []string{"message.created", "message.read"},
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if out.ID == "" {
		t.Fatalf("expected id")
	}
	if !out.Enabled {
		t.Fatalf("expected enabled")
	}
	if len(out.Events) != 2 {
		t.Fatalf("unexpected events count: %d", len(out.Events))
	}
}

func TestCreateWebhookRejectsInvalidURL(t *testing.T) {
	svc := New()
	_, err := svc.Create(context.Background(), CreateInput{
		BotUserID:   "bot-user-1",
		EndpointURL: "ftp://example.com/webhook",
		Events:      []string{"message.created"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestCreateWebhookRejectsDuplicate(t *testing.T) {
	svc := New()
	_, err := svc.Create(context.Background(), CreateInput{
		BotUserID:   "bot-user-1",
		EndpointURL: "https://example.com/webhook",
		Events:      []string{"message.created"},
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = svc.Create(context.Background(), CreateInput{
		BotUserID:   "bot-user-1",
		EndpointURL: "https://example.com/webhook",
		Events:      []string{"message.created"},
	})
	if err == nil {
		t.Fatalf("expected duplicate error")
	}
}
