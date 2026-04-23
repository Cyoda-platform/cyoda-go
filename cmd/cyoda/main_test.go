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

// TestHelpConfigDatabase_ListsStorageBackends verifies that the config.database
// help topic (rendered via the help CLI entrypoint) describes all storage
// backends and CYODA_POSTGRES_URL. This replaces the former
// TestPrintStorageHelp_ListsPluginsAndRequired which called the now-deleted
// printStorageHelp() function.
func TestHelpConfigDatabase_ListsStorageBackends(t *testing.T) {
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	code := runHelpCmd([]string{"config", "database"})
	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	s := buf.String()

	if code != 0 {
		t.Errorf("runHelpCmd(config database) exit = %d", code)
	}
	for _, want := range []string{"memory", "postgres", "sqlite", "CYODA_POSTGRES_URL"} {
		if !strings.Contains(s, want) {
			t.Errorf("config.database help missing %q:\n%s", want, s)
		}
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
