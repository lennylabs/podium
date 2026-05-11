# Contributing to Podium

Thanks for your interest in Podium.

## Ways to contribute

- **Open an issue or start a discussion.** Questions, bug reports, missing use cases, and concrete suggestions are all welcome. File an [issue](https://github.com/lennylabs/podium/issues) or start a [discussion](https://github.com/lennylabs/podium/discussions).
- **Submit a pull request.** For non-trivial changes, please open an issue or discussion first so we can align on direction before you invest time in code.
- **Fix typos and broken links.** Small documentation PRs are welcome anytime. Keep them focused.

## Development setup

### Prerequisites

- Go 1.26 or later for the registry, CLI, and MCP server.
- Python 3.10 or later for the `podium-py` SDK.
- Node.js 20 or later for the `@lennylabs/podium-sdk` TypeScript SDK.
- GNU make.

### Build

```bash
go build ./...
```

This builds every Go binary in the module (`podium`, `podium-server`, `podium-mcp`) into the Go build cache. Pass `-o` to write a specific binary to disk:

```bash
go build -o bin/podium ./cmd/podium
```

### Test

The Go suite runs in a single lane:

```bash
make test
```

The full suite completes in roughly 10 seconds on a recent laptop. The Tier 2 lane exercises real external services (Postgres, S3-compatible object stores, Sigstore, embedding providers) and is opt-in:

```bash
make test-live
```

Tier 2 tests inspect `PODIUM_LIVE_*` environment variables and self-skip when the corresponding service is not configured. The CI environment provisions these services; local runs can configure them via the variables documented in each `*_live_test.go` file.

The SDK suites run independently:

```bash
cd sdks/podium-py
pip install -e .
pytest

cd sdks/podium-ts
npm install
npm test
```

### Spec coverage and matrix audit

Tests carry a `// Spec: §X.Y` annotation that ties them to a spec section. The reporters under `tools/` use those annotations:

```bash
make speccov         # Spec-section coverage report.
make speccov-drift   # Fail if any test cites a missing section.
make matrix-audit    # Audit per-cell coverage of the documented spec matrices.
make coverage-gate   # Run the full CI gate locally (lint + drift + matrix + coverage).
```

`make help` lists every target.

### Adding a test

A new test should carry a `// Spec:` annotation pointing to the spec section it exercises. Multi-cite is supported via `Spec: §A / §B — note`. Tests that have no spec correspondence use `Spec: n/a — <reason>`.

## Releases

Maintainer-facing notes on cutting a release (versioning, tagging, CI workflow, patch flow) live in [`RELEASING.md`](RELEASING.md). External-system setup (PyPI Trusted Publisher, npm token, GHCR, future-infra checklist) lives in [`OPERATIONS.md`](OPERATIONS.md).

## Ground rules

- **License:** Podium is [MIT-licensed](LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin (DCO):** sign off each commit with `git commit -s`. No separate CLA.
- **Code of Conduct:** participation is subject to the [Contributor Covenant](CODE_OF_CONDUCT.md).
- **Security issues:** do not file public issues for vulnerabilities. See [SECURITY.md](SECURITY.md) for the disclosure process.

## Getting help

- Documentation: [`docs/`](docs/)
- Governance and decision-making: [`GOVERNANCE.md`](GOVERNANCE.md)
