---
topic: config.database
title: "database configuration"
stability: stable
see_also:
  - config
  - run
---

# config.database

## NAME

config.database — storage backend selection and per-backend connection settings.

## SYNOPSIS

Select the backend with `CYODA_STORAGE_BACKEND`. Configure the chosen backend via its
per-backend variables (`CYODA_SQLITE_*` or `CYODA_POSTGRES_*`). The `memory` backend
requires no additional configuration.

## OPTIONS

### Backend selection

- `CYODA_STORAGE_BACKEND` — storage backend to use: `sqlite`, `postgres`, or `memory`
  (default: `sqlite`)

### SQLite backend (`CYODA_SQLITE_*`)

Used when `CYODA_STORAGE_BACKEND=sqlite`.

- `CYODA_SQLITE_PATH` — path to the SQLite database file (default: `cyoda.db`)
- `CYODA_SQLITE_AUTO_MIGRATE` — run embedded SQL migrations on startup (default: `true`)
- `CYODA_SQLITE_BUSY_TIMEOUT` — busy timeout for lock contention (default: `5s`)
- `CYODA_SQLITE_CACHE_SIZE` — SQLite page cache size in pages (default: `-2000`, meaning 2 MB)
- `CYODA_SQLITE_SEARCH_SCAN_LIMIT` — max rows scanned per search query (default: `10000`)

The prefix `CYODA_SQLITE_` is used to namespace all SQLite configuration variables.

### PostgreSQL backend (`CYODA_POSTGRES_*`)

Used when `CYODA_STORAGE_BACKEND=postgres`.

- `CYODA_POSTGRES_URL` — PostgreSQL connection string, e.g. `postgres://user:pass@host/db`
  (required when using postgres backend)
- `CYODA_POSTGRES_URL_FILE` — file path for `CYODA_POSTGRES_URL` (takes precedence)
- `CYODA_POSTGRES_MAX_CONNS` — maximum pool connections (default: `25`)
- `CYODA_POSTGRES_MIN_CONNS` — minimum pool connections (default: `5`)
- `CYODA_POSTGRES_MAX_CONN_IDLE_TIME` — max idle time before closing a connection (default: `5m`)
- `CYODA_POSTGRES_AUTO_MIGRATE` — run embedded SQL migrations on startup (default: `true`)

The prefix `CYODA_POSTGRES_` is used to namespace all PostgreSQL configuration variables.

### Memory backend

Used when `CYODA_STORAGE_BACKEND=memory`. No additional configuration needed. Data is
not persisted across restarts. Suitable for development and testing only.

## EXAMPLES

**SQLite (default):**

```
CYODA_STORAGE_BACKEND=sqlite
CYODA_SQLITE_PATH=/var/data/cyoda.db
CYODA_SQLITE_AUTO_MIGRATE=true
```

**PostgreSQL:**

```
CYODA_STORAGE_BACKEND=postgres
CYODA_POSTGRES_URL=postgres://cyoda:secret@localhost:5432/cyoda
CYODA_POSTGRES_MAX_CONNS=50
```

**In-memory (tests/dev):**

```
CYODA_STORAGE_BACKEND=memory
```

## SEE ALSO

- config
- run
