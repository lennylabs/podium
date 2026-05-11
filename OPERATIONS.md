# Operations

Manual steps that supplement the automated workflows. Each item lists what to do, why, and when it's needed.

> Status legend:
> - **[ ]** not yet done.
> - **[x]** done; kept here as a reference / runbook.

---

## One-time setup

Required before the first `vX.Y.Z` tag fires the release workflow successfully.

### [ ] Configure PyPI Trusted Publisher

The release workflow's `publish-py` job uses [PyPI Trusted Publishing](https://docs.pypi.org/trusted-publishers/) (OIDC-based, no API token stored as a secret). Do this once:

1. Reserve the project name. The first release must be uploaded manually so PyPI knows the package exists:
   ```bash
   cd sdks/podium-py
   python -m pip install build twine
   python -m build
   python -m twine upload dist/*    # use a temporary API token; revoke after.
   ```
2. Go to [pypi.org/manage/project/podium/settings/publishing/](https://pypi.org/manage/project/podium/settings/publishing/).
3. Add a new trusted publisher with:
   - **Owner**: `lennylabs`
   - **Repository**: `podium`
   - **Workflow**: `release.yml`
   - **Environment**: `pypi`

After this, every tagged release uploads automatically with no token rotation.

### [ ] Create the GitHub `pypi` environment

The `publish-py` job pins itself to `environment: pypi` so the trusted-publisher binding is honored.

1. Go to repo Settings → Environments → New environment → `pypi`.
2. Optional: require manual approval before deploys to this environment if you want a human gate before each PyPI upload.

### [ ] Add the `NPM_TOKEN` repo secret

The `publish-ts` job needs an automation token for the `@podium` scope:

1. On npmjs.com, [create an automation token](https://www.npmjs.com/settings/<your-username>/tokens) with **Automation** type. Automation tokens bypass 2FA and are designed for CI.
2. Add it to GitHub: repo Settings → Secrets and variables → Actions → New repository secret named `NPM_TOKEN`.

Rotate this token annually or when a maintainer leaves.

### [ ] Reserve the `@podium` npm scope

Same logic as PyPI: the first publish has to happen manually so npm reserves the scope.

```bash
cd sdks/podium-ts
npm install
npm login                              # interactive
npm publish --access public            # uses your personal credentials
```

Subsequent publishes run from CI via `NPM_TOKEN`.

### [ ] Confirm GHCR access

The `container` job pushes to `ghcr.io/lennylabs/podium-server`. The `GITHUB_TOKEN` provided to workflows already has `packages: write` per the workflow's `permissions:` block, so no extra credentials are required. The first push creates the package; check it appears at [github.com/orgs/lennylabs/packages](https://github.com/orgs/lennylabs/packages) and make it public if appropriate.

---

## Per-release checklist

These run alongside the `RELEASING.md` flow. Most are reminders rather than blockers.

- [ ] CHANGELOG has a populated section for the version being cut.
- [ ] No `0.0.0-dev` / `unknown` strings remain in any source file (`grep -r '0\.0\.0-dev' --include='*.go' .`).
- [ ] `make test` passes locally on the release commit.
- [ ] The four version files agree on the release number (see RELEASING.md for the list).
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
