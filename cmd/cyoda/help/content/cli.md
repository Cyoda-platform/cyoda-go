---
topic: cli
title: "cyoda CLI — subcommand reference"
stability: stable
see_also:
  - config
  - run
  - quickstart
---

# cli

## NAME

cli — the cyoda command-line interface.

## SYNOPSIS

`cyoda [<subcommand>] [<flags>]`

## DESCRIPTION

cyoda is a Go binary that embeds the full platform: API server, schema engine, workflow runner, and storage plugins. Invoked with no subcommand, it starts the server using environment-provided configuration. Subcommands provide operational affordances — `init` for first-run bootstrap, `health` for liveness probes, `migrate` for schema migrations.

Global flags `--help` (or `-h`) and `--version` (or `-v`) are recognized before subcommand dispatch.

## SUBCOMMANDS

- `cyoda init [--force]` — Write a starter user config enabling sqlite. See `cyoda help cli init`.
- `cyoda health` — Probe `/readyz` on the admin listener. See `cyoda help cli health`.
- `cyoda migrate [--timeout <duration>]` — Run schema migrations for the configured backend and exit. See `cyoda help cli migrate`.
- `cyoda help [<topic>...] [--format=<fmt>]` — Browse the help topic tree. See `cyoda help cli help`.

## OPTIONS

- `--help`, `-h` — Show help for the `cli` topic. Equivalent to `cyoda help cli`.
- `--version`, `-v` — Print the binary's ldflag-injected version, commit SHA, and build date. Exits 0.

## CONFIGURATION

All server configuration is via environment variables with the `CYODA_` prefix. Variables can be placed in `.env` files and loaded automatically using profiles. See `cyoda help config` for the full reference.

## EXAMPLES

```
# Start the server with defaults (in-memory storage, mock auth)
cyoda

# First-run bootstrap then start
cyoda init && cyoda

# Check version of an installed binary
cyoda --version

# Run with profiles: postgres storage + observability
CYODA_PROFILES=postgres,otel cyoda
```

## SEE ALSO

- config
- run
- quickstart
