# compose-with-observability (dev convenience)

Runs cyoda-go alongside a bundled Grafana+Prometheus+Tempo stack
(`grafana/otel-lgtm`) for local observability development.

Not for production. Grafana here is unauthenticated; Postgres is
seeded with dev credentials; the image tag tracks `:latest`. The
canonical Docker provisioning artifact lives at
`deploy/docker/compose.yaml` and does not bundle observability —
operators point cyoda-go at their own telemetry backend via OTLP or
scrape `/metrics` directly.

Usage:

    docker compose up

Grafana is exposed on http://127.0.0.1:3000.
