# Operations

Manual steps that supplement the automated workflows. Each item lists what to do, why, and when it's needed.

> Status legend:
>
> - **[ ]** not yet done.
> - **[x]** done; kept here as a reference / runbook.

---

## Quick reference

### Repo secrets to create

| Secret          | Used by                             | Required?                                                                           |
| :-------------- | :---------------------------------- | :---------------------------------------------------------------------------------- |
| `NPM_TOKEN`     | `release.yml` → `publish-ts`        | Yes, before first release                                                           |
| `CODECOV_TOKEN` | `test.yml` → `go` (coverage upload) | Optional; the Codecov GitHub App handles uploads via OIDC without a token           |

### What's not a secret

| Thing                  | Why no secret needed                                           |
| :--------------------- | :------------------------------------------------------------- |
| PyPI uploads           | OIDC via Trusted Publisher, bound on PyPI's side               |
| Codecov uploads        | The Codecov GitHub App grants OIDC-based access                |
| GHCR container pushes  | `GITHUB_TOKEN` already has `packages: write` per the workflow  |
| Postgres / MinIO in CI | Service containers set their own credentials inline            |
| `PODIUM_SIGSTORE_*`    | Sigstore live tests are manual-only; never run from a workflow |

### GitHub one-time settings (not secrets)

| Setting                        | Where                                        |
| :----------------------------- | :------------------------------------------- |
| `pypi` environment             | Settings → Environments → New environment    |
| Codecov GitHub App             | github.com/apps/codecov → Install on repo    |
| PyPI Trusted Publisher binding | pypi.org → manage project → publishing       |
| Branch protection on `main`    | Settings → Branches → required status checks |
| Tag protection on `v*`         | Settings → Tags → New rule                   |
| Dependabot security updates    | Settings → Code security and analysis        |

Each item below expands on these with the exact steps. Local-dev environment variables for live tests are in [Live integration environment variables](#live-integration-environment-variables).

---

## One-time setup

Required before the first `vX.Y.Z` tag fires the release workflow successfully.

### [ ] Configure PyPI Trusted Publisher

The release workflow's `publish-py` job uses [PyPI Trusted Publishing](https://docs.pypi.org/trusted-publishers/) (OIDC-based, no API token stored as a secret). Do this once:

The PyPI distribution name is `podium-sdk` (the plain `podium` name was taken). The Python import name stays `podium` — users run `pip install podium-sdk` then `from podium import Client`.

1. Reserve the project name. The first release must be uploaded manually so PyPI knows the package exists:
   ```bash
   cd sdks/podium-py
   python -m pip install build twine
   python -m build
   python -m twine upload dist/*    # use a temporary API token; revoke after.
   ```
2. Go to [pypi.org/manage/project/podium-sdk/settings/publishing/](https://pypi.org/manage/project/podium-sdk/settings/publishing/).
3. Add a new trusted publisher with:
   - **Owner**: `lennylabs`
   - **Repository**: `podium`
   - **Workflow**: `release.yml`
   - **Environment**: `pypi`

After this, every tagged release uploads automatically with no token rotation.

### [x] Create the GitHub `pypi` environment

The `publish-py` job pins itself to `environment: pypi` so the trusted-publisher binding is honored.

1. Go to repo Settings → Environments → New environment → `pypi`.
2. Optional: require manual approval before deploys to this environment if you want a human gate before each PyPI upload.

### [x] Add the `NPM_TOKEN` repo secret

The `publish-ts` job needs a token that can publish to the `@lennylabs` scope. npm has two flavors of token today; both work.

**Granular Access Token** (the modern default; what most accounts see now):

1. npmjs.com → avatar → Access Tokens → **Generate New Token** → **Granular Access Token**.
2. Name: anything (`podium-ci`).
3. Expiration: pick a date (max 1 year). Set a calendar reminder to rotate.
4. Allowed IP ranges: leave empty.
5. Packages and scopes: **Read and write** → **Specific organizations and packages** → pick the `lennylabs` org (or `@lennylabs/*`).
6. Organizations: No access.
7. **Allow this token to bypass two-factor authentication when publishing**: check this box. Without it, `npm publish` fails with HTTP 403 ("granular access token with bypass 2fa enabled is required").
8. Generate, copy.

**Classic Automation token** (legacy flow; bypasses 2FA implicitly, no expiry):

1. npmjs.com → avatar → Access Tokens → **Generate New Token** → look for a **Classic Token** option (sometimes behind a "Switch to legacy" toggle).
2. Type: **Automation**.
3. Generate, copy.

In either case, add it to GitHub: repo Settings → Secrets and variables → Actions → New repository secret named `NPM_TOKEN`.

Rotate the token annually or when a maintainer leaves. Granular tokens force the issue by expiring on their own; classic tokens rely on you remembering.

> **Reserving the package manually from your laptop?** That uses your interactive login, not `NPM_TOKEN`. If 2FA is enforced on the account, run `npm publish --access public --otp=123456` with the current 6-digit code from your authenticator app, or run `npm publish --access public` and respond when prompted.

### [x] Create the `@lennylabs` npm organization

The package name in `sdks/podium-ts/package.json` is `@lennylabs/podium-sdk` — a _scoped_ package. The `@lennylabs` scope is owned by an npm organization, so the organization has to exist before you can publish to that scope.

1. Sign in (or create an account) at [npmjs.com](https://www.npmjs.com).
2. Top-right avatar → **Add organization**, or go directly to [npmjs.com/org/create](https://www.npmjs.com/org/create).
3. Organization name: `lennylabs`. This claims the `@lennylabs` scope.
4. Plan: the free tier is sufficient for public packages.
5. After creation, the owning account is automatically an organization owner; add co-maintainers later through the org's members page.

The `@podium` scope was already taken on npm, which is why this project uses `@lennylabs/podium-sdk` rather than `@podium/sdk`. If `lennylabs` is also taken when you get to this step, pick another scope (`@lennylabs-podium`, `@podiumio`, etc.) and update the `name` field in `sdks/podium-ts/package.json` to match. The Python distribution name (`podium-sdk` on PyPI) and the npm scope name do not have to be aligned.

### [x] Reserve `@lennylabs/podium-sdk` with a first publish

With the organization in place, the first publish under the scope creates the package and reserves its name. Two ways to do this:

**Manually** (gives you a working publish before wiring CI):

```bash
cd sdks/podium-ts
npm install
npm login                              # interactive; use the org-owning account
npm publish --access public            # creates @lennylabs/podium-sdk on npm
```

`--access public` is required for scoped packages — they default to private, which is a paid feature.

**Or let the release workflow do it**. Configure `NPM_TOKEN`, push a tag, and the `publish-ts` job runs the same `npm publish --access public`. The first run reserves the package; subsequent tags publish updates.

### [ ] Confirm GHCR access

The `container` job pushes to `ghcr.io/lennylabs/podium-server`. The `GITHUB_TOKEN` provided to workflows already has `packages: write` per the workflow's `permissions:` block, so no extra credentials are required. The first push creates the package; check it appears at [github.com/orgs/lennylabs/packages](https://github.com/orgs/lennylabs/packages) and make it public if appropriate.

### [ ] Configure branch protection on `main`

Prevents direct pushes and enforces CI before merge. GitHub setting, not a repo file:

1. Go to Settings → Branches → Branch protection rules → Add rule.
2. Branch name pattern: `main`.
3. Enable:
   - **Require a pull request before merging** → require 1 approving review, dismiss stale reviews on new commits.
   - **Require status checks to pass** → mark these required:
     - `go` (from `test.yml`)
     - `podium-py` (from `test.yml`)
     - `podium-ts` (from `test.yml`)
     - `speccov` (from `spec-coverage.yml`)
     - `analyze (go)`, `analyze (python)`, `analyze (javascript-typescript)` (from `codeql.yml`)
   - **Require branches to be up to date before merging**.
   - **Require linear history** (no merge commits; PRs land via squash or rebase).
   - **Restrict who can push** → leave unchecked (the branch is protected; PR review handles authorization).
4. Save.

Bypass for maintainers is optional. For a project still in pre-release, allowing the project owner to push directly during emergencies is reasonable; tighten once 1.0 is out.

### [ ] Configure tag protection on `v*`

Pairs with the `validate-tag` job in `release.yml`. The workflow gate prevents publishing from a feature-branch tag; tag protection prevents the tag from being created at all by anyone without admin access.

1. Go to Settings → Tags → New rule.
2. Tag name pattern: `v*`.
3. Save.

By default this restricts creation, deletion, and updating of matching tags to users with admin access. For a single-maintainer project that's the owner; once the project adds maintainers, grant them the role through team membership rather than loosening this rule.

Tag protection plus the `validate-tag` workflow gate gives defense in depth:

- Tag protection stops most accidents — a contributor with write access can't push a `v*` tag at all.
- `validate-tag` catches the remaining case — an admin who mistakenly tags an unmerged commit gets a CI failure before any artifact publishes.

### [ ] Set up Codecov

The `go` job in `test.yml` uploads coverage to Codecov. Two paths, depending on how Codecov is configured for the repo:

**GitHub App (recommended)** — no token required:

1. Install the [Codecov GitHub App](https://github.com/apps/codecov) on the `lennylabs/podium` repo.
2. That's it. `codecov-action@v5` exchanges a GitHub Actions OIDC token for short-lived Codecov credentials at upload time; no static secret lives in the repo.

**Upload token (fallback)** — useful for redundancy or if you ever flip the repo to private:

1. Sign in to [codecov.io](https://codecov.io) with GitHub.
2. Repo dashboard → **Settings** → **General** → **Repository Upload Token**.
3. Copy. Add to GitHub: repo Settings → Secrets and variables → Actions → New repository secret named `CODECOV_TOKEN`.

`codecov-action@v5` uses the token when present, falls back to OIDC otherwise. The workflow already sets `fail_ci_if_error: false`, so a missing or flaky upload doesn't block PR merges or releases either way.

### [ ] Enable Dependabot security updates

GitHub's free product, on by default for public repos. Confirm at Settings → Code security and analysis → Dependabot alerts and Dependabot security updates are both **On**. The non-security version-bump PRs come from the `.github/dependabot.yml` config that's already committed.

---

## Live integration environment variables

Tier 2 tests inspect these and self-skip when unset. `make test` runs only the in-process suite; `make test-live` (or any `go test ./...` invocation with the variables set) exercises real backends. Tests gate on individual variables, so partial coverage works: set only the Postgres group and the rest stay skipped.

### Quickstart with docker-compose

The repo ships a `docker-compose.yml` and matching `make` targets that spin up Postgres (with pgvector preinstalled) and MinIO locally. The same images run as service containers in `nightly.yml` and `release.yml`, so behavior on the laptop matches CI.

```bash
make services-up        # start Postgres + MinIO
make test-live          # full Go suite with env vars pointing at the local services
make services-down      # stop the services (keeps volumes)
```

`make test-live` sets the Postgres + S3 variables inline; you don't need to source anything. For ad-hoc commands (`go test ./pkg/objectstore/...`), copy `.env.example` to `.env.local` and source it from your shell or direnv.

Need a different backend (managed Postgres, real S3, etc.)? Override the `LIVE_*` make variables on the command line:

```bash
make test-live LIVE_POSTGRES_DSN="postgres://…" LIVE_S3_ENDPOINT="s3.amazonaws.com"
```

### Postgres (store + pgvector)

| Variable                     | Required?            | Purpose                                                                                                                                                          | Example                                                              |
| :--------------------------- | :------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------- |
| `PODIUM_POSTGRES_DSN`        | Yes for either suite | `pkg/store/postgres_test.go` RegistryStore conformance + pgvector fallback.                                                                                      | `postgres://podium:podium@localhost:5432/podium?sslmode=disable`     |
| `PODIUM_POSTGRES_DSN_VECTOR` | Optional             | When set, `pkg/vector/pgvector_test.go` uses this DSN instead of `PODIUM_POSTGRES_DSN`. Useful when the deployment splits metadata and vectors across databases. | `postgres://podium:podium@localhost:5432/podium_vec?sslmode=disable` |

The target database needs the `vector` extension installed (`CREATE EXTENSION vector;`).

### S3-compatible object storage

| Variable                      | Required? | Purpose                                                                       | Example          |
| :---------------------------- | :-------- | :---------------------------------------------------------------------------- | :--------------- |
| `PODIUM_S3_ENDPOINT`          | Yes       | Host:port for MinIO / Ceph / real S3. Skips when unset.                       | `localhost:9000` |
| `PODIUM_S3_BUCKET`            | Yes       | Pre-created bucket name.                                                      | `podium-test`    |
| `PODIUM_S3_REGION`            | Optional  | Defaults to `us-east-1`.                                                      | `us-west-2`      |
| `PODIUM_S3_ACCESS_KEY_ID`     | Optional  | Anonymous access when unset.                                                  | `minioadmin`     |
| `PODIUM_S3_SECRET_ACCESS_KEY` | Optional  | Pairs with the access key.                                                    | `minioadmin`     |
| `PODIUM_S3_USE_SSL`           | Optional  | Set to `"false"` for plain HTTP. Any other value (including unset) means TLS. | `false`          |

### Sigstore (keyless signing)

`pkg/sign/sigstore_live_test.go` skips unless **all four** are set:

| Variable                              | Purpose                                                                       | Example                       |
| :------------------------------------ | :---------------------------------------------------------------------------- | :---------------------------- |
| `PODIUM_SIGSTORE_FULCIO_URL`          | Fulcio CA endpoint.                                                           | `https://fulcio.sigstore.dev` |
| `PODIUM_SIGSTORE_REKOR_URL`           | Rekor transparency log.                                                       | `https://rekor.sigstore.dev`  |
| `PODIUM_SIGSTORE_OIDC_TOKEN`          | OIDC token Fulcio binds into the cert. In CI, sourced from `id-token: write`. | `eyJ…`                        |
| `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` | Path to the trust bundle (intermediate + root CA chain).                      | `/path/to/sigstore-root.pem`  |

### What's not gated by env vars today

The cloud vector backends (Pinecone, Weaviate, Qdrant) and embedding providers (OpenAI, Voyage, Cohere, Ollama) have production implementations but only Tier 1 mocks. No `*_live_test.go` exists for them yet, so the corresponding API-key variables are unused by the suite. Adding live coverage for those is tracked under [Future infra](#future-infra) implicitly; revisit when the cost / value tradeoff makes sense.

### About fallbacks and config-shape coverage

Two of the variables above have fallback behavior:

- `PODIUM_POSTGRES_DSN_VECTOR` falls back to `PODIUM_POSTGRES_DSN`.
- `PODIUM_S3_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` are optional (anonymous access otherwise).

**You do not need to run the full integration suite multiple times to cover both branches of these fallbacks.** The fallback code is three lines of `os.Getenv` resolution that converges on a single `Open(dsn)` / `NewS3(cfg)` call; the database or S3 behavior under test is identical regardless of which variable supplied the value. Re-running the integration suite to exercise a `dsn == ""` branch costs minutes and adds zero behavioral coverage.

If you want airtight coverage of the resolution logic itself:

- Write a small unit test using `t.Setenv` that asserts each branch picks the right DSN / cred combo.
- Run integration tests once with a realistic config.

Separately, the underlying backends behave differently along some axes that are worth a deliberate second pass when a deployment changes:

- **TLS on vs off** (`PODIUM_S3_USE_SSL`): presigned URL signing and certificate validation paths differ. CI runs against MinIO with TLS off; a one-off run against real S3 with TLS on is the right way to validate.
- **Anonymous vs credentialed S3**: the SDK takes a different code path. If a deployment uses one or the other, run that configuration once before shipping.
- **Split vs shared Postgres DSN**: only matters when a deployment actually splits them.

These are deployment-config validations, not fallback-coverage testing. Add a matrix job in CI when a deployment shape genuinely depends on the variant; otherwise one pass is enough.

### Pasteable template

Fill in the values you have; leave the rest empty. The unset ones skip cleanly.

```sh
# Tier 2 live integrations. Empty values stay skipped.
export PODIUM_POSTGRES_DSN="postgres://podium:podium@localhost:5432/podium?sslmode=disable"
export PODIUM_POSTGRES_DSN_VECTOR=""

export PODIUM_S3_ENDPOINT="localhost:9000"
export PODIUM_S3_BUCKET="podium-test"
export PODIUM_S3_REGION=""
export PODIUM_S3_ACCESS_KEY_ID="minioadmin"
export PODIUM_S3_SECRET_ACCESS_KEY="minioadmin"
export PODIUM_S3_USE_SSL="false"

export PODIUM_SIGSTORE_FULCIO_URL=""
export PODIUM_SIGSTORE_REKOR_URL=""
export PODIUM_SIGSTORE_OIDC_TOKEN=""
export PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE=""
```

In CI, every variable above maps to a repo Secret (Settings → Secrets and variables → Actions). `integration-live.yml` provides Postgres and MinIO via service containers, so only Sigstore needs a real secret today.

---

## Per-release checklist

These run alongside the `RELEASING.md` flow. Most are reminders rather than blockers.

- [ ] CHANGELOG has a populated section for the version being cut.
- [ ] No `0.0.0-dev` / `unknown` strings remain in any source file (`grep -r '0\.0\.0-dev' --include='*.go' .`).
- [ ] `make test` passes locally on the release commit.
- [ ] The four version files agree on the release number (see RELEASING.md for the list).
- [ ] Last night's `nightly.yml` run is green for `main`. If not, fix before tagging — the release runs the same battery and will fail too.
- [ ] If the release touches `pkg/sign` or the `SignatureProvider` contract, run the Sigstore live tests manually against real Fulcio + Rekor (see `RELEASING.md` → "Sigstore live tests are manual"). The release workflow does not exercise them.
- [ ] After the release workflow runs, smoke-test one published artifact (download a binary, run `podium version`).

---

## Future infra

Items that aren't required today but become useful as the project grows. Track here so they don't get lost.

- [ ] **Code signing for macOS binaries**. Without a Developer ID signature + notarization, macOS users get a Gatekeeper warning. Requires an Apple Developer account ($99/year) and the right CI machinery.
- [ ] **Code signing for Windows binaries**. Same story with a code-signing certificate from DigiCert / Sectigo / similar.
- [ ] **Reproducible builds**. Verify by building twice with `SOURCE_DATE_EPOCH` pinned and comparing hashes.
- [ ] **SBOM generation**. Attach a CycloneDX or SPDX SBOM to each GitHub Release; useful for downstream supply-chain auditors. Tools: [Syft](https://github.com/anchore/syft), [Go's built-in `-buildvcs`](https://go.dev/blog/govulncheck).
- [ ] **Provenance attestations**. SLSA Build L3 attestations via [slsa-github-generator](https://github.com/slsa-framework/slsa-github-generator).
- [ ] **Documentation site rebuild on release**. The docs/ site is served from GitHub Pages; configure a workflow that rebuilds on tag push.
- [ ] **Status page or uptime monitoring**. If the project hosts a registry instance, a public status page (Statuspage, BetterStack, or self-hosted) becomes useful.
- [ ] **Release notification channel**. A webhook posting "v0.2.0 released" to Slack / Discord / a mailing list keeps users informed without checking GitHub.
- [ ] **Vulnerability scanning of container images**. Trivy or Grype in a scheduled workflow against the ghcr.io tags.

---

## How this file is maintained

When you complete an item, flip `[ ]` to `[x]` and leave the description in place as a runbook for the next maintainer (or for re-bootstrapping after a credential rotation). Delete items only when they no longer apply.

When you add a new external dependency or manual procedure, capture it here before the knowledge ages out of memory.
