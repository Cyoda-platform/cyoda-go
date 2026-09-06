package e2e_test

// type_admission_test.go drives the design's §9 error/status table and the
// e2e-layer cells of its §10 coverage matrix through the full HTTP stack
// (PostgreSQL + httptest.Server + JWT auth). See
// docs/superpowers/specs/2026-09-04-555-type-admission-design.md.
//
// Rows already covered elsewhere are NOT duplicated here:
//   - the whole-number-into-DOUBLE invariant across all four change levels,
//     and the 2147483648 / 9007199254740993 mantissa boundary
//     (model_double_whole_number_test.go — Task 12's inverted assertions);
//   - kind mismatch, array into scalar (§9's other 400 row) and the
//     unique-key non-scalar guard — both pre-existing, both unrelated to
//     value admission and unaffected by this design, and both already
//     exercised through the full HTTP stack
//     (model_kind_enforcement_test.go's TestModelKindEnforcement_*Rejects*
//     and model_kind_branch_extension_test.go's
//     TestModelKindBranch_KeyedPathCannotGainAContainer);
//   - homogeneous array-element growth at ARRAY_ELEMENTS and array-width
//     growth at ARRAY_LENGTH, both pre-existing and mechanism-level rather
//     than value-admission-specific (model_extension_test.go).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// noProcessorTypeAdmissionWorkflow is a workflow whose single automatic
// transition needs no processor — used by the rows below that need an entity
// to reach a queryable (searchable) state.
func noProcessorTypeAdmissionWorkflow(name string) string {
	return fmt.Sprintf(`{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1.1", "name": %q, "initialState": "NONE", "active": true,
			"states": {
				"NONE": {"transitions": [{"name": "init", "next": "CREATED", "manual": false}]},
				"CREATED": {}
			}
		}]
	}`, name)
}

// assertModelUnchanged fails the test if the two exported models differ,
// byte for byte. Used throughout to pin "held ⟹ the model does not move".
func assertModelUnchanged(t *testing.T, before, after map[string]any) {
	t.Helper()
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Errorf("model changed under a write that should have been held\n  before: %s\n  after:  %s", beforeJSON, afterJSON)
	}
}

// --- §9 row 1: held value, no changeLevel → 200. This is the case no
// configuration could work around — strict validation never consults the
// level, so before this design nothing could make this write succeed. ---

func TestTypeAdmission_HeldValueNoChangeLevel_Succeeds(t *testing.T) {
	t.Run("whole number past 2^31 into a DOUBLE leaf", func(t *testing.T) {
		const model = "e2e-typeadm-strict-double"
		importModelSampleE2E(t, model, 1, `{"amount":10.5}`)
		lockModelE2E(t, model, 1)
		// No changeLevel call at all: strict validation.

		before := exportModelE2E(t, model, 1)
		status, body := createEntityRawE2E(t, model, 1, `{"amount":2147483648}`)
		if status != http.StatusOK {
			t.Fatalf("2147483648 is held by a DOUBLE leaf; status = %d, want 200; body: %s", status, body)
		}
		after := exportModelE2E(t, model, 1)
		assertModelUnchanged(t, before, after)
	})

	t.Run("date-shaped string into a STRING leaf", func(t *testing.T) {
		const model = "e2e-typeadm-strict-string"
		importModelSampleE2E(t, model, 1, `{"note":"hello"}`)
		lockModelE2E(t, model, 1)

		before := exportModelE2E(t, model, 1)
		status, body := createEntityRawE2E(t, model, 1, `{"note":"2026-03-01"}`)
		if status != http.StatusOK {
			t.Fatalf("a date-shaped string is held by a STRING leaf; status = %d, want 200; body: %s", status, body)
		}
		after := exportModelE2E(t, model, 1)
		assertModelUnchanged(t, before, after)
	})
}

// --- §9 row 2: held value, level below TYPE → 200. Mirrors the DOUBLE
// whole-number case already pinned for the numeric family
// (model_double_whole_number_test.go); this is the STRING/temporal side of
// the same rule. ---

func TestTypeAdmission_HeldValueBelowTypeLevel_Succeeds(t *testing.T) {
	for _, level := range []string{"ARRAY_LENGTH", "ARRAY_ELEMENTS", "TYPE", "STRUCTURAL"} {
		t.Run(level, func(t *testing.T) {
			model := "e2e-typeadm-below-type-" + level
			importModelSampleE2E(t, model, 1, `{"note":"hello"}`)
			lockModelE2E(t, model, 1)
			setChangeLevelE2E(t, model, 1, level)

			before := exportModelE2E(t, model, 1)
			status, body := createEntityRawE2E(t, model, 1, `{"note":"2026-03-01"}`)
			if status != http.StatusOK {
				t.Fatalf("a date-shaped string is held at any level; status = %d, want 200; body: %s", status, body)
			}
			after := exportModelE2E(t, model, 1)
			assertModelUnchanged(t, before, after)
		})
	}
}

// --- §9 row 3: unheld value, no changeLevel → 400 INCOMPATIBLE_TYPE, with
// the structured fieldPath/expectedType/actualType Props. No existing
// internal/e2e test asserted these Props through the full HTTP stack. ---

func TestTypeAdmission_UnheldNoChangeLevel_IncompatibleTypeWithProps(t *testing.T) {
	const model = "e2e-typeadm-strict-unheld"
	importModelSampleE2E(t, model, 1, `{"note":"hello"}`)
	lockModelE2E(t, model, 1)

	before := exportModelE2E(t, model, 1)
	status, body := createEntityRawE2E(t, model, 1, `{"note":5}`)
	if status != http.StatusBadRequest {
		t.Fatalf("a number is not a string; status = %d, want 400; body: %s", status, body)
	}
	assertErrorCode(t, body, "INCOMPATIBLE_TYPE")

	var pd struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(body), &pd); err != nil {
		t.Fatalf("decode problem detail: %v; body: %s", err, body)
	}
	if got := pd.Properties["fieldPath"]; got != "note" {
		t.Errorf("properties.fieldPath: got %v, want %q", got, "note")
	}
	expected, ok := pd.Properties["expectedType"].([]any)
	if !ok || len(expected) != 1 || expected[0] != "STRING" {
		t.Errorf(`properties.expectedType: got %v, want ["STRING"]`, pd.Properties["expectedType"])
	}
	if got, ok := pd.Properties["actualType"].(string); !ok || got == "" {
		t.Errorf("properties.actualType: got %v, want a non-empty type label", pd.Properties["actualType"])
	}

	after := exportModelE2E(t, model, 1)
	assertModelUnchanged(t, before, after)
}

// --- §9 rows 4-5: unheld value below TYPE → 400 VALIDATION_FAILED naming the
// level; at TYPE → 200, model widened. Uses the temporal side (a year is not
// a timestamp) rather than the numeric side already pinned by
// model_double_whole_number_test.go, and doubles as §10's "\"2026\" into
// ZONED_DATE_TIME still gated" row. ---

func TestTypeAdmission_UnheldBelowTypeLevel_ThenWidensAtType(t *testing.T) {
	const model = "e2e-typeadm-zoned-widen"
	importModelSampleE2E(t, model, 1, `{"ts":"2026-03-01T10:00:00Z"}`)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "ARRAY_LENGTH")

	before := exportModelE2E(t, model, 1)
	status, body := createEntityRawE2E(t, model, 1, `{"ts":"2026"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("a year is not a timestamp — still a genuine type change; status = %d, want 400; body: %s", status, body)
	}
	if !strings.Contains(body, "VALIDATION_FAILED") {
		t.Errorf("body must carry VALIDATION_FAILED; body: %s", body)
	}
	if !strings.Contains(body, "TYPE") {
		t.Errorf("body must name the level that resolves it; body: %s", body)
	}
	after := exportModelE2E(t, model, 1)
	assertModelUnchanged(t, before, after)

	setChangeLevelE2E(t, model, 1, "TYPE")
	status, body = createEntityRawE2E(t, model, 1, `{"ts":"2026"}`)
	if status != http.StatusOK {
		t.Fatalf("TYPE level permits the widen; status = %d, want 200; body: %s", status, body)
	}
	exported := exportModelE2E(t, model, 1)
	exportedJSON, _ := json.Marshal(exported)
	if !strings.Contains(string(exportedJSON), "YEAR") {
		t.Errorf("the leaf must have widened to include YEAR; schema: %s", exportedJSON)
	}
}

// --- §9 row 6: wrong JSON kind for every declared type → 400, unchanged.
// One declared type per scalar JSON kind, and every cross-kind write is
// still refused as INCOMPATIBLE_TYPE. Also fills §10's "JSON number into
// STRING still gated", "JSON boolean into STRING still gated" and "JSON
// string \"2024\" into INTEGER still gated" rows. ---

func TestTypeAdmission_WrongJSONKindForEveryDeclaredType(t *testing.T) {
	const model = "e2e-typeadm-wrong-kind"
	importModelSampleE2E(t, model, 1, `{"s":"x","n":1,"b":true}`)
	lockModelE2E(t, model, 1)

	cases := []struct{ name, payload string }{
		{"number into STRING", `{"s":5}`},
		{"boolean into STRING", `{"s":true}`},
		{"string into INTEGER", `{"n":"2024"}`},
		{"boolean into INTEGER", `{"n":true}`},
		{"string into BOOLEAN", `{"b":"true"}`},
		{"number into BOOLEAN", `{"b":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := createEntityRawE2E(t, model, 1, tc.payload)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", status, body)
			}
			assertErrorCode(t, body, "INCOMPATIBLE_TYPE")
		})
	}
}

// --- §9 row 8: PUT with a held value → 200. ---

func TestTypeAdmission_PUT_HeldValue_Succeeds(t *testing.T) {
	const model = "e2e-typeadm-put"
	setupModelSampleWithWorkflow(t, model, `{"note":"hello"}`, noProcessorTypeAdmissionWorkflow("typeadm-put-wf"))

	entityID := createEntityE2E(t, model, 1, `{"note":"plain"}`)

	resp := doAuth(t, http.MethodPut, fmt.Sprintf("/api/entity/JSON/%s", entityID), `{"note":"2026-03-01"}`)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT with a held value; status = %d, want 200; body: %s", resp.StatusCode, body)
	}
}

// --- §9 row 9: PATCH with a held value → 200. ---

func TestTypeAdmission_PATCH_HeldValue_Succeeds(t *testing.T) {
	const model = "e2e-typeadm-patch"
	importModelSampleE2E(t, model, 1, `{"note":"hello"}`)
	lockModelE2E(t, model, 1)

	entityID, createTxID := createEntityE2EWithTxID(t, model, 1, `{"note":"plain"}`)

	resp := patchEntity(t,
		fmt.Sprintf("/api/entity/JSON/%s", entityID),
		"application/merge-patch+json",
		createTxID,
		`{"note":"2026-03-01"}`,
	)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH with a held value; status = %d, want 200; body: %s", resp.StatusCode, body)
	}
}

// --- §9 row 10: POST .../collection with a held value → 200. ---

func TestTypeAdmission_CollectionCreate_HeldValue_Succeeds(t *testing.T) {
	const model = "e2e-typeadm-collection"
	importModelSampleE2E(t, model, 1, `{"note":"hello"}`)
	lockModelE2E(t, model, 1)

	status, body := collectionCreate(t, []string{
		collectionItem(model, 1, `{"note":"2026-03-01"}`),
	})
	if status != http.StatusOK {
		t.Fatalf("collection-create with a held value; status = %d, want 200; body: %s", status, body)
	}
}

// --- §9 processor-output ingress row: a processor returning a date-shaped
// string into a STRING leaf now completes the transition, where before it
// failed it (WORKFLOW_FAILED) — a user-visible workflow behaviour change,
// not just an API one. Strict validation (no changeLevel), so no
// configuration could have avoided the old failure either. ---

func TestTypeAdmission_ProcessorOutput_HeldValueCompletesTransition(t *testing.T) {
	const model = "e2e-typeadm-procout"
	registerRewriter(t, "typeadm-procout-date", `{"note":"2026-03-01","status":"new"}`)
	defer procSvc.Reset()

	setupModelSampleWithWorkflow(t, model, `{"note":"hello","status":"new"}`, procOutputWorkflow("typeadm-procout-date"))

	resp := doAuth(t, http.MethodPost, fmt.Sprintf("/api/entity/JSON/%s/1", model),
		`{"note":"x","status":"new"}`)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a processor returning a date-shaped string into a STRING leaf must complete the transition; status = %d, want 200; body: %s", resp.StatusCode, body)
	}
}

// --- §9 search table. ---

func TestTypeAdmission_Search(t *testing.T) {
	t.Run("EQUALS 5.0 finds and NOT_EQUAL 5.0 excludes a stored 5 on INTEGER", func(t *testing.T) {
		const model = "e2e-typeadm-search-int5"
		setupModelSampleWithWorkflow(t, model, `{"amount":10}`, noProcessorTypeAdmissionWorkflow("typeadm-int5-wf"))
		createEntityE2E(t, model, 1, `{"amount":5}`)

		_, hits := directSearch(t, model, 1, `{"type":"simple","jsonPath":"$.amount","operatorType":"EQUALS","value":5.0}`)
		if len(hits) != 1 {
			t.Errorf("EQUALS 5.0 must find the stored 5; got %d hits", len(hits))
		}
		_, hits = directSearch(t, model, 1, `{"type":"simple","jsonPath":"$.amount","operatorType":"NOT_EQUAL","value":5.0}`)
		if len(hits) != 0 {
			t.Errorf("NOT_EQUAL 5.0 must not match the stored 5; got %d hits", len(hits))
		}
	})

	t.Run("EQUALS with trailing zeros finds a stored 5 on DOUBLE", func(t *testing.T) {
		const model = "e2e-typeadm-search-double5"
		setupModelSampleWithWorkflow(t, model, `{"amount":10.5}`, noProcessorTypeAdmissionWorkflow("typeadm-double5-wf"))
		createEntityE2E(t, model, 1, `{"amount":5}`)

		_, hits := directSearch(t, model, 1, `{"type":"simple","jsonPath":"$.amount","operatorType":"EQUALS","value":5.000000000000000000}`)
		if len(hits) != 1 {
			t.Errorf("EQUALS 5.000...0 must find the stored 5 on a DOUBLE leaf; got %d hits", len(hits))
		}
	})

	t.Run("NOT_EQUAL -0.0 does not match a stored 0", func(t *testing.T) {
		const model = "e2e-typeadm-search-negzero"
		setupModelSampleWithWorkflow(t, model, `{"amount":10.5}`, noProcessorTypeAdmissionWorkflow("typeadm-negzero-wf"))
		createEntityE2E(t, model, 1, `{"amount":0}`)

		_, hits := directSearch(t, model, 1, `{"type":"simple","jsonPath":"$.amount","operatorType":"NOT_EQUAL","value":-0.0}`)
		if len(hits) != 0 {
			t.Errorf("NOT_EQUAL -0.0 must not match a stored 0; got %d hits", len(hits))
		}
	})

	t.Run("[DOUBLE] LESS_THAN 1e300 finds a stored 2147483648", func(t *testing.T) {
		const model = "e2e-typeadm-search-ceiling-compare"
		setupModelSampleWithWorkflow(t, model, `{"amount":10.5}`, noProcessorTypeAdmissionWorkflow("typeadm-ceilcmp-wf"))
		createEntityE2E(t, model, 1, `{"amount":2147483648}`)

		_, hits := directSearch(t, model, 1, `{"type":"simple","jsonPath":"$.amount","operatorType":"LESS_THAN","value":1e300}`)
		if len(hits) != 1 {
			t.Errorf("[DOUBLE] < 1e300 must find the stored 2147483648 (the out-of-range NotNull residual); got %d hits", len(hits))
		}
	})

	t.Run("a condition no declared type accepts is 400 CONDITION_TYPE_MISMATCH", func(t *testing.T) {
		const model = "e2e-typeadm-search-mismatch"
		setupModelSampleWithWorkflow(t, model, `{"amount":10}`, noProcessorTypeAdmissionWorkflow("typeadm-mismatch-wf"))
		createEntityE2E(t, model, 1, `{"amount":5}`)

		status, body := searchDirectRaw(t, model, 1,
			`{"type":"simple","jsonPath":"$.amount","operatorType":"GREATER_THAN","value":"not-a-number"}`, "")
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body: %s", status, body)
		}
		assertErrorCode(t, body, "CONDITION_TYPE_MISMATCH")
	})
}

// --- §10 rows not covered above: the DOUBLE ceiling and its gated
// neighbours (magnitude, precision, scale). 9007199254740993 (the precision
// boundary already at Task 12's exact value) is intentionally NOT repeated
// here. ---

func TestTypeAdmission_DoubleBoundaries(t *testing.T) {
	t.Run("value at the ceiling is held and then found", func(t *testing.T) {
		const model = "e2e-typeadm-double-ceiling"
		setupModelSampleWithWorkflow(t, model, `{"amount":10.5}`, noProcessorTypeAdmissionWorkflow("typeadm-ceiling-wf"))

		before := exportModelE2E(t, model, 1)
		createEntityE2E(t, model, 1, `{"amount":9.99999999999999e292}`)
		after := exportModelE2E(t, model, 1)
		assertModelUnchanged(t, before, after)

		_, hits := directSearch(t, model, 1, `{"type":"simple","jsonPath":"$.amount","operatorType":"EQUALS","value":9.99999999999999e292}`)
		if len(hits) != 1 {
			t.Errorf("the ceiling value must be findable; got %d hits", len(hits))
		}
	})

	t.Run("above the ceiling is still gated", func(t *testing.T) {
		const model = "e2e-typeadm-double-above-ceiling"
		importModelSampleE2E(t, model, 1, `{"amount":10.5}`)
		lockModelE2E(t, model, 1)
		setChangeLevelE2E(t, model, 1, "ARRAY_LENGTH")

		status, body := createEntityRawE2E(t, model, 1, `{"amount":9.99999999999999e300}`)
		if status != http.StatusBadRequest {
			t.Fatalf("above the ceiling is a genuine type change; status = %d, want 400; body: %s", status, body)
		}
	})

	t.Run("16 significant digits is still gated (precision)", func(t *testing.T) {
		const model = "e2e-typeadm-double-precision"
		importModelSampleE2E(t, model, 1, `{"amount":10.5}`)
		lockModelE2E(t, model, 1)
		setChangeLevelE2E(t, model, 1, "ARRAY_LENGTH")

		status, body := createEntityRawE2E(t, model, 1, `{"amount":1.234567890123456}`)
		if status != http.StatusBadRequest {
			t.Fatalf("16 significant digits exceeds DOUBLE's mantissa; status = %d, want 400; body: %s", status, body)
		}
	})

	t.Run("scale past 292 is still gated", func(t *testing.T) {
		const model = "e2e-typeadm-double-scale"
		importModelSampleE2E(t, model, 1, `{"amount":10.5}`)
		lockModelE2E(t, model, 1)
		setChangeLevelE2E(t, model, 1, "ARRAY_LENGTH")

		status, body := createEntityRawE2E(t, model, 1, `{"amount":1e-400}`)
		if status != http.StatusBadRequest {
			t.Fatalf("scale past 292 is a genuine type change; status = %d, want 400; body: %s", status, body)
		}
	})
}

// --- §10 row: 1.5 into UNBOUND_INTEGER still gated (not whole). 2^127
// (170141183460469231731687303715884105728) is one past BIG_INTEGER's
// Int128 ceiling, so registration classifies the leaf UNBOUND_INTEGER. ---

func TestTypeAdmission_UnboundIntegerRefusesFraction(t *testing.T) {
	const model = "e2e-typeadm-unbound-integer"
	importModelSampleE2E(t, model, 1, `{"amount":170141183460469231731687303715884105728}`)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "ARRAY_LENGTH")

	status, body := createEntityRawE2E(t, model, 1, `{"amount":1.5}`)
	if status != http.StatusBadRequest {
		t.Fatalf("1.5 is not whole; UNBOUND_INTEGER never admits it; status = %d, want 400; body: %s", status, body)
	}
}

// --- §10 row: high-scale value into BIG_DECIMAL: held, then found.
// BIG_DECIMAL admission is magnitude-only, so a value differing only in
// fractional digits from the registered sample is held. ---

func TestTypeAdmission_BigDecimalHighScale_HeldThenFound(t *testing.T) {
	const model = "e2e-typeadm-bigdecimal"
	setupModelSampleWithWorkflow(t, model, `{"amount":1.234567890123456789}`, noProcessorTypeAdmissionWorkflow("typeadm-bigdec-wf"))

	before := exportModelE2E(t, model, 1)
	beforeJSON, _ := json.Marshal(before)
	if !strings.Contains(string(beforeJSON), "BIG_DECIMAL") {
		t.Fatalf("sample must register amount as BIG_DECIMAL: %s", beforeJSON)
	}

	createEntityE2E(t, model, 1, `{"amount":1.23456789012345678901234567890}`)
	after := exportModelE2E(t, model, 1)
	assertModelUnchanged(t, before, after)

	_, hits := directSearch(t, model, 1, `{"type":"simple","jsonPath":"$.amount","operatorType":"EQUALS","value":1.23456789012345678901234567890}`)
	if len(hits) != 1 {
		t.Errorf("a high-scale value held by BIG_DECIMAL must be findable; got %d hits", len(hits))
	}
}

// --- §10 rows: registration still yields {STRING, LOCAL_DATE}, and a
// "2026" write into that leaf stays {STRING, LOCAL_DATE} — the value is held
// by the STRING branch and does not grow a YEAR branch the way registration
// would. §8 keeps registration unchanged; this design changes only
// ingestion. ---

func TestTypeAdmission_StringLocalDateUnion_RegistrationAndStability(t *testing.T) {
	const model = "e2e-typeadm-string-localdate"
	importModelSampleE2E(t, model, 1, `{"note":"hello"}`)
	importModelSampleE2E(t, model, 1, `{"note":"2026-03-01"}`)
	lockModelE2E(t, model, 1)

	root := rootBucketE2E(t, model, 1)
	noteType, _ := root[".note"].(string)
	if !strings.Contains(noteType, "STRING") || !strings.Contains(noteType, "LOCAL_DATE") {
		t.Fatalf("registration must yield {STRING, LOCAL_DATE}; got %q", noteType)
	}

	before := exportModelE2E(t, model, 1)
	status, body := createEntityRawE2E(t, model, 1, `{"note":"2026"}`)
	if status != http.StatusOK {
		t.Fatalf(`"2026" is held by the STRING branch; status = %d, want 200; body: %s`, status, body)
	}
	after := exportModelE2E(t, model, 1)
	assertModelUnchanged(t, before, after)
}

// --- §10 row: mixed-kind array [2147483648, "hello"] into a [DOUBLE]
// element. Each element is judged individually: 2147483648 is held by
// DOUBLE and contributes no label; "hello" needs only ARRAY_ELEMENTS to add
// STRING. Before this design the array was fused into one description
// before judging, so 2147483648 would have contributed a LONG/large-integer
// label alongside "hello"'s STRING — this pins that it no longer does. ---

func TestTypeAdmission_MixedKindArray_ElementsJudgedIndividually(t *testing.T) {
	const model = "e2e-typeadm-mixed-array"
	importModelSampleE2E(t, model, 1, `{"amounts":[10.5]}`)
	lockModelE2E(t, model, 1)
	setChangeLevelE2E(t, model, 1, "ARRAY_ELEMENTS")

	status, body := createEntityRawE2E(t, model, 1, `{"amounts":[2147483648,"hello"]}`)
	if status != http.StatusOK {
		t.Fatalf(`2147483648 is held by DOUBLE and "hello" needs only ARRAY_ELEMENTS to add STRING; status = %d, want 200; body: %s`, status, body)
	}

	root := rootBucketE2E(t, model, 1)
	elemType, _ := root[".amounts[*]"].(string)
	if !strings.Contains(elemType, "DOUBLE") || !strings.Contains(elemType, "STRING") {
		t.Errorf(".amounts[*] must declare both DOUBLE and STRING; got %q", elemType)
	}
	if strings.Contains(elemType, "LONG") || strings.Contains(elemType, "UNBOUND_DECIMAL") {
		t.Errorf(".amounts[*] must not show a fused numeric label — that would mean 2147483648 was judged by its whole-array fusion rather than on its own; got %q", elemType)
	}
}

// --- §10 row: strict validation is never more permissive than
// ARRAY_LENGTH. An array-width-growing write needs ARRAY_LENGTH permission;
// strict (no changeLevel) is a superset of every gate a level imposes, so it
// must refuse what ARRAY_LENGTH would accept. ---

func TestTypeAdmission_StrictNeverMorePermissiveThanArrayLength(t *testing.T) {
	const sample = `{"items":[1,2]}`
	const widerPayload = `{"items":[1,2,3]}`

	const strictModel = "e2e-typeadm-strict-vs-arraylength-strict"
	importModelSampleE2E(t, strictModel, 1, sample)
	lockModelE2E(t, strictModel, 1)
	// No changeLevel: strict.
	status, body := createEntityRawE2E(t, strictModel, 1, widerPayload)
	if status != http.StatusBadRequest {
		t.Fatalf("array width growth needs ARRAY_LENGTH; strict must refuse it; status = %d, want 400; body: %s", status, body)
	}

	const arrayLengthModel = "e2e-typeadm-strict-vs-arraylength-al"
	importModelSampleE2E(t, arrayLengthModel, 1, sample)
	lockModelE2E(t, arrayLengthModel, 1)
	setChangeLevelE2E(t, arrayLengthModel, 1, "ARRAY_LENGTH")
	status, body = createEntityRawE2E(t, arrayLengthModel, 1, widerPayload)
	if status != http.StatusOK {
		t.Fatalf("ARRAY_LENGTH must permit what strict refuses; status = %d, want 200; body: %s", status, body)
	}
}
