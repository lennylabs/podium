# Proposal 0051: Record the tenant on every registry audit event and scope §8.5 erasure to the requesting tenant on a multi-tenant registry

- Issue: (to be filed)
- Status: Implemented (2026-10-04). OQ-1: one shared chain with a tenant attribute (Decision 1). OQ-2: unlabeled records are untouched by a tenant-scoped erase and not reported (Decision 7). OQ-3: operator tenant.managed events record no tenant (Decision 4). OQ-4: keep all three fixes in this proposal as drafted: the post-erase re-anchor (SPEC-3), the superseded_head key, and the endpoint-sink user.erased emission.
- Date: 2026-10-04

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §8.1 gains a `tenant` attribute on registry audit events: the org whose audit stream the event belongs to, empty for events about the registry as a whole, and part of the hashed body only when non-empty (SPEC-1). `pkg/audit.Event` carries it, `canonicalBody` hashes it conditionally, and the JSON line carries it as a top-level `tenant` key (CODE-1). Request-scoped emitters set it from one rule keyed on multi-tenant mode, which a new `server.WithMultiTenant()` option carries to the server. Ingest and re-sign events record the tenant that owns the layer or artifact, and deployment-wide emitters leave it empty (CODE-3).
- §8.5 replaces the 0047 multi-tenant refusal with tenant-scoped erasure. On a multi-tenant registry `POST /v1/admin/erase` acts in the §6.3.1-routed tenant, redacts and discovers aliases only in that tenant's records, leaves unlabeled records unchanged without reporting anything about them, and records `user.erased` on an endpoint sink as well as a file sink (SPEC-2). `audit.EraseUser` takes an `EraseScope` (CODE-2), the handler resolves the tenant before the admin check (CODE-4), serverboot mounts the route through `srv.TenantRouted` (CODE-5), and the offline CLI form, which §8.5 now names, keeps whole-file scope with corrected help text (CODE-6).
- §8.6 requires an immediate re-anchor after an erasure rewrites the chain, and names `superseded_head` on both rewrite events (SPEC-3). The erase handler calls a `WithAfterErase` hook wired to the existing `reAnchor` closure (CODE-4, CODE-5). This part is separable; see OQ-4.
- Tests at every level: `pkg/audit` unit tests, server and serverboot integration tests, extensions of the multi-tenant routing, no-router, offline erase, and single-tenant registry erase end-to-end cases on the binary, and manual scenario S88 (TEST-1 through TEST-4).
- The HTTP API reference, the CLI reference, the clustered deployment page, and the `[Unreleased]` changelog entries follow (DOC-1).

**Fixed decisions.**

- One shared audit sink and chain per deployment, with a hashed `tenant` attribute on each record. No per-tenant sink.
- An empty `tenant` adds nothing to `canonicalBody`, so every record written before this change keeps its hash and existing logs pass `FileSink.Verify` with no migration.
- The `podium:unrouted` binding never reaches a record. On a multi-tenant registry, an event from a request that resolves to no provisioned tenant records no tenant. Emitters never call `(*core.Registry).TenantFor` to label an event on a multi-tenant registry, whether or not a tenant router is installed.
- Deployment-wide events (`audit.anchored`, `audit.anchor_failed`, `audit.gap_detected`, `audit.retention_enforced`, `registry.read_only_entered`, `registry.read_only_exited`, `vector.outbox_lagging`) and the operator's `tenant.managed` events record no tenant.
- Ingest events record `lc.TenantID`, `artifact.signed` from the re-sign pass records `row.rec.TenantID`, and the MCP server's local sink records no tenant.
- On a multi-tenant registry the erase reads and rewrites only records whose `tenant` equals the routed tenant. Unlabeled records are never touched there, and the response reports nothing about them. On a single-tenant registry the erase reaches the bound tenant's records and unlabeled records. The offline CLI form reaches every record.
- The erase handler runs `requireTenant` before `authAdmin`. An unrouted multi-tenant request gets `403 auth.forbidden` with no admin callback, no store read, and no audit rewrite. Under `oidc-jwt`, an unknown organization gets `401 auth.tenant_unknown` from `srv.TenantRouted`.
- The zero `EraseScope` is refused by `EraseUser` with an error. A server caller that forgets to set the tenant cannot erase across tenants.
- With an endpoint sink, the handler appends a tenant-labeled `user.erased` and returns `500 registry.unavailable` when that append fails.
- Erase is the only reader that filters by tenant. The 0048 audit-volume meter keeps keying by `registry.TenantFor(ctx)`, and `Anchor`, `Verify`, and retention stay deployment-wide passes.
- No new §6.10 error code, environment variable, flag, or SPI. The erase response keeps its existing fields and gains none.

**Watch out for.**

- **The `tenant` key must not change the hash of an unlabeled record.** Append `tenant=<id>` after the `caller_network` block only when non-empty, the way `caller_network` itself is handled (`pkg/audit/audit.go:186-188`). TEST-1(b) pins this against a committed pre-change line. An unconditional `tenant=` part breaks every existing log.
- **`core.TenantFor` is unsafe as a label source.** On a multi-tenant registry it falls back to the bound tenant `podium:unrouted` (`pkg/registry/core/core.go:221-226`; `internal/serverboot/serverboot.go:1107-1109`). A multi-tenant registry with no identity verifier, or in public mode, installs no tenant router (`internal/serverboot/serverboot.go:1451`), so router presence does not identify a single-tenant registry. The label rule keys on multi-tenant mode: it reads `core.TenantFromContext` on a multi-tenant registry and uses the bound tenant only on a single-tenant registry (CODE-3).
- **The default tenant is routable on a multi-tenant registry.** Do not attribute unlabeled records to it (`internal/serverboot/serverboot.go:887`; `internal/serverboot/orgid.go:27-71`). Its admin would gain reach over every tenant's legacy records.
- **Alias discovery must be scoped too.** `EraseUser` collects aliases from every record today (`pkg/audit/retention.go:215-225`). An alias seen only in another tenant's records must not drive redaction in the requesting tenant. TEST-1(d) seeds exactly that case.
- **Each intermediate step fails closed.** After S5 the handler no longer carries the 0047 refusal, but serverboot still mounts the route unrouted, so `requireTenant` answers 403 for every multi-tenant request until S6 mounts it through `srv.TenantRouted`. The unsafe state is a multi-tenant erase that reaches `EraseUser` without `requireTenant` having run, and no step produces it. Keep `requireTenant` ahead of `authAdmin` in every revision of the handler.
- **Existing refusal tests break at S5 and S6.** `TestLayerEndpoint_MultiTenantEraseRefused` (`pkg/registry/server/layer_tenant_test.go:330`), the "erase refused for a B admin" subtest (`pkg/registry/server/layers_tenant_routing_test.go`), and the end-to-end erase steps (`test/e2e/layer_tenant_routing_test.go:185-188`, `:416-419`) assert the 0047 refusal. Land S5 through S9 in one pull request and treat the suite as green only at S9.
- **On a single-tenant registry the label and the erase scope come from different sources.** Request events take the core's bound tenant through `withAuditMetaMiddleware`, and the erase scope takes the endpoint's `tenantID` (`internal/serverboot/serverboot.go:1485`). If they differ, every single-tenant erase skips the records written after this change. The existing single-tenant erase tests seed only unlabeled records and cannot see it. The extended `TestStandardDeploy_AdminEraseRegistry` (TEST-3) pins the agreement between the core's bound tenant and the endpoint `tenantID`, because only the binary wires both. TEST-2(d) `TestErase_SingleTenantReachesOwnLabels` builds a `LayerEndpoint` with no core, so it pins only the agreement between the handler's erase scope and the endpoint's own `emitLayerEvent` label, both of which read `e.tenantID`.
- **The test fixture mounts erase unrouted.** `newTenantRoutingFixture` mounts `f.ep.EraseHandler()` directly (`pkg/registry/server/layers_tenant_routing_test.go:308`). Until TEST-2(a) wraps it in `f.srv.TenantRouted`, no routed-erase test in that file reaches a routed tenant.
- **The offline form must run with the registry stopped.** `FileSink` chains off an in-process `lastHash` (`pkg/audit/file.go:86-88`), and `rewriteWithChain` renames a re-hashed file into place (`pkg/audit/retention.go:326-355`). A live registry's next append would carry a stale `prev_hash` and raise `audit.gap_detected`.
- **A clustered deployment keeps one file per replica.** An erase rewrites only the file of the replica that serves the request (`docs/deployment/clustered.md:33`). DOC-1(c) states it.
- **Parts of `test/e2e` skip silently on macOS.** TEST-3 runs on the standard stack. Grep the run output for `SKIP` and confirm on the Linux CI lane.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1, SPEC-2. §8.1 gains the `tenant` attribute, and §8.5 replaces the multi-tenant refusal with tenant-scoped erasure. Bundled because SPEC-1 has no consumer without SPEC-2, both edit `spec/08-audit-and-observability.md`, and one reader reviews the pair.
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-3. §8.6 re-anchors after an erasure and names `superseded_head` on both rewrite events. Skip this step, and the parts of S5, S6, S7, S8, S9, S10, and S11 marked "SPEC-3" or conditioned on OQ-4 splitting, when OQ-4 resolves to splitting.
      Levels: —. Depends on: —
- [x] **S3 · code** — CODE-1. `audit.Event.Tenant`, conditional hashing, and the `tenant` wire key.
      Levels: unit. Depends on: S1
- [x] **S4 · code** — CODE-3. Every registry emitter labels its events through the single tenant rule, keyed on multi-tenant mode through `server.WithMultiTenant()`, which serverboot sets from `cfg.multiTenant`.
      Levels: unit, integration, e2e. Depends on: S3
- [x] **S5 · code** — CODE-2, CODE-4, CODE-6. `EraseScope`, `UserErasedEvent`, the tenant-scoped handler, the endpoint-sink `user.erased`, the `WithAfterErase` hook, and the CLI caller and help text. Bundled because the `EraseUser` signature and the shared event builder change in one commit for both callers (`pkg/registry/server/layers.go`, `cmd/podium/admin.go`) to compile.
      Levels: unit, integration, e2e. Depends on: S2, S4
- [x] **S6 · code** — CODE-5. serverboot mounts the erase route through `srv.TenantRouted` and wires the re-anchor hook.
      Levels: integration, e2e. Depends on: S5
- [x] **S7 · test** — TEST-1. `pkg/audit` unit tests for the hash, the wire key, legacy verification, and scoped erasure.
      Levels: unit. Depends on: S5
- [x] **S8 · test** — TEST-2. Server and serverboot tests for handler order, routing, the endpoint-sink event, the hook, emitter labels, and a single-tenant erase that reaches records its own emitters labeled.
      Levels: unit, integration. Depends on: S6
- [x] **S9 · test** — TEST-3. The multi-tenant end-to-end routing case erases in one tenant and reads the audit file, the no-router case reads the audit file for unlabeled events, the offline CLI erase case seeds tenant-labeled records, and the single-tenant registry erase case asserts that a request event and `user.erased` carry the same tenant.
      Levels: e2e. Depends on: S6
- [x] **S10 · test** — TEST-4. Manual scenario S88.
      Levels: manual. Depends on: S6
- [x] **S11 · docs** — DOC-1. HTTP API and CLI references, the clustered deployment page, and the changelog. Land in the same pull request as S6.
      Levels: —. Depends on: S6

**Ordering constraints.** S1 and S2 land the rules every later step cites. S3 precedes S4 because the emitters set `Event.Tenant`. S4 precedes S5 so that the records a scoped erase selects are labeled before the refusal is lifted. S5 precedes S6 because S6 calls `WithAfterErase`. S7 through S11 need the wiring they assert.

## Current state and the gap

### One sink and no tenant on a record

§4.7.1 gives each org its own audit stream, and §6.3.1 has a multi-tenant registry select that org's audit stream for each request. The implementation keeps one registry audit sink for the whole deployment. `openAuditSink` builds a single hash-chained `FileSink` or a single `EndpointSink` from `PODIUM_AUDIT_LOG_PATH` (`internal/serverboot/audit_anchor.go:40-58`; §13.12). `audit.Event` has no tenant attribute (`pkg/audit/audit.go:126-153`), and `canonicalBody` hashes none (`pkg/audit/audit.go:159-195`).

A few system events put the boot tenant ID in `Target` by convention: `internal/serverboot/readonly_audit.go:20,36` and `internal/serverboot/vector_outbox.go:170`, both fed the boot `tenantID` at `internal/serverboot/serverboot.go:1736-1737`. `tenant.managed` does the same (`pkg/registry/server/tenants.go:171,229,248`). `Target` usually holds an artifact or layer ID, so it cannot serve as a tenant discriminator. The user-attributed request events that erasure redacts carry no tenant at all.

`FileSink.Verify` (`pkg/audit/file.go:125-158`) recomputes each record's self-hash from `canonicalBody` plus `PrevHash` and checks the `PrevHash` linkage. A tenant attribute therefore has to be part of `canonicalBody` and of the `jsonEvent` wire form (`pkg/audit/file.go:170-257`), and it has to leave the hash of a record with no tenant unchanged. Otherwise existing logs fail `Verify`.

### Erasure redacts the whole file, so 0047 refused it

`audit.EraseUser` (`pkg/audit/retention.go:198-289`) loads the whole file. It discovers aliases (215-225) and redacts `Caller`, `CallerEmail`, `CallerGroups`, and `Context` values (228-262) on every record without scope, appends `user.erased` (276-282), and re-chains the whole file with `rewriteWithChain` (326-355). On a multi-tenant registry, a routed tenant admin's erase would therefore redact other tenants' records, and aliases seen only in another tenant's records would drive the redaction.

Proposal 0047 closed that hole by refusing `POST /v1/admin/erase` with `403 auth.forbidden` on every multi-tenant registry (`pkg/registry/server/layers.go:972-981`). §8.5's fourth bullet now mandates that refusal, so GDPR erasure is unavailable on every multi-tenant registry. 0047 OQ-3 records the condition for lifting it: the redaction must touch only records written under the requesting tenant.

### Lifting the refusal involves more than deleting the guard

- The erase route is mounted unrouted (`internal/serverboot/serverboot.go:1539`), so on a multi-tenant registry `e.tenant()` reports no tenant (`pkg/registry/server/layers.go:239-244`). `AdminAuthorize` would check the grant against the `TenantFor` fallback (`pkg/registry/core/admin.go:21-32`; `pkg/registry/core/core.go:221-226`) instead of the caller's tenant.
- `rewriteWithChain` gives new hashes to every record from the first redacted record to the end, including other tenants' records. Every `audit.anchored` head in that range then names a hash that no longer exists (`pkg/audit/anchor.go:27-58`). The erase handler does not re-anchor (`pkg/registry/server/layers.go:1031-1043`). Only the retention pass re-anchors (`internal/serverboot/serverboot.go:1689-1719`; `internal/serverboot/audit_retention.go:69-70`), and §8.6 requires re-anchoring only after retention. The periodic scheduler signs the new head on its next tick, so the gap is latency and the loss of the superseded head.
- With an endpoint sink, `auditFile` is nil, so the handler rewrites nothing and emits no `user.erased` event (`pkg/registry/server/layers.go:1032-1038`; `pkg/audit/retention.go:276`). That conflicts with §8.5 "Erasure is itself logged as a `user.erased` event", on single-tenant deployments as well.
- Records written before the change carry no tenant. The default tenant is bootstrapped and routable on a multi-tenant registry (`internal/serverboot/serverboot.go:887`; `internal/serverboot/orgid.go:27-71`), so attributing those records to it would hand its admin other tenants' legacy records.

### Readers of the log

The registry has no per-tenant audit read or query route. The 0048 audit-volume meter counts emitted events by `registry.TenantFor(ctx)` and never reads the log (`internal/serverboot/audit_emitter.go:66-73`; `internal/serverboot/serverboot.go:1637-1638`). `Anchor` (`pkg/audit/anchor.go:66`), `Verify` (`pkg/audit/file.go:125`), and retention `Enforce` (`pkg/audit/retention.go:100`) read the file as deployment-wide passes. Erase is the only reader that acts for a tenant.

## Decisions

- **Decision 1. One shared chain with a tenant attribute (open to reviewer; OQ-1).** Record the tenant as an attribute on every registry audit event, and do not open one sink per tenant. §13.12 defines `PODIUM_AUDIT_LOG_PATH` as one sink. Per-tenant sinks would need a path template, one anchor schedule, verify scheduler, and retention pass per tenant, a per-tenant `EndpointSink`, and a home for deployment-wide events (`audit.anchored`, `audit.gap_detected`, `audit.retention_enforced`) that belong to no tenant. §4.7.1 and §6.3.1 name a per-org stream and say nothing about storage, and a tenant-labeled shared chain gives each org a stream filtered by that label. The accepted cost: a tenant-scoped erase re-hashes later records of other tenants without changing their content, and Decision 5 handles the anchors this affects.
- **Decision 2. The tenant attribute is hashed.** `canonicalBody` appends `tenant=<id>` only when the attribute is non-empty, following the existing `CallerNetwork` pattern (`pkg/audit/audit.go:186-188`). A record with no tenant then hashes exactly as before, so existing logs keep passing `FileSink.Verify` with no migration. The wire key is a top-level `tenant` in `jsonEvent` with `omitempty`, and `eventForJSON` and `eventFromJSON` both carry it.
- **Decision 3. Which tenant each emitter records.** A request-scoped event records the tenant §6.3.1 selected for the request, or the bound tenant on a single-tenant registry. A request that resolves to no provisioned tenant on a multi-tenant registry records no tenant: the internal `podium:unrouted` binding (`internal/serverboot/orgid.go:16`) names no org and never reaches a record. Ingest events record `lc.TenantID`, and `artifact.signed` from the re-sign pass records `row.rec.TenantID`. Events about the registry as a whole record no tenant: `audit.anchored`, `audit.anchor_failed`, `audit.gap_detected`, `audit.retention_enforced`, `registry.read_only_entered`, `registry.read_only_exited`, and `vector.outbox_lagging`. Read-only mode and the outbox worker are deployment-wide, and the `tenantID` they receive is the boot tenant (`internal/serverboot/serverboot.go:1736-1737`). The MCP server's local sink (`cmd/podium-mcp/main.go:696-763`) records no tenant.
- **Decision 4. Operator events record no tenant (open to reviewer; OQ-3).** The operator's `tenant.managed` events record no tenant. §4.7.1 places the operator grant outside any org, and an operator's records about provisioning tenant X should not be rewritable by tenant X's admin. Consequence: an operator's identity in `tenant.managed` records is redacted only by the offline whole-file form.
- **Decision 5. Re-anchor after an erasure (open to reviewer; OQ-4).** After an erase rewrites the chain, the registry re-anchors immediately through the same `reAnchor` hook retention uses, and `user.erased` records the superseded chain head in a `superseded_head` context key, as `audit.retention_enforced` does (`pkg/audit/retention.go:130-147`). Anchors are deployment-wide: one anchor key, one chain, and one scheduler. The gap exists identically on a single-tenant registry today, so this part is separable (SPEC-3).
- **Decision 6. Erasure scope.** On a multi-tenant registry, the erase acts in the §6.3.1-routed tenant. Alias discovery and redaction both read only records whose tenant equals that tenant, and records of other tenants and records with no tenant are untouched. On a single-tenant registry, the erase reaches records labeled with the bound tenant and records with no tenant. A log written only in single-tenant mode holds nothing else, so the redacted records are identical to what today's whole-file erase produces. Records that a single-tenant registry inherited from an earlier multi-tenant run, labeled with other tenants, are no longer reached. The offline `podium admin erase --local/--audit-path` form keeps whole-file scope.
- **Decision 7. Unlabeled records on a multi-tenant registry (open to reviewer; OQ-2).** A tenant-scoped erase does not touch records with no tenant, and its response reports nothing about them. On a multi-tenant registry the unlabeled records include every record written before this change, across all tenants, and the records of unrouted requests. A per-user count over them would let a tenant admin submit any user ID and learn how many records outside the tenant name that user, which the per-org audit streams of §4.7.1 and §6.3.1 forbid. The response is therefore identical whether or not the user appears in unlabeled records. Treating them as the default tenant's is unsafe: the registry does not record which mode wrote a record, and the default tenant's admin is a routed principal on a multi-tenant registry (`internal/serverboot/serverboot.go:887-893`; `internal/serverboot/orgid.go:27-39`). That admin would gain reach over other tenants' legacy records. Operator consequence: legacy PII on a multi-tenant registry is redacted only by the offline whole-file form, run against `PODIUM_AUDIT_LOG_PATH` while the registry is stopped. That form reaches every tenant's records for the user.
- **Decision 8. Routing.** Mount `/v1/admin/erase` through `srv.TenantRouted`, as `/v1/layers` is. Run `requireTenant` before `authAdmin` so that an unrouted request on a multi-tenant registry is refused with `403 auth.forbidden` before the admin callback, which admits everyone under public mode or with no IdP, and before any store read or rewrite. Under `oidc-jwt`, a verified organization that names no provisioned tenant gets the §6.3.1 `401 auth.tenant_unknown`, and the 0047 special case in §8.5 is removed. On a multi-tenant registry with no IdP, or in public mode, no request is routed (`pkg/registry/server/server.go:481-484`), so erase stays refused there.
- **Decision 9. Endpoint sink.** When the registry sink is an external endpoint, the handler still purges the tenant's layers, appends a tenant-labeled `user.erased` through the sink (`transformed=0`, plus the admin), and leaves redaction of the shipped stream to the receiver. A failed append returns `500 registry.unavailable`. This closes the §8.5 "Erasure is itself logged" gap that exists today on single-tenant endpoint deployments as well.
- **Decision 10. Per-tenant readers.** No tenant-facing audit read route exists, so no reader beyond erase needs a tenant filter. The 0048 meter keeps keying by `registry.TenantFor(ctx)`. Request-scoped events take their label from the per-request `AuditMeta` (CODE-3), which resolves the routed tenant on a multi-tenant registry and the bound tenant on a single-tenant one, so the label on a recorded event and the meter key agree for every provisioned tenant. They differ only for an unrouted request, which the meter keys under `podium:unrouted` and the record leaves unlabeled. `Anchor`, `Verify`, and retention stay deployment-wide. A SIEM receiving the endpoint stream gets the `tenant` attribute on every record and can filter by it.
- **Decision 11. The zero scope is refused.** CODE-2 reduces `EraseScope` to two fields, where an empty `Tenant` matches every tenant label. The zero value would then reach every labeled record and no unlabeled one, which no caller wants and which would fail open for a server caller that forgot the tenant. `EraseUser` refuses `EraseScope{}` with `audit.ErrEraseScope` before reading the file. The whole-file scope is written `EraseScope{Unlabeled: true}` and is used only by the offline CLI.

## Spec amendment: §8.1 tenant in audit events

**SPEC-1.** `spec/08-audit-and-observability.md`, §8.1 "Audit Events". It lands in step S1 together with SPEC-2. Insert a new paragraph immediately after the paragraph that begins "**Caller identity in audit events.** Read events (`domain.loaded`, `domains.searched`, `artifacts.searched`, `artifact.loaded`) record the caller's identity from the OAuth token" and before the heading "## 8.2 PII Redaction":

> **Tenant in audit events.** A registry audit event that concerns one tenant records that tenant's org ID (§4.7.1) as `tenant`. The records that carry an org's ID form that org's audit stream (§4.7.1, §6.3.1). An event about an ingest, a signature, or another operation on a tenant's layer or artifact records the tenant that owns that layer or artifact, even when a request triggered it. Any other event raised by a request records the tenant §6.3.1 selects for the request, and a single-tenant registry records its sole tenant. On a multi-tenant registry, an event from a request that resolves to no provisioned tenant records no tenant. An event about the registry as a whole records no tenant. This covers audit-chain maintenance events such as `audit.anchored` and `audit.anchor_failed` (§8.6), registry mode events such as `registry.read_only_entered` and `registry.read_only_exited`, and `vector.outbox_lagging` (§4.7). An event an operator raises under the operator grant also records no tenant, because that grant is scoped to no tenant (§4.7.1). `tenant` is part of the hashed event body (§8.6), and an empty `tenant` adds nothing to that body.

The paragraph names no event type the spec does not define. `audit.gap_detected`, `audit.retention_enforced`, and `tenant.managed` fall under the classes it states. The rationale that a log written earlier still verifies lives in the CODE-1 comment and in TEST-1(b), outside the normative text.

## Spec amendment: §8.5 tenant-scoped erasure

**SPEC-2.** `spec/08-audit-and-observability.md`, §8.5 "Erasure". It lands in step S1 together with SPEC-1 and makes the edits below.

(a) The second bullet currently reads:

> - Redacts the user identity in audit records (replaces with `redacted-<sha256(user_id+salt)>`).

Replace it with:

> - Redacts the user identity (replaces it with `redacted-<sha256(user_id+salt)>`) in the requesting tenant's audit records. On a multi-tenant registry, these are the records whose `tenant` (§8.1) is the tenant §6.3.1 selects for the request. Alias discovery reads only those records. On a single-tenant registry, these are the records whose `tenant` is the sole tenant and the records that name no tenant.

(b) The third bullet ("Preserves audit event sequencing for integrity.") is unchanged.

(c) The fourth bullet currently reads:

> - On a multi-tenant registry, erasure is refused with `403 auth.forbidden` for every caller, before the admin check, before any layer is read, and before any audit record is rewritten, because the redaction this section describes would reach audit records outside the requesting tenant's audit stream (§4.7.1). The erasure endpoint selects no tenant, so this refusal also answers a request whose verified organization names no provisioned tenant, in place of the §6.3.1 `auth.tenant_unknown` rejection.

Replace it with two bullets:

> - On a multi-tenant registry, erasure acts in the tenant §6.3.1 selects for the request. The admin check (§4.7.2) runs in that tenant, and only that tenant's layers are purged. Under `oidc-jwt`, a request whose verified organization names no provisioned tenant is rejected with `auth.tenant_unknown` (§6.3.1). Any other request that resolves to no tenant is refused with `403 auth.forbidden` before the admin check, before any layer is read, and before any audit record is rewritten.
> - On a multi-tenant registry, erasure leaves records that name no tenant (§8.1) unchanged, and the response carries no information about them, so a tenant admin cannot learn whether the user appears in records outside the tenant. An operator redacts them with the offline erasure form shown above, run against the registry's file sink (`PODIUM_AUDIT_LOG_PATH`, §13.12) while the registry is stopped. That form reaches every record in the file whatever its tenant, and every tombstone it writes uses the salt the operator supplies.

(d) The closing sentence currently reads:

> Use this command for GDPR right-to-erasure. Erasure is itself logged as a `user.erased` event.

Replace it with:

> Use this command for GDPR right-to-erasure. Erasure is itself logged as a `user.erased` event that names the requesting tenant. The event is recorded whether the registry sink is a file or an external endpoint, and with an external endpoint the registry rewrites no record and the receiving system owns redaction of the shipped stream.

(e) The command block at the top of §8.5 currently reads:

> ```
> podium admin erase <user_id>
> ```

Replace it with the block and the sentence below, so the offline erasure form that the (c) bullet names is defined in §8.5:

> ```
> podium admin erase <user_id>
> podium admin erase <user_id> --local --audit-path <file> --operator <admin-id> --salt <salt>
> ```
>
> The first form calls the registry. The second form is the offline erasure form: it rewrites the audit file at `<file>` directly without calling the registry, reaches every record in that file whatever its tenant, and records `<admin-id>` as the invoking admin on a `user.erased` event that names no tenant.

The CLI accepts this form today (`cmd/podium/admin.go:236-271`, where `--local` or `--audit-path` selects it and `--operator` is required), so the edit names an existing command and adds no surface.

## Spec amendment: §8.6 re-anchoring after an erasure

**SPEC-3.** `spec/08-audit-and-observability.md`, §8.6 "Audit Integrity", paragraph "**Local chain-head anchoring.**". It lands in step S2. Skip it when OQ-4 resolves to splitting the anchor work into its own proposal.

(a) The sentence currently reads:

> It anchors again immediately after a §8.4 retention pass moves the head.

Replace it with:

> It anchors again immediately after a §8.4 retention pass moves the head or a §8.5 erasure rewrites the chain.

(b) In the next sentence, change "and a failed attempt after a retention pass is logged" to "and a failed attempt after a retention pass or an erasure is logged". The sentence then reads:

> A failed periodic attempt is recorded as `audit.anchor_failed`, and a failed attempt after a retention pass or an erasure is logged, and the next periodic anchor signs the current head.

(c) Insert after that sentence:

> The `audit.retention_enforced` and `user.erased` events that close a rewrite record the chain head the rewrite superseded as `superseded_head`, so a verifier holding an anchor of that head can reconcile it with the rewritten log.

## Proposed solution

### CODE-1. `pkg/audit`: `Event.Tenant`, hashed and serialized

Targets: `pkg/audit/audit.go` (`Event`, `canonicalBody`), `pkg/audit/file.go` (`jsonEvent`, `eventForJSON`, `eventFromJSON`).

- Add `Tenant string` to `Event`:

  ```go
  // Tenant is the §4.7.1 org ID whose audit stream the event belongs to;
  // empty for deployment-wide events and for records written before the
  // attribute existed.
  //
  // Spec: §8.1
  Tenant string
  ```

- In `canonicalBody`, after the `caller_network` block:

  ```go
  // An empty tenant contributes nothing, so a record written before the
  // attribute existed keeps its hash and an existing log still verifies.
  if e.Tenant != "" {
      parts = append(parts, "tenant="+e.Tenant)
  }
  ```

- In `jsonEvent` add ``Tenant string `json:"tenant,omitempty"` ``, and copy it in `eventForJSON` and `eventFromJSON`. `Memory.Append` and `FileSink.Append` need no change, because they hash `canonicalBody`.

### CODE-2. `pkg/audit`: `EraseUser` takes a scope

Target: `pkg/audit/retention.go` (`EraseUser`, lines 198-289).

```go
// EraseScope selects the records an erasure reads and rewrites.
//
// Spec: §8.5
type EraseScope struct {
    Tenant    string // records labeled with this tenant; "" matches every tenant label (the offline whole-file form)
    Unlabeled bool   // also records that carry no tenant
}

// ErrEraseScope reports an EraseScope that selects nothing coherent. The zero
// value is refused so a caller that forgot the tenant cannot erase across
// tenants.
var ErrEraseScope = errors.New("audit: erase scope names no tenant and excludes unlabeled records")

func EraseUser(ctx context.Context, sink *FileSink, userID, salt, admin string, scope EraseScope) (int, error)
```

Callers pass these values:

| Caller | Scope |
|:--|:--|
| Multi-tenant registry | `EraseScope{Tenant: routed}` |
| Single-tenant registry | `EraseScope{Tenant: bound, Unlabeled: true}` |
| Offline CLI | `EraseScope{Unlabeled: true}`, which reaches every record |

Behavior:

- `EraseUser` returns `ErrEraseScope` for `EraseScope{}` before reading the file (Decision 11).
- A record is in scope when `scope.Tenant == "" && rec.Tenant != ""`, or `rec.Tenant == scope.Tenant && rec.Tenant != ""`, or `rec.Tenant == "" && scope.Unlabeled`.
- Alias discovery (today 215-225) reads only in-scope records. Redaction (today 228-262) rewrites only in-scope records. The doc comment states that the alias set comes from in-scope records only.
- `EraseUser` keeps returning the transformed count as `(int, error)`, as it does today (`pkg/audit/retention.go:198`). The count covers in-scope records only, and `EraseUser` computes nothing over out-of-scope records, so the return value carries no information about records outside the scope (Decision 7).
- `user.erased` is built by `UserErasedEvent` (CODE-4) with `Tenant: scope.Tenant`. The `admin` context key stays conditional on a non-empty admin, as it is today (`pkg/audit/retention.go:268-271`).
- **SPEC-3:** before the append, capture `supersededHead := events[len(events)-1].Hash`, or `""` when the log is empty, mirroring `Enforce` at `pkg/audit/retention.go:136-147`, and pass it to `UserErasedEvent`. When SPEC-3 is dropped, pass `""` and `UserErasedEvent` omits the key.
- `rewriteWithChain` is unchanged.

### CODE-3. Emitters record the tenant

Targets: `pkg/registry/server/audit_context.go` (`AuditMeta`, `withAuditMetaMiddleware`, `auditMetaFrom`, `emitAuditEvent`, new `auditTenant`), `pkg/registry/server/server.go` (`Server.multiTenant`, new `WithMultiTenant`, `WithTenantRouter`), `pkg/registry/server/admin.go:53,68`, `pkg/registry/server/tenants.go:171,229,248`, `pkg/registry/server/layers.go:491,1711` (`emitLayerEvent`, reorder), `internal/serverboot/audit_emitter.go` (`auditEmitterFor`), `internal/serverboot/reingest.go` (`ingestAuditEmitter` and its caller), `internal/serverboot/rehash.go:635-647` (`appendSignedEvent`), `internal/serverboot/serverboot.go` (about 1630-1660, the emitter wiring; 1451-1464, the `WithMultiTenant` option).

1. Add `Tenant string` to `server.AuditMeta`. Resolve it with one rule keyed on multi-tenant mode. Router presence is not that signal: serverboot installs the tenant router only when `cfg.multiTenant && verifierInstalled` (`internal/serverboot/serverboot.go:1451-1457`), but binds the registry to `podium:unrouted` whenever `cfg.multiTenant` is set (`internal/serverboot/serverboot.go:1105-1109`). A multi-tenant registry with no identity verifier, or in public mode, therefore has no router, and its `(*core.Registry).TenantFor` returns `podium:unrouted` (`pkg/registry/core/core.go:221-226`).
   - On a multi-tenant registry, use `core.TenantFromContext(ctx)`, or `""` when the request carries no routed tenant.
   - On a single-tenant registry, use the bound tenant.
   - The rule never uses a `TenantFor` result as the label on a multi-tenant registry, so `podium:unrouted` cannot reach a record. Cite `// Spec: §8.1, §6.3.1`.

   One unexported helper in `pkg/registry/server/audit_context.go`, `auditTenant(ctx context.Context, multiTenant bool, bound string) string`, implements the rule. It matches `LayerEndpoint.tenant` (`pkg/registry/server/layers.go:239-244`) except that the unrouted case yields `""`. Its callers pass these arguments:

   | Caller | `multiTenant` | `bound` |
   |:--|:--|:--|
   | `Server.withAuditMetaMiddleware` | `s.multiTenant` | `s.core.TenantFor(ctx)`, which the helper ignores on a multi-tenant registry and which is the bound tenant on a single-tenant registry, where no tenant is routed |
   | The layer endpoint's emit path | `e.multiTenant` | `e.tenantID` |

   `Server` gains a `multiTenant bool` field and a `WithMultiTenant()` option that sets it. `WithTenantRouter` also sets it, because a router is installed only in multi-tenant mode, so every existing caller of `WithTenantRouter`, including `newTenantRoutingFixture` (`pkg/registry/server/layers_tenant_routing_test.go:279-280`), keeps its multi-tenant labeling with no change. Nothing clears the field. serverboot appends `server.WithMultiTenant()` to `bootOpts` when `cfg.multiTenant` is set, which is the condition that calls `layers.WithTenantRouting()` (`internal/serverboot/serverboot.go:1487-1493`), before `server.New` (`internal/serverboot/serverboot.go:1464`). `withAuditMetaMiddleware` is the field's only reader. If serverboot omits the option, a multi-tenant registry with no verifier labels its request events `podium:unrouted`; TEST-2(f) and the extended `TestLayerEndpoint_MultiTenantNoRouter` (TEST-3) observe that.

   `withAuditMetaMiddleware` sits inside `withTenantRouting` (`pkg/registry/server/server.go:431`), so the routed tenant is on the context when it builds `AuditMeta`. With no router, `withTenantRouting` is a pass-through (`pkg/registry/server/server.go:446`), so a multi-tenant request there carries no routed tenant and records none. The layer endpoint is mounted outside `srv.Handler()`, so its events reach `emitAuditEvent`'s fallback path (`auditMetaFrom`, `pkg/registry/server/audit_context.go:165-168`), which cannot see the mode.

   **IMPLEMENTOR'S CHOICE:** how the layer endpoint hands its label to `emitAuditEvent` (a `tenant string` parameter on `emitAuditEvent`, or attaching an `AuditMeta` with `Tenant` set before the call) — the label must equal `auditTenant(ctx, e.multiTenant, e.tenantID)`, no path may derive it from router presence or from `TenantFor` on a multi-tenant registry, and TEST-2(f) asserts the label on a routed layer write, on an unrouted multi-tenant admin grant and meta-tool read with and without a router, and on a single-tenant request.

2. `emitAuditEvent` and `auditEmitterFor` set `ev.Tenant = m.Tenant` from the `AuditMeta` they already read (`pkg/registry/server/audit_context.go:165`; `internal/serverboot/audit_emitter.go:44`). `auditEmitterFor` takes no new parameter, and `emitAuditEvent` changes only as the item 1 IMPLEMENTOR'S CHOICE allows. When `auditEmitterFor` finds no `AuditMeta` on the context, it records no tenant. `wrapAuditVolume` is unchanged.
3. `tenant.managed` (`pkg/registry/server/tenants.go:171,229,248`) records no tenant, per Decision 4, through the shared rule with no dedicated wrapper. Every tenant-management handler runs `tenantAdminGate` before it emits (`pkg/registry/server/tenants.go:120,139,181,237`), and the gate answers `404 registry.tenant_management_unavailable` when no tenant router is installed (`pkg/registry/server/tenants.go:104-108`). The event is therefore emitted only on a server with a router, which `WithTenantRouter` marks multi-tenant, so the single-tenant bound-tenant arm never applies to it. `withTenantRouting` passes `/v1/admin/tenants` through without routing (`pkg/registry/server/server.go:455-458`), so the request carries no routed tenant and `auditTenant` yields `""` even when the operator's org names a provisioned tenant. Add a `// Spec: §8.1` comment at each emit naming Decision 4 and the routing bypass it relies on. TEST-2(f) pins both halves.
4. Where no request exists, label directly: `ingestAuditEmitter` takes the tenant as a parameter and its caller passes `lc.TenantID` (`internal/serverboot/reingest.go:90`), and `appendSignedEvent` sets `Tenant: row.rec.TenantID`.
5. The deployment-wide emitters (`internal/serverboot/readonly_audit.go:17,33`, `internal/serverboot/vector_outbox.go:167`, the anchor, verify, and retention passes) leave `Tenant` empty. Each gains a one-line `// Spec: §8.1` comment saying the event describes the registry as a whole. Their existing `Target` values are unchanged.

### CODE-4. Erase handler: tenant-scoped erase, endpoint-sink `user.erased`, post-erase hook

Targets: `pkg/registry/server/layers.go` (erase, lines 957-1043; `WithTenantRouting` doc at 219-224; new `WithAfterErase`), `pkg/audit/retention.go` (new `UserErasedEvent`).

- Remove the `if e.multiTenant` refusal block (`pkg/registry/server/layers.go:976-979`).
- Replace `tenantID, _ := e.tenant(r.Context())` with `tenantID, ok := e.requireTenant(w, r); if !ok { return }`, placed before `authAdmin`. The order is: the method check, the read-only check (`rejectIfReadOnly`), `requireTenant`, `authAdmin`, body decode and validation (`user_id` and `salt`), layer purge, audit. This is today's order (`pkg/registry/server/layers.go:964-1001`) with `requireTenant` in place of the refusal block and the `e.tenant` call, so an unrouted or non-admin caller that sends a malformed body gets 403 before any 400.
- Build `scope := audit.EraseScope{Tenant: tenantID, Unlabeled: !e.multiTenant}`. `requireTenant` guarantees a non-empty `tenantID` on a multi-tenant endpoint, and the single-tenant `tenantID` is the bound tenant.
- Add one exported builder in `pkg/audit`:

  ```go
  // UserErasedEvent builds the §8.5 user.erased record. EraseUser and the
  // registry's endpoint-sink path both call it, so the tombstone and the
  // context layout are defined once.
  //
  // Spec: §8.5, §8.6
  func UserErasedEvent(userID, salt, admin, tenant string, transformed int, supersededHead string) Event
  ```

  It holds the tombstone in `Target`, the `system:retention` caller fallback, the `transformed` context key, the `admin` key when admin is non-empty, and the `superseded_head` key when `supersededHead` is non-empty. It sets `Tenant: tenant` and `Timestamp: time.Now().UTC()`, as `EraseUser` does today (`pkg/audit/retention.go:278`). `pkg/audit` has no package-level injected clock, so tests assert the timestamp only as non-zero.
- When `auditFile != nil`: call `EraseUser` with the scope, then call `e.afterErase(r.Context())` when the hook is non-nil (**SPEC-3**).
- When `auditFile == nil` and `auditSink != nil`: append `audit.UserErasedEvent(userID, salt, admin, tenantID, 0, "")` through `e.auditSink`. Do not discard the `Append` error. On failure return `500 registry.unavailable` with a message naming the failed `user.erased` delivery, in the style of the existing `EraseUser` failure branch (`pkg/registry/server/layers.go:1035`). The layer purge has already happened at that point; the edge-case table records it.
- The response keeps `erased`, `layers_purged`, and `audit_events_redacted` (`pkg/registry/server/layers.go:1039-1043`) and gains no field. It reports nothing about records outside the scope, so a multi-tenant erase of a user who appears only in other tenants' records or in unlabeled records returns the same body as an erase of a user who appears nowhere (Decision 7).
- **SPEC-3:** declare `func (e *LayerEndpoint) WithAfterErase(fn func(context.Context)) *LayerEndpoint`, documented as the §8.6 re-anchor seam.
- Update the `WithTenantRouting` doc at `pkg/registry/server/layers.go:219-224` ("erasure is refused for every caller" becomes "erasure acts in the routed tenant and refuses an unrouted request") and the erase doc comment at `pkg/registry/server/layers.go:957-964` to describe tenant-scoped redaction.

### CODE-5. serverboot: route the erase endpoint and wire the re-anchor

Targets: `internal/serverboot/serverboot.go:1531-1539` (erase mount and comment), `internal/serverboot/serverboot.go:1689-1700` (`reAnchor`), `internal/serverboot/audit_retention.go:65-71`.

- Mount the route as `mux.Handle("/v1/admin/erase", srv.TenantRouted(layers.EraseHandler()))`. Replace the comment with the routing rationale the `/v1/layers` comment above it gives.
- **SPEC-3:** change `reAnchor` to `func(ctx context.Context)`. The retention scheduler passes its own context in place of the buried `context.Background()` at `internal/serverboot/serverboot.go:1697`. Generalize the log line to `audit re-anchor after chain rewrite failed`. After `reAnchor` is built, add `if reAnchor != nil { layers.WithAfterErase(reAnchor) }`. Set it before `serveUntilShutdown` starts the accept loop (`internal/serverboot/serverboot.go:1813`), so no request observes a half-set hook.

### CODE-6. CLI offline erase keeps whole-file scope

Target: `cmd/podium/admin.go:227-271`.

- Change the call to `audit.EraseUser(context.Background(), sink, userID, *salt, *operator, audit.EraseScope{Unlabeled: true})` and print the returned count as today.
- Rewrite the `adminEraseCmd` doc comment (`cmd/podium/admin.go:227-232`): the `--local`/`--audit-path` form rewrites any `FileSink` log in full, whatever the tenant, which is the MCP local sink by default or the registry's `PODIUM_AUDIT_LOG_PATH` file, and records `--operator` on an unlabeled `user.erased`.
- Change the flag help (`cmd/podium/admin.go:237-238`). `--audit-path`: "audit log file to rewrite (default ~/.podium/audit.log; pass the registry's PODIUM_AUDIT_LOG_PATH only while the registry is stopped)". `--local`: "rewrite an audit log file directly instead of calling the registry".
- Add a comment at the call site: the registry must be stopped because `FileSink` chains off an in-process `lastHash` and `rewriteWithChain` renames a re-hashed file into place, so a live registry's next append would break the chain and raise `audit.gap_detected`.
- Change the `--salt` help text at `cmd/podium/admin.go:239` so it no longer calls the salt "per tenant": the offline form applies one salt to every record it rewrites.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Multi-tenant erase by a routed tenant admin | 200; only that tenant's layers purged; only records labeled with that tenant redacted | §8.5 bullets (SPEC-2); `docs/reference/http-api.md` Erase a user (DOC-1(e)) |
| Multi-tenant erase, unrouted request (trusted-headers unknown org, no credential, public mode, no IdP) | 403 `auth.forbidden`; no admin callback, no store read, no rewrite | §8.5 "Any other request that resolves to no tenant" (SPEC-2); DOC-1(e) |
| Multi-tenant erase, `oidc-jwt`, verified organization names no tenant | 401 `auth.tenant_unknown` | §8.5 (SPEC-2), §6.3.1; DOC-1(e) |
| Records written before this change on a multi-tenant registry | Untouched by the registry erase and not reported in the response; redacted only by the offline form with the registry stopped (accepted, Decision 7) | §8.5 new bullet (SPEC-2); DOC-1(b), DOC-1(c), DOC-1(e) |
| Records labeled with another tenant on a single-tenant registry (left from an earlier multi-tenant run) | Untouched by the registry erase; the offline form reaches them (accepted) | §8.5 "On a single-tenant registry, these are the records whose `tenant` is the sole tenant and the records that name no tenant" (SPEC-2); DOC-1(e); TEST-1(j) |
| An alias of the user appears only in another tenant's records | Not used for redaction in the requesting tenant | §8.5 "Alias discovery reads only those records" (SPEC-2); DOC-1(e) |
| A tenant admin erases a user who appears only in other tenants' records or in unlabeled records | 200 with a body identical to an erase of a user who appears nowhere; no record changes | §8.5 "the response carries no information about them" (SPEC-2); TEST-2(d) |
| Operator identity in `tenant.managed` records | No tenant admin's erase reaches it; the offline form does (accepted, Decision 4) | §8.1 operator sentence (SPEC-1); DOC-1(c) |
| Other tenants' later records after a scoped erase | Content unchanged; `hash` and `prev_hash` change; `FileSink.Verify` passes (accepted, Decision 1) | §8.5 "Preserves audit event sequencing for integrity"; §8.6 (SPEC-3); DOC-1(e) |
| An anchor whose head falls after the first redacted record | No longer matches the log; `user.erased` carries `superseded_head`, and a new `audit.anchored` follows immediately | §8.6 (SPEC-3); DOC-1(e) |
| Immediate re-anchor fails | Logged; the next periodic anchor signs the current head | §8.6 "a failed attempt after a retention pass or an erasure is logged" (SPEC-3); existing §8.6 text |
| SPEC-3 dropped (OQ-4) | No immediate re-anchor; the next periodic tick anchors the new head; `user.erased` has no `superseded_head` | Existing §8.6 text; no new docs sentence |
| Registry sink is an external endpoint | Layers purged; `user.erased` sent with `transformed=0`; no record rewritten; the receiver owns redaction | §8.5 closing sentence (SPEC-2); DOC-1(c), DOC-1(e) |
| Endpoint append of `user.erased` fails | 500 `registry.unavailable`; the layer purge already applied stays applied (accepted); a retry purges nothing more and resends the event | §8.5 "Erasure is itself logged" (SPEC-2); DOC-1(e) |
| Clustered deployment with per-replica file sinks | The erase rewrites only the serving replica's file (accepted) | `docs/deployment/clustered.md` (DOC-1(c)) |
| Offline form run while the registry is live | The registry's next append breaks the chain and `audit.gap_detected` follows (operator error) | §8.5 "while the registry is stopped" (SPEC-2); `docs/reference/cli.md` (DOC-1(b)); CLI help (CODE-6) |
| Offline form with one salt across tenants | Every tombstone it writes uses that salt | §8.5 new bullet (SPEC-2); DOC-1(b) |
| Empty log | Erase succeeds; `user.erased` is the only record; `superseded_head` empty | §8.5 (SPEC-2); TEST-1(g) |
| A server caller passes the zero `EraseScope` | `ErrEraseScope`; nothing read or rewritten | Code-only guard (Decision 11); TEST-1(h) |
| An event from an unrouted multi-tenant request, with or without a tenant router installed (for example `admin.granted` or a meta-tool read under public mode or with no IdP) | Records no tenant; `podium:unrouted` never appears | §8.1 "records no tenant" (SPEC-1); DOC-1(d) |
| Deployment-wide event | Records no tenant | §8.1 (SPEC-1); DOC-1(d) |
| A SIEM separating tenants on the endpoint stream | Filters on the `tenant` key; unlabeled records belong to no tenant | §8.1 (SPEC-1); DOC-1(d) |

## Testing

All new tests carry `// Spec:` annotations in the existing `// Spec: §8.5 — ...` style.

### TEST-1. `pkg/audit` unit tests

Targets: `pkg/audit/caller_fields_test.go`, `pkg/audit/file_test.go`, `pkg/audit/retention_test.go`.

- (a) Add a `"tenant"` row to the `TestCanonicalBody_IncludesCallerFields` table (`pkg/audit/caller_fields_test.go:16`): setting `Tenant` changes `canonicalBody`. `// Spec: §8.1`.
- (b) `TestEvent_UnlabeledHashUnchanged`: a JSON line written by the pre-change writer, committed as a literal with no `tenant` key and its stored hash. Assert that `FileSink.Verify` passes over a file holding it and that the recomputed hash equals the stored hash. `// Spec: §8.1, §8.6`.
- (c) Extend `TestFileSink_CallerFieldsRoundTrip` (`pkg/audit/caller_fields_test.go:40`) to set `Tenant`. Assert the top-level `"tenant"` JSON key, that `eventFromJSON` reads it back, and that `Verify` passes. Assert that an empty `Tenant` writes no `"tenant"` key. `// Spec: §8.1`.
- (d) `TestEraseUser_TenantScoped`: a log with records of tenants A and B and unlabeled records, all naming alice. One A record carries, as `CallerEmail` or a `Context` value, an alias email that appears on B's alice records and nowhere in A's alice records. Erase alice with `EraseScope{Tenant: "A"}`. Assert:
  - A's alice records carry the tombstone, and the A record holding the B-only alias stays unredacted.
  - B records and unlabeled records are byte-identical apart from `hash` and `prev_hash`.
  - The returned count equals the number of A records transformed, and an erase of the same log with the B and unlabeled records removed returns the same count, so the count carries nothing about out-of-scope records.
  - Records before the first redacted record keep their hashes.
  - `Verify` passes.
  - `user.erased` carries `Tenant` A and, under SPEC-3, `superseded_head` equal to the pre-erase last hash.
  `// Spec: §8.5, §8.6`.
- (e) `TestEraseUser_UnlabeledScope`: on a log with only bound-tenant and unlabeled records, erase with `EraseScope{Tenant: bound, Unlabeled: true}` and, on a copy, with `EraseScope{Unlabeled: true}`. Compare `Caller`, `CallerEmail`, `CallerGroups`, and `Context` of every record before `user.erased`; they are equal. Assert that both calls return the same count. The final record differs in timestamp and tenant and is excluded. `// Spec: §8.5`.
- (f) `TestEraseUser_AllRecords`: `EraseScope{Unlabeled: true}` redacts alice in A, B, and unlabeled records, and `user.erased` has no `tenant`. `// Spec: §8.5`.
- (g) Empty log: erase succeeds, `user.erased` is the only record, `superseded_head` is absent, and no panic. `// Spec: §8.5, §8.6`.
- (h) `TestEraseUser_ZeroScopeRefused`: `EraseScope{}` returns `ErrEraseScope` (`errors.Is`) and leaves the file byte-identical. `// Spec: §8.5`.
- (i) `TestUserErasedEvent`: the builder sets `Tenant`, the tombstone, `transformed`, and omits `admin` and `superseded_head` when empty. `// Spec: §8.5`.
- (j) `TestEraseUser_SingleTenantScopeExcludesForeignLabels`: a log with alice records labeled with the bound tenant S, alice records labeled with another tenant F, and unlabeled alice records. One F record carries an alias email of alice that appears in no S-labeled or unlabeled record, and one S record carries that alias as a `Context` value. Erase alice with `EraseScope{Tenant: "S", Unlabeled: true}`. Assert that the S-labeled and unlabeled alice records carry the tombstone, that the F-labeled records are byte-identical apart from `hash` and `prev_hash`, that the S record holding the F-only alias keeps it unredacted, that the returned count equals the number of S-labeled and unlabeled records transformed, and that `Verify` passes. A predicate that admitted every record under `Unlabeled: true` fails the F-record and alias assertions. `// Spec: §8.5`.

### TEST-2. Registry server and serverboot tests

Targets: `pkg/registry/server/erase_test.go`, `pkg/registry/server/layer_tenant_test.go`, `pkg/registry/server/layers_tenant_routing_test.go`, `pkg/registry/server/audit_context_test.go`, `pkg/registry/server/tenants_test.go`, `internal/serverboot/audit_emitter_test.go`, `internal/serverboot/coverage_gaps_test.go`, `internal/serverboot/rehash_test.go`, `test/integration/erase_test.go`, `test/integration/audit_sink_redirect_test.go`.

- (a) In `newTenantRoutingFixture`, mount the erase route as `f.srv.TenantRouted(f.ep.EraseHandler())`, mirroring CODE-5.
- (b) Convert `TestLayerEndpoint_MultiTenantEraseRefused` (`pkg/registry/server/layer_tenant_test.go:330`) into `TestLayerEndpoint_MultiTenantEraseScoped`: tenant `"acme"` returns 200 and purges `alice-personal`; tenant `""` returns 403 `auth.forbidden` with zero admin calls and no purge. `// Spec: §8.5`.
- (c) In `TestLayerTenantRouting_RoutedWritesAndEraseRefusal`, renamed `TestLayerTenantRouting_RoutedWritesAndErase`:
  - Replace "erase refused for a B admin" with "B admin erase purges only carol-b". Carol's A layers and A rows stay unchanged, B's records are redacted, and A's records are byte-identical apart from `hash` and `prev_hash`.
  - Add "erase refused for a caller who is admin only in another tenant", which pins that the admin check runs in the routed tenant (Decision 8). Build the fixture with `grants: {trTenantB: {trOliviaB.user}}` over `eraseSeeds` and send the erase of carol as `trOliviaA` (`pkg/registry/server/layers_tenant_routing_test.go:56`), who routes to A and holds admin only in B and in the poison tenant P, which `trSeedPoison` grants to every fixture subject (`pkg/registry/server/layers_tenant_routing_test.go:234-242`). Assert `403 auth.forbidden`, that the admin callback ran exactly once, that carol's layers and the A and B rows are unchanged, and that the audit file is byte-identical. Add a second case with no `grants`, where olivia holds admin only in P, with the same assertions. `assertEraseRefused` asserts zero admin calls (`pkg/registry/server/layers_tenant_routing_test.go:756-760`), so these cases use a sibling helper that asserts one call and otherwise makes the same checks. An implementation that evaluated the grant against the bound tenant or an unrouted context would admit olivia and fail both cases.
  - The rejecting-provider subtest (715-725) expects 401 `auth.tenant_unknown`, with the file and rows unchanged.
  - The always-admit subtest (`pkg/registry/server/layers_tenant_routing_test.go:710`) sends the erase as `trDave`, whose org `trTenantB` routes to B once TEST-2(a) mounts erase through `f.srv.TenantRouted`, so it would admit dave and purge carol-b. Switch its caller to `trErin` (`:59`), who carries no org and stays unrouted, keep `assertEraseRefused`, and rename it "erase refused for an unrouted caller under an always-admit callback". Its comment states that an always-admit callback does not override the `requireTenant` refusal.
  - The `trUnroutedWrites` erase row (982) and the `NoRouter` erase assertion (1106) stay as they are. They pin the `requireTenant`-before-`authAdmin` order with zero store writes and zero admin calls. Update only their comments.
  `// Spec: §8.5, §4.7.2, §6.3.1`.
- (d) In `erase_test.go`:
  - **SPEC-3:** the `afterErase` hook runs exactly once after a file rewrite, receives a context derived from the request context (assert a value placed on the request context), and does not run with `WithEraseSink(nil)`.
  - On a multi-tenant fixture, a tenant A admin erases bob, who appears only in B-labeled and unlabeled records, and then erases dave, who appears in no record. The two response bodies are equal apart from the `erased` value (`layers_purged` empty, `audit_events_redacted` 0), and the response carries no key beyond `erased`, `layers_purged`, and `audit_events_redacted`.
  - With an endpoint sink whose `Append` fails, the response is 500 `registry.unavailable`.
  - `TestErase_SingleTenantReachesOwnLabels` joins the emitter label (CODE-3) to the erase scope (CODE-4) on a single-tenant endpoint. The existing single-tenant seeds carry no tenant (`pkg/registry/server/erase_test.go:96-100`; `test/integration/erase_test.go:70-74`), and an unlabeled record is redacted under `Unlabeled: true` whatever `scope.Tenant` holds, so those tests cannot detect a wrong tenant. Build a single-tenant `NewLayerEndpoint(st, "t", ...)` with `WithAudit` and `WithEraseSink` on one `FileSink`, mounting `Handler()` and `EraseHandler()` as `eraseTestEndpoint` does (`pkg/registry/server/erase_test.go:25-48`), with an identity resolver that returns the subject named in a test request header so alice and carol can each call. alice registers the user-defined layer `alice-personal` through `POST /v1/layers`; the register path sets `Owner` from the caller (`pkg/registry/server/layers.go:1382`) and `emitLayerEvent` records it as the `owner` context value (`pkg/registry/server/layers.go:488-491`), so the server's own emit path writes a `"t"`-labeled record naming alice. Seed through the `audit` package one unlabeled alice record and one alice record labeled with the foreign tenant `"f"`. carol erases alice. Assert:
    - the emitter-written record carries `tenant` `"t"` before the erase and carries the tombstone in place of alice after it;
    - the unlabeled record carries the tombstone;
    - the `"f"`-labeled record is byte-identical apart from `hash` and `prev_hash`;
    - `audit_events_redacted` equals the number of `"t"`-labeled and unlabeled alice records;
    - the file-sink `user.erased` carries `tenant` `"t"`, and `FileSink.Verify` passes.
    An erase scope whose tenant differs from the emitter's label leaves the emitter-written record unredacted and fails the first assertion.
  `// Spec: §8.5, §8.6, §8.1`.
- (e) Extend `TestErase_EndpointRedirectPurgesAndForwards` (`test/integration/audit_sink_redirect_test.go:26`) to assert that `user.erased` reaches the endpoint with `tenant`, the admin as `Caller`, and `transformed=0`. `// Spec: §8.5`.
- (f) Emitter labels:
  - `audit_context_test.go`: `admin.granted` from a routed request carries the routed tenant. An unrouted trusted-headers `POST /v1/admin/grants` on a multi-tenant server under public mode records no tenant, and in particular not `podium:unrouted`. A server built with `WithMultiTenant()` and no `WithTenantRouter`, over a core bound to `podium:unrouted`, records no tenant on `admin.granted`, once under `WithPublicMode()` and once with no identity verifier. A single-tenant request carries the bound tenant.
  - `tenants_test.go`: on a multi-tenant server from `bootTenantServer` (`pkg/registry/server/tenants_test.go:25-43`), whose router resolves every org to the provisioned tenant `default` and whose core is bound to `default`, an operator's `POST /v1/admin/tenants` whose org routes to `default` records `tenant.managed` with no tenant. Extend `TestTenants_SingleTenantUnavailable` (`pkg/registry/server/tenants_test.go:187`) with an audit sink and assert that the router-less server answers `404 registry.tenant_management_unavailable` on `POST /v1/admin/tenants` and writes no audit record.
  - A routed layer write and a reorder record the routed tenant.
  - `audit_emitter_test.go`: `auditEmitterFor` labels a routed request's event with the routed tenant, and that label equals the `wrapAuditVolume` meter key for the request. It records no tenant for an unrouted multi-tenant request and for a meta-tool read on a server built with `WithMultiTenant()` and no router (public mode, and no IdP), and for those requests the meter key is `podium:unrouted` while the record carries no tenant.
  - `coverage_gaps_test.go` (`ingestAuditEmitter`, 188-229): events carry the passed tenant.
  - `rehash_test.go`: `appendSignedEvent` labels with `rec.TenantID`.
  `// Spec: §8.1, §6.3.1`.
- (g) In `test/integration/erase_test.go`, keep `TestErase_SQLitePurgesLayersAndRedactsAudit` unchanged as the single-tenant pin, and add a two-tenant SQLite case beside it. `// Spec: §8.5`.

### TEST-3. End-to-end multi-tenant erase on the binary

Targets: `test/e2e/layer_tenant_routing_test.go` and `test/e2e/standard_deployment_test.go`. The change adds no new file.

- `ltrSetup` (56-79): before boot, create a temporary `PODIUM_AUDIT_LOG_PATH` file and seed it through `audit.NewFileSink` and `Append` with one record whose `Caller` is `f.dave` and whose `Tenant` is empty. Add `PODIUM_AUDIT_LOG_PATH=<file>`, `PODIUM_AUDIT_SIGNING_KEY_PATH=<temp key>`, and `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=3600` to the environment. Store the path on `ltrFixture`.
- Before step 7, dave registers one more user layer routed to `default` (`f.as(f.dave, "default")`), so dave has layers and audit records in both the bootstrap tenant and globex.
- Step 7: carol, the `default` admin, erases dave and gets 200. `layers_purged` lists only dave's `default` layer, and the body carries only `erased`, `layers_purged`, and `audit_events_redacted`. Dave's two globex layers are still listed. Read the log through the `audit` package: `default`-tenant records naming dave carry the tombstone; globex records naming dave and the seeded legacy record are unchanged; `FileSink.Verify` passes; `user.erased` has `tenant` equal to the `default` org ID. **SPEC-3:** `user.erased` has a non-empty `superseded_head`; poll until the first `audit.anchored` after `user.erased` exists, and assert its `Target` equals the hash of a record at or after `user.erased`.
- `TestLayerEndpoint_MultiTenantUnknownOrgRejected` step 3 (416-419) asserts 401 `auth.tenant_unknown` for frank's erase. Rewrite the step comment (414-415) and the §8.5 citation (373-374) to say the erase route is tenant-routed.
- `TestLayerEndpoint_MultiTenantNoRouter` (`test/e2e/layer_tenant_routing_test.go:325`) keeps 403 `auth.forbidden`. Its comment states that an unrouted erase is refused before the admin check. Extend it: add `PODIUM_AUDIT_LOG_PATH=<temp file>` to its environment, issue one meta-tool read (`/v1/search_artifacts`) after the refusals, then read the file through the `audit` package and assert that it holds the `artifacts.searched` record, that the record carries no `tenant`, and that no record carries `podium:unrouted`. This boot installs no tenant router, so it pins the CODE-3 `WithMultiTenant` wiring on the binary. `// Spec: §8.1`.
- Extend `TestStandardDeploy_AdminErase` (`test/e2e/standard_deployment_test.go:830`) for the CODE-6 offline form: seed the audit file through the `audit` package with alice records labeled `A`, labeled `B`, and unlabeled, run `podium admin erase --audit-path <file> --salt tenant-salt --operator carol@acme.com alice@acme.com`, then assert that every alice record carries the tombstone whatever its tenant, that `user.erased` has no `tenant` key, and that `FileSink.Verify` passes. It runs the binary without the standard stack, so it does not skip on darwin. `// Spec: §8.5`.
- Extend `TestStandardDeploy_AdminEraseRegistry` (`test/e2e/standard_deployment_test.go:866`), which boots a single-tenant `--standalone` server with `PODIUM_AUDIT_LOG_PATH` (`test/e2e/discovery_search_test.go:81-87`), to pin the serverboot wiring on the single-tenant path. The request-event label comes from the core's bound tenant through `withAuditMetaMiddleware`, and the erase scope and `user.erased` tenant come from the `tenantID` serverboot passes to `server.NewLayerEndpoint` (`internal/serverboot/serverboot.go:1485`); a wiring that hands the two different IDs makes every single-tenant erase skip the records written after this change. Before the erase, issue one `/v1/search_artifacts` read and wait for its `artifacts.searched` record, as `test/e2e/discovery_search_test.go:719-720` does. After the erase, read the file through the `audit` package and assert that the `artifacts.searched` record carries a non-empty `tenant`, and that `user.erased` carries the same `tenant`. It runs the binary without the standard stack, so it does not skip on darwin. `// Spec: §8.1, §8.5`.
- Rewrite the file header (1-16) and the `TestLayerEndpoint_MultiTenantRouting` doc comment (155-156) so the `// Spec: §8.5` lines describe tenant-scoped erasure, and add `// Spec: §8.1, §8.6`.
- Keep `requireCustomTrustStore` and `msSkipIfNoStack`. The `layer_tenant_routing_test.go` cases skip on darwin; confirm on the Linux CI lane.

### TEST-4. Manual scenario S88

See Manual validation.

## Manual validation

**TEST-4.** Add scenario S88 to `test/manual-validation.md` after S87 (`test/manual-validation.md:10780`), and the index row after the S87 row (`test/manual-validation.md:238`). If another proposal takes S88 first, use the next free S-number at apply time in the index row, the heading, and checklist step S10.

```markdown
| S88 | Erasure on a multi-tenant registry redacts only the requesting tenant's audit records | standard | none | none | Postgres, S3 |
```

The scenario text:

~~~~markdown
## S88: Erasure on a multi-tenant registry redacts only the requesting tenant's audit records

**Goal.** Validate that `POST /v1/admin/erase` on a multi-tenant registry
purges and redacts only in the caller's tenant, leaves other tenants' records
and unlabeled records unchanged, re-anchors the chain, and that the offline form then redacts the remainder with a chain that
still verifies.

**Covers.** §8.5 tenant-scoped erasure, the §8.1 `tenant` attribute, and the
§8.6 re-anchor after an erasure, over §6.3.1 routing, through the compiled
binary under `trusted-headers`.

**Why by hand.** An operator reads the raw audit log across tenants. A globex
record carrying dave's tombstone after carol's erase, a record carrying
`"tenant":"podium:unrouted"`, a `user.erased` with no `tenant`, or an
`audit.gap_detected` after the restart is the defect this scenario catches.
The standard-stack end-to-end test skips silently on macOS.

**Prerequisites.** Local Postgres and MinIO from `make services-up`, plus
`test.env`. Skip if either is absent.

**Steps.**

1. Run S87 step 1 with these additions before `podium serve`. Leave
   `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` unset, so the verify scheduler runs
   at its default interval and verifies once at start.

   ```bash
   export PODIUM_AUDIT_LOG_PATH="$WORK/audit.log"
   export PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor.key"
   export PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=3600
   ```

   **Expect.** `server_alive` reports the server running. The step makes no
   claim about `$WORK/audit.log`, which `FileSink` creates on its first
   append; step 3 creates it if nothing has yet.

2. Run S87 step 2 to provision `globex-$S` as olivia.

   **Expect.** `$GLOBEX` is a UUID.

3. Create unlabeled records attributed to dave. Stop the registry, append one
   record with an empty tenant, and restart it.

   ```bash
   kill "$SRV"; wait "$SRV" 2>/dev/null
   cat > "$WORK/seed.go" <<'EOF'
   package main

   import (
       "context"
       "os"
       "time"

       "github.com/lennylabs/podium/pkg/audit"
   )

   func main() {
       s, err := audit.NewFileSink(os.Args[1])
       if err != nil { panic(err) }
       if err := s.Append(context.Background(), audit.Event{Type: "artifact.loaded", Caller: "dave@acme.com", Target: "legacy", Timestamp: time.Now().UTC()}); err != nil { panic(err) }
   }
   EOF
   go -C "$REAL_HOME/projects/podium" run "$WORK/seed.go" "$PODIUM_AUDIT_LOG_PATH"
   podium serve --no-embeddings --bind 127.0.0.1:8188 >> "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null "$URL/healthz"
   jq -c 'select(.target=="legacy") | {caller: .caller.identity, tenant}' "$PODIUM_AUDIT_LOG_PATH"
   ```

   **Expect.** One line, `{"caller":"dave@acme.com","tenant":null}`.

4. As dave, register a user layer in `default` and one in `globex-$S`.

   ```bash
   for org in default "globex-$S"; do
     as dave@acme.com "$org" -s -o /dev/null -w "%{http_code}\n" -X POST -H 'Content-Type: application/json' \
       -d "{\"id\":\"dave-$org-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/dave/notes.git\",\"ref\":\"main\"}" "$URL/v1/layers"
   done
   jq -r 'select(.caller.identity=="dave@acme.com") | .tenant // "-"' "$PODIUM_AUDIT_LOG_PATH" | sort | uniq -c
   ```

   **Expect.** Two `201` codes. The tenant counts list the `default` tenant ID,
   `$GLOBEX`, and `-` (the seeded record). No line reads `podium:unrouted`.

5. As carol, the `default` admin, erase dave.

   ```bash
   as carol@acme.com default -X POST -H 'Content-Type: application/json' \
     -d '{"user_id":"dave@acme.com","salt":"s88-salt"}' "$URL/v1/admin/erase" | tee "$WORK/erase.json"
   ```

   **Expect.** HTTP 200. `layers_purged` lists only `dave-default-<pid>`, and
   the body carries only `erased`, `layers_purged`, and
   `audit_events_redacted`.

6. Inspect the log.

   ```bash
   jq -c 'select(.caller.identity=="dave@acme.com") | {type, tenant}' "$PODIUM_AUDIT_LOG_PATH"
   jq -c 'select(.type=="user.erased") | {tenant, context}' "$PODIUM_AUDIT_LOG_PATH"
   jq -r '.type' "$PODIUM_AUDIT_LOG_PATH" | awk '/user.erased/{e=1} e && /audit.anchored/{print "anchored after erase"; exit}'
   ```

   **Expect.** No remaining dave record carries the `default` tenant ID; the
   remaining ones carry `$GLOBEX` or no tenant. `user.erased` carries the
   `default` tenant ID and a non-empty `superseded_head`. The last command
   prints `anchored after erase`.

7. Stop the registry and run the offline form against its file.

   ```bash
   kill "$SRV"; wait "$SRV" 2>/dev/null
   podium admin erase dave@acme.com --local --audit-path "$PODIUM_AUDIT_LOG_PATH" \
     --operator olivia@acme.com --salt s88-salt
   jq -c 'select(.caller.identity=="dave@acme.com")' "$PODIUM_AUDIT_LOG_PATH" | wc -l
   ```

   **Expect.** The command reports a redacted count of 1 or more, and the
   final count is `0`: the globex and unlabeled records now carry the
   tombstone.

8. Restart the registry and check the chain.

   ```bash
   podium serve --no-embeddings --bind 127.0.0.1:8188 >> "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null "$URL/healthz"
   jq -r 'select(.type=="audit.gap_detected") | .type' "$PODIUM_AUDIT_LOG_PATH" | wc -l
   grep -c "gap" "$WORK/srv.log"
   ```

   **Expect.** Both counts are `0`. The verify scheduler checks the chain at
   start, so a non-zero count means the rewritten chain does not verify.

**Cleanup.** Delete `dave-globex-$S-<pid>` with `DELETE /v1/layers?id=...` as
dave, stop the server, `rm -rf "$WORK"`, and
`(cd "$REAL_HOME/projects/podium" && make services-down)` when finished with the
standard-mode scenarios.
~~~~

When OQ-4 resolves to splitting SPEC-3 out, drop `superseded_head` and the anchored check from step 6's Expect block.

**IMPLEMENTOR'S CHOICE:** the exact request in step 3's seeding snippet and the jq filters — each step must still produce the Expect outcome as written, and the seeded record must have an empty tenant and `dave@acme.com` as `Caller`.

## Documentation changes

**DOC-1.** It lands in step S11, in the same pull request as S6.

(a) `CHANGELOG.md`. The 0047 entry at `CHANGELOG.md:277-286` ("**`POST /v1/admin/erase` is refused on a multi-tenant registry**") sits under `## [Unreleased]` and was never released. Replace it in place with a `Changed` entry:

> - **`POST /v1/admin/erase` acts in the request's tenant on a multi-tenant registry** (§8.5, §6.3.1): on a registry started with `PODIUM_MULTI_TENANT=true`, erasure purges the user's layers and redacts the audit records of the tenant the caller's organization selects. A request that resolves to no tenant is refused with `403 auth.forbidden`, and under `oidc-jwt` an organization that names no provisioned tenant gets `401 auth.tenant_unknown`. Audit records that carry no tenant, which includes every record written before this release, are left unchanged, and the response reports nothing about them. `podium admin erase --local --audit-path <PODIUM_AUDIT_LOG_PATH>` redacts them while the registry is stopped.

Add under `Added`:

> - **Registry audit events record a `tenant` attribute** (§8.1): each record names the org whose audit stream it belongs to. Events about the registry as a whole, operator `tenant.managed` events, events from requests that resolve to no provisioned tenant, and records written before this release carry no tenant. The attribute is part of the hash chain only when set, so existing logs still verify.

Add under `Fixed`:

> - **Erasure is logged with an external audit endpoint, and the chain is re-anchored after an erasure** (§8.5, §8.6): a registry whose `PODIUM_AUDIT_LOG_PATH` names an endpoint now sends `user.erased`, and a registry with local anchoring enabled anchors the rewritten chain immediately. `user.erased` records the superseded head as `superseded_head`.

Drop the `Fixed` entry's second clause when OQ-4 resolves to splitting.

(b) `docs/reference/cli.md`, `podium admin erase`.
- At line 721, replace "unregisters and purges the user's owned layers and redacts the registry audit stream;" with "unregisters and purges the user's owned layers and redacts the user identity in the audit records of the tenant the request acts in;".
- At line 721, replace the sentence "On a registry started with `PODIUM_MULTI_TENANT=true`, the registry form is refused with `auth.forbidden` for every caller, including a caller whose verified organization names no provisioned tenant, and changes nothing, because the registry keeps one audit file for every tenant." with:

  > On a registry started with `PODIUM_MULTI_TENANT=true`, the registry form acts in the tenant the caller's organization selects and redacts only that tenant's audit records. Records that carry no tenant are left unchanged, and the response reports nothing about them; the local form redacts them.

- Replace the Registry row of the mode table (line 730, which begins "| Registry (default) | Calls `/v1/admin/erase`.") with:

  > | Registry (default) | Calls `/v1/admin/erase`. Requires `--registry` (defaults to `PODIUM_REGISTRY`). Purges owned layers and redacts the user identity in the audit records of the tenant the request acts in. |

- Replace the Local row of the mode table (line 731, which begins "| Local (`--local` or `--audit-path`) | Redacts the local MCP audit log directly") with:

  > | Local (`--local` or `--audit-path`) | Rewrites an audit log file directly (default `~/.podium/audit.log`). `--audit-path` may name the registry's `PODIUM_AUDIT_LOG_PATH` file, and the local form then redacts every record in it whatever its tenant. Stop the registry first, because a running registry's next write would break the rewritten chain. Requires `--operator` to record the invoking admin. Every tombstone uses the one `--salt` supplied. |

(c) `docs/deployment/clustered.md:37`, GDPR erasure bullet. In the first sentence, replace "redacts their identity across the registry audit stream behind" with "redacts their identity in the audit records of the tenant the request acts in behind". The returned values (the purged layer ids and the count of redacted audit events) are unchanged. Replace the sentence "A registry started with `PODIUM_MULTI_TENANT=true` refuses `podium admin erase` with `auth.forbidden` and changes nothing, because the registry keeps one audit file for every tenant and a redaction would reach other tenants' records. A single-tenant registry performs erasure as described." with:

> On a registry started with `PODIUM_MULTI_TENANT=true`, the erase acts in the tenant the caller's organization selects and redacts only the audit records labeled with that tenant. Records that carry no tenant, such as those written before the `tenant` attribute existed, are left unchanged, the response reports nothing about them, and they are redacted by `podium admin erase --local --audit-path` against the replica's audit file while that replica is stopped. The erase rewrites only the audit file of the replica that serves the request. With an external audit endpoint, it rewrites no record, sends `user.erased`, and leaves redaction of the shipped stream to the receiving system. Operator `tenant.managed` records carry no tenant and are reached only by the local form.

(d) `docs/deployment/clustered.md:33`, "Audit across replicas" bullet. Append:

> Each registry audit record carries a `tenant` attribute naming the org whose audit stream it belongs to, and a SIEM separates the per-tenant streams by filtering on it. Records that describe the registry as a whole, operator `tenant.managed` records, records of requests that resolve to no provisioned tenant, and records written before the attribute existed carry no tenant.

(e) `docs/reference/http-api.md`.
- Line 498, Tenant selection paragraph. Replace the last sentence ("`POST /v1/admin/erase` is refused with `403 auth.forbidden` for every caller on a multi-tenant registry, ...") with:

  > `POST /v1/admin/erase` follows the same tenant selection, as [Erase a user](#erase-a-user-gdpr) states.

- Lines 676-678, Erase a user. Replace "redacts the user identity across the registry audit stream" with "redacts the user identity in the audit records of the tenant the request acts in". Replace the paragraph "On a registry started with `PODIUM_MULTI_TENANT=true`, the endpoint answers `403 auth.forbidden` for every caller, ..." with:

  > The response carries `erased`, `layers_purged`, and `audit_events_redacted`. On a registry started with `PODIUM_MULTI_TENANT=true`, the endpoint acts in the tenant the caller's organization selects: the admin check, the layer purge, and the redaction use that tenant, and alias discovery reads only that tenant's records. A request that resolves to no tenant is refused with `403 auth.forbidden` before the admin check, and under `oidc-jwt` an organization that names no provisioned tenant gets `401 auth.tenant_unknown`. Records that carry no tenant are left unchanged. The response reports nothing about them or about other tenants' records, so it is the same whether or not the user appears there. On a single-tenant registry, the endpoint redacts the tenant's records and the records that carry no tenant. Records of other tenants keep their content, and their `hash` and `prev_hash` values change when they follow a redacted record. When local anchoring is enabled, the registry anchors the rewritten chain immediately, and `user.erased` records the head it replaced as `superseded_head`. When the audit sink is an external endpoint, the registry sends `user.erased` and rewrites no record, and it answers `500 registry.unavailable` when that send fails.

  Drop the anchoring sentence when OQ-4 resolves to splitting.

No runnable block is added, so no `tools/doccov/manifest.yaml` entry is needed.

## Open questions

**OQ-1 (Decision 1). Shared chain or per-tenant sinks.** Record a tenant attribute on one shared chain (proposed), or open one sink and chain per tenant? The shared chain means a tenant's erase re-hashes later records of other tenants without changing their content.

**OQ-2 (Decision 7). Unlabeled records on a multi-tenant registry.** Leave unlabeled records untouched and report nothing about them (proposed), or treat them as the default tenant's? The proposed rule leaves legacy PII to the offline whole-file form run with the registry stopped. The alternative lets the default tenant's admin redact other tenants' legacy records.

**OQ-3 (Decision 4). Operator events.** Should the operator's `tenant.managed` events record no tenant (proposed), or the managed tenant? With the managed tenant, that tenant's admin could redact the operator's identity in them.

**OQ-4 (Decisions 5 and 9). Scope of this proposal.** Keep the post-erase re-anchor, the `superseded_head` key, and the endpoint-sink `user.erased` emission here (drafted), or split them out? Both gaps predate this change and also affect single-tenant registries. Adversarial review preferred moving SPEC-3, the `superseded_head` and `WithAfterErase` parts of CODE-2, CODE-4, and CODE-5, and the matching test assertions into a separate §8.6 proposal covering every chain rewrite (retention drops, the query-text-only retention pass at `pkg/audit/retention.go:131-134`, and erasure). Every staged section marks those parts "SPEC-3" or conditions them on OQ-4 splitting, so they drop out cleanly. The endpoint-sink `user.erased` stays here under either answer, because SPEC-2 rewrites the §8.5 sentence it satisfies.

## Non-goals

- One audit file, chain, or endpoint per tenant. Decision 1 records the alternative.
- Attributing records written before this change to any tenant, and any migration or backfill of the tenant attribute.
- A per-tenant audit read, query, or export endpoint. None exists, and §8 defines none.
- Per-tenant retention policy. §8.4 retention stays a deployment-wide pass.
- Counting the HTTP-boundary events (`admin.granted`, `layer.config_changed`, `layer.user_registered`) against the §4.7.8 audit-volume meter. This existing gap is outside erasure.
- Tenant labeling in the MCP server's local audit sink, which serves one user and no tenant.
- Redaction of a stream already shipped to an external endpoint, which rests with the receiver.
- Re-anchoring after the query-text-only retention pass, which rewrites the chain without moving the anchor today (`internal/serverboot/audit_retention.go:66`, where the re-anchor runs only when `dropped > 0`). SPEC-3(a) words the retention trigger as a pass that moves the head, so the spec does not require it. OQ-4 names it for a separate §8.6 proposal.
- A new error code, environment variable, or SPI. The change reuses `auth.forbidden`, `auth.tenant_unknown`, `registry.unavailable`, `srv.TenantRouted`, and the existing `reAnchor` seam.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-04, automated)

- **The label rule keyed on router presence wrote `podium:unrouted` on a multi-tenant registry with no identity verifier.** serverboot installs the router only when `cfg.multiTenant && verifierInstalled` (`internal/serverboot/serverboot.go:1451`) but binds core to `podium:unrouted` whenever `cfg.multiTenant` is set (`:1105-1109`). CODE-3 item 1 now keys the rule on multi-tenant mode through the `auditTenant` helper, a `Server.multiTenant` field set by a new `WithMultiTenant()` option and by `WithTenantRouter`, and serverboot sets the option from `cfg.multiTenant`. The IMPLEMENTOR'S CHOICE, the Summary, Fixed decisions, Watch out for, the edge table, the CODE-3 targets, checklist S4, TEST-2(f) (no-router cases for `admin.granted` and a meta-tool read), and TEST-3 (`TestLayerEndpoint_MultiTenantNoRouter` reads the audit file) follow.
- **The always-admit erase subtest's caller routes to B after TEST-2(a).** TEST-2(c) now switches that subtest from `trDave` to the unrouted `trErin` and keeps `assertEraseRefused`.
- **CODE-4 misstated the erase handler's order.** It now reads method check, read-only check, `requireTenant`, `authAdmin`, body decode and validation, layer purge, audit, with the `pkg/registry/server/layers.go:964-1001` citation.
- **`UserErasedEvent` cited an injected clock that `pkg/audit` lacks.** CODE-4 now sets `time.Now().UTC()`, as `EraseUser` does at `pkg/audit/retention.go:278`.
- **DOC-1(b) cited the Registry row as the Local row, and the "registry audit stream" phrasing stayed in cli.md and clustered.md.** DOC-1(b) now cites the Local row at line 731 by its text, adds edits for the Registry row at line 730 and the first sentence of line 721, and DOC-1(c) adds an edit for the first sentence of clustered.md:37.
- **`audit_events_unattributed` gave a tenant admin a cross-tenant oracle.** The field is removed. Decision 7, OQ-2, the Summary, Fixed decisions, SPEC-2(c), CODE-2 (`EraseUser` keeps returning `(int, error)`), CODE-4, CODE-6, the edge table, DOC-1(a), (b), (c), and (e), TEST-1(d) and (e), TEST-3, and S88 drop it. TEST-2(d) asserts that erasing a user who appears only outside the tenant returns the same body as erasing a user who appears nowhere, and a new edge-table row records the outcome.
- **DOC-1(d) and the changelog `Added` entry omitted unlabeled operator, unrouted-request, and legacy records.** Both now list them.
- **SPEC-3(a) required a re-anchor after the query-text-only retention pass.** It now reads "after a §8.4 retention pass moves the head or a §8.5 erasure rewrites the chain", and the Non-goal cites `internal/serverboot/audit_retention.go:66`.
- **SPEC-2 pointed to an offline erasure form the spec does not define.** New SPEC-2(e) adds the `--local --audit-path --operator --salt` form to the §8.5 command block with a sentence defining it, and SPEC-2(c) refers to it.
- **S88 step 1 expected the audit file to exist at boot.** The Expect block no longer asserts it, because `FileSink` creates the file on its first append (`pkg/audit/file.go:97`).
- **No end-to-end test covered the CODE-6 offline form over tenant-labeled records.** TEST-3 extends `TestStandardDeploy_AdminErase` (`test/e2e/standard_deployment_test.go:830`) with A-labeled, B-labeled, and unlabeled alice records, and checklist S9 names it.
- **TEST-2(f) asserted that the label equals the meter key for an unrouted request, which contradicts Decision 10.** The `audit_emitter_test.go` bullet now ties the equality to the routed case only and asserts that an unrouted request and a no-router meta-tool read leave the record unlabeled while the meter keys them under `podium:unrouted`, because serverboot binds core to `podium:unrouted` whenever `cfg.multiTenant` is set (`internal/serverboot/serverboot.go:1105-1109`) and meters by `registry.TenantFor` (`:1638`).

### Pass 2 (2026-10-04, automated)

- **The CODE-3 item 3 rationale guarded a single-tenant labeling path that cannot occur, and TEST-2(f) asserted on an event a single-tenant server never emits.** Every tenant-management handler runs `tenantAdminGate`, which answers `404 registry.tenant_management_unavailable` with no router (`pkg/registry/server/tenants.go:104-108`), and `withTenantRouting` passes `/v1/admin/tenants` through unrouted (`pkg/registry/server/server.go:455-458`). CODE-3 item 3 now drops the `emitOperatorEvent` wrapper and states that the shared rule yields no tenant for these events. TEST-2(f) replaces the single-tenant assertion with a `tenants_test.go` bullet: an operator whose org routes to the provisioned tenant `default` records an unlabeled `tenant.managed`, and the extended `TestTenants_SingleTenantUnavailable` asserts the 404 and no audit record. The TEST-2 targets list `tenants_test.go`.
- **No test pinned that the erase admin check runs in the routed tenant.** TEST-2(c) adds "erase refused for a caller who is admin only in another tenant", sent as `trOliviaA` with admin in B, and a second case with admin only in the poison tenant P. Both assert `403 auth.forbidden`, one admin call, unchanged layers and rows, and a byte-identical audit file. The TEST-2(c) annotation adds §4.7.2.
- **CODE-6 cited `cmd/podium/admin.go:240` for the `--salt` help, which is the `--operator` flag.** It now cites `cmd/podium/admin.go:239`.
- **The edge-table row for foreign-labeled records on a single-tenant registry cited DOC-1(c), which covers only multi-tenant registries.** The row now cites DOC-1(e) and TEST-1(j).
- **Checklist S2's OQ-4 skip list omitted S8 and S11.** S2 now names S5, S6, S7, S8, S9, S10, and S11 and the parts marked "SPEC-3" or conditioned on OQ-4 splitting, and OQ-4 states the same marking rule.
- **No test pinned that a single-tenant erase leaves records labeled with another tenant untouched.** New TEST-1(j) `TestEraseUser_SingleTenantScopeExcludesForeignLabels` erases with `EraseScope{Tenant: "S", Unlabeled: true}` over S-labeled, F-labeled, and unlabeled records, including an F-only alias, and asserts that F records keep their content, that the alias drives no redaction, and that the count excludes F records.

### Pass 3 (2026-10-04, automated)

- **No handler-level test pinned that a single-tenant erase reaches records its own emitters labeled with the bound tenant.** The single-tenant erase tests seed only unlabeled records (`pkg/registry/server/erase_test.go:96-100`; `test/integration/erase_test.go:70-74`), which `Unlabeled: true` redacts whatever `scope.Tenant` holds. TEST-2(d) adds `TestErase_SingleTenantReachesOwnLabels`: alice registers a layer through the endpoint so `emitLayerEvent` writes a `"t"`-labeled record, seeded unlabeled and `"f"`-labeled alice records sit beside it, and carol's erase must tombstone the `"t"` and unlabeled records, leave the `"f"` record unchanged, report their count, and write `user.erased` with `tenant` `"t"` on the file sink. TEST-3 extends `TestStandardDeploy_AdminEraseRegistry` to assert on the binary that a request event's `tenant` equals the `user.erased` `tenant`, which joins the core's bound tenant to the `tenantID` serverboot passes to `server.NewLayerEndpoint` (`internal/serverboot/serverboot.go:1485`). Watch out for, and checklist steps S8 and S9, follow.
- **Correction: the Watch out for bullet credited TEST-2(d) with pinning the core-versus-endpoint tenant agreement.** `TestErase_SingleTenantReachesOwnLabels` builds only a `LayerEndpoint` (as `eraseTestEndpoint` does at `pkg/registry/server/erase_test.go:25-48`), so `withAuditMetaMiddleware` and `s.core.TenantFor` never run, and its labeled record comes from `emitLayerEvent` (`pkg/registry/server/layers.go:486-491`), which reads `e.tenantID` like the erase scope. The bullet now credits TEST-3 with the core-versus-endpoint agreement and TEST-2(d) with the agreement between the handler's erase scope and the endpoint's own emitter label.
