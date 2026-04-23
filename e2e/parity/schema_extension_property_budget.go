package parity

import (
	"testing"
	"time"
)

// PropertyBudgetPerBackend is the per-backend wall-clock ceiling for
// the SchemaExtensionByteIdentityProperty entry. Spec §7.3 sets a
// 120 s total CI ceiling across all backends; 40 s per backend gives
// ~3× headroom over current runtimes (~10 s observed).
const PropertyBudgetPerBackend = 40 * time.Second

// RunSchemaExtensionPropertyBudget re-runs the property entry against
// the given fixture and asserts the wall-clock stays under the
// per-backend CI share. Invoked from each backend's test package so
// a regression (e.g. seed-count bump, per-iteration cost increase)
// fails deterministically at build time instead of silently bloating
// CI. Skipped under -short like the property entry itself.
func RunSchemaExtensionPropertyBudget(t *testing.T, fixture BackendFixture) {
	if testing.Short() {
		t.Skip("property-budget check runs only in full mode")
	}
	start := time.Now()
	RunSchemaExtensionByteIdentityProperty(t, fixture)
	elapsed := time.Since(start)
	t.Logf("SchemaExtensionByteIdentityProperty wall clock: %v (budget %v)", elapsed, PropertyBudgetPerBackend)
	if elapsed > PropertyBudgetPerBackend {
		t.Fatalf("SchemaExtensionByteIdentityProperty exceeded per-backend CI budget: %v > %v", elapsed, PropertyBudgetPerBackend)
	}
}
