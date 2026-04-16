# Documentation Hygiene

## Keep documentation in sync with code

When making changes that affect the public interface or developer workflow, check whether documentation is still accurate. The main places to look:

- **`README.md`** — what the project is, how to run it, configuration reference
- **`CONTRIBUTING.md`** — how to develop, test, and submit changes
- **`cmd/cyoda-go/main.go` (`printHelp()`)** — CLI help text
- **`CLAUDE.md`** — AI developer context, development gates, workflow

When adding or changing environment variables, update `printHelp()`, `README.md`, and `DefaultConfig()` together.

## What not to update

- `docs/plans/` — historical records, not living documents
- Don't write docs for things that are obvious from the code
