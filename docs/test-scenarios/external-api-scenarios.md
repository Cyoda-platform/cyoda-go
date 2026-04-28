# External API Scenarios

cyoda-go ships a copy of cyoda-cloud's language-agnostic External API
Scenario Dictionary under `e2e/externalapi/scenarios/` as reference
documentation.

- **Source:** `cyoda/.ai/plans/external-api-scenarios/` in the cyoda-cloud
  repository. Files are copied verbatim — do not edit them here; propose
  changes upstream.
- **Runner:** The Go test functions that implement these scenarios live
  under `e2e/parity/externalapi/` and register in `e2e/parity/registry.go`.
  YAML is the spec, Go is the source of truth.
- **Triage status:** `e2e/externalapi/dictionary-mapping.md` tracks which
  scenarios are implemented, which are gaps, and which are deliberately
  skipped (internal-only or shape-only).
- **Driver:** `e2e/externalapi/driver/` — `NewInProcess(fixture)` for
  parity-harness use; `NewRemote(baseURL, jwt)` for pointing at an
  arbitrary cyoda instance.
- **Design:** `docs/superpowers/specs/2026-04-24-external-api-scenarios-design.md`
