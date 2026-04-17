# Docker provisioning — design

**Status:** Accepted
**Date:** 2026-04-17
**Scope:** Per-target provisioning of cyoda for Docker users (primarily application developers embedding cyoda in their own compose stacks, secondarily quick-try users running a single container). Consumes the shared provisioning design at `docs/superpowers/specs/2026-04-16-provisioning-shared-design.md`.

## Motivation

The shared provisioning work ships a multi-arch cyoda image via GoReleaser but does not yet provide the three things Docker consumers actually need:

1. A canonical `Dockerfile` for the release pipeline (`release.yml` currently has a guard that aborts if `deploy/docker/Dockerfile` is missing).
2. A reference `compose.yaml` that documents the env vars, ports, and volume cyoda needs — so an application developer can crib the fragment into their own compose file.
3. A functional dev-iteration script for contributors modifying cyoda itself (the existing `scripts/dev/run-docker-dev.sh` is self-guarded non-functional until these artifacts exist).

This spec produces those three artifacts plus one small binary addition (a `cyoda health` subcommand for compose/k8s health probes).

## Audience mapping

Restated from the shared spec:

| Install path | Persona | Priority |
|---|---|---|
| `docker pull ghcr.io/cyoda-platform/cyoda:latest` | Quick-try users | Zero-config, memory default, run + hit API |
| `deploy/docker/compose.yaml` — reference | Application developers embedding cyoda | Crib env + ports + volume into their own compose |
| `scripts/dev/run-docker-dev.sh` | Contributors modifying cyoda | Fast iteration: local source → local image → running container |

Per the shared spec, the **image itself is neutral about configuration** — no storage-backend or other opinionated env vars baked in. The canonical compose supplies sqlite as the default; `docker run` against the bare image falls through to the binary's compiled-in memory default.

## 1. Dockerfile — `deploy/docker/Dockerfile`

```dockerfile
# syntax=docker/dockerfile:1

# Stage a pre-chowned data directory. Distroless/static has no shell,
# so we can't `RUN mkdir && chown` in the final image. This two-stage
# pattern stages an empty /data owned by 65532:65532 in a temporary
# image, then COPY-chowns it into the distroless image. The resulting
# /var/lib/cyoda is then present with correct ownership, which Docker
# propagates to an empty named volume mounted on top of it on first
# container start.
#
# busybox pinned to a specific version for release-pipeline
# reproducibility. Bump intentionally; the stage is discarded post-build
# so image size is irrelevant.
FROM busybox:1.37 AS stage
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static
ARG TARGETPLATFORM

# Binary path /cyoda is referenced by:
#   - deploy/docker/compose.yaml healthcheck ("/cyoda health")
#   - the ENTRYPOINT below
# Keep these in sync if the binary ever moves.
COPY $TARGETPLATFORM/cyoda /cyoda
COPY --from=stage --chown=65532:65532 /data /var/lib/cyoda

USER 65532:65532
EXPOSE 8080 9090 9091

ENTRYPOINT ["/cyoda"]
```

**Design decisions:**

- **Base:** `gcr.io/distroless/static` — ~2MB, CA bundle + tzdata included, no shell, no package manager. Runs our `CGO_ENABLED=0` static Go binary. Industry-standard for distroless Go tools (kubectl, helm, cert-manager, cosign).
- **Pre-built binary only.** The Dockerfile expects `$TARGETPLATFORM/cyoda` to be present in the build context, which is what GoReleaser's `dockers_v2` provides. Contributors iterating on cyoda use `scripts/dev/run-docker-dev.sh` (§4), which stages a locally-built binary into this context shape.
- **Non-root.** `USER 65532:65532` is distroless's built-in `nonroot` user. No filesystem writes inside the image; data goes to the mounted volume at `/var/lib/cyoda/`.
- **No CMD.** `ENTRYPOINT ["/cyoda"]` with no args means any trailing argument on `docker run IMAGE <args>` is passed as the subcommand: `docker run IMAGE health`, `docker run IMAGE init --force`, `docker run IMAGE --help`. No need to override entrypoint.
- **No `HEALTHCHECK`.** Rationale in §3. Compose users get healthcheck via the compose file; k8s users define probes in the Deployment spec. The distroless Go-tool convention is no Dockerfile `HEALTHCHECK`; orchestrators own health policy.

**Volume chown — why the two-stage `busybox` pattern:** the `nonroot` user needs write access to `/var/lib/cyoda/`. Docker's behavior when mounting a named volume:

- If the image's path **exists** with content, Docker copies that content into the empty volume on first mount, preserving ownership.
- If the image's path **doesn't exist**, Docker creates the mount point as `root:root` — and the `nonroot` process gets `EACCES`.

Distroless/static has no `/var/lib/cyoda/`. Without staging, the container starts as `nonroot` but the volume's mount point is `root:root`, and cyoda fails to open `cyoda.db`. The two-stage pattern above creates `/var/lib/cyoda/` in the image with `65532:65532` ownership, so the named volume inherits correct ownership on first mount.

This is also why Dockerfile `RUN mkdir && chown` doesn't work in distroless: no shell, no `mkdir` binary. A multi-stage `COPY --from=stage` is the idiomatic workaround.

## 2. Canonical compose — `deploy/docker/compose.yaml`

```yaml
services:
  cyoda:
    image: ${CYODA_IMAGE:-ghcr.io/cyoda-platform/cyoda:latest}
    ports:
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:9090:9090"
      - "127.0.0.1:9091:9091"
    environment:
      CYODA_STORAGE_BACKEND: sqlite
      CYODA_SQLITE_PATH: /var/lib/cyoda/cyoda.db
      CYODA_ADMIN_BIND_ADDRESS: 0.0.0.0
      # For production: uncomment BOTH lines below and set CYODA_JWT_SIGNING_KEY
      # in your shell before `docker compose up`.
      # Multi-line PEM: `export CYODA_JWT_SIGNING_KEY="$(cat key.pem)"`
      # CYODA_REQUIRE_JWT: "true"
      # CYODA_JWT_SIGNING_KEY: ${CYODA_JWT_SIGNING_KEY:?set CYODA_JWT_SIGNING_KEY before compose up}
    volumes:
      - cyoda-data:/var/lib/cyoda
    # Adjust start_period for slower environments (Postgres migrations,
    # cluster mode). Overriding here keeps health policy visible rather
    # than baking it into the Dockerfile.
    healthcheck:
      test: ["CMD", "/cyoda", "health"]
      interval: 10s
      timeout: 3s
      start_period: 30s
      retries: 3

volumes:
  cyoda-data:
```

**Design decisions:**

- **Single service, sqlite-only.** Matches the shared spec's Docker default (sqlite via compose env). Users who want Postgres + observability use `examples/compose-with-observability/compose.yaml`; `deploy/docker/README.md` links there.
- **Image override via `${CYODA_IMAGE:-...}`.** The dev script (§4) sets `CYODA_IMAGE` to a locally-tagged image so compose uses the just-built bits. Default is the published `:latest`.
- **All three ports bound host-side to `127.0.0.1:`.** Safe — the admin port (9091) is unauthenticated-by-design; loopback-only exposure aligns with the shared spec's admin-listener safety rule. Users editing this for a shared-host deployment would flip to `0.0.0.0:9091` explicitly, taking the responsibility on.
- **`CYODA_ADMIN_BIND_ADDRESS=0.0.0.0`** inside the container. Docker's port mapping forwards to the container's eth0, not its loopback, so the container-side bind must be `0.0.0.0`. The host-side `127.0.0.1:` mapping is the network boundary.
- **Named volume `cyoda-data`** at `/var/lib/cyoda/`. Persists across `compose down`/`up`. `compose down -v` wipes.
- **Healthcheck block here, not in Dockerfile.** All parameters visible to an operator reading the compose file. Changing `start_period` from 30s to 60s for cluster mode is a one-line edit; no image rebuild.

## 3. `cyoda health` subcommand

New file `cmd/cyoda/health.go`:

```go
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// runHealth implements 'cyoda health': GET /readyz on the admin listener,
// exit 0 on 200, 1 otherwise. Primary consumer is the compose healthcheck
// and the Helm chart's readinessProbe.
func runHealth(args []string) int {
	port := os.Getenv("CYODA_ADMIN_PORT")
	if port == "" {
		port = "9091"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%s/readyz", port)
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cyoda health: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "cyoda health: %s returned %d\n", url, resp.StatusCode)
		return 1
	}
	return 0
}
```

Wired into `cmd/cyoda/main.go`'s subcommand dispatch alongside `init`:

```go
case "init":
    os.Exit(runInit(os.Args[2:]))
case "health":
    os.Exit(runHealth(os.Args[2:]))
```

`printHelp()` gains a row:

```
  cyoda health            Probe /readyz on the admin listener (exits 0 if ready).
                          Used by Dockerfile/compose/k8s health probes.
```

**Why `/readyz`, not `/livez`:** the canonical compose is single-service, so `depends_on: service_healthy` doesn't apply here directly. But the rationale is `/readyz`'s semantic — "ready to accept requests" (storage reachable, migrations applied, bootstrap complete) — is what matters for user-assembled multi-service compose files where a downstream service gates on cyoda's readiness. `/livez` is "process alive, don't restart me"; it's the right probe for Kubernetes `livenessProbe` but not for `depends_on`. `cyoda health` optimizes for the cribbing use case.

**Tests** follow the `init_test.go` pattern: `httptest.NewServer` stands up a fake admin listener and the test exercises each outcome.

| Test | Server response | Expected exit code |
|---|---|---|
| `TestCyodaHealth_Ready` | 200 OK | 0 |
| `TestCyodaHealth_NotReady` | 503 Service Unavailable | 1 |
| `TestCyodaHealth_ConnectionRefused` | (server not started) | 1 |
| `TestCyodaHealth_Timeout` | handler sleeps 5s; client timeout is 2s | 1 |
| `TestCyodaHealth_RespectsAdminPort` | 200 on a non-default port; env var set | 0 |

The timeout test is load-bearing: a deadlocked readiness handler looks exactly like "server accepts connection then hangs forever" to the health client. The 2s client timeout is what keeps Docker's healthcheck from inheriting the deadlock; losing that test would let a regression in the timeout value go unnoticed.

**What this subcommand does NOT do:** it does not attempt to diagnose the degraded-runtime cases (storage went down after cyoda is running, cluster membership lost, etc.). Those require richer readiness semantics that `ReadinessCheck()` doesn't yet implement. See the shared spec's note on the future SPI-level `Ready(ctx)` method.

## 4. Dev script — `scripts/dev/run-docker-dev.sh`

Replaces the currently-guarded non-functional version. Stages a locally-built binary into the `dockers_v2` context shape and runs the canonical Dockerfile + compose against a local image tag.

```bash
#!/bin/bash
# Dev helper: builds cyoda from source, produces a local image, runs compose.
# Contributors use this to test local changes in a container before they land.
#
# Build flags here (-ldflags="-s -w") are intentionally minimal — version
# injection and release optimizations are owned by .goreleaser.yaml. The
# :dev image tag makes it visually obvious this isn't a release build.
set -eu

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$PROJECT_ROOT"

# Detect host platform in dockers_v2 context form (linux/amd64 or linux/arm64).
case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "run-docker-dev.sh: unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac
PLATFORM="linux/$ARCH"

# Stage binary under .buildctx/$PLATFORM/cyoda for the dockers_v2 context.
# NOTE: the root .dockerignore doesn't apply here — buildctx is the build
# context root for this invocation, not the repo root.
BUILDCTX=".buildctx"
trap 'rm -rf "$BUILDCTX"' EXIT
rm -rf "$BUILDCTX"
mkdir -p "$BUILDCTX/$PLATFORM"

echo "Building cyoda for $PLATFORM..."
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" \
    go build -ldflags="-s -w" -o "$BUILDCTX/$PLATFORM/cyoda" ./cmd/cyoda

LOCAL_TAG="ghcr.io/cyoda-platform/cyoda:dev"
echo "Building image $LOCAL_TAG..."
# BuildKit auto-injects TARGETPLATFORM from --platform, so no --build-arg needed
# for the Dockerfile's `COPY $TARGETPLATFORM/cyoda /cyoda` line.
docker buildx build --load \
    --platform "$PLATFORM" \
    -t "$LOCAL_TAG" \
    -f deploy/docker/Dockerfile \
    "$BUILDCTX"

echo "Running compose..."
CYODA_IMAGE="$LOCAL_TAG" docker compose -f deploy/docker/compose.yaml up "$@"
```

The `.buildctx` directory is gitignored (pattern `/.buildctx/` added to `.gitignore`). Cleanup is via `trap` so failures mid-run don't leave stale staging dirs.

**`CYODA_IMAGE` override is a dev convention, not a public contract.** This env var exists solely so `run-docker-dev.sh` can point the canonical compose at a local image tag without editing the compose file. Users cribbing the compose into their own stack should just set `image:` directly — we don't promise `CYODA_IMAGE` as stable across releases.

**Root `.dockerignore` is now a no-op for the release pipeline.** GoReleaser's `dockers_v2` uses the staged `$TARGETPLATFORM/` context (binary + nothing else), so the root `.dockerignore` affects only hypothetical `docker build .` from the repo root — which is no longer a supported path. Cleanup of the now-dead `.dockerignore` can happen in this PR or a follow-up; implementation plan picks.

**Build-flag drift vs GoReleaser config** is a known minor concern: the `go build -ldflags="-s -w"` line here won't track version-injection flags that `.goreleaser.yaml` uses for release builds. For dev images the drift is acceptable (no version injection needed to iterate locally). If a future Makefile consolidation creates a single-source-of-truth build target, this script is the natural first caller.

Flow:
1. Cross-compile (honestly, same-arch compile since we're targeting linux-host-arch) a static binary via `go build`.
2. Stage it at `.buildctx/linux/$ARCH/cyoda` — the layout `dockers_v2` expects.
3. Build the image with `docker buildx build --load` against the canonical `deploy/docker/Dockerfile`, passing the staged context.
4. Run `compose up` with `CYODA_IMAGE` overriding the published `:latest` to the locally-tagged `:dev`.

## 5. GoReleaser `dockers_v2` migration

Closes issue #52. The existing `.goreleaser.yaml` currently uses the deprecated `dockers:` + `docker_manifests:` block pair. Replace with a single `dockers_v2:` block pointing at `deploy/docker/Dockerfile`:

```yaml
dockers_v2:
  - id: cyoda
    images:
      - ghcr.io/cyoda-platform/cyoda
    tags:
      - "{{ .Version }}"
      - "{{ if not .Prerelease }}latest{{ end }}"
    platforms:
      - linux/amd64
      - linux/arm64
    dockerfile: deploy/docker/Dockerfile
    labels:
      org.opencontainers.image.source: https://github.com/cyoda-platform/cyoda-go
      org.opencontainers.image.version: "{{ .Version }}"
      org.opencontainers.image.revision: "{{ .Commit }}"
      org.opencontainers.image.created: "{{ .Date }}"
      org.opencontainers.image.licenses: Apache-2.0
```

**Semantic changes vs the old config:**

- Single buildx invocation per image → native multi-arch manifest (no separate `docker_manifests:` assembly needed).
- Artifacts under `$TARGETPLATFORM/` — which is exactly what the Dockerfile in §1 `COPY`s from.

**`:latest` suppression on prereleases — needs verification.** The old config used `skip_push: '{{ .Prerelease }}'` on the `:latest` manifest, which is a documented-safe pattern. The sample above uses `{{ if not .Prerelease }}latest{{ end }}` inside `tags:` — which, if GoReleaser emits the empty string as a literal tag, will fail at push time. Two fallback patterns to pick from during implementation, based on what `goreleaser release --snapshot` actually produces:

1. If empty-tag suppression works: keep the single-entry form above.
2. If not: split into two `dockers_v2` entries — one for `{{ .Version }}`, one for `latest` with an explicit `skip_push: "{{ .Prerelease }}"`.

Implementation step (§9) runs `goreleaser release --snapshot --skip=publish` against both patterns and pins the one that works.

**`docker_signs:` compatibility — also needs verification.** The existing config's `docker_signs:` block (keyless cosign on `artifacts: manifests`) is expected to continue working against `dockers_v2`-produced manifests, but the `dockers_v2` internal artifact pipeline differs from the old `dockers:` + `docker_manifests:` pair. The `artifacts: manifests` selector MAY resolve to a different set. Implementation step (§9) runs a snapshot with `cosign-installer` + `goreleaser release --snapshot --skip=publish` and confirms the signing step succeeds before the migration PR ships.

**The existing `release.yml` "Dockerfile must exist" guard** continues to catch the case where someone pushes a `v*` tag before this spec's PR merges; after merge, the Dockerfile exists and the guard passes.

## 6. `deploy/docker/README.md`

Extend the current placeholder to describe:

- The canonical compose (what it does, how to run).
- The env vars cyoda needs (for users cribbing into their own compose).
- A pointer to `examples/compose-with-observability/` for Postgres + Grafana.
- A pointer to `scripts/dev/run-docker-dev.sh` for contributors iterating on cyoda itself.
- **A pointer to the Helm chart** for Kubernetes / production use. Compose is for development and evaluation; production orchestration (HA, TLS, network policy, PDB) is the Helm chart's territory per the shared spec. Make this steering explicit so users don't default to "just put compose on a prod host."
- **Security posture note:** admin port unauthenticated-by-design; bound loopback-only by default; flip to `0.0.0.0:9091` only for network-reachable clusters and then add authentication upstream. Mock auth is the startup default; `CYODA_REQUIRE_JWT=true` is the production safety floor and users cribbing this compose into production must set it.

## 7. File changes summary

| File | Status |
|---|---|
| `deploy/docker/Dockerfile` | new |
| `deploy/docker/compose.yaml` | new |
| `deploy/docker/README.md` | extend existing placeholder |
| `cmd/cyoda/health.go` | new |
| `cmd/cyoda/health_test.go` | new |
| `cmd/cyoda/main.go` (subcommand dispatch + printHelp) | extend |
| `scripts/dev/run-docker-dev.sh` | rewrite (from guarded-non-functional to functional) |
| `.gitignore` | add `/.buildctx/` |
| `.goreleaser.yaml` | `dockers:` + `docker_manifests:` → `dockers_v2:` |

## 8. Out of scope for this spec

- **Debug image variant** (e.g., `cyoda:debug` with shell + curl). Useful for in-container debugging; orthogonal to the canonical image's "minimal + non-root" posture. Can be a later addition. If the Helm chart (future) documents `kubectl debug --image=...` workflows, it must supply the debug image reference itself — this spec ships only the production image.
- **CI docker-compose smoke-test job.** §9 step 8 is a manual smoke test for this PR. No automated CI runs `docker compose up` → hit `/api/health` → teardown on every push. Gap is a real regression risk; a follow-up issue should track adding this as a CI job alongside the shellcheck job that already runs on `scripts/install.sh`. Not blocking for this PR.
- **`docker build .` from a fresh clone without any staging.** Deliberate: the canonical Dockerfile is release-optimized (pre-built binary). Contributors wanting to iterate use `scripts/dev/run-docker-dev.sh`.
- **Compose `profiles:` for sqlite vs postgres.** Canonical compose is sqlite-only to match the shared spec. `examples/compose-with-observability/` already covers the postgres recipe.
- **Cosign-signed `install.sh`-style docker one-liner** (like `curl install.sh | sh` for Docker). `docker pull` already has its own trust chain (image registry + manifest signing via cosign, already in the release pipeline).
- **Secrets management via Docker secrets.** Compose ships dev-safe defaults; production secrets handling is the Helm chart's concern (per the shared spec).
- **Richer runtime readiness (storage-ping, cluster-membership checks).** Requires an SPI-level `StoreFactory.Ready(ctx)` method. Future enhancement; especially relevant for the cyoda-go-cassandra plugin (cluster-join latency) but out of scope here.
- **Makefile build-target consolidation.** Would give `scripts/dev/run-docker-dev.sh` a single source of truth for build flags. Deferred; flag drift between the dev script and GoReleaser is acceptable for dev-only builds.
- **`_FILE` env-var suffix convention for secrets.** Common Docker/Kubernetes pattern: `CYODA_JWT_SIGNING_KEY_FILE=/run/secrets/key.pem` instead of inlining a multi-line PEM into the environment. The binary currently reads `CYODA_JWT_SIGNING_KEY` directly. Adding `_FILE` support is a small enhancement that would make Docker Compose secrets and Kubernetes mounted Secrets clean to wire — worth a follow-up issue. Not blocking for this PR; users can still use `export CYODA_JWT_SIGNING_KEY="$(cat key.pem)"` today.

## 9. Downstream implementation plan

The implementation plan generated from this spec covers:

1. `cyoda health` subcommand: Go implementation, tests (including the timeout case), subcommand dispatch, `printHelp` update (TDD).
2. `deploy/docker/Dockerfile` creation (two-stage with the busybox chown pattern).
3. `deploy/docker/compose.yaml` creation with healthcheck block and commented `CYODA_REQUIRE_JWT` hint.
4. `deploy/docker/README.md` extension (security posture, Helm cross-link, observability example pointer).
5. `scripts/dev/run-docker-dev.sh` rewrite from guarded-non-functional to functional.
6. `.gitignore` update for `/.buildctx/`.
7. `.goreleaser.yaml` migration from `dockers:`/`docker_manifests:` to `dockers_v2:`. **Before committing the migration, run `goreleaser release --snapshot --skip=publish` against a staged `.buildctx/` to verify two behaviors specifically:**
   - The `:latest` conditional-tag pattern (empty-tag suppression vs two-entry + `skip_push`). Pick the pattern that actually produces a valid release on a non-prerelease tag AND a prerelease tag.
   - That `docker_signs:` (keyless cosign on `artifacts: manifests`) still binds to the `dockers_v2`-produced manifest set and successfully signs. If the signing step skips or fails because the artifact selector doesn't match, the `docker_signs:` config needs updating alongside.
8. Smoke-test the full flow: `scripts/dev/run-docker-dev.sh` → compose up → `curl http://127.0.0.1:8080/api/health` returns 200, `docker ps` shows healthy. Additionally assert volume ownership is 65532 by running `docker compose exec cyoda /cyoda health` (works) and spot-checking via a debug container: `docker run --rm -v <compose-volume>:/data alpine stat -c '%u' /data` returns `65532`. This catches the named-volume-permissions issue at its natural surface.
9. (Follow-up) Close issue #52 once the `dockers_v2` migration merges. Consider opening a new issue for CI docker-compose smoke testing (§8 out-of-scope).
10. (Follow-up) Decide whether to delete the now-dead root `.dockerignore` in this PR or a tidy-up follow-up.
