# Proposal 0046: Key webhook receivers per tenant: receiver CRUD on the request's routed tenant and delivery on the event's scope tenant (§7.3.2)

- Issue: (to be filed)
- Status: Applied to spec (2026-10-03). The approval was decided on the user's behalf under the overnight authorization, and the staged edits were signed off as written. OQ-1: accept the documented re-registration on upgrade for every deployment that sets PODIUM_WEBHOOK_STORE_PATH, single-tenant included; a load-time rewrite of "default" rows would be a migration path that code-best-practices.md disallows pre-1.0, and the operator action is stated in the CHANGELOG.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.3.2 states that each receiver belongs to the tenant whose admin registered it, that the receiver CRUD endpoints read and write only the receivers of the tenant the request resolves to (§6.3.1) and answer an `id` that names another tenant's receiver as an unknown `id`, that delivery reaches only the receivers of the event's tenant, and that an event published with no tenant reaches no receiver, and that within the event's tenant a receiver's event filter is the only narrowing applied (SPEC-1).
- `pkg/webhook`: `Worker.Deliver` returns without listing any receiver when the tenant is empty, and `FileStore.Put` refuses a receiver with an empty URL, tenant, or ID, as `MemoryStore.Put` already does (CODE-1, with its unit tests and fixture repairs in TEST-1).
- `pkg/registry/server/webhooks.go`: the list, create, get, update, and delete handlers key the webhook store on `s.core.TenantFor(r.Context())` (CODE-2). `pkg/registry/server/events.go`: the webhook goroutine in `Server.PublishEvent` delivers under `scope.TenantID` (CODE-3).
- `pkg/registry/server/server.go`: the `tenant` field and `WithTenant` comments name the §4.7.8 quota limiter as their only reader, and the webhook fixtures drop `WithTenant` (CODE-4).
- Tests at every level the change reaches: the `pkg/webhook` unit tests (TEST-1), an in-process two-tenant HTTP integration suite (TEST-2), and a multi-tenant standard-stack end-to-end suite on the binary (TEST-3).
- Docs, changelog, and manual validation follow: the HTTP API reference drops the shared-pool note and describes per-tenant receivers, the unrouted refusals, and the store file's tenant keying, and `docs/consuming/custom-via-sdk.md` confines the receiver-narrowing sentence to the event's tenant (DOC-1), the `[Unreleased]` section gains a `Fixed` and a breaking `Changed` entry (CL-1), and `test/manual-validation.md` gains scenario S84 (MV-1).

**Fixed decisions.**

- The fix lands in this proposal, and SPEC-1 lands in `spec/` before any code.
- Receiver CRUD keys on the `*core.Registry` method `s.core.TenantFor(r.Context())`. No package-level `core.TenantFor` exists, and none is added.
- Delivery keys on `scope.TenantID` from the `core.EventScope` that `PublishEvent` already receives.
- The empty-tenant guard lives in `Worker.Deliver`. `FileStore.load` keeps loading legacy rows unchanged.
- The change adds no §6.10 error code and no CRUD branch for an unrouted request. The existing `401 auth.tenant_unknown` and `403 auth.forbidden` refusals follow from §6.3.1 and the §7.3.2 Receiver authorization paragraph, and SPEC-1 does not restate them.
- A request that names another tenant's receiver `id` gets the response an unknown `id` gets: `404 registry.not_found` on `GET` and `PUT`, `204 No Content` on `DELETE`, and the other tenant's receiver is left in place.
- The change adds no migration, re-keying, or dual lookup for receivers persisted under the literal `default`, and it lands in a MINOR bump.
- `Server.tenant` and `WithTenant` stay, because the §4.7.8 quota limiter still reads `s.tenant`.

**Watch out for.**

- **CODE-2 and CODE-3 land in one commit.** CODE-2 alone stores receivers under the routed tenant while `Deliver` still lists the literal `default`, which breaks delivery on every deployment, single-tenant ones included.
- **Single-tenant deployments are affected.** A single-tenant registry binds to the UUIDv5 of `podium:org:default` (`internal/serverboot/orgid.go:43-71`, `pkg/registry/core/tenant.go:18-19`), so `TenantFor` returns that UUID and never the literal `default`. Do not assume that a single-tenant boot keeps the old key.
- **`server.NewFromFilesystem` is misleading.** It is the only constructor that creates a tenant whose ID is the literal `default` (`pkg/registry/server/server.go:319-320`), and serverboot does not use it. A test built on it does not reproduce the boot binding.
- **CODE-1's `FileStore.Put` validation breaks two existing tests silently.** `TestFileStore_DeletePersists` and `TestFileStore_ListByTenant` (`pkg/webhook/file_store_test.go:87-123`) `Put` receivers with no URL and discard the error. After CODE-1 the first passes vacuously and the second fails. TEST-1(4) repairs both in the same commit as CODE-1.
- **A webhook fixture whose core tenant differs from its receivers' `TenantID` stops receiving deliveries.** The fixtures that pass `WithWebhooks` (`pkg/registry/server/admin_test.go`, `events_test.go`, `readonly_writes_test.go`, `webhook_actor_test.go`, `webhooks_test.go`, `receiver_wire_test.go`, `webhook_routing_test.go`, `webhooks_secret_test.go`, `webhook_debounce_integration_test.go`, `webhooks_crud_test.go`, `webhooks_hardening_test.go`, `test/e2e/http_api_test.go`, and `test/e2e/plugin_spi_test.go`) bind core to the same string their receivers and scopes use today. Confirm that for each one after S3, because a mismatch shows up as a delivery wait timing out rather than as a failed assertion.
- **The multi-tenant binary publishes every layer and ingest event under the bootstrap tenant.** The layer endpoint is mounted outside `withTenantRouting` and writes to the boot tenant (`internal/serverboot/serverboot.go:1484`, `:1498-1500`). The binary therefore cannot produce an event of a non-bootstrap tenant. TEST-2 covers that direction in process, and TEST-3 asserts only the bootstrap-tenant direction for delivery.
- **`requireSubprocessTLSTrust` skips the whole test on darwin.** TEST-3 therefore splits into two functions. Call it only in `TestWebhookReceivers_MultiTenantDeliveryIsolation`, immediately before step 6, so `TestWebhookReceivers_MultiTenantCRUDIsolation` runs on every platform with a stack.
- **Parts of `test/e2e` skip silently on macOS, and TEST-3 skips without `PODIUM_POSTGRES_DSN` and `PODIUM_S3_BUCKET`.** Grep the run output for `SKIP` before claiming end-to-end verification.
- **Spec and code line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §7.3.2 states per-tenant receiver ownership, CRUD and delivery confinement, and the no-tenant delivery rule.
      Levels: —. Depends on: —
- [ ] **S2 · code** — CODE-1, TEST-1. `Worker.Deliver` refuses an empty tenant, `FileStore.Put` validates like `MemoryStore.Put`, and the unit tests and the repaired fixtures land with them. Bundled because TEST-1(4) repairs fixtures that CODE-1 breaks, and both touch `pkg/webhook` for one reviewer.
      Levels: unit. Depends on: S1
- [ ] **S3 · code** — CODE-2, CODE-3. Receiver CRUD keys on the routed tenant and delivery on the event's scope tenant. Bundled because either change alone breaks delivery on every deployment.
      Levels: unit, integration, e2e. Depends on: S2
- [ ] **S4 · code** — CODE-4. The `tenant` field and `WithTenant` comments name the quota limiter, and the webhook fixtures drop `WithTenant`.
      Levels: unit, integration, e2e. Depends on: S3
- [ ] **S5 · test** — TEST-2. In-process two-tenant HTTP integration suite for receiver CRUD and delivery.
      Levels: integration. Depends on: S3
- [ ] **S6 · test** — TEST-3. Multi-tenant standard-stack end-to-end suite on the binary.
      Levels: e2e. Depends on: S3
- [ ] **S7 · docs** — DOC-1. The HTTP API reference drops the shared-pool note and describes per-tenant receivers, and `docs/consuming/custom-via-sdk.md` confines the receiver-narrowing sentence to the event's tenant. Land it in the same pull request as S3 so no released build documents behavior it lacks.
      Levels: —. Depends on: S3
- [ ] **S8 · docs** — CL-1. The `[Unreleased]` `Fixed` and `Changed` entries.
      Levels: —. Depends on: S3
- [ ] **S9 · docs** — MV-1. Manual-validation scenario S84.
      Levels: —. Depends on: S3

**Ordering constraints.** S1 lands the rule every later step cites. S2 precedes S3 so the empty-tenant guard is in place before delivery starts keying on a value that a future publisher could leave empty. S4, S5, and S6 need the wiring from S3 and may proceed in parallel.

## Current state and the gap

### The spec makes receivers per-tenant

§7.3.2 configures receivers per org, calls the fan-out tenant-wide, and requires the per-tenant admin role for receiver CRUD because receivers are an org-level configuration. §4.7 makes the org the tenant boundary. Proposal 0042 recorded the receiver-tenancy row as having no spec text (its OQ-1) and landed an interim note in `docs/reference/http-api.md` that documents the shared pool (commit c924ca12).

### The code keeps one receiver pool per process

Every receiver CRUD handler keys the webhook store on the Server's fixed `s.tenant` field: list (`pkg/registry/server/webhooks.go:91`), the create that stamps `TenantID: s.tenant` (`webhooks.go:139`), get (`webhooks.go:182`), the `PUT` read-modify-write (`webhooks.go:195`), and delete (`webhooks.go:253`). `server.New` sets `s.tenant` to the literal `"default"` (`pkg/registry/server/server.go:284`), and serverboot never calls `WithTenant` (`server.go:271`). The only callers are tests: `pkg/registry/server/events_test.go:465`, `webhook_debounce_integration_test.go:124`, `webhook_routing_test.go:62` and `:249`, `webhook_actor_test.go:44`, `options_test.go:29`, and `test/e2e/plugin_spi_test.go:527`.

Delivery has the same defect. `Server.PublishEvent` receives a `core.EventScope` carrying the event's tenant (`pkg/registry/server/events.go:249`) and uses it only for the stream audience (`events.go:274`). The webhook goroutine calls `s.webhooks.Deliver(context.Background(), s.tenant, ...)` (`events.go:265`), and `Worker.Deliver` lists receivers by that argument alone (`pkg/webhook/webhook.go:190-191`).

### The cross-tenant exposure

On a multi-tenant registry, serverboot binds core to `podium:unrouted` (`internal/serverboot/serverboot.go:1106-1108`) and installs the tenant router (`serverboot.go:1455`). `requireAdmin` evaluates the admin grant against the routed tenant through `core.AdminAuthorize` and `r.TenantFor(ctx)` (`pkg/registry/server/admin.go:113-122`, `pkg/registry/core/admin.go:21-32`). An admin of tenant A therefore passes the gate and then lists, reads, updates, and deletes the receivers that tenant B's admin registered, and every receiver receives the events of every tenant.

### Single-tenant deployments are affected by the fix

A single-tenant registry binds to `orgIDForName("default")`, the UUIDv5 of `podium:org:default` (`internal/serverboot/orgid.go:43-71`, `pkg/registry/core/tenant.go:18-19`, `internal/serverboot/serverboot.go:887` and `:1106-1115`). `TenantFor` returns that UUID when no router is installed (`pkg/registry/core/core.go:217-222`). Receivers that `PODIUM_WEBHOOK_STORE_PATH` persisted under the literal `"default"` (`pkg/webhook/file_store.go:24`, `:89-95`, `:130-131`) therefore stop being listed and stop receiving events on every deployment that sets that variable. Only the test helper `server.NewFromFilesystem` creates a tenant whose ID is the literal `"default"` (`server.go:319-320`), and serverboot does not use it.

### The store alone does not fail closed

`MemoryStore.Put` rejects an empty `TenantID` (`pkg/webhook/webhook.go:494-497`). `FileStore.Put` and `FileStore.load` accept one (`pkg/webhook/file_store.go:56-58`, `:117-122`), and `FileStore.List("")` returns such rows (`file_store.go:89-100`). No product path writes such a row: server CRUD stamps a non-empty tenant, and `recordResult` writes back only rows it already listed (`webhook.go:328`, `:356`). A store file written by hand or by an external tool can still hold one. §7.6 states the empty-tenant rule only for the change-event stream, and §7.3.2 states no counterpart for receivers.

### The unrouted refusal already holds

Under a verified provider, `withTenantRouting` rejects an unresolvable org with `401 auth.tenant_unknown` (`pkg/registry/server/server.go:465-480`, `internal/serverboot/serverboot.go:1454`). Under `trusted-headers` the request falls through to the never-provisioned `podium:unrouted` tenant (`internal/serverboot/orgid.go:16`), which holds no admin grant, so `requireAdmin` refuses with `403 auth.forbidden` before any store access (`webhooks.go:85-88`, `:166-168`). `handleAdminGrants` returns the same code (`admin.go:23-25`). Both outcomes follow from §6.3.1 per-request tenant selection and the §7.3.2 Receiver authorization paragraph. No test-delivery endpoint exists (`server.go:422-424`, `docs/reference/http-api.md:714-718`), so the CRUD surface to re-key is list, create, get, update, and delete.

## Decisions

- **D1. Fix the defect in this proposal.** The re-keying implements existing §7.3.2 text (per-org receivers, a tenant-wide fan-out, and the per-tenant admin role) read with §4.7, where the org is the tenant. The SPEC-1 sentences make the tenant ownership explicit and add the empty-tenant delivery rule, which §7.3.2 does not state today. Under `spec-driven-development.md` that rule lands in `spec/` before the code.
- **D2. CRUD keys on `s.core.TenantFor(r.Context())`.** It is the method on `*core.Registry` (`pkg/registry/core/core.go:217`). No package-level `core.TenantFor` exists, and none is added. The server already resolves the routed tenant this way in `handleEvents` (`pkg/registry/server/events.go:143`).
- **D3. Delivery keys on `scope.TenantID`.** The ingest publishers fill it from the row tenant: `domain.published` at `pkg/registry/ingest/ingest.go:496`, `artifact.published` and `artifact.deprecated` at `ingest.go:849`, and `layer.ingested` and `layer.history_rewritten` through `layerScope` at `pkg/registry/ingest/orchestrator.go:213-214`. The `layer.config_changed` publishers fill it from the `LayerEndpoint`'s boot tenant (`pkg/registry/server/layers.go:453` and `:1596`, `internal/serverboot/serverboot.go:1484`). That is the tenant whose layer configuration the write changed, because the endpoint writes to that tenant, so keying delivery on it attributes the event correctly. Routing the layer endpoint per tenant is a separate concern (see the non-goals).
- **D4. The empty-tenant guard goes in `Worker.Deliver`.** Every caller of the canonical fan-out then fails closed (`pkg/webhook/webhook.go:190`). `FileStore.Put` also gains the validation `MemoryStore.Put` already performs (`webhook.go:494-497`), so both Store implementations refuse a receiver with no tenant. `FileStore.load` keeps loading legacy rows unchanged. The `Deliver` guard keeps an empty-tenant row unreachable, and refusing the file at boot would turn a stale row into a startup failure.
- **D5. No new error code and no new CRUD branch for the unrouted case.** The existing refusals (`401 auth.tenant_unknown` under a verified provider and `403 auth.forbidden` under `trusted-headers`) already run before any store access. They follow from §6.3.1 and the §7.3.2 Receiver authorization paragraph, so SPEC-1 does not restate them. TEST-2 and TEST-3 pin them.
- **D6. Another tenant's receiver `id` is answered as an unknown `id`.** `GET` and `PUT` return `404 registry.not_found` (`webhooks.go:182-186`, `:195-199`). `DELETE` returns `204 No Content`, because both stores' `Delete` is idempotent on a missing key (`pkg/webhook/webhook.go:513-520`, `file_store.go:123-128`). The other tenant's receiver is left untouched. The response does not disclose whether the `id` exists in another tenant.
- **D7. No migration, re-keying, or dual lookup for receivers persisted under the literal `"default"`** (`code-best-practices.md`, Configuration and compatibility). The breaking change reaches every deployment that sets `PODIUM_WEBHOOK_STORE_PATH`, single-tenant ones included, because their bound tenant is the UUIDv5 of the `default` org rather than the literal string. CL-1 states the operator action. A deployment without `PODIUM_WEBHOOK_STORE_PATH` holds receivers in memory and loses them on every restart, so it needs no action beyond the usual re-registration after the upgrade restart. The change lands in a MINOR bump.
- **D8. `Server.tenant` and `WithTenant` stay.** The §4.7.8 quota limiter still reads `s.tenant` (`pkg/registry/server/server.go:818`, `:843`, `:1018`). Their doc comments are rewritten to drop the webhook role, and the webhook tests that pass `WithTenant` for receiver lookup drop that option (CODE-4).

## Spec amendment: §7.3.2 receiver tenancy

**SPEC-1.** `spec/07-external-integration.md`, §7.3.2 "Outbound Webhooks", the paragraph that follows the single-event JSON schema block. The paragraph currently reads:

> Receivers are configured per org (URL + HMAC secret). The tenant-wide receiver fan-out does not apply the §4.6 layer visibility evaluator or the §7.6 change-event stream visibility rule: a receiver carries no caller identity, and its event filter is the only narrowing applied to the events it receives.

Insert four sentences after the first sentence, so the paragraph reads:

> Receivers are configured per org (URL + HMAC secret). Each receiver belongs to the tenant (§4.7.1) whose admin registered it. The receiver CRUD endpoints read and write only the receivers of the tenant the request resolves to (§6.3.1), so a request routed to one tenant neither lists nor addresses a receiver of another tenant, and an `id` that names another tenant's receiver is answered as an unknown `id`. The registry delivers an event only to the receivers of the tenant the event belongs to. An event published with no tenant reaches no receiver. The tenant-wide receiver fan-out does not apply the §4.6 layer visibility evaluator or the §7.6 change-event stream visibility rule: a receiver carries no caller identity, and within the event's tenant its event filter is the only narrowing applied to the events it receives.

Nothing else in §7.3.2 changes. The receiver object keeps "The object carries no tenant identifier", which still holds because ownership is implicit in the tenant the request resolves to. The Receiver authorization paragraph keeps its text, and together with §6.3.1 per-request tenant selection it already determines the refusal of a request that resolves to no provisioned tenant. The amendment adds no §6.10 error code, receiver field, environment variable, or matrix cell.

## Proposed solution

### CODE-1. `pkg/webhook`: `Deliver` fails closed on an empty tenant; `FileStore.Put` validates like `MemoryStore.Put`

Targets: `pkg/webhook/webhook.go` (`Worker.Deliver`, the doc comment at lines 171-189 and the body at line 190), and `pkg/webhook/file_store.go` (`FileStore.Put`, lines 116-120).

No product path writes a receiver with an empty tenant: server CRUD stamps a non-empty tenant (`server.go:284`, `core.go:217-222`), and `recordResult` writes back only rows it already listed (`webhook.go:328`, `:356`). A store file written by hand or by an external tool can still hold such a row, and `FileStore` loads it and lists it under the empty key (`file_store.go:56-58`, `:89-100`). The `Deliver` guard makes the SPEC-1 rule independent of the Store implementation and of what the file contains.

(a) At the top of `Worker.Deliver`, before `w.Store.List`:

```go
// Spec: §7.3.2 — an event published with no tenant reaches no receiver.
// The guard sits in the fan-out entry point so it holds for every Store,
// including a FileStore that loaded a hand-written row with no tenant.
if tenantID == "" {
	return nil
}
```

Rewrite the first doc-comment line to: "Deliver fans the event out to every matching receiver of tenantID. An empty tenantID reaches no receiver, and Deliver returns nil without reading the store (§7.3.2)." Keep the rest of the doc comment.

(b) In `FileStore.Put`, before taking the lock:

```go
if r.URL == "" || r.TenantID == "" || r.ID == "" {
	return ErrInvalidConfig
}
```

`FileStore.load` is unchanged (D4).

(c) Update the existing `FileStore` fixtures that `Put` a receiver with no URL. `TestFileStore_DeletePersists` (`pkg/webhook/file_store_test.go:92`) and `TestFileStore_ListByTenant` (`file_store_test.go:108-110`) each gain `URL: "https://example/hook"`, and their ignored `_ = s.Put(...)` calls fail the test on error, so the validation cannot turn them into vacuous passes. TEST-1(4) carries this repair.

### CODE-2. Server: receiver CRUD keys on the request's routed tenant

Target: `pkg/registry/server/webhooks.go` (`handleWebhooksList`, lines 85-150, and `handleWebhookOne`, lines 160-260).

In both handlers, after the `requireAdmin` check succeeds and before the method switch, resolve the tenant once:

```go
// Spec: §7.3.2 — a receiver belongs to the tenant the request resolves
// to (§6.3.1), the same tenant requireAdmin authorized the caller in, so
// an admin of one tenant never reads or writes another tenant's receivers.
tenant := s.core.TenantFor(r.Context())
```

Replace `s.tenant` with `tenant` at `webhooks.go:91` (List), `:139` (`TenantID: tenant` on create), `:182` (Get), `:195` (the `PUT`'s Get), and `:253` (Delete). The `PUT` write path stores `current`, whose `TenantID` comes from the Get keyed on `tenant`, so the write stays in the routed tenant without a further edit. Keep `requireAdmin` ahead of the resolution so the unrouted refusals (D5) still run before any store access. No status code changes (D6).

### CODE-3. Server: webhook delivery keys on the event's scope tenant

Target: `pkg/registry/server/events.go` (the `PublishEvent` doc comment at lines 238-248 and the goroutine at line 265).

Replace `s.tenant` with `scope.TenantID` in the webhook goroutine:

```go
go func() {
	_ = s.webhooks.Deliver(context.Background(), scope.TenantID, eventType, traceID, actor, data)
}()
```

`scope` is a value parameter, so the closure reads `scope.TenantID` directly and no local copy is needed. CODE-3 lands in the same commit as CODE-2 (step S3).

In the doc comment, replace "Receivers are not filtered by scope (§7.3.2)." with: "The receiver pool is the receivers of the scope's tenant (scope.TenantID), and an event with no tenant reaches no receiver. Within that pool, each receiver's event filter is the only narrowing applied, and the §7.6 layer and path conditions do not apply to receivers (§7.3.2)." In the inline comment above the goroutine (`events.go:257-263`), edit the sentence that begins "Worker.Deliver fans the event out to every matching receiver in the tenant": replace "in the tenant" with "of the event's tenant (scope.TenantID)" and keep the rest of that sentence. Keep the first sentence (the asynchronous-delivery rationale) and the background-context sentence unchanged. Keep `// Spec: §7.6` and add `// Spec: §7.3.2`.

### CODE-4. Server: drop the webhook role from the tenant field and `WithTenant` comments; update webhook test fixtures

Targets: `pkg/registry/server/server.go` (the `tenant` field comment at lines 92-95 and the `WithTenant` comment at lines 267-270), and the webhook fixtures listed below.

After CODE-2 and CODE-3 the webhook path no longer reads `s.tenant`. Only the quota limiter does (`server.go:818`, `:843`, `:1018`), so the comments that call the field the webhook receiver lookup tenant become false (D8).

1. Rewrite the `tenant` field comment: "tenant is the key the §4.7.8 quota limiter charges search and materialize calls to. Webhook receivers do not read it: receiver CRUD keys on the request's routed tenant and delivery on the event's scope tenant (§7.3.2)."
2. Rewrite the `WithTenant` comment: "WithTenant sets the key the §4.7.8 quota limiter charges."
3. Remove the `WithTenant` option from every webhook fixture that passes it for receiver lookup: `pkg/registry/server/events_test.go:465` (`"t"`), `webhook_debounce_integration_test.go:124`, `webhook_routing_test.go:62`, `webhook_routing_test.go:249`, `webhook_actor_test.go:44`, and `test/e2e/plugin_spi_test.go:527` (all `"default"`). In each one, confirm that the `core.New` bound tenant, the receivers' `TenantID`, and the published `EventScope.TenantID` are the same string. All six satisfy this today, so the removal changes no test outcome.
4. Leave `options_test.go:29` in place, because it exercises the option itself.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| An admin of tenant A lists receivers | Only tenant A's receivers | §7.3.2 "read and write only the receivers of the tenant the request resolves to" (SPEC-1); `docs/reference/http-api.md` Outbound webhooks (DOC-1(b)) |
| An admin of tenant A creates a receiver | The receiver is stored under tenant A | §7.3.2 "Each receiver belongs to the tenant ... whose admin registered it" (SPEC-1); DOC-1(b), DOC-1(d) |
| An admin of tenant A sends `GET` or `PUT` with tenant B's receiver `id` | `404 registry.not_found`; tenant B's receiver is unchanged | §7.3.2 "answered as an unknown `id`" (SPEC-1); DOC-1(c) |
| An admin of tenant A sends `DELETE` with tenant B's receiver `id` | `204 No Content`; tenant B's receiver is left in place | §7.3.2 "answered as an unknown `id`" (SPEC-1); DOC-1(c) |
| An event of tenant A | Delivered to tenant A's matching receivers only | §7.3.2 "delivers an event only to the receivers of the tenant the event belongs to" (SPEC-1); DOC-1(b) |
| An event published with no tenant | Reaches no receiver; no store read | §7.3.2 "An event published with no tenant reaches no receiver" (SPEC-1); DOC-1(b) |
| A hand-written store file row with no tenant ID | Loaded at boot; listed by no tenant's CRUD and delivered no event (accepted, D4) | §7.3.2 no-tenant sentence (SPEC-1); DOC-1(d) |
| `FileStore.Put` of a receiver with no URL, tenant, or ID | `ErrInvalidConfig`; nothing persisted | No spec text needed: no product path writes such a row; CODE-1 brings `FileStore` to `MemoryStore`'s existing validation |
| `oidc-jwt` request whose `org_id` names no provisioned tenant on a multi-tenant registry | `401 auth.tenant_unknown` before any receiver is read (existing) | §6.3.1 Per-request tenant selection; DOC-1(c) |
| `oidc-jwt` token with no `org_id`, or `trusted-headers` request whose organization resolves to no tenant, on a multi-tenant registry | `403 auth.forbidden` before any receiver is read (existing) | §6.3.1 and §7.3.2 Receiver authorization; DOC-1(c) |
| Receivers persisted under the literal `default` by an earlier release | Neither listed nor delivered to after the upgrade; they stay inert in the file until the operator removes it (accepted, D7) | §7.3.2 "Each receiver belongs to the tenant ... whose admin registered it" (SPEC-1); `CHANGELOG.md` Changed (CL-1) |
| Deployment without `PODIUM_WEBHOOK_STORE_PATH` | Receivers held in memory, lost on restart (existing); registered again after the upgrade restart | Existing sentence in `docs/reference/http-api.md` Receiver CRUD ("Receivers are held in memory unless ..."); CL-1 |
| Multi-tenant binary: a layer or ingest event | Carries the bootstrap tenant, so only the bootstrap tenant's receivers receive it (accepted; the layer endpoint and ingest are not routed per tenant) | §7.3.2 delivery sentence (SPEC-1); DOC-1(b) bootstrap-tenant sentence |
| A debounced receiver of tenant A | Its trailing window collects only tenant A's events, because tenant selection precedes the window | §7.3.2 Per-receiver debounce window (existing) with SPEC-1; no docs change |
| Two tenants' admins write receivers concurrently | Each write lands in its own tenant; the store's existing mutex serializes the file write | §7.3.2 (SPEC-1); no docs change |

## Testing

**TEST-1 · unit, `pkg/webhook/file_store_test.go` and `pkg/webhook/webhook_test.go` (package `webhook_test`).** Lands in S2 with CODE-1. Each test function carries `// Spec: §7.3.2`.

1. Drop the drafted `TestDeliver_ConfinesToTenant`. `TestFileStore_ListByTenant` covers store-level confinement, and TEST-2(c) covers delivery confinement end to end through `PublishEvent`.
2. `TestDeliver_EmptyTenantReachesNoReceiver`, `FileStore` only. Write a JSON file by hand containing one receiver with `"TenantID": ""` and a URL that points at a `newReceiverServer` (`pkg/webhook/webhook_test.go:54`). Load it with `LoadFileStore`, call `Deliver(ctx, "", ...)`, and assert a nil error, zero deliveries at the receiver server, and an unchanged `FailureCount` from `Get(ctx, "", id)`. No `MemoryStore` repeat, because `MemoryStore.Put` cannot create an empty-tenant row and the repeat would pass with or without the guard.
3. `TestFileStore_PutRejectsIncompleteReceiver`. A table over an empty URL, an empty `TenantID`, and an empty ID. Each case asserts `errors.Is(err, webhook.ErrInvalidConfig)`, reloads the file with `LoadFileStore`, and asserts that `List` for that case's tenant is empty. Fold each case into one combined assertion through a small local `must` helper, which keeps assertion branches from counting as uncovered lines in `codecov/patch`.
4. Repair the fixtures CODE-1's validation breaks, in the same commit. Give every receiver in `TestFileStore_DeletePersists` and `TestFileStore_ListByTenant` (`pkg/webhook/file_store_test.go:87-123`) a non-empty URL, and replace `_ = s.Put(...)` with checked errors so a refused `Put` fails the test.

**TEST-2 · integration, `pkg/registry/server/webhooks_tenant_test.go` (new, package `server_test`).** Lands in S5. Each test function carries `// Spec: §7.3.2`, and the routing cases also carry `// Spec: §6.3.1`. Run under `go test -race`, because delivery runs on a goroutine.

Fixture: a memory store holding tenants `A` and `B`, with alice granted admin in `A` and bob granted admin in `B`. `core.New` binds to a third tenant, so a handler that reads the bound tenant instead of the routed one fails visibly. `server.New` takes `WithWebhooks` over a `MemoryStore` worker whose SSRF policy admits the `httptest` receivers, `WithTenantRouter` with a resolver that maps `"A"` to `A` and `"B"` to `B`, and a `WithIdentityResolver` that sets both `Sub` (from `X-Test-User`) and `OrgID` (from `X-Test-Org`) on an authenticated identity, because `withTenantRouting` resolves from `id.OrgID`. The fixture never passes `WithTenant`. Reuse the `tenantResolver` wiring in `internal/serverboot/multitenant_integration_test.go` where that keeps the fixture smaller. Polling uses the `waitFor` helper pattern from `webhook_routing_test.go:68`.

- (a) CRUD isolation. alice creates `ra` routed to `A`, and bob creates `rb` routed to `B`. alice's `GET /v1/webhooks` returns `[ra]`, and bob's returns `[rb]`. The stored rows carry `TenantID` `A` and `B` respectively. The case fails today, because both list under `"default"`.
- (b) Cross-tenant `id`. alice's `GET /v1/webhooks/{rb}` and `PUT /v1/webhooks/{rb}` each return `404 registry.not_found`, and the `PUT` leaves `rb`'s `url` and `event_filter` unchanged. alice's `DELETE /v1/webhooks/{rb}` returns `204 No Content`, and bob's `GET /v1/webhooks/{rb}` then returns `200` with the receiver. Then the same-tenant positive path: bob's `PUT /v1/webhooks/{rb}` with `{"disabled": true}` returns `200`, and bob's next `GET /v1/webhooks/{rb}` returns `200` with `disabled: true`. bob's `DELETE /v1/webhooks/{rb}` returns `204`, bob's next `GET /v1/webhooks/{rb}` returns `404 registry.not_found`, and the store holds no row for tenant `B` and `id` `rb`. The cross-tenant outcomes alone also match a handler that still reads `s.tenant` at the `PUT`'s Get (`webhooks.go:195`) or at Delete (`webhooks.go:253`), because the fixture's `s.tenant` is `"default"` and both stores' `Delete` is idempotent on a missing key. The same-tenant `PUT` and `DELETE` fail against either regression.
- (c) Delivery isolation in both directions. Register `ra` in `A` and `rb` in `B`. Publish `layer.config_changed` with `EventScope{TenantID: "A"}` and data `{"marker": "A"}`, then the same with `TenantID` `"B"` and `{"marker": "B"}`. `waitFor` until each receiver has exactly one hit, then assert that `ra`'s body carries marker `A` and `rb`'s carries marker `B`. The positive barrier needs no negative window, and the case fails under today's `s.tenant` keying.
- (e) Unrouted refusal. `GET /v1/webhooks` and `GET /v1/webhooks/{ra}`, each with `X-Test-Org: globex` (unresolvable), under `rejectUnknown=false` (`403 auth.forbidden`) and under `rejectUnknown=true` (`401 auth.tenant_unknown`). After each request, assert that the receiver store's row count is unchanged. Cites `// Spec: §7.3.2` (Receiver authorization) and `// Spec: §6.3.1`.
- (f) Single-tenant binding. A second fixture has no router, binds `core.New` to `"T"`, and grants alice admin in `T`. alice creates a receiver, the stored row carries `TenantID` `"T"`, and a `PublishEvent` with `EventScope{TenantID: "T"}` reaches it. The case fails today, because `s.tenant` is `"default"` while the core is bound to `"T"`.

The empty-tenant rule is pinned in TEST-1(2) and is not repeated here, because in this fixture an empty-scope publish reaches no receiver with or without the fix.

**TEST-3 · e2e, `test/e2e/webhook_tenant_isolation_test.go` (new).** Lands in S6. The tenant router, the bootstrap admin seeding, and the delivery goroutine are wired only in the booted binary. The test follows the 0042 multi-tenant pattern (`test/e2e/events_visibility_test.go:189-255`). It runs only on the standard stack and skips through `msSkipIfNoStack` when `PODIUM_POSTGRES_DSN` or `PODIUM_S3_BUCKET` is unset, so a run claimed as verification must be grepped for `SKIP`.

Split it into two test functions:

- `TestWebhookReceivers_MultiTenantCRUDIsolation` covers steps 1-5 and 7. Its only gate is `msSkipIfNoStack`, so it runs on every platform with a stack. Annotations: `// Spec: §7.3.2`, `// Spec: §6.3.1`, and `// Spec: §4.7.1` (the org name `default` resolves to the bootstrap tenant's ID).
- `TestWebhookReceivers_MultiTenantDeliveryIsolation` covers steps 1-4 and 6. It also calls `requireSubprocessTLSTrust(t)` immediately before step 6. Annotation: `// Spec: §7.3.2`.

Steps:

1. Create two TLS sinks with `newNotificationSink(t, withSinkTLS())` (`test/e2e/notification_sink_helpers_test.go:129`, `:136`) before boot. Boot with `msStartStandardServerEnv` (`test/e2e/standard_stack_parity_test.go:150`) and the extra environment `PODIUM_IDENTITY_PROVIDER=trusted-headers`, `PODIUM_MULTI_TENANT=true`, a per-run `PODIUM_TRUSTED_PROXY_SECRET`, `PODIUM_BOOTSTRAP_ADMINS=<carol-suffix>@acme.com`, `PODIUM_OPERATOR_ADMINS=<dave-suffix>@acme.com`, `PODIUM_WEBHOOK_ALLOWED_TARGETS=<both sink hosts>`, and `SSL_CERT_FILE=<written sink certificate>`, all as explicit `extraEnv` entries. If building those strings duplicates `notification_sink_helpers_test.go:367-370`, extract a small shared helper that returns them and use it in both places. Do not route through `webhookHardeningServer` or `withSink`, whose `bootOption`s `msStartStandardServerEnv` cannot accept.
2. dave provisions `globex-<suffix>` through `POST /v1/admin/tenants` and the test records the returned org ID.
3. Seed bob's admin grant in that tenant by calling `GrantAdmin` directly on a Postgres store opened with the stack DSN. No API grants a new tenant's first admin, because `handleAdminGrants` calls `requireAdmin` against the routed tenant.
4. carol (`X-Podium-User-Org: default`) creates `ra` pointing at sink A, and bob (`X-Podium-User-Org: globex-<suffix>`) creates `rb` pointing at sink B. Both return `201`.
5. carol's list holds `ra` and no `rb`, and bob's list holds `rb` and no `ra`. carol's `GET` and `PUT` of `rb` return `404 registry.not_found`, carol's `DELETE` of `rb` returns `204`, and bob's `GET` of `rb` then returns `200`.
6. carol registers two user-defined layers whose git URLs are under `.invalid`, with per-run IDs, and `POST /v1/layers/reorder` with their IDs in reverse registration order. Sink A receives exactly one `layer.config_changed` POST with a valid `X-Podium-Signature`. After sink A has received it, sink B receives no POST within the existing event window. This direction is the only one the binary can produce, because every layer event carries the bootstrap tenant.
7. Refusal. carol with `X-Podium-User-Org: initech-<suffix>` (unprovisioned) and carol with no `X-Podium-User-Org` each get `403 auth.forbidden` on `GET /v1/webhooks` and on `GET /v1/webhooks/{ra}`. carol's and bob's lists are unchanged afterwards.

Subjects, tenant names, and layer IDs carry a per-run suffix so the shared database does not collide across runs.

**Coverage.** Measure with the cross-package profile (`go test -coverpkg=./... -coverprofile=cover.out ./pkg/webhook/... ./pkg/registry/server/...`) and confirm the changed lines in `webhooks.go`, `events.go`, `webhook.go`, and `file_store.go` reach 85%. The serverboot wiring is unchanged, so no subprocess coverage target moves.

## Manual validation

**MV-1.** Add scenario S84 to `test/manual-validation.md` after S83, the last scenario in the current tree (`test/manual-validation.md:10188`), and add the index row after the S83 row (`test/manual-validation.md:234`). If another proposal takes S84 before this one is implemented, use the next free S-number at apply time, append the scenario after the last scenario and the index row after the last index row, and use that one number in the index row, the scenario heading, checklist step S9, and the Summary bullet that names the scenario.

The scenario runs on the standard stack. A standalone SQLite boot with `PODIUM_MULTI_TENANT` would start, but §13.10 keeps multi-tenancy out of scope for standalone, so a manual check of a standalone multi-tenant registry would validate a configuration the spec does not define. The standard stack matches TEST-3.

```markdown
| S84 | Webhook receivers are isolated per tenant on a multi-tenant registry | standard | none | none | Postgres, S3 |
```

Lands in S9. The scenario text:

~~~~markdown
## S84: Webhook receivers are isolated per tenant on a multi-tenant registry

**Goal.** Validate that on a multi-tenant registry each tenant's admin lists
and addresses only its own webhook receivers, that a request naming another
tenant's receiver is answered as an unknown receiver, that an unrouted request
is refused, and that an event of one tenant is delivered only to that tenant's
receivers.

**Covers.** The §7.3.2 receiver tenancy rule, §6.3.1 per-request tenant
selection under `trusted-headers`, and the receiver CRUD and delivery path
through the compiled binary.

**Prerequisites.** Local Postgres and MinIO from `make services-up`, plus
`test.env` (Postgres DSN, S3 settings), `psql`, `openssl`, and `python3`. Skip
if any is absent. Go on macOS verifies TLS against the system keychain and
ignores `SSL_CERT_FILE`, so on macOS run step 6 only after trusting
`$WORK/sink.pem` in the login keychain, or run the scenario on Linux.

**Why by hand.** An operator reads the raw POST bodies and `X-Podium-Signature`
headers at both listeners side by side, together with the two
`GET /v1/webhooks` lists. Any POST at bob's listener after carol's reorder, or
any receiver of the other tenant in either list, is the defect this scenario
catches.

**Steps.**

1. Run the isolation block. Start services, load the environment, and create a
   self-signed certificate for `127.0.0.1` that both listeners share.

   ```bash
   (cd "$REAL_HOME/projects/podium" && make services-up)
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export SFX="$$"
   openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=127.0.0.1" \
     -addext "subjectAltName=IP:127.0.0.1" \
     -keyout "$WORK/sink.key" -out "$WORK/sink.pem"
   cat > "$WORK/sink.py" <<'EOF'
   import http.server, ssl, sys
   class H(http.server.BaseHTTPRequestHandler):
       def do_POST(self):
           body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
           print("SIG", self.headers.get("X-Podium-Signature"), flush=True)
           print("BODY", body.decode(), flush=True)
           self.send_response(200); self.end_headers()
   srv = http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H)
   ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
   ctx.load_cert_chain(sys.argv[2], sys.argv[3])
   srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
   srv.serve_forever()
   EOF
   python3 "$WORK/sink.py" 9541 "$WORK/sink.pem" "$WORK/sink.key" > "$WORK/carol-sink.log" 2>&1 &
   python3 "$WORK/sink.py" 9542 "$WORK/sink.pem" "$WORK/sink.key" > "$WORK/bob-sink.log" 2>&1 &
   ```

   **Expect.** Both listeners are running, and both log files are empty.

2. Boot a multi-tenant `trusted-headers` server with carol as the bootstrap
   admin of the `default` tenant and dave as an operator.

   ```bash
   export SECRET="mv-proxy-$SFX"
   PODIUM_IDENTITY_PROVIDER=trusted-headers PODIUM_MULTI_TENANT=true \
   PODIUM_TRUSTED_PROXY_SECRET="$SECRET" \
   PODIUM_BOOTSTRAP_ADMINS="carol-$SFX@acme.com" \
   PODIUM_OPERATOR_ADMINS="dave-$SFX@acme.com" \
   PODIUM_WEBHOOK_ALLOWED_TARGETS=127.0.0.1/32 \
   SSL_CERT_FILE="$WORK/sink.pem" \
   PODIUM_NO_EMBEDDINGS=true \
   podium serve --bind 127.0.0.1:8184 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   export PODIUM_REGISTRY=http://127.0.0.1:8184
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null "$PODIUM_REGISTRY/healthz"
   server_alive "$SRV" "$WORK/srv.log"
   as() { # as <user> <org> <curl args...>
     local u="$1" o="$2"; shift 2
     if [ -n "$o" ]; then
       curl -s -w '\n%{http_code}\n' -H "X-Podium-Proxy-Secret: $SECRET" \
         -H "X-Podium-User-Sub: $u" -H "X-Podium-User-Org: $o" "$@"
     else
       curl -s -w '\n%{http_code}\n' -H "X-Podium-Proxy-Secret: $SECRET" \
         -H "X-Podium-User-Sub: $u" "$@"
     fi
   }
   ```

   **Expect.** `server_alive` reports the server running. The boot passes no
   layer path, because step 6 registers the scenario's layers over HTTP, and
   `PODIUM_NO_EMBEDDINGS=true` keeps search BM25-only, so the boot needs no
   embedding provider or API key. The `as` helper sends the organization header
   through an explicit branch, so the block behaves the same under bash and zsh.

3. dave provisions the `globex` tenant, and the operator seeds bob's admin grant
   in it directly in Postgres. No API grants a new tenant's first admin.

   ```bash
   as "dave-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/admin/tenants" \
     -H 'Content-Type: application/json' -d "{\"name\":\"globex-$SFX\"}" | tee "$WORK/tenant.json"
   GLOBEX_ID=$(sed '$d' "$WORK/tenant.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   psql "$PODIUM_POSTGRES_DSN" -c "INSERT INTO \"org_$GLOBEX_ID\".admin_grants (user_id, org_id, granted_at) VALUES ('bob-$SFX@globex.com', '$GLOBEX_ID', now());"
   ```

   **Expect.** The provisioning request returns HTTP 201 with the tenant's ID,
   and `psql` reports `INSERT 0 1`.

4. carol registers a receiver for her listener, and bob registers one for his.

   ```bash
   as "carol-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/webhooks" \
     -H 'Content-Type: application/json' -d '{"url":"https://127.0.0.1:9541/h"}' | tee "$WORK/ra.json"
   as "bob-$SFX@globex.com" "globex-$SFX" -X POST "$PODIUM_REGISTRY/v1/webhooks" \
     -H 'Content-Type: application/json' -d '{"url":"https://127.0.0.1:9542/h"}' | tee "$WORK/rb.json"
   RA=$(sed '$d' "$WORK/ra.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   RB=$(sed '$d' "$WORK/rb.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   ```

   **Expect.** Both requests return HTTP 201 with the receiver and its unmasked
   secret.

5. Each admin lists receivers, and carol addresses bob's receiver by its `id`.

   ```bash
   as "carol-$SFX@acme.com" default "$PODIUM_REGISTRY/v1/webhooks"
   as "bob-$SFX@globex.com" "globex-$SFX" "$PODIUM_REGISTRY/v1/webhooks"
   as "carol-$SFX@acme.com" default "$PODIUM_REGISTRY/v1/webhooks/$RB"
   as "carol-$SFX@acme.com" default -X PUT "$PODIUM_REGISTRY/v1/webhooks/$RB" \
     -H 'Content-Type: application/json' -d '{"disabled":true}'
   as "carol-$SFX@acme.com" default -X DELETE "$PODIUM_REGISTRY/v1/webhooks/$RB"
   as "bob-$SFX@globex.com" "globex-$SFX" "$PODIUM_REGISTRY/v1/webhooks/$RB"
   as "carol-$SFX@acme.com" initech "$PODIUM_REGISTRY/v1/webhooks"
   as "carol-$SFX@acme.com" "" "$PODIUM_REGISTRY/v1/webhooks"
   ```

   **Expect.** carol's list holds `$RA` alone, and bob's list holds `$RB` alone.
   carol's `GET` and `PUT` of `$RB` return HTTP 404 with `registry.not_found`.
   carol's `DELETE` of `$RB` returns HTTP 204. bob's `GET` of `$RB` then
   returns HTTP 200 with `disabled: false`. The `initech` request and the
   request with no organization header each return HTTP 403 with
   `auth.forbidden`.

6. carol registers two user-defined layers whose git URLs are under `.invalid`,
   so no fetch runs, and reorders them in reverse registration order, which
   publishes `layer.config_changed`.

   ```bash
   for id in "mv-a-$SFX" "mv-b-$SFX"; do
     as "carol-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/layers" \
       -H 'Content-Type: application/json' \
       -d "{\"id\":\"$id\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/acme/$id.git\",\"ref\":\"main\"}"
   done
   as "carol-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/layers/reorder" \
     -H 'Content-Type: application/json' -d "{\"order\":[\"mv-b-$SFX\",\"mv-a-$SFX\"]}"
   sleep 5
   cat "$WORK/carol-sink.log"; echo ---; cat "$WORK/bob-sink.log"
   ```

   **Expect.** Each registration returns HTTP 201, and the reorder returns HTTP
   200. `carol-sink.log` holds a `SIG` line with a non-empty signature and a
   `BODY` line whose `event` is `layer.config_changed` and whose `data.layer`
   names both layers. `bob-sink.log` is empty.
~~~~

Step 2 passes no `--layer-path`. The flag only exports `PODIUM_LAYER_PATH` (`cmd/podium/serve.go:30`, `:69-71`), and boot opens that directory with `filesystem.Open` and fails on a missing path (`internal/serverboot/serverboot.go:464-472`, `pkg/registry/filesystem/registry.go:121-123`). Nothing in the scenario creates `$WORK/reg`, and the scenario registers its layers over HTTP, as the 0042 multi-tenant e2e test boots without a layer path (`test/e2e/standard_stack_parity_test.go:150`). Step 2 also sets `PODIUM_NO_EMBEDDINGS=true`. Without it a `postgres` store defaults the vector backend to `pgvector` and the embedding provider to `openai` (`internal/serverboot/serverboot.go:2398-2424`), config validation then requires `OPENAI_API_KEY` (`serverboot.go:2583-2585`), and `test.env.example:118` ships that key commented out. `PODIUM_NO_EMBEDDINGS=true` sets both to `none` (`serverboot.go:2393-2396`), which matches the index row's `none` embeddings and `none` vector backend.

The layer registration body matches the one `evRegisterUserLayer` sends (`test/e2e/events_visibility_test.go:267-275`), and `POST /v1/admin/tenants` returns the tenant under `id` with HTTP 201 on a first create (`pkg/registry/server/tenants.go:33`, `:167-169`).

## Documentation changes

**DOC-1.** `docs/reference/http-api.md`, Outbound webhooks section ((a) through (d)), and `docs/consuming/custom-via-sdk.md` ((e)). Lands in S7.

(a) Remove the `[!NOTE]` callout at lines 686-687, which reads:

> On a multi-tenant registry, every routed tenant shares one receiver pool: a receiver registered by one tenant's admin receives the events of every tenant.

(b) In the paragraph at line 684, after "Configure receivers per org (URL + HMAC secret).", insert:

> Each receiver belongs to the tenant whose admin registered it. The receiver CRUD routes read and write only the receivers of the tenant the request resolves to, and the registry delivers an event only to the receivers of the event's tenant. An event with no tenant reaches no receiver. On a multi-tenant registry, layer and ingest events belong to the bootstrap `default` tenant, so only that tenant's receivers receive them.

In the same paragraph, replace "A receiver receives every event its event filter matches, whatever layer the event names, and it carries no layer scope." with:

> A receiver receives every event of its tenant that its event filter matches, whatever layer the event names, and it carries no layer scope.

The current sentence was true of the shared pool that (a) removes, and left unchanged it contradicts the inserted delivery sentence.

(c) After the Receiver CRUD authorization paragraph at line 721 ("Every method on these routes requires the per-tenant admin role ..."), append:

> On a multi-tenant registry, a request routed to no provisioned tenant is refused before any receiver is read. Under `oidc-jwt`, a token whose `org_id` names no provisioned tenant returns `401 auth.tenant_unknown`. A token without an `org_id`, and any request under `trusted-headers` whose organization resolves to no tenant, returns `403 auth.forbidden`. A single-tenant registry does not consult the organization value and serves every request against its sole tenant. A `GET` or `PUT` of another tenant's receiver `id` returns `404 registry.not_found`, and a `DELETE` of one returns `204 No Content` and leaves the receiver in place.

(d) In the paragraph that begins "`POST` accepts", after "in which case the store reloads them on restart.", append:

> The file stores each receiver under the ID of the tenant that registered it. The registry loads a row that carries no tenant ID and neither lists it nor delivers to it.

(e) In `docs/consuming/custom-via-sdk.md` at line 138, replace "Outbound webhooks carry the same event types to receivers that a tenant admin configures, and a receiver's event filter is the only narrowing applied to it." with:

> Outbound webhooks carry the same event types to the receivers of the event's tenant, which a tenant admin configures, and within that tenant a receiver's event filter is the only narrowing applied to it.

The SPEC-1 receiver paragraph, DOC-1(b), DOC-1(e), and the CODE-3 `events.go` doc comment ("Within that pool, each receiver's event filter is the only narrowing applied") state the same predicate: delivery is confined to the event's tenant, and the event filter is the only narrowing within it.

The note about files written by earlier releases (rows keyed under `default`) and the re-registration step belong to the CL-1 Changed entry. The reference page states current behavior only.

**CL-1.** `CHANGELOG.md`, `## [Unreleased]`. Lands in S8.

Under `### Fixed` (line 100), add:

> - **Webhook receivers are keyed per tenant** (§7.3.2): receiver CRUD reads and writes only the receivers of the tenant the request resolves to, and the registry delivers each event only to the receivers of the event's tenant. Previously every tenant on a multi-tenant registry shared one receiver pool, so one tenant's admin could list, update, and delete another tenant's receivers, and every receiver received the events of every tenant. An event with no tenant now reaches no receiver, and a `PODIUM_WEBHOOK_STORE_PATH` store refuses to write a receiver with no URL, tenant, or ID.

Under `### Changed` (line 188), add:

> - **Persisted webhook receivers must be registered again** (§7.3.2): a `PODIUM_WEBHOOK_STORE_PATH` file written by an earlier release keys every receiver under `default`. That key matches no tenant ID, including the single-tenant `default` org, whose ID is a UUID, so after the upgrade those receivers are neither listed nor delivered to. Before upgrading, record each receiver's `url`, `event_filter`, and `debounce` from `GET /v1/webhooks`, and its secret from the store file, because the API returns the secret masked. After upgrading, register each receiver again with `POST /v1/webhooks`, passing the recorded `secret`. Removing the old file before the restart discards the stale rows. A deployment without `PODIUM_WEBHOOK_STORE_PATH` keeps receivers in memory and is unaffected beyond the usual re-registration after a restart. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the former keying.

## Open questions

**OQ-1. Upgrade impact on single-tenant deployments.** The task assumed that single-tenant deployments already route to `"default"` and are unaffected. They are not: their bound tenant is the UUIDv5 of the `default` org (`internal/serverboot/orgid.go:64-71`), so every deployment that sets `PODIUM_WEBHOOK_STORE_PATH` loses its persisted receivers on upgrade and must register them again. The draft accepts this under the pre-1.0 no-shim rule and documents the operator action in CL-1. The alternative is a one-time load-time rewrite of `default` rows to the bound tenant on single-tenant boots, which is a migration path `code-best-practices.md` disallows for external compatibility. Confirm that the documented re-registration is acceptable.

## Non-goals

- Routing the §7.3.1 layer endpoint per tenant. It is mounted outside `withTenantRouting` and writes to the boot tenant (`internal/serverboot/serverboot.go:1484`, `:1498-1500`), so `layer.config_changed` and ingest events on a multi-tenant binary carry the bootstrap tenant. This proposal delivers each event to the receivers of the tenant it carries.
- Keying the §4.7.8 quota limiter per routed tenant. It still charges `s.tenant`, the literal `"default"` (`pkg/registry/server/server.go:818`, `:843`, `:1018`), which is a separate cross-tenant sharing defect for a follow-up proposal.
- An API path that grants the first admin of a newly provisioned tenant. TEST-3 and MV-1 seed it directly in the store.
- Migrating, re-keying, or pruning receivers persisted under `"default"`. No shim or dual lookup is added (D7), and stale rows stay inert in the file until the operator removes it.
- Refusing to load a `PODIUM_WEBHOOK_STORE_PATH` file that contains an empty-tenant row (D4).
- A test-delivery endpoint. None exists in the spec, the server, the docs, or the CLI.
- Any change to `DeliverBatch`, the debounce buffer, the SSRF policy, or the receiver object's wire fields.
- A §7.3.2 sentence restating the unrouted CRUD refusal. §6.3.1 per-request tenant selection and the existing Receiver authorization paragraph already determine it, and a second copy could drift from its source (D5).

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-03, automated)

- **MV-1 step 2 booted with `--layer-path "$WORK/reg"`, a directory nothing creates, so boot failed in `filesystem.Open`.** Fixed in MV-1 step 2: the `podium serve` command drops `--layer-path`, because step 6 registers the layers over HTTP. The Expect block and the paragraph after the scenario record why, with the `serve.go`, `serverboot.go`, and `filesystem/registry.go` evidence.
- **MV-1 booted the standard stack with the default `pgvector` and `openai` embedding provider, which requires `OPENAI_API_KEY`, while the prerequisites and the index row named none.** Fixed in MV-1 step 2: the boot sets `PODIUM_NO_EMBEDDINGS=true`, which matches the index row's `none` embeddings and vector backend. The paragraph after the scenario cites the default and validation sites.
- **No listed test failed if the `PUT` and `DELETE` handlers kept keying on `s.tenant`.** Fixed in TEST-2(b): bob's same-tenant `PUT` of `rb` returns `200` and persists the change, and bob's same-tenant `DELETE` returns `204` and removes the `(B, rb)` row. Either regression fails one of these checks.
- **CODE-3 pointed the inline-comment rewrite at the first sentence, which is the asynchronous-delivery rationale.** Fixed in CODE-3: the edit targets the sentence that begins "Worker.Deliver fans the event out", replaces "in the tenant" with "of the event's tenant (scope.TenantID)", and keeps the other sentences.

### Pass 2 (2026-10-03, automated)

- **MV-1's `as` helper built the optional `X-Podium-User-Org` header with `${o:+-H "..."}`, which zsh expands as a single word (`test/manual-validation.md:33-40`), so every routed request in S84 lost its organization header under zsh.** Fixed in MV-1 step 2: the helper branches on `[ -n "$o" ]` and passes `-H "X-Podium-User-Org: $o"` as two words only when an organization is given, following the `post()` helper in `test/manual-validation.md:2606-2614`. The step 2 Expect block records that the helper behaves the same under bash and zsh.

### Pass 3 (2026-10-03, automated)

- **DOC-1(c) and the edge-case row for the `403 auth.forbidden` refusal stated the tenant-routing refusals without a multi-tenant qualifier, although serverboot installs the tenant router only when `cfg.multiTenant && verifierInstalled` (`internal/serverboot/serverboot.go:1450`) and `withTenantRouting` passes the request through when no router is installed (`pkg/registry/server/server.go:452-455`).** Fixed in DOC-1(c): the appended text opens with "On a multi-tenant registry," and adds that a single-tenant registry does not consult the organization value and serves every request against its sole tenant, matching §6.3.1 (`spec/06-mcp-server.md:75`). The edge-case row for the `403 auth.forbidden` case gains "on a multi-tenant registry", matching the `401 auth.tenant_unknown` row.

### Pass 4 (2026-10-03, automated)

- **The receiver-narrowing sentences in `docs/reference/http-api.md:684` ("A receiver receives every event its event filter matches") and `docs/consuming/custom-via-sdk.md:138` ("a receiver's event filter is the only narrowing applied to it") stayed unqualified after DOC-1, and SPEC-1 kept "its event filter is the only narrowing applied" unqualified, which contradicts the new tenant confinement and the CODE-3 comment.** Fixed in DOC-1(b), which now also replaces the third sentence of the line 684 paragraph with "A receiver receives every event of its tenant that its event filter matches" and corrects the anchor from line 685 to line 684; in the new DOC-1(e), which rewrites `custom-via-sdk.md:138` to confine delivery to the event's tenant; and in the SPEC-1 amended paragraph, which reads "within the event's tenant its event filter is the only narrowing applied". The DOC-1 header, the Summary, and checklist step S7 name the added page.
