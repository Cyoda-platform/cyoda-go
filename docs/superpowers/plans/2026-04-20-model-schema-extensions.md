# Model Schema Extensions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the Postgres hot-row serialization conflict on concurrent entity updates of a model with `ChangeLevel` set, by splitting `ModelStore` into stable metadata plus an append-only typed-op extension log, adding `ExtendSchema` to the SPI, rewriting the ingestion path to use it, and wrapping storage in a `LOCKED`-caching decorator with gossip invalidate + singleflight refresh-on-stale.

**Architecture:** Two-table representation in Postgres (`models` + `model_schema_extensions`), plugin-internal savepoints every 64 deltas, `ApplyFunc` injected at factory construction so the schema package's op catalog stays out of plugin dependencies. `SchemaDelta` is opaque bytes at the SPI boundary; op kinds, merge rules, and commutativity/monotonicity tests live in `internal/domain/model/schema`. Cache decorator admits any `LOCKED` descriptor; TTL lease with ±10 % jitter + singleflight `RefreshAndGet` handle both steady-state and thundering-herd.

**Tech Stack:** Go 1.26.2, `log/slog`, pgx/v5, go-memdb/map-based memory plugin, mattn/go-sqlite3 for SQLite, `github.com/hashicorp/memberlist` via `internal/cluster/registry`, `golang.org/x/sync/singleflight`, testcontainers-go for E2E.

**Spec:** `docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md`.

**Companion spec (out of scope for this plan):** `cyoda-go-cassandra/docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md`.

---

## File Map

**SPI repository (`../cyoda-go-spi`, separate `go.mod`):**
- Modify: `persistence.go` — add `SchemaDelta` type and `ExtendSchema` method on `ModelStore` interface.

**Main repo (`cyoda-go`):**

*Schema package (new functions + types):*
- Create: `internal/domain/model/schema/ops.go` — `SchemaOp`, `SchemaOpKind`, op constructors.
- Create: `internal/domain/model/schema/ops_test.go`
- Create: `internal/domain/model/schema/apply.go` — `Apply` (replays op list).
- Create: `internal/domain/model/schema/apply_test.go`
- Create: `internal/domain/model/schema/diff.go` — `Diff` (computes op list from `old` vs `new`).
- Create: `internal/domain/model/schema/diff_test.go`
- Create: `internal/domain/model/schema/properties_test.go` — commutativity and validation-monotonicity property tests.
- Modify: `internal/domain/model/schema/validate.go` — introduce typed `ValidationError.Kind` field and `ErrKindUnknownElement` sentinel; add `HasUnknownSchemaElement(err error) bool` helper.

*Handler:*
- Modify: `internal/domain/entity/handler.go` — rewrite `validateOrExtend`; add `validateWithRefresh` wrapper.
- Modify: `internal/domain/entity/service.go` — three call sites of `validateOrExtend` stay; refresh-wrapper used at strict-validate branch.
- Modify: `internal/domain/search/service.go` — wrap field-path validation in refresh-on-stale.

*Cache:*
- Create: `internal/cluster/modelcache/cache.go`
- Create: `internal/cluster/modelcache/cache_test.go`
- Create: `internal/cluster/modelcache/payload.go` — tiny JSON-encoded `(tenantID, ref)` message for the gossip topic.
- Create: `internal/cluster/modelcache/payload_test.go`
- Create: `internal/cluster/modelcache/integration_test.go` — canonical read-side self-healing test.

*Postgres plugin:*
- Modify: `plugins/postgres/model_store.go` — add `ExtendSchema`; rewrite `Get` to fold log; rewrite `Save` to `DELETE`-then-`INSERT`; add `Unlock` dev-time assertion.
- Create: `plugins/postgres/model_extensions.go` — log scan + savepoint emission helpers.
- Create: `plugins/postgres/model_extensions_test.go`
- Modify: `plugins/postgres/store_factory.go` — add `ApplyFunc` to factory config, thread it into `modelStore` constructor.
- Modify: `plugins/postgres/plugin.go` — factory receives `ApplyFunc` from main-repo wiring.
- Replace: `plugins/postgres/migrations/000001_initial_schema.up.sql` — consolidated schema including `model_schema_extensions`.
- Replace: `plugins/postgres/migrations/000001_initial_schema.down.sql`
- Delete: `plugins/postgres/migrations/00000{2..5}_*.sql` — collapsed into 0001.

*SQLite plugin:*
- Modify: `plugins/sqlite/model_store.go` — add `ExtendSchema`; plugin-thin behaviour (apply-in-place).
- Modify: `plugins/sqlite/store_factory.go` — add `ApplyFunc` wiring.
- Modify: `plugins/sqlite/plugin.go`
- Modify: `plugins/sqlite/migrations/000001_initial_schema.up.sql` — consolidated (single file already; no delete).
- Modify: `plugins/sqlite/migrations/000001_initial_schema.down.sql`

*Memory plugin:*
- Modify: `plugins/memory/model_store.go` — add `ExtendSchema`; apply-in-place.
- Modify: `plugins/memory/store_factory.go` — add `ApplyFunc` wiring.
- Modify: `plugins/memory/plugin.go`

*Wiring:*
- Modify: `cmd/cyoda/main.go` — wire `schema.Apply` as the plugin `ApplyFunc`; wrap returned `ModelStore` in `modelcache.CachingModelStore`.

*Docs:*
- Modify: `docs/CONSISTENCY.md` — new "Model/Data Contract" section.
- Modify: `docs/ARCHITECTURE.md` — cross-refs in §2.3, §3, §4.

*Tests (additions):*
- Create: `internal/e2e/model_schema_extensions_test.go` — regression (bulk create + bulk update) and canonical self-healing E2E.
- Modify: `plugins/postgres/conformance_test.go` — add `ExtendSchema` conformance cases.
- Modify: `plugins/sqlite/conformance_test.go`
- Modify: `plugins/memory/conformance_test.go`

---

## Phase A — SPI changes

### Task A1: Add `SchemaDelta` type + `ExtendSchema` method to the SPI

**Files:**
- Modify: `../cyoda-go-spi/persistence.go`
- Test: `../cyoda-go-spi/persistence_test.go` (create if absent, otherwise extend)

**Context for the engineer.** The SPI lives in a sibling repo at `/Users/paul/go-projects/cyoda-light/cyoda-go-spi`. Use a feature branch `feat/model-schema-extensions` there. After committing, we'll bump the main repo's `go.mod` to a fresh version — **never force-move an existing tag** (see memory `feedback_go_module_tags_immutable.md`).

- [ ] **Step 1: Create worktree / feature branch in the SPI repo**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi
git checkout -b feat/model-schema-extensions
```

Expected: `Switched to a new branch 'feat/model-schema-extensions'`.

- [ ] **Step 2: Write the failing test for the new SPI shape**

Create `/Users/paul/go-projects/cyoda-light/cyoda-go-spi/persistence_extendschema_test.go`:

```go
package spi_test

import (
	"context"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// Compile-time only: guarantees the ModelStore interface carries
// ExtendSchema(ctx, ref, delta) with the agreed signature.
type extendSchemaCompile struct{ spi.ModelStore }

var _ = (*extendSchemaCompile)(nil)

func TestSchemaDeltaIsByteSlice(t *testing.T) {
	var d spi.SchemaDelta = []byte("raw-bytes-plugins-store-opaquely")
	if string(d) != "raw-bytes-plugins-store-opaquely" {
		t.Fatalf("SchemaDelta must be assignable from and convertible to []byte")
	}
}

func TestExtendSchemaSignature(t *testing.T) {
	// Compile-time check: force the interface type against an anonymous struct
	// that implements ExtendSchema. If the interface drops or renames the method,
	// this won't compile.
	var _ spi.ModelStore = (*anonExtendSchemaImpl)(nil)
}

type anonExtendSchemaImpl struct{}

func (anonExtendSchemaImpl) Save(_ context.Context, _ *spi.ModelDescriptor) error {
	return nil
}
func (anonExtendSchemaImpl) Get(_ context.Context, _ spi.ModelRef) (*spi.ModelDescriptor, error) {
	return nil, nil
}
func (anonExtendSchemaImpl) GetAll(_ context.Context) ([]spi.ModelRef, error) { return nil, nil }
func (anonExtendSchemaImpl) Delete(_ context.Context, _ spi.ModelRef) error   { return nil }
func (anonExtendSchemaImpl) Lock(_ context.Context, _ spi.ModelRef) error     { return nil }
func (anonExtendSchemaImpl) Unlock(_ context.Context, _ spi.ModelRef) error   { return nil }
func (anonExtendSchemaImpl) IsLocked(_ context.Context, _ spi.ModelRef) (bool, error) {
	return false, nil
}
func (anonExtendSchemaImpl) SetChangeLevel(_ context.Context, _ spi.ModelRef, _ spi.ChangeLevel) error {
	return nil
}
func (anonExtendSchemaImpl) ExtendSchema(_ context.Context, _ spi.ModelRef, _ spi.SchemaDelta) error {
	return nil
}
```

- [ ] **Step 3: Run — it must fail (type/method missing)**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && go test ./... -run TestSchemaDelta -v
```

Expected: compile error mentioning `undefined: spi.SchemaDelta` or missing `ExtendSchema` method on `ModelStore`.

- [ ] **Step 4: Add the type + method to `persistence.go`**

Open `persistence.go` and:

1. Above the `ModelStore` interface declaration, add:

```go
// SchemaDelta is an opaque, plugin-agnostic serialization of an
// additive schema change. Bytes are produced by the consuming
// application's schema diff logic (e.g. cyoda-go's
// internal/domain/model/schema) and replayed by an injected apply
// function in the plugin. Plugins persist bytes verbatim; they MUST
// NOT interpret them.
type SchemaDelta []byte
```

2. Inside `ModelStore`, directly after the existing method list and before the closing brace, add:

```go
	// ExtendSchema appends an additive schema delta to the model.
	//
	// Contract:
	//   - Semantically equivalent to "append the delta to the model's
	//     extension log, participating in the active entity
	//     transaction." A plugin whose storage doesn't natively model
	//     a log may implement this by applying the delta to the
	//     in-store schema so long as the externally observable
	//     behaviour matches: visible iff the entity tx commits; no
	//     conflict with concurrent data ops; result equal to what an
	//     append-and-fold would produce.
	//   - Save remains the full-replace path for admin operations and
	//     is disjoint from ExtendSchema via the model state machine:
	//     Save requires UNLOCKED, ExtendSchema requires LOCKED with
	//     ChangeLevel != "".
	//   - Concurrent ExtendSchema calls on distinct entity
	//     transactions targeting the same model MUST NOT conflict
	//     with each other at the storage layer.
	//   - Deltas produced by a well-formed schema diff are
	//     commutative and validation-monotone; plugins have no
	//     obligation beyond store-and-forward.
	ExtendSchema(ctx context.Context, ref ModelRef, delta SchemaDelta) error
```

- [ ] **Step 5: Run — test now passes**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && go test ./... -v
```

Expected: all tests (pre-existing + new) PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi && git add persistence.go persistence_extendschema_test.go && git commit -m "$(cat <<'EOF'
feat(spi): add SchemaDelta opaque-bytes type and ExtendSchema method

Opaque-bytes shape at the SPI boundary means the op catalog, merge
rules, and apply semantics stay in the consuming repo — plugins
store and forward. See cyoda-go design doc
2026-04-20-model-schema-extensions-design.md §4.2.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task A2: Wire main repo + plugins to use the SPI change via `go.work` for local dev

**Files:**
- Modify: `go.work` (main repo)

We don't tag a release yet — development uses the local worktree via `go.work`. Final tag/publish happens in Task Z1 before the PR.

- [ ] **Step 1: Add `cyoda-go-spi` to the workspace**

Edit `/Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/model-schema-extensions/go.work`:

```go.mod
go 1.26.2

use (
	.
	./plugins/memory
	./plugins/postgres
	./plugins/sqlite
	/Users/paul/go-projects/cyoda-light/cyoda-go-spi
)
```

- [ ] **Step 2: Verify workspace resolves the local SPI**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/model-schema-extensions && go list -m github.com/cyoda-platform/cyoda-go-spi
```

Expected: prints `github.com/cyoda-platform/cyoda-go-spi => /Users/paul/go-projects/cyoda-light/cyoda-go-spi`.

- [ ] **Step 3: Sanity-build and re-run short test suite**

```bash
go build ./... 2>&1 | tail -20 && go test -short ./... 2>&1 | tail -20
```

Expected: all builds and tests pass. (The SPI addition is additive; no existing code breaks.)

- [ ] **Step 4: Commit**

```bash
git add go.work && git commit -m "$(cat <<'EOF'
chore(workspace): include cyoda-go-spi for local development

Enables iteration on the new ExtendSchema SPI method across main
repo + plugins without pre-releasing a sibling module. Task Z1
will bump go.mod versions to a tagged release and remove this
workspace entry.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase B — Schema package: op catalog, Apply, Diff

### Task B1: Op-kind types and constructors

**Files:**
- Create: `internal/domain/model/schema/ops.go`
- Create: `internal/domain/model/schema/ops_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/model/schema/ops_test.go`:

```go
package schema

import (
	"testing"
)

func TestSchemaOpKindString(t *testing.T) {
	cases := []struct {
		kind SchemaOpKind
		want string
	}{
		{KindAddProperty, "add_property"},
		{KindAddEnumValue, "add_enum_value"},
		{KindBroadenType, "broaden_type"},
		{KindAddArrayItemType, "add_array_item_type"},
		{KindExtendOneOf, "extend_one_of"},
		{KindExtendAnyOf, "extend_any_of"},
	}
	for _, tc := range cases {
		if string(tc.kind) == "" || string(tc.kind) != tc.want {
			t.Errorf("kind %q: got %q want %q", tc.want, string(tc.kind), tc.want)
		}
	}
}

func TestNewOpsRoundTripJSON(t *testing.T) {
	ops := []SchemaOp{
		NewAddProperty("/properties/addr", "zip", []byte(`{"type":"string"}`)),
		NewAddEnumValue("/properties/status/enum", []byte(`"ARCHIVED"`)),
		NewBroadenType("/properties/age/type", []byte(`"null"`)),
	}
	delta, err := MarshalDelta(ops)
	if err != nil {
		t.Fatalf("MarshalDelta: %v", err)
	}
	got, err := UnmarshalDelta(delta)
	if err != nil {
		t.Fatalf("UnmarshalDelta: %v", err)
	}
	if len(got) != len(ops) {
		t.Fatalf("round trip: want %d ops got %d", len(ops), len(got))
	}
	for i := range ops {
		if got[i].Kind != ops[i].Kind {
			t.Errorf("op %d: kind %q got %q", i, ops[i].Kind, got[i].Kind)
		}
		if got[i].Path != ops[i].Path {
			t.Errorf("op %d: path %q got %q", i, ops[i].Path, got[i].Path)
		}
		if string(got[i].Payload) != string(ops[i].Payload) {
			t.Errorf("op %d: payload mismatch", i)
		}
	}
}
```

- [ ] **Step 2: Run — expect fail**

```bash
go test ./internal/domain/model/schema/... -run 'TestSchemaOpKindString|TestNewOpsRoundTripJSON' -v
```

Expected: compile errors about undefined `SchemaOpKind`, `NewAddProperty`, etc.

- [ ] **Step 3: Create `internal/domain/model/schema/ops.go`**

```go
package schema

import (
	"encoding/json"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// SchemaOpKind enumerates the catalog of additive schema operations.
// Every kind satisfies commutativity (folds order-independently when
// two deltas act on the same path) and validation-monotonicity (any
// document valid against B remains valid against Apply(B, op)).
// Ops that tighten the accepted set (e.g. adding to `required`) are
// deliberately excluded — they are not additive in the operational
// sense regardless of JSON Schema taxonomy.
//
// Wire format is stable: kind strings are persisted in the extension
// log and gossiped between cyoda-go versions. Adding a new kind is
// forward-incompatible across versions (see spec §8).
type SchemaOpKind string

const (
	KindAddProperty      SchemaOpKind = "add_property"
	KindAddEnumValue     SchemaOpKind = "add_enum_value"
	KindBroadenType      SchemaOpKind = "broaden_type"
	KindAddArrayItemType SchemaOpKind = "add_array_item_type"
	KindExtendOneOf      SchemaOpKind = "extend_one_of"
	KindExtendAnyOf      SchemaOpKind = "extend_any_of"
)

// SchemaOp is one entry in a serialized SchemaDelta. Payload shape is
// determined by Kind:
//
//	add_property           Payload = raw JSON of the property definition to add.
//	                       Path ends at the object's /properties node; additional
//	                       field on the op (Name) below holds the new key.
//	add_enum_value         Payload = raw JSON of the scalar enum value.
//	                       Path ends at the enclosing /enum array.
//	broaden_type           Payload = raw JSON string of the type primitive to add
//	                       (e.g. "null"). Path ends at the /type scalar or array.
//	add_array_item_type    Payload = raw JSON of the item schema variant.
//	                       Path ends at /items (or /prefixItems).
//	extend_one_of          Payload = raw JSON of the branch schema. Path at /oneOf.
//	extend_any_of          Payload = raw JSON of the branch schema. Path at /anyOf.
type SchemaOp struct {
	Kind    SchemaOpKind    `json:"kind"`
	Path    string          `json:"path"`              // RFC 6901 JSON pointer
	Name    string          `json:"name,omitempty"`    // add_property: the new property key
	Payload json.RawMessage `json:"payload,omitempty"` // op-specific; see table above
}

// Constructors ensure callers produce well-formed ops.

func NewAddProperty(parentPath, name string, definition []byte) SchemaOp {
	return SchemaOp{Kind: KindAddProperty, Path: parentPath, Name: name, Payload: definition}
}

func NewAddEnumValue(enumArrayPath string, valueJSON []byte) SchemaOp {
	return SchemaOp{Kind: KindAddEnumValue, Path: enumArrayPath, Payload: valueJSON}
}

func NewBroadenType(typePath string, addedTypeJSON []byte) SchemaOp {
	return SchemaOp{Kind: KindBroadenType, Path: typePath, Payload: addedTypeJSON}
}

func NewAddArrayItemType(itemsPath string, variantJSON []byte) SchemaOp {
	return SchemaOp{Kind: KindAddArrayItemType, Path: itemsPath, Payload: variantJSON}
}

func NewExtendOneOf(oneOfPath string, branchJSON []byte) SchemaOp {
	return SchemaOp{Kind: KindExtendOneOf, Path: oneOfPath, Payload: branchJSON}
}

func NewExtendAnyOf(anyOfPath string, branchJSON []byte) SchemaOp {
	return SchemaOp{Kind: KindExtendAnyOf, Path: anyOfPath, Payload: branchJSON}
}

// MarshalDelta serializes an op list into the opaque bytes that the
// SPI carries on ExtendSchema.
func MarshalDelta(ops []SchemaOp) (spi.SchemaDelta, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("MarshalDelta: %w", err)
	}
	return spi.SchemaDelta(b), nil
}

// UnmarshalDelta is the inverse of MarshalDelta.
func UnmarshalDelta(delta spi.SchemaDelta) ([]SchemaOp, error) {
	if len(delta) == 0 {
		return nil, nil
	}
	var ops []SchemaOp
	if err := json.Unmarshal(delta, &ops); err != nil {
		return nil, fmt.Errorf("UnmarshalDelta: %w", err)
	}
	return ops, nil
}
```

- [ ] **Step 4: Run — tests pass**

```bash
go test ./internal/domain/model/schema/... -run 'TestSchemaOpKindString|TestNewOpsRoundTripJSON' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/ops.go internal/domain/model/schema/ops_test.go && git commit -m "feat(schema): op catalog types and delta marshal helpers

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task B2: `Apply` function — op replay

**Files:**
- Create: `internal/domain/model/schema/apply.go`
- Create: `internal/domain/model/schema/apply_test.go`

- [ ] **Step 1: Failing test — add_property into a fresh object**

Create `internal/domain/model/schema/apply_test.go`:

```go
package schema

import (
	"encoding/json"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// mustUnmarshalNode parses a canonical JSON schema fragment used by tests.
func mustUnmarshalNode(t *testing.T, src string) *ModelNode {
	t.Helper()
	node, err := Unmarshal([]byte(src))
	if err != nil {
		t.Fatalf("Unmarshal(%q): %v", src, err)
	}
	return node
}

func mustMarshalDelta(t *testing.T, ops []SchemaOp) spi.SchemaDelta {
	t.Helper()
	d, err := MarshalDelta(ops)
	if err != nil {
		t.Fatalf("MarshalDelta: %v", err)
	}
	return d
}

func TestApplyAddProperty_InsertsKey(t *testing.T) {
	base := mustUnmarshalNode(t, `{"type":"object","properties":{"name":{"type":"string"}}}`)
	delta := mustMarshalDelta(t, []SchemaOp{
		NewAddProperty("/properties", "email", json.RawMessage(`{"type":"string"}`)),
	})
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out, _ := Marshal(got)
	want := `{"type":"object","properties":{"email":{"type":"string"},"name":{"type":"string"}}}`
	if string(out) != want {
		t.Fatalf("got %s\nwant %s", out, want)
	}
}

func TestApplyAddEnumValue_SetUnion(t *testing.T) {
	base := mustUnmarshalNode(t, `{"type":"string","enum":["A","B"]}`)
	delta := mustMarshalDelta(t, []SchemaOp{
		NewAddEnumValue("/enum", json.RawMessage(`"C"`)),
	})
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out, _ := Marshal(got)
	want := `{"type":"string","enum":["A","B","C"]}`
	if string(out) != want {
		t.Fatalf("got %s\nwant %s", out, want)
	}
}

func TestApplyAddEnumValue_Idempotent(t *testing.T) {
	base := mustUnmarshalNode(t, `{"type":"string","enum":["A","B"]}`)
	delta := mustMarshalDelta(t, []SchemaOp{
		NewAddEnumValue("/enum", json.RawMessage(`"A"`)), // already present
	})
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out, _ := Marshal(got)
	want := `{"type":"string","enum":["A","B"]}`
	if string(out) != want {
		t.Fatalf("idempotent add: got %s want %s", out, want)
	}
}

func TestApplyBroadenType_FromScalarToUnion(t *testing.T) {
	base := mustUnmarshalNode(t, `{"type":"string"}`)
	delta := mustMarshalDelta(t, []SchemaOp{
		NewBroadenType("/type", json.RawMessage(`"null"`)),
	})
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out, _ := Marshal(got)
	want := `{"type":["null","string"]}`
	if string(out) != want {
		t.Fatalf("got %s want %s", out, want)
	}
}

func TestApplyEmptyDelta_Noop(t *testing.T) {
	base := mustUnmarshalNode(t, `{"type":"string"}`)
	got, err := Apply(base, nil)
	if err != nil {
		t.Fatalf("Apply(nil): %v", err)
	}
	out, _ := Marshal(got)
	want := `{"type":"string"}`
	if string(out) != want {
		t.Fatalf("got %s want %s", out, want)
	}
}
```

- [ ] **Step 2: Run — expect fail**

```bash
go test ./internal/domain/model/schema/... -run '^TestApply' -v
```

Expected: compile error about undefined `Apply`.

- [ ] **Step 3: Create `internal/domain/model/schema/apply.go`**

```go
package schema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// Apply replays the opaque SchemaDelta bytes onto base, returning a new
// ModelNode. The same function is used by plugins (via factory injection)
// to fold the extension log on Get, and by tests to verify commutativity
// and validation-monotonicity.
//
// Apply does not mutate base. It returns a fresh tree.
func Apply(base *ModelNode, delta spi.SchemaDelta) (*ModelNode, error) {
	if base == nil {
		return nil, fmt.Errorf("Apply: base is nil")
	}
	if len(delta) == 0 {
		return cloneNode(base), nil
	}
	ops, err := UnmarshalDelta(delta)
	if err != nil {
		return nil, fmt.Errorf("Apply: %w", err)
	}

	// Round-trip through JSON to get a mutable generic tree.
	raw, err := Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("Apply: marshal base: %w", err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("Apply: reparse base: %w", err)
	}

	for i, op := range ops {
		tree, err = applyOp(tree, op)
		if err != nil {
			return nil, fmt.Errorf("Apply: op %d (%s %q): %w", i, op.Kind, op.Path, err)
		}
	}

	out, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("Apply: re-marshal: %w", err)
	}
	return Unmarshal(out)
}

func applyOp(root any, op SchemaOp) (any, error) {
	switch op.Kind {
	case KindAddProperty:
		return applyAddProperty(root, op)
	case KindAddEnumValue:
		return applyAddScalarSetUnion(root, op.Path, op.Payload)
	case KindBroadenType:
		return applyBroadenType(root, op.Path, op.Payload)
	case KindAddArrayItemType:
		return applyAddObjectSetUnion(root, op.Path, op.Payload)
	case KindExtendOneOf, KindExtendAnyOf:
		return applyAddObjectSetUnion(root, op.Path, op.Payload)
	default:
		return nil, fmt.Errorf("unknown op kind %q", op.Kind)
	}
}

// applyAddProperty sets root[path]/properties/name = definition when name is absent.
// If name is present and the payloads differ, the merge rule is schema-union
// (polymorphic broadening) — the resulting property is oneOf the two definitions.
func applyAddProperty(root any, op SchemaOp) (any, error) {
	parent, err := resolvePointer(root, op.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve parent: %w", err)
	}
	obj, ok := parent.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("path %q: expected object, got %T", op.Path, parent)
	}
	var incoming any
	if err := json.Unmarshal(op.Payload, &incoming); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	existing, present := obj[op.Name]
	if !present {
		obj[op.Name] = incoming
		return root, nil
	}
	// Idempotent on exact payload.
	if deepEqualJSON(existing, incoming) {
		return root, nil
	}
	// Divergent: schema-union via oneOf.
	obj[op.Name] = map[string]any{
		"oneOf": []any{existing, incoming},
	}
	return root, nil
}

// applyAddScalarSetUnion appends a scalar value to an array located at path
// if not already present.
func applyAddScalarSetUnion(root any, path string, payload json.RawMessage) (any, error) {
	parent, containerKey, container, err := resolvePointerParent(root, path)
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	arr, ok := container.([]any)
	if !ok {
		return nil, fmt.Errorf("path %q: expected array, got %T", path, container)
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("unmarshal value: %w", err)
	}
	for _, el := range arr {
		if deepEqualJSON(el, v) {
			return root, nil
		}
	}
	arr = append(arr, v)
	// Canonicalize: sort scalars so order is determinism-independent.
	sortIfAllStrings(arr)
	return setPointerChild(root, parent, containerKey, arr), nil
}

// applyBroadenType merges a type primitive into the /type value.
// Result is an alphabetically sorted array of unique type names.
func applyBroadenType(root any, path string, payload json.RawMessage) (any, error) {
	parent, containerKey, container, err := resolvePointerParent(root, path)
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	var addedStr string
	if err := json.Unmarshal(payload, &addedStr); err != nil {
		return nil, fmt.Errorf("unmarshal type primitive: %w", err)
	}
	var result []string
	switch cur := container.(type) {
	case string:
		if cur == addedStr {
			return root, nil
		}
		result = []string{cur, addedStr}
	case []any:
		seen := make(map[string]bool)
		for _, el := range cur {
			s, ok := el.(string)
			if !ok {
				return nil, fmt.Errorf("non-string in type array: %T", el)
			}
			if !seen[s] {
				result = append(result, s)
				seen[s] = true
			}
		}
		if !seen[addedStr] {
			result = append(result, addedStr)
		}
	default:
		return nil, fmt.Errorf("path %q: unexpected /type shape %T", path, container)
	}
	sort.Strings(result)
	if len(result) == 1 {
		return setPointerChild(root, parent, containerKey, result[0]), nil
	}
	arr := make([]any, len(result))
	for i, s := range result {
		arr[i] = s
	}
	return setPointerChild(root, parent, containerKey, arr), nil
}

// applyAddObjectSetUnion appends an object variant to an array located at
// path, keyed by a signature hash so structurally identical adds dedup.
func applyAddObjectSetUnion(root any, path string, payload json.RawMessage) (any, error) {
	parent, containerKey, container, err := resolvePointerParent(root, path)
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	arr, ok := container.([]any)
	if !ok {
		// prefixItems/items may start as a single schema object, not an array.
		// Wrap and proceed.
		arr = []any{container}
	}
	var incoming any
	if err := json.Unmarshal(payload, &incoming); err != nil {
		return nil, fmt.Errorf("unmarshal variant: %w", err)
	}
	for _, el := range arr {
		if signatureEqual(el, incoming) {
			return root, nil
		}
	}
	arr = append(arr, incoming)
	return setPointerChild(root, parent, containerKey, arr), nil
}

// ---- JSON pointer helpers ------------------------------------------------

func resolvePointer(root any, pointer string) (any, error) {
	if pointer == "" || pointer == "/" {
		return root, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("pointer must start with /: %q", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	cur := root
	for _, part := range parts {
		part = decodePointerSegment(part)
		switch c := cur.(type) {
		case map[string]any:
			next, ok := c[part]
			if !ok {
				return nil, fmt.Errorf("missing segment %q", part)
			}
			cur = next
		case []any:
			// Numeric segments unsupported here; schemas don't use positional paths.
			return nil, fmt.Errorf("unexpected array at segment %q", part)
		default:
			return nil, fmt.Errorf("cannot descend into %T at segment %q", cur, part)
		}
	}
	return cur, nil
}

// resolvePointerParent returns the parent container (a map) plus the key,
// plus the current child value at that key.
func resolvePointerParent(root any, pointer string) (parent map[string]any, key string, child any, err error) {
	if pointer == "" || pointer == "/" {
		return nil, "", nil, fmt.Errorf("cannot resolve parent of root")
	}
	idx := strings.LastIndex(pointer, "/")
	parentPtr := pointer[:idx]
	lastKey := decodePointerSegment(pointer[idx+1:])
	resolved, err := resolvePointer(root, parentPtr)
	if err != nil {
		return nil, "", nil, err
	}
	parentMap, ok := resolved.(map[string]any)
	if !ok {
		return nil, "", nil, fmt.Errorf("parent of %q is %T, not object", pointer, resolved)
	}
	return parentMap, lastKey, parentMap[lastKey], nil
}

func setPointerChild(root any, parent map[string]any, key string, value any) any {
	parent[key] = value
	return root
}

func decodePointerSegment(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}

// ---- deep-equal / signatures --------------------------------------------

func deepEqualJSON(a, b any) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}

// signatureEqual compares two object variants for purposes of set-union.
// A full canonical-JSON equality is sufficient for phase 1 and is what the
// commutativity property tests exercise.
func signatureEqual(a, b any) bool { return deepEqualJSON(a, b) }

func sortIfAllStrings(arr []any) {
	for _, el := range arr {
		if _, ok := el.(string); !ok {
			return
		}
	}
	sort.SliceStable(arr, func(i, j int) bool {
		return arr[i].(string) < arr[j].(string)
	})
}

// cloneNode produces an independent copy of node. Used when delta is empty
// so callers still own the returned tree.
func cloneNode(node *ModelNode) *ModelNode {
	raw, _ := Marshal(node)
	out, _ := Unmarshal(raw)
	return out
}
```

- [ ] **Step 4: Run — the four basic-case tests pass**

```bash
go test ./internal/domain/model/schema/... -run '^TestApply' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/apply.go internal/domain/model/schema/apply_test.go && git commit -m "feat(schema): Apply fn — per-kind merge rules for additive schema deltas

Each op-kind has a commutative, validation-monotone merge rule:
add_property idempotent-or-schema-union, enum/items set-union by
deep-equality, broaden_type set-union over type primitives with
sorted output.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task B3: Commutativity property test with path-relationship axis

**Files:**
- Create: `internal/domain/model/schema/properties_test.go`

- [ ] **Step 1: Write the failing property test**

Create `internal/domain/model/schema/properties_test.go`:

```go
package schema

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"
)

// genSample produces (base, opA, opB) triples. pathRel ∈ {"disjoint","equal","prefix"}.
type pathRel string

const (
	relDisjoint pathRel = "disjoint"
	relEqual    pathRel = "equal"
	relPrefix   pathRel = "prefix"
)

type sampleGen func(r *rand.Rand, rel pathRel) (base string, opA, opB SchemaOp)

// samplesForKinds returns a generator for each ordered pair of op-kinds.
func samplesForKinds(k1, k2 SchemaOpKind) sampleGen {
	return func(r *rand.Rand, rel pathRel) (string, SchemaOp, SchemaOp) {
		switch {
		case k1 == KindAddProperty && k2 == KindAddProperty:
			return genTwoAddProperty(r, rel)
		case k1 == KindAddEnumValue && k2 == KindAddEnumValue:
			return genTwoAddEnum(r, rel)
		case k1 == KindBroadenType && k2 == KindBroadenType:
			return genTwoBroadenType(r, rel)
		case (k1 == KindAddProperty && k2 == KindAddEnumValue) ||
			(k1 == KindAddEnumValue && k2 == KindAddProperty):
			return genPropertyPlusEnum(r, rel, k1)
		default:
			// Other pairs default to property/property — covered by the other cases;
			// this ensures every axis is exercised at least once.
			return genTwoAddProperty(r, rel)
		}
	}
}

// TestCommutativity_ByKindPairAndPathRelationship is the core property.
// Apply(Apply(base, opA), opB) must equal Apply(Apply(base, opB), opA)
// for every (kind, kind, path-relationship) combination in the axis grid.
func TestCommutativity_ByKindPairAndPathRelationship(t *testing.T) {
	kinds := []SchemaOpKind{
		KindAddProperty, KindAddEnumValue, KindBroadenType,
	}
	rels := []pathRel{relDisjoint, relEqual, relPrefix}
	const samplesPerCell = 10
	r := rand.New(rand.NewPCG(42, 7))

	for _, k1 := range kinds {
		for _, k2 := range kinds {
			gen := samplesForKinds(k1, k2)
			for _, rel := range rels {
				for s := 0; s < samplesPerCell; s++ {
					baseJSON, opA, opB := gen(r, rel)
					name := fmt.Sprintf("%s/%s/%s/#%d", k1, k2, rel, s)
					t.Run(name, func(t *testing.T) {
						assertCommutative(t, baseJSON, opA, opB)
					})
				}
			}
		}
	}
}

func assertCommutative(t *testing.T, baseJSON string, opA, opB SchemaOp) {
	t.Helper()
	base := mustUnmarshalNode(t, baseJSON)
	dA := mustMarshalDelta(t, []SchemaOp{opA})
	dB := mustMarshalDelta(t, []SchemaOp{opB})

	ab, err := Apply(base, dA)
	if err != nil {
		t.Fatalf("Apply A: %v", err)
	}
	ab, err = Apply(ab, dB)
	if err != nil {
		t.Fatalf("Apply B after A: %v", err)
	}

	ba, err := Apply(base, dB)
	if err != nil {
		t.Fatalf("Apply B: %v", err)
	}
	ba, err = Apply(ba, dA)
	if err != nil {
		t.Fatalf("Apply A after B: %v", err)
	}

	abBytes, _ := Marshal(ab)
	baBytes, _ := Marshal(ba)
	if string(abBytes) != string(baBytes) {
		t.Errorf("not commutative:\nA then B: %s\nB then A: %s", abBytes, baBytes)
	}
}

// ---- generators (concrete, deterministic via seeded rand) --------------

func genTwoAddProperty(r *rand.Rand, rel pathRel) (string, SchemaOp, SchemaOp) {
	switch rel {
	case relDisjoint:
		base := `{"type":"object","properties":{"name":{"type":"string"}}}`
		return base,
			NewAddProperty("/properties", "a1", json.RawMessage(`{"type":"string"}`)),
			NewAddProperty("/properties", "a2", json.RawMessage(`{"type":"integer"}`))
	case relEqual:
		// Same path, same name, divergent payloads → schema-union.
		base := `{"type":"object","properties":{"name":{"type":"string"}}}`
		return base,
			NewAddProperty("/properties", "x", json.RawMessage(`{"type":"string"}`)),
			NewAddProperty("/properties", "x", json.RawMessage(`{"type":"integer"}`))
	case relPrefix:
		// A adds /properties/addr (object), B adds /properties/addr/properties/zip.
		base := `{"type":"object","properties":{}}`
		return base,
			NewAddProperty("/properties", "addr",
				json.RawMessage(`{"type":"object","properties":{}}`)),
			NewAddProperty("/properties/addr/properties", "zip",
				json.RawMessage(`{"type":"string"}`))
	}
	panic("unreachable")
}

func genTwoAddEnum(r *rand.Rand, rel pathRel) (string, SchemaOp, SchemaOp) {
	base := `{"type":"string","enum":["A"]}`
	switch rel {
	case relDisjoint:
		// Two enums in two separate fields of an object.
		base = `{"type":"object","properties":{"a":{"type":"string","enum":["A"]},"b":{"type":"string","enum":["X"]}}}`
		return base,
			NewAddEnumValue("/properties/a/enum", json.RawMessage(`"B"`)),
			NewAddEnumValue("/properties/b/enum", json.RawMessage(`"Y"`))
	case relEqual:
		return base,
			NewAddEnumValue("/enum", json.RawMessage(`"B"`)),
			NewAddEnumValue("/enum", json.RawMessage(`"C"`))
	case relPrefix:
		base = `{"type":"object","properties":{"tag":{"type":"string","enum":["A"]}}}`
		// A broadens type; B adds enum value. Different axes, same nesting.
		return base,
			NewBroadenType("/properties/tag/type", json.RawMessage(`"null"`)),
			NewAddEnumValue("/properties/tag/enum", json.RawMessage(`"B"`))
	}
	panic("unreachable")
}

func genTwoBroadenType(r *rand.Rand, rel pathRel) (string, SchemaOp, SchemaOp) {
	switch rel {
	case relDisjoint:
		base := `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}}}`
		return base,
			NewBroadenType("/properties/a/type", json.RawMessage(`"null"`)),
			NewBroadenType("/properties/b/type", json.RawMessage(`"null"`))
	case relEqual:
		base := `{"type":"string"}`
		return base,
			NewBroadenType("/type", json.RawMessage(`"null"`)),
			NewBroadenType("/type", json.RawMessage(`"number"`))
	case relPrefix:
		base := `{"type":"object","properties":{"a":{"type":"string"}}}`
		return base,
			NewAddProperty("/properties", "b", json.RawMessage(`{"type":"integer"}`)),
			NewBroadenType("/properties/a/type", json.RawMessage(`"null"`))
	}
	panic("unreachable")
}

func genPropertyPlusEnum(r *rand.Rand, rel pathRel, firstKind SchemaOpKind) (string, SchemaOp, SchemaOp) {
	base := `{"type":"object","properties":{"status":{"type":"string","enum":["A"]}}}`
	prop := NewAddProperty("/properties", "newField", json.RawMessage(`{"type":"integer"}`))
	enum := NewAddEnumValue("/properties/status/enum", json.RawMessage(`"B"`))
	_ = rel // all three relationships collapse to the same sample here
	if firstKind == KindAddProperty {
		return base, prop, enum
	}
	return base, enum, prop
}
```

- [ ] **Step 2: Run — expect PASS (Apply already implements these rules)**

```bash
go test ./internal/domain/model/schema/... -run 'TestCommutativity' -v -count=1
```

Expected: all subtests PASS. If any fail, Apply has an order-dependency bug and must be fixed before proceeding.

- [ ] **Step 3: If any subtest fails — fix the merge rule, re-run**

If a failure surfaces (common candidates: the `sortIfAllStrings` canonicalization missing a case, or `applyBroadenType` not preserving stable order), fix inline in `apply.go`, re-run, repeat until green. This is the point where the property test earns its keep.

- [ ] **Step 4: Commit**

```bash
git add internal/domain/model/schema/properties_test.go && git commit -m "test(schema): commutativity property test across kind × path-relationship × sample axes

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task B4: Validation-monotonicity property test

**Files:**
- Modify: `internal/domain/model/schema/properties_test.go`

- [ ] **Step 1: Append the monotonicity test**

At the end of `properties_test.go`, add:

```go
// TestValidationMonotonicity — for every op-kind sample, every document
// valid against the base schema must remain valid against Apply(base, op).
// Encodes "additive = strictly broadening accepted set."
func TestValidationMonotonicity(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		op      SchemaOp
		docs    []string
	}{
		{
			name: "add_property_does_not_reject_old_docs",
			base: `{"type":"object","properties":{"name":{"type":"string"}}}`,
			op:   NewAddProperty("/properties", "email", json.RawMessage(`{"type":"string"}`)),
			docs: []string{`{"name":"alice"}`, `{}`, `{"name":"bob"}`},
		},
		{
			name: "add_enum_value_accepts_old_values",
			base: `{"type":"string","enum":["A","B"]}`,
			op:   NewAddEnumValue("/enum", json.RawMessage(`"C"`)),
			docs: []string{`"A"`, `"B"`},
		},
		{
			name: "broaden_type_accepts_old_type_values",
			base: `{"type":"string"}`,
			op:   NewBroadenType("/type", json.RawMessage(`"null"`)),
			docs: []string{`"hello"`, `""`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := mustUnmarshalNode(t, tc.base)
			delta := mustMarshalDelta(t, []SchemaOp{tc.op})
			extended, err := Apply(base, delta)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			for _, docStr := range tc.docs {
				var doc any
				if err := json.Unmarshal([]byte(docStr), &doc); err != nil {
					t.Fatalf("parse doc %q: %v", docStr, err)
				}
				if errs := Validate(base, doc); len(errs) != 0 {
					t.Fatalf("precondition: doc %s must validate against base (got %v)", docStr, errs)
				}
				if errs := Validate(extended, doc); len(errs) != 0 {
					t.Errorf("doc %s valid under base but invalid after %s: %v", docStr, tc.op.Kind, errs)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run — expect PASS**

```bash
go test ./internal/domain/model/schema/... -run 'TestValidationMonotonicity' -v
```

Expected: all subtests PASS. If `broaden_type` or another kind rejects previously-valid docs, the merge rule is buggy — fix in `apply.go`.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/properties_test.go && git commit -m "test(schema): validation-monotonicity property test

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task B5: `Diff` function — extract op list from (old, new)

**Files:**
- Create: `internal/domain/model/schema/diff.go`
- Create: `internal/domain/model/schema/diff_test.go`

- [ ] **Step 1: Write failing round-trip test**

Create `internal/domain/model/schema/diff_test.go`:

```go
package schema

import (
	"encoding/json"
	"testing"
)

func TestDiff_AddOneProperty(t *testing.T) {
	oldS := mustUnmarshalNode(t, `{"type":"object","properties":{"name":{"type":"string"}}}`)
	newS := mustUnmarshalNode(t, `{"type":"object","properties":{"name":{"type":"string"},"email":{"type":"string"}}}`)

	delta, err := Diff(oldS, newS)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(delta) == 0 {
		t.Fatal("expected non-nil delta for differing schemas")
	}

	folded, err := Apply(oldS, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, _ := Marshal(folded)
	want, _ := Marshal(newS)
	if string(got) != string(want) {
		t.Fatalf("round trip mismatch\n got  %s\n want %s", got, want)
	}
}

func TestDiff_NoChange_ReturnsNil(t *testing.T) {
	schemaSrc := `{"type":"object","properties":{"name":{"type":"string"}}}`
	oldS := mustUnmarshalNode(t, schemaSrc)
	newS := mustUnmarshalNode(t, schemaSrc)

	delta, err := Diff(oldS, newS)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if delta != nil {
		t.Errorf("expected nil delta for identical schemas, got %q", string(delta))
	}
}

func TestDiff_BroadenType(t *testing.T) {
	oldS := mustUnmarshalNode(t, `{"type":"string"}`)
	newS := mustUnmarshalNode(t, `{"type":["null","string"]}`)

	delta, err := Diff(oldS, newS)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	ops, _ := UnmarshalDelta(delta)
	if len(ops) != 1 || ops[0].Kind != KindBroadenType {
		t.Fatalf("expected single broaden_type op, got %+v", ops)
	}
	var added string
	_ = json.Unmarshal(ops[0].Payload, &added)
	if added != "null" {
		t.Errorf("expected added type 'null', got %q", added)
	}
}

func TestDiff_AddEnumValue(t *testing.T) {
	oldS := mustUnmarshalNode(t, `{"type":"string","enum":["A","B"]}`)
	newS := mustUnmarshalNode(t, `{"type":"string","enum":["A","B","C"]}`)

	delta, err := Diff(oldS, newS)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	ops, _ := UnmarshalDelta(delta)
	if len(ops) != 1 || ops[0].Kind != KindAddEnumValue {
		t.Fatalf("expected single add_enum_value, got %+v", ops)
	}
}
```

- [ ] **Step 2: Run — expect fail**

```bash
go test ./internal/domain/model/schema/... -run '^TestDiff' -v
```

Expected: compile error (`undefined: Diff`).

- [ ] **Step 3: Create `internal/domain/model/schema/diff.go`**

```go
package schema

import (
	"encoding/json"
	"fmt"
	"sort"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// Diff emits the serialized delta expressing `new` as an additive change
// over `old`. Callers guarantee `new` is produced by schema.Extend with a
// valid ChangeLevel — non-additive differences are a programming error in
// Extend, not a runtime mode Diff needs to handle gracefully.
//
// Returns:
//   - (delta, nil) when the schemas differ.
//   - (nil, nil) when old == new semantically — callers treat this as a
//     no-op and skip ExtendSchema.
//   - (nil, err) if the change cannot be expressed by the op catalog.
//     This is a design bug surfaced by the Extend-completeness test (§7).
func Diff(old, new *ModelNode) (spi.SchemaDelta, error) {
	if old == nil || new == nil {
		return nil, fmt.Errorf("Diff: nil input")
	}

	oldRaw, err := Marshal(old)
	if err != nil {
		return nil, fmt.Errorf("Diff: marshal old: %w", err)
	}
	newRaw, err := Marshal(new)
	if err != nil {
		return nil, fmt.Errorf("Diff: marshal new: %w", err)
	}
	if string(oldRaw) == string(newRaw) {
		return nil, nil // no-op
	}

	var oldTree, newTree any
	if err := json.Unmarshal(oldRaw, &oldTree); err != nil {
		return nil, fmt.Errorf("Diff: reparse old: %w", err)
	}
	if err := json.Unmarshal(newRaw, &newTree); err != nil {
		return nil, fmt.Errorf("Diff: reparse new: %w", err)
	}

	var ops []SchemaOp
	if err := diffNode(oldTree, newTree, "", &ops); err != nil {
		return nil, err
	}
	return MarshalDelta(ops)
}

func diffNode(oldV, newV any, path string, ops *[]SchemaOp) error {
	// Fast path: equal subtrees contribute nothing.
	if deepEqualJSON(oldV, newV) {
		return nil
	}

	oldMap, oldIsMap := oldV.(map[string]any)
	newMap, newIsMap := newV.(map[string]any)

	if oldIsMap && newIsMap {
		return diffObject(oldMap, newMap, path, ops)
	}

	// Type changed at this path — the only additive case is broaden_type
	// at a /type key.
	if isTypeField(path) {
		return diffTypeField(oldV, newV, path, ops)
	}

	return fmt.Errorf("diff: non-additive change at %q (%T → %T)", path, oldV, newV)
}

func diffObject(oldM, newM map[string]any, path string, ops *[]SchemaOp) error {
	// Keys removed → not additive.
	for k := range oldM {
		if _, ok := newM[k]; !ok {
			return fmt.Errorf("diff: key %q removed at %q", k, path)
		}
	}

	// Sort keys for deterministic op ordering.
	keys := make([]string, 0, len(newM))
	for k := range newM {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		oldV, present := oldM[k]
		childPath := path + "/" + encodePointerSegment(k)
		if !present {
			// New property at this path.
			if isPropertiesParent(path) {
				// The parent is /properties; emit an add_property op scoped at path.
				payload, _ := json.Marshal(newM[k])
				*ops = append(*ops, NewAddProperty(path, k, payload))
				continue
			}
			// New value under /enum, /oneOf etc.
			if err := emitAddAtContainer(path, k, newM[k], ops); err != nil {
				return err
			}
			continue
		}
		if err := diffNode(oldV, newM[k], childPath, ops); err != nil {
			return err
		}
	}

	// Arrays handled via the diffArrayContainer path when the parent key
	// is one of /enum, /oneOf, /anyOf, /items, /prefixItems.
	return diffArrayContainers(oldM, newM, path, ops)
}

func diffArrayContainers(oldM, newM map[string]any, path string, ops *[]SchemaOp) error {
	for _, containerKey := range []string{"enum", "oneOf", "anyOf", "items", "prefixItems", "type"} {
		oldV, oldHas := oldM[containerKey]
		newV, newHas := newM[containerKey]
		if !newHas {
			continue
		}
		childPath := path + "/" + containerKey
		if !oldHas {
			// Container newly added: treat every element as an append.
			if err := emitContainerContents(childPath, newV, ops); err != nil {
				return err
			}
			continue
		}
		if deepEqualJSON(oldV, newV) {
			continue
		}
		if containerKey == "type" {
			if err := diffTypeField(oldV, newV, childPath, ops); err != nil {
				return err
			}
			continue
		}
		if err := diffArrayAtPath(oldV, newV, childPath, ops); err != nil {
			return err
		}
	}
	return nil
}

func diffArrayAtPath(oldV, newV any, path string, ops *[]SchemaOp) error {
	oldArr, ok1 := oldV.([]any)
	newArr, ok2 := newV.([]any)
	if !ok1 || !ok2 {
		return fmt.Errorf("diff: %q expected arrays", path)
	}
	for _, nv := range newArr {
		if containsEqual(oldArr, nv) {
			continue
		}
		if err := emitAddAtContainer(path, "", nv, ops); err != nil {
			return err
		}
	}
	// Removed values → not additive.
	for _, ov := range oldArr {
		if !containsEqual(newArr, ov) {
			return fmt.Errorf("diff: value removed from %q", path)
		}
	}
	return nil
}

func diffTypeField(oldV, newV any, path string, ops *[]SchemaOp) error {
	oldTypes := typeSet(oldV)
	newTypes := typeSet(newV)
	for _, ot := range oldTypes {
		if !stringSliceContains(newTypes, ot) {
			return fmt.Errorf("diff: type %q removed from %q", ot, path)
		}
	}
	for _, nt := range newTypes {
		if stringSliceContains(oldTypes, nt) {
			continue
		}
		payload, _ := json.Marshal(nt)
		*ops = append(*ops, NewBroadenType(path, payload))
	}
	return nil
}

func emitAddAtContainer(containerPath, key string, v any, ops *[]SchemaOp) error {
	switch lastSegment(containerPath) {
	case "enum":
		payload, _ := json.Marshal(v)
		*ops = append(*ops, NewAddEnumValue(containerPath, payload))
		return nil
	case "oneOf":
		payload, _ := json.Marshal(v)
		*ops = append(*ops, NewExtendOneOf(containerPath, payload))
		return nil
	case "anyOf":
		payload, _ := json.Marshal(v)
		*ops = append(*ops, NewExtendAnyOf(containerPath, payload))
		return nil
	case "items", "prefixItems":
		payload, _ := json.Marshal(v)
		*ops = append(*ops, NewAddArrayItemType(containerPath, payload))
		return nil
	}
	return fmt.Errorf("diff: cannot emit add for %q at %q", key, containerPath)
}

func emitContainerContents(path string, v any, ops *[]SchemaOp) error {
	arr, ok := v.([]any)
	if !ok {
		// Single-element container (e.g. items as an object); emit one add.
		return emitAddAtContainer(path, "", v, ops)
	}
	for _, el := range arr {
		if err := emitAddAtContainer(path, "", el, ops); err != nil {
			return err
		}
	}
	return nil
}

// ---- small helpers -------------------------------------------------------

func isTypeField(path string) bool       { return lastSegment(path) == "type" }
func isPropertiesParent(path string) bool { return lastSegment(path) == "properties" }

func lastSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func encodePointerSegment(s string) string {
	// Inverse of decodePointerSegment in apply.go.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func typeSet(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, el := range t {
			if s, ok := el.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func stringSliceContains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

func containsEqual(arr []any, v any) bool {
	for _, el := range arr {
		if deepEqualJSON(el, v) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run — Diff tests pass**

```bash
go test ./internal/domain/model/schema/... -run '^TestDiff' -v
```

Expected: PASS.

- [ ] **Step 5: Run the full schema-package suite**

```bash
go test ./internal/domain/model/schema/... -v
```

Expected: PASS (apply + diff + properties + validate + extend all green).

- [ ] **Step 6: Commit**

```bash
git add internal/domain/model/schema/diff.go internal/domain/model/schema/diff_test.go && git commit -m "feat(schema): Diff fn — op list extraction with no-op short-circuit

Returns nil delta for semantically equal schemas so callers can skip
ExtendSchema without a pointer-equality check on Extend's output.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task B6: Extend-completeness test (coverage gate against catalog regressions)

**Files:**
- Modify: `internal/domain/model/schema/properties_test.go`

- [ ] **Step 1: Append the completeness test**

At the end of `properties_test.go`:

```go
// TestExtendCompleteness — every (ChangeLevel, input-shape) combination
// that schema.Extend permits must produce a delta that Diff can encode
// AND Apply can replay back to the Extend output. A regression here
// (e.g. Extend gains a new code path without a matching op-kind) fails
// loudly instead of silently emitting an unencodable change at runtime.
func TestExtendCompleteness(t *testing.T) {
	type tc struct {
		name     string
		level    string // passed to Extend via spi.ChangeLevel
		base     string
		incoming string
	}
	cases := []tc{
		{"structural_add_property", "STRUCTURAL",
			`{"type":"object","properties":{"name":{"type":"string"}}}`,
			`{"name":"alice","email":"a@b.com"}`},
		{"type_broaden_null", "TYPE",
			`{"type":"object","properties":{"age":{"type":"integer"}}}`,
			`{"age":null}`},
		{"array_items_new_variant", "ARRAY_ELEMENTS",
			`{"type":"object","properties":{"xs":{"type":"array","items":{"type":"string"}}}}`,
			`{"xs":["a",1]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := mustUnmarshalNode(t, c.base)
			var incoming any
			if err := json.Unmarshal([]byte(c.incoming), &incoming); err != nil {
				t.Fatalf("parse incoming: %v", err)
			}
			incomingNode, err := walkIncomingForTest(incoming)
			if err != nil {
				t.Fatalf("walk incoming: %v", err)
			}
			extended, err := Extend(base, incomingNode, c.level)
			if err != nil {
				t.Fatalf("Extend: %v", err)
			}
			delta, err := Diff(base, extended)
			if err != nil {
				t.Fatalf("Diff: %v — the op catalog is incomplete for this Extend output. Either add a matching SchemaOp kind, or constrain Extend to not produce this shape.", err)
			}
			if delta == nil {
				// Fine — shape didn't actually change (e.g. incoming already covered).
				return
			}
			folded, err := Apply(base, delta)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			gotRaw, _ := Marshal(folded)
			wantRaw, _ := Marshal(extended)
			if string(gotRaw) != string(wantRaw) {
				t.Errorf("round trip mismatch\n got  %s\n want %s", gotRaw, wantRaw)
			}
		})
	}
}

// walkIncomingForTest is a local shim for importer.Walk; kept here to
// avoid an import cycle between schema and importer during tests.
func walkIncomingForTest(v any) (*ModelNode, error) {
	raw, _ := json.Marshal(v)
	// Approximate: round-trip through the schema parser treating the
	// document as itself a ModelNode-like structure. Real test setups
	// may substitute importer.Walk directly if it's importable here.
	return Unmarshal(raw)
}
```

**Note for the implementer.** If the test file cannot import `spi.ChangeLevel` types directly (e.g. when schema lives below the spi import boundary), replace the string argument with the typed constant from the existing Extend signature — find what `schema.Extend` takes in today's code and mirror that. If the `walkIncomingForTest` shim turns out to diverge from `importer.Walk` in a way that masks a regression, replace it with a call to `importer.Walk` — moving the test into a new `_integration_test.go` file is acceptable to avoid cycles.

- [ ] **Step 2: Run**

```bash
go test ./internal/domain/model/schema/... -run 'TestExtendCompleteness' -v
```

Expected: PASS. If it fails with "op catalog is incomplete", either extend the catalog (new op-kind) or narrow `Extend`.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/properties_test.go && git commit -m "test(schema): Extend-completeness gate — every Extend output round-trips through Diff+Apply

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task B7: Typed `UnknownSchemaElement` error class

**Files:**
- Modify: `internal/domain/model/schema/validate.go`
- Modify: `internal/domain/model/schema/validate_test.go`

- [ ] **Step 1: Write failing test for the classifier**

Append to `internal/domain/model/schema/validate_test.go`:

```go
func TestHasUnknownSchemaElement_TypedSentinel(t *testing.T) {
	model := mustUnmarshalNode(t, `{"type":"object","properties":{"name":{"type":"string"}}}`)
	doc := map[string]any{"name": "alice", "mystery": 123}
	errs := Validate(model, doc)
	if len(errs) == 0 {
		t.Fatal("expected validation error for unknown field 'mystery'")
	}
	if !HasUnknownSchemaElement(ValidationErrors(errs)) {
		t.Errorf("classifier should flag unknown-field error; errs=%v", errs)
	}
}

func TestHasUnknownSchemaElement_IgnoresTypeMismatch(t *testing.T) {
	model := mustUnmarshalNode(t, `{"type":"object","properties":{"age":{"type":"integer"}}}`)
	doc := map[string]any{"age": "not-an-int"}
	errs := Validate(model, doc)
	if len(errs) == 0 {
		t.Fatal("expected validation error for type mismatch")
	}
	if HasUnknownSchemaElement(ValidationErrors(errs)) {
		t.Errorf("classifier must not flag pure type mismatch; errs=%v", errs)
	}
}

func TestHasUnknownSchemaElement_MixedMultiError(t *testing.T) {
	model := mustUnmarshalNode(t, `{"type":"object","properties":{"age":{"type":"integer"}}}`)
	doc := map[string]any{"age": "not-an-int", "mystery": 1}
	errs := Validate(model, doc)
	if !HasUnknownSchemaElement(ValidationErrors(errs)) {
		t.Errorf("classifier must flag mixed errors that contain any unknown-element; errs=%v", errs)
	}
}
```

- [ ] **Step 2: Run — expect fail (compile or behavioural)**

```bash
go test ./internal/domain/model/schema/... -run 'TestHasUnknownSchemaElement' -v
```

Expected: compile error (`undefined: HasUnknownSchemaElement`, `ValidationErrors`) or wrong-value failures.

- [ ] **Step 3: Extend `validate.go`**

Open `internal/domain/model/schema/validate.go` and:

1. Introduce an error-kind constant and attach it to `ValidationError`:

```go
// ValidationErrorKind classifies the failure. Consumers use HasUnknownSchemaElement
// to decide whether a stale cached schema might be responsible, triggering the
// bounded refresh-on-stale flow in internal/domain/entity/handler.go.
type ValidationErrorKind int

const (
	ErrKindOther ValidationErrorKind = iota
	ErrKindUnknownElement
)

// ValidationError describes a single validation failure at a specific path.
type ValidationError struct {
	Path    string
	Message string
	Kind    ValidationErrorKind
}
```

2. Where the validator currently emits "unknown field" — in `validateObject`, the block that iterates over data keys not present in the model — set `Kind: ErrKindUnknownElement`. Example patch:

```go
for name := range obj {
    if _, ok := children[name]; !ok {
        errs = append(errs, ValidationError{
            Path:    joinPath(path, name),
            Message: fmt.Sprintf("unknown field %q", name),
            Kind:    ErrKindUnknownElement,
        })
    }
}
```

3. Add the classifier and a small wrapper:

```go
// ValidationErrors wraps a slice of ValidationError as an error so callers
// can pass it through error chains. HasUnknownSchemaElement unwraps back out.
type ValidationErrors []ValidationError

func (v ValidationErrors) Error() string {
	msgs := make([]string, len(v))
	for i, e := range v {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}

// HasUnknownSchemaElement returns true if err carries at least one
// ValidationError of kind ErrKindUnknownElement.
func HasUnknownSchemaElement(err error) bool {
	if err == nil {
		return false
	}
	var ves ValidationErrors
	if errors.As(err, &ves) {
		for _, e := range ves {
			if e.Kind == ErrKindUnknownElement {
				return true
			}
		}
	}
	return false
}
```

Add `"errors"` to the file's import group.

- [ ] **Step 4: Run — tests pass**

```bash
go test ./internal/domain/model/schema/... -v
```

Expected: all PASS (the new classifier + pre-existing validator tests).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/validate.go internal/domain/model/schema/validate_test.go && git commit -m "feat(schema): typed UnknownSchemaElement validation-error class + classifier

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase C — Migrations: collapse to 0001 per plugin; add `model_schema_extensions`

### Task C1: Collapse Postgres migrations into a single `0001`

**Files:**
- Replace: `plugins/postgres/migrations/0000{1..5}_*.{up,down}.sql`
- Create: `plugins/postgres/migrations/000001_initial_schema.up.sql`
- Create: `plugins/postgres/migrations/000001_initial_schema.down.sql`

- [ ] **Step 1: Gather current schema**

```bash
cat plugins/postgres/migrations/000001_initial_schema.up.sql plugins/postgres/migrations/000002_rls_no_force.up.sql plugins/postgres/migrations/000003_search_jobs.up.sql plugins/postgres/migrations/000004_search_jobs_nullable.up.sql plugins/postgres/migrations/000005_search_jobs_tenant_pk.up.sql 2>&1 > /tmp/postgres-combined.sql && head -100 /tmp/postgres-combined.sql
```

Read the combined file end-to-end so you understand every table and policy.

- [ ] **Step 2: Write the new consolidated up migration**

Replace the content of `plugins/postgres/migrations/000001_initial_schema.up.sql` with the full, post-collapse schema. Build it by copying content from 000001 through 000005 into one file, applying each later migration's changes directly (e.g., 000005's redesigned `search_jobs` primary key replaces 000003's), and then **appending** the new `model_schema_extensions` table:

```sql
-- (contents of prior 000001 through 000005, merged as if every migration
-- had already applied, with:
--   - 000002's RLS changes folded into the CREATE POLICY statements
--   - 000003/000004/000005's search_jobs table defined in its final form
-- Implementation note for the engineer: do NOT copy-paste five files
-- in sequence; apply each later change to the earlier DDL so the result
-- is a single coherent CREATE.)

-- ... consolidated schema here ...

-- --------------------------------------------------------------------
-- Model schema extensions — append-only log of additive schema deltas.
-- See docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md §4.4.
-- --------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_schema_extensions (
    tenant_id     TEXT     NOT NULL,
    model_name    TEXT     NOT NULL,
    model_version TEXT     NOT NULL,
    seq           BIGSERIAL,
    kind          TEXT     NOT NULL CHECK (kind IN ('delta', 'savepoint')),
    payload       JSONB    NOT NULL,
    tx_id         TEXT     NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, model_name, model_version, seq)
);

CREATE INDEX IF NOT EXISTS model_schema_extensions_lookup
    ON model_schema_extensions (tenant_id, model_name, model_version, seq DESC);

ALTER TABLE model_schema_extensions ENABLE ROW LEVEL SECURITY;

CREATE POLICY model_schema_extensions_tenant_isolation
    ON model_schema_extensions
    USING (tenant_id = current_setting('app.current_tenant', true));
```

- [ ] **Step 3: Write the down migration**

Create `plugins/postgres/migrations/000001_initial_schema.down.sql` — mirror the up in reverse order (drop `model_schema_extensions` first, then each earlier table). Reuse the `DROP TABLE IF EXISTS` statements that existed in the prior `000001..000005` down migrations, adjusted for the final consolidated shape.

- [ ] **Step 4: Delete the redundant migration files**

```bash
rm plugins/postgres/migrations/000002_rls_no_force.up.sql plugins/postgres/migrations/000002_rls_no_force.down.sql plugins/postgres/migrations/000003_search_jobs.up.sql plugins/postgres/migrations/000003_search_jobs.down.sql plugins/postgres/migrations/000004_search_jobs_nullable.up.sql plugins/postgres/migrations/000004_search_jobs_nullable.down.sql plugins/postgres/migrations/000005_search_jobs_tenant_pk.up.sql plugins/postgres/migrations/000005_search_jobs_tenant_pk.down.sql
```

- [ ] **Step 5: Run the postgres plugin migrate test to verify up-then-down still works**

```bash
cd plugins/postgres && go test -run TestMigrate -v ./...
```

Expected: PASS. The migrate compat test may need updating — if it expects specific migration versions, mirror the collapse there too.

- [ ] **Step 6: Commit**

```bash
git add plugins/postgres/migrations/ && git commit -m "chore(postgres,migrate): collapse 000001..000005 into a single 0001 + add model_schema_extensions

Pre-release greenfield — no deployments to carry forward. New initial
schema is the net result of all prior migrations plus the append-only
model_schema_extensions table defined in the spec.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task C2: Update SQLite migration (already 0001) — add `model_schema_extensions`

**Files:**
- Modify: `plugins/sqlite/migrations/000001_initial_schema.up.sql`
- Modify: `plugins/sqlite/migrations/000001_initial_schema.down.sql`

- [ ] **Step 1: Append the new table to the up migration**

At the end of `plugins/sqlite/migrations/000001_initial_schema.up.sql`:

```sql
-- --------------------------------------------------------------------
-- Model schema extensions — append-only log of additive schema deltas.
-- See docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md §4.4.
-- SQLite is single-node by design; this table exists for SPI parity
-- and for the conformance tests. Fold is trivial since there is only
-- one writer.
-- --------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_schema_extensions (
    tenant_id     TEXT     NOT NULL,
    model_name    TEXT     NOT NULL,
    model_version TEXT     NOT NULL,
    seq           INTEGER  NOT NULL,  -- plugin-assigned monotonic counter
    kind          TEXT     NOT NULL CHECK (kind IN ('delta', 'savepoint')),
    payload       BLOB     NOT NULL,
    tx_id         TEXT     NOT NULL,
    created_at    TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (tenant_id, model_name, model_version, seq)
);

CREATE INDEX IF NOT EXISTS model_schema_extensions_lookup
    ON model_schema_extensions (tenant_id, model_name, model_version, seq DESC);
```

- [ ] **Step 2: Prepend drop to the down migration**

At the start of `plugins/sqlite/migrations/000001_initial_schema.down.sql`:

```sql
DROP INDEX IF EXISTS model_schema_extensions_lookup;
DROP TABLE IF EXISTS model_schema_extensions;
```

- [ ] **Step 3: Run the sqlite plugin migrate test**

```bash
cd plugins/sqlite && go test -run TestMigrate -v ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add plugins/sqlite/migrations/ && git commit -m "chore(sqlite,migrate): add model_schema_extensions table

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase D — Plugin implementations

### Task D1: Postgres `ExtendSchema` + `Get` fold — ApplyFunc injection

**Files:**
- Modify: `plugins/postgres/store_factory.go`
- Modify: `plugins/postgres/plugin.go`
- Modify: `plugins/postgres/model_store.go`
- Create: `plugins/postgres/model_extensions.go`
- Create: `plugins/postgres/model_extensions_test.go`

- [ ] **Step 1: Write a failing conformance-style test for ExtendSchema append**

Create `plugins/postgres/model_extensions_test.go`:

```go
//go:build !short

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/postgres"
	"github.com/cyoda-platform/cyoda-go/plugins/postgres/internal/testsupport"
)

func TestExtendSchema_AppendsDelta_VisibleAfterCommit(t *testing.T) {
	env := testsupport.NewPGEnv(t)
	defer env.Cleanup()

	factory, err := postgres.NewStoreFactory(env.Pool, postgres.Config{
		ApplyFunc: testsupport.TestApplyFunc(t),
	})
	if err != nil {
		t.Fatalf("NewStoreFactory: %v", err)
	}

	ctx := testsupport.WithTenantAndUser(context.Background(), "t1")
	// Seed a locked model with no extensions.
	ms, err := factory.ModelStore(ctx)
	if err != nil {
		t.Fatalf("ModelStore: %v", err)
	}
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	base := testsupport.LockedModelDescriptor(ref, `{"type":"object","properties":{"title":{"type":"string"}}}`, "STRUCTURAL")
	if err := ms.Save(ctx, base); err != nil {
		t.Fatalf("Save base: %v", err)
	}
	if err := ms.Lock(ctx, ref); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// Apply a delta adding `isbn`.
	ops := []map[string]any{{"kind": "add_property", "path": "/properties", "name": "isbn", "payload": json.RawMessage(`{"type":"string"}`)}}
	deltaBytes, _ := json.Marshal(ops)
	tx, txCtx, err := factory.TransactionManager().Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := ms.ExtendSchema(txCtx, ref, spi.SchemaDelta(deltaBytes)); err != nil {
		t.Fatalf("ExtendSchema: %v", err)
	}
	if err := factory.TransactionManager().Commit(txCtx, tx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Read back — should see title + isbn.
	got, err := ms.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !testsupport.SchemaHasProperty(t, got.Schema, "isbn") {
		t.Errorf("expected folded schema to contain 'isbn'; got %s", got.Schema)
	}
	if !testsupport.SchemaHasProperty(t, got.Schema, "title") {
		t.Errorf("expected folded schema to retain 'title'; got %s", got.Schema)
	}
}

func TestExtendSchema_RolledBack_NotVisible(t *testing.T) {
	env := testsupport.NewPGEnv(t)
	defer env.Cleanup()

	factory, err := postgres.NewStoreFactory(env.Pool, postgres.Config{
		ApplyFunc: testsupport.TestApplyFunc(t),
	})
	if err != nil {
		t.Fatalf("NewStoreFactory: %v", err)
	}
	ctx := testsupport.WithTenantAndUser(context.Background(), "t1")
	ms, _ := factory.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	base := testsupport.LockedModelDescriptor(ref, `{"type":"object","properties":{"title":{"type":"string"}}}`, "STRUCTURAL")
	_ = ms.Save(ctx, base)
	_ = ms.Lock(ctx, ref)

	tx, txCtx, _ := factory.TransactionManager().Begin(ctx)
	_ = ms.ExtendSchema(txCtx, ref, spi.SchemaDelta(`[{"kind":"add_property","path":"/properties","name":"isbn","payload":{"type":"string"}}]`))
	_ = factory.TransactionManager().Rollback(txCtx, tx)

	got, _ := ms.Get(ctx, ref)
	if testsupport.SchemaHasProperty(t, got.Schema, "isbn") {
		t.Errorf("rolled-back delta must not be visible; schema = %s", got.Schema)
	}
}
```

**Note for the implementer.** `plugins/postgres/internal/testsupport` is a new helper package. Create it (below) alongside this task.

- [ ] **Step 2: Create the `testsupport` helper**

Create `plugins/postgres/internal/testsupport/env.go`:

```go
package testsupport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// PGEnv holds a running postgres for a single test. Backed by
// the project's existing testcontainers helper in plugins/postgres.
type PGEnv struct {
	Pool    *pgxpool.Pool
	cleanup func()
}

func (e *PGEnv) Cleanup() { e.cleanup() }

// NewPGEnv boots a throwaway postgres. Implementation reuses the
// same testcontainer bootstrap used by conformance_test.go; wire it
// up by copying the Setup helper from that file, or by calling it
// directly if it's already exported.
func NewPGEnv(t *testing.T) *PGEnv {
	t.Helper()
	// TODO(plan-D1): replace with a direct call to the existing
	// conformance_test.go testcontainer helper. If that helper is
	// unexported, promote it to internal/testsupport in a prior
	// refactor step.
	panic("NewPGEnv: wire to existing testcontainer helper in plugins/postgres")
}

func WithTenantAndUser(ctx context.Context, tenant string) context.Context {
	uc := &spi.UserContext{
		UserID: "test-user",
		Tenant: spi.TenantContext{ID: spi.TenantID(tenant)},
	}
	return spi.WithUserContext(ctx, uc)
}

func LockedModelDescriptor(ref spi.ModelRef, schemaJSON string, changeLevel string) *spi.ModelDescriptor {
	return &spi.ModelDescriptor{
		Ref:         ref,
		State:       spi.ModelUnlocked, // caller Locks afterward
		ChangeLevel: spi.ChangeLevel(changeLevel),
		Schema:      []byte(schemaJSON),
		UpdateDate:  time.Now().UTC(),
	}
}

func SchemaHasProperty(t *testing.T, schemaBytes []byte, name string) bool {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(schemaBytes, &root); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := root["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = props[name]
	return ok
}

// TestApplyFunc returns an ApplyFunc that calls the main-repo
// schema.Apply — identical to production wiring so conformance tests
// verify the same fold path.
func TestApplyFunc(t *testing.T) func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
	t.Helper()
	return func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
		// Intentional cross-module dep only in the helper, not in plugin code.
		return applyViaSchemaPackage(base, delta)
	}
}

// applyViaSchemaPackage is defined in a sibling file (helper_apply_internal_test.go)
// to avoid polluting the production build path with an internal/domain/model/schema
// import.
var applyViaSchemaPackage func(base []byte, delta spi.SchemaDelta) ([]byte, error)

// Strings helper used by a few assertion sites.
func Norm(s string) string { return strings.TrimSpace(s) }
```

Create `plugins/postgres/internal/testsupport/helper_apply_internal_test.go` (note: `_test.go` suffix keeps the main-repo import out of the production binary):

```go
package testsupport

import (
	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

func init() {
	applyViaSchemaPackage = func(baseBytes []byte, delta spi.SchemaDelta) ([]byte, error) {
		base, err := schema.Unmarshal(baseBytes)
		if err != nil {
			return nil, err
		}
		folded, err := schema.Apply(base, delta)
		if err != nil {
			return nil, err
		}
		return schema.Marshal(folded)
	}
}
```

- [ ] **Step 3: Run — confirm failure (missing ExtendSchema)**

```bash
go test ./plugins/postgres/... -run 'TestExtendSchema_' -v
```

Expected: compile error (`ms.ExtendSchema` undefined or `ApplyFunc` missing on `Config`).

- [ ] **Step 4: Add `Config.ApplyFunc` and thread through factory**

In `plugins/postgres/store_factory.go`, extend the `Config` struct:

```go
// Config for the Postgres StoreFactory. ApplyFunc must be supplied when
// ModelStore is used (any ExtendSchema call without it is a programmer
// bug — panics on Get-fold rather than returning an error, because the
// caller should have wired it at init). See cmd/cyoda/main.go for
// production wiring.
type Config struct {
	// ... existing fields ...
	ApplyFunc func(base []byte, delta spi.SchemaDelta) ([]byte, error)
}
```

Plumb it into the `modelStore` constructor. Modify `ModelStore(ctx)`:

```go
func (f *StoreFactory) ModelStore(ctx context.Context) (spi.ModelStore, error) {
	tenant, err := resolveTenant(ctx)
	if err != nil {
		return nil, err
	}
	q := resolveQuerier(ctx, f.pool, f.txm)
	return &modelStore{
		q:         q,
		tenantID:  tenant,
		applyFunc: f.cfg.ApplyFunc,
	}, nil
}
```

- [ ] **Step 5: Add `ExtendSchema` to `plugins/postgres/model_store.go`**

Add the method:

```go
// ExtendSchema appends a delta row to model_schema_extensions. If the
// append pushes the log past a savepoint boundary (every 64 rows) a
// second SAVEPOINT row is inserted in the same tx/batch carrying the
// folded schema as of this delta.
//
// Both inserts share the ambient pgx.Tx so they are atomic with the
// entity transaction that wrapped this call.
func (s *modelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if len(delta) == 0 {
		return nil // no-op caller protection
	}
	tid := string(s.tenantID)
	txID := ""
	if tx := spi.GetTransaction(ctx); tx != nil {
		txID = tx.ID
	}

	// 1. Insert the delta row.
	var newSeq int64
	err := s.q.QueryRow(ctx, `
		INSERT INTO model_schema_extensions (tenant_id, model_name, model_version, kind, payload, tx_id)
		VALUES ($1, $2, $3, 'delta', $4, $5)
		RETURNING seq`,
		tid, ref.EntityName, ref.ModelVersion, []byte(delta), txID).Scan(&newSeq)
	if err != nil {
		return fmt.Errorf("failed to insert schema delta: %w", classifyError(err))
	}

	// 2. Savepoint every 64 deltas. seq % 64 == 0 is chosen in §4.4.
	const savepointInterval = 64
	if newSeq%savepointInterval != 0 {
		return nil
	}
	folded, err := s.foldLocked(ctx, ref)
	if err != nil {
		return fmt.Errorf("fold for savepoint: %w", err)
	}
	if _, err := s.q.Exec(ctx, `
		INSERT INTO model_schema_extensions (tenant_id, model_name, model_version, kind, payload, tx_id)
		VALUES ($1, $2, $3, 'savepoint', $4, $5)`,
		tid, ref.EntityName, ref.ModelVersion, folded, txID); err != nil {
		return fmt.Errorf("insert savepoint: %w", classifyError(err))
	}
	return nil
}
```

- [ ] **Step 6: Rewrite `Get` to fold the log**

Replace the existing `Get` implementation with:

```go
func (s *modelStore) Get(ctx context.Context, ref spi.ModelRef) (*spi.ModelDescriptor, error) {
	// 1. Read stable metadata.
	var raw []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("model %s not found: %w", ref, spi.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get model %s: %w", ref, err)
	}
	desc, err := unmarshalModelDoc(raw)
	if err != nil {
		return nil, err
	}

	// 2. Fold the extension log.
	folded, err := s.foldLocked(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("fold extension log: %w", err)
	}
	desc.Schema = folded
	return desc, nil
}

// foldLocked returns the fully-folded schema for ref. Starts from the
// most recent savepoint row (if any), else from the base schema, and
// applies every subsequent delta in seq order via the injected ApplyFunc.
func (s *modelStore) foldLocked(ctx context.Context, ref spi.ModelRef) ([]byte, error) {
	if s.applyFunc == nil {
		return nil, fmt.Errorf("ModelStore.ApplyFunc not wired — see cmd/cyoda/main.go")
	}

	// Find the most recent savepoint.
	var savepointSeq int64
	var savepointPayload []byte
	err := s.q.QueryRow(ctx, `
		SELECT seq, payload FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND kind = 'savepoint'
		ORDER BY seq DESC LIMIT 1`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&savepointSeq, &savepointPayload)
	switch {
	case err == pgx.ErrNoRows:
		savepointSeq = 0
		savepointPayload = nil
	case err != nil:
		return nil, fmt.Errorf("savepoint lookup: %w", err)
	}

	// Start from savepoint if present, else from models.base_schema.
	current := savepointPayload
	if current == nil {
		var baseRaw []byte
		if err := s.q.QueryRow(ctx, `SELECT doc->'schema' FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
			string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&baseRaw); err != nil {
			return nil, fmt.Errorf("base schema lookup: %w", err)
		}
		current = baseRaw
	}

	// Scan deltas after the savepoint.
	rows, err := s.q.Query(ctx, `
		SELECT payload FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3
		  AND kind = 'delta' AND seq > $4
		ORDER BY seq ASC`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion, savepointSeq)
	if err != nil {
		return nil, fmt.Errorf("delta scan: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var deltaBytes []byte
		if err := rows.Scan(&deltaBytes); err != nil {
			return nil, fmt.Errorf("scan delta: %w", err)
		}
		current, err = s.applyFunc(current, spi.SchemaDelta(deltaBytes))
		if err != nil {
			return nil, fmt.Errorf("apply delta: %w", err)
		}
	}
	return current, rows.Err()
}
```

- [ ] **Step 7: Run — tests PASS**

```bash
go test ./plugins/postgres/... -run 'TestExtendSchema_' -v
```

Expected: PASS. If `NewPGEnv` is still panicking because the testcontainer helper isn't wired, resolve that by promoting the existing helper into `internal/testsupport`.

- [ ] **Step 8: Commit**

```bash
git add plugins/postgres/{store_factory.go,model_store.go,internal/testsupport/} && git commit -m "feat(postgres): ExtendSchema append + fold-on-read with plugin-internal savepoints every 64 rows

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task D2: Postgres `Save` / `Unlock` lifecycle with dev-time `DELETE count == 0` assertion

**Files:**
- Modify: `plugins/postgres/model_store.go`

- [ ] **Step 1: Write failing tests**

In `plugins/postgres/model_extensions_test.go` append:

```go
func TestSave_ClearsExtensionLog(t *testing.T) {
	env := testsupport.NewPGEnv(t)
	defer env.Cleanup()
	factory, _ := postgres.NewStoreFactory(env.Pool, postgres.Config{ApplyFunc: testsupport.TestApplyFunc(t)})
	ctx := testsupport.WithTenantAndUser(context.Background(), "t1")
	ms, _ := factory.ModelStore(ctx)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}

	_ = ms.Save(ctx, testsupport.LockedModelDescriptor(ref, `{"type":"object","properties":{"a":{"type":"string"}}}`, "STRUCTURAL"))
	_ = ms.Lock(ctx, ref)

	tx, txCtx, _ := factory.TransactionManager().Begin(ctx)
	_ = ms.ExtendSchema(txCtx, ref, spi.SchemaDelta(`[{"kind":"add_property","path":"/properties","name":"b","payload":{"type":"integer"}}]`))
	_ = factory.TransactionManager().Commit(txCtx, tx)

	// Unlock drains via assertion-clear path.
	if err := ms.Unlock(ctx, ref); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	// Now Save a fresh schema — must succeed, log must be empty afterwards.
	_ = ms.Save(ctx, testsupport.LockedModelDescriptor(ref, `{"type":"object","properties":{"z":{"type":"string"}}}`, "STRUCTURAL"))
	// Verify log is empty (no deltas survive Save).
	count := testsupport.CountExtensionRows(t, env.Pool, "t1", ref)
	if count != 0 {
		t.Errorf("after Save, expected 0 extension rows, got %d", count)
	}
}
```

Add the helper to `testsupport`:

```go
// CountExtensionRows returns the number of model_schema_extensions rows
// for the given tenant/ref, bypassing RLS via a direct pool query.
func CountExtensionRows(t *testing.T, pool *pgxpool.Pool, tenantID string, ref spi.ModelRef) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SET LOCAL ROLE NONE;
		SELECT count(*) FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		tenantID, ref.EntityName, ref.ModelVersion).Scan(&n)
	if err != nil {
		t.Fatalf("count extensions: %v", err)
	}
	return n
}
```

- [ ] **Step 2: Run — expect fail (Save doesn't touch the log today)**

```bash
go test ./plugins/postgres/... -run TestSave_ClearsExtensionLog -v
```

Expected: fail — `count != 0`.

- [ ] **Step 3: Extend `Save` and `Unlock` in `model_store.go`**

Modify `Save`:

```go
func (s *modelStore) Save(ctx context.Context, desc *spi.ModelDescriptor) error {
	// ... existing INSERT / UPSERT of the models row ...

	// Sanity: Save requires UNLOCKED by contract. But we defensively clear
	// any extension rows at this point — the state machine should have
	// prevented concurrent writers, but an operator-misuse mode could
	// have left orphans.
	tag, err := s.q.Exec(ctx, `
		DELETE FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), desc.Ref.EntityName, desc.Ref.ModelVersion)
	if err != nil {
		return fmt.Errorf("clear extension log: %w", err)
	}
	if buildIsDev() && tag.RowsAffected() != 0 {
		// Dev-time misuse guard: §2 operator contract.
		return fmt.Errorf("Save found %d stale extension rows; this is an operator-misuse indicator (see CONSISTENCY.md Operator Contract)", tag.RowsAffected())
	}
	if !buildIsDev() && tag.RowsAffected() != 0 {
		slog.Warn("Save cleared stale extension rows",
			"pkg", "postgres", "tenant", s.tenantID, "ref", desc.Ref, "count", tag.RowsAffected())
	}
	return nil
}
```

Modify `Unlock`:

```go
func (s *modelStore) Unlock(ctx context.Context, ref spi.ModelRef) error {
	if err := s.updateStateField(ctx, ref, spi.ModelUnlocked, "unlock"); err != nil {
		return err
	}
	tag, err := s.q.Exec(ctx, `
		DELETE FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion)
	if err != nil {
		return fmt.Errorf("unlock: clear extensions: %w", err)
	}
	if buildIsDev() && tag.RowsAffected() != 0 {
		return fmt.Errorf("Unlock found %d live extension rows; operator misuse (see CONSISTENCY.md Operator Contract)", tag.RowsAffected())
	}
	if !buildIsDev() && tag.RowsAffected() != 0 {
		slog.Warn("Unlock drained stale extension rows",
			"pkg", "postgres", "tenant", s.tenantID, "ref", ref, "count", tag.RowsAffected())
	}
	return nil
}

// buildIsDev reports whether this build is a development build where
// operator-contract assertions are fatal. Controlled by the standard
// cyoda-go debug build flag; defaults to false in release builds.
func buildIsDev() bool { return debugMode }
```

Create or reuse `plugins/postgres/build_mode.go`:

```go
package postgres

// debugMode toggles dev-time operator-contract assertions. Wired from
// cmd/cyoda/main.go based on the CYODA_DEBUG env var. Falls back to
// false for release builds.
var debugMode = false

// SetDebugMode is wired at init by the main binary.
func SetDebugMode(on bool) { debugMode = on }
```

- [ ] **Step 4: Run — test passes**

```bash
go test ./plugins/postgres/... -run TestSave_ClearsExtensionLog -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add plugins/postgres/ && git commit -m "feat(postgres): Save/Unlock clear extension log + dev-time assertion

Operator contract (spec §2): writers drained before Unlock. The
dev-time DELETE-RETURNING-count assertion catches operator misuse
in development; production logs a WARN and continues.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task D3: SQLite `ExtendSchema` — thin apply-in-place

**Files:**
- Modify: `plugins/sqlite/store_factory.go`
- Modify: `plugins/sqlite/model_store.go`
- Modify: `plugins/sqlite/plugin.go`
- Create: `plugins/sqlite/model_store_extendschema_test.go`

- [ ] **Step 1: Write failing test**

Create `plugins/sqlite/model_store_extendschema_test.go`:

```go
package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/plugins/sqlite"
)

func TestSQLiteExtendSchema_AppliesInPlace(t *testing.T) {
	factory := newTestFactory(t) // existing conformance helper
	ctx := withTenant(t, "t1")
	ms, _ := factory.ModelStore(ctx)

	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	_ = ms.Save(ctx, lockedDescriptor(ref, `{"type":"object","properties":{"title":{"type":"string"}}}`, "STRUCTURAL"))
	_ = ms.Lock(ctx, ref)

	delta, _ := json.Marshal([]map[string]any{{
		"kind": "add_property", "path": "/properties", "name": "isbn",
		"payload": json.RawMessage(`{"type":"string"}`),
	}})
	if err := ms.ExtendSchema(ctx, ref, spi.SchemaDelta(delta)); err != nil {
		t.Fatalf("ExtendSchema: %v", err)
	}
	got, _ := ms.Get(ctx, ref)
	if !schemaHasProperty(t, got.Schema, "isbn") {
		t.Errorf("expected isbn in schema; got %s", got.Schema)
	}
}
```

The helpers `newTestFactory`, `withTenant`, `lockedDescriptor`, `schemaHasProperty` live in the existing conformance test file or should be added there.

- [ ] **Step 2: Run — expect fail**

```bash
go test ./plugins/sqlite/... -run TestSQLiteExtendSchema_AppliesInPlace -v
```

Expected: compile error (`ms.ExtendSchema` undefined).

- [ ] **Step 3: Add `ApplyFunc` to `plugins/sqlite/store_factory.go`**

```go
type Config struct {
	// ... existing ...
	ApplyFunc func(base []byte, delta spi.SchemaDelta) ([]byte, error)
}
```

Pass `f.cfg.ApplyFunc` into every `modelStore` construction in `ModelStore(ctx)`.

- [ ] **Step 4: Add the field + method to `plugins/sqlite/model_store.go`**

Add field:

```go
type modelStore struct {
	db        *sql.DB
	tenantID  spi.TenantID
	applyFunc func(base []byte, delta spi.SchemaDelta) ([]byte, error)
}
```

Add method:

```go
// ExtendSchema applies the delta directly to the stored schema in one
// UPDATE. SQLite is single-node-only — the append-and-fold pattern from
// Postgres is not needed here. The effect is equivalent to one atomic
// append + fold.
func (s *modelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if len(delta) == 0 {
		return nil
	}
	if s.applyFunc == nil {
		return fmt.Errorf("sqlite modelStore: ApplyFunc not wired")
	}

	// Read current schema inside a tx for atomicity against concurrent admin ops.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT json(doc) FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&raw)
	if err != nil {
		return fmt.Errorf("sqlite modelStore ExtendSchema: load: %w", err)
	}
	var doc modelDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("unmarshal doc: %w", err)
	}

	folded, err := s.applyFunc(doc.Schema, delta)
	if err != nil {
		return fmt.Errorf("apply delta: %w", err)
	}
	doc.Schema = folded
	updated, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("remarshal doc: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE models SET doc = jsonb(?) WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		updated, string(s.tenantID), ref.EntityName, ref.ModelVersion); err != nil {
		return fmt.Errorf("update doc: %w", err)
	}
	return tx.Commit()
}
```

- [ ] **Step 5: Run — tests pass**

```bash
go test ./plugins/sqlite/... -run TestSQLiteExtendSchema -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add plugins/sqlite/ && git commit -m "feat(sqlite): ExtendSchema via in-place apply (single-node plugin parity)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task D4: Memory `ExtendSchema` — thin apply-in-place

**Files:**
- Modify: `plugins/memory/store_factory.go`
- Modify: `plugins/memory/model_store.go`
- Create: `plugins/memory/model_store_extendschema_test.go`

- [ ] **Step 1: Write failing test**

Create `plugins/memory/model_store_extendschema_test.go` (pattern identical to SQLite's test in Task D3, substitute the memory factory bootstrap).

- [ ] **Step 2: Run — expect fail**

```bash
go test ./plugins/memory/... -run TestMemoryExtendSchema -v
```

Expected: compile error.

- [ ] **Step 3: Add `ApplyFunc` to memory factory + add method to model_store**

Mirror Task D3's structural changes against `plugins/memory/store_factory.go` and `plugins/memory/model_store.go`. The apply is a direct in-place mutation of `s.factory.modelData[s.tenant][ref].Schema`:

```go
func (s *ModelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if len(delta) == 0 {
		return nil
	}
	if s.applyFunc == nil {
		return fmt.Errorf("memory modelStore: ApplyFunc not wired")
	}
	s.factory.modelMu.Lock()
	defer s.factory.modelMu.Unlock()
	entry, ok := s.factory.modelData[s.tenant][ref]
	if !ok {
		return fmt.Errorf("model %s not found: %w", ref, spi.ErrNotFound)
	}
	folded, err := s.applyFunc(entry.Schema, delta)
	if err != nil {
		return err
	}
	entry.Schema = folded
	return nil
}
```

- [ ] **Step 4: Run — PASS**

```bash
go test ./plugins/memory/... -v
```

- [ ] **Step 5: Commit**

```bash
git add plugins/memory/ && git commit -m "feat(memory): ExtendSchema via in-place apply (single-node plugin parity)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task D5: Per-plugin conformance tests for ExtendSchema

**Files:**
- Modify: `plugins/postgres/conformance_test.go`
- Modify: `plugins/sqlite/conformance_test.go`
- Modify: `plugins/memory/conformance_test.go`

- [ ] **Step 1: Add a shared conformance helper**

In each plugin's `conformance_test.go`, add a test named `TestConformance_ExtendSchema` that exercises:

- Empty delta → no-op.
- Single `add_property` delta → `Get` returns a folded schema containing the new property.
- Two concurrent `add_property` deltas on different properties → both land (verify via two separate `ExtendSchema` calls within separate transactions, then `Get`).

Copy the exact test body between plugins to keep them source-identical. Factor into a shared helper under `plugins/<plug>/conformance_test.go` rather than a cross-plugin package — each plugin has its own `go.mod`, so factoring to `internal/testsupport` in the main repo would not be importable from plugin tests.

- [ ] **Step 2: Run per plugin**

```bash
for p in memory sqlite postgres; do echo "--- $p ---"; cd plugins/$p && go test -run TestConformance_ExtendSchema -v; cd -; done
```

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add plugins/*/conformance_test.go && git commit -m "test(plugins): ExtendSchema conformance across all backends

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase E — Caching decorator

### Task E1: `CachingModelStore` — cache-any-LOCKED + TTL with ±10 % jitter

**Files:**
- Create: `internal/cluster/modelcache/cache.go`
- Create: `internal/cluster/modelcache/cache_test.go`
- Create: `internal/cluster/modelcache/payload.go`
- Create: `internal/cluster/modelcache/payload_test.go`

- [ ] **Step 1: Failing test for admission policy + TTL**

Create `internal/cluster/modelcache/cache_test.go`:

```go
package modelcache_test

import (
	"context"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/modelcache"
)

// stubStore counts Get calls so we can verify caching actually intercepts.
type stubStore struct {
	spi.ModelStore
	gets int
	desc *spi.ModelDescriptor
}

func (s *stubStore) Get(_ context.Context, _ spi.ModelRef) (*spi.ModelDescriptor, error) {
	s.gets++
	return s.desc, nil
}

type manualClock struct{ now time.Time }

func (c *manualClock) Now() time.Time        { return c.now }
func (c *manualClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func locked(ref spi.ModelRef, level spi.ChangeLevel) *spi.ModelDescriptor {
	return &spi.ModelDescriptor{Ref: ref, State: spi.ModelLocked, ChangeLevel: level, Schema: []byte(`{"type":"object"}`)}
}

func TestCache_AdmitsAnyLockedDescriptor(t *testing.T) {
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	inner := &stubStore{desc: locked(ref, "STRUCTURAL")}
	clk := &manualClock{now: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)}
	c := modelcache.New(inner, nil, clk, time.Hour)

	_, _ = c.Get(context.Background(), ref)
	_, _ = c.Get(context.Background(), ref)
	if inner.gets != 1 {
		t.Errorf("expected 1 inner Get (second call cached), got %d", inner.gets)
	}
}

func TestCache_DoesNotAdmitUnlocked(t *testing.T) {
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	desc := &spi.ModelDescriptor{Ref: ref, State: spi.ModelUnlocked, ChangeLevel: "", Schema: []byte(`{"type":"object"}`)}
	inner := &stubStore{desc: desc}
	clk := &manualClock{now: time.Now()}
	c := modelcache.New(inner, nil, clk, time.Hour)

	_, _ = c.Get(context.Background(), ref)
	_, _ = c.Get(context.Background(), ref)
	if inner.gets != 2 {
		t.Errorf("expected 2 inner Gets (unlocked bypasses cache), got %d", inner.gets)
	}
}

func TestCache_TTLExpiryForcesReload(t *testing.T) {
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	inner := &stubStore{desc: locked(ref, "")}
	clk := &manualClock{now: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)}
	c := modelcache.New(inner, nil, clk, time.Hour)

	_, _ = c.Get(context.Background(), ref)
	clk.advance(2 * time.Hour) // past lease
	_, _ = c.Get(context.Background(), ref)
	if inner.gets != 2 {
		t.Errorf("expected 2 inner Gets after TTL expiry, got %d", inner.gets)
	}
}

func TestCache_JitterKeepsEntriesWithinBand(t *testing.T) {
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	inner := &stubStore{desc: locked(ref, "")}
	clk := &manualClock{now: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)}
	const lease = time.Hour
	c := modelcache.New(inner, nil, clk, lease)

	// Populate cache and read back expiry via internal introspection.
	_, _ = c.Get(context.Background(), ref)
	exp := c.EntryExpiresAt(ref)
	minExpected := clk.Now().Add(lease * 90 / 100)
	maxExpected := clk.Now().Add(lease * 110 / 100)
	if exp.Before(minExpected) || exp.After(maxExpected) {
		t.Errorf("expiry %v outside ±10%% jitter band [%v, %v]", exp, minExpected, maxExpected)
	}
}
```

**Note.** `EntryExpiresAt` is a test-only introspection method; see step 3.

- [ ] **Step 2: Run — expect fail**

```bash
go test ./internal/cluster/modelcache/... -v
```

Expected: compile error.

- [ ] **Step 3: Create `cache.go`**

```go
// Package modelcache provides a CachingModelStore decorator that
// memoizes LOCKED descriptors. Correctness rests on the catalog
// invariants (commutativity + validation-monotonicity) and the
// validator-path refresh-on-stale in internal/domain/entity/handler.go;
// gossip invalidation and the TTL lease are performance/hygiene
// layers. See docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md §4.5.
package modelcache

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// Clock abstracts time.Now so tests can drive expiry deterministically.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// CachingModelStore wraps an spi.ModelStore. Zero-value not safe — use New.
type CachingModelStore struct {
	inner       spi.ModelStore
	broadcaster spi.ClusterBroadcaster
	clock       Clock
	lease       time.Duration
	jitterRand  *rand.Rand

	mu      sync.RWMutex
	entries map[cacheKey]cacheEntry

	flight singleflight.Group
}

type cacheKey struct {
	tenant string
	ref    spi.ModelRef
}

type cacheEntry struct {
	desc      *spi.ModelDescriptor
	expiresAt time.Time
}

// New constructs a CachingModelStore. broadcaster may be nil for
// single-node deployments; clock may be nil to use time.Now.
func New(inner spi.ModelStore, broadcaster spi.ClusterBroadcaster, clk Clock, lease time.Duration) *CachingModelStore {
	if clk == nil {
		clk = realClock{}
	}
	c := &CachingModelStore{
		inner:       inner,
		broadcaster: broadcaster,
		clock:       clk,
		lease:       lease,
		jitterRand:  rand.New(rand.NewPCG(uint64(clk.Now().UnixNano()), 0xC0DA6060)),
		entries:     make(map[cacheKey]cacheEntry),
	}
	if broadcaster != nil {
		broadcaster.Subscribe(topicModelInvalidate, c.handleInvalidation)
	}
	return c
}

// EntryExpiresAt is test-only introspection for the jitter band check.
func (c *CachingModelStore) EntryExpiresAt(ref spi.ModelRef) time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, e := range c.entries {
		if k.ref == ref {
			return e.expiresAt
		}
	}
	return time.Time{}
}

// ---- spi.ModelStore pass-throughs with caching --------------------------

func (c *CachingModelStore) Get(ctx context.Context, ref spi.ModelRef) (*spi.ModelDescriptor, error) {
	key := cacheKey{tenant: tenantOf(ctx), ref: ref}
	if d := c.lookup(key); d != nil {
		return d, nil
	}
	desc, err := c.inner.Get(ctx, ref)
	if err != nil || desc == nil {
		return desc, err
	}
	if desc.State != spi.ModelLocked {
		return desc, nil // UNLOCKED bypasses cache
	}
	c.store(key, desc)
	return desc, nil
}

func (c *CachingModelStore) RefreshAndGet(ctx context.Context, ref spi.ModelRef) (*spi.ModelDescriptor, error) {
	key := cacheKey{tenant: tenantOf(ctx), ref: ref}
	c.evict(key)

	v, err, _ := c.flight.Do(flightKey(key), func() (any, error) {
		desc, err := c.inner.Get(ctx, ref)
		if err != nil || desc == nil {
			return desc, err
		}
		if desc.State == spi.ModelLocked {
			c.store(key, desc)
		}
		return desc, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*spi.ModelDescriptor), nil
}

func (c *CachingModelStore) Save(ctx context.Context, desc *spi.ModelDescriptor) error {
	if err := c.inner.Save(ctx, desc); err != nil {
		return err
	}
	c.invalidate(ctx, desc.Ref)
	return nil
}

func (c *CachingModelStore) Lock(ctx context.Context, ref spi.ModelRef) error {
	if err := c.inner.Lock(ctx, ref); err != nil {
		return err
	}
	c.invalidate(ctx, ref)
	return nil
}

func (c *CachingModelStore) Unlock(ctx context.Context, ref spi.ModelRef) error {
	if err := c.inner.Unlock(ctx, ref); err != nil {
		return err
	}
	c.invalidate(ctx, ref)
	return nil
}

func (c *CachingModelStore) SetChangeLevel(ctx context.Context, ref spi.ModelRef, level spi.ChangeLevel) error {
	if err := c.inner.SetChangeLevel(ctx, ref, level); err != nil {
		return err
	}
	c.invalidate(ctx, ref)
	return nil
}

func (c *CachingModelStore) Delete(ctx context.Context, ref spi.ModelRef) error {
	if err := c.inner.Delete(ctx, ref); err != nil {
		return err
	}
	c.invalidate(ctx, ref)
	return nil
}

func (c *CachingModelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if err := c.inner.ExtendSchema(ctx, ref, delta); err != nil {
		return err
	}
	c.invalidate(ctx, ref)
	return nil
}

// Pass through reads that don't benefit from caching.
func (c *CachingModelStore) GetAll(ctx context.Context) ([]spi.ModelRef, error) {
	return c.inner.GetAll(ctx)
}
func (c *CachingModelStore) IsLocked(ctx context.Context, ref spi.ModelRef) (bool, error) {
	return c.inner.IsLocked(ctx, ref)
}

// ---- internal helpers ---------------------------------------------------

func (c *CachingModelStore) lookup(key cacheKey) *spi.ModelDescriptor {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok {
		return nil
	}
	if c.clock.Now().After(e.expiresAt) {
		return nil
	}
	return e.desc
}

func (c *CachingModelStore) store(key cacheKey, desc *spi.ModelDescriptor) {
	expires := c.clock.Now().Add(c.jitteredLease())
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{desc: desc, expiresAt: expires}
}

func (c *CachingModelStore) evict(key cacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *CachingModelStore) invalidate(ctx context.Context, ref spi.ModelRef) {
	key := cacheKey{tenant: tenantOf(ctx), ref: ref}
	c.evict(key)
	if c.broadcaster != nil {
		payload, err := EncodeInvalidation(key.tenant, ref)
		if err == nil {
			c.broadcaster.Broadcast(topicModelInvalidate, payload)
		}
	}
}

func (c *CachingModelStore) handleInvalidation(payload []byte) {
	tenant, ref, ok := DecodeInvalidation(payload)
	if !ok {
		return
	}
	c.evict(cacheKey{tenant: tenant, ref: ref})
}

// jitteredLease returns lease ± 10 %.
func (c *CachingModelStore) jitteredLease() time.Duration {
	c.mu.Lock() // jitterRand is not concurrent-safe
	defer c.mu.Unlock()
	factor := 0.9 + 0.2*c.jitterRand.Float64()
	return time.Duration(float64(c.lease) * factor)
}

func flightKey(key cacheKey) string {
	return key.tenant + "|" + key.ref.EntityName + "|" + key.ref.ModelVersion
}

// tenantOf best-effort extracts tenant from ctx; empty string on absence.
func tenantOf(ctx context.Context) string {
	uc := spi.GetUserContext(ctx)
	if uc == nil {
		return ""
	}
	return string(uc.Tenant.ID)
}

// Compile-time interface check.
var _ spi.ModelStore = (*CachingModelStore)(nil)
```

- [ ] **Step 4: Create payload codec**

Create `internal/cluster/modelcache/payload.go`:

```go
package modelcache

import (
	"encoding/json"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// topicModelInvalidate is the gossip topic used for cache drop messages.
const topicModelInvalidate = "model.invalidate"

// invalidationPayload is the wire form for model-invalidate gossip.
type invalidationPayload struct {
	TenantID     string `json:"t"`
	EntityName   string `json:"n"`
	ModelVersion string `json:"v"`
}

// EncodeInvalidation produces the payload sent on topicModelInvalidate.
func EncodeInvalidation(tenantID string, ref spi.ModelRef) ([]byte, error) {
	return json.Marshal(invalidationPayload{
		TenantID: tenantID, EntityName: ref.EntityName, ModelVersion: ref.ModelVersion,
	})
}

// DecodeInvalidation is the inverse. Returns ok=false on malformed input;
// the gossip handler drops such messages silently.
func DecodeInvalidation(raw []byte) (tenantID string, ref spi.ModelRef, ok bool) {
	var p invalidationPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", spi.ModelRef{}, false
	}
	if p.TenantID == "" || p.EntityName == "" || p.ModelVersion == "" {
		return "", spi.ModelRef{}, false
	}
	return p.TenantID, spi.ModelRef{EntityName: p.EntityName, ModelVersion: p.ModelVersion}, true
}
```

Create `internal/cluster/modelcache/payload_test.go`:

```go
package modelcache_test

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/modelcache"
)

func TestInvalidation_RoundTrip(t *testing.T) {
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "3"}
	raw, err := modelcache.EncodeInvalidation("tenant-A", ref)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	gotTenant, gotRef, ok := modelcache.DecodeInvalidation(raw)
	if !ok || gotTenant != "tenant-A" || gotRef != ref {
		t.Errorf("round trip: tenant=%q ref=%+v ok=%v", gotTenant, gotRef, ok)
	}
}

func TestInvalidation_DecodeRejectsBlanks(t *testing.T) {
	if _, _, ok := modelcache.DecodeInvalidation([]byte(`{"t":"","n":"","v":""}`)); ok {
		t.Error("decode should reject empty fields")
	}
	if _, _, ok := modelcache.DecodeInvalidation([]byte(`{not-json`)); ok {
		t.Error("decode should reject malformed JSON")
	}
}
```

- [ ] **Step 5: Run all cache tests**

```bash
go test ./internal/cluster/modelcache/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cluster/modelcache/ && git commit -m "feat(modelcache): cache-any-LOCKED decorator + TTL lease with ±10 % jitter + singleflight RefreshAndGet

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task E2: Cross-node gossip invalidation integration test

**Files:**
- Create: `internal/cluster/modelcache/integration_test.go`

- [ ] **Step 1: Write the integration test**

```go
package modelcache_test

import (
	"context"
	"testing"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/cluster/modelcache"
)

// fakeBroadcaster delivers published messages to every subscriber
// synchronously, mirroring in-process multi-node semantics.
type fakeBroadcaster struct {
	handlers map[string][]func([]byte)
}

func newFakeBroadcaster() *fakeBroadcaster {
	return &fakeBroadcaster{handlers: make(map[string][]func([]byte))}
}

func (b *fakeBroadcaster) Broadcast(topic string, payload []byte) {
	// Copy handlers to a local so handlers added during dispatch don't loop.
	hs := append([]func([]byte){}, b.handlers[topic]...)
	for _, h := range hs {
		h(payload)
	}
}

func (b *fakeBroadcaster) Subscribe(topic string, h func([]byte)) {
	b.handlers[topic] = append(b.handlers[topic], h)
}

func TestCache_GossipInvalidation_DropsOnPeer(t *testing.T) {
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}

	bc := newFakeBroadcaster()
	innerA := &stubStore{desc: locked(ref, "")}
	innerB := &stubStore{desc: locked(ref, "")}
	clk := &manualClock{now: time.Now()}

	nodeA := modelcache.New(innerA, bc, clk, time.Hour)
	nodeB := modelcache.New(innerB, bc, clk, time.Hour)

	// Populate both caches.
	ctx := withTenantContext(context.Background(), "t1")
	_, _ = nodeA.Get(ctx, ref)
	_, _ = nodeB.Get(ctx, ref)
	if innerA.gets != 1 || innerB.gets != 1 {
		t.Fatalf("setup: expected 1 Get each, got A=%d B=%d", innerA.gets, innerB.gets)
	}

	// Mutate on A; gossip fires; B's entry should drop.
	if err := nodeA.Lock(ctx, ref); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	_, _ = nodeB.Get(ctx, ref)
	if innerB.gets != 2 {
		t.Errorf("expected B.Get to reload after gossip invalidation, got %d", innerB.gets)
	}
}

func withTenantContext(ctx context.Context, tenant string) context.Context {
	uc := &spi.UserContext{UserID: "u", Tenant: spi.TenantContext{ID: spi.TenantID(tenant)}}
	return spi.WithUserContext(ctx, uc)
}
```

- [ ] **Step 2: Run — PASS**

```bash
go test ./internal/cluster/modelcache/... -run TestCache_GossipInvalidation -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/cluster/modelcache/integration_test.go && git commit -m "test(modelcache): cross-node gossip invalidation integration

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase F — Handler rewrite + refresh-on-stale

### Task F1: Rewrite `validateOrExtend` to use `ExtendSchema`

**Files:**
- Modify: `internal/domain/entity/handler.go`
- Modify: `internal/domain/entity/handler_test.go` (create if absent)

- [ ] **Step 1: Failing test — concurrent updates should not produce 409**

Add to `internal/domain/entity/handler_test.go`:

```go
func TestValidateOrExtend_EmitsExtendSchemaOnAdditiveChange(t *testing.T) {
	h, ms, _ := newTestHandler(t)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	desc := &spi.ModelDescriptor{
		Ref:         ref,
		State:       spi.ModelLocked,
		ChangeLevel: "STRUCTURAL",
		Schema:      []byte(`{"type":"object","properties":{"title":{"type":"string"}}}`),
	}
	// parsedData carrying a new property.
	data := map[string]any{"title": "X", "isbn": "9780"}

	if err := h.ValidateOrExtendForTest(context.Background(), ms, desc, data); err != nil {
		t.Fatalf("validateOrExtend: %v", err)
	}

	if n := ms.ExtendSchemaCalls(); n != 1 {
		t.Errorf("expected 1 ExtendSchema call, got %d", n)
	}
	if ms.SaveCallCount() != 0 {
		t.Errorf("defect path: validateOrExtend should NOT call Save in ChangeLevel != \"\" branch, got %d", ms.SaveCallCount())
	}
}

func TestValidateOrExtend_NoopPayload_SkipsExtendSchema(t *testing.T) {
	h, ms, _ := newTestHandler(t)
	ref := spi.ModelRef{EntityName: "Book", ModelVersion: "1"}
	desc := &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelLocked, ChangeLevel: "STRUCTURAL",
		Schema: []byte(`{"type":"object","properties":{"title":{"type":"string"}}}`),
	}
	data := map[string]any{"title": "X"} // no new properties

	_ = h.ValidateOrExtendForTest(context.Background(), ms, desc, data)

	if n := ms.ExtendSchemaCalls(); n != 0 {
		t.Errorf("expected 0 ExtendSchema calls for no-op payload, got %d", n)
	}
}
```

`newTestHandler` and a stub `ModelStore` must be added to the test helper (create a small `spystore_test.go` if one doesn't exist; the stub records `ExtendSchema`/`Save` calls).

- [ ] **Step 2: Run — expect fail**

```bash
go test ./internal/domain/entity/... -run TestValidateOrExtend -v
```

Expected: fail, test infrastructure missing or old code still calls Save.

- [ ] **Step 3: Rewrite `validateOrExtend` in `handler.go`**

Replace the entire body with:

```go
// validateOrExtend validates parsedData against the model schema. When
// ChangeLevel is set, it computes an additive typed-op delta from the
// incoming shape and calls ExtendSchema (never Save — Save is the
// admin-path full-replace and is disjoint from ExtendSchema via §2).
// On no-op payloads Diff returns (nil, nil) and we skip ExtendSchema.
func (h *Handler) validateOrExtend(ctx context.Context, modelStore spi.ModelStore, desc *spi.ModelDescriptor, parsedData any) error {
	modelNode, err := schema.Unmarshal(desc.Schema)
	if err != nil {
		return fmt.Errorf("failed to unmarshal model schema: %w", err)
	}

	if desc.ChangeLevel == "" {
		errs := schema.Validate(modelNode, parsedData)
		if len(errs) > 0 {
			return schema.ValidationErrors(errs)
		}
		return nil
	}

	incomingModel, err := importer.Walk(parsedData)
	if err != nil {
		return fmt.Errorf("failed to walk data: %w", err)
	}
	extended, err := schema.Extend(modelNode, incomingModel, desc.ChangeLevel)
	if err != nil {
		return fmt.Errorf("change level violation: %w", err)
	}

	delta, err := schema.Diff(modelNode, extended)
	if err != nil {
		return fmt.Errorf("failed to compute schema delta: %w", err)
	}
	if delta == nil {
		return nil // no-op
	}
	if err := modelStore.ExtendSchema(ctx, desc.Ref, delta); err != nil {
		return fmt.Errorf("failed to extend model schema: %w", err)
	}
	return nil
}
```

Remove the now-dead `failed to marshal extended schema` classifier branch in `classifyValidateOrExtendErr` if it no longer fires — check usages.

- [ ] **Step 4: Run — PASS**

```bash
go test ./internal/domain/entity/... -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/domain/entity/ && git commit -m "fix(entity): validateOrExtend emits ExtendSchema, never Save — removes the regression hotspot

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task F2: Bounded refresh-on-stale on the strict-validate branch

**Files:**
- Modify: `internal/domain/entity/handler.go`

- [ ] **Step 1: Failing test for the refresh wrapper**

Append to `internal/domain/entity/handler_test.go`:

```go
func TestValidate_StaleSchema_RefreshesOnce(t *testing.T) {
	h, ms, _ := newTestHandler(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}

	// Initial cached descriptor missing a field.
	stale := &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelLocked, ChangeLevel: "",
		Schema: []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`),
	}
	// Post-refresh descriptor adds 'b'.
	fresh := &spi.ModelDescriptor{
		Ref: ref, State: spi.ModelLocked, ChangeLevel: "",
		Schema: []byte(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}}}`),
	}
	ms.QueueGet(stale)
	ms.QueueRefresh(fresh) // next RefreshAndGet returns this

	// Payload references 'b' — stale validation would flag unknown element.
	data := map[string]any{"a": "x", "b": 1}
	err := h.ValidateWithRefresh(context.Background(), ms, ref, data)
	if err != nil {
		t.Errorf("expected PASS after one refresh, got %v", err)
	}
	if ms.RefreshCalls() != 1 {
		t.Errorf("expected 1 refresh, got %d", ms.RefreshCalls())
	}
}
```

- [ ] **Step 2: Run — expect fail**

```bash
go test ./internal/domain/entity/... -run TestValidate_StaleSchema_RefreshesOnce -v
```

Expected: compile error (`h.ValidateWithRefresh` undefined).

- [ ] **Step 3: Add the wrapper**

Add to `internal/domain/entity/handler.go`:

```go
// ValidateWithRefresh runs strict schema validation with a bounded
// refresh-on-stale safety net. At most one refresh per call, and only
// on unknown-schema-element errors. Other validation failures surface
// directly. See spec §4.3.
func (h *Handler) ValidateWithRefresh(ctx context.Context, modelStore spi.ModelStore, ref spi.ModelRef, data any) error {
	desc, err := modelStore.Get(ctx, ref)
	if err != nil {
		return err
	}
	errs := validateAgainst(desc, data)
	if errs == nil {
		return nil
	}
	if !schema.HasUnknownSchemaElement(errs) {
		return errs
	}
	refresher, ok := modelStore.(interface {
		RefreshAndGet(context.Context, spi.ModelRef) (*spi.ModelDescriptor, error)
	})
	if !ok {
		return errs // plugin has no cache; refresh wouldn't help.
	}
	freshDesc, rErr := refresher.RefreshAndGet(ctx, ref)
	if rErr != nil {
		return rErr
	}
	errs2 := validateAgainst(freshDesc, data)
	if errs2 == nil {
		return nil
	}
	return errs2
}

func validateAgainst(desc *spi.ModelDescriptor, data any) error {
	if desc == nil {
		return fmt.Errorf("nil descriptor")
	}
	node, err := schema.Unmarshal(desc.Schema)
	if err != nil {
		return fmt.Errorf("unmarshal schema: %w", err)
	}
	errs := schema.Validate(node, data)
	if len(errs) == 0 {
		return nil
	}
	return schema.ValidationErrors(errs)
}
```

- [ ] **Step 4: Run — PASS**

```bash
go test ./internal/domain/entity/... -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/domain/entity/handler.go internal/domain/entity/handler_test.go && git commit -m "feat(entity): ValidateWithRefresh wrapper — one-shot refresh-on-stale on unknown-element errors

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task F3: Apply refresh wrapper in `search` field-path validation

**Files:**
- Modify: `internal/domain/search/service.go`

- [ ] **Step 1: Locate the schema-validation call site in search service**

```bash
grep -n "schema.Validate\|HasUnknownSchemaElement\|unknown field" internal/domain/search/*.go
```

Identify the precise function that validates search condition field paths against the model schema. (In today's code this may be implicit via `match.Evaluate` or a dedicated validator.) If no explicit schema validation happens pre-execution, add one using `validateAgainst` from Task F2.

- [ ] **Step 2: Write failing test**

Add to `internal/domain/search/service_test.go`:

```go
func TestSearch_StaleSchemaFieldPath_RefreshesOnce(t *testing.T) {
	svc, ms := newSearchServiceWithStubStore(t)
	ref := spi.ModelRef{EntityName: "E", ModelVersion: "1"}
	ms.QueueGet(&spi.ModelDescriptor{
		Ref: ref, State: spi.ModelLocked,
		Schema: []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`),
	})
	ms.QueueRefresh(&spi.ModelDescriptor{
		Ref: ref, State: spi.ModelLocked,
		Schema: []byte(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}}}`),
	})

	// Condition references $.b — stale schema rejects, refresh accepts.
	cond := buildJsonPathCondition("$.b", "EQUALS", 42)
	_, err := svc.Search(context.Background(), ref, cond, searchOpts{})
	if err != nil {
		t.Errorf("expected search to succeed after refresh, got %v", err)
	}
	if ms.RefreshCalls() != 1 {
		t.Errorf("expected 1 refresh, got %d", ms.RefreshCalls())
	}
}
```

`buildJsonPathCondition` and `newSearchServiceWithStubStore` are test helpers to add if absent.

- [ ] **Step 3: Run — expect fail**

```bash
go test ./internal/domain/search/... -run TestSearch_StaleSchemaFieldPath -v
```

- [ ] **Step 4: Thread the refresh wrapper**

At the pre-execution validation call site in `internal/domain/search/service.go`, replace any direct schema-read + local validate with:

```go
if err := h.Handler.ValidateWithRefresh(ctx, modelStore, ref, conditionAsValidateDoc(cond)); err != nil {
    return nil, err
}
```

where `conditionAsValidateDoc` converts a Condition tree into a minimal document exercising only the field paths it references (so missing schema elements surface as unknown-element errors in the classifier).

- [ ] **Step 5: Run — PASS**

- [ ] **Step 6: Commit**

```bash
git add internal/domain/search/ && git commit -m "feat(search): refresh-on-stale on field-path validation via Handler.ValidateWithRefresh

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase G — Factory wiring

### Task G1: Wire `ApplyFunc` and `CachingModelStore` in `cmd/cyoda/main.go`

**Files:**
- Modify: `cmd/cyoda/main.go`

- [ ] **Step 1: Read the existing plugin initialization code**

```bash
grep -n "NewStoreFactory\|StoreFactory\|modelcache\|postgres.Config\|sqlite.Config\|memory.Config" cmd/cyoda/main.go
```

Identify where `postgres.NewStoreFactory`, `sqlite.NewStoreFactory`, and `memory.NewStoreFactory` are called.

- [ ] **Step 2: Add `ApplyFunc` to each factory config**

At each factory-construction site:

```go
applyFunc := func(base []byte, delta spi.SchemaDelta) ([]byte, error) {
    baseNode, err := schema.Unmarshal(base)
    if err != nil {
        return nil, err
    }
    folded, err := schema.Apply(baseNode, delta)
    if err != nil {
        return nil, err
    }
    return schema.Marshal(folded)
}
postgresCfg := postgres.Config{
    // ... existing fields ...
    ApplyFunc: applyFunc,
}
```

Identical wiring for `sqlite.Config` and `memory.Config`.

- [ ] **Step 3: Wrap the returned `ModelStore` factory output in `CachingModelStore`**

The factory already exposes a `ModelStore(ctx)` method. Since decoration happens at the store-access seam, the cleanest approach is to wrap the factory itself:

```go
// After constructing the plugin factory:
innerFactory := postgresFactory // or sqliteFactory / memoryFactory
cachingFactory := modelcache.WrapFactory(innerFactory, broadcaster, realClock{}, time.Hour)
```

If `WrapFactory` doesn't yet exist (and the plugin factories return concrete types), add a small helper:

```go
// WrapFactory returns a new StoreFactory-equivalent whose ModelStore(ctx)
// returns the inner store wrapped in CachingModelStore. All other Store
// methods pass through.
func WrapFactory(inner storeFactoryMS, bc spi.ClusterBroadcaster, clk Clock, lease time.Duration) storeFactoryMS {
    return &wrappedFactory{inner: inner, bc: bc, clk: clk, lease: lease}
}

type storeFactoryMS interface {
    ModelStore(ctx context.Context) (spi.ModelStore, error)
    // ... every other method the main binary pulls from the factory ...
}

type wrappedFactory struct {
    inner storeFactoryMS
    bc    spi.ClusterBroadcaster
    clk   Clock
    lease time.Duration
}

func (w *wrappedFactory) ModelStore(ctx context.Context) (spi.ModelStore, error) {
    inner, err := w.inner.ModelStore(ctx)
    if err != nil {
        return nil, err
    }
    return New(inner, w.bc, w.clk, w.lease), nil
}
// Other methods pass through verbatim.
```

The precise list of methods on `storeFactoryMS` comes from what `cmd/cyoda/main.go` actually calls on the factory object — audit in this step.

- [ ] **Step 4: Feed the gossip broadcaster**

Main wires the existing `*registry.Gossip` (from `internal/cluster/registry`) as `spi.ClusterBroadcaster`. Pull the same instance already used for compute dispatch.

- [ ] **Step 5: Sanity-build and run short test suite**

```bash
go build ./... && go test -short ./... 2>&1 | tail -20
```

- [ ] **Step 6: Commit**

```bash
git add cmd/cyoda/main.go internal/cluster/modelcache/ && git commit -m "feat(cmd): wire ApplyFunc + CachingModelStore wrapper in main binary

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase H — E2E regression + self-healing

### Task H1: Regression E2E — the reported bug

**Files:**
- Create: `internal/e2e/model_schema_extensions_test.go`

- [ ] **Step 1: Write the reproducer**

```go
package e2e_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

// TestE2E_ConcurrentUpdate_NoConflictOnChangeLevelModel is the direct
// repro of the reported regression: bulk create then bulk update on a
// model with ChangeLevel produced 4/5 CONFLICT responses on Postgres
// because validateOrExtend called modelStore.Save on every update.
// After the fix, ExtendSchema is append-only and all N updates commit.
func TestE2E_ConcurrentUpdate_NoConflictOnChangeLevelModel(t *testing.T) {
	env := bootstrapE2E(t)
	defer env.Shutdown()

	// 1. Lock a User model with ChangeLevel = STRUCTURAL.
	mustImportModel(t, env, "User", 1, `{"type":"object","properties":{"email":{"type":"string"}}}`, "STRUCTURAL")

	// 2. Bulk create 8 distinct users (sequential — not the stressed path).
	ids := make([]string, 8)
	for i := 0; i < 8; i++ {
		ids[i] = mustCreateEntity(t, env, "User", 1, map[string]any{
			"email": emailFor(i), "createProtectedFields": []string{},
		})
	}

	// 3. Concurrent updates on all 8, each with a payload shape identical
	//    to the create (so extension is a no-op — but the defect fired Save
	//    unconditionally).
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = updateEntity(env, ids[i], map[string]any{
				"email": emailFor(i), "createProtectedFields": []string{},
			})
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Errorf("update[%d] failed: %v", i, e)
		}
	}
}

func emailFor(i int) string { return fmt.Sprintf("user%d@example.com", i) }
```

`bootstrapE2E`, `mustImportModel`, `mustCreateEntity`, and `updateEntity` piggy-back on existing E2E helpers in `internal/e2e/`; reuse them.

- [ ] **Step 2: Run the reproducer against the *pre-fix* main branch to confirm it fails there**

(Skip if already confirmed during brainstorming — but recommended sanity check.)

```bash
git stash && git checkout main -- internal/domain/entity/handler.go && go test ./internal/e2e/... -run TestE2E_ConcurrentUpdate -v
```

Expected: 4 of 8 updates fail with 409. Then:

```bash
git checkout HEAD -- internal/domain/entity/handler.go && git stash pop
```

- [ ] **Step 3: Run on the feature branch — PASS**

```bash
go test ./internal/e2e/... -run TestE2E_ConcurrentUpdate -v
```

Expected: all 8 updates PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/e2e/model_schema_extensions_test.go && git commit -m "test(e2e): regression reproducer — 8 concurrent updates on ChangeLevel model all succeed

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task H2: Canonical read-side self-healing E2E

**Files:**
- Modify: `internal/e2e/model_schema_extensions_test.go`

- [ ] **Step 1: Append the self-healing scenario**

```go
// TestE2E_ReadSideSelfHealing — spec §7 canonical scenario.
// Two in-process nodes share a Postgres, but we simulate gossip
// partition by disconnecting the broadcaster between them. Node A
// commits an extension adding newField; Node B has stale cache.
// A search on Node B filtering on newField first fails validation,
// then refreshes, then succeeds.
func TestE2E_ReadSideSelfHealing(t *testing.T) {
	envA, envB := bootstrapTwoNodeE2E(t) // nodes share DB; independent caches
	defer envA.Shutdown()
	defer envB.Shutdown()

	mustImportModel(t, envA, "Book", 1, `{"type":"object","properties":{"title":{"type":"string"}}}`, "STRUCTURAL")

	// Prime node B's cache with the initial schema.
	_, _ = searchEntities(envB, "Book", 1, jsonPathEq("$.title", "warmup"))

	// Disconnect gossip between A and B.
	disconnectGossip(envA, envB)

	// Node A commits an ExtendSchema adding `isbn`.
	_, _ = createEntity(envA, "Book", 1, map[string]any{"title": "t", "isbn": "9780"})

	// Node B searches by $.isbn — validation must refresh and succeed.
	results, err := searchEntities(envB, "Book", 1, jsonPathEq("$.isbn", "9780"))
	if err != nil {
		t.Fatalf("search on stale node should refresh and succeed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 hit after refresh, got %d", len(results))
	}
}
```

`bootstrapTwoNodeE2E` and `disconnectGossip` are new helpers — add to `internal/e2e/helpers_twonode.go`. Two nodes share a postgres testcontainer but each has its own `httptest.Server` + `modelcache.CachingModelStore`. `disconnectGossip` replaces the shared broadcaster with node-local stubs so invalidations stop crossing.

- [ ] **Step 2: Run**

```bash
go test ./internal/e2e/... -run TestE2E_ReadSideSelfHealing -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/e2e/ && git commit -m "test(e2e): canonical read-side self-healing — stale cache refreshes on unknown-element and succeeds

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase I — Documentation

### Task I1: `docs/CONSISTENCY.md` — new Model/Data Contract section

**Files:**
- Modify: `docs/CONSISTENCY.md`

- [ ] **Step 1: Add the new section**

After §2 ("What this contract catches") of `CONSISTENCY.md`, insert a new §2a:

```markdown
## 2a. Model/Data Contract

Data operations require the model to be `LOCKED`. A locked model carries
a `ChangeLevel` that governs what additive schema evolution is permitted
at ingestion time. Five invariants apply to additive model mutation:

1. **Non-interference.** Schema mutation must not conflict with
   concurrent data ops on the same model.
2. **Commit-bound visibility.** Schema mutation is visible iff the
   owning entity transaction commits.
3. **Commutativity.** Concurrent deltas fold to the same schema
   regardless of apply order.
4. **Validation-monotonicity.** Any document valid against base stays
   valid against `Apply(base, delta)`. Ops that tighten the accepted
   set are not in the catalog.
5. **State-machine disjointness.** `Save` (admin, UNLOCKED) and
   `ExtendSchema` (ingestion, LOCKED) cannot run concurrently.

**Operator contract on `Unlock`.** `LOCKED → UNLOCKED` requires the
application to have drained writers to this model first. Concurrent
`ExtendSchema` during `Unlock` is undefined behaviour. The Postgres
plugin's extension-log DELETE-and-assert is a development-time guard
against operator misuse, not a production race guard.

**Accepted policy.** `SetChangeLevel` under `LOCKED → LOCKED` may tighten
permitted extensions while an `ExtendSchema` under the prior level is
in flight. The extension commits against the new authority if it wins
the race, or rolls back otherwise. Audit trail preserved either way.
```

- [ ] **Step 2: Commit**

```bash
git add docs/CONSISTENCY.md && git commit -m "docs(consistency): Model/Data Contract — five invariants + operator contract

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task I2: `docs/ARCHITECTURE.md` cross-references

**Files:**
- Modify: `docs/ARCHITECTURE.md`

- [ ] **Step 1: Insert cross-refs in §2.3 (Postgres plugin)**

At the end of §2.3's description of the postgres plugin, add:

> The postgres `ModelStore` represents each model as a stable `models`
> row plus an append-only `model_schema_extensions` log; `ExtendSchema`
> appends a typed-op delta atomically with the entity tx, and `Get`
> folds the log back via `schema.Apply`. See `CONSISTENCY.md §2a` for
> the contract and `docs/superpowers/specs/2026-04-20-model-schema-extensions-design.md`
> for the plugin-internal savepoint mechanism.

- [ ] **Step 2: Add note in §3 (Transaction Model)**

Near the description of entity-granular SI+FCW, add:

> Model schema mutation participates in entity transactions via
> `ModelStore.ExtendSchema`. Additive-only by construction — see
> `CONSISTENCY.md §2a`.

- [ ] **Step 3: Add `model.invalidate` to the list of gossip topics in §4**

> Topics currently multiplexed over gossip: member-metadata (compute
> dispatch tags) and `model.invalidate` (drop-signal for the
> `CachingModelStore` decorator).

- [ ] **Step 4: Commit**

```bash
git add docs/ARCHITECTURE.md && git commit -m "docs(architecture): cross-refs to Model/Data Contract and model.invalidate gossip topic

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase Z — Finish line

### Task Z1: Release the SPI module; update `go.mod` to the tagged version

**Files:**
- Modify: `go.mod`
- Modify: `plugins/memory/go.mod`, `plugins/postgres/go.mod`, `plugins/sqlite/go.mod`
- Modify: `go.work` (remove local SPI entry)

**Note on process.** Never force-move a Go module tag (see
`feedback_go_module_tags_immutable.md`). This task releases a **new**
tag.

- [ ] **Step 1: (human step) Tag and push the SPI repo**

```
cd /Users/paul/go-projects/cyoda-light/cyoda-go-spi
git push origin feat/model-schema-extensions
# open PR, review, merge to main
git checkout main && git pull
git tag v0.6.0 && git push origin v0.6.0
```

Ask Paul to execute this step — it requires push permissions and a chosen version.

- [ ] **Step 2: Bump SPI version in every `go.mod`**

Once the tag is published:

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/model-schema-extensions
go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0
(cd plugins/memory && go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0)
(cd plugins/postgres && go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0)
(cd plugins/sqlite && go get github.com/cyoda-platform/cyoda-go-spi@v0.6.0)
go mod tidy
(cd plugins/memory && go mod tidy)
(cd plugins/postgres && go mod tidy)
(cd plugins/sqlite && go mod tidy)
```

- [ ] **Step 3: Remove the workspace `use` for the local SPI checkout**

Edit `go.work`, drop the `/Users/paul/go-projects/cyoda-light/cyoda-go-spi` line so CI (which has no local SPI checkout) resolves the tagged version only.

- [ ] **Step 4: Build + full test suite**

```bash
go build ./... && go test -short ./... 2>&1 | tail
```

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum go.work plugins/*/go.{mod,sum} && git commit -m "chore: bump cyoda-go-spi to v0.6.0 (ExtendSchema)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task Z2: End-of-deliverable verification

**Files:** none.

- [ ] **Step 1: Full test suite, short mode**

```bash
go test -short ./... 2>&1 | tail -20
```

Expected: PASS.

- [ ] **Step 2: Full test suite including E2E**

```bash
go test ./... 2>&1 | tail -20
```

Expected: PASS. Requires Docker running for Postgres testcontainer.

- [ ] **Step 3: Per-plugin suites**

Plugin submodules are NOT run by `go test ./...` from repo root (see memory `feedback_plugin_submodule_tests.md`). Run each explicitly:

```bash
for p in memory postgres sqlite; do echo "--- $p ---"; (cd plugins/$p && go test ./...); done
```

- [ ] **Step 4: `go vet` across all modules**

```bash
go vet ./... && (cd plugins/memory && go vet ./...) && (cd plugins/postgres && go vet ./...) && (cd plugins/sqlite && go vet ./...)
```

- [ ] **Step 5: One-shot race detector sweep**

```bash
go test -race ./... 2>&1 | tail -20
```

(See `feedback_race_testing_discipline.md` — race is a single end-of-deliverable gate, not per-step.)

- [ ] **Step 6: `make todos` to list any deferred work**

```bash
make todos 2>&1 | head -30
```

Expected: no `TODO(plan-2026-04-20-…)` entries — Gate 6 demands resolved, not deferred.

- [ ] **Step 7: Smoke test — bring the binary up and hit the endpoint**

```bash
go build -o /tmp/cyoda ./cmd/cyoda && CYODA_STORAGE_BACKEND=memory /tmp/cyoda &
sleep 2
curl -s http://localhost:8080/health | head
kill %1
```

Expected: `200 OK` from `/health`.

- [ ] **Step 8: Final commit if any lint fixes surfaced**

```bash
git status
git add -A && git commit -m "chore: post-verification cleanup" # only if something changed
```

---

## Out of scope for this plan (tracked elsewhere)

- **Cassandra plugin realization.** Lives in `../cyoda-go-cassandra` on its own `feat/model-schema-extensions` branch; spec committed there. Follow-up plan will be written in that repo using its own writing-plans session.
- **Gossip codec profiling + potential replacement.** Plan Z leaves the JSON-encoded `(tenant, ref)` payload as-is per spec §9. If profiling shows it's a hotspot, replace with a tagged varint format in a separate PR.
- **Savepoint interval tuning.** Hardcoded at 64 per spec §9. If a hot model surfaces adverse fold costs, promote to a plugin-level config knob.
- **Forward-compat across SPI op-kind versions.** Noted in spec §8 as a post-GA concern; no Phase 1 work.
