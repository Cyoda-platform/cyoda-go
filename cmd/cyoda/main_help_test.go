package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHelp_FlagDelegatesToHelpCli verifies that `cyoda --help` delegates to
// the help subsystem and renders the "cli" topic. The storage-section coverage
// that was previously in this test is now in TestPrintStorageHelp_ListsPluginsAndRequired
// (main_test.go, package main) — split in Task 13 because --help no longer
// calls printHelp() directly.
func TestHelp_FlagDelegatesToHelpCli(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/cyoda", "--help")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go run cyoda --help: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "cli") {
		t.Errorf("expected --help output to contain 'cli'; got:\n%s", s)
	}
}
