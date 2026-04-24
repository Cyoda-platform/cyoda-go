---
topic: cli.health
title: "cyoda health — liveness probe"
stability: stable
see_also:
  - telemetry
---

# cli.health

## NAME

cli.health — probe the admin listener's `/readyz` endpoint.

## SYNOPSIS

`cyoda health`

## DESCRIPTION

`cyoda health` sends an HTTP GET to `http://127.0.0.1:<port>/readyz` and exits 0 if the server responds with HTTP 200. Any non-200 response or connection error causes exit 1.

The probe uses a hard-coded 2-second HTTP client timeout. This timeout is load-bearing: a deadlocked readiness handler looks identical to "server accepts the connection then hangs" from the client's perspective. Without a timeout, Docker's `HEALTHCHECK` would inherit the deadlock and never mark the container unhealthy.

The port is read from `CYODA_ADMIN_PORT` (default: `9091`). The admin listener always binds to `127.0.0.1` from the probe's perspective — `cyoda health` is designed to run inside the same container or on the same host as the server.

Primary consumers:

- **Docker Compose** — `HEALTHCHECK` stanza in the service definition.
- **Helm chart** — `readinessProbe` invokes `cyoda health` before the pod is marked ready.

## OPTIONS

`cyoda health` accepts no flags.

## ENVIRONMENT VARIABLES

- `CYODA_ADMIN_PORT` — Admin listener port to probe (default: `9091`).

## EXIT CODES

- `0` — Server responded HTTP 200. Instance is ready.
- `1` — Connection failed, timed out, or server returned a non-200 status.

## EXAMPLES

```
# Basic probe (uses CYODA_ADMIN_PORT or default 9091)
cyoda health

# Probe a server on a non-default admin port
CYODA_ADMIN_PORT=19091 cyoda health

# Use in a shell script
if cyoda health; then
  echo "server is ready"
else
  echo "server not ready" >&2
  exit 1
fi
```

## SEE ALSO

- telemetry
