package app

import (
	"os"
	"testing"
)

// Note on postgres DB config:
// Before Plan 3, cfg.DB (postgres.DBConfig) lived on Config. With the
// plugin refactor, postgres configuration is plugin-internal — read
// from CYODA_POSTGRES_* via the injected getenv inside the plugin.
// Nothing in the app layer needs to see or validate it.

func TestDefaultConfig_StorageBackendDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.StorageBackend != "memory" {
		t.Errorf("expected StorageBackend=memory, got %q", cfg.StorageBackend)
	}
}

func TestConfig_StorageBackendFromEnv(t *testing.T) {
	os.Setenv("CYODA_STORAGE_BACKEND", "postgres")
	defer os.Unsetenv("CYODA_STORAGE_BACKEND")

	cfg := DefaultConfig()
	if cfg.StorageBackend != "postgres" {
		t.Errorf("expected StorageBackend=postgres, got %q", cfg.StorageBackend)
	}
}
