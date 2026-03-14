package media

import (
	"context"
	"net"
	"net/url"
	"testing"
)

func stubImportURLLookupIP(t *testing.T, fn func(ctx context.Context, network, host string) ([]net.IP, error)) {
	t.Helper()
	prev := importURLLookupIP
	importURLLookupIP = fn
	t.Cleanup(func() { importURLLookupIP = prev })
}

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

func TestValidateImportURL_BlocksNonAllowlistedHost(t *testing.T) {
	ctx := context.Background()
	parsed, _ := url.Parse("https://example.com/file.png")
	if err := validateImportURL(ctx, parsed); err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateImportURL_AllowsAllowlistedHost(t *testing.T) {
	ctx := context.Background()
	stubImportURLLookupIP(t, func(ctx context.Context, network, host string) ([]net.IP, error) {
		_ = ctx
		_ = network
		_ = host
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})

	parsed, _ := url.Parse("https://media.giphy.com/media/abc/giphy.gif")
	if err := validateImportURL(ctx, parsed); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateImportURL_BlocksNonStandardPort(t *testing.T) {
	ctx := context.Background()
	parsed, _ := url.Parse("https://media.giphy.com:8443/media/abc/giphy.gif")
	if err := validateImportURL(ctx, parsed); err == nil {
		t.Fatalf("expected error")
	}
}
