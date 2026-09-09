// internal/domain/model/schema/property_skip_guard_test.go
package schema_test

import "testing"

// maxPropertySkipRatio bounds how much of a property suite's seed budget
// may end in t.Skip (the generator's draw was rejected by Extend at the
// configured level) before the suite is no longer trustworthy.
//
// property_budget_test.go enforces a TIME budget, not a coverage one: a
// suite that skips 95% of its seeds and races through the rest would still
// be green and fast, and nothing would notice that it stopped exercising
// the property it claims to state. The value-based Admit rule (Task 8/9)
// changes which generator draws Extend rejects — a future change could do
// the same again — so this ceiling exists specifically to catch a suite
// going quietly vacuous, which a wall-clock budget cannot.
//
// 0.5 is deliberately loose: today's suites measure well under it (see the
// fix report in task-9-report.md for the per-suite ratios from a real run).
// The point is not to pin the current ratio tightly, only to fail loudly if
// a suite crosses from "occasionally rejects an incompatible draw" into
// "mostly skips."
const maxPropertySkipRatio = 0.5

// assertSkipRatio fails t if skipped seeds exceed maxPropertySkipRatio of
// the total (ran + skipped). Call it once, after a property suite's seed
// loop has finished, with the counts accumulated during that loop.
func assertSkipRatio(t *testing.T, ran, skipped int, label string) {
	t.Helper()
	total := ran + skipped
	if total == 0 {
		return
	}
	ratio := float64(skipped) / float64(total)
	if ratio > maxPropertySkipRatio {
		t.Fatalf("%s: skip ratio %.2f (%d/%d skipped) exceeds the %.2f ceiling — "+
			"the generator may no longer be producing draws this property can "+
			"exercise; a green, fast run is not enough on its own to trust this suite",
			label, ratio, skipped, total, maxPropertySkipRatio)
	}
}
