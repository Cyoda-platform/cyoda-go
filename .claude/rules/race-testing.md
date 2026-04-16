---
paths:
  - "**/*.go"
---
# Race Detector Discipline

`go test -race` is a sanity check at the end of a deliverable, not a continuous-iteration
gate. Run it once before creating a PR — not at every intermediate step.

## Rules

- **During iteration** (subagent dispatch, single-task verification, between commits):
  use `go test -short ./...` or scoped tests like `go test ./internal/foo/...`. No `-race`.
- **Before PR creation** (and only then): one full `go test -race ./...` as a sanity check.
- **If a race-related bug is suspected**: run `-race` on the specific package
  while debugging, then drop it once the fix lands.

## Why

`-race` instrumentation makes tests 2-10x slower. Running it at every step burns
wall-clock time without finding new bugs — races that exist after a small change
were almost certainly there before. The end-of-deliverable run catches what matters.
