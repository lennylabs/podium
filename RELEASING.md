# Releasing Podium

Maintainer-facing notes for cutting a Podium release.

One-time setup steps (PyPI Trusted Publisher, npm token, GHCR access) live in [`OPERATIONS.md`](OPERATIONS.md). The release workflow assumes those are in place.

## Versioning

Podium follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html): `MAJOR.MINOR.PATCH`.

- **MAJOR**: incompatible change to a public surface (CLI flags, HTTP API, manifest schema, SPI signature).
- **MINOR**: backward-compatible addition.
- **PATCH**: backward-compatible fix.

Pre-1.0, the convention loosens: breaking changes can land in MINOR bumps (`0.1.0 → 0.2.0`). Hold the major-bump-on-break rule once `1.0.0` ships.

### Pre-release suffixes

Versions in flight carry a suffix that semver orders before the plain version:

```
0.2.0-dev  <  0.2.0-alpha.1  <  0.2.0-rc.1  <  0.2.0
```

`main` always carries the `-dev` suffix for the next planned release. A plain `go build` on `main` produces a binary that prints, for example, `podium 0.2.0-dev`, which is recognizably unreleased.

## Where the version lives

| Place | Notes |
|:--|:--|
| `internal/buildinfo/buildinfo.go` | Default `Version` constant. Source of truth when no ldflags are passed. **The one place that needs a manual edit per release.** |
| `sdks/podium-py/pyproject.toml` | Marks the version as dynamic; setuptools-scm derives it from the git tag. **No manual edit per release.** |
| `sdks/podium-ts/package.json` | Permanent `"0.0.0-dev"` placeholder. The release workflow rewrites it from the git tag before `npm publish`. **No manual edit per release.** |
| Git tag | Authoritative for a release: `vX.Y.Z`. Drives the Go binary version (via `make build` + `git describe`), the Python wheel version (via setuptools-scm), and the npm package version (via the workflow's `npm version` step). |
| `Makefile` `build` target | Injects `git describe --tags --dirty --always` into the binary via `-ldflags`. |

The two SDKs are fully tag-driven; only `buildinfo.go` requires a manual edit per release. The Go default is in source code so a plain `go build` on `main` still reports a recognizable `0.X.Y-dev` even when no ldflags are passed.

## Cutting a release

1. **Pick the version.** Walk the CHANGELOG and the merged diff since the previous tag. Decide MAJOR, MINOR, or PATCH.

2. **Bump the Go default version** (drop the `-dev` suffix):
   - `internal/buildinfo/buildinfo.go`: `Version = "0.2.0"`.

   The two SDKs need no edit at this step — setuptools-scm and the release workflow's `npm version` step will derive `0.2.0` from the tag.

3. **Update `CHANGELOG.md`.** Move the unreleased section into a dated header `## [0.2.0] - YYYY-MM-DD`. Add the compare link at the bottom.

4. **Commit, tag, push.**
   ```bash
   git commit -am "Release v0.2.0"
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin main
   git push origin v0.2.0
   ```

5. **CI builds and publishes.** The release workflow runs `make build VERSION=v0.2.0` for the Go binaries, `python -m build` (under setuptools-scm) for the Python wheel, and `npm version --no-git-tag-version 0.2.0 && npm publish` for the TS SDK. All three report `0.2.0`. The workflow publishes binaries to GitHub Releases, the wheel + sdist to PyPI, the npm package, and the container image to ghcr.io.

6. **Open the next development cycle.** On `main`, bump the Go default:
   - `buildinfo.go`: `Version = "0.3.0-dev"`.

   Commit as `Begin 0.3.0 development`. Both SDKs pick up the next dev version automatically (`0.2.1.dev<N>+g<sha>` for Python from setuptools-scm; `0.0.0-dev` is the permanent TS local placeholder).

## Patch releases

A patch release branches from the released tag, not from `main`:

```bash
git checkout -b release/0.2.x v0.2.0
# cherry-pick or land the fix
# bump to 0.2.1 in the four version files
git commit -am "Release v0.2.1"
git tag -a v0.2.1 -m "v0.2.1"
git push origin release/0.2.x
git push origin v0.2.1
```

The `main` line continues toward `0.3.0` independently. Merge the fix back into `main` if it isn't already there.

## What CI does on a tag

A push of `vX.Y.Z` triggers the release workflow. A `validate-tag` job runs first and rejects tags whose commit is not reachable from `main` or any `release/*` branch — a safety net that prevents publishing from a feature branch or stale fork.

Once that gate passes, five test jobs run in parallel and every publish job lists all five in `needs:`, so a single failure blocks every artifact:

| Job | Covers |
|:--|:--|
| `go` | Full Go suite: `make lint`, `make speccov-drift`, `make matrix-audit`, `make test`, `make coverage-budget`. |
| `python` | `pytest` against `sdks/podium-py`. |
| `typescript` | `vitest` against `sdks/podium-ts`. |
| `postgres` | Live `pkg/store` and `pkg/vector` against a `pgvector/pgvector:pg16` service container. |
| `s3` | Live `pkg/objectstore` against a MinIO service container. |

Once every test job passes, the workflow:

1. Cross-compiles the binaries for `linux/{amd64,arm64}`, `darwin/arm64`, and `windows/amd64`.
2. Creates a GitHub Release named `vX.Y.Z` with the CHANGELOG section as the body. Pre-release tags (with a hyphen) are marked accordingly.
3. Publishes `podium-sdk` (the PyPI distribution name; imports as `podium`) via Trusted Publishing.
4. Publishes `@lennylabs/podium-sdk` to npm with provenance.
5. Builds and pushes multi-arch container images to `ghcr.io/lennylabs/podium-server`.

The same five test jobs (minus the coverage budget and matrix audit, which are advisory) also run on `nightly.yml` against `main` at 03:00 UTC. A green nightly is the strongest signal that a release-from-`main` will succeed.

The workflow lives in `.github/workflows/release.yml`.

### Sigstore live tests are manual

`pkg/sign/sigstore_live_test.go` is a Tier 2 suite the release gate does not run. The single test in it is `TestSigstoreKeyless_LiveSmoke`. The release gate runs the mocked `pkg/sign/sigstore_test.go` suite (round-trip, tampered hash, foreign trust root, Fulcio outage, and missing Rekor entry), so the signing logic is covered between manual runs; the live smoke adds end-to-end coverage of a real Fulcio certificate and a real Rekor inclusion proof.

**Decision: keep the live smoke manual and documented.** A credentialed CI lane is not wired. The live test gates on an ambient OIDC token (`PODIUM_SIGSTORE_OIDC_TOKEN`) that the configured Fulcio issuer accepts. A CI lane would have to mint a GitHub Actions OIDC token and bind it as a trusted issuer on the Fulcio instance, and even the staging instance writes each signed artifact into a public transparency log. The staging-lane option is recorded below as a follow-up the maintainer can opt into.

**Cadence.** Run the live smoke manually before any release whose diff touches `pkg/sign` or the `SignatureProvider` contract. For releases that do not touch the signing path, the mocked suite in `pkg/sign/sigstore_test.go` is sufficient.

Point the manual run at the Sigstore staging instance (`fulcio.sigstage.dev` / `rekor.sigstage.dev`) rather than production. Staging is free and keeps the public production transparency log clean. The test skips unless `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_OIDC_TOKEN`, and `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` are all set; `PODIUM_SIGSTORE_REKOR_URL` is read when present.

```bash
export PODIUM_SIGSTORE_FULCIO_URL=https://fulcio.sigstage.dev
export PODIUM_SIGSTORE_REKOR_URL=https://rekor.sigstage.dev
export PODIUM_SIGSTORE_OIDC_TOKEN=$(gcloud auth print-identity-token)   # or any IdP the staging issuer accepts
export PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE=/path/to/sigstage-trust-bundle.pem
go test ./pkg/sign/... -count=1 -v -run TestSigstoreKeyless_LiveSmoke
```

The `-run` pattern must name the test exactly. A pattern that matches nothing (for example an outdated `TestSigstore_Live`) reports `PASS` while running zero tests, so the manual smoke would be skipped while appearing to succeed.

#### Staging-Sigstore release lane (future option)

A release-lane job that runs the live smoke against staging Sigstore on every signing-path release is feasible after the OIDC plumbing is in place. It is not wired today. To add it:

- Grant the signing job `permissions: id-token: write`, mint the Actions OIDC token (`ACTIONS_ID_TOKEN_REQUEST_URL` / `ACTIONS_ID_TOKEN_REQUEST_TOKEN`) with the audience the staging Fulcio issuer expects, and export it as `PODIUM_SIGSTORE_OIDC_TOKEN`.
- Register the GitHub Actions OIDC issuer as a trusted issuer on the staging Fulcio instance (or reuse sigstage's existing GitHub issuer binding), and ship the staging trust root as `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE`.
- Gate the job to releases whose diff touches `pkg/sign`, so unrelated releases are not written into staging Rekor.

The payoff is per-release automated coverage of the live signing path. The cost is the issuer-binding setup plus an external-dependency flake surface on the release-blocking path, and each run writes to the staging transparency log. Given the OIDC-token constraint, the manual procedure above is the default.

## Hotfix without a tag

If something goes sideways between tags and a fix has to ship before the next planned release, you have two options:

- **Cut an unscheduled minor**: bump `0.2.0-dev` straight to `0.2.0`, tag, release, then bump `main` to `0.3.0-dev`. Use this when the fix is significant enough to warrant a release on its own.
- **Cut a patch from the most recent tag**: see the patch-release workflow above. Use this when `main` carries unfinished work that shouldn't ship with the fix.

Pick the patch path when in doubt; it isolates the fix from in-flight changes.
