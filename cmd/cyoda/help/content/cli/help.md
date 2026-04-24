---
topic: cli.help
title: "cyoda help — the help subsystem"
stability: stable
see_also:
  - cli
---

# cli.help

## NAME

cli.help — the cyoda help subsystem and topic-tree contract.

## SYNOPSIS

`cyoda help [<topic>...] [--format=<fmt>]`

## DESCRIPTION

`cyoda help` is the built-in documentation browser. Topics are organized in a dot-separated tree (`cli`, `cli.serve`, `config.database`, etc.). Invoking `cyoda help` with no arguments prints a summary of all top-level topics. Invoking it with one or more topic segments navigates the tree.

The help content is embedded in the binary at build time — no network access or external files are needed.

The same topic tree is also available as a REST endpoint on the running server: `GET /api/help/<topic>` returns JSON, suitable for tooling and programmatic introspection. Release archives include a pre-rendered `help/` directory for offline reference.

## OPTIONS

- `--format=<auto|text|markdown|json>` — Default `auto` selects text on a TTY and markdown off-TTY.

## TOPIC ACTIONS

Some topics publish machine-readable actions invoked as `cyoda help <topic> <action>`:

- `cyoda help openapi json` / `yaml` — OpenAPI spec in either format
- `cyoda help grpc proto` / `json` — gRPC proto source or descriptor JSON

Actions emit raw content to stdout without rendering the help body. They do not accept `--format`.

## STABILITY

Topic additions are non-breaking. Renaming or removing a topic requires a deprecation window and an entry in CONTRIBUTING.md. Topic paths are stable for the duration of a major version.

## EXAMPLES

```
# Show all top-level topics
cyoda help

# Show the cli topic
cyoda help cli

# Show the serve subtopic
cyoda help cli serve

# Output JSON for the full topic tree
cyoda help --format=json

# Output JSON for a single topic
cyoda help --format=json cli serve
```

## SEE ALSO

- cli
