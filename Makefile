.PHONY: lint lint-schema fix test test-integration check

## lint: format check + golangci-lint (matches CI)
lint:
	@echo "==> Checking gofmt..."
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	@echo "==> Running golangci-lint..."
	golangci-lint run --timeout=3m --build-tags=integration ./...

## lint-schema: check schema.sql against design policies
lint-schema:
	bash scripts/lint-schema.sh

## fix: auto-fix formatting
fix:
	gofmt -w .

## test: unit tests with local DB (docker compose)
test:
	docker compose up -d postgres --wait
	atlas migrate apply --env local
	go test ./...

## test-integration: integration tests (DB must already be running)
## Pass GOTEST_FLAGS for CI-specific options (e.g., coverage)
test-integration:
	go test -tags=integration -race -timeout=5m $(GOTEST_FLAGS) ./...

## check: full local pre-commit check (lint + schema lint + test)
check: lint lint-schema test
