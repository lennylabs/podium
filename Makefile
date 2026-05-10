# Podium build / test orchestration.
#
# Most targets dispatch to `go` and the tools under `tools/`.
#
# Conventions:
#   GOFLAGS extra flags forwarded to `go test`.

SHELL := /bin/bash

GO    ?= go

# Build-time version metadata. Override on the command line:
#   make build VERSION=v0.1.0
# A release pipeline that pushes binaries should set all three.
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# -ldflags injects the values into internal/buildinfo at link time.
LDFLAGS := -X 'github.com/lennylabs/podium/internal/buildinfo.Version=$(VERSION)' \
           -X 'github.com/lennylabs/podium/internal/buildinfo.Commit=$(COMMIT)' \
           -X 'github.com/lennylabs/podium/internal/buildinfo.Date=$(DATE)'

.PHONY: help test test-live bench build \
        lint update-golden \
        speccov speccov-uncovered speccov-drift speccov-report \
        coverage coverage-budget coverage-per-package coverage-gate \
        matrix matrix-list matrix-audit matrix-scaffold \
        tools clean

help:
	@echo "Podium make targets:"
	@echo "  test             Run the full Go test suite (single lane)"
	@echo "  test-live        Run env-gated Tier 2 tests against real Postgres/S3/Sigstore/embedding providers"
	@echo "  bench            Run §7.1 latency benchmarks (informational)"
	@echo "  lint             Run linters (golangci-lint when available)"
	@echo "  update-golden    Re-run tests with UPDATE_GOLDEN=1"
	@echo "  speccov          Print spec-section coverage report"
	@echo "  speccov-uncovered  Print spec sections with no citing test"
	@echo "  speccov-drift    Fail if any test cites a missing spec section"
	@echo "  coverage         Run tests with -coverprofile and print summary"
	@echo "  coverage-budget  Assert overall coverage >= COVERAGE_MIN (default 50)"
	@echo "  coverage-per-package  Print per-package coverage breakdown"
	@echo "  coverage-gate    Run all coverage checks the CI runs"
	@echo "  matrix-audit     Audit spec-table coverage (§6.7.1, §6.10, etc.)"
	@echo "  matrix-list      List the documented spec matrices"
	@echo "  matrix-scaffold  Print Go test stubs for missing matrix cells"
	@echo "  build            Build podium, podium-server, podium-mcp into ./bin/ with version metadata"
	@echo "  tools            Build the helper binaries to ./bin/"
	@echo "  clean            Remove build artifacts"

# ----- Test lanes ------------------------------------------------------------

# Single-lane test target: the entire Go suite runs in one invocation.
# Tier 2 integration tests gate themselves on PODIUM_LIVE_* env vars and
# are skipped by default. Use `make test-live` to opt in.
test:
	$(GO) test $(GOFLAGS) -count=1 ./...

# Tier 2 integration tests against real external services (Postgres, S3,
# Sigstore, embedding providers). The tests inspect PODIUM_LIVE_* env vars
# and skip themselves when the corresponding service is not configured.
test-live:
	PODIUM_LIVE=1 $(GO) test $(GOFLAGS) -count=1 -tags=live ./...

# Run the §7.1 latency benchmark suite. Output is informational;
# CI does not gate on absolute numbers because cloud runners vary.
bench:
	$(GO) test -bench=. -benchmem -benchtime=10x -run=^$$ ./test/bench/...

# ----- Coverage / spec traceability -----------------------------------------

speccov: speccov-report

speccov-report: tools
	@./bin/speccov report

speccov-uncovered: tools
	@./bin/speccov uncovered

speccov-drift: tools
	@./bin/speccov drift

COVERAGE_MIN ?= 50

coverage: tools
	@./bin/coverage report

coverage-budget: tools
	@./bin/coverage budget -min $(COVERAGE_MIN)

coverage-per-package: tools
	@./bin/coverage per-package

# ----- Matrix audit ---------------------------------------------------------

matrix: matrix-audit

matrix-audit: tools
	@./bin/matrix audit

matrix-list: tools
	@./bin/matrix list

matrix-scaffold: tools
	@./bin/matrix scaffold

coverage-gate: lint speccov-drift matrix-audit coverage-budget

# ----- Lint / golden / tools / clean ----------------------------------------

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
	  golangci-lint run; \
	else \
	  echo "golangci-lint not installed; running go vet only"; \
	  $(GO) vet ./...; \
	fi

update-golden:
	UPDATE_GOLDEN=1 $(GO) test $(GOFLAGS) -count=1 ./...

build:
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/podium ./cmd/podium
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/podium-server ./cmd/podium-server
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/podium-mcp ./cmd/podium-mcp

tools:
	@mkdir -p bin
	$(GO) build -o bin/speccov ./tools/speccov
	$(GO) build -o bin/matrix ./tools/matrix
	$(GO) build -o bin/coverage ./tools/coverage

clean:
	rm -rf bin coverage.out
