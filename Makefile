.PHONY: lint lint-schema modernize fix test test-integration test-stripe-e2e check

# golangci-lint is installed as the official release binary, the method its
# docs recommend (https://golangci-lint.run/docs/welcome/install/local/); a
# go.mod `tool` install is unsupported upstream. This is the single version pin
# for local runs, the pre-commit hook and CI, and Renovate updates it (see the
# custom manager in renovate.json). The versioned file name makes a bump
# reinstall on the next `make lint`.
GOLANGCI_LINT_VERSION := v2.14.0
GOLANGCI_LINT := bin/golangci-lint-$(GOLANGCI_LINT_VERSION)

## lint: format check + static analysis (matches CI)
lint: $(GOLANGCI_LINT)
	@echo "==> Checking gofmt..."
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	@echo "==> Running go vet..."
	go vet -tags=integration ./...
	@echo "==> Running golangci-lint..."
	$(GOLANGCI_LINT) run --timeout=3m --build-tags=integration ./...

# The install script is taken from the same tag as the binary, and it verifies
# the release tarball against the release's checksums.
$(GOLANGCI_LINT):
	@echo "==> Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."
	@rm -rf bin/.golangci-lint-tmp
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh | sh -s -- -b bin/.golangci-lint-tmp $(GOLANGCI_LINT_VERSION)
	mv bin/.golangci-lint-tmp/golangci-lint $@
	@rm -rf bin/.golangci-lint-tmp

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

## test-stripe-e2e: opt-in Stripe SANDBOX (test-mode) end-to-end — NO real money.
## Requires a test-mode key in STRIPE_SECRET_KEY (rk_test_…/sk_test_…); charges use
## test cards and fake balances. Runs the ④ authorize→capture integration test and
## the Connect settlement money-out PoC (self-provisions its recipient account).
## See docs/stripe-sandbox-e2e.md for setup and what each leg verifies.
test-stripe-e2e:
	STRIPE_INTEGRATION_TEST=1 STRIPE_CONNECT_POC=1 \
		go test -count=1 -v -run 'Integration|Settlement_PoC' ./internal/infrastructure/payment/

## check: full local pre-commit check (lint + schema lint + test)
check: lint lint-schema modernize test
