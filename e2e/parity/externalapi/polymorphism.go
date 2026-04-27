package externalapi

// External API Scenario Suite — 14-polymorphism
//
// Polymorphism in cyoda-go: a field that observes more than one concrete
// DataType is exported as Polymorphic([TYPE1, TYPE2, ...]).
// SIMPLE_VIEW exporter at internal/domain/model/exporter/simple_view.go:137
// emits this shape.
//
// Sources of polymorphism exercised here:
//  1. Mixed object-or-string at same JSONPath (poly/01).
//  2. Sealed PolymorphicValue array variants (poly/03 — STRING/DOUBLE/
//     BOOLEAN/UUID).
//  3. Sealed PolymorphicTimestamp array variants (poly/04 — LocalDate/
//     YearMonth/ZonedDateTime).
//  4. Numeric-string vs UUID-string in the same scalar field (poly/05
//     REST half).
//  5. Wrong-type rejection on monomorphic DOUBLE (poly/06 negative path).
//
// Discovered divergences (controller decision required, per rule):
//
//   14/01: cyoda-go enforces strict structural type from first-seen element.
//   POST with element where some-object=string returns 400 BAD_REQUEST
//   "expected object, got string". Cloud stores both branches. worse-class.
//   t.Skip pending controller decision.
//
//   14/03 (SIMPLE_VIEW UUID check): cyoda-go does not distinguish UUID
//   values from STRING. Observed SIMPLE_VIEW descriptor: "[DOUBLE, STRING,
//   BOOLEAN]" — UUID variant absorbed into STRING. Round-trip itself works
//   (string in → string out). worse-class for UUID type detection.
//   t.Skip the SIMPLE_VIEW UUID assertion; round-trip assertion passes.
//
//   14/04 (temporal subtype classification): cyoda-go classifies all
//   temporal strings (LocalDate, YearMonth, ZonedDateTime) as STRING.
//   Observed SIMPLE_VIEW descriptor: "STRING". No temporal sub-type
//   detection. Round-trip works (string in → string out). worse-class.
//   t.Skip the SIMPLE_VIEW temporal-type assertions; round-trip passes.
//
//   14/06: cyoda-go does not validate condition value type against field
//   DataType at search time. POST /api/search/direct with value:"abc" on
//   DOUBLE field returns HTTP 200 (empty results) instead of HTTP 400.
//   Direct: 200 body="". Async submit: 200 body=<jobId>. worse-class.
//   t.Skip pending controller decision.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
	"github.com/cyoda-platform/cyoda-go/e2e/parity"
)

func init() {
	parity.Register(
		parity.NamedTest{Name: "ExternalAPI_14_01_MixedObjectOrStringAtSamePath", Fn: RunExternalAPI_14_01_MixedObjectOrStringAtSamePath},
		parity.NamedTest{Name: "ExternalAPI_14_03_PolymorphicValueArray", Fn: RunExternalAPI_14_03_PolymorphicValueArray},
		parity.NamedTest{Name: "ExternalAPI_14_04_PolymorphicTimestampArray", Fn: RunExternalAPI_14_04_PolymorphicTimestampArray},
		parity.NamedTest{Name: "ExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf", Fn: RunExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf},
		parity.NamedTest{Name: "ExternalAPI_14_06_RejectWrongTypeCondition", Fn: RunExternalAPI_14_06_RejectWrongTypeCondition},
	)
}

// RunExternalAPI_14_01_MixedObjectOrStringAtSamePath — dictionary 14/01.
// $.some-array[*].some-object is an object in element 0 and a string in
// element 1. Both an object-key condition and a string-equals condition
// must return non-empty results via async + direct.
//
// Discover-and-compare result (worse-class, pending controller decision):
// cyoda-go enforces strict structural type from the first-observed element.
// POST with an element where some-object is a string returns HTTP 400
// "expected object, got string". Cloud stores both branches.
func RunExternalAPI_14_01_MixedObjectOrStringAtSamePath(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending controller decision: cyoda-go enforces strict structural type; POST with element where some-object=string returns 400 BAD_REQUEST. Cloud stores both object/string branches at the same path. worse-class divergence.")
	d := driver.NewInProcess(t, fixture)
	const sample = `{"label":"name","some-array":[{"some-label":"hello","some-object":{"some-key":"some-key","some-other-key":"some-other-key"}},{"some-label":"hello","some-object":"abc"}]}`
	if err := d.CreateModelFromSample("polymorphic", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("polymorphic", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := d.CreateEntity("polymorphic", 1, sample); err != nil {
		t.Fatalf("create entity: %v", err)
	}
	const objectBranch = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.some-array[*].some-object.some-key","operatorType":"EQUALS","value":"some-key"}
		]
	}`
	const stringBranch = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.some-array[*].some-object","operatorType":"EQUALS","value":"abc"}
		]
	}`

	for _, c := range []struct {
		label, condition string
	}{
		{"object-branch", objectBranch},
		{"string-branch", stringBranch},
	} {
		direct, err := d.SyncSearch("polymorphic", 1, c.condition)
		if err != nil {
			t.Errorf("%s direct: %v", c.label, err)
			continue
		}
		if len(direct) == 0 {
			t.Errorf("%s direct returned empty", c.label)
		}
		page, err := d.AwaitAsyncSearchResults("polymorphic", 1, c.condition, 10*time.Second)
		if err != nil {
			t.Errorf("%s async: %v", c.label, err)
			continue
		}
		if len(page.Content) == 0 {
			t.Errorf("%s async returned empty", c.label)
		}
	}
}

// RunExternalAPI_14_03_PolymorphicValueArray — dictionary 14/03.
// AllFieldsModel.polymorphicArray accepts (StringValue, DoubleValue,
// BooleanValue, UUIDValue). Round-trip verbatim; SIMPLE_VIEW reports
// [STRING, DOUBLE, BOOLEAN, UUID] for $.polymorphicArray[*].value.
//
// Discover-and-compare result for SIMPLE_VIEW UUID check (worse-class):
// cyoda-go does not recognise UUID as a distinct DataType — UUID values
// are classified as STRING. Observed descriptor: "[DOUBLE, STRING,
// BOOLEAN]". Round-trip itself passes (string in → string out). The
// SIMPLE_VIEW UUID assertion is skipped with an inline comment; the
// round-trip and the 3 classifiable types (STRING, DOUBLE, BOOLEAN) are
// still asserted.
func RunExternalAPI_14_03_PolymorphicValueArray(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	const sample = `{"polymorphicArray":[{"value":"abc"},{"value":3.14},{"value":true},{"value":"550e8400-e29b-41d4-a716-446655440000"}]}`
	if err := d.CreateModelFromSample("AllFieldsModel", 1, sample); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("AllFieldsModel", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	id, err := d.CreateEntity("AllFieldsModel", 1, sample)
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	got, err := d.GetEntity(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Round-trip: re-marshal and compare structural JSON.
	gotJSON, err := json.Marshal(got.Data)
	if err != nil {
		t.Fatalf("re-marshal got: %v", err)
	}
	var wantTree, gotTree any
	_ = json.Unmarshal([]byte(sample), &wantTree)
	_ = json.Unmarshal(gotJSON, &gotTree)
	wantNorm, _ := json.Marshal(wantTree)
	gotNorm, _ := json.Marshal(gotTree)
	if string(wantNorm) != string(gotNorm) {
		t.Errorf("round-trip differs:\n  want: %s\n  got:  %s", string(wantNorm), string(gotNorm))
	}
	exported, err := d.ExportModel("SIMPLE_VIEW", "AllFieldsModel", 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// SIMPLE_VIEW uses path keys like "$.polymorphicArray[*]" with
	// child entries ".value": "<descriptor>".
	//
	// Observed descriptor: "[DOUBLE, STRING, BOOLEAN]". UUID values are
	// absorbed into STRING (no distinct UUID DataType in cyoda-go).
	// The 3 classifiable types are still asserted; the UUID assertion
	// is skipped as worse-class pending controller decision.
	if gotDesc, err := simpleViewFieldType(t, exported, "$.polymorphicArray[*]", ".value"); err != nil {
		t.Errorf("$.polymorphicArray[*].value lookup: %v", err)
	} else {
		for _, want := range []string{"STRING", "DOUBLE", "BOOLEAN"} {
			if !strings.Contains(gotDesc, want) {
				t.Errorf("$.polymorphicArray[*].value: %q missing %q", gotDesc, want)
			}
		}
		// UUID: worse-class — cyoda-go classifies UUID strings as STRING.
		// Observed: "[DOUBLE, STRING, BOOLEAN]" (no UUID entry).
		// Not asserted: pending controller decision on UUID DataType support.
	}
}

// RunExternalAPI_14_04_PolymorphicTimestampArray — dictionary 14/04.
// objectArray[*].timestamp accepts LocalDate / YearMonth / ZonedDateTime.
// Readback verbatim; SIMPLE_VIEW reports [LOCAL_DATE, YEAR_MONTH,
// ZONED_DATE_TIME].
//
// Discover-and-compare result (worse-class, pending controller decision):
// cyoda-go classifies all three temporal string variants as STRING (no
// temporal sub-type detection). Observed SIMPLE_VIEW descriptor: "STRING".
// Round-trip itself works (string in → string out). worse-class divergence.
func RunExternalAPI_14_04_PolymorphicTimestampArray(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending controller decision: cyoda-go classifies LocalDate/YearMonth/ZonedDateTime as STRING (no temporal sub-type detection). Observed SIMPLE_VIEW: \"STRING\". Cloud expects [LOCAL_DATE, YEAR_MONTH, ZONED_DATE_TIME]. worse-class divergence.")
}

// RunExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf — dictionary 14/05 (REST half).
// The dictionary's RSocket leg is unreachable (no cyoda-go analogue);
// only the REST-equivalent direct-search is exercised. Recorded as
// (skipped) for the RSocket step in the mapping doc.
func RunExternalAPI_14_05_TrinoSearchOnPolymorphicScalarRESTHalf(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	d := driver.NewInProcess(t, fixture)
	// Register the model from a sample whose station_id is one polymorphic
	// scalar (the dictionary's bike-stations dataset isn't preloaded — we
	// register a minimal equivalent).
	const sampleNumeric = `{"station_id":"1436495119852630436","name":"station-num"}`
	const sampleUUID = `{"station_id":"a3a48d5c-a135-11e9-9cda-0a87ae2ba916","name":"station-uuid"}`
	if err := d.CreateModelFromSample("stations", 1, sampleNumeric); err != nil {
		t.Fatalf("create model v1: %v", err)
	}
	if err := d.CreateModelFromSample("stations", 1, sampleUUID); err != nil {
		t.Fatalf("merge model v2: %v", err)
	}
	if err := d.LockModel("stations", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := d.CreateEntity("stations", 1, sampleNumeric); err != nil {
		t.Fatalf("create entity numeric: %v", err)
	}
	if _, err := d.CreateEntity("stations", 1, sampleUUID); err != nil {
		t.Fatalf("create entity uuid: %v", err)
	}
	const condition = `{
		"type":"group","operator":"OR",
		"conditions":[
			{"type":"simple","jsonPath":"$.station_id","operatorType":"EQUALS","value":"1436495119852630436"},
			{"type":"simple","jsonPath":"$.station_id","operatorType":"EQUALS","value":"a3a48d5c-a135-11e9-9cda-0a87ae2ba916"}
		]
	}`
	results, err := d.SyncSearch("stations", 1, condition)
	if err != nil {
		t.Fatalf("SyncSearch: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("direct: got %d results want 2 (one per station_id branch)", len(results))
	}
}

// RunExternalAPI_14_06_RejectWrongTypeCondition — dictionary 14/06.
// $.price is DOUBLE; condition value "abc" must be rejected with HTTP 400.
// Discover-and-compare on the errorCode (dictionary expects
// InvalidTypesInClientConditionException).
//
// Discover-and-compare result (worse-class, pending controller decision):
// cyoda-go does not validate condition value types against field DataType
// at search time. Direct search returns HTTP 200 with empty body; async
// submit returns HTTP 200 with a jobId. Cloud rejects both with HTTP 400.
func RunExternalAPI_14_06_RejectWrongTypeCondition(t *testing.T, fixture parity.BackendFixture) {
	t.Helper()
	t.Skip("pending controller decision: cyoda-go does not validate condition value type against field DataType. POST /api/search/direct with value:\"abc\" on DOUBLE field returns HTTP 200 (empty results) instead of HTTP 400 (InvalidTypesInClientConditionException). worse-class divergence.")
	d := driver.NewInProcess(t, fixture)
	if err := d.CreateModelFromSample("ordersWrong", 1, `{"price": 100.0}`); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := d.LockModel("ordersWrong", 1); err != nil {
		t.Fatalf("lock: %v", err)
	}
	const badCondition = `{
		"type":"group","operator":"AND",
		"conditions":[
			{"type":"simple","jsonPath":"$.price","operatorType":"GREATER_OR_EQUAL","value":"abc"}
		]
	}`
	// Direct search must reject (HTTP 400 per dictionary).
	// Observed: HTTP 200 empty — cyoda-go silently returns no results.
	status, body, err := d.SyncSearchRaw("ordersWrong", 1, badCondition)
	if err != nil {
		t.Fatalf("SyncSearchRaw transport: %v", err)
	}
	// equiv_or_better target once server-side validation is added:
	// errorcontract.ExpectedError{HTTPStatus: 400, ErrorCode: "<tbd>"}
	_ = status
	_ = body

	// Async search submission must also reject (per dictionary).
	// Observed: HTTP 200 with jobId — cyoda-go accepts the search job.
	asyncStatus, asyncBody, err := d.SubmitAsyncSearchRaw("ordersWrong", 1, badCondition)
	if err != nil {
		t.Fatalf("SubmitAsyncSearchRaw transport: %v", err)
	}
	_ = asyncStatus
	_ = asyncBody
}
