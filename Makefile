BIN      := raftly-server
BUILD_DIR := bin
CMD      := ./cmd/server

.PHONY: all build test test-short fmt vet clean \
		docker-build docker-up docker-down docker-logs \
		bench scenario

all: build

## Build the server binary
build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BIN) $(CMD)

## Run all tests with race detector
test:
	go test -race -timeout 180s ./...

## Run only unit/integration tests (skip slow chaos tests)
test-short:
	go test -race -timeout 60s -short ./...

## Format all Go source files
fmt:
	gofmt -w ./..

## Run go vet
vet:
	go vet ./...

## Remove build artifacts
clean:
	rm -rf $(BUILD_DIR)

## Build the Docker image
docker-build:
	docker compose -f docker/docker-compose.yml build

## Start the 3-node cluster + Prometheus + Grafana
docker-up:
	docker compose -f docker/docker-compose.yml up -d
	@echo "Cluster up — HTTP: :8001/:8002/:8003  Metrics: :9090  Grafana: :3000"

## Stop and remove containers (data volumes are kept)
docker-down:
	docker compose -f docker/docker-compose.yml down

## Tail logs from all containers
docker-logs:
	docker compose -f docker/docker-compose.yml logs -f

## Run benchmarks
bench:
	@bash scripts/bench.sh

## Run a named chaos scenario. Usage: make scenario NAME=split-brain-2011
scenario:
	@bash scripts/scenario.sh $(NAME)

Note: Makefile indentation must use tabs, not spaces.