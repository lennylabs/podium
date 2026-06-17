# Release process

Project-wide steps for cutting a Podium release. These steps apply when bumping the version, writing the changelog entry, and tagging a release. [`RELEASING.md`](../../RELEASING.md) is the fuller maintainer reference for the publish pipeline, the SDK version derivation, and the Sigstore live smoke. This rule is the operative checklist for preparing and tagging a release.

## Top-level principle

A release is prepared by a PR and cut by a tag push. A prep PR bumps the version and the changelog on a `release/X.Y.Z` branch and merges to `main`. An annotated `vX.Y.Z` tag is then pushed against the merged commit, and the tag push triggers the publish pipeline. The version lives in source so a plain `go build` reports it, and the SDKs derive their version from the tag.

## Versioning

Podium follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) (`MAJOR.MINOR.PATCH`). Before `1.0.0`, a backward-incompatible change lands in a MINOR bump. Choose the increment by reading `CHANGELOG.md` and the merged diff since the previous tag.

## Where the version lives

- `internal/buildinfo/buildinfo.go`: the `Version` constant. Edit it by hand each release. It is the `go build` fallback; the release pipeline overrides it from the tag via `-ldflags`.
- `sdks/podium-py/pyproject.toml` and `sdks/podium-ts/package.json`: tag-derived. setuptools-scm derives the Python version from the tag, and the workflow rewrites the npm version from the tag. Neither needs a manual edit per release.
- The git tag `vX.Y.Z`: authoritative for a release.

## Preparing the release PR

1. Branch from `main`: `git checkout -b release/X.Y.Z`.
2. Bump `internal/buildinfo/buildinfo.go` `Version` to `X.Y.Z`.
3. Add a `CHANGELOG.md` entry:
   - Insert a `## [X.Y.Z] - YYYY-MM-DD` section below `## [Unreleased]`, dated the release day.
   - Update the `[Unreleased]` compare link to `compare/vX.Y.Z...HEAD`.
   - Add the `[X.Y.Z]: https://github.com/lennylabs/podium/releases/tag/vX.Y.Z` reference link.
   - Group entries under `Added`, `Changed`, `Fixed`, `Removed`, and `Documentation`. Cover user-facing changes, and omit test-only and CI-only changes.
   - Match the `## [X.Y.Z] - YYYY-MM-DD` header format exactly. The release workflow extracts this section verbatim as the GitHub Release body, so a malformed header drops the release notes.
4. Confirm `go build ./...` and `go test ./internal/buildinfo/` pass.
5. Commit as `chore(release): prep vX.Y.Z` with a DCO sign-off (`git commit -s`). The prep commit touches only `CHANGELOG.md` and `internal/buildinfo/buildinfo.go`.
6. Open a PR with base `main`. Let CI pass before merging.

## Tagging the release

After the prep PR merges to `main`:

```bash
git checkout main && git pull
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

The tag push triggers `.github/workflows/release.yml`. A `validate-tag` job rejects any tag whose commit is not reachable from `main` or a `release/*` branch, so tag from one of those branches. Once the test battery passes, the workflow publishes the cross-compiled binaries to the GitHub Release, the wheel and sdist to PyPI, the npm package, and the container image to ghcr.io.

Push the tag after the prep PR merges, against the merged commit on `main`. The prep PR itself carries no tag.

## Patch releases

A patch release branches from the released tag rather than from `main`, so unfinished `main` work does not ship with the fix:

```bash
git checkout -b release/X.Y.x vX.Y.Z
# land the fix, bump buildinfo.Version, add the CHANGELOG entry
git commit -s -am "chore(release): prep vX.Y.Z+1"
git push origin release/X.Y.x
# open the PR, merge it, then tag vX.Y.Z+1 from release/X.Y.x
```

Merge the fix back into `main` when it is not already there.

## Before tagging a signing-path release

When the release diff touches `pkg/sign` or the `SignatureProvider` contract, run the Sigstore live smoke against the staging instance first. The release gate does not run it. The procedure is in `RELEASING.md`.

## Where these rules apply

- A change to the `Version` constant in `internal/buildinfo/buildinfo.go`.
- A new `CHANGELOG.md` release section.
- A `vX.Y.Z` tag push.

## Escape hatches

- An unscheduled release that must ship before the next planned one follows the patch-release path from the most recent tag, or an unscheduled MINOR bump from `main`. `RELEASING.md` records both.
- A pre-release uses a suffix that semver orders before the plain version (`X.Y.Z-rc.1`). The release workflow marks a hyphenated tag as a prerelease automatically.

## Maintenance

When the release workflow or the version locations change, update this rule and `RELEASING.md` together.
