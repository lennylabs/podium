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
- **Severity**: P1. **Lane**: PR. **Status**: open.
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
- **Severity**: P1. **Lane**: tooling. **Status**: open.
- **Current**: The script decides a doc source is "already built" by checking for
  `test/e2e/docs_<slug>_test.go`. The committed gate forbids that exact filename,
  and the real tests are feature-named, so the check always reports every source
  as unbuilt and would regenerate forbidden files.
- **Evidence**: `close-build-gaps.py:228-229`; the gate at
  `test/e2e/naming_convention_test.go:37`.
- **Target**: Point built-detection at the feature-named convention or the
  G-DOC-1 manifest, or retire the script.

### G-DOC-3: Quickstart examples are documented incorrectly
- **Severity**: P1. **Lane**: PR. **Status**: open.
- **Current**: The quickstart shows output and paths the suite asserts are wrong.
  The documented `podium sync` output reads `personal/hello/greet@1.0.0 →
  .claude/agents/greet.md`, but a skill materializes to
  `.claude/skills/greet/SKILL.md`. The doc also claims watch mode uses `fsnotify`,
  while the implementation polls.
- **Evidence**: `docs/getting-started/quickstart.md:155-157`, `:175`, and
  `:183-184`; assertions at `test/e2e/quickstart_flow_test.go:194-200` (skills
  path and absence of the agents path) and `:205-225` (output format), plus the
  poll-based watch assertion.
- **Target**: Correct the quickstart prose and output, or change the code to
  match. The tests already enumerate every divergence.

### G-DOC-4: `podium lint <path>` positional form is documented but fails
- **Severity**: P1. **Lane**: PR. **Status**: open.
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
- **Severity**: P2. **Lane**: PR. **Status**: open.
- **Current**: A worked example uses a hyphenated default collection while the
  implementation defaults to an underscore form (flagged in the
  `vector_backend_config_test.go` skip note).
- **Evidence**: `docs/deployment/vector-backends.md` examples; skip note in
  `test/e2e/vector_backend_config_test.go`.
- **Target**: Align the doc with the implemented default, then assert it.

### G-DOC-7: Landing and index pages with duplicated runnable content
- **Severity**: P2. **Lane**: PR. **Status**: open.
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

### G-STACK-1: No author-to-consumer parity on the managed stack
- **Severity**: P1. **Lane**: PR (Postgres and S3 already present) plus Release (vector). **Status**: open.
- **Current**: Live Postgres, pgvector, and S3 are exercised at the package
  conformance level, but no full author-to-consumer journey runs against the
  managed stack.
- **Target**: One e2e that runs the quickstart flow end-to-end on Postgres plus
  pgvector plus S3, and a Release-lane variant that swaps in a managed vector
  backend.

### G-STACK-2: Concurrency and atomicity are thin
- **Severity**: P1. **Lane**: PR. **Status**: open.
- **Current**: The shared cache directory, the shared audit hash chain, and
  concurrent sync to one target are tested single-threaded or not at all.
- **Target**: Add tests for concurrent same-version ingest, concurrent sync to a
  shared target, and concurrent writers to the audit chain, asserting the chain
  stays valid and the cache stays consistent.

---

# HARN: Harness drift

### G-HARN-1: Real-agent harness suite is out of CI
- **Severity**: P1. **Lane**: a dedicated scheduled job. **Status**: open.
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
- **Severity**: P2. **Lane**: PR. **Status**: open.
- **Current**: `goleak` appears in no package. Watch mode, MCP stdio, and SSE
  goroutine leaks are unguarded.
- **Target**: Add `goleak.VerifyTestMain` to the packages with long-running
  goroutines.

### G-INFRA-2: Ambient backend env can leak into "backend absent" tests
- **Severity**: P1. **Lane**: PR. **Status**: open.
- **Current**: A recent fix scrubbed `PODIUM_POSTGRES_DSN` and `PODIUM_S3_*` from
  CLI subprocess env so `make test-live` would not suppress the no-autostandalone
  refusal. The class of bug remains: any new test asserting "backend absent"
  behavior inherits ambient backend env.
- **Evidence**: scrub at `test/e2e/helpers_test.go:60-73`.
- **Target**: Route all "backend absent" tests through the scrub helper and add a
  guard that fails if a backend env var is present where the test asserts its
  absence.

### G-INFRA-3: `TEST_INFRASTRUCTURE_PLAN.md` describes an unbuilt system
- **Severity**: P2. **Lane**: documentation. **Status**: open.
- **Current**: The plan describes fast, medium, and slow lanes, `testcontainers-go`,
  phase gating, 95% coverage floors, and mutation testing. None of it is built,
  and the real coverage floor is 50.
- **Target**: Reconcile the plan with the implemented system or mark it
  superseded, so it stops implying coverage that does not exist.

### G-INFRA-4: Sigstore live signing is manual-only
- **Severity**: P2. **Lane**: manual today; consider Release. **Status**: open.
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
