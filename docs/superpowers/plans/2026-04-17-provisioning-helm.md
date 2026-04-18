# Helm Provisioning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a production-ready v0.1 Helm chart for cyoda-go at `deploy/helm/cyoda/`, the small binary-side changes it depends on (a new `cyoda migrate` subcommand, `_FILE`-suffix support for credential env vars, a bootstrap-secret tightening), the chart CI that validates it on every PR, and the two release workflows that publish it.

**Architecture:** Gateway API first-class routing (HTTPRoute + GRPCRoute, parentRefs into an operator-provided shared Gateway), StatefulSet always (cluster-mode always on, HMAC auto-generated via lookup with a GitOps-safety guard), projected-volume secret mounting via `_FILE` env vars, migration run in a pre-install/pre-upgrade Helm hook Job. Postgres-only backend; zero chart dependencies. Chart publishes to GitHub Pages via `helm/chart-releaser-action` on `cyoda-*` tags. See `docs/superpowers/specs/2026-04-17-provisioning-helm-design.md` for the design rationale and `docs/superpowers/specs/2026-04-16-provisioning-shared-design.md` for shared foundations.

**Tech Stack:** Go 1.26+, `log/slog`, Helm v3.16+, Kubernetes 1.31+, Gateway API v1.2, `hashicorp/memberlist`, `kubeconform`, `chart-testing` (`ct`), `helm/chart-releaser-action`, `kind` + Envoy Gateway for CI.

---

## Prerequisites

Before starting Task 1, verify:

- Working tree is clean: `git status` shows only untracked `.worktrees/`.
- Go toolchain installed: `go version` reports 1.26 or newer.
- Docker Desktop or equivalent running (needed for E2E tests that use testcontainers and for the local kind-based chart install smoke test).
- `helm` v3.16+ installed locally (verify: `helm version --short`).
- `kubectl` installed locally for manual verification steps.

If any is missing, install before proceeding — tests in this plan assume they work.

---

## File structure

### Binary side (Go)

```
app/
  config_secret_env.go              (NEW) — resolveSecretEnv helper
  config_secret_env_test.go         (NEW) — unit tests for helper
  config.go                         (MODIFY) — call resolveSecretEnv at 4 credential sites
  config_test.go                    (MODIFY) — add coverage for _FILE precedence + trimming
  app.go                            (MODIFY) — tighten bootstrap-secret handling
  app_bootstrap_test.go             (NEW) — tests for the tightened behavior

cmd/cyoda/
  main.go                           (MODIFY) — add "migrate" dispatch case + update printHelp
  migrate.go                        (NEW) — runMigrate implementation
  migrate_test.go                   (NEW) — unit + e2e tests for migrate subcommand

README.md                           (MODIFY) — document new _FILE suffix support + migrate subcommand
```

### Chart (Helm)

```
deploy/helm/cyoda/
  Chart.yaml                        (NEW)
  .helmignore                       (NEW)
  values.yaml                       (NEW)
  values.schema.json                (NEW)
  README.md                         (REPLACE — placeholder → full operator doc)
  templates/
    _helpers.tpl                    (NEW)
    NOTES.txt                       (NEW)
    serviceaccount.yaml             (NEW)
    service.yaml                    (NEW)
    service-headless.yaml           (NEW)
    configmap.yaml                  (NEW)
    secret-hmac.yaml                (NEW)
    secret-bootstrap.yaml           (NEW)
    statefulset.yaml                (NEW)
    pdb.yaml                        (NEW)
    job-migrate.yaml                (NEW)
    servicemonitor.yaml             (NEW)
    networkpolicy.yaml              (NEW)
    gateway-httproute.yaml          (NEW)
    gateway-grpcroute.yaml          (NEW)
    ingress-http.yaml               (NEW)
    ingress-grpc.yaml               (NEW)
    tests/
      test-readyz.yaml              (NEW)
```

### CI and release workflows

```
.github/
  ct.yaml                           (NEW) — chart-testing config
  workflows/
    helm-chart-ci.yml               (NEW) — lint + template + kubeconform + ct install
    release-chart.yml               (MODIFY) — activate from pre-stub
    bump-chart-appversion.yml       (MODIFY) — activate from pre-stub

MAINTAINING.md                      (MODIFY or NEW) — document Pages prerequisite
```

---

## Task 1: `resolveSecretEnv` helper and `_FILE` suffix support

Spec reference: design §4 "`_FILE` suffix support in the binary".

**Files:**
- Create: `app/config_secret_env.go`
- Create: `app/config_secret_env_test.go`
- Modify: `app/config.go` (four credential call sites)
- Modify: `app/config_test.go` (add precedence + trimming test coverage)

### Step 1: Write the failing tests for `resolveSecretEnv`

- [ ] Create `app/config_secret_env_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSecretEnv_PlainEnvOnly(t *testing.T) {
	t.Setenv("TEST_SECRET", "plain-value")
	t.Setenv("TEST_SECRET_FILE", "")

	got, err := resolveSecretEnv("TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "plain-value" {
		t.Errorf("want %q, got %q", "plain-value", got)
	}
}

func TestResolveSecretEnv_FileOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("file-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET", "")
	t.Setenv("TEST_SECRET_FILE", path)

	got, err := resolveSecretEnv("TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file-value" {
		t.Errorf("want trimmed %q, got %q", "file-value", got)
	}
}

func TestResolveSecretEnv_FileWinsOverPlain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("from-file"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET", "from-env")
	t.Setenv("TEST_SECRET_FILE", path)

	got, err := resolveSecretEnv("TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-file" {
		t.Errorf("_FILE must win; want %q, got %q", "from-file", got)
	}
}

func TestResolveSecretEnv_FileUnreadable(t *testing.T) {
	t.Setenv("TEST_SECRET", "")
	t.Setenv("TEST_SECRET_FILE", "/nonexistent/path/to/secret")

	_, err := resolveSecretEnv("TEST_SECRET")
	if err == nil {
		t.Fatal("expected error reading nonexistent file, got nil")
	}
}

func TestResolveSecretEnv_TrimsTrailingWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("value\n\n   \r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET", "")
	t.Setenv("TEST_SECRET_FILE", path)

	got, err := resolveSecretEnv("TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "value" {
		t.Errorf("trailing whitespace not trimmed; want %q, got %q", "value", got)
	}
}

func TestResolveSecretEnv_EmptyFileTreatedAsUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("   \n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET", "")
	t.Setenv("TEST_SECRET_FILE", path)

	got, err := resolveSecretEnv("TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("file with only whitespace should return empty; got %q", got)
	}
}

func TestResolveSecretEnv_NeitherSet(t *testing.T) {
	t.Setenv("TEST_SECRET", "")
	t.Setenv("TEST_SECRET_FILE", "")

	got, err := resolveSecretEnv("TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("neither set should return empty; got %q", got)
	}
}
```

### Step 2: Run tests to confirm they fail

- [ ] Run: `go test ./app/ -run TestResolveSecretEnv -v`
  Expected: compile error `undefined: resolveSecretEnv`.

### Step 3: Implement the helper

- [ ] Create `app/config_secret_env.go`:

```go
package app

import (
	"fmt"
	"os"
	"strings"
)

// resolveSecretEnv returns the value of the named env var, OR — if that
// env var is empty and <name>_FILE is set — reads the value from the
// file at the path given by <name>_FILE.
//
// Precedence: <name>_FILE wins if both are set (documented and tested).
// The _FILE path is the canonical Docker/Kubernetes pattern for passing
// credentials without exposing them in `env` output.
//
// Trailing whitespace (spaces, tabs, \n, \r) is stripped from file
// contents — safe for both DSN strings and multi-line PEM keys. A file
// whose contents trim to empty is treated as unset (the caller's
// normal downstream validation reports the real problem).
//
// Errors: returned only when <name>_FILE points at a path that cannot
// be read. Silent fallthrough to empty would let a typo'd path look
// like a missing credential, which is hard to debug.
func resolveSecretEnv(name string) (string, error) {
	fileVar := name + "_FILE"
	if path := os.Getenv(fileVar); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s=%q: %w", fileVar, path, err)
		}
		return strings.TrimRight(string(data), " \t\n\r"), nil
	}
	return os.Getenv(name), nil
}
```

### Step 4: Run tests to confirm they pass

- [ ] Run: `go test ./app/ -run TestResolveSecretEnv -v`
  Expected: all 7 tests PASS.

### Step 5: Apply `_FILE` suffix to the four credential sites in `config.go`

- [ ] Locate the four call sites in `app/config.go` (grep will find them):
  - `CYODA_POSTGRES_URL` — DSN
  - `CYODA_JWT_SIGNING_KEY` — PEM
  - `CYODA_HMAC_SECRET` — HMAC key
  - `CYODA_BOOTSTRAP_CLIENT_SECRET` — M2M secret

  Actual line numbers may drift; use `grep -n` to find the current locations. Each site currently calls one of the existing env helpers (`envString`, `envPEM`, or a direct `os.Getenv`). Wrap each with `resolveSecretEnv` so the `_FILE` pattern is honored.

- [ ] Verify `CYODA_POSTGRES_URL` is actually resolved in the postgres plugin, not the app package. The app package reads IAM + cluster + bootstrap; DSN reads from the plugin config. Apply the same `resolveSecretEnv` pattern there. Grep: `grep -rn "CYODA_POSTGRES_URL" plugins/postgres/`.

Example modification pattern for `app/config.go`:

```go
// before:
// JWTSigningKey: envPEM("CYODA_JWT_SIGNING_KEY"),

// after:
jwtKey, err := resolveSecretEnv("CYODA_JWT_SIGNING_KEY")
if err != nil {
    return nil, err
}
// (then normalize to PEM the same way envPEM did)
cfg.IAM.JWTSigningKey = jwtKey
```

For the plugin site, use the same pattern but with the plugin's local helper structure. Keep the changes minimal — no refactoring of surrounding code beyond the credential resolution.

### Step 6: Write test coverage for `config.go` integration

- [ ] Open `app/config_test.go` (create if needed). Add a test that verifies a `_FILE` path works end-to-end through the config loader for one representative credential (JWT signing key — the most complex since it's multi-line PEM):

```go
func TestLoadConfig_JWTSigningKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	pemPath := filepath.Join(dir, "jwt-signing-key.pem")
	pem := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBAD...\n-----END PRIVATE KEY-----\n"
	if err := os.WriteFile(pemPath, []byte(pem), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CYODA_JWT_SIGNING_KEY", "")
	t.Setenv("CYODA_JWT_SIGNING_KEY_FILE", pemPath)
	t.Setenv("CYODA_IAM_MODE", "jwt")

	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig failed: %v", err)
	}
	if !strings.Contains(cfg.IAM.JWTSigningKey, "BEGIN PRIVATE KEY") {
		t.Errorf("expected PEM content loaded via _FILE; got %q", cfg.IAM.JWTSigningKey)
	}
}
```

(Function name `DefaultConfig` is illustrative — use the actual constructor. Check the current name first with `grep -n "^func.*Config" app/config.go`.)

### Step 7: Run the full app package tests

- [ ] Run: `go test -short ./app/... -v`
  Expected: all tests PASS, including the new `_FILE` coverage.

- [ ] Run: `go test -short ./plugins/postgres/... -v`
  Expected: all tests PASS if you modified postgres DSN resolution (skip if not).

### Step 8: Commit

- [ ] Run:

```bash
git add app/config_secret_env.go app/config_secret_env_test.go \
        app/config.go app/config_test.go
# plus plugins/postgres/ files if DSN resolution lives there
git commit -m "feat(config): _FILE suffix support for credential env vars

Add resolveSecretEnv helper that reads <VAR>_FILE as a path when <VAR>
is unset, with _FILE taking precedence when both are set. Trailing
whitespace is stripped (safe for DSN strings and multi-line PEM keys).

Applied at four credential sites: CYODA_POSTGRES_URL,
CYODA_JWT_SIGNING_KEY, CYODA_HMAC_SECRET, CYODA_BOOTSTRAP_CLIENT_SECRET.
Canonical Docker/Kubernetes pattern (postgres, mysql, redis all use it)
for passing secrets without exposing them in env output.

Required by the Helm chart (projected-volume secret mounting)."
```

---

## Task 2: Tighten bootstrap client secret handling

Spec reference: design §4 "Bootstrap secret tightening (binary side)".

**Behavior change summary:**
- Remove the stdout print on auto-generate.
- In `jwt` mode, `CYODA_BOOTSTRAP_CLIENT_SECRET` is required (fatal startup error if unset).
- In `mock` mode, `CYODA_BOOTSTRAP_CLIENT_SECRET` is ignored.
- No env-driven behavior change: the chart always provides the env.

**Files:**
- Modify: `app/app.go` (bootstrap-secret handling path)
- Create: `app/app_bootstrap_test.go`

### Step 1: Find the current auto-generate-and-print code

- [ ] Run: `grep -n "BOOTSTRAP_CLIENT_SECRET\|bootstrap.*secret\|generated.*secret\|print.*secret" app/app.go`
  Note the current location. Read the surrounding function to understand how IAM mode is detected in context.

### Step 2: Write the failing tests

- [ ] Create `app/app_bootstrap_test.go`:

```go
package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestBootstrapSecret_JwtModeRequired verifies that in jwt mode, an
// unset CYODA_BOOTSTRAP_CLIENT_SECRET is a fatal startup error.
func TestBootstrapSecret_JwtModeRequired(t *testing.T) {
	t.Setenv("CYODA_IAM_MODE", "jwt")
	t.Setenv("CYODA_JWT_SIGNING_KEY", testPEMFixture(t))
	t.Setenv("CYODA_BOOTSTRAP_CLIENT_SECRET", "")

	_, err := validateBootstrapConfig(testConfigWithJWT(t))
	if err == nil {
		t.Fatal("expected error when CYODA_BOOTSTRAP_CLIENT_SECRET unset in jwt mode; got nil")
	}
	if !strings.Contains(err.Error(), "CYODA_BOOTSTRAP_CLIENT_SECRET") {
		t.Errorf("error should name the missing env var; got: %v", err)
	}
}

// TestBootstrapSecret_MockModeIgnored verifies that mock mode doesn't
// require the bootstrap secret.
func TestBootstrapSecret_MockModeIgnored(t *testing.T) {
	t.Setenv("CYODA_IAM_MODE", "mock")
	t.Setenv("CYODA_BOOTSTRAP_CLIENT_SECRET", "")

	_, err := validateBootstrapConfig(testConfigWithMock(t))
	if err != nil {
		t.Errorf("mock mode should not require bootstrap secret; got: %v", err)
	}
}

// TestBootstrapSecret_NoStdoutPrint verifies that no secret value is
// ever written to stdout or the slog default handler. Captures any
// output produced during Bootstrap initialization with a configured
// secret, then asserts the secret value does not appear.
func TestBootstrapSecret_NoStdoutPrint(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const secret = "canary-secret-value-must-not-appear-in-logs"
	t.Setenv("CYODA_IAM_MODE", "jwt")
	t.Setenv("CYODA_JWT_SIGNING_KEY", testPEMFixture(t))
	t.Setenv("CYODA_BOOTSTRAP_CLIENT_SECRET", secret)

	if _, err := validateBootstrapConfig(testConfigWithJWT(t)); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if strings.Contains(buf.String(), secret) {
		t.Errorf("bootstrap client secret MUST NOT appear in logs; output:\n%s", buf.String())
	}
}

// Helper function stubs — replace with the actual config constructor
// and PEM fixture loader used elsewhere in the app package.
func testPEMFixture(t *testing.T) string { t.Helper(); return "<test PEM>" }
func testConfigWithJWT(t *testing.T) *Config { t.Helper(); return &Config{} }
func testConfigWithMock(t *testing.T) *Config { t.Helper(); return &Config{} }
```

**Note on test shape:** the exact helper names depend on the existing test infrastructure in the `app` package. Before committing, replace `testPEMFixture`, `testConfigWithJWT`, `testConfigWithMock`, and `validateBootstrapConfig` with whatever the existing code actually provides. Find them with `grep -n "^func test\|^func Test" app/app_test.go` and `grep -n "validate\|Validate" app/app.go`. If `validateBootstrapConfig` doesn't exist, the test is driving you to extract it — that's intentional.

### Step 3: Run tests to confirm they fail

- [ ] Run: `go test ./app/ -run TestBootstrapSecret -v`
  Expected: FAIL — either `validateBootstrapConfig` doesn't exist, or the existing code auto-generates silently (TestBootstrapSecret_JwtModeRequired fails), or prints the secret (TestBootstrapSecret_NoStdoutPrint fails).

### Step 4: Implement the tightening in `app/app.go`

- [ ] Locate the existing code that handles `CYODA_BOOTSTRAP_CLIENT_SECRET`. Replace the auto-generate-and-print logic with a required-in-jwt-mode validation. Extract the validation into `validateBootstrapConfig` if not already a testable function.

The change is approximately:

```go
// Before (illustrative — actual code may differ):
// if cfg.Bootstrap.ClientSecret == "" {
//     cfg.Bootstrap.ClientSecret = randomHex(32)
//     fmt.Printf("generated bootstrap client secret: %s\n", cfg.Bootstrap.ClientSecret)
// }

// After:
func validateBootstrapConfig(cfg *Config) (*Config, error) {
    if cfg.IAM.Mode != "jwt" {
        // Mock mode: bootstrap secret is irrelevant; zero it to avoid any
        // accidental use and return.
        cfg.Bootstrap.ClientSecret = ""
        return cfg, nil
    }
    if cfg.Bootstrap.ClientSecret == "" {
        return nil, fmt.Errorf(
            "CYODA_BOOTSTRAP_CLIENT_SECRET is required when CYODA_IAM_MODE=jwt; " +
                "set it explicitly (e.g. via a Kubernetes Secret) or switch to CYODA_IAM_MODE=mock")
    }
    return cfg, nil
}
```

Wire this into the startup path where bootstrap was previously handled. Remove any `fmt.Printf`, `fmt.Println`, `slog.Info(..., "secret", ...)`, or other stdout/log code that included the generated secret value.

### Step 5: Run tests to confirm they pass

- [ ] Run: `go test ./app/ -run TestBootstrapSecret -v`
  Expected: all 3 tests PASS.

### Step 6: Run the full app package tests to catch regressions

- [ ] Run: `go test -short ./app/... -v`
  Expected: all PASS. If existing tests that rely on auto-generated secrets break, update them to set `CYODA_BOOTSTRAP_CLIENT_SECRET` explicitly — they were testing deprecated behavior.

### Step 7: Commit

- [ ] Run:

```bash
git add app/app.go app/app_bootstrap_test.go
git commit -m "feat(iam): require CYODA_BOOTSTRAP_CLIENT_SECRET in jwt mode

Remove the auto-generate-and-print-to-stdout behavior. In jwt mode the
env var is now required (fatal startup error if unset); in mock mode
it's ignored.

Rationale: the old behavior leaked a production-sensitive value into
pod log aggregation in Kubernetes, and the 'printed once' UX was always
lost on restart anyway. Laptop users set it explicitly in their .env
file or run mock mode. Chart users get it via an auto-generated Secret
(chart-managed via Helm lookup pattern)."
```

---

## Task 3: `cyoda migrate` subcommand

Spec reference: design §5 "Binary — `cyoda migrate` subcommand".

**Files:**
- Create: `cmd/cyoda/migrate.go`
- Create: `cmd/cyoda/migrate_test.go`
- Modify: `cmd/cyoda/main.go` (add dispatch case)

### Step 1: Find the existing dispatch pattern in main.go

- [ ] Run: `grep -n "os.Args\|case \"" cmd/cyoda/main.go | head -30`
  Identify the switch statement that currently dispatches `init` and `health`. The new `migrate` case slots in the same style.

### Step 2: Write the failing tests

- [ ] Create `cmd/cyoda/migrate_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

// TestRunMigrate_MemoryBackendNoOp confirms that the memory backend's
// migrate is a no-op that exits 0 with an informational message.
func TestRunMigrate_MemoryBackendNoOp(t *testing.T) {
	t.Setenv("CYODA_STORAGE_BACKEND", "memory")

	code := runMigrate(nil)
	if code != 0 {
		t.Errorf("memory backend migrate should exit 0; got %d", code)
	}
}

// TestRunMigrate_UnknownFlagRejected verifies argument parsing errors
// exit non-zero rather than silently ignoring bad input.
func TestRunMigrate_UnknownFlagRejected(t *testing.T) {
	code := runMigrate([]string{"--notaflag"})
	if code == 0 {
		t.Error("unknown flag should cause non-zero exit")
	}
}

// TestRunMigrate_TimeoutFlagParsed verifies the --timeout flag is
// recognized and produces a runMigrateConfig with the expected value.
// This tests the flag plumbing, not the actual timeout-enforcement
// behavior (which requires a real backend).
func TestRunMigrate_TimeoutFlagParsed(t *testing.T) {
	cfg, err := parseMigrateArgs([]string{"--timeout", "10m"})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if cfg.Timeout.String() != "10m0s" {
		t.Errorf("want timeout 10m, got %s", cfg.Timeout)
	}
}

// TestRunMigrate_MissingPostgresDSN confirms a clear error when the
// postgres backend is selected but no DSN is provided.
func TestRunMigrate_MissingPostgresDSN(t *testing.T) {
	t.Setenv("CYODA_STORAGE_BACKEND", "postgres")
	t.Setenv("CYODA_POSTGRES_URL", "")
	t.Setenv("CYODA_POSTGRES_URL_FILE", "")

	code := runMigrate(nil)
	if code == 0 {
		t.Error("missing DSN should cause non-zero exit")
	}
}

// TestRunMigrate_IntegrationPostgres runs an end-to-end migration
// against a real Postgres via testcontainers. Verifies: first call
// applies migrations; second call is idempotent; schema-newer-than-code
// refuses.
func TestRunMigrate_IntegrationPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; run without -short")
	}
	pgURL := startTestPostgres(t)
	t.Setenv("CYODA_STORAGE_BACKEND", "postgres")
	t.Setenv("CYODA_POSTGRES_URL", pgURL)

	// First run: applies migrations.
	if code := runMigrate(nil); code != 0 {
		t.Fatalf("first migrate failed with code %d", code)
	}

	// Second run: idempotent.
	if code := runMigrate(nil); code != 0 {
		t.Fatalf("second migrate (idempotent) failed with code %d", code)
	}

	// Artificially advance schema_version beyond code's max.
	advanceSchemaVersion(t, pgURL, 99999)

	// Should refuse (schema newer than code).
	if code := runMigrate(nil); code == 0 {
		t.Error("migrate should refuse schema-newer-than-code")
	}
}

func startTestPostgres(t *testing.T) string {
	t.Helper()
	// Use the same testcontainers helper the e2e/parity/postgres/ tests use.
	// Find it with: grep -rn "testcontainers.*postgres" e2e/
	// Return a usable DSN.
	t.Skip("TODO: wire to existing testcontainers helper")
	return ""
}

func advanceSchemaVersion(t *testing.T, dsn string, version int) {
	t.Helper()
	// Open the DB, exec UPDATE schema_version SET version = $1 LIMIT 1, close.
	// Simple enough to inline.
	t.Skip("TODO: implement")
}
```

**Note:** the `startTestPostgres` and `advanceSchemaVersion` helpers are stubs. Before this test goes green, wire them into the existing testcontainers helper (probably at `e2e/parity/postgres/fixture.go` or `plugins/postgres/testcontainers/` — grep to find). If extracting a small shared helper is cleaner, do so as part of this task.

### Step 3: Run tests to confirm they fail

- [ ] Run: `go test ./cmd/cyoda/ -run TestRunMigrate -v -short`
  Expected: compile error `undefined: runMigrate, parseMigrateArgs`.

### Step 4: Implement `migrate.go`

- [ ] Create `cmd/cyoda/migrate.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/cyoda-platform/cyoda-go/app"
)

type migrateConfig struct {
	Timeout time.Duration
}

func parseMigrateArgs(args []string) (*migrateConfig, error) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum duration for migration run")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return &migrateConfig{Timeout: *timeout}, nil
}

// runMigrate is the entry point for the `cyoda migrate` subcommand.
// Returns the exit code: 0 on success, 1 on any error.
//
// Behavior:
//   - Loads the same env config the server does (so _FILE resolution
//     and every CYODA_* env var honor the identical rules).
//   - Dispatches on CYODA_STORAGE_BACKEND: memory and sqlite backends
//     have no migrations (sqlite uses embedded SQL run on first use,
//     not a separate migrate step); memory is always a no-op.
//   - Postgres backend runs the plugin's migration logic.
//   - Respects the schema-compatibility contract: refuses to run if
//     the database schema is newer than the code's embedded max
//     version.
//   - Exits cleanly: no admin listener opened, no background loops,
//     no lingering goroutines. Short-lived process.
func runMigrate(args []string) int {
	cfg, err := parseMigrateArgs(args)
	if err != nil {
		// flag package already wrote the error to stderr
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	appCfg, err := app.DefaultConfig()
	if err != nil {
		slog.Error("loading config", "err", err)
		return 1
	}

	switch appCfg.StorageBackend {
	case "memory":
		slog.Info("memory backend has no migrations — no-op")
		return 0
	case "sqlite":
		slog.Info("sqlite backend applies migrations lazily on first open — no-op for migrate subcommand")
		return 0
	case "postgres":
		return runPostgresMigrate(ctx, appCfg)
	default:
		slog.Error("unknown storage backend", "backend", appCfg.StorageBackend)
		return 1
	}
}

func runPostgresMigrate(ctx context.Context, appCfg *app.Config) int {
	// The postgres plugin exposes a Migrate function that takes a DSN
	// and runs migrations to the embedded max version, respecting the
	// schema-compat contract (refuses on schema-newer-than-code).
	//
	// Find the actual API with: grep -n "^func.*Migrate" plugins/postgres/*.go
	// Most likely signature: Migrate(ctx, dsn string) error

	start := time.Now()
	err := pgMigrate(ctx, appCfg.Postgres.URL)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Error("migration timed out", "timeout_err", err)
			return 1
		}
		slog.Error("migration failed", "err", err)
		return 1
	}
	slog.Info("migrations applied", "duration", time.Since(start))
	return 0
}

// pgMigrate wraps the postgres plugin's migration entry point.
// Extracted as a package-level var to let tests inject a fake.
var pgMigrate = func(ctx context.Context, dsn string) error {
	// Import plugins/postgres at the call site and invoke its Migrate.
	// Exact function depends on what the plugin exposes — check
	// plugins/postgres/migrate.go. If the plugin's migration logic is
	// not yet exposed as a standalone function (it's called from inside
	// plugin initialization), extract it here as part of this task.
	return fmt.Errorf("TODO: wire to plugins/postgres.Migrate")
}
```

- [ ] Replace the `pgMigrate` stub body with the real plugin call. Find the current migration logic with `grep -n "applyMigrations\|runMigrations" plugins/postgres/`. If it's only invoked via plugin init and not exposed, add a thin exported wrapper in the plugin package that the subcommand can call. Keep the extraction minimal — same function, just accessible.

### Step 5: Wire up the dispatch case in `main.go`

- [ ] Modify `cmd/cyoda/main.go`. Locate the dispatch switch (found in Step 1) and add:

```go
case "migrate":
    os.Exit(runMigrate(os.Args[2:]))
```

Positioned alphabetically or after `health`, matching the file's existing style.

### Step 6: Run unit tests to confirm they pass

- [ ] Run: `go test -short ./cmd/cyoda/ -run TestRunMigrate -v`
  Expected: short tests PASS. Integration test skipped (needs Docker).

### Step 7: Run the integration test

- [ ] Run: `go test ./cmd/cyoda/ -run TestRunMigrate_IntegrationPostgres -v`
  Expected: PASS (requires Docker running for testcontainers).

### Step 8: Run the full command package

- [ ] Run: `go test ./cmd/cyoda/... -v`
  Expected: all PASS including the existing `printHelp` tests and `init`/`health` tests.

### Step 9: Commit

- [ ] Run:

```bash
git add cmd/cyoda/migrate.go cmd/cyoda/migrate_test.go cmd/cyoda/main.go
# plus any plugins/postgres/ changes if you exported a wrapper
git commit -m "feat(cmd): cyoda migrate subcommand

New subcommand matching the existing init/health pattern. Runs schema
migrations for the configured backend and exits 0/1 — no long-running
process, no admin listener.

Dispatch:
  memory/sqlite: no-op (migrations embedded at open time)
  postgres: runs the plugin's migration logic
  unknown: fails with a clear error

Respects the shared-spec schema-compat contract: refuses to run when
DB schema is newer than code's embedded max version (same error the
server would produce on startup).

Needed by the Helm chart: the migration Job runs 'cyoda migrate' as
a pre-install/pre-upgrade hook."
```

---

## Task 4: Update `printHelp` and `README.md` for new binary behavior

Spec reference: design §1 "In scope — binary-side changes" + shared spec Gate 4 (documentation hygiene).

**Files:**
- Modify: `cmd/cyoda/main.go` (`printHelp` function)
- Modify: `README.md`

### Step 1: Update `printHelp` to mention `migrate` subcommand

- [ ] Find `printHelp` in `cmd/cyoda/main.go` (`grep -n "printHelp" cmd/cyoda/main.go`).
- [ ] Add a "Subcommands" section listing `init`, `health`, `migrate` with one-line descriptions each. If the existing help text already has such a section, append `migrate`.

Example addition:

```go
fmt.Println("Subcommands:")
fmt.Println("  init     — write a sqlite user config file for desktop use")
fmt.Println("  health   — probe /readyz on the admin port (exit 0 ready, 1 otherwise)")
fmt.Println("  migrate  — run schema migrations for the configured backend and exit")
```

### Step 2: Update `printHelp` to mention `_FILE` suffix convention

- [ ] In the env-var reference section of `printHelp` (where the existing `CYODA_*` vars are documented), add a paragraph on the `_FILE` convention:

```go
fmt.Println("Secret env vars (credentials):")
fmt.Println("  The four credential env vars — CYODA_POSTGRES_URL, CYODA_JWT_SIGNING_KEY,")
fmt.Println("  CYODA_HMAC_SECRET, CYODA_BOOTSTRAP_CLIENT_SECRET — also accept a _FILE")
fmt.Println("  variant that reads the value from the file at the given path. The _FILE")
fmt.Println("  variant takes precedence if both are set.")
fmt.Println("")
fmt.Println("  Example:")
fmt.Println("    export CYODA_JWT_SIGNING_KEY_FILE=/etc/cyoda/secrets/jwt-signing-key.pem")
```

### Step 3: Update `printHelp` to document the bootstrap secret tightening

- [ ] Near the existing `CYODA_BOOTSTRAP_CLIENT_SECRET` description, update to reflect the new required-in-jwt-mode behavior:

```go
fmt.Println("  CYODA_BOOTSTRAP_CLIENT_SECRET")
fmt.Println("    REQUIRED when CYODA_IAM_MODE=jwt. Ignored in mock mode.")
fmt.Println("    Fatal startup error if unset in jwt mode.")
```

Remove any text that mentions auto-generation or stdout printing of the generated value.

### Step 4: Run the existing printHelp test

- [ ] Run: `go test ./cmd/cyoda/ -run TestPrintHelp -v`
  Expected: existing tests PASS. If any assertion checks for text that's been removed (e.g., "generated secret"), update the assertion to reflect the new text.

### Step 5: Update `README.md`

- [ ] Add a short section to `README.md` under the existing environment-variable documentation explaining the `_FILE` suffix support:

```markdown
### Credential env vars: `_FILE` suffix support

The four credential env vars — `CYODA_POSTGRES_URL`, `CYODA_JWT_SIGNING_KEY`,
`CYODA_HMAC_SECRET`, `CYODA_BOOTSTRAP_CLIENT_SECRET` — accept a `_FILE`
variant that reads the value from the file at the given path:

```bash
# Equivalent:
export CYODA_JWT_SIGNING_KEY="$(cat /path/to/key.pem)"
export CYODA_JWT_SIGNING_KEY_FILE=/path/to/key.pem
```

`_FILE` takes precedence when both are set. Trailing whitespace is stripped
from file contents — safe for both DSN strings and multi-line PEM keys.

This is the canonical Docker/Kubernetes pattern (postgres, mysql, redis,
keycloak, etc. all use it) and is how the Helm chart wires credentials
from Secrets to the pod without exposing them in `env` output.
```

- [ ] Add the `cyoda migrate` subcommand to whatever table or list of subcommands exists in `README.md`. If no such list exists yet (only `init` and `health` are documented inline), add a "Subcommands" section:

```markdown
## Subcommands

- `cyoda init` — write a sqlite user config file (desktop use)
- `cyoda health` — probe `/readyz` and exit 0 ready, 1 otherwise (Docker HEALTHCHECK)
- `cyoda migrate` — run schema migrations for the configured backend and exit
```

- [ ] Update the `CYODA_BOOTSTRAP_CLIENT_SECRET` row in the env-var reference table (or inline description) to say "Required in jwt mode; ignored in mock mode. No auto-generation." Remove any text mentioning the previous generate-and-print behavior.

### Step 6: Run `go vet` to catch syntax issues in modified Go files

- [ ] Run: `go vet ./...`
  Expected: no output (clean vet).

### Step 7: Commit

- [ ] Run:

```bash
git add cmd/cyoda/main.go README.md
git commit -m "docs: document migrate subcommand, _FILE suffix, bootstrap tightening

printHelp and README updated in sync:
- New 'migrate' subcommand in the subcommands list
- _FILE suffix convention for the four credential env vars
- CYODA_BOOTSTRAP_CLIENT_SECRET now documented as required in jwt mode,
  auto-generation language removed

Matches Gate 4 (documentation hygiene) — printHelp + README + DefaultConfig
evolve together."
```

---

## Task 5: Chart skeleton — `Chart.yaml`, `_helpers.tpl`, `.helmignore`, `values.yaml`, `values.schema.json`

Spec reference: design §2 "Directory layout" and §4 "values.yaml — top-level shape".

**Files:**
- Create: `deploy/helm/cyoda/Chart.yaml`
- Create: `deploy/helm/cyoda/.helmignore`
- Create: `deploy/helm/cyoda/values.yaml`
- Create: `deploy/helm/cyoda/values.schema.json`
- Create: `deploy/helm/cyoda/templates/_helpers.tpl`
- Modify: `deploy/helm/README.md` (replace placeholder content)

### Step 1: Create `Chart.yaml`

- [ ] Create `deploy/helm/cyoda/Chart.yaml`:

```yaml
apiVersion: v2
name: cyoda
description: |
  cyoda-go: a lightweight, multi-node Go digital twin of the Cyoda platform.
  This chart ships a production-ready Helm deployment backed by an external
  Postgres, fronted by Gateway API (default) or a still-maintained Ingress
  controller (transitional).
type: application
version: 0.1.0
appVersion: "0.1.0"  # bump-chart-appversion.yml syncs this to the binary
kubeVersion: ">=1.31.0"
keywords:
  - cyoda
  - digital-twin
  - workflow
home: https://github.com/cyoda-platform/cyoda-go
sources:
  - https://github.com/cyoda-platform/cyoda-go
maintainers:
  - name: Cyoda Platform
    url: https://github.com/cyoda-platform
icon: https://raw.githubusercontent.com/cyoda-platform/cyoda-go/main/docs/logo.png
annotations:
  artifacthub.io/changes: |
    - Initial chart release
```

If the logo URL doesn't exist yet in the repo, remove the `icon:` line rather than leaving a broken link.

### Step 2: Create `.helmignore`

- [ ] Create `deploy/helm/cyoda/.helmignore`:

```
# Patterns to ignore when packaging the chart.
.DS_Store
.git/
.gitignore
.idea/
.vscode/
.project
.tox/
*.tmproj
*.orig
*.rej
*.swp
*.bak
*~

# Do not ship test fixtures in release tarballs
test/
tests/fixtures/
```

### Step 3: Create `_helpers.tpl`

- [ ] Create `deploy/helm/cyoda/templates/_helpers.tpl`:

```gotmpl
{{/*
Expand the name of the chart.
*/}}
{{- define "cyoda.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "cyoda.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart-name label (for chart-version tracking).
*/}}
{{- define "cyoda.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every rendered resource.
*/}}
{{- define "cyoda.labels" -}}
helm.sh/chart: {{ include "cyoda.chart" . }}
{{ include "cyoda.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels (stable across upgrades).
*/}}
{{- define "cyoda.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cyoda.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "cyoda.serviceAccountName" -}}
{{- default (include "cyoda.fullname" .) .Values.serviceAccount.name }}
{{- end }}

{{/*
Chart-managed HMAC Secret name (used when no existingSecret is provided).
*/}}
{{- define "cyoda.hmacSecretName" -}}
{{- if .Values.cluster.hmacSecret.existingSecret -}}
{{ .Values.cluster.hmacSecret.existingSecret }}
{{- else -}}
{{ printf "%s-hmac" (include "cyoda.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Chart-managed bootstrap client Secret name.
*/}}
{{- define "cyoda.bootstrapSecretName" -}}
{{- if .Values.bootstrap.clientSecret.existingSecret -}}
{{ .Values.bootstrap.clientSecret.existingSecret }}
{{- else -}}
{{ printf "%s-bootstrap" (include "cyoda.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Image reference: falls back to .Chart.AppVersion if image.tag is unset.
*/}}
{{- define "cyoda.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}
```

### Step 4: Create `values.yaml`

- [ ] Create `deploy/helm/cyoda/values.yaml` exactly matching the shape documented in spec §4. Full file:

```yaml
# Default values for cyoda.
# See the README and docs/superpowers/specs/2026-04-17-provisioning-helm-design.md
# for the rationale behind every section.

# -- Number of cyoda pods. Scale up with `--set replicas=3`.
# Cluster mode is always on; at replicas=1 it runs as a "cluster of one"
# (see internal/cluster/registry/gossip.go).
replicas: 1

# -- Log level (debug, info, warn, error).
logLevel: info

image:
  repository: ghcr.io/cyoda-platform/cyoda
  # -- Image tag. Defaults to .Chart.AppVersion when empty.
  tag: ""
  pullPolicy: IfNotPresent

# -- imagePullSecrets for air-gapped or mirrored registries.
# Example: [{name: ghcr-pull-secret}]
imagePullSecrets: []

resources:
  requests:
    cpu: 100m
    memory: 256Mi
  limits:
    cpu: 1000m
    memory: 512Mi

# -- Postgres DSN via operator-managed Secret. REQUIRED.
# The Secret must have a key (default "dsn") whose value is the full
# connection string: postgres://user:pass@host:5432/db?sslmode=require
postgres:
  existingSecret: ""
  existingSecretKey: dsn

# -- JWT signing key via operator-managed Secret. REQUIRED.
# The Secret must have a key (default "signing-key.pem") whose value is
# the full PEM-encoded RSA private key.
jwt:
  existingSecret: ""
  existingSecretKey: signing-key.pem
  issuer: cyoda
  expirySeconds: 3600

cluster:
  # -- HMAC secret used for gossip encryption AND inter-node HTTP dispatch
  # auth. Chart auto-generates via Helm lookup on first install if
  # existingSecret is unset; GitOps controllers (Argo CD) MUST provide
  # existingSecret — see README > "Using with GitOps".
  hmacSecret:
    existingSecret: ""
    existingSecretKey: secret

bootstrap:
  # -- Bootstrap M2M client secret. Same auto-gen pattern as HMAC.
  clientSecret:
    existingSecret: ""
    existingSecretKey: secret
  # -- Bootstrap client ID. Auto-generated by the binary if empty.
  clientId: ""
  tenantId: default-tenant
  userId: admin
  roles: "ROLE_ADMIN,ROLE_M2M"

# -- Arbitrary additional env vars. Each entry is {name, value} OR
# {name, valueFrom}. Use this for OTel configuration, feature flags, etc.
# DO NOT set CYODA_*_FILE or the four credential env vars via this knob —
# they are set by the chart and duplicates are rejected by Kubernetes.
extraEnv: []

service:
  type: ClusterIP

# -- Gateway API routing (recommended, default ON).
# Chart renders HTTPRoute + GRPCRoute that parentRef into an
# operator-provided Gateway. Chart does NOT render the Gateway itself.
gateway:
  enabled: true
  parentRefs: []    # REQUIRED when enabled; list of parent Gateway refs
  http:
    hostnames: []
  grpc:
    hostnames: []

# -- Ingress routing (transitional; ingress-nginx retired March 2026).
# Prefer gateway.enabled=true unless you're mid-migration.
ingress:
  enabled: false
  className: ""
  http:
    host: ""
    paths:
      - path: /
        pathType: Prefix
    annotations: {}
    tls: []
  grpc:
    host: ""
    paths:
      - path: /
        pathType: Prefix
    annotations:
      nginx.ingress.kubernetes.io/backend-protocol: GRPC
    tls: []

monitoring:
  serviceMonitor:
    enabled: false
    interval: 30s
    labels: {}

# -- Optional NetworkPolicy. Restricts admin-port ingress to declared
# namespaces (e.g., the Prometheus namespace) and gossip-port ingress
# to chart-managed pods. Requires a CNI that enforces NetworkPolicy.
networkPolicy:
  enabled: false
  metricsFromNamespaces: []

migrate:
  activeDeadlineSeconds: 600
  backoffLimit: 2
  resources:
    requests: { cpu: 100m, memory: 128Mi }
    limits:   { cpu: 500m, memory: 256Mi }

podDisruptionBudget:
  enabled: true    # rendered only when replicas > 1
  minAvailable: 1

serviceAccount:
  create: true
  name: ""          # defaults to fullname
  annotations: {}

podAnnotations: {}
podLabels: {}
nodeSelector: {}
tolerations: []
affinity: {}

nameOverride: ""
fullnameOverride: ""
```

### Step 5: Create `values.schema.json`

- [ ] Create `deploy/helm/cyoda/values.schema.json`. This validates every `helm install`/`upgrade` at render time:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["postgres", "jwt"],
  "additionalProperties": true,
  "properties": {
    "replicas": {
      "type": "integer",
      "minimum": 1
    },
    "logLevel": {
      "type": "string",
      "enum": ["debug", "info", "warn", "error"]
    },
    "image": {
      "type": "object",
      "required": ["repository"],
      "properties": {
        "repository": {"type": "string", "minLength": 1},
        "tag": {"type": "string"},
        "pullPolicy": {"type": "string", "enum": ["Always", "IfNotPresent", "Never"]}
      }
    },
    "imagePullSecrets": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["name"],
        "properties": {"name": {"type": "string"}}
      }
    },
    "postgres": {
      "type": "object",
      "required": ["existingSecret", "existingSecretKey"],
      "properties": {
        "existingSecret": {"type": "string", "minLength": 1},
        "existingSecretKey": {"type": "string", "minLength": 1}
      }
    },
    "jwt": {
      "type": "object",
      "required": ["existingSecret", "existingSecretKey"],
      "properties": {
        "existingSecret": {"type": "string", "minLength": 1},
        "existingSecretKey": {"type": "string", "minLength": 1},
        "issuer": {"type": "string"},
        "expirySeconds": {"type": "integer", "minimum": 60}
      }
    },
    "cluster": {
      "type": "object",
      "properties": {
        "hmacSecret": {
          "type": "object",
          "properties": {
            "existingSecret": {"type": "string"},
            "existingSecretKey": {"type": "string", "minLength": 1}
          }
        }
      }
    },
    "bootstrap": {
      "type": "object",
      "properties": {
        "clientSecret": {
          "type": "object",
          "properties": {
            "existingSecret": {"type": "string"},
            "existingSecretKey": {"type": "string", "minLength": 1}
          }
        },
        "clientId": {"type": "string"},
        "tenantId": {"type": "string", "minLength": 1},
        "userId": {"type": "string", "minLength": 1},
        "roles": {"type": "string", "minLength": 1}
      }
    },
    "extraEnv": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["name"],
        "oneOf": [
          {
            "required": ["name", "value"],
            "properties": {
              "name": {"type": "string", "minLength": 1},
              "value": {"type": "string"}
            }
          },
          {
            "required": ["name", "valueFrom"],
            "properties": {
              "name": {"type": "string", "minLength": 1},
              "valueFrom": {"type": "object"}
            }
          }
        ]
      }
    },
    "gateway": {
      "type": "object",
      "properties": {
        "enabled": {"type": "boolean"},
        "parentRefs": {"type": "array"},
        "http": {
          "type": "object",
          "properties": {
            "hostnames": {"type": "array", "items": {"type": "string"}}
          }
        },
        "grpc": {
          "type": "object",
          "properties": {
            "hostnames": {"type": "array", "items": {"type": "string"}}
          }
        }
      },
      "if": {"properties": {"enabled": {"const": true}}},
      "then": {
        "properties": {
          "parentRefs": {"type": "array", "minItems": 1},
          "http": {"properties": {"hostnames": {"minItems": 1}}},
          "grpc": {"properties": {"hostnames": {"minItems": 1}}}
        }
      }
    },
    "ingress": {
      "type": "object",
      "properties": {
        "enabled": {"type": "boolean"},
        "http": {
          "type": "object",
          "properties": {"host": {"type": "string"}}
        },
        "grpc": {
          "type": "object",
          "properties": {"host": {"type": "string"}}
        }
      },
      "if": {"properties": {"enabled": {"const": true}}},
      "then": {
        "properties": {
          "http": {"properties": {"host": {"minLength": 1}}},
          "grpc": {"properties": {"host": {"minLength": 1}}}
        }
      }
    },
    "monitoring": {"type": "object"},
    "networkPolicy": {"type": "object"},
    "migrate": {"type": "object"},
    "podDisruptionBudget": {"type": "object"},
    "serviceAccount": {"type": "object"},
    "podAnnotations": {"type": "object"},
    "podLabels": {"type": "object"},
    "nodeSelector": {"type": "object"},
    "tolerations": {"type": "array"},
    "affinity": {"type": "object"},
    "nameOverride": {"type": "string"},
    "fullnameOverride": {"type": "string"}
  },
  "allOf": [
    {
      "if": {
        "properties": {
          "gateway": {"properties": {"enabled": {"const": true}}},
          "ingress": {"properties": {"enabled": {"const": true}}}
        },
        "required": ["gateway", "ingress"]
      },
      "then": {
        "errorMessage": "gateway.enabled and ingress.enabled are mutually exclusive — pick one."
      }
    }
  ]
}
```

The `gateway && ingress` exclusive check uses the `allOf + if/then` pattern. Note that helm's schema validator (which is `invopop/jsonschema` or similar) may not honor every JSON Schema keyword equally; if `errorMessage` is unsupported the validator still rejects the input, just with a less-friendly error. If the mutual-exclusion check isn't caught at schema time, the chart's `_helpers.tpl` or a `fail` template in the body catches it at render time.

### Step 6: Replace the `deploy/helm/README.md` placeholder

- [ ] Overwrite `deploy/helm/README.md` (currently a placeholder pointing at the shared spec) with a minimal pointer to the chart's own README:

```markdown
# cyoda Helm charts

| Chart | Version | AppVersion | Purpose |
|-------|---------|------------|---------|
| [cyoda/](./cyoda/) | 0.1.0 | 0.1.0 | Production Helm deployment |

See the per-chart `README.md` for operator documentation, values schema,
and upgrade notes.

For the overall provisioning design, see
`docs/superpowers/specs/2026-04-17-provisioning-helm-design.md`.
```

### Step 7: Verify the chart lints successfully

- [ ] Run: `helm lint deploy/helm/cyoda`
  Expected: `1 chart(s) linted, 0 chart(s) failed`. Warnings about missing templates are OK at this stage — templates come in later tasks.

- [ ] Run: `helm template cyoda-test deploy/helm/cyoda --set postgres.existingSecret=test-dsn --set jwt.existingSecret=test-jwt --set gateway.parentRefs[0].name=test-gw --set gateway.parentRefs[0].sectionName=https --set gateway.http.hostnames[0]=cyoda.example.com --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com`
  Expected: currently produces no output (no templates yet). Should at least pass schema validation without complaint.

### Step 8: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/Chart.yaml deploy/helm/cyoda/.helmignore \
        deploy/helm/cyoda/values.yaml deploy/helm/cyoda/values.schema.json \
        deploy/helm/cyoda/templates/_helpers.tpl \
        deploy/helm/README.md
git commit -m "feat(helm): chart skeleton — Chart.yaml, values, schema, helpers

Scaffolds deploy/helm/cyoda/ with Chart.yaml (name=cyoda, version=0.1.0),
values.yaml matching the spec §4 shape, values.schema.json enforcing
render-time invariants (required secrets, gateway-ingress exclusivity,
minItems on hostnames), _helpers.tpl with cyoda.fullname / labels /
selectorLabels / serviceAccountName / image helpers.

Chart lints cleanly with no templates yet. Subsequent tasks add
resource templates one kind at a time."
```

---

## Task 6: ServiceAccount, Service, and headless Service templates

Spec reference: design §3 "Services" and "Pod specification — ServiceAccount".

**Files:**
- Create: `deploy/helm/cyoda/templates/serviceaccount.yaml`
- Create: `deploy/helm/cyoda/templates/service.yaml`
- Create: `deploy/helm/cyoda/templates/service-headless.yaml`

### Step 1: Create ServiceAccount template

- [ ] Create `deploy/helm/cyoda/templates/serviceaccount.yaml`:

```yaml
{{- if .Values.serviceAccount.create }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "cyoda.serviceAccountName" . }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
  {{- with .Values.serviceAccount.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
automountServiceAccountToken: false
{{- end }}
```

### Step 2: Create client-facing Service template

- [ ] Create `deploy/helm/cyoda/templates/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "cyoda.fullname" . }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
spec:
  type: {{ .Values.service.type }}
  selector:
    {{- include "cyoda.selectorLabels" . | nindent 4 }}
  ports:
    - name: http
      port: 8080
      targetPort: http
      protocol: TCP
    - name: grpc
      port: 9090
      targetPort: grpc
      protocol: TCP
    - name: metrics
      port: 9091
      targetPort: metrics
      protocol: TCP
```

### Step 3: Create headless Service template (TCP + UDP on gossip port)

- [ ] Create `deploy/helm/cyoda/templates/service-headless.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "cyoda.fullname" . }}-headless
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
spec:
  clusterIP: None
  # Required so peers discover each other before readiness passes —
  # otherwise a cluster-of-N never reaches ready because pods can't
  # find peers until ready, and can't be ready until they find peers.
  publishNotReadyAddresses: true
  selector:
    {{- include "cyoda.selectorLabels" . | nindent 4 }}
  ports:
    # memberlist uses BOTH TCP (state exchange) and UDP (SWIM probes)
    # on the same port. Service must declare both protocols.
    - name: gossip-tcp
      port: 7946
      targetPort: gossip-tcp
      protocol: TCP
    - name: gossip-udp
      port: 7946
      targetPort: gossip-udp
      protocol: UDP
```

### Step 4: Verify templates render

- [ ] Run: `helm template cyoda-test deploy/helm/cyoda --set postgres.existingSecret=test-dsn --set jwt.existingSecret=test-jwt --set gateway.parentRefs[0].name=test-gw --set gateway.http.hostnames[0]=cyoda.example.com --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com`
  Expected: output contains three `kind: Service`-ish objects (ServiceAccount + 2 Services).

### Step 5: Validate against Kubernetes schema

- [ ] Install `kubeconform` if not present: `go install github.com/yannh/kubeconform/cmd/kubeconform@latest`
- [ ] Run:

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set gateway.parentRefs[0].name=test-gw \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
  | kubeconform -strict -kubernetes-version 1.31.0
```

Expected: no violations.

### Step 6: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/serviceaccount.yaml \
        deploy/helm/cyoda/templates/service.yaml \
        deploy/helm/cyoda/templates/service-headless.yaml
git commit -m "feat(helm): ServiceAccount + client Service + headless Service

Dedicated SA with automountServiceAccountToken: false (defense in depth
— cyoda doesn't talk to the kube API).

Client Service exposes named ports http/grpc/metrics for consumption
by the Gateway/Ingress layer and ServiceMonitor. Headless Service
publishes gossip on both TCP and UDP protocols (memberlist requirement;
SWIM probes run over UDP, state exchange over TCP) and has
publishNotReadyAddresses=true so peers can discover each other during
bootstrap."
```

---

## Task 7: ConfigMap for non-secret env

Spec reference: design §4 "ConfigMap / Secret split".

**Files:**
- Create: `deploy/helm/cyoda/templates/configmap.yaml`

### Step 1: Create the ConfigMap

- [ ] Create `deploy/helm/cyoda/templates/configmap.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "cyoda.fullname" . }}-env
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
data:
  # Port and listener configuration — chart-managed, not operator-tunable
  # (the chart's security model depends on these defaults).
  CYODA_HTTP_PORT: "8080"
  CYODA_GRPC_PORT: "9090"
  CYODA_ADMIN_PORT: "9091"
  # Bind to all pod interfaces so ServiceMonitor scraping reaches /metrics.
  # Pod is bounded by NetworkPolicy (optional) and by not having routable
  # external egress; the admin listener is unauthenticated by design.
  CYODA_ADMIN_BIND_ADDRESS: "0.0.0.0"

  # IAM — production floor: require JWT, refuse to start if not configured.
  CYODA_IAM_MODE: jwt
  CYODA_REQUIRE_JWT: "true"

  # Storage — postgres only for Helm; migration runs in the Job, not the pod.
  CYODA_STORAGE_BACKEND: postgres
  CYODA_POSTGRES_AUTO_MIGRATE: "false"

  # Logging + JWT claims.
  CYODA_LOG_LEVEL: {{ .Values.logLevel | quote }}
  CYODA_JWT_ISSUER: {{ .Values.jwt.issuer | quote }}
  CYODA_JWT_EXPIRY_SECONDS: {{ .Values.jwt.expirySeconds | quote }}

  # Bootstrap identity.
  {{- if .Values.bootstrap.clientId }}
  CYODA_BOOTSTRAP_CLIENT_ID: {{ .Values.bootstrap.clientId | quote }}
  {{- end }}
  CYODA_BOOTSTRAP_TENANT_ID: {{ .Values.bootstrap.tenantId | quote }}
  CYODA_BOOTSTRAP_USER_ID: {{ .Values.bootstrap.userId | quote }}
  CYODA_BOOTSTRAP_ROLES: {{ .Values.bootstrap.roles | quote }}
```

### Step 2: Verify + commit

- [ ] Run the same `helm template` + `kubeconform` commands from Task 6, step 5.
  Expected: no violations.

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/configmap.yaml
git commit -m "feat(helm): ConfigMap for non-secret env

Separates non-secret env (ports, IAM mode, backend selection, log level,
JWT claims, bootstrap identity) from secret env (credentials via Secret
projection). Pod references this via envFrom in the StatefulSet.

Sensitive values never touch a ConfigMap; non-sensitive never touch a
Secret."
```

---

## Task 8: Chart-managed HMAC and bootstrap Secrets with GitOps safety guard

Spec reference: design §4 "Auto-generation pattern for chart-managed Secrets".

**Files:**
- Create: `deploy/helm/cyoda/templates/secret-hmac.yaml`
- Create: `deploy/helm/cyoda/templates/secret-bootstrap.yaml`

### Step 1: Create HMAC Secret template with GitOps safety guard

- [ ] Create `deploy/helm/cyoda/templates/secret-hmac.yaml`:

```yaml
{{- if not .Values.cluster.hmacSecret.existingSecret }}
{{- $name := printf "%s-hmac" (include "cyoda.fullname" .) }}
{{- $key  := .Values.cluster.hmacSecret.existingSecretKey }}
{{- $existing := (lookup "v1" "Secret" .Release.Namespace $name) }}
{{- if not $existing }}
  {{- /*
    Secret doesn't exist. Before generating, verify we have live cluster
    access — otherwise we'd take the else branch on every reconcile (Argo
    CD default path, helm template, --dry-run) and re-randomize the HMAC,
    which breaks gossip encryption AND inter-node HTTP dispatch auth
    (internal/cluster/dispatch/forwarder.go) mid-cluster-lifetime.
  */ -}}
  {{- $ns := (lookup "v1" "Namespace" "" .Release.Namespace) }}
  {{- if not $ns }}
    {{- fail "cluster.hmacSecret.existingSecret is required when the chart is rendered without live cluster access (helm template, Argo CD, --dry-run, or a namespace that does not yet exist — e.g. first-time 'helm install --create-namespace'). Fix: (a) kubectl create namespace <ns> first; (b) pre-create the HMAC Secret and set cluster.hmacSecret.existingSecret; or (c) use external-secrets-operator. See the chart README > 'Using with GitOps'." }}
  {{- end }}
{{- end }}
{{- $value := "" }}
{{- if $existing }}
{{- $value = index $existing.data $key }}
{{- else }}
{{- $value = randAlphaNum 48 | b64enc }}
{{- end }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ $name }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
type: Opaque
data:
  {{ $key }}: {{ $value | quote }}
{{- end }}
```

### Step 2: Create bootstrap-secret template (identical pattern)

- [ ] Create `deploy/helm/cyoda/templates/secret-bootstrap.yaml`:

```yaml
{{- if not .Values.bootstrap.clientSecret.existingSecret }}
{{- $name := printf "%s-bootstrap" (include "cyoda.fullname" .) }}
{{- $key  := .Values.bootstrap.clientSecret.existingSecretKey }}
{{- $existing := (lookup "v1" "Secret" .Release.Namespace $name) }}
{{- if not $existing }}
  {{- $ns := (lookup "v1" "Namespace" "" .Release.Namespace) }}
  {{- if not $ns }}
    {{- fail "bootstrap.clientSecret.existingSecret is required when the chart is rendered without live cluster access (helm template, Argo CD, --dry-run, or a namespace that does not yet exist — e.g. first-time 'helm install --create-namespace'). Fix: (a) kubectl create namespace <ns> first; (b) pre-create the bootstrap Secret and set bootstrap.clientSecret.existingSecret; or (c) use external-secrets-operator. See the chart README > 'Using with GitOps'." }}
  {{- end }}
{{- end }}
{{- $value := "" }}
{{- if $existing }}
{{- $value = index $existing.data $key }}
{{- else }}
{{- $value = randAlphaNum 48 | b64enc }}
{{- end }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ $name }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
type: Opaque
data:
  {{ $key }}: {{ $value | quote }}
{{- end }}
```

### Step 3: Verify with `helm template`

- [ ] Run:

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set gateway.parentRefs[0].name=test-gw \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com
```

Expected: FAILS with the GitOps-safety-guard `fail` message — the template runs without live cluster access (no namespace lookup succeeds), so the guard fires. **This is the correct behavior.**

- [ ] Run with an explicit HMAC + bootstrap existingSecret override:

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set cluster.hmacSecret.existingSecret=test-hmac \
    --set bootstrap.clientSecret.existingSecret=test-bootstrap \
    --set gateway.parentRefs[0].name=test-gw \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com
```

Expected: renders without error (no chart-managed Secret objects produced, since both existingSecret are set).

### Step 4: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/secret-hmac.yaml \
        deploy/helm/cyoda/templates/secret-bootstrap.yaml
git commit -m "feat(helm): chart-managed HMAC + bootstrap Secrets with GitOps guard

Two templates rendered only when the corresponding existingSecret is
unset. Both use the lookup-based auto-gen pattern (randAlphaNum 48 on
first install, reuses existing value on subsequent reconciles).

GitOps safety: a second lookup on the Namespace acts as a live-cluster
detector. If it returns empty, we're in helm template / Argo CD default
path / --dry-run / brand-new namespace — all cases where generating
would re-randomize on every render and silently rotate the secret.
The chart fails with an actionable message pointing at the three
escape hatches (pre-create namespace; pre-create Secret; use
external-secrets-operator).

existingSecretKey knob is honored on both the read and write paths
symmetrically, so 'value is written and read under the same key'
holds regardless of which subset of the knobs the operator sets."
```

---

## Task 9: StatefulSet template

Spec reference: design §3 "Pod specification" (and §4 for the credential-env wiring).

This is the largest single template in the chart. Break the step into sub-steps for readability.

**Files:**
- Create: `deploy/helm/cyoda/templates/statefulset.yaml`

### Step 1: Create the StatefulSet template

- [ ] Create `deploy/helm/cyoda/templates/statefulset.yaml`:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {{ include "cyoda.fullname" . }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
spec:
  serviceName: {{ include "cyoda.fullname" . }}-headless
  replicas: {{ .Values.replicas }}
  # Pods start in parallel rather than strictly ordered — cyoda's cluster
  # mode handles peer discovery via gossip; we don't need the 0-then-1-then-2
  # ordering that StatefulSet provides by default.
  podManagementPolicy: Parallel
  updateStrategy:
    type: RollingUpdate
  selector:
    matchLabels:
      {{- include "cyoda.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "cyoda.selectorLabels" . | nindent 8 }}
        {{- with .Values.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- with .Values.podAnnotations }}
      annotations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
    spec:
      serviceAccountName: {{ include "cyoda.serviceAccountName" . }}
      automountServiceAccountToken: false
      {{- with .Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: cyoda
          image: {{ include "cyoda.image" . }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities:
              drop: [ALL]
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            - name: grpc
              containerPort: 9090
              protocol: TCP
            - name: metrics
              containerPort: 9091
              protocol: TCP
            - name: gossip-tcp
              containerPort: 7946
              protocol: TCP
            - name: gossip-udp
              containerPort: 7946
              protocol: UDP
          env:
            # Cluster identity (downward API)
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: CYODA_NODE_ID
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: CYODA_NODE_ADDR
              value: "http://$(CYODA_NODE_ID).{{ include "cyoda.fullname" . }}-headless.$(POD_NAMESPACE).svc.cluster.local:9090"
            - name: CYODA_GOSSIP_ADDR
              value: "0.0.0.0:7946"
            # Credentials via _FILE suffix (projected-volume mount)
            - name: CYODA_POSTGRES_URL_FILE
              value: /etc/cyoda/secrets/postgres-dsn
            - name: CYODA_JWT_SIGNING_KEY_FILE
              value: /etc/cyoda/secrets/jwt-signing-key.pem
            - name: CYODA_HMAC_SECRET_FILE
              value: /etc/cyoda/secrets/hmac-secret
            - name: CYODA_BOOTSTRAP_CLIENT_SECRET_FILE
              value: /etc/cyoda/secrets/bootstrap-client-secret
            {{- with .Values.extraEnv }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          envFrom:
            - configMapRef:
                name: {{ include "cyoda.fullname" . }}-env
          livenessProbe:
            httpGet:
              path: /livez
              port: metrics
            initialDelaySeconds: 10
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /readyz
              port: metrics
            initialDelaySeconds: 5
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 3
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
          volumeMounts:
            - name: secrets
              mountPath: /etc/cyoda/secrets
              readOnly: true
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: secrets
          projected:
            defaultMode: 0400
            sources:
              - secret:
                  name: {{ .Values.postgres.existingSecret }}
                  items:
                    - key: {{ .Values.postgres.existingSecretKey }}
                      path: postgres-dsn
              - secret:
                  name: {{ .Values.jwt.existingSecret }}
                  items:
                    - key: {{ .Values.jwt.existingSecretKey }}
                      path: jwt-signing-key.pem
              - secret:
                  name: {{ include "cyoda.hmacSecretName" . }}
                  items:
                    - key: {{ .Values.cluster.hmacSecret.existingSecretKey }}
                      path: hmac-secret
              - secret:
                  name: {{ include "cyoda.bootstrapSecretName" . }}
                  items:
                    - key: {{ .Values.bootstrap.clientSecret.existingSecretKey }}
                      path: bootstrap-client-secret
        - name: tmp
          emptyDir: {}
      {{- with .Values.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
  # volumeClaimTemplates intentionally empty — cyoda is stateless when
  # backed by Postgres (state lives in the database, not the pod).
```

### Step 2: Render and kubeconform-validate

- [ ] Run (with all existingSecret overrides so the GitOps guard passes):

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set cluster.hmacSecret.existingSecret=test-hmac \
    --set bootstrap.clientSecret.existingSecret=test-bootstrap \
    --set gateway.parentRefs[0].name=test-gw \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
  > /tmp/rendered.yaml
```

Expected: renders without error. Grep for `kind: StatefulSet` to confirm presence.

- [ ] Run: `kubeconform -strict -kubernetes-version 1.31.0 /tmp/rendered.yaml`
  Expected: no violations.

### Step 3: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/statefulset.yaml
git commit -m "feat(helm): StatefulSet template

Always StatefulSet (regardless of replica count) for stable pod DNS
needed by gossip peer discovery. Always cluster-mode (NODE_ID from
downward API, NODE_ADDR computed from headless-service DNS).

Credentials mounted via projected volume at /etc/cyoda/secrets with
defaultMode 0400; the four _FILE env vars point at the file paths so
the binary reads them from disk (not from env, which would leak into
kubectl describe).

Security context applies CIS hardening (nonroot UID 65532,
readOnlyRootFilesystem, capabilities.drop: [ALL], seccompProfile
RuntimeDefault). Pod's own SA has automountServiceAccountToken: false —
cyoda doesn't talk to the kube API.

Probes on /livez and /readyz on the metrics port. Resources and
extraEnv and nodeSelector/tolerations/affinity passthrough.
volumeClaimTemplates intentionally empty — stateless when Postgres-backed."
```

---

## Task 10: Migration Job and PodDisruptionBudget

Spec reference: design §5 "Chart — migration Job" and §3 pod shape bullet on PDB.

**Files:**
- Create: `deploy/helm/cyoda/templates/job-migrate.yaml`
- Create: `deploy/helm/cyoda/templates/pdb.yaml`

### Step 1: Create the migration Job

- [ ] Create `deploy/helm/cyoda/templates/job-migrate.yaml`:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ include "cyoda.fullname" . }}-migrate-{{ .Release.Revision }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "0"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
spec:
  backoffLimit: {{ .Values.migrate.backoffLimit }}
  activeDeadlineSeconds: {{ .Values.migrate.activeDeadlineSeconds }}
  template:
    metadata:
      labels:
        {{- include "cyoda.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: migrate
    spec:
      restartPolicy: Never
      serviceAccountName: {{ include "cyoda.serviceAccountName" . }}
      automountServiceAccountToken: false
      {{- with .Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: migrate
          image: {{ include "cyoda.image" . }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities:
              drop: [ALL]
          command: [/cyoda, migrate]
          env:
            - name: CYODA_POSTGRES_URL_FILE
              value: /etc/cyoda/secrets/postgres-dsn
          envFrom:
            - configMapRef:
                name: {{ include "cyoda.fullname" . }}-env
          volumeMounts:
            - name: secrets
              mountPath: /etc/cyoda/secrets
              readOnly: true
          resources:
            {{- toYaml .Values.migrate.resources | nindent 12 }}
      volumes:
        # Only mounts the DSN — migration doesn't need JWT/HMAC/bootstrap.
        # Principle of least privilege.
        - name: secrets
          projected:
            defaultMode: 0400
            sources:
              - secret:
                  name: {{ .Values.postgres.existingSecret }}
                  items:
                    - key: {{ .Values.postgres.existingSecretKey }}
                      path: postgres-dsn
```

### Step 2: Create the PDB (rendered only when replicas > 1)

- [ ] Create `deploy/helm/cyoda/templates/pdb.yaml`:

```yaml
{{- if and .Values.podDisruptionBudget.enabled (gt (int .Values.replicas) 1) }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "cyoda.fullname" . }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
spec:
  minAvailable: {{ .Values.podDisruptionBudget.minAvailable }}
  selector:
    matchLabels:
      {{- include "cyoda.selectorLabels" . | nindent 6 }}
{{- end }}
```

### Step 3: Verify rendering at replicas=1 and replicas=3

- [ ] Run (replicas=1 — expect NO PDB in output):

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set cluster.hmacSecret.existingSecret=test-hmac \
    --set bootstrap.clientSecret.existingSecret=test-bootstrap \
    --set gateway.parentRefs[0].name=test-gw \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
  | grep "kind: PodDisruptionBudget" || echo "no PDB (correct for replicas=1)"
```

Expected: "no PDB (correct for replicas=1)".

- [ ] Run (replicas=3 — expect PDB present):

```bash
helm template cyoda-test deploy/helm/cyoda --set replicas=3 \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set cluster.hmacSecret.existingSecret=test-hmac \
    --set bootstrap.clientSecret.existingSecret=test-bootstrap \
    --set gateway.parentRefs[0].name=test-gw \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
  | grep "kind: PodDisruptionBudget"
```

Expected: one match.

### Step 4: kubeconform both renders

- [ ] Repeat the kubeconform check from Task 6 step 5 for both replicas=1 and replicas=3.
  Expected: no violations in either.

### Step 5: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/job-migrate.yaml \
        deploy/helm/cyoda/templates/pdb.yaml
git commit -m "feat(helm): migration Job + PodDisruptionBudget

Migration Job runs /cyoda migrate as a pre-install + pre-upgrade hook,
blocking on completion before the StatefulSet rolls. Unique name per
release revision (no collisions across upgrades). Successful Jobs clean
up via hook-delete-policy; failed Jobs retained for kubectl logs
postmortem. backoffLimit: 2, activeDeadlineSeconds: 600 by default.

Job mounts only the DSN — principle of least privilege, migration
doesn't need JWT/HMAC/bootstrap secrets.

PDB rendered only when replicas > 1 with minAvailable: 1. Guards
rolling-upgrade disruptions against total availability loss."
```

---

## Task 11: Gateway API HTTPRoute and GRPCRoute templates

Spec reference: design §3 "Routing — Gateway API by default".

**Files:**
- Create: `deploy/helm/cyoda/templates/gateway-httproute.yaml`
- Create: `deploy/helm/cyoda/templates/gateway-grpcroute.yaml`

### Step 1: Create HTTPRoute template

- [ ] Create `deploy/helm/cyoda/templates/gateway-httproute.yaml`:

```yaml
{{- if .Values.gateway.enabled }}
{{- if .Values.ingress.enabled }}
{{- fail "gateway.enabled and ingress.enabled are mutually exclusive — pick one." }}
{{- end }}
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ include "cyoda.fullname" . }}-http
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
spec:
  parentRefs:
    {{- toYaml .Values.gateway.parentRefs | nindent 4 }}
  hostnames:
    {{- toYaml .Values.gateway.http.hostnames | nindent 4 }}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: {{ include "cyoda.fullname" . }}
          port: 8080
{{- end }}
```

### Step 2: Create GRPCRoute template

- [ ] Create `deploy/helm/cyoda/templates/gateway-grpcroute.yaml`:

```yaml
{{- if .Values.gateway.enabled }}
apiVersion: gateway.networking.k8s.io/v1
kind: GRPCRoute
metadata:
  name: {{ include "cyoda.fullname" . }}-grpc
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
spec:
  parentRefs:
    {{- toYaml .Values.gateway.parentRefs | nindent 4 }}
  hostnames:
    {{- toYaml .Values.gateway.grpc.hostnames | nindent 4 }}
  rules:
    - backendRefs:
        - name: {{ include "cyoda.fullname" . }}
          port: 9090
{{- end }}
```

### Step 3: Render and kubeconform-validate

- [ ] Run (with Gateway API CRD schemas via schema-location):

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set cluster.hmacSecret.existingSecret=test-hmac \
    --set bootstrap.clientSecret.existingSecret=test-bootstrap \
    --set gateway.parentRefs[0].name=platform-gw \
    --set gateway.parentRefs[0].namespace=gateway-system \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
  > /tmp/rendered.yaml
```

Check output contains both `kind: HTTPRoute` and `kind: GRPCRoute`.

- [ ] Validate. The Gateway API CRDs need a schema location:

```bash
kubeconform -strict -kubernetes-version 1.31.0 \
  -schema-location default \
  -schema-location 'https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.31.0-standalone-strict/{{.ResourceKind}}-{{.Group}}-{{.ResourceAPIVersion}}.json' \
  /tmp/rendered.yaml
```

Expected: no violations. (If the Gateway API schema isn't in that repo, a fallback is `-skip HTTPRoute,GRPCRoute` — but try without first.)

### Step 4: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/gateway-httproute.yaml \
        deploy/helm/cyoda/templates/gateway-grpcroute.yaml
git commit -m "feat(helm): Gateway API HTTPRoute + GRPCRoute templates

Rendered when gateway.enabled=true (default). Both route kinds
parentRef into an operator-provided shared Gateway (chart doesn't
render its own Gateway — see spec Non-goals). Chart 'fail's fast if
both gateway.enabled and ingress.enabled are set.

Gateway API GA since k8s 1.31 and is the successor to ingress-nginx
(retired March 2026). First-class GRPCRoute replaces annotation-based
gRPC routing hacks."
```

---

## Task 12: Transitional Ingress templates

Spec reference: design §3 "Routing — ... Ingress transitional".

**Files:**
- Create: `deploy/helm/cyoda/templates/ingress-http.yaml`
- Create: `deploy/helm/cyoda/templates/ingress-grpc.yaml`

### Step 1: Create HTTP Ingress template

- [ ] Create `deploy/helm/cyoda/templates/ingress-http.yaml`:

```yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "cyoda.fullname" . }}-http
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
  {{- with .Values.ingress.http.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- with .Values.ingress.className }}
  ingressClassName: {{ . | quote }}
  {{- end }}
  {{- with .Values.ingress.http.tls }}
  tls:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  rules:
    - host: {{ .Values.ingress.http.host | quote }}
      http:
        paths:
          {{- range .Values.ingress.http.paths }}
          - path: {{ .path | quote }}
            pathType: {{ .pathType }}
            backend:
              service:
                name: {{ include "cyoda.fullname" $ }}
                port:
                  name: http
          {{- end }}
{{- end }}
```

### Step 2: Create gRPC Ingress template

- [ ] Create `deploy/helm/cyoda/templates/ingress-grpc.yaml`:

```yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "cyoda.fullname" . }}-grpc
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
  {{- with .Values.ingress.grpc.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- with .Values.ingress.className }}
  ingressClassName: {{ . | quote }}
  {{- end }}
  {{- with .Values.ingress.grpc.tls }}
  tls:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  rules:
    - host: {{ .Values.ingress.grpc.host | quote }}
      http:
        paths:
          {{- range .Values.ingress.grpc.paths }}
          - path: {{ .path | quote }}
            pathType: {{ .pathType }}
            backend:
              service:
                name: {{ include "cyoda.fullname" $ }}
                port:
                  name: grpc
          {{- end }}
{{- end }}
```

### Step 3: Render in ingress mode and validate

- [ ] Run:

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set cluster.hmacSecret.existingSecret=test-hmac \
    --set bootstrap.clientSecret.existingSecret=test-bootstrap \
    --set gateway.enabled=false \
    --set ingress.enabled=true \
    --set ingress.className=nginx \
    --set ingress.http.host=cyoda.example.com \
    --set ingress.grpc.host=grpc.cyoda.example.com \
  > /tmp/rendered.yaml
```

Output should contain two `kind: Ingress` entries.

- [ ] Run: `kubeconform -strict -kubernetes-version 1.31.0 /tmp/rendered.yaml`
  Expected: no violations.

### Step 4: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/ingress-http.yaml \
        deploy/helm/cyoda/templates/ingress-grpc.yaml
git commit -m "feat(helm): transitional HTTP + gRPC Ingress templates

Off by default; enabled via ingress.enabled=true for operators on
still-maintained Ingress controllers (Traefik, Kong, HAProxy). The
gRPC Ingress pre-seeds nginx.ingress.kubernetes.io/backend-protocol:
GRPC; operators on other controllers override the annotations.

ingress-nginx itself retired March 2026. This path exists to unblock
operators mid-migration to Gateway API and will be deprecated in a
future chart major version."
```

---

## Task 13: ServiceMonitor, NetworkPolicy, and helm test hook

Spec reference: design §3 "Observability" and "NetworkPolicy (optional, v0.1)" and §2 tests subdirectory.

**Files:**
- Create: `deploy/helm/cyoda/templates/servicemonitor.yaml`
- Create: `deploy/helm/cyoda/templates/networkpolicy.yaml`
- Create: `deploy/helm/cyoda/templates/tests/test-readyz.yaml`

### Step 1: Create ServiceMonitor template

- [ ] Create `deploy/helm/cyoda/templates/servicemonitor.yaml`:

```yaml
{{- if .Values.monitoring.serviceMonitor.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "cyoda.fullname" . }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
    {{- with .Values.monitoring.serviceMonitor.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  selector:
    matchLabels:
      {{- include "cyoda.selectorLabels" . | nindent 6 }}
  endpoints:
    - port: metrics
      interval: {{ .Values.monitoring.serviceMonitor.interval }}
      path: /metrics
{{- end }}
```

### Step 2: Create NetworkPolicy template

- [ ] Create `deploy/helm/cyoda/templates/networkpolicy.yaml`:

```yaml
{{- if .Values.networkPolicy.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "cyoda.fullname" . }}
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      {{- include "cyoda.selectorLabels" . | nindent 6 }}
  policyTypes:
    - Ingress
  ingress:
    # Application traffic: unrestricted at this layer. The Gateway/Ingress
    # is the boundary.
    - ports:
        - port: 8080
          protocol: TCP
        - port: 9090
          protocol: TCP

    # Metrics scraping: only from operator-declared namespaces (typically
    # the Prometheus namespace).
    {{- with .Values.networkPolicy.metricsFromNamespaces }}
    - from:
        {{- range . }}
        - namespaceSelector:
            {{- toYaml . | nindent 12 }}
        {{- end }}
      ports:
        - port: 9091
          protocol: TCP
    {{- end }}

    # Gossip: peer-to-peer, only from chart-managed pods.
    - from:
        - podSelector:
            matchLabels:
              {{- include "cyoda.selectorLabels" . | nindent 14 }}
      ports:
        - port: 7946
          protocol: TCP
        - port: 7946
          protocol: UDP
{{- end }}
```

### Step 3: Create helm test hook

- [ ] Create `deploy/helm/cyoda/templates/tests/test-readyz.yaml`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: {{ include "cyoda.fullname" . }}-test-readyz
  labels:
    {{- include "cyoda.labels" . | nindent 4 }}
  annotations:
    "helm.sh/hook": test
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
spec:
  restartPolicy: Never
  containers:
    - name: curl
      image: curlimages/curl:8.10.1
      command:
        - sh
        - -c
        - |
          set -eux
          # Hit /readyz via in-cluster DNS on the metrics port.
          curl -fsS --max-time 5 http://{{ include "cyoda.fullname" . }}:9091/readyz
```

### Step 4: Render with each opt-in and validate

- [ ] Run:

```bash
helm template cyoda-test deploy/helm/cyoda \
    --set postgres.existingSecret=test-dsn \
    --set jwt.existingSecret=test-jwt \
    --set cluster.hmacSecret.existingSecret=test-hmac \
    --set bootstrap.clientSecret.existingSecret=test-bootstrap \
    --set gateway.parentRefs[0].name=test-gw \
    --set gateway.http.hostnames[0]=cyoda.example.com \
    --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
    --set monitoring.serviceMonitor.enabled=true \
    --set networkPolicy.enabled=true \
    --set networkPolicy.metricsFromNamespaces[0].matchLabels."kubernetes\.io/metadata\.name"=monitoring \
  | grep "^kind: " | sort -u
```

Expected output includes `ServiceMonitor`, `NetworkPolicy`, and `Pod` (from the test hook).

### Step 5: Commit

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/servicemonitor.yaml \
        deploy/helm/cyoda/templates/networkpolicy.yaml \
        deploy/helm/cyoda/templates/tests/test-readyz.yaml
git commit -m "feat(helm): ServiceMonitor + NetworkPolicy + helm test hook

ServiceMonitor rendered when monitoring.serviceMonitor.enabled=true —
selects port 'metrics' on the main Service for kube-prometheus-stack
scraping.

NetworkPolicy rendered when networkPolicy.enabled=true. Restricts
metrics-port ingress to operator-declared namespaces (typically
Prometheus) and gossip-port ingress to chart-managed pods. Default
off because enforcement requires a CNI that supports NetworkPolicy
(Calico, Cilium, Weave; not the default kindnet).

helm test hook pod hits /readyz via in-cluster DNS — operators run
'helm test cyoda' after install for a smoke check."
```

---

## Task 14: NOTES.txt and chart README

Spec reference: design §2 directory (NOTES.txt, README.md) + design §4 (GitOps guidance) + design §3 (reference topology).

**Files:**
- Create: `deploy/helm/cyoda/templates/NOTES.txt`
- Create: `deploy/helm/cyoda/README.md`

### Step 1: Create NOTES.txt

- [ ] Create `deploy/helm/cyoda/templates/NOTES.txt`:

```
cyoda v{{ .Chart.AppVersion }} installed into namespace {{ .Release.Namespace }}.

Routing:
{{- if .Values.gateway.enabled }}
  Gateway API is enabled. Routes created:
    HTTPRoute:  {{ join ", " .Values.gateway.http.hostnames }}
    GRPCRoute:  {{ join ", " .Values.gateway.grpc.hostnames }}
  Ensure the parent Gateway has listeners that accept these hostnames.
{{- else if .Values.ingress.enabled }}
  Ingress is enabled (transitional path — prefer gateway.enabled=true).
    HTTP  Ingress: {{ .Values.ingress.http.host }}
    gRPC  Ingress: {{ .Values.ingress.grpc.host }}
{{- else }}
  No external routing configured. In-cluster access via Service
  {{ include "cyoda.fullname" . }} on port 8080 (http) / 9090 (grpc).
{{- end }}

Credentials (retrieve chart-managed secrets):
{{- if not .Values.cluster.hmacSecret.existingSecret }}
  kubectl get secret {{ include "cyoda.hmacSecretName" . }} -n {{ .Release.Namespace }} \
    -o jsonpath='{.data.{{ .Values.cluster.hmacSecret.existingSecretKey }}}' | base64 -d
{{- end }}
{{- if not .Values.bootstrap.clientSecret.existingSecret }}
  kubectl get secret {{ include "cyoda.bootstrapSecretName" . }} -n {{ .Release.Namespace }} \
    -o jsonpath='{.data.{{ .Values.bootstrap.clientSecret.existingSecretKey }}}' | base64 -d
{{- end }}

Smoke test:
  helm test {{ .Release.Name }} -n {{ .Release.Namespace }}

WARNING: do not set the following env vars via extraEnv — the chart
already sets them and Kubernetes rejects duplicates:
  CYODA_POSTGRES_URL / CYODA_POSTGRES_URL_FILE
  CYODA_JWT_SIGNING_KEY / CYODA_JWT_SIGNING_KEY_FILE
  CYODA_HMAC_SECRET / CYODA_HMAC_SECRET_FILE
  CYODA_BOOTSTRAP_CLIENT_SECRET / CYODA_BOOTSTRAP_CLIENT_SECRET_FILE

To override any of the four credentials, change the referenced
existingSecret — do not add an extraEnv entry.
```

### Step 2: Create chart README

- [ ] Create `deploy/helm/cyoda/README.md`:

```markdown
# cyoda Helm chart

Production-ready Helm deployment of cyoda-go backed by an external
Postgres, fronted by Gateway API (default) or a still-maintained
Ingress controller (transitional).

Chart version: 0.1.0 — AppVersion: pinned by `bump-chart-appversion.yml`
on each binary release.

## Installation

### Prerequisites

- Kubernetes 1.31+ (Gateway API CRDs required if using `gateway.enabled=true`).
- An existing Postgres instance reachable from the cluster, with a
  dedicated database and role for cyoda.
- A JWT RSA signing key. Generate with:
  ```bash
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 \
    -out jwt-signing-key.pem
  ```

### Create the required Secrets

```bash
kubectl create namespace cyoda

kubectl -n cyoda create secret generic cyoda-dsn \
  --from-literal=dsn='postgres://cyoda:REDACTED@pg.example.com:5432/cyoda?sslmode=require'

kubectl -n cyoda create secret generic cyoda-jwt \
  --from-file=signing-key.pem=./jwt-signing-key.pem
```

### Install

```bash
helm repo add cyoda https://cyoda-platform.github.io/cyoda-go
helm repo update

helm install cyoda cyoda/cyoda -n cyoda \
  --set postgres.existingSecret=cyoda-dsn \
  --set jwt.existingSecret=cyoda-jwt \
  --set gateway.parentRefs[0].name=platform-gateway \
  --set gateway.parentRefs[0].namespace=gateway-system \
  --set gateway.http.hostnames[0]=cyoda.example.com \
  --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com
```

### Scale to 3 replicas (cluster mode)

```bash
helm upgrade cyoda cyoda/cyoda -n cyoda \
  --reuse-values \
  --set replicas=3
```

No mode flip needed — cluster mode is always on; at replicas=1 it runs
as a "cluster of one".

## Using with GitOps (Argo CD)

The chart auto-generates the HMAC and bootstrap-client Secrets via
Helm's `lookup` function on first install. **This does not work with
Argo CD's default render path** (which uses `helm template`, where
`lookup` is a no-op). Without mitigation, Argo CD would re-randomize
the secrets on every reconcile, breaking gossip encryption and
inter-node HTTP dispatch auth.

The chart catches this at render time and fails with an actionable
error message. To fix:

**Option A: pre-create the Secrets and pass `existingSecret`:**

```bash
kubectl -n cyoda create secret generic cyoda-hmac \
  --from-literal=secret=$(openssl rand -hex 32)
kubectl -n cyoda create secret generic cyoda-bootstrap \
  --from-literal=secret=$(openssl rand -hex 32)
```

```yaml
cluster:
  hmacSecret:
    existingSecret: cyoda-hmac
bootstrap:
  clientSecret:
    existingSecret: cyoda-bootstrap
```

**Option B: use external-secrets-operator** to sync from a real secret
store (Vault, AWS Secrets Manager, etc.) into the two Secret names.

## Reference topology (Gateway API + Cloudflare tunnel)

```
     ┌─────────────────────────┐
     │ External origin         │
     │ (Cloudflare tunnel etc) │
     └──────────┬──────────────┘
                │
     ┌──────────▼──────────────┐
     │ Gateway (platform ns)   │
     │ envoy-gateway, contour, │
     │ cilium, istio…          │
     └──┬────────────────────┬─┘
        │ HTTPRoute          │ GRPCRoute
        │                    │
    ┌───▼───┐            ┌───▼───┐
    │Service│            │Service│
    │cyoda: │            │cyoda: │
    │ http  │            │ grpc  │
    └───┬───┘            └───┬───┘
        │                    │
        └─────────┬──────────┘
                  │
             ┌────▼────┐
             │  cyoda  │
             │ pod(s)  │
             └─────────┘
```

## Migrating from ingress-nginx

`ingress-nginx` was retired by SIG Network in March 2026. Use the
`ingress` values block in this chart as a transitional affordance
until you've migrated to Gateway API. For migration tooling, see
[Ingress2Gateway 1.0](https://kubernetes.io/blog/2026/03/20/ingress2gateway-1-0-release/).

## Values reference

See [`values.yaml`](./values.yaml). Every key is documented inline.

## Troubleshooting

### "cluster.hmacSecret.existingSecret is required when the chart is rendered without live cluster access"

You're running `helm template`, `helm install --dry-run`, Argo CD
default path, or installing into a not-yet-created namespace. See
"Using with GitOps" above.

### `helm upgrade` hangs on the migration Job

Check logs: `kubectl logs -n cyoda job/cyoda-migrate-<release-revision>`.
If the migration is slow, increase `migrate.activeDeadlineSeconds`.
If the Job fails permanently, Helm rolls back values and old pods keep
serving — investigate before retrying.

### `CYODA_*_FILE` in `extraEnv` causes install to fail with duplicate env

Remove it. The chart sets all four credential env vars; to change a
credential, change the referenced `existingSecret`.
```

### Step 3: Render, helm lint, and commit

- [ ] Run: `helm lint deploy/helm/cyoda`
  Expected: clean lint.

- [ ] Run:

```bash
git add deploy/helm/cyoda/templates/NOTES.txt deploy/helm/cyoda/README.md
git commit -m "feat(helm): NOTES.txt + chart README

NOTES.txt: post-install guidance — configured hostnames, how to
retrieve auto-generated Secrets, helm test pointer, extraEnv collision
warning on the four credential env vars.

README: prerequisites, installation flow (including secret setup),
scale-up with a single --set, GitOps guidance (the GitOps-safety
guard and the two escape hatches), reference topology diagram,
migration guidance from ingress-nginx, troubleshooting.

Closes the chart's operator documentation for v0.1."
```

---

## Task 15: Chart CI — layer 1 (lint + template + kubeconform)

Spec reference: design §7 "Layer 1 — lint + template + validate".

**Files:**
- Create: `.github/workflows/helm-chart-ci.yml`
- Create: `.github/ct.yaml`

### Step 1: Create `.github/ct.yaml`

- [ ] Create `.github/ct.yaml`:

```yaml
remote: origin
target-branch: main
chart-dirs:
  - deploy/helm
charts:
  - deploy/helm/cyoda
validate-maintainers: false
check-version-increment: true
helm-extra-args: "--timeout 5m"
helm-extra-set-args: >-
  --set postgres.existingSecret=test-dsn
  --set jwt.existingSecret=test-jwt
```

### Step 2: Create the CI workflow — layer 1 only for now

- [ ] Create `.github/workflows/helm-chart-ci.yml`:

```yaml
name: Helm chart CI

on:
  push:
    branches: [main]
    paths:
      - 'deploy/helm/**'
      - '.github/workflows/helm-chart-ci.yml'
      - '.github/ct.yaml'
  pull_request:
    paths:
      - 'deploy/helm/**'
      - '.github/workflows/helm-chart-ci.yml'
      - '.github/ct.yaml'

jobs:
  helm-lint-and-validate:
    name: Lint, template, kubeconform
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: azure/setup-helm@v4
        with:
          version: v3.16.2

      - name: Install kubeconform
        run: |
          curl -sSL \
            https://github.com/yannh/kubeconform/releases/download/v0.6.7/kubeconform-linux-amd64.tar.gz \
            | tar -xz
          sudo mv kubeconform /usr/local/bin/
          kubeconform -v

      - name: helm lint
        run: helm lint deploy/helm/cyoda

      - name: helm template — default (Gateway API, replicas=1)
        run: |
          helm template cyoda deploy/helm/cyoda \
            --set postgres.existingSecret=test-dsn \
            --set jwt.existingSecret=test-jwt \
            --set cluster.hmacSecret.existingSecret=test-hmac \
            --set bootstrap.clientSecret.existingSecret=test-bootstrap \
            --set gateway.parentRefs[0].name=test-gw \
            --set gateway.parentRefs[0].namespace=gateway-system \
            --set gateway.http.hostnames[0]=cyoda.example.com \
            --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
            > /tmp/default.yaml
          kubeconform -strict -kubernetes-version 1.31.0 \
            -schema-location default \
            -schema-location 'https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.31.0-standalone-strict/{{.ResourceKind}}-{{.Group}}-{{.ResourceAPIVersion}}.json' \
            -skip HTTPRoute,GRPCRoute \
            /tmp/default.yaml

      - name: helm template — ingress mode
        run: |
          helm template cyoda deploy/helm/cyoda \
            --set postgres.existingSecret=test-dsn \
            --set jwt.existingSecret=test-jwt \
            --set cluster.hmacSecret.existingSecret=test-hmac \
            --set bootstrap.clientSecret.existingSecret=test-bootstrap \
            --set gateway.enabled=false \
            --set ingress.enabled=true \
            --set ingress.className=nginx \
            --set ingress.http.host=cyoda.example.com \
            --set ingress.grpc.host=grpc.cyoda.example.com \
            > /tmp/ingress.yaml
          kubeconform -strict -kubernetes-version 1.31.0 /tmp/ingress.yaml

      - name: helm template — 3-replica cluster mode
        run: |
          helm template cyoda deploy/helm/cyoda \
            --set replicas=3 \
            --set postgres.existingSecret=test-dsn \
            --set jwt.existingSecret=test-jwt \
            --set cluster.hmacSecret.existingSecret=test-hmac \
            --set bootstrap.clientSecret.existingSecret=test-bootstrap \
            --set gateway.parentRefs[0].name=test-gw \
            --set gateway.http.hostnames[0]=cyoda.example.com \
            --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
            > /tmp/replicas3.yaml
          kubeconform -strict -kubernetes-version 1.31.0 \
            -skip HTTPRoute,GRPCRoute \
            /tmp/replicas3.yaml
          # Must render a PDB at replicas=3.
          grep -q "kind: PodDisruptionBudget" /tmp/replicas3.yaml

      - name: helm template — monitoring + networkPolicy
        run: |
          helm template cyoda deploy/helm/cyoda \
            --set postgres.existingSecret=test-dsn \
            --set jwt.existingSecret=test-jwt \
            --set cluster.hmacSecret.existingSecret=test-hmac \
            --set bootstrap.clientSecret.existingSecret=test-bootstrap \
            --set gateway.parentRefs[0].name=test-gw \
            --set gateway.http.hostnames[0]=cyoda.example.com \
            --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
            --set monitoring.serviceMonitor.enabled=true \
            --set networkPolicy.enabled=true \
            --set 'networkPolicy.metricsFromNamespaces[0].matchLabels.kubernetes\.io/metadata\.name=monitoring' \
            > /tmp/monitoring.yaml
          kubeconform -strict -kubernetes-version 1.31.0 \
            -skip HTTPRoute,GRPCRoute,ServiceMonitor \
            /tmp/monitoring.yaml
          grep -q "kind: ServiceMonitor" /tmp/monitoring.yaml
          grep -q "kind: NetworkPolicy" /tmp/monitoring.yaml

      - name: helm template — GitOps safety guard fires when expected
        run: |
          # Without cluster.hmacSecret.existingSecret AND without live
          # cluster access (helm template), the guard must fire.
          set +e
          output=$(helm template cyoda deploy/helm/cyoda \
            --set postgres.existingSecret=test-dsn \
            --set jwt.existingSecret=test-jwt \
            --set bootstrap.clientSecret.existingSecret=test-bootstrap \
            --set gateway.parentRefs[0].name=test-gw \
            --set gateway.http.hostnames[0]=cyoda.example.com \
            --set gateway.grpc.hostnames[0]=grpc.cyoda.example.com \
            2>&1)
          set -e
          if ! echo "$output" | grep -q "cluster.hmacSecret.existingSecret is required"; then
            echo "FAIL: expected GitOps-safety guard to fire; got:"
            echo "$output"
            exit 1
          fi
          echo "PASS: guard fired as expected."
```

The `-skip HTTPRoute,GRPCRoute,ServiceMonitor` flag tells kubeconform to ignore unknown CRD-based kinds when a schema URL isn't available. For layer 2 we'll add real schemas; for layer 1 the render-without-error check is the main value.

### Step 3: Push and verify CI passes

- [ ] Push the branch and watch the workflow on GitHub Actions.
  Expected: all steps PASS.

If local simulation is preferred:

- [ ] Run locally the exact sequence of `helm template` and `kubeconform` commands from the workflow. All should pass.

### Step 4: Commit

- [ ] Run:

```bash
git add .github/workflows/helm-chart-ci.yml .github/ct.yaml
git commit -m "ci(helm): layer 1 — lint + template + kubeconform

Runs on every PR touching deploy/helm/** or the workflow file itself.
Five scenarios exercised:
  - Default (Gateway API, replicas=1)
  - Ingress mode
  - 3-replica cluster mode (asserts PDB rendered)
  - Monitoring + NetworkPolicy opt-in
  - GitOps safety guard fires when chart-managed Secrets are enabled
    without live cluster access

~30 seconds total. Catches template syntax, schema violations, and
the guard-must-fire assertion so a regression in the GitOps mitigation
trips CI rather than shipping silently."
```

---

## Task 16: Chart CI — layer 2 (ct install on kind with Envoy Gateway)

Spec reference: design §7 "Layer 2 — `ct install` on kind".

**Files:**
- Modify: `.github/workflows/helm-chart-ci.yml` (add second job)

### Step 1: Append the layer 2 job

- [ ] Edit `.github/workflows/helm-chart-ci.yml` and add below the existing job:

```yaml
  helm-install-smoke:
    name: ct install on kind
    needs: helm-lint-and-validate
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: azure/setup-helm@v4
        with:
          version: v3.16.2

      - name: Set up kind cluster
        uses: helm/kind-action@v1
        with:
          version: v0.24.0
          cluster_name: cyoda-chart-ci
          kubernetes_version: v1.31.0
          wait: 120s

      - name: Install Gateway API v1.2 CRDs
        run: |
          kubectl apply -f \
            https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
          kubectl wait --for=condition=established crd/gateways.gateway.networking.k8s.io --timeout=60s

      - name: Install Envoy Gateway
        run: |
          helm install eg oci://docker.io/envoyproxy/gateway-helm \
            --version v1.2.0 \
            -n envoy-gateway-system \
            --create-namespace \
            --wait --timeout 5m

      - name: Start Postgres sidecar
        run: |
          kubectl create namespace cyoda
          kubectl -n cyoda run pg \
            --image=postgres:16-alpine \
            --env=POSTGRES_PASSWORD=test \
            --env=POSTGRES_DB=cyoda \
            --env=POSTGRES_USER=cyoda \
            --port=5432
          kubectl -n cyoda expose pod pg --port=5432
          kubectl -n cyoda wait --for=condition=ready pod/pg --timeout=120s

      - name: Generate ephemeral JWT signing key
        run: |
          openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 \
            -out /tmp/test-jwt.pem

      - name: Create test Secrets in cyoda namespace
        run: |
          kubectl -n cyoda create secret generic test-dsn \
            --from-literal=dsn='postgres://cyoda:test@pg:5432/cyoda?sslmode=disable'
          kubectl -n cyoda create secret generic test-jwt \
            --from-file=signing-key.pem=/tmp/test-jwt.pem

      - name: Create Gateway for CI
        run: |
          kubectl apply -n cyoda -f - <<'EOF'
          apiVersion: gateway.networking.k8s.io/v1
          kind: Gateway
          metadata:
            name: test-gateway
          spec:
            gatewayClassName: envoy-gateway
            listeners:
              - name: http
                port: 80
                protocol: HTTP
                allowedRoutes:
                  namespaces:
                    from: Same
          EOF

      - name: chart-testing install
        run: |
          docker run --rm \
            --network host \
            -v "$HOME/.kube":/root/.kube \
            -v "$(pwd)":/workspace \
            -w /workspace \
            quay.io/helmpack/chart-testing:v3.11.0 \
            ct install \
              --config .github/ct.yaml \
              --namespace cyoda \
              --helm-extra-set-args \
                "--set=postgres.existingSecret=test-dsn \
                 --set=jwt.existingSecret=test-jwt \
                 --set=gateway.parentRefs[0].name=test-gateway \
                 --set=gateway.http.hostnames[0]=cyoda.ci \
                 --set=gateway.grpc.hostnames[0]=grpc.cyoda.ci"

      - name: Smoke test /readyz via port-forward
        run: |
          kubectl -n cyoda port-forward svc/cyoda 9091:metrics &
          PF_PID=$!
          trap "kill $PF_PID" EXIT
          sleep 3
          curl -fsS --max-time 5 http://localhost:9091/readyz

      - name: helm test cyoda
        run: kubectl wait --for=condition=complete -n cyoda job/cyoda-migrate-1 --timeout=300s && helm test cyoda -n cyoda

      - name: Collect diagnostics on failure
        if: failure()
        run: |
          kubectl -n cyoda get all
          kubectl -n cyoda describe pods
          kubectl -n cyoda logs -l app.kubernetes.io/name=cyoda --all-containers --tail=200 || true
          kubectl -n cyoda logs job/cyoda-migrate-1 --all-containers --tail=200 || true
```

### Step 2: Push and verify on CI

- [ ] Push and watch the second job run. It takes ~3-5 minutes (kind startup + Envoy Gateway install + Postgres + chart install).
  Expected: PASS end-to-end including `helm test`.

If diagnostics show issues:
- Migration Job failing → check DSN, check the binary's `migrate` subcommand works with `--sslmode=disable`.
- Pod crash-looping → check the projected volume mounts; the four Secrets must exist before the StatefulSet starts.
- `helm install` timing out → increase `--timeout` in `ct.yaml`.

### Step 3: Commit

- [ ] Run:

```bash
git add .github/workflows/helm-chart-ci.yml
git commit -m "ci(helm): layer 2 — ct install on kind with Envoy Gateway

Second job in the workflow, runs after layer 1 passes. Stands up:
  - kind cluster on Kubernetes 1.31
  - Gateway API v1.2 CRDs
  - Envoy Gateway (reference Gateway API implementation)
  - Postgres sidecar (postgres:16-alpine)
  - Ephemeral RSA signing key generated at runtime (no committed PEM)
  - Gateway resource for Gateway API routing

Then runs 'ct install', waits for /readyz, and runs 'helm test cyoda'.
Failure-mode diagnostics collect pod state and logs for debugging.

Catches real install-time failures: Secret wiring, probe paths, the
migration Job running end-to-end, Gateway API routing actually working."
```

---

## Task 17: Release workflows activation and MAINTAINING.md

Spec reference: design §6 "Release mechanics".

**Files:**
- Modify: `.github/workflows/release-chart.yml`
- Modify: `.github/workflows/bump-chart-appversion.yml`
- Modify: `MAINTAINING.md` (create if doesn't exist)

### Step 1: Check current state of the pre-stub workflows

- [ ] Run: `cat .github/workflows/release-chart.yml .github/workflows/bump-chart-appversion.yml`
  Read the existing content. Preserve any guardrails already present; add the active-work steps on top.

### Step 2: Replace `release-chart.yml` with the fully active version

- [ ] Overwrite `.github/workflows/release-chart.yml`:

```yaml
name: Release chart

on:
  push:
    tags:
      - 'cyoda-*'

jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write
      pages: write
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Verify chart directory exists
        run: |
          if [ ! -f deploy/helm/cyoda/Chart.yaml ]; then
            echo "::error::deploy/helm/cyoda/Chart.yaml not present; cannot release chart at tag ${GITHUB_REF}"
            exit 1
          fi

      - name: Verify Chart.yaml version matches tag
        run: |
          TAG_VERSION="${GITHUB_REF#refs/tags/cyoda-}"
          CHART_VERSION=$(yq eval '.version' deploy/helm/cyoda/Chart.yaml)
          if [ "$TAG_VERSION" != "$CHART_VERSION" ]; then
            echo "::error::Tag version ($TAG_VERSION) does not match Chart.yaml version ($CHART_VERSION)."
            exit 1
          fi

      - name: Verify GitHub Pages is configured
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          # gh api returns 404 if Pages has never been enabled on the repo.
          # First-release foot-gun: tag pushed, gh-pages populated, but
          # nothing is served. Catch it at release time.
          if ! gh api "repos/${GITHUB_REPOSITORY}/pages" >/dev/null 2>&1; then
            cat <<'EOF'
          ::error::GitHub Pages is not configured for this repo.
          Enable Pages before the first release:
            Repo Settings → Pages → Source: "Deploy from a branch"
            Branch: gh-pages / (root)
          See MAINTAINING.md.
          EOF
            exit 1
          fi

      - uses: azure/setup-helm@v4
        with:
          version: v3.16.2

      - name: Install kubeconform
        run: |
          curl -sSL \
            https://github.com/yannh/kubeconform/releases/download/v0.6.7/kubeconform-linux-amd64.tar.gz \
            | tar -xz
          sudo mv kubeconform /usr/local/bin/

      - name: helm lint
        run: helm lint deploy/helm/cyoda

      - name: helm template + kubeconform
        run: |
          helm template cyoda deploy/helm/cyoda \
            --set postgres.existingSecret=x \
            --set jwt.existingSecret=x \
            --set cluster.hmacSecret.existingSecret=x \
            --set bootstrap.clientSecret.existingSecret=x \
            --set gateway.parentRefs[0].name=x \
            --set gateway.http.hostnames[0]=x \
            --set gateway.grpc.hostnames[0]=x \
          | kubeconform -strict -kubernetes-version 1.31.0 \
              -skip HTTPRoute,GRPCRoute

      - name: Configure Git for chart-releaser
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "41898282+github-actions[bot]@users.noreply.github.com"

      - name: Run chart-releaser
        uses: helm/chart-releaser-action@v1.6.0
        with:
          charts_dir: deploy/helm
          skip_existing: false
          mark_as_latest: true
        env:
          CR_TOKEN: "${{ secrets.GITHUB_TOKEN }}"
```

### Step 3: Replace `bump-chart-appversion.yml` with the fully active version

- [ ] Overwrite `.github/workflows/bump-chart-appversion.yml`:

```yaml
name: Bump chart appVersion

on:
  push:
    tags:
      - 'v*'

jobs:
  bump:
    runs-on: ubuntu-latest
    permissions:
      contents: write
      pull-requests: write
    # Skip pre-release tags (v0.2.0-rc.1, v0.3.0-beta.1, etc.)
    if: "!contains(github.ref, '-')"
    steps:
      - uses: actions/checkout@v4
        with:
          ref: main
          fetch-depth: 0

      - name: Verify chart directory exists
        run: |
          if [ ! -f deploy/helm/cyoda/Chart.yaml ]; then
            echo "::warning::deploy/helm/cyoda/Chart.yaml not present; skipping appVersion bump."
            exit 0
          fi

      - name: Install yq
        uses: mikefarah/yq@master

      - name: Update Chart.yaml appVersion
        env:
          NEW_VERSION: ${{ github.ref_name }}
        run: |
          VERSION="${NEW_VERSION#v}"
          export VERSION
          yq eval '.appVersion = strenv(VERSION)' -i deploy/helm/cyoda/Chart.yaml
          echo "Updated appVersion to $VERSION"
          cat deploy/helm/cyoda/Chart.yaml

      - name: Create PR
        uses: peter-evans/create-pull-request@v7
        with:
          branch: chore/bump-chart-appversion-${{ github.ref_name }}
          commit-message: |
            chore(helm): bump chart appVersion to ${{ github.ref_name }}

            Triggered by binary release ${{ github.ref_name }}.

            A human reviews this PR. If the appVersion bump should
            trigger a chart release, also bump chart version in the
            same PR and tag the chart after merge.
          title: "chore(helm): bump chart appVersion to ${{ github.ref_name }}"
          body: |
            Automated appVersion bump following binary release
            **${{ github.ref_name }}**.

            This PR updates `deploy/helm/cyoda/Chart.yaml` `appVersion`
            to match. It does NOT touch chart `version:`.

            Reviewer: if this bump should advertise the new binary as
            the default (chart release), also increment chart `version`
            in this PR, then after merge tag with `cyoda-<new-version>`
            to trigger `release-chart.yml`.
          labels: helm, chart-release
```

### Step 4: Create or update `MAINTAINING.md`

- [ ] Check if `MAINTAINING.md` exists: `ls MAINTAINING.md 2>/dev/null`
- [ ] Create or update with a section documenting the Pages prerequisite:

```markdown
# Maintaining cyoda-go

## Prerequisites for chart releases

Before the first `cyoda-*` tag is pushed (chart release), the repo
maintainer must enable GitHub Pages:

1. Repo Settings → Pages
2. Source: "Deploy from a branch"
3. Branch: `gh-pages` / `(root)`
4. Save

The `gh-pages` branch is created by `chart-releaser-action` on first
release and does not need to pre-exist.

The `release-chart.yml` workflow verifies Pages is configured via
`gh api repos/:owner/:repo/pages` and fails fast with an actionable
message if not — but the setup must happen once, by a human, before
any `cyoda-*` tag.

## Chart version vs binary appVersion

Two independent tag streams:

- `v*` (e.g. `v0.2.0`): binary release. Triggers:
  - `release.yml` (binaries + container image)
  - `bump-chart-appversion.yml` (opens PR bumping chart appVersion)
- `cyoda-*` (e.g. `cyoda-0.2.0`): chart release. Triggers:
  - `release-chart.yml` (packages + publishes to gh-pages)

Standard pattern: merge the appVersion-bump PR, optionally also bump
chart `version:` in the same PR if shipping a chart release, then tag
`cyoda-<new-version>` after merge.
```

### Step 5: Commit

- [ ] Run:

```bash
git add .github/workflows/release-chart.yml \
        .github/workflows/bump-chart-appversion.yml \
        MAINTAINING.md
git commit -m "ci(helm): activate release-chart + bump-chart-appversion workflows

release-chart.yml now does real work on cyoda-* tags:
  - Verifies chart dir exists and Chart.yaml version matches tag
  - Verifies GitHub Pages is configured via gh api (catches the
    first-release foot-gun where Pages is never enabled)
  - Lints + templates + kubeconform-validates
  - Invokes helm/chart-releaser-action to publish to gh-pages

bump-chart-appversion.yml now uses yq (not sed) to update the
Chart.yaml appVersion field on v* tags (pre-release tags skipped).
sed is brittle against YAML quoting/anchor variations; yq is a
standard GitHub Actions step.

MAINTAINING.md documents the one-time Pages-enable prerequisite that
has to happen by a human before the first chart release."
```

---

## Task 18: Integration validation — full end-to-end smoke

**Files:** none (validation-only task)

### Step 1: Run the full binary test suite

- [ ] Run: `go test ./... -v`
  Expected: all PASS including E2E tests (requires Docker).

### Step 2: Run `go vet` and race detector

- [ ] Run: `go vet ./...`
  Expected: clean.

- [ ] Run: `go test -short -race ./...`
  Expected: PASS. Any race-detector finding is a blocker — fix before proceeding.

### Step 3: Run the plugin submodule tests

- [ ] Per the "Plugin submodules need explicit test runs" memory, run each submodule:

```bash
(cd plugins/memory && go test -short ./...)
(cd plugins/sqlite && go test -short ./...)
(cd plugins/postgres && go test -short ./...)   # requires Docker
```

Expected: all PASS.

### Step 4: Verify the chart locally with kind (dry run of what CI does)

- [ ] If Docker and kind are available locally, run a quick install smoke:

```bash
kind create cluster --name cyoda-chart-local
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
helm install eg oci://docker.io/envoyproxy/gateway-helm --version v1.2.0 -n envoy-gateway-system --create-namespace --wait --timeout 5m

kubectl create namespace cyoda
kubectl -n cyoda run pg --image=postgres:16-alpine --env=POSTGRES_PASSWORD=test --env=POSTGRES_DB=cyoda --env=POSTGRES_USER=cyoda --port=5432
kubectl -n cyoda expose pod pg --port=5432
kubectl -n cyoda wait --for=condition=ready pod/pg --timeout=120s

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /tmp/test-jwt.pem
kubectl -n cyoda create secret generic test-dsn --from-literal=dsn='postgres://cyoda:test@pg:5432/cyoda?sslmode=disable'
kubectl -n cyoda create secret generic test-jwt --from-file=signing-key.pem=/tmp/test-jwt.pem

kubectl apply -n cyoda -f - <<'EOF'
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: test-gateway
spec:
  gatewayClassName: envoy-gateway
  listeners:
    - name: http
      port: 80
      protocol: HTTP
      allowedRoutes:
        namespaces:
          from: Same
EOF

helm install cyoda deploy/helm/cyoda -n cyoda \
  --set postgres.existingSecret=test-dsn \
  --set jwt.existingSecret=test-jwt \
  --set gateway.parentRefs[0].name=test-gateway \
  --set gateway.http.hostnames[0]=cyoda.local \
  --set gateway.grpc.hostnames[0]=grpc.cyoda.local \
  --wait --timeout 5m

kubectl -n cyoda port-forward svc/cyoda 9091:metrics &
curl -fsS http://localhost:9091/readyz
helm test cyoda -n cyoda

# Cleanup
kind delete cluster --name cyoda-chart-local
```

Expected: every step succeeds; `/readyz` returns 200; `helm test` reports PASS.

### Step 5: File the six follow-up issues listed in spec §8

- [ ] Create GitHub issues for F1–F5 (F6 was promoted to in-scope). Use `gh issue create` for each:

```bash
gh issue create --title "Chart CI layer 3 — multi-replica cluster-mode install with gossip coordination" --body "See docs/superpowers/specs/2026-04-17-provisioning-helm-design.md §8 — F1. Acceptance: CI job installs chart at replicas=3, gateway.enabled=true, verifies 3 pods reach Ready, verifies gossip.go logs 'cluster of 3', tears down cleanly. Runs on main + nightly."
gh issue create --title "Chart CI — helm upgrade migration-path testing" --body "See spec §8 — F2. When v0.2 ships with a schema change, add CI: install v0.1.0, upgrade to v0.2.0, verify migration Job ran and new pods serve traffic."
gh issue create --title "Migration guide: ingress.enabled=true → gateway.enabled=true via Ingress2Gateway 1.0" --body "See spec §8 — F3. Operator-facing doc at deploy/helm/cyoda/docs/migrating-from-ingress.md."
gh issue create --title "Chart docs: Gateway API PolicyAttachment patterns for rate limiting / auth" --body "See spec §8 — F4. Document recommended BackendTrafficPolicy / SecurityPolicy overlays for Envoy Gateway; chart does not render these."
gh issue create --title "Chart v0.2+: HPA, PodMonitor alternative, external-secrets-operator, fine-grained egress NetworkPolicy" --body "See spec §8 — F5. Each is a separable increment in its own minor chart version with its own values schema addition and tests."
```

### Step 6: Final commit (if any README polish is needed)

- [ ] Review the main-repo `README.md` one more time for any section still referring to pre-chart state. If found, update in a small follow-up commit.

```bash
# If anything changed:
git add README.md
git commit -m "docs: polish references to Helm after chart lands"
```

---

## Self-review

### Spec coverage

Cross-check each spec section against the task list:

| Spec section | Covered by |
|---|---|
| §1 In scope — Helm chart at deploy/helm/cyoda/ | Tasks 5–14 |
| §1 In scope — `cyoda migrate` subcommand | Task 3 |
| §1 In scope — `_FILE` suffix support | Task 1 |
| §1 In scope — bootstrap-secret tightening | Task 2 |
| §1 In scope — Chart CI two layers | Tasks 15 + 16 |
| §1 In scope — NetworkPolicy template | Task 13 |
| §1 In scope — release workflow activation | Task 17 |
| §2 Directory layout | Tasks 5 (Chart.yaml/helpers/values); 6 (SA/Service); 7 (ConfigMap); 8 (Secrets); 9 (StatefulSet); 10 (Job/PDB); 11 (Gateway routes); 12 (Ingress); 13 (ServiceMonitor/NetworkPolicy/test); 14 (NOTES/README) |
| §3 Workload (StatefulSet always, cluster-mode always) | Task 9 |
| §3 Services with gossip TCP+UDP | Task 6 |
| §3 Routing — Gateway API default, Ingress transitional | Tasks 11, 12 |
| §3 Observability — probes + ServiceMonitor + extraEnv | Tasks 9, 13 |
| §3 NetworkPolicy | Task 13 |
| §4 Four credentials, _FILE wiring | Tasks 1, 9 |
| §4 `_FILE` suffix in binary | Task 1 |
| §4 Auto-generation + GitOps guard | Task 8 |
| §4 No `resource-policy: keep` on either secret | Task 8 (explicitly absent) |
| §4 `existingSecretKey` knobs symmetric | Tasks 5 (values), 8 (chart-managed template writes under the knob), 9 (volume projection reads the knob) |
| §4 ConfigMap/Secret split | Task 7 |
| §4 `extraEnv` schema validation | Task 5 (schema) |
| §4 Bootstrap secret tightening + stdout-print removal | Task 2 |
| §5 Migration subcommand | Task 3 |
| §5 Migration Job with pre-install+pre-upgrade hooks | Task 10 |
| §6 release-chart.yml with Pages check | Task 17 |
| §6 bump-chart-appversion.yml with yq | Task 17 |
| §6 MAINTAINING.md | Task 17 |
| §7 Layer 1 CI | Task 15 |
| §7 Layer 2 CI | Task 16 |
| §7 Ephemeral JWT key (no committed PEM) | Task 16 |
| §8 Follow-up issues filed | Task 18, Step 5 |

All spec requirements have tasks.

### Placeholder scan

Grep for the red-flag patterns:

- `TBD` / `TODO` inline: none at the plan level. Two deliberate `// TODO:` stubs in Task 3's test helpers where the engineer must wire to the existing testcontainers helper — these are pointed at concrete existing code, not hand-waves.
- "implement later" / "fill in details": none.
- "Add appropriate error handling": none — all error paths have concrete code.
- "Similar to Task N": none.

### Type / method consistency

- `resolveSecretEnv(name string) (string, error)` — consistent across Tasks 1 and 3.
- `runMigrate(args []string) int`, `parseMigrateArgs([]string) (*migrateConfig, error)` — consistent across Task 3 references.
- `validateBootstrapConfig(*Config) (*Config, error)` — introduced in Task 2 tests; implemented in Task 2 step 4.
- Chart helpers: `cyoda.fullname`, `cyoda.labels`, `cyoda.selectorLabels`, `cyoda.serviceAccountName`, `cyoda.hmacSecretName`, `cyoda.bootstrapSecretName`, `cyoda.image` — all defined in Task 5 and referenced consistently in Tasks 6–13.
- Values keys: `postgres.existingSecret` / `existingSecretKey`, `jwt.existingSecret` / `existingSecretKey`, `cluster.hmacSecret.existingSecret` / `existingSecretKey`, `bootstrap.clientSecret.existingSecret` / `existingSecretKey` — consistent across schema (Task 5), chart-managed Secret templates (Task 8), and StatefulSet projection (Task 9).
- Port names (`http`, `grpc`, `metrics`, `gossip-tcp`, `gossip-udp`) — consistent between the container ports (Task 9), the Services (Task 6), the routes (Task 11), and the NetworkPolicy (Task 13).

No inconsistencies found.
