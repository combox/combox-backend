package media

import (
	"context"
	"net/url"
	"testing"
)

func TestValidateImportURL_BlocksLocalhost(t *testing.T) {
	ctx := context.Background()
	parsed, _ := url.Parse("http://localhost/file.png")
	if err := validateImportURL(ctx, parsed); err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateImportURL_BlocksPrivateIP(t *testing.T) {
	ctx := context.Background()
	parsed, _ := url.Parse("http://192.168.1.10/file.png")
	if err := validateImportURL(ctx, parsed); err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateImportURL_AllowsPublicIP(t *testing.T) {
	ctx := context.Background()
	parsed, _ := url.Parse("https://8.8.8.8/file.png")
	if err := validateImportURL(ctx, parsed); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateImportURL_BlocksNonStandardPort(t *testing.T) {
	ctx := context.Background()
	parsed, _ := url.Parse("https://8.8.8.8:8443/file.png")
	if err := validateImportURL(ctx, parsed); err == nil {
		t.Fatalf("expected error")
	}
}
