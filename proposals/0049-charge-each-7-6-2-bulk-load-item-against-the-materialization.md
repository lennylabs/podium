# Proposal 0049: Charge each §7.6.2 bulk-load item against the materialization rate, and resolve the user-layer cap tenant-first

- Issue: (to be filed)
- Status: Applied to spec (2026-10-03). The proposal was approved on the same date, decided on the user's behalf under the overnight authorization, and signed off as staged. OQ-1: keep the staged behavior (the user-layer cap reads the tenant record on both deployment modes, D10); ignoring it on single-tenant would add plumbing and tests and make GET /v1/quota's stored max_user_layers differ from the enforced value, while the stale-record edge case is documented in SPEC-3(b), DOC-1(d), and CL-1.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.6.2 gains a **Materialization rate** bullet: each bulk-load item counts as one load against the §4.7.8 materialization rate of the request's tenant, items are charged in request order whatever their outcome, and the first refused item and every later item come back as per-item `quota.materialize_rate_exceeded` errors inside the batch's 200 response (SPEC-1). The §4.7.8 charge-site sentence names each bulk-load item with a reference to §7.6.2 and adds no refusal sentence, and the §13.12 `PODIUM_QUOTA_MATERIALIZE_RATE` row scopes its refusal sentence to `load_artifact` and names the per-item report (SPEC-2).
- §4.7.8 states that the per-identity user-layer cap resolves in the same order as the rate budgets: a positive tenant value is enforced, zero selects the deployment default, and a negative value disables the cap. §13.12 gains a `PODIUM_MAX_USER_LAYERS` row in the `### Quotas` table that proposal 0048 adds (SPEC-3).
- `pkg/registry/server` charges every bulk-load item through one shared `Server.allowMaterialize` helper and a pure `admitPrefix` helper, writes the refused tail as per-item §6.10 envelopes, and rewrites the code's `suggested_action` to name the bulk load (CODE-1).
- `pkg/registry/server` `effectiveLayerCap` reads the tenant record first and falls back to the `WithMaxUserLayers` deployment default, then to 3. The comments in `pkg/store`, `internal/serverboot`, and `cmd/podium` that describe the cap follow (CODE-2).
- Tests at the unit, in-process integration, and end-to-end levels, a §6.10 matrix cell for `quota.materialize_rate_exceeded`, the HTTP API, SDK, error-code, CLI, and deployment references, and the `[Unreleased]` changelog entry follow (TEST-1, TEST-2, TEST-3, DOC-1, CL-1).

**Fixed decisions.**

- Charging is per item. Each item, duplicates included, costs one materialization token from the bucket `load_artifact` charges, keyed on `s.core.TenantFor(ctx)` under the record routing carried on the context (D1, D5).
- Items are charged in request order, before any item is loaded, whatever the item's outcome. A `visibility.denied` item is charged, and no charge is refunded (D2).
- Admission is a prefix. The first refused item ends charging: it and every later item are refused without a limiter call and without a load (D3).
- A refused item carries the §6.10 envelope `quota.materialize_rate_exceeded` with the message "tenant materialize budget exhausted", `retryable: true`, and the registry's `suggested_action`. The batch status stays 200, and no new error code is added (D4).
- A request rejected as a whole charges nothing (D5).
- `QuotaLimiter.AllowMaterialize` keeps the single-token signature proposal 0048 gives it. No multi-token method is added (D6).
- `handleLoadArtifact` and `handleBatchLoad` both charge through the unexported `Server.allowMaterialize(ctx)`, and both refusal paths use one message constant, so the two charge sites cannot drift to different keys (D7).
- `admitPrefix` is the only charging form in the handler. Interleaving the charge with the load is not an option (CODE-1).
- The SDKs change in no code, test, or docstring (D8).
- `max_user_layers` follows the 0048 D2 order on every deployment mode: a positive tenant value wins, zero selects `PODIUM_MAX_USER_LAYERS` or 3, and a negative tenant value disables the cap (D9).
- `effectiveLayerCap` keeps reading the tenant record from the store, on single-tenant and multi-tenant registries alike. A `GetTenant` error falls through to the deployment default (D10). Whether a single-tenant registry should ignore the record is OQ-1.
- `PODIUM_MAX_USER_LAYERS` keeps its parser, so it never disables the cap (D11).
- This proposal is implemented after proposals 0046, 0047, and 0048, in that order (D12).
- No compatibility shim is kept. Callers above the materialization rate receive per-item quota errors, deployments with non-zero tenant `max_user_layers` values now enforce them ahead of `PODIUM_MAX_USER_LAYERS`, and CL-1 states the operator action for each (D13).
- `quota.materialize_rate_exceeded` joins the §6.10 matrix axis, and the TEST-1 per-item batch test carries its `// Matrix:` annotation (D14).

**Watch out for.**

- **This proposal edits text that proposal 0048 writes.** SPEC-2 and SPEC-3 replace sentences in the §4.7.8 paragraphs and the §13.12 table that 0048 SPEC-1 and SPEC-2 add, and DOC-1 replaces sentences that 0048 DOC-1(a) and DOC-1(b) add. If 0048 has not landed, stop: SPEC-1, SPEC-2, SPEC-3, CODE-1, and CODE-2 do not apply as written. Locate every edit by its quoted text. Line numbers are anchors at the time of writing.
- **`TestLayerCap_NegativeDisablesCap` fails as soon as CODE-2 lands.** It pins the deployment-first order (`pkg/registry/server/layer_cap_test.go:135-154`). CODE-2 and TEST-2 therefore land in one step (S5).
- **Do not interleave the charge and the load.** `admitPrefix` charges every admitted item before the first `core.LoadArtifact` call. The tests depend on that: `rateBucket.allow` reads `time.Now()` directly (`pkg/registry/server/rate_limit.go:53`), and the up-front charges run microseconds apart, so no token refills between them.
- **Do not call the limiter after a refusal.** A token that refills while a batch loads would otherwise admit a later item after an earlier one was refused, and the admitted set would stop being a prefix.
- **Build the per-item envelope through `enrichEnvelope`.** An `ErrorResponse` built by hand without it carries `retryable: false` and no `suggested_action` (`pkg/registry/server/error_envelope.go:145-157`).
- **Do not add a multi-token limiter method.** 0048 D7 makes the retune and the charge one critical section under `b.mu`. A batch charge method would reopen that invariant.
- **Update the §6.10 matrix axis in the same step as the `// Matrix:` annotation.** `tools/matrix/matrices.go:118` lists the codes by hand, and the code is not on it today.
- **A tenant-record read fault now loosens a smaller tenant cap to the deployment default.** Before this change, a deployment that set `PODIUM_MAX_USER_LAYERS` never read the record. D10 accepts the new fault path, and the `effectiveLayerCap` doc comment states why.
- **Do not align the single-tenant cap with the rate budgets in this change.** 0048 makes a single-tenant registry ignore its stored rate values. The cap still reads the stored record on a single-tenant registry, and `TestLayerCap_TenantQuotaOverride` and `test/integration/layer_cap_test.go` depend on that. OQ-1 holds the question.
- **`envInt` clamps a negative or non-integer value to 0** (`internal/serverboot/serverboot.go:267-277`). Only a tenant record or a library caller's `WithMaxUserLayers` can carry a negative cap.
- **Parts of `test/e2e` skip silently on macOS.** Grep the run output for `SKIP` before claiming end-to-end verification.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §7.6.2 gains the **Materialization rate** bullet.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The first sentence of the 0048 §4.7.8 charge-site paragraph names each bulk-load item (SPEC-2(a)), and the 0048 §13.12 `PODIUM_QUOTA_MATERIALIZE_RATE` row's refusal sentence names `load_artifact` and the per-item bulk-load report (SPEC-2(b)).
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §4.7.8 states the tenant-first user-layer cap order, and §13.12 gains the `PODIUM_MAX_USER_LAYERS` row.
      Levels: —. Depends on: —
- [ ] **S4 · code** — CODE-1. `Server.allowMaterialize`, `admitPrefix`, the shared message constant, `materializeQuotaEnvelope`, the per-item charge in `handleBatchLoad`, and the `suggested_action` text land.
      Levels: unit, integration, e2e. Depends on: S1, S2
- [ ] **S5 · code** — CODE-2, TEST-2. `effectiveLayerCap` reads the tenant record first, the cap comments and flag help follow, and the layer-cap unit tests change with it. Bundled because the existing `TestLayerCap_NegativeDisablesCap` fails on the reordered code, so neither half is green alone.
      Levels: unit, integration, e2e. Depends on: S3
- [ ] **S6 · test** — TEST-1. Unit and in-process integration tests for the batch charge, and the §6.10 matrix cell.
      Levels: unit, integration. Depends on: S4
- [ ] **S7 · test** — TEST-3. The end-to-end bulk-load quota test on the binary and the comment update on `TestDocHTTPAPI_LayerCapConfigurable`.
      Levels: e2e. Depends on: S4, S5
- [ ] **S8 · docs** — DOC-1. The HTTP API, SDK, error-code, CLI, and clustered deployment references describe the per-item charge and the tenant-first cap. Land S8 in the same pull request as S4 and S5 so no released build documents behavior it lacks.
      Levels: —. Depends on: S4, S5
- [ ] **S9 · docs** — CL-1. The `[Unreleased]` `Fixed`, `Changed`, and `Documentation` entries.
      Levels: —. Depends on: S8

**Ordering constraints.** S1 to S3 land the rules the later steps cite. S2 follows S1 because its sentence points to the §7.6.2 bullet. S4 and S5 touch different files and may proceed in parallel. S6 needs the helpers S4 adds, and S7 needs both code steps. S1 starts only after proposals 0046, 0047, and 0048 are implemented.

## Current state and the gap

Proposal 0048 (Approved 2026-10-03, unimplemented) charges search and load quotas to the request's routed tenant. It resolves limits with `EffectiveLimits(rec, def)`: a positive tenant value is enforced, zero selects the deployment default, and a negative value disables the budget (0048 D2). It recorded two consistency gaps as follow-ups, OQ-3 and OQ-2. This proposal closes both. It is written against the tree after 0046, 0047, and 0048 land, in that order.

### The bulk load bypasses the materialization rate

`POST /v1/artifacts:batchLoad` is routed to `handleBatchLoad` (`pkg/registry/server/server.go:405`). The handler accepts up to `BatchLoadCap = 50` IDs (`pkg/registry/server/batch_load.go:18`, `:103-107`) and calls `loadOneForBatch`, and therefore `core.LoadArtifact`, once per ID (`batch_load.go:108-112`, `:116-120`). It makes no limiter call. Outside tests, the only `AllowMaterialize` call is in `handleLoadArtifact` (`server.go:1015-1018`), and `core.LoadArtifact` charges nothing.

Both SDKs split larger sets into chunks of 50 IDs and post them one after another (`sdks/podium-py/podium/client.py:1462-1487`; `sdks/podium-ts/src/index.ts:1385-1429`). A caller that `load_artifact` refuses with `429 quota.materialize_rate_exceeded` can therefore keep loading any number of artifacts through `Client.load_artifacts`, `loadArtifacts`, or the raw endpoint, and the §4.7.8 materialization rate does not bound it.

### The spec is silent on the bulk-load charge

§7.6.2 defines per-item status and states that partial failure does not fail the batch, but it names no quota. §4.7.8 names no charge site today. After 0048 SPEC-1(b), §4.7.8 names only `load_artifact`, and 0048 D15 leaves the batch uncharged explicitly. The code `quota.materialize_rate_exceeded` exists in the implementation (`pkg/registry/server/error_envelope.go:40-43`; `server.go:1016`) and in `docs/reference/error-codes.md:151`. Neither §4.7.8 nor §6.10 names it: §6.10 lists only "`quota.storage_exceeded` etc." After 0048, the §13.12 `PODIUM_QUOTA_MATERIALIZE_RATE` row (0048 SPEC-2) and the `docs/reference/cli.md` environment row (0048 DOC-1(b)) name it in the unqualified sentence "A request over the limit is refused with `quota.materialize_rate_exceeded`." That sentence is accurate while `load_artifact` is the only charge site. Once the bulk load is charged, an over-limit `POST /v1/artifacts:batchLoad` request is not refused: it returns 200 with per-item errors (§7.6.2 "Partial failure does not fail the batch", `spec/07-external-integration.md:731`; `batch_load.go:113`). The §6.10 matrix axis in `tools/matrix/matrices.go:118` does not list it.

### The user-layer cap resolves deployment-first

`effectiveLayerCap` (`pkg/registry/server/layers.go:576-590`) returns a non-zero `WithMaxUserLayers` value first, then a non-zero tenant `store.Quota.MaxUserLayers`, then `DefaultMaxUserLayers` (3). Serverboot sets that override from `PODIUM_MAX_USER_LAYERS` (`internal/serverboot/serverboot.go:2313`, `:1490`), so the deployment variable beats every tenant record. `docs/reference/http-api.md:585` documents this order.

The current spec has no deployment-first rule. §4.7.8 says the limits are per-org and admin-configurable and that a zero tenant value selects the default. §7.3.1 says "configurable per tenant", and §4.7.2 lists the default user-layer cap among tenant-level settings. No spec section names `PODIUM_MAX_USER_LAYERS`. 0048 SPEC-1(a) adds the sentence "a deployment-configured cap applies ahead of the tenant value". 0048 added it only to describe the current code (its Pass 1 review log) and left the question open (0048 D3, OQ-2).

That sentence describes override precedence, under which the deployment value replaces the tenant value. It does not describe a ceiling such as min(deployment, tenant). The exception for a deployment-wide ceiling therefore does not apply, and the cap should follow the 0048 D2 order.

## Decisions

- **D1. Each bulk-load item is charged separately.** Every item the batch would load counts as one materialization against the bucket `load_artifact` charges: the tenant from `s.core.TenantFor(ctx)`, under the tenant record routing put on the context (`tenantQuotaFrom(ctx)`, 0048 D5). The bucket key and the limit therefore resolve the same way for both endpoints. A single-tenant registry charges the deployment default from `PODIUM_QUOTA_MATERIALIZE_RATE`, and an unrouted multi-tenant request charges the shared `podium:unrouted` budget, as 0048 D4 and D11 already do for `load_artifact`.
- **D2. Items are charged in request order, before they are loaded, whatever their outcome.** An item that later comes back as `visibility.denied` (absent or hidden) or with an attestation or presign error is still charged. This matches `load_artifact`, which charges before it checks `id`, before the `as_admin` gate, and before `core.LoadArtifact`, and never refunds (`server.go:1015-1035`). Matching `load_artifact` is the reason for this rule. The §7.6.2 no-existence-leak rule does not decide it: core returns one `ErrNotFound` for hidden and absent artifacts (`pkg/registry/core/core.go:1570-1601`), `batchLoadError` maps both to `visibility.denied` (`batch_load.go:201-206`), and `GET /v1/quota` exposes no bucket state (`pkg/registry/server/quota.go:20-26`). Any outcome-based policy would treat hidden and absent items alike. CODE-1 charges every admitted item before the first load. Because the charge does not depend on the outcome, the timing has no observable effect, and SPEC-1 states only the order.
- **D3. The first refused item ends charging for the rest of the batch.** When the bucket refuses item k, item k and every later item come back as `status: "error"` with the `quota.materialize_rate_exceeded` envelope, and none of them is loaded. The limiter is not called again for them, so the refused items take no tokens. Items 0 to k-1 load and are reported as today. The admitted items are therefore always a prefix of the request. Without this rule, a token that refills partway through a batch could admit a later item after an earlier one was refused, and the result would depend on timing.
- **D4. A refused item's per-item error is the envelope `load_artifact` writes.** The code is `quota.materialize_rate_exceeded` and the message is "tenant materialize budget exhausted". `enrichEnvelope` supplies `retryable: true` and `suggested_action` (`error_envelope.go:40-43`, `:145-157`). The batch status stays 200, following the §7.6.2 partial-failure rule and the existing handler (`batch_load.go:83-86`, `:113`). No new error code is added.
- **D5. Request-level rejections charge nothing.** A wrong method, an undecodable body, empty `ids`, and more than 50 IDs are rejected before any charge (`batch_load.go:88-107`). None of them loads an item. A duplicate ID is charged once for each occurrence, because the handler loads each occurrence (`batch_load.go:110-112`) and does not deduplicate. A refused item is not resolved, so it freezes no `latest` version for the session (§7.6.2 session consistency).
- **D6. `AllowMaterialize` keeps the signature 0048 gives it, which charges one token per call** (`AllowMaterialize(tenantID string, rec store.Quota) bool`, 0048 CODE-1). The batch calls it once per admitted item, at most 50 times per request, and a refusal stops further calls. No multi-token limiter method is added, so the 0048 bucket invariant (0048 D7: an in-place retune and the charge share one critical section under `b.mu`) stays unchanged.
- **D7. One helper names the charge key.** The unexported `Server.allowMaterialize(ctx)` wraps `s.quota.AllowMaterialize(s.core.TenantFor(ctx), tenantQuotaFrom(ctx))`. `handleLoadArtifact` and `handleBatchLoad` both call it, so the two charge sites cannot drift to different keys. The message string becomes one unexported constant that both the 429 writer and the per-item envelope use.
- **D8. The SDKs gain no new surface.** Python turns a per-item envelope into a `RegistryError` on `BatchResult.error` and does not raise (`client.py:456-477`, `:67-90`). `BatchResult.materialize()` raises that error on an error item (`client.py:363-364`). TypeScript keeps the raw envelope on `BatchResult.error` (`index.ts:635-642`) and throws `registryErrorFromEnvelope` from `materialize()` (`index.ts:661-671`). Neither SDK stops posting later chunks after a refusal, and each later chunk's items are charged and reported on their own. No SDK code, test, or docstring changes. The SDK docstrings already state that error items carry a §6.10 envelope (`client.py:1450-1456`, `index.ts:1368-1374`), and the dropped TEST-4 (Non-goals) records why no SDK test is added.
- **D9. `max_user_layers` follows the 0048 D2 order.** A positive tenant `max_user_layers` is enforced. A zero tenant value selects the deployment default, which is `PODIUM_MAX_USER_LAYERS` when that is non-zero and 3 otherwise. A negative tenant value disables the cap. The deployment-wide ceiling exception does not apply. No current spec text states a deployment ceiling (§4.7.8, §7.3.1, and no `PODIUM_MAX_USER_LAYERS` anywhere in `spec/`). The code implements an override rather than min(deployment, tenant) (`layers.go:582-590`). 0048 labels its deployment-first sentence descriptive and leaves it open (0048 D3, OQ-2, Pass 1 review log).
- **D10. `effectiveLayerCap` keeps its existing store read of the tenant record**, `GetTenant` with the tenant passed as a parameter after 0047 CODE-1, and only the order changes. The read runs on single-tenant and multi-tenant registries alike, so a single-tenant registry still enforces a non-zero stored `max_user_layers` ahead of the variable. A `GetTenant` error falls through to the deployment default, which is the current fault behavior for that read. That fault can loosen a tenant cap smaller than the deployment default. The loosening is accepted because `register`'s owned-layer count reads the same store, so a sustained fault refuses the request before the cap matters. Moving the cap onto 0048's context-carried record would also change the single-tenant source of the value. That is a separate decision, and OQ-1 records it.
- **D11. `PODIUM_MAX_USER_LAYERS` keeps its parser.** `envInt` treats a negative or non-integer value as 0 (`internal/serverboot/serverboot.go:267-277`), so the variable cannot disable the cap deployment-wide. Only a negative tenant value disables it. The variable gets a row in the §13.12 `### Quotas` table that 0048 SPEC-2 adds. This documents the variable the binary already reads and adds no new one. A library caller can still pass a negative `WithMaxUserLayers` value, which disables the cap for zero-valued tenants. That behavior is kept, and the doc comments say so.
- **D12. Sequencing.** This proposal is implemented after 0046, 0047, and 0048, in that order. Its code sketches use 0048's `tenantQuotaFrom` and `AllowMaterialize(tenantID, rec)` and 0047's tenant-parameterized `effectiveLayerCap`. It replaces text that 0048 writes: the SPEC-1(a) user-layer sentences, the first sentence of the SPEC-1(b) charge-site paragraph, the refusal sentence of the SPEC-2 `PODIUM_QUOTA_MATERIALIZE_RATE` row, the DOC-1(a) sentence that leaves the bulk load uncharged, the `max_user_layers` sentences DOC-1(a) keeps, and the refusal sentence of the DOC-1(b) `PODIUM_QUOTA_MATERIALIZE_RATE` environment row. The 0048 edge-case rows for `max_user_layers` and for the §7.6.2 bulk load are superseded by this proposal's edge-case table. It does not edit the 0048 proposal file. If 0048 has not landed, stop: SPEC-1, SPEC-2, SPEC-3, CODE-1, and CODE-2 do not apply as written.
- **D13. Breaking-change posture.** Pre-1.0, no shim is kept. Programmatic callers above the materialization rate now receive per-item quota errors from `load_artifacts` and `loadArtifacts`. Deployments that set `PODIUM_MAX_USER_LAYERS` and also have tenants with a non-zero `max_user_layers` now enforce the tenant value. CL-1 states the operator action for each change.
- **D14. §6.10 matrix obligation.** SPEC-1 names `quota.materialize_rate_exceeded` in §7.6.2, which joins the §13.12 `PODIUM_QUOTA_MATERIALIZE_RATE` row, as SPEC-2(b) rewrites it, as spec text that names it. The code is added to the §6.10 axis in `tools/matrix/matrices.go`, and the TEST-1 per-item batch test carries `// Matrix: §6.10 (quota.materialize_rate_exceeded)`, so `make coverage-gate` stays green.

## Spec amendment: §7.6.2 bulk-load materialization rate

**SPEC-1.** `spec/07-external-integration.md`, §7.6.2 "Bulk Fetch", the **Semantics.** list (lines 728-732 at the time of writing). Insert a new bullet immediately after the bullet that reads:

> - **Partial failure** does not fail the batch. Each item carries its own status.

and before the bullet that begins "**Bandwidth:**". The new bullet:

> - **Materialization rate:** each item counts as one load against the §4.7.8 materialization rate of the tenant the request resolves to, as a `load_artifact` request does. Items are charged in request order whatever their outcome, so an item that comes back with `visibility.denied` is charged. When the rate refuses an item, that item and every later item come back as `status: "error"` with `quota.materialize_rate_exceeded`. They are neither loaded nor charged, and they resolve no `latest` version for the session. The items before the refused one are served as usual. A request rejected as a whole, such as one above the hard cap, charges nothing.

The rest of §7.6.2 is unchanged.

## Spec amendment: §4.7.8 and §13.12 bulk-load charge site

**SPEC-2.** Two edits, in one commit (step S2): SPEC-2(a) in §4.7.8 and SPEC-2(b) in §13.12. SPEC-2(a) targets `spec/04-artifact-model.md`, §4.7.8 "Quotas", the paragraph that 0048 SPEC-1(b) inserts. That paragraph begins:

> Each `search_domains` and `search_artifacts` request counts against the search QPS limit, and each `load_artifact` request counts against the materialization rate, of the tenant the request resolves to (§6.3.1).

**SPEC-2(a).** Replace only that first sentence with:

> Each `search_domains` and `search_artifacts` request counts against the search QPS limit, and each `load_artifact` request and each item of a bulk load (§7.6.2) counts against the materialization rate, of the tenant the request resolves to (§6.3.1).

The rest of the paragraph is unchanged. No sentence naming the refusal code is added to §4.7.8: the SPEC-1 bullet is the single statement of the per-item report and the 200 status, and SPEC-2(b) scopes the §13.12 refusal sentence to `load_artifact`.

**SPEC-2(b).** `spec/13-deployment.md`, §13.12, the `### Quotas` table that 0048 SPEC-2 adds, the `PODIUM_QUOTA_MATERIALIZE_RATE` row. In that row only, replace the sentence

> A request over the limit is refused with `quota.materialize_rate_exceeded`.

with:

> A `load_artifact` request over the limit is refused with `quota.materialize_rate_exceeded`, and a bulk-load item over the limit is reported with that code inside the batch's 200 response (§7.6.2).

The `PODIUM_QUOTA_SEARCH_QPS` row carries a parallel sentence naming `quota.search_qps_exceeded`, which stays unchanged because no search path is a batch. SPEC-3(b) appends a row to the same table in step S3, and the two edits touch different rows.

## Spec amendment: §4.7.8 and §13.12 user-layer cap order

**SPEC-3.** Two edits, in one commit (step S3). §7.3.1 is unchanged: its sentence "Default cap: 3 user-defined layers per identity, configurable per tenant." stays accurate under the tenant-first order, and so do §1.4 and §4.7.2.

(a) `spec/04-artifact-model.md`, §4.7.8 "Quotas", the second paragraph as 0048 SPEC-1(a) replaces it. That paragraph ends with these sentences:

> A `storage` value of zero or less is treated as no limit. For the default user-layer cap, a deployment-configured cap applies ahead of the tenant value. Otherwise a zero tenant value selects 3 user-defined layers per identity, and a negative tenant value disables the cap.

Keep the `storage` sentence, and replace the two user-layer sentences with:

> The per-identity user-layer cap resolves in the same order as search QPS, materialization rate, and audit volume: a positive tenant value is enforced, a zero tenant value selects the deployment default, and a negative tenant value disables the cap. The cap's deployment default is 3 user-defined layers per identity unless the deployment configures another (§13.12).

(b) `spec/13-deployment.md`, §13.12, the `### Quotas` table that 0048 SPEC-2 adds. Append a row after the `PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY` row:

> | `PODIUM_MAX_USER_LAYERS` | Deployment default for the §4.7.8 per-identity user-defined-layer cap (§7.3.1). It applies to every tenant whose own `max_user_layers` is zero. A value of 0 selects 3 user-defined layers per identity. A negative or non-integer value is treated as 0, so this variable cannot disable the cap. A single-tenant registry reads its tenant's stored `max_user_layers`, so a non-zero stored value applies there ahead of this variable. Environment only; no config-file key. | `0` |

The table's closing sentence, "A single-tenant registry rejects `/v1/admin/tenants` (§7.3.3) and does not read its tenant's stored search QPS, materialization rate, or audit volume values, so these variables set its limits for those budgets.", is unchanged. It names the three rate budgets only, and the new row states the cap's single-tenant behavior.

## Proposed solution

### CODE-1. `pkg/registry/server`: charge each batch item in request order

Targets: `pkg/registry/server/batch_load.go` (`handleBatchLoad` :87-114, new helper), `pkg/registry/server/rate_limit.go` (new `Server.allowMaterialize`, the message constant, and the per-item envelope builder), `pkg/registry/server/server.go` (`handleLoadArtifact` charge at :1015-1018), and `pkg/registry/server/error_envelope.go` (`quota.materialize_rate_exceeded` `suggestedAction` at :40-43).

**`rate_limit.go`.**

- `const materializeQuotaMessage = "tenant materialize budget exhausted"`, unexported.
- `func (s *Server) allowMaterialize(ctx context.Context) bool`, unexported, carrying `// Spec: §4.7.8, §7.6.2`. It returns `s.quota.AllowMaterialize(s.core.TenantFor(ctx), tenantQuotaFrom(ctx))`. A nil `s.quota` allows every charge, as 0048's nil-limiter guard already does. The comment states that both load paths call it so their bucket key and limit cannot drift (D7).
- `func materializeQuotaEnvelope() *ErrorResponse`, unexported, carrying `// Spec: §7.6.2, §6.10`. It builds `&ErrorResponse{Code: "quota.materialize_rate_exceeded", Message: materializeQuotaMessage}`, calls `enrichEnvelope` on it, and returns it, as `batchLoadError` does for `visibility.denied`.

**`batch_load.go`.**

- `func admitPrefix(n int, allow func() bool) int`, unexported, carrying `// Spec: §7.6.2, §4.7.8`. It calls `allow` for indexes 0 to n-1 in order, stops at the first false, and returns the number of calls that returned true. With `n == 0` it calls nothing and returns 0. Its doc comment states the prefix rule (D3), states that every admitted item is charged before any load, and gives the two reasons this is safe: the charge does not depend on the outcome (D2), and `core.LoadArtifact` draws no tokens.
- In `handleBatchLoad`, after the request validation and `id := s.identity(r)`, compute `admitted := admitPrefix(len(req.IDs), func() bool { return s.allowMaterialize(r.Context()) })`. Then loop once over `req.IDs`. An index below `admitted` appends `s.loadOneForBatch(...)` as today. Any other index appends `BatchLoadEnvelope{ID: artifactID, Status: "error", Error: materializeQuotaEnvelope()}`. Write the result with `writeJSON(w, http.StatusOK, out)` as today.
- Update the `handleBatchLoad` doc comment: each item is charged against the materialization rate in request order, and the first refused item and every later item come back as per-item quota errors (§7.6.2, §4.7.8).

**`server.go`.** In `handleLoadArtifact`, replace the 0048 call `s.quota.AllowMaterialize(s.core.TenantFor(r.Context()), tenantQuotaFrom(r.Context()))` with `s.allowMaterialize(r.Context())` at its current position, before the method's `id` check. Replace the message literal in the `writeQuotaError` call with `materializeQuotaMessage`. The 429 response is byte-identical.

**`error_envelope.go`.** Change the `quota.materialize_rate_exceeded` `suggestedAction` to `"Reduce the load_artifact and bulk-load request rate, retry refused items after a backoff, or raise the tenant's materialize quota."` `retryable` stays true. Only this file contains the old string, so no test pins it.

### CODE-2. `pkg/registry/server`: resolve the user-layer cap tenant record first

Targets: `pkg/registry/server/layers.go` (`DefaultMaxUserLayers` comment :27-32, `maxUserLayers` field comment :70-74, `WithMaxUserLayers` :370-376, `effectiveLayerCap` :576-590 as parameterized by 0047 CODE-1), `pkg/store/store.go:89-94`, `internal/serverboot/serverboot.go:2019-2021`, and `cmd/podium/admin_tenant.go:82`.

**`effectiveLayerCap`.** After 0047 the function takes the tenant as a parameter. Reorder its body so the tenant record is read first:

```go
// effectiveLayerCap resolves the §7.3.1 user-defined-layer cap for
// tenantID in the §4.7.8 order. A non-zero tenant
// store.Quota.MaxUserLayers wins (a negative value disables the cap).
// A zero tenant value, a missing record, or a GetTenant error selects
// the deployment default: the WithMaxUserLayers value when non-zero,
// else DefaultMaxUserLayers. A read fault can therefore apply a default
// looser than the tenant's own cap; this is accepted because register's
// owned-layer count reads the same store, so a sustained fault refuses
// the request before the cap matters. The resolved value is never zero,
// so the caller treats cap > 0 as "enforce" and cap < 0 as "unlimited".
//
// Spec: §4.7.8, §7.3.1
func (e *LayerEndpoint) effectiveLayerCap(ctx context.Context, tenantID string) int {
	if t, err := e.store.GetTenant(ctx, tenantID); err == nil && t.Quota.MaxUserLayers != 0 {
		return t.Quota.MaxUserLayers
	}
	if e.maxUserLayers != 0 {
		return e.maxUserLayers
	}
	return DefaultMaxUserLayers
}
```

The `GetTenant` read now runs on every capped registration, including when a deployment default is set. The function has no other caller than `register`.

**Comments.**

- `DefaultMaxUserLayers` (`layers.go:27-32`): "A non-zero per-tenant store.Quota.MaxUserLayers overrides it, and a zero tenant value selects the WithMaxUserLayers deployment default when that is non-zero, else this value (§4.7.8)."
- `maxUserLayers` field (`layers.go:70-74`): "maxUserLayers is the §4.7.8 deployment default for the per-identity user-defined-layer cap. It applies to a tenant whose max_user_layers is zero. Zero selects DefaultMaxUserLayers, and a negative value disables the cap for zero-valued tenants. See effectiveLayerCap."
- `WithMaxUserLayers` (`layers.go:370-376`): "WithMaxUserLayers sets the deployment default for the §7.3.1 per-identity cap on user-defined layers. A tenant's non-zero max_user_layers is enforced ahead of it. For a zero-valued tenant, a positive value caps at that count, zero leaves DefaultMaxUserLayers in place, and a negative value disables the cap. Serverboot never passes a negative value, because envInt clamps PODIUM_MAX_USER_LAYERS to 0 or more."
- `pkg/store/store.go:89-94`, the `MaxUserLayers` doc comment: keep the first sentence and its quotations, and replace the rest with "Zero selects the deployment default (the registry's WithMaxUserLayers value, which serverboot reads from PODIUM_MAX_USER_LAYERS, or 3 when that is zero). A positive value is enforced whatever the deployment default is, and a negative value disables the cap. The register handler resolves and enforces it (pkg/registry/server/layers.go)."
- `internal/serverboot/serverboot.go:2019-2021`, the `cfg.maxUserLayers` comment: "§4.7.8/§7.3.1 deployment default for the per-identity user-defined-layer cap. It applies to tenants whose max_user_layers is zero, and zero here selects server.DefaultMaxUserLayers (3). envInt clamps negative input to 0, so this value never disables the cap." The wiring at `:2313` and `:1490` is unchanged.

**Flag help.** `cmd/podium/admin_tenant.go:82`: the `max-user-layers` help becomes `"per-identity user-defined-layer cap (0 selects the deployment default; a negative value disables the cap)"`. No test asserts the help text.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Bulk load within the materialization rate | Every item is charged once and served as today | §7.6.2 **Materialization rate** bullet (SPEC-1); §4.7.8 charge-site sentence (SPEC-2); `docs/reference/http-api.md` bulk-load section (DOC-1) |
| The rate refuses item k of a batch | Items 0 to k-1 are served. Item k and every later item come back as `status: "error"` with `quota.materialize_rate_exceeded`, `retryable: true`, and are neither loaded nor charged. HTTP status 200 | §7.6.2 "that item and every later item come back" (SPEC-1); §13.12 `PODIUM_QUOTA_MATERIALIZE_RATE` row "a bulk-load item over the limit is reported with that code inside the batch's 200 response" (SPEC-2(b)); `docs/reference/http-api.md` bulk-load section, `docs/consuming/custom-via-sdk.md`, `docs/reference/error-codes.md`, `docs/reference/cli.md` environment row (DOC-1) |
| An item that comes back `visibility.denied` (hidden or absent) | Charged, as a `load_artifact` request for it is | §7.6.2 "whatever their outcome, so an item that comes back with `visibility.denied` is charged" (SPEC-1); `docs/reference/http-api.md` bulk-load section (DOC-1) |
| An item that fails after admission with another error, or a `load_artifact` request that fails after its charge | Charged and not refunded (existing for `load_artifact`, accepted for the batch) | §7.6.2 "whatever their outcome" (SPEC-1); `docs/reference/http-api.md` bulk-load section (DOC-1) |
| Duplicate IDs in one batch | Each occurrence is charged and loaded | §7.6.2 "each item counts as one load" (SPEC-1); `docs/reference/http-api.md` bulk-load section (DOC-1) |
| Request rejected as a whole (wrong method, undecodable body, empty `ids`, more than 50 IDs) | 400 or 405 as today, and nothing is charged | §7.6.2 "A request rejected as a whole, such as one above the hard cap, charges nothing" (SPEC-1); `docs/reference/http-api.md` bulk-load section (DOC-1) |
| Refused item named `latest` with a `session_id` | Resolves no version, so the session freezes nothing for it | §7.6.2 "they resolve no `latest` version for the session" (SPEC-1) |
| SDK splits a set above 50 IDs and one chunk meets a refusal | The SDK still posts the remaining chunks. Their items are charged and reported on their own, so a later chunk can be served after a token refills (accepted, D8) | §7.6.2 hard-cap and **Materialization rate** bullets (SPEC-1); `docs/consuming/custom-via-sdk.md` (DOC-1) |
| `load_artifact` and the bulk load from one tenant | Both draw on one bucket, so either can exhaust the other's budget | §4.7.8 charge-site sentence (SPEC-2); `docs/reference/http-api.md` Quota section (DOC-1) |
| Unrouted multi-tenant bulk load | Charged to the shared `podium:unrouted` budget at the deployment default, as `load_artifact` is under 0048 | §4.7.8 unrouted sentences (0048 SPEC-1(b)); §7.6.2 "the tenant the request resolves to" (SPEC-1); `docs/deployment/clustered.md` Quotas bullet (0048 DOC-1(c)) |
| Single-tenant bulk load | Charged at `PODIUM_QUOTA_MATERIALIZE_RATE` | §13.12 `PODIUM_QUOTA_MATERIALIZE_RATE` row (0048 SPEC-2); `docs/reference/cli.md` environment table (0048 DOC-1(b)) |
| No limiter installed, or a resolved rate of zero or less | Every item is loaded and nothing is charged (existing limiter behavior) | §4.7.8 "A limit whose tenant value is zero and whose deployment default is zero is not enforced" (0048 SPEC-1(a)); `docs/reference/http-api.md` Quota section (0048 DOC-1(a)) |
| Multi-replica deployment | Each replica keeps its own buckets (existing, accepted) | Existing behavior; no new text |
| MCP server | Unaffected: it does not call the bulk endpoint | §7.6.2 "The MCP server does not call this endpoint" (existing) |
| Tenant `max_user_layers` positive and `PODIUM_MAX_USER_LAYERS` set | The tenant value is enforced | §4.7.8 user-layer sentence (SPEC-3(a)); `docs/reference/http-api.md` Quota section, `docs/reference/cli.md` flag row (DOC-1) |
| Tenant `max_user_layers` zero | `PODIUM_MAX_USER_LAYERS` when non-zero, else 3 | §4.7.8 (SPEC-3(a)); §13.12 `PODIUM_MAX_USER_LAYERS` row (SPEC-3(b)); `docs/reference/cli.md` environment row (DOC-1) |
| Tenant `max_user_layers` negative | The cap is disabled for that tenant whatever the deployment default is | §4.7.8 (SPEC-3(a)); `docs/reference/http-api.md` Quota section, `docs/reference/cli.md` flag row (DOC-1) |
| `PODIUM_MAX_USER_LAYERS` negative or non-integer | Treated as 0, so the cap is 3 for zero-valued tenants | §13.12 `PODIUM_MAX_USER_LAYERS` row (SPEC-3(b)); `docs/reference/cli.md` environment row (DOC-1) |
| `GetTenant` fails while resolving the cap | The deployment default applies, which can be looser than the tenant's own cap (accepted, D10) | No spec text (an internal fault outcome, as in 0048 D6); the `effectiveLayerCap` doc comment (CODE-2) |
| Single-tenant registry whose stored record holds a non-zero `max_user_layers`, for example from an earlier multi-tenant run | The stored value applies ahead of `PODIUM_MAX_USER_LAYERS`, and `/v1/admin/tenants` is unavailable to change it (accepted pending OQ-1) | §13.12 `PODIUM_MAX_USER_LAYERS` row "A single-tenant registry reads its tenant's stored `max_user_layers`" (SPEC-3(b)); `docs/reference/cli.md` environment row (DOC-1); CL-1 `Changed` |
| `GET /v1/quota` `max_user_layers` | Reports the stored value. Under the new order a non-zero stored value is the enforced cap, and `0` means the deployment default | §4.7.8 "It reports the storage and user-layer values as stored" (0048 SPEC-1(c)); `docs/reference/http-api.md` Quota section (DOC-1) |
| Library caller passes a negative `WithMaxUserLayers` | The cap is disabled for zero-valued tenants. A tenant's positive value is still enforced (kept, D11) | No spec text (a library option); the `WithMaxUserLayers` doc comment (CODE-2) |

## Testing

**TEST-1 · unit and integration, `pkg/registry/server`.** Lands in S6. Targets: `pkg/registry/server/batch_load_quota_test.go` (new, package `server_test`), `pkg/registry/server/batch_load_test.go` (`newBatchFixture` gains a variadic `...server.Option` passed to `server.New`), `pkg/registry/server/batch_load_internal_test.go` (new, package `server`), and `tools/matrix/matrices.go` (§6.10 axis :118). Each test func carries `// Spec: §7.6.2, §4.7.8`.

- **`TestAdmitPrefix`** (package-internal). A table drives `admitPrefix` with a recording `allow`:
  - answers `[true, false, true]` with n=3 returns 1 after exactly two calls;
  - all-true with n=3 returns 3 after three calls; and
  - n=0 returns 0 and makes no call.
- **`TestBatchLoad_ChargesEachItemAndRefusesThePrefixTail`**, also carrying `// Matrix: §6.10 (quota.materialize_rate_exceeded)`. `newBatchFixture(t, server.WithQuotaLimiter(server.NewQuotaLimiter(server.QuotaLimits{MaterializeRate: 1})))`. POST ids `[team/a, team/b, team/a]`. Expect HTTP 200. Item 0 is `ok`. Items 1 and 2 carry `quota.materialize_rate_exceeded` with `retryable` true, a non-empty `suggested_action`, and empty `manifest_body`, `content_hash`, and `delivery_hash`. Then an immediate `GET /v1/load_artifact?id=team/a` on the same server returns 429 `quota.materialize_rate_exceeded`, which pins the bucket shared with `load_artifact`. A comment states that the window is the 1 s refill of a rate-1 bucket.
- **`TestBatchLoad_ChargesDuplicateOccurrences`.** Rate 2, ids `[team/a, team/a, team/b]`. Items 0 and 1 are `ok`, and item 2 carries `quota.materialize_rate_exceeded`. The charges run before any load, microseconds apart, so the 0.5 s refill of a rate-2 bucket does not reach the case.
- **`TestBatchLoad_ChargesItemsThatFailToLoad`.** Rate 1, ids `[team/missing, team/a]`. Item 0 is `visibility.denied`, and item 1 carries `quota.materialize_rate_exceeded`.
- **`TestBatchLoad_RequestRejectionsChargeNothing`.** Rate 1. A `GET`, a malformed body, `{"ids": []}`, and 51 IDs each return their existing 4xx. Then a batch `[team/a]` returns item 0 `ok`, which shows the token is unspent.
- **`TestBatchLoad_ChargesTheRoutedTenant`**, also carrying `// Spec: §6.3.1`. Reuse the router stub and tenant records from 0048's `pkg/registry/server/quota_tenant_routing_test.go`: tenant A holds `materialize_rate` 1, tenant B a zero quota, and the limiter's deployment default is 2. A's batch of two IDs returns item 1 with `quota.materialize_rate_exceeded`. B's batch of two IDs then returns no item with a quota code. The fixture seeds no artifact, so admitted items come back `visibility.denied`, and the test asserts only the absence or presence of the quota code, as 0048 TEST-2 case 1 does. This pins that the batch charges the routed key under the carried record.
- **Matrix.** Add `"quota.materialize_rate_exceeded"` to the §6.10 axis in `tools/matrix/matrices.go`, after `"quota.storage_exceeded"`, in the same commit as the annotation.
- **Coverage.** Confirm 85% of the changed lines with `go test -coverpkg=./pkg/registry/server/... -coverprofile=cover.out ./pkg/registry/server/... ./test/integration/...` and `go tool cover -func=cover.out`.

The existing `batch_load_test.go` cases run without `WithQuotaLimiter` and already pin that a nil limiter loads every item, so no separate nil-limiter test is added.

**TEST-2 · unit, `pkg/registry/server/layer_cap_test.go`.** Lands in S5 with CODE-2. Every new or changed test carries `// Spec: §4.7.8, §7.3.1` and uses the `NewLayerEndpoint` signature that exists after 0047 CODE-1.

- (a) Replace `TestLayerCap_NegativeDisablesCap` (:135-154) with **`TestLayerCap_TenantValueAheadOfDeploymentDefault`**: tenant `MaxUserLayers` 2 with `WithMaxUserLayers(1)`. Registrations `a` and `b` return 201, and `c` returns 429 `quota.layer_count_exceeded`. Before the change, `b` returns 429.
- (b) **`TestLayerCap_NegativeTenantDisablesCap`**: tenant `MaxUserLayers` -1 with `WithMaxUserLayers(1)`. Five registrations all return 201.
- (c) **`TestLayerCap_NegativeDeploymentDefaultDisablesZeroTenant`**: a zero-valued tenant with `WithMaxUserLayers(-1)`. Five registrations all return 201.
- (d) **`TestLayerCap_TenantReadFaultUsesDeploymentDefault`**: reuse `flakyStore` from `pkg/registry/server/readonly_probe_test.go:15-25` with `failTenant` set, and add no new wrapper. The tenant record holds `MaxUserLayers` 5 and the endpoint uses `WithMaxUserLayers(1)`. The second registration returns 429, which shows the fault fell through to the deployment default.
- (e) Keep the name `TestLayerCap_WithMaxUserLayersOverridesDefault`. Update only its comment to say that `WithMaxUserLayers` is the deployment default that applies to a zero-valued tenant.
- (f) Leave `TestLayerCap_TenantQuotaOverride`, `TestServer_LayerCountEnvelope_Details` (`pkg/registry/server/error_envelope_http_test.go:79-84`), and `test/integration/layer_cap_test.go` unchanged. Each uses either a zero-valued tenant or no override, and each still passes.

**TEST-3 · e2e, `test/e2e`.** Lands in S7.

- (a) **`TestArtifactResponse_BatchLoadQuotaMaterialize`** in `test/e2e/artifact_response_test.go`, next to `TestArtifactResponse_QuotaMaterialize` (:798-820). It carries `// Spec: §4.7.8, §7.6.2, §13.12` and runs on the default lane. Call `writeRegistry` with two artifacts, then `startServerArgs` with `HOME=<tmp>`, `PODIUM_QUOTA_MATERIALIZE_RATE=1`, and `serve --standalone --layer-path <reg>`. `postJSON` to `/v1/artifacts:batchLoad` with both IDs returns HTTP 200. Item 0 is `ok`. Item 1 is `status: "error"` with code `quota.materialize_rate_exceeded`, `retryable` true, a non-empty `suggested_action`, and an empty `manifest_body`. The test makes no follow-up `load_artifact` call and no sleep. TEST-1 pins the shared bucket, and refill is existing limiter behavior.
- (b) `TestDocHTTPAPI_LayerCapConfigurable` (`test/e2e/http_api_test.go:866-886`): update only its comment to say that `PODIUM_MAX_USER_LAYERS` is the deployment default for a tenant whose `max_user_layers` is zero, and that the standalone bootstrap tenant holds zero. The assertions are unchanged.

Run `go test ./test/e2e/ -run 'TestArtifactResponse_BatchLoadQuota|TestDocHTTPAPI_LayerCap' -v`, and grep the output for `SKIP` before claiming verification. Measure subprocess coverage with `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...` as `.claude/rules/test-coverage.md` describes. TEST-1's routed-tenant case and TEST-2 cover routing and precedence at the level the change reaches, so no multi-tenant end-to-end case is added: the serverboot wiring of both variables is unchanged and already pinned on the binary by `TestArtifactResponse_QuotaMaterialize` and `TestDocHTTPAPI_LayerCapConfigurable`.

## Manual validation

No scenario is staged. A quota refusal is an HTTP status plus an error envelope, which TEST-3 asserts on the compiled binary, and the hand-run alternative is timing-sensitive. Non-goals records the dropped MV-1 and its reasons. Scenario S59 exports `PODIUM_MAX_USER_LAYERS=10` against a standalone registry whose bootstrap tenant holds a zero `max_user_layers` (`test/manual-validation.md:4310-4316`), so its prose stays accurate under the tenant-first order and needs no edit.

## Documentation changes

**DOC-1.** Lands in S8. No runnable block is added, so no `tools/doccov/manifest.yaml` entry is needed.

(a) `docs/reference/http-api.md`.

- Bulk-load section: after the paragraph that begins "Visibility is identical to `load_artifact`" (:292 at the time of writing), insert:

  > Each item counts as one load against the materialization rate of the caller's tenant, the budget `load_artifact` also draws on. Items are charged in request order whatever their outcome, so an item that comes back with `visibility.denied` is charged. When the rate refuses an item, that item and every later item come back as `status: "error"` with `quota.materialize_rate_exceeded` and `retryable: true`. They are not loaded or charged, and the items before them are served as usual. The batch status stays 200. A request rejected as a whole, such as one with more than 50 IDs, is not charged.

- Quota section: replace the 0048 DOC-1(a) sentence "The bulk load `POST /v1/artifacts:batchLoad` is not charged against the materialization rate." with:

  > Each item of a bulk load `POST /v1/artifacts:batchLoad` counts against the materialization rate. The bulk-load section describes how a refused item is reported.

- Quota section: replace the `max_user_layers` sentences (:585 at the time of writing, which 0048 DOC-1(a) keeps), from "A zero `max_user_layers` selects the deployment-configured cap" to the end of that paragraph, with:

  > A positive `max_user_layers` is the enforced cap. A zero value selects the deployment default, which is `PODIUM_MAX_USER_LAYERS` when the deployment sets it and 3 otherwise, and a negative value disables the cap.

(b) `docs/consuming/custom-via-sdk.md:123`. After "Partial failure does not fail the batch; each item carries its own status.", append:

> Each item counts as one load against the tenant's materialization rate. An item the rate refuses, and every item after it in the same request, comes back as `status: "error"` with `quota.materialize_rate_exceeded`, and its error carries `retryable: true`. The SDK still posts the remaining 50-ID chunks after a refusal, and their items are charged and reported on their own.

(c) `docs/reference/error-codes.md:151`, the `quota.materialize_rate_exceeded` row. Keep the row's text, including the sentence 0048 DOC-1(d) appends, and append:

> The registry answers `load_artifact` with HTTP 429, and on each `artifacts:batchLoad` item the rate refuses it returns the code as the item's error envelope (`status: "error"`, `retryable: true`) inside the batch's 200 response.

(d) `docs/reference/cli.md`.

- The `podium admin tenant` flag table, `--max-user-layers` row (:555):

  > | `--max-user-layers N` | Per-identity cap on user-defined layers. A positive value is enforced ahead of the deployment default. `0` selects the deployment default (`PODIUM_MAX_USER_LAYERS`, or 3 when that is unset); a negative value disables the cap. |

- The environment-variable table, the `PODIUM_QUOTA_MATERIALIZE_RATE` row that 0048 DOC-1(b) adds. In that row only, replace the sentence "A request over the limit is refused with `quota.materialize_rate_exceeded`." with:

  > A `load_artifact` request over the limit is refused with `quota.materialize_rate_exceeded`, and a bulk-load item over the limit is reported with that code inside the batch's 200 response.

  The `PODIUM_QUOTA_SEARCH_QPS` row's parallel sentence is unchanged.

- The environment-variable table: add a row after the `PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY` row that 0048 DOC-1(b) adds:

  > | `PODIUM_MAX_USER_LAYERS` | Registry-process boot setting, environment only and no config-file key. Deployment default for the per-identity user-defined-layer cap. It applies to every tenant whose own `max_user_layers` is zero. `0`, the default, selects 3, and a negative or non-integer value is treated as `0`, so the variable cannot disable the cap. A single-tenant registry reads its tenant's stored `max_user_layers`, so a non-zero stored value applies there ahead of this variable. |

(e) `docs/deployment/clustered.md:85`. Replace "Default cap is 3 user-defined layers per identity, configurable per tenant." with:

> Default cap is 3 user-defined layers per identity, configurable per tenant. `PODIUM_MAX_USER_LAYERS` sets the cap for a tenant whose own value is zero.

`docs/consuming/handling-artifact-responses.md:259` ("Back off and retry") already holds for a per-item quota error and is unchanged.

**CL-1.** `CHANGELOG.md`, `## [Unreleased]`. Lands in S9.

Under `### Fixed`:

> - **The bulk load is charged against the materialization rate** (§7.6.2, §4.7.8): each item of `POST /v1/artifacts:batchLoad` (`Client.load_artifacts`, `loadArtifacts`) now counts as one load against the materialization rate of the request's tenant, the budget `load_artifact` draws on. When the rate refuses an item, that item and every later item in the request come back as per-item `quota.materialize_rate_exceeded` errors with `retryable: true`, and the batch status stays 200. Previously the bulk load charged nothing, so a caller that `load_artifact` refused could keep loading through it. A client that loads above its tenant's rate now receives these per-item errors; back off and retry the refused items, or raise the tenant's `materialize_rate`.

Under `### Changed`:

> - On a multi-tenant registry, a tenant's positive `max_user_layers` is now enforced ahead of `PODIUM_MAX_USER_LAYERS` (§4.7.8, §7.3.1), in the same order as the search QPS, materialization rate, and audit-volume budgets. `PODIUM_MAX_USER_LAYERS` applies to every tenant whose value is zero, and a negative tenant value disables the cap. A deployment that relied on the variable to override larger tenant values sets those tenants' `max_user_layers` to 0, or to the intended cap, with `podium admin tenant update`. A single-tenant registry still reads its stored tenant record for this cap, unlike the three rate budgets. When that record holds a non-zero `max_user_layers`, the record's value now applies ahead of `PODIUM_MAX_USER_LAYERS`. `/v1/admin/tenants` is unavailable on a single-tenant registry, so the operator cannot change that value there.

Under `### Documentation`:

> - §7.6.2 states how each bulk-load item is charged and reported, §4.7.8 names the bulk load as a materialization charge site and states the user-layer cap order, and §13.12 documents `PODIUM_MAX_USER_LAYERS` and states in the `PODIUM_QUOTA_MATERIALIZE_RATE` row that an over-limit bulk-load item is reported inside the batch's 200 response. `docs/reference/http-api.md`, `docs/consuming/custom-via-sdk.md`, `docs/reference/error-codes.md`, `docs/reference/cli.md` (the `--max-user-layers` row, the `PODIUM_QUOTA_MATERIALIZE_RATE` environment row, and the new `PODIUM_MAX_USER_LAYERS` environment row), and `docs/deployment/clustered.md` follow, and the `podium admin tenant` flag help states the cap's zero and negative rule.

## Open questions

**OQ-1. The single-tenant source of the user-layer cap.** On a single-tenant registry, should the user-layer cap ignore the stored default-tenant record, as 0048 D11 does for the three rate budgets? Under this proposal, `effectiveLayerCap` still reads the tenant record on both modes (D10). A single-tenant registry whose bootstrap record holds a non-zero `max_user_layers`, for example one left from an earlier multi-tenant run, enforces that value ahead of `PODIUM_MAX_USER_LAYERS`. A single-tenant registry rejects `/v1/admin/tenants`, so the operator cannot change that record there. SPEC-3(b), DOC-1(d), and CL-1 state this behavior.

Ignoring the record on a single-tenant registry would gate the `GetTenant` read on 0047's `multiTenant` field, change the §13.12 row and the 0048 closing sentence to name `max_user_layers`, drop the last three sentences of the CL-1 `Changed` entry, and add a TEST-2 case and a standalone end-to-end case with a pre-seeded stale record, following 0048 TEST-4. It would also move `TestLayerCap_TenantQuotaOverride` and `test/integration/layer_cap_test.go`, which exercise the record on a single-tenant endpoint, onto a multi-tenant endpoint, and it would make `GET /v1/quota`'s stored `max_user_layers` differ from the enforced value on a single-tenant registry. Reading the cap from 0048's context-carried record is one way to do it, and it also drops the second store read on a multi-tenant registry.

## Non-goals

- Charging the bulk load per request, or refusing a whole batch with HTTP 429. Per-item charging is fixed, and §7.6.2 keeps the batch status at 200.
- A multi-token or batch charge method on `QuotaLimiter`. The batch makes at most 50 single-token calls under 0048's existing atomic charge (D6).
- Refunding a token for an item that fails to load, or for a `load_artifact` request that fails after its charge. Both paths charge before the outcome is known (D2).
- Changing the SDK chunking loop to stop after a quota refusal, or adding SDK retry or backoff for retryable per-item errors (D8).
- Charging the search QPS budget on any new path, or naming `quota.search_qps_exceeded` in §4.7.8 or on the §6.10 matrix axis.
- Moving `effectiveLayerCap` onto 0048's context-carried tenant record, or changing which record a single-tenant registry reads for the cap (D10, OQ-1).
- Letting `PODIUM_MAX_USER_LAYERS` accept a negative value to disable the cap deployment-wide. The `envInt` parser is unchanged (D11).
- Adding a config-file key for `PODIUM_MAX_USER_LAYERS` or any `PODIUM_QUOTA_*` variable.
- Injecting a clock into `rateBucket`. The D3 prefix rule is pinned through the pure `admitPrefix` helper.
- Exposing the bulk load as an MCP meta-tool, or changing the MCP server, which does not call this endpoint (§7.6.2).
- Editing proposal 0048. This proposal replaces 0048's text in the tree after 0048 lands (D12).
- SDK tests for the per-item quota error (TEST-4, dropped). Neither SDK has code that depends on the error code. Python's `_batch_result_from` sends every per-item `error` through `_registry_error_from_envelope`, which special-cases only `registry.read_only` and turns every other code into a `RegistryError` carrying `retryable`, `details`, and `suggested_action` (`sdks/podium-py/podium/client.py:66-90`, `:456-477`), and `BatchResult.materialize` raises it for any non-ok status (`:363-364`). TypeScript keeps the raw envelope (`sdks/podium-ts/src/index.ts:635-642`), and `materialize()` throws `registryErrorFromEnvelope` with `retryable` and `suggested_action` passed through (`:661-671`). The behavior is already pinned with other codes: per-item parsing and the re-raise from `materialize()` in `sdks/podium-py/tests/test_errors.py:41-52` and `:93-110` and in `sdks/podium-ts/src/errors.test.ts:35-46` and `:87-105`, `retryable=True` parsing in `test_errors.py:23-29`, and partial failure without a raise or throw in `sdks/podium-py/tests/test_client.py:452-466` and `sdks/podium-ts/src/index.test.ts:396-419`. Swapping `registry.not_found` for `quota.materialize_rate_exceeded` runs the same lines. No SDK line changes (D8), so `.claude/rules/test-coverage.md` asks for no new SDK test, and naming one code in each SDK docstring would go stale because any code can appear per item. TEST-1 and TEST-3 pin the server behavior where it lives, and DOC-1(b) carries the reader-facing note.
- A manual-validation scenario for the bulk-load charge (MV-1, dropped). TEST-3 runs the same compiled binary with the same boot (`serve --standalone --layer-path` and `PODIUM_QUOTA_MATERIALIZE_RATE`) and asserts the 200 status, the served prefix, and the refused item's code and `retryable` flag, and the SDK step would repeat existing SDK coverage. The drafted rationale, that the bypass is visible from curl, does not justify a hand-run scenario: scenarios in `test/manual-validation.md` carry a "Why by hand" section naming something end-to-end tests cannot drive, as S83 does with the `podium sync` watcher (`test/manual-validation.md:10197-10204`), and a quota refusal is an HTTP status plus an envelope. Proposal 0048 dropped its own MV-1 for the same reasons, including that an "immediately refused" hand-run step depends on curl latency because buckets refill continuously (`pkg/registry/server/rate_limit.go:46-63`). The scenario would also add to the S-number contention: S84 and S85 are taken in the tree (`test/manual-validation.md:10306`, `:10415`), and proposals 0046 and 0047 also claim them. The S59 note about `PODIUM_MAX_USER_LAYERS` is a prose check, and the Manual validation section records that S59 stays accurate.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Drafting reconciliation (2026-10-03)

- **The draft's charge timing conflicted with CODE-1.** D2 said each charge happens immediately before the item's load, while CODE-1 makes `admitPrefix`, which charges every admitted item before the first load, the only form. D2 now states that items are charged in request order before they are loaded, and SPEC-1 states only the order, which is the observable rule.
- **TEST-1's `admitPrefix` unit test was conditional on a helper CODE-1 had made optional.** CODE-1 now makes `admitPrefix` the only form, so the unit test is unconditional.
- **D8 said the SDK change was tests and docstrings, while TEST-4 was dropped and its reasons reject docstring edits.** D8 now states that no SDK code, test, or docstring changes.
- **D14 said SPEC-2 names the code.** SPEC-2 no longer adds a sentence naming it, so D14 cites SPEC-1 and the 0048 §13.12 row.
- **SPEC-3 offered two treatments of the single-tenant cap.** Settling it, so that a single-tenant registry ignores the stored record, would require moving `TestLayerCap_TenantQuotaOverride` and `test/integration/layer_cap_test.go` onto multi-tenant endpoints, contradicting TEST-2(f), and would conflict with D10, the CL-1 entry, the reduced TEST-3, and the Non-goals bullet on the single-tenant record. The proposal keeps OQ-1 open, and the §13.12 row, the CLI reference row, and CL-1 state that a single-tenant registry enforces a non-zero stored `max_user_layers` ahead of the variable.

### Pass 1 (2026-10-03, automated)

- **The 0048 `PODIUM_QUOTA_MATERIALIZE_RATE` rows in §13.12 and `docs/reference/cli.md` still said an over-limit request is refused, contradicting the 200 bulk-load response.** The proposal had assumed those rows name only the `load_artifact` refusal, but 0048 SPEC-2 and DOC-1(b) state "A request over the limit is refused with `quota.materialize_rate_exceeded`." without qualification. SPEC-2 now has a SPEC-2(b) edit that rewrites that sentence in the §13.12 row to name the `load_artifact` refusal and the per-item report inside the batch's 200 response, and DOC-1(d) makes the same edit to the `docs/reference/cli.md` environment row. The gap section, the SPEC-2 closing paragraph, D12, D14, the Summary, the S2 checklist step, the Watch-out bullet on 0048 text, the refused-item edge-case row, and the CL-1 `Documentation` entry follow.
