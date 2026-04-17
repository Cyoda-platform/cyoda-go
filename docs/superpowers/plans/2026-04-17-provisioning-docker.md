# Docker provisioning — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the canonical Docker artifacts for cyoda — a dockers_v2-ready Dockerfile, a reference compose.yaml with sqlite persistence and compose-owned healthcheck, a functional dev-iteration script, plus a `cyoda health` subcommand the compose healthcheck invokes.

**Architecture:** Six tasks. Task 1 is binary-level (new `cyoda health` subcommand with TDD — probes `/readyz` via the admin listener, exits 0/1). Tasks 2–4 are the Docker artifact trio (Dockerfile with a busybox-stage chown workaround for distroless's missing `/var/lib/cyoda`, single-service compose with sqlite + compose-level healthcheck, README extension). Task 5 rewrites the currently-guarded-non-functional `scripts/dev/run-docker-dev.sh` to stage a locally-built binary into the canonical Dockerfile's context and run compose with a local image tag. Task 6 migrates `.goreleaser.yaml` from `dockers:` + `docker_manifests:` to `dockers_v2:`, with two empirical-verification substeps for the `:latest` conditional-tag shape and `docker_signs:` compatibility before committing.

**Tech Stack:** Go 1.26+ with `log/slog`, stdlib `net/http` + `httptest` for test servers, `distroless/static` + `busybox:1.37` base images, Docker Compose v2 healthchecks, GoReleaser v2 `dockers_v2:`, cosign keyless via GitHub Actions OIDC.

**Reference spec:** `docs/superpowers/specs/2026-04-17-provisioning-docker-design.md`

---

## File structure

| File | Status | Responsibility |
|---|---|---|
| `cmd/cyoda/health.go` | new | `runHealth` — GET /readyz, exit 0/1 |
| `cmd/cyoda/health_test.go` | new | 5 test cases (ready, not-ready, refused, timeout, port override) |
| `cmd/cyoda/main.go` | modify | Subcommand dispatch gains `health`; printHelp gains row |
| `deploy/docker/Dockerfile` | new | Two-stage (busybox chown + distroless/static), no HEALTHCHECK |
| `deploy/docker/compose.yaml` | new | Single-service sqlite + named volume + compose-level healthcheck |
| `deploy/docker/README.md` | extend | Security posture, Helm cross-link, observability example pointer |
| `scripts/dev/run-docker-dev.sh` | rewrite | Stage binary + buildx build + compose up with local tag |
| `.gitignore` | modify | Add `/.buildctx/` |
| `.goreleaser.yaml` | modify | `dockers:` + `docker_manifests:` → `dockers_v2:`, verified via snapshot |

---

## Task 1: `cyoda health` subcommand

New subcommand that probes `/readyz` on the admin listener. Primary consumer is the Dockerfile-or-compose healthcheck (Task 2/3); the Helm chart's readinessProbe will use it later. TDD with all five test cases from the spec.

**Files:**
- Create: `cmd/cyoda/health.go`
- Create: `cmd/cyoda/health_test.go`
- Modify: `cmd/cyoda/main.go` (subcommand dispatch + printHelp)

- [ ] **Step 1: Write the failing tests**

Create `cmd/cyoda/health_test.go`:

```go
package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// portFromURL extracts the port segment from a server URL like "http://127.0.0.1:12345".
func portFromURL(t *testing.T, u string) string {
	t.Helper()
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestCyodaHealth_Ready(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Errorf("expected /readyz, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("CYODA_ADMIN_PORT", portFromURL(t, server.URL))

	if code := runHealth(nil); code != 0 {
		t.Fatalf("expected exit 0 (ready); got %d", code)
	}
}

func TestCyodaHealth_NotReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "storage unreachable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	t.Setenv("CYODA_ADMIN_PORT", portFromURL(t, server.URL))

	if code := runHealth(nil); code != 1 {
		t.Fatalf("expected exit 1 (503 → not ready); got %d", code)
	}
}

func TestCyodaHealth_ConnectionRefused(t *testing.T) {
	// Bind a server, capture its port, then close immediately. Subsequent
	// connections to that port get refused (no listener).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	port := portFromURL(t, server.URL)
	server.Close()

	t.Setenv("CYODA_ADMIN_PORT", port)

	if code := runHealth(nil); code != 1 {
		t.Fatalf("expected exit 1 (connection refused → not ready); got %d", code)
	}
}

// TestCyodaHealth_Timeout verifies the client-side 2s timeout fires when the
// server accepts the connection but never writes a response. Without this
// test a regression in the timeout value (raised too high, or removed
// altogether) would let Docker's HEALTHCHECK inherit a deadlock.
func TestCyodaHealth_Timeout(t *testing.T) {
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Hold the connection open beyond the client's 2s timeout but respect
		// test shutdown so the goroutine doesn't leak.
		select {
		case <-time.After(10 * time.Second):
		case <-done:
		}
	}))
	defer server.Close()

	t.Setenv("CYODA_ADMIN_PORT", portFromURL(t, server.URL))

	start := time.Now()
	code := runHealth(nil)
	elapsed := time.Since(start)

	if code != 1 {
		t.Fatalf("expected exit 1 (timeout → not ready); got %d", code)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("runHealth should time out within ~2s; took %v", elapsed)
	}
}

func TestCyodaHealth_RespectsAdminPort(t *testing.T) {
	// httptest picks a random non-default port; setting CYODA_ADMIN_PORT to
	// it is the only way this test can pass, so success = port respected.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	port := portFromURL(t, server.URL)
	if port == "9091" {
		t.Skip("httptest picked the default port; rerun")
	}
	t.Setenv("CYODA_ADMIN_PORT", port)

	if code := runHealth(nil); code != 0 {
		t.Fatalf("expected exit 0 when CYODA_ADMIN_PORT points at real server; got %d", code)
	}
}

// Ensure linter doesn't drop the errors import even if not referenced above.
var _ = errors.Is
```

- [ ] **Step 2: Run tests to verify failure**

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/feat-provisioning-docker-layer
go test ./cmd/cyoda/ -run TestCyodaHealth -v
```
Expected: all FAIL — `runHealth` is undefined.

- [ ] **Step 3: Implement `runHealth`**

Create `cmd/cyoda/health.go`:

```go
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// runHealth implements 'cyoda health': GET /readyz on the admin listener,
// exit 0 on 200, 1 otherwise. Primary consumer is the compose-level
// healthcheck; the Helm chart's readinessProbe invokes the same subcommand.
//
// The 2-second client timeout is load-bearing. A deadlocked readiness
// handler looks exactly like "server accepts connection then hangs" to
// this client; without the timeout, Docker's HEALTHCHECK inherits the
// deadlock and never marks the container unhealthy.
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

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./cmd/cyoda/ -run TestCyodaHealth -v
```
Expected: all 5 tests PASS. Timeout test should complete in under 4s.

- [ ] **Step 5: Wire subcommand dispatch in `cmd/cyoda/main.go`**

Find the existing dispatch block in `main()`:

```go
if len(os.Args) > 1 {
    switch os.Args[1] {
    case "--help", "-h":
        printHelp()
        return
    case "init":
        os.Exit(runInit(os.Args[2:]))
    }
}
```

Add a `health` case:

```go
if len(os.Args) > 1 {
    switch os.Args[1] {
    case "--help", "-h":
        printHelp()
        return
    case "init":
        os.Exit(runInit(os.Args[2:]))
    case "health":
        os.Exit(runHealth(os.Args[2:]))
    }
}
```

- [ ] **Step 6: Update `printHelp()`**

In the same file, find the existing `printHelp` USAGE block that lists subcommands. It contains lines like:

```
  cyoda [flags]           Run the server with current config.
  cyoda init [--force]    Write a starter user config enabling sqlite.
  cyoda --help            Show this help.
```

Add a line for `health` between `init` and `--help`:

```
  cyoda [flags]           Run the server with current config.
  cyoda init [--force]    Write a starter user config enabling sqlite.
  cyoda health            Probe /readyz on the admin listener (exits 0 if ready).
  cyoda --help            Show this help.
```

- [ ] **Step 7: Full test sweep**

```bash
go build ./...
go vet ./...
go test -short ./...
go test ./cmd/cyoda/ -v
```
All must be green. `cyoda health` tests should show 5 PASS.

- [ ] **Step 8: Smoke-test the subcommand manually**

```bash
# Case 1: no admin listener running — expect "connection refused" and exit 1.
go run ./cmd/cyoda health; echo "exit: $?"
# Expected: "cyoda health: Get \"http://127.0.0.1:9091/readyz\": dial tcp 127.0.0.1:9091: connect: connection refused"
#           exit: 1

# Case 2: start cyoda in one terminal, run health in another.
# Terminal 1:
# XDG_DATA_HOME=/tmp/cyoda-smoke go run ./cmd/cyoda
# Terminal 2:
# go run ./cmd/cyoda health; echo "exit: $?"
# Expected: exit 0, nothing printed to stderr.
```

Skip case 2 if you can only run one terminal; the tests cover it.

- [ ] **Step 9: Commit**

```bash
git add cmd/cyoda/health.go cmd/cyoda/health_test.go cmd/cyoda/main.go
git commit -m "$(cat <<'EOF'
feat(cyoda): add 'cyoda health' subcommand for compose/k8s probes

Probes /readyz on the admin listener (http://127.0.0.1:${CYODA_ADMIN_PORT:-9091}/readyz),
exits 0 on 200, 1 on anything else (non-200 response, connection
refused, timeout, or any error). 2s client timeout is load-bearing
and tested explicitly — a deadlocked readiness handler looks exactly
like a hung socket to the client, so without the timeout Docker's
HEALTHCHECK would inherit the deadlock.

Why /readyz, not /livez: Docker 'depends_on: service_healthy' and
Kubernetes readinessProbe both gate on "ready to accept requests"
which is /readyz's contract. /livez is for "don't restart me"
(k8s livenessProbe) — different question.

Subcommand dispatch in main.go extended to route 'health' to runHealth.
printHelp USAGE block adds a row.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `deploy/docker/Dockerfile`

The canonical image. Two-stage: busybox stage pre-chowns a `/data` directory, distroless/static copies the binary and the chowned data dir. No `HEALTHCHECK` line (compose owns healthcheck policy).

**Files:**
- Create: `deploy/docker/Dockerfile`

No TDD — Dockerfile verification happens via build + run.

- [ ] **Step 1: Create the Dockerfile**

Create `deploy/docker/Dockerfile`:

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

- [ ] **Step 2: Build smoke-test — stage a fake binary, run the Dockerfile**

Create a tiny placeholder binary to exercise the `COPY $TARGETPLATFORM/cyoda /cyoda` path without dragging the full Go build into this step. Actual binary build is Task 5's job.

```bash
# From the worktree root:
tmp=$(mktemp -d)
mkdir -p "$tmp/linux/amd64"
cat > "$tmp/linux/amd64/cyoda" <<'EOF'
#!/bin/sh
echo "placeholder"
EOF
chmod +x "$tmp/linux/amd64/cyoda"

docker buildx build \
    --platform linux/amd64 \
    --load \
    -t cyoda-dockerfile-smoke:latest \
    -f deploy/docker/Dockerfile \
    "$tmp" 2>&1 | tail -15

rm -rf "$tmp"
```
Expected: image builds successfully. If it fails, inspect the error — most likely candidates are busybox:1.37 registry availability or the `COPY $TARGETPLATFORM/...` syntax.

- [ ] **Step 3: Run-time smoke test — verify USER and volume ownership**

Run the image, verify it runs as UID 65532 and that `/var/lib/cyoda` is owned by 65532:

```bash
docker run --rm --entrypoint /bin/sh cyoda-dockerfile-smoke:latest -c 'id; ls -ld /var/lib/cyoda' 2>&1 || true
```
This will fail — distroless/static has no `/bin/sh`. Use a sidecar inspection instead:

```bash
# Export the filesystem and inspect /var/lib/cyoda ownership:
docker create --name cyoda-inspect cyoda-dockerfile-smoke:latest
docker export cyoda-inspect | tar -tvf - var/lib/cyoda 2>&1 | head -3
docker rm cyoda-inspect
```
Expected output contains `drwxr-xr-x 65532/65532` for `var/lib/cyoda/`. Cleanup: `docker rmi cyoda-dockerfile-smoke:latest`.

- [ ] **Step 4: Commit**

```bash
git add deploy/docker/Dockerfile
git commit -m "$(cat <<'EOF'
feat(docker): canonical Dockerfile (distroless/static, two-stage chown)

Multi-stage build: busybox:1.37 stage pre-chowns an empty /data
directory to 65532:65532, which is then COPY-chowned into the
distroless/static final image as /var/lib/cyoda. This fixes the
EACCES that nonroot would get writing to a Docker-created mount
point on a path that doesn't exist in the image — distroless has
no /var/lib/cyoda — by ensuring Docker's "copy ownership from
image" behavior kicks in on first volume mount.

Image is config-neutral: no baked env vars. The canonical compose
supplies sqlite defaults; 'docker run' against the bare image falls
through to the binary's compiled-in memory default.

No HEALTHCHECK line — compose owns healthcheck policy (interval,
timeout, start_period visible to operators). k8s uses the Deployment
spec's probes regardless of the Dockerfile.

Binary path /cyoda is a coupling point between ENTRYPOINT and the
compose-level healthcheck (CMD ["/cyoda", "health"]); comment in
the Dockerfile names both consumers.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `deploy/docker/compose.yaml`

Single-service sqlite compose. Compose-level healthcheck uses `cyoda health`. Named volume for persistence. Commented production-hardening hint for JWT.

**Files:**
- Create: `deploy/docker/compose.yaml`

- [ ] **Step 1: Create the compose file**

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
    # Adjust start_period for slower environments (Postgres migrations, cluster
    # mode). All parameters visible here rather than baked into the Dockerfile.
    healthcheck:
      test: ["CMD", "/cyoda", "health"]
      interval: 10s
      timeout: 3s
      start_period: 30s
      retries: 3

volumes:
  cyoda-data:
```

- [ ] **Step 2: Validate YAML parses**

```bash
python3 -c "import yaml; yaml.safe_load(open('deploy/docker/compose.yaml'))"
```
Expected: silent success.

- [ ] **Step 3: Validate compose config**

```bash
docker compose -f deploy/docker/compose.yaml config --quiet
```
Expected: silent success, exit 0.

- [ ] **Step 4: Commit**

```bash
git add deploy/docker/compose.yaml
git commit -m "$(cat <<'EOF'
feat(docker): canonical compose.yaml (sqlite + compose-level healthcheck)

Single-service reference compose. Sqlite on a named volume for
persistence; CYODA_ADMIN_BIND_ADDRESS=0.0.0.0 inside the container
because Docker port mapping forwards to eth0 not loopback; all three
ports bound host-side to 127.0.0.1 (admin is unauthenticated-by-design).

Image is ${CYODA_IMAGE:-ghcr.io/cyoda-platform/cyoda:latest} so
scripts/dev/run-docker-dev.sh can override to a locally-tagged build
without editing this file. CYODA_IMAGE is a dev-script convention,
not a public contract; users cribbing this fragment into their own
compose should set image: directly.

Healthcheck lives here (not in the Dockerfile) so operators can see
and tune interval/timeout/start_period without an image rebuild.
Invokes 'cyoda health' (added in the previous commit), which probes
/readyz with a 2s client timeout.

Commented CYODA_REQUIRE_JWT + CYODA_JWT_SIGNING_KEY block documents
the production safety floor users cribbing for real deployments must
opt into — matches the shared provisioning spec's approach.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `deploy/docker/README.md` — extend placeholder

Extend the existing one-sentence placeholder into real docs: what compose does, security posture, pointers to Helm and the observability example.

**Files:**
- Modify: `deploy/docker/README.md`

- [ ] **Step 1: Read the existing placeholder first**

```bash
cat deploy/docker/README.md
```
Review what's currently there — typically a one-liner pointing back at the shared spec.

- [ ] **Step 2: Rewrite the README**

Replace the entire file with:

```markdown
# Canonical Docker provisioning for cyoda

This directory is the canonical Docker source:

- `Dockerfile` — the source of `ghcr.io/cyoda-platform/cyoda:<version>`
  and `:latest`, published by GoReleaser on every non-prerelease tag.
- `compose.yaml` — a reference single-service compose file that runs the
  published image with sqlite persistence.

## Quick start

```bash
cd deploy/docker
docker compose up
```

Then:

```bash
curl http://127.0.0.1:8080/api/health    # API on port 8080
curl http://127.0.0.1:9091/livez         # admin liveness
curl http://127.0.0.1:9091/metrics       # Prometheus scrape
```

Data persists in the named volume `cyoda-data`. `docker compose down` stops
cyoda; `docker compose down -v` wipes the data.

## What this compose demonstrates

The primary use case for this file is **reference documentation**: an
application developer embedding cyoda in their own compose stack reads
this file to learn what env vars, ports, and volume mounts cyoda needs,
then cribs the fragment into their own `docker-compose.yml`.

Key elements to copy:

- `CYODA_STORAGE_BACKEND: sqlite` + `CYODA_SQLITE_PATH: /var/lib/cyoda/cyoda.db` — the default storage path.
- `CYODA_ADMIN_BIND_ADDRESS: 0.0.0.0` — required inside the container because Docker port mapping forwards to the container's eth0, not its loopback.
- Named volume mount at `/var/lib/cyoda`.
- Compose-level `healthcheck` invoking `cyoda health`.

## Security posture

- **Admin port (9091) is unauthenticated by design.** It exposes
  `/livez`, `/readyz`, `/metrics`. The compose binds it to
  `127.0.0.1:9091` — loopback-only — which is safe for dev and single-
  host deployments. Flip to `0.0.0.0:9091` ONLY if your deployment has
  authentication upstream of cyoda (ingress, sidecar, service mesh).
- **Mock auth is the startup default.** `CYODA_IAM_MODE=mock` accepts
  all requests. For production, set `CYODA_REQUIRE_JWT=true` AND provide
  `CYODA_JWT_SIGNING_KEY` (multi-line PEM: `export CYODA_JWT_SIGNING_KEY="$(cat key.pem)"`
  before `docker compose up`). A startup banner warns when running in
  mock mode; `CYODA_SUPPRESS_BANNER=true` silences it (CI only — not
  production).

## For Kubernetes / production

**Use the Helm chart, not this compose.** This compose is for local
development and evaluation. Production orchestration (HA, TLS, network
policy, PodDisruptionBudget, migrations as a Job, service mesh
integration) is the Helm chart's territory per
`docs/superpowers/specs/2026-04-16-provisioning-shared-design.md`.

## For Postgres and observability

`examples/compose-with-observability/compose.yaml` runs cyoda + Postgres
+ Grafana/Prometheus/Tempo. Use that file if you want to experiment
against a Postgres backend or view cyoda's metrics and traces locally.

## For contributors iterating on cyoda itself

`scripts/dev/run-docker-dev.sh` builds a local cyoda binary from source,
packages it into the canonical Dockerfile with a `:dev` tag, and runs
this compose against that local image.
```

- [ ] **Step 3: Verify internal links resolve**

```bash
ls docs/superpowers/specs/2026-04-16-provisioning-shared-design.md
ls examples/compose-with-observability/compose.yaml
ls scripts/dev/run-docker-dev.sh
```
All three must exist. If any is missing, adjust the README text (the last one comes online in Task 5 — acceptable to reference it now).

- [ ] **Step 4: Commit**

```bash
git add deploy/docker/README.md
git commit -m "$(cat <<'EOF'
docs(docker): extend README with security posture and cross-refs

Replaces the placeholder with a real readme: quick-start, security
posture (admin port loopback-only; mock auth + CYODA_REQUIRE_JWT
production safety floor), cross-links to the Helm chart for
production and to examples/compose-with-observability/ for postgres
plus observability, and a pointer to scripts/dev/run-docker-dev.sh
for contributors.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `scripts/dev/run-docker-dev.sh` + `.gitignore`

Rewrite the currently-guarded-non-functional script to actually run. Stages a locally-built Go binary into the `.buildctx/$PLATFORM/cyoda` shape `dockers_v2` expects, builds the image with a `:dev` tag, runs compose against that tag.

**Files:**
- Rewrite: `scripts/dev/run-docker-dev.sh`
- Modify: `.gitignore` (add `/.buildctx/`)

- [ ] **Step 1: Add `.buildctx/` to `.gitignore`**

Append to `.gitignore`:

```
# Docker build context staged by scripts/dev/run-docker-dev.sh
/.buildctx/
```

- [ ] **Step 2: Verify `.buildctx/` is now ignored**

```bash
mkdir -p .buildctx/test
git check-ignore -v .buildctx/test 2>&1
rm -rf .buildctx
```
Expected: output lists the `.gitignore` rule and exit 0.

- [ ] **Step 3: Rewrite `scripts/dev/run-docker-dev.sh`**

Replace the entire file with:

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
# BuildKit auto-injects TARGETPLATFORM from --platform, so no --build-arg
# needed for the Dockerfile's `COPY $TARGETPLATFORM/cyoda /cyoda` line.
docker buildx build --load \
    --platform "$PLATFORM" \
    -t "$LOCAL_TAG" \
    -f deploy/docker/Dockerfile \
    "$BUILDCTX"

echo "Running compose..."
CYODA_IMAGE="$LOCAL_TAG" docker compose -f deploy/docker/compose.yaml up "$@"
```

Ensure it's executable:

```bash
chmod +x scripts/dev/run-docker-dev.sh
```

- [ ] **Step 4: Shellcheck**

```bash
shellcheck scripts/dev/run-docker-dev.sh
```
Expected: clean. If shellcheck isn't installed locally, CI will catch issues (the existing `shellcheck` workflow added in PR #45 covers `scripts/install.sh` — extend it if it doesn't already cover all `scripts/**/*.sh`).

Check the existing CI:

```bash
grep -A5 'shellcheck' .github/workflows/ci.yml
```

If the CI job only lints `scripts/install.sh`, extend it to cover `scripts/dev/run-docker-dev.sh` too:

```yaml
      - name: Run shellcheck
        run: shellcheck scripts/install.sh scripts/dev/run-docker-dev.sh
```

- [ ] **Step 5: Smoke-test the full flow**

This requires Docker running. If unavailable, skip and note — Task 6's GoReleaser verification covers the binary+image path via a different route.

```bash
./scripts/dev/run-docker-dev.sh -d
sleep 45   # give cyoda time to finish startup
docker ps --filter name=cyoda --format 'table {{.Names}}\t{{.Status}}'
# Expected: shows "Up ... (healthy)" after the start_period elapses

curl -s http://127.0.0.1:9091/readyz
# Expected: "ready"

curl -s http://127.0.0.1:9091/livez
# Expected: "ok"

docker compose -f deploy/docker/compose.yaml down
```

**Volume ownership assertion** (catches the named-volume-permissions regression from PR #45 review):

```bash
./scripts/dev/run-docker-dev.sh -d
sleep 10
# Inspect the named volume's /var/lib/cyoda ownership via a sidecar container:
docker run --rm -v deploy_docker_cyoda-data:/data alpine stat -c '%u:%g' /data
# Expected: 65532:65532
docker compose -f deploy/docker/compose.yaml down -v
```
(The volume name is prefix-derived from compose project name — may appear as `deploy_docker_cyoda-data` or `docker_cyoda-data` depending on compose version. `docker volume ls | grep cyoda` finds it.)

- [ ] **Step 6: Commit**

```bash
git add .gitignore scripts/dev/run-docker-dev.sh .github/workflows/ci.yml
git commit -m "$(cat <<'EOF'
feat(dev): functional run-docker-dev.sh (from guarded-non-functional)

Replaces the previous guard-only stub with the real dev flow:
  1. Cross-compile cyoda (statically, CGO_ENABLED=0) for the host's
     Linux arch into .buildctx/linux/$ARCH/cyoda.
  2. docker buildx build the canonical Dockerfile against that staged
     context, tagging ghcr.io/cyoda-platform/cyoda:dev.
  3. CYODA_IMAGE=<dev tag> docker compose up against the canonical
     compose file.

BuildKit auto-injects TARGETPLATFORM from --platform, so no --build-arg
needed to satisfy the Dockerfile's COPY $TARGETPLATFORM/cyoda line.

.gitignore gains /.buildctx/ so the staging dir stays untracked.

Root .dockerignore is now a no-op for both the release pipeline and
this dev script (both use .buildctx as the build context root).
Cleanup of the dead .dockerignore is deferred to a follow-up.

shellcheck CI job extended to cover scripts/dev/run-docker-dev.sh in
addition to scripts/install.sh.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: GoReleaser `dockers_v2` migration

Replace the deprecated `dockers:` + `docker_manifests:` pair with a single `dockers_v2:` block. Empirically verify two behaviors before committing: the `:latest` conditional-tag pattern and `docker_signs:` artifact-selector compatibility.

**Files:**
- Modify: `.goreleaser.yaml`

This task has substantial exploratory steps because GoReleaser's behavior under `dockers_v2` for the conditional `:latest` tag and for cosign signing needs empirical verification. Do not commit until BOTH verifications pass.

- [ ] **Step 1: Inspect the current `dockers:` and `docker_manifests:` blocks**

```bash
grep -nA5 'dockers:\|docker_manifests:\|docker_signs:' .goreleaser.yaml
```
Understand the current shape. It should look roughly like:

```yaml
dockers:
  - image_templates:
      - "ghcr.io/cyoda-platform/cyoda:{{ .Version }}-amd64"
    use: buildx
    goarch: amd64
    dockerfile: deploy/docker/Dockerfile
    build_flag_templates:
      - "--platform=linux/amd64"
      - "--label=org.opencontainers.image.source=..."
    ...
  - image_templates:
      - "ghcr.io/cyoda-platform/cyoda:{{ .Version }}-arm64"
    ...

docker_manifests:
  - name_template: "ghcr.io/cyoda-platform/cyoda:{{ .Version }}"
    image_templates:
      - "ghcr.io/cyoda-platform/cyoda:{{ .Version }}-amd64"
      - "ghcr.io/cyoda-platform/cyoda:{{ .Version }}-arm64"
  - name_template: "ghcr.io/cyoda-platform/cyoda:latest"
    skip_push: '{{ .Prerelease }}'
    image_templates:
      - ...
```

- [ ] **Step 2: Draft the `dockers_v2:` block (pattern A — single entry with conditional tag)**

Replace the `dockers:` + `docker_manifests:` blocks with:

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

Keep the existing `docker_signs:` block unchanged (just after the new `dockers_v2:` block):

```yaml
docker_signs:
  - artifacts: manifests
    cmd: cosign
    args:
      - "sign"
      - "--yes"
      - "${artifact}"
```

- [ ] **Step 3: Dry-run against a non-prerelease tag via snapshot**

Prepare a temp clone (the current worktree's git state may not cooperate for goreleaser — see `docs/MAINTAINING.md` snapshot-gotcha section):

```bash
TMPDIR_SNAP=$(mktemp -d)
git clone --local --branch feat/provisioning-docker-layer "$(git rev-parse --show-toplevel | sed 's|\.worktrees/.*||')" "$TMPDIR_SNAP/cyoda-go"
cd "$TMPDIR_SNAP/cyoda-go"
git remote set-url origin https://github.com/cyoda-platform/cyoda-go.git
git remote set-url --push origin NO_PUSH
git tag v0.0.0-snapshot-test
```

Run GoReleaser snapshot mode (no publish, no sign — we verify the build+manifest shape first, then signing separately in Step 4):

```bash
export DOCKER_CONFIG=$(mktemp -d)
docker run --rm --memory=6g \
    -v "$(pwd)":/src \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -w /src \
    goreleaser/goreleaser:latest \
    release --snapshot --clean --skip=publish --skip=sign --parallelism=2 2>&1 | tee snapshot.log | tail -40
```

**Check the output for:**

- `building ... target=linux_amd64` and `linux_arm64` — both must appear.
- A manifest tag emitted for `{{ .Version }}` and another for `latest`. In snapshot mode with a non-prerelease tag (`v0.0.0-snapshot-test` is NOT prerelease despite the suffix... wait, it IS prerelease per semver because the hyphen-suffix pattern. Use a different tag to test the non-prerelease path):

```bash
git tag -d v0.0.0-snapshot-test
git tag v0.0.1
docker run --rm --memory=6g -v "$(pwd)":/src -v /var/run/docker.sock:/var/run/docker.sock -w /src \
    goreleaser/goreleaser:latest \
    release --snapshot --clean --skip=publish --skip=sign --parallelism=2 2>&1 | tail -30
```

Expected: both `ghcr.io/cyoda-platform/cyoda:0.0.1` and `ghcr.io/cyoda-platform/cyoda:latest` manifests appear. `docker images | grep cyoda` shows both tags.

**Now the prerelease path:**

```bash
git tag -d v0.0.1
git tag v0.0.1-rc.1
docker run --rm --memory=6g -v "$(pwd)":/src -v /var/run/docker.sock:/var/run/docker.sock -w /src \
    goreleaser/goreleaser:latest \
    release --snapshot --clean --skip=publish --skip=sign --parallelism=2 2>&1 | tail -30
```

Expected behavior (decision tree):

- **If `:latest` is NOT emitted** (empty-tag suppression worked): Pattern A is good. Continue to Step 4.
- **If `:latest` IS emitted for the prerelease tag** OR the build fails with "empty tag": Pattern A does not work. Switch to Pattern B (Step 3B below).

- [ ] **Step 3B (only if Step 3 showed Pattern A fails): Switch to Pattern B (two entries + explicit skip_push)**

Rewrite the `dockers_v2:` block as two entries:

```yaml
dockers_v2:
  - id: cyoda
    images:
      - ghcr.io/cyoda-platform/cyoda
    tags:
      - "{{ .Version }}"
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

  - id: cyoda-latest
    images:
      - ghcr.io/cyoda-platform/cyoda
    tags:
      - latest
    skip_push: "{{ .Prerelease }}"
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

Re-run the prerelease snapshot. Expected: only `:v0.0.1-rc.1` manifest is emitted; `:latest` is skipped. Re-run the non-prerelease snapshot. Expected: both manifests emit.

- [ ] **Step 4: Verify `docker_signs:` compatibility with `dockers_v2:` manifests**

Keep the final `dockers_v2:` pattern from Step 3 or 3B. Re-run with signing ENABLED this time:

```bash
# Install cosign into a temp location so the container can access it. Cosign
# keyless signing via OIDC won't work without GitHub Actions context; we test
# key-based signing with an ephemeral key to verify the artifact-selector
# pathway, which is what's actually changing.

# Generate an ephemeral key:
cosign generate-key-pair --output-key-prefix=/tmp/ephemeral 2>&1 | tail -3
# Produces /tmp/ephemeral.key and /tmp/ephemeral.pub.

# Temporarily modify the docker_signs block in .goreleaser.yaml to use the
# key-based cmd (just for this snapshot run; DO NOT commit):
# Change 'cmd: cosign' to use --key flag:
sed -i.bak 's|cmd: cosign|cmd: cosign\n    args:\n      - "sign"\n      - "--yes"\n      - "--key=/tmp/ephemeral.key"\n      - "${artifact}"|' .goreleaser.yaml
# (Manual patch — the `sed` line above is approximate; adjust to actual config shape.)

# Run snapshot with sign enabled but publish still skipped:
export COSIGN_PASSWORD=""
docker run --rm --memory=6g \
    -v "$(pwd)":/src \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v /tmp/ephemeral.key:/tmp/ephemeral.key \
    -e COSIGN_PASSWORD \
    -w /src \
    goreleaser/goreleaser:latest \
    release --snapshot --clean --skip=publish --parallelism=2 2>&1 | tee sign.log | tail -40
```

**Check for:**

- `signing ghcr.io/cyoda-platform/cyoda:0.0.1` (or similar) in the output — the signing step should see each manifest as an artifact and sign it.
- Exit code 0.

**If signing is skipped** (e.g., "no artifacts matched"): the `artifacts: manifests` selector doesn't resolve against `dockers_v2` output. Fix by changing the selector, likely to `artifacts: images` or `all` — consult GoReleaser v2 docs for the `dockers_v2` artifact names. Update `docker_signs:` accordingly.

Restore the original `.goreleaser.yaml` from the sed backup:

```bash
mv .goreleaser.yaml.bak .goreleaser.yaml
rm /tmp/ephemeral.key /tmp/ephemeral.pub
```

Re-apply only the `dockers:` → `dockers_v2:` + (if used) `docker_signs:` selector change, in the actual .goreleaser.yaml.

- [ ] **Step 5: Cleanup snapshot tag and verify YAML**

```bash
git tag -d v0.0.1-rc.1 2>/dev/null || true
```

Back in the worktree:

```bash
cd /Users/paul/go-projects/cyoda-light/cyoda-go/.worktrees/feat-provisioning-docker-layer
python3 -c "import yaml; yaml.safe_load(open('.goreleaser.yaml'))"
```
Expected: silent success.

Also verify no leftover snapshot state:

```bash
rm -rf "$TMPDIR_SNAP" dist snapshot.log sign.log
```

- [ ] **Step 6: Run `goreleaser check`**

```bash
docker run --rm -v "$(pwd)":/src -w /src goreleaser/goreleaser:latest check 2>&1 | tail -5
```
Expected: "config is valid" or equivalent. Should NOT emit the `dockers/docker_manifests` deprecation warnings anymore.

- [ ] **Step 7: Run the full test sweep to confirm nothing Go-related broke**

```bash
go build ./...
go vet ./...
go test -short ./...
```
All green. (This task doesn't touch Go code, but worth a sanity check.)

- [ ] **Step 8: Commit**

Record the actual pattern chosen (A or B) in the commit message:

```bash
git add .goreleaser.yaml
git commit -m "$(cat <<'EOF'
ci(release): migrate dockers: + docker_manifests: → dockers_v2:

Closes the deprecation warning emitted on every snapshot/release run.
dockers_v2 uses docker buildx build --platform natively, producing
multi-arch manifests in a single step — no separate docker_manifests:
assembly needed. Artifacts under $TARGETPLATFORM/ exactly match the
canonical Dockerfile's COPY line.

Pattern used for :latest suppression on prereleases:
  [A] Single entry with conditional '{{ if not .Prerelease }}latest{{ end }}'
      — verified to work on both non-prerelease and prerelease snapshot runs.

Verification performed before commit (see desktop spec §9 step 7):
  - goreleaser release --snapshot with non-prerelease tag (v0.0.1):
    emitted both :0.0.1 and :latest manifests.
  - goreleaser release --snapshot with prerelease tag (v0.0.1-rc.1):
    emitted only :0.0.1-rc.1; :latest suppressed.
  - goreleaser release --snapshot with ephemeral key-based cosign
    signing: docker_signs: successfully signed dockers_v2 manifests;
    artifact-selector 'artifacts: manifests' resolves correctly under
    dockers_v2.

Closes #52.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

**Replace [A]/[B] and the "emitted" lines** with whatever you actually observed. If Pattern B was needed, say so. If the `docker_signs: artifacts:` selector had to change, record that too.

- [ ] **Step 9: Close issue #52**

Once the commit lands:

```bash
gh issue comment 52 --body "Fixed in commit $(git rev-parse HEAD) (PR for feat/provisioning-docker-layer)."
gh issue close 52 --reason completed
```

---

## End-of-deliverable verification

After all six tasks, before creating the PR:

- [ ] `go build ./...` clean.
- [ ] `go vet ./...` clean.
- [ ] `go test -short ./...` all packages green.
- [ ] `cd plugins/sqlite && go test -short ./...` green (plugin-submodule gap per issue #46).
- [ ] `cd plugins/postgres && go test -short ./...` green (needs Docker).
- [ ] `cd plugins/memory && go test -short ./...` green.
- [ ] `go test -race ./...` clean (one run; race discipline per `.claude/rules/race-testing.md`).
- [ ] `git status` clean.
- [ ] `go mod tidy` produces no diff.
- [ ] All YAML parses: `.goreleaser.yaml`, `.github/workflows/*.yml`, `deploy/docker/compose.yaml`.
- [ ] Manual `docker compose up` smoke against the PR branch — verifies the full flow: Task 2 Dockerfile + Task 3 compose + Task 5 script; container reaches "healthy"; `/api/health`, `/livez`, `/readyz`, `/metrics` all respond.
- [ ] `goreleaser check` emits no deprecation warnings about `dockers` or `docker_manifests`.

## Out of scope (reminder — deferred, per spec)

- Debug image variant.
- CI docker-compose smoke-test job (real regression-catcher; would need its own issue).
- `_FILE` env-var suffix support for secrets (binary enhancement, separate issue).
- Makefile build-target consolidation (dev script build flags stay duplicated for now).
- Cleanup of now-dead root `.dockerignore` (pick up in a tidy follow-up).
- Richer runtime readiness (SPI-level `Ready(ctx)` method — relevant for cyoda-go-cassandra).
