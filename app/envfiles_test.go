package app

import (
	"fmt"
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

func TestUserConfigPathResolved_LinuxWithXDG(t *testing.T) {
	got := userConfigPathResolved("linux",
		func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return "/tmp/cfg"
			}
			return ""
		},
		func() (string, error) { return "/home/u", nil },
	)
	want := filepath.Join("/tmp/cfg", "cyoda", "cyoda.env")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUserConfigPathResolved_LinuxNoXDG(t *testing.T) {
	got := userConfigPathResolved("linux",
		func(key string) string { return "" },
		func() (string, error) { return "/home/u", nil },
	)
	want := filepath.Join("/home/u", ".config", "cyoda", "cyoda.env")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUserConfigPathResolved_macOS(t *testing.T) {
	got := userConfigPathResolved("darwin",
		func(key string) string { return "" },
		func() (string, error) { return "/Users/u", nil },
	)
	want := filepath.Join("/Users/u", ".config", "cyoda", "cyoda.env")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUserConfigPathResolved_WindowsWithAppData(t *testing.T) {
	got := userConfigPathResolved("windows",
		func(key string) string {
			if key == "AppData" {
				return `C:\Users\u\AppData\Roaming`
			}
			return ""
		},
		func() (string, error) { return `C:\Users\u`, nil },
	)
	want := filepath.Join(`C:\Users\u\AppData\Roaming`, "cyoda", "cyoda.env")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUserConfigPathResolved_WindowsNoAppData(t *testing.T) {
	got := userConfigPathResolved("windows",
		func(key string) string { return "" },
		func() (string, error) { return `C:\Users\u`, nil },
	)
	want := filepath.Join(`C:\Users\u`, "AppData", "Roaming", "cyoda", "cyoda.env")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUserConfigPathResolved_HomeLookupFails(t *testing.T) {
	got := userConfigPathResolved("linux",
		func(key string) string { return "" },
		func() (string, error) { return "", fmt.Errorf("no home") },
	)
	if got != "" {
		t.Fatalf("expected empty path when home lookup fails, got %q", got)
	}
}

func TestSystemConfigPathsResolved_Linux(t *testing.T) {
	got := systemConfigPathsResolved("linux", func(key string) string { return "" })
	if len(got) != 1 || got[0] != "/etc/cyoda/cyoda.env" {
		t.Fatalf("got %v, want [/etc/cyoda/cyoda.env]", got)
	}
}

func TestSystemConfigPathsResolved_macOSEmpty(t *testing.T) {
	got := systemConfigPathsResolved("darwin", func(key string) string { return "" })
	if len(got) != 0 {
		t.Fatalf("got %v, want empty (macOS has no system config path)", got)
	}
}

func TestSystemConfigPathsResolved_WindowsWithProgramData(t *testing.T) {
	got := systemConfigPathsResolved("windows",
		func(key string) string {
			if key == "ProgramData" {
				return `C:\ProgramData`
			}
			return ""
		},
	)
	want := filepath.Join(`C:\ProgramData`, "cyoda", "cyoda.env")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v want [%v]", got, want)
	}
}

func TestSystemConfigPathsResolved_WindowsNoProgramData(t *testing.T) {
	got := systemConfigPathsResolved("windows", func(key string) string { return "" })
	if len(got) != 0 {
		t.Fatalf("got %v, want empty when ProgramData unset", got)
	}
}

func TestLoadEnvFiles_AutoloadsUserConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cyoda")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(cfgDir, "cyoda.env")
	if err := os.WriteFile(cfgFile, []byte("CYODA_TEST_AUTOLOAD=from-user-config\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Point the OS-appropriate env var at the temp dir so both linux/darwin
	// (XDG_CONFIG_HOME) and windows (AppData) branches resolve into tmp.
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("AppData", tmp)
	// Prevent inherited system config from leaking into the test.
	t.Setenv("ProgramData", filepath.Join(tmp, "no-such"))

	os.Unsetenv("CYODA_TEST_AUTOLOAD")
	t.Cleanup(func() { os.Unsetenv("CYODA_TEST_AUTOLOAD") })

	wd := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	LoadEnvFiles()

	if got := os.Getenv("CYODA_TEST_AUTOLOAD"); got != "from-user-config" {
		t.Fatalf("expected CYODA_TEST_AUTOLOAD from user config, got %q", got)
	}
}

func TestLoadEnvFiles_ShellEnvOverridesUserConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cyoda")
	_ = os.MkdirAll(cfgDir, 0755)
	_ = os.WriteFile(filepath.Join(cfgDir, "cyoda.env"),
		[]byte("CYODA_TEST_OVERRIDE=from-user-config\n"), 0644)

	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("AppData", tmp)
	t.Setenv("ProgramData", filepath.Join(tmp, "no-such"))
	t.Setenv("CYODA_TEST_OVERRIDE", "from-shell")

	wd := t.TempDir()
	prev, _ := os.Getwd()
	_ = os.Chdir(wd)
	t.Cleanup(func() { _ = os.Chdir(prev) })

	LoadEnvFiles()

	if got := os.Getenv("CYODA_TEST_OVERRIDE"); got != "from-shell" {
		t.Fatalf("shell env should win; got %q", got)
	}
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
		{"../escape", 0},    // path traversal rejected
		{"foo/bar", 0},      // path separator rejected
		{"valid,../bad", 1}, // only valid kept
	}
	for _, tt := range tests {
		got := splitProfiles(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitProfiles(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}
