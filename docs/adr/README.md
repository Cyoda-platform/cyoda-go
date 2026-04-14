# Architecture Decision Records

This directory holds Architecture Decision Records (ADRs) for `cyoda-go`.

## What goes here

Substantive architectural decisions with non-obvious trade-offs: concurrency models, transactional semantics, protocol choices, cross-cutting invariants, load-bearing assumptions. Each ADR captures the **why**, not the **what** — the code captures the what.

## What does not go here

- Implementation specs (`docs/superpowers/specs/`)
- Implementation plans (`docs/superpowers/plans/`)
- Product requirements (`docs/PRD.md`)
- System architecture reference (`docs/ARCHITECTURE.md`)

## Format

Each ADR is a separate markdown file named `NNNN-kebab-case-title.md`, numbered sequentially. The template is:

```markdown
# NNNN. Title

**Status:** Proposed | Accepted | Superseded by NNNN | Deprecated
**Date:** YYYY-MM-DD

## Context
What problem is being solved? What constraints apply?

## Decision
What was decided?

## Consequences
What are the positive, negative, and neutral consequences of this decision?
What becomes easier? What becomes harder? What new risks are introduced?

## Alternatives considered
Other options that were weighed and rejected, with reasoning.
```

## Predecessor ADRs

`cyoda-light-go` (the predecessor repository) has ADRs that informed the architecture captured here. They are not copied — they lived under different module paths and predate the plugin refactor. When a decision in this repository descends from one of those predecessor ADRs, the new ADR cites the predecessor by path (e.g., "supersedes `cyoda-light-go/docs/adr/0001-blanket-force-abort-on-shard-takeover.md`").

Cassandra-specific ADRs live in the proprietary `cyoda-go-cassandra` repository, not here.
