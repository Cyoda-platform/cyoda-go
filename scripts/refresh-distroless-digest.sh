#!/usr/bin/env bash
# refresh-distroless-digest.sh — re-resolve the gcr.io/distroless/static
# digest pin in deploy/docker/Dockerfile.
#
# Why: the Dockerfile pins the distroless base image by @sha256:... for
# supply-chain integrity. Upstream rebuilds the :latest tag periodically with
# CVE patches, so the pinned digest goes stale.
#
# Cadence: monthly, or whenever Renovate/Dependabot opens a "distroless update
# available" issue. Run this script, review the diff, commit if the new digest
# resolves to a multi-arch index (it should), and push.
#
# Usage:
#   ./scripts/refresh-distroless-digest.sh           # dry-run, prints proposed change
#   ./scripts/refresh-distroless-digest.sh --apply   # rewrites Dockerfile in place
#
# Resolution strategy (first that succeeds wins):
#   1. `crane digest` (recommended; ships with go-containerregistry)
#   2. `docker manifest inspect` (requires logged-in docker)
#   3. `curl` against the GCR v2 manifests endpoint (no auth needed for public
#      distroless)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE="${REPO_ROOT}/deploy/docker/Dockerfile"
IMAGE="gcr.io/distroless/static"
TAG="latest"

apply=0
if [[ "${1:-}" == "--apply" ]]; then
  apply=1
fi

resolve_digest() {
  if command -v crane >/dev/null 2>&1; then
    crane digest "${IMAGE}:${TAG}"
    return
  fi
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    if digest="$(docker manifest inspect -v "${IMAGE}:${TAG}" 2>/dev/null | grep -m1 '"digest"' | sed -E 's/.*"(sha256:[a-f0-9]+)".*/\1/')"; then
      if [[ -n "${digest}" ]]; then
        echo "${digest}"
        return
      fi
    fi
  fi
  # Fallback: GCR v2 API. Request the OCI image index so we get the multi-arch
  # digest, not a single-platform manifest.
  curl -fsSI \
    -H "Accept: application/vnd.docker.distribution.manifest.list.v2+json" \
    -H "Accept: application/vnd.oci.image.index.v1+json" \
    "https://gcr.io/v2/distroless/static/manifests/${TAG}" \
    | awk -F': ' 'tolower($1) == "docker-content-digest" { sub(/\r$/, "", $2); print $2 }'
}

new_digest="$(resolve_digest)"
if [[ -z "${new_digest}" || "${new_digest}" != sha256:* ]]; then
  echo "error: could not resolve digest for ${IMAGE}:${TAG}" >&2
  exit 1
fi

current_line="$(grep -E '^FROM gcr\.io/distroless/static@sha256:' "${DOCKERFILE}" || true)"
if [[ -z "${current_line}" ]]; then
  echo "error: could not find pinned FROM line in ${DOCKERFILE}" >&2
  exit 1
fi
current_digest="$(printf '%s\n' "${current_line}" | sed -E 's/.*@(sha256:[a-f0-9]+).*/\1/')"

if [[ "${current_digest}" == "${new_digest}" ]]; then
  echo "Already up to date: ${current_digest}"
  exit 0
fi

today="$(date -u +%Y-%m-%d)"
new_from="FROM ${IMAGE}@${new_digest}"
# Annotation comment lives on the line below FROM.
new_annotation="# was :${TAG}, pinned ${today}"

echo "Current : ${current_digest}"
echo "New     : ${new_digest}"
echo "Date    : ${today}"

if (( apply == 0 )); then
  echo
  echo "Dry run. Re-run with --apply to rewrite ${DOCKERFILE}."
  exit 0
fi

# Use a temp file to avoid sed -i portability issues.
tmp="$(mktemp)"
awk -v new_from="${new_from}" -v new_annotation="${new_annotation}" '
  /^FROM gcr\.io\/distroless\/static@sha256:/ {
    print new_from
    # Replace the immediately-following annotation comment if present.
    if ((getline next_line) > 0) {
      if (next_line ~ /^# was :/) {
        print new_annotation
      } else {
        print new_annotation
        print next_line
      }
    }
    next
  }
  { print }
' "${DOCKERFILE}" > "${tmp}"
mv "${tmp}" "${DOCKERFILE}"
echo "Updated ${DOCKERFILE}"
