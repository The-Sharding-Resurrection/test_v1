# Ethereum Sharding Makefile

.PHONY: help storage storage-force benchmark benchmark-quick test docker-up docker-down docker-restart clean

# Default target
help:
	@echo "Ethereum Sharding Experiment - Available targets:"
	@echo ""
	@echo "  make storage          - Generate storage (only if contracts changed)"
	@echo "  make storage-force    - Force regenerate storage"
	@echo "  make benchmark        - Run benchmark (ensures storage exists)"
	@echo "  make benchmark-quick  - Quick benchmark (5s, local only)"
	@echo "  make test             - Run Go tests"
	@echo "  make docker-up        - Start Docker network"
	@echo "  make docker-down      - Stop Docker network"
	@echo "  make docker-restart   - Restart Docker network"
	@echo "  make clean            - Clean generated files"
	@echo ""

# Smart storage regeneration: only regenerate if contracts changed
storage:
	@echo "Checking if storage needs regeneration..."
	@if [ ! -d storage/test_statedb ] || \
	   [ ! -f storage/test_statedb/shard0_root.txt ] || \
	   [ contracts -nt storage/test_statedb ] || \
	   [ storage/create_storage.go -nt storage/test_statedb ]; then \
		echo "Storage is stale, regenerating..."; \
		time go run ./storage/create_storage.go; \
	else \
		echo "Storage is up-to-date, skipping regeneration."; \
	fi

# Force storage regeneration
storage-force:
	@echo "Force regenerating storage..."
	@time go run ./storage/create_storage.go

# Build benchmark binary
benchmark-bin:
	@echo "Building benchmark binary..."
	@go build -o benchmark ./cmd/benchmark

# Run full benchmark (ensures storage and benchmark binary exist)
benchmark: storage benchmark-bin
	@echo "Starting benchmark..."
	@./benchmark -duration 10 -injection-rate 1000 -ct-ratio 0.5 -contract-ratio 0.0

# Quick benchmark (5s, local only)
benchmark-quick: storage benchmark-bin
	@echo "Starting quick benchmark (5s, local only)..."
	@./benchmark -duration 5 -injection-rate 500 -ct-ratio 0.0 -contract-ratio 0.0

# Run Go tests
test:
	@echo "Running Go tests..."
	@go test ./...

# Docker operations
docker-up:
	@echo "Starting Docker network..."
	@docker compose up --build -d
	@echo "Waiting for services to be healthy..."
	@sleep 5
	@docker compose ps

docker-down:
	@echo "Stopping Docker network..."
	@docker compose down

docker-restart:
	@echo "Restarting Docker network..."
	@docker compose restart
	@echo "Waiting for services to be healthy..."
	@sleep 5
	@docker compose ps

# Clean generated files
clean:
	@echo "Cleaning generated files..."
	@rm -rf storage/test_statedb
	@rm -f benchmark
	@rm -f results/*.csv
	@echo "Done."
