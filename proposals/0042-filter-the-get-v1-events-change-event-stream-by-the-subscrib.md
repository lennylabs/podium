# Proposal 0042: Filter the GET /v1/events change-event stream by the subscriber's §4.6 visibility at delivery time, and state the webhook receiver delivery scope

- Issue: (to be filed)
- Status: Implemented (2026-10-03). Signed off as staged. OQ-1 (keying receivers per routed tenant) goes to a separate follow-up proposal; DOC-1(d) documents the shared pool meanwhile. OQ-2: keep the drafted posture (an admin subscriber gets only its own §4.6 view; overrides stay explicit and audited). Sub-question: accept as drafted (the unverified free-form IdP label case), recorded as a follow-up.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.6 gains the change-event stream visibility rule: the registry delivers an event to a `GET /v1/events` subscriber only where the event's recorded tenant is the subscriber's tenant and the subscriber's identity can see the layer the event names under §4.6, narrowed by §6.3.1 path scopes on artifact and domain events, with a prior-visibility arm on `layer.config_changed` and a per-subscriber rewrite of the reorder event. §4.6, §7.3.1, and §7.5.4 point to it (SPEC-1). §7.3.2 states that the receiver fan-out applies no layer visibility filter (SPEC-2).
- `pkg/registry/core` gains `EventScope`, the per-event `EventAudience` evaluator with a cancellable single-flight layer read and a per-event group memo, a `layerConfigs` split out of `resolveLayers`, and an exported `TenantFor` (CODE-1).
- `pkg/registry/ingest.EventEmitter`, `Server.PublishEvent`, and `LayerEndpoint.WithEventPublisher` take a `core.EventScope`. The ingest and orchestrator publish sites and the layer endpoint set it, including the pre-change visibility on an update and an unregister and the full reorder list (CODE-2, CODE-3).
- `pkg/registry/server/events.go` evaluates each event in the `handleEvents` loop after dequeue, against the identity and tenant resolved when the stream opened. The webhook goroutine is unchanged (CODE-4).
- Tests at every level: a core unit suite, an in-process HTTP integration suite, and a trusted-headers end-to-end suite on the binary, including a multi-tenant routing case on the standard stack (TEST-1, TEST-2, TEST-3).
- Docs, changelog, and manual validation follow: the HTTP API reference, the harness and SDK consumer pages, the `[Unreleased]` entry, and scenario S83 (DOC-1, DOC-2, CL-1, MV-1).

**Fixed decisions.**

- The stream fails closed. An event the registry cannot evaluate is withheld from that subscriber (decision taken on the user's behalf). A non-cancellation layer-read error is cached for the event and withholds it from every subscriber, and a read that failed because the evaluating subscriber's context ended is not cached.
- Evaluation runs at delivery, in the per-connection loop. `eventBus.publish` and `Server.PublishEvent` perform no store read. The layer list is read once per event, by the first subscriber whose evaluation needs it, and each group is resolved once per event, by the first subscriber whose evaluation needs it. Every later subscriber of that event reuses those reads, so the visibility the event's shared reads observe governs its delivery to every subscriber.
- Every rule requires the event's recorded tenant to equal the subscriber's routed tenant and keys on the layer the event names. Artifact events also gate on `ScopeSet.AllowsRead(artifact ID)`, and `domain.published` on `ScopeSet.AllowsRead(domain path)`.
- The prior-visibility arm applies to `layer.config_changed` alone, is a snapshot the publisher carries, and is evaluated against `Layers[0]` only.
- A reorder event reaches a subscriber that can see at least one named layer, and its `layer` value is rewritten for that subscriber to the visible subset in reorder order.
- The event's scope is a typed argument (`core.EventScope`). No code parses payload keys to find the tenant or the layer. Payloads are unchanged apart from the reorder rewrite.
- The identity and the tenant are resolved once, when the stream opens. SCIM-resolved groups and stored layer visibility are re-read per event.
- The evaluator lives in `pkg/registry/core`. `resolveLayers` keeps its boot-time fallback for reads. The event path uses `layerConfigs`, withholds on a read error, and falls back to the boot-time layers only on an empty list.
- The §4.6 public-mode and no-identity bypasses admit every layer before any store read, and scopes still narrow. The filesystem-registry bypass is not named: a §13.11 filesystem-source registry serves no stream.
- The §4.7.2 admin role confers no override on the stream in the code. Whether the spec states this outright is OQ-2.
- An event published with no layer reaches no stream subscriber, and admin-only delivery of such events was rejected. A future publish site names its layer or amends §7.6.
- A withheld event leaves no trace on the stream, and the `_heartbeat` line still reaches every subscriber.
- Webhook receivers are not filtered by §4.6. Webhook delivery code is unchanged.
- The proposal adds no §6.10 error code, endpoint, SPI, environment variable, flag, or SDK change. Pre-1.0, the signature change carries no shim.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.
- **The signature change does not compile in parts.** `internal/serverboot/serverboot.go:1487` passes `srv.PublishEvent` to `WithEventPublisher`, and `internal/serverboot/reingest.go:53` passes it as the orchestrator's `ingest.SourceIngestOptions.PublishEvent` emitter. CODE-2, CODE-3, and CODE-4 therefore land in one commit (step S4).
- **Existing tests that publish without a scope stop receiving events silently.** A nil or empty scope is withheld, so a stream assertion in an unmodified test hangs until its timeout rather than failing at the publish. `pkg/registry/server/events_test.go` (:53, :108), `pkg/registry/server/options_test.go` (:45), and `test/e2e/http_api_test.go` (`TestHTTPAPI_EventsTypeFilter`, :1051-1052) need a scope whose `TenantID` matches the registry they boot. `TestHTTPAPI_EventsTypeFilter` also backs a runnable docs example for `docs/reference/http-api.md`.
- **Do not evaluate inside `eventBus.publish`.** It runs synchronously on the ingest path under `b.mu.RLock` (`pkg/registry/server/events.go:68-81`). A store read there stalls ingest and every subscriber.
- **Do not hold a mutex across the layer read.** A `sync.Mutex` wait ignores context, so a subscriber whose client disconnected could not leave while another subscriber's store read hangs. CODE-1 uses a `done` channel that waiters select on together with their own `ctx.Done()`.
- **Do not decide cacheability by matching the store error against `ctx.Err()`.** Backends wrap errors. Decide from `ctx.Err() != nil` on the evaluating context.
- **The reorder payload's `layer` value is a joined string.** Do not split it to find the layers. `EventScope.Layers` carries the list.
- **The payload map is shared.** The bus subscribers and the webhook goroutine read the same `Data` map. The reorder rewrite copies it before setting `layer`.
- **`resolveLayers` keeps its fallback.** The read path falls back to the boot-time layers on a store error (`pkg/registry/core/core.go:412-416`). Changing that is out of scope, and TEST-1 case 7 guards it.
- **Multi-tenant tenant values disagree by design.** In multi-tenant mode `core` binds to `podium:unrouted` and the producers publish under the bootstrap tenant (`internal/serverboot/serverboot.go:887`, `:1106-1109`, `:1484`, `:1635`; `internal/serverboot/reingest.go:53`). An unrouted subscriber therefore matches no event, and a subscriber routed to the bootstrap tenant matches that tenant's events. Only the spawned binary wires these values together. TEST-2(d) binds `core` to `"t"` and cannot reproduce the `podium:unrouted` binding, so TEST-3 case 3 boots a multi-tenant binary. §13.10 keeps multi-tenancy out of standalone (`spec/13-deployment.md:217`), so that case runs on the standard stack and skips where no Postgres and S3 are configured. The layer endpoint is mounted outside `srv.Handler()` (`internal/serverboot/serverboot.go:1506-1507`, `:1580`), so `/v1/layers*` is never tenant-routed and its admin gate resolves against `podium:unrouted` (`:1494-1502`; `pkg/registry/core/admin.go:25`). `PODIUM_BOOTSTRAP_ADMINS` seeds grants only under the bootstrap tenant (`serverboot.go:897`), and the Postgres store cannot hold a grant under `podium:unrouted` (`pkg/store/postgres.go:47`, `:58-62`). No caller of a multi-tenant Postgres binary therefore passes that gate, so an admin layer write is refused, and so is any operation on a git repository that resolves to the file transport (`pkg/registry/server/layer_capabilities.go:44-48`, `:92-102`). TEST-3 case 3 therefore seeds no admin and produces its bootstrap-tenant event without either. Fixing the unrouted admin gate is outside this proposal.
- **Existing anonymous-stream end-to-end tests become bypass regression guards.** `TestLayerUpdate_RotationWakesNoWatcher` (`test/e2e/layer_visibility_narrowing_test.go:143`) and the streams in `test/e2e/notification_sink_primitive_test.go` open an anonymous stream on a no-identity server. If they start failing, the no-identity bypass is broken. Do not adjust them to pass.
- **Parts of `test/e2e` skip silently on macOS.** Grep the run output for `SKIP` before claiming end-to-end verification.
- **`layer.user_registered` never reaches the stream.** The prior-visibility arm covers admin-defined layers only (`pkg/registry/server/layers.go:416-437`).

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. §7.6 gains the change-event stream visibility rule, §4.6 and §7.5.4 gain pointers, and §7.3.1 names §7.6 as the stream's home.
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-2. §7.3.2 states that the receiver fan-out applies no §4.6 or §7.6 filter.
      Levels: —. Depends on: —
- [x] **S3 · code** — CODE-1. `core.EventScope`, `EventAudience`, the `layerConfigs` split, and `TenantFor` land in `pkg/registry/core`.
      Levels: unit. Depends on: S1
- [x] **S4 · code** — CODE-2, CODE-3, CODE-4. The emitter carries the scope, every publish site sets it, and `handleEvents` filters at delivery. Bundled because the `ingest.EventEmitter`, `Server.PublishEvent`, and `WithEventPublisher` signatures must change in one commit for `internal/serverboot` (`serverboot.go` and `reingest.go`) to compile.
      Levels: unit, integration, e2e. Depends on: S3
- [x] **S5 · test** — TEST-1. Core unit suite for the evaluator, the memo, and cancellation.
      Levels: unit. Depends on: S3
- [x] **S6 · test** — TEST-2. In-process HTTP integration suite for `/v1/events` filtering and the unfiltered receiver.
      Levels: integration. Depends on: S4
- [x] **S7 · test** — TEST-3. Trusted-headers end-to-end suite on the binary, including the standard-stack multi-tenant routing case.
      Levels: e2e. Depends on: S4
- [x] **S8 · docs** — DOC-1, DOC-2. The HTTP API reference and the harness and SDK consumer pages describe the filtered stream and the receiver delivery scope. Bundled because the passages describe the stream delivery rule and its receiver counterpart, and one reader reviews them together. Land S8 in the same pull request as S4 so no released build documents behavior it lacks.
      Levels: —. Depends on: S4
- [x] **S9 · docs** — CL-1. The `[Unreleased]` `Fixed`, `Changed`, and `Documentation` entries.
      Levels: —. Depends on: S4
- [x] **S10 · docs** — MV-1. Manual-validation scenario S83.
      Levels: manual. Depends on: S4

**Ordering constraints.** S1 and S2 land the rule every later step cites. S3 precedes S4 because S4 constructs `core.EventScope`. S5 may proceed in parallel with S4. S6 and S7 need the server wiring from S4.

## Current state and the gap

### The stream delivers every event to every subscriber

`GET /v1/events` delivers every published event to every subscriber, whatever the subscriber's identity. `handleEvents` (`pkg/registry/server/events.go:118-131`) reads only the `type` query parameter and calls `s.events.subscribe(types)`. It never calls `s.identity(r)`, never reads the per-request tenant that `withTenantRouting` places on the context (`pkg/registry/server/server.go:452-486`), and consults no visibility. `eventBus.publish` (`events.go:68-81`) skips a subscriber only when its type filter rejects the event. Neither `eventSubscription` nor `registryEvent` (`events.go:37-55`) carries a tenant or a layer. `Server.PublishEvent` (`events.go:192-218`) hands the payload unchanged to the bus and, in a goroutine with `context.Background()`, to `webhooks.Deliver(…, s.tenant, …)` (`events.go:207-209`). `Worker.Deliver` (`pkg/webhook/webhook.go:190-218`) filters receivers only by `Disabled` and `Matches(eventType)`.

### Every published event names a layer

The production publish sites are:

| Event | Payload | Site |
|:--|:--|:--|
| `domain.published` | `{domain, layer, tenant}` | `pkg/registry/ingest/ingest.go:488-492` |
| `artifact.published` | `{id, version, content_hash, layer, tenant}` | `ingest.go:840-846` |
| `artifact.deprecated` | `{id, version, layer}` | `ingest.go:848-852` |
| `layer.history_rewritten` | `{tenant, layer, prior_ref, new_ref}` | `pkg/registry/ingest/orchestrator.go:129-134` |
| `layer.ingested` | `{tenant, layer, reference, counts}` | `orchestrator.go:172-181` |
| `layer.config_changed` | `{layer, action}` | `pkg/registry/server/layers.go:532-540`, reached from `emitLayerEvent` (`:416-437`) and from reorder (`:1563-1566`) |

Both artifact events also name the artifact ID. A subscriber who cannot see a layer under §4.6 therefore learns the IDs, versions, and layers of the artifacts it holds and the domain paths it publishes. Publishing a hidden parent discloses that parent with no `extends:` chain involved. Proposal 0027 recorded this in its Non-goals and filed it separately.

### The spec does not sanction the leak

§4.6 says "Read-side enforcement happens at the registry on every call." §4.7.2 says read access is "enforced at the registry on every API call". §7.6 lists `client.subscribe(...)` among the SDK reads and says "Identity providers, the cache, visibility filtering, layer composition, and audit are all the same as in the MCP path". No spec text names `/v1/events` or promises unfiltered delivery. Two documentation passages describe the current behavior: `docs/consuming/configure-your-harness.md:47` ("The stream itself applies no per-caller filtering") and `docs/reference/http-api.md:594` ("omit it to receive every event"). Because the spec states no stream-specific rule, the fix defines what "can see" means for each event type.

### Constraints on a fail-closed delivery-time filter

1. The read paths combine layer visibility with the caller's §6.3.1 path scopes. `visibleManifests` (`pkg/registry/core/core.go:2101-2125`) applies `EffectiveLayersWith` and then `applyReadScope`. It is not fail-closed: `resolveLayers` falls back to the boot-time layers on a store error (`core.go:412-416`), and an empty layer list admits everything (`core.go:2114`).
2. `layer.config_changed` is not always resolvable from the current store. Unregister publishes after the `DeleteLayerConfig` soft delete (`layers.go:1486-1491`), and List and Get exclude tombstones (`pkg/store/store.go:446-450`). A visibility withdrawal (`wakesWatchers`, `layers.go:495-506`) has to reach exactly the subscribers who lost visibility, because `pkg/sync/watch_server.go:83-147` re-syncs only on a trigger and needs that wake to remove what it materialized. A reorder publishes `layer = strings.Join(req.Order, ",")` (`layers.go:1566`), a comma-joined list that no per-layer lookup resolves and that can name hidden layers.
3. The bus is in-process. `publish` runs synchronously on the ingest path under `b.mu.RLock`, with 256-slot drop-on-full buffers (`events.go:94`). Store-backed evaluation therefore belongs in the per-connection loop (`events.go:156-173`), and the loop has to stay cheap. With SCIM wired, `resolveGroup` performs one `MembersOf` store read per `groups:` entry (`internal/serverboot/serverboot.go:1195-1204`).

### Identity and tenancy

Identity is available in every mode through `s.identity(r)` (`server.go:1433-1446`). It is verified only when a verifier is installed (`pkg/registry/server/identity_verify.go:39-49`). It is `IsPublic` in public mode and with no identity provider (`server.go:288-293`; `internal/serverboot/identity_verify.go:56-61`), and `layer.VisibleWith` short-circuits on that flag (`pkg/layer/composer.go:64-67`).

In multi-tenant mode, one Server and one bus serve every routed tenant. The event producers are bound to the bootstrap tenant, and webhook receivers are registered and delivered under the fixed key `s.tenant = "default"` (`server.go:284`; `pkg/registry/server/webhooks.go:91`, `:139`, `:182`, `:195`, `:253`; `events.go:208`).

## Decisions

- **Fix it and fail closed.** The decision was taken on the user's behalf. The stream delivers an event to a subscriber only where the subscriber's identity can see what the event concerns under the §4.6 evaluator the read endpoints use. An event the registry cannot evaluate is withheld from that subscriber.
- **One evaluation per event type, keyed on the layer the event names.**
  - `artifact.published` and `artifact.deprecated`: the ingesting layer must be visible under §4.6, and §6.3.1 scopes must permit discovering the artifact ID (`ScopeSet.AllowsRead`, `pkg/layer/scope.go:82`).
  - `domain.published`: the layer must be visible, and scopes must permit the domain path.
  - `layer.ingested` and `layer.history_rewritten`: the layer must be visible.
  - `layer.config_changed` for register, update, restore, or unregister: the layer must be visible under its current stored record, or under the visibility it held before the change.
  - `layer.config_changed` for a reorder: at least one named layer must be visible, and the delivered `layer` value is rewritten for that subscriber to the visible subset, in reorder sequence.

  This matches the layer-then-scope intersection in `visibleManifests`, and §4.7.2 has no per-artifact roles.
- **The pre-change visibility arm is a publish-time snapshot.** The publisher passes `VisibilityOf(before)` on an update and `VisibilityOf(cfg)` on an unregister, since the record is tombstoned by delivery time. Without the snapshot, the unregister wake is withheld from everyone, and the withdrawal wake is withheld from exactly the watchers who must remove what they materialized. The arm discloses nothing new, because the subscriber could already see the layer it names.
- **Evaluation happens at delivery.** It runs in the `handleEvents` loop after dequeue, never inside `eventBus.publish` or `PublishEvent`, at the shared reads the Summary fixes; every later subscriber reuses those reads through the per-event memo below. A change made after the event's shared read governs the next event. A subscriber withheld at CODE-1 steps 1 to 3, or admitted at step 4 or by `layer.VisibleWith` before it calls the group resolver, triggers no read, so a change made after that subscriber's evaluation and before the shared read still governs the current event. The subscriber's identity and tenant are resolved once, at subscribe time, through `s.identity(r)` and the routed tenant. JWT groups are fixed for the life of the stream.
- **A per-event memo bounds the cost.** The first subscriber to evaluate the event performs one `ListLayerConfigs` read for the event's tenant, and every other subscriber reuses the result. `resolveGroup` results are memoized per group for the event. Store reads per event per replica are therefore 1 plus the number of distinct groups named by the event's layers, independent of the subscriber count. Each subscriber's remaining work is an in-memory `VisibleWith` over the event's layers. No tunable constant is added, so no `PODIUM_*` knob is needed (§13.12).
- **The event path diverges from `visibleManifests` at four fail-closed points.** (a) A `ListLayerConfigs` error withholds the event and does not fall back to the boot-time layers. (b) An event published with no layer is withheld from every subscriber. (c) An event whose recorded tenant differs from the subscriber's routed tenant, or that is published with no tenant, is withheld. (d) A layer absent from the resolved list is not visible under the current arm. The read path's fallback on an empty store list (the boot-time layers) is kept, so the two surfaces resolve the same layer list whenever the read succeeds. A memo resolution that failed only because the evaluating subscriber's context was cancelled is not cached, so one disconnecting subscriber cannot withhold the event from the others.
- **Events with no layer reach no stream subscriber.** None reaches the bus today. `admin.granted`, `visibility.denied`, `freeze.break_glass`, `user.erased`, `registry.read_only_entered` and `registry.read_only_exited`, `artifact.signed`, `layer.user_registered`, and the read events go only to the audit sink (§8.1; `layers.go:433-437` publishes only `layer.config_changed`). Receivers would deliver such an event under §7.3.2. Admin-only delivery was rejected for two reasons. It would add a per-event admin-grant lookup for an event type that does not exist yet. In public-mode and no-identity deployments `AdminAuthorize` refuses every caller (`pkg/registry/core/admin.go:21-23`), so admin-only delivery would reach no one there. A future publish site must name its layer or amend §7.6.
- **The §4.7.2 admin role confers no override on the stream.** An admin subscriber receives what its own §4.6 view admits. The §4.7.2 override is an explicit diagnostic that is audited per use, and stream delivery writes no audit record. The stream exists to wake re-resolution of the subscriber's own effective view, which §4.6 already defines. OQ-2 lets the reviewer choose the §7.3.1 layer-list posture instead, and SPEC-1 stages the admin sentence separately for that reason.
- **The §4.6 bypasses apply unchanged.** With `IsPublic` (public mode, or no request-time verifier), the evaluator admits every layer before any store lookup. Every subscriber therefore receives every event of its tenant, narrowed only by presented scopes, as `visibleManifests` does. A §13.11 filesystem-source registry has no registry server and serves no stream, so its bypass is not named. Under `injected-session-token`, an anonymous subscriber is refused with 401 before the stream opens (existing behavior). Under `oidc-jwt` with no token, and under `trusted-headers` with no headers, the subscriber resolves as unauthenticated and non-public and receives events only for `public: true` layers (`composer.go:68-73`).
- **Outbound webhook receivers (§7.3.2) are not filtered by §4.6.** A receiver receives every event its event filter matches. A receiver carries no identity to evaluate, and receiver configuration is admin-only. SPEC-2 states the absence of the filter in one sentence. Receivers gain no layer or visibility scope field: the receiver object stays as it is, and `event_filter` remains the only narrowing. Webhook delivery code is unchanged.
- **The heartbeat is unaffected.** The `_heartbeat` frame (`events.go:168-172`) is written by the handler and is not a bus event, so it still reaches a subscriber who can see nothing. A withheld event leaves no trace on the stream, which matches §4.5.5 and the §7.3.1 layer-read rule that withholding is silent.
- **The event's scope travels as a typed argument.** The scope comprises the tenant, the layer list, the artifact or domain path, and the prior visibility. `ingest.EventEmitter` changes from `func(ctx, eventType, data)` to `func(ctx, scope core.EventScope, eventType, data)`, and every caller is updated. Payload keys are inconsistent (`artifact.deprecated` and `layer.config_changed` carry no tenant), and the reorder `layer` value is a joined string, so key parsing would be stringly typed and could not fail closed reliably.
- **The evaluator lives in `pkg/registry/core`.** That package is the canonical §4.6 surface and owns `resolveLayers`, `VisibilityOf`, `resolveGroup`, and the boot-time layers. `resolveLayers` is refactored so its store read is reusable with an error result, and the event path does not copy the code. `pkg/registry/ingest` gains an import of `core` for the scope type. There is no cycle: `core`'s production dependencies do not include `ingest` (checked with `go list -deps ./pkg/registry/core`), and `core`'s tests are package `core_test`.
- **No new surface.** The proposal adds no §6.10 error code, endpoint, SPI, environment variable, flag, or SDK change. Both SDK subscribe implementations already send the client's credentials (`sdks/podium-py/podium/client.py:1530`; `sdks/podium-ts/src/index.ts`, near line 1447), and the `podium sync` watcher sends its bearer token (`pkg/sync/watch_server.go:105-110`).

## Spec amendment: §7.6 change-event stream visibility

**SPEC-1.** Four anchors, in `spec/07-external-integration.md` and `spec/04-artifact-model.md`. All four edits land in one commit (step S1).

(a) `spec/07-external-integration.md`, §7.6 "Language SDKs". Insert a new paragraph after the paragraph that begins "Identity providers, the cache, visibility filtering, layer composition, and audit are all the same as in the MCP path" and before the paragraph that begins "The SDKs deliberately do not implement the MCP meta-tool semantics":

> **Change-event stream visibility.** `client.subscribe(...)` reads the registry's change-event stream, which the §7.5.4 watcher also reads. The registry resolves the subscriber's identity and tenant once, when the stream opens. It evaluates each event no earlier than the event's first delivery to a subscriber, against the stored layer visibility and the group membership it reads once for that event and applies to every subscriber of the event, so a visibility change made before that first evaluation governs the event's delivery to every subscriber. The registry records, with each event it places on the stream, the tenant and the layer or layers the event concerns; these are not necessarily fields of the event's `data`. The registry delivers an event to a subscriber only where the event's recorded tenant is the subscriber's tenant and the §4.6 evaluator reports a layer recorded for the event visible to the subscriber. On `artifact.published` and `artifact.deprecated`, the subscriber's §6.3.1 path scopes must also permit discovering the artifact the event names, and on `domain.published` they must permit the domain path it names. A `layer.config_changed` recorded for a `register`, `update`, `restore`, or `unregister` is also delivered to a subscriber that could see the layer under the visibility the layer held before the change, so a subscriber that loses sight of a layer, or whose visible layer is unregistered, receives the event that withdraws it. A `layer.config_changed` recorded for a `reorder` is delivered to a subscriber that can see at least one reordered layer, and the `layer` value delivered to that subscriber names, in reorder sequence, only the reordered layers that subscriber can see. An event the registry cannot evaluate is withheld; this covers an event whose tenant's layer list cannot be read, an event published with no layer, and an event published with no tenant. A withheld event leaves no trace on the stream and is reported through no §6.10 error code, on the same footing as §4.5.5, and the keepalive line reaches every subscriber. The §4.6 public-mode and no-identity bypasses apply on the same terms: the evaluator reports every layer visible, so a subscriber there receives every event of its tenant that its scopes permit. Outbound webhook receivers are governed by §7.3.2.

**OQ-2 rider.** Append the following two sentences to the end of paragraph (a) only when the reviewer resolves OQ-2 as drafted. While OQ-2 is open, paragraph (a) lands without them. The paragraph as written already yields the drafted posture, because it names no admin arm; the rider states the divergence from §7.3.1 outright.

> A holder of the §4.7.2 admin role receives what its own §4.6 view admits. This differs from the §7.3.1 layer read visibility rule, because the stream wakes re-resolution of the subscriber's own §4.6 view and the §4.7.2 override is a per-use, audited diagnostic; a reorder event's `layer` value can therefore name fewer layers than the admin's own reorder response reports.

(b) `spec/04-artifact-model.md`, §4.6 "Layers and Visibility", subsection "Visibility". The paragraph currently reads:

> Read-side enforcement happens at the registry on every call. Git provider permissions are not consulted at request time; visibility is governed entirely by the registry config (or, for user-defined layers, by the registration record).

Insert after its first sentence:

> The §7.6 change-event stream applies this evaluator to each event it delivers, on the terms §7.6 states.

(c) `spec/07-external-integration.md`, §7.3.1, paragraph "**The layer lifecycle event follows what the write changed.**". The sentence currently reads:

> A `layer.config_changed` reaches the §7.5.4 change-event stream where the change can alter what a profile resolves, which is a change to the layer's visibility, its order, or its source location.

Replace it with:

> A `layer.config_changed` reaches the §7.6 change-event stream where the change can alter what a profile resolves, which is a change to the layer's visibility, its order, or its source location, on the delivery terms §7.6 states, which include a subscriber that could see the layer before the change.

(d) `spec/07-external-integration.md`, §7.5.4 "Watch Mode and Toggle Persistence". Append to the end of the "**Watch mode**" bullet, after "Toggles persist across events and across watcher restarts.":

> The registry delivers a watcher only the events the §7.6 change-event stream visibility rule admits for its caller.

## Spec amendment: §7.3.2 receiver delivery scope

**SPEC-2.** `spec/07-external-integration.md`, §7.3.2 "Outbound Webhooks". The line currently reads:

> Receivers are configured per org (URL + HMAC secret).

Append one sentence to the same line, so it reads:

> Receivers are configured per org (URL + HMAC secret). The tenant-wide receiver fan-out does not apply the §4.6 layer visibility evaluator or the §7.6 change-event stream visibility rule: a receiver carries no caller identity, and its event filter is the only narrowing applied to the events it receives.

Nothing else in §7.3.2 changes. The existing text already states the per-org configuration, the receiver object, the admin-only CRUD, and the debounce window's reach.

## Proposed solution

### CODE-1. `core`: the event scope type and the per-event audience evaluator

`pkg/registry/core/event_visibility.go` (new), and `pkg/registry/core/core.go`.

**Types.**

```go
// EventScope names what a §7.6 change event concerns. The publisher sets it,
// because payload keys differ by event type and a reorder's `layer` value is
// a joined string that no per-layer lookup resolves.
//
// Spec: §7.6
type EventScope struct {
	TenantID string            // the tenant the event belongs to; "" is withheld
	Layers   []string          // the layers the event names, in reorder sequence for a reorder; empty is withheld
	Path     string            // the artifact ID or domain path; "" skips the §6.3.1 scope gate
	Prior    *layer.Visibility // pre-change visibility of Layers[0] on a layer.config_changed; nil otherwise
}

// EventAudience evaluates one published event for each subscriber that
// dequeues it, sharing one layer read and one group memo across them.
type EventAudience struct { /* unexported fields, see below */ }

// NewEventAudience performs no I/O, so Server.PublishEvent stays off the store.
func (r *Registry) NewEventAudience(scope EventScope) *EventAudience

// Visible reports whether the event reaches the subscriber id routed to
// tenant. visible lists the layers in scope.Layers that id can see, in
// order. rewrite is true when scope.Layers has more than one entry, which
// tells the caller to rewrite the payload's `layer` value.
func (a *EventAudience) Visible(ctx context.Context, id layer.Identity, tenant string) (visible []string, rewrite bool, ok bool)

// TenantFor returns the tenant a request resolves against.
func (r *Registry) TenantFor(ctx context.Context) string
```

`TenantFor` is the exported form of `tenantFor` (`core.go:217`). Rename the unexported method and update its callers in the package; do not keep both.

**Evaluation order in `Visible`.**

1. `scope.TenantID == ""` or `tenant != scope.TenantID`: withheld.
2. `len(scope.Layers) == 0`: withheld.
3. `scope.Path != ""` and `!layer.ParseScopes(id.Scopes).AllowsRead(scope.Path)`: withheld. An inactive scope set allows every path.
4. `id.IsPublic`: return `scope.Layers`, `rewrite`, `true`, with no store read. The doc comment states that this arm mirrors `visibleManifests` (`core.go:2113`) and is deliberately looser than the §7.3.1 `readableBy` gate (`pkg/registry/server/layers.go:254`). Under a deployment that names only a free-form provider label, an anonymous subscriber therefore receives `layer.config_changed` for every layer, while `GET /v1/layers` returns nothing to that subscriber. OQ-2 raises this with the reviewer.
5. Resolve the layer list once per event through the single-flight described below. A resolution error withholds the event.
6. For each name in `scope.Layers`, the layer is visible when the resolved list contains it and `layer.VisibleWith(resolved, id, memoResolver)` holds, or, for `scope.Layers[0]` only, when `scope.Prior != nil` and `layer.VisibleWith(layer.Layer{ID: name, Visibility: *scope.Prior}, id, memoResolver)` holds.
7. No visible layer: withheld. Otherwise return the visible names in `scope.Layers` order.

**The layer read.** Split `resolveLayers` into `layerConfigs(ctx, tenant string) ([]layer.Layer, error)`, which reads `ListLayerConfigs` for the given tenant and composes the admin-defined layers below the user-defined ones exactly as `resolveLayers` does today, and the wrapper `resolveLayers(ctx)`, which calls `layerConfigs(ctx, r.tenantFor(ctx))` and returns `r.layers` on an error or an empty list. The event path calls `layerConfigs(ctx, scope.TenantID)`. On an error it withholds. On an empty list it uses `r.layers`, so the two surfaces resolve the same list whenever the read succeeds.

**The single-flight.** `EventAudience` holds `mu sync.Mutex`, `started bool`, `resolved bool`, `waiters int`, `done chan struct{}`, and the result fields (`layers map[string]layer.Layer`, `err error`), all guarded by `mu`. `NewEventAudience` allocates `done`; `resolved` and `started` start false. Every caller of `Visible` that reaches step 5 runs this loop:

1. Lock `mu`. If `resolved` is true, read `layers` and `err`, unlock, and continue at step 6 (a cached `err` withholds).
2. If `started` is false, set it, unlock, and become the evaluator (below).
3. Otherwise capture `done := a.done` and increment `waiters` while `mu` is held, unlock, and wait on `select { case <-done: case <-ctx.Done(): return nil, false, false }`. When the select returns, decrement `waiters` under `mu`; when `done` closed, return to step 1. `waiters` exists so TEST-1 case 14 can observe a waiting caller. A waiter never reads `a.done` after unlocking, so it cannot wait on a channel that replaced the one it saw.

The evaluator runs `layerConfigs` under its own `ctx` with `mu` released. No request-path store read in `pkg/registry/core` or `pkg/registry/server` carries a timeout today (the only `context.WithTimeout` uses are the readiness probe and the read-only probe), so the evaluating subscriber's request context is the bound, and the comment says so. When the read returns, the evaluator locks `mu` once and, in that single critical section, does one of two things:

- The read failed and the evaluator's `ctx.Err() != nil`: set `started` to false, keep `old := a.done`, store a fresh channel in `a.done`, close `old`, unlock, and return withheld for itself only. Every waiter wakes on `old`, returns to step 1, and the first to lock `mu` becomes the next evaluator. Cacheability is decided from the evaluating context, never by matching the store error.
- Otherwise: store `layers` and `err`, set `resolved` to true, close `a.done`, and unlock. A cached error withholds the event for every subscriber.

No caller holds `mu` across I/O. `resolved` is set only in the second branch and never cleared; `started` is set in step 2 and cleared only in the first branch.

**The group memo.** `memoResolver` wraps `r.resolveGroup` in a mutex-guarded `map[string][]string` held on the audience. `layer.GroupResolver` takes no context, and the serverboot SCIM resolver already reports an error as nil members, which denies (`internal/serverboot/serverboot.go:1195-1204`), so caching nil for the life of the event is fail-closed. The comment states this. A nil `r.resolveGroup` passes through as nil, which `VisibleWith` treats as the JWT-only path. The memo never moves to the `Registry` or the stream, so each event re-reads SCIM membership, and TEST-1 case 15 pins this.

**Exports in use.** `VisibilityOf` (`core.go:450`) is already exported and is what CODE-3 uses to build `Prior`.

Annotate the file and each exported identifier with `// Spec: §7.6` and, on the evaluator, `// Spec: §4.6, §6.3.1`.

### CODE-2. `ingest`: `EventEmitter` carries the scope

`pkg/registry/ingest/ingest.go` (`EventEmitter` at :315-319; publish sites at :488-492 and :840-852) and `pkg/registry/ingest/orchestrator.go` (:129-134, :172-181).

```go
// EventEmitter is the §7.6 publish surface. Server.PublishEvent satisfies it.
// The scope names the tenant and the layers the event concerns, which the
// change-event stream evaluates per subscriber at delivery.
type EventEmitter func(ctx context.Context, scope core.EventScope, eventType string, data map[string]any)
```

The publish sites pass:

| Event | Scope |
|:--|:--|
| `domain.published` | `core.EventScope{TenantID: dr.TenantID, Layers: []string{dr.Layer}, Path: dr.Path}` |
| `artifact.published`, `artifact.deprecated` | `core.EventScope{TenantID: mr.TenantID, Layers: []string{mr.Layer}, Path: mr.ArtifactID}` |
| `layer.history_rewritten`, `layer.ingested` | `core.EventScope{TenantID: cfg.TenantID, Layers: []string{cfg.ID}}` |

The payload maps are unchanged. Every test that constructs an `EventEmitter` closure (`pkg/registry/ingest/events_test.go`, `concurrent_test.go`, `source_confinement_test.go`) is updated to the new signature. `events_test.go` records the scope beside the event type and asserts the three scope rows above, with `// Spec: §7.6`.

### CODE-3. Layer endpoint: `layer.config_changed` with its scope, prior visibility, and reorder list

`pkg/registry/server/layers.go` (`publishEvent` field at :80-85; `WithEventPublisher` at :387-394; `emitLayerEvent` at :416-437; `publishConfigChanged` at :529-540; reorder at :1563-1566).

- The `publishEvent` field and `WithEventPublisher` take `ingest.EventEmitter`.
- `publishConfigChanged(ctx, scope core.EventScope, layerField, action string)` publishes the unchanged `{layer, action}` payload.
- In `emitLayerEvent`, build the scope as `core.EventScope{TenantID: e.tenantID, Layers: []string{cfg.ID}}` and set `Prior`:
  - action `unregister`: `v := core.VisibilityOf(cfg); Prior = &v`, because `cfg` is the pre-delete record (`layers.go:1471`, `:1491`).
  - update, when `before.ID != ""`: `v := core.VisibilityOf(before); Prior = &v`.
  - register and restore: `nil`.
- The `wakesWatchers` gate is unchanged.
- In reorder, publish with `core.EventScope{TenantID: e.tenantID, Layers: req.Order}` and `layerField` `strings.Join(req.Order, ",")`. The audit event at :1564-1565 is unchanged. Add a `// Spec: §7.6` citation.

`pkg/registry/server/layer_event_publish_test.go` and `layer_update_emission_test.go` update `eventRecorder.publish` to the new signature and record the scope. `layer_event_publish_test.go` asserts, with `// Spec: §7.6`: an unregister carries `Prior` equal to the deleted record's visibility; an update that changes `users` carries `Prior` equal to the record before the update; a register carries a nil `Prior`; and a reorder carries `Layers` equal to the request order.

### CODE-4. Server: delivery-time filtering in `handleEvents`

`pkg/registry/server/events.go` (`registryEvent` at :49-55; `handleEvents` at :118-175; `PublishEvent` at :192-218).

- `registryEvent` gains the unexported field `audience *core.EventAudience`. It carries no JSON tag, because `encoding/json` skips unexported fields.
- `PublishEvent(ctx context.Context, scope core.EventScope, eventType string, data map[string]any)` sets `audience: s.core.NewEventAudience(scope)` on the bus event. The webhook goroutine is unchanged and ignores the scope. The doc comment states that the payload map is shared by the bus subscribers and the webhook goroutine, so a per-subscriber rewrite copies it first and never mutates it.
- `handleEvents` resolves `id := s.identity(r)` and `tenant := s.core.TenantFor(r.Context())` before it subscribes. In the `case ev, ok := <-sub.ch` arm, after the `ok` check, it calls `ev, deliver := s.deliverable(r.Context(), ev, id, tenant)` and continues the loop when `deliver` is false. The heartbeat arm is unchanged.
- `func (s *Server) deliverable(ctx context.Context, ev registryEvent, id layer.Identity, tenant string) (registryEvent, bool)`: a nil `ev.audience` returns false. Otherwise it calls `ev.audience.Visible(ctx, id, tenant)`. When `ok` is false it returns false. When `rewrite` is true it copies `ev.Data` into a new map, sets `layer` to `strings.Join(visible, ",")`, and returns the event carrying the copy. The comment cites `// Spec: §7.6` and states that `r.Context()` is what keeps a cancelled subscriber's failed read out of the memo.

Tests that call `PublishEvent` are updated in the same commit:

- `pkg/registry/server/events_test.go` (:53, :108) passes `core.EventScope{TenantID: "t", Layers: []string{"L"}}`, matching the `core.New(st, "t", nil)` it builds (:27, :90).
- `pkg/registry/server/options_test.go` (:45) passes `core.EventScope{TenantID: "default", Layers: []string{"L"}}`, matching the `core.New(st, "default", nil)` it builds (:42).
- Under the default `IsPublic` resolver the event is admitted in both files.
- `test/e2e/http_api_test.go` `TestHTTPAPI_EventsTypeFilter` publishes `artifact.published` with `core.EventScope{TenantID: "default", Layers: []string{"L"}, Path: "finance/run"}` and `layer.ingested` with `core.EventScope{TenantID: "default", Layers: []string{"team-finance"}}`, matching `apiInProcCore` (:119-128). The type-filter assertion still holds. `TestHTTPAPI_OutboundWebhook` (:1106) passes a scope argument.
- `pkg/registry/server/webhooks_test.go`, `webhook_actor_test.go`, `webhook_routing_test.go`, and `webhook_debounce_integration_test.go` pass a scope argument. Their receiver assertions do not depend on it.
- `test/e2e/plugin_spi_test.go` (:550, :586, :600) passes the `srv.PublishEvent` method value and compiles unchanged.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Subscriber cannot see the layer an event names | Event withheld; nothing on the stream marks it | §7.6 "only where ... a layer recorded for the event visible" and "A withheld event leaves no trace" (SPEC-1); `docs/reference/http-api.md` Events stream (DOC-1) |
| Visibility changes between publish and delivery | The visibility the event's shared read observes governs its delivery to every subscriber; a change after the event's shared read governs the next event (accepted, the cost of the shared memo) | §7.6 "evaluates each event no earlier than the event's first delivery to a subscriber" (SPEC-1); DOC-1 |
| Admin withdraws a layer from a subscriber | The withdrawing `layer.config_changed` reaches the subscriber; later events for the layer do not | §7.6 prior-visibility sentence (SPEC-1); DOC-1; `docs/consuming/configure-your-harness.md` (DOC-2) |
| Admin unregisters a layer the subscriber could see | The `layer.config_changed` reaches the prior audience | §7.6 (SPEC-1); DOC-1 |
| Register or restore of a layer the subscriber cannot see | Withheld | §7.6 (SPEC-1); DOC-1 |
| Reorder naming hidden and visible layers | Delivered with `layer` naming the visible subset in order | §7.6 reorder sentence (SPEC-1); DOC-1 |
| Reorder naming only hidden layers | Withheld | §7.6 "at least one reordered layer" (SPEC-1); DOC-1 |
| Admin subscriber after a reorder | `layer` names the admin's own §4.6 subset, which can differ from the reorder response (accepted while OQ-2 is open) | §7.6 (SPEC-1) and the OQ-2 rider; CL-1 |
| Path-scoped token and an artifact or domain event outside the scope | Withheld | §7.6 "§6.3.1 path scopes must also permit" (SPEC-1); DOC-1, DOC-2 |
| Path-scoped token and a `layer.*` event | Not narrowed by path scopes; layer visibility alone governs | §7.6 names the scope gate on artifact and domain events only (SPEC-1); DOC-1 |
| `ListLayerConfigs` fails | Withheld from every subscriber of that event; a watcher misses that trigger until the next event (accepted) | §7.6 "an event whose tenant's layer list cannot be read" (SPEC-1); DOC-1 (sentence staged in DOC-1) |
| Store list empty and the boot-time list does not contain the layer | Withheld | §7.6 "An event the registry cannot evaluate is withheld" (SPEC-1); DOC-1 |
| Layer deleted between publish and delivery, no `Prior` | Withheld | §7.6 (SPEC-1); DOC-1 |
| Event published with no layer or no tenant | Withheld; no such event is published today | §7.6 (SPEC-1); DOC-1 |
| Event of another tenant | Withheld | §7.6 "the event's recorded tenant is the subscriber's tenant" (SPEC-1); DOC-1 (sentence staged in DOC-1) |
| Unrouted subscriber on a multi-tenant registry | Receives no event, because no event names its tenant (accepted; TEST-3 case 3 pins it on the binary) | §7.6 tenant condition (SPEC-1); DOC-1 tenant sentence |
| Public mode or no identity provider | Every event of the tenant that the subscriber's scopes permit | §4.6 bypasses; §7.6 last sentences (SPEC-1); DOC-1 |
| `injected-session-token`, no credential | 401 before the stream opens (existing) | §6.3.2; existing behavior, no new text |
| `oidc-jwt` with no token, or `trusted-headers` with no headers | Events for `public: true` layers only | §4.6 `public: true` row; §7.6 (SPEC-1); DOC-1 |
| Free-form provider label with no verifier, anonymous subscriber | Receives `layer.config_changed` for every layer while `GET /v1/layers` returns that subscriber nothing (accepted while OQ-2 is open) | §4.6 no-identity bypass; §7.6 (SPEC-1); OQ-2 |
| Credential expires while the stream is open | Stream stays open with the identity resolved at open (existing, accepted) | §7.6 "resolves ... once, when the stream opens" (SPEC-1); DOC-1 (sentence staged in DOC-1) |
| JWT group claim changes mid-stream | Not observed until the stream reopens | §7.6 (SPEC-1); DOC-1 identity-at-open sentence |
| SCIM group membership changes mid-stream | Observed by every event whose shared read of that group follows the change | §7.6 "the group membership it reads once for that event" (SPEC-1); DOC-1 |
| Heartbeat for a subscriber who can see nothing | `_heartbeat` still arrives | §7.6 "the keepalive line reaches every subscriber" (SPEC-1); DOC-1 |
| The first evaluating subscriber's store read hangs | That subscriber's stream stalls, heartbeats included, until the read returns or its context ends; waiting subscribers leave on their own context (accepted) | CODE-1 comment; not documented, because no request-path store read in the registry carries a timeout today and the behavior matches every other read |
| The first evaluating subscriber disconnects mid-read | Its failure is not cached; a subscriber already waiting wakes, becomes the evaluator, and reads again, and a later subscriber does the same | CODE-1; not documented, internal liveness |
| Slow subscriber fills its 256-slot buffer | Events dropped (existing) | Existing behavior, no new text |
| Multi-replica deployment | Each replica filters its own in-process bus | Non-goal; existing behavior |
| Webhook receiver and a hidden-layer event | Delivered when its event filter matches | §7.3.2 sentence (SPEC-2); `docs/reference/http-api.md` Outbound webhooks (DOC-1) |
| Multi-tenant registry, receivers of different tenants | All routed tenants share one receiver pool (accepted, deferred to OQ-1) | No spec text; DOC-1(d) stages a note when OQ-1 is deferred |
| User-defined layer change | Emits `layer.user_registered`, which never reaches the stream (existing) | §7.3.1 lifecycle paragraph; existing behavior |

## Testing

**TEST-1 · unit, `pkg/registry/core/event_visibility_test.go` (new), package `core_test`.** Lands in S5. Each test func carries `// Spec: §7.6` directly above it, plus `// Spec: §4.6` or `// Spec: §6.3.1` where relevant.

- A local wrapper embeds `store.Store`, counts `ListLayerConfigs` calls, can return an injected error on every read or on its first read only, can block every read on a channel or block its first read only, and returns `ctx.Err()` when the context is done. `storetest.FaultStore` faults only tenant and operator methods (`pkg/store/storetest/faultstore.go:144-185`), so it cannot serve here. Without the `ctx.Err()` return, the memory store succeeds despite a cancelled context and case 11 passes without exercising the no-cache branch.
- A counting closure passed to `WithGroupResolver` (`pkg/registry/core/core.go:361-364`) records resolver calls, and the test can switch the member list it returns between calls.
- `pkg/registry/core/export_test.go` (new, package `core`) exports `EventAudienceWaiters(a *EventAudience) int`, which reads `waiters` under `mu`. It is the only test hook, and case 14 uses it.
- Cases, limited to branches the evaluator owns:
  1. Delegation: a `groups: [eng]` layer admits an identity in `eng` and withholds one outside it. This pair stands in for the §4.6 clause matrix, which `pkg/layer/composer_test.go` already pins.
  2. `IsPublic` on a groups layer: visible, and the wrapper records zero `ListLayerConfigs` calls.
  3. Path scope gate: scopes `[podium:read:finance/*]` withhold `Path: "eng/x"` and admit `Path: "finance/x"`. An empty `Path` skips the gate.
  4. Prior arm: `Prior` admits while the current record denies (withdrawal). A tombstoned or absent layer with `Prior` set is visible only to the prior audience. `Prior` on a reorder applies to `Layers[0]` only.
  5. Reorder subset: `[hidden, visible]` returns `[visible]` with `rewrite` true. `[hidden]` alone is withheld.
  6. Fail-closed guards: empty `Layers`, empty `TenantID`, and a `TenantID` mismatch each withhold. An absent layer with no `Prior` is withheld.
  7. Store error: the wrapper returns an injected non-context error on its first read only and succeeds afterwards. A `Visible` call with a live context is withheld. A second `Visible` call on the same audience, also with a live context, is still withheld, and the wrapper has recorded exactly one `ListLayerConfigs` call, which pins that the cached error applies to every subscriber of the event (CODE-1 second branch; the `ListLayerConfigs` fails edge row). A second audience whose injected first-read error wraps `context.Canceled` (`fmt.Errorf("store: %w", context.Canceled)`) while the evaluating context stays live gives the same result, which pins that cacheability is decided from `ctx.Err()` on the evaluating context and never by matching the error. The read-path regression guard uses the every-read error mode, goes through a public read (`SearchArtifacts` or `LoadArtifact` over the same failing store), and asserts that boot-time-layer content is still served. No unexported function is called.
  8. An empty store list falls back to the boot-time layers.
  9. Delivery-time evaluation: build the audience, change the stored visibility, then call `Visible`; the result reflects the new record.
  10. Memo bound: 50 identities, none in the layer's 3 groups. `ListLayerConfigs` is called once and the resolver once per group.
  11. Cancellation of the first caller: the first `Visible` call with a cancelled context withholds the event and caches nothing, and a second call with a live context resolves and reads the store again.
  12. Cancellation of a waiter: while the first caller's read blocks on the wrapper's channel, a second caller whose context is cancelled returns withheld promptly. The first caller then completes and a third caller reuses its result with no further read.
  13. 20 goroutines call `Visible` concurrently. A comment states that the test exists for `go test -race`.
  14. Cancellation of the evaluator with a waiter: the wrapper blocks its first read only. Caller A's read blocks. Caller B, with a live context, calls `Visible` in a goroutine, and the test polls `core.EventAudienceWaiters` under a bounded deadline until it reports 1, which confirms B is in the step-3 select, and asserts that B has not returned. Only then cancel A's context so the wrapper returns `ctx.Err()`. A returns withheld, and B returns visible within a bounded deadline after exactly one more `ListLayerConfigs` read, which shows B took over as evaluator and did not wait on the replacement channel. Run under `go test -race`.
  15. Per-event group re-read: a `groups: [eng]` layer, an identity whose JWT `Groups` omits `eng` so `layer.VisibleWith` reaches the resolver (`pkg/layer/composer.go:75-88`), and the counting resolver from the bullet above. With the resolver answering that the identity is a member, audience A's `Visible` admits. Switch the resolver to answer non-member and build audience B for a second event with the same scope: B's `Visible` withholds, and the resolver call count has risen. Switch it back and build audience C: C's `Visible` admits. This pins that the group memo lives on the audience and no SCIM answer is cached across events, on both the admit and the withhold side (the SCIM edge row). Carry `// Spec: §4.6` beside `// Spec: §7.6`.
- Coverage: confirm 85% of `event_visibility.go` and the `layerConfigs` split with `go test -coverpkg=./pkg/registry/core/... -coverprofile=cover.out ./pkg/registry/core/... ./pkg/registry/server/...` and `go tool cover -func=cover.out`.

**TEST-2 · integration, `pkg/registry/server/events_test.go`.** Lands in S6. Build the server with `server.New` over `core.New(<memory store wrapped to count ListLayerConfigs calls on entry and, when the test arms it, to block each call on a channel the test holds>, "t", nil)`, with `WithIdentityResolver` mapping a test header to a `layer.Identity`, and with `SetHeartbeatForTesting`. Put `// Spec: §7.6` above each test func.

- (a) alice is in group `eng` and bob is not. Publish a `groups: [eng]` event, then a public-layer event, both with scope `TenantID` `"t"`. alice receives both. bob's first non-heartbeat event is the public-layer one, which shows the withheld event left no trace.
- (b) A reorder with scope `Layers: [eng, pub]`: bob's `layer` is `"pub"` and alice's is `"eng,pub"`. The test also asserts the map passed to `PublishEvent` still reads `"eng,pub"` after both deliveries.
- (c) An identity with scopes `[podium:read:finance/*]` does not receive `artifact.published` with `Path: "eng/x"` and does receive it with `Path: "finance/x"`.
- (d) Multi-tenant: the resolver returns `IsAuthenticated: true` and `OrgID: "orgB"`, and a `WithTenantRouter` stub maps `orgB` to tenant `"B"`. This subscriber does not receive an event whose scope `TenantID` is `"t"`, and does receive one whose scope `TenantID` is `"B"`. `withTenantRouting` routes only an authenticated identity with a non-empty `OrgID` (`server.go:469-473`), so both fields are required.
- (e) The default `IsPublic` resolver, and separately `WithPublicMode`, receive the groups-layer event, and the counting store records zero reads.
- (f) Arm the wrapper's block. With two streams already open for identities in group `eng` (alice and dave), publish the `groups: [eng]` event. `PublishEvent` returns within a bounded deadline while the read is still blocked, which pins that the publish path does not wait on a store read. Release the channel. After both streams have received the event, the entry count is exactly 1, which pins the shared memo. The test asserts no count at the moment `PublishEvent` returns, because an open subscriber's goroutine may already have entered the read by then.
- (g) A webhook receiver at an `httptest` target, registered with an admin identity, receives the groups-layer event that bob's stream withheld. Cite `// Spec: §7.3.2` above this test.

**TEST-3 · e2e, `test/e2e/events_visibility_test.go` (new).** Lands in S7. Add `openEventStreamAs(t testing.TB, srv *serverProc, headers http.Header, eventTypes ...string) *eventStreamClient` beside `openEventStream` in `test/e2e/notification_sink_helpers_test.go`, and have `openEventStream` call it with nil headers. Boot a trusted-headers standalone server modeled on `gwTrustedHeadersServer` (`test/e2e/auth_gateway_test.go:53`), add `PODIUM_BOOTSTRAP_ADMINS=carol@acme.com`, and return the `eng-layer` root so the test can add an artifact. Put `// Spec: §7.6` above each test, `// Spec: §7.5.4` above the withdrawal test, and `// Spec: §6.3.1` and `// Spec: §4.7.1` above the multi-tenant routing test. Cases 1 and 2 use the standalone fixture, and case 3 boots its own standard-stack server with no bootstrap admin.

1. `TestEventStream_LayerVisibilityPerCaller` covers the wiring of identity, tenant, and publisher scope. Open streams as alice (`engineering`), as bob, and with no headers. carol adds an artifact under the `eng-layer` root and reingests `eng-layer`, and alice receives `artifact.published` and `layer.ingested` naming `eng-layer`. carol then reingests `public-layer`. The first event on bob's stream and on the anonymous stream is the `public-layer` `layer.ingested`, and neither stream ever carries an `eng-layer` event.
2. `TestEventStream_VisibilityChangeMidStream` covers delivery-time evaluation and the prior arm. With bob's stream held open, carol sends `PUT /v1/layers/update?id=eng-layer` with `{"users":["bob@acme.com"]}`. bob receives that `layer.config_changed` and the `eng-layer` `layer.ingested` from the next reingest. carol then sends `{"users":[]}`. bob still receives the withdrawing `layer.config_changed`, a following `eng-layer` reingest does not reach him, and the `public-layer` reingest after it is his next event.
3. `TestEventStream_MultiTenantRouting` covers the multi-tenant tenant wiring that the in-process suites cannot reach. §13.10 keeps multi-tenancy out of standalone (`spec/13-deployment.md:217`), so the case calls `msSkipIfNoStack` (`test/e2e/standard_stack_parity_test.go:110-125`) and boots with `msStartStandardServerEnv` (`:150`). The extra environment it appends overrides `msStandardEnv` (`:185-224`) under `exec.Cmd`'s last-value-wins rule for duplicate keys: `PODIUM_IDENTITY_PROVIDER=trusted-headers`, `PODIUM_MULTI_TENANT=true`, `PODIUM_TRUSTED_PROXY_SECRET=<secret>` (required on a multi-tenant trusted-headers registry, `pkg/registry/server/config_validate.go:158-163`). Every request below carries `X-Podium-Proxy-Secret` (`pkg/identity/trusted_headers.go:40`) and the same subject in `X-Podium-User-Sub` (`:29`), written here as carol.
   - **Streams.** Open three streams as carol, so layer visibility admits the case's layer on all three and only the tenant condition separates them. The routed stream carries `X-Podium-User-Org: default`, which `tenantResolver` resolves through `orgIDForName` to the bootstrap tenant (`internal/serverboot/orgid.go:23-38`, `:43`, `:64-71`). The unprovisioned-org stream carries `X-Podium-User-Org: globex`, which trusted-headers leaves on the bound tenant without rejecting it (`internal/serverboot/serverboot.go:1450-1455`, `pkg/registry/server/server.go:469-482`). The no-org stream carries no org header, which `withTenantRouting` never routes (`server.go:470`).
   - **The event.** The case produces one event of the bootstrap tenant that names a layer carol can see, and it asserts that the producing request succeeded before it reads any stream. The routed stream receives an event naming that layer. After it has, `waitForEvent` on the unprovisioned-org and no-org streams for any event type that names the layer returns false within a bounded window, which pins that an unrouted subscriber receives no event of the bootstrap tenant even for a layer its identity can see.
   **IMPLEMENTOR'S CHOICE:** how the case produces the bootstrap-tenant event on the multi-tenant standard-stack binary — (1) the producing request passes on a multi-tenant binary, where no admin layer write and no file-transport repository is available (see the multi-tenant Watch-out bullet); (2) the case asserts that the producing request succeeded before it reads any stream, so a production failure fails fast rather than timing out; (3) the layer ID and every identity are unique per run on the shared database; (4) the case skips through `msSkipIfNoStack` and when an external tool it needs is missing; (5) all three streams use the one subject that can see the layer, so only the tenant condition separates them.
   **IMPLEMENTOR'S CHOICE:** the window length — at least as long as the bound the routed stream waits on.

The reorder rewrite and the unfiltered receiver are pinned in-process by TEST-2, and the webhook code does not change. `TestLayerUpdate_RotationWakesNoWatcher` and the anonymous streams in `test/e2e/notification_sink_primitive_test.go` are the no-identity bypass regression guards; name them in the TEST-3 file's header comment. Unregister goes through the same prior-snapshot path as test 2's withdrawal, and TEST-1 covers its tombstone lookup. Run `go test ./test/e2e/ -run 'TestEventStream_' -v` and grep the output for `SKIP` before claiming verification. Case 3 skips without `PODIUM_POSTGRES_DSN` and `PODIUM_S3_BUCKET`, and it runs in the `Test with coverage` step of `.github/workflows/test.yml:106-113`, which sets both. Measure the subprocess coverage with `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...` as `.claude/rules/test-coverage.md` describes.

## Manual validation

**MV-1.** Add scenario S83 to `test/manual-validation.md` after S82, the last scenario in the current tree (`test/manual-validation.md:10105`), and add the index row after the S82 row (`test/manual-validation.md:233`). S82 is the Python SDK materialize harness rejection that proposal 0044 staged and commit e61ae37e landed. Drafts 0040 and 0043 also stage a new scenario under S82. If another proposal takes S83 before this one is implemented, use the next free S-number at apply time, append the scenario after the last scenario and the index row after the last index row, and use that one number in the index row, the scenario heading, checklist step S10, and the Summary bullet that names the scenario.

```markdown
| S83 | The change-event stream withholds events from layers the caller cannot see | standalone | none | none | none |
```

Lands in S10. The scenario text:

~~~~markdown
## S83: The change-event stream withholds events from layers the caller cannot see

**Goal.** Validate that `GET /v1/events` delivers each event only to callers
who can see the layer it names, that a visibility grant and a withdrawal take
effect on an open stream without reconnecting, that the withdrawing event still
reaches the caller who lost the layer, and that `podium sync --watch` removes
what it materialized from that layer.

**Covers.** The §7.6 change-event stream visibility rule and the §7.5.4 watcher
through the compiled binary. This is an operator check through the compiled
binary and the `podium sync` watcher, which the trusted-headers end-to-end test
cannot drive.

**Why by hand.** An operator reads the raw NDJSON stream of two callers side by
side and the files a watcher writes and deletes. A stream that carries an
`eng-internal` line for bob, or a watcher that keeps the `eng-internal` files
after the withdrawal, is the defect this scenario catches.

**Steps.**

1. Run the isolation block. Set up the S12 registry (layers `public-handbook`
   with `public: true` and `eng-internal` with `groups: [engineering]`), the
   runtime key, and the SCIM provisioning exactly as S12 steps 2 and 3 do, with
   `PODIUM_BOOTSTRAP_ADMINS=carol@acme.com` exported before `podium serve`.
   Mint three tokens.

   ```bash
   ALICE=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com --groups engineering)
   BOB=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub bob@acme.com --email bob@acme.com)
   CAROL=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub carol@acme.com --email carol@acme.com)
   printf '%s' "$BOB" > "$WORK/bob.tok"
   ```

   **Expect.** The three tokens are non-empty, and `server_alive` reports the
   server running.

2. Open a stream as alice and as bob, each logging to a file.

   ```bash
   curl -sN -H "Authorization: Bearer $ALICE" "$PODIUM_REGISTRY/v1/events" > "$WORK/alice.ndjson" &
   ALICE_CURL=$!
   curl -sN -H "Authorization: Bearer $BOB" "$PODIUM_REGISTRY/v1/events" > "$WORK/bob.ndjson" &
   BOB_CURL=$!
   ```

   **Expect.** Both files exist and stay empty or carry only `_heartbeat`
   lines.

3. Add an artifact to `eng-internal` and reingest it as carol.

   ```bash
   podium artifact scaffold --type skill --description "Engineering rollback" --force "$WORK/eng/rollback"
   PODIUM_SESSION_TOKEN="$CAROL" podium layer reingest --registry "$PODIUM_REGISTRY" eng-internal
   sleep 2
   grep -v _heartbeat "$WORK/alice.ndjson"; echo "--- bob ---"; grep -v _heartbeat "$WORK/bob.ndjson"
   ```

   **Expect.** alice's file carries `artifact.published` and `layer.ingested`
   lines naming `eng-internal`. bob's file carries no line other than
   `_heartbeat`. An `eng-internal` line in bob's file means the stream is
   unfiltered.

4. Grant bob the layer, then reingest.

   ```bash
   curl -s -X PUT -H "Authorization: Bearer $CAROL" -H 'Content-Type: application/json' \
     "$PODIUM_REGISTRY/v1/layers/update?id=eng-internal" -d '{"users":["bob@acme.com"]}'
   PODIUM_SESSION_TOKEN="$CAROL" podium layer reingest --registry "$PODIUM_REGISTRY" eng-internal
   sleep 2
   grep -v _heartbeat "$WORK/bob.ndjson"
   ```

   **Expect.** bob's file now carries a `layer.config_changed` line naming
   `eng-internal` with action `update`, followed by the `eng-internal`
   `layer.ingested` line. bob's `curl` was not restarted.

5. Start a watcher as bob, then withdraw the grant.

   ```bash
   mkdir -p "$WORK/bob-target"
   PODIUM_SESSION_TOKEN_FILE="$WORK/bob.tok" podium sync --watch --registry "$PODIUM_REGISTRY" --target "$WORK/bob-target" > "$WORK/watch.log" 2>&1 &
   WATCH=$!
   sleep 3; find "$WORK/bob-target" -path '*rollback*' -o -path '*deploy*'
   curl -s -X PUT -H "Authorization: Bearer $CAROL" -H 'Content-Type: application/json' \
     "$PODIUM_REGISTRY/v1/layers/update?id=eng-internal" -d '{"users":[]}'
   sleep 3
   PODIUM_SESSION_TOKEN="$CAROL" podium layer reingest --registry "$PODIUM_REGISTRY" eng-internal
   sleep 2
   tail -n 5 "$WORK/bob.ndjson"; find "$WORK/bob-target" -path '*rollback*' -o -path '*deploy*'
   ```

   **Expect.** Before the withdrawal, the `find` lists the `eng-internal`
   artifacts in bob's target. After it, bob's file carries a second
   `layer.config_changed` line for `eng-internal`, and no `eng-internal` line
   follows it although carol reingested the layer. The second `find` prints
   nothing. A missing withdrawal line, or `eng-internal` files left in the
   target, is the defect.

6. Optional: reorder with bob outside `eng-internal`.

   ```bash
   curl -s -X POST -H "Authorization: Bearer $CAROL" -H 'Content-Type: application/json' \
     "$PODIUM_REGISTRY/v1/layers/reorder" -d '{"order":["eng-internal","public-handbook"]}'
   sleep 2; grep reorder "$WORK/bob.ndjson" "$WORK/alice.ndjson"
   ```

   **Expect.** bob's reorder line reads `"layer":"public-handbook"`. alice's
   reads `"layer":"eng-internal,public-handbook"`. When the stored precedence
   already matches the requested order, no reorder line appears on either
   stream; swap the order and repeat.

**Cleanup.** `kill $ALICE_CURL $BOB_CURL $WATCH`, stop the server, and
`rm -rf "$WORK"`.
~~~~

## Documentation changes

**DOC-1.** `docs/reference/http-api.md`. Lands in S8.

(a) Events stream (line 594 at the time of writing). Change "omit it to receive every event." to "omit it to receive every event type." Then append to the same paragraph, after the sentence that names `client.subscribe(events)`:

> The registry delivers an event only when the caller's identity can see the layer the event names under the layer visibility rules. For each event, it reads the layer visibility once and each group's membership once, when a caller's delivery first needs them, and applies those reads to every caller of the event. The caller's path-scoped OAuth scopes also narrow artifact and domain events. A `layer.config_changed` also reaches a caller that could see the layer before the change. The event a reorder records names only the reordered layers the caller can see. An event of another tenant is withheld, and so is an event the registry cannot evaluate, for example because the layer list cannot be read. A withheld event leaves no trace on the stream, and the `_heartbeat` line reaches every caller. The registry resolves the caller's identity when the stream opens, so a credential that expires while the stream is open does not close it, and a group claim in that credential applies until the caller reconnects. A registry started in public mode or with no identity provider configured admits every layer, so a caller there receives every event its scopes permit.

(b) Outbound webhooks (line 686). After "Configure receivers per org (URL + HMAC secret).", add:

> Layer visibility does not narrow receiver delivery. A receiver receives every event its event filter matches, whatever layer the event names, and it carries no layer scope.

(c) Subscriptions (SDK) (lines 786-790). Append to the first paragraph:

> The stream applies the caller's layer visibility, as the Events stream section describes.

(d) Only when OQ-1 is resolved as a separate proposal, add after (b):

> [!NOTE]
> On a multi-tenant registry, every routed tenant shares one receiver pool: a receiver registered by one tenant's admin receives the events of every tenant.

No runnable block is added, so no `tools/doccov/manifest.yaml` entry is needed.

**DOC-2.** Lands in S8 with DOC-1.

(a) `docs/consuming/configure-your-harness.md:47`. Replace "The stream itself applies no per-caller filtering; layer visibility applies when the triggered run re-reads the caller's effective view." with:

> The registry delivers an event on the stream only when the caller's identity can see the layer the event names, and the caller's path-scoped OAuth scopes also narrow the artifact events. A `layer.config_changed` that removes a layer from the caller's view still reaches a caller that could see the layer before the change, so the triggered run deletes what it materialized from that layer. Each triggered run re-reads the caller's effective view. The Events stream section of the HTTP API reference states the full delivery rule.

Leave the diagram and its ASCII fallback unchanged; neither makes a filtering claim.

(b) `docs/consuming/custom-via-sdk.md:139`. Replace "The same events fire outbound webhooks; the subscription is the in-process equivalent for code that's already running." with:

> The subscription delivers only the events whose layer the client's identity can see, narrowed by the client's path-scoped OAuth scopes for artifact and domain events. Outbound webhooks carry the same event types to receivers that a tenant admin configures, and a receiver's event filter is the only narrowing applied to it.

**CL-1.** `CHANGELOG.md`, `## [Unreleased]`. Lands in S9.

Under `### Fixed`:

> - **The change-event stream applies layer visibility** (§4.6, §7.6): the registry delivers an event on `GET /v1/events`, and therefore through the SDK `subscribe` helpers and the `podium sync` watcher, only when the subscriber's identity can see the layer the event names. For each event, the registry reads the layer visibility once, when a subscriber's delivery first needs it, and applies that read to every subscriber of the event. Artifact and domain events are also narrowed by the subscriber's §6.3.1 path scopes, and an event published for another tenant is withheld. A subscriber that loses visibility of a layer, or whose visible layer is unregistered, still receives the `layer.config_changed` that withdraws it. A reorder's event names only the reordered layers the subscriber can see. A holder of the §4.7.2 admin role receives only the events its own §4.6 view admits. Previously every subscriber received every event, which disclosed artifact IDs, versions, domain paths, and layer names from layers the subscriber could not see. Under public mode and with no identity provider configured, the evaluator still admits every layer, so a subscriber in those modes receives every event of its tenant that its scopes permit.

Under `### Changed`:

> - `ingest.EventEmitter` and `Server.PublishEvent` take a `core.EventScope` that names the event's tenant, its layers, the artifact ID or domain path, and, on a `layer.config_changed`, the layer's visibility before the change. Every caller is updated, and no compatibility form remains.

Under `### Documentation`:

> - §7.6 states the change-event stream visibility rule, and §4.6, §7.3.1, and §7.5.4 point to it. §7.3.2 states that the receiver fan-out applies no layer visibility filter and that a receiver's event filter is the only narrowing applied to it. `docs/consuming/configure-your-harness.md` no longer states that the stream applies no per-caller filtering, and `docs/reference/http-api.md` describes the filtered stream and the receiver delivery scope.

If OQ-2 is resolved for the §7.3.1 posture, replace the `Fixed` sentence about the admin role with the posture the reviewer chose.

## Open questions

**OQ-1. Multi-tenant webhook keying.** Should this proposal also key receiver CRUD on the request's routed tenant (`core.TenantFor`) and delivery on the event's scope tenant? That closes the cross-tenant receiver pool. It also orphans every receiver persisted under `"default"` in `PODIUM_WEBHOOK_STORE_PATH` on single-tenant deployments, which no shim would recover under the pre-1.0 rules, so operators would have to re-register. The draft leaves it to a separate proposal and stages DOC-1(d) to document the shared pool meanwhile. SPEC-2 makes no tenant claim, so it holds either way.

**OQ-2. Admin posture on the stream.** The draft gives an admin subscriber only what its own §4.6 view admits, because the §4.7.2 override is an explicit, per-use audited diagnostic. The alternative mirrors the §7.3.1 layer-read rule: an admin subscriber receives every event of the tenant, at the cost of one `ListAdminGrants` read per event, shared through the memo. Under that alternative, should each delivery to an admin of an event outside its §4.6 view record `admin.visibility_override`? The SPEC-1 rider lands only if the drafted posture is confirmed.

A related sub-question: under a deployment that names only a free-form identity-provider label with no verifier, the evaluator's `IsPublic` arm admits every event for an anonymous subscriber, including `layer.config_changed` for every layer, while the §7.3.1 `readableBy` gate returns that subscriber no layers on `GET /v1/layers`. The draft accepts this, because the catalog read path (`visibleManifests`) already admits that subscriber to every artifact. Should the stream instead follow the stricter layer-list gate for `layer.config_changed`?

## Non-goals

- Keying webhook receivers by tenant in multi-tenant mode. Receivers are registered and delivered under the fixed key `s.tenant = "default"`, so in multi-tenant mode every tenant admin's receivers share one pool. Fixing it requires re-keying receivers already persisted at `PODIUM_WEBHOOK_STORE_PATH` (`internal/serverboot/serverboot.go:1220-1230`), which is a breaking change for single-tenant operators. See OQ-1.
- A per-receiver layer or visibility scope. The receiver object stays `{id, url, secret, event_filter, disabled, failure_count, last_delivery, last_failure, created_at, debounce}`.
- Re-verifying the subscriber's credential during an open stream. A token that expires mid-stream keeps the stream open with the identity resolved at subscribe time, which is existing behavior. JWT group claims are likewise fixed for the life of the stream, while SCIM-resolved groups and layer visibility are re-read per event.
- A cross-replica bus or a durable broker. The comment at `pkg/registry/server/events.go:17-19` describes a Kafka or NATS swap that does not exist. Each replica filters its own in-process bus.
- Removing the `tenant` key from the `artifact.published`, `domain.published`, `layer.ingested`, and `layer.history_rewritten` payloads. After this fix the value is the subscriber's own tenant, so it discloses nothing across tenants.
- Correcting the §7.3.2 list of webhook event types, which omits `layer.config_changed` although `Worker.Deliver` also sends it to receivers with an empty filter.
- An admin override on the stream. See OQ-2.
- A timeout on request-path store reads. No such read carries one today, and adding one is a registry-wide change.
- Any change to the SDKs, the `podium sync` watcher, the 256-slot buffer, or the heartbeat interval.
- Routing `/v1/layers*` by tenant on a multi-tenant registry, whose unrouted admin gate the multi-tenant Watch-out bullet describes, needs its own proposal.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-03, automated)

- **The reingest wiring site was cited as `pkg/registry/server/reingest.go`, which does not exist.** The Watch-out signature bullet now cites `internal/serverboot/serverboot.go:1487` for `WithEventPublisher` and `internal/serverboot/reingest.go:53` for the orchestrator's `ingest.SourceIngestOptions.PublishEvent`. The multi-tenant Watch-out bullet cites `internal/serverboot/reingest.go:53`, and checklist step S4 names only `internal/serverboot`.
- **The per-event memo contradicted the rule that visibility at delivery governs each subscriber.** The text now matches the mechanism, which keeps the read bound. SPEC-1(a) states that the registry evaluates each event no earlier than its first delivery and applies that one read of layer visibility and group membership to every subscriber. The Summary fixed decision, the "Evaluation happens at delivery" decision, the publish-to-delivery and SCIM edge rows, DOC-1(a), and CL-1 carry the same predicate.
- **TEST-2(f) asserted a zero read count while open subscribers could already be reading.** The TEST-2 counting store can now block `ListLayerConfigs` on a channel the test holds. Case (f) asserts that `PublishEvent` returns while the read is blocked, releases the channel, and asserts exactly one read after two visible subscribers receive the event. It asserts no count at the moment of return.
- **`options_test.go` builds tenant `"default"`, so the staged scope tenant `"t"` did not match.** The CODE-4 test bullet is split. `events_test.go` passes `TenantID: "t"` (`core.New` at :27, :90), and `options_test.go` passes `TenantID: "default"` (`core.New` at :42).
- **A single-flight waiter could block on the replacement `done` channel.** CODE-1 now states the waiter loop: a waiter captures `done` while it holds `mu`, waits on it and on its own context, and returns to the `mu` check when it closes. The evaluator either publishes the result and closes `done`, or resets `started`, swaps `done`, and closes the old channel, in one critical section. The new `resolved` field marks a published result. The disconnect edge row states that a waiting subscriber takes over as evaluator.
- **No test pinned the waiter retry after the evaluator is cancelled.** TEST-1 case 14 cancels the blocked evaluator while a live-context waiter waits. It asserts that the evaluator is withheld and that the waiter resolves visible after exactly one more read within a bounded deadline, under `go test -race`.
- **SPEC-1 said every stream event names its tenant, but two delivered payloads carry no tenant.** SPEC-1(a) now states that the registry records the tenant and the layers with each event, and that these need not be fields of the event's `data`. The delivery condition reads "the event's recorded tenant" and "a layer recorded for the event". The withhold clause names an event published with no layer or no tenant. The fail-closed decision (b) and (c) and the matching edge rows use the same wording.
- **Correction: the "first evaluation" predicate misstated when the shared reads happen.** The layer list read happens at the first subscriber that reaches CODE-1 step 5, and each group is resolved by the first subscriber whose `layer.VisibleWith` call reaches the resolver (`pkg/layer/composer.go:64-87` returns earlier on `IsPublic`, `public`, `organization`, or a JWT group match), so an earlier subscriber's evaluation can precede both reads. The Summary fixed decision and the "Evaluation happens at delivery" decision now state that the layer list and each group are read once per event, by the first subscriber whose evaluation needs them, and that later subscribers reuse those reads. The publish-to-delivery and SCIM edge rows key on the event's shared read, and DOC-1(a) and CL-1 state the same timing. SPEC-1 already used "no earlier than the event's first delivery" and is unchanged.
- **Correction: TEST-1 case 14 could not distinguish a waiting caller B from one that arrived after the evaluator's swap.** CODE-1 adds a `waiters` count guarded by `mu`, incremented in step 3 before unlocking and decremented when the select returns. A new `pkg/registry/core/export_test.go` exports `EventAudienceWaiters`. Case 14 polls it until B is in the step-3 select and asserts B has not returned before it cancels A. The TEST-1 wrapper bullet now lists the block-first-read-only mode.

### Pass 2 (2026-10-03, automated)

- **The multi-tenant tenant wiring the proposal said only the binary exercises had no end-to-end test.** TEST-3 gains case 3, `TestEventStream_MultiTenantRouting`. §13.10 keeps multi-tenancy out of standalone (`spec/13-deployment.md:217`), so the case boots the standard stack through `msStartStandardServerEnv` with `PODIUM_IDENTITY_PROVIDER=trusted-headers`, `PODIUM_MULTI_TENANT=true`, and `PODIUM_TRUSTED_PROXY_SECRET`, and skips through `msSkipIfNoStack` where no Postgres and S3 are configured. It asserts that a stream routed by `X-Podium-User-Org: default` receives the bootstrap tenant's `layer.ingested`, and that streams with an unprovisioned org or no org receive no event naming that layer. The multi-tenant Watch-out bullet now names TEST-3 case 3, states why TEST-2(d) cannot reproduce the `podium:unrouted` binding, and carries refreshed citations (`internal/serverboot/serverboot.go:887`, `:1106-1109`, `:1484`, `:1635`). The unrouted-subscriber edge row, the Summary test bullet, checklist step S7, the TEST-3 annotation sentence, and the TEST-3 run note name the case.
- **TEST-3 case 3 cited `msSkipIfNoStack` at the import block.** The citation pointed at `test/e2e/standard_stack_parity_test.go:51-59`, which is the file's import list. It now reads `:110-125`, where the helper is defined and skips when `PODIUM_POSTGRES_DSN` or `PODIUM_S3_BUCKET` is unset.

### Pass 3 (2026-10-03, automated)

- **No test pinned that a non-cancellation `ListLayerConfigs` error is cached and withholds the event from every subscriber.** TEST-1 case 7 now has the wrapper fail its first read only with a non-context error. Two live-context `Visible` calls on the same audience are both withheld, and the wrapper records exactly one `ListLayerConfigs` call. A second audience repeats the check with an injected error that wraps `context.Canceled` while the evaluating context stays live, which pins the Watch-out rule that cacheability is decided from `ctx.Err()`. The TEST-1 wrapper bullet lists the fail-first-read-only mode, and the read-path regression guard in case 7 names the every-read error mode it uses.

### Pass 4 (2026-10-03, automated)

- **TEST-3 case 3 relied on admin layer writes that a multi-tenant registry refuses.** The layer endpoint is mounted outside `srv.Handler()` (`internal/serverboot/serverboot.go:1506-1507`, `:1580`), so `/v1/layers*` is never tenant-routed and its admin gate checks `IsAdmin(sub, "podium:unrouted")` (`:1494-1502`; `pkg/registry/core/admin.go:25`). `PODIUM_BOOTSTRAP_ADMINS` seeds only the bootstrap tenant, and the Postgres store refuses the `podium:unrouted` org ID outright (`pkg/store/postgres.go:47`, `:58-62`), so seeding a grant there is not available either. A local-path git repository is admin-only on both the reingest and the inbound-webhook paths (`pkg/registry/server/layer_capabilities.go:92-102`). Case 3 now opens all three streams as one subject, carol, and produces the bootstrap-tenant `layer.ingested` from a user-defined git layer carol owns, served over HTTP by the new test helper `msGitHTTPServe` (`git http-backend` under `net/http/cgi`) and reingested on the `authorizeLayerWrite` owner arm. The case drops `PODIUM_BOOTSTRAP_ADMINS`, asserts the reingest's 200 before reading streams, and the IMPLEMENTOR'S CHOICE marker adds a per-run subject because of the per-owner layer cap. The multi-tenant Watch-out bullet states that the layer endpoint is unrouted and why case 3 seeds no admin, the TEST-3 intro names the helper, and a Non-goals bullet records the unrouted admin gate as a separate defect. Pass 5 note: the `msGitHTTPServe` helper and the user-defined-layer recipe were pruned in pass 5; see below.

### Pass 5 (2026-10-03, automated)

- **TEST-3 case 3 had grown into a recipe for subsystems orthogonal to the stream filter, and the unrouted admin-gate explanation appeared in three places.** Case 3 drops the "Why the event comes from a user-defined layer" paragraph, the cited register-and-reingest recipe, and the `msGitHTTPServe` helper (`git http-backend` under `net/http/cgi`). It keeps the stream setup and the assertions, and an IMPLEMENTOR'S CHOICE marker delegates how the case produces the bootstrap-tenant event under five constraints: the request passes on a multi-tenant binary, its success is asserted before any stream is read, the layer ID and identities are unique per run, the case skips without the stack or a needed tool, and all three streams use one subject that can see the layer. The window marker keeps only the window length, because per-run uniqueness moved into the new marker. The multi-tenant Watch-out bullet is now the single statement of the unrouted admin gate and carries the bootstrap-admin and file-transport citations formerly in case 3. Non-goals and case 3 point to it. The "Evaluation happens at delivery" decision drops the sentences that restated the Summary's shared-read timing and now reads "withheld at CODE-1 steps 1 to 3, or admitted at step 4", which matches the CODE-1 evaluation order, where step 4 is the `IsPublic` admit.

### Pass 6 (2026-10-03, automated)

- **No test pinned that SCIM group membership is re-read per event rather than cached across events.** TEST-1 gains case 15. A counting `WithGroupResolver` closure (`pkg/registry/core/core.go:361-364`) whose answer the test switches serves a `groups: [eng]` layer to an identity whose JWT groups omit `eng`, so `layer.VisibleWith` reaches the resolver (`pkg/layer/composer.go:75-88`). Audience A admits while the resolver answers member, audience B for a second event withholds after the switch to non-member with a higher resolver count, and audience C admits after the switch back. The TEST-1 resolver bullet states that the test can switch the returned member list, and the CODE-1 group-memo paragraph states that the memo never moves to the `Registry` or the stream and names case 15.

### Pass 7 (2026-10-03, automated)

- **MV-1 staged scenario S82, which the current tree already uses.** `test/manual-validation.md:233` and `:10105` carry S82 for the Python SDK materialize harness rejection, which proposal 0044 staged and commit e61ae37e landed, and drafts 0040 and 0043 also stage S82. MV-1 now adds scenario S83 after S82 and its index row after the S82 row. It states that if another proposal takes S83 first, the implementor uses the next free S-number, appends after the last scenario and the last index row, and keeps that one number in the index row, the scenario heading, checklist step S10, and the Summary bullet. The index row, the scenario heading, checklist step S10, and the Summary bullet now read S83.
