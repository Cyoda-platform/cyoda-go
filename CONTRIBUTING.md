# Contributing to Cyoda-Go

## Methodology

This project follows **strict Red/Green TDD** and **trunk-based development** on `main`.

## Delivery Flow

Every feature follows this flow:

```
1. Create feature branch from main
2. Execute with strict Red/Green TDD:
   a. Write failing test (RED) — run it, verify it fails
   b. Implement minimal code (GREEN) — run it, verify it passes
   c. Refactor — all tests stay green
   d. Commit
3. Run E2E tests:
   - If Docker socket is available: run directly (go test ./internal/e2e/ -v)
   - If sandboxed without Docker: human operator runs E2E tests and provides feedback
4. Code review (code-reviewer)
   -> Fix all Critical/Important findings
5. Security audit (security-auditor)
   -> Fix all Critical/Important findings
6. Create PR to main
7. Squash merge
```

## Testing Policy

Every feature must have tests at the appropriate level before it can be merged.

**Unit tests** cover individual functions and components in isolation. They run fast, need no infrastructure, and form the bulk of the test suite.

**E2E tests** (`internal/e2e/`) spin up a full Cyoda-Go instance backed by PostgreSQL (via testcontainers) and exercise the complete HTTP API path — from request to database and back. They verify that wiring, middleware, auth, persistence, and business logic work together correctly. E2E tests require Docker.

**Reconciliation tests** (`test/recon/`, build tag `cyoda_recon`) compare Cyoda-Go responses against Cyoda Cloud to verify API-level compatibility. These are optional and require Cloud credentials.

```bash
go test ./... -v                          # all unit tests (no Docker needed)
go test ./internal/e2e/ -v               # E2E tests (requires Docker)
go test -race ./...                       # race detector — run before every PR
go test -tags cyoda_recon ./test/recon/   # reconciliation (optional, needs Cloud)
```

## Common Commands

| Command | Description |
|---------|-------------|
| `go run ./cmd/cyoda` | Run from source |
| `go build -o bin/cyoda ./cmd/cyoda` | Build executable |
| `go test ./... -v` | Run all tests |
| `go test -race ./...` | Run tests with race detector |
| `go test -coverprofile=coverage.out ./...` | Test coverage |
| `./scripts/dev/run-docker-dev.sh` | Start with Docker + PostgreSQL |

## CI checks

Every PR must pass the following gates before it can be merged:

| Job | Workflow | Purpose |
|-----|----------|---------|
| `test` | `ci.yml` | Unit + integration tests, race detector, `go build`. |
| `per-module-hygiene` | `ci.yml` | Each plugin module builds and vets independently with `GOWORK=off` (protects downstream consumers). |
| `security` | `ci.yml` | `govulncheck` against the root module and each plugin submodule; `actions/dependency-review-action` on PR diffs. |
| `Analyze Go` | `codeql.yml` | CodeQL static analysis (`security-and-quality` pack). Findings surface in the Security tab and as PR annotations. Also runs on a weekly cron. |
| `shellcheck` | `ci.yml` | Lint for shell scripts. |

The `security` job is blocking: any vulnerability finding in the call graph, or any new dependency at `moderate` severity or above, fails the PR. To reproduce locally:

```bash
go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
govulncheck ./...
(cd plugins/memory && govulncheck ./...)
(cd plugins/postgres && govulncheck ./...)
(cd plugins/sqlite && govulncheck ./...)
```

Release builds additionally run a Trivy scan against the published GHCR image (`.github/workflows/release.yml`). Results are surfaced in the release run's job summary. This is advisory — the tag is already published by the time Trivy runs; pre-merge gating is the `security` job's responsibility.

## Dependencies

No external web frameworks. No DI frameworks. No ORM.

| Dependency | Purpose |
|------------|---------|
| Go standard library `net/http` | HTTP server and routing (Go 1.22+ pattern matching) |
| `github.com/google/uuid` | UUID generation |
| `github.com/jackc/pgx/v5` | PostgreSQL driver (only loaded when postgres backend is active) |
| `google.golang.org/grpc` | gRPC server for externalized processor/criteria dispatch |

## Provisioning Artifacts

Canonical provisioning artifacts (Helm chart, Docker Compose files) live under `deploy/`. Developer convenience scripts (local run, Docker dev setup) live under `scripts/dev/`. For the design rationale and structure of the shared provisioning layer, see [`docs/superpowers/specs/2026-04-16-provisioning-shared-design.md`](docs/superpowers/specs/2026-04-16-provisioning-shared-design.md).

## Developer Setup

1. [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) with [superpowers](https://github.com/obra/superpowers)
2. [agent-safehouse](https://github.com/eugene1g/safehouse) — `brew install eugene1g/safehouse/agent-safehouse`
3. [Zed editor](https://zed.dev) — `brew install --cask zed`
