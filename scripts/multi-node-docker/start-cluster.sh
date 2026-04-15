#!/bin/bash
#
# Start a multi-node cyoda-go cluster with nginx load balancer.
# Handles secret generation, nginx config, and docker-compose orchestration.
#
# Usage: ./start-cluster.sh [NUM_NODES]       (default: 3)
#        ./start-cluster.sh --nodes NUM_NODES
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Parse arguments: accept both positional and --nodes flag
NUM_NODES=3
while [[ $# -gt 0 ]]; do
    case "$1" in
        --nodes)  NUM_NODES="$2"; shift 2 ;;
        --nodes=*) NUM_NODES="${1#*=}"; shift ;;
        -d|--detach) EXTRA_ARGS+=("$1"); shift ;;
        [0-9]*) NUM_NODES="$1"; shift ;;
        *) EXTRA_ARGS+=("$1"); shift ;;
    esac
done

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

if [[ "$NUM_NODES" -lt 1 || "$NUM_NODES" -gt 20 ]]; then
    log_error "--nodes must be between 1 and 20 (got $NUM_NODES)"
    exit 1
fi

log_info "Preparing cyoda-go cluster with $NUM_NODES node(s)"

# ── Secrets (generate once, persist to .env, reuse on restart) ────────
ENV_FILE="$SCRIPT_DIR/.env"
if [[ -f "$ENV_FILE" ]]; then
    log_info "Loading secrets from $ENV_FILE"
    source "$ENV_FILE"
else
    log_info "First run — generating secrets and saving to $ENV_FILE"
fi

# Env vars override persisted values; persisted values override defaults
JWT_KEY_B64="${JWT_KEY_B64:-$(openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 2>/dev/null | base64 | tr -d '\n')}"
HMAC_SECRET="${HMAC_SECRET:-$(openssl rand -hex 32)}"
BOOTSTRAP_CLIENT_ID="${CYODA_BOOTSTRAP_CLIENT_ID:-${BOOTSTRAP_CLIENT_ID:-m2m.user}}"
BOOTSTRAP_CLIENT_SECRET="${CYODA_BOOTSTRAP_CLIENT_SECRET:-${BOOTSTRAP_CLIENT_SECRET:-$(openssl rand -hex 32)}}"
BOOTSTRAP_TENANT_ID="${CYODA_BOOTSTRAP_TENANT_ID:-${BOOTSTRAP_TENANT_ID:-riskblocs}}"
BOOTSTRAP_ROLES="${CYODA_BOOTSTRAP_ROLES:-${BOOTSTRAP_ROLES:-ROLE_ADMIN,ROLE_M2M}}"

# Persist for next run
cat > "$ENV_FILE" <<ENVEOF
# Auto-generated cluster config — stable across restarts. Delete this file to regenerate.
JWT_KEY_B64=${JWT_KEY_B64}
HMAC_SECRET=${HMAC_SECRET}
BOOTSTRAP_CLIENT_ID=${BOOTSTRAP_CLIENT_ID}
BOOTSTRAP_CLIENT_SECRET=${BOOTSTRAP_CLIENT_SECRET}
BOOTSTRAP_TENANT_ID=${BOOTSTRAP_TENANT_ID}
BOOTSTRAP_ROLES=${BOOTSTRAP_ROLES}
ENVEOF

# ── Ports (from env, falling back to single-node defaults) ───────────
HTTP_PORT="${CYODA_HTTP_PORT:-8123}"
GRPC_PORT="${CYODA_GRPC_PORT:-9123}"
GOSSIP_PORT=7946
LB_HTTP_PORT="$HTTP_PORT"
LB_GRPC_PORT="$GRPC_PORT"

# ── Seed nodes: first min(N,3) ──────────────────────────────────────
SEED_COUNT=$((NUM_NODES < 3 ? NUM_NODES : 3))
SEED_LIST=""
for i in $(seq 1 "$SEED_COUNT"); do
    [[ -n "$SEED_LIST" ]] && SEED_LIST+=","
    SEED_LIST+="cyoda-go-node-${i}:${GOSSIP_PORT}"
done

# ── Generate nginx.conf ─────────────────────────────────────────────
generate_nginx_conf() {
    local num_nodes=$1
    local nginx_conf="$SCRIPT_DIR/nginx.conf"

    log_info "Generating nginx.conf for $num_nodes nodes..."

    cat > "$nginx_conf" << 'NGINX_HEADER'
worker_processes auto;

events {
    worker_connections 4096;
    multi_accept on;
}

http {
    log_format upstream_log '[$time_local] '
                          '$remote_addr -> $upstream_addr '
                          '"$request" $status '
                          'upstream_status=$upstream_status '
                          'upstream_time=$upstream_response_time';

    access_log /dev/stdout upstream_log;
    error_log  /dev/stderr;

    client_max_body_size 16m;

    # HTTP upstream for cyoda-go nodes
    upstream minicyoda_http {
NGINX_HEADER

    for i in $(seq 1 "$num_nodes"); do
        echo "        server cyoda-go-node-${i}:${HTTP_PORT} max_fails=3 fail_timeout=30s;" >> "$nginx_conf"
    done

    cat >> "$nginx_conf" << 'NGINX_HTTP_FOOTER'
        keepalive 32;
        keepalive_timeout 300s;
        keepalive_requests 10000;
    }

    # gRPC upstream for cyoda-go nodes
    upstream minicyoda_grpc {
NGINX_HTTP_FOOTER

    for i in $(seq 1 "$num_nodes"); do
        echo "        server cyoda-go-node-${i}:${GRPC_PORT} max_fails=3 fail_timeout=30s;" >> "$nginx_conf"
    done

    cat >> "$nginx_conf" << NGINX_FOOTER
    }

    # HTTP server
    server {
        listen ${HTTP_PORT};
        server_name localhost;

        location /health {
            access_log off;
            return 200 'OK';
            add_header Content-Type text/plain;
        }

        location / {
            proxy_pass http://minicyoda_http;
            proxy_http_version 1.1;
            proxy_set_header Host \$host;
            proxy_set_header X-Real-IP \$remote_addr;
            proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto \$scheme;
            proxy_set_header X-Tx-Token \$http_x_tx_token;
            proxy_connect_timeout 10s;
            proxy_send_timeout 60s;
            proxy_read_timeout 60s;
            proxy_buffer_size 128k;
            proxy_buffers 4 256k;
            proxy_busy_buffers_size 256k;
        }
    }

    # gRPC server (HTTP/2 for grpc_pass)
    server {
        listen ${GRPC_PORT} http2;
        server_name localhost;

        location / {
            grpc_pass grpc://minicyoda_grpc;
            grpc_read_timeout 3600s;
            grpc_send_timeout 3600s;
            grpc_connect_timeout 10s;
            grpc_socket_keepalive on;
            grpc_set_header X-Real-IP \$remote_addr;
            grpc_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
            grpc_next_upstream off;
            error_page 502 503 504 = @grpc_error;
        }

        location @grpc_error {
            internal;
            default_type application/grpc;
            add_header grpc-status 14;
            add_header grpc-message "Backend service temporarily unavailable";
            add_header content-type application/grpc;
            return 204;
        }
    }
}
NGINX_FOOTER
}

# ── Generate docker-compose.generated.yml ────────────────────────────
generate_docker_compose() {
    local num_nodes=$1
    local compose_file="$SCRIPT_DIR/docker-compose.generated.yml"

    log_info "Generating docker-compose.generated.yml for $num_nodes nodes..."

    cat > "$compose_file" << COMPOSE_HEADER
# Auto-generated by start-cluster.sh — do not edit manually.

x-minicyoda-common: &minicyoda-common
  build:
    context: ${PROJECT_ROOT}
    dockerfile: Dockerfile
  networks:
    - minicyoda-network

x-minicyoda-env: &minicyoda-env
  CYODA_HTTP_PORT: "${HTTP_PORT}"
  CYODA_GRPC_PORT: "${GRPC_PORT}"
  CYODA_LOG_LEVEL: "info"
  CYODA_STORAGE_BACKEND: "postgres"
  CYODA_POSTGRES_URL: "postgres://minicyoda:minicyoda@postgres:5432/minicyoda?sslmode=disable"
  CYODA_POSTGRES_AUTO_MIGRATE: "true"
  CYODA_IAM_MODE: "jwt"
  CYODA_JWT_SIGNING_KEY: "${JWT_KEY_B64}"
  CYODA_BOOTSTRAP_CLIENT_ID: "${BOOTSTRAP_CLIENT_ID}"
  CYODA_BOOTSTRAP_CLIENT_SECRET: "${BOOTSTRAP_CLIENT_SECRET}"
  CYODA_BOOTSTRAP_TENANT_ID: "${BOOTSTRAP_TENANT_ID}"
  CYODA_BOOTSTRAP_ROLES: "${BOOTSTRAP_ROLES}"
  CYODA_OTEL_ENABLED: "true"
  OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel-backend:4318"

services:
  # WARNING: Grafana is unauthenticated by default. Do NOT expose to untrusted networks.
  otel-backend:
    image: grafana/otel-lgtm:latest
    container_name: minicyoda-otel
    ports:
      - "127.0.0.1:3000:3000"
    volumes:
      - ${PROJECT_ROOT}/scripts/grafana/dashboards:/otel-lgtm/grafana/dashboards/cyoda-go:ro
      - ${PROJECT_ROOT}/scripts/grafana/provisioning/dashboards/default.yml:/otel-lgtm/grafana/conf/provisioning/dashboards/cyoda-go.yml:ro
    networks:
      - minicyoda-network

  postgres:
    image: postgres:17-alpine
    container_name: minicyoda-postgres
    environment:
      POSTGRES_DB: minicyoda
      POSTGRES_USER: minicyoda
      POSTGRES_PASSWORD: minicyoda
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U minicyoda -d minicyoda"]
      interval: 2s
      timeout: 5s
      retries: 10
    networks:
      - minicyoda-network

  load-balancer:
    image: nginx:alpine
    container_name: minicyoda-lb
    ports:
      - "${LB_HTTP_PORT}:${LB_HTTP_PORT}"
      - "${LB_GRPC_PORT}:${LB_GRPC_PORT}"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
    depends_on:
COMPOSE_HEADER

    for i in $(seq 1 "$num_nodes"); do
        cat >> "$compose_file" << DEPENDS
      cyoda-go-node-${i}:
        condition: service_started
DEPENDS
    done

    cat >> "$compose_file" << 'LB_FOOTER'
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:${LB_HTTP_PORT}/health"]
      interval: 10s
      timeout: 5s
      retries: 3
    networks:
      - minicyoda-network

LB_FOOTER

    # Generate each node
    for i in $(seq 1 "$num_nodes"); do
        if [[ "$num_nodes" -gt 1 ]]; then
            cat >> "$compose_file" << NODE_CLUSTER
  cyoda-go-node-${i}:
    <<: *minicyoda-common
    container_name: minicyoda-node${i}
    environment:
      <<: *minicyoda-env
      CYODA_CLUSTER_ENABLED: "true"
      CYODA_NODE_ID: "node-${i}"
      CYODA_NODE_ADDR: "http://cyoda-go-node-${i}:${HTTP_PORT}"
      CYODA_GOSSIP_ADDR: "0.0.0.0:${GOSSIP_PORT}"
      CYODA_SEED_NODES: "${SEED_LIST}"
      CYODA_HMAC_SECRET: "${HMAC_SECRET}"
      CYODA_TX_TTL: "60s"
    depends_on:
      postgres:
        condition: service_healthy

NODE_CLUSTER
        else
            cat >> "$compose_file" << NODE_SINGLE
  cyoda-go-node-${i}:
    <<: *minicyoda-common
    container_name: minicyoda-node${i}
    environment:
      <<: *minicyoda-env
    depends_on:
      postgres:
        condition: service_healthy

NODE_SINGLE
        fi
    done

    cat >> "$compose_file" << 'FOOTER'
volumes:
  pgdata:

networks:
  minicyoda-network:
    driver: bridge
FOOTER
}

# ── Generate ──────────────────────────────────────────────────────────
generate_nginx_conf "$NUM_NODES"
generate_docker_compose "$NUM_NODES"

log_info "Starting cluster with $NUM_NODES node(s)..."
log_info "Endpoints:"
log_info "  HTTP: http://localhost:${LB_HTTP_PORT}"
log_info "  gRPC: localhost:${LB_GRPC_PORT}"
if [[ "$NUM_NODES" -gt 1 ]]; then
    log_info "Cluster:"
    log_info "  Seed nodes: $(seq -f 'node-%.0f' -s ', ' 1 "$SEED_COUNT")"
    log_info "  HMAC secret: ${HMAC_SECRET:0:8}..."
fi

cd "$SCRIPT_DIR"
docker compose -f docker-compose.generated.yml up --build "${EXTRA_ARGS[@]}"
