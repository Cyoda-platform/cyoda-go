package main

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCatalog_NoopReturnsEntityUnchanged verifies the noop processor
// dispatches without modifying the entity.
func TestCatalog_NoopReturnsEntityUnchanged(t *testing.T) {
	cat := newCatalog()
	proc, ok := cat.processor("noop")
	if !ok {
		t.Fatal("noop processor not registered")
	}
	in := &Entity{
		ID:   "ent-1",
		Data: json.RawMessage(`{"x":1}`),
	}
	out, err := proc(context.Background(), in, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("noop returned error: %v", err)
	}
	if string(out.Data) != string(in.Data) {
		t.Errorf("noop changed data: got %s, want %s", string(out.Data), string(in.Data))
	}
}

// TestCatalog_AlwaysTrueReturnsTrue verifies the always-true criterion.
func TestCatalog_AlwaysTrueReturnsTrue(t *testing.T) {
	cat := newCatalog()
	crit, ok := cat.criterion("always-true")
	if !ok {
		t.Fatal("always-true criterion not registered")
	}
	got, err := crit(context.Background(), &Entity{ID: "ent-1"}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("always-true returned error: %v", err)
	}
	if !got {
		t.Error("always-true returned false")
	}
}

// TestCatalog_AlwaysFalseReturnsFalse verifies the always-false criterion.
func TestCatalog_AlwaysFalseReturnsFalse(t *testing.T) {
	cat := newCatalog()
	crit, ok := cat.criterion("always-false")
	if !ok {
		t.Fatal("always-false criterion not registered")
	}
	got, err := crit(context.Background(), &Entity{ID: "ent-1"}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("always-false returned error: %v", err)
	}
	if got {
		t.Error("always-false returned true")
	}
}

// TestCatalog_TagWithFoo verifies tag-with-foo adds tag:"foo" to entity data.
func TestCatalog_TagWithFoo(t *testing.T) {
	cat := newCatalog()
	proc, ok := cat.processor("tag-with-foo")
	if !ok {
		t.Fatal("tag-with-foo processor not registered")
	}
	in := &Entity{
		ID:   "ent-2",
		Data: json.RawMessage(`{"x":1}`),
	}
	out, err := proc(context.Background(), in, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tag-with-foo returned error: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(out.Data, &data); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if data["tag"] != "foo" {
		t.Errorf("tag-with-foo: got tag=%v, want foo", data["tag"])
	}
}

// TestCatalog_BumpAmount verifies bump-amount increments data.amount by 1.
func TestCatalog_BumpAmount(t *testing.T) {
	cat := newCatalog()
	proc, ok := cat.processor("bump-amount")
	if !ok {
		t.Fatal("bump-amount processor not registered")
	}
	in := &Entity{
		ID:   "ent-3",
		Data: json.RawMessage(`{"amount":5}`),
	}
	out, err := proc(context.Background(), in, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("bump-amount returned error: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(out.Data, &data); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if data["amount"] != float64(6) {
		t.Errorf("bump-amount: got amount=%v, want 6", data["amount"])
	}
}

// TestCatalog_InjectError verifies inject-error returns a non-nil error.
func TestCatalog_InjectError(t *testing.T) {
	cat := newCatalog()
	proc, ok := cat.processor("inject-error")
	if !ok {
		t.Fatal("inject-error processor not registered")
	}
	_, err := proc(context.Background(), &Entity{ID: "ent-4", Data: json.RawMessage(`{}`)}, json.RawMessage(`{}`))
	if err == nil {
		t.Error("inject-error returned nil error, want non-nil")
	}
}

// TestCatalog_SlowConfigurableZeroMS verifies slow-configurable with 0ms returns immediately.
func TestCatalog_SlowConfigurableZeroMS(t *testing.T) {
	cat := newCatalog()
	proc, ok := cat.processor("slow-configurable")
	if !ok {
		t.Fatal("slow-configurable processor not registered")
	}
	in := &Entity{ID: "ent-5", Data: json.RawMessage(`{}`)}
	out, err := proc(context.Background(), in, json.RawMessage(`{"sleep_ms":0}`))
	if err != nil {
		t.Fatalf("slow-configurable returned error: %v", err)
	}
	if out.ID != in.ID {
		t.Errorf("slow-configurable changed entity ID: got %s, want %s", out.ID, in.ID)
	}
}

// TestCatalog_SetField verifies set-field sets the specified field.
func TestCatalog_SetField(t *testing.T) {
	cat := newCatalog()
	proc, ok := cat.processor("set-field")
	if !ok {
		t.Fatal("set-field processor not registered")
	}
	in := &Entity{ID: "ent-6", Data: json.RawMessage(`{"x":1}`)}
	cfg := json.RawMessage(`{"field":"status","value":"active"}`)
	out, err := proc(context.Background(), in, cfg)
	if err != nil {
		t.Fatalf("set-field returned error: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(out.Data, &data); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if data["status"] != "active" {
		t.Errorf("set-field: got status=%v, want active", data["status"])
	}
}

// TestCatalog_AmountGt100 verifies amount-gt-100 returns true for 200 and false for 50.
func TestCatalog_AmountGt100(t *testing.T) {
	cat := newCatalog()
	crit, ok := cat.criterion("amount-gt-100")
	if !ok {
		t.Fatal("amount-gt-100 criterion not registered")
	}

	got, err := crit(context.Background(), &Entity{ID: "ent-7", Data: json.RawMessage(`{"amount":200}`)}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("amount-gt-100 returned error: %v", err)
	}
	if !got {
		t.Error("amount-gt-100: got false for amount=200, want true")
	}

	got, err = crit(context.Background(), &Entity{ID: "ent-8", Data: json.RawMessage(`{"amount":50}`)}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("amount-gt-100 returned error: %v", err)
	}
	if got {
		t.Error("amount-gt-100: got true for amount=50, want false")
	}
}

// TestCatalog_FieldEquals verifies field-equals returns true for matching and false for non-matching.
func TestCatalog_FieldEquals(t *testing.T) {
	cat := newCatalog()
	crit, ok := cat.criterion("field-equals")
	if !ok {
		t.Fatal("field-equals criterion not registered")
	}

	cfg := json.RawMessage(`{"field":"color","value":"blue"}`)
	got, err := crit(context.Background(), &Entity{ID: "ent-9", Data: json.RawMessage(`{"color":"blue"}`)}, cfg)
	if err != nil {
		t.Fatalf("field-equals returned error: %v", err)
	}
	if !got {
		t.Error("field-equals: got false for matching value, want true")
	}

	got, err = crit(context.Background(), &Entity{ID: "ent-10", Data: json.RawMessage(`{"color":"red"}`)}, cfg)
	if err != nil {
		t.Fatalf("field-equals returned error: %v", err)
	}
	if got {
		t.Error("field-equals: got true for non-matching value, want false")
	}
}
