---
topic: cli.serve
title: "cyoda serve — start the API server"
stability: stable
see_also:
  - config
  - run
  - quickstart
---

# cli.serve

## NAME

cli.serve — start the cyoda API server.

## SYNOPSIS

`cyoda` (no subcommand; serving is the default mode)

## DESCRIPTION

Starting with no subcommand loads configuration from environment variables, validates the IAM mode, and binds the REST, gRPC, and admin listeners. The server is single-process, multi-tenant, and stateful — storage is provided by one of the pluggable backends (`memory`, `sqlite`, or `postgres`); see `cyoda help config` for backend selection.

On startup, the binary prints an ASCII banner with version, commit, build date, HTTP port, gRPC port, IAM mode, context path, and active storage profiles. The banner is suppressed by `CYODA_SUPPRESS_BANNER=true`.

The server handles graceful shutdown on `SIGINT` (Ctrl+C) or `SIGTERM`: the HTTP and admin servers drain in-flight requests within a 10-second deadline, then the storage backend is closed and the process exits.

## LISTENERS

Three TCP listeners start concurrently:

- **REST API** — `CYODA_HTTP_PORT` (default: 8080). All entity, schema, workflow, and auth endpoints. Context path prefix: `CYODA_CONTEXT_PATH` (default: `/api`).
- **gRPC** — `CYODA_GRPC_PORT` (default: 9090). Externalized-processor streaming.
- **Admin** — `CYODA_ADMIN_BIND_ADDRESS:CYODA_ADMIN_PORT` (default: `127.0.0.1:9091`). `/livez`, `/readyz`, and `/metrics` endpoints. Admin port is bound to localhost by default; the Helm chart overrides `CYODA_ADMIN_BIND_ADDRESS` so the kubelet can reach `/readyz` without traversing the service mesh.

## KEY ENVIRONMENT VARIABLES

- `CYODA_STORAGE_BACKEND` — Active storage plugin: `memory` (default), `sqlite`, or `postgres`.
- `CYODA_IAM_MODE` — Auth mode: `mock` (default) or `jwt`.
- `CYODA_REQUIRE_JWT` — Refuse to start unless jwt mode and signing key are set (default: false).
- `CYODA_HTTP_PORT` — HTTP listen port (default: 8080).
- `CYODA_GRPC_PORT` — gRPC listen port (default: 9090).
- `CYODA_ADMIN_PORT` — Admin port for health and metrics (default: 9091).
- `CYODA_CONTEXT_PATH` — Context path prefix for all routes (default: `/api`).
- `CYODA_PROFILES` — Comma-separated profile names for `.env` file layering (default: none).
- `CYODA_LOG_LEVEL` — Log level: `debug`, `info`, `warn`, or `error` (default: `info`).
- `CYODA_OTEL_ENABLED` — Enable OpenTelemetry tracing and metrics (default: false).

## EXAMPLES

```
# Quickstart: in-memory storage, mock auth (no configuration required)
cyoda

# SQLite backend with debug logging
CYODA_STORAGE_BACKEND=sqlite CYODA_LOG_LEVEL=debug cyoda

# PostgreSQL backend, JWT auth, loaded via profiles
CYODA_PROFILES=postgres,jwt \
  CYODA_JWT_SIGNING_KEY="$(cat signing.pem)" \
  cyoda

# Suppress startup banner (useful in CI)
CYODA_SUPPRESS_BANNER=true cyoda
```

## SEE ALSO

- config
- run
- quickstart
