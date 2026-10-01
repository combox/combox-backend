package chat

import "testing"

func TestMessagePreviewHeadCollapsesWhitespace(t *testing.T) {
	got := messagePreviewHead("  hello\n\t  world  ", 200)
	if got != "hello world" {
		t.Fatalf("messagePreviewHead() = %q, want %q", got, "hello world")
	}
}

func TestMessagePreviewHeadTruncatesToLimit(t *testing.T) {
	got := messagePreviewHead("привет, мир", 5)
	if want := "приве…"; got != want {
		t.Fatalf("messagePreviewHead() = %q, want %q", got, want)
	}
}

func TestMessagePreviewHeadUnlimited(t *testing.T) {
	got := messagePreviewHead("short", 0)
	if got != "short" {
		t.Fatalf("messagePreviewHead() = %q, want %q", got, "short")
	}
}

func TestMessagePreviewHeadEmpty(t *testing.T) {
	if got := messagePreviewHead("   \n ", 160); got != "" {
		t.Fatalf("messagePreviewHead() = %q, want empty string", got)
	}
}
