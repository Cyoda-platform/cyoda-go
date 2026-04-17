package app

import (
	"os"
	"testing"
)

func TestDefaultConfig_AdminDefaults(t *testing.T) {
	t.Setenv("CYODA_ADMIN_PORT", "")
	_ = os.Unsetenv("CYODA_ADMIN_PORT")
	t.Setenv("CYODA_ADMIN_BIND_ADDRESS", "")
	_ = os.Unsetenv("CYODA_ADMIN_BIND_ADDRESS")
	cfg := DefaultConfig()
	if cfg.Admin.Port != 9091 {
		t.Errorf("Admin.Port = %d, want 9091", cfg.Admin.Port)
	}
	if cfg.Admin.BindAddress != "127.0.0.1" {
		t.Errorf("Admin.BindAddress = %q, want %q", cfg.Admin.BindAddress, "127.0.0.1")
	}
}

func TestDefaultConfig_AdminOverrides(t *testing.T) {
	t.Setenv("CYODA_ADMIN_PORT", "7777")
	t.Setenv("CYODA_ADMIN_BIND_ADDRESS", "0.0.0.0")
	cfg := DefaultConfig()
	if cfg.Admin.Port != 7777 {
		t.Errorf("Admin.Port = %d, want 7777", cfg.Admin.Port)
	}
	if cfg.Admin.BindAddress != "0.0.0.0" {
		t.Errorf("Admin.BindAddress = %q, want %q", cfg.Admin.BindAddress, "0.0.0.0")
	}
}
