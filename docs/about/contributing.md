---
title: Contributing
nav_order: 2
description: How to contribute to Podium, set up a development environment, and run the tests, plus the license, DCO, and code of conduct.
---

# Contributing to Podium

This page covers how to contribute to Podium: the ways to take part, the development setup, the test and coverage commands, and the ground rules every contribution follows.

## Ways to contribute

- **Open an issue or start a discussion.** Questions, bug reports, missing use cases, and concrete suggestions are welcome. File an [issue](https://github.com/lennylabs/podium/issues) or start a [discussion](https://github.com/lennylabs/podium/discussions).
- **Submit a pull request.** For a non-trivial change, open an issue or discussion first, so the direction is agreed before the code is written.
- **Fix typos and broken links.** Small, focused documentation PRs are welcome at any time.

[Implementation status](status#what-contributions-help-most-today) lists the contributions that help most today.

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

The command builds every Go binary in the module (`podium`, `podium-server`, and `podium-mcp`) into the Go build cache. Pass `-o` to write a specific binary to disk:

```bash
go build -o bin/podium ./cmd/podium
```

### Test

The Go suite runs in a single lane:

```bash
make test
```

The full suite completes in one to two minutes on a recent laptop. Two opt-in lanes exercise real external services:

```bash
make test-live           # Postgres and MinIO, started by `make services-up`.
make test-live-external  # Managed vector backends and embedding providers.
```

`make test-live` points the suite at the local `docker-compose` services and sets `PODIUM_POSTGRES_DSN` and the `PODIUM_S3_*` variables. `make test-live-external` sets `PODIUM_LIVE_EXTERNAL=1` and forwards the per-service credentials from the environment. [Live vector backend tests](live-vector-backends) covers the accounts, collections, and credentials that lane needs for the managed vector backends, and the CI workflow that runs it. Each live test self-skips with a reason when its service is not configured.

The Sigstore live checks stay manual. `RELEASING.md` records the variables to set before a signing-path release.

The SDK suites run independently:

```bash
cd sdks/podium-py
pip install -e '.[verify]'
pip install pytest
pytest

cd sdks/podium-ts
npm install
npm test
```

### Spec coverage and matrix audit

Each test carries a `// Spec: §X.Y` annotation that ties it to a spec section. The reporters under `tools/` read those annotations:

```bash
make speccov         # Spec-section coverage report.
make speccov-drift   # Fail if any test cites a missing section.
make matrix-audit    # Audit per-cell coverage of the documented spec matrices.
make coverage-gate   # Run the full CI gate locally (lint + drift + matrix + coverage).
```

`make help` lists every target.

Give a new test a `// Spec:` annotation that points to the spec section it exercises. To cite several sections, write `Spec: §A / §B — note`. A test with no spec correspondence uses `Spec: n/a — <reason>`.

### Documentation site

The documentation pages live in `docs/` as markdown. The TypeScript package in
`site/` generates the site that publishes them, and it needs Node 20 or later.

```bash
make docs-dev     # Rebuild on change and serve on http://localhost:4321/podium/
make docs-check   # Run the validation gate and the generator's tests
make docs-build   # Write the full site into site/dist
```

`make docs-check` reports an unresolvable link, a missing anchor, an unknown
frontmatter key, and an image with no alt text, among other rules. The same
checks run on every pull request that touches `docs/`, `site/`, or `README.md`.
`site/README.md` documents the frontmatter keys, the callout syntax, and the
steps to add a component.

## Ground rules

- **License.** Podium is [MIT-licensed](https://github.com/lennylabs/podium/blob/main/LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin (DCO).** Sign off each commit with `git commit -s`. Podium requires no separate CLA.
- **Code of Conduct.** Participation is subject to the [Contributor Covenant](https://github.com/lennylabs/podium/blob/main/CODE_OF_CONDUCT.md).
- **Security issues.** Do not file public issues for vulnerabilities. See the [security policy](https://github.com/lennylabs/podium/blob/main/SECURITY.md) for the disclosure process.

## Getting help

- [Overview](../overview) is the entry point to the documentation.
- [Governance](governance) describes how decisions are made.
