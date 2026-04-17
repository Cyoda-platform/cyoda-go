# Substantive issues
## 1. The named-volume permissions claim in §1 is optimistic and needs verification. You write that "Docker's named volumes inherit permissions from the first writer by default... the cyoda binary writes to it as UID 65532, and Docker records the ownership." That's not quite how it works. When a named volume mounts onto a path that doesn't exist in the image (and distroless/static has no /var/lib/cyoda/), Docker creates the mount point as root:root. The nonroot process then tries to write and gets EACCES. Docker's "copy ownership from image" behavior only kicks in when the path does exist in the image with files in it.

The clean fix is a two-stage build that stages a pre-chowned empty directory:

```dockerfile
FROM busybox:stable AS stage
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/cyoda /cyoda
COPY --from=stage --chown=65532:65532 /data /var/lib/cyoda
USER 65532:65532
EXPOSE 8080 9090 9091
ENTRYPOINT ["/cyoda"]
```

The §9 smoke test should explicitly assert writability (not just that /api/health returns 200 — sqlite might fall back to a non-persistent mode and still answer). Something like docker compose exec cyoda stat -c '%u' /var/lib/cyoda/cyoda.db returning 65532.

## 2. cyoda health has no timeout test. 
The table covers 200, 503, and connection-refused, but not the "server accepts connection then hangs" case — which is exactly what a deadlocked readiness check looks like in practice. The 2s client timeout is where the interesting behavior lives; it should have a TestCyodaHealth_Timeout using an httptest.Server handler that sleeps 5s.

## 3. The docker_signs: 
assertion ("stays — uses the same manifest target and doesn't change") deserves verification, not assertion. dockers_v2 produces manifest artifacts via a different internal path than the old dockers + docker_manifests pair. I'm not confident artifacts: manifests resolves to the same set. Worth a GoReleaser dry-run (goreleaser release --snapshot --clean --skip=publish,sign or similar) in the implementation phase before the migration PR ships, and noting that verification in §9.

## 4. {{ if not .Prerelease }}latest{{ end }} producing an empty tag — behavior on dockers_v2. 
The old config used skip_push: '{{ .Prerelease }}' on the :latest manifest, which is a documented-safe pattern. Empty-string tag suppression is plausible but not explicitly guaranteed in the dockers_v2 spec I've seen. If GoReleaser emits a literal "" tag and fails, the fallback pattern is to keep :latest as a separate entry with skip_push: auto (which honors IsSnapshot and IsNightly but not Prerelease — so you'd want skip_push: "{{ .Prerelease }}" explicitly). Worth pinning down before merge.

# Smaller things worth addressing
## Compose: 
add a commented production-hardening hint. The README documents CYODA_REQUIRE_JWT=true as the production safety floor that Docker deliberately leaves off. Users cribbing this fragment into their own stack for real deployments will miss that. A three-line comment near the environment: block:

```yaml
# For production: uncomment and supply a JWT signing key.
# CYODA_REQUIRE_JWT: "true"
# CYODA_JWT_SIGNING_KEY: ${CYODA_JWT_SIGNING_KEY:?set signing key}
```
costs nothing and plugs the documented footgun.

## The /readyz rationale cites depends_on: 
service_healthy — but the canonical compose is single-service. The rationale is still correct (readiness is the right gate for the cribbing use case, which is multi-service), but the §3 paragraph reads as if the canonical compose uses depends_on, which it doesn't. One sentence acknowledging that "this matters most for downstream services in user-assembled compose files" would tighten it.

## Dev script: 
.dockerignore interaction. The repo has a .dockerignore at root. With the .buildctx/ context shape, it doesn't apply (different build context root) — but that's non-obvious and worth one line in the script comment or a §8 out-of-scope note explaining that the existing .dockerignore is now a no-op for the release pipeline and only affects hypothetical docker build . from repo root. If it's truly dead, consider removing it.

## Dev script: 
duplicated build invocation. The go build -ldflags="-s -w" -o ... line will drift from whatever the release build settings become (the goreleaser config already injects -X main.version=...). Worth calling a Makefile target like make build-linux-static BINDIR=.buildctx/$PLATFORM so there's one source of truth for build flags.

## Health client: 
consider localhost vs 127.0.0.1. Explicit 127.0.0.1 is fine for distroless/static (which ships a valid /etc/hosts), but localhost generalizes slightly better if the base image ever changes. Minor; either is defensible.

## §6 README: 
cross-link to Helm. The shared spec apparently owns production orchestration via Helm. The Docker README should explicitly say "for Kubernetes / production, use the Helm chart — this compose is for development and evaluation" so the security posture note isn't the only steering away from prod use.

# Questions

Is there an existing CI job that does docker compose up smoke-testing, or does §9 step 8 stand up a new one? A release-blocker artifact without CI coverage is a regression risk.
The shared spec presumably documents the CYODA_IMAGE override contract — worth confirming the dev script's use of it matches the contract shape (e.g., does the shared spec promise :latest as the default, or :stable?).
§8 lists "debug image variant" as out of scope. Fair, but: do the Helm chart's kubectl debug --image workflows assume one exists? If so, there's an implicit dependency to flag.
