// Package client holds the HTTP and gRPC clients used by parity scenarios
// to talk to a running cyoda-go server.
//
// Value types (EntityResult, EntityMetadata, EntityChangeMeta, etc.)
// mirror the canonical Cyoda Cloud public API surface.
//
// Two approved deviations from the published OpenAPI spec apply:
//
//   - Entity GET responses use a {type, data, meta} envelope, not
//     bare data — the published OpenAPI spec is inaccurate here.
//   - modelKey is present on getOneEntity meta and absent from
//     getAllEntities / search meta. The asymmetry is intentional.
//
// HTTP responses are decoded with json.Decoder.DisallowUnknownFields().
// Any new field added to the canonical contract without a matching
// update to these value types fails round-trip tests loudly.
//
// These types are decoupled from internal/common so the parity package
// can be imported by an external Go module — in particular by
// out-of-tree storage plugins that want to validate against the same
// scenarios.
//
// Note: this package is intended for use only inside parity tests.
// All operation methods take *testing.T for failure attribution via
// t.Helper(), so the client cannot be used outside a *testing.T
// context. Production use is not supported.
package client
