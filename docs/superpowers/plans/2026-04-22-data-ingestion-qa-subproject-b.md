# Sub-project B Implementation Plan (cyoda-go + cyoda-go-spi)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend A.2's in-memory schema-transformation invariants across the storage boundary for memory, postgres, and sqlite plugins, establishing cross-plugin byte-identical fold, savepoint atomicity, and concurrent-extension convergence (B-I1 through B-I8 per the spec).

**Architecture:** The work spans three repos. **cyoda-go-spi** gains `ErrRetryExhausted` and an `ExtendSchema` contract-clarification godoc update. **cyoda-go** refactors postgres's hardcoded savepoint interval to config-driven, adds save-on-lock to postgres, converts sqlite from apply-in-place to log-based (mirroring postgres's algorithm adapted for `BEGIN IMMEDIATE`), adds per-plugin gray-box tests, extends the `e2e/parity/` registry with B's black-box scenarios, and adds a property-based parity entry with a deterministic in-memory oracle. **cyoda-go-cassandra** has its own sister plan (2026-04-22 in that repo); this plan covers only cyoda-go and cyoda-go-spi.

**Tech Stack:** Go 1.26, `log/slog`, PostgreSQL 16 (testcontainers), SQLite (in-proc via `modernc.org/sqlite`), `e2e/parity` HTTP harness, `gentree` generator from A.2, `math/rand/v2` PCG seeding.

**Companion spec:** `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-b-design.md` (rev 3).

**Sister plan for Cassandra:** `cyoda-go-cassandra/docs/superpowers/plans/2026-04-22-data-ingestion-qa-subproject-b-cassandra.md` (written separately after this plan).

---

## File Structure

**Files modified in cyoda-go-spi** (`/Users/paul/go-projects/cyoda-light/cyoda-go-spi/`):
- `errors.go` — add `ErrRetryExhausted` + godoc.
- `errors_test.go` — test ErrRetryExhausted is distinct from ErrConflict.
- `persistence.go` — update `ExtendSchema` method godoc with retry contract + ctx-cancellation semantics.

**Files modified or created in cyoda-go worktree** (`/Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence/`):

- `plugins/postgres/config.go` — add `SchemaSavepointInterval` field; `parseConfig` reads `CYODA_SCHEMA_SAVEPOINT_INTERVAL` default 64.
- `plugins/postgres/store_factory.go` — thread savepoint interval into the store.
- `plugins/postgres/model_store.go` — refactor `ExtendSchema` savepoint trigger from `newSeq%64==0` to "since last savepoint" using `cfg.SchemaSavepointInterval`. Add `Lock` save-on-lock path.
- `plugins/postgres/model_extensions.go` — hand `lastSavepointSeq` back from `foldLocked` so `ExtendSchema` can reuse it for the trigger check.
- `plugins/postgres/model_extensions_test.go` — new tests for B-I3/B-I4/B-I6/B-I7/ctx-cancel/unlock-asymmetry.
- `plugins/postgres/fcw_test.go` — no change expected (existing FCW tests stay green).

- `plugins/sqlite/config.go` — add `SchemaSavepointInterval` + `SchemaExtendMaxRetries` fields; `parseConfig` reads env.
- `plugins/sqlite/store_factory.go` — thread both knobs.
- `plugins/sqlite/model_store.go` — replace apply-in-place with log-based `ExtendSchema`; add `foldLocked`; add `Lock` save-on-lock path; wrap `ExtendSchema` in `SQLITE_BUSY` retry loop.
- `plugins/sqlite/model_extensions_test.go` — rewrite existing tests; add B-I1..B-I8 sqlite-local tests.
- `plugins/sqlite/model_extensions.go` — **create** (new file mirroring postgres's `model_extensions.go`).

- `plugins/memory/model_extensions_test.go` — add B-I6 + B-I7 tests. No production-code change.

- `e2e/parity/registry.go` — add five named parity entries + one property-based entry.
- `e2e/parity/schema_extension_byte_identity.go` — **create**.
- `e2e/parity/schema_extension_atomic_rejection.go` — **create**.
- `e2e/parity/schema_extension_concurrent_convergence.go` — **create**.
- `e2e/parity/schema_extension_save_on_lock.go` — **create**.
- `e2e/parity/schema_extension_cache_invalidation.go` — **create**.
- `e2e/parity/schema_extension_property.go` — **create** (property-based harness + oracle).
- `e2e/parity/schema_extension_property_budget_test.go` — **create** (meta-test for 120 s ceiling).

- `internal/cluster/modelcache/cache_test.go` — add explicit B-I8 test for `ExtendSchema` path (confirms existing invalidation is correct under B's new savepoint/retry paths).

- `cmd/cyoda/main.go` — `printHelp()` documents the two new env vars.
- `README.md` — config table entries for the two new env vars with "Honored by" column.
- `docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md` — §6 invariant table: replace `TBD | B | ...` rows with B-I1..B-I8.

**Test-helper conventions:**
- Postgres: `newTestFactory(t)` exists in `plugins/postgres/conformance_test.go:124`. When a fixture pattern like `newPGFixture(t)` is referenced in a task, it's shorthand for constructing a `StoreFactory` via `newTestFactory`, setting `SetApplyFunc`, saving a test tenant context, and calling `ModelStore()`. If a nearby test already uses this exact shape, reuse its helper verbatim; otherwise add a `newPGFixture(t *testing.T)` at the top of `model_extensions_test.go`.
- Memory: pattern similar — use the existing `StoreFactory`/`ModelStore()` construction in surrounding tests. If a `newTestFactory` helper doesn't exist, add one following postgres's shape.
- Sqlite: pattern similar. When the plan references `newSQLiteFixtureWithInterval(t, N)`, it's shorthand for constructing a factory with an explicit `SchemaSavepointInterval` override. Add as a local helper in `model_extensions_test.go`.
- Tenant context: use the existing `withTenant(ctx, "t1")` helper from each plugin's test tree; if absent, add a three-line helper that sets the tenant claim in `spi.Context`.

These helpers are convenience shims, not load-bearing. Each task that invokes them expects the subagent to locate the existing one in the surrounding file or create a minimal local copy.

**Files NOT touched by this plan:**
- `plugins/memory/model_store.go` — no production change; B-I7 convergence test asserts existing mutex-serialized behavior.
- `plugins/postgres/migrations/*` — no DDL change (the `model_schema_extensions` table already supports both kinds).
- `plugins/sqlite/migrations/*` — no DDL change (the table already exists in `000001_initial_schema.up.sql:141-154` but was unused).
- `internal/cluster/modelcache/cache.go` — B-I8 already implemented at line 178-184 (`ExtendSchema` path invalidates after successful inner call).

---

## Task 1: [cyoda-go-spi] Branch + ErrRetryExhausted

**Files:**
- Working dir: `/Users/paul/go-projects/cyoda-light/cyoda-go-spi/`
- Modify: `errors.go`, `errors_test.go`

- [ ] **Step 1.1: Create feature branch in cyoda-go-spi**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi
git checkout -b feat/subproject-b-persistence
git status
```

Expected: branch `feat/subproject-b-persistence` created, clean working tree.

- [ ] **Step 1.2: Write the failing test for ErrRetryExhausted**

Append to `/Users/paul/go-projects/cyoda-light/cyoda-go-spi/errors_test.go`:

```go
func TestErrRetryExhausted_DistinctFromErrConflict(t *testing.T) {
	if errors.Is(spi.ErrRetryExhausted, spi.ErrConflict) {
		t.Error("ErrRetryExhausted must not unwrap to ErrConflict — they are distinct failure modes")
	}
	if errors.Is(spi.ErrConflict, spi.ErrRetryExhausted) {
		t.Error("ErrConflict must not unwrap to ErrRetryExhausted")
	}
	if spi.ErrRetryExhausted.Error() == "" {
		t.Error("ErrRetryExhausted must have a non-empty message")
	}
}
```

- [ ] **Step 1.3: Run the test to verify it fails**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi
go test -run TestErrRetryExhausted_DistinctFromErrConflict -v
```

Expected: FAIL — `undefined: spi.ErrRetryExhausted` (since the error is not yet declared).

- [ ] **Step 1.4: Add ErrRetryExhausted to errors.go**

Append to `errors.go`:

```go
// ErrRetryExhausted indicates the plugin's retry budget for a
// transparently-retried operation was consumed without success.
// Returned by ExtendSchema when CYODA_SCHEMA_EXTEND_MAX_RETRIES
// attempts have completed without success AND the context was not
// cancelled. Callers may choose to retry at a higher level (with
// backoff) or surface the condition to the end user.
//
// Distinct from ErrConflict: ErrConflict means a single attempt hit
// a conflict; ErrRetryExhausted means the plugin exhausted its
// configured retry budget.
var ErrRetryExhausted = errors.New("retry budget exhausted")
```

- [ ] **Step 1.5: Run the test to verify it passes**

```bash
go test -run TestErrRetryExhausted_DistinctFromErrConflict -v
go test ./...
```

Expected: test PASSes, full suite green.

- [ ] **Step 1.6: Commit**

```bash
git add errors.go errors_test.go
git commit -m "feat(spi): add ErrRetryExhausted for B retry contract

ExtendSchema implementations with a native conflict surface (sqlite
SQLITE_BUSY, cassandra LWT applied:false) wrap their retries internally
up to a configurable budget. Exhaustion without success and without
ctx cancellation surfaces as ErrRetryExhausted — distinct from
ErrConflict which remains reserved for single-attempt conflict
reporting.

Refs data-ingestion-qa-subproject-b-design.md §4.1, §4.2, §5.3."
```

---

## Task 2: [cyoda-go-spi] ExtendSchema contract godoc update

**Files:**
- Working dir: `/Users/paul/go-projects/cyoda-light/cyoda-go-spi/`
- Modify: `persistence.go`

- [ ] **Step 2.1: Locate the ExtendSchema method declaration**

```bash
grep -n "ExtendSchema" /Users/paul/go-projects/cyoda-light/cyoda-go-spi/persistence.go
```

Find the godoc comment block preceding the `ExtendSchema` method declaration on the `ModelStore` interface.

- [ ] **Step 2.2: Update the godoc**

Replace the existing godoc block immediately above `ExtendSchema(...) error` with:

```go
// ExtendSchema appends a schema delta for the model at ref. The
// delta is an opaque, plugin-agnostic blob that the plugin stores
// verbatim in its extension log; folding the log into the current
// schema is done on read via a plugin-injected ApplyFunc.
//
// Contract:
//   - Success (nil return) means the extension is durably committed
//     and visible to subsequent reads on this node.
//   - A non-nil error means no persisted effect — no log entry,
//     no savepoint, no partial state.
//   - Plugins with a native conflict surface (sqlite SQLITE_BUSY,
//     cassandra LWT applied:false) retry transparently up to a
//     configurable budget. On exhaustion without ctx cancellation,
//     return ErrRetryExhausted.
//   - Context cancellation between retry attempts returns ctx.Err()
//     (wrapped with attempt count), not ErrRetryExhausted. Mid-attempt
//     cancellation follows backend-native behavior.
//   - Plugins without a conflict surface (memory, postgres) commit
//     immediately or fail with the backend's native error.
//
// Empty or nil deltas are a no-op and return nil.
ExtendSchema(ctx context.Context, ref ModelRef, delta SchemaDelta) error
```

- [ ] **Step 2.3: Verify the package still builds and tests pass**

```bash
go build ./...
go test ./...
```

Expected: clean build, all tests green.

- [ ] **Step 2.4: Commit**

```bash
git add persistence.go
git commit -m "docs(spi): clarify ExtendSchema retry + ctx-cancellation contract

Make the contract explicit before B's plugin changes consume it:
plugins with a native conflict surface retry internally to a
configurable budget; exhaustion without cancellation surfaces as
ErrRetryExhausted; ctx cancellation between attempts returns
ctx.Err() (never ErrRetryExhausted); mid-attempt cancellation
follows backend-native behavior; no persisted effect on non-nil
error; empty deltas are a no-op.

Refs data-ingestion-qa-subproject-b-design.md §4.2."
```

---

## Task 3: [cyoda-go-spi] Cut v0.6.0 tag (end of Phase 1)

**Files:** none — git-only.

- [ ] **Step 3.1: Review the branch's commits**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi
git log --oneline main..feat/subproject-b-persistence
```

Expected: two commits — `feat(spi): add ErrRetryExhausted ...` and `docs(spi): clarify ExtendSchema retry ...`.

- [ ] **Step 3.2: Merge to main and tag**

Open a PR on GitHub with `gh pr create` for the two commits, or (if maintainer prerogative allows a direct merge) fast-forward main:

```bash
git checkout main
git merge feat/subproject-b-persistence
git tag v0.6.0 -a -m "Release v0.6.0 — B retry + ExtendSchema contract clarifications"
git push origin main
git push origin v0.6.0
```

Expected: tag `v0.6.0` visible on the remote.

**Checkpoint.** After this task, the cyoda-go plugin modules can bump `go.mod` to `v0.6.0`. Subsequent tasks in cyoda-go consume this tag.

---

## Task 4: [plugins/postgres] Add SchemaSavepointInterval to Config

**Files:**
- Working dir: `/Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence/`
- Modify: `plugins/postgres/config.go`

- [ ] **Step 4.1: Write a failing test for parseConfig reading the new env**

Append to `plugins/postgres/config_secret_test.go`:

```go
func TestParseConfig_SchemaSavepointInterval(t *testing.T) {
	env := map[string]string{
		"CYODA_POSTGRES_URL":              "postgres://localhost/x",
		"CYODA_SCHEMA_SAVEPOINT_INTERVAL": "128",
	}
	cfg, err := parseConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.SchemaSavepointInterval != 128 {
		t.Errorf("SchemaSavepointInterval = %d, want 128", cfg.SchemaSavepointInterval)
	}
}

func TestParseConfig_SchemaSavepointInterval_DefaultOnUnset(t *testing.T) {
	env := map[string]string{
		"CYODA_POSTGRES_URL": "postgres://localhost/x",
	}
	cfg, _ := parseConfig(func(k string) string { return env[k] })
	if cfg.SchemaSavepointInterval != 64 {
		t.Errorf("SchemaSavepointInterval default = %d, want 64", cfg.SchemaSavepointInterval)
	}
}

func TestParseConfig_SchemaSavepointInterval_DefaultOnInvalid(t *testing.T) {
	env := map[string]string{
		"CYODA_POSTGRES_URL":              "postgres://localhost/x",
		"CYODA_SCHEMA_SAVEPOINT_INTERVAL": "not-an-int",
	}
	cfg, _ := parseConfig(func(k string) string { return env[k] })
	if cfg.SchemaSavepointInterval != 64 {
		t.Errorf("SchemaSavepointInterval on invalid input = %d, want 64 (fallback)", cfg.SchemaSavepointInterval)
	}
}

func TestParseConfig_SchemaSavepointInterval_DefaultOnZero(t *testing.T) {
	env := map[string]string{
		"CYODA_POSTGRES_URL":              "postgres://localhost/x",
		"CYODA_SCHEMA_SAVEPOINT_INTERVAL": "0",
	}
	cfg, _ := parseConfig(func(k string) string { return env[k] })
	if cfg.SchemaSavepointInterval != 64 {
		t.Errorf("SchemaSavepointInterval on 0 = %d, want 64 (min 1 with fallback to default)", cfg.SchemaSavepointInterval)
	}
}
```

- [ ] **Step 4.2: Run tests to verify they fail**

```bash
cd plugins/postgres
go test -run TestParseConfig_SchemaSavepointInterval -v
```

Expected: FAIL — undefined field `SchemaSavepointInterval`.

- [ ] **Step 4.3: Add the field + env read**

In `plugins/postgres/config.go`, update `config` struct and `parseConfig`:

```go
type config struct {
	URL                     string
	MaxConns                int32
	MinConns                int32
	MaxConnIdleTime         time.Duration
	AutoMigrate             bool
	SchemaSavepointInterval int // default 64; read from CYODA_SCHEMA_SAVEPOINT_INTERVAL; min 1
}

func parseConfig(getenv func(string) string) (config, error) {
	// ... existing body ...
	cfg := config{
		URL:                     url,
		MaxConns:                int32(envInt(getenv, "CYODA_POSTGRES_MAX_CONNS", 25)),
		MinConns:                int32(envInt(getenv, "CYODA_POSTGRES_MIN_CONNS", 5)),
		MaxConnIdleTime:         envDuration(getenv, "CYODA_POSTGRES_MAX_CONN_IDLE_TIME", 5*time.Minute),
		AutoMigrate:             envBool(getenv, "CYODA_POSTGRES_AUTO_MIGRATE", true),
		SchemaSavepointInterval: envIntMin1(getenv, "CYODA_SCHEMA_SAVEPOINT_INTERVAL", 64),
	}
	if cfg.URL == "" {
		return cfg, fmt.Errorf("CYODA_POSTGRES_URL is required")
	}
	return cfg, nil
}
```

Add helper near the existing `envInt`:

```go
// envIntMin1 reads an integer env var, applies the default when unset
// or invalid, and also applies the default when the value is < 1.
// Used for interval-style config where 0 is not a meaningful value.
func envIntMin1(getenv func(string) string, key string, dflt int) int {
	v := envInt(getenv, key, dflt)
	if v < 1 {
		slog.Warn("env var below minimum; using default", "key", key, "value", v, "default", dflt)
		return dflt
	}
	return v
}
```

Ensure `log/slog` is imported in `config.go`.

- [ ] **Step 4.4: Run tests to verify they pass**

```bash
go test -run TestParseConfig_SchemaSavepointInterval -v
go test ./...
```

Expected: all four new tests PASS, rest of the plugin suite green.

- [ ] **Step 4.5: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/postgres/config.go plugins/postgres/config_secret_test.go
git commit -m "feat(postgres): add SchemaSavepointInterval config knob

Reads CYODA_SCHEMA_SAVEPOINT_INTERVAL from env, default 64 (unchanged
from the current hardcoded value). Invalid values and <1 fall back to
default with a logged warning. Field not yet consumed — Task 7 rewires
the savepoint trigger.

Refs data-ingestion-qa-subproject-b-design.md §4.3."
```

---

## Task 5: [plugins/sqlite] Add SchemaSavepointInterval + SchemaExtendMaxRetries to Config

**Files:**
- Modify: `plugins/sqlite/config.go`, `plugins/sqlite/config_test.go` (or create).

- [ ] **Step 5.1: Write failing tests**

Create or append to `plugins/sqlite/config_test.go`:

```go
func TestParseConfig_SchemaKnobs_Defaults(t *testing.T) {
	env := map[string]string{}
	cfg, _ := parseConfig(func(k string) string { return env[k] })
	if cfg.SchemaSavepointInterval != 64 {
		t.Errorf("SchemaSavepointInterval default = %d, want 64", cfg.SchemaSavepointInterval)
	}
	if cfg.SchemaExtendMaxRetries != 8 {
		t.Errorf("SchemaExtendMaxRetries default = %d, want 8", cfg.SchemaExtendMaxRetries)
	}
}

func TestParseConfig_SchemaKnobs_ReadFromEnv(t *testing.T) {
	env := map[string]string{
		"CYODA_SCHEMA_SAVEPOINT_INTERVAL":  "128",
		"CYODA_SCHEMA_EXTEND_MAX_RETRIES":  "16",
	}
	cfg, _ := parseConfig(func(k string) string { return env[k] })
	if cfg.SchemaSavepointInterval != 128 {
		t.Errorf("interval = %d, want 128", cfg.SchemaSavepointInterval)
	}
	if cfg.SchemaExtendMaxRetries != 16 {
		t.Errorf("max retries = %d, want 16", cfg.SchemaExtendMaxRetries)
	}
}

func TestParseConfig_SchemaKnobs_DefaultOnInvalid(t *testing.T) {
	env := map[string]string{
		"CYODA_SCHEMA_SAVEPOINT_INTERVAL": "-5",
		"CYODA_SCHEMA_EXTEND_MAX_RETRIES": "0",
	}
	cfg, _ := parseConfig(func(k string) string { return env[k] })
	if cfg.SchemaSavepointInterval != 64 {
		t.Errorf("interval on -5 = %d, want 64 (fallback)", cfg.SchemaSavepointInterval)
	}
	if cfg.SchemaExtendMaxRetries != 8 {
		t.Errorf("max retries on 0 = %d, want 8 (fallback)", cfg.SchemaExtendMaxRetries)
	}
}
```

- [ ] **Step 5.2: Run to verify failure**

```bash
cd plugins/sqlite
go test -run TestParseConfig_SchemaKnobs -v
```

Expected: FAIL — undefined fields.

- [ ] **Step 5.3: Implement**

In `plugins/sqlite/config.go`, add the two fields to the config struct and read them in `parseConfig` via an `envIntMin1` helper (paralleling the postgres helper from Task 4). If sqlite's `config.go` does not already have the helper, add it (copying the function body from Task 4 Step 4.3).

```go
type config struct {
	// ... existing ...
	SchemaSavepointInterval int
	SchemaExtendMaxRetries  int
}
```

In `parseConfig`:

```go
cfg.SchemaSavepointInterval = envIntMin1(getenv, "CYODA_SCHEMA_SAVEPOINT_INTERVAL", 64)
cfg.SchemaExtendMaxRetries = envIntMin1(getenv, "CYODA_SCHEMA_EXTEND_MAX_RETRIES", 8)
```

- [ ] **Step 5.4: Verify**

```bash
go test -run TestParseConfig_SchemaKnobs -v
go test ./...
```

All PASS.

- [ ] **Step 5.5: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/sqlite/config.go plugins/sqlite/config_test.go
git commit -m "feat(sqlite): add SchemaSavepointInterval + SchemaExtendMaxRetries config

Defaults: interval 64, max retries 8. Invalid or <1 values fall back
to defaults with logged warnings. Fields not yet consumed — Tasks
15-19 wire them into the log-based ExtendSchema and retry wrapper.

Refs data-ingestion-qa-subproject-b-design.md §4.3."
```

---

## Task 6: [plugins/memory] B-I6 — explicit rejection atomicity test

**Files:**
- Modify: `plugins/memory/model_extensions_test.go`.

- [ ] **Step 6.1: Write the test**

Append to `plugins/memory/model_extensions_test.go`:

```go
// TestMemory_ExtendSchema_RejectionLeavesDescriptorUnmutated asserts
// B-I6 for the memory backend: when the injected applyFunc returns
// an error, the model descriptor's schema bytes are unchanged.
// Memory has no extension log, so "no persisted trace" reduces to
// "descriptor unmutated."
func TestMemory_ExtendSchema_RejectionLeavesDescriptorUnmutated(t *testing.T) {
	factory := newTestFactory(t)
	rejectingApply := func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		return nil, fmt.Errorf("simulated ChangeLevel violation")
	}
	factory.SetApplyFunc(rejectingApply)

	store := factory.ModelStore()
	ctx := withTenant(t.Context(), "t1")
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}

	// Seed a descriptor with a known schema.
	desc := &spi.ModelDescriptor{
		Ref:    ref,
		Schema: []byte(`{"type":"object"}`),
		State:  spi.ModelDraft,
	}
	if err := store.Save(ctx, desc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Capture before-state.
	before, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get (before): %v", err)
	}
	beforeBytes := append([]byte(nil), before.Schema...)

	// Attempt the extension; it must fail.
	err = store.ExtendSchema(ctx, ref, spi.SchemaDelta(`{"op":"add-field"}`))
	if err == nil {
		t.Fatal("ExtendSchema with rejecting applyFunc must return error")
	}

	// Assert the descriptor schema is byte-identical to the before-state.
	after, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get (after): %v", err)
	}
	if !bytes.Equal(beforeBytes, after.Schema) {
		t.Errorf("schema mutated on rejection: before=%q after=%q", beforeBytes, after.Schema)
	}
}
```

Use the existing test-helper factory pattern from the surrounding file (`newTestFactory`, `withTenant`, etc.). If those helpers don't exist, add them at the top of the test file.

- [ ] **Step 6.2: Run to verify it passes (memory already honors this)**

```bash
cd plugins/memory
go test -run TestMemory_ExtendSchema_RejectionLeavesDescriptorUnmutated -v
```

Expected: PASS — memory's existing implementation already handles this by structure (apply-or-no-assign).

- [ ] **Step 6.3: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/memory/model_extensions_test.go
git commit -m "test(memory): B-I6 — rejection leaves descriptor unmutated

Explicit test for memory's contribution to B-I6 (cross-storage
atomicity on rejection). Memory has no log, so the invariant
reduces to 'descriptor schema unmutated when applyFunc returns
error.' Existing implementation already honors this; test
promotes the implicit assurance to an executable contract.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I6, §5.1."
```

---

## Task 7: [plugins/memory] B-I7 — concurrent-extend convergence test

**Files:**
- Modify: `plugins/memory/model_extensions_test.go`.

- [ ] **Step 7.1: Write the test**

Append to `plugins/memory/model_extensions_test.go`:

```go
// TestMemory_ExtendSchema_ConvergenceUnderConcurrency asserts B-I7
// for memory: N goroutines extending the same model concurrently
// produce a final schema identical to a single-goroutine replay of
// the same deltas in any serial order (by A.2's I2 commutativity).
// The assertion is on state equivalence across orderings, not on
// "no torn writes" — which would be circular given the mutex.
func TestMemory_ExtendSchema_ConvergenceUnderConcurrency(t *testing.T) {
	const N = 8

	// applyFunc concatenates deltas into the schema so final bytes
	// are deterministic regardless of apply order.
	sortedApply := func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		// Represent "schema" as a sorted concatenation of delta bytes
		// so the result is commutative under set-union.
		m := map[string]struct{}{}
		for _, chunk := range bytes.Split(base, []byte{'\n'}) {
			if len(chunk) > 0 {
				m[string(chunk)] = struct{}{}
			}
		}
		m[string(delta)] = struct{}{}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return []byte(strings.Join(keys, "\n")), nil
	}

	// Build expected: single-goroutine serial replay of deltas.
	deltas := make([]spi.SchemaDelta, N)
	for i := 0; i < N; i++ {
		deltas[i] = spi.SchemaDelta(fmt.Sprintf("d%02d", i))
	}
	expected := []byte{}
	for _, d := range deltas {
		v, _ := sortedApply(expected, d)
		expected = v
	}

	// Run N goroutines.
	factory := newTestFactory(t)
	factory.SetApplyFunc(sortedApply)
	store := factory.ModelStore()
	ctx := withTenant(t.Context(), "t1")
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	if err := store.Save(ctx, &spi.ModelDescriptor{Ref: ref, Schema: []byte{}, State: spi.ModelDraft}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			if err := store.ExtendSchema(ctx, ref, deltas[i]); err != nil {
				t.Errorf("goroutine %d ExtendSchema: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// Read final state and compare to serial-replay expected.
	got, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Schema, expected) {
		t.Errorf("concurrent final state != serial replay\n  got:  %q\n  want: %q", got.Schema, expected)
	}
}
```

- [ ] **Step 7.2: Run it**

```bash
cd plugins/memory
go test -run TestMemory_ExtendSchema_ConvergenceUnderConcurrency -v -count=5
```

Expected: PASS on every run (5 runs confirms determinism).

- [ ] **Step 7.3: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/memory/model_extensions_test.go
git commit -m "test(memory): B-I7 — concurrent-extend convergence

N goroutines extending the same model must produce a final schema
byte-identical to a single-goroutine serial replay of the same
deltas. Assertion on state equivalence (I2 commutativity), not on
'no torn writes' which mutex makes circular.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I7, §5.1."
```

---

## Task 8: [plugins/postgres] Thread lastSavepointSeq out of foldLocked

**Rationale:** The refactor of the savepoint trigger from `newSeq%64==0` to `(newSeq - lastSavepointSeq) >= interval` needs `lastSavepointSeq`. Today `foldLocked` queries it internally and discards it. We lift that value out so the `ExtendSchema` savepoint-trigger path can reuse the query result without a second round-trip.

**Files:**
- Modify: `plugins/postgres/model_extensions.go`, `plugins/postgres/model_extensions_test.go`.

- [ ] **Step 8.1: Write a failing test**

Append to `plugins/postgres/model_extensions_test.go`:

```go
// TestFoldLocked_ReturnsLastSavepointSeq — new helper function
// lastSavepointSeq(ctx, ref) (int64, error) returns the seq of
// the most-recent savepoint row for ref, or 0 if none exists.
// Task 10 refactor uses this to drive the savepoint trigger.
func TestLastSavepointSeq_NoSavepoint(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	seq, err := fx.store.lastSavepointSeq(fx.ctx, ref)
	if err != nil {
		t.Fatalf("lastSavepointSeq: %v", err)
	}
	if seq != 0 {
		t.Errorf("lastSavepointSeq on empty log = %d, want 0", seq)
	}
}

func TestLastSavepointSeq_ReturnsMostRecent(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))
	// Use the test helper to force a savepoint at seq=64 and seq=128
	// (requires interval=64 which is the default).
	for i := 0; i < 128; i++ {
		if err := fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d%d", i))); err != nil {
			t.Fatalf("ExtendSchema %d: %v", i, err)
		}
	}
	seq, err := fx.store.lastSavepointSeq(fx.ctx, ref)
	if err != nil {
		t.Fatalf("lastSavepointSeq: %v", err)
	}
	if seq != 128 {
		t.Errorf("lastSavepointSeq = %d, want 128", seq)
	}
}
```

- [ ] **Step 8.2: Run to verify failure**

```bash
cd plugins/postgres
go test -run "TestLastSavepointSeq_" -v
```

Expected: FAIL — `lastSavepointSeq` method undefined.

- [ ] **Step 8.3: Implement**

In `plugins/postgres/model_extensions.go`, add:

```go
// lastSavepointSeq returns the seq of the most-recent savepoint row
// for ref, or 0 if no savepoint rows exist. Used by ExtendSchema to
// drive the savepoint trigger without a separate round-trip at
// extension time.
func (s *modelStore) lastSavepointSeq(ctx context.Context, ref spi.ModelRef) (int64, error) {
	var seq int64
	err := s.q.QueryRow(ctx, `
		SELECT seq FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&seq)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("lastSavepointSeq: %w", err)
	default:
		return seq, nil
	}
}
```

- [ ] **Step 8.4: Run and confirm pass**

```bash
go test -run "TestLastSavepointSeq_" -v
go test ./...
```

All PASS.

- [ ] **Step 8.5: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/postgres/model_extensions.go plugins/postgres/model_extensions_test.go
git commit -m "feat(postgres): add lastSavepointSeq helper for B-I4 refactor

Task 9 refactors the savepoint trigger to '(newSeq - lastSavepointSeq)
>= interval'. This helper returns the most-recent savepoint seq,
surfacing a value the foldLocked query already computes internally.
Reused by both fold and extend paths; no extra round-trip on
extension.

Refs data-ingestion-qa-subproject-b-design.md §5.2 point 1."
```

---

## Task 9: [plugins/postgres] Refactor savepoint trigger to config-driven "since last savepoint"

**Files:**
- Modify: `plugins/postgres/model_store.go` (the `ExtendSchema` method body around line 276).
- Modify: `plugins/postgres/store_factory.go` (thread `cfg.SchemaSavepointInterval` into the store).

- [ ] **Step 9.1: Write the failing test**

Append to `plugins/postgres/model_extensions_test.go`:

```go
// TestExtendSchema_SavepointTriggerRespectsIntervalChange — B-I4.
// Start with interval 64, commit past the first savepoint at
// seq=64. Change interval to 128, commit more deltas. The next
// savepoint fires at (newSeq - 64) >= 128, i.e. newSeq=192, NOT
// at the next global multiple of 128 (seq=128) which would be only
// 64 deltas past the first savepoint.
func TestExtendSchema_SavepointTriggerRespectsIntervalChange(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	// Interval = 64 (default). Commit 64 deltas — expect one savepoint at seq=64.
	for i := 0; i < 64; i++ {
		if err := fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d%d", i))); err != nil {
			t.Fatalf("ExtendSchema %d: %v", i, err)
		}
	}
	lastSP, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if lastSP != 64 {
		t.Fatalf("after 64 deltas with interval=64, lastSavepointSeq = %d, want 64", lastSP)
	}

	// Rebuild the store with interval=128 (simulates operator reconfig).
	fx.reopenWithInterval(t, 128)

	// Commit 63 more deltas (total 127, newSeq=127). No new savepoint:
	// 127-64 = 63 < 128.
	for i := 0; i < 63; i++ {
		if err := fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d64-%d", i))); err != nil {
			t.Fatalf("ExtendSchema post-reconfig %d: %v", i, err)
		}
	}
	lastSP, _ = fx.store.lastSavepointSeq(fx.ctx, ref)
	if lastSP != 64 {
		t.Errorf("interval=128, only 63 deltas since savepoint: lastSavepointSeq = %d, want still 64", lastSP)
	}

	// One more delta — seq=128, 128-64=64 < 128, still no savepoint.
	_ = fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta("d-boundary-nope"))
	lastSP, _ = fx.store.lastSavepointSeq(fx.ctx, ref)
	if lastSP != 64 {
		t.Errorf("interval=128 at seq=128 (only 64 since savepoint): lastSavepointSeq = %d, want 64", lastSP)
	}

	// Commit until seq-since-last-savepoint reaches 128 (need 64 more deltas).
	for i := 0; i < 64; i++ {
		_ = fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d128-%d", i)))
	}
	lastSP, _ = fx.store.lastSavepointSeq(fx.ctx, ref)
	if lastSP != 192 {
		t.Errorf("after 128 deltas since last savepoint: lastSavepointSeq = %d, want 192", lastSP)
	}
}
```

Add `reopenWithInterval` to the test fixture — constructs a fresh `modelStore` against the same database, with the config overridden.

- [ ] **Step 9.2: Run to verify failure**

```bash
go test -run TestExtendSchema_SavepointTriggerRespectsIntervalChange -v
```

Expected: FAIL — the test will fail once the reconfig happens, because the current hardcoded `newSeq%64==0` trigger is tied to the global seq, not to the "since last savepoint" count.

- [ ] **Step 9.3: Refactor ExtendSchema**

In `plugins/postgres/model_store.go`, replace the `if newSeq%64 == 0 { ... }` block at line 276-292 with:

```go
lastSP, err := s.lastSavepointSeq(ctx, ref)
if err != nil {
	return fmt.Errorf("savepoint trigger lookup for %s: %w", ref, err)
}
if newSeq-lastSP >= int64(s.cfg.SchemaSavepointInterval) {
	base, err := s.baseSchema(ctx, ref)
	if err != nil {
		return fmt.Errorf("savepoint base-schema read for %s: %w", ref, err)
	}
	folded, err := s.foldLocked(ctx, ref, base)
	if err != nil {
		return fmt.Errorf("savepoint fold for %s (seq=%d): %w", ref, newSeq, err)
	}
	if _, err := s.q.Exec(ctx,
		`INSERT INTO model_schema_extensions
		    (tenant_id, model_name, model_version, kind, payload, tx_id)
		 VALUES ($1, $2, $3, 'savepoint', $4, $5)`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion, folded, txID); err != nil {
		return fmt.Errorf("failed to write savepoint for %s (seq=%d): %w", ref, newSeq, err)
	}
}
return nil
```

Add `cfg config` field to the `modelStore` struct and thread it through the constructor in `plugins/postgres/store_factory.go`. Where `NewPool`/store-construction happens, pass the full `config` into the store and store `s.cfg = cfg`. The `modelStore` likely already receives a few config values; this is one more field.

- [ ] **Step 9.4: Update the docstring for ExtendSchema**

Replace the existing docstring above `ExtendSchema` in `plugins/postgres/model_store.go`:

```go
// ExtendSchema appends a delta row to model_schema_extensions. When
// the count of deltas since the most recent savepoint reaches the
// configured interval (cfg.SchemaSavepointInterval, default 64),
// a savepoint row holding the fully-folded schema is inserted in
// the same transaction so future Gets can start from there rather
// than replaying the entire log.
//
// Under REPEATABLE READ there is no schema-write conflict surface:
// concurrent writers both succeed, and A.2 I2 (commutativity)
// guarantees the fold is equivalent regardless of interleaving.
// No retry wrapper.
//
// Empty or nil deltas are a no-op.
```

- [ ] **Step 9.5: Run all postgres tests**

```bash
go test ./...
```

Expected: all PASS including the new test and the existing 14 model_extensions tests + 6 FCW tests. The previously-named `TestExtendSchema_SavepointEvery64` may need to stay green at the default interval=64 (verify visually; if its assertion was exactly `seq%64==0` it still holds because the new trigger gives the same result at default interval when starting from no savepoint).

- [ ] **Step 9.6: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/postgres/model_store.go plugins/postgres/store_factory.go plugins/postgres/model_extensions_test.go
git commit -m "feat(postgres): config-driven savepoint interval, 'since last savepoint'

Refactor the savepoint trigger from hardcoded 'newSeq % 64 == 0'
(global-seq modulo) to '(newSeq - lastSavepointSeq) >= interval'
(since-last-savepoint), driven by cfg.SchemaSavepointInterval
(default 64; unchanged in shipped behavior).

Why since-last-savepoint: it survives operator reconfiguration
gracefully. If interval changes from 64 to 128, the next savepoint
fires 128 deltas past the most recent one, not at some arbitrary
future multiple of 128 from a global counter.

Docstring updated to note postgres has no conflict surface on
schema writes under REPEATABLE READ — concurrent writers both
succeed, commutativity (A.2 I2) guarantees equivalent fold.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I4, §5.2."
```

---

## Task 10: [plugins/postgres] Save-on-lock + unlock asymmetry

**Files:**
- Modify: `plugins/postgres/model_store.go` (the `Lock` method).
- Modify: `plugins/postgres/model_extensions_test.go`.

- [ ] **Step 10.1: Write failing tests**

Append to `plugins/postgres/model_extensions_test.go`:

```go
// TestExtendSchema_SaveOnLock — B-I3. Lock transition writes a
// savepoint row atomically with the lock state change.
func TestExtendSchema_SaveOnLock(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	// Pre-lock: no savepoint.
	before, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if before != 0 {
		t.Fatalf("pre-lock lastSavepointSeq = %d, want 0", before)
	}

	// Lock.
	if err := fx.store.Lock(fx.ctx, ref); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// Post-lock: a savepoint exists.
	after, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if after == 0 {
		t.Error("post-lock lastSavepointSeq = 0, want nonzero (savepoint must be written on lock)")
	}

	// Savepoint payload equals the folded schema.
	var payload []byte
	fx.db.QueryRow(fx.ctx,
		`SELECT payload FROM model_schema_extensions
		 WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3 AND kind='savepoint'
		 ORDER BY seq DESC LIMIT 1`,
		fx.tenantID, ref.EntityName, ref.ModelVersion).Scan(&payload)
	if !bytes.Equal(payload, []byte(`{"base":true}`)) {
		t.Errorf("savepoint payload = %q, want base schema %q", payload, `{"base":true}`)
	}
}

// TestExtendSchema_UnlockDoesNotWriteSavepoint — §5.2 asymmetry.
func TestExtendSchema_UnlockDoesNotWriteSavepoint(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	_ = fx.store.Lock(fx.ctx, ref)
	seqAfterLock, _ := fx.store.lastSavepointSeq(fx.ctx, ref)

	if err := fx.store.Unlock(fx.ctx, ref); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	seqAfterUnlock, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if seqAfterUnlock != seqAfterLock {
		t.Errorf("Unlock wrote a savepoint: before=%d after=%d (want unchanged; save-on-unlock is deliberately omitted)", seqAfterLock, seqAfterUnlock)
	}
}
```

- [ ] **Step 10.2: Run to verify failure**

```bash
go test -run "TestExtendSchema_SaveOnLock|TestExtendSchema_UnlockDoesNotWriteSavepoint" -v
```

Expected: first test FAILs (save-on-lock not yet implemented), second passes.

- [ ] **Step 10.3: Implement save-on-lock in the Lock method**

In `plugins/postgres/model_store.go`, locate the `Lock(ctx, ref)` method. Wrap the existing state-change logic in a transaction that also inserts a savepoint row:

```go
func (s *modelStore) Lock(ctx context.Context, ref spi.ModelRef) error {
	// ... existing preconditions (model exists, not already locked) ...

	// Compute the current folded schema BEFORE changing state.
	base, err := s.baseSchema(ctx, ref)
	if err != nil {
		return fmt.Errorf("lock fold base for %s: %w", ref, err)
	}
	folded, err := s.foldLocked(ctx, ref, base)
	if err != nil {
		return fmt.Errorf("lock fold for %s: %w", ref, err)
	}

	txID := ""
	if tx := spi.GetTransaction(ctx); tx != nil {
		txID = tx.ID
	}

	// Update lock state + insert savepoint atomically.
	if _, err := s.q.Exec(ctx,
		`UPDATE models SET doc = jsonb_set(doc, '{state}', to_jsonb('LOCKED'::text)) WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion); err != nil {
		return fmt.Errorf("lock state update for %s: %w", ref, err)
	}

	if _, err := s.q.Exec(ctx,
		`INSERT INTO model_schema_extensions
		    (tenant_id, model_name, model_version, kind, payload, tx_id)
		 VALUES ($1, $2, $3, 'savepoint', $4, $5)`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion, folded, txID); err != nil {
		return fmt.Errorf("lock savepoint for %s: %w", ref, err)
	}
	return nil
}
```

Inspect the actual existing `Lock` implementation first and adapt — the UPDATE shape may differ (e.g., a dedicated `is_locked` column). The key elements are: (a) lock state change and savepoint insert happen in the same `ctx` (= same transaction); (b) savepoint is written AFTER the state change; (c) the payload is the fold as-of pre-lock.

- [ ] **Step 10.4: Run tests to verify pass**

```bash
go test -run "TestExtendSchema_SaveOnLock|TestExtendSchema_UnlockDoesNotWriteSavepoint" -v
go test ./...
```

All PASS.

- [ ] **Step 10.5: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/postgres/model_store.go plugins/postgres/model_extensions_test.go
git commit -m "feat(postgres): save-on-lock atomicity (B-I3); unlock asymmetry test

On Lock transition (unlocked → locked), compute the fold and write
a savepoint row atomically with the lock state change in the same
transaction. Post-lock reads start from this savepoint, keeping
fold cost bounded independent of pre-lock extension count.

Unlock deliberately does NOT write a savepoint — extension log is
unchanged, fold cost identical before and after, and the size
trigger continues to fire on further draft-phase extensions.
A savepoint on unlock would be load-bearing-free noise.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I3, §5.2."
```

---

## Task 11: [plugins/postgres] B-I7 — commutative-append convergence test

**Files:**
- Modify: `plugins/postgres/model_extensions_test.go`.

- [ ] **Step 11.1: Write the test**

```go
// TestExtendSchema_CommutativeAppend_ConvergesUnderConcurrency — B-I7
// for postgres: N goroutines extend concurrently, all commit (no
// retry path), and the fold is a deterministic set-union of the
// deltas equivalent to any serial ordering.
func TestExtendSchema_CommutativeAppend_ConvergesUnderConcurrency(t *testing.T) {
	const N = 8
	fx := newPGFixture(t)
	// Wire an applyFunc that does set-union of newline-separated tokens.
	fx.factory.SetApplyFunc(setUnionApplyFunc)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})

	deltas := make([]string, N)
	for i := 0; i < N; i++ {
		deltas[i] = fmt.Sprintf("d%02d", i)
	}

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			err := fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(deltas[i]))
			if err != nil {
				t.Errorf("ExtendSchema #%d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// Compute expected via serial replay.
	expected := []byte{}
	for _, d := range deltas {
		expected, _ = setUnionApplyFunc(expected, spi.SchemaDelta(d))
	}
	expectedSorted := sortNewlineTokens(expected)

	got, err := fx.store.Get(fx.ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	gotSorted := sortNewlineTokens(got.Schema)
	if !bytes.Equal(gotSorted, expectedSorted) {
		t.Errorf("concurrent fold != serial-replay fold\n  got:  %q\n  want: %q", gotSorted, expectedSorted)
	}

	// Bonus: assert all N deltas landed as rows in the log.
	var count int
	fx.db.QueryRow(fx.ctx,
		`SELECT COUNT(*) FROM model_schema_extensions
		 WHERE tenant_id=$1 AND model_name=$2 AND model_version=$3 AND kind='delta'`,
		fx.tenantID, ref.EntityName, ref.ModelVersion).Scan(&count)
	if count != N {
		t.Errorf("delta row count = %d, want %d (all writers must commit)", count, N)
	}
}
```

Define test helpers `setUnionApplyFunc` and `sortNewlineTokens` at the top of the test file (or in a shared `testutil.go`):

```go
func setUnionApplyFunc(base []byte, delta spi.SchemaDelta) ([]byte, error) {
	m := map[string]struct{}{}
	for _, tok := range bytes.Split(base, []byte{'\n'}) {
		if len(tok) > 0 {
			m[string(tok)] = struct{}{}
		}
	}
	m[string(delta)] = struct{}{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return []byte(strings.Join(keys, "\n")), nil
}
func sortNewlineTokens(b []byte) []byte {
	parts := strings.Split(string(b), "\n")
	sort.Strings(parts)
	return []byte(strings.Join(parts, "\n"))
}
```

- [ ] **Step 11.2: Run the test**

```bash
cd plugins/postgres
go test -run TestExtendSchema_CommutativeAppend_ConvergesUnderConcurrency -v -count=3
```

Expected: PASS on all 3 runs. If it fails, the likely cause is the test fixture not wiring `ApplyFunc`; adjust the fixture.

- [ ] **Step 11.3: Commit**

```bash
git add plugins/postgres/model_extensions_test.go
git commit -m "test(postgres): B-I7 — commutative-append convergence

N goroutines extending the same model under REPEATABLE READ both
commit and the final fold equals any serial-replay fold. No retry
path in postgres (no conflict surface on schema writes). Convergence
is A.2 I2 + I5 applied directly.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I7 note, §5.2."
```

---

## Task 12: [plugins/postgres] Context-cancellation test

**Files:**
- Modify: `plugins/postgres/model_extensions_test.go`.

- [ ] **Step 12.1: Write the test**

```go
// TestExtendSchema_ContextCancellation_ReturnsCtxErr — §4.2 contract.
// Postgres doesn't retry, but the cancellation-contract still applies
// uniformly: if ctx is cancelled mid-query, the returned error wraps
// ctx.Err() and is not ErrRetryExhausted.
func TestExtendSchema_ContextCancellation_ReturnsCtxErr(t *testing.T) {
	fx := newPGFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	ctx, cancel := context.WithCancel(fx.ctx)
	cancel() // cancel before calling — guarantees ctx.Err() before any query

	err := fx.store.ExtendSchema(ctx, ref, spi.SchemaDelta(`d1`))
	if err == nil {
		t.Fatal("ExtendSchema with cancelled ctx must fail")
	}
	if errors.Is(err, spi.ErrRetryExhausted) {
		t.Errorf("cancelled ctx → ErrRetryExhausted (want ctx.Err()); err = %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx → non-ctx error; err = %v", err)
	}
}
```

- [ ] **Step 12.2: Run and verify**

```bash
go test -run TestExtendSchema_ContextCancellation_ReturnsCtxErr -v
```

Expected: PASS — postgres's pgx driver surfaces `context.Canceled` natively; no wrapper logic needed on postgres.

- [ ] **Step 12.3: Commit**

```bash
git add plugins/postgres/model_extensions_test.go
git commit -m "test(postgres): ctx cancellation returns ctx.Err(), not ErrRetryExhausted

Asserts the §4.2 contract uniformly across postgres (which has no
retry loop) by verifying pgx's native cancellation behavior surfaces
the context error rather than anything from the retry-budget path.

Refs data-ingestion-qa-subproject-b-design.md §4.2."
```

---

## Task 13: [plugins/postgres] Fold-across-savepoint-boundary test

**Files:**
- Modify: `plugins/postgres/model_extensions_test.go`.

- [ ] **Step 13.1: Write the test**

```go
// TestExtendSchema_FoldAcrossSavepointBoundary_ByteIdentical — B-I1 +
// B-I2 local. Fold a log that spans one or more savepoints; assert
// the result is byte-identical to folding the same deltas without
// savepoints (simulated by disabling the savepoint trigger via a
// huge interval).
func TestExtendSchema_FoldAcrossSavepointBoundary_ByteIdentical(t *testing.T) {
	// Case A: savepoints enabled, interval=64.
	a := newPGFixture(t)
	a.factory.SetApplyFunc(setUnionApplyFunc)
	refA := spi.ModelRef{EntityName: "A", ModelVersion: "1"}
	a.SaveModel(t, refA, []byte{})
	for i := 0; i < 150; i++ {
		_ = a.store.ExtendSchema(a.ctx, refA, spi.SchemaDelta(fmt.Sprintf("d%03d", i)))
	}
	got, _ := a.store.Get(a.ctx, refA)
	gotSorted := sortNewlineTokens(got.Schema)

	// Case B: savepoints effectively disabled, interval=huge.
	b := newPGFixtureWithInterval(t, 1_000_000)
	b.factory.SetApplyFunc(setUnionApplyFunc)
	refB := spi.ModelRef{EntityName: "B", ModelVersion: "1"}
	b.SaveModel(t, refB, []byte{})
	for i := 0; i < 150; i++ {
		_ = b.store.ExtendSchema(b.ctx, refB, spi.SchemaDelta(fmt.Sprintf("d%03d", i)))
	}
	want, _ := b.store.Get(b.ctx, refB)
	wantSorted := sortNewlineTokens(want.Schema)

	if !bytes.Equal(gotSorted, wantSorted) {
		t.Errorf("fold with savepoints != fold without\n  with savepoints:    %q\n  without savepoints: %q", gotSorted, wantSorted)
	}
}
```

Add `newPGFixtureWithInterval(t, interval)` helper to the fixture.

- [ ] **Step 13.2: Run**

```bash
go test -run TestExtendSchema_FoldAcrossSavepointBoundary_ByteIdentical -v
```

Expected: PASS.

- [ ] **Step 13.3: Commit**

```bash
git add plugins/postgres/model_extensions_test.go
git commit -m "test(postgres): B-I2 — fold across savepoint boundary byte-identical

Run 150 deltas with interval=64 (multiple savepoints fire) and the
same sequence with interval=1_000_000 (no savepoints). Folds must
be byte-identical. B-I2 transparency asserted at the plugin layer.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I2, §5.2."
```

---

## Task 14: [plugins/postgres] Rejection leaves no savepoint or op

**Files:**
- Modify: `plugins/postgres/model_extensions_test.go`.

- [ ] **Step 14.1: Write the test**

```go
// TestExtendSchema_RejectionLeavesNoSavepointOrOp — B-I6 tightening.
// If applyFunc errors (simulating a ChangeLevel violation), the
// delta row and any would-be savepoint must both be absent.
func TestExtendSchema_RejectionLeavesNoSavepointOrOp(t *testing.T) {
	fx := newPGFixture(t)
	// Wire a rejecting applyFunc, but only for the RESTRICT delta.
	fx.factory.SetApplyFunc(func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		if bytes.Contains([]byte(delta), []byte("RESTRICT")) {
			return nil, fmt.Errorf("simulated ChangeLevel violation")
		}
		return setUnionApplyFunc(base, delta)
	})
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})

	// Commit 63 valid deltas — just below the 64 interval.
	for i := 0; i < 63; i++ {
		_ = fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d%d", i)))
	}

	// Capture pre-state.
	var preDeltaCount, preSPCount int
	fx.db.QueryRow(fx.ctx, `SELECT COUNT(*) FROM model_schema_extensions WHERE kind='delta'`).Scan(&preDeltaCount)
	fx.db.QueryRow(fx.ctx, `SELECT COUNT(*) FROM model_schema_extensions WHERE kind='savepoint'`).Scan(&preSPCount)

	// Attempt the 64th delta — but the applyFunc will reject. The
	// delta row itself MAY still be written by postgres (INSERT is
	// pre-fold); but since the savepoint path calls fold which calls
	// applyFunc, the savepoint write must fail and the whole tx
	// rolls back. Assert both the delta row and the savepoint row
	// are absent.
	err := fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta("RESTRICT-d63"))
	if err == nil {
		t.Fatal("ExtendSchema with rejecting applyFunc on savepoint path must fail")
	}

	var postDeltaCount, postSPCount int
	fx.db.QueryRow(fx.ctx, `SELECT COUNT(*) FROM model_schema_extensions WHERE kind='delta'`).Scan(&postDeltaCount)
	fx.db.QueryRow(fx.ctx, `SELECT COUNT(*) FROM model_schema_extensions WHERE kind='savepoint'`).Scan(&postSPCount)

	if postDeltaCount != preDeltaCount {
		t.Errorf("rejected extension added a delta row: before=%d after=%d", preDeltaCount, postDeltaCount)
	}
	if postSPCount != preSPCount {
		t.Errorf("rejected extension added a savepoint row: before=%d after=%d", preSPCount, postSPCount)
	}
}
```

Note: this test requires that `ExtendSchema` runs inside an outer transaction that can roll back BOTH the delta row and the savepoint attempt when the savepoint-fold call errors. Verify the existing `ExtendSchema` is called within a pgx transaction (the plugin's `Querier` resolution uses `spi.GetTransaction(ctx)` at line 261). If the test runs WITHOUT an ambient tx, the delta row persists despite the savepoint failure. In that case, adjust the test fixture to wrap the call in a pgx `BeginTx`/rollback-on-error scope, OR fix the production code so `ExtendSchema` self-wraps when no ambient tx is present. The right answer is the latter — `ExtendSchema` must be internally atomic regardless of ambient-tx presence.

- [ ] **Step 14.2: If necessary, add self-wrapping tx to ExtendSchema**

If the test fails at this step, add to `plugins/postgres/model_store.go` `ExtendSchema` a self-wrap that begins a tx if none is active:

```go
func (s *modelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if len(delta) == 0 {
		return nil
	}
	if spi.GetTransaction(ctx) == nil {
		// Self-wrap for atomicity when no ambient tx is active.
		return s.withinTx(ctx, func(tctx context.Context) error {
			return s.extendSchemaInTx(tctx, ref, delta)
		})
	}
	return s.extendSchemaInTx(ctx, ref, delta)
}
```

Where `withinTx` uses `pgxpool.Begin(ctx)` + `Commit`/`Rollback`. Adapt to the plugin's existing tx helpers.

- [ ] **Step 14.3: Run and verify**

```bash
go test -run TestExtendSchema_RejectionLeavesNoSavepointOrOp -v
go test ./...
```

All PASS.

- [ ] **Step 14.4: Commit**

```bash
git add plugins/postgres/model_store.go plugins/postgres/model_extensions_test.go
git commit -m "feat(postgres): ExtendSchema self-wraps in tx for B-I6 atomicity

When ExtendSchema runs at the interval boundary and the fold call
inside the savepoint path fails (e.g., applyFunc rejects), the whole
operation — delta row + savepoint attempt — must roll back together.
Self-wrap in a pgx transaction when no ambient tx is active; reuse
the ambient tx otherwise. B-I6 tightening test asserts no delta or
savepoint row survives a rejected extension.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I6, §5.2."
```

---

## Task 15: [plugins/sqlite] Create model_extensions.go — foldLocked

**Rationale:** sqlite currently has no fold logic. B introduces `foldLocked` mirroring postgres's algorithm. The function lives in a new file matching postgres's structure.

**Files:**
- Create: `plugins/sqlite/model_extensions.go`.
- Modify: `plugins/sqlite/model_extensions_test.go` (add fold tests).

- [ ] **Step 15.1: Write the failing test**

In `plugins/sqlite/model_extensions_test.go`, add:

```go
// TestSQLite_foldLocked_NoDeltas_ReturnsBase — sanity check for the
// new fold path. If no extension rows exist, fold returns the base
// schema verbatim (applyFunc not required).
func TestSQLite_foldLocked_NoDeltas_ReturnsBase(t *testing.T) {
	fx := newSQLiteFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))

	got, err := fx.store.foldLocked(fx.ctx, ref, []byte(`{"base":true}`))
	if err != nil {
		t.Fatalf("foldLocked: %v", err)
	}
	if !bytes.Equal(got, []byte(`{"base":true}`)) {
		t.Errorf("foldLocked (no deltas) = %q, want base %q", got, `{"base":true}`)
	}
}

// TestSQLite_foldLocked_MultipleDeltas_AppliesInOrder — fold returns
// the forward-applied accumulation of delta payloads in seq order.
func TestSQLite_foldLocked_MultipleDeltas_AppliesInOrder(t *testing.T) {
	fx := newSQLiteFixture(t)
	fx.factory.SetApplyFunc(setUnionApplyFunc)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})
	// Insert three delta rows directly (bypassing ExtendSchema to isolate the fold test).
	for i, d := range []string{"d01", "d02", "d03"} {
		fx.db.Exec(fx.ctx,
			`INSERT INTO model_schema_extensions (tenant_id, model_name, model_version, seq, kind, payload, tx_id)
			 VALUES (?, ?, ?, ?, 'delta', ?, '')`,
			fx.tenantID, ref.EntityName, ref.ModelVersion, i+1, []byte(d))
	}

	got, err := fx.store.foldLocked(fx.ctx, ref, []byte{})
	if err != nil {
		t.Fatalf("foldLocked: %v", err)
	}
	expected, _ := setUnionApplyFunc([]byte{}, spi.SchemaDelta("d01"))
	expected, _ = setUnionApplyFunc(expected, spi.SchemaDelta("d02"))
	expected, _ = setUnionApplyFunc(expected, spi.SchemaDelta("d03"))
	if !bytes.Equal(got, expected) {
		t.Errorf("foldLocked = %q, want %q", got, expected)
	}
}
```

- [ ] **Step 15.2: Run to verify failure**

```bash
cd plugins/sqlite
go test -run TestSQLite_foldLocked -v
```

Expected: FAIL — `foldLocked` is undefined.

- [ ] **Step 15.3: Create the file**

Create `plugins/sqlite/model_extensions.go`. The algorithm mirrors postgres's `foldLocked`; the dialect differences are minor.

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// foldLocked returns the fully-folded schema for ref. Starts from
// the most-recent savepoint payload (if any), else from the
// caller-supplied baseSchema (models.doc.schema), and applies every
// subsequent delta row in seq order via the injected ApplyFunc.
//
// When no extensions exist, returns baseSchema verbatim. ApplyFunc
// is only required when at least one delta must be applied.
func (s *modelStore) foldLocked(ctx context.Context, ref spi.ModelRef, baseSchema []byte) ([]byte, error) {
	var savepointSeq int64
	var savepointPayload []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT seq, payload FROM model_schema_extensions
		WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&savepointSeq, &savepointPayload)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		savepointSeq = 0
		savepointPayload = nil
	case err != nil:
		return nil, fmt.Errorf("savepoint lookup: %w", err)
	}

	current := savepointPayload
	if current == nil {
		current = baseSchema
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT payload FROM model_schema_extensions
		WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND kind = 'delta' AND seq > ?
		ORDER BY seq ASC`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion, savepointSeq)
	if err != nil {
		return nil, fmt.Errorf("delta scan: %w", err)
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		var deltaBytes []byte
		if err := rows.Scan(&deltaBytes); err != nil {
			return nil, fmt.Errorf("scan delta: %w", err)
		}
		if first {
			if s.applyFunc == nil {
				return nil, fmt.Errorf("model has pending schema deltas but ApplyFunc is not wired on the factory — see cmd/cyoda/main.go")
			}
			first = false
		}
		current, err = s.applyFunc(current, spi.SchemaDelta(deltaBytes))
		if err != nil {
			return nil, fmt.Errorf("apply delta: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delta iteration: %w", err)
	}
	return current, nil
}

func (s *modelStore) lastSavepointSeq(ctx context.Context, ref spi.ModelRef) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `
		SELECT seq FROM model_schema_extensions
		WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&seq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("lastSavepointSeq: %w", err)
	default:
		return seq, nil
	}
}
```

Add `applyFunc` field to the sqlite store if it doesn't already exist, wired via the factory's `WithApplyFunc` option (if the sqlite plugin already has this option per the memory/postgres pattern, reuse; otherwise add).

- [ ] **Step 15.4: Run and verify**

```bash
go test -run TestSQLite_foldLocked -v
go test ./...
```

All PASS.

- [ ] **Step 15.5: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/sqlite/model_extensions.go plugins/sqlite/model_extensions_test.go plugins/sqlite/model_store.go plugins/sqlite/store_factory.go
git commit -m "feat(sqlite): add foldLocked + lastSavepointSeq (B-I1 infrastructure)

Mirrors postgres's algorithm adapted for sqlite dialect. Starts
from the most-recent savepoint or the caller-supplied base schema
and forward-applies deltas in seq order via the injected ApplyFunc.

Not yet consumed by ExtendSchema — Task 16 rewrites ExtendSchema
to append-to-log and calls this on the Get path.

Refs data-ingestion-qa-subproject-b-design.md §5.3."
```

---

## Task 16: [plugins/sqlite] Rewrite ExtendSchema to append-to-log

**Files:**
- Modify: `plugins/sqlite/model_store.go` (the `ExtendSchema` method).
- Modify: `plugins/sqlite/model_extensions_test.go` (adapt existing tests for log semantics).

- [ ] **Step 16.1: Rewrite existing tests for log semantics**

The existing tests in `plugins/sqlite/model_extensions_test.go` were written for apply-in-place. Rewrite each to expect log-based behavior:

- `TestSQLite_ExtendSchema_AppliesInPlace` → `TestSQLite_ExtendSchema_AppendsToLog` — assert a delta row exists after ExtendSchema, and `Get` returns the folded result.
- `TestSQLite_ExtendSchema_MultiDeltaFold` — assert three deltas all yield log rows and fold produces the expected set-union.
- `TestSQLite_ExtendSchema_CrossTenantIsolation` — unchanged semantically but mechanics shift from "schema column" to "log rows".
- `TestSQLite_ExtendSchema_EmptyDeltaIsNoop` — unchanged.
- `TestSQLite_ExtendSchema_MissingApplyFunc_Errors` — unchanged but the error now surfaces on `Get` (when fold is attempted), not on `ExtendSchema`.
- `TestSQLite_ExtendSchema_ModelNotFound` — semantic equivalent: if the model does not exist, ExtendSchema fails fast.

Since this is a large rewrite, update the tests first so the failing signals are meaningful.

- [ ] **Step 16.2: Run the rewritten tests to verify failure**

```bash
cd plugins/sqlite
go test -run TestSQLite_ExtendSchema -v
```

Expected: FAIL — apply-in-place code still produces old behavior.

- [ ] **Step 16.3: Rewrite ExtendSchema**

Replace the body of `ExtendSchema` in `plugins/sqlite/model_store.go:238-285`:

```go
func (s *modelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if len(delta) == 0 {
		return nil
	}
	if s.applyFunc == nil {
		// Not strictly required at extend time — deltas can be stored
		// without an applyFunc; fold-on-read is where it's needed. But
		// a missing applyFunc at extend time signals a misconfigured
		// factory and fails fast matches the existing memory/postgres
		// behavior.
		return fmt.Errorf("ExtendSchema: ApplyFunc is not wired on the factory")
	}

	// Retry loop for SQLITE_BUSY — Task 19 adds this; for now, a
	// single attempt.
	return s.extendSchemaAttempt(ctx, ref, delta)
}

func (s *modelStore) extendSchemaAttempt(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return classifyError(fmt.Errorf("begin tx: %w", err))
	}
	defer tx.Rollback()

	// Verify the model exists and capture base schema.
	base, err := s.baseSchemaInTx(ctx, tx, ref)
	if err != nil {
		return err
	}

	// Determine next seq.
	var nextSeq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM model_schema_extensions
		 WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&nextSeq); err != nil {
		return classifyError(fmt.Errorf("next seq lookup: %w", err))
	}

	txID := ""
	if sptx := spi.GetTransaction(ctx); sptx != nil {
		txID = sptx.ID
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO model_schema_extensions
		    (tenant_id, model_name, model_version, seq, kind, payload, tx_id, created_at)
		 VALUES (?, ?, ?, ?, 'delta', ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion, nextSeq, []byte(delta), txID); err != nil {
		return classifyError(fmt.Errorf("append delta: %w", err))
	}

	// Savepoint trigger — Task 17 completes this. For Task 16, no savepoint.
	_ = base // used by savepoint path added in Task 17

	if err := tx.Commit(); err != nil {
		return classifyError(fmt.Errorf("commit: %w", err))
	}
	return nil
}

// baseSchemaInTx reads models.doc.schema inside the given tx.
func (s *modelStore) baseSchemaInTx(ctx context.Context, tx *sql.Tx, ref spi.ModelRef) ([]byte, error) {
	var doc []byte
	err := tx.QueryRowContext(ctx,
		`SELECT doc FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("model %s not found: %w", ref, spi.ErrNotFound)
	}
	if err != nil {
		return nil, classifyError(fmt.Errorf("base schema lookup: %w", err))
	}
	// Extract the .schema field from the JSON doc, matching the
	// convention used on read.
	return extractSchemaFromDoc(doc)
}
```

Adapt to sqlite's `database/sql` API and the plugin's existing patterns. If `baseSchemaInTx` or `extractSchemaFromDoc` don't exist, create them with the simplest implementation that honors the model's existing `doc` column shape.

Also: **remove** the apply-in-place code that previously updated `models.doc`. The schema is now the fold of log rows on top of the base; ExtendSchema does not rewrite `models.doc`.

Update the `Get` method to call `foldLocked` (per Task 15):

```go
func (s *modelStore) Get(ctx context.Context, ref spi.ModelRef) (*spi.ModelDescriptor, error) {
	// ... existing code to fetch models row ...
	folded, err := s.foldLocked(ctx, ref, baseSchemaBytes)
	if err != nil {
		return nil, err
	}
	desc.Schema = folded
	return desc, nil
}
```

- [ ] **Step 16.4: Run rewritten tests, confirm pass**

```bash
go test -run TestSQLite_ExtendSchema -v
go test ./...
```

Expected: all PASS including the rewritten tests. Existing non-schema tests stay green.

- [ ] **Step 16.5: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/sqlite/model_store.go plugins/sqlite/model_extensions.go plugins/sqlite/model_extensions_test.go
git commit -m "feat(sqlite): convert ExtendSchema from apply-in-place to log-based

Populates the previously-unused model_schema_extensions table with
delta rows on each extension. Get fetches the base schema from
models.doc and folds the log via foldLocked. Matches postgres's
algorithm adapted for sqlite dialect + database/sql.

Existing tests rewritten for log semantics. Behavior visible to
callers is unchanged for single-extension cases; multi-extension
cases now go through the canonical fold path.

Task 17 adds savepoint triggering. Task 19 adds SQLITE_BUSY
retry.

Refs data-ingestion-qa-subproject-b-design.md §5.3 points 1-2."
```

---

## Task 17: [plugins/sqlite] Add savepoint triggering (interval + on-lock)

**Files:**
- Modify: `plugins/sqlite/model_store.go`.
- Modify: `plugins/sqlite/model_extensions_test.go`.

- [ ] **Step 17.1: Write failing tests**

Append to `plugins/sqlite/model_extensions_test.go`:

```go
// TestSQLite_ExtendSchema_SavepointAtConfigInterval — B-I4 for sqlite.
func TestSQLite_ExtendSchema_SavepointAtConfigInterval(t *testing.T) {
	fx := newSQLiteFixtureWithInterval(t, 10)
	fx.factory.SetApplyFunc(setUnionApplyFunc)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})

	for i := 0; i < 10; i++ {
		_ = fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d%d", i)))
	}

	seq, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if seq != 10 {
		t.Errorf("savepoint seq after 10 deltas with interval=10 = %d, want 10", seq)
	}
}

// TestSQLite_ExtendSchema_SaveOnLock — B-I3 for sqlite.
func TestSQLite_ExtendSchema_SaveOnLock(t *testing.T) {
	fx := newSQLiteFixture(t)
	fx.factory.SetApplyFunc(setUnionApplyFunc)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))
	_ = fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta("d1"))

	before, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if before != 0 {
		t.Fatalf("pre-lock lastSavepointSeq = %d, want 0", before)
	}
	if err := fx.store.Lock(fx.ctx, ref); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	after, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if after == 0 {
		t.Error("post-lock lastSavepointSeq = 0, want nonzero")
	}
}

// TestSQLite_ExtendSchema_UnlockDoesNotWriteSavepoint — §5.3 asymmetry.
func TestSQLite_ExtendSchema_UnlockDoesNotWriteSavepoint(t *testing.T) {
	fx := newSQLiteFixture(t)
	fx.factory.SetApplyFunc(setUnionApplyFunc)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte(`{"base":true}`))
	_ = fx.store.Lock(fx.ctx, ref)
	beforeUnlock, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if err := fx.store.Unlock(fx.ctx, ref); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	afterUnlock, _ := fx.store.lastSavepointSeq(fx.ctx, ref)
	if beforeUnlock != afterUnlock {
		t.Errorf("Unlock mutated lastSavepointSeq: before=%d after=%d", beforeUnlock, afterUnlock)
	}
}
```

- [ ] **Step 17.2: Run, verify failures**

```bash
cd plugins/sqlite
go test -run "TestSQLite_ExtendSchema_(SavepointAtConfigInterval|SaveOnLock|UnlockDoesNotWriteSavepoint)" -v
```

Expected: all three FAIL.

- [ ] **Step 17.3: Implement savepoint trigger in extendSchemaAttempt**

Add after the DELETE row INSERT in `extendSchemaAttempt`:

```go
	// Savepoint trigger.
	lastSP, err := s.lastSavepointSeqInTx(ctx, tx, ref)
	if err != nil {
		return classifyError(err)
	}
	if nextSeq-lastSP >= int64(s.cfg.SchemaSavepointInterval) {
		folded, err := s.foldLockedInTx(ctx, tx, ref, base)
		if err != nil {
			return classifyError(err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO model_schema_extensions
			    (tenant_id, model_name, model_version, seq, kind, payload, tx_id, created_at)
			 VALUES (?, ?, ?, ?, 'savepoint', ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
			string(s.tenantID), ref.EntityName, ref.ModelVersion, nextSeq+1, folded, txID); err != nil {
			return classifyError(fmt.Errorf("append savepoint: %w", err))
		}
	}
```

**Note:** The savepoint row uses `nextSeq+1`, reserving one sequence step for the savepoint itself (so it clusters after the delta). Alternative: use `nextSeq` with `kind='savepoint'` as a *separate* row that shares the seq; the PK on `(tenant, name, version, seq)` forbids this, so the `+1` approach is correct.

Add tx-aware variants of `lastSavepointSeq` and `foldLocked` (`lastSavepointSeqInTx`, `foldLockedInTx`) that take `tx *sql.Tx` instead of using `s.db`. Copy the bodies of the existing non-tx helpers and replace `s.db` calls with `tx` calls.

Implement save-on-lock in the `Lock` method, similar to postgres's Task 10. Lock opens its own `tx`, executes the state change, computes the fold, and inserts the savepoint. Use the de-dup rule: when Lock fires at the exact interval boundary, skip the interval check (lock savepoint supersedes).

- [ ] **Step 17.4: Run tests, verify pass**

```bash
go test -run "TestSQLite_ExtendSchema" -v
go test ./...
```

All PASS.

- [ ] **Step 17.5: Commit**

```bash
git add plugins/sqlite/model_store.go plugins/sqlite/model_extensions.go plugins/sqlite/model_extensions_test.go
git commit -m "feat(sqlite): savepoint triggering (B-I3, B-I4) with unlock asymmetry

- Savepoint on size threshold: when (nextSeq - lastSavepointSeq) >=
  cfg.SchemaSavepointInterval, the same tx inserts a savepoint row
  at nextSeq+1 with the folded schema.
- Save-on-lock: Lock writes a savepoint atomically with the state
  change in a single sqlite transaction. De-dup: when Lock coincides
  with the interval threshold, only the lock savepoint is written.
- Unlock deliberately does NOT write a savepoint (§5.3 asymmetry).

Refs data-ingestion-qa-subproject-b-design.md §3 B-I3 B-I4, §5.3."
```

---

## Task 18: [plugins/sqlite] Upgrade-path test — pre-B sqlite file

**Files:**
- Modify: `plugins/sqlite/model_extensions_test.go`.

- [ ] **Step 18.1: Write the test**

```go
// TestSQLite_ExtendSchema_UpgradeFromPreBDeployment — asserts that
// an existing sqlite database with populated models.doc.schema and
// an empty model_schema_extensions table opens cleanly under the
// new log-based plugin. The first Get returns the base schema
// verbatim (zero deltas ⇒ identity fold).
func TestSQLite_ExtendSchema_UpgradeFromPreBDeployment(t *testing.T) {
	fx := newSQLiteFixture(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	// Simulate pre-B state: insert model directly, do NOT populate
	// the extension log, and do NOT call ExtendSchema.
	baseSchema := `{"type":"object","pre_b":true}`
	fx.db.ExecContext(fx.ctx,
		`INSERT INTO models (tenant_id, model_name, model_version, doc, state, change_level)
		 VALUES (?, ?, ?, json_object('schema', json(?)), 'DRAFT', '')`,
		fx.tenantID, ref.EntityName, ref.ModelVersion, baseSchema)

	// Confirm the extension log is empty.
	var count int
	fx.db.QueryRowContext(fx.ctx,
		`SELECT COUNT(*) FROM model_schema_extensions
		 WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		fx.tenantID, ref.EntityName, ref.ModelVersion).Scan(&count)
	if count != 0 {
		t.Fatalf("pre-B fixture has %d extension rows, want 0", count)
	}

	// Get returns the base schema verbatim.
	desc, err := fx.store.Get(fx.ctx, ref)
	if err != nil {
		t.Fatalf("Get on pre-B model: %v", err)
	}
	if !bytes.Equal(desc.Schema, []byte(baseSchema)) {
		t.Errorf("pre-B Get returned %q, want verbatim %q", desc.Schema, baseSchema)
	}
}
```

Adapt the INSERT column/expression to the real `models` table schema (field names, how `doc.schema` is packed).

- [ ] **Step 18.2: Run and verify**

```bash
cd plugins/sqlite
go test -run TestSQLite_ExtendSchema_UpgradeFromPreBDeployment -v
```

Expected: PASS — `foldLocked` with zero deltas returns the base.

- [ ] **Step 18.3: Commit**

```bash
git add plugins/sqlite/model_extensions_test.go
git commit -m "test(sqlite): upgrade-path from pre-B deployment

An existing sqlite file with populated models.doc.schema and empty
model_schema_extensions table must open cleanly under the log-based
plugin: zero deltas ⇒ foldLocked returns the base verbatim. No data
migration needed.

Refs data-ingestion-qa-subproject-b-design.md §5.3 point 5."
```

---

## Task 19: [plugins/sqlite] Add SQLITE_BUSY transparent retry

**Files:**
- Modify: `plugins/sqlite/model_store.go`.
- Modify: `plugins/sqlite/model_extensions_test.go`.

- [ ] **Step 19.1: Write failing tests**

```go
// TestSQLite_ExtendSchema_TransparentRetry_ConvergesUnderBusy — B-I7
// for sqlite. N goroutines extending concurrently all commit within
// the retry budget.
func TestSQLite_ExtendSchema_TransparentRetry_ConvergesUnderBusy(t *testing.T) {
	const N = 8
	fx := newSQLiteFixture(t)
	fx.factory.SetApplyFunc(setUnionApplyFunc)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})

	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d%d", i)))
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("ExtendSchema #%d failed: %v", i, err)
		}
	}

	// All N deltas landed.
	var count int
	fx.db.QueryRowContext(fx.ctx,
		`SELECT COUNT(*) FROM model_schema_extensions WHERE kind='delta'`).Scan(&count)
	if count != N {
		t.Errorf("delta row count = %d, want %d", count, N)
	}
}

// TestSQLite_ExtendSchema_ContextCancellation_ReturnsCtxErr — §4.2.
func TestSQLite_ExtendSchema_ContextCancellation_ReturnsCtxErr(t *testing.T) {
	fx := newSQLiteFixture(t)
	fx.factory.SetApplyFunc(setUnionApplyFunc)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})

	ctx, cancel := context.WithCancel(fx.ctx)
	cancel()

	err := fx.store.ExtendSchema(ctx, ref, spi.SchemaDelta("d1"))
	if err == nil {
		t.Fatal("ExtendSchema with cancelled ctx must fail")
	}
	if errors.Is(err, spi.ErrRetryExhausted) {
		t.Errorf("cancelled ctx → ErrRetryExhausted (want ctx.Err()); err = %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx → non-context error; err = %v", err)
	}
}
```

- [ ] **Step 19.2: Run to verify failure**

```bash
go test -run "TestSQLite_ExtendSchema_TransparentRetry|TestSQLite_ExtendSchema_ContextCancellation" -v
```

Expected: the `_TransparentRetry_` test may PASS under light concurrency (SQLite's `busy_timeout` already waits); if it fails with `SQLITE_BUSY`, the retry wrapper is needed. The cancellation test FAILs without ctx-aware retry.

- [ ] **Step 19.3: Add retry wrapper around extendSchemaAttempt**

Replace `ExtendSchema`:

```go
func (s *modelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if len(delta) == 0 {
		return nil
	}
	if s.applyFunc == nil {
		return fmt.Errorf("ExtendSchema: ApplyFunc is not wired on the factory")
	}
	var lastErr error
	for attempt := 1; attempt <= s.cfg.SchemaExtendMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("ExtendSchema cancelled after %d attempts: %w", attempt-1, err)
		}
		err := s.extendSchemaAttempt(ctx, ref, delta)
		if err == nil {
			return nil
		}
		if !errors.Is(err, spi.ErrConflict) {
			// Non-retryable error (or ctx-driven). Surface immediately.
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("%w after %d attempts: %v",
		spi.ErrRetryExhausted, s.cfg.SchemaExtendMaxRetries, lastErr)
}
```

Where `classifyError` (already present per the earlier survey at `plugins/sqlite/errors.go`) maps `SQLITE_BUSY` to `spi.ErrConflict`.

- [ ] **Step 19.4: Run tests to verify pass**

```bash
go test -run "TestSQLite_ExtendSchema_TransparentRetry|TestSQLite_ExtendSchema_ContextCancellation" -v -count=3
go test ./...
```

All PASS (3 runs confirms determinism under the retry loop).

- [ ] **Step 19.5: Commit**

```bash
git add plugins/sqlite/model_store.go plugins/sqlite/model_extensions_test.go
git commit -m "feat(sqlite): SQLITE_BUSY transparent retry (B-I7) + ctx cancellation

Wrap extendSchemaAttempt in a retry loop up to cfg.SchemaExtendMaxRetries
(default 8). SQLITE_BUSY classifies as spi.ErrConflict (already via
classifyError). Ctx cancellation between attempts returns ctx.Err()
wrapped with attempt count, not ErrRetryExhausted. Exhaustion without
cancellation returns ErrRetryExhausted wrapping the last conflict.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I7, §4.2, §5.3."
```

---

## Task 20: [plugins/sqlite] Remaining per-plugin B tests

**Files:**
- Modify: `plugins/sqlite/model_extensions_test.go`.

Add the remaining tests that parallel postgres's suite:

- [ ] **Step 20.1: Write and run the tests**

```go
// TestSQLite_ExtendSchema_RejectionLeavesNoPersistedTrace — B-I6.
func TestSQLite_ExtendSchema_RejectionLeavesNoPersistedTrace(t *testing.T) {
	fx := newSQLiteFixture(t)
	fx.factory.SetApplyFunc(func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		if bytes.Contains([]byte(delta), []byte("RESTRICT")) {
			return nil, fmt.Errorf("simulated rejection")
		}
		return setUnionApplyFunc(base, delta)
	})
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	fx.SaveModel(t, ref, []byte{})

	// Warm the log to the interval boundary.
	fx.reopenWithInterval(t, 10)
	for i := 0; i < 9; i++ {
		_ = fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta(fmt.Sprintf("d%d", i)))
	}

	var preCount int
	fx.db.QueryRowContext(fx.ctx, `SELECT COUNT(*) FROM model_schema_extensions`).Scan(&preCount)

	// The 10th delta triggers the savepoint fold; the RESTRICT delta rejects.
	err := fx.store.ExtendSchema(fx.ctx, ref, spi.SchemaDelta("RESTRICT-d9"))
	if err == nil {
		t.Fatal("ExtendSchema with rejecting applyFunc must fail")
	}

	var postCount int
	fx.db.QueryRowContext(fx.ctx, `SELECT COUNT(*) FROM model_schema_extensions`).Scan(&postCount)
	if postCount != preCount {
		t.Errorf("rejected extension added rows: before=%d after=%d", preCount, postCount)
	}
}

// TestSQLite_ExtendSchema_FoldAcrossSavepointBoundary_ByteIdentical — B-I2 local.
func TestSQLite_ExtendSchema_FoldAcrossSavepointBoundary_ByteIdentical(t *testing.T) {
	// Same pattern as postgres Task 13 — adapt to sqlite.
	a := newSQLiteFixtureWithInterval(t, 10)
	a.factory.SetApplyFunc(setUnionApplyFunc)
	refA := spi.ModelRef{EntityName: "A", ModelVersion: "1"}
	a.SaveModel(t, refA, []byte{})
	for i := 0; i < 30; i++ {
		_ = a.store.ExtendSchema(a.ctx, refA, spi.SchemaDelta(fmt.Sprintf("d%03d", i)))
	}
	got, _ := a.store.Get(a.ctx, refA)

	b := newSQLiteFixtureWithInterval(t, 1_000_000)
	b.factory.SetApplyFunc(setUnionApplyFunc)
	refB := spi.ModelRef{EntityName: "B", ModelVersion: "1"}
	b.SaveModel(t, refB, []byte{})
	for i := 0; i < 30; i++ {
		_ = b.store.ExtendSchema(b.ctx, refB, spi.SchemaDelta(fmt.Sprintf("d%03d", i)))
	}
	want, _ := b.store.Get(b.ctx, refB)

	gotSorted := sortNewlineTokens(got.Schema)
	wantSorted := sortNewlineTokens(want.Schema)
	if !bytes.Equal(gotSorted, wantSorted) {
		t.Errorf("fold with savepoints != fold without\n  got:  %q\n  want: %q", gotSorted, wantSorted)
	}
}
```

```bash
cd plugins/sqlite
go test -run "TestSQLite_ExtendSchema_(RejectionLeaves|FoldAcrossSavepoint)" -v
go test ./...
```

All PASS.

- [ ] **Step 20.2: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
git add plugins/sqlite/model_extensions_test.go
git commit -m "test(sqlite): B-I2 B-I6 sqlite-local coverage

- RejectionLeavesNoPersistedTrace asserts B-I6 tightening.
- FoldAcrossSavepointBoundary_ByteIdentical asserts B-I2 via
  savepointed-vs-unsavepointed fold equivalence.

Completes sqlite-local B-I1..B-I8 coverage alongside earlier tasks.

Refs data-ingestion-qa-subproject-b-design.md §3, §5.3."
```

---

## Task 21: [internal/cluster/modelcache] B-I8 verification test for ExtendSchema path

**Files:**
- Modify: `internal/cluster/modelcache/cache_test.go`.

- [ ] **Step 21.1: Write the test**

Append to `cache_test.go`:

```go
// TestCachingModelStore_ExtendSchema_InvalidatesCache — B-I8. After a
// successful ExtendSchema via the CachingModelStore decorator, a
// subsequent Get must NOT return the pre-extension cached result.
func TestCachingModelStore_ExtendSchema_InvalidatesCache(t *testing.T) {
	inner := &fakeModelStore{
		get: func(ref spi.ModelRef) (*spi.ModelDescriptor, error) {
			return &spi.ModelDescriptor{Ref: ref, Schema: []byte(`v1`), State: spi.ModelLocked}, nil
		},
	}
	broadcaster := &noopBroadcaster{}
	clock := fakeClock{now: time.Now()}
	cache := modelcache.New(inner, broadcaster, clock, 1*time.Minute)

	ctx := withTenant(t.Context(), "t1")
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}

	// Warm the cache.
	desc1, _ := cache.Get(ctx, ref)
	if string(desc1.Schema) != "v1" {
		t.Fatalf("warm Get = %q, want v1", desc1.Schema)
	}

	// Flip the inner-store return value to simulate "extension changed the schema".
	inner.get = func(ref spi.ModelRef) (*spi.ModelDescriptor, error) {
		return &spi.ModelDescriptor{Ref: ref, Schema: []byte(`v2-extended`), State: spi.ModelLocked}, nil
	}

	// Call ExtendSchema.
	if err := cache.ExtendSchema(ctx, ref, spi.SchemaDelta(`d1`)); err != nil {
		t.Fatalf("ExtendSchema: %v", err)
	}

	// The NEXT Get must see v2, not the cached v1.
	desc2, _ := cache.Get(ctx, ref)
	if string(desc2.Schema) != "v2-extended" {
		t.Errorf("post-extend Get returned stale cached schema: got %q, want v2-extended", desc2.Schema)
	}
}
```

Use the existing fake / mock infrastructure in `cache_test.go` (there's likely a `fakeModelStore` or `mockModelStore` pattern already).

- [ ] **Step 21.2: Run and verify**

```bash
go test ./internal/cluster/modelcache/ -run TestCachingModelStore_ExtendSchema_InvalidatesCache -v
```

Expected: PASS — existing cache code at `cache.go:178-184` already calls `invalidate` on successful `ExtendSchema`.

- [ ] **Step 21.3: Commit**

```bash
git add internal/cluster/modelcache/cache_test.go
git commit -m "test(modelcache): B-I8 — ExtendSchema invalidates local cache

Asserts CachingModelStore.ExtendSchema invalidates the cache entry
for ref after a successful inner call. The existing implementation
at cache.go:178-184 already honors this; the test promotes the
implicit assurance to an executable contract and guards against
regression under B's new savepoint/retry paths.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I8."
```

---

## Task 22: [e2e/parity] Add SchemaExtensionCrossBackendByteIdentity

**Rationale:** Parity tests drive real backends via the HTTP layer. This test seeds a deterministic extension sequence, asserts the `Get` response's `schema.Marshal` bytes are byte-identical across memory/postgres/sqlite.

**Files:**
- Create: `e2e/parity/schema_extension_byte_identity.go`.
- Modify: `e2e/parity/registry.go` (add entry).

- [ ] **Step 22.1: Write the test function**

Create `e2e/parity/schema_extension_byte_identity.go`:

```go
package parity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// RunSchemaExtensionCrossBackendByteIdentity drives a deterministic
// 20-extension sequence through the HTTP layer and asserts the
// returned schema bytes match a precomputed canonical form.
// Asserts B-I1 at the observable HTTP boundary.
func RunSchemaExtensionCrossBackendByteIdentity(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "b1-byte-identity"
	const modelVersion = 1

	sampleDoc := `{"field_0":"","field_1":"","field_2":"","field_3":"","field_4":"","field_5":"","field_6":"","field_7":"","field_8":"","field_9":"","field_10":"","field_11":"","field_12":"","field_13":"","field_14":"","field_15":"","field_16":"","field_17":"","field_18":"","field_19":""}`
	if err := c.ImportModel(t, modelName, modelVersion, sampleDoc); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	if err := c.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	if err := c.ImportWorkflow(t, modelName, modelVersion, simpleWorkflowJSON); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}

	// Submit 20 entity creates, each introducing a new field. This
	// drives ExtendSchema under the hood once per new field.
	for i := 0; i < 20; i++ {
		body := map[string]string{}
		for j := 0; j <= i; j++ {
			body[fmt.Sprintf("field_%d", j)] = fmt.Sprintf("v%d", j)
		}
		raw, _ := json.Marshal(body)
		if _, err := c.CreateEntity(t, modelName, modelVersion, string(raw)); err != nil {
			t.Fatalf("CreateEntity #%d: %v", i, err)
		}
	}

	// Retrieve the folded schema.
	schema, err := c.GetModelSchema(t, modelName, modelVersion)
	if err != nil {
		t.Fatalf("GetModelSchema: %v", err)
	}

	// Canonical expected bytes — computed by a deterministic pure
	// function over the same input sequence. The oracle function
	// lives in the in-memory schema package.
	expected := expectedSchemaFromSequence(t, 20)

	if !bytes.Equal(schema, expected) {
		t.Errorf("cross-backend byte identity failed for %s\n  got:      %q\n  expected: %q", fixture.Name(), schema, expected)
	}
}
```

Create or extend `e2e/parity/oracle.go` (new file) with the deterministic oracle helpers. One shared helper drives every B-parity test; subsequent tasks reuse it.

```go
// Package parity — oracle.go
//
// Deterministic in-memory oracle used by B parity tests. Each helper
// produces the schema bytes that a byte-identical fold MUST return
// for the named input sequence, computed via importer.Walk +
// schema.Extend + schema.Marshal. Backends matching these bytes
// satisfy B-I1.
package parity

import (
	"encoding/json"
	"fmt"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// expectedFoldFromBodies computes the expected folded schema bytes
// for a sequence of JSON bodies applied sequentially at
// ChangeLevelStructural. The first body seeds the schema; each
// subsequent body is Walk + Extend.
func expectedFoldFromBodies(t *testing.T, bodies []map[string]any) []byte {
	t.Helper()
	var current *schema.ModelNode
	for i, body := range bodies {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("expectedFoldFromBodies: marshal body %d: %v", i, err)
		}
		walked, err := importer.Walk(raw)
		if err != nil {
			t.Fatalf("expectedFoldFromBodies: Walk body %d: %v", i, err)
		}
		if current == nil {
			current = walked
			continue
		}
		next, err := schema.Extend(current, walked, spi.ChangeLevelStructural)
		if err != nil {
			t.Fatalf("expectedFoldFromBodies: Extend body %d: %v", i, err)
		}
		current = next
	}
	if current == nil {
		return nil
	}
	out, err := schema.Marshal(current)
	if err != nil {
		t.Fatalf("expectedFoldFromBodies: Marshal: %v", err)
	}
	return out
}

// expectedSchemaFromSequence computes the expected folded schema for
// the 20-field-widening sequence used by
// RunSchemaExtensionCrossBackendByteIdentity.
func expectedSchemaFromSequence(t *testing.T, n int) []byte {
	t.Helper()
	bodies := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		body := map[string]any{}
		for j := 0; j <= i; j++ {
			body[fmt.Sprintf("field_%d", j)] = fmt.Sprintf("v%d", j)
		}
		bodies = append(bodies, body)
	}
	return expectedFoldFromBodies(t, bodies)
}
```

Verify the concrete signatures match the in-process schema package:
- `schema.Apply(base *ModelNode, delta spi.SchemaDelta) (*ModelNode, error)`
- `schema.Marshal(n *ModelNode) ([]byte, error)`
- `schema.Extend(existing, incoming *ModelNode, level spi.ChangeLevel) (*ModelNode, error)`
- `importer.Walk(data any) (*schema.ModelNode, error)` — accepts an `any` (typically `[]byte` or a parsed JSON structure).

Adjust if your local tree's signatures differ. Check `internal/domain/model/schema/extend.go:34` and `internal/domain/model/importer/walker.go:20` for the authoritative forms.

Add `GetModelSchema` method to `e2e/parity/client/client.go` if it doesn't exist. Return the raw schema bytes as the HTTP layer produces them:

```go
// GetModelSchema fetches the folded schema bytes for (modelName,
// modelVersion). Returns the raw bytes suitable for byte-identical
// comparison across backends.
func (c *Client) GetModelSchema(t *testing.T, modelName string, modelVersion int) ([]byte, error) {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/models/%s/%d/schema", c.BaseURL, modelName, modelVersion)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("NewRequest: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetModelSchema: status=%d body=%s", resp.StatusCode, body)
	}
	return io.ReadAll(resp.Body)
}
```

Adapt the URL to the real endpoint path (check `internal/api/handlers/` for the actual route). If the endpoint returns a wrapped JSON envelope (e.g., `{"schema": ...}`), unwrap it and return the inner `schema` bytes so the comparison is of the raw fold output, not of the envelope.

- [ ] **Step 22.2: Register in e2e/parity/registry.go**

Add to the `allTests` slice (after the existing `SchemaExtensionsSequentialFoldAcrossRequests` entry):

```go
	// Sub-project B — cross-backend byte identity
	{"SchemaExtensionCrossBackendByteIdentity", RunSchemaExtensionCrossBackendByteIdentity},
```

- [ ] **Step 22.3: Run parity tests against each backend**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
go test ./e2e/parity/memory/... -run Parity/SchemaExtensionCrossBackendByteIdentity -v
go test ./e2e/parity/postgres/... -run Parity/SchemaExtensionCrossBackendByteIdentity -v
go test ./e2e/parity/sqlite/... -run Parity/SchemaExtensionCrossBackendByteIdentity -v
```

Expected: PASS on all three.

- [ ] **Step 22.4: Commit**

```bash
git add e2e/parity/schema_extension_byte_identity.go e2e/parity/registry.go e2e/parity/client/client.go
git commit -m "test(parity): B-I1 — cross-backend byte identity via 20-field-widening sequence

New parity entry RunSchemaExtensionCrossBackendByteIdentity drives a
deterministic schema-widening sequence through the HTTP layer on
each backend fixture and asserts the returned schema bytes match a
canonical expected computed via the in-memory schema package as
oracle. Inherited by Cassandra via go.mod refresh.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I1, §7.1."
```

---

## Task 23: [e2e/parity] SchemaExtensionAtomicRejection (B-I6)

**Files:**
- Create: `e2e/parity/schema_extension_atomic_rejection.go`.
- Modify: `e2e/parity/registry.go`.

- [ ] **Step 23.1: Write the test**

```go
package parity

import (
	"encoding/json"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// RunSchemaExtensionAtomicRejection asserts B-I6: when a
// ChangeLevel-violating extension is attempted, no partial state
// is persisted on the backend. The assertion is by HTTP round-trip:
// fetch the schema before and after, verify byte-identical.
func RunSchemaExtensionAtomicRejection(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "b1-atomic-reject"
	const modelVersion = 1
	sampleDoc := `{"x":42}`
	if err := c.ImportModel(t, modelName, modelVersion, sampleDoc); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	if err := c.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	if err := c.SetChangeLevel(t, modelName, modelVersion, "ArrayLength"); err != nil {
		t.Fatalf("SetChangeLevel: %v", err)
	}
	if err := c.ImportWorkflow(t, modelName, modelVersion, simpleWorkflowJSON); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}

	// Capture the pre-extension schema bytes.
	preSchema, _ := c.GetModelSchema(t, modelName, modelVersion)

	// Submit an entity that forces Structural schema change — rejected
	// by the ChangeLevel=ArrayLength gate.
	structuralBody, _ := json.Marshal(map[string]any{"x": 42, "newField": "appear"})
	_, err := c.CreateEntity(t, modelName, modelVersion, string(structuralBody))
	if err == nil {
		t.Fatal("CreateEntity with structural shape-change under ChangeLevel=ArrayLength must fail")
	}

	// Post-extension schema: byte-identical to pre-extension.
	postSchema, _ := c.GetModelSchema(t, modelName, modelVersion)
	if !bytes.Equal(preSchema, postSchema) {
		t.Errorf("rejected extension mutated schema\n  pre:  %q\n  post: %q", preSchema, postSchema)
	}
}
```

Add `SetChangeLevel` to `client.go` if missing.

- [ ] **Step 23.2: Register in registry.go**

```go
	{"SchemaExtensionAtomicRejection", RunSchemaExtensionAtomicRejection},
```

- [ ] **Step 23.3: Run tests**

```bash
go test ./e2e/parity/memory/... -run Parity/SchemaExtensionAtomicRejection -v
go test ./e2e/parity/postgres/... -run Parity/SchemaExtensionAtomicRejection -v
go test ./e2e/parity/sqlite/... -run Parity/SchemaExtensionAtomicRejection -v
```

All PASS.

- [ ] **Step 23.4: Commit**

```bash
git add e2e/parity/schema_extension_atomic_rejection.go e2e/parity/registry.go e2e/parity/client/client.go
git commit -m "test(parity): B-I6 — atomic rejection via ChangeLevel gate

Drive a structural shape-change through the HTTP layer against a
model locked at ChangeLevel=ArrayLength. Assert the request fails
and the schema bytes are byte-identical pre- and post-call.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I6, §7.1."
```

---

## Task 24: [e2e/parity] SchemaExtensionConcurrentConvergence (B-I7)

**Files:**
- Create: `e2e/parity/schema_extension_concurrent_convergence.go`.
- Modify: `e2e/parity/registry.go`.

- [ ] **Step 24.1: Write the test**

```go
// RunSchemaExtensionConcurrentConvergence asserts B-I7: 10
// concurrent extensions on the same model all succeed and the
// final fold is byte-identical to any serial ordering of the
// same deltas.
func RunSchemaExtensionConcurrentConvergence(t *testing.T, fixture BackendFixture) {
	const N = 10
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "b1-concurrent"
	const modelVersion = 1
	sampleDoc := `{}`
	// Seed a model that accepts structural changes.
	for i := 0; i <= N; i++ {
		sampleDoc = fmt.Sprintf(`{%s}`, strings.TrimPrefix(strings.TrimSuffix(
			fmt.Sprintf(`{%s,"field_%d":""}`, strings.TrimPrefix(strings.TrimSuffix(sampleDoc, "}"), "{"), i), "{"), "}"))
	}
	if err := c.ImportModel(t, modelName, modelVersion, sampleDoc); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	if err := c.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	if err := c.ImportWorkflow(t, modelName, modelVersion, simpleWorkflowJSON); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{fmt.Sprintf("field_%d", i): "v"})
			if _, err := c.CreateEntity(t, modelName, modelVersion, string(body)); err != nil {
				t.Errorf("goroutine %d CreateEntity: %v", i, err)
			}
		}()
	}
	wg.Wait()

	got, _ := c.GetModelSchema(t, modelName, modelVersion)

	// Expected: same schema as serial replay of the N deltas via oracle.
	bodies := make([]map[string]any, 0, N+1)
	// Include the sampleDoc as body 0 so the oracle sees the same initial schema.
	var seed map[string]any
	_ = json.Unmarshal([]byte(sampleDoc), &seed)
	bodies = append(bodies, seed)
	for i := 0; i < N; i++ {
		bodies = append(bodies, map[string]any{fmt.Sprintf("field_%d", i): "v"})
	}
	expected := expectedFoldFromBodies(t, bodies)
	if !bytes.Equal(got, expected) {
		t.Errorf("concurrent-fold != serial-replay-fold\n  got:      %q\n  expected: %q", got, expected)
	}
}
```

- [ ] **Step 24.2: Register + run + commit**

```go
	{"SchemaExtensionConcurrentConvergence", RunSchemaExtensionConcurrentConvergence},
```

```bash
go test ./e2e/parity/{memory,postgres,sqlite}/... -run Parity/SchemaExtensionConcurrentConvergence -v
git add e2e/parity/schema_extension_concurrent_convergence.go e2e/parity/registry.go
git commit -m "test(parity): B-I7 — concurrent-extension convergence

10 goroutines creating entities with disjoint new fields concurrently.
Final schema fold byte-identical to a serial replay of the same
deltas via the in-memory oracle.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I7, §7.1."
```

---

## Task 25: [e2e/parity] SchemaExtensionSavepointOnLockFoldEquivalence (B-I2, B-I3)

**Files:**
- Create: `e2e/parity/schema_extension_save_on_lock.go`.
- Modify: `e2e/parity/registry.go`.

- [ ] **Step 25.1: Write the test**

```go
// RunSchemaExtensionSavepointOnLockFoldEquivalence asserts B-I2 and
// B-I3: a lock-triggered savepoint does not change the observable
// fold (B-I2 transparency), and its existence is not required for
// correct reads (B-I1 byte identity holds across backends regardless
// of savepoint-on-lock presence).
//
// Cross-backend byte identity is the deciding test; per-backend
// inspection of savepoint rows lives in each plugin's gray-box tests.
func RunSchemaExtensionSavepointOnLockFoldEquivalence(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "b1-lock-fold"
	const modelVersion = 1
	sampleDoc := `{"field_0":""}`
	if err := c.ImportModel(t, modelName, modelVersion, sampleDoc); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}

	// Extend in draft (pre-lock).
	body, _ := json.Marshal(map[string]any{"field_0": "pre", "field_1": "pre"})
	if _, err := c.CreateEntity(t, modelName, modelVersion, string(body)); err != nil {
		t.Fatalf("CreateEntity (pre-lock): %v", err)
	}

	// Lock.
	if err := c.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel: %v", err)
	}

	// Extend post-lock (ChangeLevel-compatible).
	body2, _ := json.Marshal(map[string]any{"field_0": "post", "field_1": "post", "field_2": "post"})
	if _, err := c.CreateEntity(t, modelName, modelVersion, string(body2)); err != nil {
		t.Fatalf("CreateEntity (post-lock): %v", err)
	}

	got, _ := c.GetModelSchema(t, modelName, modelVersion)

	// Expected: in-memory oracle over the same body sequence. Does
	// NOT model savepoint-on-lock, but by B-I2 the savepoint is
	// transparent, so the backend's bytes must equal the oracle's.
	var body1, body2 map[string]any
	_ = json.Unmarshal(body, &body1)
	_ = json.Unmarshal(body2Raw, &body2)
	expected := expectedFoldFromBodies(t, []map[string]any{
		{"field_0": ""}, // sampleDoc
		body1,
		body2,
	})
	if !bytes.Equal(got, expected) {
		t.Errorf("fold across lock != serial replay; savepoint-on-lock affected observable bytes\n  got:      %q\n  expected: %q", got, expected)
	}
}
```

Bind `body2Raw := body2` before the CreateEntity call so the oracle can reuse it.

- [ ] **Step 25.2: Register, run, commit**

```go
	{"SchemaExtensionSavepointOnLockFoldEquivalence", RunSchemaExtensionSavepointOnLockFoldEquivalence},
```

```bash
go test ./e2e/parity/{memory,postgres,sqlite}/... -run Parity/SchemaExtensionSavepointOnLockFoldEquivalence -v
git add e2e/parity/schema_extension_save_on_lock.go e2e/parity/registry.go
git commit -m "test(parity): B-I2 B-I3 — savepoint-on-lock transparent to fold

Extend in draft, lock, extend post-lock. Fold bytes across all
backends must equal the in-memory oracle's serial-replay result —
savepoint-on-lock is transparent per B-I2.

Refs data-ingestion-qa-subproject-b-design.md §3 B-I2 B-I3, §7.1."
```

---

## Task 26: [e2e/parity] SchemaExtensionLocalCacheInvalidationOnCommit (B-I8)

**Files:**
- Create: `e2e/parity/schema_extension_cache_invalidation.go`.
- Modify: `e2e/parity/registry.go`.

- [ ] **Step 26.1: Write the test**

```go
// RunSchemaExtensionLocalCacheInvalidationOnCommit asserts B-I8:
// after an extension commits, the next Get on the same node
// returns post-extension state (not stale cached state).
func RunSchemaExtensionLocalCacheInvalidationOnCommit(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	const modelName = "b1-cache-inv"
	const modelVersion = 1
	if err := c.ImportModel(t, modelName, modelVersion, `{"field_0":""}`); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	if err := c.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	if err := c.ImportWorkflow(t, modelName, modelVersion, simpleWorkflowJSON); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}

	// Warm the cache.
	schema1, _ := c.GetModelSchema(t, modelName, modelVersion)

	// Submit an extension via entity create.
	body, _ := json.Marshal(map[string]any{"field_0": "", "new_field": "appear"})
	if _, err := c.CreateEntity(t, modelName, modelVersion, string(body)); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	// Immediate Get must reflect the extension.
	schema2, _ := c.GetModelSchema(t, modelName, modelVersion)
	if bytes.Equal(schema1, schema2) {
		t.Errorf("Get returned stale cached schema after extension — cache not invalidated")
	}
}
```

- [ ] **Step 26.2: Register, run, commit**

```go
	{"SchemaExtensionLocalCacheInvalidationOnCommit", RunSchemaExtensionLocalCacheInvalidationOnCommit},
```

```bash
go test ./e2e/parity/{memory,postgres,sqlite}/... -run Parity/SchemaExtensionLocalCacheInvalidationOnCommit -v
git add e2e/parity/schema_extension_cache_invalidation.go e2e/parity/registry.go
git commit -m "test(parity): B-I8 — local cache invalidation on ExtendSchema commit

Warm the cache via Get; submit an entity that extends the schema;
immediately Get again and assert the returned bytes differ (cache
invalidation on the ExtendSchema commit path).

Refs data-ingestion-qa-subproject-b-design.md §3 B-I8, §7.1."
```

---

## Task 27: [e2e/parity] Property-based entry with in-memory oracle

**Files:**
- Create: `e2e/parity/schema_extension_property.go`.
- Modify: `e2e/parity/registry.go`.

- [ ] **Step 27.1: Create the property harness**

```go
package parity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema/gentree"
)

// RunSchemaExtensionByteIdentityProperty drives a seeded extension
// sequence through the fixture backend and asserts the fold bytes
// equal the in-memory oracle's output for the same seed.
//
// Fans out to 50 seeded subtests per invocation.
func RunSchemaExtensionByteIdentityProperty(t *testing.T, fixture BackendFixture) {
	if testing.Short() {
		t.Skip("property-based parity tests require full mode (drop -short)")
	}
	const numSeeds = 50
	for seed := int64(1); seed <= numSeeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed_%02d", seed), func(t *testing.T) {
			runOneSeed(t, fixture, seed)
		})
	}
}

func runOneSeed(t *testing.T, fixture BackendFixture, seed int64) {
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*31+1)))
	gen := gentree.NewGenerator(gentree.DefaultConfig(), rng)

	extensions := make([]any, 0, 8)
	for i := 0; i < 8; i++ {
		extensions = append(extensions, gen.GenValue(rng, 3, 5))
	}

	// Oracle: in-memory schema construction.
	expected := expectedSchemaFromExtensions(t, extensions)

	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)
	modelName := fmt.Sprintf("b1-prop-seed-%d", seed)
	const modelVersion = 1

	// Use the first extension as the sample doc.
	sample, _ := json.Marshal(extensions[0])
	if err := c.ImportModel(t, modelName, modelVersion, string(sample)); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	if err := c.LockModel(t, modelName, modelVersion); err != nil {
		t.Fatalf("LockModel: %v", err)
	}
	if err := c.ImportWorkflow(t, modelName, modelVersion, simpleWorkflowJSON); err != nil {
		t.Fatalf("ImportWorkflow: %v", err)
	}

	for i, ext := range extensions[1:] {
		body, _ := json.Marshal(ext)
		if _, err := c.CreateEntity(t, modelName, modelVersion, string(body)); err != nil {
			t.Logf("CreateEntity seed=%d ext=%d: %v (may be expected if shape incompatible)", seed, i+1, err)
		}
	}

	got, _ := c.GetModelSchema(t, modelName, modelVersion)
	if !bytes.Equal(got, expected) {
		t.Errorf("seed=%d backend=%s\n  got:      %q\n  expected: %q", seed, fixture.Name(), got, expected)
	}
}

func expectedSchemaFromExtensions(t *testing.T, extensions []any) []byte {
	t.Helper()
	var current *schema.ModelNode
	for i, ext := range extensions {
		raw, err := json.Marshal(ext)
		if err != nil {
			t.Fatalf("expectedSchemaFromExtensions: marshal #%d: %v", i, err)
		}
		walked, err := importer.Walk(raw)
		if err != nil {
			t.Fatalf("expectedSchemaFromExtensions: Walk #%d: %v", i, err)
		}
		if current == nil {
			current = walked
			continue
		}
		next, err := schema.Extend(current, walked, spi.ChangeLevelStructural)
		if err != nil {
			// Shape-incompatible extensions may fail in the oracle but
			// the HTTP stack should reject them with the same error.
			// Record as a marker so the assertion below can still match
			// a failure state.
			t.Logf("Extend rejected #%d (expected: %v)", i, err)
			continue
		}
		current = next
	}
	if current == nil {
		return nil
	}
	out, err := schema.Marshal(current)
	if err != nil {
		t.Fatalf("expectedSchemaFromExtensions: Marshal: %v", err)
	}
	return out
}
```

Replace the `panic` stub with the real oracle implementation referencing `internal/domain/model/schema` and `internal/domain/model/importer`.

- [ ] **Step 27.2: Register and run**

```go
	{"SchemaExtensionByteIdentityProperty", RunSchemaExtensionByteIdentityProperty},
```

```bash
go test ./e2e/parity/{memory,postgres,sqlite}/... -run Parity/SchemaExtensionByteIdentityProperty -v
```

Expected: 50 subtests × 3 backends = 150 PASSes per CI run.

- [ ] **Step 27.3: Commit**

```bash
git add e2e/parity/schema_extension_property.go e2e/parity/registry.go
git commit -m "test(parity): property-based B-I1 with in-memory oracle, 50 seeds

RunSchemaExtensionByteIdentityProperty drives 50 seeded extension
sequences through each backend and asserts fold bytes equal the
deterministic in-memory oracle output. Skipped under -short;
enforced under full CI.

Refs data-ingestion-qa-subproject-b-design.md §7.3."
```

---

## Task 28: [e2e/parity] Runtime budget meta-test

**Files:**
- Create: `e2e/parity/schema_extension_property_budget_test.go`.

- [ ] **Step 28.1: Write the meta-test**

```go
package parity_test

import (
	"testing"
	"time"
)

// TestParity_SchemaExtensionProperty_Budget_CI enforces the 120 s
// CI hard-fail ceiling specified in the B spec §7.3 for the
// property-based parity entry.
//
// Runs only under the "parity-budget" build tag — invoked explicitly
// from CI after the main suite.
func TestParity_SchemaExtensionProperty_Budget_CI(t *testing.T) {
	if testing.Short() {
		t.Skip("budget check runs only in full mode")
	}
	start := time.Now()
	// Kick off the same sequence SchemaExtensionByteIdentityProperty
	// runs (N goroutines against each available fixture).
	// Adapt to the fixture-discovery pattern used by e2e/parity.
	t.Run("memory", ...)
	t.Run("postgres", ...)
	t.Run("sqlite", ...)
	elapsed := time.Since(start)
	t.Logf("property-suite wall clock: %v", elapsed)
	if elapsed > 120*time.Second {
		t.Fatalf("property-suite exceeded 120s CI ceiling: took %v", elapsed)
	}
}
```

This meta-test is a sanity check, not primary coverage. It's fine to be fairly minimal. If this conflicts with parity's existing test shape, adapt — the goal is "full-run wall-clock check."

- [ ] **Step 28.2: Run and commit**

```bash
go test ./e2e/parity/ -run TestParity_SchemaExtensionProperty_Budget_CI -v
git add e2e/parity/schema_extension_property_budget_test.go
git commit -m "test(parity): runtime-budget meta-test for property suite (120s CI ceiling)

Meta-test that enforces the §7.3 CI wall-clock ceiling as a
hard-fail. Runs the property entry against each available backend
and verifies total elapsed < 120s.

Refs data-ingestion-qa-subproject-b-design.md §7.3."
```

---

## Task 29: [docs] printHelp, README, and overview §6 updates

**Files:**
- Modify: `cmd/cyoda/main.go` (printHelp).
- Modify: `README.md`.
- Modify: `docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md`.

- [ ] **Step 29.1: Update printHelp**

In `cmd/cyoda/main.go`, locate the `printHelp()` function. Add to the config-reference section:

```go
	fmt.Println("  CYODA_SCHEMA_SAVEPOINT_INTERVAL (default: 64)")
	fmt.Println("      Number of extensions between savepoint rows in the schema-extension")
	fmt.Println("      log. Honored by postgres, sqlite, cassandra plugins. Ignored by memory.")
	fmt.Println("  CYODA_SCHEMA_EXTEND_MAX_RETRIES (default: 8)")
	fmt.Println("      Plugin-layer retry budget for ExtendSchema on backends with a native")
	fmt.Println("      conflict surface. Honored by sqlite (SQLITE_BUSY), cassandra (LWT).")
	fmt.Println("      Ignored by memory and postgres (no schema-write conflict surface).")
```

- [ ] **Step 29.2: Update README.md**

In `README.md`, find the configuration reference section. Add rows to the env-var table:

```markdown
| `CYODA_SCHEMA_SAVEPOINT_INTERVAL` | `64` | Number of extensions between savepoint rows. Honored by: postgres, sqlite, cassandra. Ignored by memory (no log). |
| `CYODA_SCHEMA_EXTEND_MAX_RETRIES` | `8` | Plugin-layer retry budget for ExtendSchema. Honored by: sqlite (SQLITE_BUSY), cassandra (LWT). Ignored by memory, postgres (no conflict surface on schema writes). |
```

- [ ] **Step 29.3: Update overview §6 invariant table**

In `docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md`, replace the two `TBD | B | ...` rows at §6 with:

```markdown
| `schema.Marshal(Load(...))` byte-identical across plugins for identical history | B | B-I1 — Cross-plugin byte-identical fold |
| Savepoint additions do not change the observable fold | B | B-I2 — Savepoint transparency |
| Lock commits lock-state + savepoint atomically or neither | B | B-I3 — Save-on-lock atomicity |
| Savepoint at (newSeq - lastSavepointSeq) >= interval, atomic with the triggering op | B | B-I4 — Save-on-size-threshold atomicity |
| Extensions apply in commit order; fold is deterministic per history | B | B-I5 — Causal-order preservation |
| Rejected `ExtendSchema` leaves no persisted trace across storage | B | B-I6 — Cross-storage atomicity on rejection |
| Concurrent extensions converge to an order-independent final fold | B | B-I7 — Concurrent-extension convergence |
| Local cache invalidation on commit — subsequent Gets see post-extension state | B | B-I8 — Local cache invalidation |
```

- [ ] **Step 29.4: Commit**

```bash
git add cmd/cyoda/main.go README.md docs/superpowers/specs/2026-04-21-data-ingestion-qa-overview.md
git commit -m "docs: env vars in printHelp/README + overview §6 invariant table

- printHelp documents CYODA_SCHEMA_SAVEPOINT_INTERVAL and
  CYODA_SCHEMA_EXTEND_MAX_RETRIES with 'honored by' per plugin.
- README config table gets the same rows.
- Overview §6 replaces the two TBDs with B's eight concrete
  invariants.

Gate 4 (documentation hygiene) for B's config additions.

Refs data-ingestion-qa-subproject-b-design.md §8."
```

---

## Task 30: [plugins/*/go.mod] Bump cyoda-go-spi to v0.6.0

**Files:**
- Modify: `plugins/postgres/go.mod`, `plugins/sqlite/go.mod`, `plugins/memory/go.mod` (if they pin spi).
- Modify: `go.mod` at repo root.

- [ ] **Step 30.1: Bump each go.mod**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence/plugins/postgres
go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0
go mod tidy
cd ../sqlite
go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0
go mod tidy
cd ../memory
go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0 2>/dev/null || true
go mod tidy
cd ../..
go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0
go mod tidy
```

- [ ] **Step 30.2: Verify**

```bash
go build ./...
go test -short ./...
```

All PASS.

- [ ] **Step 30.3: Commit**

```bash
git add plugins/postgres/go.mod plugins/postgres/go.sum plugins/sqlite/go.mod plugins/sqlite/go.sum plugins/memory/go.mod plugins/memory/go.sum go.mod go.sum
git commit -m "chore: bump cyoda-go-spi to v0.6.0

Picks up ErrRetryExhausted and the tightened ExtendSchema contract
godoc. Required by B's plugin changes.

Refs data-ingestion-qa-subproject-b-design.md §6."
```

---

## Task 31: Gate 5 — Full verification

**Files:** none — verification only.

- [ ] **Step 31.1: Run full test suite (Docker required)**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/subproject-b-persistence
go test ./... -v 2>&1 | tee /tmp/b-full-test.log
grep -E "^(FAIL|--- FAIL)" /tmp/b-full-test.log | head
```

Expected: zero failures. If any FAIL, investigate and fix before proceeding.

- [ ] **Step 31.2: Per-plugin submodule tests**

```bash
cd plugins/memory && go test ./... -v 2>&1 | tail -20
cd ../postgres && go test ./... -v 2>&1 | tail -20
cd ../sqlite && go test ./... -v 2>&1 | tail -20
cd ../..
```

All PASS.

- [ ] **Step 31.3: go vet**

```bash
go vet ./...
```

Expected: no output (clean).

- [ ] **Step 31.4: Race detector one-shot**

```bash
go test -race ./... 2>&1 | tail -30
```

Expected: no races detected.

- [ ] **Step 31.5: Summarize in a final commit (if any docs were missed)**

If all above pass, no commit needed. If any trailing doc update is required, commit it here.

---

## Self-Review Checklist

After completing all tasks, verify:

- [ ] **Spec coverage:** every invariant B-I1..B-I8 has tests in at least one track.
- [ ] **Config coverage:** `CYODA_SCHEMA_SAVEPOINT_INTERVAL` and `CYODA_SCHEMA_EXTEND_MAX_RETRIES` work with defaults, env overrides, and invalid-input fallbacks across postgres and sqlite.
- [ ] **Per-plugin submodule tests green:** memory, postgres, sqlite.
- [ ] **Parity registry:** five new named entries + one property entry, all passing on memory/postgres/sqlite.
- [ ] **Cassandra inheritance:** parity registry entries are plugged into the `e2e/parity/registry.go` `allTests` slice so the Cassandra plugin picks them up automatically on its next go.mod refresh.
- [ ] **Overview §6 updated:** TBD rows replaced with concrete invariants.
- [ ] **Docs in sync:** printHelp + README + overview all reflect the two new env vars and the eight invariants.
- [ ] **No leftover TODOs** in the changed files (`grep -rn "TODO\|FIXME\|XXX" plugins/sqlite/ plugins/postgres/ e2e/parity/ | grep -v _test.go`).

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-22-data-ingestion-qa-subproject-b.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
