package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A leaf declared DOUBLE admits a whole number: INTEGER widens into DOUBLE,
// so the write changes nothing about the model's admitted value space and
// must not spend a ChangeLevel permission. Before the fix the write path
// classified the value's type from the value alone and compared labels, so
// every whole-number write to a DOUBLE leaf was refused as a "type change"
// on any model configured below TYPE level — ARRAY_LENGTH and
// ARRAY_ELEMENTS, both ordinary settings.
func TestModelExtension_WholeNumberIntoDoubleLeaf(t *testing.T) {
	for _, level := range []string{"ARRAY_LENGTH", "ARRAY_ELEMENTS", "TYPE", "STRUCTURAL"} {
		t.Run(level, func(t *testing.T) {
			model := "e2e-double-whole-" + level
			// The sample declares a DOUBLE leaf and a DOUBLE array element.
			importModelSampleE2E(t, model, 1, `{"amount":10.5,"amounts":[1.5,2.5]}`)
			lockModelE2E(t, model, 1)
			setChangeLevelE2E(t, model, 1, level)

			before := exportModelE2E(t, model, 1)

			// All three spellings of a whole number classify identically —
			// the walker strips trailing zeros and normalises exponents.
			for _, payload := range []string{
				`{"amount":1000,"amounts":[3,4]}`,
				`{"amount":1000.0,"amounts":[3.0,4.0]}`,
				`{"amount":1e3,"amounts":[3e0,4e0]}`,
			} {
				status, body := createEntityRawE2E(t, model, 1, payload)
				if status != http.StatusOK {
					t.Fatalf("write %s: status = %d, want 200; body: %s", payload, status, body)
				}
			}

			// The model still declares exactly what it did: a whole number
			// into a DOUBLE leaf is not a schema change at any level.
			after := exportModelE2E(t, model, 1)
			beforeJSON, _ := json.Marshal(before)
			afterJSON, _ := json.Marshal(after)
			if string(beforeJSON) != string(afterJSON) {
				t.Errorf("model changed under a whole-number write to a DOUBLE leaf\n  before: %s\n  after:  %s",
					beforeJSON, afterJSON)
			}
		})
	}
}

// The boundary, and why it moved. Classification by LABEL condemned every
// whole number past 2^31 as LONG, and LONG does not widen into DOUBLE
// because 2^63 exceeds DOUBLE's 53-bit mantissa. The mantissa argument is
// right; the instrument was wrong. 2147483648 is ten significant digits and
// exactly representable, and it was refused only by association with values
// that are not.
//
// Admission judges the value: a decimal of at most 15 significant digits
// round-trips uniquely through a binary64 double, which is what DOUBLE's
// findability and the lossless float8 pushdown need, so 2147483648 (ten
// digits) is held at the most restrictive level with no model change, while
// 9007199254740993 (sixteen) is not — it is still a genuine type change:
// refused below TYPE with the level named, and at TYPE it widens the leaf
// rather than slipping in silently.
func TestModelExtension_WholeNumberPastIntegerRangeIntoDoubleLeaf(t *testing.T) {
	const model = "e2e-double-long"
	importModelSampleE2E(t, model, 1, `{"amount":10.5}`)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "ARRAY_LENGTH")

	before := exportModelE2E(t, model, 1)

	// Within DOUBLE's mantissa, held at the most restrictive level.
	status, body := createEntityRawE2E(t, model, 1, `{"amount":2147483648}`)
	if status != http.StatusOK {
		t.Fatalf("2147483648 needs 10 significant digits, DOUBLE holds it; status = %d, want 200; body: %s", status, body)
	}
	after := exportModelE2E(t, model, 1)
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Errorf("model changed under a held whole-number write\n  before: %s\n  after:  %s", beforeJSON, afterJSON)
	}

	// Past the mantissa boundary, still a genuine type change.
	status, body = createEntityRawE2E(t, model, 1, `{"amount":9007199254740993}`)
	if status != http.StatusBadRequest {
		t.Fatalf("9007199254740993 needs 16 significant digits, past DOUBLE's mantissa; status = %d, want 400; body: %s", status, body)
	}
	if !strings.Contains(body, "TYPE") {
		t.Errorf("the rejection must name the level that resolves it; body: %s", body)
	}

	// Raising the level resolves it, and the leaf really does widen — the
	// two types share no common type below UNBOUND_DECIMAL.
	setChangeLevelE2E(t, model, 1, "TYPE")
	status, body = createEntityRawE2E(t, model, 1, `{"amount":9007199254740993}`)
	if status != http.StatusOK {
		t.Fatalf("TYPE level permits it; status = %d, want 200; body: %s", status, body)
	}
	exported, _ := json.Marshal(exportModelE2E(t, model, 1))
	if !strings.Contains(string(exported), "UNBOUND_DECIMAL") {
		t.Errorf("the leaf must have widened to UNBOUND_DECIMAL; schema: %s", exported)
	}
}
