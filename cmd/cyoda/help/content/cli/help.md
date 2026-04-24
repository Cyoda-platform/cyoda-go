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

Release archives include a pre-rendered `help/` directory for offline reference.

## OPTIONS

- `--format=<auto|text|markdown|json>` — Default `auto` selects text on a TTY and markdown off-TTY.

## TOPIC ACTIONS

Some topics publish machine-readable actions invoked as `cyoda help <topic> <action>`. Actions emit raw content to stdout without rendering the help body.

The `cyoda help` top-level summary lists topics that have actions registered. Each topic's render output includes an `ACTIONS` footer enumerating its actions. The JSON payload of `cyoda help <topic> --format=json` carries an `actions` array of the same names.

Actions do not accept `--format` (that flag governs the help body's output form, not action emission).

## REST API

The same topic tree is served over HTTP on the running server. No authentication is required — help content is public. Responses are machine-parseable (`application/json` for topic payloads, `application/problem+json` for errors).

### Endpoints

- `GET {CYODA_CONTEXT_PATH}/help` — returns the full topic tree as a `HelpPayload` JSON document.
- `GET {CYODA_CONTEXT_PATH}/help/{topic}` — returns a single `TopicDescriptor` JSON document for the addressed topic.
- `OPTIONS {CYODA_CONTEXT_PATH}/help` — CORS preflight; returns `204 No Content`.
- `OPTIONS {CYODA_CONTEXT_PATH}/help/{topic}` — CORS preflight; returns `204 No Content`.

`CYODA_CONTEXT_PATH` defaults to `/api`. Both mount points are relative to the configured context path.

### Methods

Only `GET` and `OPTIONS` are accepted. Any other method (`POST`, `PUT`, `DELETE`, `PATCH`) returns `405 Method Not Allowed` with header `Allow: GET, OPTIONS` and a `BAD_REQUEST` error body (`application/problem+json`).

### Topic path syntax

The `{topic}` URL segment uses the canonical dotted form (e.g. `errors.VALIDATION_FAILED`, `config.database`, `cli.serve`). The CLI-argv form with spaces is not accepted at the REST layer.

Valid path characters: `[A-Za-z0-9._-]+` with the additional constraint that the first and last character must each be `[A-Za-z0-9]` (leading/trailing dots and hyphens are rejected). A path that fails this pattern returns `400 BAD_REQUEST`.

### Response — full tree

`GET {CYODA_CONTEXT_PATH}/help` returns a `HelpPayload` object.

```json
{
  "schema": 1,
  "version": "0.6.1",
  "topics": [
    {
      "topic": "cli.help",
      "path": ["cli", "help"],
      "title": "cyoda help — the help subsystem",
      "synopsis": "the cyoda help subsystem and topic-tree contract.",
      "body": "# cli.help\n\n## NAME\n\n...",
      "sections": [
        { "name": "NAME", "body": "cli.help — the cyoda help subsystem and topic-tree contract." },
        { "name": "SYNOPSIS", "body": "`cyoda help [<topic>...] [--format=<fmt>]`" }
      ],
      "see_also": ["cli"],
      "stability": "stable",
      "actions": [],
      "children": []
    }
  ]
}
```

`HelpPayload` fields:

- `schema` (integer) — payload schema version. Currently `1`. Consumers must check this before parsing; a higher value signals additive fields were added.
- `version` (string) — ldflag-injected binary version string (e.g. `"0.6.1"`).
- `topics` (array of `TopicDescriptor`) — every topic in the tree, in walk order.

### Response — single topic

`GET {CYODA_CONTEXT_PATH}/help/{topic}` returns a `TopicDescriptor` object.

```json
{
  "topic": "cli.help",
  "path": ["cli", "help"],
  "title": "cyoda help — the help subsystem",
  "synopsis": "the cyoda help subsystem and topic-tree contract.",
  "body": "# cli.help\n\n## NAME\n\ncli.help — the cyoda help subsystem and topic-tree contract.\n\n...",
  "sections": [
    { "name": "NAME", "body": "cli.help — the cyoda help subsystem and topic-tree contract." },
    { "name": "SYNOPSIS", "body": "`cyoda help [<topic>...] [--format=<fmt>]`" },
    { "name": "DESCRIPTION", "body": "..." },
    { "name": "OPTIONS", "body": "..." },
    { "name": "STABILITY", "body": "..." },
    { "name": "EXAMPLES", "body": "..." },
    { "name": "SEE ALSO", "body": "..." }
  ],
  "see_also": ["cli"],
  "stability": "stable",
  "actions": [],
  "children": ["cli.help"]
}
```

`TopicDescriptor` fields:

- `topic` (string) — canonical dotted path (e.g. `"cli.help"`).
- `path` (array of strings) — topic path split into segments (e.g. `["cli", "help"]`).
- `title` (string) — topic title from the front-matter `title:` field.
- `synopsis` (string) — single-line description extracted from the NAME or DESCRIPTION section; inline markers stripped, whitespace collapsed.
- `body` (string) — full raw markdown source of the topic file.
- `sections` (array of objects) — H2-delimited blocks; each object has `name` (string, the H2 heading text) and `body` (string, everything between this H2 and the next H2 or end-of-file, trimmed).
- `see_also` (array of strings) — dotted topic paths from the front-matter `see_also:` list.
- `stability` (string) — one of `"stable"`, `"evolving"`, or `"experimental"`.
- `actions` (array of strings) — names of machine-readable actions the topic supports; empty array when none.
- `children` (array of strings, omitted when empty) — dotted paths of direct child topics.

### CORS

All `GET` responses carry `Access-Control-Allow-Origin: *`.

`OPTIONS` preflight responses carry:

- `Access-Control-Allow-Origin: *`
- `Access-Control-Allow-Methods: GET, OPTIONS`
- `Access-Control-Allow-Headers: Content-Type, Authorization`
- `Access-Control-Max-Age: 86400`

### Errors

Errors use RFC 9457 Problem Details (`application/problem+json`). See `errors` topic for the full envelope shape.

- `400 BAD_REQUEST` — the `{topic}` path segment contains disallowed characters (fails `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`). Also returned for any method other than `GET` or `OPTIONS` (with `Allow: GET, OPTIONS` response header).
- `404 HELP_TOPIC_NOT_FOUND` — the `{topic}` is well-formed but does not resolve to any topic in the tree.

### Examples

```bash
# Fetch the full topic tree and inspect schema version and binary version
curl -s http://localhost:8080/api/help | jq '{schema: .schema, version: .version}'

# Fetch a single topic and extract its registered actions
curl -s http://localhost:8080/api/help/cli.help | jq '.actions'

# Fetch an unknown topic — observe the 404 HELP_TOPIC_NOT_FOUND body
curl -s -w "\nHTTP %{http_code}\n" http://localhost:8080/api/help/no.such.topic

# Send a disallowed method and confirm 405 with Allow header
curl -si -X POST http://localhost:8080/api/help | grep -E "^HTTP|^Allow:"

# CORS preflight — confirm 204 and preflight headers
curl -si -X OPTIONS http://localhost:8080/api/help | grep -E "^HTTP|^Access-Control"
```

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
