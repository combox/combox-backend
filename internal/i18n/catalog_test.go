package i18n

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogTranslateAndFallback(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "en.json"), `{"status.ok":"ok","key.only_en":"en_value"}`)
	mustWriteFile(t, filepath.Join(dir, "ru.json"), `{"status.ok":"ok_ru"}`)

	catalog, err := LoadDir(dir, "en")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := catalog.Translate("ru-RU", "status.ok"); got != "ok_ru" {
		t.Fatalf("expected ru value, got %s", got)
	}
	if got := catalog.Translate("ru-RU", "key.only_en"); got != "en_value" {
		t.Fatalf("expected en fallback, got %s", got)
	}
	if got := catalog.Translate("xx-XX", "status.ok"); got != "ok" {
		t.Fatalf("expected default locale value, got %s", got)
	}
}

func TestCatalogMissingDefaultLocale(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "ru.json"), `{"status.ok":"ok_ru"}`)

	_, err := LoadDir(dir, "en")
	if err == nil {
		t.Fatalf("expected error when default locale file is missing")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
