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

The server handles graceful shutdown on `SIGINT` (Ctrl+C) or `SIGTERM`: the HTTP and admin servers drain in-flight requests within a hardcoded 10-second deadline, then the storage backend is closed and the process exits.

## LISTENERS

Three TCP listeners start concurrently:

- **REST API** — `CYODA_HTTP_PORT` (default: 8080). All entity, schema, workflow, and auth endpoints. Context path prefix: `CYODA_CONTEXT_PATH` (default: `/api`).
- **gRPC** — `CYODA_GRPC_PORT` (default: 9090). Externalized-processor streaming.
- **Admin** — `CYODA_ADMIN_BIND_ADDRESS:CYODA_ADMIN_PORT` (default: `127.0.0.1:9091`). `/livez`, `/readyz`, and `/metrics` endpoints. Admin port is bound to localhost by default; the Helm chart overrides `CYODA_ADMIN_BIND_ADDRESS` so the kubelet can reach `/readyz` without traversing the service mesh.

## ENVIRONMENT VARIABLES

All configuration is via environment variables. The subtopics below enumerate the complete per-subsystem variable sets:

- `config` — all top-level server options (HTTP port, log level, OTel, etc.)
- `config.database` — storage backend selection and per-backend connection settings
- `config.auth` — IAM mode, JWT issuer, signing key
- `config.grpc` — gRPC listener and compute-node credentials
- `config.schema` — schema-extension log tuning

Variables read specifically during server boot (not covered by the config subtopics above):

- `CYODA_HTTP_PORT` (int, default: `8080`) — HTTP API listen port.
- `CYODA_GRPC_PORT` (int, default: `9090`) — gRPC listen port.
- `CYODA_ADMIN_PORT` (int, default: `9091`) — admin listener port for `/livez`, `/readyz`, `/metrics`.
- `CYODA_ADMIN_BIND_ADDRESS` (string, default: `127.0.0.1`) — admin listener bind address.
- `CYODA_OTEL_ENABLED` (bool, default: `false`) — initialize the OpenTelemetry SDK at startup.
- `CYODA_LOG_LEVEL` (string, default: `info`) — accepted: `debug|info|warn|error`.
- `CYODA_SUPPRESS_BANNER` (bool, default: `false`) — suppress the ASCII startup banner and mock-auth warning.

## STARTUP EXIT CODES

- `0` — clean shutdown after SIGINT or SIGTERM.
- `1` — startup failure: IAM validation failed (`CYODA_REQUIRE_JWT` contract not met), OTel SDK initialization error, gRPC port-bind failure, or backend connection failure during `app.New`.

## EXAMPLES

```
# Run with defaults (in-memory storage, mock auth)
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
