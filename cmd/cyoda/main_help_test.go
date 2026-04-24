package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHelp_FlagShowsSummary verifies that `cyoda --help` shows the USAGE +
// FLAGS + TOPICS summary (same as `cyoda help` with no args). Users can still
// run `cyoda help cli` for the full CLI reference.
func TestHelp_FlagShowsSummary(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/cyoda", "--help")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go run cyoda --help: %v", err)
	}
	s := string(out)
	for _, want := range []string{"USAGE", "FLAGS", "TOPICS"} {
		if !strings.Contains(s, want) {
			t.Errorf("--help output missing %q; got:\n%s", want, s)
		}
	}
}
