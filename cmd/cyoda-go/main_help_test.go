package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHelp_StorageSection exercises the plugin-driven help rendering.
// It builds the binary and runs --help; the storage section must list
// registered plugins in sorted order and show CYODA_POSTGRES_URL as required.
func TestHelp_StorageSection(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/cyoda-go", "--help")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go run cyoda-go --help: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "Available: memory, postgres") {
		t.Errorf("expected sorted plugin list; got:\n%s", s)
	}
	if !strings.Contains(s, "CYODA_POSTGRES_URL") || !strings.Contains(s, "(required)") {
		t.Errorf("expected CYODA_POSTGRES_URL to appear with (required); got:\n%s", s)
	}
	if !strings.Contains(s, "No configuration required.") {
		t.Errorf("expected memory plugin to be listed with 'No configuration required.'; got:\n%s", s)
	}
}
