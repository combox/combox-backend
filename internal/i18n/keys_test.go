package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAllLocaleFilesHaveSameKeysAsEnglish(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	stringsDir := filepath.Clean(filepath.Join(wd, "..", "..", "strings"))

	files, err := filepath.Glob(filepath.Join(stringsDir, "*.json"))
	if err != nil {
		t.Fatalf("glob strings files: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no locale files found in %s", stringsDir)
	}

	basePath := filepath.Join(stringsDir, "en.json")
	baseDict := readStringMap(t, basePath)

	for _, file := range files {
		if filepath.Base(file) == "en.json" {
			continue
		}
		dict := readStringMap(t, file)

		for key := range baseDict {
			if _, ok := dict[key]; !ok {
				t.Fatalf("locale %s missing key %q", filepath.Base(file), key)
			}
		}
		for key := range dict {
			if _, ok := baseDict[key]; !ok {
				t.Fatalf("locale %s has extra key %q", filepath.Base(file), key)
			}
		}
	}
}

func readStringMap(t *testing.T, path string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}
