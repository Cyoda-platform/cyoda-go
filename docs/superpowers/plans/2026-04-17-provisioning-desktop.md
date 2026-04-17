# Desktop provisioning — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the five desktop install paths for cyoda — `go install`, GH Release archives, Homebrew tap, `curl | sh` installer, unsigned `.deb`/`.rpm` — with sqlite-default UX for packaged paths and OS-aware data-dir conventions across Linux, macOS, and Windows.

**Architecture:** Two small binary changes (OS-aware sqlite default path; env-file autoload from user + system config paths) enable the `cyoda init` subcommand that packaged installs rely on. GoReleaser's `nfpms:` and `brews:` stanzas produce the `.deb`/`.rpm` packages and the Homebrew formula; a hand-written `scripts/install.sh` serves the `curl | sh` path. One new docs file captures the maintainer one-time setup for the Homebrew tap and GitHub App.

**Tech Stack:** Go 1.26+, `log/slog`, `github.com/joho/godotenv` (already in use), GoReleaser + nFPM (already in `.goreleaser.yaml`), Homebrew formula DSL, POSIX `sh` for the installer, `shellcheck` for CI.

**Reference spec:** `docs/superpowers/specs/2026-04-17-provisioning-desktop-design.md`

---

## Prerequisites (manual, before this plan executes)

1. **Prerequisite A — rename.** `cyoda-go` → `cyoda` for user-facing artifact names (binary, image, chart, package, formula). This lands on the shared-layer PR #44 as additional commits **before #44 merges**. Files touched by the rename include #44's own newly-added files (`.goreleaser.yaml`, `release.yml`, `release-chart.yml`, `bump-chart-appversion.yml`, `examples/compose-with-observability/compose.yaml`, the `cmd/cyoda-go/` directory), plus pre-existing README / CONTRIBUTING / `printHelp()` references. Repo name, Go module path, plugin module paths, and `CYODA_*` env-var prefix are unchanged.

   This plan assumes the rename has already happened. All file paths below use `cmd/cyoda/` (not `cmd/cyoda-go/`); all user-facing strings say `cyoda`; the image is `ghcr.io/cyoda-platform/cyoda`.

2. **Prerequisite B — version reset.** Before the first desktop release cuts, a maintainer runs:

   ```bash
   # In cyoda-go-spi repo:
   for tag in $(git tag --list 'v*'); do git push --delete origin "$tag"; done
   git tag v0.1.0
   git push origin v0.1.0

   # In cyoda-go-cassandra repo:
   git push --delete origin v0.1.1
   git tag v0.1.0
   git push origin v0.1.0

   # In cyoda-go repo (after #44 merges to main):
   git tag plugins/memory/v0.1.0    # replaces any existing plugin tag if present
   git tag plugins/postgres/v0.1.0
   git tag plugins/sqlite/v0.1.0
   git push origin plugins/memory/v0.1.0 plugins/postgres/v0.1.0 plugins/sqlite/v0.1.0
   ```

   Safe because nothing has been consumed publicly (confirmed). These steps are manual, not part of this plan's TDD flow.

3. **Prerequisite C — one-time GitHub App setup.** Required before the first release triggers the Homebrew-publishing job. Documented in Task 7 of this plan.

---

## File structure

| File | Status | Responsibility |
|---|---|---|
| `plugins/sqlite/config.go` | modify | OS-aware exported `DefaultDBPath()` |
| `plugins/sqlite/config_test.go` | extend | Both OS branches via injected getenv / homedir |
| `app/envfiles.go` | modify | `LoadEnvFiles` searches system + user config paths; export `UserConfigPath()` / `SystemConfigPaths()` helpers |
| `app/envfiles_test.go` | extend | Per-OS path resolution; load-order precedence |
| `cmd/cyoda/main.go` | modify | Minimal subcommand dispatch; named import of sqlite plugin |
| `cmd/cyoda/init.go` | new | `cyoda init` subcommand logic |
| `cmd/cyoda/init_test.go` | new | Subcommand behavior (system config present, user exists, fresh write, --force) |
| `.goreleaser.yaml` | modify | `nfpms:` + `brews:` stanzas; unversioned `file_name_template` aliases for `releases/latest/download` URLs |
| `scripts/install.sh` | new | POSIX sh installer (OS/arch detect, checksum verify, `cyoda init`) |
| `.github/workflows/ci.yml` | modify | Add `shellcheck` step over `scripts/install.sh` |
| `README.md` | modify | Install + Configuration sections |
| `docs/MAINTAINING.md` | new | One-time Homebrew tap + GitHub App setup for maintainers |

---

## Task 1: OS-aware `DefaultDBPath()` in sqlite plugin

Replaces the Linux-XDG-only `defaultDBPath()` with a testable, exported OS-aware helper. Exports the function so `cmd/cyoda/init.go` can call it to write the absolute resolved path into the user config.

**Files:**
- Modify: `plugins/sqlite/config.go` (rename `defaultDBPath` → `DefaultDBPath`, OS-aware implementation via testable inner function)
- Modify: `plugins/sqlite/config_test.go` (new subtests for all branches)

- [ ] **Step 1: Write failing tests**

Append to `plugins/sqlite/config_test.go`:

```go
package sqlite

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDefaultDBPathResolved_LinuxWithXDG(t *testing.T) {
	got := defaultDBPathResolved("linux",
		func(key string) string {
			if key == "XDG_DATA_HOME" {
				return "/tmp/xdg"
			}
			return ""
		},
		func() (string, error) { return "/home/u", nil },
	)
	want := filepath.Join("/tmp/xdg", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_LinuxNoXDG(t *testing.T) {
	got := defaultDBPathResolved("linux",
		func(key string) string { return "" },
		func() (string, error) { return "/home/u", nil },
	)
	want := filepath.Join("/home/u", ".local", "share", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_macOSNoXDG(t *testing.T) {
	got := defaultDBPathResolved("darwin",
		func(key string) string { return "" },
		func() (string, error) { return "/Users/u", nil },
	)
	want := filepath.Join("/Users/u", ".local", "share", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_WindowsWithLocalAppData(t *testing.T) {
	got := defaultDBPathResolved("windows",
		func(key string) string {
			if key == "LocalAppData" {
				return `C:\Users\u\AppData\Local`
			}
			return ""
		},
		func() (string, error) { return `C:\Users\u`, nil },
	)
	want := filepath.Join(`C:\Users\u\AppData\Local`, "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_WindowsNoLocalAppData(t *testing.T) {
	got := defaultDBPathResolved("windows",
		func(key string) string { return "" },
		func() (string, error) { return `C:\Users\u`, nil },
	)
	want := filepath.Join(`C:\Users\u`, "AppData", "Local", "cyoda", "cyoda.db")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultDBPathResolved_HomeLookupFails(t *testing.T) {
	got := defaultDBPathResolved("linux",
		func(key string) string { return "" },
		func() (string, error) { return "", errors.New("no home") },
	)
	if got != "cyoda.db" {
		t.Fatalf("expected fallback %q, got %q", "cyoda.db", got)
	}
}

func TestDefaultDBPath_DelegatesToResolved(t *testing.T) {
	// DefaultDBPath should not panic and should return a non-empty path on the host OS.
	got := DefaultDBPath()
	if got == "" {
		t.Fatal("DefaultDBPath returned empty")
	}
	if !filepath.IsAbs(got) && got != "cyoda.db" {
		t.Fatalf("expected absolute path or literal fallback, got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd plugins/sqlite && go test -run DefaultDBPath -v`
Expected: FAIL — `defaultDBPathResolved` and `DefaultDBPath` don't exist yet (`defaultDBPath` does but is unexported).

- [ ] **Step 3: Rewrite `plugins/sqlite/config.go`'s path helper**

Replace the existing `defaultDBPath()` function with:

```go
// DefaultDBPath returns the per-OS default path for the sqlite database
// file. Linux and macOS share XDG semantics ($XDG_DATA_HOME/cyoda/cyoda.db,
// fallback ~/.local/share/cyoda/cyoda.db). Windows uses %LocalAppData%\cyoda\
// cyoda.db. Returns "cyoda.db" (current directory) when the user home
// directory cannot be determined.
func DefaultDBPath() string {
	return defaultDBPathResolved(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

// defaultDBPathResolved is the testable implementation of DefaultDBPath.
// Injecting goos, getenv, and home makes both OS branches reachable in
// tests regardless of the host platform.
func defaultDBPathResolved(goos string, getenv func(string) string, home func() (string, error)) string {
	if goos == "windows" {
		if local := getenv("LocalAppData"); local != "" {
			return filepath.Join(local, "cyoda", "cyoda.db")
		}
		h, err := home()
		if err != nil {
			return "cyoda.db"
		}
		return filepath.Join(h, "AppData", "Local", "cyoda", "cyoda.db")
	}
	if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "cyoda", "cyoda.db")
	}
	h, err := home()
	if err != nil {
		return "cyoda.db"
	}
	return filepath.Join(h, ".local", "share", "cyoda", "cyoda.db")
}
```

Add `"runtime"` to the imports in `plugins/sqlite/config.go` if it's not already present. Then update every caller in the package that used `defaultDBPath()` (lowercase) to call `DefaultDBPath()` (exported). Expect one call site in `config.go` itself — likely `Path: envStringFn(getenv, "CYODA_SQLITE_PATH", DefaultDBPath())`.

- [ ] **Step 4: Update `ConfigVars()` metadata**

Open `plugins/sqlite/plugin.go`. Find the `CYODA_SQLITE_PATH` entry:

```go
{Name: "CYODA_SQLITE_PATH", Description: "Database file path", Default: "$XDG_DATA_HOME/cyoda-go/cyoda.db"},
```

Change the `Default` field to reflect the per-OS reality. Use a short human-friendly summary rather than trying to encode both branches exactly:

```go
{Name: "CYODA_SQLITE_PATH", Description: "Database file path", Default: "$XDG_DATA_HOME/cyoda/cyoda.db (macOS: same; Windows: %LocalAppData%\\cyoda\\cyoda.db)"},
```

- [ ] **Step 5: Run tests to verify pass**

Run: `cd plugins/sqlite && go test -run DefaultDBPath -v`
Expected: all seven tests PASS.

- [ ] **Step 6: Run full plugin + root tests**

```bash
cd plugins/sqlite && go test ./...
cd ../.. && go test -short ./...
```
Expected: all green. (If the package previously exported nothing that referenced `defaultDBPath`, no external callers break.)

- [ ] **Step 7: Commit**

```bash
git add plugins/sqlite/config.go plugins/sqlite/config_test.go plugins/sqlite/plugin.go
git commit -m "$(cat <<'EOF'
feat(sqlite): OS-aware DefaultDBPath; export for use by cyoda init

Replaces the Linux-XDG-only defaultDBPath with an exported DefaultDBPath
branched on runtime.GOOS. Linux + macOS share XDG semantics; Windows
uses %LocalAppData%\cyoda\cyoda.db. An inner defaultDBPathResolved takes
injected getenv + homedir lookups so tests cover both branches on any
host OS without build tags.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Env-file autoload from user + system config paths

Extends `LoadEnvFiles()` to also consult system and user config paths per OS. Exports `UserConfigPath()` and `SystemConfigPaths()` helpers so `cyoda init` (Task 3) uses the same path resolution.

**Files:**
- Modify: `app/envfiles.go`
- Modify: `app/envfiles_test.go`

- [ ] **Step 1: Write failing tests for path helpers**

Append to `app/envfiles_test.go`:

```go
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

func TestSystemConfigPathsResolved_Linux(t *testing.T) {
	got := systemConfigPathsResolved("linux", func(key string) string { return "" })
	if len(got) != 1 || got[0] != "/etc/cyoda/cyoda.env" {
		t.Fatalf("got %v, want [/etc/cyoda/cyoda.env]", got)
	}
}

func TestSystemConfigPathsResolved_macOS(t *testing.T) {
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
	want := []string{filepath.Join(`C:\ProgramData`, "cyoda", "cyoda.env")}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSystemConfigPathsResolved_WindowsNoProgramData(t *testing.T) {
	got := systemConfigPathsResolved("windows", func(key string) string { return "" })
	if len(got) != 0 {
		t.Fatalf("got %v, want empty when ProgramData unset", got)
	}
}
```

Also append to the imports at the top of the file if not present: `"path/filepath"`.

- [ ] **Step 2: Write failing tests for LoadEnvFiles autoload from user config**

```go
func TestLoadEnvFiles_AutoloadsUserConfig(t *testing.T) {
	// Set up a temp dir as XDG_CONFIG_HOME and drop a cyoda/cyoda.env in it.
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cyoda")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(cfgDir, "cyoda.env")
	if err := os.WriteFile(cfgFile, []byte("CYODA_TEST_AUTOLOAD=from-user-config\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", tmp)
	os.Unsetenv("CYODA_TEST_AUTOLOAD")
	t.Cleanup(func() { os.Unsetenv("CYODA_TEST_AUTOLOAD") })

	// Run LoadEnvFiles from a directory that has no .env, so only the autoload path applies.
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
	t.Setenv("CYODA_TEST_OVERRIDE", "from-shell") // shell env already set

	wd := t.TempDir()
	prev, _ := os.Getwd()
	_ = os.Chdir(wd)
	t.Cleanup(func() { _ = os.Chdir(prev) })

	LoadEnvFiles()

	if got := os.Getenv("CYODA_TEST_OVERRIDE"); got != "from-shell" {
		t.Fatalf("shell env should win; got %q", got)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./app/ -run 'TestUserConfigPathResolved|TestSystemConfigPathsResolved|TestLoadEnvFiles_Autoloads|TestLoadEnvFiles_ShellEnvOverrides' -v`
Expected: FAIL — helpers don't exist, LoadEnvFiles doesn't autoload from user config.

- [ ] **Step 4: Add helper functions and extend LoadEnvFiles**

Edit `app/envfiles.go`. Add imports if not present: `"path/filepath"`, `"runtime"`.

Insert helper functions (place them below `LoadEnvFiles`, above the existing `splitProfiles`):

```go
// UserConfigPath returns the OS-appropriate path to the per-user cyoda
// config file (not necessarily existing on disk). Called by LoadEnvFiles
// and by the 'cyoda init' subcommand.
func UserConfigPath() string {
	return userConfigPathResolved(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func userConfigPathResolved(goos string, getenv func(string) string, home func() (string, error)) string {
	if goos == "windows" {
		if ad := getenv("AppData"); ad != "" {
			return filepath.Join(ad, "cyoda", "cyoda.env")
		}
		h, err := home()
		if err != nil {
			return ""
		}
		return filepath.Join(h, "AppData", "Roaming", "cyoda", "cyoda.env")
	}
	// Linux + macOS: XDG
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "cyoda", "cyoda.env")
	}
	h, err := home()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".config", "cyoda", "cyoda.env")
}

// SystemConfigPaths returns the OS-appropriate system-wide cyoda config
// paths (not necessarily existing on disk). macOS returns an empty slice
// intentionally — Homebrew formulas can't cleanly write to a system path.
func SystemConfigPaths() []string {
	return systemConfigPathsResolved(runtime.GOOS, os.Getenv)
}

func systemConfigPathsResolved(goos string, getenv func(string) string) []string {
	switch goos {
	case "linux":
		return []string{"/etc/cyoda/cyoda.env"}
	case "windows":
		if pd := getenv("ProgramData"); pd != "" {
			return []string{filepath.Join(pd, "cyoda", "cyoda.env")}
		}
		return nil
	default: // darwin and anything else
		return nil
	}
}
```

Modify `LoadEnvFiles` to merge system + user config paths into the `files` slice, in order. Replace the current body:

```go
func LoadEnvFiles() {
	profiles := splitProfiles(os.Getenv("CYODA_PROFILES"))

	// Load order (later values override earlier; shell env always wins):
	//  1. System config (per OS) — lowest precedence among loaded files
	//  2. User config (per OS)
	//  3. .env in CWD
	//  4. .env.<profile> in CWD (per profile, in declaration order)
	//  5. Shell environment (not loaded from a file; wins unconditionally)
	var files []string
	files = append(files, SystemConfigPaths()...)
	if u := UserConfigPath(); u != "" {
		files = append(files, u)
	}
	files = append(files, ".env")
	for _, p := range profiles {
		files = append(files, ".env."+p)
	}

	merged := make(map[string]string)
	var loaded []string
	for _, f := range files {
		vars, err := godotenv.Read(f)
		if err != nil {
			continue
		}
		loaded = append(loaded, f)
		for k, v := range vars {
			merged[k] = v
		}
	}

	applied := 0
	for k, v := range merged {
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
			applied++
		}
	}

	if len(loaded) > 0 {
		slog.Info("loaded env files",
			"files", loaded,
			"profiles", profiles,
			"vars_applied", applied,
		)
	} else if len(profiles) > 0 {
		slog.Warn("CYODA_PROFILES set but no .env files found",
			"profiles", profiles,
			"searched", files,
		)
	}
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./app/ -run 'TestUserConfigPathResolved|TestSystemConfigPathsResolved|TestLoadEnvFiles_Autoloads|TestLoadEnvFiles_ShellEnvOverrides' -v`
Expected: all PASS.

- [ ] **Step 6: Run full test suite**

```bash
go build ./...
go vet ./...
go test -short ./...
```
Expected: all green. Pre-existing `TestLoadEnvFiles*` tests should still pass since the new system/user paths don't exist in their temp dirs by default.

- [ ] **Step 7: Commit**

```bash
git add app/envfiles.go app/envfiles_test.go
git commit -m "$(cat <<'EOF'
feat(envfiles): autoload system + user config paths per OS

LoadEnvFiles now searches (in order, later wins): system config
(/etc/cyoda/cyoda.env on Linux, %ProgramData%\cyoda\cyoda.env on
Windows), user config (XDG_CONFIG_HOME on Linux/macOS, %AppData%\cyoda\
on Windows), CWD .env, CWD .env.<profile>. Shell env always wins.

Exports UserConfigPath() and SystemConfigPaths() so cyoda init can
check both for pre-existing configs before writing a user file.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `cyoda init` subcommand

Adds subcommand dispatch and the `init` subcommand. Minimal: no cobra — stdlib `flag` per subcommand is enough for a single subcommand.

**Files:**
- Modify: `cmd/cyoda/main.go` (subcommand dispatch; change blank import of sqlite plugin to a named import so `sqlite.DefaultDBPath()` is callable)
- Create: `cmd/cyoda/init.go`
- Create: `cmd/cyoda/init_test.go`

- [ ] **Step 1: Write failing tests**

Create `cmd/cyoda/init_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each init test runs with t.TempDir()-scoped XDG_CONFIG_HOME so no real
// user config is touched. On Linux/macOS XDG_CONFIG_HOME drives the user
// config path; system config paths are checked via overrides applied
// via t.Setenv on the relevant vars.

func setupIsolatedConfig(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// Override ProgramData on Windows so tests don't see a real system config.
	t.Setenv("ProgramData", filepath.Join(tmp, "no-such-programdata"))
	return tmp
}

func TestCyodaInit_WritesUserConfigFresh(t *testing.T) {
	xdg := setupIsolatedConfig(t)

	code := runInit([]string{})
	if code != 0 {
		t.Fatalf("runInit exit code = %d, want 0", code)
	}
	path := filepath.Join(xdg, "cyoda", "cyoda.env")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected config at %s, got %v", path, err)
	}
	content := string(data)
	if !strings.Contains(content, "CYODA_STORAGE_BACKEND=sqlite") {
		t.Errorf("missing CYODA_STORAGE_BACKEND=sqlite in:\n%s", content)
	}
	if !strings.Contains(content, "# CYODA_SQLITE_PATH=") {
		t.Errorf("missing commented CYODA_SQLITE_PATH in:\n%s", content)
	}
	// The commented CYODA_SQLITE_PATH must be a resolved absolute path — not a $XDG placeholder.
	if strings.Contains(content, "$XDG_DATA_HOME") {
		t.Errorf("found unresolved $XDG_DATA_HOME placeholder in:\n%s", content)
	}
}

func TestCyodaInit_ExitsZeroWhenUserConfigExists(t *testing.T) {
	xdg := setupIsolatedConfig(t)
	path := filepath.Join(xdg, "cyoda", "cyoda.env")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	// Pre-seed a marker the test can verify is preserved.
	if err := os.WriteFile(path, []byte("CYODA_MARKER=preserve-me\n"), 0600); err != nil {
		t.Fatal(err)
	}

	code := runInit([]string{})
	if code != 0 {
		t.Fatalf("runInit exit code = %d, want 0", code)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "CYODA_MARKER=preserve-me") {
		t.Errorf("existing file was clobbered; content: %s", data)
	}
}

func TestCyodaInit_ForceOverwritesUserConfig(t *testing.T) {
	xdg := setupIsolatedConfig(t)
	path := filepath.Join(xdg, "cyoda", "cyoda.env")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte("CYODA_MARKER=preserve-me\n"), 0600)

	code := runInit([]string{"--force"})
	if code != 0 {
		t.Fatalf("runInit exit code = %d, want 0", code)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "CYODA_MARKER=preserve-me") {
		t.Errorf("--force should have overwritten file; content: %s", data)
	}
	if !strings.Contains(string(data), "CYODA_STORAGE_BACKEND=sqlite") {
		t.Errorf("--force should have written a fresh config; content: %s", data)
	}
}

// System-config-precedence test skipped on Windows + non-Linux because /etc/cyoda
// is Linux-only and we can't easily override it from within a test.
// We verify the no-op path via the "no system file exists" happy path above.
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd cmd/cyoda && go test -run TestCyodaInit -v`
Expected: FAIL — `runInit` doesn't exist.

- [ ] **Step 3: Add subcommand dispatch in `cmd/cyoda/main.go`**

Find the existing early `--help`/`-h` short-circuit near the top of `main()`:

```go
if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
	printHelp()
	return
}
```

Replace with an extended dispatch block:

```go
if len(os.Args) > 1 {
	switch os.Args[1] {
	case "--help", "-h":
		printHelp()
		return
	case "init":
		os.Exit(runInit(os.Args[2:]))
	}
}
```

Change the sqlite plugin import from blank to named:

```go
// old
_ "github.com/cyoda-platform/cyoda-go/plugins/sqlite"
// new
sqliteplugin "github.com/cyoda-platform/cyoda-go/plugins/sqlite"
```

If `sqliteplugin` isn't referenced anywhere in `main.go`, `goimports`/`go vet` will warn (unused). We resolve that by calling `sqliteplugin.DefaultDBPath()` inside `init.go` — which lives in the same package.

- [ ] **Step 4: Create `cmd/cyoda/init.go`**

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cyoda-platform/cyoda-go/app"
	sqliteplugin "github.com/cyoda-platform/cyoda-go/plugins/sqlite"
)

// runInit implements the 'cyoda init' subcommand. See docs/superpowers/specs/
// 2026-04-17-provisioning-desktop-design.md for the design rationale.
//
// Exit codes:
//
//	0 — config written, or a config is already active (user or system)
//	1 — could not compute or write config (I/O error)
//	2 — bad flags
func runInit(args []string) int {
	fs := flag.NewFlagSet("cyoda init", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing user config")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// 1. If any system config file already exists, do nothing.
	for _, p := range app.SystemConfigPaths() {
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("system-wide cyoda config already present at %s; no user config needed (use --force to write a user config anyway)\n", p)
			if !*force {
				return 0
			}
		}
	}

	// 2. If user config exists and --force not set, do nothing.
	userPath := app.UserConfigPath()
	if userPath == "" {
		fmt.Fprintln(os.Stderr, "cyoda init: cannot compute user config path (no home directory detected)")
		return 1
	}
	if _, err := os.Stat(userPath); err == nil && !*force {
		fmt.Printf("config already exists at %s (use --force to overwrite)\n", userPath)
		return 0
	}

	// 3. Write user config.
	if err := os.MkdirAll(filepath.Dir(userPath), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "cyoda init: cannot create config directory: %v\n", err)
		return 1
	}
	content := fmt.Sprintf(`# cyoda user config — written by 'cyoda init'
# Shell-exported vars override values here.

CYODA_STORAGE_BACKEND=sqlite
# CYODA_SQLITE_PATH=%s   # uncomment to override
`, sqliteplugin.DefaultDBPath())
	if err := os.WriteFile(userPath, []byte(content), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "cyoda init: cannot write config: %v\n", err)
		return 1
	}
	fmt.Printf("wrote config to %s\n", userPath)
	return 0
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `cd cmd/cyoda && go test -run TestCyodaInit -v`
Expected: all three tests PASS.

- [ ] **Step 6: Update `printHelp()` in `cmd/cyoda/main.go`**

Find the `printHelp()` function body. Add an `init` subcommand entry in the USAGE block. Near the existing usage examples:

```
  cyoda                  Run the server with current config.
  cyoda init             Write a user config enabling sqlite persistence.
                         Idempotent; exits 0 if any config already active.
  cyoda --help           Show this help.
```

Exact placement depends on current `printHelp` layout — maintain the existing alignment convention (the Shared PR enforced column 73 for `(default:` anchors).

- [ ] **Step 7: Run full suite + build**

```bash
go build ./...
go vet ./...
go test -short ./...
```
Expected: all green. Smoke test the subcommand:

```bash
# Run in a scratch dir so we don't touch a real ~/.config/cyoda
tmp=$(mktemp -d)
XDG_CONFIG_HOME="$tmp" go run ./cmd/cyoda init
cat "$tmp/cyoda/cyoda.env"
# Expect CYODA_STORAGE_BACKEND=sqlite plus a commented CYODA_SQLITE_PATH with an absolute path
XDG_CONFIG_HOME="$tmp" go run ./cmd/cyoda init
# Expect: "config already exists at ... (use --force to overwrite)" and exit 0
rm -rf "$tmp"
```

- [ ] **Step 8: Commit**

```bash
git add cmd/cyoda/main.go cmd/cyoda/init.go cmd/cyoda/init_test.go
git commit -m "$(cat <<'EOF'
feat(cyoda): add 'cyoda init' subcommand

Minimal subcommand dispatch (stdlib flag, no cobra). 'cyoda init'
writes a starter user config with CYODA_STORAGE_BACKEND=sqlite and
a commented CYODA_SQLITE_PATH showing the absolute per-OS default
resolved at write time (not a $XDG placeholder the env-file parser
can't expand). Checks system config paths first and exits 0 if one
is already active; --force bypasses that check.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: GoReleaser `nfpms:` stanza for `.deb` / `.rpm`

Adds unsigned `.deb` and `.rpm` package generation with a `CYODA_STORAGE_BACKEND=sqlite` system config file. Uses an unversioned `file_name_template` so the README's `releases/latest/download/` URLs resolve stably.

**Files:**
- Modify: `.goreleaser.yaml`

No TDD — GoReleaser config is YAML without meaningful unit tests; verification is `goreleaser check`.

- [ ] **Step 1: Add `nfpms:` stanza**

Open `.goreleaser.yaml`. After the `archives:` block, add:

```yaml
nfpms:
  - id: cyoda
    package_name: cyoda
    vendor: Cyoda Platform
    homepage: https://github.com/cyoda-platform/cyoda-go
    maintainer: Cyoda Platform <noreply@cyoda.com>
    description: Lightweight Go EDBMS — digital twin of the Cyoda platform.
    license: Apache-2.0
    formats: [deb, rpm]
    bindir: /usr/bin
    file_name_template: "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"
    contents:
      - src: scripts/packaging/cyoda.env
        dst: /etc/cyoda/cyoda.env
        type: config|noreplace
```

The `file_name_template` emits filenames like `cyoda_linux_amd64.deb` (no version), which maps cleanly to `releases/latest/download/cyoda_linux_amd64.deb`. GoReleaser also preserves the default versioned names as additional assets on the release, so users pinning a specific version use `releases/download/v0.2.0/cyoda_0.2.0_linux_amd64.deb` (the default name).

- [ ] **Step 2: Create the system config template**

Create `scripts/packaging/cyoda.env`:

```
# cyoda system config — installed by the .deb/.rpm package.
# Shell-exported vars and per-user config override values here.
CYODA_STORAGE_BACKEND=sqlite
```

- [ ] **Step 3: Verify the config with `goreleaser check`**

```bash
goreleaser check
# or, without installing goreleaser:
docker run --rm -v "$(pwd)":/src -w /src goreleaser/goreleaser check
```
Expected: config valid. (If it complains about `deploy/docker/Dockerfile` missing — that's expected until the Docker per-target plan lands, and goreleaser `check` only enforces structural validity, not the existence of external files.)

- [ ] **Step 4: Smoke-build the packages locally (optional)**

```bash
goreleaser build --snapshot --clean --single-target
goreleaser release --snapshot --clean --skip=publish
# Artifacts land in ./dist; .deb and .rpm should be present.
ls dist/*.deb dist/*.rpm 2>/dev/null
```

Install the generated `.deb` into a disposable Debian container to verify contents:

```bash
docker run --rm -v "$(pwd)/dist":/dist debian:stable bash -c '
  dpkg -i /dist/cyoda_linux_amd64.deb &&
  test -x /usr/bin/cyoda &&
  test -f /etc/cyoda/cyoda.env &&
  grep -q "CYODA_STORAGE_BACKEND=sqlite" /etc/cyoda/cyoda.env &&
  echo OK
'
```
Expected: prints `OK`. Skip this smoke test if `goreleaser` isn't available locally — the release workflow will catch structural errors on the first tag.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml scripts/packaging/cyoda.env
git commit -m "$(cat <<'EOF'
feat(release): nFPM .deb/.rpm with unversioned filename template

Unsigned package downloads. /usr/bin/cyoda binary, /etc/cyoda/cyoda.env
system config (nFPM config|noreplace — preserves user edits across
upgrades). file_name_template emits cyoda_linux_amd64.{deb,rpm} so
README install one-liners use /releases/latest/download/<name>
stable URLs; versioned filenames still ship for version-pinning users.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: GoReleaser `brews:` stanza for Homebrew tap

Adds formula auto-generation to the `cyoda-platform/homebrew-cyoda-go` tap. The formula ships a `post_install` block running `cyoda init` automatically (idempotent per Task 3). Verification includes running `brew audit --strict` against the generated formula.

**Files:**
- Modify: `.goreleaser.yaml`
- Modify: `.github/workflows/release.yml` (adds the App-token minting step)

- [ ] **Step 1: Add `brews:` stanza**

After `nfpms:` in `.goreleaser.yaml`, add:

```yaml
brews:
  - name: cyoda
    repository:
      owner: cyoda-platform
      name: homebrew-cyoda-go
      token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"
    homepage: https://github.com/cyoda-platform/cyoda-go
    description: Lightweight Go EDBMS — digital twin of the Cyoda platform.
    license: Apache-2.0
    commit_author:
      name: cyoda-platform-release-bot
      email: noreply@cyoda.com
    test: |
      system "#{bin}/cyoda", "--help"
    install: |
      bin.install "cyoda"
    post_install: |
      system "#{bin}/cyoda", "init"
    caveats: |
      cyoda is configured to use sqlite with data stored in
      #{ENV["HOME"]}/.local/share/cyoda/cyoda.db

      If you want to reconfigure, run: cyoda init --force
      Or set CYODA_STORAGE_BACKEND in your shell environment.
```

`HOMEBREW_TAP_TOKEN` will be a short-lived installation token minted by a GitHub App (wired in Step 3 below).

- [ ] **Step 2: Wire the App-token minting into `release.yml`**

Open `.github/workflows/release.yml`. Before the `goreleaser/goreleaser-action` step (or wherever GoReleaser runs), add:

```yaml
      - name: Generate Homebrew tap token
        id: homebrew-tap-token
        uses: actions/create-github-app-token@v1
        with:
          app-id: ${{ secrets.HOMEBREW_TAP_APP_ID }}
          private-key: ${{ secrets.HOMEBREW_TAP_APP_KEY }}
          owner: cyoda-platform
          repositories: homebrew-cyoda-go
```

Then pass the minted token into GoReleaser's environment. Find the existing `goreleaser/goreleaser-action@v6` step and update its `env:` block to include:

```yaml
          HOMEBREW_TAP_TOKEN: ${{ steps.homebrew-tap-token.outputs.token }}
```

alongside the existing `GITHUB_TOKEN` and `GOWORK`.

- [ ] **Step 3: Run `brew audit --strict` against the generated formula**

A release must not be cut before the formula passes audit. Since the formula is generated by GoReleaser, we snapshot one locally and audit it:

```bash
goreleaser release --snapshot --clean --skip=publish
# Formula file lands in ./dist (look for cyoda.rb).

# Simulate a local tap install to run audit against:
mkdir -p /tmp/homebrew-cyoda-go/Formula
cp dist/cyoda.rb /tmp/homebrew-cyoda-go/Formula/cyoda.rb
brew tap cyoda-platform/cyoda-go /tmp/homebrew-cyoda-go
brew audit --strict cyoda-platform/cyoda-go/cyoda
# or, more comprehensively:
brew audit --strict --new --formula cyoda-platform/cyoda-go/cyoda
```
Expected: audit passes cleanly, or surfaces specific warnings that are investigated.

If `brew audit --strict` flags the `post_install` block, fall back per the P1 section of the design spec:
1. **First fallback: caveats-only with prominent formatting.** Remove `post_install:` from the `brews:` stanza. Update the `caveats:` block to lead with a visually distinct line so users skimming notice it. Example:
   ```yaml
   caveats: |
     ==============================================================
     Run 'cyoda init' to enable sqlite persistence.
     Without it, cyoda starts with the in-memory backend and
     loses data on every restart.
     ==============================================================
   ```
2. **Last resort: wrapper shell script in the formula's `bin/`.** See Task 5a below (implement only if Fallback 1 is also deemed insufficient).

Record the audit result and the choice made in the commit message for Step 5.

- [ ] **Step 4: Cleanup local tap and dist**

```bash
brew untap cyoda-platform/cyoda-go
rm -rf /tmp/homebrew-cyoda-go ./dist
```

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml .github/workflows/release.yml
git commit -m "$(cat <<'EOF'
feat(release): Homebrew formula with post_install 'cyoda init'

GoReleaser 'brews:' stanza auto-commits the cyoda.rb formula to
cyoda-platform/homebrew-cyoda-go on non-prerelease v* tags. Formula
ships a post_install block running 'cyoda init' automatically —
idempotent via init's exit-0-on-exists contract, so reinstalls and
upgrades don't fail. Caveats block covers the same ground as backup
documentation.

release.yml mints a short-lived installation token via
actions/create-github-app-token@v1 from the HOMEBREW_TAP_APP_ID +
HOMEBREW_TAP_APP_KEY secrets; no PAT involved.

brew audit --strict run locally against the snapshot formula —
audit clean (or: fell back to caveats-only — record actual
outcome).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5a (conditional): wrapper-script fallback for Homebrew

Only execute this task if both Task 5 Step 3 audit failed AND Task 5 Step 3's first fallback (caveats-only with prominent formatting) was deemed insufficient.

If you're here: GoReleaser's `brews:` stanza can't easily install a shell wrapper script. The cleanest path is a `post_install` block that itself creates the wrapper (which Homebrew audit may still flag), or a custom `install:` block. Consult the Homebrew Formula Cookbook and the GoReleaser Homebrew docs at implementation time — the exact wrapper-install pattern is environment-specific.

If you reach this fallback, document the decision path taken in the implementation commit and update the design spec's P1 section inline with "actual path chosen".

---

## Task 6: `scripts/install.sh`

POSIX sh installer. OS/arch detection, SHA256 verification, install to `~/.local/bin/cyoda`, invoke `cyoda init` (warn-continue on failure).

**Files:**
- Create: `scripts/install.sh`
- Modify: `.github/workflows/ci.yml` (add `shellcheck` over the script)

- [ ] **Step 1: Write `scripts/install.sh`**

```sh
#!/bin/sh
# cyoda installer — downloads the latest (or pinned) release for the
# current OS/arch, verifies SHA256, installs to ~/.local/bin/cyoda,
# and runs 'cyoda init' to enable sqlite persistence.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/cyoda-platform/cyoda-go/main/scripts/install.sh | sh
#
# Pin a version:
#   CYODA_VERSION=v0.2.0 curl -fsSL ... | sh
#
# Pin a different install directory:
#   CYODA_INSTALL_DIR=~/bin curl -fsSL ... | sh

set -eu

REPO="cyoda-platform/cyoda-go"
INSTALL_DIR="${CYODA_INSTALL_DIR:-$HOME/.local/bin}"

err() { printf 'install.sh: error: %s\n' "$*" >&2; }
warn() { printf 'install.sh: warning: %s\n' "$*" >&2; }
info() { printf '%s\n' "$*"; }

require() {
    for cmd in "$@"; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            err "required command not found: $cmd"
            exit 1
        fi
    done
}
require curl tar sha256sum

detect_os() {
    os=$(uname -s)
    case "$os" in
        Linux)  printf 'linux' ;;
        Darwin) printf 'darwin' ;;
        *)
            err "unsupported OS: $os (expected Linux or Darwin)"
            exit 1
            ;;
    esac
}

detect_arch() {
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) printf 'amd64' ;;
        aarch64|arm64) printf 'arm64' ;;
        *)
            err "unsupported arch: $arch (expected x86_64/amd64 or aarch64/arm64)"
            exit 1
            ;;
    esac
}

resolve_version() {
    if [ -n "${CYODA_VERSION:-}" ]; then
        printf '%s' "$CYODA_VERSION"
        return
    fi
    # GitHub API: latest non-prerelease.
    curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' \
        | head -n1
}

main() {
    os=$(detect_os)
    arch=$(detect_arch)
    version=$(resolve_version)
    if [ -z "$version" ]; then
        err "could not resolve latest version from GitHub API"
        exit 1
    fi
    version_bare="${version#v}"

    info "Installing cyoda $version for $os/$arch into $INSTALL_DIR"

    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT

    archive="cyoda_${version_bare}_${os}_${arch}.tar.gz"
    url="https://github.com/$REPO/releases/download/$version/$archive"
    sums_url="https://github.com/$REPO/releases/download/$version/SHA256SUMS"

    info "Downloading $url"
    if ! curl -fsSL -o "$tmp/$archive" "$url"; then
        err "download failed: $url"
        exit 1
    fi
    if ! curl -fsSL -o "$tmp/SHA256SUMS" "$sums_url"; then
        err "download failed: $sums_url"
        exit 1
    fi

    info "Verifying checksum"
    (cd "$tmp" && grep " $archive\$" SHA256SUMS | sha256sum -c -) || {
        err "SHA256 verification failed for $archive"
        exit 1
    }

    info "Extracting"
    tar -xzf "$tmp/$archive" -C "$tmp"

    mkdir -p "$INSTALL_DIR"
    mv "$tmp/cyoda" "$INSTALL_DIR/cyoda"
    chmod +x "$INSTALL_DIR/cyoda"

    # Warn if INSTALL_DIR isn't on PATH.
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) : ;;
        *)
            warn "$INSTALL_DIR is not on your PATH."
            warn "Add it by running (for bash):"
            warn "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc"
            warn "or (for zsh):"
            warn "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.zshrc"
            ;;
    esac

    info "Running cyoda init"
    if ! "$INSTALL_DIR/cyoda" init; then
        warn "cyoda init failed; cyoda is installed but no user config was written."
        warn "Re-run 'cyoda init' manually once the issue is resolved."
    fi

    info ""
    info "cyoda $version installed."
    info "Start with:"
    info "  cyoda"
    info "See README:"
    info "  https://github.com/$REPO#quick-start"
}

main "$@"
```

Make it executable:

```bash
chmod +x scripts/install.sh
```

- [ ] **Step 2: Run `shellcheck` locally**

```bash
shellcheck scripts/install.sh
```
Expected: clean output (or specific warnings with remediation). If not installed: `brew install shellcheck` or `apt install shellcheck`.

Fix any warnings before proceeding.

- [ ] **Step 3: Add `shellcheck` to CI**

Open `.github/workflows/ci.yml`. Find a place after the Go test matrix (or add a new job). Add:

```yaml
  shellcheck:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: shellcheck
        run: |
          sudo apt-get update && sudo apt-get install -y shellcheck
          shellcheck scripts/install.sh
```

Verify YAML parses:

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"
```

- [ ] **Step 4: Smoke-test the script against a fake release**

If a real v0.1.0 release doesn't yet exist (it won't at this point), skip Step 4 or manually set up a fake release. The script's real validation is the integration test that runs after the first `v*` tag publishes.

Optional manual test once a real release exists:

```bash
tmp=$(mktemp -d)
CYODA_INSTALL_DIR="$tmp/bin" XDG_CONFIG_HOME="$tmp/cfg" sh scripts/install.sh
"$tmp/bin/cyoda" --help
cat "$tmp/cfg/cyoda/cyoda.env"
rm -rf "$tmp"
```

- [ ] **Step 5: Commit**

```bash
git add scripts/install.sh .github/workflows/ci.yml
git commit -m "$(cat <<'EOF'
feat(installer): curl|sh installer with checksum verification

POSIX sh. Detects OS/arch, downloads latest (or CYODA_VERSION-pinned)
release, verifies SHA256, installs to ~/.local/bin/cyoda, and runs
'cyoda init' to enable sqlite. Warns if ~/.local/bin is not on PATH.
Treats 'cyoda init' failure as warn-continue — binary is installed
correctly even if config writing hiccups.

CI runs shellcheck on every push.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Maintainer guide for Homebrew tap + GitHub App setup

One-time setup steps the maintainer follows before the first release triggers the Homebrew-publishing job. Lives in `docs/MAINTAINING.md` so maintainers can find it.

**Files:**
- Create: `docs/MAINTAINING.md`

No TDD — pure documentation.

- [ ] **Step 1: Create `docs/MAINTAINING.md`**

```markdown
# Maintaining cyoda-go

Notes for cyoda-go maintainers on tasks that aren't part of the regular
development workflow.

## One-time setup: Homebrew tap release automation

Before the first `v*` tag triggers the GoReleaser Homebrew-publishing job,
these steps must be completed once.

### 1. Create the empty tap repository

- New repo: `cyoda-platform/homebrew-cyoda-go` (public, empty).
- README.md in the tap repo: a short paragraph explaining the tap and
  linking to the main repo. GoReleaser will push the `cyoda.rb` formula
  on every release.

### 2. Create the GitHub App

A GitHub App (not a personal access token) mints short-lived installation
tokens for release automation. Advantages over a PAT: org-owned, no human
account tied to it, no expiration to track.

1. Navigate to `https://github.com/organizations/cyoda-platform/settings/apps`.
2. Click **New GitHub App**.
3. Fill in:
   - App name: `cyoda-platform-release-bot` (must be globally unique; add
     a suffix if taken).
   - Homepage URL: `https://github.com/cyoda-platform/cyoda-go`
   - Webhook: uncheck "Active" (no webhook needed).
   - Permissions → Repository permissions:
     - Contents: Read and write
   - Permissions → Account permissions: (none)
   - Where can this GitHub App be installed?: "Only on this account".
4. Click **Create GitHub App**.
5. After creation, at the top of the App settings page, note the **App ID**
   (a 6-7 digit number).
6. Scroll to **Private keys** and click **Generate a private key**. A
   `.pem` file downloads.

### 3. Install the App on the tap repo

1. On the App settings page, click **Install App** in the left sidebar.
2. Choose the `cyoda-platform` org.
3. Under **Repository access**, select **Only select repositories** and
   add `cyoda-platform/homebrew-cyoda-go`. Do NOT install on the whole
   org.
4. Click **Install**.

### 4. Configure secrets in the cyoda-go repo

1. Navigate to `https://github.com/cyoda-platform/cyoda-go/settings/secrets/actions`.
2. Add secret `HOMEBREW_TAP_APP_ID`: the numeric App ID from step 2.5.
3. Add secret `HOMEBREW_TAP_APP_KEY`: the full contents of the `.pem`
   file from step 2.6, including the `-----BEGIN PRIVATE KEY-----` and
   `-----END PRIVATE KEY-----` lines.
4. Delete the local `.pem` file. The private key only needs to live in
   the Actions secret from now on.

### 5. Verify

On the next non-prerelease `v*` tag push, the release workflow's
"Generate Homebrew tap token" step mints a short-lived installation token,
GoReleaser uses it to push the formula to `homebrew-cyoda-go`, and the
tap's commit history shows `cyoda-platform-release-bot` as the author.

### Rotation

If the private key is compromised or needs rotation:

1. App settings → **Private keys** → **Generate a private key** for a new key.
2. Immediately update `HOMEBREW_TAP_APP_KEY` in the cyoda-go repo secrets.
3. App settings → delete the old private key.

No release-job code changes are needed — the App ID is stable across
rotations.

## One-time setup: version reset across coordinated repos

Before the first public release cuts, existing pre-public tags in the
three coordinated repos are deleted and recreated at `v0.1.0`. See the
desktop provisioning spec
(`docs/superpowers/specs/2026-04-17-provisioning-desktop-design.md`)
Prerequisite B for the exact commands.
```

- [ ] **Step 2: Commit**

```bash
git add docs/MAINTAINING.md
git commit -m "$(cat <<'EOF'
docs(maintainers): one-time setup for Homebrew tap and GitHub App

Captures the manual steps a maintainer runs before the first release
triggers Homebrew publishing: create the tap repo, create the GitHub
App, install it on the tap repo, store the App ID + private key as
Actions secrets. Also points at the version-reset section of the
desktop spec.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: README Install + Configuration sections

User-facing documentation: the five install paths with copy-paste one-liners, plus a Configuration subsection explaining env-file autoload.

**Files:**
- Modify: `README.md`

No TDD — documentation. Verification: every listed URL must resolve to a real asset after the first release cuts.

- [ ] **Step 1: Add Install section near the top of README.md**

Find the existing "Quick Start" section (or equivalent). Before it, add a new `## Install` section:

```markdown
## Install

### macOS or Linux via Homebrew

```bash
brew install cyoda-platform/cyoda-go/cyoda
```

The formula automatically runs `cyoda init` after install, enabling
sqlite persistence with data in `~/.local/share/cyoda/cyoda.db`.

### Any Unix via curl

```bash
curl -fsSL https://raw.githubusercontent.com/cyoda-platform/cyoda-go/main/scripts/install.sh | sh
```

Installs to `~/.local/bin/cyoda` and runs `cyoda init`. Pin a specific
version with `CYODA_VERSION=v0.2.0 curl ... | sh`. Pin a different
install directory with `CYODA_INSTALL_DIR=~/bin curl ... | sh`.

### Debian or Ubuntu

```bash
wget https://github.com/cyoda-platform/cyoda-go/releases/latest/download/cyoda_linux_amd64.deb
sudo dpkg -i cyoda_linux_amd64.deb
```

Drops `/usr/bin/cyoda` and `/etc/cyoda/cyoda.env` (system-wide sqlite
default). Replace `amd64` with `arm64` for ARM. To pin a specific
version: `wget https://github.com/cyoda-platform/cyoda-go/releases/download/v0.2.0/cyoda_0.2.0_linux_amd64.deb`.

### Fedora or RHEL

```bash
wget https://github.com/cyoda-platform/cyoda-go/releases/latest/download/cyoda_linux_amd64.rpm
sudo rpm -i cyoda_linux_amd64.rpm
```

### From source

```bash
go install github.com/cyoda-platform/cyoda-go/cmd/cyoda@latest
```

Uses the binary's compiled-in `memory` default. Set
`CYODA_STORAGE_BACKEND=sqlite` or run `cyoda init` for persistence.
```

- [ ] **Step 2: Add Configuration subsection**

Below the Install section, add:

```markdown
### Configuration

cyoda reads config from these sources, in increasing order of precedence
(later values override earlier):

1. Compiled-in defaults (memory backend, port 8080, mock auth).
2. System config file: `/etc/cyoda/cyoda.env` on Linux, `%ProgramData%\cyoda\cyoda.env`
   on Windows. macOS has no system config path.
3. User config file: `~/.config/cyoda/cyoda.env` on Linux + macOS,
   `%AppData%\cyoda\cyoda.env` on Windows.
4. `.env` and `.env.<profile>` in the current working directory (profiles
   selected by `CYODA_PROFILES=...`). See
   [`.env.sqlite.example`](.env.sqlite.example), [`.env.postgres.example`](.env.postgres.example),
   [`.env.local.example`](.env.local.example), [`.env.jwt.example`](.env.jwt.example).
5. Shell environment variables (always win).

Run `cyoda init` to write a starter user config with sqlite enabled.
Run `cyoda --help` for the full list of env vars.
```

- [ ] **Step 3: Verify internal links resolve**

```bash
ls .env.sqlite.example .env.postgres.example .env.local.example .env.jwt.example
```
All four must exist. If any is missing from a prior task's work, create or fix — out-of-scope if all four are already present.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs(readme): Install + Configuration sections for desktop provisioning

Five install paths (Homebrew, curl|sh, .deb, .rpm, go install) with
copy-paste one-liners. URLs use /releases/latest/download/ for the
.deb/.rpm rows so the README doesn't rot on every release; versioned
URLs documented for users pinning a specific version.

Configuration subsection documents the autoload hierarchy (system
config → user config → CWD .env → shell env) and links to the four
.env.*.example files.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## End-of-deliverable verification

After all eight tasks, before creating the PR:

- [ ] Run the race detector once: `go test -race ./...` — must be clean. (Per project convention, `-race` is an end-of-deliverable check, not a per-step gate.)
- [ ] Run E2E suite: `go test ./internal/e2e/... -v` (requires Docker).
- [ ] Confirm `git status` is clean.
- [ ] Confirm `go mod tidy` produces no diff.
- [ ] Smoke test `scripts/install.sh` locally if possible (requires a real release; skip if not yet available).
- [ ] Skim `docs/MAINTAINING.md` for typos and broken links.

## Out of scope (reminder — deferred, per spec)

- Windows packaging beyond `.zip` (Scoop, Chocolatey, Winget, MSI).
- Signed `.deb`/`.rpm` + hosted apt/rpm repository.
- `brew services` integration.
- systemd unit in `.deb`/`.rpm`.
- macOS `.pkg` installer.
- `cyoda keygen` or JWT generation in `cyoda init`.
- cosign verification in `install.sh`.
- `install.cyoda.io` redirect.
- Service integration on any platform.
