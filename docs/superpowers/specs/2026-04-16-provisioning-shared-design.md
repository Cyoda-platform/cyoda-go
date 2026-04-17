# Canonical provisioning for cyoda-go — shared design

**Status:** Accepted
**Date:** 2026-04-16 (revised 2026-04-17)
**Scope:** Cross-target concerns shared by the three per-target provisioning designs (desktop, Docker, Helm). Per-target mechanics are deferred to their own specs.

## Motivation

cyoda-go is open source and ships three first-party storage plugins (`memory`, `sqlite`, `postgres`). Today the repo has dev-era artifacts (root `Dockerfile`, `docker-compose.yml`, ad-hoc shell scripts) and a Nexus-only image publishing pipeline. There are no public release artifacts, no Helm chart, and no README badges. The purpose of this work is to produce canonical provisioning flows for three audiences — evaluators (desktop), application developers (Docker), and operators (Helm) — and to clean up dev-era artifacts that now confuse rather than help.

This spec captures the decisions that apply across all three targets. Each per-target spec is brainstormed separately and consumes these decisions as inputs.

## Audience mapping

| Target | Persona | Priority |
|---|---|---|
| Desktop (prebuilt binaries + `go install`) | Evaluators, CLI users | 60-second start, no dependencies |
| Docker (image + reference compose) | Application developers | Repeatable local instance, volume persistence, easy opt-in to real auth |
| Helm chart | Operators running production | HA, real secrets, observability hooks |

## Runtime philosophy

The Go binary and the Docker image stay **unopinionated about configuration**. All `CYODA_*` environment variables pass through untouched; the binary's existing `DefaultConfig()` is the compiled-in floor (in particular, storage defaults to `memory`, unchanged).

**Opinionated defaults live in the provisioning layer** — packaging wrappers (Homebrew formula, `.deb`/`.rpm` postinstall, `curl|sh` installer), the reference compose file, and the Helm chart's `values.yaml`. One binary, three curated starting points.

## Per-target defaults

| Target | Storage backend | Auth | Data location |
|---|---|---|---|
| Desktop | `sqlite` (via packaging) | mock (with startup banner) if JWT unset | `$XDG_DATA_HOME/cyoda-go/` (falls back to `~/.local/share/cyoda-go/`) |
| Docker | `sqlite` (via compose env / image `CMD` wrapper) | mock (with startup banner) if JWT unset | `/var/lib/cyoda-go` (volume mount target) |
| Helm | `postgres` | mock (with startup banner) if JWT unset | N/A (Postgres-backed) |

Helm bundles `bitnami/postgresql` as a subchart with `postgresql.enabled: true` **by default** so `helm install cyoda-go oci://...` works against stock values. The chart ships a `values-production.yaml` preset that sets `postgresql.enabled: false` and expects an externally-managed Postgres URL — this is the preset production operators layer on top.

### How desktop gets sqlite without changing the binary

The Go binary's compiled-in default stays `memory` (unchanged). Desktop users who install via:

- `go install github.com/Cyoda-platform/cyoda-go/cmd/cyoda-go@latest` — get the raw binary with memory default. Power-user path; they're expected to set env.
- **Homebrew formula, `.deb`/`.rpm`, or the `curl|sh` installer script** — get a thin wrapper (or systemd unit, or launchd plist, or shell script on PATH) that sets `CYODA_STORAGE_BACKEND=sqlite` and `CYODA_SQLITE_PATH=$XDG_DATA_HOME/cyoda-go/cyoda.db` before invoking the binary.

The packaging layer owns the opinion. The desktop per-target spec picks which packaging paths ship in the first release.

## Secrets and auth

Two kinds of secret material, treated asymmetrically:

- **JWT signing key** — the cryptographic root of trust. The binary **never generates** this. In `jwt` mode, a missing key is a fatal startup error. Operators provide it via env (`CYODA_JWT_SIGNING_KEY`), env file, or mounted Kubernetes `Secret`. Documentation points at `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048`.
- **Bootstrap M2M client secret** — an ergonomic convenience for first-time setup. The binary **does** auto-generate this when `CYODA_BOOTSTRAP_CLIENT_SECRET` is unset, and prints the generated value to stdout exactly once at startup so the operator can copy it. This is existing, deliberate behavior — preserved as-is.

When `CYODA_IAM_MODE` is unset (or set explicitly to `mock`), the binary runs in **mock auth mode** and emits a prominent multi-line startup banner warning that auth is disabled and the instance must not be exposed to untrusted networks.

The banner is emitted **unconditionally whenever mock mode is active** — whether mock was chosen by default or set explicitly. Opt-out is a dedicated flag: `CYODA_SUPPRESS_BANNER=true` silences it. Our own E2E fixtures set that flag to keep test output clean; production operators never set it.

## Binary health and observability surface

The binary exposes endpoints on two listeners:

### API listener — `CYODA_HTTP_PORT` (default `8080`)

| Endpoint | Purpose |
|---|---|
| `/api/*` | Application traffic |
| `/api/health` | Existing overall health (retained for backwards compatibility) |

Plus the gRPC listener on `CYODA_GRPC_PORT` (default `9090`).

### Admin listener — `CYODA_ADMIN_PORT` (default `9091`)

| Endpoint | Purpose | Probe target |
|---|---|---|
| `/livez` | Liveness — process responsive, event loop alive | Kubernetes `livenessProbe`, Docker `HEALTHCHECK` |
| `/readyz` | Readiness — storage reachable, migrations applied, bootstrap complete | Kubernetes `readinessProbe`, load balancer health |
| `/metrics` | Prometheus pull endpoint | Prometheus scrape / `ServiceMonitor` |

The admin listener is **unauthenticated by design**. Operators must bind it to `localhost` or a cluster-internal network only — never expose it to untrusted traffic. The canonical Helm chart exposes a ClusterIP-only `Service` for the admin port; canonical compose binds it to `127.0.0.1`.

OTLP push (existing) stays, orthogonal to `/metrics`. Pull and push are both supported; operators pick one or both.

Canonical compose and Helm chart are **minimal** — they do not bundle Grafana / Prometheus / Tempo. The current Grafana-bundled compose relocates to `examples/compose-with-observability/` as an explicit dev convenience.

## Port convention

One convention across all three canonical artifacts, for consistency:

| Purpose | Env var | Default |
|---|---|---|
| HTTP API | `CYODA_HTTP_PORT` | `8080` |
| gRPC | `CYODA_GRPC_PORT` | `9090` |
| Admin (health + metrics) | `CYODA_ADMIN_PORT` | `9091` |

The `8123`/`9123` ports currently used by the `local` profile and its helper scripts remain a **local-profile override only** — they do not leak into the canonical provisioning artifacts.

## Schema compatibility contract

Shared across all deployment topologies. On startup the binary reads the on-disk schema version and compares it to the version its embedded migrations target:

- **Schema version matches** — proceed.
- **Schema older than code**, `AUTO_MIGRATE=true` — migrate forward, then proceed.
- **Schema older than code**, `AUTO_MIGRATE=false` — fail fast with a clear error message that points the operator at the migration procedure (Helm `Job`, or `cyoda-go migrate` CLI).
- **Schema newer than code** — fail fast unconditionally, regardless of `AUTO_MIGRATE`. This is what makes rolling downgrades / stale-binary-after-schema-change safe: the new pod gracefully refuses to serve against a schema it doesn't understand, rather than corrupting data.

No polling, no waiting. Failing fast with a clear error is the entire contract. Each deployment target's own spec builds on this (Helm's pre-install `Job` disables auto-migrate on the main deployment and runs the migration out-of-band; desktop/Docker leave auto-migrate on).

## Release and publishing model

**Decoupled versioning** — two independent tag streams. Versioning resets to `0.1.0` (app) and `0.1.0` (chart) — there is no prior history to preserve.

### App tags (`v0.1.0`, `v0.2.0-rc.1`, …) — binary and image

Triggered by pushing a semver tag matching `v*`. Produces:

- Binaries for `linux` / `darwin` / `windows` × `amd64` / `arm64`, uploaded to GitHub Releases with `SHA256SUMS` and cosign signatures. Built via GoReleaser. Binaries are **release-only** — no per-commit binary publishing.
- Multi-arch image `ghcr.io/cyoda-platform/cyoda-go:<version>`. For non-prerelease tags, also moves `:latest` to this image. **Prerelease semver tags** (`v0.2.0-rc.1`, `v0.3.0-beta.2`, etc.) are marked as GitHub pre-releases and **do not move `:latest`**. Images follow **Pattern A** — release tags only, no rolling `:main` / `:edge` tag. `:latest` always tracks the newest non-prerelease stable.
- Images signed with cosign using **keyless Sigstore signing via GitHub Actions OIDC**. No private keys are stored in the repo or GH secrets; the signature is bound to the workflow identity.
- SBOM (CycloneDX or SPDX) attached to each image.

Windows binaries ship from day one. Windows-specific packaging paths (MSI, Chocolatey, Scoop, Winget, service install) are deferred to the desktop per-target spec — a `.exe` in a `.zip` on the GH Release is the initial coverage.

### Chart tags (`cyoda-go-0.1.0`, `cyoda-go-0.2.0`, …) — Helm chart

Triggered by pushing a tag matching `cyoda-go-*`. Uses [`helm/chart-releaser-action`](https://github.com/helm/chart-releaser-action) — the Helm org's canonical chart-release workflow:

- Packages the chart from `deploy/helm/cyoda-go/`.
- Chart's `appVersion` field pins a tested app version (e.g., `appVersion: "0.1.0"`).
- The exact publishing target — GitHub Pages (chart-releaser's original mode, `gh-pages` branch with `index.yaml`) versus OCI at `ghcr.io/cyoda-platform/charts/cyoda-go` — is a mechanical choice for the Helm per-target spec. Both are supported by current tooling; this shared spec only commits to the tag scheme and packaging action.

The `cyoda-go-<version>` tag form is the convention `chart-releaser-action` expects. Diverging from it would mean writing a custom workflow — not worth it.

### Private Nexus publishing

Removed. The existing `.github/workflows/docker-publish.yml` is deleted.

## Migrations

Binary behavior unchanged. Both `CYODA_SQLITE_AUTO_MIGRATE` and `CYODA_POSTGRES_AUTO_MIGRATE` default `true`, use embedded SQL files via `embed.FS`, and are idempotent. The per-target nuance (Helm disabling auto-migrate and running migrations in a pre-install / pre-upgrade `Job` to avoid races across rolling pods) is deferred to the Helm-specific spec, and builds directly on the shared schema-compatibility contract above.

## Legacy cleanup

Executed as part of this work. Explicit decisions for every conspicuous root-level artifact, whether it moves or stays.

| Path | Action |
|---|---|
| `Dockerfile` (repo root) | Delete; canonical multi-arch Dockerfile lives at `deploy/docker/Dockerfile` |
| `docker-compose.yml` (repo root) | Delete; canonical compose at `deploy/docker/compose.yaml` |
| `cyoda-go-docker.sh` | Move to `scripts/dev/run-docker-dev.sh`; remove hardcoded bootstrap client secret (rely on runtime auto-generation) |
| `cyoda-go.sh` | Move to `scripts/dev/run-local.sh` |
| `scripts/multi-node-docker/` | Keep in place; add README pointer clarifying it's a dev/test tool |
| `.github/workflows/docker-publish.yml` | Delete; replaced by `release.yml` and `release-chart.yml` |
| Grafana-bundled compose fragments | Relocate to `examples/compose-with-observability/` with a README explaining scope |
| `CLAUDE.md`, `.claude/` | Keep — AI-dev workflow guardrails, intentionally in-repo |
| `safe-claude.sh` | Keep — AI-dev helper, intentionally in-repo |
| `OVERVIEW.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `LICENSE` | Keep — standard OSS metadata |
| `CYODA_OVERVIEW.md` et al | N/A — not present |

## Target repo layout after this work

```
deploy/
  docker/
    Dockerfile
    compose.yaml
    README.md
  helm/
    cyoda-go/                    # chart skeleton created here; filled in per-target spec
      values-production.yaml     # production preset: postgresql.enabled=false, external URL required
examples/
  compose-with-observability/
scripts/
  dev/
    run-local.sh
    run-docker-dev.sh
  multi-node-docker/             # unchanged
.github/workflows/
  ci.yml                         # unchanged
  release.yml                    # NEW: GoReleaser + multi-arch image + keyless cosign on v* tags
  release-chart.yml              # NEW: helm/chart-releaser-action on cyoda-go-* tags
```

## README badges

Added in the final step of the provisioning work, after first-release artifacts exist:

- CI build status
- Go Report Card
- Go Reference (pkg.go.dev)
- Latest GitHub release
- License (Apache-2.0)

A GHCR image-availability badge is **not** included — shields.io has no canonical provider for it, and the alternatives (custom endpoints, third-party counters) are flaky. Revisit once a reliable provider exists.

Sequencing badges after the first release ensures no badge points at a non-existent artifact.

## Out of scope for this spec

Each per-target spec takes these as inputs and fills in the mechanics:

- **Desktop:** packaging-path selection (Homebrew tap vs `curl|sh` installer vs `.deb`/`.rpm`); exact wrapper-script mechanism for injecting sqlite defaults; per-OS data-dir conventions beyond XDG; Windows packaging (MSI/Chocolatey/Scoop/Winget/service install); GoReleaser configuration details.
- **Docker:** exact `compose.yaml` shape, volume semantics, non-root UID policy, `HEALTHCHECK` wiring, image labelling (OCI annotations).
- **Helm:** chart values schema, probe parameters, Postgres subchart toggles beyond the default, `ServiceMonitor`, `NetworkPolicy`, `PodDisruptionBudget`, resource defaults, migration `Job`, upgrade hooks.

Also out of scope:

- **SPI versioning policy.** The `github.com/cyoda-platform/cyoda-go-spi` module's release cadence and compatibility guarantees are governed by its own repo's policy. A breaking SPI change forces synchronized plugin releases; this spec assumes the SPI's own policy handles that coordination and does not attempt to constrain it here.

## Downstream implementation plan

The implementation plan generated from this spec covers the shared-layer work:

1. Add `/livez`, `/readyz`, `/metrics` endpoints on a new admin listener (`CYODA_ADMIN_PORT=9091`), separate from the API listener (TDD).
2. Add `CYODA_SUPPRESS_BANNER` env var; emit mock-auth warning banner unconditionally in mock mode unless suppressed (TDD).
3. Implement the schema-compatibility contract: fail fast on schema-newer-than-code, fail fast on schema-older + auto-migrate off (TDD).
4. Introduce the target repo layout: `deploy/`, `examples/`, `scripts/dev/`.
5. Execute the legacy cleanup moves and deletions (including sanitizing the relocated dev scripts).
6. Add `.github/workflows/release.yml` (GoReleaser + multi-arch image + keyless cosign + SBOM on `v*` tags; prereleases don't move `:latest`).
7. Add `.github/workflows/release-chart.yml` using `helm/chart-releaser-action`, gated on the chart's existence.
8. Delete `.github/workflows/docker-publish.yml`.
9. Update `README.md`, `CONTRIBUTING.md`, `cmd/cyoda-go/main.go` `printHelp()` for new endpoints, new env vars (`CYODA_ADMIN_PORT`, `CYODA_SUPPRESS_BANNER`), new port convention, and new layout.
10. Add README badges (five) in a final commit once the first release has produced artifacts.

The per-target specs (desktop, Docker, Helm) are separate brainstorming cycles that consume this spec.
