# Proposal 0004: Webhook Hardening

- Issue: (to be filed)
- Status: Draft
- Date: 2026-06-24

## Summary

The outbound webhook subsystem (§7.3.2) has gaps that are independent of any single consumer. Receiver registration is not authorization-gated beyond identity: the CRUD handlers at `/v1/webhooks` (`pkg/registry/server/webhooks.go`) reject writes in read-only mode but never call `requireAdmin` or `requireOperator`, and `pathRequiresIdentity` returns true for the route (`pkg/registry/server/identity_verify.go:73`) only when an identity verifier is configured, because `withIdentityVerification` is a pass-through when none is set (`identity_verify.go:40`). So any authenticated caller in a tenant can register a receiver, and on an unauthenticated standalone bind anyone who can reach the port can. A receiver makes the registry POST to an operator-supplied URL (`pkg/webhook/webhook.go:254`), so a caller who can register one receives catalog event metadata at a chosen URL and can point the registry at an internal endpoint it would not otherwise reach. Separately, the registry delivers one webhook per event (`Worker.Deliver`, `pkg/webhook/webhook.go:143`, called once per `PublishEvent`), so a burst, such as one source commit that ingests several layers and emits a `layer.ingested` per layer, fans out into many deliveries and, for a receiver that drives CI, many redundant runs.

This proposal hardens the webhook subsystem. It gates receiver CRUD behind the per-tenant admin role, adds an SSRF policy on the receiver URL, and adds a per-receiver debounce window that coalesces a burst into one batch delivery. The debounce changes the §7.3.2 delivery body for a debounced receiver, additively and scoped to receivers that opt in. Proposal 0003 (multi-harness marketplace publishing) depends on this proposal for its event-driven trigger and does not require it: 0003's scheduled pattern uses no receiver, and its event-driven pattern functions on the existing per-event delivery. `spec/` is read-only, so this proposal carries the amendments to §7.3.2, §13.12, and §6.10, as proposals 0001 and 0002 did.

## Current state and the gaps

### Receiver registration is not admin-gated

The route is registered plainly (`pkg/registry/server/server.go:380`):

```
mux.HandleFunc("/v1/webhooks", s.handleWebhooksList)
mux.HandleFunc("/v1/webhooks/", s.handleWebhookOne)
```

`handleWebhooksList` (GET list, POST create) and `handleWebhookOne` (GET, PUT, DELETE) call `rejectIfReadOnly` for the mutating methods (`webhooks.go:38`) and write receivers scoped to `s.tenant`, but neither calls `requireAdmin` (per-tenant admin, `admin.go:113`, `core.AdminAuthorize`) or `requireOperator` (instance operator, `tenants.go:88`). The admin and tenant endpoints gate inside their handlers (`admin.go:23,86`; `server.go:813,974`; `tenants.go:110`); the webhook handlers are the mutating endpoints that omit the gate. Identity is verified for the route when a verifier is configured, because `pathRequiresIdentity` exempts only `/healthz`, `/readyz`, and `/scim/` (`identity_verify.go:73`), but `withIdentityVerification` is a pass-through when no verifier is set (`identity_verify.go:40`).

A receiver makes the registry POST to `Receiver.URL` (`webhook.go:254`), so a caller who can register one receives catalog event metadata (artifact IDs, layer IDs, actor identities) at a chosen URL, and can point the registry at an internal endpoint it would not otherwise reach.

### Delivery is per-event

`Worker.Deliver` (`webhook.go:143`) fans one event out to the matching receivers, and the server calls it once per `PublishEvent`. The ingest path emits `artifact.published` per artifact (`pkg/registry/ingest/ingest.go:790`) and `layer.ingested` once per completed layer cycle (`pkg/registry/ingest/orchestrator.go:172`). A consumer that wants one signal per source update receives one delivery per artifact on the `artifact.published` stream, or one per layer when several layers ingest together on the `layer.ingested` stream. A receiver that drives CI turns each delivery into a run.

## Proposed solution

### Receiver authorization

Gate the receiver CRUD endpoints behind the per-tenant admin role. `GET`, `POST`, `PUT`, and `DELETE` on `/v1/webhooks` call `requireAdmin` first, returning `auth.forbidden` (§6.10) for a non-admin caller, and the mutating methods then call `rejectIfReadOnly` as today, mirroring `handleAdminGrants` (`admin.go:23`). This matches the §7.3.2 framing that receivers are an org-level configuration, and closes the gap where the webhook endpoints were the mutating endpoints without an admin gate. Receiver management inherits the standalone and no-auth posture of the other admin endpoints: where there is no identity, it follows the same path as admin grants rather than remaining open.

### SSRF policy on the receiver URL

Validate `Receiver.URL` at registration and re-check at delivery, because the registry originates the request. By default the registry requires the `https` scheme and rejects a URL that resolves to a loopback, link-local, or private address (for example `127.0.0.0/8`, `::1`, `169.254.0.0/16`, and the RFC 1918 ranges), and it does not follow a redirect to such a target. A deployment whose receiver is legitimately internal, such as an in-cluster relay, sets an allowlist of permitted hosts or CIDRs through registry config (`PODIUM_WEBHOOK_ALLOWED_TARGETS`), which the validation consults. A rejected target returns `registry.invalid_argument` with a message naming the disallowed host.

### Per-receiver debounce window

Add a `Debounce` duration to the `Receiver` struct (`webhook.go:49`) and a `debounce` field to the `POST /v1/webhooks` and `PUT /v1/webhooks/{id}` body (`webhooks.go:41`). The default is unset, which preserves per-event delivery. A receiver with `debounce` set holds the events it matches in a trailing window that opens on the first matched event, deduplicates them by event type and key (the key is the artifact ID for `artifact.published` and `artifact.deprecated`, the layer ID for `layer.ingested` and `layer.history_rewritten`, and the domain path for `domain.published`), and on window expiry delivers them as one batch through `Worker.Deliver`. The retry, backoff, and `MaxConcurrent` behavior of the worker (`webhook.go:89`) applies to the batch delivery unchanged. The window applies to every event type the receiver matches through its `EventFilter` (`webhook.go:64`), so an operator enables debounce on the receiver that drives CI and leaves the receivers that feed chat or dashboards on per-event delivery.

The debounce buffer is in-process. A registry restart mid-window may drop a buffered batch, which is consistent with the at-least-once, best-effort delivery the subsystem already provides through retries. A CI receiver re-renders the full catalog on its next trigger, so a dropped batch is recovered by the next event or a periodic run. Persisting the buffer is an open question.

### Batch delivery body

For a debounced receiver, the delivery body is a batch envelope:

```json
{
  "event": "batch",
  "trace_id": "...",
  "timestamp": "...",
  "window": { "start": "...", "end": "..." },
  "count": 12,
  "events": [
    { "event": "layer.ingested", "timestamp": "...", "actor": {}, "data": { "layer": "team-shared" } }
  ]
}
```

Each element of `events` is the single-event body the receiver would otherwise have received (`webhook.go:159`). The single-event body is unchanged for a receiver without a debounce window, so the batch body is additive and scoped to receivers that opt in. The registry signs the batch body with the receiver secret through the existing `SignBody` (`webhook.go:280`), so the HMAC contract is unchanged. A receiver that only needs a trigger signal reads `count` or the event types and ignores the rest.

## Spec amendment: §7.3.2 receiver authorization and SSRF

Amend `spec/07-external-integration.md` §7.3.2 to state that the receiver CRUD endpoints (`GET`, `POST`, `PUT`, and `DELETE /v1/webhooks`) require the per-tenant admin role and return `auth.forbidden` for a non-admin caller, alongside the existing read-only rejection for the mutating methods. Add the SSRF policy: the registry requires `https`, rejects loopback, link-local, and private targets by default, does not follow redirects to such targets, and consults the `PODIUM_WEBHOOK_ALLOWED_TARGETS` allowlist for deployments with internal receivers.

## Spec amendment: §7.3.2 debounce window and batch body

Amend §7.3.2 to add the per-receiver `debounce` window to the receiver configuration and to specify the batch delivery body above. State that the window deduplicates by event type and key, that the key is defined per event type, that the batch body is additive and scoped to debounced receivers, and that the single-event body is unchanged for receivers without a window. Note that the buffer is in-process and a restart may drop a buffered batch, consistent with best-effort delivery.

## Spec amendment: §13.12 config

Add `PODIUM_WEBHOOK_ALLOWED_TARGETS` to the §13.12 variable table: a comma-separated allowlist of hosts or CIDRs that the receiver-URL SSRF policy permits in addition to public addresses, empty by default.

## Spec amendment: §6.10 error codes

The receiver endpoints reuse `auth.forbidden` for a non-admin caller and `registry.invalid_argument` for a rejected receiver URL, both in existing namespaces. No new code is introduced.

## Relationship to proposal 0003

Proposal 0003's event-driven publishing pattern registers a receiver and benefits from both parts of this proposal: the authorization closes the open registration surface, and the debounce window coalesces a burst of `layer.ingested` into one CI dispatch. Proposal 0003 does not depend on this proposal to function. Its scheduled pattern uses no receiver, and its event-driven pattern works on the existing per-event delivery, where the CI system's concurrency control collapses redundant runs. This proposal is independently useful to every webhook consumer, including chat, dashboards, and ticket trackers.

## Open questions

1. **Payload form.** The batch body preserves every coalesced event under `events`. An alternative is a summary-only body (counts by event type and the affected layers) without the per-event array, which is smaller and sufficient for a trigger but loses itemization for other receivers. A third option is lossy last-only delivery, which keeps the single-event body and drops the rest, avoiding any schema change at the cost of dropping events. The batch body is proposed.
2. **Buffer durability.** The debounce buffer is in-process and a restart may drop a buffered batch. Whether to persist the buffer, given the best-effort delivery model and the recovery by a later event, is open.
3. **Dedup key per event type.** The proposed keys (artifact ID, layer ID, domain path) need confirmation against every event type the subsystem emits.
4. **GET gating.** `GET /v1/webhooks` reveals receiver URLs and masked secrets. Gating it behind admin alongside the mutating methods is proposed; whether a narrower read role is wanted is open.
5. **SSRF default strictness.** Requiring `https` and blocking private targets by default is proposed. Whether a deployment needs a looser default, rather than the allowlist override, is open.
