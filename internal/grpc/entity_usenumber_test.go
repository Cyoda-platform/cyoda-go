package grpc_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestCloudEventDecode_UseNumber_LargeInteger ensures that numerics
// larger than 2^53 survive the gRPC dispatch decoder. The production
// fix installs json.UseNumber() at every ingestion site in
// internal/grpc/entity.go; this test asserts the library behavior
// we rely on.
func TestCloudEventDecode_UseNumber_LargeInteger(t *testing.T) {
	const payload = `{"payload":{"data":{"x":9007199254740993}}}`
	type Envelope struct {
		Payload struct {
			Data any `json:"data"`
		} `json:"payload"`
	}

	// Without UseNumber — reproduces the current production bug.
	var without Envelope
	if err := json.Unmarshal([]byte(payload), &without); err != nil {
		t.Fatalf("without UseNumber: %v", err)
	}
	withoutStr := mustMarshal(t, without.Payload.Data)
	// Expect: float64 round-trip loses the final digit. We just log it
	// for documentation; not an assertion because the loss is the bug.
	t.Logf("without UseNumber re-marshals as: %s", withoutStr)

	// With UseNumber — the fix.
	var with Envelope
	dec := json.NewDecoder(bytes.NewReader([]byte(payload)))
	dec.UseNumber()
	if err := dec.Decode(&with); err != nil {
		t.Fatalf("with UseNumber: %v", err)
	}
	withStr := mustMarshal(t, with.Payload.Data)
	if !strings.Contains(withStr, "9007199254740993") {
		t.Errorf("with UseNumber: expected exact magnitude preserved; got %s", withStr)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
