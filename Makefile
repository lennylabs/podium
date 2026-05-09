# Podium build / test orchestration.
#
# This Makefile is the user-facing entry point for the autonomous TDD loop.
# Most targets dispatch to `go` and the tools under `tools/`.
#
# Conventions:
#   PHASE   active phase override; defaults to the contents of .phase.
#   GOFLAGS extra flags forwarded to `go test`.
#
# See TEST_INFRASTRUCTURE_PLAN.md for the design rationale.

SHELL := /bin/bash

PHASE ?= $(shell cat .phase 2>/dev/null || echo 0)
GO    ?= go

TEST_PKGS_FAST   := ./...
TEST_PKGS_MEDIUM := ./test/integration/...
TEST_PKGS_SLOW   := ./test/e2e/... ./test/conformance/...

export PODIUM_PHASE := $(PHASE)

.PHONY: help test-fast test-medium test-slow test-phase test \
        lint update-golden status next advance \
        speccov speccov-uncovered speccov-drift speccov-report \
        coverage coverage-gate \
        tools clean

help:
	@echo "Podium make targets:"
	@echo "  test-fast        Run unit tests in ./... (under one minute)"
	@echo "  test-medium      Run integration tests"
	@echo "  test-slow        Run e2e and conformance suites"
	@echo "  test-phase       Run the test set for PHASE=N (default: .phase)"
	@echo "  test             Alias for test-fast"
	@echo "  status           Print active phase and one-screen summary"
	@echo "  next             Print the next failing test"
	@echo "  advance          Bump .phase if the active phase is fully green"
	@echo "  lint             Run linters (golangci-lint when available)"
	@echo "  update-golden    Re-run tests with UPDATE_GOLDEN=1"
	@echo "  speccov          Print spec-section coverage report"
	@echo "  speccov-uncovered  Print spec sections with no citing test"
	@echo "  speccov-drift    Fail if any test cites a missing spec section"
	@echo "  coverage         Run tests with -coverprofile and print summary"
	@echo "  coverage-gate    Run all coverage checks the CI runs"
	@echo "  tools            Build the helper binaries to ./bin/"
	@echo "  clean            Remove build artifacts"

# ----- Test lanes ------------------------------------------------------------

test: test-fast

test-fast:
	@echo "PODIUM_PHASE=$(PHASE) running fast lane"
	$(GO) test $(GOFLAGS) -count=1 $(TEST_PKGS_FAST)

test-medium:
	@echo "PODIUM_PHASE=$(PHASE) running medium lane"
	$(GO) test $(GOFLAGS) -count=1 -tags=medium $(TEST_PKGS_MEDIUM)

test-slow:
	@echo "PODIUM_PHASE=$(PHASE) running slow lane"
	$(GO) test $(GOFLAGS) -count=1 -tags=slow,medium $(TEST_PKGS_SLOW)

test-phase:
	@$(MAKE) test-fast PHASE=$(PHASE)

# ----- Phase orchestration ---------------------------------------------------

status: tools
	@./bin/phasegate status

next: tools
	@./bin/phasegate next

advance: tools
	@./bin/phasegate advance

# ----- Coverage / spec traceability -----------------------------------------

speccov: speccov-report

speccov-report: tools
	@./bin/speccov report

speccov-uncovered: tools
	@./bin/speccov uncovered

speccov-drift: tools
	@./bin/speccov drift

coverage:
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	@$(GO) tool cover -func=coverage.out | tail -1

coverage-gate: lint speccov-drift coverage

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

tools:
	@mkdir -p bin
	$(GO) build -o bin/speccov ./tools/speccov
	$(GO) build -o bin/phasegate ./tools/phasegate

clean:
	rm -rf bin coverage.out
