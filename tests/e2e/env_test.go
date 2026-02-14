//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

type testEnv struct {
	PostgresDSN string
	ValkeyAddr  string

	repoRoot string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	pg := os.Getenv("E2E_POSTGRES_DSN")
	vk := os.Getenv("E2E_VALKEY_ADDR")
	if pg == "" || vk == "" {
		t.Skip("set E2E_POSTGRES_DSN and E2E_VALKEY_ADDR (or start ./docker-compose.e2e.yml and export them)")
	}

	return &testEnv{
		PostgresDSN: pg,
		ValkeyAddr:  vk,
		repoRoot:    findRepoRoot(t),
	}
}

func (e *testEnv) migrationsPath() string { return filepath.Join(e.repoRoot, "migrations") }
func (e *testEnv) stringsPath() string    { return filepath.Join(e.repoRoot, "strings") }

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatalf("failed to find repo root from %s", dir)
		}
		dir = next
	}
}
