package events

import (
	"bytes"
	"encoding/json"
)

// decodeWithUseNumber decodes JSON with UseNumber enabled so numeric
// literals in freeform fields (map[string]interface{} / interface{}) are
// preserved as json.Number instead of being coerced to float64 and losing
// precision above 2^53 (issue #79).
//
// This helper backs every generated UnmarshalJSON method in this package.
// The generator emits `json.Unmarshal(value, ...)` by default; a post-
// processing step in scripts/generate-events.sh rewrites those calls to
// reference this helper. Keep the helper in a hand-written file (not the
// generated types.go) so regeneration preserves it.
func decodeWithUseNumber(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}
