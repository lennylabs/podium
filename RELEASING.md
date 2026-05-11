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
| `internal/buildinfo/buildinfo.go` | Default `Version` constant. Source of truth when no ldflags are passed. |
| `sdks/podium-py/pyproject.toml` and `sdks/podium-py/podium/__init__.py` | PEP 440 form: `0.2.0.dev0` while in flight, `0.2.0` when released. |
| `sdks/podium-ts/package.json` | Semver: `0.2.0-dev` while in flight, `0.2.0` when released. |
| Git tag | Authoritative for a release: `vX.Y.Z`. |
| `Makefile` `build` target | Injects `git describe --tags --dirty --always` into the binary via `-ldflags`. |

Keep the three text sources (`buildinfo.go`, the Python files, the npm `package.json`) in sync.

## Cutting a release

1. **Pick the version.** Walk the CHANGELOG and the merged diff since the previous tag. Decide MAJOR, MINOR, or PATCH.

2. **Bump the version strings to the release number** (drop the `-dev` / `.dev0` suffix):
   - `internal/buildinfo/buildinfo.go`: `Version = "0.2.0"`.
   - `sdks/podium-py/pyproject.toml`: `version = "0.2.0"`.
   - `sdks/podium-py/podium/__init__.py`: `__version__ = "0.2.0"`.
   - `sdks/podium-ts/package.json`: `"version": "0.2.0"`.

3. **Update `CHANGELOG.md`.** Move the unreleased section into a dated header `## [0.2.0] - YYYY-MM-DD`. Add the compare link at the bottom.

4. **Commit, tag, push.**
   ```bash
   git commit -am "Release v0.2.0"
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin main
   git push origin v0.2.0
   ```

5. **CI builds release binaries.** The release workflow runs `make build VERSION=v0.2.0`, which produces binaries reporting `podium v0.2.0 (<sha>, built <date>)`. The workflow publishes them to GitHub Releases plus PyPI plus npm.

6. **Open the next development cycle.** On `main`, bump the version strings to the next `-dev`:
   - `buildinfo.go`: `Version = "0.3.0-dev"`.
   - `pyproject.toml`: `version = "0.3.0.dev0"`.
   - `__init__.py`: `__version__ = "0.3.0.dev0"`.
   - `package.json`: `"version": "0.3.0-dev"`.

   Commit as `Begin 0.3.0 development`.

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

A push of `vX.Y.Z` triggers the release workflow. Five test jobs run in parallel and every publish job lists all five in `needs:`, so a single failure blocks every artifact:

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
3. Publishes `podium-py` to PyPI via Trusted Publishing.
4. Publishes `@podium/sdk` to npm with provenance.
5. Builds and pushes multi-arch container images to `ghcr.io/lennylabs/podium-server`.

The same five test jobs (minus the coverage budget and matrix audit, which are advisory) also run on `nightly.yml` against `main` at 03:00 UTC. A green nightly is the strongest signal that a release-from-`main` will succeed.

The workflow lives in `.github/workflows/release.yml`.

### Sigstore live tests are manual

`pkg/sign/sigstore_live_test.go` is the one Tier 2 suite the release gate does not run. Real Fulcio and Rekor cost money per call and pollute the public transparency log; a sigstore-mock alternative isn't worth the maintenance burden. Verify the signing path manually before any release that changes `pkg/sign` or touches the `SignatureProvider` contract:

```bash
export PODIUM_SIGSTORE_FULCIO_URL=https://fulcio.sigstore.dev
export PODIUM_SIGSTORE_REKOR_URL=https://rekor.sigstore.dev
export PODIUM_SIGSTORE_OIDC_TOKEN=$(gcloud auth print-identity-token)   # or any IdP
export PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE=/path/to/trust-bundle.pem
go test ./pkg/sign/... -count=1 -v -run TestSigstore_Live
```

For releases that don't touch the signing path, the mocked Sigstore tests in `pkg/sign/sigstore_test.go` are sufficient.

## Hotfix without a tag

If something goes sideways between tags and a fix has to ship before the next planned release, you have two options:

- **Cut an unscheduled minor**: bump `0.2.0-dev` straight to `0.2.0`, tag, release, then bump `main` to `0.3.0-dev`. Use this when the fix is significant enough to warrant a release on its own.
- **Cut a patch from the most recent tag**: see the patch-release workflow above. Use this when `main` carries unfinished work that shouldn't ship with the fix.

Pick the patch path when in doubt; it isolates the fix from in-flight changes.
