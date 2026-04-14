//go:build cyoda_recon

package recon

import (
	"os"
	"testing"
)

func TestLoadCloudConfigDefaults(t *testing.T) {
	// Clear any env vars that might be set.
	os.Unsetenv("CYODA_CLOUD_BASE_URL")
	os.Unsetenv("CYODA_CLOUD_TOKEN_URL")
	os.Unsetenv("CYODA_CLOUD_CLIENT_ID")
	os.Unsetenv("CYODA_CLOUD_CLIENT_SECRET")

	cfg := loadCloudConfig()

	if cfg.BaseURL != "https://localhost:8443/api" {
		t.Errorf("expected default BaseURL, got %q", cfg.BaseURL)
	}
	if cfg.TokenURL != "https://localhost:8443/api/oauth/token" {
		t.Errorf("expected default TokenURL, got %q", cfg.TokenURL)
	}
	// Note: ClientID/ClientSecret may be non-empty if a .env file exists
	// in the test directory. We only verify the URL defaults here.
}

func TestParseDotEnvMissingFile(t *testing.T) {
	result := parseDotEnv("nonexistent.env")
	if len(result) != 0 {
		t.Errorf("expected empty map for missing file, got %d entries", len(result))
	}
}

func TestParseDotEnvDoesNotPollute(t *testing.T) {
	// Verify parseDotEnv does not set environment variables.
	key := "CYODA_RECON_TEST_SENTINEL"
	os.Unsetenv(key)

	// Write a temp .env with a sentinel key.
	tmpFile := t.TempDir() + "/test.env"
	os.WriteFile(tmpFile, []byte(key+"=should_not_leak\n"), 0644)

	result := parseDotEnv(tmpFile)
	if result[key] != "should_not_leak" {
		t.Errorf("expected sentinel in map, got %q", result[key])
	}
	if v, exists := os.LookupEnv(key); exists {
		// Never print the actual value — it could be a credential in a real scenario.
		t.Errorf("parseDotEnv must not set env vars, but %s was set (length %d)", key, len(v))
	}
}

func TestNewCyodaCloudClient(t *testing.T) {
	cfg := CloudConfig{
		BaseURL:      "https://example.com/api",
		TokenURL:     "https://example.com/api/oauth/token",
		ClientID:     "test-id",
		ClientSecret: "test-secret",
	}

	client := newCyodaCloudClient(cfg)
	if client == nil {
		t.Fatal("expected non-nil http.Client")
	}
}
