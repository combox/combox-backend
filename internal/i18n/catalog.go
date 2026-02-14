package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Catalog struct {
	defaultLocale string
	values        map[string]map[string]string
}

func LoadDir(path, defaultLocale string) (*Catalog, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read strings dir: %w", err)
	}

	values := make(map[string]map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}

		locale := strings.TrimSuffix(strings.ToLower(name), ".json")
		data, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			return nil, fmt.Errorf("read strings file %s: %w", name, err)
		}

		var dictionary map[string]string
		if err := json.Unmarshal(data, &dictionary); err != nil {
			return nil, fmt.Errorf("decode strings file %s: %w", name, err)
		}
		values[locale] = dictionary
	}

	defaultLocale = normalizeLocale(defaultLocale)
	if _, ok := values[defaultLocale]; !ok {
		return nil, fmt.Errorf("default locale file is missing: %s.json", defaultLocale)
	}

	return &Catalog{defaultLocale: defaultLocale, values: values}, nil
}

func (c *Catalog) Translate(requestLocale, key string) string {
	locale := c.ResolveLocale(requestLocale)
	if value, ok := c.values[locale][key]; ok {
		return value
	}
	if value, ok := c.values[c.defaultLocale][key]; ok {
		return value
	}
	return key
}

func (c *Catalog) ResolveLocale(raw string) string {
	normalizedRaw := strings.TrimSpace(strings.ToLower(raw))
	locale := normalizeLocale(raw)
	if locale == "" {
		return c.defaultLocale
	}
	if _, ok := c.values[locale]; ok {
		return locale
	}

	for supported := range c.values {
		if strings.HasPrefix(normalizedRaw, supported+"-") || strings.HasPrefix(normalizedRaw, supported+"_") {
			return supported
		}
	}

	return c.defaultLocale
}

func normalizeLocale(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';'
	})
	if len(parts) == 0 {
		return ""
	}
	code := strings.TrimSpace(parts[0])
	if len(code) < 2 {
		return ""
	}
	if len(code) >= 2 {
		return code[:2]
	}
	return code
}
