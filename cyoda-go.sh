#!/bin/bash
# Run Cyoda-Go locally with the 'local' profile.
# Override with: CYODA_PROFILES=postgres,otel ./cyoda-go.sh
CYODA_PROFILES=${CYODA_PROFILES:-local} go run ./cmd/cyoda-go "$@"
