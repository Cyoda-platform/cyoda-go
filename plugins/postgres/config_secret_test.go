package postgres

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseConfig_URLFromFile verifies that CYODA_POSTGRES_URL_FILE is
// honoured when CYODA_POSTGRES_URL is empty.
func TestParseConfig_URLFromFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "pg-url")
	url := "postgres://user:pass@host:5432/db"
	if err := os.WriteFile(secretPath, []byte(url+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	getenv := func(key string) string {
		switch key {
		case "CYODA_POSTGRES_URL":
			return ""
		case "CYODA_POSTGRES_URL_FILE":
			return secretPath
		default:
			return ""
		}
	}

	cfg, err := parseConfig(getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != url {
		t.Errorf("want URL %q, got %q", url, cfg.URL)
	}
}

// TestParseConfig_URLFileTakesPrecedence verifies that CYODA_POSTGRES_URL_FILE
// wins over CYODA_POSTGRES_URL when both are set.
func TestParseConfig_URLFileTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "pg-url")
	fileURL := "postgres://from-file@host/db"
	if err := os.WriteFile(secretPath, []byte(fileURL), 0600); err != nil {
		t.Fatal(err)
	}

	getenv := func(key string) string {
		switch key {
		case "CYODA_POSTGRES_URL":
			return "postgres://from-env@host/db"
		case "CYODA_POSTGRES_URL_FILE":
			return secretPath
		default:
			return ""
		}
	}

	cfg, err := parseConfig(getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != fileURL {
		t.Errorf("_FILE must win; want %q, got %q", fileURL, cfg.URL)
	}
}

// TestParseConfig_URLFileUnreadable verifies that an unreadable _FILE path
// returns an error rather than silently treating the URL as empty.
func TestParseConfig_URLFileUnreadable(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "CYODA_POSTGRES_URL":
			return ""
		case "CYODA_POSTGRES_URL_FILE":
			return "/nonexistent/path/to/pg-url"
		default:
			return ""
		}
	}

	_, err := parseConfig(getenv)
	if err == nil {
		t.Fatal("expected error for unreadable _FILE path, got nil")
	}
}
