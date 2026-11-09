# Proposal 0056: State that multi-tenancy, freeze windows, SCIM, and outbound webhooks run on any registry server when configured, and key single-tenant wording on PODIUM_MULTI_TENANT rather than on the backend

- Issue: (to be filed)
- Status: Applied to spec (2026-11-09). Verified on 2026-11-08 after 6 adversarial review rounds (5 findings fixed).
- Date: 2026-11-08

This document stages spec, documentation, test, and Go changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. No runtime behavior changes. The one product-code edit changes a §6.10 `suggested_action` string and the comment above it.

## Summary

**What changes.**

- §13.10 replaces the "Out of scope for standalone." paragraph with a configuration-gated paragraph, drops "stays out of scope for standalone" from its gateway-delegated identity paragraph, splits the `trusted-headers` bind rule into its single-tenant and multi-tenant cases in that paragraph and in the comparison table's Identity provider row, and states that `podium admin migrate-to-standard` carries the manifests, layer configuration, and admin grants of the `default` org alone while it copies the object-store blobs and the audit log whole (SPEC-1, SPEC-2, SPEC-7). §13.11.3 moves multi-tenancy and SCIM from the remote-server list to the server-but-not-remote paragraph (SPEC-3).
- §6.3.1, §6.3.3, §6.10, and §7.3.3 define a single-tenant registry as one started without `PODIUM_MULTI_TENANT` on either backend, §6.10's `suggested_action` drops "on a standard backend", and §7.3.3 scopes its forced-RLS and operator-sentinel sentence to the Postgres backend (SPEC-4, SPEC-5, SPEC-6).
- `pkg/registry/server/error_envelope.go` mirrors the §6.10 `suggested_action` and corrects its comment, and `pkg/registry/server/tenants_test.go` pins the new string (CODE-1).
- A new end-to-end test boots `serve --standalone` in multi-tenant mode under `trusted-headers` and exercises tenant management and routing on the SQLite store (TEST-1). A manual scenario covers the same path and the single-tenant refusal (MV-1).
- `README.md`, `docs/overview.md`, `docs/deployment/*.md`, `docs/getting-started/*.md`, `docs/reference/*.md`, and `site/src/content/landing.ts` describe the clustered tier as adding Postgres tenant isolation and high availability, and stop calling SCIM or multi-tenancy clustered-only (DOC-1 through DOC-6). The reference pages define a single-tenant registry by `PODIUM_MULTI_TENANT`, and `docs/reference/error-codes.md` and `docs/deployment/single-node.md` state the outcome of a multi-tenant registry with no identity verifier (DOC-4, DOC-7). `docs/deployment/single-node.md` states the migration's `default`-org metadata limit and that blobs and the audit log are copied whole (DOC-9). `docs/deployment/gateway-delegated-identity.md` defines a single-tenant registry by `PODIUM_MULTI_TENANT` rather than by deployment tier or org count (DOC-10). `CHANGELOG.md` gains a `### Documentation` bullet (DOC-8).

**Fixed decisions.**

- The spec follows the code. No gate is added or removed for any of the four features (D-1).
- The §13.10 replacement states each precondition the code enforces, and cites the section that owns it rather than restating it (D-2).
- Schema-per-org, forced row-level security, and the operator-list sentinel are Postgres-only, and the text says so (D-3, D-7).
- A single-tenant registry is keyed on `PODIUM_MULTI_TENANT` on either backend. The `registry.tenant_management_unavailable` code, message, status, and matrix cell stay (D-4).
- The §6.10 `suggested_action` drops "on a standard backend" in spec and code in the same commit (D-5).
- §13.11.3's filesystem list stays as it is, outbound webhooks included (D-6).
- No new env var, error code, `registry.yaml` key, flag, HTTP member, SDK change, or spec heading (D-8).
- Every spec edit and CODE-1 land in one commit, before the test and docs steps (D-9).
- The release vehicle is 0.5.3, under the existing `### Documentation` group in `[Unreleased]` (D-10).
- Accurate operational advice stays (D-11). The migration's metadata limit to the `default` org is documented and left unchanged, and the text states that blobs and the audit log are copied whole (D-12).
- The case of `PODIUM_MULTI_TENANT` with no request-time identity verifier stays unspecified. DOC-4 and DOC-7 state its current outcome to operators, SPEC-1 gains no clause for it, and the spec text stays with Q-2.

**Watch out for.**

- **This reverses an earlier scoping decision.** Proposals 0001 (`proposals/0001-server-side-request-authentication.md:175`), 0002, and 0046 (line 258) kept standalone single-tenant on purpose. SPEC-1's rationale says so. Do not describe the old paragraph as a mere self-contradiction in commit messages or the PR.
- **The spec edits land together.** SPEC-1 contradicts §13.10 line 155 (the comparison table's Identity provider row), §13.10 line 217, §13.11.3 line 353, §6.3.1 line 81, §6.3.3 line 115, §6.10 line 523, and §7.3.3 line 217 until SPEC-2 through SPEC-6 land, and §13.10 line 172 (the upgrade path) reads as migrating every tenant's metadata until SPEC-7 lands. CODE-1 mirrors SPEC-5 and must change in the same commit under the mirrored-surfaces rule in `.claude/rules/spec-driven-development.md`.
- **Setting `PODIUM_MULTI_TENANT` alone does not route requests.** The tenant router is installed only when a request-time identity verifier is installed (`internal/serverboot/serverboot.go:1452-1459`). A multi-tenant registry with no verifier, or in public mode, leaves every request unrouted and still answers `/v1/admin/tenants` with `registry.tenant_management_unavailable`. That divergence from §4.7.1 is a non-goal. No staged sentence may say the variable alone is enough, and CODE-1's comment does not claim it. DOC-4 and DOC-7's `error-codes.md` row state this outcome for operators, and the spec text for the case stays with Q-2.
- **"An identity provider the registry verifies" in DOC-3, DOC-4, and DOC-7 covers `trusted-headers`.** It means any request-time identity verifier, which is the §4.7.8 term and the SPEC-1 wording. Do not narrow it to `oidc-jwt` while applying the edit.
- **`podium admin migrate-to-standard` carries the `default` org's metadata alone.** The metadata pump reads the manifests, layer configuration, and admin grants of the `default` org only (`cmd/podium/admin_migrate.go:299-313`). The object pump walks the whole source object store with no tenant filter (`cmd/podium/admin_migrate.go:393-401`), and the audit step copies the single process-wide audit file byte for byte (`cmd/podium/admin_migrate.go:205-211`; `internal/serverboot/serverboot.go:982`), so every tenant's blobs and audit events reach the target. DOC-4 and DOC-5 point single-node operators at the clustered tier for Postgres isolation, so SPEC-7 and DOC-9 state both halves. Do not write that a non-default tenant's artifacts or audit history stay behind, and do not extend the migration in this change.
- **TEST-1 sends `X-Podium-User-Sub`.** The trusted-headers subject header is `X-Podium-User-Sub` (`pkg/identity/trusted_headers.go:29`). With `X-Podium-User-Id` the caller has no subject, `OperatorAuthorize` rejects it, and the create returns 403.
- **An empty layer list does not prove routing.** An unrouted list and a list routed to a tenant with no layers both answer 200 with `{"layers":[]}` (`pkg/registry/server/layers.go:1632-1635`). TEST-1 step 4 and MV-1 step 4 therefore register a user layer as dave in `globex` and list it back as dave in each org. Keep the same subject for the cross-tenant list, because a user layer is visible to its owner alone (`pkg/registry/server/layers.go:1511`, `:322-342`).
- **TEST-1 must not call `msSkipIfNoStack`.** Its purpose is the SQLite backend. Parts of `test/e2e` skip silently on macOS, so run it with `-v` and confirm no `SKIP` line before claiming it passed.
- **The forced-RLS sentence in §7.3.3 is Postgres-only.** `SQLite.ListTenants` is a plain `SELECT` (`pkg/store/sqlite.go:235-238`), and the sentinel exists only in `pkg/store/postgres.go:219-247`. SPEC-6 prefixes the sentence; do not delete it.
- **§13.10's last two sentences stay byte-identical.** "deployment shape" and the em-dash in the vulnerability-scanning sentence are a non-goal.
- **`site/src/content/landing.ts` has no snapshot test.** `site/test` asserts structural classes only. Run `npm test` and `npm run check` under `site/` anyway, because the array literal must stay valid TypeScript.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3, SPEC-4, SPEC-5, SPEC-6, SPEC-7, CODE-1. Every spec edit and the §6.10 code mirror land in one commit. The spec edits are bundled because SPEC-1 contradicts the other sections until they change, and CODE-1 rides in the spec commit because the mirrored-surfaces rule requires both sides of §6.10 in the same commit.
      Levels: unit. Depends on: —
- [ ] **S2 · test** — TEST-1. The standalone multi-tenant end-to-end test under `trusted-headers`.
      Levels: e2e. Depends on: S1
- [ ] **S3 · test** — MV-1. Scenario S90 in `test/manual-validation.md`.
      Levels: —. Depends on: S1
- [ ] **S4 · docs** — DOC-1. The tier tables and the landing page name Postgres tenant isolation and high availability.
      Levels: unit (site). Depends on: S1
- [ ] **S5 · docs** — DOC-2. The getting-started tier summaries drop multi-tenancy from the clustered stack.
      Levels: —. Depends on: S1
- [ ] **S6 · docs** — DOC-3. `clustered.md` description, multi-tenancy bullet, and SCIM bullet.
      Levels: —. Depends on: S1
- [ ] **S7 · docs** — DOC-4, DOC-9. `single-node.md` "What the tier omits" and "Migrating to clustered". Bundled because both edit the same page and the same reader checks the migration advice against its limit.
      Levels: —. Depends on: S1
- [ ] **S8 · docs** — DOC-5. `local.md` and `progressive-adoption.md` reasons to move to clustered.
      Levels: —. Depends on: S1
- [ ] **S9 · docs** — DOC-6. SCIM availability lines in `integrations.md` and `access-control.md`.
      Levels: —. Depends on: S1
- [ ] **S10 · docs** — DOC-7. The reference pages' single-tenant wording for tenant management.
      Levels: —. Depends on: S1
- [ ] **S11 · docs** — DOC-10. The single-tenant definition in `gateway-delegated-identity.md`.
      Levels: —. Depends on: S1
- [ ] **S12 · docs** — DOC-8. The CHANGELOG `### Documentation` bullet.
      Levels: —. Depends on: S1–S11

**Ordering constraints.** S1 lands first, and `make coverage-gate` passes on it before any later step (D-9). S2 through S11 are independent of one another. S12 describes the whole change and lands last. Land every step in one pull request.

## Current state and the gap

### What the binary does

The code gates none of the four features by deployment mode or store backend.

- **SCIM.** The receiver mounts at `/scim/v2/` whenever `PODIUM_SCIM_TOKENS` holds a non-blank token (`internal/serverboot/serverboot.go:71-86`, `:1289-1291`; `pkg/registry/server/server.go:435-436`). The pushed directory widens `groups:` visibility only under `oidc-jwt` and `injected-session-token` (`internal/serverboot/serverboot.go:111-118`, `:1198`).
- **Multi-tenancy.** The mode turns on from `PODIUM_MULTI_TENANT` alone (`internal/serverboot/serverboot.go:2316`). The tenant router is installed only when an identity verifier is also installed (`internal/serverboot/serverboot.go:1452-1459`). The router's resolver reads tenants through the generic `store.Store` (`internal/serverboot/orgid.go:27-41`). The tenant-admin gate checks only `tenantRouter == nil` (`pkg/registry/server/tenants.go:104-108`). The SQLite store implements tenant CRUD over tables keyed by `tenant_id` (`pkg/store/sqlite.go:80-111`, `:204-238`). A multi-tenant `trusted-headers` boot with no proxy secret is refused on standalone too (`pkg/registry/server/config_validate.go:157-164`; `test/e2e/auth_gateway_test.go:253-258`). With the mode on, the registry binds a no-data tenant (`internal/serverboot/serverboot.go:1103-1110`), and the layers in `registry.yaml` are stored under the `default` tenant.
- **Freeze windows.** `freeze_windows` is read from `registry.yaml` unconditionally (`internal/serverboot/yaml_config.go:456-459`) and enforced on reingest (`internal/serverboot/reingest.go:64`).
- **Outbound webhooks.** The worker is always built and attached (`internal/serverboot/serverboot.go:1218-1231`, `:1262`; `pkg/registry/server/server.go:431-433`). Receiver registration requires an authenticated per-tenant admin (`pkg/registry/core/admin.go:21-23`).
- **`--standalone`.** The flag only sets `PODIUM_STANDALONE` (`cmd/podium/serve.go:93-99`), which only suppresses the zero-flag auto-bootstrap (`internal/serverboot/standalone_bootstrap.go:43`).
- **End-to-end coverage.** Tests boot `serve --standalone` with SCIM (`test/e2e/auth_scim_visibility_test.go:55-63`), with a freeze window (`test/e2e/governance_adoption_test.go:771-816`), and with §7.3.2 receivers (`test/e2e/notification_sink_helpers_test.go`, `test/e2e/notification_sink_primitive_test.go:57`). No test exercises tenant management or routing on SQLite. Every `PODIUM_MULTI_TENANT` end-to-end test except the startup refusal calls `msSkipIfNoStack` (`test/e2e/layer_tenant_routing_test.go:93`, `test/e2e/quota_tenant_isolation_test.go:136`, `test/e2e/webhook_tenant_isolation_test.go:68`, `test/e2e/events_visibility_test.go:215`), and `internal/serverboot/multitenant_integration_test.go` uses `store.NewMemory()`.
- **Migration.** `podium admin migrate-to-standard` probes the `default` org's derived ID and the literal `default`, and migrates the manifests, layer configuration, and admin grants of those tenants alone (`cmd/podium/admin_migrate.go:299-313`). It copies every blob in the source object store with no tenant filter (`cmd/podium/admin_migrate.go:393-401`), and, when `--target-audit-log` names a target, it copies the single audit file the server writes for every tenant byte for byte (`cmd/podium/admin_migrate.go:205-211`; `internal/serverboot/serverboot.go:982`).

### Where the text disagrees

- **§13.10** heads a paragraph "Out of scope for standalone." over the four features and says in the same paragraph that they "can be enabled individually". It calls them "inert without the supporting infrastructure", which is false for freeze windows and multi-tenancy. The gateway-delegated identity paragraph says multi-tenancy "stays out of scope for standalone".
- **§13.11.3** lists "Multi-tenancy and SCIM." under features that require "specifically a remote server".
- **§6.3.1, §6.3.3, §6.10, and §7.3.3** treat a standalone backend as single-tenant whatever its configuration, and §7.3.3 limits tenant management to a "multi-tenant standard backend". §4.7.1 and the §13.12 `PODIUM_MULTI_TENANT` row already key on the mode, which matches the code.
- **§6.10's `suggested_action`** says "on a standard backend", mirrored at `pkg/registry/server/error_envelope.go:119-123`.
- **Docs.** `docs/deployment/integrations.md:94` and `docs/deployment/access-control.md:126` say SCIM is "available on the clustered tier". `README.md:139`, `docs/overview.md:72`, `docs/deployment/index.md:15`, `site/src/content/landing.ts:388-392`, `docs/deployment/clustered.md:4`, `:31`, and `:36`, `docs/getting-started/how-it-works.md:221-232`, and `docs/getting-started/concepts.md:99-100` present multi-tenancy and SCIM as what the clustered tier adds. `docs/deployment/single-node.md:155`, `:159`, and `:196`, `docs/deployment/local.md:162`, and `docs/deployment/progressive-adoption.md:120` give them as reasons to leave single node. `docs/reference/http-api.md:691`, `docs/reference/cli.md:564`, and `docs/reference/error-codes.md:161` say a "single-tenant or standalone registry" rejects tenant management. `docs/deployment/gateway-delegated-identity.md:100` says a single-tenant registry "covers a single-node deployment and a clustered one with a single org", which keys single-tenancy on the tier and the org count.

### What stays a difference between the tiers

Schema-per-org, forced row-level security, and the operator-list sentinel exist only on the Postgres backend (§4.7.1 Postgres isolation, §13.12 Tenancy, §7.3.3; `pkg/store/postgres.go:219-247`, `:360`). SQLite scopes rows by a `tenant_id` column. Postgres-backed isolation, replicas, and high availability remain accurate reasons to choose the clustered tier.

## Decisions

- **D-1. Spec follows code.** The code gates these features on configuration only, and end-to-end tests run them on `serve --standalone`. The fix rewrites the spec and docs and changes no runtime behavior. The heading `## 13.10 Standalone Deployment` and every § number stay. A store gate was considered and rejected: SCIM, freeze windows, and webhooks already run on standalone in end-to-end tests, and SQLite already implements tenant CRUD.
- **D-2. The §13.10 replacement states the preconditions the code enforces.** (a) Multi-tenant routing needs `PODIUM_MULTI_TENANT` plus a request-time identity verifier (`internal/serverboot/serverboot.go:1452-1459`, `:1495-1500`; §4.7.8). (b) Multi-tenant `trusted-headers` needs `PODIUM_TRUSTED_PROXY_SECRET`, which §6.3.3 and §13.12 already state. (c) SCIM needs `PODIUM_SCIM_TOKENS` and an IdP that can reach the server, and its directory feeds `groups:` only under the providers §6.3.1 names (§13.12). (d) Freeze windows need `registry.yaml` `freeze_windows` (§4.7.2). (e) Webhook receiver registration needs the per-tenant admin role (§7.3.2 Receiver authorization). The replacement cites the owning section for each rule instead of restating it.
- **D-3. No claim of equal isolation on every backend.** Schema-per-org, forced RLS, and the operator-list sentinel are Postgres-only, and the SQLite store scopes rows by tenant. The clustered tier's added value is stated as Postgres-backed tenant isolation, replicas, and high availability.
- **D-4. Single-tenancy is keyed on `PODIUM_MULTI_TENANT` on either backend**, matching §4.7.1 and §13.12. §6.3.1, §6.3.3, §6.10, §7.3.3, and §13.10 drop "standalone backend" as a single-tenant class and the "out of scope for standalone" rationale. The `registry.tenant_management_unavailable` code, its message, its HTTP status, and its matrix cell stay.
- **D-5. The §6.10 `suggested_action` and its code mirror change together.** `spec/06-mcp-server.md` and `pkg/registry/server/error_envelope.go` are a mirrored §6.10 surface, so "on a standard backend" leaves both in the same commit as the comment above the entry. No existing test asserts the string, and `tools/matrix/matrices.go:79` keys §6.10 on code names only. CODE-1 is the only product-code change, and its commit extends `TestTenants_SingleTenantUnavailable` to pin the new string.
- **D-6. §13.11.3 keeps its filesystem-versus-server list** unchanged, outbound webhooks (§7.3.2) included, because a filesystem source lacks them. Only the "Multi-tenancy and SCIM." bullet leaves the remote-server list, and the paragraph after the list absorbs it. "Centralized audit independent of clones." stays.
- **D-7. The §7.3.3 GET bullet's forced-RLS and sentinel sentence is scoped to the Postgres backend.** Once §7.3.3 states that the endpoints run on any multi-tenant registry, the unscoped sentence would describe SQLite falsely.
- **D-8. No new surface.** The change adds no env var, error code, `registry.yaml` key, flag, HTTP member, SDK change, or spec heading. TEST-1 adds one standalone multi-tenant end-to-end test, because the spec now states that multi-tenancy runs on a standalone server and no end-to-end test exercises tenant management or routing on SQLite.
- **D-9. Spec first, in one commit with its code mirror.** SPEC-1 through SPEC-7 land in one spec commit, because SPEC-1 contradicts §6.3.1, §6.3.3, §6.10, §7.3.3, and the rest of §13.10 and §13.11.3 until they change. CODE-1 lands in that commit, because the mirrored-surfaces rule requires both sides of a §6.10 entry to change together, and that rule is the narrower one where it meets the spec-first commit rule. The commit passes `make coverage-gate` before TEST-1, MV-1, and the docs steps.
- **D-10. Release vehicle.** The change ships in 0.5.3. The CHANGELOG bullet goes under the existing `### Documentation` group in `[Unreleased]`, which already holds the proposal 0055 entries.
- **D-11. Accurate operational advice stays.** These lines are unchanged: `docs/deployment/clustered.md:9` ("multi-tenant requirements"), `docs/getting-started/how-it-works.md:225-227` ("Who it is for"), `docs/deployment/clustered.md:34` (freeze windows already say a single node reads the same list), `docs/deployment/gateway-delegated-identity.md:57` (SCIM already described as keyed on the variable alone; line 100 on the same page is rewritten by DOC-10), and `docs/deployment/local.md:37` and `:171` (filesystem-tier descriptions). Lines that make a capability claim the code contradicts are rewritten. Advice lines are reframed around Postgres isolation, replicas, and scale.
- **D-12. The migration limit is documented and left unchanged.** `podium admin migrate-to-standard` migrates the manifests, layer configuration, and admin grants of the `default` org alone, and it copies the object-store blobs and the audit log whole, including those of every tenant (`cmd/podium/admin_migrate.go:299-313`, `:393-401`, `:205-211`). Once the spec says a standalone registry runs multi-tenant, an operator following DOC-4's migration advice from a multi-tenant single node would leave its other tenants' metadata behind without warning, and would also carry their blobs and audit events into the target. SPEC-7 and DOC-9 state both halves. Extending the migration is Q-3.

## Spec amendment: §13.10 Standalone Deployment, configuration-gated features

**SPEC-1.** `spec/13-deployment.md`, §13.10, the paragraph that begins "**Out of scope for standalone.**" (line 258). Replace the whole paragraph.

**Rationale.** Proposals 0001 (line 175), 0002, and 0046 (line 258) kept multi-tenancy out of scope for standalone. The code never gated it: `PODIUM_MULTI_TENANT` plus any request-time verifier installs the tenant router on SQLite (`internal/serverboot/serverboot.go:1452-1459`), and SQLite keys every table by `tenant_id`. With the mode on, the registry binds a no-data tenant, so 0001's synthetic-org objection does not apply. This change defines standalone multi-tenancy instead of adding a store gate. A store gate was rejected because SCIM, freeze windows, and webhooks already run on standalone in end-to-end tests, and SQLite already supports tenant CRUD. The current paragraph also calls the features "inert without the supporting infrastructure", which is false for freeze windows (`internal/serverboot/yaml_config.go:456-459`) and for multi-tenancy. SPEC-1 lands in one spec commit with SPEC-2 through SPEC-7 (D-9).

Current text:

> **Out of scope for standalone.** Multi-tenancy, freeze windows, SCIM, and outbound webhooks. These are present in the binary but inert without the supporting infrastructure (an IdP for SCIM, etc.). They can be enabled individually when their dependencies are available. §8.6 local chain-head anchoring runs on a standalone server as on any other registry process (§13.12). Vulnerability scanning is out of scope for every deployment shape, not just standalone — see §1.1, §4.7.7.

Replacement:

> **Features enabled by configuration.** Multi-tenancy, freeze windows, SCIM, and outbound webhooks run on a standalone server as on any other registry process, and each takes effect only when it is configured. Multi-tenant mode runs when `PODIUM_MULTI_TENANT` is `true` (§13.12), and it routes requests to tenants only on a registry with a request-time identity verifier (§6.3.1, §6.3.3). The standalone SQLite store scopes each row by its tenant, and the schema-per-org isolation and forced row-level security of §4.7.1 are properties of the Postgres backend. Freeze windows apply when `registry.yaml` lists `freeze_windows` (§4.7.2). The SCIM 2.0 receiver mounts when `PODIUM_SCIM_TOKENS` lists a token (§13.12), and it receives pushes only from an IdP that can reach the server. Outbound webhook receivers are registered by an authenticated tenant admin (§7.3.2), so registering one requires an identity provider. §8.6 local chain-head anchoring runs on a standalone server as on any other registry process (§13.12). Vulnerability scanning is out of scope for every deployment shape, not just standalone — see §1.1, §4.7.7.

The last two sentences are unchanged. The replacement omits the `groups:`-provider restriction, which the §13.12 `PODIUM_SCIM_TOKENS` row states, and the unrouted behavior of a multi-tenant registry with no verifier, which §4.7.8 states.

## Spec amendment: §13.10 Standalone Deployment, gateway-delegated identity

**SPEC-2.** `spec/13-deployment.md`, §13.10. Two edits: the paragraph that begins "**Gateway-delegated identity (`oidc-jwt`, `trusted-headers`).**" (line 217), whose third and fourth sentences are replaced, and the Standalone cell of the Identity provider row in the "What changes from the standard topology" comparison table (line 155), whose last clause is replaced. The rest of the paragraph and of the table is unchanged.

**Rationale.** With `PODIUM_MULTI_TENANT` set and either provider installed, a standalone server installs the tenant router (`internal/serverboot/serverboot.go:1452-1459`), and the router resolves tenants through the generic store (`internal/serverboot/orgid.go:27-41`). `test/e2e/auth_gateway_test.go:253-258` shows that the multi-tenant validation runs on `serve --standalone`: it asserts the startup refusal and does not exercise a running multi-tenant standalone server. Once the paragraph allows multi-tenancy, the unqualified bind sentence would cover the multi-tenant case, where it is false: the registry refuses a multi-tenant `trusted-headers` boot with no proxy secret on any bind, loopback included (`pkg/registry/server/config_validate.go:157-169`, where `--allow-public-bind` is consulted only in the single-tenant branch; §6.3.3; §13.12 `PODIUM_TRUSTED_PROXY_SECRET`). The comparison table's Identity provider row states the same bind rule without the qualification, so it is split the same way. Leaving it would give §13.10 two incompatible startup rules for a multi-tenant `trusted-headers` standalone registry. §6.3.3 (`spec/06-mcp-server.md:147`) and §13.12 (`spec/13-deployment.md:516`) already qualify the rule by tenancy.

Current text, third sentence:

> Neither adds multi-tenancy, which stays out of scope for standalone, so the registry resolves every caller to its sole tenant.

Replacement:

> Neither turns on multi-tenancy. Without `PODIUM_MULTI_TENANT` the registry resolves every caller to its sole tenant, and with it the registry selects each request's tenant from the caller's organization (§6.3.1).

Current text, fourth sentence:

> `trusted-headers` reads identity from unverified headers, so it constrains the bind: a loopback bind is always allowed, and a non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set.

Replacement:

> `trusted-headers` reads identity from unverified headers, so it constrains the bind. On a single-tenant registry a loopback bind is always allowed, and a non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set. On a multi-tenant registry the proxy secret is required on every bind, loopback included, and an unset secret fails to start with `config.trusted_headers_multitenant_no_secret` (§6.3.3).

Current text, Identity provider row, Standalone cell, last clause (after the semicolon):

> `trusted-headers` on a non-loopback bind fails to start with `config.trusted_headers_public_bind` unless a proxy secret or `--allow-public-bind` is set.

Replacement:

> on a single-tenant registry, `trusted-headers` on a non-loopback bind fails to start with `config.trusted_headers_public_bind` unless a proxy secret or `--allow-public-bind` is set, and on a multi-tenant registry it requires `PODIUM_TRUSTED_PROXY_SECRET` on every bind, loopback included, and otherwise fails to start with `config.trusted_headers_multitenant_no_secret`.

The cell keeps the text before the semicolon, and the trailing padding before the closing `|` is unchanged.

## Spec amendment: §13.10 Standalone Deployment, upgrade path

**SPEC-7.** `spec/13-deployment.md`, §13.10, the paragraph that begins "**Upgrade path.**" (line 172). Append two sentences at the end of the paragraph.

**Rationale.** `podium admin migrate-to-standard` migrates the manifests, layer configuration, and admin grants of the tenants it finds under the `default` org's derived ID and the literal `default` (`cmd/podium/admin_migrate.go:299-313`). The object pump copies every blob in the source object store with no tenant filter (`cmd/podium/admin_migrate.go:393-401`), and the audit step copies the single audit file the server writes for every tenant (`cmd/podium/admin_migrate.go:205-211`; `internal/serverboot/serverboot.go:982`). Once SPEC-1 states that a standalone registry runs multi-tenant, the paragraph's "Layer config, admin grants, and audit history are preserved" reads as covering every tenant's metadata, which it does not, and a sentence that limited the whole migration to the `default` org would misstate the blobs and the audit log (D-12). The appended sentences scope the limit to the metadata the code restricts.

Current text:

> **Upgrade path.** A standalone deployment migrates to standard via `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (covered in §13.4). Layer config, admin grants, and audit history are preserved; embeddings are re-computed against the target vector backend on first ingest.

Replacement:

> **Upgrade path.** A standalone deployment migrates to standard via `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (covered in §13.4). Layer config, admin grants, and audit history are preserved; embeddings are re-computed against the target vector backend on first ingest. The migration carries the manifests, layer configuration, and admin grants of the `default` org alone. On a standalone registry started with `PODIUM_MULTI_TENANT`, a tenant provisioned through §7.3.3 keeps its manifests, layer configuration, and admin grants in the standalone store, while the object-store blobs, and the audit log when the migration copies it, are copied whole, including those of every tenant.

## Spec amendment: §13.11.3 What's Available, remote-server list

**SPEC-3.** `spec/13-deployment.md`, §13.11.3. Delete the bullet `- Multi-tenancy and SCIM.` (line 353) under "Features that require **specifically a remote server** (not just any server):", so the list keeps `- Centralized audit independent of clones.` alone. Replace the paragraph after the list (line 355). The filesystem list above it (lines 341-348) is unchanged (D-6).

**Rationale.** Any registry server runs multi-tenancy and SCIM, a standalone one included (`internal/serverboot/serverboot.go:71-86`, `:1452-1459`; `test/e2e/auth_scim_visibility_test.go:55-63`). The paragraph after the list already states the server-but-not-remote case for identity-based visibility.

Current text:

> Identity-based visibility filtering requires a server but not specifically a remote one: a standalone server fronted by a gateway provides it through `oidc-jwt` or `trusted-headers` (§6.3.3), and a remote standard deployment runs its own OIDC IdP.

Replacement:

> Identity-based visibility filtering, multi-tenancy, and SCIM require a server but not specifically a remote one. A standalone server fronted by a gateway provides identity-based visibility through `oidc-jwt` or `trusted-headers` (§6.3.3), and a remote standard deployment runs its own OIDC IdP. A standalone server runs multi-tenancy and SCIM under the conditions §13.10 states, and SCIM also needs a server the IdP can reach.

## Spec amendment: §6.3.1 and §6.3.3, single-tenant registry

**SPEC-4.** `spec/06-mcp-server.md`. Two sentences change. The rest of each paragraph is unchanged.

**Rationale.** Both sentences say a standalone backend is single-tenant whatever its configuration. The code keys on `PODIUM_MULTI_TENANT` with no store check (`internal/serverboot/serverboot.go:2316`, `:1452-1466`), and §4.7.1 and §13.12 already key on the mode.

§6.3.1, the paragraph "**Per-request tenant selection.**" (line 81), last sentence.

Current text:

> A single-tenant registry, whether a standalone backend (§13.10) or a standard backend started without `PODIUM_MULTI_TENANT`, resolves every authenticated request against its sole tenant and does not consult the organization value; provisioning additional tenants on such a registry does not enable per-request routing.

Replacement:

> A single-tenant registry, which is a standalone (§13.10) or standard (§13.1) registry started without `PODIUM_MULTI_TENANT`, resolves every authenticated request against its sole tenant and does not consult the organization value; provisioning additional tenants on such a registry does not enable per-request routing.

§6.3.3, the second paragraph of the section (line 115), third sentence.

Current text:

> On a single-tenant registry, whether a standalone backend (§13.10) or a standard backend started without `PODIUM_MULTI_TENANT`, the registry resolves every authenticated caller to its sole tenant and does not consult the organization value; provisioning additional tenants on such a registry does not enable per-request routing.

Replacement:

> On a single-tenant registry, which is a standalone (§13.10) or standard (§13.1) registry started without `PODIUM_MULTI_TENANT`, the registry resolves every authenticated caller to its sole tenant and does not consult the organization value; provisioning additional tenants on such a registry does not enable per-request routing.

## Spec amendment: §6.10 Error Model, registry.tenant_management_unavailable

**SPEC-5.** `spec/06-mcp-server.md`, §6.10. Two edits.

**Rationale.** The tenant-admin gate rejects only when no tenant router is installed (`pkg/registry/server/tenants.go:104-108`). A standalone SQLite server with `PODIUM_MULTI_TENANT` and a verifier serves `/v1/admin/tenants` (`pkg/store/sqlite.go:204-238`). The "single-tenant" condition is accurate. The "standalone backend, where multi-tenancy is out of scope" condition and the "on a standard backend" hint are not.

The tenant-management paragraph (line 523), last sentence. Replace the clause before the semicolon; the rest of the sentence ("it joins the existing `registry.*` namespace and introduces no new namespace:") is unchanged.

Current text:

> `registry.tenant_management_unavailable` reports a `/v1/admin/tenants` request on a single-tenant or standalone backend (§13.10), where multi-tenancy is out of scope; it joins the existing `registry.*` namespace and introduces no new namespace:

Replacement:

> `registry.tenant_management_unavailable` reports a `/v1/admin/tenants` request on a single-tenant registry (§6.3.1), one started without `PODIUM_MULTI_TENANT`; it joins the existing `registry.*` namespace and introduces no new namespace:

The JSON envelope that follows (line 530). Replace the `suggested_action` line alone.

Current text:

> `"suggested_action": "Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) on a standard backend to manage tenants."`

Replacement:

> `"suggested_action": "Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) to manage tenants."`

The code, `message`, `retryable` value, and matrix cell are unchanged. CODE-1 changes the mirror in the same commit.

## Spec amendment: §7.3.3 Tenant Management

**SPEC-6.** `spec/07-external-integration.md`, §7.3.3. Two edits.

**Rationale.** The availability paragraph limits the endpoints to a "multi-tenant standard backend" and says a standalone backend rejects them "because multi-tenancy is out of scope for standalone". The code checks only for a tenant router (`pkg/registry/server/tenants.go:104-108`), and SQLite implements `CreateTenant` and `ListTenants`. Once the endpoints are stated to run on SQLite, the GET bullet's unscoped forced-RLS and sentinel sentence would describe SQLite falsely, because `SQLite.ListTenants` is a plain `SELECT` (`pkg/store/sqlite.go:235-238`) and the sentinel is Postgres-only (`pkg/store/postgres.go:219-247`).

The availability paragraph (line 217). Replace the whole paragraph.

Current text:

> The tenant-management endpoints are available only on a multi-tenant standard backend (§13.1). A single-tenant backend, whether a standalone backend (§13.10) with its sole synthetic `default` org or a standard backend started without `PODIUM_MULTI_TENANT`, rejects every `/v1/admin/tenants` request with `registry.tenant_management_unavailable` (§6.10), because multi-tenancy is out of scope for standalone (§13.10) and the standalone routing does not consult tenants created through the API.

Replacement:

> The tenant-management endpoints are available only on a multi-tenant registry (§6.3.1, started with `PODIUM_MULTI_TENANT`), on a standalone (§13.10) or a standard (§13.1) backend. A single-tenant registry, with its sole `default` org, rejects every `/v1/admin/tenants` request with `registry.tenant_management_unavailable` (§6.10), because its routing binds every request to that org and does not consult tenants created through the API.

The `GET /v1/admin/tenants` bullet (line 211), second sentence. Prefix it with "On the Postgres backend, " and lower-case the following "The". The first sentence and the sentences after it stay as written.

Current text, second sentence opening:

> The tenants registry is under `FORCE ROW LEVEL SECURITY`, so the registry re-scopes the policy through an operator sentinel rather than by setting a per-org `org_id`: ...

Replacement opening:

> On the Postgres backend, the tenants registry is under `FORCE ROW LEVEL SECURITY`, so the registry re-scopes the policy through an operator sentinel rather than by setting a per-org `org_id`: ...

The remainder of the sentence is unchanged.

## Proposed solution

### CODE-1. `pkg/registry/server/error_envelope.go` and its unit test

Replace the entry at `pkg/registry/server/error_envelope.go:119-124` with:

```go
	// §7.3.3: tenant management is a multi-tenant-only capability. A
	// single-tenant registry has no additional tenant to manage, so the
	// operation never succeeds outside multi-tenant mode.
	"registry.tenant_management_unavailable": {
		suggestedAction: "Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) to manage tenants.",
	},
```

The comment leaves out "on either backend", which adds nothing once standalone is dropped. It leaves out a claim that a restart with `PODIUM_MULTI_TENANT` is enough, because a restart with the variable alone does not install the router when no verifier is present (`internal/serverboot/serverboot.go:1452`), and that case is a non-goal. It replaces "deployment mode", which in this project names local, single-node, and clustered.

Extend `TestTenants_SingleTenantUnavailable` in `pkg/registry/server/tenants_test.go`. In the loop over the four requests, decode `suggested_action` from the response body and require it to equal `Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) to manage tenants.` exactly. Add `Spec: §6.10` to the test's comment beside the existing `Spec: §7.3.3 / §4.7.1`. The 404 status and code assertions stay.

Run `gofmt -l pkg/registry/server` (prints nothing) and `go test ./pkg/registry/server/`.

### TEST-1. Standalone multi-tenant registry end-to-end

Add `TestGateway_StandaloneMultiTenantManagesTenants` to `test/e2e/auth_gateway_test.go`, beside `TestGateway_TrustedHeadersMultitenantNoSecretRefused`. Annotate it with `// Spec: §13.10`, `// Spec: §7.3.3`, `// Spec: §6.3.1`, and `// Spec: §7.3.1`. Boot it through `gwTrustedHeadersRegistry` with `PODIUM_TRUSTED_PROXY_SECRET=<secret>`, `PODIUM_MULTI_TENANT=true`, and `PODIUM_OPERATOR_ADMINS=alice@acme.com`. Do not call `msSkipIfNoStack`.

Every caller sends `X-Podium-User-Sub`, `X-Podium-User-Org`, and `X-Podium-Proxy-Secret`.

1. **Positive routing control.** A caller with sub `bob@acme.com` and org `default` reads `gwLayerIDs`, and the result contains `public-layer`. This proves a provisioned org routes to its tenant on the SQLite store.
2. **Create.** As alice (sub `alice@acme.com`, org `default`), `POST /v1/admin/tenants` with `{"name":"globex"}` returns 201 with a non-empty `id`. Use `apiDoAs` and `apiWantStatus`.
3. **List.** As alice, `GET /v1/admin/tenants` returns 200, and its `tenants` list names both `default` and `globex`.
4. **Routing to the API-created tenant.** As dave (sub `dave@acme.com`, org `globex`), register the user-defined layer `gw-mt-dave` with `evRegisterUserLayer` (`test/e2e/events_visibility_test.go:270`), which requires 201 and a stored user-defined layer. Require the `webhook_url` it returns to contain `/v1/ingest/webhook/` + `core.OrgIDForName("globex")` + `/gw-mt-dave`, the tenant-qualified URL §7.3.1 states for a multi-tenant registry (`pkg/registry/server/layers.go:196-200`). Then `gwLayerIDs` as dave in org `globex` lists `gw-mt-dave` and not `public-layer`, and `gwLayerIDs` as dave in org `default` lists `public-layer` and not `gw-mt-dave`. Only a request routed to the `globex` tenant passes this step: an unrouted registration is refused with 403 (`pkg/registry/server/layers.go:250-259`, `:1353`), and an unrouted list is empty (`:1632-1635`). The absence check lists as dave rather than as another subject, because the list narrows a non-admin caller to the layers that caller can see (`pkg/registry/server/layers.go:322-342`) and a user-defined layer is visible to its owner alone (`:1511`), so a different subject would not see `gw-mt-dave` even if it had been stored in the default tenant.
5. **Unprovisioned org.** A caller with org `initech`, which names no tenant, reads `gwLayerIDs` and gets no layers with 200, because this is the `trusted-headers` unrouted path of §6.3.1. The same caller's `POST /v1/layers` with `ltrGitLayer("gw-mt-erin")` is refused with `403 auth.forbidden`, asserted with `apiWantCodeAs`, which is the §7.3.1 Tenant selection refusal of a write that resolves to no tenant.

Reuse `apiDoAs`, `apiWantStatus`, `apiWantCodeAs`, `evRegisterUserLayer`, `ltrGitLayer`, `gwLayerIDs`, and `gwListsLayer`, and add no new helper. The file gains the `github.com/lennylabs/podium/pkg/registry/core` import for `core.OrgIDForName`. The `http.Header` helpers and `gwLayerIDs`, which takes a `map[string]string`, carry the same three headers for a given caller.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| `PODIUM_MULTI_TENANT=true` on a standalone server under `oidc-jwt`, `trusted-headers`, or `injected-session-token` | The tenant router is installed, each request routes by organization, and `/v1/admin/tenants` serves operators | §13.10 "Multi-tenant mode runs when `PODIUM_MULTI_TENANT` is `true` (§13.12), and it routes requests to tenants only on a registry with a request-time identity verifier" (SPEC-1); `docs/deployment/single-node.md` (DOC-4), `docs/deployment/clustered.md` (DOC-3) |
| `PODIUM_MULTI_TENANT=true` with no identity provider, or in public mode | No tenant router is installed. Every layer request is unrouted and sees an empty view, and `/v1/admin/tenants` answers `404 registry.tenant_management_unavailable`. Deferred (non-goal, Q-2) | §13.10 "routes requests to tenants only on a registry with a request-time identity verifier" (SPEC-1); §4.7.8 "every request on a registry with no request-time identity verifier"; `docs/deployment/single-node.md` "Without such a provider, or in public mode, a registry started with `PODIUM_MULTI_TENANT` binds every request to a tenant that holds no data" (DOC-4) and `docs/reference/error-codes.md` "or one started with it but with no identity provider the registry verifies, or in public mode" (DOC-7) state the outcome, and `docs/deployment/clustered.md` (DOC-3) names the verifier precondition |
| Multi-tenant `trusted-headers` with no `PODIUM_TRUSTED_PROXY_SECRET`, loopback bind, standalone | Startup fails with `config.trusted_headers_multitenant_no_secret` | §13.10 "On a multi-tenant registry the proxy secret is required on every bind, loopback included" (SPEC-2); §6.3.3; `docs/deployment/gateway-delegated-identity.md`, `docs/reference/error-codes.md` |
| Single-tenant `trusted-headers`, non-loopback bind, no secret and no `--allow-public-bind` | Startup fails with `config.trusted_headers_public_bind` | §13.10 "On a single-tenant registry a loopback bind is always allowed, and a non-loopback bind fails to start" (SPEC-2); `docs/deployment/gateway-delegated-identity.md` |
| Tenant isolation on a multi-tenant SQLite store | Rows are scoped by a `tenant_id` column. The store has no per-org schema and no row-level security. Accepted (D-3) | §13.10 "The standalone SQLite store scopes each row by its tenant, and the schema-per-org isolation and forced row-level security of §4.7.1 are properties of the Postgres backend" (SPEC-1); `docs/deployment/single-node.md` (DOC-4), `docs/deployment/clustered.md` (DOC-3) |
| `GET /v1/admin/tenants` on a multi-tenant SQLite store | Returns every tenant through a plain read. No sentinel is involved | §7.3.3 "On the Postgres backend, the tenants registry is under `FORCE ROW LEVEL SECURITY`" (SPEC-6); `docs/reference/http-api.md` (DOC-7) |
| Caller org names no provisioned tenant, `trusted-headers`, multi-tenant | 200 with an empty layer list (public visibility only), and a layer write is refused with `403 auth.forbidden` | §6.3.1 "under `trusted-headers`, the request is treated as anonymous and sees public visibility only"; §7.3.1 Tenant selection "`list` returns it no layers, and a write operation is refused with `403 auth.forbidden`"; `docs/deployment/gateway-delegated-identity.md` |
| Caller org names no provisioned tenant, `oidc-jwt`, multi-tenant | Rejected with `auth.tenant_unknown` | §6.3.1 "a value that resolves to no provisioned tenant is rejected with `auth.tenant_unknown`"; `docs/reference/error-codes.md` |
| `/v1/admin/tenants` on a single-tenant registry, either backend | `404 registry.tenant_management_unavailable`, before operator authorization, with the `suggested_action` "Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) to manage tenants." | §6.10 (SPEC-5), §7.3.3 (SPEC-6); `docs/reference/http-api.md`, `docs/reference/cli.md`, `docs/reference/error-codes.md` (DOC-7) |
| `podium admin migrate-to-standard` from a multi-tenant standalone store | Only the `default` org's manifests, layer configuration, and admin grants migrate. Other tenants' manifests, layer configuration, and admin grants stay in the SQLite store, while every tenant's object-store blobs, and the audit log when `--target-audit-log` names a target, reach the target. Accepted (D-12, Q-3) | §13.10 "The migration carries the manifests, layer configuration, and admin grants of the `default` org alone" (SPEC-7); `docs/deployment/single-node.md` "Migrating to clustered" (DOC-9), `docs/deployment/progressive-adoption.md` (DOC-5) |
| SCIM on a standalone server the IdP cannot reach | The receiver mounts on the token alone and receives no push from that IdP | §13.10 "it receives pushes only from an IdP that can reach the server" (SPEC-1); §13.11.3 (SPEC-3); `docs/deployment/integrations.md` (DOC-6) |
| SCIM under `trusted-headers` | The receiver accepts pushes, and no visibility read consults the directory | §6.3.1 "Where the directory expansion applies"; §13.12 `PODIUM_SCIM_TOKENS`; `docs/deployment/access-control.md` (DOC-6), `docs/deployment/gateway-delegated-identity.md` |
| A freeze window on a standalone server | Reingest inside the window is rejected with `ingest.frozen` unless broken with the break-glass flags | §13.10 "Freeze windows apply when `registry.yaml` lists `freeze_windows` (§4.7.2)" (SPEC-1); `docs/deployment/clustered.md:34` (unchanged, D-11) |
| Registering a webhook receiver on a standalone server with no identity provider | Rejected with `auth.forbidden`, because no caller is authenticated as a tenant admin | §13.10 "registering one requires an identity provider" (SPEC-1); §7.3.2 Receiver authorization; `docs/deployment/extending.md` |
| Outbound webhooks on a filesystem source | Unavailable | §13.11.3 filesystem list (unchanged, D-6); `docs/deployment/local.md` |

## Testing

**New and extended tests.**

- **TEST-1, `TestGateway_StandaloneMultiTenantManagesTenants`** (e2e, `// Spec: §13.10`, `// Spec: §7.3.3`, `// Spec: §6.3.1`, `// Spec: §7.3.1`). It pins the new §13.10 and §7.3.3 statements at the binary level on SQLite. An unrouted layer list and a list routed to a tenant with no layers both answer 200 with an empty list, so an empty list alone cannot show that a tenant created through `POST /v1/admin/tenants` routes. Step 4 is the control that does: its registration succeeds, advertises the `globex` tenant ID, and lists back in `globex` alone only when the request routes to that tenant, and it fails when the SQLite store stores the tenant inactive or under an ID the resolver cannot find (`internal/serverboot/orgid.go:33-38`). Step 4 also checks that the `globex` tenant sees none of the default tenant's layers and that the default tenant does not hold the `globex` layer. Step 1 shows that the boot-provisioned `default` tenant routes, which step 4's default-org list relies on. Step 5 checks the §6.3.1 unrouted path for an unprovisioned org under `trusted-headers`, and the §7.3.1 refusal of its write.
- **CODE-1, `TestTenants_SingleTenantUnavailable`** (unit, `// Spec: §7.3.3 / §4.7.1`, gains `Spec: §6.10`). The extension pins the new `suggested_action` on the single-tenant refusal for all four methods, which is the spec-named failure path for tenant management.

**Existing tests that pin restated claims.** Confirm each passes unchanged.

- §13.10 SPEC-2 multi-tenant refusal: `TestGateway_TrustedHeadersMultitenantNoSecretRefused` and `TestGateway_TrustedHeadersNonLoopbackBindRefused` in `test/e2e/auth_gateway_test.go`.
- SCIM on standalone: `test/e2e/auth_scim_visibility_test.go`.
- Freeze windows on standalone: the freeze-window case in `test/e2e/governance_adoption_test.go`.
- §7.3.2 receivers on standalone: `test/e2e/notification_sink_primitive_test.go`.
- §7.3.3 lifecycle: `TestTenants_CRUDRoundTrip` in `pkg/registry/server/tenants_test.go`, and `cmd/podium/admin_tenant_test.go`.

SPEC-7 states pre-existing migration behavior and is not pinned by a new test. A test that asserts a non-default tenant is left behind would codify a limitation Q-3 may lift. If the reviewer answers Q-3 by keeping the limit, add a `cmd/podium/admin_migrate_test.go` case with `// Spec: §13.10` in a follow-up.

**Commands per step.**

- S1: `gofmt -l pkg/registry/server` prints nothing, then `go build ./...`, `go test ./pkg/registry/server/ ./cmd/podium/`, and `make speccov-drift matrix-audit`, and confirm neither reports a change.
- S2: `go vet ./test/e2e/...`, then `go test ./test/e2e/ -run 'TestGateway_' -v`, and confirm `TestGateway_StandaloneMultiTenantManagesTenants` reports `PASS` with no `SKIP`.
- S4: `cd site && npm test && npm run check`.
- S4 through S11: `make docs-check`.
- Closing step: `make lint` and `make coverage-gate`.

**Verification for the whole change.** After S11, run:

```bash
grep -rn -e 'out of scope for standalone' -e 'single-tenant or standalone' -e 'on a standard backend' \
  -e 'multi-tenant standard backend' -e 'available on the \[clustered\]' -e 'covers a single-node deployment' \
  README.md docs spec site/src pkg internal cmd
```

The expected result is no output.

## Manual validation

**MV-1.** Append scenario S90 to `test/manual-validation.md`, after S89, in the document's existing conventions. Ports 8191 and 8192 are unused by other scenarios.

````markdown
## S90: A standalone registry manages and routes tenants in multi-tenant mode

**Goal.** Validate that a standalone registry on the SQLite store, started with
`PODIUM_MULTI_TENANT` behind `trusted-headers`, provisions and lists tenants
through `/v1/admin/tenants`, routes each caller to the tenant its organization
names, including a tenant created through the API, refuses a layer write from
an organization that names no tenant, and that a single-tenant standalone
registry refuses tenant management with a suggested action that names no
backend.

**Covers.** §13.10 Features enabled by configuration, §6.3.1 per-request tenant
selection, §7.3.1 Tenant selection, §7.3.3 Tenant Management, and the §6.10
`registry.tenant_management_unavailable` envelope, through the compiled binary.

**Why by hand.** An operator reads the raw tenant list and the layer lists of
one caller in different tenants side by side. A `registry.tenant_management_unavailable`
answer from the multi-tenant server, a `globex-<pid>` registration refused
with `auth.forbidden`, which means the registry did not route to the tenant the
API created, a `globex-<pid>` list that names `public-layer`, or a suggested
action that says "on a standard backend" is the defect this scenario catches.

**Steps.**

1. Run the isolation block. Write a public layer, then serve a multi-tenant
   standalone registry behind `trusted-headers`.

   ```bash
   S=$$
   podium artifact scaffold --type context --description "Public welcome" "$WORK/pub/welcome"
   cat > "$WORK/registry.yaml" <<EOF
   registry:
     layers:
       - id: public-layer
         source:
           local:
             path: $WORK/pub
         visibility:
           public: true
   EOF
   export PODIUM_CONFIG_FILE="$WORK/registry.yaml"
   export PODIUM_IDENTITY_PROVIDER=trusted-headers
   export PODIUM_TRUSTED_PROXY_SECRET=gateway-secret
   export PODIUM_OPERATOR_ADMINS=olivia@acme.com
   PODIUM_MULTI_TENANT=true podium serve --standalone --no-embeddings --bind 127.0.0.1:8191 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8191/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8191
   as() { curl -s -H "X-Podium-Proxy-Secret: gateway-secret" -H "X-Podium-User-Sub: $1" -H "X-Podium-User-Org: $2" "${@:3}"; }
   ```

   **Expect.** `server_alive` reports the server running, and the log carries
   `multi-tenant mode: routing requests by organization`.

2. As bob (org `default`), list layers.

   ```bash
   as bob@acme.com default "$URL/v1/layers"
   ```

   **Expect.** The list names `public-layer`.

3. As olivia, the operator, create a per-run tenant and list tenants.

   ```bash
   as olivia@acme.com default -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
     -d "{\"name\":\"globex-$S\"}" "$URL/v1/admin/tenants" | tee "$WORK/tenant.json"
   GLOBEX=$(python3 -c "import json; print(json.loads(open('$WORK/tenant.json').read().rsplit('HTTP',1)[0])['id'])")
   echo "globex tenant id: $GLOBEX"
   as olivia@acme.com default "$URL/v1/admin/tenants" \
     | python3 -c "import json,sys; print(sorted(t['name'] for t in json.load(sys.stdin)['tenants']))"
   ```

   **Expect.** The create answers `HTTP 201`, and `$GLOBEX` is a UUID rather
   than the string `globex-<pid>`. The list prints both `default` and
   `globex-<pid>`. A `registry.tenant_management_unavailable` answer is the
   defect.

4. As dave (org `globex-<pid>`), register a user layer, then list layers as
   dave in `globex-<pid>` and as dave in `default`.

   ```bash
   as dave@acme.com "globex-$S" -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"dave-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/dave/notes.git\",\"ref\":\"main\"}" "$URL/v1/layers"
   echo "dave in globex sees:  $(as dave@acme.com "globex-$S" "$URL/v1/layers" | python3 -c "import json,sys; print([l['id'] for l in json.load(sys.stdin)['layers']])")"
   echo "dave in default sees: $(as dave@acme.com default "$URL/v1/layers" | python3 -c "import json,sys; print([l['id'] for l in json.load(sys.stdin)['layers']])")"
   ```

   **Expect.** The registration answers `HTTP 201`, and its `webhook_url`
   contains `/v1/ingest/webhook/$GLOBEX/dave-<pid>`. The `globex` list
   contains `dave-<pid>` and not `public-layer`. The `default` list contains
   `public-layer` and not `dave-<pid>`. `HTTP 403` with `auth.forbidden` on the
   registration means the registry did not route to the tenant the API created.
   `public-layer` in the `globex` list means the default tenant's layers leaked
   across tenants. Both lists are read as dave because a user layer is visible
   to its owner alone, so another caller's list would omit `dave-<pid>` in
   either tenant.

5. As erin (org `initech-<pid>`, which names no tenant), list layers and
   register one.

   ```bash
   as erin@acme.com "initech-$S" -w "\nHTTP %{http_code}\n" "$URL/v1/layers"
   as erin@acme.com "initech-$S" -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"erin-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/erin/x.git\",\"ref\":\"main\"}" "$URL/v1/layers"
   ```

   **Expect.** The list prints `{"layers":[]}` and answers `HTTP 200`. The
   registration answers `HTTP 403` with error code `auth.forbidden`.

6. Stop the server and serve the same configuration single-tenant, then
   request tenant management.

   ```bash
   kill "$SRV"; wait "$SRV" 2>/dev/null
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8192 > "$WORK/srv1.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8192/healthz
   server_alive "$SRV" "$WORK/srv1.log"
   as olivia@acme.com default -w "\nHTTP %{http_code}\n" "http://127.0.0.1:8192/v1/admin/tenants"
   ```

   **Expect.** `HTTP 404` with code `registry.tenant_management_unavailable`
   and `suggested_action` exactly `Start the registry in multi-tenant mode
   (PODIUM_MULTI_TENANT) to manage tenants.` The words `standard backend` in
   the response are the defect.

**Cleanup.** Stop the server with `kill "$SRV"` and `rm -rf "$WORK"`.

---
````

## Documentation changes

### DOC-1. Tier tables and the landing page

The tables list multi-tenancy and SCIM group sync as what the clustered tier adds over single node, and both run on a single node. The tier adds Postgres schema-per-org isolation with row-level security, and replicas for high availability (D-3).

- `README.md:139` and `docs/deployment/index.md:15`, last cell: "Everything in single node, plus multi-tenancy, SCIM group sync, and high availability" becomes "Everything in single node, plus Postgres schema-per-org tenant isolation and high availability across registry replicas".
- `docs/overview.md:72`, last cell: "Multi-tenancy, SCIM group sync, and high availability" becomes "Postgres schema-per-org tenant isolation and high availability across registry replicas".
- `site/src/content/landing.ts:388-392`: the clustered `plus` array becomes `["Postgres tenant isolation", "High availability"]`, keeping the file's one-element-per-line layout. No site test pins the entries.

### DOC-2. Getting-started tier summaries: drop multi-tenancy from the clustered stack

The intro of `docs/getting-started/how-it-works.md` lists multi-tenancy as part of the clustered stack, and "What it adds" names multi-tenancy while the same bullet says SCIM and freeze windows run on a single node. `docs/getting-started/concepts.md:99-100` carries the same stack sentence.

- `docs/getting-started/how-it-works.md:221-222` becomes "Registry replicas behind a load balancer, with Postgres, pgvector, S3, and OIDC." The sentence after it is unchanged.
- `docs/getting-started/how-it-works.md:230-232`, keeping the bullet's line wrap:

  ```markdown
  - **What it adds.** High availability across registry replicas and Postgres
    schema-per-org tenant isolation. Multi-tenancy, SCIM group push, freeze
    windows, outbound webhooks, the hash-chained audit log, and its local
    chain-head anchoring run on a single node as well.
  ```

- `docs/getting-started/concepts.md:99-100` becomes:

  ```markdown
  - **Clustered**: registry replicas behind a load balancer, with Postgres,
    S3-compatible object storage, and OIDC.
  ```

The "Who it is for" bullet stays (D-11).

### DOC-3. `clustered.md`: description, multi-tenancy bullet, and SCIM bullet

- Line 4, frontmatter `description`: "Adds multi-tenancy, freeze windows, hash-chained audit, and SCIM." becomes "Adds Postgres schema-per-org tenant isolation and high availability across replicas." The first sentence of the description is unchanged.
- Line 31 becomes:

  ```markdown
  - **Multi-tenancy.** Per-tenant layer lists, admin grants, audit streams, and quotas. The tenant boundary is the org. Multi-tenant mode runs on any registry server started with `PODIUM_MULTI_TENANT=true` and an identity provider the registry verifies, which routes each request to the tenant its organization names. On the clustered tier's Postgres backend, each org has its own schema and cross-org tables use row-level security, while a single-node SQLite store scopes each row by its tenant.
  ```

- Line 36 becomes:

  ```markdown
  - **SCIM 2.0.** Group membership push from OIDC IdPs that support it. Layer visibility references group claims directly. A single-node deployment mounts the same receiver when `PODIUM_SCIM_TOKENS` lists a bearer token.
  ```

The headings and every fenced block on the page are unchanged.

### DOC-4. `single-node.md`: "What the tier omits" and "Migrating to clustered"

- Line 155, "Single node omits multi-tenancy. A single-node deployment is single-tenant.", becomes:

  ```markdown
  A single-node deployment is single-tenant by default. Setting `PODIUM_MULTI_TENANT=true` with an identity provider the registry verifies turns on multi-tenant routing on a single node, and the SQLite store scopes each row by its tenant. Without such a provider, or in public mode, a registry started with `PODIUM_MULTI_TENANT` binds every request to a tenant that holds no data, so every caller sees an empty catalog and `/v1/admin/tenants` answers `registry.tenant_management_unavailable`. A single node omits the Postgres schema-per-org isolation and row-level security the clustered tier provides for multi-tenant deployments, and it runs no replicas.
  ```

- The signing and anchoring paragraph (line 157) stays.
- Line 159 becomes "When a multi-tenant deployment needs Postgres-backed isolation, or the registry needs replicas, see [Clustered](clustered)."
- Line 196: "typically because multi-tenancy, OIDC group claims through SCIM, or production-grade availability is required" becomes "typically because production-grade availability across replicas, Postgres-backed tenant isolation, or scale beyond tens of developers is required". The rest of the sentence and the fenced command stay.

The heading "What the tier omits" has no inbound links and stays.

### DOC-9. `single-node.md`: the migration carries the default org's metadata alone

Under `## Migrating to clustered`, insert one paragraph immediately after the fenced `podium admin migrate-to-standard` block:

```markdown
The migration carries the artifact manifests, layer configuration, and admin grants of the `default` org alone. On a single node started with `PODIUM_MULTI_TENANT`, a tenant provisioned through `podium admin tenant` keeps its artifact manifests, layer configuration, and admin grants in the single-node store. The migration copies the artifact content blobs of every tenant, and with `--target-audit-log` it copies the whole audit log, which holds every tenant's events. A deployment that serves several tenants starts on the clustered tier (see [Progressive adoption](progressive-adoption)).
```

No fenced block changes, so the `tools/doccov` classification is unaffected.

### DOC-5. `local.md` and `progressive-adoption.md`: reasons to move to clustered

- `docs/deployment/local.md:162` becomes:

  ```markdown
  **To clustered.** When production availability or Postgres-backed tenant isolation is required, follow [Clustered](clustered) and use `podium admin migrate-to-standard` to export the single-node state.
  ```

- `docs/deployment/progressive-adoption.md:120` becomes:

  ```markdown
  - **Multi-tenant from the start.** A deployment that serves separate customer organizations runs in multi-tenant mode (`PODIUM_MULTI_TENANT`) from the start, with an identity provider that authenticates each caller. Start it on the [clustered](clustered) tier with a per-tenant layer plan and skip the single-node phase. The clustered tier's Postgres backend isolates each org in its own schema.
  ```

DOC-5 is correct only after SPEC-1 and SPEC-3 land, which S1 guarantees.

### DOC-6. SCIM availability lines

- `docs/deployment/integrations.md:94`: replace ", and it is available on the [clustered](clustered) tier." with ", and the registry mounts its receiver at `/scim/v2/` on a single-node or clustered deployment when `PODIUM_SCIM_TOKENS` lists a bearer token." The following sentences are unchanged.
- `docs/deployment/access-control.md:126` becomes:

  ```markdown
  - **SCIM 2.0 push.** The IdP pushes membership to the registry. The registry mounts the SCIM receiver on a single-node or clustered deployment when `PODIUM_SCIM_TOKENS` lists a bearer token, and it expands `groups:` filters against the pushed directory under `oidc-jwt` and `injected-session-token` but not under `trusted-headers` (see [Gateway-delegated identity](gateway-delegated-identity)). SCIM is recommended once group-based visibility gates artifacts that matter.
  ```

Do not link [OIDC identity](oidc/) as the source of the provider list, and do not state that the IdP can reach the registry as the mount precondition. The receiver mounts on the token alone.

### DOC-7. Reference pages: single-tenant wording for tenant management

- `docs/reference/http-api.md:691`: "A request on a single-tenant or standalone registry is rejected with `404 registry.tenant_management_unavailable`, before operator authorization." becomes "A request on a single-tenant registry, one started without `PODIUM_MULTI_TENANT`, is rejected with `404 registry.tenant_management_unavailable`, before operator authorization."
- `docs/reference/cli.md:564`: "a single-tenant or standalone registry rejects them with `registry.tenant_management_unavailable`" becomes "a single-tenant registry rejects them with `registry.tenant_management_unavailable`".
- `docs/reference/error-codes.md:161`, description cell, becomes "A `/v1/admin/tenants` request (or a `podium admin tenant` command) reached a registry that serves no tenant management: a single-tenant registry, one started without `PODIUM_MULTI_TENANT`, or one started with it but with no identity provider the registry verifies, or in public mode. Start the registry in multi-tenant mode with `PODIUM_MULTI_TENANT` and an identity provider the registry verifies to manage tenants."

The row names the no-verifier and public-mode causes because the tenant-admin gate rejects whenever no tenant router is installed (`pkg/registry/server/tenants.go:105-107`), and the router is installed only when `PODIUM_MULTI_TENANT` and a request-time identity verifier are both present (`internal/serverboot/serverboot.go:1452`). Public mode installs no verifier (`internal/serverboot/serverboot.go:1357-1360`). The row describes the current behavior for operators, and the spec text for that case stays with Q-2.

### DOC-10. `gateway-delegated-identity.md`: single-tenant definition

The "Single-tenant and multi-tenant" section mirrors §6.3.3, and its first sentence keys single-tenancy on the deployment tier and the org count. A single node started with `PODIUM_MULTI_TENANT` and a verifier routes by organization (`internal/serverboot/serverboot.go:1452-1459`), and a clustered registry with one org and `PODIUM_MULTI_TENANT=true` binds the no-data tenant (`internal/serverboot/serverboot.go:1103-1110`), so §4.7.1 keys the mode on `PODIUM_MULTI_TENANT` and never on the count of provisioned tenants (`spec/04-artifact-model.md:794`).

- `docs/deployment/gateway-delegated-identity.md:100`: "On a single-tenant registry, which covers a single-node deployment and a clustered one with a single org, the registry resolves every authenticated caller to its sole tenant and does not consult the organization value." becomes "On a single-tenant registry, which is a single-node or clustered registry started without `PODIUM_MULTI_TENANT`, the registry resolves every authenticated caller to its sole tenant and does not consult the organization value."

The rest of the section, including the multi-tenant paragraph at line 102, is unchanged. No fenced block changes.

### DOC-8. CHANGELOG

Append to the existing `### Documentation` group under `## [Unreleased]`:

```markdown
- **Multi-tenancy, freeze windows, SCIM, and outbound webhooks are documented as running on every registry server** (§13.10, §13.11.3, §6.3.1, §6.3.3, §6.10, §7.3.3): a standalone registry runs each of them when its configuration is present. Multi-tenant mode needs `PODIUM_MULTI_TENANT` and an identity provider that authenticates each caller, and the SCIM receiver mounts when `PODIUM_SCIM_TOKENS` lists a token. Schema-per-org isolation and row-level security remain properties of the Postgres backend, and the SQLite store scopes rows by tenant. A single-tenant registry is one started without `PODIUM_MULTI_TENANT` on either backend, and the `registry.tenant_management_unavailable` suggested action no longer says "on a standard backend". `podium admin migrate-to-standard` carries the manifests, layer configuration, and admin grants of the `default` org alone while it copies every tenant's blobs and, with `--target-audit-log`, the whole audit log, and the spec and the single-node page now say so. The README, the deployment pages, and the landing page describe the clustered tier as adding Postgres tenant isolation and high availability.
```

## Open questions

- **Q-1. Scope of the patch.** CODE-1 changes the client-visible §6.10 `suggested_action` string so the spec and its code mirror stay consistent, and TEST-1 adds one end-to-end test that pins standalone multi-tenancy. Should both stay in this 0.5.3 patch, or should the spec keep "on a standard backend" in the envelope and the test be deferred?
- **Q-2. Multi-tenant mode without a verifier.** With `PODIUM_MULTI_TENANT` set and no identity verifier (no provider, or public mode), the router is absent, so `/v1/admin/tenants` returns `registry.tenant_management_unavailable` although §4.7.1 keys the reject on the mode alone. Should that divergence get its own proposal, given that this change restates the `PODIUM_MULTI_TENANT` keying in §6.10 and §7.3.3? Meanwhile DOC-4 and DOC-7 describe the current outcome in the docs, and a decision that changes the outcome updates those sentences.
- **Q-3. Multi-tenant migration.** `podium admin migrate-to-standard` migrates the manifests, layer configuration, and admin grants of the `default` org alone, while it copies every tenant's blobs and the whole audit log, and SPEC-7 and DOC-9 document that. Should a later proposal extend the migration to enumerate every tenant through `ListTenants`, or should the limit stay, with a pinning test added in a follow-up? Resolved at sign-off: multi-tenancy stays supported on a standalone server, and extending the migration to every tenant is follow-up work outside this proposal. SPEC-7 and DOC-9 document the current limit until that work lands.

## Non-goals

- Any runtime behavior change: no gate added or removed for multi-tenancy, SCIM, freeze windows, or webhooks on any backend, and no change to the tenant-admin gate in `pkg/registry/server/tenants.go`.
- Specifying or changing the case where `PODIUM_MULTI_TENANT` is set and no identity verifier is installed (Q-2). This is a separate pre-existing divergence that needs its own investigation. DOC-4 and DOC-7 describe its current outcome to operators and leave the spec text unchanged.
- Adding Postgres-equivalent isolation (row-level security or per-org schemas) to the SQLite store.
- Extending `podium admin migrate-to-standard` to migrate tenants beyond `default` (Q-3).
- Documenting `PODIUM_WEBHOOK_STORE_PATH` or receiver persistence in the spec. The variable is wired (`internal/serverboot/serverboot.go:1218-1231`) but has no §13.12 row, and that gap is separate.
- Changing §8.6 anchoring text, the vulnerability-scanning sentence, or doc-style cleanup of unrelated wording on edited lines ("deployment shape" and the em-dash in §13.10).
- Editing the §9.1 webhook `NotificationProvider` text, which is distinct from §7.3.2 outbound webhooks.
- Editing `site/design/README.md:158`, a design-record table that is not published reader documentation.
- Editing `spec/10-mvp-build-sequence.md:3`, which describes the historical build phasing.
- Changing the `registry.tenant_management_unavailable` code, message, HTTP status, or §6.10 matrix cell.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-08)

- The draft placed SPEC-1 in a commit of its own (S1) and SPEC-5 in a second spec commit (S2), with CODE-1 later. SPEC-1 contradicts six other spec passages until they change, and the mirrored-surfaces rule puts CODE-1 in the SPEC-5 commit. D-9 and the checklist now land every spec edit and CODE-1 in S1.
- `cmd/podium/admin_migrate.go:299-313` migrates the `default` org alone, which the draft did not record. D-12, SPEC-7, DOC-9, Q-3, an edge-case row, and a Watch-out bullet record the accepted limit and stage the sentences that state it.
- No test pinned the §6.10 `suggested_action`. CODE-1 now extends `TestTenants_SingleTenantUnavailable` to assert the new string.
- The change alters an HTTP response and a documented deployment posture, so MV-1 stages scenario S90.
- DOC-8 said the SCIM receiver needs "an IdP that can reach the registry" and that multi-tenant mode needs "an identity provider the registry verifies". DOC-6's review rejects the first as the mount precondition, and DOC-5's review notes that the registry verifies callers through a provider. DOC-8 now says the receiver mounts when `PODIUM_SCIM_TOKENS` lists a token and that multi-tenant mode needs an identity provider that authenticates each caller, and it gains the migration sentence.

### Pass 1 (2026-11-08, automated)

- `docs/deployment/gateway-delegated-identity.md:100` defined a single-tenant registry as "a single-node deployment and a clustered one with a single org", which contradicts SPEC-1, SPEC-4, DOC-3, DOC-4, and the §4.7.1 rule that the mode is keyed on `PODIUM_MULTI_TENANT` and never on the tenant count, and no staged edit or verification pattern covered it. DOC-10 rewrites the sentence to key on the variable, step S11 stages it and the CHANGELOG step becomes S12, D-11 records that line 100 is rewritten, "Where the text disagrees" lists the line, the Summary names DOC-10, and the verification grep gains `'covers a single-node deployment'`.
- The Standalone cell of the §13.10 comparison table's Identity provider row (`spec/13-deployment.md:155`) kept the unqualified `trusted-headers` bind rule that SPEC-2 splits by tenancy in the prose, so §13.10 would state two incompatible startup rules for a multi-tenant `trusted-headers` standalone registry (`pkg/registry/server/config_validate.go:157-169` consults `--allow-public-bind` only in the single-tenant branch). SPEC-2 now also replaces the cell's last clause with the single-tenant and multi-tenant cases, its rationale explains why, the Watch-out list names line 155, and the Summary names the table row.
- SPEC-7, DOC-9, DOC-8, the Watch-out bullet, and D-12 said the migration carries the `default` org alone, but only the metadata pump is limited to that org: the object pump copies every blob with no tenant filter (`cmd/podium/admin_migrate.go:393-401`) and the audit step copies the single process-wide audit file (`cmd/podium/admin_migrate.go:205-211`; `internal/serverboot/serverboot.go:982`). SPEC-7's replacement and rationale, DOC-9, DOC-8, the Watch-out bullet, D-12, the Summary, the Current state migration bullet, the edge-case row, and Q-3 now limit the `default`-only statement to manifests, layer configuration, and admin grants, and state that blobs and the audit log (when `--target-audit-log` names a target) are copied whole, including every tenant's.

### Pass 2 (2026-11-08, automated)

- The edge-case row for `PODIUM_MULTI_TENANT=true` with no identity provider, or in public mode, cited DOC-4 and DOC-3 as covering its outcome, but neither stated it, and DOC-7's `error-codes.md` row gave "started without `PODIUM_MULTI_TENANT`" as the only cause of `registry.tenant_management_unavailable` and the variable alone as the remedy, which is false when the variable is set and no verifier is installed (`internal/serverboot/serverboot.go:1103-1110`, `:1452`; `pkg/registry/server/tenants.go:105-107`). DOC-4's replacement now adds a sentence stating that such a registry binds every request to a tenant that holds no data, shows an empty catalog, and answers `/v1/admin/tenants` with `registry.tenant_management_unavailable`. DOC-7's `error-codes.md` row names the no-verifier and public-mode causes and adds the identity provider to the remedy, with a rationale paragraph citing the gate. The edge-case row quotes both sentences, and the Watch-out bullet, Q-2, and the non-goal record that the docs describe the current outcome while the spec text for the case stays with Q-2. SPEC-1 gains no clause, because a spec sentence fixing the no-verifier outcome would contradict the §4.7.1 keying that Q-2 leaves open.

### Pass 3 (2026-11-08, automated)

- TEST-1 step 4 and MV-1 step 4 expected an empty layer list from a caller in the API-created `globex` tenant, which an unrouted request also returns (`pkg/registry/server/layers.go:1632-1635`), and the Testing section named step 1 as the control, although step 1 routes only the boot-provisioned `default` tenant (`internal/serverboot/orgid.go:65-73`). A SQLite tenant stored inactive or under an ID the resolver cannot find (`internal/serverboot/orgid.go:33-38`) passed both. TEST-1 step 4 now registers `gw-mt-dave` as dave in `globex` with `evRegisterUserLayer`, requires a `webhook_url` carrying `core.OrgIDForName("globex")` (§7.3.1, `pkg/registry/server/layers.go:196-200`), and lists it back as dave in `globex` (present, `public-layer` absent) and in `default` (`public-layer` present, `gw-mt-dave` absent). Step 5 adds the `403 auth.forbidden` refusal of the `initech` caller's `POST /v1/layers` through `apiWantCodeAs` (§7.3.1 Tenant selection). TEST-1 gains `// Spec: §7.3.1` and the reused helpers and import are named. The Testing bullet names step 4 as the routing control. MV-1 step 3 captures `$GLOBEX`, step 4 registers and lists as dave following S87, a new step 5 lists and registers as erin in `initech-<pid>` and expects `HTTP 403` `auth.forbidden`, the former step 5 becomes step 6, and the Goal, Covers, and Why-by-hand paragraphs name the routing defect. The cross-tenant absence check lists as dave in `default` rather than as bob, because the list narrows a non-admin to layers that caller can see and a user layer is visible to its owner alone (`pkg/registry/server/layers.go:322-342`, `:1511`), so bob's list would omit the layer even after a leak. The edge-case row for an unprovisioned org under `trusted-headers` gains the write refusal, and a Watch-out bullet records the empty-list trap.
