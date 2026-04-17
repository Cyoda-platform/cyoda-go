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
FROM gcr.io/distroless/static

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/cyoda /cyoda

USER 65532:65532
EXPOSE 8080 9090 9091

ENTRYPOINT ["/cyoda"]
```

**Design decisions:**

- **Base:** `gcr.io/distroless/static` — ~2MB, CA bundle + tzdata included, no shell, no package manager. Runs our `CGO_ENABLED=0` static Go binary. Industry-standard for distroless Go tools (kubectl, helm, cert-manager, cosign).
- **Pre-built binary only.** The Dockerfile expects `$TARGETPLATFORM/cyoda` to be present in the build context, which is what GoReleaser's `dockers_v2` provides. Contributors iterating on cyoda use `scripts/dev/run-docker-dev.sh` (§4), which stages a locally-built binary into this context shape.
- **Non-root.** `USER 65532:65532` is distroless's built-in `nonroot` user. No filesystem writes inside the image; data goes to the mounted volume at `/var/lib/cyoda/`.
- **No CMD.** `ENTRYPOINT ["/cyoda"]` with no args means users can `docker run ... cyoda health`, `cyoda init --force`, `cyoda --help` without overriding entrypoint.
- **No `HEALTHCHECK`.** Rationale in §3. Compose users get healthcheck via the compose file; k8s users define probes in the Deployment spec. The distroless Go-tool convention is no Dockerfile `HEALTHCHECK`; orchestrators own health policy.

**Volume chown:** the `nonroot` user needs write access to `/var/lib/cyoda/`. Docker's named volumes inherit permissions from the first writer by default. When compose creates the volume fresh, the `cyoda` binary writes to it as UID 65532, and Docker records the ownership. Subsequent `compose up` runs inherit correctly. No Dockerfile `RUN chown` needed, no init container needed.

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

**Why `/readyz`, not `/livez`:** Docker's `depends_on: service_healthy` semantic is "ready to accept requests" — which is exactly `/readyz`'s contract (storage reachable, migrations applied, bootstrap complete). `/livez` is "process alive, don't restart me"; it's useful for liveness probes but not what `compose up` gates on.

**Tests** follow the `init_test.go` pattern: `httptest.NewServer` stands up a fake admin listener and the test exercises each outcome.

| Test | Server response | Expected exit code |
|---|---|---|
| `TestCyodaHealth_Ready` | 200 OK | 0 |
| `TestCyodaHealth_NotReady` | 503 Service Unavailable | 1 |
| `TestCyodaHealth_ConnectionRefused` | (server not started) | 1 |
| `TestCyodaHealth_RespectsAdminPort` | 200 on a non-default port; env var set | 0 |

**What this subcommand does NOT do:** it does not attempt to diagnose the degraded-runtime cases (storage went down after cyoda is running, cluster membership lost, etc.). Those require richer readiness semantics that `ReadinessCheck()` doesn't yet implement. See the shared spec's note on the future SPI-level `Ready(ctx)` method.

## 4. Dev script — `scripts/dev/run-docker-dev.sh`

Replaces the currently-guarded non-functional version. Stages a locally-built binary into the `dockers_v2` context shape and runs the canonical Dockerfile + compose against a local image tag.

```bash
#!/bin/bash
# Dev helper: builds cyoda from source, produces a local image, runs compose.
# Contributors use this to test local changes in a container before they land.
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
BUILDCTX=".buildctx"
trap 'rm -rf "$BUILDCTX"' EXIT
rm -rf "$BUILDCTX"
mkdir -p "$BUILDCTX/$PLATFORM"

echo "Building cyoda for $PLATFORM..."
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" \
    go build -ldflags="-s -w" -o "$BUILDCTX/$PLATFORM/cyoda" ./cmd/cyoda

LOCAL_TAG="ghcr.io/cyoda-platform/cyoda:dev"
echo "Building image $LOCAL_TAG..."
docker buildx build --load \
    --platform "$PLATFORM" \
    --build-arg TARGETPLATFORM="$PLATFORM" \
    -t "$LOCAL_TAG" \
    -f deploy/docker/Dockerfile \
    "$BUILDCTX"

echo "Running compose..."
CYODA_IMAGE="$LOCAL_TAG" docker compose -f deploy/docker/compose.yaml up "$@"
```

The `.buildctx` directory is gitignored (pattern `/.buildctx/` added to `.gitignore`). Cleanup is via `trap` so failures mid-run don't leave stale staging dirs.

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
- `skip_push` on `:latest` for prereleases becomes inline: `{{ if not .Prerelease }}latest{{ end }}` produces the tag only when `Prerelease` is false, otherwise emits an empty string which `dockers_v2` skips.

**The `docker_signs:` block** (keyless cosign on manifests) stays — it uses the same manifest target and doesn't change.

**The existing `release.yml` "Dockerfile must exist" guard** continues to catch the case where someone pushes a `v*` tag before this spec's PR merges; after merge, the Dockerfile exists and the guard passes.

## 6. `deploy/docker/README.md`

Extend the current placeholder to describe:

- The canonical compose (what it does, how to run).
- The env vars cyoda needs (for users cribbing into their own compose).
- A pointer to `examples/compose-with-observability/` for Postgres + Grafana.
- A pointer to `scripts/dev/run-docker-dev.sh` for contributors iterating on cyoda itself.
- Security posture note: admin port unauthenticated-by-design; bound loopback-only by default; flip to `0.0.0.0:9091` only for network-reachable clusters and then add authentication upstream.

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

- **Debug image variant** (e.g., `cyoda:debug` with shell + curl). Useful for in-container debugging; orthogonal to the canonical image's "minimal + non-root" posture. Can be a later addition.
- **`docker build .` from a fresh clone without any staging.** Deliberate: the canonical Dockerfile is release-optimized (pre-built binary). Contributors wanting to iterate use `scripts/dev/run-docker-dev.sh`.
- **Compose `profiles:` for sqlite vs postgres.** Canonical compose is sqlite-only to match the shared spec. `examples/compose-with-observability/` already covers the postgres recipe.
- **Cosign-signed `install.sh`-style docker one-liner** (like `curl install.sh | sh` for Docker). `docker pull` already has its own trust chain (image registry + manifest signing via cosign, already in the release pipeline).
- **Secrets management via Docker secrets.** Compose ships dev-safe defaults; production secrets handling is the Helm chart's concern (per the shared spec).
- **Richer runtime readiness (storage-ping, cluster-membership checks).** Requires an SPI-level `StoreFactory.Ready(ctx)` method. Future enhancement; especially relevant for the cyoda-go-cassandra plugin (cluster-join latency) but out of scope here.

## 9. Downstream implementation plan

The implementation plan generated from this spec covers:

1. `cyoda health` subcommand: Go implementation, tests, subcommand dispatch, `printHelp` update (TDD).
2. `deploy/docker/Dockerfile` creation.
3. `deploy/docker/compose.yaml` creation with healthcheck block.
4. `deploy/docker/README.md` extension.
5. `scripts/dev/run-docker-dev.sh` rewrite from guarded-non-functional to functional.
6. `.gitignore` update for `/.buildctx/`.
7. `.goreleaser.yaml` migration from `dockers:`/`docker_manifests:` to `dockers_v2:`.
8. Smoke-test the full flow: `scripts/dev/run-docker-dev.sh` → compose up → `curl http://127.0.0.1:8080/api/health` returns 200, `docker ps` shows healthy.
9. (Optional) Follow up by closing issue #52 once the `dockers_v2` migration merges.
