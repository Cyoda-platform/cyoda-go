---
paths:
  - "internal/**/*.go"
---
# Logging Policy

Use `log/slog` exclusively. Never use `log.Printf` or `fmt.Printf` for operational logging.

## Log Levels

- **ERROR:** Something failed that shouldn't have. Requires investigation.
- **WARN:** Unexpected but recoverable. Might indicate a problem if repeated.
- **INFO:** High-level flow milestones. Reading INFO tells you what the system is doing.
- **DEBUG:** Detailed flow tracing with payload previews via `logging.PayloadPreview()` (first 200 chars).

## Rules

- Never log credentials, tokens, secrets, or signing keys at any level.
- Structured context: include `pkg`, `memberId`, `entityId`, `eventType` fields as appropriate.
- One event = one log line at one level. Don't log the same event at INFO and DEBUG.
- Runtime switchable: `POST /api/admin/log-level` with `{"level": "debug"}`.
- Startup default: `CYODA_LOG_LEVEL` env var, defaults to `info`.
