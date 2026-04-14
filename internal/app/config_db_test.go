package app

import (
	"os"
	"testing"
)

func TestDefaultConfig_DBDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DB.URL != "" {
		t.Errorf("expected empty DB URL by default, got %q", cfg.DB.URL)
	}
	if cfg.DB.MaxConns != 25 {
		t.Errorf("expected MaxConns=25, got %d", cfg.DB.MaxConns)
	}
	if cfg.DB.MinConns != 5 {
		t.Errorf("expected MinConns=5, got %d", cfg.DB.MinConns)
	}
	if cfg.DB.MaxConnIdleTime != "5m" {
		t.Errorf("expected MaxConnIdleTime=5m, got %q", cfg.DB.MaxConnIdleTime)
	}
	if cfg.DB.AutoMigrate != true {
		t.Error("expected AutoMigrate=true by default")
	}
}

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
