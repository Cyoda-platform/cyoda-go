package predicate_test

import (
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/domain/search/predicate"
)

func TestParseSimpleCondition(t *testing.T) {
	body := []byte(`{"type": "simple", "jsonPath": "$.name", "operatorType": "EQUALS", "value": "Alice"}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sc, ok := cond.(*predicate.SimpleCondition)
	if !ok {
		t.Fatalf("expected *SimpleCondition, got %T", cond)
	}
	if sc.JsonPath != "$.name" {
		t.Errorf("expected jsonPath $.name, got %s", sc.JsonPath)
	}
	if sc.OperatorType != "EQUALS" {
		t.Errorf("expected operatorType EQUALS, got %s", sc.OperatorType)
	}
	if sc.Value != "Alice" {
		t.Errorf("expected value Alice, got %v", sc.Value)
	}
	if sc.Type() != "simple" {
		t.Errorf("expected type simple, got %s", sc.Type())
	}
}

func TestParseLifecycleCondition(t *testing.T) {
	body := []byte(`{"type": "lifecycle", "field": "state", "operatorType": "EQUALS", "value": "CREATED"}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lc, ok := cond.(*predicate.LifecycleCondition)
	if !ok {
		t.Fatalf("expected *LifecycleCondition, got %T", cond)
	}
	if lc.Field != "state" {
		t.Errorf("expected field state, got %s", lc.Field)
	}
	if lc.OperatorType != "EQUALS" {
		t.Errorf("expected operatorType EQUALS, got %s", lc.OperatorType)
	}
	if lc.Value != "CREATED" {
		t.Errorf("expected value CREATED, got %v", lc.Value)
	}
	if lc.Type() != "lifecycle" {
		t.Errorf("expected type lifecycle, got %s", lc.Type())
	}
}

func TestParseGroupWithNestedSimple(t *testing.T) {
	body := []byte(`{
		"type": "group",
		"operator": "AND",
		"conditions": [
			{"type": "simple", "jsonPath": "$.age", "operatorType": "GREATER_THAN", "value": 18},
			{"type": "simple", "jsonPath": "$.active", "operatorType": "EQUALS", "value": true}
		]
	}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gc, ok := cond.(*predicate.GroupCondition)
	if !ok {
		t.Fatalf("expected *GroupCondition, got %T", cond)
	}
	if gc.Operator != "AND" {
		t.Errorf("expected operator AND, got %s", gc.Operator)
	}
	if len(gc.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(gc.Conditions))
	}
	if gc.Type() != "group" {
		t.Errorf("expected type group, got %s", gc.Type())
	}

	c0, ok := gc.Conditions[0].(*predicate.SimpleCondition)
	if !ok {
		t.Fatalf("expected conditions[0] to be *SimpleCondition, got %T", gc.Conditions[0])
	}
	if c0.JsonPath != "$.age" {
		t.Errorf("expected jsonPath $.age, got %s", c0.JsonPath)
	}
	// JSON numbers decode as float64
	if c0.Value != float64(18) {
		t.Errorf("expected value 18, got %v", c0.Value)
	}

	c1, ok := gc.Conditions[1].(*predicate.SimpleCondition)
	if !ok {
		t.Fatalf("expected conditions[1] to be *SimpleCondition, got %T", gc.Conditions[1])
	}
	if c1.Value != true {
		t.Errorf("expected value true, got %v", c1.Value)
	}
}

func TestParseArrayConditionWithNulls(t *testing.T) {
	body := []byte(`{"type": "array", "jsonPath": "$.tags", "values": ["a", "b", null]}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac, ok := cond.(*predicate.ArrayCondition)
	if !ok {
		t.Fatalf("expected *ArrayCondition, got %T", cond)
	}
	if ac.JsonPath != "$.tags" {
		t.Errorf("expected jsonPath $.tags, got %s", ac.JsonPath)
	}
	if len(ac.Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(ac.Values))
	}
	if ac.Values[0] != "a" {
		t.Errorf("expected values[0]=a, got %v", ac.Values[0])
	}
	if ac.Values[1] != "b" {
		t.Errorf("expected values[1]=b, got %v", ac.Values[1])
	}
	if ac.Values[2] != nil {
		t.Errorf("expected values[2]=nil, got %v", ac.Values[2])
	}
	if ac.Type() != "array" {
		t.Errorf("expected type array, got %s", ac.Type())
	}
}

func TestParseFunctionCondition(t *testing.T) {
	body := []byte(`{"type": "function", "name": "myFunc", "args": [1, 2]}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fc, ok := cond.(*predicate.FunctionCondition)
	if !ok {
		t.Fatalf("expected *FunctionCondition, got %T", cond)
	}
	if fc.Type() != "function" {
		t.Errorf("expected type function, got %s", fc.Type())
	}
}

func TestParseInvalidJSON(t *testing.T) {
	body := []byte(`{not valid json`)
	_, err := predicate.ParseCondition(body)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseUnknownType(t *testing.T) {
	body := []byte(`{"type": "unknown_xyz"}`)
	_, err := predicate.ParseCondition(body)
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestParseMaxDepthExceeded(t *testing.T) {
	// Build a deeply nested group condition that exceeds maxParseDepth (50).
	// Each level wraps the previous in a group with one child.
	var b strings.Builder
	depth := 55
	for i := 0; i < depth; i++ {
		b.WriteString(`{"type":"group","operator":"AND","conditions":[`)
	}
	b.WriteString(`{"type":"simple","jsonPath":"$.x","operatorType":"EQUALS","value":1}`)
	for i := 0; i < depth; i++ {
		b.WriteString(`]}`)
	}

	_, err := predicate.ParseCondition([]byte(b.String()))
	if err == nil {
		t.Fatal("expected error for excessive nesting depth, got nil")
	}
	if !strings.Contains(err.Error(), "maximum depth") {
		t.Errorf("expected depth limit error, got: %v", err)
	}
}

func TestParseAtMaxDepthSucceeds(t *testing.T) {
	// Build a nested group condition exactly at maxParseDepth (50 levels).
	// This should succeed because we check depth > 50, not depth >= 50.
	var b strings.Builder
	depth := 50
	for i := 0; i < depth; i++ {
		b.WriteString(`{"type":"group","operator":"AND","conditions":[`)
	}
	b.WriteString(`{"type":"simple","jsonPath":"$.x","operatorType":"EQUALS","value":1}`)
	for i := 0; i < depth; i++ {
		b.WriteString(`]}`)
	}

	_, err := predicate.ParseCondition([]byte(b.String()))
	if err != nil {
		t.Fatalf("expected success at exactly max depth, got error: %v", err)
	}
}

func TestParseSimpleConditionOperatorAlias(t *testing.T) {
	// Cyoda schema uses "operator" as primary field name, with "operatorType" as alias.
	body := []byte(`{"type":"simple","jsonPath":"$.name","operator":"EQUALS","value":"Alice"}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("failed to parse with 'operator' field: %v", err)
	}
	sc, ok := cond.(*predicate.SimpleCondition)
	if !ok {
		t.Fatalf("expected *SimpleCondition, got %T", cond)
	}
	if sc.OperatorType != "EQUALS" {
		t.Errorf("expected operatorType EQUALS, got %q", sc.OperatorType)
	}
}

func TestParseLifecycleConditionOperatorAlias(t *testing.T) {
	body := []byte(`{"type":"lifecycle","field":"state","operator":"EQUALS","value":"ACTIVE"}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("failed to parse with 'operator' field: %v", err)
	}
	lc, ok := cond.(*predicate.LifecycleCondition)
	if !ok {
		t.Fatalf("expected *LifecycleCondition, got %T", cond)
	}
	if lc.OperatorType != "EQUALS" {
		t.Errorf("expected operatorType EQUALS, got %q", lc.OperatorType)
	}
}

func TestParseGroupConditionOperatorAlias(t *testing.T) {
	body := []byte(`{"type":"group","operator":"AND","conditions":[{"type":"simple","jsonPath":"$.x","operator":"EQUALS","value":1}]}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("failed to parse with 'operator' field: %v", err)
	}
	gc, ok := cond.(*predicate.GroupCondition)
	if !ok {
		t.Fatalf("expected *GroupCondition, got %T", cond)
	}
	if gc.Operator != "AND" {
		t.Errorf("expected operator AND, got %q", gc.Operator)
	}
}

func TestParseSimpleConditionOperationAlias(t *testing.T) {
	// Legacy alias "operation" (backwards compat, per @JsonAlias("operation", "operatorType")).
	body := []byte(`{"type":"simple","jsonPath":"$.is_errored","operation":"EQUALS","value":false}`)
	cond, err := predicate.ParseCondition(body)
	if err != nil {
		t.Fatalf("failed to parse with 'operation' field: %v", err)
	}
	sc, ok := cond.(*predicate.SimpleCondition)
	if !ok {
		t.Fatalf("expected *SimpleCondition, got %T", cond)
	}
	if sc.OperatorType != "EQUALS" {
		t.Errorf("expected operatorType EQUALS, got %q", sc.OperatorType)
	}
}
