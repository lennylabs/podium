# Proposal 0047: Route the §7.3.1 layer endpoints by the request's tenant on a multi-tenant registry

- Issue: (to be filed)
- Status: Applied to spec (2026-10-03). Approved on 2026-10-03 and decided on the user's behalf under the overnight authorization. Signed off as staged. OQ-1: keep both webhook route forms as drafted (single-tenant URLs unchanged; the tenant-qualified form only on a multi-tenant registry), so no single-tenant operator re-registers. OQ-2: the existing §7.6 behavior stands and DOC-1(a) documents the stream-versus-list difference; a containment rule, if wanted, is a separate decision proposal. OQ-3: refuse erasure on a multi-tenant registry as staged; tenant-scoped audit records or a per-tenant audit sink is recorded as a follow-up.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

The problem statement raised two items. Item 1, the multi-tenant layer endpoints, is a code defect against §6.3.1, and this proposal fixes it. Item 2, the `layer.config_changed` audience on the change-event stream, is withdrawn: the spec edit it needed (SPEC-1) was dropped in review because it contradicts §7.6 and §7.5.4, and without that edit the stream gate (CODE-5) would be code with no spec basis. The Non-goals record both, and the Edge cases table records the stream behavior the proposal accepts.

## Summary

**What changes.**

- §7.3.1 gains a "Tenant selection" paragraph, the webhook-URL sentence names the tenant ID on a multi-tenant registry, the layer-object sentence says the endpoints serve the tenant §6.3.1 selects per request, and the Layer read visibility whole-list sentence is made subject to Tenant selection. §8.5 states that erasure is refused with `403 auth.forbidden` on a multi-tenant registry, because the redaction would reach audit records outside the requesting tenant's §4.7.1 audit stream. §7.3.4 states that `manage_any_layer` follows the same tenant selection. §13.10 states that the layer panel presents the registration control to a caller that resolves to no tenant and leaves the Tenant selection refusal to the registry (SPEC-2).
- `pkg/registry/server/server.go` extracts the §6.3.1 routing decision from `withTenantRouting` into one helper and exposes `(*Server).TenantRouted(http.Handler)` for the layer routes and `(*Server).TenantRoutedNoReject(http.Handler)`, which never refuses, for the posture read (CODE-1).
- `pkg/registry/server/layers.go` resolves its tenant per request, refuses an unrouted write with `403 auth.forbidden` before the admin gate, refuses erasure on a multi-tenant endpoint, and takes the event and notification tenant from the stored layer record. `pkg/registry/server/layer_capabilities.go` reports `manage_any_layer` false for an unrouted request on a multi-tenant endpoint (CODE-2). The response of every route for every caller and provider class is in the Decisions Unrouted outcome table.
- The inbound webhook gains the multi-tenant route `/v1/ingest/webhook/{tenant-id}/{layer-id}` and the advertised URL carries the tenant ID; a single-tenant registry keeps `/v1/ingest/webhook/{layer-id}` (CODE-3).
- `internal/serverboot/serverboot.go` mounts `/v1/layers` and `/v1/layers/` through `srv.TenantRouted`, leaves `/v1/admin/erase` and `/v1/ingest/webhook/` unwrapped, mounts the §7.3.4 posture read `/v1/ui/session` through `srv.TenantRoutedNoReject`, and enables per-request tenant resolution on the layer endpoint when `PODIUM_MULTI_TENANT` is set (CODE-4).
- Tests, docs, changelog, and manual validation follow: an in-process suite in `pkg/registry/server` whose fixture binds the endpoint to a poisoned boot tenant that no org routes to, standard-stack end-to-end cases on the binary, the HTTP API reference, CLI reference, and deployment pages, the `[Unreleased]` entries, and scenario S85 (TEST-1, TEST-3, DOC-1, CL-1, MV-1).

**Fixed decisions.**

- Item 1 is a code defect against §6.3.1. SPEC-2 adds no new surface.
- One tenant per request serves the store reads and writes, the admin gate, the user-defined layer cap, and the reorder event scope. Per-layer events and the ingest-failure notification take the tenant from the stored layer record.
- One routing helper serves both the meta-tool chain and the layer routes. No second copy of the rule exists.
- The layer routes do not pass through `withIdentityVerification`. `TenantRouted` treats a verification error as unrouted and leaves the §7.3.1 credential-failure envelopes to the endpoint.
- The Unrouted outcome table in Decisions fixes every route's response for every caller and provider class on a multi-tenant registry. A rejecting provider is keyed by `rejectUnknownTenant`, which every verifier-installing provider except `trusted-headers` sets, rather than by a provider name.
- `POST /v1/admin/erase` on a multi-tenant registry is refused with `403 auth.forbidden` for every caller, before the admin gate, any store access, and any audit rewrite. The erase route is not tenant-routed (Unrouted outcome table, row U1). The registry keeps one audit file for every tenant, while §4.7.1 and §6.3.1 give each tenant its own audit stream, so a tenant admin's redaction would rewrite other tenants' records. OQ-3 records what lifting the refusal requires.
- The §7.3.4 `layer_capabilities.manage_any_layer` member is evaluated in the request's routed tenant and is false for an unrouted request on a multi-tenant registry, so the posture read and the layer write gate stay one expression on the admin arm. The posture read refuses no request (Unrouted outcome table).
- The web UI is unchanged. The posture read still reports `subject` for an authenticated caller that resolves to no tenant, so the layer panel still offers that caller the registration control through the owner arm (`web/ui/src/surfaces/layerrights.ts:75-77`, `:92`). SPEC-2 part 4 makes §13.10 present that control and leave the Tenant selection refusal to the registry. No posture member is added.
- A single-tenant registry is unchanged in every response, including its webhook URL and its erasure.
- The webhook path segment is the tenant ID (the store key), never the tenant name. The handler requires an active provisioned tenant and otherwise answers `404 registry.not_found`.
- The §4.7.8 audit-volume meter stays keyed by the boot tenant, which diverges from the §4.7.8 per-tenant cap (`spec/04-artifact-model.md:905`). The divergence predates this proposal, is recorded in the Non-goals, and is documented in DOC-1(e). `internal/serverboot/reingest.go` is not edited.
- The change-event stream rule in §7.6 is unchanged. No `layer.config_changed` gate is added.
- No new §6.10 code, environment variable, flag, SPI, meta-tool, or SDK change. Pre-1.0, the `webhookURL` and webhook-route changes carry no compatibility shim.

**Watch out for.**

- **Proposal 0046 has landed (PR #144).** Its text states that on a multi-tenant registry layer and ingest events belong to the bootstrap `default` tenant: the sentence "On a multi-tenant registry, layer and ingest events belong to the bootstrap `default` tenant, so only that tenant's receivers receive them." in `docs/reference/http-api.md` (Outbound webhooks paragraph), and the doc comment on `TestWebhookReceivers_MultiTenantDeliveryIsolation` in `test/e2e/webhook_tenant_isolation_test.go` ("because the layer endpoint writes to the boot tenant, so the binary can produce only that direction"). Routing the layer endpoints makes both false. Rewrite the doc sentence to say a layer or ingest event belongs to the tenant whose layer it names, and update the test comment (and, where the binary can now produce an event of a non-default tenant, extend that test to assert the other direction) in the DOC-1 and TEST-3 steps.
- **The two tenants disagree today, and fixing one alone is worse.** The store uses the fixed `e.tenantID` (the bootstrap default tenant, `internal/serverboot/serverboot.go:1484`), while `registry.AdminAuthorize` reads `core.TenantFor(ctx)`, which on a multi-tenant boot falls back to `podium:unrouted` (`pkg/registry/core/core.go:217-222`, `internal/serverboot/serverboot.go:1106-1109`). Routing the context without switching the store reads would let a tenant admin write the default tenant's rows. CODE-2 changes both through one resolver.
- **`requireTenant` must run before the admin callback.** On a multi-tenant registry started in public mode or with no identity provider, the callback admits every caller (`internal/serverboot/serverboot.go:1499-1501`), and no store refuses a write under the never-provisioned `podium:unrouted` tenant (memory keys rows by tenant ID, and the SQLite `layer_configs` table has no foreign key). Put the check after `rejectIfReadOnly`, and after `verifiedCaller` on `reorder` and `list`, so the §7.3.1 credential-failure envelopes still take precedence.
- **Do not take the request tenant in `notifyIngestFailure` or `layerEventScope`.** The webhook path reaches `notifyIngestFailure` through `runIngestAndRespond` with no routed context, because CODE-4 leaves the webhook route unwrapped. Both helpers read `cfg.TenantID`, which every store read fills.
- **The posture read must be routed too.** `layer_capabilities.manage_any_layer` is `e.authAdmin(r) == nil` (`pkg/registry/server/layer_capabilities.go:24-26`), mounted at `/v1/ui/session` on the boot mux (`internal/serverboot/serverboot.go:1561-1572`). Routing the layer routes alone leaves the posture read checking the admin grant in `podium:unrouted` while the layer write checks the routed tenant, and the web UI layer panel renders its controls from that value (`spec/13-deployment.md:179`). CODE-2 and CODE-4 change both.
- **Erasure on a multi-tenant registry is refused outright.** `audit.EraseUser` rewrites the one registry audit file (`pkg/registry/server/layers.go:952`). Do not admit a routed tenant admin's erase until OQ-3 is resolved; the refusal runs before `authAdmin`, so an always-admit callback does not reach the rewrite.
- **Do not move the layer routes under `srv.Handler()`.** `withIdentityVerification` refuses a failed credential before the handler runs, which would answer `auth.untrusted_*` on `update`, `restore`, `unregister`, and `reingest`, where §7.3.1 fixes `403 auth.forbidden`.
- **Do not change the reingest meter key alone.** The meter is recorded under the boot tenant (`internal/serverboot/audit_emitter.go:63-69`, `internal/serverboot/serverboot.go:1606`). Checking `Allow` under `lc.TenantID` while `Record` stays on the boot tenant leaves every routed tenant's counter at zero, so the quota never refuses.
- **The tenant name is not the tenant ID.** Tenants are keyed by a UUIDv5 ID, and the name is an alias (`internal/serverboot/orgid.go:44-71`). The webhook segment, the tests, and the docs all use the ID, and the default tenant's segment is `OrgIDForName("default")`, never the string `default`.
- **`TestEventStream_MultiTenantRouting` keeps passing, and its comment goes stale.** It registers layers on the owner arm because "a multi-tenant registry refuses" admin layer writes (`test/e2e/events_visibility_test.go:196-206`). After this fix that clause is false. Update the comment and leave the assertions alone. The test also relies on a bare `globex` org being unprovisioned (`:237`), so TEST-3 provisions only suffixed names.
- **Proposal 0046 edits nearby code.** It re-keys webhook receivers per routed tenant and also reads `withTenantRouting`. Whichever lands second rebases onto the extracted `routeTenant` helper. 0046 also takes manual-validation number S84, so this proposal uses S85.
- **A read left on the boot tenant does not fail on its own.** On a multi-tenant endpoint the boot tenant is a provisioned tenant, so a missed `e.tenantID` read returns or changes valid rows. After CODE-2, `grep -n 'e\.tenantID' pkg/registry/server/*.go` must report only the single-tenant arm of `tenant`, and TEST-1's poisoned boot tenant `P` observes every residual read except the four that cases 2 and 4 assert explicitly.
- **`injected-session-token` also rejects an unknown org, and §6.3.1 does not say so.** The router sets `rejectUnknownTenant` for every verifier-installing provider except `trusted-headers` (`internal/serverboot/serverboot.go:1454`). The Unrouted outcome table keys row U1 by that flag, the shared helper keeps the behavior, and the end-to-end case uses `oidc-jwt`, so no test pins behavior the spec does not state. The Non-goals record the spec gap.
- **Parts of `test/e2e` skip silently on macOS, and the standard-stack cases skip without Postgres and S3.** `TestLayerEndpoint_MultiTenantUnknownOrgRejected` also skips on darwin through `requireCustomTrustStore`, so it runs only on the CI Linux lane. Grep the run output for `SKIP` before claiming end-to-end verification.
- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-2. §7.3.1 gains Tenant selection, the tenant-qualified webhook sentence, the per-request layer-object wording, and the Tenant selection qualifier on the Layer read visibility whole-list sentence; §8.5 gains the multi-tenant erasure refusal bullet; §7.3.4 qualifies `manage_any_layer` by Tenant selection; §13.10 gains the layer-panel exception for the Tenant selection refusal.
      Levels: —. Depends on: —
- [ ] **S2 · code** — CODE-1. `routeTenant`, `writeTenantUnknown`, and the exported `(*Server).TenantRouted` and `(*Server).TenantRoutedNoReject` land in `pkg/registry/server/server.go`; `withTenantRouting` calls the helper.
      Levels: unit, integration. Depends on: S1
- [ ] **S3 · code** — CODE-2. `LayerEndpoint` resolves its tenant per request, refuses unrouted writes, refuses erasure on a multi-tenant endpoint, `Capabilities` reports false for an unrouted request, and `core.TenantFromContext` is exported. After it, `e.tenantID` is read only in the single-tenant arm of `tenant`.
      Levels: unit, integration. Depends on: S1
- [ ] **S4 · code** — CODE-3. The tenant-qualified webhook route and URL on a multi-tenant endpoint.
      Levels: unit, integration. Depends on: S3
- [ ] **S5 · code** — CODE-4. serverboot mounts the layer routes through `TenantRouted`, leaves the erase and webhook routes unwrapped, mounts the posture read through `TenantRoutedNoReject`, and calls `WithTenantRouting()` when `PODIUM_MULTI_TENANT` is set.
      Levels: integration, e2e. Depends on: S2, S3, S4
- [ ] **S6 · test** — TEST-1. In-process routing, refusal, erasure, posture, no-router, and webhook suite in `pkg/registry/server`, built through `newTenantRoutingFixture` over the poisoned boot tenant `P`.
      Levels: unit, integration. Depends on: S2, S3, S4
- [ ] **S7 · test** — TEST-3. `TestLayerEndpoint_MultiTenantRouting`, `TestLayerEndpoint_MultiTenantNoRouter`, and `TestLayerEndpoint_MultiTenantUnknownOrgRejected` (under `oidc-jwt` with the `startOIDCTestIdP` TLS IdP, Linux only) on the standard-stack binary, and the stale comment in `TestEventStream_MultiTenantRouting`.
      Levels: e2e. Depends on: S5
- [ ] **S8 · docs** — DOC-1. HTTP API reference, deployment, layer, access-control, and CLI pages. Land in the same pull request as S5 so no released build documents behavior it lacks.
      Levels: —. Depends on: S5
- [ ] **S9 · docs** — CL-1. The `[Unreleased]` `Fixed`, `Changed`, and `Documentation` entries.
      Levels: —. Depends on: S5
- [ ] **S10 · docs** — MV-1. Manual-validation scenario S85.
      Levels: manual. Depends on: S5

**Ordering constraints.** S2 and S3 are independent and may proceed in parallel. S4 needs the multi-tenant field S3 introduces. S5 compiles only against S2, S3, and S4. Until S5 lands, no binary behavior changes, because serverboot neither wraps the routes nor enables per-request resolution.

## Current state and the gap

### The layer routes bypass tenant routing

On every boot, serverboot mounts `/v1/layers`, `/v1/layers/`, `/v1/ingest/webhook/`, and `/v1/admin/erase` directly on the outer boot mux (`internal/serverboot/serverboot.go:1505-1514`). The meta-tool handler `srv.Handler()` is mounted only at the catch-all `/` (`:1580`). Go's `ServeMux` sends each request to the most specific matching pattern, so these requests never pass through `withTenantRouting` (`pkg/registry/server/server.go:440`, `:452-485`). That middleware is the only place `core.ContextWithTenant` is attached (`server.go:475`). The outer wrapper at `serverboot.go:1748` adds otelhttp, `SecurityHeaders`, and `BrowserOriginGate`, and it attaches no tenant.

### Two tenants, neither the caller's

On a multi-tenant boot the layer endpoint resolves two different tenants.

- The endpoint is built as `server.NewLayerEndpoint(st, tenantID, mode)` (`internal/serverboot/serverboot.go:1484`), where `tenantID` is the bootstrap default tenant `OrgIDForName("default")` (`serverboot.go:887`; `internal/serverboot/orgid.go:64-71`). Every store read and write and every published `EventScope` uses that fixed `e.tenantID`: `pkg/registry/server/layers.go:351`, `:358`, `:453`, `:586`, `:924`, `:934`, `:1005`, `:1262`, `:1313`, `:1366`, `:1417`, `:1447`, `:1476`, `:1498`, `:1512`, `:1550`, `:1581`, `:1596`, `:1652`, `:1834`, and `pkg/registry/server/webhook_ingest.go:46`.
- The admin gate calls `registry.AdminAuthorize(r.Context(), layerIdentity(r))` (`serverboot.go:1494-1503`), which checks `IsAdmin` against `r.TenantFor(ctx)` (`pkg/registry/core/admin.go:25`). With no tenant on the context, `TenantFor` falls back to the core registry's bound tenant (`core.go:217-222`), which is `podium:unrouted` on a multi-tenant boot (`serverboot.go:1106-1109`; `orgid.go:16`). The bootstrap admin grants are seeded in the default tenant (`serverboot.go:898`; `seedBootstrapAdmins` at `:419-423`).

The store reads therefore resolve to the default tenant, and only the admin gate resolves to `podium:unrouted`. The consequences are as follows.

- (a) Wherever an identity provider is configured and public mode is off, every admin-arm layer operation on a multi-tenant registry is refused, bootstrap admins included.
- (b) A routed tenant's list, user-layer register, update, reorder, restore, unregister, reingest, and erase read and write the default tenant's rows. None of these operations reaches the caller's own tenant.
- (c) Layer events are scoped to the default tenant. `EventAudience.Visible` requires an exact tenant match (`pkg/registry/core/event_visibility.go:95`), and the subscriber's tenant is `s.core.TenantFor` of its routed request (`pkg/registry/server/events.go:143`). A subscriber routed to any other tenant never receives these events.

### The spec already requires per-request routing

§6.3.1 Per-request tenant selection and `PODIUM_MULTI_TENANT` in §13.12 apply to every request. The only explicit exemption covers the §7.3.3 tenant-management endpoints (§4.7.1 Provisioning), which matches `server.go:466-469`. Item 1 is therefore a code defect against the current spec.

The spec leaves the following consequences open, and SPEC-2 closes them:

- which outcome an unrouted layer request gets, including on a registry started in public mode or with no identity provider configured, where the §7.3.1 whole-list and admin-admission rules would otherwise admit it;
- what §8.5 erasure does on a multi-tenant registry, where §4.7.1 gives each tenant its own audit stream and the registry writes one audit file for every tenant;
- which tenant the §7.3.4 `manage_any_layer` member is evaluated in, and how the §13.10 layer panel treats a registration control it offers to a caller that resolves to no tenant;
- how the inbound webhook, which carries a per-layer secret and no caller organization, selects its tenant.

The last point matters because the fix otherwise breaks webhooks for every non-default tenant. The advertised URL is `/v1/ingest/webhook/{id}` (`layers.go:179-184`), and the handler looks the layer up under `e.tenantID` (`webhook_ingest.go:46`). Layer rows are keyed by `(tenant_id, id)` in every backend, the store has no lookup across tenants, and layer IDs can collide between tenants, so the URL has to carry the tenant.

### Item 2 and why it is withdrawn

`GET /v1/layers` applies `readableBy` (`layers.go:254-271`): the whole list goes to a caller the admin callback admits, an empty list goes to a caller that resolves no verified subject, and `layer.VisibleWith` applies to everyone else. The stream applies the §7.6 rule, which follows the subscriber's own §4.6 view. An anonymous subscriber on a registry that verifies callers therefore receives `layer.config_changed` for every `public: true` layer while `GET /v1/layers` lists none to it (`pkg/layer/composer.go:65-71`; `internal/serverboot/identity_verify.go:254`, `:262`, `:287-304`). In the free-form provider-label posture no `layer.config_changed` can be published through the binary, because every layer write is refused there (`pkg/registry/core/admin.go:21-23`).

The current spec states this behavior. §7.6 says the stream delivers where "the §4.6 evaluator reports a layer recorded for the event visible" and that the bypasses "apply on the same terms", and it states its divergence from the §7.3.1 layer read rule for admins. Changing it would need a §7.6 edit that also covers `layer.ingested`, `layer.history_rewritten`, and the §7.5.4 withdrawal wake, which is a separate decision (OQ-2). This proposal documents the behavior in DOC-1(a) and changes no stream code.

## Decisions

- **Item 1 is a code defect against §6.3.1.** SPEC-2 states behavior §6.3.1 already implies, closes the points the spec leaves open, and adds no new surface.
- **One tenant per request.** Every §7.3.1 layer-management route (`/v1/layers` with GET, POST, and DELETE, plus `/reorder`, `/reingest`, `/update`, and `/restore`) and the §7.3.4 posture read resolve one tenant per request. It is the context tenant `routeTenant` attaches. The layer routes read it through `TenantRouted`, and the posture read through `TenantRoutedNoReject`, which is the non-rejecting routing result. That result equals the `TenantRouted` result wherever `TenantRouted` admits the request, and it is unrouted where `TenantRouted` answers `401` (Unrouted outcome table, row U1). The erase route and the webhook route read no context tenant. It serves the store reads and writes, the admin gate (`AdminAuthorize` through `core.TenantFor`), the `manage_any_layer` capability, the per-tenant user-layer cap, and the reorder `EventScope`. Per-layer events and the ingest-failure notification take the tenant from the stored record's `TenantID`, which equals the request tenant on a routed request and is the only tenant available on the webhook path.
- **One routing helper.** The routing decision is extracted from `withTenantRouting` into one helper in `pkg/registry/server`, used by both the meta-tool chain and the layer routes.
- **A wrapper that does not verify up front.** `(*Server).TenantRouted` resolves the identity through the installed verifier, treats a verification error as unrouted, and leaves the refusal to the endpoint as today. §7.3.1 fixes which layer operations refuse a failed credential with its own envelope (`list` and `reorder`) and which answer `auth.forbidden` (the other writes), so `withIdentityVerification` cannot be reused.
- **Unrouted outcome table.** This table is the single statement of the response each route gives on a multi-tenant registry, and every other section cites it. A rejecting provider is one whose router sets `rejectUnknownTenant`, which is every provider that installs a verifier except `trusted-headers` (`internal/serverboot/serverboot.go:1450-1455`): `oidc-jwt`, which §6.3.1 names, and `injected-session-token`, which §6.3.1 does not name (Non-goals). Layer write means register, update, restore, unregister, reorder, and reingest. Posture means `layer_capabilities.manage_any_layer` on `GET /v1/ui/session`.

  | Caller | Layer read | Layer write | `POST /v1/admin/erase` | Posture | Pinned by |
  |:--|:--|:--|:--|:--|:--|
  | R. Org resolves to an active tenant T | T's list under Layer read visibility | Acts in T; admin gate in T | `403 auth.forbidden` | `200`; admin gate evaluated in T | TEST-1 1–4, 10; TEST-3 Routing 2–5, 7 |
  | U1. Authenticated, org names no active tenant, rejecting provider | `401 auth.tenant_unknown`, `details.token_org_id` (from `TenantRouted`) | Same `401` | `403 auth.forbidden` (route not tenant-routed) | `200`, `subject`, false (`TenantRoutedNoReject`) | TEST-1 2, 5, 10; TEST-3 UnknownOrgRejected 2–4 |
  | U2. Authenticated, org names no active tenant, `trusted-headers` | `200`, `[]` | `403 auth.forbidden` | `403 auth.forbidden` | `200`, `subject`, false | TEST-1 8, 10; TEST-3 Routing 6–7 |
  | U3. Authenticated, no org value, any provider (`server.go:470`) | `200`, `[]` | `403 auth.forbidden` | `403 auth.forbidden` | `200`, `subject`, false | TEST-1 8, 10 |
  | U4. No credential, under a provider that treats it as anonymous (every provider except `injected-session-token`, `identity_verify.go:197-204`) | `200`, `[]` | `403 auth.forbidden` | `403 auth.forbidden` | `200`, no `subject`, false | TEST-1 8, 10 |
  | U5. Credential fails verification, including no credential under `injected-session-token` | The verification envelope | `reorder`: the verification envelope; other writes: `403 auth.forbidden` | `403 auth.forbidden` | `200`, no `subject`, false | TEST-1 6 |
  | U6. No router installed: no identity provider, public mode, or a provider label with no verifier (`serverboot.go:1450`) | `200`, `[]` | `403 auth.forbidden` | `403 auth.forbidden` | `200`, false | TEST-1 11; TEST-3 NoRouter |

  Precedence is per route, and it follows the order of the existing guards. The U1 `401` from `TenantRouted` comes first on the layer routes. On `list`, the U5 envelope comes next. On `reorder`, the U5 envelope comes next and `registry.read_only` after it, because `reorder` calls `verifiedCaller` before `rejectIfReadOnly` (`pkg/registry/server/layers.go:1536-1540`). On the other writes and on erase, `registry.read_only` comes next (`layers.go:899`, `:997`, `:1150`, `:1436`, `:1490`, `:1623`). The unrouted `403` (`requireTenant`, or the erase `multiTenant` check) follows on every route, and the admin gate comes last. The posture read refuses no request. The U2 to U4 and U6 `403` overrides the public-mode and no-provider admission, which would otherwise write rows into the never-provisioned `podium:unrouted` tenant.

  The webhook carries no caller, so the rows do not apply to it. On `POST /v1/ingest/webhook/{tenant-id}/{layer-id}`, an active provisioned tenant and an existing layer go to the HMAC check (`401 ingest.webhook_invalid` on a bad signature) and then to ingest in that tenant. An unknown or inactive tenant, or a layer absent from that tenant, answers `404 registry.not_found`. The single-segment route answers `404` from the mux. TEST-1 case 9 and TEST-3 Routing step 4 pin these outcomes.

  A single-tenant registry installs no router and leaves `multiTenant` unset, so every cell keeps today's behavior, including erasure and the single-segment webhook. TEST-1 case 7 pins it.
- **Single-tenant is unchanged.** No router is installed (`serverboot.go:1450`), `TenantRouted` returns its handler unchanged, and the endpoint's tenant resolves to the boot tenant.
- **The webhook URL names the tenant ID on a multi-tenant registry.** A delivery carries no caller organization, so the advertised URL is `/v1/ingest/webhook/{tenant-id}/{layer-id}`, where the tenant segment is the store key. The handler resolves only an active provisioned tenant. A single-tenant registry keeps `/v1/ingest/webhook/{layer-id}`. The per-mode form breaks no single-tenant registration. Using one tenant-qualified form everywhere is the alternative OQ-1 names.
- **Erasure is refused on a multi-tenant registry.** `erase` lists and purges layers under the fixed `e.tenantID` (`pkg/registry/server/layers.go:924`, `:934`) and redacts through `audit.EraseUser` over the one registry audit file every tenant shares (`:952`). §4.7.1 gives each org its own audit stream (`spec/04-artifact-model.md:788`), and §6.3.1 selects that stream per request (`spec/06-mcp-server.md:75`), so a routed tenant admin's redaction would rewrite records written under other tenants, which breaks the §4.7 tenant boundary. Today the erase admin gate checks `podium:unrouted`, so every erase on a multi-tenant registry with a verifying provider is already refused; a public-mode or no-provider multi-tenant registry admits it and rewrites the whole file. The fail-closed choice refuses every erase on a multi-tenant endpoint with `403 auth.forbidden` before the admin gate, any store access, and any audit rewrite, and keeps a single-tenant registry unchanged. Admitting a routed tenant admin's erase under a stated deployment-wide reach would contradict §4.7.1 and §6.3.1, and restricting the redaction to the routed tenant's records is not possible, because `audit.Event` carries no tenant (`pkg/registry/server/audit_context.go:169-176`). OQ-3 records the constraint any later lifting of the refusal satisfies.
- **The posture read follows the layer gate.** `LayerEndpoint.Capabilities` reports `manage_any_layer` as `e.authAdmin(r) == nil` (`pkg/registry/server/layer_capabilities.go:24-26`), and §7.3.4 requires it to report whether the layer endpoints admit the caller on the admin arm (`spec/07-external-integration.md:230`). Once the layer endpoints check the routed tenant and refuse an unrouted write, the posture read must evaluate in the same tenant and report false where `requireTenant` refuses, or the web UI would hide admin-arm controls that succeed in the caller's routed tenant and offer admin-arm controls that are refused. The member covers the admin arm alone. The web UI predicts the owner arm from the posture `subject` (`web/ui/src/surfaces/layerrights.ts:92`), and the posture read still reports `subject` for an authenticated caller that resolves to no tenant, so the panel still offers that caller the registration control (`web/ui/src/surfaces/layerrights.ts:75-77`; `web/ui/src/App.tsx:339`; `web/ui/src/surfaces/LayerPanel.tsx:335`). Such a caller lists no layers, so registration is the only write control the panel can offer it. SPEC-2 part 4 records that control as a §13.10 exception the registry answers, because the alternative, a new posture member that reports whether the request resolves to a tenant, adds a §7.3.4 surface and a web UI change for one control whose refusal the registry already states. Mounting `/v1/ui/session` through `TenantRoutedNoReject` routes the request, and `Capabilities` checks `e.tenant` before `authAdmin`. The mount never answers `401 auth.tenant_unknown`, because the unedited §7.3.4 lead paragraph (`spec/07-external-integration.md:223`) says the read refuses no request for lack of a credential, verifies a carried credential only to report `subject` and `email` and to evaluate `layer_capabilities`, and answers `200` to a request that resolves no subject. The posture cells of the Unrouted outcome table follow from this. The routing result is used only to evaluate `layer_capabilities`, so the read discloses no tenant data.
- **The audit-volume meter stays as it is, and its divergence is recorded.** §4.7.8 defines the audit-volume limit as a per-tenant daily cap (`spec/04-artifact-model.md:905`), while the meter records the events it counts under the boot tenant (`internal/serverboot/serverboot.go:1603-1606`) and the reingest path checks the same key (`internal/serverboot/reingest.go:39`). On a multi-tenant registry the cap is therefore counted deployment-wide, before and after this proposal. Per-tenant keying needs `Record` keyed by each event's tenant, which is outside this defect; the Non-goals record the gap and DOC-1(e) documents it.
- **No new surface.** No §6.10 code, environment variable, flag, SPI, meta-tool, or SDK change. Pre-1.0, the `webhookURL` and webhook-route changes carry no shim.

## Spec amendment: §7.3.1 tenant selection and the multi-tenant webhook URL

**SPEC-2, part 1.** `spec/07-external-integration.md`, §7.3.1 "Authoring and Ingestion". Four anchors.

(a) The paragraph after the `podium layer` command block (line 111 at the time of writing) currently reads:

> `podium layer register` returns the webhook URL and HMAC secret to register on the source repo. Registering a Git source without configuring the webhook leaves the layer at its initial commit until the first manual reingest.

Insert after its first sentence:

> On a multi-tenant registry, the URL carries the layer's tenant ID, because a delivery carries no caller organization from which §6.3.1 could select the tenant. A delivery naming a tenant that is not provisioned and active is refused as naming no layer.

(b) The paragraph that begins "The layer object never carries the layer's inbound webhook HMAC secret under any name" (line 115). Its last sentence currently reads:

> The layer object also carries no tenant identifier, under §7.2.1: the layer-management endpoints serve one tenant, so the value would be constant across every record and would republish the registry's stored tenant identifier on a read that is not admin-gated.

Replace it with:

> The layer object also carries no tenant identifier, under §7.2.1: the layer-management endpoints serve the one tenant §6.3.1 selects for the request, so the value would be constant across every record of a response and would republish the registry's stored tenant identifier on a read that is not admin-gated.

(c) The paragraph "**Layer read visibility.**" (line 121). Its first sentence currently reads:

> The layer read operation `list` returns, on both its live and its soft-deleted arm, the tenant's whole layer list to a caller holding the §4.7.2 admin role, and to every caller on a registry started with no identity provider configured or one started in public mode (§13.10), which authenticates no caller.

Replace it with:

> The layer read operation `list` returns, on both its live and its soft-deleted arm, the tenant's whole layer list to a caller holding the §4.7.2 admin role, and to every caller on a registry started with no identity provider configured or one started in public mode (§13.10), which authenticates no caller, subject to the Tenant selection rule on a multi-tenant registry.

(d) Insert a new paragraph after the "**Layer read visibility.**" paragraph and before the paragraph that begins "**Patch semantics on the update body.**":

> **Tenant selection.** On a multi-tenant registry, each layer-management operation acts in the tenant §6.3.1 selects for the request. The §4.7.2 admin check, the layer list it reads and writes, the user-defined layer cap, and the tenant §7.6 records for the change event the operation publishes all use that tenant. A request that §6.3.1 resolves to no tenant acts in the no-data tenant (§13.12), which holds no layers. `list` returns it no layers, and a write operation is refused with `403 auth.forbidden` before any layer is read. This refusal takes precedence over the whole-list rule of Layer read visibility and over the admin bypass on a registry started in public mode or with no identity provider configured. The credential-failure refusals this section states for `list` and `reorder` take precedence over this refusal.

## Spec amendment: §8.5 erasure on a multi-tenant registry

**SPEC-2, part 2.** `spec/08-audit-and-observability.md`, §8.5 "Erasure". The bullet list currently reads:

> - Unregisters and purges any user-defined layers owned by the user (and the artifacts ingested from them).
> - Redacts the user identity in audit records (replaces with `redacted-<sha256(user_id+salt)>`).
> - Preserves audit event sequencing for integrity.

Append a fourth bullet after "Preserves audit event sequencing for integrity.":

> - On a multi-tenant registry, erasure is refused with `403 auth.forbidden` for every caller, before the admin check, before any layer is read, and before any audit record is rewritten, because the redaction this section describes would reach audit records outside the requesting tenant's audit stream (§4.7.1). The erasure endpoint selects no tenant, so this refusal also answers a request whose verified organization names no provisioned tenant, in place of the §6.3.1 `auth.tenant_unknown` rejection.

The bullet records the fail-closed outcome OQ-3 selects. Its last sentence states the precedence over §6.3.1 because the refusal depends on the deployment alone, so routing the erase request would add only a second outcome for the same request; CODE-4 therefore leaves the erase route unwrapped. It leaves §4.7.1 and §6.3.1 unchanged, so the per-org audit stream they state remains the requirement any later erasure on a multi-tenant registry meets.

## Spec amendment: §7.3.4 layer capabilities on a multi-tenant registry

**SPEC-2, part 3.** `spec/07-external-integration.md`, §7.3.4, the `layer_capabilities` bullet (line 230 at the time of writing). Its sentence currently reads:

> On a registry started with no identity provider configured, or one started in public mode (§13.10), those endpoints admit every caller on that arm, so the member is true there, including on a request that resolves no subject.

Replace it with:

> On a registry started with no identity provider configured, or one started in public mode (§13.10), those endpoints admit every caller on that arm, so the member is true there, including on a request that resolves no subject. On a multi-tenant registry the member is evaluated in the tenant §6.3.1 selects for the request, under the §7.3.1 Tenant selection rule, and it is false for a request §6.3.1 resolves to no tenant, which that rule refuses on every write. The posture read applies no §6.3.1 refusal, so a request whose verified organization names no provisioned tenant is answered `200` with the member false.

## Spec amendment: §13.10 layer panel and the Tenant selection refusal

**SPEC-2, part 4.** `spec/13-deployment.md`, §13.10, the "**Layer panel**" bullet (line 179 at the time of writing). Its sentence currently reads:

> Two arms are exceptions.

Replace it with:

> The following arms are exceptions.

Then, after the sentence that currently reads:

> A refusal the target's own fields do not settle, such as a `git` repository string that resolves to the Git file transport, is presented and answered by the registry.

insert:

> On a multi-tenant registry, the §7.3.1 Tenant selection refusal is presented and answered by the registry as well. The posture read reports the subject of an authenticated caller that §6.3.1 resolves to no tenant, and the registration the dialog would build names that subject as its owner, so the panel offers that caller the registration control. Such a caller lists no layers, so the registration control is the only write control this rule offers it, and the registry refuses the registration under the §7.3.1 Tenant selection rule or the §6.3.1 rejection.

The edit replaces the counted lead-in because the bullet then carries a further exception. It leaves the panel's prediction rule and the §7.3.4 posture members unchanged, so the web UI needs no change: `mayTake` already offers registration on `caps.manage_any_layer || ownedByCaller(newLayerTarget(subject), subject)` (`web/ui/src/surfaces/layerrights.ts:75-77`, `:92`), and the panel presents a refused operation where it was attempted, as the bullet's last sentence states.

No §6.10 entry changes. SPEC-2 lands in one commit (step S1).

## Proposed solution

### CODE-1. Extract the tenant routing decision and expose a layer-route wrapper

`pkg/registry/server/server.go` (`withTenantRouting` at `:452-485`).

- Add an unexported `func (s *Server) routeTenant(r *http.Request, id layer.Identity) (ctx context.Context, reject bool)`. It returns `core.ContextWithTenant(r.Context(), tenantID)` when `id.IsAuthenticated && id.OrgID != ""` and `s.tenantRouter` resolves. It returns `reject=true` when it does not resolve and `s.rejectUnknownTenant` is set. In every other case it returns `r.Context()` unchanged.
- Move the `auth.tenant_unknown` envelope, with `details.token_org_id`, into `writeTenantUnknown(w http.ResponseWriter, orgID string)`.
- Rewrite `withTenantRouting` to call `routeTenant` and `writeTenantUnknown`. Keep the `pathRequiresIdentity` and `/v1/admin/tenants` bypasses unchanged.
- Add an exported, documented `func (s *Server) TenantRouted(next http.Handler) http.Handler`:
  - When `s.tenantRouter == nil`, it returns `next` unchanged.
  - Otherwise it resolves `id, err := s.idVerifier(r)` when a verifier is installed. A verification error, or no verifier, yields the zero `layer.Identity`, which leaves the request unrouted. The endpoint then refuses a failed credential itself.
  - It calls `routeTenant`, writes `writeTenantUnknown` on reject, and otherwise serves `next` with the returned context.
- Add an exported, documented `func (s *Server) TenantRoutedNoReject(next http.Handler) http.Handler` for the §7.3.4 posture read. It behaves as `TenantRouted` except that it ignores `reject`: an org that `routeTenant` does not resolve leaves the request unrouted, and the wrapper serves `next` with `r.Context()`. Its doc comment says §7.3.4 refuses no posture request, so the read reports `manage_any_layer` false where the layer endpoints answer `401 auth.tenant_unknown`. Cite `// Spec: §7.3.4, §6.3.1`.
- `routeTenant`, `TenantRouted`, and `withTenantRouting` carry `// Spec: §6.3.1, §7.3.1`. The doc comment on `TenantRouted` records why it does not reuse `withIdentityVerification`.

### CODE-2. `LayerEndpoint` resolves its tenant per request and refuses unrouted writes

`pkg/registry/server/layers.go` (`NewLayerEndpoint` `:189`, `lookupLayerForWrite` `:350`, `layerEventScope` `:452`, `effectiveLayerCap` `:582`, `erase` `:893`, `update` `:991`, `register` `:1149`, `list` `:1408`, `restore` `:1430`, `unregister` `:1489`, `reorder` `:1530`, `reingest` `:1617`, `notifyIngestFailure` `:1827`), `pkg/registry/server/layer_capabilities.go` (`Capabilities` `:24`), and `pkg/registry/core/core.go`.

- Export `core.tenantFromContext` as `core.TenantFromContext(ctx) (string, bool)` and update its callers in `core`. The endpoint reads the context through it, so no second copy of the context key exists.
- Add the option `(*LayerEndpoint).WithTenantRouting() *LayerEndpoint`, which sets an unexported `multiTenant bool`. Its doc comment says the registry routes each request by §6.3.1 and that a request carrying no routed tenant is unrouted.
- Add `func (e *LayerEndpoint) tenant(ctx context.Context) (tenantID string, routed bool)`. Without `multiTenant` it returns `(e.tenantID, true)`. With it, it returns the context tenant and `true` when `core.TenantFromContext` reports one, and `("", false)` otherwise. It never falls back to `e.tenantID` on a multi-tenant endpoint, because that fallback would send an unknown-org `trusted-headers` caller to the default tenant's layers.
- Add `func (e *LayerEndpoint) requireTenant(w http.ResponseWriter, r *http.Request) (string, bool)`, which writes `403 auth.forbidden` with the message "request resolves to no tenant" when `tenant` reports unrouted. Cite `// Spec: §7.3.1 (Tenant selection)`.
- In `register`, `update`, `restore`, `unregister`, `reorder`, and `reingest`, call `requireTenant` after `rejectIfReadOnly` (and after `verifiedCaller` in `reorder`) and before `authAdmin` or the admin callback. Pass the returned tenant explicitly to `lookupLayerForWrite`, `effectiveLayerCap`, `register`'s stored `TenantID`, and the reorder scope at `:1596`, and use it for every store read and write in those handlers that reads `e.tenantID` today, including `register`'s owned-layer count (`:1313`) and default-order read (`:1366`), and `reorder`'s layer list before the renumbering (`:1550`) and after it (`:1581`). The `:1581` list builds the reorder response and feeds the precedence comparison that decides whether an event is published (`:1590`, `:1599`), so a read left on the boot tenant would return another tenant's layers to the caller.
- In `erase`, after `rejectIfReadOnly` and before `authAdmin`, answer `403 auth.forbidden` with the message "erasure is not available on a multi-tenant registry" when `multiTenant` is set, before the body is decoded, before `ListLayerConfigs`, and before `audit.EraseUser`. Cite `// Spec: §8.5, §4.7.1`. The state it reads is `multiTenant`, which only `WithTenantRouting()` sets and nothing clears. Without `multiTenant`, `erase` is unchanged in behavior and reads its tenant through `tenant`, which returns `e.tenantID`, the sole tenant on a single-tenant endpoint. If the check does not fire on a multi-tenant endpoint, a public-mode or no-provider registry rewrites every tenant's audit records; TEST-1 case 2 and TEST-3 `TestLayerEndpoint_MultiTenantNoRouter` observe that through the audit file and the response code.
- In `Capabilities`, return `LayerCapabilities{}` (every member false) when `multiTenant` is set and `e.tenant(r.Context())` reports unrouted, and otherwise keep `e.authAdmin(r) == nil`. Update its doc comment to say the reported value and the enforced gate read the same tenant resolver and the same admin callback. Cite `// Spec: §7.3.4, §7.3.1 (Tenant selection)`. Its sole caller is the `SessionPosture.Capabilities` seam that serverboot fills (`internal/serverboot/serverboot.go:1571`); the signature is unchanged. Its context carries a routed tenant only because CODE-4 mounts the posture read through `TenantRoutedNoReject`; without that mount every multi-tenant posture read reports false, which TEST-1 case 10 and TEST-3 step 7 observe.
- `list` keeps `verifiedCaller` first, then returns `{"layers": []}` with `200` when unrouted, and otherwise reads the resolved tenant.
- `layerEventScope(before, cfg, action)` and `notifyIngestFailure(ctx, cfg, err)` take the tenant from `cfg.TenantID`.
- `NewLayerEndpoint` keeps its signature. After this change `e.tenantID` is read in exactly one place, the single-tenant arm of `tenant`. `erase` and the single-tenant arm of `handleWebhook` obtain it through `tenant`, and `lookupLayerForWrite`, `effectiveLayerCap`, and `webhookURL` take the tenant as a parameter. A reviewer verifies the invariant with `grep -n 'e\.tenantID' pkg/registry/server/*.go`, which must report only that line. A read left on the boot tenant does not fail on its own on a multi-tenant endpoint, because the boot tenant is a provisioned tenant; TEST-1's poisoned boot tenant observes it.

### CODE-3. Tenant-qualified inbound webhook route on a multi-tenant registry

`pkg/registry/server/layers.go` (`webhookURL` `:179`, `WebhookHandler` `:969`), `pkg/registry/server/webhook_ingest.go` (`handleWebhook` `:29-90`).

- `webhookURL(tenantID, layerID string)`. With `multiTenant` set, the path is `/v1/ingest/webhook/` + `url.PathEscape(tenantID)` + `/` + `url.PathEscape(layerID)`, where `tenantID` is the stored `lc.TenantID`. Without it, the path stays `/v1/ingest/webhook/` + `url.PathEscape(layerID)`. Both callers, `register` (`pkg/registry/server/layers.go:1393`) and the `rotate_webhook_secret` arm of `update` (`:1142`), pass the layer record's `cfg.TenantID`, never `e.tenantID`.
- `WebhookHandler` registers `POST /v1/ingest/webhook/{tenant}/{id}` when `multiTenant` is set and `POST /v1/ingest/webhook/{id}` otherwise. A multi-tenant endpoint does not serve the single-segment route, so a delivery to it answers `404` from the mux.
- `handleWebhook` takes the tenant from `r.PathValue("tenant")` on a multi-tenant endpoint and from `e.tenant(r.Context())` otherwise, which returns `e.tenantID`. On a multi-tenant endpoint it calls `e.store.GetTenant(ctx, tenant)` first and answers `404 registry.not_found` ("no such layer") when the tenant is absent or `Active` is false, then reads `GetLayerConfig(ctx, tenant, id)`. The HMAC check and the rest of the handler are unchanged. The active check matters because a deactivated tenant's rows persist, so `GetLayerConfig` alone would still find its layer.
- Cite `// Spec: §7.3.1` on both functions.

### CODE-4. serverboot wiring

`internal/serverboot/serverboot.go` (`:1449-1462`, `:1483-1514`, `:1561-1572`).

- When `cfg.multiTenant` is set, chain `.WithTenantRouting()` onto the `NewLayerEndpoint` builder. This is the same condition that selects the multi-tenant webhook form, and it holds whether or not a verifier is installed, so a free-form-label multi-tenant boot is wholly unrouted.
- Mount `/v1/layers` and `/v1/layers/` through `srv.TenantRouted(...)`. Leave `/v1/ingest/webhook/` unwrapped: the existing prefix mount reaches the tenant-qualified subpath. Leave `/v1/admin/erase` unwrapped as well (`internal/serverboot/serverboot.go:1514`): the erase refusal reads `multiTenant` alone, and wrapping it would answer a row-U1 caller (Unrouted outcome table) `401 auth.tenant_unknown` (`rejectUnknownTenant` is true for every provider except `trusted-headers`, `serverboot.go:1454`; `pkg/registry/server/server.go:474-479`) where SPEC-2 part 2 fixes `403 auth.forbidden` for every caller. A single-tenant erase reads its tenant through `tenant`, which returns `e.tenantID`, and needs no routed context. TEST-3 `TestLayerEndpoint_MultiTenantUnknownOrgRejected` pins the layer and erase mount choices, and the posture mount below, on the binary under a rejecting provider.
- Inside the existing `cfg.webUI` block, mount `server.PathWebUISession` as `srv.TenantRoutedNoReject(server.SessionPosture{...}.Handler())`, with the struct fields unchanged. Keep the comment that the reported capability and the enforced gate are one expression, and add that both read the routed tenant and that the posture mount never refuses, because §7.3.4 answers every posture request `200`. A single-tenant registry installs no router, so `TenantRoutedNoReject` returns the posture handler unchanged there.
- Add a comment at the mount noting that a routed layer request verifies its credential twice, once in `TenantRouted` and once in the endpoint's resolver, and that the verifiers keep no replay state, so the second verification is harmless.
- The `WithAdminAuth` closure is unchanged. `buildReingestRunner` and `internal/serverboot/reingest.go` are unchanged, so `auditMeter.Allow(tenantID)` stays keyed by the boot tenant, as `Record` is.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Routed caller lists or writes layers | Acts in the caller's routed tenant only | §7.3.1 Tenant selection (SPEC-2); `docs/reference/http-api.md` layer section (DOC-1(c)), `docs/deployment/clustered.md` (DOC-1(e)), `docs/deployment/layers.md` (DOC-1(f)) |
| Tenant admin of tenant A acts in tenant B | An update, restore, unregister, or reingest of a layer the caller does not own is refused with `403 auth.forbidden`; a register under an unused ID resolves to a user-defined layer owned by the caller, as for any authenticated non-admin (`pkg/registry/server/layers.go:1224-1229`); an admin grant applies only in its own tenant | §7.3.1 Tenant selection and Layer write authorization (SPEC-2); DOC-1(c) |
| Any multi-tenant request that §6.3.1 resolves to no tenant, a credential failure on a layer route, and any webhook delivery | As the Decisions Unrouted outcome table states | §6.3.1 (existing), §7.3.1 Tenant selection and webhook sentence (SPEC-2); DOC-1(c), (d), (g) |
| Webhook delivery to the single-segment route on a multi-tenant registry | `404`; the route is not served (accepted, pre-1.0; every Git layer's webhook is re-registered) | §7.3.1 webhook sentence (SPEC-2); CL-1 `Changed`, DOC-1(e) step 6, DOC-1(f) |
| Layer rows a routed caller wrote into the default tenant before the fix | Stay in the default tenant. The owner cannot reach such a row after the fix, because every layer write acts in the tenant §6.3.1 selects from the owner's organization and `DELETE /v1/layers?id=...` answers `404 registry.not_found` there (`pkg/registry/server/layers.go:1498-1501`). An admin of the default tenant, such as a bootstrap admin whose organization is `default`, removes the row with `podium layer unregister` or `DELETE /v1/layers?id=...`. The owner registers the layer again from the owning tenant, which needs no prior unregister because the two rows live under different tenant keys (accepted, no migration) | CL-1 `Fixed` entry; `docs/deployment/clustered.md` step 5 (DOC-1(e)) |
| Erasure and the posture read on a multi-tenant registry | As the Unrouted outcome table states; erasure purges no layer and rewrites no audit record (fail-closed pending OQ-3) | §8.5 new bullet, §7.3.4 (SPEC-2 parts 2 and 3); DOC-1(e), (h), (i) |
| Erasure on a single-tenant registry | Unchanged: purges the user's layers and redacts the registry audit file | §8.5 (existing) |
| Web UI layer panel for an authenticated caller that resolves to no tenant | The panel lists no layers and offers the registration control through the owner arm; the registry answers the registration `403 auth.forbidden`, or `401 auth.tenant_unknown` under a rejecting provider (Unrouted outcome table, row U1), and the panel presents the refusal (accepted; no web UI change) | §13.10 Layer panel exception (SPEC-2 part 4); DOC-1(i) |
| Reingest on a routed tenant's layer | Counted against the audit-volume meter keyed by the boot tenant, so the cap is spent deployment-wide (known divergence from the §4.7.8 per-tenant cap that predates this proposal; per-tenant keying is a non-goal) | §4.7.8 (`spec/04-artifact-model.md:905`, unchanged); Non-goals; `docs/deployment/clustered.md` Quotas bullet (DOC-1(e)) |
| Admin, owner, or anonymous subscriber on a verifying provider receives `layer.config_changed` for a `public: true` layer that `GET /v1/layers` lists none of to it | Delivered, because the stream follows the subscriber's §4.6 view (accepted; item 2 withdrawn) | §7.6 Change-event stream visibility (existing); `docs/reference/http-api.md` Events stream (DOC-1(a)) |
| Single-tenant registry | Unchanged in every response, including the webhook URL | §6.3.1 single-tenant sentence (existing); no new text |

## Testing

**TEST-1 · unit and integration, `pkg/registry/server`.** Lands in S6. The fixture is an in-memory store holding tenants `A` and `B` (both active) and an inactive tenant `C`, behind `srv.TenantRouted(layers.Handler())`, an unwrapped `layers.EraseHandler()` (the CODE-4 mount), `srv.TenantRoutedNoReject(server.SessionPosture{Identity: postureIdentity, Capabilities: layers.Capabilities}.Handler())`, and `srv.Handler()` over `httptest`. The router is installed through `server.WithTenantRouter(resolve, rejectUnknown)` (`pkg/registry/server/server.go:220`), where `resolve` is a fixture closure over the store that mirrors `tenantResolver` (`internal/serverboot/orgid.go:23-38`): it maps org `A` to tenant `A` and org `B` to tenant `B`, and returns `("", false)` for `C` (inactive), for `poison-p`, and for every other org; `rejectUnknown` is a per-case parameter. The fixture does not reuse the stub in `pkg/registry/server/tenants_test.go:34-36`, which routes every org to `"default"`. The fixture defines one stub verifier that reads `X-Test-User`, `X-Test-Org`, and an `X-Test-Fail` header that selects a typed error. The `Server` is built with both `server.WithIdentityVerifier(stub)` (`pkg/registry/server/server.go:209-211`) and `server.WithTenantRouter(resolve, rejectUnknown)`, because `WithTenantRouter` sets only the router and the reject flag (`server.go:220-225`), and `TenantRouted` and `TenantRoutedNoReject` leave every request unrouted when `s.idVerifier` is nil (CODE-1). The endpoint receives the same stub through `WithIdentityResolver(stub)` (`pkg/registry/server/layers.go:208`). One verifier on both seams mirrors serverboot, which installs `layerVerify` through `server.WithIdentityVerifier(layerVerify)` (`internal/serverboot/serverboot.go:1389`, `:1416`, `:1440`) and through `WithIdentityResolver(layerCallerResolver(layerVerify))` (`:1492`). The posture handler's `Identity` field is `postureIdentity`, a test-local closure that mirrors `layerIdentityResolver` (`internal/serverboot/identity_verify.go:74-82`), which the test package cannot import because it is unexported in `internal/serverboot`. It calls the same stub verifier, so case 10's `subject` assertions observe the caller of the routed request. It returns the verified identity when the verifier succeeds and `layer.Identity{IsPublic: true}` when it returns an error, so an `X-Test-Fail` credential resolves the anonymous identity and the posture response omits `subject` (`pkg/registry/server/webui_session.go:73-74`). The posture mount thereby matches the CODE-4 boot mount, which sets `Identity: layerIdentity` (`internal/serverboot/serverboot.go:1565`, built at `:1483`). The endpoint's boot tenant, and the tenant `core.New` binds, is a fourth tenant `P` (ID `poison-p`, active, `store.Quota{MaxUserLayers: 1}`) that the resolver maps no org to. The endpoint is built with `.WithTenantRouting()`. One constructor, `newTenantRoutingFixture(t, opts)`, builds every case except case 7 and case 2's single-tenant erase control, and case 11 builds through it with the router disabled, keeping `WithIdentityVerifier(stub)` and dropping only `WithTenantRouter`. Case 2's single-tenant erase control builds a separate router-less endpoint without `.WithTenantRouting()`, whose boot tenant is an ordinary tenant `S` with no poison checks, because a correct single-tenant erase purges the erased user's layers in its boot tenant and would change a `P` snapshot. The constructor seeds `P` as follows:

- (a) an admin grant for every fixture identity;
- (b) a `public: true` layer at order 900, which is `P`'s highest order;
- (c) two user-defined layers owned by each fixture identity, below order 900;
- (d) for every layer seeded in `A` or `B`, a twin with the same ID, class, owner, visibility, and tombstone state, an order below 900, a distinct webhook secret, and `Repo` `https://git.invalid/poison-p/<id>`;
- (e) for the two IDs the `B` caller registers in case 4, admin-defined twins owned by `poison@p.invalid`, the first live and the second tombstoned.

The fixture snapshots `GetTenant(P)`, `ListLayerConfigs(P)`, and `ListDeletedLayerConfigs(P)` at construction, and a `t.Cleanup` fails the case if any of them changed. Its request helper fails the case when a response body contains `poison-p`. Its event publisher, notifier, and reingest runner fail the case on a scope, `tenant` tag, or `LayerConfig.TenantID` equal to `poison-p`. Its counting store and counting admin callback start counting after seeding.

These checks observe every boot-tenant read except four, which the cases assert explicitly:

- the default-order read (`pkg/registry/server/layers.go:1366`), which case 2 asserts;
- unregister's delete (`:1512`), which case 2 asserts, because the memory store's `DeleteLayerConfig` returns nil for a key it does not hold (`pkg/store/memory.go:509-511`) and a delete left on `P` changes no row;
- the owned-layer count (`:1313`), which case 4 asserts;
- the quota lookup (`:586`), which case 4 asserts.

The rest are observed as follows:

- list (`:1417`, both arms, including `?deleted=true`) and the post-reorder list (`:1581`) through the marker;
- register's stored tenant (`:1262`) and erase (`:924`, `:934`) through the snapshot;
- update (`:1005`), restore's tombstone scan and restore (`:1447`, `:1476`), unregister's lookup (`:1498`), and reorder renumbering (`:1550`) through the `404 registry.not_found` response, because case 2's `B` caller acts only on layers it registers during the case, which have no `P` twin (`layers.go:1007`, `:1461`, `:1477-1478`, `:1500-1501`, `:1563`);
- `webhookURL` through the marker in `webhook_url`;
- the event scopes (`:453`, `:1596`), the notification (`:1834`), and reingest (`:1652`) through the stubs;
- the webhook lookup (`webhook_ingest.go:46`) through case 9's rotated-secret delivery, which a lookup in `P` answers `404`;
- register's lookup (`:351`, `:358`) through the poison-owned twins (case 4);
- the `AdminAuthorize` fallback through `P`'s grants (cases 3 and 10).

New file `pkg/registry/server/layers_tenant_routing_test.go` holds every numbered case except case 7, which adds no test, and case 9, which extends `pkg/registry/server/webhook_ingest_test.go`. Every case runs under `-race`.

1. Routed list. `// Spec: §6.3.1, §7.3.1`. dave (org `B`) lists exactly his `B` layers, carol (org `A`) lists exactly hers, and neither list names the other tenant's layers. The soft-deleted arm (`?deleted=true`) follows the same rule.
2. Routed writes and the erasure refusal. `// Spec: §6.3.1, §7.3.1, §8.5`. Register, update, reorder, restore, unregister, and reingest issued by a `B` caller change only `B` rows; a snapshot of `A` rows is byte-identical before and after. The published `EventScope.TenantID` is `B` on each event, including the reorder. The case starts with no live layers in `B`, and the `B` caller acts only on layers it registers during the case, which have no `P` twin. The first layer the `B` caller registers is stored with order 10 and the next with order 20. This detects a default-order read left on the boot tenant (`pkg/registry/server/layers.go:1366`), because that read would see `P`'s order-900 layer and store 910. After the `B` caller's unregister, the unregistered layer is absent from `B`'s live list and present in `B`'s `?deleted=true` list. This detects a delete left on the boot tenant (`:1512`), because the memory store's `DeleteLayerConfig` returns nil for a key it does not hold (`pkg/store/memory.go:509-511`), so that delete answers `200` and leaves the `B` row live. The fixture checks cannot observe either read, because neither changes a `P` row or returns a `P` field. Under an always-admit admin callback, so that `readableBy` admits every layer it is given, the `B` caller's reorder returns a `layers` list naming exactly `B`'s layers and none of `A`'s. Run the `A` and `B` write sequences in parallel goroutines and assert no crossover, which pins that the resolver keeps no shared per-request state. Erase, issued by a `B` admin and again under an always-admit admin callback, answers `403 auth.forbidden`; the admin callback records zero calls, the user's `A` and `B` layers remain, and the endpoint's audit file is byte-identical before and after. The same erase on case 2's single-tenant erase control purges the user's layers and redacts the file, which pins that the refusal reads `multiTenant` alone. With `rejectUnknown` set, a verified caller whose org names no tenant sends the same erase and gets `403 auth.forbidden` rather than `401 auth.tenant_unknown`, and the audit file is byte-identical before and after, which pins that the erase route is not tenant-routed.
3. Admin gate per tenant. `// Spec: §4.7.2, §7.3.1`. olivia holds the admin grant in `A` only, and the fixture seeds an admin-defined layer `shared` in `B`. Routed to `A`, she registers a layer with no admin-only field and gets `201` with `layer.user_defined` false. Routed to `B`, the same body gets `201` with `layer.user_defined` true and `layer.owner` olivia, because a failed admin check resolves an authenticated caller's registration to the user-defined class (`pkg/registry/server/layers.go:1224-1229`); `B` holds no new admin-defined row. Routed to `B`, her update of `shared` answers `403 auth.forbidden` and leaves the row unchanged.
4. User-layer cap per tenant. `// Spec: §7.3.1 (Tenant selection)`. The endpoint is built with no `WithMaxUserLayers` override, because an override returns before the per-tenant quota lookup (`pkg/registry/server/layers.go:582-585`) and would hide a lookup that still reads the boot tenant. Tenant `A` carries `store.Quota{MaxUserLayers: 1}` and tenant `B` carries `store.Quota{MaxUserLayers: 2}`. The fixture seeds two user-defined layers owned by dave in `A` directly through the store, which bypasses the cap, and none in `B`. Routed to `B`, his first and second registers of new layer IDs each get `201`. Routed to `A`, a register of a new layer ID answers `429 quota.layer_count_exceeded` with `details.limit` 1 and `details.current` 2 (`pkg/registry/server/layers.go:1332-1334`). Each boot-tenant read on this path fails the case on its own against `P`:

   - An owned-layer count that still reads the boot tenant (`:1313`) counts dave's two `P` layers against `B`'s cap of 2, so the first `B` register answers `429`.
   - A quota lookup in `effectiveLayerCap` that still reads the boot tenant (`:586`) applies `P`'s cap of 1, so the second `B` register answers `429`.
   - A lookup in `lookupLayerForWrite` that still reads the boot tenant finds a poison-owned admin-defined twin and answers `403 auth.forbidden`. At `:351` this hits the live twin of the first ID; at `:358` it hits the tombstoned twin of the second ID.

   `P`'s cap stays nonzero, because `effectiveLayerCap` ignores a zero quota (`:586`).
5. `TenantRouted` reject branch. `// Spec: §6.3.1, §6.10`. With `rejectUnknown` set, a verified caller whose org names no tenant gets `401 auth.tenant_unknown` with `details.token_org_id` equal to the org, and the wrapped handler is never invoked (a counting handler records zero calls). With `rejectUnknown` unset, the same request reaches the endpoint unrouted.
6. Credential failure under a router. `// Spec: §7.3.1`. The stub returns an `identity.UntrustedRuntimeError`. `list` and `reorder` answer `auth.untrusted_runtime`, and `update` answers `403 auth.forbidden`. The wrapper invokes the endpoint in every case.
7. Single-tenant guard. No new test. The existing router-less suites (`layers_test.go`, the `layer_*_test.go` files, `erase_test.go`, and the webhook tests) must pass unchanged.
8. Unrouted refusal. `// Spec: §7.3.1 (Tenant selection), §8.5`. Three identities: anonymous, authenticated with no org, and authenticated with an unknown org under `rejectUnknown` unset. The admin callback is an always-admit counting closure. `list` returns `200` with `[]`. Register, update, restore, unregister, reorder, reingest, and erase each answer `403 auth.forbidden`, the counting store records no write and no `IsAdmin` call, and the admin callback records zero calls.
9. Webhook route. `// Spec: §7.3.1`. A `B` git layer's registration returns a `webhook_url` whose path is `/v1/ingest/webhook/B/<id>`, with a layer ID that needs escaping round-tripping. A POST to it with a bad signature answers `401 ingest.webhook_invalid`. The same layer ID under `A`, where it does not exist, answers `404 registry.not_found`. An unknown tenant segment and the inactive tenant `C` (holding a layer of that ID, `Tenant.Active=false`) each answer `404 registry.not_found`. A POST to `/v1/ingest/webhook/<id>` on the multi-tenant endpoint answers `404`. A webhook-triggered ingest failure records its notification under `B`, which pins CODE-2's `cfg.TenantID` path. A `B` caller then sends `POST /v1/layers/update` with `rotate_webhook_secret: true` on the same layer; the returned `webhook_url` path is `/v1/ingest/webhook/B/<id>`, which carries no `poison-p` segment. A delivery to it, signed with the rotated secret, passes the HMAC check and reaches the `B` layer, which pins the `update` caller of `webhookURL`. A lookup left on the boot tenant answers that delivery `404 registry.not_found`, because `P` holds no layer of that ID.
10. Posture read per tenant. `// Spec: §7.3.4, §7.3.1 (Tenant selection), §13.10`. olivia routed to `A` reads `layer_capabilities.manage_any_layer` true, and routed to `B` reads false. Under an always-admit admin callback, the anonymous, no-org, and unknown-org identities (with `rejectUnknown` unset) read false, and a routed `B` caller reads true. With `rejectUnknown` set, a verified unknown org answers `200` with `subject` present and `manage_any_layer` false, and the posture handler is invoked, while the same request to `GET /v1/layers` answers `401 auth.tenant_unknown`. The §13.10 exception (SPEC-2 part 4) is pinned on the registry side: with `rejectUnknown` unset, the unknown-org identity reads `200` with `subject` equal to its user and `manage_any_layer` false, the inputs on which the panel offers registration, and that caller's `POST /v1/layers` with no admin-only field answers `403 auth.forbidden` with no store write. The existing single-tenant posture tests pass unchanged.
11. Multi-tenant endpoint behind a server with no tenant router. `// Spec: §6.3.1, §7.3.1 (Tenant selection), §8.5`. The endpoint is built with `.WithTenantRouting()` and its layer handler is wrapped in `TenantRouted` of a `Server` built with `WithIdentityVerifier(stub)` and without `WithTenantRouter` (its erase handler unwrapped), under an always-admit admin callback. `TenantRouted` returns `next` unchanged when `s.tenantRouter` is nil (CODE-1), so every request stays unrouted whether or not a verifier is installed, which is the state CODE-4 produces on a multi-tenant boot with no verifier. `GET /v1/layers` returns `{"layers":[]}`, register and erase answer `403 auth.forbidden`, a follow-up list is still empty, the store records no write, and the posture read reports `manage_any_layer` false.

Measure with `go test -race -coverpkg=./... -coverprofile=cover.out ./pkg/registry/... ./internal/serverboot/...` and confirm each new function reaches 85% with `go tool cover -func=cover.out`.

**TEST-3 · e2e, `test/e2e/layer_tenant_routing_test.go` (new), and a comment update in `test/e2e/events_visibility_test.go`.** Lands in S7. Every case runs on the standard stack and skips cleanly through `msSkipIfNoStack` where Postgres and S3 are absent, and `TestLayerEndpoint_MultiTenantUnknownOrgRejected` also skips on darwin through `requireCustomTrustStore`; TEST-1 cases 2, 5, 8, 10, and 11 pin the same refusals in process on every platform.

`TestLayerEndpoint_MultiTenantRouting`, `// Spec: §6.3.1, §7.3.1, §8.5`. Boot through `msStartStandardServerEnv` with `PODIUM_IDENTITY_PROVIDER=trusted-headers`, `PODIUM_MULTI_TENANT=true`, `PODIUM_WEB_UI=true` (which mounts the §7.3.4 posture read), a per-run `PODIUM_TRUSTED_PROXY_SECRET`, `PODIUM_BOOTSTRAP_ADMINS=carol-<s>@acme.com`, and `PODIUM_OPERATOR_ADMINS=olivia-<s>@acme.com`, where `<s>` is a per-run suffix.

1. olivia sends `POST /v1/admin/tenants` with name `globex-<s>`. Record the returned tenant ID and assert it equals the UUIDv5 `OrgIDForName("globex-<s>")` computes.
2. carol (org `default`) registers a network-git layer under `git.invalid` with no admin-only field and gets `201` with `layer.user_defined` false. Before this fix the same request answers `201` with `layer.user_defined` true, because the admin check against `podium:unrouted` fails and an authenticated caller's registration falls to the user-defined class (`pkg/registry/server/layers.go:1224-1229`), so the status code alone does not detect the defect. Add an explicit `t.Cleanup` DELETE for that layer.
3. dave-<s> (org `globex-<s>`) registers two user layers through `evRegisterUserLayer`. dave's `GET /v1/layers` lists exactly his layers, and carol's lists neither. Each `webhook_url` contains `/v1/ingest/webhook/<globex tenant ID>/`.
4. A POST to dave's advertised `webhook_url` with a bad signature answers `401 ingest.webhook_invalid`. The same path with an unknown tenant segment answers `404 registry.not_found`. dave's `POST /v1/layers/update` with `rotate_webhook_secret: true` on one of his layers returns a `webhook_url` containing `/v1/ingest/webhook/<globex tenant ID>/`, and not the default tenant's ID.
5. Open streams as dave (org `globex-<s>`) and carol (org `default`). dave's reorder of his two layers delivers `layer.config_changed` on dave's stream, and carol's stream receives nothing naming those layers within `evWindow`.
6. erin-<s> (org `initech-<s>`, unprovisioned) gets `[]` on `GET /v1/layers` and `403 auth.forbidden` on register.
7. `GET /v1/ui/session` reports `layer_capabilities.manage_any_layer` true for carol (org `default`) and false for dave and for erin. carol's `POST /v1/admin/erase` naming dave answers `403 auth.forbidden`, and dave's layers are still listed afterwards.

`TestLayerEndpoint_MultiTenantNoRouter`, `// Spec: §6.3.1, §7.3.1 (Tenant selection), §8.5`. Boot through `msStartStandardServerEnv` with `PODIUM_MULTI_TENANT=true`, `PODIUM_WEB_UI=true`, and no identity provider. Assert the server log does not carry `multi-tenant mode: routing requests by organization`, which pins that no router is installed. `GET /v1/layers` returns `{"layers":[]}`, `POST /v1/layers` and `POST /v1/admin/erase` answer `403 auth.forbidden`, a follow-up `GET /v1/layers` is still empty, and `GET /v1/ui/session` reports `manage_any_layer` false.

`TestLayerEndpoint_MultiTenantUnknownOrgRejected`, `// Spec: §6.3.1, §7.3.1 (Tenant selection), §7.3.4, §8.5`. This case pins the CODE-4 mount choices whose responses differ only under a provider that rejects an unknown org: the layer routes through `TenantRouted`, the erase route unwrapped, and the posture read through `TenantRoutedNoReject`. Under `trusted-headers` and with no identity provider, the wrappers produce the same responses, so `TestLayerEndpoint_MultiTenantRouting` and `TestLayerEndpoint_MultiTenantNoRouter` cannot detect a wrong wrapper. Call `requireCustomTrustStore(t)` first, because darwin ignores `SSL_CERT_FILE` (`test/e2e/auth_oidc_jwt_test.go:64-69`), then start the TLS IdP with `idp := startOIDCTestIdP(t, "")` (`:75`). Create a key pair with `injKeyPair` (`test/e2e/injected_token_helpers_test.go:32`), because `msStandardEnv` seeds `PODIUM_RUNTIME_KEYS_PATH` from its `pemPath`. Boot through `msStartStandardServerEnv` with that `pemPath` and, in `extraEnv`, `PODIUM_MULTI_TENANT=true`, `PODIUM_WEB_UI=true`, `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, `PODIUM_OAUTH_ISSUER=` + `idp.srv.URL`, `PODIUM_OAUTH_AUDIENCE=` + `oidcAudience`, and `SSL_CERT_FILE=` + `idp.caFile`. These override the `injected-session-token` entries `msStandardEnv` sets (`test/e2e/standard_stack_parity_test.go:210-211`). `oidc-jwt` is the provider §6.3.1 names as rejecting an unknown org, and it installs a router with `rejectUnknownTenant` true (`internal/serverboot/serverboot.go:1450-1455`). Mint each token with `idp.token(t, claims)`, where `claims` carries `iss` `idp.srv.URL`, `aud` `oidcAudience`, `exp`, `sub` and `email` set to the user, and `org_id`, and send it as `Authorization: Bearer`. This case runs on the CI Linux lane and skips on darwin. TEST-1 cases 2, 5, and 10 pin the same row-U1 cells in process on every platform.

1. Control. alice@acme.com, the bootstrap admin `msStandardEnv` sets, with `org_id` `default`, gets `200` on `GET /v1/layers` and reads `layer_capabilities.manage_any_layer` true on `GET /v1/ui/session`. This shows the token verifies and routes, so the `401` below comes from the unknown org alone.
2. frank-<s>@acme.com with `org_id` `initech-<s>` (unprovisioned) gets `401 auth.tenant_unknown` on `GET /v1/layers`, with `details.token_org_id` equal to `initech-<s>`.
3. The same token on `POST /v1/admin/erase`, with a body naming frank-<s>@acme.com and a non-empty salt, gets `403 auth.forbidden`. A `401` here means the erase route was wrapped in `TenantRouted`.
4. The same token on `GET /v1/ui/session` gets `200` with `subject` reported and `layer_capabilities.manage_any_layer` false. A `401` here means the posture read was mounted through `TenantRouted`.

**IMPLEMENTOR'S CHOICE:** how `TestLayerEndpoint_MultiTenantNoRouter` clears the identity provider that `msStandardEnv` sets (`test/e2e/standard_stack_parity_test.go:210`), for example an overriding empty `PODIUM_IDENTITY_PROVIDER=` entry in `extraEnv`. The boot must succeed with `PODIUM_MULTI_TENANT=true` and no identity provider, and the log assertion above must hold.

Do not provision a bare `globex`, because `TestEventStream_MultiTenantRouting` relies on it being unprovisioned. In that test, rewrite the clause "which a multi-tenant registry refuses" in the IMPLEMENTOR'S CHOICE comment so it no longer claims admin layer writes are refused, and leave its assertions unchanged. Run the suite with `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/ -run 'TestLayerEndpoint_MultiTenant|TestEventStream_'` and convert the profile with `go tool covdata textfmt` to confirm the mount-site and wrapper lines run in the subprocess, and grep the output for `SKIP`.

## Manual validation

**MV-1.** Add scenario S85 to `test/manual-validation.md` after the last scenario in the tree, and its index row after the last index row (`test/manual-validation.md:231-234` at the time of writing). Proposal 0046 stages S84. If another proposal takes S85 first, use the next free S-number and use it consistently in the index row, the heading, and checklist step S10. The scenario binds `127.0.0.1:8188`, which no scenario binds. Lands in S10.

```markdown
| S85 | Layer operations act in the caller's tenant on a multi-tenant registry | standard | none | none | Postgres, S3 |
```

~~~~markdown
## S85: Layer operations act in the caller's tenant on a multi-tenant registry

**Goal.** Validate that on a multi-tenant registry the layer endpoints read and
write the layer list of the tenant the caller's organization names, that a
bootstrap admin can manage the default tenant's layers, that the advertised
webhook URL carries the tenant ID, and that a caller whose organization names
no tenant lists nothing and cannot write.

**Covers.** §6.3.1 per-request tenant selection and §7.3.1 Tenant selection on
the layer endpoints, through the compiled binary under `trusted-headers`.

**Why by hand.** An operator reads the raw layer lists of callers in different
tenants side by side. A user-defined class on carol's admin register, a dave layer
in carol's list, or a `webhook_url` without the tenant ID is the defect this
scenario catches. The standard-stack end-to-end test skips silently on macOS
without the stack.

**Prerequisites.** Local Postgres and MinIO from `make services-up`, plus
`test.env` (Postgres DSN, S3 settings). Skip if either is absent.

**Steps.**

1. Run the isolation block. Start services, load the environment, and boot a
   multi-tenant registry behind `trusted-headers`.

   ```bash
   (cd "$REAL_HOME/projects/podium" && make services-up)
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_MULTI_TENANT=true
   export PODIUM_IDENTITY_PROVIDER=trusted-headers
   export PODIUM_TRUSTED_PROXY_SECRET=gateway-secret
   export PODIUM_OPERATOR_ADMINS=olivia@acme.com
   export PODIUM_BOOTSTRAP_ADMINS=carol@acme.com
   S=$$
   mkdir -p "$WORK/reg"
   podium serve --layer-path "$WORK/reg" --no-embeddings --bind 127.0.0.1:8188 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8188/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8188
   as() { curl -s -H "X-Podium-Proxy-Secret: gateway-secret" -H "X-Podium-User-Sub: $1" -H "X-Podium-User-Org: $2" "${@:3}"; }
   ```

   **Expect.** `server_alive` reports the server running, and the log carries
   `multi-tenant mode: routing requests by organization`.

2. Provision a per-run tenant as the operator. The `podium` CLI sends only a
   bearer token, so this step uses curl with the trusted headers.

   ```bash
   as olivia@acme.com default -X POST -H 'Content-Type: application/json' \
     -d "{\"name\":\"globex-$S\"}" "$URL/v1/admin/tenants" | tee "$WORK/tenant.json"
   GLOBEX=$(python3 -c "import json,sys; print(json.load(open('$WORK/tenant.json'))['id'])")
   echo "globex tenant id: $GLOBEX"
   ```

   **Expect.** The response carries an `id`, and `$GLOBEX` is a UUID rather
   than the string `globex-<pid>`.

3. As carol (org `default`), register an admin-defined Git layer.

   ```bash
   as carol@acme.com default -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"team-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/acme/team.git\",\"ref\":\"main\"}" "$URL/v1/layers" \
     | tee "$WORK/team.json"
   python3 -c "import json; print('user_defined:', json.loads(open('$WORK/team.json').read().rsplit('HTTP',1)[0])['layer']['user_defined'])"
   ```

   **Expect.** `HTTP 201` and `user_defined: False`. `user_defined: True`
   means the admin gate still checks the unrouted tenant, which resolves
   carol's registration to the user-defined class.

4. As dave (org `globex-<pid>`), register a user layer, then list as dave and
   as carol.

   ```bash
   as dave@acme.com "globex-$S" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"dave-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/dave/notes.git\",\"ref\":\"main\"}" "$URL/v1/layers" \
     | python3 -c "import json,sys; print('webhook_url:', json.load(sys.stdin).get('webhook_url'))"
   echo "dave sees:  $(as dave@acme.com "globex-$S" "$URL/v1/layers" | python3 -c "import json,sys; print([l['id'] for l in json.load(sys.stdin)['layers']])")"
   echo "carol sees: $(as carol@acme.com default "$URL/v1/layers" | python3 -c "import json,sys; print([l['id'] for l in json.load(sys.stdin)['layers']])")"
   ```

   **Expect.** `webhook_url` contains `/v1/ingest/webhook/$GLOBEX/dave-<pid>`.
   dave's list contains `dave-<pid>` and not `team-<pid>`. carol's list
   contains `team-<pid>` and not `dave-<pid>`.

5. As erin (org `initech-<pid>`, which names no tenant), list and register.

   ```bash
   echo "erin list: $(as erin@acme.com "initech-$S" "$URL/v1/layers")"
   as erin@acme.com "initech-$S" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"erin-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/erin/x.git\",\"ref\":\"main\"}" "$URL/v1/layers"
   ```

   **Expect.** The list is `{"layers":[]}`. The register answers `403` with
   error code `auth.forbidden`.

**Cleanup.** Delete `team-<pid>` and `dave-<pid>` with `DELETE /v1/layers?id=...`
as carol and dave, stop the server, `rm -rf "$WORK"`, and
`(cd "$REAL_HOME/projects/podium" && make services-down)` when finished with the
standard-mode scenarios.
~~~~

**IMPLEMENTOR'S CHOICE:** the exact register body fields and the `podium serve` flags in step 1. Each must match what the current `POST /v1/layers` handler and the standard-mode `podium serve` accept, verified by running the scenario once, and the Expect blocks must stay as written.

## Open questions

**OQ-1. One webhook route form or two.** The draft keeps `/v1/ingest/webhook/{layer-id}` on a single-tenant registry and serves `/v1/ingest/webhook/{tenant-id}/{layer-id}` only on a multi-tenant one. The alternative serves the tenant-qualified form on every deployment, with the boot tenant's ID on single-tenant. That leaves one route pattern and one URL format, and it changes every single-tenant webhook URL, which every operator would re-register. A third option splits CODE-3 into its own proposal, which means holding CODE-4 until it lands, because without CODE-3 every non-default tenant's Git webhook answers `404`.

**OQ-2. A stream-to-list containment rule.** The withdrawn item 2 asked that the change-event stream name a layer to a subscriber only where `GET /v1/layers` would list that layer to it. A coherent version must cover every event that names a layer (`layer.config_changed`, `layer.ingested`, `layer.history_rewritten`, and the layer recorded for artifact events), reconcile the §7.5.4 withdrawal wake an anonymous watcher of a public layer relies on, and decide how a subscriber resolved as anonymous during a JWKS outage is treated for the life of its stream. Should a separate decision proposal take this up, or does the existing §7.6 behavior stand?

**OQ-3. Erasure on a multi-tenant registry.** §4.7.1 gives each org its own audit stream (`spec/04-artifact-model.md:788`) and §6.3.1 selects that stream per request (`spec/06-mcp-server.md:75`), while the registry writes one audit file for every tenant and `audit.EraseUser` rewrites all of it (`pkg/registry/server/layers.go:952`); `audit.Event` carries no tenant (`pkg/registry/server/audit_context.go:169-176`). Admitting a routed tenant admin's erase would therefore let the admin of tenant B rewrite records written under tenant A. The staged default, recorded here as the recommended option, refuses every erase on a multi-tenant registry with `403 auth.forbidden` before the admin gate, any store access, and any audit rewrite (SPEC-2 part 2, CODE-2). Erasure on a multi-tenant registry before this proposal was already refused under a verifying provider, so the default removes no capability those deployments had. Any later proposal that lifts the refusal must satisfy one of two constraints: either the redaction touches only records written under the requesting tenant, which needs a tenant on each audit record or a per-tenant audit sink, or the spec is amended in §4.7.1 and §6.3.1 to state one shared audit stream and §8.5 names a cross-tenant actor (such as the §4.7.1 operator role) as the only caller that may redact it. Should a follow-up proposal take either path, or does the refusal stand?

## Documentation changes

**DOC-1.** Lands in S8. The response statements in (c), (d), (f), (g), (h), and (i) are written from the Decisions Unrouted outcome table, and none of them names a provider where the table keys the provider class by `rejectUnknownTenant`.

(a) `docs/reference/http-api.md`, Events stream paragraph (line 593 at the time of writing). Before its last sentence ("A registry started in public mode or with no identity provider configured admits every layer..."), insert:

> The stream follows the caller's layer visibility rather than the layer list rule, so a caller with no verified subject on a registry that verifies callers receives the events of `public: true` layers although `GET /v1/layers` lists it no layers.

(b) `docs/reference/http-api.md`, line 374. Replace "The object carries no tenant identifier, because these endpoints serve one tenant." with "The object carries no tenant identifier, because these endpoints serve the one tenant the request's organization selects."

(c) `docs/reference/http-api.md`, layer endpoints section (around lines 346 to 525). After the layer read visibility passage, add a "Tenant selection" paragraph:

> On a multi-tenant registry, each layer endpoint acts in the tenant the caller's organization selects. The admin check, the layer list, the user-defined layer cap, and the tenant whose change-event subscribers receive the operation's event all use that tenant. A request the registry rejects on its other endpoints with `401 auth.tenant_unknown`, because its verified organization names no provisioned tenant, receives the same rejection on every layer endpoint. Any other request that resolves to no tenant lists no layers, and every layer write is refused with `403 auth.forbidden`, including on a registry started in public mode or with no identity provider configured. A credential that fails verification on `list` or `reorder` keeps its own error envelope. `POST /v1/admin/erase` is refused with `403 auth.forbidden` for every caller on a multi-tenant registry, including a caller the layer endpoints reject with `auth.tenant_unknown`, as [Erase a user](#erase-a-user-gdpr) states.

(d) `docs/reference/http-api.md`, Ingest webhook (line 525) and the `webhook_url` example (line 425). Add the multi-tenant route `POST /v1/ingest/webhook/{tenant-id}/{layer-id}` beside the existing one, with one sentence: "On a multi-tenant registry, the URL carries the layer's tenant ID, and a delivery naming a tenant that is not provisioned and active answers `404 registry.not_found`." Add a second example `webhook_url` of the form `https://registry.acme.com/v1/ingest/webhook/<tenant-id>/team-finance`. If OQ-1 selects one form everywhere, replace the route instead.

(e) `docs/deployment/clustered.md`:
  - The GDPR erasure bullet (line 37): state that a registry started with `PODIUM_MULTI_TENANT=true` refuses `podium admin erase` against the registry with `auth.forbidden`, because the registry keeps one audit file for every tenant and a redaction would reach other tenants' records, and that a single-tenant registry performs erasure as described.
  - The Quotas bullet (line 38): state that on a multi-tenant registry the audit-volume budget is counted across every tenant's events under the default tenant, so one tenant's audit traffic spends the budget every tenant's ingest shares.
  - "Per-tenant layer model" (line 42) and step 5 (line 191): state that the layer endpoints act in the caller's organization tenant and that a tenant's admin manages that tenant's layer list. In step 5, add that layers a routed caller registered on a multi-tenant registry before this release remain in the default tenant, that the owner registers the layer again from the owning tenant, and that an admin of the default tenant, such as a bootstrap admin whose organization is `default`, removes the stale row with `podium layer unregister`, because the owner's own requests no longer reach the default tenant.
  - Step 6 (line 197): state that the webhook URL carries the tenant ID on a multi-tenant registry and that Git layers registered before this release need their webhook re-registered.

(f) `docs/deployment/layers.md` and `docs/reference/cli.md`:
  - `docs/deployment/layers.md` line 143 (the Git webhook row of the ingestion table), the comment in the registration example at line 85 ("The registry returns a webhook URL and HMAC secret"), and `docs/reference/cli.md` line 426: state that on a multi-tenant registry `podium layer register` returns a webhook URL carrying the tenant ID. If OQ-1 selects one form everywhere, state it without the multi-tenant qualifier.
  - `docs/deployment/layers.md` line 119 ("A caller holding the tenant `admin` role, and every caller on a registry that authenticates none, sees every layer in the tenant."): append that on a multi-tenant registry `podium layer list` reads the tenant the caller's organization selects, that a caller the registry rejects on its other endpoints with `401 auth.tenant_unknown`, because its verified organization names no provisioned tenant, receives the same rejection from `podium layer list` (row U1), and that any other caller whose organization selects no tenant sees no layers, including on a registry that authenticates no caller (rows U2 to U4 and U6).

(g) `docs/deployment/gateway-delegated-identity.md` (line 102): add one sentence saying the layer endpoints follow the same routing, so a caller whose `X-Podium-User-Org` names no tenant lists no layers and cannot write one.

(h) `docs/reference/http-api.md` and `docs/reference/cli.md`, erasure and the session posture:
  - `docs/reference/http-api.md`, Erase a user (line 644): add "On a registry started with `PODIUM_MULTI_TENANT=true`, the endpoint answers `403 auth.forbidden` for every caller, including a caller whose verified organization names no provisioned tenant, and changes nothing, because the registry keeps one audit file for every tenant."
  - `docs/reference/cli.md`, `podium admin erase` (line 692): add the same statement for the registry form.
  - `docs/reference/http-api.md`, Session posture (line 83): after "including on a request that resolves no subject.", add "On a multi-tenant registry the member is evaluated in the tenant the caller's organization selects, and it is false for a request that resolves to no tenant. A caller that the layer endpoints reject with `401 auth.tenant_unknown` is answered `200` with the member false."

(i) `docs/deployment/access-control.md` line 102: after the sentence naming `manage_any_layer` as the arm that covers a write on a layer the caller does not own, add "On a multi-tenant registry the value reports the admin role in the tenant the caller's organization selects, and it is false for a caller whose organization selects no tenant. Such a caller's layer requests are refused with `401 auth.tenant_unknown` when the registry rejects it with that code on its other endpoints, and any other such caller lists no layers. The web UI still offers an authenticated caller of either kind the registration control, because the session posture reports its subject, and the registry refuses that registration."

No runnable example is added, so `tools/doccov/manifest.yaml` is unchanged.

**CL-1.** `CHANGELOG.md`, `[Unreleased]`, in the existing subsections. Lands in S9.

```markdown
### Fixed
- **Layer endpoints act in the caller's tenant on a multi-tenant registry** (§6.3.1, §7.3.1, §7.3.4): on a registry started with `PODIUM_MULTI_TENANT=true`, the layer-management endpoints under `/v1/layers` read and write the layer list of the tenant §6.3.1 selects for the request, and they check the §4.7.2 admin role, apply the user-defined layer cap, and scope the change events they publish to that tenant. The `layer_capabilities.manage_any_layer` member of `GET /v1/ui/session` reports the admin role in the same tenant. Previously these endpoints read and wrote the default tenant's layers for every caller and checked the admin role against the unrouted tenant, so on a registry with an identity provider configured and public mode off every admin layer operation was refused, including those of bootstrap admins, and an admin's registration was stored as a user-defined layer. Layer rows that routed callers registered on a multi-tenant registry before this release remain in the default tenant; to move one, register it again from the owning tenant and have an admin of the default tenant unregister the stale row, because the owner's requests no longer reach the default tenant.
- **A layer request that resolves to no tenant is refused on a multi-tenant registry** (§7.3.1): a request the registry rejects on its other endpoints with `401 auth.tenant_unknown`, because its verified organization names no provisioned tenant, receives the same rejection on the layer endpoints. Any other request that resolves to no tenant lists no layers, is refused with `403 auth.forbidden` on every layer write, and reads `manage_any_layer` as false, including on a registry started in public mode or with no identity provider configured.

### Changed
- **The inbound webhook URL carries the layer's tenant ID on a multi-tenant registry** (§7.3.1): on a multi-tenant registry, `podium layer register` returns `/v1/ingest/webhook/{tenant-id}/{layer-id}`, and the `/v1/ingest/webhook/{layer-id}` route is not served. Re-register the webhook URL on the source repository for every Git layer on a multi-tenant registry, default-tenant layers included; the default tenant's segment is its tenant ID. Single-tenant registries keep `/v1/ingest/webhook/{layer-id}`.
- **`POST /v1/admin/erase` is refused on a multi-tenant registry** (§8.5, §4.7.1): on a registry started with `PODIUM_MULTI_TENANT=true`, erasure answers `403 auth.forbidden` for every caller, including a caller whose verified organization names no provisioned tenant, and changes nothing, because the registry keeps one audit file for every tenant and a redaction would rewrite other tenants' records. A registry with an identity provider configured and public mode off already refused it; a registry in public mode or with no identity provider configured previously admitted it and redacted every tenant's records. Single-tenant registries are unchanged.

### Documentation
- HTTP API reference: layer tenant selection, the tenant-qualified webhook route, the multi-tenant erasure refusal, the per-tenant `manage_any_layer` posture member, and the change-event stream's delivery of public-layer events to a caller with no verified subject.
- Deployment pages: the per-tenant layer model, the tenant-qualified webhook URL, the pre-release layer rows left in the default tenant, and the deployment-wide audit-volume budget on a multi-tenant registry.
```

If OQ-1 selects one form everywhere, the `Changed` entry covers single-tenant registries too.

## Non-goals

- **SPEC-1, limiting `layer.config_changed` on the stream to subscribers the §7.3.1 layer read would list a layer to.** Review dropped it. The `layer.config_changed` payload carries only `{layer, action}` (`pkg/registry/server/layers.go:563-566`), while `layer.ingested` and `layer.history_rewritten` name the same layer and carry `reference`, `prior_ref`, and `new_ref` (`pkg/registry/ingest/orchestrator.go:133-186`), so the edit would hide the least revealing event and keep the others, and the containment it promised would not hold. It contradicts the §7.6 prior-visibility withdrawal rule and §7.5.4, whose watcher re-resolves only on `artifact.published`, `artifact.deprecated`, and `layer.config_changed`: an anonymous watcher of a public layer would miss the withdrawal or reorder wake and keep stale content. A once-per-stream admission would also pin a subscriber that opened during a JWKS outage (resolved as anonymous, `internal/serverboot/identity_verify.go:259-264`) out of every `layer.config_changed` for the life of the stream. It brings a second visibility rule into a stream §7.6 defines by the subscriber's own §4.6 view. The free-form-label instance is unreachable, because no layer write succeeds there (`pkg/registry/core/admin.go:21-23`). OQ-2 records the decision a coherent version would need.
- **CODE-5, the server-side `layer.config_changed` stream gate, and the parts of other deliverables that served it.** It is withdrawn with SPEC-1, because without the §7.6 edit the gate contradicts the current spec. This removes the `WithLayerListAdmission` option, the `deliverable` signature change, the hoisted shared admin callback in CODE-4 and its `SessionPosture` comment rewrite, the stream cases in TEST-1, `TestEventStream_AnonymousGetsNoLayerConfigChanged` in TEST-3, the stream entry in CL-1, the anonymous-stream extension of S83 in MV-1, and the watcher-limit sentence for `docs/consuming/configure-your-harness.md`.
- **TEST-2, a serverboot wiring test.** Review dropped it. No test in `internal/serverboot` calls `Run()` (`internal/serverboot/serverboot.go:794`), the existing harnesses rebuild the wiring by hand (`internal/serverboot/multitenant_integration_test.go:38-73`), and the mount site sits inline in `Run()`. Reaching it in process would need an extracted seam this proposal does not add. TEST-3 drives the mount site on the binary, including under a provider that rejects an unknown org (`TestLayerEndpoint_MultiTenantUnknownOrgRejected`), where each CODE-4 wrapper choice changes the response, and TEST-1 pins the routing logic in process, so TEST-2 would repeat TEST-3.
- Keying outbound §7.3.2 webhook receivers per routed tenant. Proposal 0046 covers it.
- Naming `injected-session-token` in §6.3.1. The binary rejects an unknown org with `auth.tenant_unknown` under every verifier-installing provider except `trusted-headers` (`internal/serverboot/serverboot.go:1454`), and an injected token carries `org_id` (`spec/06-mcp-server.md:93`; `pkg/identity/runtime.go:262-263`). §6.3.1, the §6.10 row and its prose, §4.7.1 Provisioning, and §13.12 name only `oidc-jwt`. The gap predates this proposal and applies to the meta-tool chain as well. This proposal keeps the shared helper's behavior and pins only the `oidc-jwt` case end to end (TEST-3). A follow-up change proposal states the `injected-session-token` routing in §6.3.1.
- Keying the §4.7.8 audit-volume meter per tenant. §4.7.8 states a per-tenant daily cap (`spec/04-artifact-model.md:905`), and the meter counts the events it records under the boot tenant (`internal/serverboot/serverboot.go:1606`, `internal/serverboot/reingest.go:39`), so on a multi-tenant registry the cap is deployment-wide. This is a known spec-to-code gap that predates this proposal and is documented in DOC-1(e). A correct per-tenant meter records each event under its own tenant, which `core.AuditEvent` does not carry today.
- Admitting erasure on a multi-tenant registry, by partitioning the audit redaction by tenant or by amending §4.7.1 and §6.3.1 for a shared stream (OQ-3).
- Granting the §4.7.2 admin role a stream override, or changing any change-event stream delivery.
- Changing the §4.6 no-identity bypass for meta-tool reads, or refusing the free-form-label posture at startup.
- Moving the layer routes under `srv.Handler()` and `withIdentityVerification`, which would change the §7.3.1 credential-failure semantics on the write operations.
- Adding a §6.10 error code, environment variable, flag, SPI, or SDK change.
- Provisioning or seeding a per-tenant admin for a newly created tenant, an existing gap in `/v1/admin/tenants`.
- Migrating layer rows that a pre-fix multi-tenant registry wrote into the default tenant on behalf of routed callers. Pre-1.0, owners register them again from the owning tenant, an admin of the default tenant unregisters the stale rows, and CL-1 states the affected window.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-03, automated)

- **DOC-1(f) named a nonexistent `docs/consuming/layers.md`.** DOC-1(f) now targets `docs/deployment/layers.md` line 143 (the Git webhook row) and the line 85 registration comment, and adds a line 119 edit qualifying the whole-list sentence with the multi-tenant tenant selection rule.
- **The Edge cases table cited DOC-1(f) for clustered.md edits and for pre-fix-rows text no DOC item staged.** The clustered.md references are relabelled DOC-1(e), and DOC-1(e) step 5 now stages the sentence that pre-release layer rows stay in the default tenant and are moved by unregistering and re-registering from the owning tenant.
- **The §7.3.4 posture `manage_any_layer` stayed unrouted.** CODE-4 mounts `/v1/ui/session` through `TenantRouted`, CODE-2 makes `Capabilities` report false for an unrouted request on a multi-tenant endpoint, SPEC-2 part 3 qualifies the §7.3.4 "member is true there" sentence, DOC-1(h) and DOC-1(i) carry the docs, TEST-1 case 10 and TEST-3 step 7 pin it, and the Summary, Decisions, Watch-outs, Edge cases, checklist, and CL-1 name it.
- **The §8.5 bullet declared one registry-wide audit stream, contradicting §4.7.1 and §6.3.1 and letting a tenant admin redact other tenants' records.** The staged default is now the fail-closed refusal: erasure on a multi-tenant registry answers `403 auth.forbidden` before the admin gate, any store access, and any audit rewrite (SPEC-2 part 2, CODE-2). OQ-3 names the §4.7.1 and §6.3.1 conflict and the constraint any lifting proposal satisfies; the Decisions, Summary, Edge cases, TEST-1 case 2, TEST-3, DOC-1(c), (e), and (h), CL-1, and the Non-goals follow.
- **SPEC-2(d) said the §8.1 event names a tenant.** (d) now names "the tenant §7.6 records for the change event the operation publishes"; DOC-1(c) and CL-1 use the matching wording.
- **The TEST-1 fixture reused a router stub that maps every org to `default`.** The fixture now specifies its own resolver through `server.WithTenantRouter`, mirroring `tenantResolver`, with `rejectUnknown` per case, and states that it does not reuse the `tenants_test.go` stub.
- **An Edge cases row said §4.7.8 never states the audit-volume meter per tenant.** The row, the Fixed decision, the Decisions bullet, and the Non-goal now record the deployment-wide meter as a known divergence from the §4.7.8 per-tenant cap (`spec/04-artifact-model.md:905`), and DOC-1(e) documents it on the clustered.md Quotas bullet.
- **The admin-register assertions passed against the regression.** TEST-3 step 2 and S85 step 3 now assert `layer.user_defined` false, and TEST-3 step 2 states that the pre-fix outcome is `201` with `user_defined` true.
- **TEST-1 case 3 expected a `403` the register path cannot produce.** Case 3 now asserts `user_defined` false in `A` and true in `B` for the same register body, and pins the `B` admin refusal with an update of a seeded admin-defined layer that answers `403 auth.forbidden`. The matching Edge cases row now states the register outcome.
- **No test pinned a multi-tenant boot with no tenant router.** TEST-1 case 11 wraps a multi-tenant endpoint in `TenantRouted` of a router-less `Server`, and TEST-3 adds `TestLayerEndpoint_MultiTenantNoRouter` on the binary, asserting the empty list, the `403` refusals on register and erase, and the false posture member.
- **The routed posture read added a `401 auth.tenant_unknown` refusal that the unedited §7.3.4 lead paragraph rules out.** CODE-1 adds `(*Server).TenantRoutedNoReject`, which routes like `TenantRouted` but leaves an unresolved org unrouted, and CODE-4 mounts `/v1/ui/session` through it, so a verified `oidc-jwt` token whose org names no provisioned tenant reads `200` with `manage_any_layer` false. SPEC-2 part 3 now states that the posture read applies no §6.3.1 refusal. The Summary, Fixed decisions, checklist S2 and S5, the Decisions bullet, CODE-2, the Edge cases row, the TEST-1 fixture and case 10, and DOC-1(h) follow, and §7.3.4 keeps its "refuses no request", "for no other purpose", and "discloses no tenant data" sentences unchanged.

### Pass 2 (2026-10-03, automated)

- **The paragraph after the open-points list called the webhook item "the third point".** It now reads "The last point", which is the inbound-webhook item after the erasure and posture items were added.
- **Staged §8.5 answered every multi-tenant erase `403`, while the `TenantRouted` erase mount answered a verified `oidc-jwt` caller with an unprovisioned org `401 auth.tenant_unknown` first.** CODE-4 now leaves `/v1/admin/erase` unwrapped (`internal/serverboot/serverboot.go:1514`, `:1454`; `pkg/registry/server/server.go:474-479`), because the refusal reads `multiTenant` alone. The SPEC-2 part 2 bullet states that the refusal takes the place of the §6.3.1 `auth.tenant_unknown` rejection, and the Summary, Fixed decisions, Decisions, checklist S5, the Edge cases rows, the TEST-1 fixture and cases 2 and 11, DOC-1(c), DOC-1(h), and CL-1 follow. TEST-1 case 2 adds an erase with `rejectUnknown` set and an unknown org that asserts `403` and an unchanged audit file.
- **No test pinned the tenant-qualified `webhook_url` on the `rotate_webhook_secret` update path.** CODE-3 names both `webhookURL` callers (`pkg/registry/server/layers.go:1142`, `:1393`) and requires `cfg.TenantID`. TEST-1 case 9 adds a `B` rotation that asserts the `/v1/ingest/webhook/B/<id>` path and a delivery signed with the rotated secret, and TEST-3 step 4 adds dave's rotation asserting the globex tenant ID in the URL.

### Pass 3 (2026-10-03, automated)

- **No binary-level test pinned the CODE-4 mount choices that differ only under a provider that rejects an unknown org.** TEST-3 adds `TestLayerEndpoint_MultiTenantUnknownOrgRejected`, which boots the standard stack with `PODIUM_MULTI_TENANT=true`, `PODIUM_WEB_UI=true`, and the `injected-session-token` provider `msStandardEnv` sets (`test/e2e/standard_stack_parity_test.go:210`; `rejectUnknownTenant` true per `internal/serverboot/serverboot.go:1450-1455`). It asserts a routed control token, then for an `org_id` naming no provisioned tenant `401 auth.tenant_unknown` on `GET /v1/layers`, `403 auth.forbidden` on `POST /v1/admin/erase`, and `200` with `manage_any_layer` false on `GET /v1/ui/session`. Checklist S7, the TEST-3 lead sentence, CODE-4, and the TEST-2 Non-goal name the case.

### Pass 4 (2026-10-03, automated)

- **TEST-1 case 4 could not detect `effectiveLayerCap` still reading the boot tenant's quota.** The case now runs with no `WithMaxUserLayers` override, which returns before the quota lookup (`pkg/registry/server/layers.go:582-585`), and gives tenant `A` a quota of 1 and tenant `B` a quota of 2. The fixture seeds two user-defined layers owned by dave in `A` through the store. Both of dave's registers routed to `B` get `201`, and his register routed to `A` answers `429 quota.layer_count_exceeded` with `details.limit` 1 and `details.current` 2. A count at `:1313` that reads `A` refuses the first `B` register, and a quota lookup at `:586` that reads `A` refuses the second. The case cites `// Spec: §7.3.1 (Tenant selection)`.
- **Correction: the rewritten TEST-1 case 4 did not detect an owned-layer count that still reads the boot tenant.** With one seeded `A` layer and `B`'s cap of 2, a count that reads `A` computes 1+1 against 2 on both `B` registers, and both get `201`, so the case passed under that regression while claiming to pin `:1313`. The fixture now seeds two user-defined layers for dave in `A` and none in `B`, keeps the `A` quota at 1 and the `B` quota at 2, and expects `details.current` 2 on the `A` refusal. A count that reads `A` refuses the first `B` register (2+1 > 2, `pkg/registry/server/layers.go:1312-1325`), and a quota lookup that reads `A` refuses the second (1+1 > 1, `:582-589`). The case 4 text and the bullet above state the same values.

### Pass 5 (2026-10-03, automated)

- **The web UI still offered registration to an authenticated caller that resolves to no tenant, which the Tenant selection rule refuses, and the Decisions bullet claimed the posture change prevented that.** The posture read reports `subject` for that caller and `mayTake` admits registration on `caps.manage_any_layer || ownedByCaller(newLayerTarget(subject), subject)` (`web/ui/src/surfaces/layerrights.ts:75-77`, `:92`; `web/ui/src/App.tsx:339`; `web/ui/src/surfaces/LayerPanel.tsx:335`), contrary to the §13.10 layer-panel rule (`spec/13-deployment.md:179`). SPEC-2 part 4 amends §13.10 so the Tenant selection refusal joins the refusals the panel presents and the registry answers, and replaces the counted "Two arms are exceptions." lead-in. No posture member and no web UI change are added, because the registry already states the refusal and such a caller lists no layers, so registration is the only control affected. The Decisions bullet now limits its claim to the admin arm, and the Summary, Fixed decisions, checklist S1, the open-points list, a new Edge cases row, TEST-1 case 10 (which now cites `§13.10` and asserts the posture inputs and the `403` on that caller's register), and DOC-1(i) follow.
- **No test detected the reorder response list or the default register order still reading the boot tenant.** CODE-2 now names `register`'s owned-layer count and default-order read (`pkg/registry/server/layers.go:1313`, `:1366`) and `reorder`'s lists before and after the renumbering (`:1550`, `:1581`) among the reads that take the resolved tenant. TEST-1 case 2 starts with `B` empty and `A` holding a layer at order 500, asserts the first two `B` registers store orders 10 and 20, asserts the `B` reorder response names exactly `B`'s layers under an always-admit callback, and asserts a repeated reorder with the same order publishes no event (`:1590`, `:1599`).
- **The precedence-comparison citation pointed at a comment line.** CODE-2, TEST-1 case 2, and the entry above cited `pkg/registry/server/layers.go:1588`, which falls inside the comment block above the comparison. They now cite `:1590`, the `slices.Equal(precedenceSequence(layers), precedenceSequence(updated))` check that decides whether the reorder event is published, alongside `:1599`, the response write.

### Redesign 1 (2026-10-03, automated)

- **Areas redesigned.** Two areas were redesigned. The first is the statement of unrouted outcomes, which the Summary, Fixed decisions, Decisions, CODE-4, the Edge cases table, DOC-1, and CL-1 each restated per provider. The second is the TEST-1 detection of a read left on the boot tenant, which relied on per-case detectors against a boot tenant `A` that is also an ordinary routed tenant.
- **Why the outcome statements changed.** The restatements named `oidc-jwt` as the rejecting provider, while the code rejects an unknown org under every verifier-installing provider except `trusted-headers` (`internal/serverboot/serverboot.go:1454`). A single Unrouted outcome table in Decisions now states the response of the layer read, the layer writes, `POST /v1/admin/erase`, and the §7.3.4 posture read for each caller class, keyed by `rejectUnknownTenant`. The table also states the per-route precedence of the existing guards and the webhook outcomes. Every other section cites the table, and DOC-1 and CL-1 describe the rejecting case by its observable behavior rather than by a provider name.
- **Why the end-to-end unknown-org case changed provider.** `TestLayerEndpoint_MultiTenantUnknownOrgRejected` used `injected-session-token`, whose unknown-org rejection §6.3.1 does not state. It now boots under `oidc-jwt` with the `startOIDCTestIdP` TLS IdP, so it pins only behavior the spec states. It skips on darwin through `requireCustomTrustStore`, and TEST-1 cases 2, 5, and 10 cover the same cells in process. A new Non-goal records the `injected-session-token` spec gap for a follow-up proposal.
- **Why the boot-tenant detection changed.** The endpoint's boot tenant is now a poisoned tenant `P` (`poison-p`) that no org routes to. `P` holds admin grants, twins of the fixture layers, a high-order public layer, and poison-owned twins for case 4. One constructor, `newTenantRoutingFixture`, fails a case on any change to `P`'s rows, any `poison-p` marker in a response, or any `poison-p` scope at the event, notification, and reingest stubs. The four reads these checks cannot observe (the default-order read, unregister's delete, the owned-layer count, and the quota lookup) are asserted in cases 2 and 4. CODE-2 now states a grep invariant: after the change `e.tenantID` is read only in the single-tenant arm of `tenant`, and CODE-2, CODE-3, and CODE-4 read the single-tenant erase and webhook tenant through `tenant`.
- **Deleted.** The Decisions bullets "Unrouted outcomes use existing codes and fail closed" (replaced by the table) and "A multi-tenant registry without a verifier is wholly unrouted" (now row U6); the eight Edge cases rows from the `oidc-jwt` unknown-org row through the webhook unknown-tenant row, the multi-tenant erase row, and the three posture rows (replaced by two rows); the provider-specific unrouted sentences in Fixed decisions, the posture Decisions bullet, CODE-4, DOC-1(c), (f), (h), and (i), and CL-1; the TEST-1 "third tenant value" sentence and the boot tenant `A`; case 2's order-500 layer in `A` and its second-reorder no-event detector; case 4's detectors that read `A` as the boot tenant; and the `injKeyPair`-signed `injected-session-token` tokens (`injClaims`, `injSignJWT`) in TEST-3. The key pair itself stays, because `msStandardEnv` seeds `PODIUM_RUNTIME_KEYS_PATH` from it.
- **Reconciled.** The Summary test bullet names the poisoned boot tenant. The Watch-outs gain entries for the grep invariant and the `injected-session-token` gap, and the macOS entry names the darwin skip of the unknown-org case. Checklist S3 names the grep invariant, S6 names the fixture constructor, and S7 names the `oidc-jwt` IdP and the Linux-only lane; no step dependency changed. The TEST-3 lead sentence names the darwin skip.
- **Skipped edits.** None. Every anchor in the redesign matched the proposal text exactly once.
- **Open decisions recorded by the redesign.** The provider for the end-to-end unknown-org case defaults to `oidc-jwt`; the alternative keeps `injected-session-token`, which runs on darwin without an IdP but pins rejection behavior the spec does not state. The treatment of the `injected-session-token` rejection defaults to a Non-goal; the alternative extends §6.3.1 in this proposal, which changes the §6.10 row and several spec sites and widens SPEC-2 beyond this defect.

### Pass 6 (2026-10-03, automated)

- **The TEST-1 posture fixture left `SessionPosture.Identity` nil, so case 10's `subject` assertions could not pass.** `SessionPosture.Handler` writes `subject` only when `Identity` is non-nil (`pkg/registry/server/webui_session.go:73-75`), and the fixture set only `Capabilities`, while the CODE-4 boot mount sets `Identity: layerIdentity` (`internal/serverboot/serverboot.go:1565`). The fixture now mounts `server.SessionPosture{Identity: postureIdentity, Capabilities: layers.Capabilities}`, where `postureIdentity` is a test-local closure that mirrors the unexported `layerIdentityResolver` (`internal/serverboot/identity_verify.go:74-82`): it calls the same `X-Test-User`/`X-Test-Org` stub verifier, and it resolves `layer.Identity{IsPublic: true}` on a verification error, so an `X-Test-Fail` credential reads as anonymous. The fragment that introduced the stub verifier is now a complete sentence.

### Pass 7 (2026-10-03, automated)

- **The TEST-1 fixture never installed the stub verifier on the `Server`, so `TenantRouted` routed no request.** `WithTenantRouter` sets only `tenantRouter` and `rejectUnknownTenant` (`pkg/registry/server/server.go:220-225`), `s.idVerifier` is set only by `WithIdentityVerifier` (`server.go:209-211`), and CODE-1 leaves a request unrouted when no verifier is installed, so cases 1 through 5 and case 10 could not pass. The fixture now builds the `Server` with `server.WithIdentityVerifier(stub)` and `server.WithTenantRouter(resolve, rejectUnknown)`, passes the same stub to the endpoint through `WithIdentityResolver` (`pkg/registry/server/layers.go:208`), and uses it in `postureIdentity`, mirroring serverboot's single `layerVerify` (`internal/serverboot/serverboot.go:1389`, `:1416`, `:1440`, `:1492`). Case 11 keeps `WithIdentityVerifier(stub)` and drops only `WithTenantRouter`, and its text now states that a router-less `TenantRouted` passes the request through unrouted whether or not a verifier is installed.

### Pass 8 (2026-10-03, automated)

- **The documented remediation for pre-fix default-tenant rows was unreachable by the owner.** After CODE-2 every layer write acts in the tenant §6.3.1 selects from the caller's organization (`internal/serverboot/orgid.go:23-38`, `spec/06-mcp-server.md:80`), so the owner's unregister looks the layer up in the owning tenant and answers `404 registry.not_found` (`pkg/registry/server/layers.go:1498-1501`), and the owner arm of `authorizeLayerWrite` (`layers.go:323-329`) never applies to a default-tenant caller. The Edge cases row, DOC-1(e) step 5, the CL-1 `Fixed` entry, and the Non-goals bullet now state that the owner registers the layer again from the owning tenant, with no prior unregister because the rows live under different tenant keys, and that an admin of the default tenant, such as a bootstrap admin whose organization is `default`, removes the stale row with `podium layer unregister` or `DELETE /v1/layers?id=...`.
