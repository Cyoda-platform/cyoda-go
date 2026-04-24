---
topic: cli.migrate
title: "cyoda migrate — run storage migrations"
stability: stable
see_also:
  - config
  - cli.init
---

# cli.migrate

## NAME

cli.migrate — run schema migrations for the configured storage backend and exit.

## SYNOPSIS

`cyoda migrate [--timeout <duration>]`

## DESCRIPTION

`cyoda migrate` is a short-lived process that applies pending schema migrations for the configured storage backend, then exits cleanly — no admin listener, no background loops, no lingering goroutines.

It loads the same configuration the server does via `app.DefaultConfig`, honoring all `CYODA_*` environment variables and `_FILE` suffix resolution identically to the main server process.

Dispatch is on `CYODA_STORAGE_BACKEND`:

- **memory** — No-op; exits 0. The memory backend has no schema to migrate.
- **sqlite** — No-op; exits 0. SQLite applies migrations lazily on first open; the migrate subcommand is not needed.
- **postgres** — Runs the postgres plugin's embedded migration logic against `CYODA_POSTGRES_URL`. Exits 0 on success, 1 on error.
- **other** — Exits 1 with "unknown storage backend".

The migrate subcommand respects the schema-compatibility contract: it refuses to run if the database schema is newer than the code's embedded maximum version. This prevents a rollback from accidentally downgrading a schema.

The primary consumer is the Helm chart's pre-install and pre-upgrade Job, which runs `cyoda migrate` before starting the server to ensure the schema is up to date.

## OPTIONS

- `--timeout <duration>` — Maximum duration for the migration run (default: 5 minutes). Accepts Go duration strings: `30s`, `2m`, `10m`, etc.

## ENVIRONMENT VARIABLES

- `CYODA_STORAGE_BACKEND` — Selects the backend to migrate (default: `memory`).
- `CYODA_POSTGRES_URL` — PostgreSQL DSN, required when backend is `postgres`. Accepts `CYODA_POSTGRES_URL_FILE` variant.

## EXIT CODES

- `0` — Migration succeeded (or was a no-op for memory/sqlite).
- `1` — Runtime error: bad config, database unreachable, migration failure, or timeout.
- `2` — Flag-parse error.

## EXAMPLES

```
# Migrate postgres schema (reads CYODA_POSTGRES_URL from environment)
CYODA_STORAGE_BACKEND=postgres \
  CYODA_POSTGRES_URL="postgres://user:pass@localhost/cyoda" \
  cyoda migrate

# Migrate with a custom timeout
CYODA_STORAGE_BACKEND=postgres \
  CYODA_POSTGRES_URL="postgres://user:pass@localhost/cyoda" \
  cyoda migrate --timeout 2m

# No-op — memory backend
cyoda migrate
```

## SEE ALSO

- config
- cli.init
