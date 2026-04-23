---
topic: config
title: "cyoda configuration reference"
stability: stable
see_also:
  - cli
  - run
---

# config

## NAME

config — environment-driven configuration for cyoda.

## SYNOPSIS

All configuration is environment variables prefixed with `CYODA_`. Topics group related variables:

- `config.database` — storage backend selection, per-backend connection settings
- `config.auth` — IAM mode, JWT issuer, admin controls
- `config.grpc` — gRPC listener and compute-node credentials
- `config.schema` — schema-extension log tuning

## DESCRIPTION

### Precedence

Explicit command-line flags beat environment variables, which beat default values. cyoda uses
environment variables as the primary configuration surface. The `_FILE` suffix pattern allows
reading a secret from a file path instead of the variable value — for example,
`CYODA_POSTGRES_URL_FILE=/etc/secrets/db-url` takes precedence over `CYODA_POSTGRES_URL`
if both are set.

### Profile loader

`CYODA_PROFILES` is a comma-separated list of profile names. For each name `N`, a file
`cyoda.N.env` is loaded from the working directory before the process's own environment is
consulted. This supports local development without exporting many variables.

**Example:**

```
CYODA_PROFILES=postgres,otel go run ./cmd/cyoda
```

loads `cyoda.postgres.env` and `cyoda.otel.env` from the working directory.

### Server options

- `CYODA_HTTP_PORT` — HTTP listen port (default: `8080`)
- `CYODA_CONTEXT_PATH` — URL prefix for all routes (default: `/api`)
- `CYODA_ERROR_RESPONSE_MODE` — error detail level: `sanitized` or `verbose` (default: `sanitized`)
- `CYODA_LOG_LEVEL` — log level: `debug`, `info`, `warn`, or `error` (default: `info`)
- `CYODA_SUPPRESS_BANNER` — silence startup and mock-auth banners (default: `false`)
- `CYODA_STARTUP_TIMEOUT` — deadline for plugin and TM init (default: `30s`)
- `CYODA_MAX_STATE_VISITS` — max visits per state in workflow cascade (default: `10`)
- `CYODA_MODEL_CACHE_LEASE` — model cache lease duration (default: `5m`)
- `CYODA_DEBUG` — reserved for future debug verbosity flag

### Admin and metrics

- `CYODA_ADMIN_PORT` — admin port for health and metrics (default: `9091`)
- `CYODA_ADMIN_BIND_ADDRESS` — admin listener bind address (default: `127.0.0.1`)
- `CYODA_METRICS_REQUIRE_AUTH` — require Bearer auth on `/metrics` (default: `false`)
- `CYODA_METRICS_BEARER` — static Bearer token for `GET /metrics` (default: unset)
- `CYODA_METRICS_BEARER_FILE` — file path for `CYODA_METRICS_BEARER`
- `CYODA_OTEL_ENABLED` — enable OpenTelemetry tracing and metrics (default: `false`)

### Search and transaction internals

- `CYODA_SEARCH_SNAPSHOT_TTL` — search snapshot TTL (default: `1h`)
- `CYODA_SEARCH_REAP_INTERVAL` — search snapshot reap interval (default: `5m`)
- `CYODA_TX_TTL` — transaction TTL (default: `60s`)
- `CYODA_TX_REAP_INTERVAL` — transaction reap interval (default: `10s`)
- `CYODA_TX_OUTCOME_TTL` — transaction outcome TTL (default: `5m`)

### Cluster and dispatch

- `CYODA_CLUSTER_ENABLED` — enable multi-node clustering (default: `false`)
- `CYODA_NODE_ID` — unique node identifier (required when cluster enabled)
- `CYODA_NODE_ADDR` — this node's address (default: `http://localhost:8080`)
- `CYODA_GOSSIP_ADDR` — gossip protocol listen address (default: `:7946`)
- `CYODA_GOSSIP_STABILITY_WINDOW` — gossip stability window (default: `2s`)
- `CYODA_SEED_NODES` — comma-separated seed node addresses (default: empty)
- `CYODA_PROXY_TIMEOUT` — request proxy timeout (default: `30s`)
- `CYODA_DISPATCH_WAIT_TIMEOUT` — dispatch wait timeout (default: `5s`)
- `CYODA_DISPATCH_FORWARD_TIMEOUT` — dispatch forward timeout (default: `30s`)
- `CYODA_KEEPALIVE_INTERVAL` — keep-alive send interval in seconds (default: `10`)
- `CYODA_KEEPALIVE_TIMEOUT` — keep-alive timeout in seconds (default: `30`)

## SEE ALSO

- cli
- run
