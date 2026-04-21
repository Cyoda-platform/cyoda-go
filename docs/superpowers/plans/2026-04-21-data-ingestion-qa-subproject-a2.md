# Sub-project A.2 — Schema-Transformation Round-Trip Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build property-based and fixture-catalog coverage of the schema-transformation pipeline so the master invariant `Apply(old, Diff(old, Extend(old, Walk(data), level))) ≡ Extend(old, Walk(data), level)` is asserted byte-for-byte across every shape `importer.Walk` produces, every `(kind, kind)` transformation cell, and every `ChangeLevel`.

**Architecture:** New generator package `internal/domain/model/schema/gentree/` produces random `*ModelNode` trees and matched `(old, incoming, level)` tuples with deterministic `math/rand/v2` seeding. Five property-test files (roundtrip, commutativity, monotonicity, idempotence, permutation) drive the generator. Two hand-tables (axis2 kind matrix, axis3 ChangeLevel matrix) cover the discrete cells. A ~40-entry `Catalog` pins named regression fixtures. Polymorphic-slot kind-conflict cells (6 of them) are registered as `t.Skip` pointing at Sub-project A.3.

**Tech Stack:** Go 1.26, `math/rand/v2` (`PCG`), `testing`, existing `internal/domain/model/schema/` API (`Extend`, `Diff`, `Apply`, `Marshal`, `Validate`, `UnmarshalDelta`), existing `internal/domain/model/importer.Walk`.

**Spec:** `docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a2-design.md` (rev 2).

---

## Reference — exact API shapes (do not re-discover)

These are the signatures every task relies on. Paste into the implementer's instructions if needed.

```go
// internal/domain/model/schema/extend.go:34
func Extend(existing, incoming *ModelNode, level spi.ChangeLevel) (*ModelNode, error)

// internal/domain/model/schema/diff.go:17
func Diff(oldN, newN *ModelNode) (spi.SchemaDelta, error) // (nil, nil) on semantic no-op

// internal/domain/model/schema/apply.go:21
func Apply(base *ModelNode, delta spi.SchemaDelta) (*ModelNode, error) // nil delta → clone of base

// internal/domain/model/schema/codec.go:94
func Marshal(n *ModelNode) ([]byte, error)

// internal/domain/model/schema/ops.go:154
func UnmarshalDelta(delta spi.SchemaDelta) ([]SchemaOp, error)

// internal/domain/model/schema/validate.go:56
func Validate(model *ModelNode, data any) []ValidationError

// internal/domain/model/importer/walker.go:19
func Walk(data any) (*schema.ModelNode, error)
```

**SchemaOp shape** (`ops.go:59`):
```go
type SchemaOp struct {
    Kind    SchemaOpKind    `json:"kind"`
    Path    string          `json:"path"`
    Name    string          `json:"name,omitempty"`
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

**SchemaOpKind values** (3 total, `ops.go:21`):
- `KindAddProperty` = `"add_property"` — Structural level
- `KindBroadenType` = `"broaden_type"` — Type level
- `KindAddArrayItemType` = `"add_array_item_type"` — ArrayElements level

**ChangeLevel linear order** (low → high): `"" < spi.ChangeLevelArrayLength < spi.ChangeLevelArrayElements < spi.ChangeLevelType < spi.ChangeLevelStructural`.

**DataType enum** (21 values, `types.go:8–66`): `Integer, Long, BigInteger, UnboundInteger, Double, BigDecimal, UnboundDecimal, String, Character, LocalDate, LocalDateTime, LocalTime, ZonedDateTime, Year, YearMonth, UUIDType, TimeUUIDType, ByteArray, Boolean, Null`.

**ModelNode builders** (`node.go:42–108`):
```go
NewObjectNode() *ModelNode
NewLeafNode(dt DataType) *ModelNode
NewArrayNode(element *ModelNode) *ModelNode
(n *ModelNode).SetChild(name string, child *ModelNode)
```

---

## Task 1: Generator foundation — `GenConfig` and seeded RNG plumbing

**Files:**
- Create: `internal/domain/model/schema/gentree/gentree.go`
- Create: `internal/domain/model/schema/gentree/gentree_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/domain/model/schema/gentree/gentree_test.go
package gentree

import "testing"

func TestDefaultConfigSane(t *testing.T) {
    cfg := DefaultConfig()
    if cfg.MaxDepth < 3 {
        t.Fatalf("DefaultConfig MaxDepth=%d, want >=3", cfg.MaxDepth)
    }
    if cfg.MaxWidth < 3 {
        t.Fatalf("DefaultConfig MaxWidth=%d, want >=3", cfg.MaxWidth)
    }
    if cfg.Seed == 0 {
        t.Fatalf("DefaultConfig.Seed=0, want non-zero default")
    }
}

func TestNewRNGSameSeedSameSequence(t *testing.T) {
    r1 := NewRNG(42)
    r2 := NewRNG(42)
    for i := 0; i < 16; i++ {
        a := r1.Uint64()
        b := r2.Uint64()
        if a != b {
            t.Fatalf("seed 42, step %d: %d != %d", i, a, b)
        }
    }
}
```

- [ ] **Step 2: Run test — verify it fails**

Run: `go test ./internal/domain/model/schema/gentree/...`
Expected: FAIL — `undefined: DefaultConfig`, `undefined: NewRNG`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/domain/model/schema/gentree/gentree.go
//
// Package gentree produces random ModelNode trees and JSON-like values
// for property-based testing of the schema transformation pipeline.
// Determinism: use only ordered data structures when emitting tree
// shape. Never `range` over maps in generator paths — see
// TestGeneratorIsMapFree.
package gentree

import (
    "math/rand/v2"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

type GenConfig struct {
    Seed             int64
    MaxDepth         int
    MaxWidth         int
    KindWeights      KindWeights
    PrimitiveWeights map[schema.DataType]float64
    AllowNulls       bool
    TargetLevel      spi.ChangeLevel
}

type KindWeights struct {
    Leaf, Object, Array float64
}

func DefaultConfig() GenConfig {
    return GenConfig{
        Seed:        1,
        MaxDepth:    5,
        MaxWidth:    6,
        KindWeights: KindWeights{Leaf: 0.5, Object: 0.3, Array: 0.2},
        PrimitiveWeights: map[schema.DataType]float64{
            schema.Integer:       5,
            schema.Long:          3,
            schema.BigInteger:    1,
            schema.UnboundInteger: 1,
            schema.Double:        3,
            schema.BigDecimal:    2,
            schema.UnboundDecimal: 1,
            schema.String:        5,
            schema.Boolean:       2,
            schema.Null:          1,
        },
        AllowNulls: true,
    }
}

// NewRNG returns a PCG-seeded *rand.Rand; same seed produces same
// sequence across Go versions.
func NewRNG(seed int64) *rand.Rand {
    // Split int64 into two uint64s for PCG's two-word seed.
    s1 := uint64(seed)
    s2 := uint64(seed) ^ 0x9E3779B97F4A7C15
    return rand.New(rand.NewPCG(s1, s2))
}
```

- [ ] **Step 4: Run test — verify it passes**

Run: `go test ./internal/domain/model/schema/gentree/... -run 'TestDefaultConfigSane|TestNewRNGSameSeedSameSequence' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/gentree/gentree.go internal/domain/model/schema/gentree/gentree_test.go
git commit -m "feat(schema/gentree): seeded RNG and GenConfig foundation"
```

---

## Task 2: `GenValue` — random JSON-ish tree producer

**Files:**
- Modify: `internal/domain/model/schema/gentree/gentree.go` (add `GenValue`, `genLeafValue`, `genObject`, `genArray`)
- Modify: `internal/domain/model/schema/gentree/gentree_test.go` (add tests)

- [ ] **Step 1: Write the failing test**

```go
// Append to gentree_test.go
func TestGenValueProducesWalkableOutput(t *testing.T) {
    cfg := DefaultConfig()
    r := NewRNG(7)
    for i := 0; i < 50; i++ {
        v := GenValue(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
        // GenValue output must be accepted by importer.Walk.
        if _, err := importer.Walk(v); err != nil {
            t.Fatalf("sample %d: Walk failed: %v (value: %#v)", i, err, v)
        }
    }
}

func TestGenValueSameSeedSameOutput(t *testing.T) {
    cfg := DefaultConfig()
    v1 := GenValue(NewRNG(99), cfg.MaxDepth, cfg.MaxWidth, cfg)
    v2 := GenValue(NewRNG(99), cfg.MaxDepth, cfg.MaxWidth, cfg)
    b1, _ := json.Marshal(v1)
    b2, _ := json.Marshal(v2)
    if string(b1) != string(b2) {
        t.Fatalf("seed 99 produced divergent output:\n  v1=%s\n  v2=%s", b1, b2)
    }
}
```

Add imports: `"encoding/json"`, `"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"`.

- [ ] **Step 2: Run — verify it fails**

Run: `go test ./internal/domain/model/schema/gentree/... -run TestGenValue -v`
Expected: FAIL — `undefined: GenValue`.

- [ ] **Step 3: Write minimal implementation**

```go
// Append to gentree.go
import "encoding/json" // add to imports

// GenValue produces a random JSON-ish value at bounded depth suitable
// for feeding into importer.Walk. At depth=0, leaves only.
func GenValue(r *rand.Rand, depth, maxWidth int, cfg GenConfig) any {
    if depth <= 0 {
        return genLeafValue(r, cfg)
    }
    w := cfg.KindWeights
    total := w.Leaf + w.Object + w.Array
    roll := r.Float64() * total
    switch {
    case roll < w.Leaf:
        return genLeafValue(r, cfg)
    case roll < w.Leaf+w.Object:
        return genObjectValue(r, depth-1, maxWidth, cfg)
    default:
        return genArrayValue(r, depth-1, maxWidth, cfg)
    }
}

func genLeafValue(r *rand.Rand, cfg GenConfig) any {
    dt := pickWeightedType(r, cfg.PrimitiveWeights)
    switch dt {
    case schema.Integer:
        return json.Number(strconv.FormatInt(int64(r.IntN(1<<20)-(1<<19)), 10))
    case schema.Long:
        return json.Number(strconv.FormatInt(int64(r.Uint64()>>1), 10))
    case schema.BigInteger:
        return json.Number("170141183460469231731687303715884105727") // 2^127-1
    case schema.UnboundInteger:
        return json.Number("12345678901234567890123456789012345678901234567890")
    case schema.Double:
        return json.Number("3.14159265358979")
    case schema.BigDecimal:
        return json.Number("1.234567890123456789")          // 18 digit
    case schema.UnboundDecimal:
        return json.Number("1.23456789012345678901")         // 20 digit
    case schema.String:
        return randString(r, 1+r.IntN(8))
    case schema.Boolean:
        return r.IntN(2) == 1
    case schema.Null:
        if cfg.AllowNulls {
            return nil
        }
        return "nullsub"
    }
    return nil
}

func genObjectValue(r *rand.Rand, depth, maxWidth int, cfg GenConfig) any {
    // Use a sorted key slice — NEVER range over the map while emitting.
    n := 1 + r.IntN(maxWidth)
    keys := make([]string, n)
    for i := 0; i < n; i++ {
        keys[i] = "k" + strconv.Itoa(i)
    }
    // Iterate keys in slice order, not map order.
    out := make(map[string]any, n)
    for _, k := range keys {
        out[k] = GenValue(r, depth, maxWidth, cfg)
    }
    return out
}

func genArrayValue(r *rand.Rand, depth, maxWidth int, cfg GenConfig) any {
    n := r.IntN(maxWidth + 1) // allow empty
    out := make([]any, n)
    for i := 0; i < n; i++ {
        out[i] = GenValue(r, depth, maxWidth, cfg)
    }
    return out
}

// pickWeightedType chooses a DataType by iterating a SORTED key slice
// (deterministic). Never range over the map directly.
func pickWeightedType(r *rand.Rand, w map[schema.DataType]float64) schema.DataType {
    keys := make([]schema.DataType, 0, len(w))
    for k := range w { // one-time build is fine; we immediately sort
        keys = append(keys, k)
    }
    sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
    var total float64
    for _, k := range keys {
        total += w[k]
    }
    roll := r.Float64() * total
    var acc float64
    for _, k := range keys {
        acc += w[k]
        if roll < acc {
            return k
        }
    }
    return keys[len(keys)-1]
}

func randString(r *rand.Rand, n int) string {
    const alpha = "abcdefghijklmnopqrstuvwxyz"
    b := make([]byte, n)
    for i := range b {
        b[i] = alpha[r.IntN(len(alpha))]
    }
    return string(b)
}
```

Add imports: `"sort"`, `"strconv"`.

- [ ] **Step 4: Run — verify it passes**

Run: `go test ./internal/domain/model/schema/gentree/... -v`
Expected: PASS for both new tests plus earlier tests.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/gentree/gentree.go internal/domain/model/schema/gentree/gentree_test.go
git commit -m "feat(schema/gentree): GenValue produces Walk-compatible random trees"
```

---

## Task 3: `GenModelNode` — direct `*ModelNode` generator

**Files:**
- Modify: `internal/domain/model/schema/gentree/gentree.go` (add `GenModelNode`)
- Modify: `internal/domain/model/schema/gentree/gentree_test.go` (add test)

- [ ] **Step 1: Write the failing test**

```go
// Append to gentree_test.go
func TestGenModelNodeDeterministicMarshal(t *testing.T) {
    cfg := DefaultConfig()
    n1 := GenModelNode(NewRNG(11), cfg.MaxDepth, cfg.MaxWidth, cfg)
    n2 := GenModelNode(NewRNG(11), cfg.MaxDepth, cfg.MaxWidth, cfg)
    b1, err := schema.Marshal(n1)
    if err != nil {
        t.Fatal(err)
    }
    b2, err := schema.Marshal(n2)
    if err != nil {
        t.Fatal(err)
    }
    if string(b1) != string(b2) {
        t.Fatalf("seed 11 produced divergent ModelNode marshal:\n  n1=%s\n  n2=%s", b1, b2)
    }
}
```

- [ ] **Step 2: Run — verify fail**

Run: `go test ./internal/domain/model/schema/gentree/... -run TestGenModelNodeDeterministicMarshal -v`
Expected: FAIL — `undefined: GenModelNode`.

- [ ] **Step 3: Write minimal implementation**

```go
// Append to gentree.go
// GenModelNode returns a random *schema.ModelNode at bounded depth.
// Determinism discipline: sorted keys only; never range maps.
func GenModelNode(r *rand.Rand, depth, maxWidth int, cfg GenConfig) *schema.ModelNode {
    if depth <= 0 {
        return schema.NewLeafNode(pickWeightedType(r, cfg.PrimitiveWeights))
    }
    w := cfg.KindWeights
    total := w.Leaf + w.Object + w.Array
    roll := r.Float64() * total
    switch {
    case roll < w.Leaf:
        return schema.NewLeafNode(pickWeightedType(r, cfg.PrimitiveWeights))
    case roll < w.Leaf+w.Object:
        n := schema.NewObjectNode()
        count := 1 + r.IntN(maxWidth)
        for i := 0; i < count; i++ {
            key := "f" + strconv.Itoa(i)
            n.SetChild(key, GenModelNode(r, depth-1, maxWidth, cfg))
        }
        return n
    default:
        return schema.NewArrayNode(GenModelNode(r, depth-1, maxWidth, cfg))
    }
}
```

- [ ] **Step 4: Run — verify pass**

Run: `go test ./internal/domain/model/schema/gentree/... -run TestGenModelNodeDeterministicMarshal -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -u
git commit -m "feat(schema/gentree): GenModelNode — direct ModelNode generator"
```

---

## Task 4: `GenExtensionPair` — matched `(old, incoming, level)` tuple

**Files:**
- Modify: `internal/domain/model/schema/gentree/gentree.go` (add `GenExtensionPair`)
- Modify: `internal/domain/model/schema/gentree/gentree_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Append to gentree_test.go
func TestGenExtensionPairProducesExtendableIncoming(t *testing.T) {
    cfg := DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    r := NewRNG(23)
    for i := 0; i < 30; i++ {
        old := GenModelNode(r, 3, 4, cfg)
        incoming := GenExtensionPair(r, old, cfg.TargetLevel, cfg)
        incomingNode, err := importer.Walk(incoming)
        if err != nil {
            t.Fatalf("sample %d: Walk incoming failed: %v", i, err)
        }
        if _, err := schema.Extend(old, incomingNode, cfg.TargetLevel); err != nil {
            // Extend may reject when GenExtensionPair randomly produces
            // incompatible shapes at lower levels; at Structural, everything
            // additive must succeed.
            t.Fatalf("sample %d: Extend at Structural rejected: %v", i, err)
        }
    }
}
```

- [ ] **Step 2: Run — verify fail**

Run: `go test ./internal/domain/model/schema/gentree/... -run TestGenExtensionPair -v`
Expected: FAIL — `undefined: GenExtensionPair`.

- [ ] **Step 3: Write minimal implementation**

```go
// Append to gentree.go
// GenExtensionPair given an existing schema returns a random JSON-like
// value whose Walk output, when fed to Extend(old, ., level), is
// typically accepted at Structural level. Strategy: emit a mutated
// view of the schema (same shape, random additional fields) so the
// extension is additive rather than kind-changing.
func GenExtensionPair(r *rand.Rand, old *schema.ModelNode, level spi.ChangeLevel, cfg GenConfig) any {
    return mutateToValue(r, old, 0, cfg)
}

func mutateToValue(r *rand.Rand, n *schema.ModelNode, depth int, cfg GenConfig) any {
    if n == nil || depth > cfg.MaxDepth {
        return genLeafValue(r, cfg)
    }
    switch n.Kind() {
    case schema.KindLeaf:
        // Emit a value compatible with the widest type in the set, plus
        // occasionally broaden to trigger a broaden_type op.
        return genLeafValue(r, cfg)
    case schema.KindObject:
        out := make(map[string]any)
        for _, name := range sortedChildNames(n) {
            out[name] = mutateToValue(r, n.Child(name), depth+1, cfg)
        }
        // ~30% of the time add a new field to drive AddProperty.
        if r.Float64() < 0.3 {
            out["extra_"+strconv.Itoa(r.IntN(1000))] = genLeafValue(r, cfg)
        }
        return out
    case schema.KindArray:
        m := r.IntN(cfg.MaxWidth + 1)
        out := make([]any, m)
        for i := 0; i < m; i++ {
            out[i] = mutateToValue(r, n.Element(), depth+1, cfg)
        }
        return out
    }
    return genLeafValue(r, cfg)
}

func sortedChildNames(n *schema.ModelNode) []string {
    children := n.Children() // returns map[string]*ModelNode (shallow copy)
    names := make([]string, 0, len(children))
    for k := range children {
        names = append(names, k)
    }
    sort.Strings(names)
    return names
}
```

**API anchors used here (confirmed in `internal/domain/model/schema/node.go`):**
- `n.Kind() NodeKind` — node.go:71
- `n.Children() map[string]*ModelNode` — node.go:83 (shallow copy)
- `n.Child(name) *ModelNode` — node.go:95
- `n.Element() *ModelNode` — node.go:77

Build the sorted key slice once from `Children()`, then iterate the slice — never the map — to preserve generator determinism per spec §4.1.

- [ ] **Step 4: Run — verify pass**

Run: `go test ./internal/domain/model/schema/gentree/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -u
git commit -m "feat(schema/gentree): GenExtensionPair for matched (old, incoming) tuples"
```

---

## Task 5: Meta-tests — coverage distribution + map-free discipline

**Files:**
- Modify: `internal/domain/model/schema/gentree/gentree_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Append to gentree_test.go

// TestGeneratorIsMapFree runs GenModelNode with the same seed twice
// and asserts byte-identical ModelNode.Marshal output. A generator
// that accidentally ranges over a map fails this with high probability.
func TestGeneratorIsMapFree(t *testing.T) {
    cfg := DefaultConfig()
    for _, seed := range []int64{1, 2, 3, 100, 1000, 54321} {
        n1 := GenModelNode(NewRNG(seed), cfg.MaxDepth, cfg.MaxWidth, cfg)
        n2 := GenModelNode(NewRNG(seed), cfg.MaxDepth, cfg.MaxWidth, cfg)
        b1, _ := schema.Marshal(n1)
        b2, _ := schema.Marshal(n2)
        if string(b1) != string(b2) {
            t.Fatalf("seed %d: divergent output — generator is not map-free", seed)
        }
    }
}

// TestCoverageDistribution samples 10_000 GenModelNode outputs and
// asserts each major shape class is produced at minimum frequency.
func TestCoverageDistribution(t *testing.T) {
    if testing.Short() {
        t.Skip("coverage distribution is a slow sanity check, skipped under -short")
    }
    cfg := DefaultConfig()
    r := NewRNG(777)
    const N = 10_000
    var leaves, objects, arrays int
    for i := 0; i < N; i++ {
        n := GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
        switch n.Kind() {
        case schema.KindLeaf:
            leaves++
        case schema.KindObject:
            objects++
        case schema.KindArray:
            arrays++
        }
    }
    // Each class must be at least 1 in 50 (= 200 in 10k).
    for name, count := range map[string]int{"leaf": leaves, "object": objects, "array": arrays} {
        if count < N/50 {
            t.Errorf("%s frequency %d < threshold %d", name, count, N/50)
        }
    }
}
```

- [ ] **Step 2: Run — verify pass** (these tests drive discipline the generator already follows)

Run: `go test ./internal/domain/model/schema/gentree/... -v`
Expected: PASS for both new tests. If `TestGeneratorIsMapFree` fails, locate and remove the offending `range` over a map in generator paths per §4.1 of the spec.

- [ ] **Step 3: Commit**

```bash
git add -u
git commit -m "test(schema/gentree): meta-tests for determinism and coverage distribution"
```

---

## Task 6: Catalog — ≥40 named fixtures

**Files:**
- Create: `internal/domain/model/schema/gentree/catalog.go`
- Create: `internal/domain/model/schema/gentree/catalog_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/domain/model/schema/gentree/catalog_test.go
package gentree

import (
    "testing"
)

func TestCatalogHasAtLeast40Fixtures(t *testing.T) {
    if n := len(Catalog); n < 40 {
        t.Fatalf("Catalog has %d fixtures, want >=40", n)
    }
}

func TestCatalogEntriesHaveRequiredFields(t *testing.T) {
    seen := make(map[string]struct{})
    for i, f := range Catalog {
        if f.Name == "" {
            t.Errorf("entry %d: empty Name", i)
        }
        if _, dup := seen[f.Name]; dup {
            t.Errorf("entry %d: duplicate Name %q", i, f.Name)
        }
        seen[f.Name] = struct{}{}
        if f.Old == nil && f.Incoming == nil {
            t.Errorf("%s: both Old and Incoming nil", f.Name)
        }
    }
}
```

- [ ] **Step 2: Run — verify fail**

Run: `go test ./internal/domain/model/schema/gentree/... -run TestCatalog -v`
Expected: FAIL — `undefined: Catalog`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/domain/model/schema/gentree/catalog.go
package gentree

import (
    "encoding/json"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// Fixture is one named, hand-crafted test case.
// Exactly one of (Old, Incoming) may be nil — a nil Old means "create from scratch".
// ExpectedKinds, when non-nil, is asserted against the Diff output verbatim.
type Fixture struct {
    Name          string
    Old           *schema.ModelNode
    Incoming      any // fed through importer.Walk
    Level         spi.ChangeLevel
    ExpectedKinds []schema.SchemaOpKind // nil = don't assert
    ExpectError   bool
}

// Catalog is the authoritative list of named regression fixtures.
// Adding entries: keep names stable once merged; tests reference names.
var Catalog = []Fixture{
    // --- Flat/nested objects ---
    {
        Name:     "FlatObjectAddSibling",
        Old:      objNode(map[string]*schema.ModelNode{"a": leaf(schema.String)}),
        Incoming: map[string]any{"a": "x", "b": json.Number("1")},
        Level:    spi.ChangeLevelStructural,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindAddProperty},
    },
    {
        Name:     "NestedObjectAddLeaf",
        Old:      objNode(map[string]*schema.ModelNode{"outer": objNode(map[string]*schema.ModelNode{"inner": leaf(schema.Integer)})}),
        Incoming: map[string]any{"outer": map[string]any{"inner": json.Number("1"), "new": "s"}},
        Level:    spi.ChangeLevelStructural,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindAddProperty},
    },
    {
        Name:     "DeeplyNestedIntegerExtend",
        Old:      deepObject(10, leaf(schema.Integer)),
        Incoming: deepObjectValue(10, json.Number("1")),
        Level:    spi.ChangeLevelStructural,
    },
    {
        Name:     "WideObjectAddOne",
        Old:      wideObject(100, schema.String),
        Incoming: wideObjectValuePlus(100, "extra"),
        Level:    spi.ChangeLevelStructural,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindAddProperty},
    },

    // --- Arrays ---
    {
        Name:     "ArrayIntegerWidenToLong",
        Old:      schema.NewArrayNode(leaf(schema.Integer)),
        Incoming: []any{json.Number("1"), json.Number("9223372036854000000")},
        Level:    spi.ChangeLevelType,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindBroadenType},
    },
    {
        Name:     "ArrayOfObjectAddFieldInElement",
        Old:      schema.NewArrayNode(objNode(map[string]*schema.ModelNode{"k": leaf(schema.Integer)})),
        Incoming: []any{map[string]any{"k": json.Number("1"), "extra": "s"}},
        Level:    spi.ChangeLevelStructural,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindAddProperty},
    },
    {
        Name:     "ArrayOfObjectWidenLeafInElement",
        Old:      schema.NewArrayNode(objNode(map[string]*schema.ModelNode{"k": leaf(schema.Integer)})),
        Incoming: []any{map[string]any{"k": json.Number("1.5")}},
        Level:    spi.ChangeLevelType,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindBroadenType},
    },
    {
        Name:     "ArrayOfArrayWidenInnerLeaf",
        Old:      schema.NewArrayNode(schema.NewArrayNode(leaf(schema.Integer))),
        Incoming: []any{[]any{json.Number("1.5")}},
        Level:    spi.ChangeLevelType,
    },
    {
        Name:     "ArrayOfArrayOfArrayElement",
        Old:      schema.NewArrayNode(schema.NewArrayNode(schema.NewArrayNode(leaf(schema.Integer)))),
        Incoming: []any{[]any{[]any{json.Number("1")}}},
        Level:    spi.ChangeLevelStructural,
    },
    {
        Name:     "EmptyArrayObservesElement",
        Old:      schema.NewArrayNode(nil),
        Incoming: []any{json.Number("1")},
        Level:    spi.ChangeLevelArrayElements,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindAddArrayItemType},
    },
    {
        Name:     "PolymorphicLeafAddInteger",
        Old:      leaf(schema.String),
        Incoming: json.Number("1"),
        Level:    spi.ChangeLevelType,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindBroadenType},
    },
    {
        Name:     "IntegerFieldSeesDouble",
        Old:      leaf(schema.Integer),
        Incoming: json.Number("3.14"),
        Level:    spi.ChangeLevelType,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindBroadenType},
    },

    // --- Unicode + edge cases ---
    {
        Name:     "UnicodeKey4ByteCodepoint",
        Old:      objNode(map[string]*schema.ModelNode{"🐙": leaf(schema.String)}),
        Incoming: map[string]any{"🐙": "tentacle", "🦊": "fox"},
        Level:    spi.ChangeLevelStructural,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindAddProperty},
    },
    {
        Name:     "SameKeyNestedDifferentType",
        Old:      objNode(map[string]*schema.ModelNode{"a": objNode(map[string]*schema.ModelNode{"b": leaf(schema.Integer)})}),
        Incoming: map[string]any{"a": map[string]any{"b": json.Number("1"), "c": map[string]any{"b": "s"}}},
        Level:    spi.ChangeLevelStructural,
    },
    {
        Name:     "NullableFieldAppears",
        Old:      objNode(map[string]*schema.ModelNode{"a": leaf(schema.String)}),
        Incoming: map[string]any{"a": "x", "b": nil},
        Level:    spi.ChangeLevelStructural,
    },

    // --- Numeric boundaries (match A.1 rev 3 §2.3) ---
    {
        Name:     "IntegerBoundaryExceedsDouble", // 2^53+1
        Old:      leaf(schema.Integer),
        Incoming: json.Number("9007199254740993"),
        Level:    spi.ChangeLevelType,
    },
    {
        Name:     "LongBoundaryPromotesBigInteger", // 2^63
        Old:      leaf(schema.Long),
        Incoming: json.Number("9223372036854775808"),
        Level:    spi.ChangeLevelType,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindBroadenType},
    },
    {
        Name:     "BigIntegerBoundaryExceeds128", // 2^127
        Old:      leaf(schema.BigInteger),
        Incoming: json.Number("340282366920938463463374607431768211456"),
        Level:    spi.ChangeLevelType,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindBroadenType},
    },
    {
        Name:     "DecimalBoundaryFitsBigDecimal", // 18 fractional digits
        Old:      leaf(schema.Double),
        Incoming: json.Number("1.234567890123456789"),
        Level:    spi.ChangeLevelType,
    },
    {
        Name:     "DecimalBoundaryExceedsBigDecimal", // 20 fractional digits
        Old:      leaf(schema.BigDecimal),
        Incoming: json.Number("1.23456789012345678901"),
        Level:    spi.ChangeLevelType,
        ExpectedKinds: []schema.SchemaOpKind{schema.KindBroadenType},
    },

    // --- ChangeLevel enforcement cases ---
    {
        Name:     "ArrayLengthGrowsPermitted",
        Old:      schema.NewArrayNode(leaf(schema.Integer)),
        Incoming: []any{json.Number("1"), json.Number("2"), json.Number("3")},
        Level:    spi.ChangeLevelArrayLength,
    },
    {
        Name:     "TypeLevelRejectsStructural",
        Old:      objNode(map[string]*schema.ModelNode{"a": leaf(schema.Integer)}),
        Incoming: map[string]any{"a": json.Number("1"), "b": "extra"},
        Level:    spi.ChangeLevelType,
        ExpectError: true,
    },
    {
        Name:     "StrictValidateRejectsNewField",
        Old:      objNode(map[string]*schema.ModelNode{"a": leaf(schema.Integer)}),
        Incoming: map[string]any{"a": json.Number("1"), "b": "extra"},
        Level:    "",
        ExpectError: true,
    },

    // --- Remaining entries to reach ≥40: mixed permutations, arrays with nulls,
    //     sparse-key objects, broaden chains, idempotent-reapply fixtures etc.
    //     See §5.1 of the spec for the full list; the implementer fills the
    //     remaining 17+ entries following the patterns above.
}

// Test helpers exposed to catalog_test.go and callers.
func leaf(dt schema.DataType) *schema.ModelNode { return schema.NewLeafNode(dt) }

func objNode(fields map[string]*schema.ModelNode) *schema.ModelNode {
    n := schema.NewObjectNode()
    // Sorted key iteration — determinism.
    keys := make([]string, 0, len(fields))
    for k := range fields {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    for _, k := range keys {
        n.SetChild(k, fields[k])
    }
    return n
}

func deepObject(depth int, inner *schema.ModelNode) *schema.ModelNode {
    if depth == 0 {
        return inner
    }
    n := schema.NewObjectNode()
    n.SetChild("x", deepObject(depth-1, inner))
    return n
}

func deepObjectValue(depth int, leaf any) any {
    if depth == 0 {
        return leaf
    }
    return map[string]any{"x": deepObjectValue(depth-1, leaf)}
}

func wideObject(n int, dt schema.DataType) *schema.ModelNode {
    node := schema.NewObjectNode()
    for i := 0; i < n; i++ {
        node.SetChild("f"+strconv.Itoa(i), leaf(dt))
    }
    return node
}

func wideObjectValuePlus(n int, extraKey string) map[string]any {
    m := make(map[string]any, n+1)
    for i := 0; i < n; i++ {
        m["f"+strconv.Itoa(i)] = "v"
    }
    m[extraKey] = "v"
    return m
}
```

Add imports: `"sort"`, `"strconv"`.

**Important:** The catalog shown has ~22 entries. The implementer must add at least 18 more (or enough to reach ≥40) following the same pattern — each covering a Category from §5.1 of the spec. Do not mark the task complete until `TestCatalogHasAtLeast40Fixtures` passes.

- [ ] **Step 4: Run — verify pass**

Run: `go test ./internal/domain/model/schema/gentree/... -run TestCatalog -v`
Expected: PASS once ≥40 fixtures are present.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/schema/gentree/catalog.go internal/domain/model/schema/gentree/catalog_test.go
git commit -m "feat(schema/gentree): Catalog with ≥40 named regression fixtures"
```

---

## Task 7: Round-trip property test — I1 + I1-bis

**Files:**
- Create: `internal/domain/model/schema/roundtrip_property_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/domain/model/schema/roundtrip_property_test.go
package schema_test

import (
    "encoding/json"
    "fmt"
    "testing"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema/gentree"
)

// TestRoundtripCatalog — I1 master invariant on curated fixtures.
// I1-bis is checked automatically: nil-delta cases must correspond to
// byte-identical schemas.
func TestRoundtripCatalog(t *testing.T) {
    for _, f := range gentree.Catalog {
        f := f
        t.Run(f.Name, func(t *testing.T) {
            old := f.Old
            if old == nil {
                old = schema.NewObjectNode()
            }
            incomingNode, err := importer.Walk(f.Incoming)
            if err != nil {
                t.Fatalf("Walk: %v", err)
            }
            extended, extErr := schema.Extend(old, incomingNode, f.Level)
            if f.ExpectError {
                if extErr == nil {
                    t.Fatalf("%s: Extend unexpectedly succeeded at level %q", f.Name, f.Level)
                }
                return
            }
            if extErr != nil {
                t.Fatalf("%s: Extend failed: %v", f.Name, extErr)
            }
            assertRoundTrip(t, old, extended, f.Name)
            if f.ExpectedKinds != nil {
                assertDeltaKinds(t, old, extended, f.ExpectedKinds)
            }
        })
    }
}

// TestRoundtripRandomSeeds — 1000 random seeds; I1 + I1-bis.
func TestRoundtripRandomSeeds(t *testing.T) {
    cfg := gentree.DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    const N = 1000
    if testing.Short() {
        // under -short keep the full count to match the runtime budget
    }
    for i := 0; i < N; i++ {
        seed := int64(i + 1)
        t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
            r := gentree.NewRNG(seed)
            old := gentree.GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            incoming := gentree.GenExtensionPair(r, old, cfg.TargetLevel, cfg)
            incomingNode, err := importer.Walk(incoming)
            if err != nil {
                t.Fatalf("Walk: %v", err)
            }
            extended, err := schema.Extend(old, incomingNode, cfg.TargetLevel)
            if err != nil {
                // Additive extension at Structural should not fail for
                // well-formed generator output.
                t.Fatalf("Extend failed: %v", err)
            }
            assertRoundTrip(t, old, extended, fmt.Sprintf("seed=%d", seed))
        })
    }
}

// assertRoundTrip enforces I1 and I1-bis on a single (old, extended)
// pair. Marshal-equality is byte-level.
func assertRoundTrip(t *testing.T, old, extended *schema.ModelNode, label string) {
    t.Helper()
    delta, err := schema.Diff(old, extended)
    if err != nil {
        t.Fatalf("%s: Diff failed: %v", label, err)
    }
    applied, err := schema.Apply(old, delta)
    if err != nil {
        t.Fatalf("%s: Apply failed: %v", label, err)
    }
    appliedBytes, _ := schema.Marshal(applied)
    extendedBytes, _ := schema.Marshal(extended)
    if string(appliedBytes) != string(extendedBytes) {
        oldB, _ := schema.Marshal(old)
        t.Fatalf("%s: I1 violated\n  old=%s\n  extended=%s\n  applied =%s",
            label, oldB, extendedBytes, appliedBytes)
    }
    // I1-bis: delta==nil iff Marshal(old)==Marshal(extended).
    oldBytes, _ := schema.Marshal(old)
    marshalEqual := string(oldBytes) == string(extendedBytes)
    if (len(delta) == 0) != marshalEqual {
        t.Fatalf("%s: I1-bis violated: delta-nil=%v but Marshal-equal=%v\n  old=%s\n  extended=%s\n  delta=%s",
            label, len(delta) == 0, marshalEqual, oldBytes, extendedBytes, string(delta))
    }
}

// assertDeltaKinds — I6 bidirectional assertion per catalog-declared expected kinds.
func assertDeltaKinds(t *testing.T, old, extended *schema.ModelNode, expected []schema.SchemaOpKind) {
    t.Helper()
    delta, err := schema.Diff(old, extended)
    if err != nil {
        t.Fatalf("Diff: %v", err)
    }
    ops, err := schema.UnmarshalDelta(delta)
    if err != nil {
        t.Fatalf("UnmarshalDelta: %v", err)
    }
    gotKinds := make(map[schema.SchemaOpKind]int)
    for _, op := range ops {
        gotKinds[op.Kind]++
    }
    for _, want := range expected {
        if gotKinds[want] == 0 {
            opsJSON, _ := json.MarshalIndent(ops, "", "  ")
            t.Fatalf("expected kind %q not present in delta ops:\n%s", want, opsJSON)
        }
    }
}
```

- [ ] **Step 2: Run — verify pass on happy path, expose any real bugs**

Run: `go test ./internal/domain/model/schema/ -run 'TestRoundtripCatalog|TestRoundtripRandomSeeds' -v -timeout 120s`
Expected: PASS for all catalog entries and all 1000 random seeds. If any fails, that is a real bug in `Diff`/`Apply`/`Extend` — halt and surface rather than weaken the test (per spec §2.3).

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/roundtrip_property_test.go
git commit -m "test(schema): round-trip + Diff-nil-correspondence properties (I1, I1-bis, I6)"
```

---

## Task 8: Axis-2 kind matrix — hand-table with `t.Skip` for conflict cells

**Files:**
- Create: `internal/domain/model/schema/axis2_kind_matrix_test.go`

- [ ] **Step 1: Write the full matrix**

```go
// internal/domain/model/schema/axis2_kind_matrix_test.go
package schema_test

import (
    "testing"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// axis2Cell describes one cell of the (existingKind, incomingKind) matrix.
// Action: "roundtrip" asserts I1; "extendContract" asserts Extend is a no-op
// (silent-drop); "skip" marks polymorphic-slot cells deferred to A.3.
type axis2Cell struct {
    Name    string
    Old     *schema.ModelNode
    Value   any
    Level   spi.ChangeLevel
    Action  string // roundtrip | extendContract | skip
    SkipMsg string
}

// TrackingIssue references the polymorphic-slot tracking issue; filled
// when GitHub issue is created (Task 15). Leave as placeholder until then.
const polymorphicSlotIssue = "polymorphic-slot semantics pending — see issue #<N>"

func TestAxis2KindMatrix(t *testing.T) {
    leaf := func(dt schema.DataType) *schema.ModelNode { return schema.NewLeafNode(dt) }
    obj := func() *schema.ModelNode {
        n := schema.NewObjectNode()
        n.SetChild("k", leaf(schema.Integer))
        return n
    }
    arr := func() *schema.ModelNode { return schema.NewArrayNode(leaf(schema.Integer)) }

    cells := []axis2Cell{
        // Same-kind: round-trip properly.
        {"LL_same_type", leaf(schema.Integer), json.Number("1"), spi.ChangeLevelStructural, "roundtrip", ""},
        {"LL_broaden", leaf(schema.Integer), json.Number("1.5"), spi.ChangeLevelType, "roundtrip", ""},
        {"OO_add_field", obj(), map[string]any{"k": json.Number("1"), "new": "s"}, spi.ChangeLevelStructural, "roundtrip", ""},
        {"AA_same_element", arr(), []any{json.Number("1")}, spi.ChangeLevelStructural, "roundtrip", ""},

        // Kind-conflict cells (6 cells × whatever levels are in scope) — skip to A.3.
        {"LO_leaf_to_object", leaf(schema.Integer), map[string]any{"x": json.Number("1")}, spi.ChangeLevelStructural, "skip", polymorphicSlotIssue},
        {"LA_leaf_to_array", leaf(schema.Integer), []any{json.Number("1")}, spi.ChangeLevelStructural, "skip", polymorphicSlotIssue},
        {"OL_object_to_leaf", obj(), json.Number("1"), spi.ChangeLevelStructural, "skip", polymorphicSlotIssue},
        {"OA_object_to_array", obj(), []any{json.Number("1")}, spi.ChangeLevelStructural, "skip", polymorphicSlotIssue},
        {"AL_array_to_leaf", arr(), json.Number("1"), spi.ChangeLevelStructural, "skip", polymorphicSlotIssue},
        {"AO_array_to_object", arr(), map[string]any{"k": json.Number("1")}, spi.ChangeLevelStructural, "skip", polymorphicSlotIssue},

        // Silent-drop Extend-contract cells: verify Extend returns old unchanged
        // when confronted with incompatible kinds at restricted levels.
        {"LO_restricted_levelType_no_op", leaf(schema.Integer), map[string]any{"k": json.Number("1")}, spi.ChangeLevelType, "extendContract", ""},
    }

    for _, c := range cells {
        c := c
        t.Run(c.Name, func(t *testing.T) {
            if c.Action == "skip" {
                t.Skip(c.SkipMsg)
            }
            incomingNode, err := importer.Walk(c.Value)
            if err != nil {
                t.Fatalf("Walk: %v", err)
            }
            switch c.Action {
            case "roundtrip":
                extended, err := schema.Extend(c.Old, incomingNode, c.Level)
                if err != nil {
                    t.Fatalf("Extend: %v", err)
                }
                assertRoundTrip(t, c.Old, extended, c.Name)
            case "extendContract":
                extended, err := schema.Extend(c.Old, incomingNode, c.Level)
                if err != nil {
                    // Reject is an acceptable Extend-contract outcome;
                    // the contract is "no partial mutation".
                    return
                }
                oldBytes, _ := schema.Marshal(c.Old)
                extBytes, _ := schema.Marshal(extended)
                if string(oldBytes) != string(extBytes) {
                    t.Fatalf("%s: Extend silently mutated without error\n  old=%s\n  extended=%s", c.Name, oldBytes, extBytes)
                }
            default:
                t.Fatalf("unknown Action %q", c.Action)
            }
        })
    }
}
```

Add import: `"encoding/json"`.

- [ ] **Step 2: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestAxis2KindMatrix -v`
Expected: PASS for non-skip cells. Polymorphic-slot cells show as `SKIP` with the tracking-issue message.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/axis2_kind_matrix_test.go
git commit -m "test(schema): axis-2 kind matrix — conflict cells skipped to A.3"
```

---

## Task 9: Axis-3 ChangeLevel matrix — I7 in-memory atomicity

**Files:**
- Create: `internal/domain/model/schema/axis3_changelevel_test.go`

- [ ] **Step 1: Write the full matrix**

```go
// internal/domain/model/schema/axis3_changelevel_test.go
package schema_test

import (
    "encoding/json"
    "testing"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

type axis3Cell struct {
    Name     string
    Old      *schema.ModelNode
    Incoming any
    Level    spi.ChangeLevel
    Accept   bool
}

func TestAxis3ChangeLevelMatrix(t *testing.T) {
    leaf := func(dt schema.DataType) *schema.ModelNode { return schema.NewLeafNode(dt) }
    obj := func() *schema.ModelNode {
        n := schema.NewObjectNode()
        n.SetChild("k", leaf(schema.Integer))
        return n
    }
    arr := func(dt schema.DataType) *schema.ModelNode { return schema.NewArrayNode(leaf(dt)) }

    // Cells: each row is a (base, incoming, level, accept?) tuple.
    // Linear order: "" < ArrayLength < ArrayElements < Type < Structural.
    cells := []axis3Cell{
        // ArrayLength permits length changes only
        {"empty_level_rejects_new_field", obj(), map[string]any{"k": json.Number("1"), "new": "s"}, "", false},
        {"arrayLength_permits_growing", arr(schema.Integer), []any{json.Number("1"), json.Number("2")}, spi.ChangeLevelArrayLength, true},
        {"arrayLength_rejects_new_element_type", arr(schema.Integer), []any{"s"}, spi.ChangeLevelArrayLength, false},
        {"arrayElements_permits_new_element_type", arr(schema.Integer), []any{"s"}, spi.ChangeLevelArrayElements, true},
        {"type_permits_broaden", leaf(schema.Integer), json.Number("1.5"), spi.ChangeLevelType, true},
        {"type_rejects_structural", obj(), map[string]any{"k": json.Number("1"), "new": "s"}, spi.ChangeLevelType, false},
        {"structural_permits_add_property", obj(), map[string]any{"k": json.Number("1"), "new": "s"}, spi.ChangeLevelStructural, true},
    }

    for _, c := range cells {
        c := c
        t.Run(c.Name, func(t *testing.T) {
            // Capture old-bytes BEFORE Extend to check I7 atomicity on reject.
            oldBytesBefore, err := schema.Marshal(c.Old)
            if err != nil {
                t.Fatalf("Marshal old: %v", err)
            }
            incomingNode, err := importer.Walk(c.Incoming)
            if err != nil {
                t.Fatalf("Walk: %v", err)
            }
            _, extErr := schema.Extend(c.Old, incomingNode, c.Level)
            oldBytesAfter, _ := schema.Marshal(c.Old)

            // I7: rejection must not mutate input *ModelNode.
            if !c.Accept {
                if extErr == nil {
                    t.Fatalf("%s: expected Extend to reject at level %q, succeeded", c.Name, c.Level)
                }
                if string(oldBytesBefore) != string(oldBytesAfter) {
                    t.Fatalf("%s: I7 violated — input mutated by rejected Extend\n  before=%s\n  after =%s", c.Name, oldBytesBefore, oldBytesAfter)
                }
            } else {
                if extErr != nil {
                    t.Fatalf("%s: expected accept at level %q, got error: %v", c.Name, c.Level, extErr)
                }
            }
        })
    }
}
```

- [ ] **Step 2: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestAxis3ChangeLevelMatrix -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/axis3_changelevel_test.go
git commit -m "test(schema): axis-3 ChangeLevel matrix with I7 in-memory atomicity"
```

---

## Task 10: Commutativity property — I2

**Files:**
- Create: `internal/domain/model/schema/commutativity_property_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/domain/model/schema/commutativity_property_test.go
package schema_test

import (
    "fmt"
    "testing"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema/gentree"
)

// TestCommutativityPaired — I2: Apply(Apply(b,d1),d2) ≡ Apply(Apply(b,d2),d1)
// for deltas produced from a shared base by two independent generator draws.
func TestCommutativityPaired(t *testing.T) {
    cfg := gentree.DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    const N = 500
    for i := 0; i < N; i++ {
        seed := int64(i + 10_000)
        t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
            r := gentree.NewRNG(seed)
            base := gentree.GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            incomingA := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)
            incomingB := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)

            nodeA, err := importer.Walk(incomingA)
            if err != nil {
                t.Fatalf("Walk A: %v", err)
            }
            nodeB, err := importer.Walk(incomingB)
            if err != nil {
                t.Fatalf("Walk B: %v", err)
            }
            extA, err := schema.Extend(base, nodeA, cfg.TargetLevel)
            if err != nil {
                t.Skipf("Extend A rejected, skipping seed: %v", err)
            }
            extB, err := schema.Extend(base, nodeB, cfg.TargetLevel)
            if err != nil {
                t.Skipf("Extend B rejected, skipping seed: %v", err)
            }
            dA, _ := schema.Diff(base, extA)
            dB, _ := schema.Diff(base, extB)

            // Apply in both orders.
            ab, err := schema.Apply(base, dA)
            if err != nil {
                t.Fatal(err)
            }
            ab, err = schema.Apply(ab, dB)
            if err != nil {
                t.Fatal(err)
            }
            ba, err := schema.Apply(base, dB)
            if err != nil {
                t.Fatal(err)
            }
            ba, err = schema.Apply(ba, dA)
            if err != nil {
                t.Fatal(err)
            }
            b1, _ := schema.Marshal(ab)
            b2, _ := schema.Marshal(ba)
            if string(b1) != string(b2) {
                t.Fatalf("I2 violated\n  base=%s\n  ab  =%s\n  ba  =%s", mustMarshal(t, base), b1, b2)
            }
        })
    }
}

func mustMarshal(t *testing.T, n *schema.ModelNode) string {
    t.Helper()
    b, err := schema.Marshal(n)
    if err != nil {
        t.Fatal(err)
    }
    return string(b)
}
```

- [ ] **Step 2: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestCommutativityPaired -v -timeout 120s`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/commutativity_property_test.go
git commit -m "test(schema): commutativity property (I2) — 500 seeded pairs"
```

---

## Task 11: Monotonicity property — I3 direct + dual

**Files:**
- Create: `internal/domain/model/schema/monotonicity_property_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/domain/model/schema/monotonicity_property_test.go
package schema_test

import (
    "fmt"
    "testing"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema/gentree"
)

// TestMonotonicityDirect — I3: a document valid against B is also valid
// against Apply(B, d). Extension never narrows the accepted set.
func TestMonotonicityDirect(t *testing.T) {
    cfg := gentree.DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    const N = 200
    for i := 0; i < N; i++ {
        seed := int64(i + 20_000)
        t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
            r := gentree.NewRNG(seed)
            base := gentree.GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            doc := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)
            if errs := schema.Validate(base, doc); len(errs) > 0 {
                t.Skipf("doc not valid against base; skipping")
            }
            newDoc := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)
            newNode, err := importer.Walk(newDoc)
            if err != nil {
                t.Fatal(err)
            }
            extended, err := schema.Extend(base, newNode, cfg.TargetLevel)
            if err != nil {
                t.Skipf("Extend rejected: %v", err)
            }
            delta, _ := schema.Diff(base, extended)
            applied, err := schema.Apply(base, delta)
            if err != nil {
                t.Fatal(err)
            }
            if errs := schema.Validate(applied, doc); len(errs) > 0 {
                t.Fatalf("I3 direct violated: doc valid against base but not applied schema: %v", errs)
            }
        })
    }
}

// TestMonotonicityDual — a document rejected by Apply(B, d) is rejected
// by B at the same path for the same reason (no new rejection causes).
func TestMonotonicityDual(t *testing.T) {
    cfg := gentree.DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    const N = 200
    for i := 0; i < N; i++ {
        seed := int64(i + 30_000)
        t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
            r := gentree.NewRNG(seed)
            base := gentree.GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            newDoc := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)
            newNode, err := importer.Walk(newDoc)
            if err != nil {
                t.Fatal(err)
            }
            extended, err := schema.Extend(base, newNode, cfg.TargetLevel)
            if err != nil {
                t.Skipf("Extend rejected: %v", err)
            }
            delta, _ := schema.Diff(base, extended)
            applied, err := schema.Apply(base, delta)
            if err != nil {
                t.Fatal(err)
            }
            // Generate a validation probe that's intentionally rejected.
            probe := gentree.GenValue(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            appliedErrs := schema.Validate(applied, probe)
            if len(appliedErrs) == 0 {
                t.Skipf("probe not rejected by applied schema; skipping")
            }
            baseErrs := schema.Validate(base, probe)
            if len(baseErrs) == 0 {
                t.Fatalf("I3 dual violated: probe rejected by applied schema (%v) but accepted by base", appliedErrs)
            }
        })
    }
}
```

- [ ] **Step 2: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestMonotonicity -v -timeout 180s`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/monotonicity_property_test.go
git commit -m "test(schema): validation-monotonicity property (I3 direct + dual)"
```

---

## Task 12: Idempotence property — I4

**Files:**
- Create: `internal/domain/model/schema/idempotence_property_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/domain/model/schema/idempotence_property_test.go
package schema_test

import (
    "fmt"
    "testing"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema/gentree"
)

// TestIdempotenceApply — I4: Apply(Apply(b, d), d) == Apply(b, d).
func TestIdempotenceApply(t *testing.T) {
    cfg := gentree.DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    const N = 500
    for i := 0; i < N; i++ {
        seed := int64(i + 40_000)
        t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
            r := gentree.NewRNG(seed)
            base := gentree.GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            incoming := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)
            incomingNode, err := importer.Walk(incoming)
            if err != nil {
                t.Fatal(err)
            }
            extended, err := schema.Extend(base, incomingNode, cfg.TargetLevel)
            if err != nil {
                t.Skip(err)
            }
            delta, _ := schema.Diff(base, extended)
            once, err := schema.Apply(base, delta)
            if err != nil {
                t.Fatal(err)
            }
            twice, err := schema.Apply(once, delta)
            if err != nil {
                t.Fatal(err)
            }
            b1, _ := schema.Marshal(once)
            b2, _ := schema.Marshal(twice)
            if string(b1) != string(b2) {
                t.Fatalf("I4 violated\n  once =%s\n  twice=%s", b1, b2)
            }
        })
    }
}

// TestIdempotenceIngest — ingesting the same data twice yields the same
// schema (extension is idempotent, not double-widening).
func TestIdempotenceIngest(t *testing.T) {
    cfg := gentree.DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    const N = 300
    for i := 0; i < N; i++ {
        seed := int64(i + 50_000)
        t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
            r := gentree.NewRNG(seed)
            base := gentree.GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            data := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)
            node, err := importer.Walk(data)
            if err != nil {
                t.Fatal(err)
            }
            e1, err := schema.Extend(base, node, cfg.TargetLevel)
            if err != nil {
                t.Skip(err)
            }
            e2, err := schema.Extend(e1, node, cfg.TargetLevel)
            if err != nil {
                t.Fatal(err)
            }
            b1, _ := schema.Marshal(e1)
            b2, _ := schema.Marshal(e2)
            if string(b1) != string(b2) {
                t.Fatalf("Extend not idempotent\n  once =%s\n  twice=%s", b1, b2)
            }
        })
    }
}
```

- [ ] **Step 2: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestIdempotence -v -timeout 120s`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/idempotence_property_test.go
git commit -m "test(schema): idempotence property (I4) — Apply×2 and Ingest×2"
```

---

## Task 13: Permutation-invariance property — I5

**Files:**
- Create: `internal/domain/model/schema/permutation_property_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/domain/model/schema/permutation_property_test.go
package schema_test

import (
    "fmt"
    "testing"

    "github.com/cyoda-platform/cyoda-go-spi"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
    "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema/gentree"
)

// TestPermutationInvariance — I5: every permutation of 3 deltas applied
// to a shared base yields a Marshal-equal result.
func TestPermutationInvariance(t *testing.T) {
    cfg := gentree.DefaultConfig()
    cfg.TargetLevel = spi.ChangeLevelStructural
    const N = 200
    for i := 0; i < N; i++ {
        seed := int64(i + 60_000)
        t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
            r := gentree.NewRNG(seed)
            base := gentree.GenModelNode(r, cfg.MaxDepth, cfg.MaxWidth, cfg)
            deltas := make([]spi.SchemaDelta, 0, 3)
            for k := 0; k < 3; k++ {
                d := gentree.GenExtensionPair(r, base, cfg.TargetLevel, cfg)
                node, err := importer.Walk(d)
                if err != nil {
                    t.Fatal(err)
                }
                ext, err := schema.Extend(base, node, cfg.TargetLevel)
                if err != nil {
                    t.Skip(err)
                }
                delta, _ := schema.Diff(base, ext)
                deltas = append(deltas, delta)
            }
            perms := [][]int{
                {0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0},
            }
            var canon string
            for idx, p := range perms {
                cur := base
                for _, j := range p {
                    next, err := schema.Apply(cur, deltas[j])
                    if err != nil {
                        t.Fatal(err)
                    }
                    cur = next
                }
                b, _ := schema.Marshal(cur)
                if idx == 0 {
                    canon = string(b)
                    continue
                }
                if string(b) != canon {
                    t.Fatalf("I5 violated\n  perm %v: %s\n  canon: %s", p, b, canon)
                }
            }
        })
    }
}
```

- [ ] **Step 2: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestPermutationInvariance -v -timeout 180s`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/permutation_property_test.go
git commit -m "test(schema): N-permutation-invariance property (I5) — 3-delta × 6 perms"
```

---

## Task 14: File polymorphic-slot tracking issue + wire reference

**Files:**
- Modify: `internal/domain/model/schema/axis2_kind_matrix_test.go` (replace `<N>` placeholder)

- [ ] **Step 1: File the GitHub issue**

```bash
gh issue create --title "Sub-project A.3: polymorphic-slot kind-conflict semantics" --body "$(cat <<'EOF'
## Scope

Implement Extend/Diff/Apply handling for kind-conflict cells (LEAF↔OBJECT, LEAF↔ARRAY, OBJECT↔ARRAY). These cells are currently registered as \`t.Skip\` in \`internal/domain/model/schema/axis2_kind_matrix_test.go\` and await this sub-project's design cycle.

## Authoritative references

- Design spec: \`docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a2-design.md\` §2.1 (Axis 2) and §5.4.
- Skip-test locations: \`internal/domain/model/schema/axis2_kind_matrix_test.go\` — cells \`LO_*\`, \`LA_*\`, \`OL_*\`, \`OA_*\`, \`AL_*\`, \`AO_*\`.
- Context on overall numeric classifier divergence: \`docs/superpowers/specs/2026-04-21-data-ingestion-qa-subproject-a1-design.md\` rev 3 §2.3.

## Acceptance

Replace each \`t.Skip\` in the axis-2 matrix with the round-trip assertion once Extend/Diff/Apply support the polymorphic-slot shape. Full property suite remains within the 60-s CI budget.

EOF
)"
```

Capture the issue number returned (say, #81). Note it.

- [ ] **Step 2: Wire the issue number**

Edit `internal/domain/model/schema/axis2_kind_matrix_test.go`:
```go
// Replace:
const polymorphicSlotIssue = "polymorphic-slot semantics pending — see issue #<N>"
// With:
const polymorphicSlotIssue = "polymorphic-slot semantics pending — see issue #81"
```

- [ ] **Step 3: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestAxis2KindMatrix -v`
Expected: PASS; skip-cells show the real issue number.

- [ ] **Step 4: Commit**

```bash
git add -u
git commit -m "test(schema): wire A.3 tracking issue number into skip messages"
```

---

## Task 15: Performance budget meta-test

**Files:**
- Create: `internal/domain/model/schema/property_budget_test.go`

- [ ] **Step 1: Write the meta-test**

```go
// internal/domain/model/schema/property_budget_test.go
package schema_test

import (
    "os/exec"
    "testing"
    "time"
)

// TestPropertyBudget runs the property suite end-to-end and asserts it
// completes within the CI-ceiling of 60 s. Advisory local target is 45 s
// — surfaced via t.Logf but not enforced.
//
// This test invokes `go test -short -run 'TestRoundtrip|TestCommutativity|TestMonotonicity|TestIdempotence|TestPermutation'`
// as a subprocess so the measurement excludes TestPropertyBudget's own cost.
func TestPropertyBudget(t *testing.T) {
    if testing.Short() {
        t.Skip("budget meta-test is a slow sanity check, skipped under -short")
    }
    start := time.Now()
    cmd := exec.Command("go", "test", "-short",
        "-run", "TestRoundtrip|TestCommutativity|TestMonotonicity|TestIdempotence|TestPermutation",
        "github.com/cyoda-platform/github.com/cyoda-platform/cyoda-go/internal/domain/model/schema")
    out, err := cmd.CombinedOutput()
    elapsed := time.Since(start)
    if err != nil {
        t.Fatalf("subprocess failed: %v\n%s", err, out)
    }
    t.Logf("property suite: %v (advisory target <= 45s local)", elapsed)
    if elapsed > 60*time.Second {
        t.Fatalf("property suite exceeded CI budget: %v > 60s", elapsed)
    }
}
```

- [ ] **Step 2: Run — verify**

Run: `go test ./internal/domain/model/schema/ -run TestPropertyBudget -v -timeout 180s`
Expected: PASS with logged elapsed time. If it fails, profile the property tests and reduce sample counts in `internal/domain/model/schema/*_property_test.go` until within budget — do NOT weaken invariants.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/schema/property_budget_test.go
git commit -m "test(schema): runtime budget meta-test — 60 s CI hard fail"
```

---

## Task 16: Final verification pass

- [ ] **Step 1: Run full test suite (short)**

Run: `go test -short ./...`
Expected: all green.

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 3: Race detector on schema package**

Run: `go test -race -short ./internal/domain/model/schema/...`
Expected: clean.

- [ ] **Step 4: E2E regression — existing model-schema-extensions E2E**

Run: `go test ./e2e/parity/... -v -run 'SchemaExtensionsSequentialFoldAcrossRequests|DeepSchemaSymmetry'`
Expected: green.

- [ ] **Step 5: End-of-deliverable race sanity**

Run: `go test -race ./...`
Expected: clean (race detector is one-shot per `.claude/rules/race-testing.md`).

- [ ] **Step 6: Final commit if any fixes were needed**

If any step surfaced issues, fix via the same red/green TDD discipline and commit. Otherwise no-op.

---

## Done checklist (success criteria from spec §6)

- [ ] I1, I1-bis, I2, I3+dual+corollary, I4, I5, I6, I7 verified by passing tests.
- [ ] Generator produces every Axis-1 shape at freq ≥ 1 per 50 samples (`TestCoverageDistribution`).
- [ ] `TestGeneratorIsMapFree` passes.
- [ ] `Catalog` contains ≥ 40 fixtures.
- [ ] Polymorphic-slot skip-tests reference a filed tracking issue (#<N> resolved to real number).
- [ ] Property suite runs ≤ 60 s on CI (`TestPropertyBudget`).
- [ ] `go test -short ./...` green, `go vet ./...` clean, race detector clean on schema package.
