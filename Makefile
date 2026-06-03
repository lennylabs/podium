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

.PHONY: help test test-live test-live-external test-auth-dex bench build \
        lint update-golden \
        speccov speccov-uncovered speccov-drift speccov-report \
        coverage coverage-budget coverage-per-package coverage-gate \
        matrix matrix-list matrix-audit matrix-scaffold \
        services-up services-down services-logs services-status \
        dex-up dex-down \
        tools clean

help:
	@echo "Podium make targets:"
	@echo "  test             Run the full Go test suite"
	@echo "  test-live        Run the suite with env vars pointing at docker-compose services"
	@echo "  test-live-external  Run the suite against managed vector/embedding services (PODIUM_LIVE_EXTERNAL=1)"
	@echo "  test-auth-dex    Bring up the bundled Dex and run the live device-code login e2e"
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
	@echo "  services-up      Start Postgres + MinIO (+ bucket) for live tests"
	@echo "  services-down    Stop the local services (keeps volumes)"
	@echo "  services-logs    Tail logs from the local services"
	@echo "  services-status  Show the local service container status"
	@echo "  dex-up           Start only the bundled Dex IdP (issuer http://localhost:5556/dex)"
	@echo "  dex-down         Stop and remove the bundled Dex IdP"
	@echo "  build            Build podium, podium-server, podium-mcp into ./bin/ with version metadata"
	@echo "  tools            Build the helper binaries to ./bin/"
	@echo "  clean            Remove build artifacts"

# ----- Test lanes ------------------------------------------------------------

# Single-lane test target: the entire Go suite runs in one invocation.
# Tier 2 integration tests gate themselves on PODIUM_LIVE_* env vars and
# are skipped by default. Use `make test-live` to opt in.
test:
	$(GO) test $(GOFLAGS) -count=1 ./...

# Run the full suite with env vars pointing at the local docker-compose
# services (see docker-compose.yml + `make services-up`). Tests that
# exercise real Postgres / S3 take the live path; everything else runs
# unchanged. Override any of LIVE_* below to point at a different
# backend (managed Postgres, real S3, etc.).
#
# Sigstore live tests stay manual; see RELEASING.md for the env vars
# to set when validating signing changes before a release.
LIVE_POSTGRES_DSN ?= postgres://podium:podium@localhost:5432/podium?sslmode=disable
LIVE_S3_ENDPOINT  ?= http://localhost:9000
LIVE_S3_BUCKET    ?= podium
LIVE_S3_KEY       ?= minioadmin
LIVE_S3_SECRET    ?= minioadmin
LIVE_S3_USE_SSL   ?= false

test-live:
	PODIUM_POSTGRES_DSN="$(LIVE_POSTGRES_DSN)" \
	PODIUM_S3_ENDPOINT="$(LIVE_S3_ENDPOINT)" \
	PODIUM_S3_BUCKET="$(LIVE_S3_BUCKET)" \
	PODIUM_S3_ACCESS_KEY_ID="$(LIVE_S3_KEY)" \
	PODIUM_S3_SECRET_ACCESS_KEY="$(LIVE_S3_SECRET)" \
	PODIUM_S3_USE_SSL="$(LIVE_S3_USE_SSL)" \
	$(GO) test $(GOFLAGS) -count=1 ./...

# Run the full suite against managed vector backends (Pinecone, Weaviate
# Cloud, Qdrant Cloud) and embedding providers (OpenAI, Cohere, Voyage,
# Ollama). PODIUM_LIVE_EXTERNAL=1 is the master switch; each managed
# vector/embedding live test additionally gates on its own per-service
# credentials and skips with a reason when they are absent, so a partial
# credential set runs only the subset it can reach. Per-service credentials
# are read from the ambient environment (CI injects them from secrets; set
# them in your shell for local runs). The vars are forwarded explicitly
# below so the credential surface this lane consumes is legible; an unset
# var stays empty and its sub-suite skips.
#
# Postgres and S3 are forwarded too so the pgvector depth tests and the
# managed-stack e2e take their live paths when a stack is running (the
# release workflow sets these to its service values).
test-live-external:
	PODIUM_LIVE_EXTERNAL=1 \
	PODIUM_VECTOR_BACKEND="$$PODIUM_VECTOR_BACKEND" \
	PODIUM_EMBEDDING_PROVIDER="$$PODIUM_EMBEDDING_PROVIDER" \
	PODIUM_EMBEDDING_MODEL="$$PODIUM_EMBEDDING_MODEL" \
	PODIUM_PINECONE_API_KEY="$$PODIUM_PINECONE_API_KEY" \
	PODIUM_PINECONE_INDEX="$$PODIUM_PINECONE_INDEX" \
	PODIUM_PINECONE_HOST="$$PODIUM_PINECONE_HOST" \
	PODIUM_PINECONE_NAMESPACE="$$PODIUM_PINECONE_NAMESPACE" \
	PODIUM_PINECONE_INFERENCE_MODEL="$$PODIUM_PINECONE_INFERENCE_MODEL" \
	PODIUM_PINECONE_CONTROL_PLANE="$$PODIUM_PINECONE_CONTROL_PLANE" \
	PODIUM_WEAVIATE_URL="$$PODIUM_WEAVIATE_URL" \
	PODIUM_WEAVIATE_API_KEY="$$PODIUM_WEAVIATE_API_KEY" \
	PODIUM_WEAVIATE_COLLECTION="$$PODIUM_WEAVIATE_COLLECTION" \
	PODIUM_WEAVIATE_VECTORIZER="$$PODIUM_WEAVIATE_VECTORIZER" \
	PODIUM_QDRANT_URL="$$PODIUM_QDRANT_URL" \
	PODIUM_QDRANT_API_KEY="$$PODIUM_QDRANT_API_KEY" \
	PODIUM_QDRANT_COLLECTION="$$PODIUM_QDRANT_COLLECTION" \
	PODIUM_QDRANT_INFERENCE_MODEL="$$PODIUM_QDRANT_INFERENCE_MODEL" \
	OPENAI_API_KEY="$$OPENAI_API_KEY" \
	PODIUM_OPENAI_MODEL="$$PODIUM_OPENAI_MODEL" \
	PODIUM_OPENAI_BASE_URL="$$PODIUM_OPENAI_BASE_URL" \
	PODIUM_OPENAI_ORG="$$PODIUM_OPENAI_ORG" \
	COHERE_API_KEY="$$COHERE_API_KEY" \
	PODIUM_COHERE_MODEL="$$PODIUM_COHERE_MODEL" \
	VOYAGE_API_KEY="$$VOYAGE_API_KEY" \
	PODIUM_VOYAGE_MODEL="$$PODIUM_VOYAGE_MODEL" \
	PODIUM_OLLAMA_URL="$$PODIUM_OLLAMA_URL" \
	PODIUM_OLLAMA_MODEL="$$PODIUM_OLLAMA_MODEL" \
	PODIUM_POSTGRES_DSN="$$PODIUM_POSTGRES_DSN" \
	PODIUM_S3_ENDPOINT="$$PODIUM_S3_ENDPOINT" \
	PODIUM_S3_BUCKET="$$PODIUM_S3_BUCKET" \
	PODIUM_S3_ACCESS_KEY_ID="$$PODIUM_S3_ACCESS_KEY_ID" \
	PODIUM_S3_SECRET_ACCESS_KEY="$$PODIUM_S3_SECRET_ACCESS_KEY" \
	PODIUM_S3_USE_SSL="$$PODIUM_S3_USE_SSL" \
	$(GO) test $(GOFLAGS) -count=1 ./...

# ----- Local services for live tests ---------------------------------------

# Honors `docker compose` (v2) by default; set DOCKER_COMPOSE=docker-compose
# to use the legacy v1 CLI.
DOCKER_COMPOSE ?= docker compose

# Live tests only need Postgres + MinIO + the bucket bootstrap. Naming
# them explicitly keeps `make services-up` from building the registry
# image and starting Dex (the full evaluation stack `docker compose up -d`
# brings up; see docker-compose.yml).
services-up:
	$(DOCKER_COMPOSE) up -d postgres minio bootstrap
	@echo "Services starting; check status with: make services-status"

services-down:
	$(DOCKER_COMPOSE) down

services-logs:
	$(DOCKER_COMPOSE) logs -f

services-status:
	$(DOCKER_COMPOSE) ps

# ----- Bundled Dex IdP for the device-code login e2e ------------------------

# `make services-up` deliberately excludes Dex; the device-code login e2e is
# the only test that needs it, so it gets its own targets. Dex has no
# dependencies and stores to an ephemeral SQLite file, so `dex-up` alone is
# enough to exercise `podium login`. The issuer is reached from the host at
# http://localhost:5556/dex (the compose file maps container port 5556).
dex-up:
	$(DOCKER_COMPOSE) up -d dex
	@echo "Dex starting at http://localhost:5556/dex; readiness is probed by the test."

dex-down:
	$(DOCKER_COMPOSE) rm -sf dex

# Bring up the bundled Dex and run the live device-code login e2e against it.
# The test (test/e2e/dex_login_test.go) drives the real `podium login`
# RFC 8628 device-code flow against Dex (http://localhost:5556/dex),
# programmatically completes the device approval as the static user, and
# asserts login obtains a token and prints the issued sub/email. It self-skips
# when Dex is unreachable, so this target is the supported way to run it.
# PODIUM_LIVE_DEX=1 marks the opt-in for parity with the other live lanes; the
# test gates on Dex reachability regardless. -run pins the single test so the
# rest of the suite is not pulled in.
test-auth-dex: dex-up
	PODIUM_LIVE_DEX=1 $(GO) test $(GOFLAGS) -count=1 -run TestDexLogin ./test/e2e/...

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
