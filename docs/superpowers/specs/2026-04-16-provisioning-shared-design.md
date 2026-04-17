# Canonical provisioning for cyoda-go — shared design

**Status:** Accepted
**Date:** 2026-04-16
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

The Go binary and the Docker image stay **unopinionated about configuration**. All `CYODA_*` environment variables pass through untouched; the binary's existing `DefaultConfig()` is the compiled-in floor.

**Opinionated defaults live in the provisioning layer** — the reference compose file, the chart's `values.yaml`, and the helper scripts. One binary, three curated starting points.

## Per-target defaults

| Target | Storage backend | Auth | Data location |
|---|---|---|---|
| Desktop | `sqlite` | mock (with startup warning) if JWT unset | `$XDG_DATA_HOME/cyoda-go/` (falls back to `~/.local/share/cyoda-go/`) |
| Docker | `sqlite` | mock (with startup warning) if JWT unset | `/var/lib/cyoda-go` (volume mount target) |
| Helm | `postgres` | mock (with startup warning) if JWT unset | N/A (Postgres-backed) |

Helm bundles `bitnami/postgresql` as an optional subchart with `postgresql.enabled: false` by default — production operators bring their own Postgres, evaluators can flip the flag.

## Secrets and auth

The binary never generates secrets. When `CYODA_IAM_MODE` is unset or the JWT signing key / bootstrap client credentials are missing, the binary starts in **mock auth mode** and emits a prominent multi-line startup banner warning that auth is disabled and the instance must not be exposed to untrusted networks.

Real deployments supply secrets from outside:

- **Desktop:** env vars or `.env` file.
- **Docker:** env file mounted at runtime or `--env` flags.
- **Helm:** Kubernetes `Secret` object referenced by the chart.

Key generation remains the operator's responsibility. Documentation points at `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048` for JWT signing keys and standard entropy sources for bootstrap secrets.

## Binary health and observability surface

The binary exposes the following endpoints; all three provisioning targets depend on this contract:

| Endpoint | Purpose | Probe target |
|---|---|---|
| `/api/health` | Existing overall health (retained for backwards compatibility) | — |
| `/livez` | Liveness — process responsive, event loop alive | Kubernetes `livenessProbe`, Docker `HEALTHCHECK` |
| `/readyz` | Readiness — storage reachable, migrations applied, bootstrap complete | Kubernetes `readinessProbe`, load balancer health |
| `/metrics` | Prometheus pull endpoint | Prometheus scrape / `ServiceMonitor` |

OTLP push (existing) stays. Pull and push are both supported; operators pick one or both.

Canonical compose and Helm chart are **minimal** — they do not bundle Grafana / Prometheus / Tempo. The current Grafana-bundled compose relocates to `examples/compose-with-observability/` as an explicit dev convenience.

## Release and publishing model

**Decoupled versioning** — two independent tag streams.

### App tags (`v1.2.3`) — binary and image

Triggered by pushing a semver tag matching `v*`. Produces:

- Binaries for linux / darwin / windows × amd64 / arm64, uploaded to GitHub Releases with `SHA256SUMS` and cosign signatures. Built via GoReleaser.
- Multi-arch image `ghcr.io/cyoda-platform/cyoda-go:<version>` (and `:latest` for the newest tag), signed with cosign, SBOM attached.

### Chart tags (`chart/v0.4.1`) — Helm chart

Triggered by pushing a tag matching `chart/v*`. Produces:

- Helm chart as an OCI artifact at `ghcr.io/cyoda-platform/charts/cyoda-go:<chart-version>`.
- Chart's `appVersion` field pins a tested app version.

### Private Nexus publishing

Removed. The existing `.github/workflows/docker-publish.yml` is deleted.

## Migrations

Binary behavior unchanged. Both `CYODA_SQLITE_AUTO_MIGRATE` and `CYODA_POSTGRES_AUTO_MIGRATE` default `true`, use embedded SQL files via `embed.FS`, and are idempotent. The per-target nuance (Helm disabling auto-migrate and running migrations in a pre-install / pre-upgrade `Job` to avoid races across rolling pods) is deferred to the Helm-specific spec.

## Legacy cleanup

Executed as part of this work:

| Path | Action |
|---|---|
| `Dockerfile` (repo root) | Delete; canonical multi-arch Dockerfile lives at `deploy/docker/Dockerfile` |
| `docker-compose.yml` (repo root) | Delete; canonical compose at `deploy/docker/compose.yaml` |
| `cyoda-go-docker.sh` | Move to `scripts/dev/run-docker-dev.sh`; remove hardcoded bootstrap client secret (replace with runtime-generated placeholder) |
| `cyoda-go.sh` | Move to `scripts/dev/run-local.sh` |
| `scripts/multi-node-docker/` | Keep in place; add README pointer clarifying it's a dev/test tool |
| `.github/workflows/docker-publish.yml` | Delete; replaced by `release.yml` and `release-chart.yml` |
| Grafana-bundled compose fragments | Relocate to `examples/compose-with-observability/` with a README explaining scope |

## Target repo layout after this work

```
deploy/
  docker/
    Dockerfile
    compose.yaml
    README.md
  helm/
    cyoda-go/                    # chart skeleton created here; filled in per-target spec
examples/
  compose-with-observability/
scripts/
  dev/
    run-local.sh
    run-docker-dev.sh
  multi-node-docker/             # unchanged
.github/workflows/
  ci.yml                         # unchanged
  release.yml                    # NEW: GoReleaser + multi-arch image + cosign on v* tags
  release-chart.yml              # NEW: Helm OCI publish on chart/v* tags
```

## README badges

Added in the final step of the provisioning work, after first-release artifacts exist:

- CI build status
- Go Report Card
- Go Reference (pkg.go.dev)
- Latest GitHub release
- License (Apache-2.0)
- GHCR image availability

Sequencing them after the first release ensures no badge points at a non-existent artifact.

## Out of scope for this spec

Each per-target spec takes these as inputs and fills in the mechanics:

- **Desktop:** `go install` vs Homebrew tap vs `curl | sh` installer; exact per-OS data-dir conventions; Windows support level; GoReleaser configuration.
- **Docker:** exact `compose.yaml` shape, volume semantics, non-root UID policy, `HEALTHCHECK` wiring, image labelling (OCI annotations).
- **Helm:** chart values schema, probe parameters, Postgres subchart toggles, `ServiceMonitor`, `NetworkPolicy`, `PodDisruptionBudget`, resource defaults, migration `Job`, upgrade hooks.

## Downstream implementation plan

The implementation plan generated from this spec covers the shared-layer work:

1. Add `/livez`, `/readyz`, and `/metrics` endpoints to the binary (TDD).
2. Add mock-auth startup warning banner (TDD).
3. Introduce the target repo layout: `deploy/`, `examples/`, `scripts/dev/`.
4. Execute the legacy cleanup moves and deletions.
5. Add `.github/workflows/release.yml` (GoReleaser + multi-arch image + cosign).
6. Add `.github/workflows/release-chart.yml` (Helm OCI publish), gated on the chart's existence.
7. Delete `.github/workflows/docker-publish.yml`.
8. Update `README.md`, `CONTRIBUTING.md`, `printHelp()` for new endpoints, new defaults, and new layout.
9. Add README badges in a final commit once the first release has produced artifacts.

The per-target specs (desktop, Docker, Helm) are separate brainstorming cycles that consume this spec.
