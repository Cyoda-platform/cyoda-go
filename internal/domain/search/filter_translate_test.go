package search

import (
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go-spi/predicate"
)

func TestConditionToFilter_SimpleEquals(t *testing.T) {
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.name",
		OperatorType: "EQUALS",
		Value:        "Alice",
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatalf("ConditionToFilter: %v", err)
	}
	if f.Op != spi.FilterEq {
		t.Errorf("Op = %s, want eq", f.Op)
	}
	if f.Path != "name" {
		t.Errorf("Path = %s, want name", f.Path)
	}
	if f.Source != spi.SourceData {
		t.Errorf("Source = %s, want data", f.Source)
	}
	if f.Value != "Alice" {
		t.Errorf("Value = %v, want Alice", f.Value)
	}
}

func TestConditionToFilter_SimpleNoPrefix(t *testing.T) {
	cond := &predicate.SimpleCondition{
		JsonPath:     "city",
		OperatorType: "EQUALS",
		Value:        "Berlin",
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "city" {
		t.Errorf("Path = %s, want city", f.Path)
	}
}

func TestConditionToFilter_SimpleNestedPath(t *testing.T) {
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.address.city",
		OperatorType: "NOT_EQUAL",
		Value:        "Berlin",
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	if f.Op != spi.FilterNe {
		t.Errorf("Op = %s, want ne", f.Op)
	}
	if f.Path != "address.city" {
		t.Errorf("Path = %s, want address.city", f.Path)
	}
}

func TestConditionToFilter_AllSimpleOperators(t *testing.T) {
	tests := []struct {
		op   string
		want spi.FilterOp
	}{
		{"EQUALS", spi.FilterEq},
		{"NOT_EQUAL", spi.FilterNe},
		{"GREATER_THAN", spi.FilterGt},
		{"LESS_THAN", spi.FilterLt},
		{"GREATER_OR_EQUAL", spi.FilterGte},
		{"LESS_OR_EQUAL", spi.FilterLte},
		{"CONTAINS", spi.FilterContains},
		{"STARTS_WITH", spi.FilterStartsWith},
		{"ENDS_WITH", spi.FilterEndsWith},
		{"LIKE", spi.FilterLike},
		{"IS_NULL", spi.FilterIsNull},
		{"NOT_NULL", spi.FilterNotNull},
		{"BETWEEN", spi.FilterBetween},
		{"MATCHES_PATTERN", spi.FilterMatchesRegex},
		{"IEQUALS", spi.FilterIEq},
		{"ICONTAINS", spi.FilterIContains},
		{"ISTARTS_WITH", spi.FilterIStartsWith},
		{"IENDS_WITH", spi.FilterIEndsWith},
	}

	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			cond := &predicate.SimpleCondition{
				JsonPath:     "$.field",
				OperatorType: tt.op,
				Value:        "val",
			}
			f, err := ConditionToFilter(cond)
			if err != nil {
				t.Fatal(err)
			}
			if f.Op != tt.want {
				t.Errorf("Op = %s, want %s", f.Op, tt.want)
			}
		})
	}
}

func TestConditionToFilter_UnknownOperator(t *testing.T) {
	cond := &predicate.SimpleCondition{
		JsonPath:     "$.field",
		OperatorType: "SOME_UNKNOWN_OP",
		Value:        "val",
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	// Unknown operators map to matches_regex to force post-filtering.
	if f.Op != spi.FilterMatchesRegex {
		t.Errorf("Op = %s, want matches_regex for unknown op", f.Op)
	}
}

func TestConditionToFilter_Lifecycle(t *testing.T) {
	cond := &predicate.LifecycleCondition{
		Field:        "state",
		OperatorType: "EQUALS",
		Value:        "ACTIVE",
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	if f.Op != spi.FilterEq {
		t.Errorf("Op = %s, want eq", f.Op)
	}
	if f.Source != spi.SourceMeta {
		t.Errorf("Source = %s, want meta", f.Source)
	}
	if f.Path != "state" {
		t.Errorf("Path = %s, want state", f.Path)
	}
	if f.Value != "ACTIVE" {
		t.Errorf("Value = %v, want ACTIVE", f.Value)
	}
}

func TestConditionToFilter_GroupAND(t *testing.T) {
	cond := &predicate.GroupCondition{
		Operator: "AND",
		Conditions: []predicate.Condition{
			&predicate.SimpleCondition{JsonPath: "$.name", OperatorType: "EQUALS", Value: "Alice"},
			&predicate.SimpleCondition{JsonPath: "$.age", OperatorType: "GREATER_THAN", Value: float64(25)},
		},
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	if f.Op != spi.FilterAnd {
		t.Errorf("Op = %s, want and", f.Op)
	}
	if len(f.Children) != 2 {
		t.Fatalf("Children count = %d, want 2", len(f.Children))
	}
	if f.Children[0].Op != spi.FilterEq {
		t.Errorf("Children[0].Op = %s, want eq", f.Children[0].Op)
	}
	if f.Children[1].Op != spi.FilterGt {
		t.Errorf("Children[1].Op = %s, want gt", f.Children[1].Op)
	}
}

func TestConditionToFilter_GroupOR(t *testing.T) {
	cond := &predicate.GroupCondition{
		Operator: "OR",
		Conditions: []predicate.Condition{
			&predicate.SimpleCondition{JsonPath: "$.city", OperatorType: "EQUALS", Value: "Berlin"},
			&predicate.SimpleCondition{JsonPath: "$.city", OperatorType: "EQUALS", Value: "Munich"},
		},
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	if f.Op != spi.FilterOr {
		t.Errorf("Op = %s, want or", f.Op)
	}
	if len(f.Children) != 2 {
		t.Fatalf("Children count = %d, want 2", len(f.Children))
	}
}

func TestConditionToFilter_NestedGroup(t *testing.T) {
	cond := &predicate.GroupCondition{
		Operator: "AND",
		Conditions: []predicate.Condition{
			&predicate.SimpleCondition{JsonPath: "$.active", OperatorType: "EQUALS", Value: true},
			&predicate.GroupCondition{
				Operator: "OR",
				Conditions: []predicate.Condition{
					&predicate.SimpleCondition{JsonPath: "$.city", OperatorType: "EQUALS", Value: "Berlin"},
					&predicate.SimpleCondition{JsonPath: "$.city", OperatorType: "EQUALS", Value: "Munich"},
				},
			},
		},
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	if f.Op != spi.FilterAnd {
		t.Errorf("Op = %s, want and", f.Op)
	}
	if len(f.Children) != 2 {
		t.Fatalf("Children count = %d, want 2", len(f.Children))
	}
	if f.Children[1].Op != spi.FilterOr {
		t.Errorf("Children[1].Op = %s, want or", f.Children[1].Op)
	}
}

func TestConditionToFilter_Array(t *testing.T) {
	cond := &predicate.ArrayCondition{
		JsonPath: "$.tags",
		Values:   []any{"go", nil, "test"},
	}
	f, err := ConditionToFilter(cond)
	if err != nil {
		t.Fatal(err)
	}
	// Array conditions force post-filtering via matches_regex.
	if f.Op != spi.FilterMatchesRegex {
		t.Errorf("Op = %s, want matches_regex for array condition", f.Op)
	}
	if f.Path != "tags" {
		t.Errorf("Path = %s, want tags", f.Path)
	}
}

func TestConditionToFilter_Function(t *testing.T) {
	cond := &predicate.FunctionCondition{}
	_, err := ConditionToFilter(cond)
	if err == nil {
		t.Fatal("expected error for FunctionCondition, got nil")
	}
}

func TestConditionToFilter_Nil(t *testing.T) {
	_, err := ConditionToFilter(nil)
	if err == nil {
		t.Fatal("expected error for nil condition, got nil")
	}
}
