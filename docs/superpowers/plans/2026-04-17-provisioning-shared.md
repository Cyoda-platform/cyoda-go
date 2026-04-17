# Canonical provisioning (shared layer) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the shared-layer changes that unblock canonical provisioning for cyoda-go across desktop, Docker, and Helm — binary behavior changes, repo layout, legacy cleanup, and release/publishing workflows. Per-target artifact content (actual Dockerfile, compose, Helm chart) is out of scope for this plan and follows in three per-target plans.

**Architecture:** Twelve self-contained tasks. Tasks 2–7 change binary behavior (Go, TDD-driven). Task 8 reorganizes the repo and retires dev-era artifacts. Tasks 9–11 add release workflows. Task 12 updates docs and adds a missing env-file example. README badges (spec step 11) are deferred to a post-release follow-up commit because they depend on real release artifacts existing.

**Tech Stack:** Go 1.26+, `net/http`, `github.com/prometheus/client_golang`, `log/slog`, `golang-migrate/migrate/v4`, GitHub Actions, GoReleaser, Sigstore cosign (keyless OIDC), `helm/chart-releaser-action`.

**Reference spec:** `docs/superpowers/specs/2026-04-16-provisioning-shared-design.md`

---

## Prerequisites (not plan tasks)

Before executing Task 9 (release.yml) **for the first time**, plugin module tags must already be published so the root `go.mod` can pin to real versions:

```
git tag plugins/memory/v0.1.0    # if not already present — already is
git tag plugins/postgres/v0.1.0  # if not already present — already is
git tag plugins/sqlite/v0.1.0    # needs to be cut
git push origin plugins/memory/v0.1.0 plugins/postgres/v0.1.0 plugins/sqlite/v0.1.0
```

Cutting `plugins/sqlite/v0.1.0` is a prerequisite that must be done manually before anyone pushes the first `v0.1.0` app tag. Tasks 9 and onward can be implemented without these tags existing; they only matter when the workflow is **triggered**.

---

## Task 1: Add `internal/admin` package with liveness and metrics endpoints

**Files:**
- Create: `internal/admin/admin.go`
- Create: `internal/admin/admin_test.go`

- [ ] **Step 1: Write failing liveness test**

```go
// internal/admin/admin_test.go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler_Livez_Returns200(t *testing.T) {
	h := NewHandler(Options{
		Readiness: func() error { return nil },
	})
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("livez: got %d, want 200", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/admin && go test -run TestHandler_Livez_Returns200 -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Write minimal admin handler**

```go
// internal/admin/admin.go
// Package admin provides the unauthenticated admin HTTP listener for
// /livez, /readyz, and /metrics. Must never bind to a public interface —
// callers are responsible for choosing CYODA_ADMIN_BIND_ADDRESS.
package admin

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Options struct {
	// Readiness returns nil when the instance is ready to serve, or a
	// non-nil error describing why it isn't. Called synchronously on
	// every /readyz probe — keep it cheap.
	Readiness func() error
}

func NewHandler(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if err := opts.Readiness(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/admin && go test -run TestHandler_Livez_Returns200 -v`
Expected: PASS.

- [ ] **Step 5: Add readiness-failure and metrics tests**

```go
// internal/admin/admin_test.go — append
func TestHandler_Readyz_Unready(t *testing.T) {
	h := NewHandler(Options{
		Readiness: func() error { return errNotReady },
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz: got %d, want 503", w.Code)
	}
}

func TestHandler_Readyz_Ready(t *testing.T) {
	h := NewHandler(Options{Readiness: func() error { return nil }})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("readyz: got %d, want 200", w.Code)
	}
}

func TestHandler_Metrics_ReturnsPrometheusFormat(t *testing.T) {
	h := NewHandler(Options{Readiness: func() error { return nil }})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics: got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("metrics: missing Content-Type")
	}
}

var errNotReady = &readyErr{msg: "not ready"}

type readyErr struct{ msg string }

func (e *readyErr) Error() string { return e.msg }
```

- [ ] **Step 6: Run all tests**

Run: `cd internal/admin && go test -v`
Expected: three tests PASS. If the prometheus dependency is missing, add it to the root `go.mod` with `go get github.com/prometheus/client_golang@latest` and retry.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/ go.mod go.sum
git commit -m "feat(admin): add /livez /readyz /metrics handler package"
```

---

## Task 2: Wire admin listener into main.go, gated by CYODA_ADMIN_PORT + CYODA_ADMIN_BIND_ADDRESS

**Files:**
- Modify: `app/config.go` (add Admin section to Config; default bind address and port)
- Modify: `cmd/cyoda-go/main.go` (start admin listener alongside HTTP/gRPC)
- Create: `app/config_admin_test.go` (defaults test)

- [ ] **Step 1: Write failing defaults test**

```go
// app/config_admin_test.go
package app

import "testing"

func TestDefaultConfig_AdminDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Admin.Port != 9091 {
		t.Errorf("Admin.Port = %d, want 9091", cfg.Admin.Port)
	}
	if cfg.Admin.BindAddress != "127.0.0.1" {
		t.Errorf("Admin.BindAddress = %q, want %q", cfg.Admin.BindAddress, "127.0.0.1")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/ -run TestDefaultConfig_AdminDefaults -v`
Expected: FAIL — no `Admin` field on Config.

- [ ] **Step 3: Add AdminConfig struct and wire defaults**

Edit `app/config.go`. Add field to `Config`:

```go
type Config struct {
	// ... existing fields ...
	Admin AdminConfig
}

type AdminConfig struct {
	Port        int
	BindAddress string
}
```

In `DefaultConfig()`, add:

```go
		Admin: AdminConfig{
			Port:        envInt("CYODA_ADMIN_PORT", 9091),
			BindAddress: envString("CYODA_ADMIN_BIND_ADDRESS", "127.0.0.1"),
		},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./app/ -run TestDefaultConfig_AdminDefaults -v`
Expected: PASS.

- [ ] **Step 5: Wire admin listener into main.go**

Edit `cmd/cyoda-go/main.go`. After the existing HTTP-server goroutine (around line 92), add:

```go
// Start admin listener (unauthenticated — bind address controls exposure).
adminAddr := fmt.Sprintf("%s:%d", cfg.Admin.BindAddress, cfg.Admin.Port)
adminServer := &http.Server{
	Addr: adminAddr,
	Handler: admin.NewHandler(admin.Options{
		Readiness: a.ReadinessCheck,
	}),
}
go func() {
	slog.Info("admin server starting", "addr", adminAddr)
	if err := adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("admin server failed", "error", err)
	}
}()
```

Add import: `"github.com/cyoda-platform/cyoda-go/internal/admin"`.

Add shutdown alongside the HTTP shutdown (after `httpServer.Shutdown`):

```go
if err := adminServer.Shutdown(shutdownCtx); err != nil {
	slog.Error("admin server shutdown failed", "error", err)
}
```

- [ ] **Step 6: Add `ReadinessCheck` method on App**

Edit `app/app.go`. Add after the existing `Handler()` method:

```go
// ReadinessCheck returns nil when the instance is ready to serve external
// traffic. Called synchronously by the /readyz admin endpoint on every
// probe — keep it cheap. By the time New() returns, the plugin factory
// has successfully opened connections and applied migrations (per the
// existing startup sequence), so a non-nil storeFactory is a sufficient
// readiness signal until the SPI gains a dedicated Ping method.
func (a *App) ReadinessCheck() error {
	if a.storeFactory == nil {
		return fmt.Errorf("storage not initialized")
	}
	return nil
}
```

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 8: Smoke-test the listener**

Run in one terminal: `CYODA_ADMIN_PORT=9091 go run ./cmd/cyoda-go`
In another terminal:
```bash
curl -s http://127.0.0.1:9091/livez    # expect: ok
curl -s http://127.0.0.1:9091/readyz   # expect: ready
curl -s http://127.0.0.1:9091/metrics | head -5   # expect: prometheus metrics
curl -s http://127.0.0.1:8080/livez    # expect: 404 — admin not on API port
```

Kill the server. If any check fails, debug before committing.

- [ ] **Step 9: Commit**

```bash
git add app/config.go app/config_admin_test.go app/app.go cmd/cyoda-go/main.go
git commit -m "feat(admin): wire admin listener with CYODA_ADMIN_PORT/BIND_ADDRESS"
```

---

## Task 3: Add CYODA_SUPPRESS_BANNER env var

**Files:**
- Modify: `cmd/cyoda-go/main.go` (existing `printBanner` function)
- Create: `cmd/cyoda-go/banner_test.go`

- [ ] **Step 1: Locate existing printBanner**

`cmd/cyoda-go/main.go:113` contains `func printBanner(cfg app.Config)`. Read it to understand current output and ANSI handling.

- [ ] **Step 2: Write failing test**

```go
// cmd/cyoda-go/banner_test.go
package main

import (
	"bytes"
	"testing"

	"github.com/cyoda-platform/cyoda-go/app"
)

func TestPrintBanner_Suppressed(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "true")
	var buf bytes.Buffer
	printBannerTo(&buf, app.DefaultConfig())
	if buf.Len() != 0 {
		t.Fatalf("expected empty output when suppressed, got %q", buf.String())
	}
}

func TestPrintBanner_NotSuppressed(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "")
	var buf bytes.Buffer
	printBannerTo(&buf, app.DefaultConfig())
	if buf.Len() == 0 {
		t.Fatalf("expected banner output, got empty")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./cmd/cyoda-go/ -run TestPrintBanner -v`
Expected: FAIL — `printBannerTo` doesn't exist.

- [ ] **Step 4: Refactor printBanner to printBannerTo(io.Writer, cfg) and add suppress check**

In `cmd/cyoda-go/main.go`:
- Rename `printBanner(cfg app.Config)` → `printBannerTo(w io.Writer, cfg app.Config)`. All existing `fmt.Println(...)`/`fmt.Printf(...)` calls become `fmt.Fprintln(w, ...)` / `fmt.Fprintf(w, ...)`.
- Add `printBanner(cfg app.Config) { printBannerTo(os.Stdout, cfg) }` as a thin wrapper — keeps existing callers unchanged.
- At the top of `printBannerTo`, add:

```go
if os.Getenv("CYODA_SUPPRESS_BANNER") == "true" {
	return
}
```

Add `"io"` to imports if not present.

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/cyoda-go/ -run TestPrintBanner -v`
Expected: both tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/cyoda-go/main.go cmd/cyoda-go/banner_test.go
git commit -m "feat(banner): add CYODA_SUPPRESS_BANNER to silence startup banner"
```

---

## Task 4: Emit mock-auth warning banner when IAM mode is mock

**Files:**
- Modify: `cmd/cyoda-go/main.go`
- Modify: `cmd/cyoda-go/banner_test.go`

- [ ] **Step 1: Write failing test**

Append to `cmd/cyoda-go/banner_test.go`:

```go
func TestMockAuthBanner_EmittedInMockMode(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "")
	var buf bytes.Buffer
	cfg := app.DefaultConfig()
	cfg.IAM.Mode = "mock"
	printMockAuthWarningTo(&buf, cfg)
	if !bytes.Contains(buf.Bytes(), []byte("MOCK AUTH")) {
		t.Fatalf("expected MOCK AUTH warning, got %q", buf.String())
	}
}

func TestMockAuthBanner_NotEmittedInJWTMode(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "")
	var buf bytes.Buffer
	cfg := app.DefaultConfig()
	cfg.IAM.Mode = "jwt"
	printMockAuthWarningTo(&buf, cfg)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning in jwt mode, got %q", buf.String())
	}
}

func TestMockAuthBanner_SuppressedByFlag(t *testing.T) {
	t.Setenv("CYODA_SUPPRESS_BANNER", "true")
	var buf bytes.Buffer
	cfg := app.DefaultConfig()
	cfg.IAM.Mode = "mock"
	printMockAuthWarningTo(&buf, cfg)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when suppressed, got %q", buf.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cyoda-go/ -run TestMockAuthBanner -v`
Expected: FAIL — `printMockAuthWarningTo` undefined.

- [ ] **Step 3: Implement the warning**

Add to `cmd/cyoda-go/main.go`:

```go
// printMockAuthWarningTo prints a prominent multi-line warning when the
// binary is about to serve requests with no real authentication. Silent
// unless IAM mode is "mock". Respects CYODA_SUPPRESS_BANNER.
func printMockAuthWarningTo(w io.Writer, cfg app.Config) {
	if os.Getenv("CYODA_SUPPRESS_BANNER") == "true" {
		return
	}
	if cfg.IAM.Mode != "mock" {
		return
	}
	yellow := "\033[33m"
	reset := "\033[0m"
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		yellow = ""
		reset = ""
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, yellow+"╔══════════════════════════════════════════════════════════════════════╗"+reset)
	fmt.Fprintln(w, yellow+"║  WARNING: MOCK AUTH IS ACTIVE                                        ║"+reset)
	fmt.Fprintln(w, yellow+"║  All requests are accepted without authentication.                   ║"+reset)
	fmt.Fprintln(w, yellow+"║  This instance MUST NOT be exposed to untrusted networks.            ║"+reset)
	fmt.Fprintln(w, yellow+"║  Set CYODA_IAM_MODE=jwt and CYODA_JWT_SIGNING_KEY to enable real     ║"+reset)
	fmt.Fprintln(w, yellow+"║  authentication. Suppress this banner with CYODA_SUPPRESS_BANNER=true.║"+reset)
	fmt.Fprintln(w, yellow+"╚══════════════════════════════════════════════════════════════════════╝"+reset)
	fmt.Fprintln(w)
}
```

- [ ] **Step 4: Wire it into main()**

In `cmd/cyoda-go/main.go` `main()`, after the existing `printBanner(cfg)` call, add:

```go
printMockAuthWarningTo(os.Stdout, cfg)
```

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/cyoda-go/ -run TestMockAuthBanner -v`
Expected: three tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/cyoda-go/main.go cmd/cyoda-go/banner_test.go
git commit -m "feat(banner): emit mock-auth warning when IAM_MODE=mock"
```

---

## Task 5: Add CYODA_REQUIRE_JWT guard

**Files:**
- Modify: `app/config.go` (add IAM.RequireJWT field)
- Modify: `app/app.go` (enforce guard during New or a new Validate step)
- Create: `app/config_require_jwt_test.go`

- [ ] **Step 1: Write failing test**

```go
// app/config_require_jwt_test.go
package app

import (
	"testing"
)

func TestDefaultConfig_RequireJWT_DefaultsFalse(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.IAM.RequireJWT {
		t.Fatalf("RequireJWT should default to false")
	}
}

func TestValidateIAM_RequireJWT_AllowsJWTWithKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IAM.RequireJWT = true
	cfg.IAM.Mode = "jwt"
	cfg.IAM.JWTSigningKey = "-----BEGIN PRIVATE KEY-----\n..."
	if err := ValidateIAM(cfg.IAM); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateIAM_RequireJWT_RejectsMockMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IAM.RequireJWT = true
	cfg.IAM.Mode = "mock"
	if err := ValidateIAM(cfg.IAM); err == nil {
		t.Fatalf("expected error when RequireJWT=true and Mode=mock")
	}
}

func TestValidateIAM_RequireJWT_RejectsMissingKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IAM.RequireJWT = true
	cfg.IAM.Mode = "jwt"
	cfg.IAM.JWTSigningKey = ""
	if err := ValidateIAM(cfg.IAM); err == nil {
		t.Fatalf("expected error when RequireJWT=true and key missing")
	}
}

func TestValidateIAM_RequireJWTFalse_AllowsMock(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IAM.RequireJWT = false
	cfg.IAM.Mode = "mock"
	if err := ValidateIAM(cfg.IAM); err != nil {
		t.Fatalf("expected mock mode allowed when RequireJWT=false, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/ -run RequireJWT -v`
Expected: FAIL — `RequireJWT` field and `ValidateIAM` function do not exist.

- [ ] **Step 3: Add RequireJWT field and ValidateIAM**

Edit `app/config.go`:

Add to `IAMConfig`:
```go
type IAMConfig struct {
	// ... existing fields ...
	RequireJWT bool // CYODA_REQUIRE_JWT — when true, binary refuses to run in mock or without signing key
}
```

In `DefaultConfig()` `IAM:` block:
```go
RequireJWT: envBool("CYODA_REQUIRE_JWT", false),
```

Add new function (in `app/config.go` or a new file `app/config_validate.go`):

```go
// ValidateIAM enforces CYODA_REQUIRE_JWT semantics. When RequireJWT is true,
// the binary refuses to start in any mode other than jwt-with-a-signing-key.
// Callers MUST invoke this before wiring auth in New(). Intended for
// production provisioning (Helm) where silent mock-auth fallback would be
// a serious security hazard.
func ValidateIAM(iam IAMConfig) error {
	if !iam.RequireJWT {
		return nil
	}
	if iam.Mode != "jwt" {
		return fmt.Errorf("CYODA_REQUIRE_JWT=true but CYODA_IAM_MODE=%q (expected \"jwt\")", iam.Mode)
	}
	if iam.JWTSigningKey == "" {
		return fmt.Errorf("CYODA_REQUIRE_JWT=true but CYODA_JWT_SIGNING_KEY is empty")
	}
	return nil
}
```

Add `"fmt"` to imports in the chosen file if not already present.

- [ ] **Step 4: Run tests**

Run: `go test ./app/ -run RequireJWT -v`
Expected: five tests PASS.

- [ ] **Step 5: Invoke ValidateIAM from main.go before New**

In `cmd/cyoda-go/main.go` `main()`, after `cfg := app.DefaultConfig()` (or wherever the config is assembled) and **before** `app.New(cfg)`:

```go
if err := app.ValidateIAM(cfg.IAM); err != nil {
	slog.Error("IAM validation failed", "error", err)
	os.Exit(1)
}
```

- [ ] **Step 6: Verify build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 7: Manual smoke test**

Run: `CYODA_REQUIRE_JWT=true go run ./cmd/cyoda-go`
Expected: binary exits with an error naming `CYODA_IAM_MODE` (mock, the default).

Run: `CYODA_REQUIRE_JWT=true CYODA_IAM_MODE=jwt go run ./cmd/cyoda-go`
Expected: binary exits with an error about missing signing key.

- [ ] **Step 8: Commit**

```bash
git add app/config.go app/config_require_jwt_test.go cmd/cyoda-go/main.go
git commit -m "feat(auth): add CYODA_REQUIRE_JWT hard guard for production deployments"
```

---

## Task 6: Schema-compatibility contract (sqlite)

**Files:**
- Modify: `plugins/sqlite/migrate.go`
- Create: `plugins/sqlite/migrate_compat_test.go`

Context: `golang-migrate`'s `migrate.Migrate.Version()` returns `(uint, dirty bool, err error)`. `migrate.ErrNilVersion` indicates a fresh DB. The embedded migration source is accessible via `source.Driver.First()` and `.Next()`; we can compute the max version at runtime. The contract: DB version > max → error; DB version < max && !AutoMigrate → error; otherwise migrate/proceed.

- [ ] **Step 1: Write failing test for schema-newer-than-code**

```go
// plugins/sqlite/migrate_compat_test.go
package sqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openMemorySQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCheckSchemaCompat_SchemaNewerThanCode(t *testing.T) {
	db := openMemorySQLite(t)
	// Simulate a DB migrated to version 999 (far beyond embedded migrations).
	if err := writeMigrationVersion(db, 999); err != nil {
		t.Fatal(err)
	}
	err := checkSchemaCompat(context.Background(), db, true /* autoMigrate */)
	if err == nil {
		t.Fatalf("expected schema-newer error, got nil")
	}
}

func TestCheckSchemaCompat_SchemaOlder_NoAutoMigrate(t *testing.T) {
	db := openMemorySQLite(t)
	// Fresh DB (version 0/nil). Embedded migrations have at least one step.
	err := checkSchemaCompat(context.Background(), db, false /* autoMigrate */)
	if err == nil {
		t.Fatalf("expected schema-older-no-automigrate error, got nil")
	}
}

func TestCheckSchemaCompat_SchemaMatches(t *testing.T) {
	db := openMemorySQLite(t)
	// Run actual migrations, then check compat.
	if err := runMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	err := checkSchemaCompat(context.Background(), db, false /* autoMigrate */)
	if err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd plugins/sqlite && go test -run CheckSchemaCompat -v`
Expected: FAIL — `checkSchemaCompat` and `writeMigrationVersion` undefined.

- [ ] **Step 3: Implement checkSchemaCompat**

Edit `plugins/sqlite/migrate.go`. Add:

```go
// checkSchemaCompat enforces the schema-compatibility contract from the
// canonical provisioning spec:
//   - schema newer than code → fatal
//   - schema older than code, autoMigrate=false → fatal
//   - schema older than code, autoMigrate=true → caller proceeds with runMigrations
//   - schema matches → proceed
func checkSchemaCompat(ctx context.Context, db *sql.DB, autoMigrate bool) error {
	driver, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{NoTxWrap: true})
	if err != nil {
		return fmt.Errorf("schema compat: create driver: %w", err)
	}
	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("schema compat: open source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("schema compat: create migrator: %w", err)
	}

	maxVersion, err := maxMigrationVersion(source)
	if err != nil {
		return fmt.Errorf("schema compat: scan embedded migrations: %w", err)
	}

	dbVersion, dirty, err := m.Version()
	switch {
	case errors.Is(err, migrate.ErrNilVersion):
		dbVersion = 0 // fresh DB
	case err != nil:
		return fmt.Errorf("schema compat: read DB version: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema compat: database migration state is dirty at version %d — manual intervention required", dbVersion)
	}

	switch {
	case uint(dbVersion) > maxVersion:
		return fmt.Errorf("schema compat: database schema version %d is newer than this binary's max migration version %d — refusing to start to avoid data corruption", dbVersion, maxVersion)
	case uint(dbVersion) < maxVersion && !autoMigrate:
		return fmt.Errorf("schema compat: database schema version %d is older than code (%d) and CYODA_SQLITE_AUTO_MIGRATE=false — set CYODA_SQLITE_AUTO_MIGRATE=true and restart, or run migrations out-of-band", dbVersion, maxVersion)
	}
	return nil
}

// maxMigrationVersion walks the embedded migration source and returns the
// highest version present.
func maxMigrationVersion(src source.Driver) (uint, error) {
	v, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("first migration: %w", err)
	}
	max := v
	for {
		next, err := src.Next(max)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("next migration after %d: %w", max, err)
		}
		max = next
	}
	return max, nil
}

// writeMigrationVersion is a test helper: sets the schema_migrations table
// to the given version (used to simulate newer-than-code scenarios).
func writeMigrationVersion(db *sql.DB, version uint) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (version uint64, dirty bool);
		DELETE FROM schema_migrations;
		INSERT INTO schema_migrations (version, dirty) VALUES (?, 0);
	`, version)
	return err
}
```

Add imports if missing: `"os"`, `"github.com/golang-migrate/migrate/v4/source"`.

- [ ] **Step 4: Run tests**

Run: `cd plugins/sqlite && go test -run CheckSchemaCompat -v`
Expected: three tests PASS.

- [ ] **Step 5: Wire checkSchemaCompat into store_factory**

Edit `plugins/sqlite/store_factory.go`. Before the existing migration call, add a compat check. Find the call to `runMigrations` and wrap it:

```go
if err := checkSchemaCompat(ctx, db, cfg.AutoMigrate); err != nil {
	return nil, err
}
if cfg.AutoMigrate {
	if err := runMigrations(ctx, db); err != nil {
		return nil, fmt.Errorf("sqlite migrations: %w", err)
	}
}
```

The `checkSchemaCompat` call is unconditional — it validates state regardless of auto-migrate. `runMigrations` only runs when `AutoMigrate=true`.

- [ ] **Step 6: Run plugin tests**

Run: `cd plugins/sqlite && go test ./...`
Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add plugins/sqlite/migrate.go plugins/sqlite/migrate_compat_test.go plugins/sqlite/store_factory.go
git commit -m "feat(sqlite): enforce schema-compatibility contract on startup"
```

---

## Task 7: Schema-compatibility contract (postgres)

**Files:**
- Modify: `plugins/postgres/migrate.go`
- Modify: `plugins/postgres/store_factory.go`
- Create: `plugins/postgres/migrate_compat_test.go`

Identical semantics to Task 6 but against postgres. Testcontainers already wires a real postgres for plugin tests.

- [ ] **Step 1: Read plugins/postgres/migrate.go**

Understand how the postgres migration driver is constructed (`pgx` vs `pq` — golang-migrate's postgres driver works with `*sql.DB`).

- [ ] **Step 2: Write failing test**

```go
// plugins/postgres/migrate_compat_test.go
package postgres

import (
	"context"
	"database/sql"
	"testing"
)

// Reuse the existing postgres testcontainer helper from
// plugins/postgres — find it with `grep -rn "testcontainers" plugins/postgres`
// and bind its returned *sql.DB to the local variable name. If no helper
// exists yet, crib the setup from `plugins/postgres/*_test.go` — the
// existing parity tests already spin up a container.

func TestCheckSchemaCompat_SchemaNewerThanCode_Postgres(t *testing.T) {
	db := startTestPostgres(t) // helper identified in the comment above
	if err := writePostgresMigrationVersion(db, 999); err != nil {
		t.Fatal(err)
	}
	if err := checkSchemaCompat(context.Background(), db, true); err == nil {
		t.Fatalf("expected schema-newer error")
	}
}

func TestCheckSchemaCompat_SchemaOlder_NoAutoMigrate_Postgres(t *testing.T) {
	db := startTestPostgres(t)
	if err := checkSchemaCompat(context.Background(), db, false); err == nil {
		t.Fatalf("expected schema-older-no-automigrate error")
	}
}

func TestCheckSchemaCompat_SchemaMatches_Postgres(t *testing.T) {
	db := startTestPostgres(t)
	if err := runMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := checkSchemaCompat(context.Background(), db, false); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

// writePostgresMigrationVersion sets schema_migrations.version directly.
func writePostgresMigrationVersion(db *sql.DB, version uint) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL, dirty boolean NOT NULL);
		DELETE FROM schema_migrations;
		INSERT INTO schema_migrations (version, dirty) VALUES ($1, false);
	`, version)
	return err
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd plugins/postgres && go test -run CheckSchemaCompat -v`
Expected: FAIL — `checkSchemaCompat` undefined. (If `startTestPostgres` is also missing, mirror the pattern from existing postgres tests.)

- [ ] **Step 4: Implement checkSchemaCompat for postgres**

Edit `plugins/postgres/migrate.go`. Add a `checkSchemaCompat` function mirroring Task 6's — same logic, but using postgres's migrate driver instead of sqlite's:

```go
func checkSchemaCompat(ctx context.Context, db *sql.DB, autoMigrate bool) error {
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("schema compat: create driver: %w", err)
	}
	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("schema compat: open source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("schema compat: create migrator: %w", err)
	}

	maxVersion, err := maxMigrationVersion(source)
	if err != nil {
		return fmt.Errorf("schema compat: scan embedded migrations: %w", err)
	}

	dbVersion, dirty, err := m.Version()
	switch {
	case errors.Is(err, migrate.ErrNilVersion):
		dbVersion = 0
	case err != nil:
		return fmt.Errorf("schema compat: read DB version: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema compat: database migration state is dirty at version %d — manual intervention required", dbVersion)
	}

	switch {
	case uint(dbVersion) > maxVersion:
		return fmt.Errorf("schema compat: database schema version %d is newer than this binary's max migration version %d — refusing to start to avoid data corruption", dbVersion, maxVersion)
	case uint(dbVersion) < maxVersion && !autoMigrate:
		return fmt.Errorf("schema compat: database schema version %d is older than code (%d) and CYODA_POSTGRES_AUTO_MIGRATE=false — set CYODA_POSTGRES_AUTO_MIGRATE=true and restart, or run migrations out-of-band", dbVersion, maxVersion)
	}
	return nil
}

// maxMigrationVersion: identical body to the sqlite plugin; duplicated
// rather than extracted to avoid a shared SPI-crossing dependency for
// a five-line helper. If a third plugin needs it, extract then.
func maxMigrationVersion(src source.Driver) (uint, error) {
	v, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("first migration: %w", err)
	}
	max := v
	for {
		next, err := src.Next(max)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("next migration after %d: %w", max, err)
		}
		max = next
	}
	return max, nil
}
```

Replace `pgxmigrate` with whatever driver alias the file already uses (likely `postgresmigrate "github.com/golang-migrate/migrate/v4/database/postgres"` — keep the existing alias).

Add imports if missing: `"os"`, `"github.com/golang-migrate/migrate/v4/source"`.

- [ ] **Step 5: Run tests**

Run: `cd plugins/postgres && go test -run CheckSchemaCompat -v`
Expected: three tests PASS (Docker required).

- [ ] **Step 6: Wire into store_factory**

Edit `plugins/postgres/store_factory.go`. Same wrap pattern as sqlite:

```go
if err := checkSchemaCompat(ctx, db, cfg.AutoMigrate); err != nil {
	return nil, err
}
if cfg.AutoMigrate {
	if err := runMigrations(ctx, db); err != nil {
		return nil, fmt.Errorf("postgres migrations: %w", err)
	}
}
```

- [ ] **Step 7: Run full plugin tests**

Run: `cd plugins/postgres && go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add plugins/postgres/migrate.go plugins/postgres/migrate_compat_test.go plugins/postgres/store_factory.go
git commit -m "feat(postgres): enforce schema-compatibility contract on startup"
```

---

## Task 8: Repo layout + legacy cleanup

**Files:**
- Create: `deploy/docker/README.md`
- Create: `deploy/helm/README.md`
- Create: `examples/compose-with-observability/compose.yaml`
- Create: `examples/compose-with-observability/README.md`
- Create: `scripts/dev/README.md`
- Create: `scripts/multi-node-docker/README.md`
- Move: `cyoda-go.sh` → `scripts/dev/run-local.sh`
- Move: `cyoda-go-docker.sh` → `scripts/dev/run-docker-dev.sh` (sanitize)
- Delete: `Dockerfile` (root)
- Delete: `docker-compose.yml` (root)
- Delete: `.github/workflows/docker-publish.yml`

This task has no TDD — it's filesystem operations.

- [ ] **Step 1: Create placeholder READMEs for new directories**

```bash
mkdir -p deploy/docker deploy/helm examples/compose-with-observability scripts/dev
```

Write `deploy/docker/README.md`:

```markdown
# Canonical Docker provisioning

Dockerfile and reference `compose.yaml` for running cyoda-go in containers.
See `docs/superpowers/specs/2026-04-16-provisioning-shared-design.md` for
the shared design. Contents are filled in by the Docker per-target spec.
```

Write `deploy/helm/README.md`:

```markdown
# Canonical Helm chart

Chart for running cyoda-go in Kubernetes. See
`docs/superpowers/specs/2026-04-16-provisioning-shared-design.md` for the
shared design. Contents (chart skeleton, values.yaml, templates) are
filled in by the Helm per-target spec.
```

Write `scripts/dev/README.md`:

```markdown
# Developer helper scripts

Local-development helpers. Not part of canonical provisioning. The
canonical artifacts live under `deploy/`.

- `run-local.sh` — run cyoda-go via `go run` using the `local` profile.
- `run-docker-dev.sh` — run cyoda-go + Postgres via docker compose for
  local development. Generates a fresh JWT signing key and a randomized
  bootstrap client secret per run.
```

Write `scripts/multi-node-docker/README.md`:

```markdown
# Multi-node docker dev cluster

Developer/test tool for spinning up multiple cyoda-go containers sharing
a Postgres backend and gossip-configured for cluster mode. Not part of
canonical provisioning.

Usage:

    ./start-cluster.sh --nodes 3
    ./stop-cluster.sh
```

- [ ] **Step 2: Move cyoda-go.sh → scripts/dev/run-local.sh**

```bash
git mv cyoda-go.sh scripts/dev/run-local.sh
```

No content changes needed — the script is already correct.

- [ ] **Step 3: Move cyoda-go-docker.sh → scripts/dev/run-docker-dev.sh and sanitize**

```bash
git mv cyoda-go-docker.sh scripts/dev/run-docker-dev.sh
```

Edit `scripts/dev/run-docker-dev.sh`. Find the hardcoded line:

```
CYODA_BOOTSTRAP_CLIENT_SECRET=78f647e3309c4c5e0d4dddcbabc9c613ba9bfd9bc5e442d912d630cdc66fe087
```

Replace with:

```bash
CYODA_BOOTSTRAP_CLIENT_SECRET=$(openssl rand -hex 32)
```

Also update the header comment to note this is a dev helper, not provisioning.

- [ ] **Step 4: Relocate Grafana-bundled compose to examples/compose-with-observability/**

Read the current root `docker-compose.yml`. The `otel-backend` service (Grafana+Prom+Tempo) and its volume mounts are the observability bits; the `cyoda-go` + `postgres` services are the dev-app bits.

Create `examples/compose-with-observability/compose.yaml` containing only the observability pieces **plus** a cyoda-go service wired to push OTLP to the local collector. Reuse the structure from the current root compose:

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: minicyoda
      POSTGRES_USER: minicyoda
      POSTGRES_PASSWORD: minicyoda
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U minicyoda -d minicyoda"]
      interval: 2s
      timeout: 5s
      retries: 10

  otel-backend:
    image: grafana/otel-lgtm:latest
    ports:
      - "127.0.0.1:3000:3000"
      - "127.0.0.1:4317:4317"
      - "127.0.0.1:4318:4318"
    volumes:
      - ../../scripts/grafana/dashboards:/otel-lgtm/grafana/dashboards/cyoda-go:ro
      - ../../scripts/grafana/provisioning/dashboards/default.yml:/otel-lgtm/grafana/conf/provisioning/dashboards/cyoda-go.yml:ro

  cyoda-go:
    image: ghcr.io/cyoda-platform/cyoda-go:latest
    ports:
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:9090:9090"
      - "127.0.0.1:9091:9091"
    environment:
      CYODA_STORAGE_BACKEND: postgres
      CYODA_POSTGRES_URL: postgres://minicyoda:minicyoda@postgres:5432/minicyoda?sslmode=disable
      CYODA_ADMIN_BIND_ADDRESS: 0.0.0.0
      CYODA_OTEL_ENABLED: "true"
      OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-backend:4318
    depends_on:
      postgres:
        condition: service_healthy
      otel-backend:
        condition: service_started

volumes:
  pgdata:
```

Write `examples/compose-with-observability/README.md`:

```markdown
# compose-with-observability (dev convenience)

Runs cyoda-go alongside a bundled Grafana+Prometheus+Tempo stack
(`grafana/otel-lgtm`) for local observability development.

**Not for production.** Grafana here is unauthenticated; Postgres is
seeded with dev credentials; the image tag tracks `:latest`. The canonical
Docker provisioning artifact lives at `deploy/docker/compose.yaml` and
does not bundle observability — operators point cyoda-go at their own
telemetry backend via OTLP or scrape `/metrics` directly.

Usage:

    docker compose up

Grafana is exposed on http://127.0.0.1:3000.
```

- [ ] **Step 5: Delete root Dockerfile, root docker-compose.yml, old publish workflow**

```bash
git rm Dockerfile docker-compose.yml .github/workflows/docker-publish.yml
```

- [ ] **Step 6: Verify nothing references deleted paths**

Run:
```bash
grep -rn "cyoda-go.sh\|cyoda-go-docker.sh" --include='*.md' --include='*.yaml' --include='*.yml' --include='*.sh' --include='*.go' --exclude-dir=docs . | grep -v scripts/dev/
```
Expected: empty (any remaining references must be updated).

Fix any references found (README.md is a likely candidate — update in Task 12).

- [ ] **Step 7: Run tests to confirm no test depended on moved paths**

Run: `go test -short ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add -A deploy/ examples/ scripts/dev/ scripts/multi-node-docker/README.md
git add -A Dockerfile docker-compose.yml .github/workflows/docker-publish.yml cyoda-go.sh cyoda-go-docker.sh
git commit -m "chore(layout): introduce deploy/, examples/, scripts/dev/; retire dev-era root artifacts"
```

---

## Task 9: Add release.yml (GoReleaser + multi-arch image + cosign + SBOM)

**Files:**
- Create: `.goreleaser.yaml`
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write `.goreleaser.yaml`**

```yaml
# .goreleaser.yaml
version: 2

before:
  hooks:
    - env GOWORK=off go mod download
    - env GOWORK=off go mod verify

builds:
  - id: cyoda-go
    main: ./cmd/cyoda-go
    env:
      - CGO_ENABLED=0
      - GOWORK=off
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.Commit}}
      - -X main.buildDate={{.Date}}

archives:
  - id: cyoda-go
    ids: [cyoda-go]
    format_overrides:
      - goos: windows
        formats: [zip]
    files:
      - LICENSE
      - README.md

checksum:
  name_template: 'SHA256SUMS'
  algorithm: sha256

dockers:
  - image_templates:
      - "ghcr.io/cyoda-platform/cyoda-go:{{ .Version }}-amd64"
    use: buildx
    goarch: amd64
    dockerfile: deploy/docker/Dockerfile
    build_flag_templates:
      - "--platform=linux/amd64"
      - "--label=org.opencontainers.image.source=https://github.com/cyoda-platform/cyoda-go"
      - "--label=org.opencontainers.image.version={{ .Version }}"
      - "--label=org.opencontainers.image.revision={{ .Commit }}"
  - image_templates:
      - "ghcr.io/cyoda-platform/cyoda-go:{{ .Version }}-arm64"
    use: buildx
    goarch: arm64
    dockerfile: deploy/docker/Dockerfile
    build_flag_templates:
      - "--platform=linux/arm64"
      - "--label=org.opencontainers.image.source=https://github.com/cyoda-platform/cyoda-go"
      - "--label=org.opencontainers.image.version={{ .Version }}"
      - "--label=org.opencontainers.image.revision={{ .Commit }}"

docker_manifests:
  - name_template: "ghcr.io/cyoda-platform/cyoda-go:{{ .Version }}"
    image_templates:
      - "ghcr.io/cyoda-platform/cyoda-go:{{ .Version }}-amd64"
      - "ghcr.io/cyoda-platform/cyoda-go:{{ .Version }}-arm64"
  - name_template: "ghcr.io/cyoda-platform/cyoda-go:latest"
    skip_push: '{{ .Prerelease }}'
    image_templates:
      - "ghcr.io/cyoda-platform/cyoda-go:{{ .Version }}-amd64"
      - "ghcr.io/cyoda-platform/cyoda-go:{{ .Version }}-arm64"

docker_signs:
  - artifacts: manifests
    cmd: cosign
    args:
      - "sign"
      - "--yes"
      - "${artifact}"

sboms:
  - artifacts: archive

release:
  prerelease: auto  # auto-marks tags like v0.2.0-rc.1 as prerelease
  draft: false
```

Note: This references `deploy/docker/Dockerfile`, which is created by the Docker per-target plan. The release workflow will fail until that Dockerfile exists. That's acceptable — the first triggered release must happen after the Docker plan lands.

- [ ] **Step 2: Write `.github/workflows/release.yml`**

```yaml
# .github/workflows/release.yml
name: Release

on:
  push:
    tags: ['v*']
  workflow_dispatch:

permissions:
  contents: write    # GH Release creation
  packages: write    # GHCR push
  id-token: write    # keyless cosign via OIDC

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # GoReleaser needs full history for changelog

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      # Pre-flight: verify that no plugin dependency resolves to a
      # pseudo-version, replace directive, or untagged SHA. Prevents
      # an accidental root tag from producing a release built against
      # unreleased plugin code.
      - name: Pre-flight — verify module dependencies
        run: |
          set -euo pipefail
          GOWORK=off go mod download
          GOWORK=off go mod verify
          echo "Plugin dependency versions pinned by go.mod:"
          GOWORK=off go list -m github.com/cyoda-platform/cyoda-go/plugins/... || true
          # Fail if any dependency looks like a pseudo-version (v0.0.0-YYYYMMDDhhmmss-hash)
          if GOWORK=off go list -m all | grep -E "v0\.0\.0-[0-9]{14}-[0-9a-f]{12}"; then
            echo "ERROR: pseudo-versions found in go.mod — all plugin deps must resolve to published tags"
            exit 1
          fi

      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: sigstore/cosign-installer@v3

      - uses: anchore/sbom-action/download-syft@v0

      - uses: goreleaser/goreleaser-action@v6
        with:
          version: latest
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          GOWORK: "off"
```

- [ ] **Step 3: Verify the workflow YAML parses**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"
```
Expected: no output (success).

- [ ] **Step 4: Verify the GoReleaser config parses**

If GoReleaser is installed:
```bash
goreleaser check --config .goreleaser.yaml
```
Expected: "config is valid" or similar. If not installed, `docker run --rm -v "$(pwd)":/src -w /src goreleaser/goreleaser check` works equivalently.

Note: `dockers:` entries reference `deploy/docker/Dockerfile` which may not exist yet. `goreleaser check` accepts this; the error only surfaces at build time.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml .github/workflows/release.yml
git commit -m "ci(release): add GoReleaser-driven release workflow with cosign + SBOM"
```

---

## Task 10: Add release-chart.yml

**Files:**
- Create: `.github/workflows/release-chart.yml`

- [ ] **Step 1: Write the workflow**

```yaml
# .github/workflows/release-chart.yml
name: Release Helm chart

on:
  push:
    tags: ['cyoda-go-*']
  workflow_dispatch:

permissions:
  contents: write
  packages: write

jobs:
  release-chart:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Guard — chart directory must exist
        run: |
          if [ ! -f deploy/helm/cyoda-go/Chart.yaml ]; then
            echo "ERROR: deploy/helm/cyoda-go/Chart.yaml not found — the Helm chart skeleton must be created by the Helm per-target plan before this workflow can succeed."
            exit 1
          fi

      - name: Configure git
        run: |
          git config user.name "$GITHUB_ACTOR"
          git config user.email "$GITHUB_ACTOR@users.noreply.github.com"

      - uses: azure/setup-helm@v4

      - uses: helm/chart-releaser-action@v1
        with:
          charts_dir: deploy/helm
          config: .github/cr.yaml
        env:
          CR_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

Add minimal `.github/cr.yaml`:

```yaml
# .github/cr.yaml
# Configuration for helm/chart-releaser-action
generate-release-notes: true
```

- [ ] **Step 2: Verify YAML parses**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release-chart.yml'))"
python3 -c "import yaml; yaml.safe_load(open('.github/cr.yaml'))"
```
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release-chart.yml .github/cr.yaml
git commit -m "ci(chart): add helm/chart-releaser-action workflow"
```

---

## Task 11: Add bump-chart-appversion.yml

**Files:**
- Create: `.github/workflows/bump-chart-appversion.yml`

- [ ] **Step 1: Write the workflow**

```yaml
# .github/workflows/bump-chart-appversion.yml
name: Bump chart appVersion

on:
  push:
    tags: ['v*']

permissions:
  contents: write
  pull-requests: write

jobs:
  bump:
    runs-on: ubuntu-latest
    # Only for non-prerelease tags. Prereleases (v*-rc.*, v*-beta.*, v*-alpha.*)
    # don't bump the chart's appVersion.
    if: ${{ !contains(github.ref_name, '-') }}
    steps:
      - uses: actions/checkout@v4
        with:
          ref: main

      - name: Guard — chart directory must exist
        id: guard
        run: |
          if [ ! -f deploy/helm/cyoda-go/Chart.yaml ]; then
            echo "skip=true" >> "$GITHUB_OUTPUT"
            echo "Skipping appVersion bump — chart not yet present."
          else
            echo "skip=false" >> "$GITHUB_OUTPUT"
          fi

      - name: Compute new appVersion
        if: steps.guard.outputs.skip == 'false'
        id: ver
        run: |
          APP_VERSION="${GITHUB_REF_NAME#v}"
          echo "app_version=$APP_VERSION" >> "$GITHUB_OUTPUT"

      - name: Update Chart.yaml
        if: steps.guard.outputs.skip == 'false'
        run: |
          set -euo pipefail
          sed -i -E "s/^appVersion:.*/appVersion: \"${{ steps.ver.outputs.app_version }}\"/" deploy/helm/cyoda-go/Chart.yaml

      - name: Open PR
        if: steps.guard.outputs.skip == 'false'
        uses: peter-evans/create-pull-request@v7
        with:
          branch: chore/bump-appversion-${{ steps.ver.outputs.app_version }}
          base: main
          commit-message: |
            chore(chart): bump appVersion to ${{ steps.ver.outputs.app_version }}
          title: "chore(chart): bump appVersion to ${{ steps.ver.outputs.app_version }}"
          body: |
            Automated bump triggered by the release of `${{ github.ref_name }}`.

            Review, merge, and then cut a `cyoda-go-<chart-version>` tag to
            publish a chart release pinned to this appVersion.
```

- [ ] **Step 2: Verify YAML parses**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/bump-chart-appversion.yml'))"
```
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/bump-chart-appversion.yml
git commit -m "ci(chart): add workflow that PRs Chart.yaml appVersion bumps on app releases"
```

---

## Task 12: Documentation updates

**Files:**
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`
- Modify: `cmd/cyoda-go/main.go` (`printHelp()`)
- Create: `.env.sqlite.example`

- [ ] **Step 1: Add `.env.sqlite.example`**

```
# Profile: sqlite — embedded SQLite storage backend.
# Copy to .env.sqlite and customize.
# Usage: CYODA_PROFILES=sqlite go run ./cmd/cyoda-go

CYODA_STORAGE_BACKEND=sqlite
CYODA_SQLITE_PATH=./cyoda.db
CYODA_SQLITE_AUTO_MIGRATE=true
# CYODA_SQLITE_BUSY_TIMEOUT=5s
# CYODA_SQLITE_CACHE_SIZE=64000
# CYODA_SQLITE_SEARCH_SCAN_LIMIT=100000
```

- [ ] **Step 2: Extend README.md storage-backends section**

Find the README's storage-modes section (currently only memory / postgres — sqlite is present in the bullet list near the top but not in the Configuration section). Add:

1. A row for **SQLite** in any storage-backends comparison table (mirror the existing memory / postgres rows).
2. A new `### SQLite` subsection under Configuration documenting:
   - `CYODA_SQLITE_PATH` (default `$XDG_DATA_HOME/cyoda-go/cyoda.db`)
   - `CYODA_SQLITE_AUTO_MIGRATE` (default `true`)
   - `CYODA_SQLITE_BUSY_TIMEOUT` (default `5s`)
   - `CYODA_SQLITE_CACHE_SIZE` (default `64000`, in KiB)
   - `CYODA_SQLITE_SEARCH_SCAN_LIMIT` (default `100000`)

3. A new `### Admin & observability` subsection documenting:
   - Admin listener address — `CYODA_ADMIN_BIND_ADDRESS` (default `127.0.0.1`), `CYODA_ADMIN_PORT` (default `9091`)
   - `/livez` — liveness
   - `/readyz` — readiness
   - `/metrics` — Prometheus pull
   - Warning that the listener is unauthenticated and must only be bound to trusted interfaces.

4. A new `### Security` subsection (or extend an existing one) documenting:
   - `CYODA_REQUIRE_JWT=true` — mandatory JWT; refuses mock-auth fallback. Recommended for any production deployment.
   - `CYODA_SUPPRESS_BANNER=true` — silences startup banners, including the mock-auth warning. For CI/test harnesses only.

5. Update the "Quick Start" section. `./cyoda-go.sh` is now `./scripts/dev/run-local.sh`. `./cyoda-go-docker.sh` is now `./scripts/dev/run-docker-dev.sh`.

- [ ] **Step 3: Extend CONTRIBUTING.md**

- Update any references to the relocated scripts.
- Add a short note that canonical provisioning artifacts live under `deploy/`, with `examples/` for dev-convenience variants. Point at the shared spec.

- [ ] **Step 4: Extend `printHelp()` in `cmd/cyoda-go/main.go`**

Find the `printHelp()` function (contains a large heredoc listing env vars). Add entries for:

- `CYODA_ADMIN_PORT` — default `9091`. Admin listener port (health + metrics).
- `CYODA_ADMIN_BIND_ADDRESS` — default `127.0.0.1`. Admin listener bind address.
- `CYODA_SUPPRESS_BANNER` — default unset. Set to `true` to silence the startup banner and mock-auth warning.
- `CYODA_REQUIRE_JWT` — default `false`. Set to `true` to refuse startup unless JWT is fully configured (no mock fallback).

- [ ] **Step 5: Verify build and tests**

Run:
```bash
go build ./...
go test -short ./...
```
Expected: PASS.

- [ ] **Step 6: Manual sanity check**

Run: `go run ./cmd/cyoda-go -h | grep -E 'CYODA_ADMIN|REQUIRE_JWT|SUPPRESS_BANNER'`
Expected: all four env vars appear in the help output.

- [ ] **Step 7: Commit**

```bash
git add README.md CONTRIBUTING.md cmd/cyoda-go/main.go .env.sqlite.example
git commit -m "docs: document admin listener, CYODA_REQUIRE_JWT, CYODA_SUPPRESS_BANNER, sqlite config"
```

---

## End-of-deliverable verification

After Task 12:

- [ ] Run full race-detector sweep: `go test -race ./...` — one time, before PR creation. If any race fires, debug and fix; this is the sole point in the plan at which `-race` is mandatory.
- [ ] Run E2E: `go test ./internal/e2e/... -v` (requires Docker).
- [ ] Confirm `git status` is clean.
- [ ] Confirm no dead references: `grep -rn "cyoda-go.sh\|cyoda-go-docker.sh" . --exclude-dir=.git` returns only results under `scripts/dev/` or historical `docs/plans/`.

---

## Post-merge, post-first-release follow-up (NOT part of this plan)

After `v0.1.0` has been tagged and `release.yml` has produced real artifacts (image on GHCR, binaries on GH Releases), add README badges in a separate commit:

- CI build status
- Go Report Card
- Go Reference (pkg.go.dev)
- Latest GitHub release
- License (Apache-2.0)

This is deferred so no badge points at a non-existent artifact on first merge.
