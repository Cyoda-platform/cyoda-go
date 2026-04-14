#!/bin/bash
#
# Stop the multi-node cyoda-go cluster.
# Usage: ./stop-cluster.sh [-v|--volumes]
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

cd "$SCRIPT_DIR"

COMPOSE_FILE="docker-compose.generated.yml"
if [ ! -f "$COMPOSE_FILE" ]; then
    log_warn "No docker-compose.generated.yml found. Nothing to stop."
    exit 0
fi

log_info "Stopping cluster..."

if [ "$1" == "--volumes" ] || [ "$1" == "-v" ]; then
    log_info "Removing containers and volumes..."
    docker compose -f "$COMPOSE_FILE" down -v
else
    docker compose -f "$COMPOSE_FILE" down
fi

log_info "Cluster stopped."
