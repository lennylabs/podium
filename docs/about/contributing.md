---
layout: default
title: Contributing
parent: About
nav_order: 3
description: How to contribute to Podium today. License, DCO, code of conduct.
---

# Contributing to Podium

Thanks for your interest in Podium.

## Ways to contribute

- **Open an issue or start a discussion.** Questions, bug reports, missing use cases, and concrete suggestions are all welcome. File an [issue](https://github.com/lennylabs/podium/issues) or start a [discussion](https://github.com/lennylabs/podium/discussions).
- **Submit a pull request.** For non-trivial changes, please open an issue or discussion first so we can align on direction before you invest time in code.
- **Fix typos and broken links.** Small documentation PRs are welcome anytime. Keep them focused.

For what's most useful to contribute today, see [Implementation status](status).

## Development setup

### Prerequisites

- Go 1.26 or later for the registry, CLI, and MCP server.
- Python 3.10 or later for the `podium-py` SDK.
- Node.js 20 or later for the `@podium/sdk` TypeScript SDK.
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

Tier 2 tests inspect `PODIUM_LIVE_*` environment variables and self-skip when the corresponding service is not configured.

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

A new test should carry a `// Spec:` annotation pointing to the spec section it exercises. Multi-cite is supported via `Spec: §A / §B — note`. Tests that have no spec correspondence use `Spec: n/a — <reason>`.

## Ground rules

- **License.** Podium is [MIT-licensed](https://github.com/lennylabs/podium/blob/main/LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin (DCO).** Sign off each commit with `git commit -s`. No separate CLA.
- **Code of Conduct.** Participation is subject to the [Contributor Covenant](https://github.com/lennylabs/podium/blob/main/CODE_OF_CONDUCT.md).
- **Security issues.** Do not file public issues for vulnerabilities. See the [security policy](https://github.com/lennylabs/podium/blob/main/SECURITY.md) for the disclosure process.

## Getting help

- Documentation index: [Home](../).
- Governance and decision-making: [Governance](governance).
