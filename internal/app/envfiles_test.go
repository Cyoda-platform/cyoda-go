package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFiles_ProfileLayering(t *testing.T) {
	// Work in a temp dir so we don't pollute the repo root.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	// Write base .env
	os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"BASE_VAR=from_base\nOVERRIDE_VAR=base_value\n"), 0644)

	// Write profile .env.test
	os.WriteFile(filepath.Join(dir, ".env.test"), []byte(
		"PROFILE_VAR=from_profile\nOVERRIDE_VAR=profile_value\n"), 0644)

	// Clear any previous values
	os.Unsetenv("BASE_VAR")
	os.Unsetenv("PROFILE_VAR")
	os.Unsetenv("OVERRIDE_VAR")
	os.Unsetenv("CYODA_PROFILES")
	t.Cleanup(func() {
		os.Unsetenv("BASE_VAR")
		os.Unsetenv("PROFILE_VAR")
		os.Unsetenv("OVERRIDE_VAR")
	})

	os.Setenv("CYODA_PROFILES", "test")
	LoadEnvFiles()

	if v := os.Getenv("BASE_VAR"); v != "from_base" {
		t.Errorf("BASE_VAR = %q, want %q", v, "from_base")
	}
	if v := os.Getenv("PROFILE_VAR"); v != "from_profile" {
		t.Errorf("PROFILE_VAR = %q, want %q", v, "from_profile")
	}
	// Profile should override base
	if v := os.Getenv("OVERRIDE_VAR"); v != "profile_value" {
		t.Errorf("OVERRIDE_VAR = %q, want %q (profile should override base)", v, "profile_value")
	}
}

func TestLoadEnvFiles_ShellEnvWins(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	os.WriteFile(filepath.Join(dir, ".env"), []byte("SHELL_WINS=from_file\n"), 0644)

	// Pre-set the var in the real environment
	os.Setenv("SHELL_WINS", "from_shell")
	os.Unsetenv("CYODA_PROFILES")
	t.Cleanup(func() { os.Unsetenv("SHELL_WINS") })

	LoadEnvFiles()

	if v := os.Getenv("SHELL_WINS"); v != "from_shell" {
		t.Errorf("SHELL_WINS = %q, want %q (shell env should win)", v, "from_shell")
	}
}

func TestLoadEnvFiles_MissingFilesSkipped(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	os.Setenv("CYODA_PROFILES", "nonexistent")
	t.Cleanup(func() { os.Unsetenv("CYODA_PROFILES") })

	// Should not panic or error — missing files are silently skipped.
	LoadEnvFiles()
}

func TestSplitProfiles_Validation(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"local", 1},
		{"local,postgres,otel", 3},
		{" local , postgres ", 2},
		{"", 0},
		{"../escape", 0},   // path traversal rejected
		{"foo/bar", 0},     // path separator rejected
		{"valid,../bad", 1}, // only valid kept
	}
	for _, tt := range tests {
		got := splitProfiles(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitProfiles(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}
