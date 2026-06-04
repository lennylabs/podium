# Podium Test Gap Inventory

This document lists the test gaps Podium needs to close, with a target lane for
each and the infrastructure required to run it. It is a forward-looking work
list. It replaces the prior end-to-end test inventory (the 135-source,
5992-test specification that previously occupied this file). The prior content
remains in Git history; recover it with `git show <prev-commit>:TEST-GAPS.md`.

Compiled 2026-06-03 from the test-infrastructure review on branch
`fix/spec-gap-remediation`.

## How to read this file

Each gap has a stable ID of the form `G-<AREA>-<n>`. The areas are AUTH
(authentication and identity), DOC (documentation-example coverage), VEC
(managed vector backends), EMB (embedding providers), PGV (Postgres and
pgvector depth), STACK (managed-stack parity and multi-tenant isolation), HARN
(harness drift), and INFRA (test infrastructure and hermeticity).

Each entry records:

- **Severity**: P0 for correctness, security, data integrity, and auth; P1 for
  important behavior and documented flows; P2 for minor or cosmetic behavior.
- **Lane**: where the test runs once built (see the lane model below).
- **Status**: open, in-progress, or closed.
- **Current**, **Evidence**, **Target**, **Work**, and where relevant
  **Depends on** and **Unblocks**.

The `F-x.y.z` IDs used in `BUILD-GAPS.md` track implementation gaps under a
separate scheme. Match a gap to a BUILD-GAPS finding by section and heading
text, because the BUILD-GAPS numbering is regenerated per pass.

## Test lane model

| Lane | Trigger | Live services exercised | Gating mechanism |
|:--|:--|:--|:--|
| PR | `pull_request`, push to `main` (`.github/workflows/test.yml`) | Postgres + pgvector, MinIO (S3) | CI sets `PODIUM_POSTGRES_DSN` and `PODIUM_S3_*`; tests skip when unset |
| Nightly | cron 03:00 UTC, manual (`.github/workflows/nightly.yml`) | Postgres + pgvector, MinIO (S3) | same env gating, plus `matrix-audit` and `coverage-budget` |
| Release | release publish or tag (target: a job in `.github/workflows/release.yml` or a dedicated `live-external.yml`) | Pinecone, Weaviate Cloud, Qdrant Cloud, OpenAI, Cohere, Voyage, Ollama | `PODIUM_LIVE_EXTERNAL=1` plus per-service credentials from CI secrets |
| Manual | `workflow_dispatch`, or `make test-live-external` locally | any subset whose credentials are present | same env switch; each sub-suite skips with a reason when its credentials are absent |
| Manual-only (existing) | RELEASING.md procedure | Sigstore Fulcio and Rekor | `PODIUM_SIGSTORE_*` |

The live external-services suite (managed vector backends and embedding
providers) runs in the Release lane and on manual invocation. It does not run on
pull requests, and it does not run nightly. Real managed services cost money and
hold per-account quotas, so this suite is reserved for the point at which a
release is cut. The suite is also runnable on demand through `make
test-live-external` for local validation before a release.

## Lane gating contract

The live external suite gates on a single switch plus per-service credentials.
`PODIUM_LIVE_EXTERNAL` must equal `1` for any external sub-suite to run. Within
that, each sub-suite checks for its own credentials and skips with a stated
reason when they are absent, so a partial credential set runs only the subset it
can reach. This mirrors the existing pattern in `pkg/objectstore/s3_live_test.go`
and `pkg/sign/sigstore_live_test.go`, where an unset endpoint or key produces a
`t.Skip` rather than a failure.

---

# AUTH: Authentication and identity

The registry verifies one auth mode server-side today: `injected-session-token`
(runtime-signed JWTs against admin-registered keys). The `oidc` mode that the
deployment cookbooks describe is not implemented. Because the end-to-end suite
runs only `serve --standalone` (no auth, every caller resolves to
`system:public`), the entire OIDC, admin, visibility, and SCIM-to-visibility
axis is skipped end-to-end. `test/e2e/auth_oidc_test.go` alone carries 24
`t.Skip` calls.

**Stage 2 (2026-06-03) adopted option A: reconcile.** The spec defines no
in-registry JWKS/OIDC verifier, so rather than build one, the OIDC cookbooks were
reconciled to the shipped identity model and e2e coverage was added for the
spec-mandated, implemented features (admin/RBAC, SCIM-to-visibility, multi-tenant
isolation, IdpGroupMapping) over the verified injected-session-token path, plus a
live Dex device-code login test. `auth_oidc_test.go` dropped from 24 skips to 6
(18 non-spec tests deleted, 1 converted). Where a gap entry's Target or Work below
describes building the verifier, that path was intentionally not taken; the Status
line on each gap records the actual resolution.

### G-AUTH-1: Server-side OIDC and JWKS verifier is not implemented
- **Severity**: P0. **Lane**: n/a (reconciled). **Status**: closed (option A, reconcile). The spec defines no in-registry JWKS verifier, so one was not built. The OIDC docs were reconciled to the shipped model (injected-session-token is the server-verified path; oauth-device-code is client-side acquisition), and the JWKS, audience-mismatch, and clock-skew tests were deleted. The Target and Work below describe the build path that was intentionally not taken.
- **Current**: The registry ships no request-time OIDC verifier. A free-form
  provider label such as `oidc` resolves to no verifier, and every caller falls
  back to anonymous-public.
- **Evidence**: `internal/serverboot/identity_verify.go:72-76` states in-code
  that `oauth-device-code` "needs the §6.3.1 server-side OIDC verifier that the
  registry does not yet ship." `identity_verify.go:86-91` is the startup guard
  that refuses to serve a provider with no verifier.
- **Target**: A verifier that fetches JWKS, validates issuer, audience,
  signature, and expiry, applies a configured clock-skew leeway, and derives
  claims (`sub`, `email`, `groups`, `org_id`) into the caller identity.
- **Work**: Implement the verifier behind the IdentityProvider seam. Add JWKS
  fetch with caching and rotation. Emit the `auth.audience_mismatch` and
  `auth.signature_invalid` error codes the docs already promise.
- **Unblocks**: G-AUTH-3, G-AUTH-4, G-AUTH-7, G-DOC-5, and roughly 25 skipped
  cases in `test/e2e/auth_oidc_test.go`.

### G-AUTH-2: The documented `identity:` config block is not parsed
- **Severity**: P0. **Lane**: PR. **Status**: closed (reconciled). The docs were corrected to the implemented `identity_provider:` schema (`type`, `audience`, `authorization_endpoint`); `test/e2e/auth_oidc_test.go::TestAuth_NestedIdentityBlockNotParsed` proves the nested `identity:` block is dropped.
- **Current**: The OIDC cookbooks instruct writing a top-level `identity:` block
  in `registry.yaml` with `provider`, `issuer`, `audience`, `jwks_uri`,
  `groups_claim`, `email_claim`, and `sub_claim`. The loader reads
  `identity_provider:` with only `type`, `audience`, and
  `authorization_endpoint`, so the documented block is dropped silently.
- **Evidence**: `docs/deployment/oidc/okta.md:54-63`; loader at
  `internal/serverboot` (the `identity_provider` mapping). The discrepancy is
  recorded in the skip reasons of `test/e2e/auth_oidc_test.go`.
- **Target**: The loader parses the documented `identity:` schema (or the docs
  are corrected to the implemented `identity_provider:` schema, decided together
  with G-AUTH-1 and G-DOC-5).
- **Work**: Add the struct fields and validation, then a config-load unit test
  asserting each field reaches the verifier.

### G-AUTH-3: In-process JWKS issuer test harness is missing
- **Severity**: P0. **Lane**: n/a. **Status**: closed (not built, obviated by option A). With no JWKS verifier to test, coverage uses the injected-token signing harness (`test/e2e/injected_token_helpers_test.go`) rather than a JWKS issuer.
- **Current**: No automated test mints a signed JWT against a served JWKS. The
  only "OIDC server" in tests is an `httptest` device-code stub that returns an
  unverifiable string.
- **Evidence**: `test/e2e/auth_oidc_test.go` device-code stub; the absent
  `identityfaker` named in `TEST_INFRASTRUCTURE_PLAN.md` §7.5.
- **Target**: An in-process issuer (`internal/testharness` or similar) that
  serves a JWKS endpoint and signs tokens with controllable issuer, audience,
  claims, expiry, and key ID, so OIDC e2e runs without a container.
- **Work**: Build the issuer. Convert the skipped `auth_oidc_test.go` cases to
  drive it: audience mismatch, issuer matching including trailing slash, JWKS
  rotation and caching, clock skew, invalid signature, and per-IdP group-claim
  mapping.
- **Depends on**: G-AUTH-1.

### G-AUTH-4: No automated test stands up a real identity provider
- **Severity**: P1. **Lane**: nightly plus manual. **Status**: closed (built). `test/e2e/dex_login_test.go` drives a live device-code login against the bundled Dex with a concurrent approval driver; wired as the nightly `dex-login` job and `make test-auth-dex`.
- **Current**: `docker-compose.yml` ships Dex for local evaluation, but
  `make services-up` excludes it and no workflow starts it. No Go test references
  Dex.
- **Evidence**: `Makefile:99-101`; no `dex` reference in `.github/workflows/` or
  any `*_test.go`.
- **Target**: One lane that runs `docker compose up`, executes `podium login`
  against Dex, and makes an authenticated request that resolves a real verified
  identity through to layer visibility.
- **Work**: Add the compose-up step and a single end-to-end identity test.
- **Depends on**: G-AUTH-1.

### G-AUTH-5: Admin and RBAC enforcement is skipped end-to-end
- **Severity**: P1. **Lane**: PR. **Status**: closed (built). `test/e2e/auth_admin_rbac_test.go` exercises grant/revoke/show-effective/load-override over a minted admin injected token and asserts the `auth.forbidden` envelope on denial.
- **Current**: Grant, revoke, and show-effective e2e tests skip because the
  standalone server resolves callers to `system:public` and `core.AdminAuthorize`
  rejects them.
- **Evidence**: `test/e2e/standard_deployment_test.go:214` and sibling skips.
- **Target**: Exercise admin enforcement through the already-shipped
  `injected-session-token` path by seeding an admin grant and minting a token
  whose `sub` matches.
- **Work**: This needs no OIDC and can land now. It is the quickest auth win.

### G-AUTH-6: SCIM-to-visibility lifecycle is never closed end-to-end
- **Severity**: P1. **Lane**: PR. **Status**: closed (built). `test/e2e/auth_scim_visibility_test.go`: a caller with no group claim sees a group-restricted layer via SCIM `MembersOf`; group removal and user deletion revoke it.
- **Current**: SCIM provisioning is tested as isolated HTTP CRUD authenticated
  by a static bearer token. The membership-to-visibility resolver is wired but
  untested above the unit level.
- **Evidence**: resolver wiring at `internal/serverboot/serverboot.go:779-786`;
  skip notes at `test/e2e/auth_oidc_test.go:730-732` and nearby.
- **Target**: Push a user and group through SCIM, mint a verified token for that
  user without a `groups` claim, and assert the registry expands the user's SCIM
  group membership through `MembersOf` into layer visibility. Then remove the
  membership or set `active:false` and assert the layer disappears.
- **Depends on**: G-AUTH-3 or G-AUTH-4.

### G-AUTH-7: Multi-tenant token isolation is not tested end-to-end
- **Severity**: P0. **Lane**: PR (live Postgres). **Status**: closed (built). `test/integration/auth_org_isolation_test.go`: an `org_id=acme` token reads only Acme data and gets 404 on Globex through the full request path, on a per-org Postgres schema, with org-scoped search. Gates on `PODIUM_POSTGRES_DSN`.
- **Current**: Storage-level cross-org denial is tested on live Postgres. No test
  asserts that a token's `org_id` claim routes a request to the correct per-org
  schema and is denied another org's data through the full request path.
- **Evidence**: storage isolation at `pkg/store/postgres_test.go:117-180`; no
  token-attested org routing test.
- **Target**: An end-to-end test where a verified token bearing `org_id=acme`
  reads only Acme data and is denied Globex data across artifacts, dependency
  edges, scope-preview counts, quota buckets, audit events, and object blobs.
- **Depends on**: G-AUTH-3.

### G-AUTH-8: Token expiry and clock-skew leeway are unit-only or absent
- **Severity**: P1. **Lane**: PR. **Status**: closed (reconciled, partly deferred). The clock-skew leeway depended on the not-built verifier, so the ±60s promise was removed from the docs. Injected-token expiry remains integration-tested; no new boundary test was added.
- **Current**: Expiry rejection is integration-tested for injected tokens. The
  shipped verifier applies no clock-skew leeway, while the OIDC docs promise a
  ±60s tolerance.
- **Evidence**: no `leeway` or `skew` handling in `pkg/identity` or
  `internal/serverboot` non-test code; promise at `docs/deployment/oidc/index.md`.
- **Target**: Implement leeway in the OIDC verifier and assert the boundary cases.
- **Depends on**: G-AUTH-1.

### G-AUTH-9: Documented auth surfaces that do not exist
- **Severity**: P1. **Lane**: PR. **Status**: closed (reconciled). The non-existent commands and codes were removed from the docs; `auth_oidc_test.go` keeps does-not-exist guards for `admin scim-token issue`, `admin claims-cache flush`, and the `layer register --visibility` form.
- **Current**: The OIDC docs reference commands and behaviors absent from the
  binary: `podium admin scim-token issue`, `podium layer register --visibility`,
  a SAML-over-OIDC bridge, Google Workspace `hd_required` enforcement, and the
  `auth.audience_mismatch` and `auth.signature_invalid` error codes.
- **Evidence**: `docs/deployment/oidc/okta.md:83-87` and `:101`;
  `docs/deployment/oidc/index.md:51-59`; the commands resolve to exit-2 "unknown
  subcommand" assertions in `test/e2e/auth_oidc_test.go` and
  `test/e2e/server_operations_test.go:869`.
- **Target**: Implement each surface or remove it from the docs, decided
  alongside G-AUTH-1 and G-DOC-5.

---

# DOC: Documentation-example coverage

Every documented source maps to a feature-named e2e file by convention. No tool
enforces the mapping, the generator meant to maintain it is broken, and several
documented examples are covered only in the sense that the test proves the
documented command or output is wrong.

### G-DOC-1: No machine-checked floor on doc-example coverage
- **Severity**: P1. **Lane**: PR. **Status**: closed (built). Stage 3 added `tools/doccov`, which extracts runnable fenced blocks from `docs/**` and `README.md`, maps each page to a feature-named `D-<slug>` e2e file or an explicit waiver, and fails on an unmapped example; wired into `.github/workflows/spec-coverage.yml`.
- **Current**: `tools/speccov` measures spec-section coverage and never reads
  `docs/`. A new documentation page with a runnable example and no test fails no
  check.
- **Evidence**: `tools/speccov` parses `spec/` headings and test annotations
  only.
- **Target**: A `tools/doccov` (or `speccov doc`) that extracts fenced command
  and code blocks from `docs/**` and `README.md`, maps each to a `D-<slug>` test
  or an explicit waiver, and fails CI on an unmapped example. Wire it into
  `.github/workflows/spec-coverage.yml`.
- **Unblocks**: a standing guarantee that every documented example is tested.

### G-DOC-2: `close-build-gaps.py` doc-coverage detection is self-defeating
- **Severity**: P1. **Lane**: tooling. **Status**: closed (fixed). Stage 3 repointed built-detection at the feature-named convention: a D-slug now resolves to its covering file by scanning `test/e2e/*_test.go` headers for the `(D-<slug>)` marker instead of `ls test/e2e/docs_<slug>_test.go`, and the doc-batch generator no longer instructs sessions to write the gate-forbidden `docs_<slug>_test.go` files.
- **Current**: The script decides a doc source is "already built" by checking for
  `test/e2e/docs_<slug>_test.go`. The committed gate forbids that exact filename,
  and the real tests are feature-named, so the check always reports every source
  as unbuilt and would regenerate forbidden files.
- **Evidence**: `close-build-gaps.py:228-229`; the gate at
  `test/e2e/naming_convention_test.go:37`.
- **Target**: Point built-detection at the feature-named convention or the
  G-DOC-1 manifest, or retire the script.

### G-DOC-3: Quickstart examples are documented incorrectly
- **Severity**: P1. **Lane**: PR. **Status**: closed. Stage 3 corrected the quickstart to the implemented `adapter:`/`target:`/`artifacts:` output format and the `.claude/skills/greet/SKILL.md` path (the `Materialized N artifact → .claude/agents/greet.md` text was wrong), matching the e2e assertions. The "watch mode uses `fsnotify`" wording was correct all along: spec §13.11.4 mandates fsnotify and the implementation is fsnotify-primary with a poll fallback (`pkg/sync/watch_fsnotify.go`). The real bug was the inverted `TestQuickstart_WatchIsPollBased` test (now `TestQuickstart_WatchUsesFsnotify`) and the stale `pkg/sync/watch.go` "no fsnotify dependency" comment, both corrected.
- **Current**: The quickstart shows output and paths the suite asserts are wrong.
  The documented `podium sync` output reads `personal/hello/greet@1.0.0 →
  .claude/agents/greet.md`, but a skill materializes to
  `.claude/skills/greet/SKILL.md`. The doc also claims watch mode uses `fsnotify`,
  which is correct: spec §13.11.4 mandates it and the implementation is
  fsnotify-primary with a poll fallback. A test wrongly asserted poll-only; the
  Status above records the fix.
- **Evidence**: `docs/getting-started/quickstart.md:155-157`, `:175`, and
  `:183-184`; assertions at `test/e2e/quickstart_flow_test.go:194-200` (skills
  path and absence of the agents path) and `:205-225` (output format), plus the
  poll-based watch assertion.
- **Target**: Correct the quickstart prose and output, or change the code to
  match. The tests already enumerate every divergence.

### G-DOC-4: `podium lint <path>` positional form is documented but fails
- **Severity**: P1. **Lane**: PR. **Status**: closed (doc fix). Stage 3 rewrote the documented invocation to `podium lint --registry <path>` in both authoring tutorials and the CLI reference, since the implemented command requires `--registry` and exits 2 without it (asserted by `TestCommandTutorial_LintPositionalRejected`). The CLI reference's `<path>` prose now describes what the registry root may contain rather than a positional argument.
- **Current**: Both authoring tutorials document `podium lint <path>` with a
  positional path, which exits 2 because `--registry` is required. The CLI
  reference repeats the broken form.
- **Evidence**: `docs/authoring/your-first-skill.md:166`,
  `docs/authoring/your-first-command.md:103`, `docs/reference/cli.md:158`;
  assertion at `test/e2e/command_tutorial_test.go:96-106`.
- **Target**: Fix the documented invocation or accept a positional path in the CLI.

### G-DOC-5: OIDC cookbooks document an unshipped feature
- **Severity**: P0. **Lane**: PR. **Status**: closed (reconciled). The six OIDC cookbooks were rewritten to the implemented `identity_provider:` model and shipped behavior; the verify gate confirms the non-spec terms are gone.
- **Current**: Every OIDC cookbook's central `registry.yaml` snippet and its
  setup commands describe behavior the binary does not provide. A reader who
  follows any guide verbatim gets a registry that resolves every caller as
  anonymous-public.
- **Evidence**: `docs/deployment/oidc/okta.md` and siblings; ties to G-AUTH-1,
  G-AUTH-2, and G-AUTH-9.
- **Target**: Reconcile the docs with the implemented or newly-built verifier and
  config schema, then cover the documented flow with the JWKS harness.

### G-DOC-6: Vector-backends doc default collection name mismatch
- **Severity**: P2. **Lane**: PR. **Status**: closed (doc fix). Stage 3 changed the two Qdrant example values from the hyphenated `podium-artifacts` to the underscore form `podium_artifacts`, matching the in-tree convention (`selfembed_config_test.go`). The "default" framing here is imprecise: `PODIUM_QDRANT_COLLECTION` has no default and is required (spec §13.12), so the fix corrects the example separator rather than a default cell; the doc table's `required` value was already correct.
- **Current**: A worked example uses a hyphenated default collection while the
  implementation defaults to an underscore form (flagged in the
  `vector_backend_config_test.go` skip note).
- **Evidence**: `docs/deployment/vector-backends.md` examples; skip note in
  `test/e2e/vector_backend_config_test.go`.
- **Target**: Align the doc with the implemented default, then assert it.

### G-DOC-7: Landing and index pages with duplicated runnable content
- **Severity**: P2. **Lane**: PR. **Status**: closed (waiver). Stage 3 waived `docs/index.md` in the G-DOC-1 doccov manifest, since its hello-world block duplicates the quickstart and README flows already covered by `quickstart_flow_test.go` and `readme_claims_test.go`. The remaining `docs/**/index.md` pages carry no runnable fenced blocks and need neither a waiver nor a dedupe.
- **Current**: `docs/index.md` and the section index pages repeat the
  hello-world example without an independent test. The content duplicates the
  README and quickstart that are already covered.
- **Target**: Either deduplicate to a single tested source or add a thin
  assertion, resolved by the G-DOC-1 manifest.

---

# VEC: Managed vector backends (live integration)

The default binary ships adapters for Pinecone, Weaviate Cloud, and Qdrant
Cloud. All three are tested only through `httptest` mocks today, and none runs
through the shared conformance contract. This is the center of the new
release-lane work.

### G-VEC-1: No managed vector backend is contacted live
- **Severity**: P1. **Lane**: Release plus manual. **Status**: closed (live suites landed in `pkg/vector/{pinecone,weaviate,qdrant}_live_test.go`: each ingests a corpus, queries, and asserts recall, gated on `PODIUM_LIVE_EXTERNAL=1` plus per-backend credentials. Release-lane CI wiring landed in `make test-live-external` and `.github/workflows/live-external.yml`. Live green requires the accounts.)
- **Current**: Pinecone, Weaviate Cloud, and Qdrant Cloud are exercised only by
  hand-rolled `httptest` mocks, including the one test labeled "integration."
- **Evidence**: `pkg/vector/pinecone.go`, `weaviate.go`, `qdrant.go`; mock tests
  in `pkg/vector/cloud_test.go` and `cloud_providers_test.go`; e2e skips at
  `test/e2e/vector_backend_config_test.go:236`, `:419`, and `:500`;
  `test/integration/pinecone_host_resolve_test.go:19` is mock-only.
- **Target**: A live suite that, for each managed backend, creates or cleans a
  collection, ingests a fixed artifact set, runs a semantic query, and asserts
  recall on the expected ranked result. See the live-suite specification below.
- **Work**: Add `pkg/vector/*_live_test.go` gated on `PODIUM_LIVE_EXTERNAL=1`
  and the per-backend credentials.

### G-VEC-2: Managed backends bypass the shared conformance contract
- **Severity**: P1. **Lane**: Release plus manual. **Status**: closed (each managed live suite runs the shared `vectortest.Suite` against the live instance through `runLiveSuite`. Live green requires the accounts.)
- **Current**: Only `memory`, `pgvector`, and `sqlite-vec` run
  `vectortest.Suite`. The three managed backends are excluded, while the package
  docstring claims they "share one contract."
- **Evidence**: `pkg/vector/vectortest/vectortest.go`; only the three local
  backends invoke `vectortest.Suite`.
- **Target**: Run `vectortest.Suite` against each live managed backend so
  put-query round-trip, tenant boundary, upsert replace, delete, dimension
  mismatch, empty tenant, and top-k bounding are checked against one contract.
  Correct the docstring to state which backends run live and which run against a
  mock.

### G-VEC-3: No e2e wires a real vector backend through the server path
- **Severity**: P1. **Lane**: Release plus manual; PR variant for pgvector. **Status**: closed (`test/e2e/vector_semantic_search_test.go`: the pgvector form runs on the PR lane against an isolated ephemeral schema, and the managed form runs on the Release lane; both assert the expected artifact returns at rank 1 through the HTTP `search_artifacts` surface).
- **Current**: `discovery_search` e2e runs against in-memory BM25. The
  `vector_backend_config` e2e skips every storage happy-path and every wire-level
  isolation assertion.
- **Evidence**: `test/e2e/discovery_search_test.go`;
  `test/e2e/vector_backend_config_test.go:236`, `:419`, `:500`.
- **Target**: Boot the standalone server with `PODIUM_VECTOR_BACKEND` set to each
  managed backend, ingest artifacts, and assert a semantic query returns the
  expected ranked result through the registry HTTP surface. Run the pgvector
  form on the PR lane against the CI Postgres container.

### G-VEC-4: Multi-tenant vector isolation is shallow
- **Severity**: P0. **Lane**: Release plus manual; PR for pgvector. **Status**: closed (per-backend two-tenant isolation in the live suites; pgvector `vec_artifacts` isolation, tenant-scoped purge, and cross-tenant delete in `pgvector_depth_test.go` on the PR lane).
- **Current**: The only isolation assertion is the conformance two-tenant case at
  dim=8. No test covers namespace or payload-filter isolation against a real
  backend, and `vec_artifacts` is a flat `tenant_id`-keyed table not covered by
  the cross-org RLS tests.
- **Evidence**: `vectortest.go` tenant-boundary case; `pkg/vector/pgvector.go`
  schema.
- **Target**: For each managed backend, write two tenants' vectors into the same
  index or collection and assert a query scoped to one tenant never returns the
  other's vectors. For pgvector, extend the cross-org isolation tests to cover
  `vec_artifacts`.

### G-VEC-5: Self-embedding and storage-only modes are untested live
- **Severity**: P1. **Lane**: Release plus manual. **Status**: closed (each managed live suite covers self-embedding and storage-only; self-embedding gates on the per-backend inference-model env and skips when unset. The Release lane runs a local Ollama for the no-account storage-only pairing.)
- **Current**: Each managed backend supports self-embedding (the backend computes
  the vector) and storage-only (the registry computes the vector through an
  embedding provider). Neither mode is tested against a live backend.
- **Evidence**: mode selection documented at
  `docs/deployment/vector-backends.md:27-34`; backend inference flags at
  `cmd/podium-mcp/local_semantic.go:241`.
- **Target**: For each backend, run the self-embedding path with its inference
  model set, and run the storage-only path with each compatible embedding
  provider. Assert both produce correct recall.

### G-VEC-6: Transactional outbox under failure is not exercised live
- **Severity**: P1. **Lane**: Release plus manual. **Status**: closed (retry-under-transient-failure proven deterministically in `test/integration/vector_outbox_consistency_test.go` via a fault-injecting provider through the `store.VectorOutbox` SPI, asserting exactly-once landing. The outbox is backend-agnostic, so a live-managed fault-injection variant adds no logic coverage and is intentionally not built; forcing a transient failure in a real managed service is not deterministically testable.)
- **Current**: For non-collocated backends, ingest coordinates the manifest
  commit and a `vector_pending` row in one transaction, and a background worker
  drives the vector write. No test exercises this against a real managed backend
  under a transient backend failure.
- **Evidence**: outbox description at `docs/deployment/vector-backends.md:202`.
- **Target**: Inject a transient backend error and assert the outbox retries to a
  consistent state without losing or duplicating vectors.

---

# EMB: Embedding providers (live integration)

All four providers are tested only against `httptest` stubs with inline JSON. No
test contacts a real API under any key gate, and there is no recorded-cassette
approach.

### G-EMB-1: No embedding provider is contacted live
- **Severity**: P1. **Lane**: Release plus manual. **Status**: closed (live dimension, determinism, batch, and auth-failure in `pkg/embedding/{openai,cohere,voyage,ollama}_live_test.go`; the rate-limit path (HTTP 429 mapped to `ErrQuota`) is asserted deterministically in `classify_test.go` and `embedding_test.go`, since a live 429 cannot be forced reliably. Release lane carries `OPENAI_API_KEY`, `COHERE_API_KEY`, `VOYAGE_API_KEY`, and a local Ollama.)
- **Current**: OpenAI, Cohere, Voyage, and Ollama are exercised only by
  `httptest` stubs. The package docstring claims providers "replay vendored
  response fixtures," while the fixtures are inline literals.
- **Evidence**: `pkg/embedding/openai.go`, `cohere.go`, `voyage.go`, `ollama.go`;
  stub tests in `pkg/embedding/embedding_test.go` and `providers_test.go`;
  inaccurate docstring at `pkg/embedding/embedding.go:9-10`.
- **Target**: A live test per provider that embeds a known phrase, asserts the
  vector dimension matches the configured model, checks determinism within
  tolerance, exercises batch input, and asserts the error paths (authentication
  failure and rate limiting). Each test gates on `PODIUM_LIVE_EXTERNAL=1` and the
  provider's key. See the live-suite specification below.

### G-EMB-2: Provider wire-format quirks are asserted only against mocks
- **Severity**: P2. **Lane**: Release plus manual. **Status**: closed (live batch-alignment assertions cover OpenAI index reordering and the Cohere response forms; the Cohere nested response form requires `PODIUM_COHERE_MODEL` to select an embed-v4 model on the Release lane).
- **Current**: The providers special-case multiple Cohere response forms and
  OpenAI index reordering, asserted only against inline mock responses.
- **Evidence**: response handling in `pkg/embedding/cohere.go` and `openai.go`.
- **Target**: Assert the same handling against a real response in the live suite,
  so a vendor wire-format change is caught.

---

# PGV: Postgres and pgvector depth

pgvector runs live on the PR lane already, but the conformance is shallow.

### G-PGV-1: pgvector conformance asserts neither metric correctness nor recall
- **Severity**: P1. **Lane**: PR. **Status**: closed (`pgvector_depth_test.go`: cosine-metric correctness on non-trivial vectors plus HNSW ANN recall with a forced index path, PR lane via `PODIUM_POSTGRES_DSN`. The ANN sub-test needs pgvector >= 0.5.0, met by the pinned `pgvector/pgvector:pg16` image.)
- **Current**: The suite runs at dim=8 and asserts near-zero self-distance and
  trivial axis-aligned ordering. The schema creates no vector index, so queries
  are exact scans.
- **Evidence**: `pkg/vector/pgvector_test.go:30`; `pkg/vector/vectortest.go`
  assertions; schema at `pkg/vector/pgvector.go:61-83`.
- **Target**: Assert that the cosine operator returns correct distances on
  non-trivial vectors, add a recall assertion, and exercise an ANN index path
  (HNSW or IVFFlat).

### G-PGV-2: No production-dimension round trip anywhere
- **Severity**: P1. **Lane**: PR. **Status**: closed (`pgvector_depth_test.go` round-trips a 1536-dim vector and rejects a dimension mismatch at that size, PR lane).
- **Current**: Every vector test runs at dim 4, 8, 16, or 32. No backend test
  uses a production dimension.
- **Target**: Add a round trip at the configured model dimension (for example
  1536 for `text-embedding-3-small`) to catch encoding, schema, and performance
  regressions that small dimensions cannot.

### G-PGV-3: pgvector model-versioning path never runs against real Postgres
- **Severity**: P1. **Lane**: PR. **Status**: closed (`pgvector_depth_test.go` exercises `PutModel`/`QueryModel`/`PurgeModelExcept` against real Postgres, including tenant-scoped purge and a legacy-untagged-row case, PR lane).
- **Current**: The model-versioning suite covers only `memory` and `sqlite-vec`.
  pgvector's `PutModel`, `QueryModel`, and `PurgeModelExcept` run against no live
  database.
- **Evidence**: `pkg/vector/model_versioned_test.go:11-12`; pgvector model SQL at
  `pkg/vector/pgvector.go:147-217`.
- **Target**: Add pgvector to the model-versioning suite so re-embed-on-model-
  change correctness is verified on the default standard-deployment backend. Ties
  to the `podium admin reembed` flow at `docs/deployment/vector-backends.md:190`.

---

# STACK: Managed-stack parity and concurrency

### G-STACK-1: Author-to-consumer parity on the managed stack
- **Severity**: P1. **Lane**: live Postgres + S3 (skips without them; no separate opt-in). **Status**: closed. `test/e2e/standard_stack_parity_test.go` boots standard mode (Postgres + S3 + injected-session-token), publishes a bundled-resource layer through a git source as the admin author, then as the authenticated consumer searches, loads, and materializes the artifact, asserting the managed stack reproduces the standalone/filesystem materialization for that artifact. A second layer stages a resource above the 256 KB inline cutoff and asserts the S3 data plane returns it as a presigned URL. The test is idempotent against the shared "default" org schema because the parity comparison is scoped to the layer's artifact.
- **Root cause**: the reingest accepted zero artifacts because the data-plane resource uploader used HTTPS against plain-HTTP MinIO. The object store derives TLS from the `PODIUM_S3_ENDPOINT` URL scheme (§13.12), and a bare `host:port` defaults to TLS on, so every bundled-resource `PUT` failed and the artifact was rejected with `ingest.resource_store_failed`. The reingest response reported `accepted: 0` with no reason, so the rejection was invisible.
- **Fixes**: the reingest endpoint and the `podium layer reingest` CLI now surface `rejected` and `embedding_failures`, so a dropped artifact reports its reason. The test qualifies `PODIUM_S3_ENDPOINT` with its scheme (there is no separate `USE_SSL` knob), and the second layer registers through the injected-token git-source flow rather than an unauthenticated `--local` path that a remote server cannot read.

### G-STACK-2: Concurrency and atomicity are thin
- **Severity**: P1. **Lane**: PR. **Status**: closed (two non-atomic spots left open). Stage 4 added genuinely-concurrent, race-clean coverage: `pkg/audit/file_concurrent_append_test.go` (16 writers on one sink plus 8 sinks in a forest, asserting the linear-chain invariant and one root per writer), `test/integration/ingest_convergence_test.go` (24 concurrent racers, asserting exactly one durable manifest and reader agreement on the content hash), and `test/integration/sync_concurrent_target_test.go` (distinct-target completeness). Two non-atomic spots are left explicitly open with documented reasons: the `cmd/podium-mcp` `contentCache.put` bare `os.WriteFile` (an optional follow-up), and concurrent same-target sync as a guarantee (`pkg/materialize` uses a fixed `.tmp` with no target lock; single-writer-per-target is confirmed as the intended contract, so the test asserts convergence and recovery rather than corruption-free mid-race).
- **Current**: The shared cache directory, the shared audit hash chain, and
  concurrent sync to one target are tested single-threaded or not at all.
- **Target**: Add tests for concurrent same-version ingest, concurrent sync to a
  shared target, and concurrent writers to the audit chain, asserting the chain
  stays valid and the cache stays consistent.

---

# HARN: Harness drift

### G-HARN-1: Real-agent harness suite is out of CI
- **Severity**: P1. **Lane**: manual-only (needs the proprietary CLIs). **Status**: closed (decision accepted: keep manual + documented). `test/harness_integration/README.md` now pins the targeted harness CLI versions the goldens assume (claude-code 2.1.160, cursor-agent 2026.06.02, codex-cli 0.136.0, Gemini CLI 0.44.1) in a single table, and documents the manual cadence for the build-tagged suite (Tier A on adapter-output or CLI-version change; Tier C before a materialization-changing release and on a periodic refresh), so drift is detectable by comparing each run's logged `--version` against the table. A scheduled CI job is not wired because the suite needs the proprietary CLIs and Tier C needs an authenticated harness; promoting the recorded versions into an in-code `testedVersion` assertion in `integration_test.go` is an optional follow-up (not taken).
- **Current**: `test/harness_integration` drives the real Claude Code, Codex,
  Gemini, and Cursor CLIs, gated behind `//go:build harness_integration`, and
  runs in no workflow. Format drift in a harness is caught only against Podium's
  own golden assumptions.
- **Evidence**: `test/harness_integration/integration_test.go`; golden coverage
  in `test/materialization/golden_test.go` and `validity_test.go`.
- **Target**: Pin the harness CLI versions the goldens target and record them.
  Consider a scheduled job on a runner with the CLIs installed.

---

# INFRA: Test infrastructure and hermeticity

### G-INFRA-1: No goroutine-leak detection
- **Severity**: P2. **Lane**: PR. **Status**: closed. `goleak.VerifyTestMain` guards two packages with long-running goroutines. `pkg/sync/leak_test.go` covers the watch and server-source SSE goroutines, and the package passes goleak under `-race`. `cmd/podium-mcp/leak_test.go` covers the stdio bridge's serve loop, whose `startTokenWatch` and `startOverlayWatch` goroutines run for the session and are released by the stop functions `serve()` defers; the package passes goleak (verified by temporarily dropping `defer stopOverlay()`, which makes goleak fail the binary with the leaked `overlayWatchLoop` stack). Adding the guard surfaced one real test leak: `TestMCPOverlayVectorBackend_ReachesAllDocumentedBackends` constructed a sqlite-vec provider (which opens a `*sql.DB` with a `connectionOpener` goroutine) and never closed it; the test now closes every provider it builds. `internal/serverboot` remains goleak-exempt by design: its anchor, verify, retention, and vector-outbox schedulers run on `context.Background()` for the process lifetime with no stop path, so they would fail goleak. A cancelable-scheduler lifecycle for `internal/serverboot` is an optional future rather than a defect fix.
- **Current**: `goleak` appears in no package. Watch mode, MCP stdio, and SSE
  goroutine leaks are unguarded.
- **Target**: Add `goleak.VerifyTestMain` to the packages with long-running
  goroutines.

### G-INFRA-2: Ambient backend env can leak into "backend absent" tests
- **Severity**: P1. **Lane**: PR. **Status**: closed. The scrub is in place (`test/e2e/helpers_test.go`, commit `1b39c60`): `mergeEnv` is the single subprocess-env chokepoint and strips `PODIUM_POSTGRES_DSN[_VECTOR]`, `PODIUM_PGVECTOR_DSN`, and the `PODIUM_S3_*` family. Stage 4 landed the guard in `test/e2e/ambient_env_guard_test.go`: a unit assertion that `mergeEnv` strips each backend var while a non-backend var survives, an explicit-override-survives test, and a real subprocess test that boots a bare `podium serve` with ambient backend env set and asserts the §13.10 no-autostandalone refusal still fires (catching a leak that bypasses `mergeEnv`, not only one inside it).
- **Current**: A recent fix scrubbed `PODIUM_POSTGRES_DSN` and `PODIUM_S3_*` from
  CLI subprocess env so `make test-live` would not suppress the no-autostandalone
  refusal. The class of bug remains: any new test asserting "backend absent"
  behavior inherits ambient backend env.
- **Evidence**: scrub at `test/e2e/helpers_test.go:60-73`.
- **Target**: Route all "backend absent" tests through the scrub helper and add a
  guard that fails if a backend env var is present where the test asserts its
  absence.

### G-INFRA-3: `TEST_INFRASTRUCTURE_PLAN.md` describes an unbuilt system
- **Severity**: P2. **Lane**: documentation. **Status**: closed (superseded). Stage 3 marked `TEST_INFRASTRUCTURE_PLAN.md` superseded with a header that points to the current model (this file's lane table, the real `Makefile` targets, the env-gated live tests, and the overall `COVERAGE_MIN=50` floor) and annotated each aspirational section (the fast/medium/slow lanes, `testcontainers-go`, phase gating, the 95% line and 90% branch floors, `goleak`, mutation testing, and the `identityfaker` family of harnesses) inline as not built, so the file no longer implies coverage that does not exist.
- **Current**: The plan describes fast, medium, and slow lanes, `testcontainers-go`,
  phase gating, 95% coverage floors, and mutation testing. None of it is built,
  and the real coverage floor is 50.
- **Target**: Reconcile the plan with the implemented system or mark it
  superseded, so it stops implying coverage that does not exist.

### G-INFRA-4: Sigstore live signing is manual-only
- **Severity**: P2. **Lane**: manual-only (existing). **Status**: closed (decision: keep manual + documented). `RELEASING.md` → "Sigstore live tests are manual" now records the cadence (run `TestSigstoreKeyless_LiveSmoke` against the Sigstore staging instance on any release whose diff touches `pkg/sign` or the `SignatureProvider` contract), the env it needs, and the corrected `-run` pattern; `OPERATIONS.md` aligns the per-release checklist and the env table to staging. A credentialed CI lane is not wired: the live test needs an ambient OIDC token the Fulcio issuer accepts, and even staging writes to a public transparency log; the staging-Sigstore release-lane wiring is recorded in `RELEASING.md` as an opt-in follow-up.
- **Current**: Live Fulcio and Rekor tests run only through the RELEASING.md
  procedure. They cost money and write to the public transparency log.
- **Target**: Decide whether to fold a single signing smoke into the Release lane
  against a staging Sigstore instance, or keep it manual and document the cadence.

---

# Live external-services suite specification

This is the suite that runs in the Release lane and on manual invocation. It
covers the three managed vector backends and the four embedding providers. It
gates on `PODIUM_LIVE_EXTERNAL=1` plus per-service credentials, and each
sub-suite skips with a reason when its credentials are absent.

## Vector backend coverage matrix

For each of Pinecone, Weaviate Cloud, and Qdrant Cloud, in both self-embedding
and storage-only modes:

- Run `vectortest.Suite` against the live backend (G-VEC-2).
- Ingest a fixed artifact set, run a semantic query, and assert recall on the
  expected ranked result (G-VEC-1, G-VEC-3).
- Write two tenants' vectors and assert query isolation by namespace, collection,
  or payload filter (G-VEC-4).
- Upsert a changed vector and assert replacement; delete and assert removal.
- Record `(model_id, dimensions)` per vector and assert re-embed on model change
  restricts query results to the current model (G-PGV-3 parallel).
- Disable the backend and assert the registry degrades to BM25 with the
  structured degraded indicator (`docs/deployment/vector-backends.md:217`).

## Embedding provider coverage

For each of OpenAI, Cohere, Voyage, and Ollama:

- Embed a known phrase and assert the vector dimension matches the configured
  model.
- Assert determinism within tolerance across two calls.
- Exercise batch input and assert per-item alignment (covers OpenAI index
  reordering and the Cohere response forms, G-EMB-2).
- Assert the authentication-failure and rate-limit error paths.

## Storage-only pairings

The storage-only vector tests require an embedding provider. Pair each managed
backend with at least one provider so the registry-computed vector path is
covered end-to-end. Ollama needs no account and is the cheapest default pairing
for the storage-only matrix. OpenAI is the reference pairing for a production
dimension (G-PGV-2).

## Gating and invocation

- Switch: `PODIUM_LIVE_EXTERNAL=1`.
- Local: `make test-live-external` (new target) sets the switch and forwards the
  credentials present in the environment.
- CI: a Release-lane job sets the switch and injects every credential from
  secrets. The job does not run on pull requests or nightly.
- Each sub-suite uses the existing skip-with-reason pattern from
  `pkg/objectstore/s3_live_test.go` and `pkg/sign/sigstore_live_test.go`.

## Environment variables read by the registry

| Backend or provider | Variables |
|:--|:--|
| Selection | `PODIUM_VECTOR_BACKEND`, `PODIUM_EMBEDDING_PROVIDER`, `PODIUM_EMBEDDING_MODEL`, `PODIUM_NO_EMBEDDINGS` |
| Pinecone | `PODIUM_PINECONE_API_KEY`, `PODIUM_PINECONE_INDEX`, `PODIUM_PINECONE_HOST` (auto-resolved), `PODIUM_PINECONE_NAMESPACE` (default `default`), `PODIUM_PINECONE_INFERENCE_MODEL` (self-embed), `PODIUM_PINECONE_CONTROL_PLANE` (override) |
| Weaviate Cloud | `PODIUM_WEAVIATE_URL`, `PODIUM_WEAVIATE_API_KEY`, `PODIUM_WEAVIATE_COLLECTION`, `PODIUM_WEAVIATE_VECTORIZER` (self-embed), `PODIUM_WEAVIATE_GRPC_URL` (reserved) |
| Qdrant Cloud | `PODIUM_QDRANT_URL`, `PODIUM_QDRANT_API_KEY`, `PODIUM_QDRANT_COLLECTION`, `PODIUM_QDRANT_INFERENCE_MODEL` (self-embed), `PODIUM_QDRANT_GRPC_PORT` (reserved, `6334`) |
| OpenAI | `OPENAI_API_KEY`, `PODIUM_OPENAI_MODEL`, `PODIUM_OPENAI_BASE_URL`, `PODIUM_OPENAI_ORG` |
| Cohere | `COHERE_API_KEY`, `PODIUM_COHERE_MODEL` |
| Voyage | `VOYAGE_API_KEY`, `PODIUM_VOYAGE_MODEL` |
| Ollama | `PODIUM_OLLAMA_URL` (default `http://localhost:11434`), `PODIUM_OLLAMA_MODEL` |

Source: `cmd/podium-mcp/local_semantic.go:178-241`,
`internal/serverboot/backend_config_test.go:147-161`, and
`docs/deployment/vector-backends.md`.

## Accounts and credentials to provision

The Release lane needs an account or runner setup for each service below. Free
tiers exist for most; confirm current limits at signup, because quotas change.

| Service | Account needed | Provision | Credentials for CI secrets | Notes |
|:--|:--|:--|:--|:--|
| Pinecone | Yes | Create a serverless index. Free Starter tier is usually sufficient for one small index. | `PODIUM_PINECONE_API_KEY`, `PODIUM_PINECONE_INDEX` | Self-embedding needs a hosted Integrated Inference model (for example `multilingual-e5-large`). Storage-only needs an embedding provider below. The host auto-resolves from the index name. |
| Weaviate Cloud | Yes | Create a Sandbox or Serverless cluster. The free Sandbox expires after a set period, so a Serverless cluster is steadier for a recurring lane. | `PODIUM_WEAVIATE_URL`, `PODIUM_WEAVIATE_API_KEY`, `PODIUM_WEAVIATE_COLLECTION` | A `text2vec-openai` vectorizer needs an OpenAI key configured on the Weaviate side. `text2vec-weaviate` self-embeds with no external key. |
| Qdrant Cloud | Yes | Create a free cluster (the 1 GB free tier is sufficient). | `PODIUM_QDRANT_URL`, `PODIUM_QDRANT_API_KEY`, `PODIUM_QDRANT_COLLECTION` | The `qdrant-cloud` backend can also point at a self-hosted Qdrant container for a cheaper PR-lane smoke, separate from this managed test. |
| OpenAI | Yes, with billing | Create an API key. Embedding calls are inexpensive. | `OPENAI_API_KEY` | Default model `text-embedding-3-small` (1536) or `text-embedding-3-large` (3072). |
| Cohere | Yes | Create an API key. A trial key is free but rate-limited; a production key is steadier for the lane. | `COHERE_API_KEY` | Embed models. |
| Voyage AI | Yes | Create an API key. A free token allotment is usually available. | `VOYAGE_API_KEY` | Embed models. |
| Ollama | No | Install Ollama on the runner and pull an embedding model (for example `nomic-embed-text`). | none (`PODIUM_OLLAMA_URL` defaults to localhost) | Self-hosted. The cheapest storage-only pairing and the only provider needing no account. |

Dex is the remaining external account-free dependency for auth: it runs from
`docker-compose.yml` with no account. See G-AUTH-4.

## CI wiring to add

- A `make test-live-external` target that sets `PODIUM_LIVE_EXTERNAL=1` and
  forwards present credentials, mirroring `make test-live`.
- A Release-lane job (in `.github/workflows/release.yml` or a dedicated
  `live-external.yml` triggered on release) that injects every credential from
  GitHub Actions secrets and runs the target. The job does not trigger on
  `pull_request` and is absent from `nightly.yml`.
- An Ollama setup step in that job (install plus `ollama pull`) so the
  storage-only matrix has a no-account provider available.
- Secret names matching the credential column above.

## User-journey coverage gaps

These gaps come from a 2026-06-03 user-journey coverage pass. The pass enumerated realistic deployment and usage journeys and gap-checked each against the e2e and integration suites. It covered solo filesystem, team git-source, standard or managed, harness materialization, multi-layer composition, search and discovery, scale and load, configuration permutations, and lifecycle operations. The entries below are the journeys that are partially covered or missing, grouped by area.

### JOURNEY: End-to-end author-to-consumer journeys

#### G-JOURNEY-1: Git-source webhook reingest produces a new searchable artifact version
- **Severity**: P1. **Lane**: PR; needs a standard server and a seeded file:// git repo, no external network. **Status**: closed (test/e2e/git_source_journey_test.go:TestGitJourney_WebhookReingestNewVersionSearchable seeds a file:// git repo, registers a git layer, delivers a valid-HMAC webhook for the initial commit, asserts the artifact ingests/searches/loads at 1.0.0, commits a version bump to 2.0.0, delivers the webhook again, and asserts search_artifacts resolves the new latest 2.0.0 and load_artifact serves both versions). **Realism**: common. **Nearest test**: test/e2e/http_api_test.go:TestHTTPAPI_IngestWebhookInvalid.
- **Current**: TestHTTPAPI_IngestWebhookInvalid registers a git layer, signs a valid HMAC delivery, and confirms it reaches the ingest pipeline, but the fake repo is unreachable so the delivery stops at ingest.source_unreachable and never ingests a new version, and TestReingestPipeline_GitSource asserts only accepted=1 and last_ingested_at on a manual reingest.
- **Target**: Boot a standard server, register a git layer pointing at a seeded file:// repo, deliver a valid-HMAC webhook for the initial commit, assert the artifact ingests and is searchable, push a new commit, deliver the webhook again, and assert search_artifacts returns the new version.
- **Sketch**: Register a git layer over a seeded file:// repo, POST a valid-HMAC webhook, assert the artifact is searchable, commit a new version, POST again, and assert search_artifacts returns the updated version.

#### G-JOURNEY-2: Git-source polling reingest detects a new commit without a webhook
- **Severity**: P2. **Lane**: PR; needs a standalone server and a seeded file:// git repo with poll trigger invocable, no external network. **Status**: closed (test/e2e/git_source_journey_test.go:TestGitJourney_PollReingestDetectsNewCommit registers a git layer with no webhook over a seeded file:// repo, runs a poll cycle — one /v1/layers/reingest poke, the unit `podium layer watch` loops — to ingest the initial commit, commits a new version, polls again, and asserts load_artifact serves the updated artifact and the audit log records two poll-driven layer.ingested events whose references are the two distinct commit SHAs plus artifact.published for both versions). **Realism**: common. **Nearest test**: test/integration/reingest_pipeline_test.go:TestReingestPipeline_GitSource.
- **Current**: The source SPI defines TriggerPoll in pkg/layer/source/source.go and only manual git reingest is exercised through TestReingestPipeline_GitSource, so no test drives a poll cycle that detects a changed ref and reingests.
- **Target**: Register a git layer with no webhook over a seeded file:// repo, commit a new artifact, invoke the poll cycle, and assert the registry detects the changed ref, reingests, load_artifact returns the updated artifact, and the audit log records a poll-driven reingest.
- **Sketch**: Register a git layer with no webhook, commit a new artifact to the seeded repo, trigger the poll cycle, and assert load_artifact returns the updated artifact and the audit log records a poll-driven reingest.

### MATERIALIZE: Harness materialization matrix

#### G-MATERIALIZE-1: Single-pass sync of every first-class type asserts mcp-server and hook config-merge files
- **Severity**: P1. **Lane**: PR; CLI sync over a filesystem registry, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_AllFirstClassTypesClaudeCodeSinglePass asserts every per-type claude-code path plus the .claude/settings.json x-podium-id hook marker and the .mcp.json x-podium server index in one pass). **Realism**: common. **Nearest test**: test/e2e/artifact_types_test.go:TestArtifactTypes_MCPServerVisibleViaSync.
- **Current**: artifact_types_test.go scaffolds, lints, and asserts the claude-code layout for skill, agent, context, command, rule, and hook, TestHarness_ClaudeCodeHookDefaultCase asserts the hook merges into .claude/settings.json, and the golden suite snapshots the full tree, but TestArtifactTypes_MCPServerVisibleViaSync syncs the mcp-server with --harness none and never asserts the .mcp.json config-merge file through a real podium sync. Two gap-checked scenarios (all-types claude-code and the mcp-server path) merge here.
- **Target**: Scaffold one artifact of each first-class type into a single-layer registry, run one podium sync --harness claude-code, and assert every per-type output path together including .claude/settings.json for the hook and a Podium-owned server entry in .mcp.json derived from server_identifier, with both config-merge files carrying ownership markers.
- **Sketch**: Build a one-artifact-per-type registry, run a single podium sync --harness claude-code, and assert all per-type paths plus Podium-owned entries in .claude/settings.json and .mcp.json in one pass.

#### G-MATERIALIZE-2: Re-sync idempotency and config-merge reconciliation through the CLI against a Cursor target
- **Severity**: P1. **Lane**: PR; CLI sync over a filesystem registry into a Cursor target, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_CursorResyncConfigMergeReconcileIdempotent re-syncs cursor over a rule, hook, and mcp-server with operator edits in .cursor/hooks.json and .cursor/mcp.json and asserts a byte-identical rule file, stable lock content_hash, one Podium entry per artifact, and surviving operator entries). **Realism**: common. **Nearest test**: pkg/sync/cleanup_test.go:TestRun_OrphanedConfigMergeReconciledNotDeleted.
- **Current**: TestRun_OrphanedConfigMergeReconciledNotDeleted and TestRun_RemovesFilesForDroppedArtifact are pkg/sync unit tests, TestHarness_SyncIdempotent re-runs only claude-code, and TestFilesystemSync_SyncIdempotent covers standalone OpWrite for the none harness, so config-merge re-sync idempotency through the CLI against a Cursor target with operator edits is not asserted. Two gap-checked scenarios (resync-config-merge-reconcile and resync-no-diff cursor) merge here.
- **Target**: Run podium sync --harness cursor twice over a rule, a hook, and an mcp-server, add a non-Podium entry to .cursor/hooks.json and .cursor/mcp.json between runs, and assert the second tree is byte-identical, lock content_hash entries are unchanged, each config file holds exactly one Podium entry per artifact, and the operator entries survive.
- **Sketch**: Sync --harness cursor over a rule, hook, and mcp-server, insert operator entries into .cursor/hooks.json and .cursor/mcp.json, re-sync, and assert byte-equal Podium entries, stable lock hashes, and surviving operator content.

#### G-MATERIALIZE-3: Watch loop rematerializes on a rule-body edit and cleans stale output within one session
- **Severity**: P1. **Lane**: PR; CLI watch against a temp registry and target, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_WatchEditAndStaleCleanupInOneSession edits a rule body and polls .claude/rules/house.md for the new content, deletes the command artifact and polls until .claude/commands/deploy.md is gone, then SIGINTs with exit 0 — all in one watch session). **Realism**: common. **Nearest test**: test/e2e/filesystem_sync_test.go:TestFilesystemSync_WatchRematerializesOnChange.
- **Current**: TestFilesystemSync_WatchRematerializesOnChange adds a new artifact and polls without editing an existing rule body, TestFilesystemSync_StaleFilesRemovedOnDelete exercises delete-driven cleanup only on a manual second sync, and TestFilesystemSync_WatchExits0OnSIGINT covers shutdown, so the edit-and-stale-cleanup-inside-the-watch-loop path is split across three journeys.
- **Target**: Start podium sync watch against a temp registry and target, edit the rule ARTIFACT.md and poll .claude/rules/{name}.md until the new content appears, delete the command artifact directory and poll until .claude/commands/{name}.md is removed by the watcher, then send SIGINT and assert exit 0.
- **Sketch**: Run sync watch, edit a rule body and poll the materialized rule file for new content, delete a command artifact and poll until its output is removed, then SIGINT and assert exit 0.

#### G-MATERIALIZE-4: Unsupported-type matrix enforced through podium sync for pi and claude-desktop
- **Severity**: P1. **Lane**: PR; CLI sync over a filesystem registry, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_UnsupportedTypeMatrixPiAndClaudeDesktop asserts pi writes only .pi/prompts/deploy.md with no agent output, claude-desktop writes nothing project-level beyond .podium/, and an agent target_harnesses:[pi] surfaces a lint.harness_capability "adapter pi cannot translate" diagnostic). **Realism**: occasional. **Nearest test**: pkg/adapter/capability_matrix_test.go:TestCapabilityMatrix_Types.
- **Current**: TestCapabilityMatrix_Types sweeps every type against every adapter at the adapter.Adapt level and asserts unsupported cells yield zero files, but no test asserts through podium sync that pi writes only .pi/prompts for a command while emitting no agent output, that claude-desktop writes no project-level files, or that a declared-but-unsupported combination surfaces a materialize warning or error.
- **Target**: Run podium sync of an agent and a command for harness pi and separately for claude-desktop, assert pi materializes only .pi/prompts/{name}.md, claude-desktop writes nothing project-level, and the declared-but-unsupported combinations surface a diagnostic.
- **Sketch**: Sync an agent and a command for pi and for claude-desktop, assert pi writes only the command prompt, claude-desktop writes nothing project-level, and the unsupported combinations emit a diagnostic.

#### G-MATERIALIZE-5: Changing a hook event reconciles the prior translated entry before re-merge on gemini
- **Severity**: P1. **Lane**: PR; CLI sync over a filesystem registry, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_GeminiHookEventChangeReconciles changes hook_event pre_tool_use→post_tool_use, bumps the version, and re-syncs gemini to assert the stale BeforeTool key is gone, AfterTool appears exactly once, and the operator key survives; the script-path rewrite is covered by pkg/adapter/hook_events_test.go. Fixed a real bug: stripPodiumOwned left an empty native-event array behind after the event moved — now pruned). **Realism**: occasional. **Nearest test**: pkg/materialize/merge_test.go:TestConfigMerge_AccumulateIdempotentRemove.
- **Current**: TestConfigMerge_AccumulateIdempotentRemove and TestConfigMerge_MapIndexReconcile prove strip-before-merge at the unit level, and the gemini golden snapshot pins event translation, but no test changes a hook_event and bumps the version then re-syncs gemini to assert the old translated entry is removed and the new one appears once with the bundled script path rewritten.
- **Target**: Sync a hook for harness gemini producing a .gemini/settings.json event entry, change its hook_event from pre_tool_use to pre_shell_execution and bump the version, re-sync, and assert the prior translated event entry is gone, the new translated event appears once, the script path is rewritten, and operator keys survive.
- **Sketch**: Sync a gemini hook, change its hook_event and bump the version, re-sync, and assert the old translated entry is removed, the new one appears once with the script path rewritten, and operator settings survive.

#### G-MATERIALIZE-6: Removing an agent and mcp-server deletes standalone output, cleans empty parents, and reconciles config-merge
- **Severity**: P1. **Lane**: PR; CLI sync over a filesystem registry, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_RemoveAgentAndMCPServerCleansAndReconciles removes an agent and an mcp-server with a seeded operator .mcp.json entry, then asserts the agent file is deleted, the empty .claude/agents/ parent is cleaned, the Podium server entry and x-podium index are stripped, and the operator entry survives). **Realism**: common. **Nearest test**: pkg/sync/cleanup_test.go:TestRun_RemovesFilesForDroppedArtifact.
- **Current**: TestRun_RemovesFilesForDroppedArtifact deletes a dropped context artifact's file and TestRun_OrphanedConfigMergeReconciledNotDeleted strips a removed hook's entry while preserving an operator key, but the standalone case uses context rather than an agent, the config-merge case uses a hook rather than an mcp-server with an operator-authored entry, and empty-parent-directory cleanup is not asserted.
- **Target**: Sync an agent and an mcp-server, seed an operator entry into .mcp.json, remove both artifacts, re-sync, and assert the agent file is deleted, the now-empty parent directory is cleaned, the Podium mcp-server entry is stripped, and the operator entry survives.
- **Sketch**: Sync an agent and an mcp-server with a seeded operator .mcp.json entry, remove both, re-sync, and assert agent-file deletion, empty-parent cleanup, the Podium entry stripped, and the operator entry intact.

#### G-MATERIALIZE-7: Re-sync reconciliation of the OpInject text and TOML path through real sync for codex, opencode, and pi
- **Severity**: P1. **Lane**: PR; CLI sync over a filesystem registry, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_CodexResyncInjectAndTomlReconcile runs the Target verbatim — codex sync over a rule and a hook, an operator line hand-edited into AGENTS.md and .codex/config.toml, a rule-body change with a version bump, then a re-sync asserting one reconciled Podium block per artifact, surviving operator lines, and valid TOML parsed via BurntSushi/toml; opencode/pi AGENTS.md inject shares the same OpInject path the codex case exercises). **Realism**: common. **Nearest test**: pkg/materialize/merge_test.go:TestStripPodiumBlocks_PreservesOperatorContent.
- **Current**: Strip-before-inject idempotency and operator-content preservation for AGENTS.md, GEMINI.md, and .codex/config.toml are unit-tested in pkg/materialize/merge_test.go, and a single codex hook inject into config.toml is asserted in harness_materialization_test.go, but no test runs podium sync twice for codex, opencode, or pi with an operator edit between runs to assert the OpInject path reconciles in place. The new-gap MATERIALIZE entries cover the OpMergeJSON JSON path (cursor, gemini, .mcp.json) but not the OpInject markdown and TOML path.
- **Target**: Run podium sync --harness codex over a rule and a hook producing AGENTS.md Podium-injected blocks and a .codex/config.toml [[hooks]] entry, hand-edit a non-Podium line into each file between runs, change the rule body and bump the version, re-sync, and assert each prior Podium block is replaced once in place, the operator lines survive, and the TOML stays valid.
- **Sketch**: Sync --harness codex over a rule and a hook, insert operator lines into AGENTS.md and .codex/config.toml, edit the rule body and bump the version, re-sync, and assert one reconciled Podium block per artifact, surviving operator lines, and valid TOML.

#### G-MATERIALIZE-8: Type-filtered multi-harness sync materializes only the selected types and stays idempotent
- **Severity**: P2. **Lane**: PR; CLI sync over a filesystem registry, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_OpenCodeTypeFilteredIdempotent runs opencode sync with the spec §7.5 comma-separated `--type rule,context` over an all-types registry, asserts AGENTS.md injected rules and the .podium/context bucket exist while .opencode/commands and opencode.json are absent, and re-runs to confirm a byte-identical tree. The sketch's repeated `--type rule --type context` form was reconciled to the spec's documented comma list). **Realism**: occasional. **Nearest test**: pkg/sync/scope_run_test.go:TestRun_ScopeTypeFilters.
- **Current**: TestRun_ScopeTypeFilters runs sync.Run with a single skill-only type filter and the e2e scope tests cover --include, but no test runs podium sync --harness opencode --type rule --type context and asserts excluded hook and mcp-server outputs are absent while the filtered set stays idempotent.
- **Target**: Run podium sync --harness opencode --type rule --type context over an all-types registry, assert AGENTS.md injected rules and .podium/context buckets exist while .opencode/commands and opencode.json mcp entries are absent, and re-run to confirm idempotency.
- **Sketch**: Run sync --harness opencode --type rule --type context over an all-types registry, assert rule and context outputs present and command and mcp outputs absent, and re-run for idempotence.

#### G-MATERIALIZE-9: Removing a claude-cowork plugin reconciles marketplace.json and cleans the plugin tree
- **Severity**: P2. **Lane**: PR; CLI sync over a filesystem registry into a claude-cowork target, no external infra. **Status**: closed (test/e2e/materialize_matrix_test.go:TestMaterialize_ClaudeCoworkRemovePluginReconcilesAndCleans syncs a skill, mcp-server, and hook with a seeded operator marketplace entry, removes the artifacts, re-syncs, and asserts the Podium marketplace and .mcp.json entries are stripped, the nested plugins/{id} trees are cleaned, and the operator entry survives. Fixed a real bug: stale-file cleanup pruned only the file's immediate parent, leaving nested plugins/{id}/skills/ and plugins/{id}/hooks/ ancestors orphaned — now prunes the empty ancestor chain up to the target root; unit-pinned by pkg/sync/cleanup_test.go:TestRun_RemovesEmptyParentTreeForDroppedArtifact). **Realism**: occasional. **Nearest test**: test/e2e/harness_materialization_test.go:TestHarness_ClaudeCoworkPluginLayout.
- **Current**: harness_materialization_test.go asserts the initial claude-cowork write of plugins/{id}/, the .claude-plugin/plugin.json manifest, and the repository-root .claude-plugin/marketplace.json entry, and pkg/sync/cleanup_test.go covers standalone deletion and JSON reconciliation at unit scale, but no test re-syncs claude-cowork after removing a plugin to assert the marketplace.json Podium entry is stripped, the plugins/{id} tree is cleaned, and an operator marketplace entry survives.
- **Target**: Sync --harness claude-cowork over a skill, an mcp-server, and a hook producing a plugins/{id} tree plus marketplace.json and plugins/{id}/.mcp.json entries, seed an operator marketplace entry, remove the artifacts, re-sync, and assert the Podium marketplace and .mcp.json entries are stripped, the emptied plugins/{id} directories are cleaned, and the operator entry survives.
- **Sketch**: Sync --harness claude-cowork over a skill, mcp-server, and hook with a seeded operator marketplace entry, remove the artifacts, re-sync, and assert the Podium marketplace and .mcp.json entries are stripped, the plugin tree is cleaned, and the operator entry survives.

### MULTILAYER: Multi-layer composition and overlays

#### G-MULTILAYER-1: Layer reorder flips the winning artifact and re-sync cleans the prior winner outputs
- **Severity**: P1. **Lane**: PR; needs a server with two user layers plus CLI sync, no external infra. **Status**: open (missing). **Realism**: occasional. **Nearest test**: test/e2e/cli_reference_test.go:TestCLI_LayerReorder.
- **Current**: TestCLI_LayerReorder registers two layers with no shared artifact ID and asserts only that the reordered layer's Order value increases with no sync, and pkg/sync/cleanup_test.go covers stale deletion only on artifact removal, so no test ties a reorder to a re-sync that flips the winning artifact version and cleans the prior winner's outputs.
- **Target**: Register two personal layers carrying the same artifact at distinct versions and outputs, sync to materialize layer B's winner, run podium layer reorder so layer A wins, re-sync, and assert the version flips, the prior winner's standalone files are deleted, the prior Podium entries are stripped from a config-merge file, and operator content is preserved.
- **Sketch**: Register two personal layers sharing one artifact at distinct versions, sync, reorder so the other layer wins, re-sync, and assert the winning version flips, prior standalone files are deleted, the config-merge file keeps only the new Podium entries, and operator content survives.

#### G-MULTILAYER-2: Hidden parent merges fields from a restricted layer the caller cannot discover
- **Severity**: P1. **Lane**: PR; needs a server with mixed-visibility layers (per-identity visibility, blocked by all-public standalone harness). **Status**: open (partial). **Realism**: occasional. **Nearest test**: test/e2e/artifact_extends_test.go:TestExtends_HiddenParentMergedResult.
- **Current**: TestExtends_HiddenParentMergedResult and TestExtends_HiddenParentNotInSearch are skipped because the standalone e2e harness boots all layers public and cannot hide a parent from a caller, and the behavior is covered only in pkg/registry/core TestExtends_HiddenParent.
- **Target**: Boot a server with a parent in an organization-visibility layer and a public child that extends it in a higher-precedence layer, query as an unauthenticated public-mode caller, assert the child's merged manifest carries the parent's inherited fields and most-restrictive sensitivity, and assert the parent ID is not loadable and never appears in search_artifacts or load_domain.
- **Sketch**: Ingest a parent into an org-visibility layer and a public child that extends it, query as an unauthenticated caller, and assert the child merges inherited fields while the parent ID is not loadable or enumerable.

#### G-MULTILAYER-3: Personal, team, and org layers shadow per-caller and pinned parents stay fixed on org publish
- **Severity**: P1. **Lane**: PR; needs a server with org, team, and personal layers plus per-caller tokens (per-identity visibility). **Status**: open (partial). **Realism**: common. **Nearest test**: test/e2e/artifact_extends_test.go:TestExtends_ChainedInheritance.
- **Current**: TestExtends_ChainedInheritance resolves a three-layer A-extends-B-extends-C chain to the top version with merged fields and TestExtends_OrgWideSkillExample covers a two-layer team-over-org union, but parent-pin stability is unit-only because TestExtends_PinNoSilentPropagation and TestExtends_PinReingestPicksNewerParent are skipped, so the per-caller-winner-differs plus pinned-parent-unchanged-on-org-publish journey is not exercised.
- **Target**: Boot a server with an org layer, a registered team layer, and alice's personal layer all carrying the same skill with extends up the chain, resolve the effective view as alice to her personal version and a caller without the personal layer to the team version, and assert the org and team pinned parents do not change when the org publishes a new patch.
- **Sketch**: Boot org, team, and personal layers carrying one skill with extends declarations, call load_artifact as alice and as bob, assert the per-caller winners differ, and assert pinned parents stay fixed after an org publish.

#### G-MULTILAYER-4: Conflicting multi-layer DOMAIN.md featured and deprioritize lists merge with overlay search fusion
- **Severity**: P2. **Lane**: PR; needs a server with two layers plus a workspace overlay, no external infra. **Status**: open (partial). **Realism**: edge. **Nearest test**: test/e2e/overlay_domain_merge_test.go:TestOverlayDomainMerge_DescriptionAndDirectChild.
- **Current**: overlay_domain_merge_test.go asserts an overlay DOMAIN.md description and direct child merge into load_domain and domain_modeling_test.go asserts layer-merge folding for unlisted, max_depth, fold_below, and keywords, but no test composes two layers with conflicting featured and deprioritize lists nor asserts an overlay keyword surfaces an overlay artifact in fused search.
- **Target**: Compose an admin layer and a user layer whose DOMAIN.md files conflict on featured and deprioritize, add a workspace overlay artifact and keyword, assert the merged notable ordering applies append-unique with most-restrictive folding in load_domain, and assert the overlay artifact surfaces in search_artifacts via the overlay keyword.
- **Sketch**: Compose two layers with conflicting featured and deprioritize DOMAIN.md lists plus an overlay artifact and keyword, assert the merged notable ordering in load_domain, and assert the overlay artifact surfaces in search_artifacts via the overlay keyword.

### VEC: Semantic and hybrid search journeys

#### G-VEC-7: Query-time embedding-provider failure degrades to BM25 and recovers semantic fusion
- **Severity**: P1. **Lane**: PR; needs pgvector (PODIUM_POSTGRES_DSN) plus a controllable mock embedder. **Status**: closed (test/e2e/vector_hybrid_search_test.go:TestVectorHybrid_DegradesToBM25AndRecovers boots pgvector with a topic-routed mock embedder, flips it to 429 mid-query, asserts BM25 keeps serving with the zero-overlap semantic target demoted and the lexical distractor still present, then restores it and asserts the target returns to rank 1. Closing it surfaced and fixed a real bug: bootstrap and reingest never embedded artifacts into a collocated backend, so semantic search silently degraded to BM25 (internal/serverboot collocated-vector ingest). **Realism**: occasional. **Nearest test**: test/e2e/vector_backend_config_test.go:TestVectorBackend_SearchDegradesToBM25WhenVectorUnreachable.
- **Current**: TestVectorBackend_SearchDegradesToBM25WhenVectorUnreachable and EmptyEmbeddingProviderDegradesToBM25 assert BM25 fallback for an unreachable backend or disabled provider at startup, but no test makes the embedding provider return 429 or errors during a live query then restores it. Five gap-checked VEC and SEARCH scenarios collapse onto this degrade-under-error-then-recover gap.
- **Target**: Boot pgvector with a controllable mock embedder, serve a baseline query, flip the embedder to return 429 and assert search_artifacts keeps returning ranked BM25 results with a degraded response or readiness signal and no silent empty set, then restore the embedder and assert semantic fusion resumes and the ranking changes on the same query.
- **Sketch**: Boot pgvector with a controllable mock embedder, serve a baseline query, flip the embedder to 429 and assert non-empty BM25 results plus a degraded signal, then restore it and assert semantic fusion resumes and the ranking changes.

#### G-VEC-8: Hybrid RRF ranks a paraphrased query whose words are absent from the target
- **Severity**: P1. **Lane**: PR; needs pgvector (PODIUM_POSTGRES_DSN) plus a mock embedder. **Status**: closed (test/e2e/vector_hybrid_search_test.go:TestVectorHybrid_RRFRanksParaphraseThenChangesWithoutEmbedder ingests a corpus whose target shares no word with the query, asserts the topic-routed vector half lifts it to rank 1 over a lexical distractor, then re-runs against a second server with PODIUM_EMBEDDING_PROVIDER unset and asserts the rank order differs, isolating the RRF semantic contribution). **Realism**: common. **Nearest test**: test/e2e/vector_semantic_search_test.go:TestVectorSemanticSearch_PgVectorThroughServer.
- **Current**: TestVectorSemanticSearch_PgVectorThroughServer asserts the matching artifact ranks first, but the query is lexically present in the target description and the file states the assertion holds whether the vector path contributes or silently degrades, so the semantic contribution is not isolated.
- **Target**: Reuse the pgvector boot, ingest a corpus where the target description and when_to_use share no words with the query, assert the semantically correct artifact ranks first, then re-run with PODIUM_EMBEDDING_PROVIDER unset and assert the rank order changes to prove reciprocal rank fusion supplied the top result.
- **Sketch**: Ingest a corpus whose target shares no words with a paraphrased query, assert the semantic match ranks first, then unset the embedding provider and assert the rank order differs.

#### G-VEC-9: Vector outbox lag signal fires under a bulk-ingest burst then drains to consistency
- **Severity**: P1. **Lane**: PR; needs pgvector (PODIUM_POSTGRES_DSN), a mock embedder, and low lag thresholds. **Status**: closed (internal/serverboot/vector_outbox_lag_burst_test.go:TestVectorOutbox_LagSignalUnderBurstThenDrains ingests a 320-artifact burst through the outbox, fails the first drain pass so depth stays above PODIUM_VECTOR_OUTBOX_LAG_DEPTH and the real vectorDrainWorker records a single vector.outbox_lagging audit event through a FileSink, then recovers the backend, drains to depth 0 with no duplication, and asserts the registry ranks the target first. White-box in-process so it runs on the PR lane with no external infra; the outbox lag machinery is backend-agnostic. **Realism**: occasional. **Nearest test**: test/integration/vector_outbox_consistency_test.go:TestVectorOutbox_RetriesToConsistency.
- **Current**: vector_outbox_consistency_test.go drives a fault-injecting provider and asserts the outbox drains to zero with no lost or duplicated vectors, and vector_outbox_test.go unit-tests the publishStats lagging transition, but no test ingests a burst large enough to cross PODIUM_VECTOR_OUTBOX_LAG_DEPTH or LAG_AGE and observe the vector.outbox_lagging signal end to end.
- **Target**: Boot pgvector with a mock embedder and low lag thresholds, ingest 300-plus artifacts in a short window, assert the outbox crosses LAG_DEPTH or LAG_AGE and records vector.outbox_lagging, then assert the batched drain reaches consistency and semantic search ranks the expected artifact first.
- **Sketch**: Boot pgvector with low lag thresholds, ingest 300-plus artifacts rapidly, assert the outbox crosses the lag threshold and signals vector.outbox_lagging, then assert the drain catches up and semantic search ranks the expected artifact first.

#### G-VEC-10: Admin reembed re-tags vectors after a model switch and purges stale-model vectors
- **Severity**: P2. **Lane**: PR; needs pgvector (PODIUM_POSTGRES_DSN) plus a mock embedder. **Status**: closed (test/e2e/vector_reembed_test.go:TestVectorReembed_ModelSwitchRetagsAndPurges ingests on pgvector with embedder model A, reboots over the same schema and shared HOME with model B so the idempotent re-ingest leaves the rows model-A, POSTs /v1/admin/reembed, and asserts every artifact vector re-tags to model B, the model-A rows purge tenant-scoped while a seeded globex row survives, semantic search returns the target first, and load_artifact content hashes are unchanged). **Realism**: occasional. **Nearest test**: pkg/vector/pgvector_depth_test.go:TestPgVector_Depth_ModelVersioning.
- **Current**: pgvector_depth_test.go exercises PutModel, model-restricted QueryModel, and PurgeModelExcept against live Postgres, and extra_handlers_test.go asserts the /v1/admin/reembed handler runs past validation, but no test drives the admin reembed flow after a configured model switch end to end.
- **Target**: Ingest artifacts with a mock embedder at model A on pgvector, change the configured embedding model to B, POST /v1/admin/reembed, and assert vectors carry model B, model-A vectors are purged, the operation is tenant-scoped, semantic search returns the expected top result, and manifest content hashes are unchanged.
- **Sketch**: Ingest at model A on pgvector, switch the configured model to B, POST /v1/admin/reembed, and assert vectors re-tag to B, model-A vectors purge tenant-scoped, semantic search is correct, and content hashes are untouched.

#### G-VEC-11: Pinecone self-embedding versus external embedder return the same top result live
- **Severity**: P2. **Lane**: Release or manual; needs live Pinecone (PODIUM_LIVE_EXTERNAL=1, PODIUM_PINECONE_API_KEY, PODIUM_PINECONE_INFERENCE_MODEL). **Status**: closed (test/e2e/vector_pinecone_routes_live_test.go:TestVectorPineconeRoutes_SelfEmbedVsExternalLive boots the server against live Pinecone twice over one fixture set, once self-embedding with PODIUM_PINECONE_INFERENCE_MODEL and once with an external OpenAI embedder, each in a unique namespace, and asserts both rank the same artifact first while each startup log records the route. Closing it fixed a real bug: PutText sent only chunk_text, so a self-embedding index whose field_map names text returned a 400; it now writes both fields. Passed live against the Pinecone account. **Realism**: occasional. **Nearest test**: test/e2e/vector_backend_config_test.go:TestVectorBackend_PineconeSelfEmbedding.
- **Current**: TestVectorBackend_PineconeSelfEmbedding and SelfEmbedExplicitOverride assert the chosen embedding route from the startup log against a refused host, and TestVectorSemanticSearch_ManagedThroughServer runs a live managed backend storage-only with an external mock embedder, so no test ingests and queries through Pinecone self-embedding live nor compares both routes.
- **Target**: Under PODIUM_LIVE_EXTERNAL=1 boot the server against live Pinecone twice, once with PODIUM_PINECONE_INFERENCE_MODEL set for self-embedding and once with an external embedder, ingest the same layer in each, and assert search_artifacts returns the same expected artifact at rank one while the log records the chosen route.
- **Sketch**: Boot against live Pinecone self-embedding and again with an external embedder, ingest the same layer in each, and assert the same rank-one artifact while the log records the route.

#### G-VEC-12: Embedding provider and managed backend cross-product through the server
- **Severity**: P2. **Lane**: Release or manual; needs PODIUM_LIVE_EXTERNAL=1 plus, per cell, both an embedding provider key and a managed backend's credentials. **Status**: closed (test/e2e/vector_provider_backend_matrix_live_test.go:TestVectorMatrix_ProviderByBackendLive runs one subtest per reachable (embedding provider, managed backend) storage-only cell and per self-embedding backend, boots a server per cell, ingests, and polls search_artifacts for the rank-1 hit; a cell skips only when its provider key or backend credentials are absent. Verified live: openai x {pinecone, weaviate-cloud, qdrant-cloud} plus pinecone/weaviate/qdrant self-embedding all pass; voyage/cohere cells skip (no keys). Closing it fixed two real self-embedding bugs: Qdrant now creates the tenant_id keyword payload index its inference query requires, and Weaviate's upsert falls back from PUT to POST on a fresh object and sends X-Weaviate-Cluster-Url for the hosted text2vec-weaviate module. Also corrected PODIUM_QDRANT_INFERENCE_MODEL in test.env to the supported sentence-transformers/all-MiniLM-L6-v2. **Realism**: occasional. **Nearest test**: test/e2e/vector_semantic_search_test.go:TestVectorSemanticSearch_ManagedThroughServer.
- **Current**: TestVectorSemanticSearch_ManagedThroughServer now iterates every managed backend (Pinecone, Weaviate Cloud, Qdrant Cloud) but with a mock OpenAI-format embedder for storage-only vectors, and the pkg/embedding live tests exercise each provider (OpenAI, Cohere, Voyage, Ollama) with no managed backend, so no test boots a server with a real embedding provider and a managed backend together. The "Storage-only pairings" section asks only for at least one provider per backend, so the full provider-by-backend matrix is untested.
- **Target**: A Release-lane matrix that, for each (embedding provider, managed backend) pair whose credentials are both present, boots a standalone server wired to that provider and backend, ingests the fixture set, and asserts semantic search returns the expected artifact at rank one. Add the self-embedding backends (Pinecone, Weaviate, and Qdrant with an inference model) as their own cells with no external provider. Skip a cell only when its provider key or its backend credentials are absent, so the matrix runs every reachable combination.
- **Sketch**: Iterate the (embedding provider, managed backend) pairs plus the self-embedding backends, boot a server per reachable cell, ingest, and assert the expected rank-one semantic hit; skip a cell only when its provider key or backend credentials are missing.

### SCALE: Scale, load, and rich catalog

#### G-SCALE-1: Large catalog of hundreds of artifacts ingested then walked through paginated load_domain
- **Severity**: P1. **Lane**: PR or Nightly; needs a standard server, no external network for a generated registry. **Status**: closed (test/e2e/scale_large_catalog_test.go:TestScale_LargeCatalogIngestAndWalk generates 416 artifacts across 16 top-level domains and 80-plus subdomains with varied DOMAIN.md discovery knobs, boots a standalone server with a 700-token tenant discovery budget via PODIUM_CONFIG_FILE, and asserts root load_domain compresses below the max_depth ceiling and stays within the token estimate with a §4.5.5 reduction note, a nested load_domain folds the sparse subdomain into the leaf set with folded_from and caps notable at notable_count, and search_artifacts browse mode is deterministic with total_matched surviving top_k truncation, scope-narrowing partitioning the catalog disjointly, ascending-id browse order, and top_k>50 rejected as registry.invalid_argument. No product bug; the spec's no-cursor pagination is scope-plus-top_k over a stable ranking). **Realism**: common. **Nearest test**: test/e2e/domain_modeling_test.go:TestDomainModel_TargetResponseTokensTightens.
- **Current**: domain_modeling_test.go and discovery_search_test.go exercise target_response_tokens, folding, and notable caps on small hand-built domain trees and ingest_convergence_test.go ingests through the real pipeline, but no test ingests several hundred artifacts across a deep domain tree then walks it.
- **Target**: Generate a registry of 400-plus artifacts across at least 12 domains and 30 subdomains with varied DOMAIN.md knobs, boot a standard server, and assert root load_domain returns a bounded subtree within the token budget, nested load_domain applies folding and notable caps, and search_artifacts paginates deterministically across pages.
- **Sketch**: Generate a 400-plus-artifact registry with varied DOMAIN.md knobs, boot a standard server, and assert root load_domain stays within the token budget, nested load_domain folds and caps, and search_artifacts paginates with stable ranking.

#### G-SCALE-2: Large-catalog re-sync after dozens of removals cleans stale outputs and reconciles config-merge at scale
- **Severity**: P1. **Lane**: PR or Nightly; CLI sync over a generated filesystem registry, no external infra. **Status**: closed (pkg/sync/scale_resync_test.go:TestScale_LargeCatalogResyncCleansAndReconciles materializes a 340-artifact claude-code catalog spanning standalone agents, commands, nested context trees, 30 hooks merging into .claude/settings.json, and 30 mcp-servers merging into .mcp.json, seeds operator-owned keys into both shared files, drops 68 artifacts (including every mcp-server), re-syncs, and asserts removed standalone files and emptied .podium/context parents are pruned, settings.json keeps the operator key and the surviving 25 hooks while dropping exactly the removed ones, .mcp.json is reconciled in place with the operator key kept and no Podium mcpServers entry or x-podium index left, a third sync is byte-idempotent on both shared files, and the lock holds exactly the surviving set with no mcp/ entry. No product bug; existing removeStalePaths/reconcileOrphanConfig/mergeJSON-prior-strip behavior holds at scale). **Realism**: common. **Nearest test**: pkg/sync/cleanup_test.go:TestRun_OrphanedConfigMergeReconciledNotDeleted.
- **Current**: pkg/sync/cleanup_test.go asserts dropped standalone artifacts are deleted and orphaned config-merge files are reconciled at unit scale, and filesystem_sync_test.go checks one stale artifact on a second sync, but no test drops dozens of artifacts spanning standalone files, hooks, and mcp-server entries from a large catalog and re-syncs.
- **Target**: Materialize a several-hundred-artifact catalog for claude-code, drop 40-plus artifacts spanning standalone files, hooks, and mcp-server entries, re-sync, and assert removed standalone files and emptied parent directories are cleaned, config-merge files retain operator-authored and surviving Podium entries, and a second re-sync is idempotent with the lock reflecting the current set.
- **Sketch**: Materialize a several-hundred-artifact catalog, drop 40-plus standalone, hook, and mcp-server artifacts, re-sync and assert stale files and emptied parents are cleaned while config-merge files keep operator and surviving Podium entries, then re-run for idempotence.

#### G-SCALE-3: Concurrent consumers sync overlapping catalog slices to distinct targets with byte-identical shared output
- **Severity**: P2. **Lane**: PR; needs a booted server plus concurrent CLI syncs under -race, no external infra. **Status**: closed (test/integration/sync_concurrent_overlap_test.go:TestSyncConcurrent_OverlappingIncludeByteIdenticalShared boots one in-process server over a generated registry, runs 8 concurrent claude-code syncs to distinct targets each with an overlapping --include of shared/** plus its own private c<k>/** slice, and after the race asserts the shared subtree (skill SKILL.md + bundled resource, agent, command, 12 context bodies) is byte-identical across every target via assertTreesEqual, no .tmp staging sibling survives anywhere, each target holds its own private items, and no other consumer's private subtree leaked in. Passes repeatedly under -race. No product bug). **Realism**: occasional. **Nearest test**: test/integration/sync_concurrent_target_test.go:TestSyncConcurrent_DistinctTargetsAllComplete.
- **Current**: sync_concurrent_target_test.go runs 8 writers for 200 rounds each to distinct targets under -race and asserts zero errors, each target complete, and no stray .tmp siblings, but each writer emits its own content token so overlapping --include slices and byte-identical shared-artifact output across targets are not asserted.
- **Target**: Boot a server, run concurrent syncs to distinct targets with overlapping --include filters, and additionally assert byte-identical materialized output across targets for the shared artifacts with no stray .tmp siblings.
- **Sketch**: Boot a server, run concurrent syncs to distinct targets with overlapping --include filters, then diff the shared artifacts across targets and assert byte-identical output and no stray .tmp siblings.

#### G-SCALE-4: Audit sampling and retention purging under high event volume with signature integrity
- **Severity**: P2. **Lane**: Nightly; needs a standalone server with sample rates and short retention, no external infra. **Status**: closed (internal/serverboot/audit_scale_volume_test.go:TestAuditScale_SamplingAlwaysRecordedRetentionAndVolume drives the production audit emitter composition — auditEmitterFor + auditVolumeEmitter, the exact functions Run() wires — over the real parseAuditSampleRates sampler, server.AuditVolumeMeter, audit.FileSink, Ed25519 signer, and audit.Enforce retention sweep. It emits 4000 each of domain.loaded and domains.searched at a 0.2 keep rate and asserts the recorded fraction lands within 0.05 of 0.2 (and is neither all-kept nor all-dropped), emits layer.ingested/artifact.published/admin.granted and asserts every one is recorded (never sampled), asserts the 5000/day audit-volume budget is spent so the meter Allow() flips to refuse while a second tenant stays allowed, then appends 50 over-age artifact.loaded events, runs the 1-year retention policy, re-anchors, and asserts the aged events are purged while the fresh one and in-window sampled events survive, the chain Verify()s post-purge, a retention_enforced marker and a second anchor are recorded, and the re-anchored head's Ed25519 signature verifies (and fails against a tampered head). Passes 20/20. No product bug). **Realism**: occasional. **Nearest test**: internal/serverboot/audit_sampling_config_test.go:TestParseAuditSampleRates.
- **Current**: audit_sampling_config_test.go parses PODIUM_AUDIT_SAMPLE_RATES and verifies retention defaults and audit_volume_enforce_test.go covers the reingest audit-volume gate, but no test drives high-frequency events and asserts the recorded count approximates the configured sampling fraction, that critical events are always recorded, or that retention purges aged events while signing stays intact.
- **Target**: Boot a server with PODIUM_AUDIT_SAMPLE_RATES on domain.loaded and search_domains plus short retention, drive a high volume of those events, assert the recorded fraction is within tolerance while ingest and admin-grant events are never sampled out, assert the retention sweep purges aged events with signatures still verifying, and assert PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY caps total writes.
- **Sketch**: Boot with sample rates on high-frequency events and short retention, drive a high volume, assert the recorded fraction is within tolerance with ingest and admin grants always recorded, then assert the retention sweep purges aged events with verification intact and the per-day volume quota caps writes.

### LIFECYCLE: Artifact and deployment lifecycle

#### G-LIFECYCLE-1: Extends parent-pin stability and reingest-driven re-resolution end to end
- **Severity**: P1. **Lane**: PR; needs a standalone server able to publish a second parent version post-boot (F-7.3.4 reingest), no external infra. **Status**: closed. `test/e2e/lifecycle_journeys_test.go:TestLifecycle_ExtendsPinStabilityAndReingest` stages a parent at 1.2.0 and a child (a distinct id in its own runtime-republish layer) pinning the major range via the G-INFRA-7 primitive: the merged child inherits the parent's `when_to_use` and folds sensitivity most-restrictively (high), publishing parent 1.3.0 leaves the pinned child 1.0.0 still merging 1.2.0 (no silent propagation), and a child reingest at a new version 1.1.0 re-resolves the pin to 1.3.0 while 1.0.0 stays frozen (immutability). A same-bytes child reingest is correctly a content-hash no-op, so the spec-faithful re-resolution path is a new child version. **Realism**: common. **Nearest test**: test/e2e/artifact_extends_test.go:TestExtends_PinReingestPicksNewerParent.
- **Current**: TestExtends_PinNoSilentPropagation and TestExtends_PinReingestPicksNewerParent are skipped because the standalone harness cannot build a multi-version parent layer, and pin storage at ingest is covered only in pkg/registry/ingest, so the journey where a newer parent ships, the child still resolves the old pin, and a child reingest re-resolves is not exercised end to end. Three gap-checked scenarios across MULTILAYER and LIFECYCLE collapse here.
- **Target**: Ingest a parent at 1.2.0 and a child pinning the major range, publish parent 1.3.0 without reingesting the child and assert the child's merged manifest still reflects 1.2.0 with inherited when_to_use and most-restrictive sensitivity, then reingest the child and assert it merges 1.3.0 with the recorded pin advanced.
- **Sketch**: Ingest parent 1.2.0 and a child pinning 1.x, publish parent 1.3.0 and assert the child still merges 1.2.0, reingest the child and assert it merges 1.3.0 with the pin advanced.

#### G-LIFECYCLE-2: Deprecation with replaced_by excludes from search while load surfaces the upgrade target
- **Severity**: P1. **Lane**: PR; needs a standalone server with post-boot reingest, no external infra. **Status**: closed. `test/e2e/lifecycle_journeys_test.go:TestLifecycle_DeprecationExcludesFromSearchLoadSurfacesTarget` publishes two live versions of one id via the G-INFRA-7 primitive, then a deprecated 3.0.0 with `replaced_by`: search omits the deprecated version (it resolves the live 2.0.0), a bare load resolves 2.0.0, and an explicit load of 3.0.0 returns `deprecated=true`, the `replaced_by` upgrade target, and a warning naming it. Required a product fix: the SQL metadata stores never persisted `replaced_by` as a column, so a record scanned from SQLite/Postgres carried an empty `ReplacedBy` and the documented §4.7.4 round-trip silently dropped; `core.replacedByOf` now recovers it from the stored frontmatter on the load path. Closes the T-D-frontmatter-60 doc-accuracy gap, with frontmatter_schema_test.go, artifact_response_test.go, and http_api_test.go strengthened to assert the target now surfaces. **Realism**: common. **Nearest test**: pkg/registry/core/latest_skips_deprecated_test.go:TestLoadArtifact_LatestSkipsDeprecatedVersion.
- **Current**: core lifecycle tests cover latest-skips-deprecated and search exclusion, and artifact_response_test.go asserts a deprecation warning on load, but frontmatter_schema_test.go T-D-frontmatter-60 records that replaced_by does not round-trip into the load warning, so the upgrade target is unit-only and not asserted in the e2e load response.
- **Target**: Ingest two versions of one id, reingest the older as deprecated with replaced_by, assert search_artifacts omits it, assert a bare load resolves to the newer version, and assert an explicit load of the older version returns a warning that names the replaced_by upgrade target.
- **Sketch**: Reingest the older of two versions as deprecated with replaced_by, assert search omits it, bare load resolves to the newer version, and explicit load of the older version warns with the replaced_by target named.

#### G-LIFECYCLE-3: Store retention purges deprecated object bytes while protecting extends-pinned parents
- **Severity**: P1. **Lane**: PR or Release; needs an object store (filesystem or S3) plus a backdating hook, no external network for filesystem. **Status**: closed. `test/integration/store_retention_objects_test.go:TestStoreRetention_PurgesDeprecatedBytesProtectsPinnedParent` drives the §8.4 purge against a SQLite store plus a filesystem object store: an unpinned deprecated version past the window is purged, a non-deprecated successor survives, and a deprecated version still pinned as an extends parent by a live child is PROTECTED so the child still loads after the sweep. Object bytes a live record still references stay retrievable (the purge removes manifest rows, never content-addressed bytes a surviving artifact uses; proactive object GC is not spec-mandated and unsafe given §4.4 dedup, so the test asserts retention rather than reclaim). Required a product fix: `PurgeDeprecatedManifests` (Memory, SQLite, Postgres) previously deleted a deprecated parent out from under a live child's hard pin, orphaning its load; all three backends now exclude versions still named by a child's `extends_pin`. `internal/serverboot/store_retention_test.go:TestRunStoreRetentionOnce_ProtectsExtendsPinnedParent` locks it in at the scheduler entry point. **Realism**: occasional. **Nearest test**: internal/serverboot/store_retention_test.go:TestRunStoreRetentionOnce_PurgesExpiredRecords.
- **Current**: store_retention_test.go backdates a deprecated version and asserts its manifest record is purged while a live version survives, but no test asserts the associated object-store resource bytes are reclaimed or that a version still pinned as an extends parent is protected from purge.
- **Target**: Ingest a deprecated version with a bundled resource plus a non-deprecated successor and a child that pins the deprecated version via extends, backdate the deprecation past PODIUM_DEPRECATED_RETENTION_DAYS, run retention, and assert the deprecated record and its object bytes are gone while the successor and the pinned parent remain.
- **Sketch**: Backdate a deprecated version with a bundled resource past the window, run retention, and assert the record and object bytes are reclaimed while a successor and an extends-pinned parent survive.

#### G-LIFECYCLE-4: In-place legacy SQLite schema upgrade preserves artifacts and audit history idempotently
- **Severity**: P1. **Lane**: PR; needs a seeded legacy-schema SQLite file, no external infra. **Status**: closed. `test/e2e/lifecycle_journeys_test.go:TestLifecycle_InPlaceSQLiteUpgradePreservesArtifactsAndAudit` seeds a legacy reduced-column SQLite database under the standalone org's UUID tenant (not the literal "default"), boots `serve --standalone` with `PODIUM_SQLITE_PATH` pointed at it so the §13.4 additive migration runs in place on OpenSQLite, asserts the artifact resolves with its original content hash, generates hash-chained `artifact.loaded` audit history, then boots a SECOND server on the now-upgraded file and the same audit log: the migration is idempotent (clean re-open, artifact survives), the first boot's audit history is intact, and the chain continues past it. The in-place migrate is the binary boot itself (§13.4 F-13.4.1; there is no separate migrate command), so a server boot against the legacy file is the upgrade. **Realism**: occasional. **Nearest test**: test/e2e/schema_forward_migration_test.go:TestForwardMigration_MigrateToStandardReadsLegacySQLite.
- **Current**: schema_forward_migration_test.go reads a legacy SQLite source into a fresh standard target and asserts the migrated artifact and content hash become readable, but the in-place upgrade-and-boot-against-the-same-database, audit-history preservation, and migrate idempotency are not asserted.
- **Target**: Seed a legacy-schema SQLite database with ingested artifacts and audit rows, run the in-place admin migrate, boot the server against the upgraded file, assert artifacts resolve with original content hashes and audit history is intact, and assert a second migrate reports a no-op.
- **Sketch**: Run in-place migrate on a legacy SQLite database with seeded artifacts and audit rows, boot against the upgraded file, assert artifacts and audit history survive, then re-run migrate and assert idempotence.

#### G-LIFECYCLE-5: Migration chain from filesystem to standalone to standard preserves content hashes and bundled bytes
- **Severity**: P1. **Lane**: PR; needs Postgres and S3 for the final hop (PODIUM_POSTGRES_DSN, PODIUM_S3_*). **Status**: closed. `test/e2e/lifecycle_migration_chain_test.go:TestLifecycle_MigrationChainFilesystemStandaloneStandard` carries one above-cutoff bundled-resource catalog through `podium sync --harness none` (filesystem), `serve --standalone` ingest (SQLite + filesystem object store, large resource served via the token-bound /objects route), and `admin migrate-to-standard` into live Postgres + S3 (large resource served via an S3 presigned URL), asserting the content hash, the large-resource bytes, and the top search result match at each stage. Skips cleanly without PODIUM_POSTGRES_DSN / PODIUM_S3_BUCKET. Required a product fix: `admin migrate-to-standard` hardcoded the literal "default" tenant id, but a real standalone source keys every row by `orgIDForName("default")` (a UUIDv5), so a real migration silently pumped zero manifests (blobs copied, metadata did not); `planMigration` now probes the UUID tenant too, covered by `cmd/podium/admin_migrate_test.go:TestAdminMigrateToStandard_MigratesStandaloneUUIDTenant`. This is the previously-skipped `readme_claims_test.go:1047` drill. **Realism**: occasional. **Nearest test**: cmd/podium/admin_migrate_test.go:TestAdminMigrateToStandard_PumpsMetadataAndObjects.
- **Current**: admin_migrate_test.go and schema_forward_migration_test.go exercise the standalone-SQLite-to-standard migration, and standard_stack_parity_test.go proves author-to-consumer byte parity, but parity stages only inline-sized resources and no single test chains filesystem sync to standalone to standard asserting byte identity at each stage.
- **Target**: Author a layer with an above-cutoff bundled resource, sync it on the filesystem, boot a standalone server over the same source and ingest, migrate into standard Postgres plus S3, and assert load_artifact returns the same content hash and resource bytes and search returns the same top result across all three modes.
- **Sketch**: Carry one bundled-resource catalog through filesystem sync, standalone ingest, and standard migration, and assert identical content hash, resource bytes, and top search result at each stage.

#### G-LIFECYCLE-6: Force-push history rewrite tolerated via PriorRef through the reingest endpoint
- **Severity**: P2. **Lane**: PR; needs a server and a seeded file:// git repo, no external network. **Status**: closed. `test/e2e/lifecycle_journeys_test.go:TestLifecycle_ForcePushToleratedThroughReingest` registers a git layer over a seeded file:// repo, ingests at commit A (recording LastIngestedRef = A), hard-resets master to an unborn branch and commits C so A is no longer reachable, and reingests through the endpoint: the default tolerant policy accepts it, load_artifact serves the rewritten 2.0.0 content, the stored ref advances to C, and the `layer.history_rewritten` audit event records prior_ref=A and new_ref=C. No product change needed; the §7.3.1 PriorRef/tolerant path was already wired (internal/serverboot/reingest.go -> pkg/registry/ingest/orchestrator.go). **Realism**: occasional. **Nearest test**: pkg/layer/source/git_test.go:TestGit_SnapshotForcePushDetected.
- **Current**: git_test.go drives a real force-push and asserts Snapshot reports HistoryRewritten, and TestServerOps_ForcePushPolicyStrict and ForcePushDefaultTolerant cover force_push_policy persistence, but no test combines a force-push of a registered layer's repo with a reingest through the endpoint.
- **Target**: Register a git layer and ingest at commit A, hard-reset and commit C so A's history is rewritten, reingest through the endpoint, and assert the second reingest succeeds, records the new ref against the existing layer via PriorRef tolerance, serves the rewritten content, and emits the history_rewritten signal.
- **Sketch**: Register a git layer, ingest at A, force-push to a rewritten head, reingest, and assert the stored ref advances, load_artifact returns post-rewrite content, and the history_rewritten signal records the prior and new refs.

### CONFIG: Deployment and configuration permutations

#### G-CONFIG-1: Probe-driven read-only flip on primary outage refuses writes, serves reads, and recovers
- **Severity**: P1. **Lane**: Nightly or Release; needs a Postgres primary plus replica topology that can be severed on demand. **Status**: open (missing). **Realism**: occasional. **Nearest test**: test/e2e/server_operations_test.go:TestServerOps_ReadOnlyAutoExit.
- **Current**: Every probe-driven read-only case in server_operations_test.go (ReadOnlyWriteEndpoints, ReadOnlyAutoExit, ReadOnlyAuditEvents, ReadOnlyProbeTuning, ReadyzInReadOnlyMode) is t.Skip because a standalone SQLite store cannot be failed after boot, and http_api_test.go forces the mode with mode.Set, so probe tuning is observable only through config show and the flip itself is not inducible. Six gap-checked scenarios across CONFIG and STACK collapse onto this single inducement gap.
- **Target**: Boot a Postgres-backed server with low PODIUM_READONLY_PROBE_FAILURES and PODIUM_READONLY_PROBE_INTERVAL, sever the primary until the probe trips, assert /readyz reports read_only while load and search keep serving and ingest returns registry.read_only, then restore the primary and assert writes resume and the read_only_entered and read_only_exited audit events are recorded.
- **Sketch**: Against a severable Postgres primary or replica deployment, set probe thresholds low, drive concurrent reads and an ingest, sever the primary, assert the read-only flip with reads serving and ingest refused, restore the primary, and assert writes resume with matching audit events.

#### G-CONFIG-2: Zero-flag standalone bootstrap selects SQLite, filesystem, Ollama, and loopback defaults
- **Severity**: P1. **Lane**: PR; needs an empty HOME temp dir and a stub Ollama endpoint, no external infra. **Status**: open (missing). **Realism**: common. **Nearest test**: test/e2e/deployment_modes_test.go:TestDeployment_ZeroFlagAutoBootstrap.
- **Current**: TestDeployment_ZeroFlagAutoBootstrap is skipped pending F-13.10.1 because zero-flag detection emits no banner and creates no ~/.podium/registry.yaml, and TestDeployment_StandaloneExplicit covers only the explicit --standalone --layer-path form, so the empty-environment defaults of sqlite metadata, filesystem objects, ollama embeddings, sqlite-vec, and bind 127.0.0.1:8080 are unverified.
- **Target**: Run podium serve with HOME pointed at a temp dir and all PODIUM_* unset, assert the startup log records the standalone defaults and bind 127.0.0.1:8080, ingest a filesystem layer, and assert search_artifacts ranks the expected artifact first against a stub Ollama embedder.
- **Sketch**: With an empty environment and temp HOME, boot podium serve, assert the standalone default backend log lines and loopback bind, ingest a filesystem layer, and run search_artifacts against a stub Ollama endpoint.

#### G-CONFIG-3: Full config precedence chain resolves CLI flag over env over registry.yaml over default through config show
- **Severity**: P1. **Lane**: PR; CLI plus config show, no external infra. **Status**: open (partial). **Realism**: occasional. **Nearest test**: test/e2e/registry_config_keys_test.go:TestRegistryConfig_BindEnvBeatsConfigFile.
- **Current**: TestRegistryConfig_BindEnvBeatsConfigFile, TestStandaloneServer_ConfigShowLayersPathSource, and TestRegistryConfig_BindFromConfigFileWhenEnvUnset cover adjacent precedence pairs, and serve_flags_test.go unit-tests the flag-to-env mapping, but no single test resolves a CLI flag winning over an env var for the same field observed through config show.
- **Target**: In one run set registry.yaml store to sqlite, PODIUM_REGISTRY_STORE to postgres, and a CLI --bind override, then assert config show resolves the store to postgres from the environment, the bind to the CLI value, and a yaml-only field to its yaml value with each reported source.
- **Sketch**: Run podium config show with a registry.yaml value, an overriding env var, and an overriding CLI flag for distinct fields, and assert each field resolves to its winning layer value and source.

#### G-CONFIG-4: Standard-mode first run fails fast and names the missing backend credential
- **Severity**: P1. **Lane**: PR; CLI serve under PODIUM_NO_AUTOSTANDALONE, no external infra. **Status**: open (partial). **Realism**: common. **Nearest test**: internal/serverboot/backend_config_test.go:TestValidate_MissingBackendValues.
- **Current**: TestValidate_MissingBackendValues unit-tests that validate names each missing variable per selected backend (PODIUM_S3_BUCKET, PODIUM_PINECONE_API_KEY, OPENAI_API_KEY, and others), and ambient_env_guard_test.go drives the no-autostandalone refusal for a bare serve, but no e2e boots a real podium serve with a backend explicitly selected and a required credential absent to assert the process exits non-zero with the missing-configuration message naming the variable.
- **Target**: Run podium serve with PODIUM_REGISTRY_STORE=postgres and no PODIUM_POSTGRES_DSN, and separately with PODIUM_OBJECT_STORE=s3 and no PODIUM_S3_BUCKET, and assert each run exits non-zero before binding with a startup error that names the specific missing variable.
- **Sketch**: Boot podium serve with a backend selected and its required env var unset, and assert a non-zero exit before bind with the missing-configuration error naming the absent variable.

### AUTH: Multi-tenant isolation, visibility, and audit

#### G-AUTH-10: Audit transparency-log signing, anchoring, verification, and PII redaction in one journey
- **Severity**: P1. **Lane**: Nightly; needs a standalone server plus an Ed25519 key file, no external infra. **Status**: open (partial). **Realism**: occasional. **Nearest test**: test/integration/audit_retention_reanchor_test.go:TestAuditRetention_ReanchorsNewHeadAfterDrop.
- **Current**: PII redaction is unit-tested in pkg/audit/redact_config_test.go and anchoring plus chain verification are integration-tested in audit_retention_reanchor_test.go, but PODIUM_AUDIT_SIGNING_KEY_PATH is read only by serverboot and is never set in any test, so no single journey combines key-path signing, anchor and verify intervals, redaction of a freeform query, and tamper detection.
- **Target**: Boot the server with PODIUM_AUDIT_SIGNING_KEY_PATH, PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS, PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS, and PODIUM_PII_REDACTION enabled, issue a search_artifacts call carrying PII in the freeform query, then assert exported audit entries are Ed25519-signed and chain-verifiable, the recorded event redacts the PII, and a manually tampered entry fails verification.
- **Sketch**: Boot with a signing key path and short anchor and verify intervals, run a search_artifacts query containing an email, export the audit log, assert entries are signed and chain-verifiable and the query is redacted, then mutate one entry and assert verification fails.

#### G-AUTH-11: Cross-tenant search and quota non-interference over shared Postgres
- **Severity**: P1. **Lane**: PR; needs live Postgres (PODIUM_POSTGRES_DSN). **Status**: open (partial). **Realism**: common. **Nearest test**: test/integration/auth_org_isolation_test.go:TestAuthOrgIsolation_OrgScopedReadsAreIsolated.
- **Current**: TestAuthOrgIsolation_OrgScopedReadsAreIsolated proves load_artifact, shared-id content_hash, and search isolation for two orgs over a shared Postgres, and TestHTTPAPI_SearchQPSQuota throttles a single tenant, but no test drives one org over PODIUM_QUOTA_SEARCH_QPS while a second org stays served, and dependency edges, scope-preview counts, quota buckets, audit events, and object blobs are not asserted cross-org. This refines existing gap G-AUTH-7 by adding cross-tenant quota and resource-class isolation rather than re-listing org-scoped reads.
- **Target**: Extend the org-isolation test to drive acme above PODIUM_QUOTA_SEARCH_QPS while globex issues in-budget searches, assert acme is throttled with quota.search_qps_exceeded while globex stays served, and assert acme receives org-scoped results or 404 on globex dependency edges, scope-preview counts, audit events, and object blobs.
- **Sketch**: Boot a shared-Postgres multi-tenant server with a low search QPS quota, exceed it with an acme token while globex searches in budget, and assert acme throttles while globex serves and dependency edges, scope preview, audit, and object reads exclude the other org.

#### G-AUTH-12: Per-caller visibility filters search across a group-restricted layer and a public layer
- **Severity**: P1. **Lane**: PR; needs a standalone server with injected-session-token JWTs, no external infra. **Status**: open (partial). **Realism**: common. **Nearest test**: test/integration/runtime_layer_visibility_test.go:TestRuntimeLayerVisibility_FullServerReadPath.
- **Current**: TestRuntimeLayerVisibility asserts search_artifacts returns the admin layer plus the caller's own user-defined layer, and server_operations_test.go asserts group visibility only on load_artifact, so no test issues one broad search across a group-restricted layer and a public layer and asserts the group filter applies to the search result set.
- **Target**: Boot a server with a groups:finance layer and a public layer both holding query-matching artifacts, mint injected-session-token JWTs with and without the finance group, and assert the two search_artifacts result sets differ by exactly the finance-layer matches while a finance member sees them.
- **Sketch**: Mint finance-group and non-member JWTs, run the identical broad search_artifacts query for each, and assert the result sets differ by exactly the finance-layer matches.

#### G-AUTH-13: Token-bound object route refuses a blob after the caller loses layer visibility
- **Severity**: P1. **Lane**: PR; needs a server with a non-public layer and per-caller tokens (per-identity visibility, blocked by all-public filesystem bootstrap). **Status**: open (partial). **Realism**: occasional. **Nearest test**: test/integration/data_plane_test.go:TestDataPlane_IngestToLoadArtifactRoundTrip.
- **Current**: TestDataPlane_IngestToLoadArtifactRoundTrip and TestHTTPAPI_ObjectsServesBytes assert the happy-path /objects/{content_hash} fetch of an above-cutoff resource with byte length and X-Content-Hash, but http_api_test.go records that the revocation re-check needs non-public layer visibility the all-public filesystem bootstrap cannot model, so no test asserts the route re-evaluates authorization and returns 403 once the caller can no longer see the owning layer.
- **Target**: Boot a server with an above-cutoff resource in a group-restricted layer, fetch the token-bound /objects route as a member and assert 200 with matching bytes, then remove the caller from the group and assert the same presigned object URL returns 403 while a still-authorized caller keeps fetching.
- **Sketch**: Ingest an above-cutoff resource into a restricted layer, fetch its /objects URL as an authorized caller and assert 200, revoke the caller's group membership, and assert the same fetch returns 403 while a still-authorized caller succeeds.

### STACK: Managed-stack data plane

#### G-STACK-3: Large bundled resource delivered via live S3 presigned URL with 403 refresh and filesystem-route contrast
- **Severity**: P1. **Lane**: PR or Release; needs S3 (PODIUM_S3_*) for the presigned path and a filesystem deployment for the /objects route. **Status**: open (partial). **Realism**: occasional. **Nearest test**: test/e2e/standard_stack_parity_test.go:TestStandardStackParity_AuthorToConsumer.
- **Current**: TestStandardStackParity_AuthorToConsumer asserts an above-cutoff resource loads under large_resources with a non-empty presigned_url and matching size, the 403 refresh is unit-tested at the MCP layer, and data_plane_test.go asserts presigned streaming, but no end-to-end test fetches the presigned URL through the real S3 data plane, triggers a 403-driven refresh there, or contrasts an S3 presigned URL against a filesystem token-bound /objects/{content_hash} route. Several large-resource scenarios merge here.
- **Target**: Load the same above-cutoff resource through an S3-backed standard server and a filesystem-backed standalone server, fetch the S3 presigned URL successfully, assert an expired URL returning 403 triggers a refresh that yields a working URL, and assert the filesystem deployment delivers via a token-bound /objects route while sub-cutoff resources stay inline on both.
- **Sketch**: Load an above-cutoff resource on S3-backed and filesystem-backed deployments, fetch the S3 presigned URL, assert a stale 403 triggers refresh, and assert the filesystem deployment uses a token-bound /objects route with inline below the cutoff.

### DOC: Description quality

#### G-DOC-8: Description-quality lint flags a vague manifest end to end while ingest accepts both
- **Severity**: P2. **Lane**: PR; CLI lint plus a standalone server, no external infra. **Status**: closed. `test/e2e/description_quality_advisory_test.go` drives the real CLI against a standalone server: it registers a local-source layer (G-INFRA-7), stages a vague, a precise, and a borderline-acceptable artifact, reingests, and asserts the §3.3 / §12 advisory fires on the vague artifact only (`advisory: finance/vague ... lint.thin_description`) with no false positive on the borderline one at the §3.3 bound ("Greet the user.", 3 words / 15 chars), then loads all three to prove ingest accepted them despite the non-gating advisory. A companion test covers the cross-artifact colliding-summary advisory (both members flagged naming the peer; a distinct summary unflagged). Surfaced through `podium layer reingest`'s `advisories` array (the §7.3.1 / §3.3 observable surface) rather than author-facing `podium lint`, because the spec attributes the thin-description and colliding-summary checks to "the registry" (§3.3) and "ingest-time lint" (§12), and `DescriptionAdvisoryRules()` is deliberately kept out of `AllRules()` (the `podium lint` schema set); wiring them into `podium lint` would flag a dozen deliberately-minimal doc example fixtures. **Realism**: common. **Nearest test**: pkg/lint/description_rules_test.go:TestThinDescription_FlagsSingleWordAndShort.
- **Current**: pkg/lint unit tests assert lint.thin_description fires on single-word and short descriptions and not on a three-word description, and the rule appears in server_operations_test.go, but no e2e drives podium lint over a registry containing a vague, a precise, and a borderline artifact then confirms ingest still succeeds for all three despite the advisory.
- **Target**: Run the real podium CLI lint over a filesystem registry with a vague, a precise, and a borderline-acceptable artifact, assert the advisory fires only on the vague one with no false positive on the borderline one, then ingest all three into the standalone server and assert success.
- **Sketch**: Run podium lint over a registry with vague, precise, and borderline descriptions, assert the advisory fires only on the vague one, then ingest all three and assert success.

### OPS: Operational notifications

#### G-OPS-1: Layer-event notification is delivered to a configured webhook receiver with an HMAC signature
- **Severity**: P2. **Lane**: PR; needs a standalone server plus an in-test webhook receiver, no external infra. **Status**: open (partial). **Realism**: occasional. **Nearest test**: pkg/notification/notification_test.go:TestWebhook_PostsJSONWithHMAC.
- **Current**: pkg/notification/notification_test.go asserts the webhook provider POSTs JSON with an HMAC and that the multi provider fans out, and serverboot openNotifier selects the provider from PODIUM_NOTIFICATION_PROVIDER, but no test boots a server with PODIUM_NOTIFICATION_PROVIDER=webhook pointed at an in-test receiver and asserts a registry-side event produces a delivered, signature-valid notification through the wired path.
- **Target**: Boot a standalone server with PODIUM_NOTIFICATION_PROVIDER=webhook and PODIUM_NOTIFICATION_WEBHOOK_URL pointed at an in-test recorder plus a secret, drive a registry event that fires the notifier, and assert the recorder receives one POST whose HMAC verifies against the secret and whose body carries the event title and severity.
- **Sketch**: Boot a server with a webhook notifier pointed at an in-test recorder, trigger a registry event, and assert one delivered POST with a verifying HMAC and the expected title and severity.

## Test-harness evolution and harness-blocked journeys

The test harness cannot express several worthy journeys today, so they are skipped. The journeys are valuable; the limitation is in the test infrastructure. The standalone e2e harness boots every layer public and resolves every caller to `system:public`, a standalone SQLite store cannot be made to fail after boot, the boot path ingests each layer once from a static fixture, and the filesystem bootstrap attaches no signatures. These are test-infrastructure limits. The harness must evolve to remove them. The journeys they block are recorded as gaps below and cross-referenced to the currently-skipped tests so none is dropped.

The capability to lift mostly exists already. `test/integration/injected_session_token_test.go` boots a server with the injected-session-token verifier and mints JWTs carrying groups and scopes; `test/integration/runtime_layer_visibility_test.go` constructs `server.New(core.New(st, "default", bootLayers), ...)` with layers of chosen visibility and issues requests as distinct callers; the managed-stack parity work added `injKeyPair`, `injGet`, `msStartStandardServer`, and `msPublishGitLayer`. The work is to package these into reusable primitives. The visibility and fault-injection journeys are cheapest and highest-fidelity in-process, where a test constructs `core.New` with chosen layers and can wrap the store; the subprocess CLI path needs more invasive seams and should be reserved for journeys that exercise the CLI and real backends.

### Enabling test-infrastructure primitives

#### G-INFRA-5: Authenticated, visibility-capable server harness
- **Severity**: P0. **Lane**: PR; in-process needs no external infra. **Status**: closed (built). `test/e2e/authserver_harness_test.go` adds `startAuthServer(spec)`: a declarative `authServerSpec` (layers with explicit public, org, `groups`, `users`, or private visibility, bootstrap admins, and optional SCIM seeding) that boots `serve --standalone` behind the injected-session-token verifier, registers the runtime key, and mints per-identity tokens (`token` and `adminToken` carry sub, email, org, groups, and scopes) plus request helpers (`get`, `do`, `searchIDs`, `loadStatus`, `loadCode`, `authGrantAdmin`). `authserver_harness_proof_test.go` proves every visibility mode, SCIM group resolution, the admin override, and scope narrowing through one server. `TestHTTPAPI_BatchLoadVisibilityDenied` and `TestArtifactResponse_ScopeDenied` are lifted off `t.Skip` onto the harness. **Realism**: enabling.
- **Current**: e2e boots `serve --standalone` (`helpers_test.go:308`), which wires no identity provider and materializes every layer public, so any per-caller or per-layer-visibility assertion is inexpressible. The capability exists only inside `test/integration` (`istServer`/`istVerifier`, `runtime_layer_visibility_test.go`) and the parity injected-token helpers.
- **Target**: a shared helper that boots a server with injected-session-token (or a stub verifier), accepts a declarative layer set with explicit visibility (public, org, `groups:<g>`, private), and mints a caller token for a given identity, org, and group set. Convert the skipped visibility, hidden-parent, group-filter, and admin-RBAC tests onto it.
- **Unblocks**: `artifact_extends_test.go:292,299,910`, `artifact_response_test.go:322,751`, `core_concepts_test.go:216,221,659`, `http_api_test.go:571,650,1419,1424`, `cli_reference_test.go:1558,1563,1568,1856,1861`, `standard_deployment_test.go:214,562,567,584`, `governance_adoption_test.go:354-429,962`, `sdk_clients_test.go:388,696`, `plugin_spi_test.go:859`, `server_operations_test.go:504,1233,1240`, `http_api_test.go:1065`, `plugin_spi_test.go:1127,1159` (the standalone fixture does not wire the orchestrator and audit sink to emit ingest events). Feeds G-AUTH-12, G-AUTH-14, G-AUTH-16, G-MULTILAYER-2, G-MULTILAYER-3.

#### G-INFRA-6: Fault-injectable store for an induced read-only flip
- **Severity**: P1. **Lane**: PR; in-process, no external infra. **Status**: closed (built). `pkg/store/storetest/faultstore.go` adds `FaultStore`, a `store.Store` decorator whose `GetTenant` health call (the call the §13.2.1 `ReadOnlyProbe` and the §13.9 `/readyz` check ping) returns an injected error while severed (`Sever`/`Restore`/`Severed`/`HealthCalls`); `NewFaultVectorStore` preserves a wrapped `store.VectorOutbox`. `pkg/registry/server/readonly_faultstore_test.go:TestReadOnlyFaultStore_InducesFullJourney` wires it into an in-process server with the real probe and the read-only audit callbacks and proves the journey: healthy writes accepted, severing trips the probe (no `ModeTracker.Set`) to read_only, reads stay served, writes return the `registry.read_only` envelope, `read_only_entered` is emitted, and restoring recovers to ready, accepts writes, and emits `read_only_exited`. Severable-Postgres lane deferred. **Realism**: enabling.
- **Current**: the read-only flip is driven by `ReadOnlyProbe.Run` calling `Store.GetTenant` (`readonly_probe.go:43`); a standalone SQLite store never errors, so the e2e tests skip or force the mode with `mode.Set`, which bypasses the probe, the audit events, and the recovery path.
- **Target**: a `store.Store` decorator whose health call returns an error while a flag is set, wired into an in-process server so the real probe trips, serves reads, refuses writes with `registry.read_only`, emits `read_only_entered` and `read_only_exited`, and recovers when the flag clears. A severable-Postgres lane can follow for higher fidelity.
- **Unblocks**: `server_operations_test.go:165,172,179,186,193,909`, `artifact_response_test.go:721`, `deployment_modes_test.go:631`. Closes the inducement half of G-CONFIG-1.

#### G-INFRA-7: Runtime layer republish and multi-version fixtures
- **Severity**: P1. **Lane**: PR; no external infra. **Status**: closed (built). `test/e2e/republish_helpers_test.go` adds `republishLayer` plus `newRepublishLayer(srv, id)` and the `publishVersion(versionSpec)` method: it registers a local-source layer at runtime against the common standalone harness (`podium layer register --local`), then each `publishVersion` rewrites the layer's on-disk artifact at the requested version (with optional `deprecated`/`replaced_by` and extra bundled files) and triggers `podium layer reingest`, polling until the version resolves. This generalizes the narrow `msPublishGitLayer` parity primitive (git-source register plus reingest against a live standard-mode server) into "publish version N of a layer" with no external infra, driving the same §7.3.1 ingest pipeline (`internal/serverboot/reingest.go`) so a version bump records a new immutable manifest version under one canonical id while prior versions stay loadable. `test/e2e/republish_primitive_test.go` proves it: `TestRepublish_MultiVersionSelectionAndLatest` (two coexisting versions, each addressable by `version=`, default resolves latest), `TestRepublish_DeprecatedSuccessorSkippedByLatest` (a deprecated successor is skipped by latest but addressable by explicit version), and `TestRepublish_SessionSnapshotStableAcrossRepublish` (a `session_id` pin stays on the snapshot across a mid-session republish while a fresh session sees the new version). **Realism**: enabling.
- **Current**: the standalone harness ingests each layer once at boot from a static fixture, so publishing a second version of a parent or a deprecated successor post-boot is unbuildable. The parity work added `msPublishGitLayer` (git-source register plus reingest at runtime), which is the missing primitive in narrow form.
- **Target**: generalize runtime publish into "publish version N of a layer," so pin-stability, deprecation, version-selection, and session-snapshot journeys can stage multiple versions.
- **Unblocks**: `artifact_extends_test.go:123,159,166`, `discovery_search_test.go:516,522,1004`, `deployment_modes_test.go:351`. Feeds G-LIFECYCLE-1, G-LIFECYCLE-2, G-JOURNEY-3.

#### G-INFRA-8: Signed-artifact ingest and tamper fixture
- **Severity**: P1. **Lane**: PR; offline keypair, no external infra. **Status**: closed (built). `test/e2e/signed_artifact_helpers_test.go` adds `signedArtifactFixture` plus `newSignedArtifactFixture(spec)`: it generates an offline Ed25519 keypair, computes the artifact's canonical content hash (§6.6 step 2 canonicalization), signs it with the real `sign.RegistryManagedKey.Sign` so the served envelope is byte-identical to what `internal/serverboot/signing.go` attaches at ingest, and serves a `load_artifact` response carrying the signature, content hash, and sensitivity over an httptest registry. `Env(policy)` points the real `podium-mcp` binary at it with the registry-managed verifier configured (`PODIUM_SIGNATURE_PROVIDER=registry-managed`, the offline `PODIUM_SIGNATURE_VERIFY_KEY`, optional `PODIUM_SIGNATURE_KEY_ID`, enforcing `PODIUM_VERIFY_SIGNATURES`); `TamperContentHash` / `TamperBody` mutate the served bytes after construction. This lifts the pkg/sign signing unit behavior and the §6.6 content-hash tamper pattern from `manifest_body_test.go`'s `mbStubRegistry` into one reusable signed-then-tampered primitive driven through the shipped binary's `enforceSignaturePolicy` -> `sign.EnforceVerification` path. Required a product fix: the consumer-side `buildSignatureProvider("registry-managed")` previously returned an empty key and could never verify, so it now loads the registry public key from `PODIUM_SIGNATURE_VERIFY_KEY` (base64 Ed25519) via the new `sign.PublicKeyFromBase64`, with `PODIUM_SIGNATURE_KEY_ID` pinning the expected fingerprint (a provider-specific option in the same §6.2 category as `PODIUM_SIGSTORE_*`). `test/e2e/signed_artifact_primitive_test.go` proves it: `TestSignedArtifact_ValidSignatureLoads` (valid envelope materializes under medium-and-above), `TestSignedArtifact_TamperedBlobRefused` (a tampered content hash aborts with `materialize.signature_invalid`), `TestSignedArtifact_TamperedBodyHitsContentHashGate` (a tampered body passes the signature gate but trips `materialize.content_hash_mismatch`), `TestSignedArtifact_LowSensitivitySkipsVerification` (below the policy floor the signature is not checked), and `TestSignedArtifact_KeyPinningRejectsRotatedKey` (a non-pinned key id is refused). **Realism**: enabling.
- **Current**: the filesystem bootstrap attaches no signatures, so signed-artifact verification and signed-then-tampered detection are inexpressible end to end and skip across `server_operations`, `governance_adoption`, and `plugin_spi`.
- **Target**: a fixture that ingests an artifact with a valid signature envelope from an offline key, plus a hook to tamper the stored bytes, so the verifier path can be asserted: a valid signature loads, a tampered blob is refused.
- **Unblocks**: `server_operations_test.go:490,497,1199`, `governance_adoption_test.go:730,953,971`, `plugin_spi_test.go:1165`. Feeds G-AUTH-15.

#### G-INFRA-9: Notification delivery sink and override seam
- **Severity**: P2. **Lane**: PR; in-process recorder, no external infra. **Status**: closed (built). `test/e2e/notification_sink_helpers_test.go` adds `notificationSink` plus `newNotificationSink(opts)`: an httptest receiver reachable from the standalone subprocess that records every delivered body, verifies `X-Podium-Signature` against a configured secret (`withSinkSecret`), and can reject every delivery (`withSinkFailEvery`) to induce §7.3.2 failures. It backs both the §7.3.2 outbound webhook receiver and the §9.1 webhook NotificationProvider, since both POST a signed JSON body. `registerWebhook`/`getWebhook`/`waitForWebhookDisabled`/`waitForWebhookFailureCount` drive the §7.3.2 receiver CRUD and read the persisted delivery state over the standalone HTTP surface (the response marshals `webhook.Receiver`'s Go field names, so the decode tags are PascalCase). `startServerWebhooks` boots the standalone server with the §7.3.2 auto-disable threshold and a fast retry backoff so auto-disable is reachable in a bounded test. `sseClient` plus `openSSE(srv, types...)` opens a bounded NDJSON read of §7.6 `/v1/events`, drops `_heartbeat` keepalives, and yields decoded events via `next`/`waitForEvent`; `openSSE` returns only after the server registered the subscription and flushed the response headers, so an event fired after it cannot be missed. This lifts the `receiverServer` recorder from `pkg/webhook/webhook_test.go` and the `extWebhookHarness` capture channel from `plugin_spi_test.go` into one reusable primitive driven through the shipped binary over HTTP. Required two product additions and one fix: serverboot now reads `PODIUM_WEBHOOK_MAX_FAILURES` (the §7.3.2 auto-disable threshold, default 32) and `PODIUM_WEBHOOK_RETRY_BACKOFF` (a comma-separated duration list overriding the retry schedule), both operator tunables in the pattern of `PODIUM_READONLY_PROBE_FAILURES`; and `handleEvents` (`pkg/registry/server/events.go`) subscribes then flushes the response headers immediately so a quiet stream no longer withholds the 200 from a subscriber for up to one heartbeat (30 s). `test/e2e/notification_sink_primitive_test.go` proves it: `TestNotificationSink_WebhookDeliversSignedEvent` (a CLI reingest fires `artifact.published`; the sink records a signature-valid delivery carrying the §7.3.2 body schema and the published id), `TestNotificationSink_FilterOmitsNonMatchingEvents` (a receiver filtered to an unfired type records nothing while an all-events receiver records the reingest), `TestNotificationSink_AutoDisablesAfterMaxFailures` (`PODIUM_WEBHOOK_MAX_FAILURES=2` plus a failing sink auto-disables after two consecutive failures), `TestNotificationSink_SSEDeliversChangeEvent` (the bounded reader receives the event a reingest fires), `TestNotificationSink_WebhookNotificationProviderRecorder` (the sink is a valid §9.1 webhook NotificationProvider endpoint), and `TestNotificationSink_StandaloneBootsWithWebhookNotifier` (the standalone server boots clean with `PODIUM_NOTIFICATION_PROVIDER=webhook` pointed at the sink). **Realism**: enabling.
- **Current**: outbound webhook and email delivery is not wired to the standalone HTTP surface and the MaxFailures auto-disable override is not exposed to the harness, so delivery, filtering, and auto-disable skip. SSE change-stream subscription also lacks a bounded streaming test client.
- **Target**: an in-test notification recorder, harness control over the provider config and MaxFailures, and a bounded SSE client, so a registry event produces a delivered signature-valid notification and the auto-disable, filter, and subscription paths can be driven.
- **Unblocks**: `plugin_spi_test.go:645,650,655,1170,1325`, `sdk_clients_test.go:394,531`. Feeds G-OPS-1, G-OPS-2.

Other harness-blocked skips map to journey gaps already recorded: large-resource externalization and presign at standalone boot (`cli_reference_test.go:2030`, `deployment_modes_test.go:735`) map to G-STACK-3; the simultaneous standalone-and-standard migration drill (`readme_claims_test.go:1047`) maps to G-LIFECYCLE-5; CLI reembed against a wired backend (`cli_reference_test.go:1573`) maps to G-VEC-10.

### Harness-blocked journeys to capture

#### G-AUTH-14: Admin RBAC through the CLI against an authenticated server
- **Severity**: P1. **Lane**: PR (needs G-INFRA-5). **Status**: open (missing). **Realism**: common. **Nearest test**: `test/e2e/cli_reference_test.go:1558` (skipped).
- **Current**: admin grant, revoke, show-effective, and the admin-versus-user layer distinction are exercised only against standalone, which resolves callers to `system:public` and rejects admin operations with 403, so `cli_reference_test.go:1558,1563,1568,1856,1861`, `standard_deployment_test.go:214,562,567,584`, `http_api_test.go:650,1419,1424`, and `readme_claims_test.go:892,897` skip. The grant table is covered only at the integration and core level.
- **Target**: boot the authenticated harness with a seeded bootstrap admin, run `podium admin grant`, `revoke`, and `show-effective` and an admin-defined layer registration as the admin token, assert the grants apply and that a non-admin token is refused with 403.
- **Sketch**: as a bootstrap-admin token grant a role through the CLI, assert show-effective reflects it, revoke it, and assert a non-admin caller is refused.

#### G-AUTH-15: Signed artifact verifies on load and a tampered blob is refused
- **Severity**: P1. **Lane**: PR (needs G-INFRA-8). **Status**: open (missing). **Realism**: occasional. **Nearest test**: `test/e2e/server_operations_test.go:490` (skipped).
- **Current**: the filesystem bootstrap attaches no signatures, so verification and tamper-detection skip at `server_operations_test.go:490,497,1199`, `governance_adoption_test.go:730,953,971`, and `plugin_spi_test.go:1165`. Signing and verification are unit-tested in `pkg/sign`.
- **Target**: ingest a validly-signed artifact, assert load verifies the signature, tamper the stored bytes, and assert the default-on verifier blocks the load with the signature error while an untampered artifact still loads.
- **Sketch**: ingest a signed artifact, load and assert verification passes, tamper the stored blob, and assert the load is refused.

#### G-AUTH-16: visibility.denied returned to an unauthorized caller on search, load, and objects
- **Severity**: P1. **Lane**: PR (needs G-INFRA-5). **Status**: open (missing). **Realism**: common. **Nearest test**: `test/e2e/http_api_test.go:571` (skipped).
- **Current**: standalone serves all layers public, so the visibility-denied path on `search_artifacts`, `load_artifact`, and `/objects` cannot be triggered, and `artifact_response_test.go:322,751`, `http_api_test.go:571`, `sdk_clients_test.go:388,696`, `server_operations_test.go:504`, and `plugin_spi_test.go:859` skip.
- **Target**: place an artifact in a restricted layer, query as a caller who cannot see it, and assert search omits it while `load_artifact` and the object route return `visibility.denied`, with an authorized caller succeeding on all three.
- **Sketch**: query a restricted artifact as an unauthorized caller, assert search omission plus `visibility.denied` on load and objects, and assert an authorized caller succeeds.

#### G-OPS-2: Outbound webhook delivery applies the event filter and auto-disables after repeated failures
- **Severity**: P2. **Lane**: PR (needs G-INFRA-9). **Status**: open (missing). **Realism**: occasional. **Nearest test**: `test/e2e/plugin_spi_test.go:650` (skipped).
- **Current**: outbound delivery is not wired to the standalone surface and the MaxFailures override is not exposed, so the delivery filter and auto-disable skip at `plugin_spi_test.go:645,650,655`.
- **Target**: configure an event filter and a low MaxFailures against a failing in-test receiver, fire matching and non-matching events, and assert only matching events attempt delivery and the provider auto-disables after the failure threshold.
- **Sketch**: configure a filtered webhook with a low failure cap against a failing receiver, fire events, and assert filtered delivery and auto-disable.

#### G-JOURNEY-3: Session snapshot stays consistent across a mid-session republish
- **Severity**: P1. **Lane**: PR (needs G-INFRA-7). **Status**: closed (test/e2e/discovery_search_test.go:TestSearch_LoadArtifactSessionPin drives the journey through the real podium-mcp bridge: a session loads an artifact at latest 1.0.0, the runtime-republish primitive stages 2.0.0 mid-session, the same session_id reloads in a fresh bridge process and materializes the pinned 1.0.0 to disk, and a fresh session pins to 2.0.0. The `session_id` forwarding the bridge already does — cmd/podium-mcp/session_id_test.go, BUILD-GAPS confirms cmd/podium-mcp/main.go — backs it; the stale "bridge does not forward session_id" skip on discovery_search_test.go:998 is also lifted into TestSearch_SearchArtifactsSessionID). **Realism**: occasional. **Nearest test**: `test/e2e/discovery_search_test.go:1004` (skipped).
- **Current**: a new version cannot be ingested into a running standalone server mid-test and the MCP bridge does not forward `session_id`, so session-snapshot consistency is inexpressible and `discovery_search_test.go:998,1004` and `deployment_modes_test.go:351` skip.
- **Target**: open a session, load an artifact, republish a newer version, and assert the same session keeps serving the pinned snapshot while a new session sees the new version. Forwarding `session_id` through the bridge is a product prerequisite.
- **Sketch**: load within a session, republish a newer version, and assert the session snapshot is stable while a fresh session sees the update.

#### G-LIFECYCLE-7: Expand-contract rolling upgrade across two binaries over one Postgres
- **Severity**: P2. **Lane**: Nightly or Release; needs Postgres and two built binaries. **Status**: closed (test/e2e/server_ops_rolling_upgrade_test.go: TestServerOps_RollingUpgradeCoexistence and TestServerOps_RollbackBeforeFinalize drive the §13.4 additive upgrade over a live Postgres metadata store. A prior-version org schema, staged with reduced manifests + layer_configs tables missing the recent additive columns plus a seeded artifact under a registered public layer, is migrated forward in place by the current binary at boot via ensureOrg; the seeded row survives, two server processes coexist against the one Postgres serving reads and writes during the overlap, the re-applied migration is a clean idempotent no-op, and reverting to the prior binary's column set still reads and writes the migrated table. The two binary versions are realized by staging the earlier binary's database state, since the harness builds one binary; the object store is filesystem and the vector backend BM25-only, so the test gates on PODIUM_POSTGRES_DSN alone. The `server_operations_test.go:1247,1254` skips are removed). **Realism**: occasional. **Nearest test**: `test/e2e/server_ops_rolling_upgrade_test.go` (TestServerOps_RollingUpgradeCoexistence, TestServerOps_RollbackBeforeFinalize).
- **Current**: an expand-contract rolling upgrade requires two binary versions sharing a Postgres database, which is not available in e2e, so `server_operations_test.go:1247,1254` skip. In-place single-binary SQLite upgrade is G-LIFECYCLE-4.
- **Target**: run an old and a new binary against one Postgres through an expand-contract migration, and assert both serve reads and writes during the overlap, the additive migration applies without downtime, and the finalize step runs cleanly.
- **Sketch**: run two binary versions over one Postgres through expand-contract, asserting both serve during the overlap and finalize is clean.

### Skips blocked on unbuilt product features

These skips name an unbuilt feature, so the test cannot pass until the feature ships. They belong in BUILD-GAPS, and several record that no BUILD-GAPS finding is filed.

- MCP-bridge mcp-server result filtering (spec §5): `artifact_types_test.go:551`, `core_concepts_test.go:479`, `discovery_search_test.go:480,486`, `deployment_modes_test.go:721` (no finding filed).
- Server-source sync HTTP path (F-2.2.2): `core_concepts_test.go:152,593,598`, `deployment_modes_test.go:257,665`.
- Server-source sync omits the SKILL.md secondary file, breaking the §2.2 bit-identical claim: `standalone_server_test.go:763`.
- `sandbox_profile` enforcement not wired: `governance_adoption_test.go:907`.
- Bundled-resource (file) merge across `extends` is unimplemented; `core.mergeChain` serves the child's resources only, so a parent-only bundled file is never composed into the child (§4.4/§4.6): `artifact_extends_test.go:308,315,322,329`.
- Zero-flag bootstrap banner, `--strict`, and `PODIUM_NO_AUTOSTANDALONE` (F-13.10.1): `deployment_modes_test.go:90,604,609`. Feeds G-CONFIG-2.
- ~~`session_id` forwarding through the MCP bridge (F): `discovery_search_test.go:998`.~~ Resolved: the bridge forwards `session_id` (cmd/podium-mcp/main.go, proven by cmd/podium-mcp/session_id_test.go); `discovery_search_test.go:998` is now TestSearch_SearchArtifactsSessionID, and G-JOURNEY-3 closed on the same capability.
- Postgres PITR plus S3 bucket restore disaster-recovery drill: `server_operations_test.go:1261` (Release or manual lane).

### Stale skips to re-check

The managed-stack parity work confirmed the §7.3.4 reingest pipeline runs (it ingested the parity artifact). Skips that assert reingest is a no-op predate that fix and should be re-evaluated: `deployment_modes_test.go:346,351`.

## Priority summary

P0 gaps: G-AUTH-1, G-AUTH-2, G-AUTH-7, G-DOC-5, G-VEC-4.
P1 gaps: G-AUTH-3, G-AUTH-4, G-AUTH-5, G-AUTH-6, G-AUTH-8, G-AUTH-9, G-DOC-1,
G-DOC-2, G-DOC-3, G-DOC-4, G-VEC-1, G-VEC-2, G-VEC-3, G-VEC-5, G-VEC-6, G-EMB-1,
G-PGV-1, G-PGV-2, G-PGV-3, G-STACK-1, G-STACK-2, G-HARN-1, G-INFRA-2.
P2 gaps: G-DOC-6, G-DOC-7, G-EMB-2, G-INFRA-1, G-INFRA-3, G-INFRA-4.

The in-process JWKS issuer harness (G-AUTH-3) together with the server-side OIDC
verifier it tests (G-AUTH-1) unblocks the most skipped auth tests and turns the
OIDC documentation into a tested flow. The live external-services suite (G-VEC
and G-EMB) is the largest new build, and it needs the accounts above and the
Release-lane wiring.

The 2026-06-03 user-journey coverage pass added 41 gaps (30 P1 and 11 P2) in the User-journey coverage gaps section: G-JOURNEY-1 and G-JOURNEY-2, G-MATERIALIZE-1 through G-MATERIALIZE-9, G-MULTILAYER-1 through G-MULTILAYER-4, G-VEC-7 through G-VEC-11, G-SCALE-1 through G-SCALE-4, G-LIFECYCLE-1 through G-LIFECYCLE-6, G-CONFIG-1 through G-CONFIG-4, G-AUTH-10 through G-AUTH-13, G-STACK-3, G-DOC-8, and G-OPS-1.

The harness-evolution pass (2026-06-03) added the enabling test-infrastructure primitives G-INFRA-5 through G-INFRA-9 and the harness-blocked journeys G-AUTH-14 through G-AUTH-16, G-OPS-2, G-JOURNEY-3, and G-LIFECYCLE-7. Each primitive lists the skipped tests it unblocks, so every harness-capability skip in the suite maps to a gap. Skips that name an unbuilt feature are listed under "Skips blocked on unbuilt product features" and belong in BUILD-GAPS.
