package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaOpKindString(t *testing.T) {
	cases := []struct {
		kind SchemaOpKind
		want string
	}{
		{KindAddProperty, "add_property"},
		{KindBroadenType, "broaden_type"},
		{KindAddArrayItemType, "add_array_item_type"},
	}
	for _, tc := range cases {
		if string(tc.kind) != tc.want {
			t.Errorf("kind %q: got %q want %q", tc.want, string(tc.kind), tc.want)
		}
	}
}

func TestNewAddPropertyCopiesPayload(t *testing.T) {
	sub := []byte(`{"kind":"LEAF","types":["STRING"]}`)
	op := NewAddProperty("address", "zip", sub)
	if op.Kind != KindAddProperty {
		t.Errorf("kind: got %q", op.Kind)
	}
	if op.Path != "address" {
		t.Errorf("path: got %q", op.Path)
	}
	if op.Name != "zip" {
		t.Errorf("name: got %q", op.Name)
	}
	if string(op.Payload) != string(sub) {
		t.Errorf("payload: got %s want %s", op.Payload, sub)
	}
	// Mutating the source must not affect the stored op.
	sub[0] = 'X'
	if op.Payload[0] == 'X' {
		t.Errorf("NewAddProperty must copy payload, but it aliased the input")
	}
}

func TestNewBroadenTypeEncodesSortedUnique(t *testing.T) {
	op, err := NewBroadenType("age", []DataType{String, Null, String})
	if err != nil {
		t.Fatalf("NewBroadenType: %v", err)
	}
	if op.Kind != KindBroadenType || op.Path != "age" {
		t.Errorf("op header mismatch: %+v", op)
	}
	var got []string
	if err := json.Unmarshal(op.Payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	want := []string{"NULL", "STRING"} // alphabetical + dedup
	if !equalStrings(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestNewAddArrayItemType_Ok(t *testing.T) {
	op, err := NewAddArrayItemType("tags", []DataType{Integer})
	if err != nil {
		t.Fatalf("NewAddArrayItemType: %v", err)
	}
	if op.Kind != KindAddArrayItemType {
		t.Errorf("kind: %q", op.Kind)
	}
}

func TestNewBroadenType_RejectsEmpty(t *testing.T) {
	_, err := NewBroadenType("x", nil)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("expected empty-list error, got %v", err)
	}
}

func TestDecodeTypeNames_RoundTrip(t *testing.T) {
	in := []DataType{String, Null}
	op, err := NewBroadenType("p", in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := DecodeTypeNames(op.Payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len: got %d want %d", len(out), len(in))
	}
	// DecodeTypeNames preserves the stable-sorted order produced by
	// encodeTypeNames — alphabetical by canonical name.
	wantOrder := []DataType{Null, String}
	for i, dt := range out {
		if dt != wantOrder[i] {
			t.Errorf("type[%d]: got %v want %v", i, dt, wantOrder[i])
		}
	}
}

func TestDecodeTypeNames_UnknownName(t *testing.T) {
	_, err := DecodeTypeNames(json.RawMessage(`["WIDGET"]`))
	if err == nil {
		t.Error("expected error on unknown type name")
	}
}

func TestMarshalUnmarshalDelta_RoundTrip(t *testing.T) {
	broaden, err := NewBroadenType("age", []DataType{Null})
	if err != nil {
		t.Fatalf("build op: %v", err)
	}
	arr, err := NewAddArrayItemType("tags", []DataType{Integer})
	if err != nil {
		t.Fatalf("build op: %v", err)
	}
	ops := []SchemaOp{
		NewAddProperty("address", "zip", []byte(`{"kind":"LEAF","types":["STRING"]}`)),
		broaden,
		arr,
	}
	delta, err := MarshalDelta(ops)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalDelta(delta)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != len(ops) {
		t.Fatalf("len: got %d want %d", len(got), len(ops))
	}
	for i := range ops {
		if got[i].Kind != ops[i].Kind {
			t.Errorf("op %d kind: got %q want %q", i, got[i].Kind, ops[i].Kind)
		}
		if got[i].Path != ops[i].Path {
			t.Errorf("op %d path: got %q want %q", i, got[i].Path, ops[i].Path)
		}
		if got[i].Name != ops[i].Name {
			t.Errorf("op %d name: got %q want %q", i, got[i].Name, ops[i].Name)
		}
		if string(got[i].Payload) != string(ops[i].Payload) {
			t.Errorf("op %d payload mismatch", i)
		}
	}
}

func TestMarshalDelta_EmptyIsNil(t *testing.T) {
	d, err := MarshalDelta(nil)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if d != nil {
		t.Errorf("expected nil delta for empty ops, got %v", d)
	}
	ops, err := UnmarshalDelta(nil)
	if err != nil {
		t.Fatalf("unmarshal nil: %v", err)
	}
	if ops != nil {
		t.Errorf("expected nil ops for nil delta, got %v", ops)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
