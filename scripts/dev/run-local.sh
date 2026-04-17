#!/bin/bash
# Run Cyoda-Go locally with the 'local' profile.
# Override with: CYODA_PROFILES=postgres,otel ./scripts/dev/run-local.sh
CYODA_PROFILES=${CYODA_PROFILES:-local} go run ./cmd/cyoda-go "$@"
