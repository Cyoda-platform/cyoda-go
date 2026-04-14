package predicate

import (
	"testing"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

func meta() common.EntityMeta {
	return common.EntityMeta{
		State:                   "CREATED",
		CreationDate:            time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		TransitionForLatestSave: "workflow.step1",
	}
}

var sampleData = []byte(`{
	"name": "Alice",
	"age": 30,
	"score": 85.5,
	"active": true,
	"city": null,
	"tags": ["go", "rust", "python"],
	"address": {"street": "Main St", "zip": "12345"},
	"laureates": [
		{"name": "Bob", "motivation": "for peace"},
		{"name": "Carol", "motivation": "for chemistry"}
	]
}`)

// --- 1. Simple EQUALS ---

func TestMatchSimpleEqualsString(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleEqualsStringFalse(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Bob"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

func TestMatchSimpleEqualsNumber(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "EQUALS", Value: float64(30)}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleEqualsNumberAsString(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "EQUALS", Value: "30"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for numeric string comparison")
	}
}

func TestMatchSimpleEqualsBool(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.active", OperatorType: "EQUALS", Value: "true"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

// --- 2. Simple NOT_EQUAL ---

func TestMatchSimpleNotEqual(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "NOT_EQUAL", Value: "Bob"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleNotEqualFalse(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "NOT_EQUAL", Value: "Alice"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

// --- 3. IS_NULL / NOT_NULL ---

func TestMatchSimpleIsNull(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.city", OperatorType: "IS_NULL"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for null field")
	}
}

func TestMatchSimpleIsNullMissing(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.nonexistent", OperatorType: "IS_NULL"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for missing field")
	}
}

func TestMatchSimpleNotNull(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "NOT_NULL"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleNotNullOnNull(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.city", OperatorType: "NOT_NULL"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false for null field")
	}
}

// --- 4. GREATER_THAN / LESS_THAN ---

func TestMatchSimpleGreaterThan(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "GREATER_THAN", Value: float64(25)}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleGreaterThanFalse(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "GREATER_THAN", Value: float64(30)}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

func TestMatchSimpleLessThan(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "LESS_THAN", Value: float64(35)}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleLessThanFalse(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "LESS_THAN", Value: float64(30)}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

// --- 5. GREATER_OR_EQUAL / LESS_OR_EQUAL ---

func TestMatchSimpleGreaterOrEqual(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "GREATER_OR_EQUAL", Value: float64(30)}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for equal value")
	}
}

func TestMatchSimpleLessOrEqual(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "LESS_OR_EQUAL", Value: float64(30)}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for equal value")
	}
}

// --- 6. CONTAINS / NOT_CONTAINS ---

func TestMatchSimpleContains(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "CONTAINS", Value: "lic"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleNotContains(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "NOT_CONTAINS", Value: "xyz"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

// --- 7. STARTS_WITH / ENDS_WITH ---

func TestMatchSimpleStartsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "STARTS_WITH", Value: "Ali"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleNotStartsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "NOT_STARTS_WITH", Value: "Bob"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleEndsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "ENDS_WITH", Value: "ice"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleNotEndsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "NOT_ENDS_WITH", Value: "xyz"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

// --- 8. IEQUALS / ICONTAINS ---

func TestMatchSimpleIEquals(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "IEQUALS", Value: "alice"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for case-insensitive equals")
	}
}

func TestMatchSimpleINotEqual(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "INOT_EQUAL", Value: "alice"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false for case-insensitive not-equal on match")
	}
}

func TestMatchSimpleIContains(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "ICONTAINS", Value: "LIC"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for case-insensitive contains")
	}
}

func TestMatchSimpleINotContains(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "INOT_CONTAINS", Value: "XYZ"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleIStartsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "ISTARTS_WITH", Value: "ALI"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleINotStartsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "INOT_STARTS_WITH", Value: "BOB"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleIEndsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "IENDS_WITH", Value: "ICE"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleINotEndsWith(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "INOT_ENDS_WITH", Value: "XYZ"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

// --- 9. MATCHES_PATTERN ---

func TestMatchSimpleMatchesPattern(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "MATCHES_PATTERN", Value: "^A.*e$"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleMatchesPatternFalse(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "MATCHES_PATTERN", Value: "^B.*"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

// --- 10. LIKE ---

func TestMatchSimpleLike(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "LIKE", Value: "A%"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for LIKE A%")
	}
}

func TestMatchSimpleLikeUnderscore(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "LIKE", Value: "Alic_"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for LIKE Alic_")
	}
}

func TestMatchSimpleLikeFalse(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "LIKE", Value: "B%"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

// --- 11. BETWEEN ---

func TestMatchSimpleBetweenString(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "BETWEEN", Value: "25,35"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for 30 between 25 and 35")
	}
}

func TestMatchSimpleBetweenSlice(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "BETWEEN", Value: []any{float64(25), float64(35)}}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchSimpleBetweenInclusiveEdge(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.age", OperatorType: "BETWEEN_INCLUSIVE", Value: "30,30"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for edge-inclusive")
	}
}

// --- 11. Lifecycle condition ---

func TestMatchLifecycleStateMatch(t *testing.T) {
	cond := &LifecycleCondition{Field: "state", OperatorType: "EQUALS", Value: "CREATED"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchLifecycleStateNoMatch(t *testing.T) {
	cond := &LifecycleCondition{Field: "state", OperatorType: "EQUALS", Value: "DELETED"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

func TestMatchLifecycleCreationDate(t *testing.T) {
	cond := &LifecycleCondition{Field: "creationDate", OperatorType: "CONTAINS", Value: "2026-01-15"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for creation date contains")
	}
}

func TestMatchLifecycleTransition(t *testing.T) {
	cond := &LifecycleCondition{Field: "transitionForLatestSave", OperatorType: "EQUALS", Value: "workflow.step1"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchLifecyclePreviousTransition(t *testing.T) {
	cond := &LifecycleCondition{Field: "previousTransition", OperatorType: "EQUALS", Value: "workflow.step1"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true for previousTransition alias")
	}
}

// --- 12. Group AND ---

func TestMatchGroupAndAllMatch(t *testing.T) {
	cond := &GroupCondition{
		Operator: "AND",
		Conditions: []Condition{
			&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"},
			&SimpleCondition{JsonPath: "$.age", OperatorType: "EQUALS", Value: float64(30)},
		},
	}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchGroupAndOneFails(t *testing.T) {
	cond := &GroupCondition{
		Operator: "AND",
		Conditions: []Condition{
			&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"},
			&SimpleCondition{JsonPath: "$.age", OperatorType: "EQUALS", Value: float64(99)},
		},
	}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

// --- 13. Group OR ---

func TestMatchGroupOrOneMatches(t *testing.T) {
	cond := &GroupCondition{
		Operator: "OR",
		Conditions: []Condition{
			&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Bob"},
			&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"},
		},
	}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestMatchGroupOrNoneMatch(t *testing.T) {
	cond := &GroupCondition{
		Operator: "OR",
		Conditions: []Condition{
			&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Bob"},
			&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Carol"},
		},
	}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false")
	}
}

// --- 14. Nested group ---

func TestMatchNestedGroupAndContainingOr(t *testing.T) {
	cond := &GroupCondition{
		Operator: "AND",
		Conditions: []Condition{
			&SimpleCondition{JsonPath: "$.active", OperatorType: "EQUALS", Value: "true"},
			&GroupCondition{
				Operator: "OR",
				Conditions: []Condition{
					&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Bob"},
					&SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"},
				},
			},
		},
	}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}

// --- 15. Array condition ---

func TestMatchArrayCondition(t *testing.T) {
	cond := &ArrayCondition{
		JsonPath: "$.tags",
		Values:   []any{"go", nil, "python"},
	}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true: index 0=go, index 1=skip, index 2=python")
	}
}

func TestMatchArrayConditionMismatch(t *testing.T) {
	cond := &ArrayCondition{
		JsonPath: "$.tags",
		Values:   []any{"go", nil, "java"},
	}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false: index 2 is python not java")
	}
}

// --- 16. Function condition → error ---

func TestMatchFunctionConditionError(t *testing.T) {
	cond := &FunctionCondition{}
	_, err := Match(cond, sampleData, meta())
	if err == nil {
		t.Error("expected error for function condition")
	}
}

// --- 17. IS_CHANGED → error ---

func TestMatchIsChangedError(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "IS_CHANGED"}
	_, err := Match(cond, sampleData, meta())
	if err == nil {
		t.Error("expected error for IS_CHANGED")
	}
}

func TestMatchIsUnchangedError(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.name", OperatorType: "IS_UNCHANGED"}
	_, err := Match(cond, sampleData, meta())
	if err == nil {
		t.Error("expected error for IS_UNCHANGED")
	}
}

// --- 18. No match: field doesn't exist ---

func TestMatchMissingFieldReturnsFalse(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.nonexistent", OperatorType: "EQUALS", Value: "anything"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false for missing field")
	}
}

// --- Array wildcard with CONTAINS ---

func TestMatchArrayWildcardContains(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.laureates[*].motivation", OperatorType: "CONTAINS", Value: "peace"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true: one laureate motivation contains 'peace'")
	}
}

func TestMatchArrayWildcardContainsNoMatch(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.laureates[*].motivation", OperatorType: "CONTAINS", Value: "physics"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("expected false: no laureate motivation contains 'physics'")
	}
}

// --- Nested field access ---

func TestMatchNestedField(t *testing.T) {
	cond := &SimpleCondition{JsonPath: "$.address.street", OperatorType: "EQUALS", Value: "Main St"}
	got, err := Match(cond, sampleData, meta())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("expected true")
	}
}
