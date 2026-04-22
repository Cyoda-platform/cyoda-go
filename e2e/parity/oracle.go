// Package parity — oracle.go
//
// Deterministic in-memory oracles used by B parity tests. These
// helpers produce the bytes that a byte-identical fold MUST return
// for the named input sequence, computed via importer.Walk +
// schema.Extend + exporter.SimpleViewExporter.Export. Backends
// matching these bytes satisfy B-I1 at the HTTP boundary.
package parity

import (
	"bytes"
	"encoding/json"
	"fmt"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/exporter"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// expectedSimpleViewFromBodies computes the canonical SIMPLE_VIEW
// bytes for a sequence of JSON bodies applied sequentially at
// ChangeLevelStructural. The first body seeds the schema; each
// subsequent body is Walk + Extend.
//
// currentState is baked into the exporter output ("LOCKED" for
// post-lock tests, "UNLOCKED" otherwise).
func expectedSimpleViewFromBodies(bodies []map[string]string, currentState string) ([]byte, error) {
	var current *schema.ModelNode
	for i, body := range bodies {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("oracle: marshal body %d: %w", i, err)
		}
		// importer.Walk requires json.Number for numeric values — strings here
		// won't hit that path, but UseNumber keeps the oracle robust if the
		// sequence is extended with numeric fields in the future.
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var parsed any
		if err := dec.Decode(&parsed); err != nil {
			return nil, fmt.Errorf("oracle: parse body %d: %w", i, err)
		}
		walked, err := importer.Walk(parsed)
		if err != nil {
			return nil, fmt.Errorf("oracle: walk body %d: %w", i, err)
		}
		if current == nil {
			current = walked
			continue
		}
		next, err := schema.Extend(current, walked, spi.ChangeLevelStructural)
		if err != nil {
			return nil, fmt.Errorf("oracle: extend body %d: %w", i, err)
		}
		current = next
	}
	if current == nil {
		return nil, nil
	}
	return exporter.NewSimpleViewExporter(currentState).Export(current)
}

// expectedSimpleViewFromSequence computes the canonical SIMPLE_VIEW
// bytes for the n-field-widening sequence used by B-I1 byte-identity
// tests. Body 0 has only field_0; body n-1 has field_0..field_{n-1}.
func expectedSimpleViewFromSequence(n int, currentState string) ([]byte, error) {
	bodies := make([]map[string]string, 0, n)
	for i := 0; i < n; i++ {
		body := map[string]string{}
		for j := 0; j <= i; j++ {
			body[fmt.Sprintf("field_%d", j)] = fmt.Sprintf("v%d", j)
		}
		bodies = append(bodies, body)
	}
	return expectedSimpleViewFromBodies(bodies, currentState)
}
