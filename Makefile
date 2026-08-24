.PHONY: lint lint-schema modernize fix test test-integration check

## lint: format check + static analysis (matches CI)
lint:
	@echo "==> Checking gofmt..."
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	@echo "==> Running go vet..."
	go vet -tags=integration ./...
	# TEMPORARY (Go 1.27): golangci-lint is disabled below because no released or
	# HEAD build can decode Go 1.27's unified-IR export-data (format version 4,
	# added for generic methods) — it fails to import `internal/cpu` via `math`
	# ("export data version 4 is greater than maximum supported version 2"),
	# which nearly every package imports. Support is landing upstream
	# (golangci/golangci-lint main @1b907273167e, 2026-08-23) but is not yet
	# complete. `go vet` above is the interim static-analysis gate. Restore the
	# line below (and the golangci-lint-action step in .github/workflows/lint.yml)
	# once a golangci-lint release fully lints a `go 1.27` module.
	# @echo "==> Running golangci-lint..."
	# golangci-lint run --timeout=3m --build-tags=integration ./...

## lint-schema: check schema.sql against design policies
lint-schema:
	bash scripts/lint-schema.sh

## modernize: check that code uses modern Go idioms (go fix)
modernize:
	@echo "==> Checking go fix modernizers..."
	@test -z "$$(go fix -diff ./... 2>&1)" || (echo "Run 'go fix ./...' to modernize code"; go fix -diff ./...; exit 1)

## fix: auto-apply go fix modernizers, then format
fix:
	@echo "==> Applying go fix modernizers..."
	go fix ./...
	go fix ./...
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
check: lint lint-schema modernize test
