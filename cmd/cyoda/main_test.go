package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHelpSubcommand_ExistsAndDispatches(t *testing.T) {
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	code := runHelpCmd([]string{"cli"})
	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "cli") {
		t.Errorf("output missing 'cli': %q", buf.String())
	}
}

// TestPrintStorageHelp_ListsPluginsAndRequired exercises the plugin-driven
// storage section rendered by printStorageHelp. Previously covered by the
// integration test TestHelp_StorageSection which ran --help; now that --help
// delegates to the help subsystem (Task 13), this unit test preserves the
// storage-section coverage.
func TestPrintStorageHelp_ListsPluginsAndRequired(t *testing.T) {
	// Capture stdout (printStorageHelp writes to os.Stdout via fmt.Println).
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	printStorageHelp()
	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	s := buf.String()

	if !strings.Contains(s, "memory") || !strings.Contains(s, "postgres") {
		t.Errorf("expected sorted plugin list; got:\n%s", s)
	}
	if !strings.Contains(s, "CYODA_POSTGRES_URL") || !strings.Contains(s, "(required)") {
		t.Errorf("expected CYODA_POSTGRES_URL to appear with (required); got:\n%s", s)
	}
	if !strings.Contains(s, "No configuration required.") {
		t.Errorf("expected memory plugin to be listed with 'No configuration required.'; got:\n%s", s)
	}
}

func TestPrintVersion_IncludesAllFields(t *testing.T) {
	version = "1.2.3"
	commit = "abc1234"
	buildDate = "2026-04-23T12:00:00Z"
	defer func() { version, commit, buildDate = "dev", "unknown", "unknown" }()

	var buf bytes.Buffer
	printVersion(&buf)
	s := buf.String()
	for _, want := range []string{"1.2.3", "abc1234", "2026-04-23T12:00:00Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("printVersion output missing %q: %q", want, s)
		}
	}
}
