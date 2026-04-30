// Package openapivalidator validates HTTP responses captured during E2E tests
// against the OpenAPI 3.1 spec at api/openapi.yaml. See doc.go for full
// architecture and the design at
// docs/superpowers/specs/2026-04-29-issue-21-openapi-conformance-design.md.
package openapivalidator

// Mode controls whether validation failures fail the suite.
//
// ModeRecord: collect mismatches, write the report file, do NOT fail.
// ModeEnforce: same, plus fail TestOpenAPIConformanceReport (full suite)
// or t.Errorf the requesting test (-run-filtered single-test workflow).
//
// Default is ModeRecord during the conformance work (commits 1-10 of #21).
// The final commit flips this to ModeEnforce. See
// docs/adr/0001-openapi-server-spec-conformance.md.
const Mode = ModeRecord

// ModeKind is an int because comparing constants by string would tempt
// runtime configuration via env var, which we explicitly rejected (see ADR).
type ModeKind int

const (
	ModeRecord ModeKind = iota
	ModeEnforce
)
