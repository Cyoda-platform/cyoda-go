.PHONY: dev-up dev-down dev-ps dev-logs dev-run dev-test build test clean docker-build docker-push todos

# --- Docker services ---

dev-up:                ## Start local services (PostgreSQL)
	docker compose up -d --wait

dev-down:              ## Stop local services
	docker compose down

dev-reset:             ## Stop services and delete volumes (fresh start)
	docker compose down -v

dev-ps:                ## Show service status
	docker compose ps

dev-logs:              ## Tail service logs
	docker compose logs -f

# --- Build & Run ---

build:                 ## Build the binary
	go build -o bin/cyoda-go ./cmd/cyoda-go

dev-run: dev-up build  ## Start services + run cyoda-go with postgres KV
	set -a && . .env.dev && set +a && ./bin/cyoda-go

# --- Docker image ---

TAG        ?= dev
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE      := cyoda-go

docker-build:          ## Build Docker image (TAG=dev)
	docker build \
		--build-arg VERSION=$(TAG) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE):$(TAG) .

docker-push:           ## Tag and push to registry (TAG=, REGISTRY= required)
ifndef REGISTRY
	$(error REGISTRY is required. Usage: make docker-push TAG=1.0.0 REGISTRY=your-registry.example.com)
endif
	docker tag $(IMAGE):$(TAG) $(REGISTRY)/cyoda/$(IMAGE):$(TAG)
	docker push $(REGISTRY)/cyoda/$(IMAGE):$(TAG)

# --- Testing ---

test:                  ## Run all tests (postgres tests skipped without DB)
	go test ./... -v

dev-test: dev-up       ## Run all tests against local postgres
	set -a && . .env.dev && set +a && go test ./... -v -count=1

# --- TODOs ---

todos:                 ## List all TODO(Pn) deferred work items
	@grep -rn "TODO(P" --include="*.go" . | sort || echo "No TODOs found"

todos-p%:              ## List TODOs for a specific plan (e.g. make todos-p6)
	@grep -rn "TODO(P$*" --include="*.go" . | sort || echo "No TODOs for P$*"

# --- Cleanup ---

clean:                 ## Remove build artifacts
	rm -rf bin/ coverage.out

help:                  ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
