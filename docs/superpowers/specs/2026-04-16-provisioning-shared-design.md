# Canonical provisioning for cyoda-go — shared design

**Status:** Accepted
**Date:** 2026-04-16 (revised 2026-04-17)
**Scope:** Cross-target concerns shared by the three per-target provisioning designs (desktop, Docker, Helm). Per-target mechanics are deferred to their own specs.

> **Supersession on artifact names (2026-04-17):** the desktop per-target spec at `docs/superpowers/specs/2026-04-17-provisioning-desktop-design.md` narrows the user-facing artifact name from `cyoda-go` to `cyoda` — binary, container image, Helm chart, `.deb`/`.rpm` package, Homebrew formula. The repo name, Go module path, plugin module paths, and `CYODA_*` env-var prefix are unchanged. Read every reference to `cyoda-go` below as `cyoda` where the context is a user-facing artifact; references to the Go module path or the GitHub repo stay as written. The rename lands on PR #44 before merge.

## Motivation

cyoda-go is open source and ships three first-party storage plugins (`memory`, `sqlite`, `postgres`). Today the repo has dev-era artifacts (root `Dockerfile`, `docker-compose.yml`, ad-hoc shell scripts) and a Nexus-only image publishing pipeline. There are no public release artifacts, no Helm chart, and no README badges. The purpose of this work is to produce canonical provisioning flows for three audiences — evaluators (desktop), application developers (Docker), and operators (Helm) — and to clean up dev-era artifacts that now confuse rather than help.

This spec captures the decisions that apply across all three targets. Each per-target spec is brainstormed separately and consumes these decisions as inputs.

## Audience mapping

| Target | Persona | Priority |
|---|---|---|
| Desktop — **packaged install** (Homebrew / `.deb` / `.rpm` / `curl\|sh`) | Evaluators, CLI users | 60-second start, no dependencies, sqlite default |
| Desktop — `go install` | Power users, Go developers | Bare binary; accepts the binary's compiled-in defaults (memory) |
| Docker (image + reference compose) | Application developers | Repeatable local instance, volume persistence, easy opt-in to real auth |
| Helm chart | Operators running production | HA, real secrets, observability hooks |

## Runtime philosophy

The Go binary and the Docker image stay **unopinionated about configuration**. All `CYODA_*` environment variables pass through untouched; the binary's existing `DefaultConfig()` is the compiled-in floor (in particular, storage defaults to `memory`, unchanged).

**Opinionated defaults live in the provisioning layer** — packaging wrappers (Homebrew formula, `.deb`/`.rpm` postinstall, `curl|sh` installer), the reference compose file, and the Helm chart's `values.yaml`. One binary, three curated starting points.

## Per-target defaults

| Target | Storage backend | Auth | Data location |
|---|---|---|---|
| Desktop (packaged) | `sqlite` (injected by packaging wrapper) | mock (with startup banner) if JWT unset | Per-OS convention; defined by desktop per-target spec |
| Docker | `sqlite` (injected by reference compose env) | mock (with startup banner) if JWT unset | `/var/lib/cyoda-go` (volume mount target) |
| Helm | `postgres` | mock (with startup banner) if JWT unset | N/A (Postgres-backed) |

The Docker **image itself is neutral** — it does not bake `CYODA_STORAGE_BACKEND=sqlite` into its environment or CMD. A raw `docker run ghcr.io/cyoda-platform/cyoda-go` honors the binary's compiled-in defaults (memory). The sqlite default applies only when a user runs the **reference compose**, which sets the env var. This keeps the image consistent with the Runtime philosophy statement above.

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

### `CYODA_REQUIRE_JWT` — hard guard for production deployments

The mock-auth fallback is appropriate for evaluators on a desktop or a developer spinning up the reference compose. It is **actively dangerous for Helm deployments**, where a misconfigured production install could silently succeed without authentication regardless of how prominent the banner is.

To close that hole, the binary honors `CYODA_REQUIRE_JWT`:

- **`CYODA_REQUIRE_JWT=true`** — the binary refuses to start unless `CYODA_IAM_MODE=jwt` **and** the JWT signing key is present. No mock fallback, no banner, no startup in any degraded mode. Fatal error on missing/invalid config.
- **`CYODA_REQUIRE_JWT` unset or `false`** — existing behavior. Mock fallback with banner when `CYODA_IAM_MODE` is absent.

The canonical Helm chart's `values.yaml` sets `CYODA_REQUIRE_JWT=true` by default. Operators who genuinely want mock mode in a cluster (lab environments, CI) explicitly override it. Desktop and Docker provisioning layers leave it unset so the friendly fallback still applies to evaluators.

## Binary health and observability surface

The binary exposes endpoints on two listeners:

### API listener — `CYODA_HTTP_PORT` (default `8080`)

| Endpoint | Purpose |
|---|---|
| `/api/*` | Application traffic |
| `/api/health` | Existing overall health (retained for backwards compatibility) |

Plus the gRPC listener on `CYODA_GRPC_PORT` (default `9090`).

### Admin listener — `CYODA_ADMIN_BIND_ADDRESS` : `CYODA_ADMIN_PORT`

| Env var | Default | Purpose |
|---|---|---|
| `CYODA_ADMIN_BIND_ADDRESS` | `127.0.0.1` | Interface the admin listener binds to |
| `CYODA_ADMIN_PORT` | `9091` | Port the admin listener binds to |

| Endpoint | Purpose | Probe target |
|---|---|---|
| `/livez` | Liveness — process responsive, event loop alive | Kubernetes `livenessProbe`, Docker `HEALTHCHECK` |
| `/readyz` | Readiness — storage reachable, migrations applied, bootstrap complete | Kubernetes `readinessProbe`, load balancer health |
| `/metrics` | Prometheus pull endpoint | Prometheus scrape / `ServiceMonitor` |

The admin listener is **unauthenticated by design**. Defaulting `CYODA_ADMIN_BIND_ADDRESS` to `127.0.0.1` ensures a bare desktop binary or a raw `docker run ...` (no port mapping) never exposes readiness/metrics to the network. Targets that need the admin listener to be reachable from outside the process override this — and in every container case they must, because the container's `127.0.0.1` is in its own network namespace and is unreachable from host port mappings or from kubelet probes:

- **Helm chart** sets `CYODA_ADMIN_BIND_ADDRESS=0.0.0.0` inside the pod so kubelet probes and a ClusterIP-only admin `Service` can reach it. The pod network (bounded) and the ClusterIP-only `Service` are the exposure boundaries.
- **Canonical compose** likewise sets `CYODA_ADMIN_BIND_ADDRESS=0.0.0.0` inside the container and constrains the host-side port mapping to `127.0.0.1:9091:9091`. Docker's port mapping forwards to the container's network interface (not its loopback), so a container-internal `127.0.0.1` bind would leave the admin port unreachable even through the host mapping. The host-side `127.0.0.1:...` mapping is the network boundary.
- **Desktop binary** uses the `127.0.0.1` default — the admin surface is reachable only from the same host, no override needed.

OTLP push (existing) stays, orthogonal to `/metrics`. Pull and push are both supported; operators pick one or both.

**Probe discipline — strict separation.** `/livez` must never check storage connectivity, external dependencies, or anything that can transiently fail. A liveness probe that flaps on a dropped database connection triggers a Kubernetes `SIGTERM` and pod restart, which is an anti-pattern: transient network blips to Postgres should not restart serving pods. `/livez` answers only "is this process still alive and its event loop responsive?" and should stay cheap and deterministic. All storage-reachability, migration-state, and bootstrap-complete checks go under `/readyz`, whose failure removes the pod from load-balancer rotation but does not trigger a restart. The code implementation must honor this split; regression on it produces production-visible stability bugs.

Canonical compose and Helm chart are **minimal** — they do not bundle Grafana / Prometheus / Tempo. The current Grafana-bundled compose relocates to `examples/compose-with-observability/` as an explicit dev convenience.

## Port convention

One convention across all three canonical artifacts, for consistency:

| Purpose | Env var | Default |
|---|---|---|
| HTTP API | `CYODA_HTTP_PORT` | `8080` |
| gRPC | `CYODA_GRPC_PORT` | `9090` |
| Admin (health + metrics) port | `CYODA_ADMIN_PORT` | `9091` |
| Admin listener bind address | `CYODA_ADMIN_BIND_ADDRESS` | `127.0.0.1` |

The `8123`/`9123` ports currently used by the `local` profile and its helper scripts remain a **local-profile override only** — they do not leak into the canonical provisioning artifacts.

## Schema compatibility contract

Applies to **durable storage backends only** (sqlite, postgres, and any future durable plugin). The memory backend has no persistent schema and is not subject to this contract.

Shared across all durable-backend deployment topologies. On startup the binary reads the on-disk schema version and compares it to the version its embedded migrations target:

- **Schema version matches** — proceed.
- **Schema older than code**, `AUTO_MIGRATE=true` — migrate forward, then proceed.
- **Schema older than code**, `AUTO_MIGRATE=false` — fail fast with a clear error message directing the operator to either (a) set `AUTO_MIGRATE=true` and restart, or (b) run the out-of-band migration procedure defined by their provisioning target (for Helm: the pre-install / pre-upgrade `Job`).
- **Schema newer than code** — fail fast unconditionally, regardless of `AUTO_MIGRATE`. This is what makes rolling downgrades / stale-binary-after-schema-change safe: the new pod gracefully refuses to serve against a schema it doesn't understand, rather than corrupting data.

No polling, no waiting. Failing fast with a clear error is the entire contract. Each deployment target's own spec builds on this (Helm's pre-install `Job` disables auto-migrate on the main deployment and runs the migration out-of-band; desktop/Docker leave auto-migrate on).

A dedicated migration entrypoint (e.g., a `cyoda-go migrate` subcommand) is **not introduced by this spec**. The Helm per-target spec decides how its migration `Job` invokes the binary — most likely by running the existing binary with `AUTO_MIGRATE=true` and a future `CYODA_MIGRATE_ONLY=true`-style flag that exits after migration. That decision is deferred.

## Release and publishing model

**Decoupled versioning** — two independent tag streams. Both reset to `0.1.0`:

- App tags at the repo root: `v0.1.0` onward. The only existing root-level tag stream is pre-public; no consumers depend on it.
- Chart tags: `cyoda-go-0.1.0` onward. No prior chart has shipped.

**Plugin module tags** (`plugins/memory/v0.1.0`, `plugins/postgres/v0.1.0`, and future `plugins/sqlite/v0.1.0`) are a separate stream governed by Go module semantics and the SPI's own versioning policy — they are **not reset** and **not controlled by this spec**. See the Out-of-Scope section.

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
  bump-chart-appversion.yml      # NEW: opens PR bumping Chart.yaml appVersion on stable v* tags
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
- **API-listener bind-address symmetry.** `CYODA_HTTP_PORT` and `CYODA_GRPC_PORT` have no matching `_BIND_ADDRESS` env vars — the API and gRPC listeners effectively bind `0.0.0.0`. That's the right default (the API is meant to be reached), but it leaves no knob for sidecar topologies where the API should only be reachable via a service-mesh proxy on the loopback interface. Symmetrizing the listener config (`CYODA_HTTP_BIND_ADDRESS`, `CYODA_GRPC_BIND_ADDRESS`) is a future ticket, not required for provisioning.

## Downstream implementation plan

The implementation plan generated from this spec covers the shared-layer work:

1. Add `/livez`, `/readyz`, `/metrics` endpoints on a new admin listener separate from the API listener. Listener is governed by `CYODA_ADMIN_PORT` (default `9091`) and `CYODA_ADMIN_BIND_ADDRESS` (default `127.0.0.1` — safe for desktop/bare `docker run`) (TDD).
2. Add `CYODA_SUPPRESS_BANNER` and `CYODA_REQUIRE_JWT` env vars. Emit the mock-auth warning banner unconditionally in mock mode unless suppressed. When `CYODA_REQUIRE_JWT=true`, binary refuses to start if JWT config is missing — no mock fallback, no banner, fatal error (TDD).
3. Implement the schema-compatibility contract: fail fast on schema-newer-than-code, fail fast on schema-older + auto-migrate off (TDD).
4. Introduce the target repo layout: `deploy/`, `examples/`, `scripts/dev/`.
5. Execute the legacy cleanup moves and deletions (including sanitizing the relocated dev scripts).
6. Add `.github/workflows/release.yml` (GoReleaser + multi-arch image + keyless cosign + SBOM on `v*` tags; prereleases don't move `:latest`). The workflow must set `GOWORK=off` when building so the release is pinned to `go.mod`-declared plugin versions rather than the local `go.work` overlay. Each plugin module (`plugins/memory`, `plugins/postgres`, `plugins/sqlite`) must have a released tag that the root `go.mod` pins to *before* the first app `v0.1.0` tag — otherwise the release build can't resolve reproducible versions for its dependencies. Cutting those plugin module tags is a prerequisite captured as an explicit pre-step in the implementation plan.

   **Pre-flight check.** The release workflow runs `GOWORK=off go mod download` and `GOWORK=off go mod verify` before invoking GoReleaser. If any plugin dependency resolves to a pseudo-version, a `replace` directive, or a non-tagged SHA, the workflow fails fast with a clear error naming the offending module. This prevents an accidental root-repo tag from producing a release built against unreleased or stale plugin code.
7. Add `.github/workflows/release-chart.yml` using `helm/chart-releaser-action`, gated on the chart's existence.
8. Add `.github/workflows/bump-chart-appversion.yml` — triggered when a stable (non-prerelease) `v*` tag is pushed, this workflow opens a PR updating `deploy/helm/cyoda-go/Chart.yaml`'s `appVersion` to the new app version. Keeps the decoupled tag streams in sync without manual edits. A human reviews and merges the PR (which becomes the trigger for the next chart tag when desired).
9. Delete `.github/workflows/docker-publish.yml`.
10. Update `README.md`, `CONTRIBUTING.md`, `cmd/cyoda-go/main.go` `printHelp()` for new endpoints, new env vars (`CYODA_ADMIN_PORT`, `CYODA_ADMIN_BIND_ADDRESS`, `CYODA_SUPPRESS_BANNER`, `CYODA_REQUIRE_JWT`), new port convention, and new layout. Additionally, close existing SQLite documentation gaps now that sqlite is elevated to a default:
    - Add **sqlite** as a first-class row in the README "Storage backends" table alongside memory and postgres.
    - Add a **Configuration > SQLite** subsection in the README documenting `CYODA_SQLITE_PATH`, `CYODA_SQLITE_AUTO_MIGRATE`, `CYODA_SQLITE_BUSY_TIMEOUT`, `CYODA_SQLITE_CACHE_SIZE`, `CYODA_SQLITE_SEARCH_SCAN_LIMIT`.
    - Add a **`.env.sqlite.example`** alongside the existing `.env.local.example` / `.env.postgres.example` / `.env.jwt.example` / `.env.otel.example` so "copy the example for your backend" works for sqlite too.
11. Add README badges (five) in a final commit once the first release has produced artifacts.

The per-target specs (desktop, Docker, Helm) are separate brainstorming cycles that consume this spec.
