---
layout: default
title: HTTP API
parent: Reference
nav_order: 2
description: "The Podium registry's HTTP/JSON API: discovery, materialization, layer management, ingest webhooks, scope preview, and health."
---

# HTTP API

The Podium registry exposes an HTTP/JSON API. Every consumer (the MCP server, language SDKs, `podium sync` in server mode, the read CLI) speaks this API. Direct MCP access to the registry is not supported; the MCP server is a consumer surface that translates HTTP responses into MCP messages.

---

## Authentication

Every call carries an OAuth-attested identity. The registry validates the JWT signature, reads claims (`sub`, `email`, `groups`), and composes the caller's effective view.

| Header | Value |
|:--|:--|
| `Authorization` | `Bearer <jwt>` |

The JWT comes from the configured identity provider:

- `oauth-device-code`: interactive device-code flow on first use; tokens cached in the OS keychain. Refresh transparent.
- `injected-session-token`: runtime-issued signed JWT. The runtime registers its signing key with the registry one-time at runtime onboarding.

In public-mode deployments, the OAuth flow is skipped; the registry serves anonymously. The audit log records `caller.identity = "system:public"`.

---

## SLO targets (server source)

| Endpoint | p99 |
|:--|:--|
| `load_domain` | < 200 ms |
| `search_domains` | < 200 ms |
| `search_artifacts` | < 200 ms |
| `load_artifact` (manifest only) | < 500 ms |
| `load_artifact` (manifest + ≤10 MB resources, cache miss) | < 2 s |

---

## Discovery

### `load_domain`

```
GET /v1/load_domain?path={path}&depth={n}&session_id={uuid}
```

Returns the map for a path. An empty `path` returns the registry root.

Response:

```json
{
  "path": "finance",
  "description": "...",
  "keywords": ["...", "..."],
  "subdomains": [
    { "path": "finance/ap", "name": "ap", "description": "..." }
  ],
  "notable": [
    {
      "id": "finance/ap/pay-invoice",
      "type": "skill",
      "summary": "...",
      "source": "featured",
      "folded_from": "<canonical subpath; omitted when not folded>"
    }
  ],
  "note": "Notable list reduced from 10 to 4 to fit the response budget."
}
```

`note` is omitted when no reduction occurred.

Output rendering (depth, folding, notable count, response budget) is governed by the discovery rules (see [Authoring → Domains](../authoring/domains)). Caller-passed `depth` is bounded by the resolved `max_depth` ceiling.

### `search_domains`

```
GET /v1/search_domains?query={q}&scope={path}&top_k={n}&session_id={uuid}
```

Hybrid retrieval over each domain's projection (frontmatter `description`, `keywords`, and truncated body). `top_k` defaults to 10.

Ranked domains are returned under the `domains` key.

Response:

```json
{
  "query": "vendor payments",
  "total_matched": 8,
  "domains": [
    {
      "path": "finance/ap",
      "name": "ap",
      "description": "...",
      "keywords": ["...", "..."],
      "score": 0.87
    }
  ]
}
```

### `search_artifacts`

```
GET /v1/search_artifacts?query={q}&type={type}&tags={tag1},{tag2}&scope={path}&top_k={n}&session_id={uuid}
```

Hybrid retrieval over artifact frontmatter. Every argument is optional. When `query` is omitted, the endpoint returns artifacts matching the filters in default order, the browse call. `top_k` defaults to 10.

Each result's `frontmatter` is the artifact's verbatim YAML frontmatter as a string.

Response:

```json
{
  "query": "variance analysis",
  "total_matched": 47,
  "results": [
    {
      "id": "finance/close-reporting/run-variance-analysis",
      "type": "skill",
      "version": "1.2.0",
      "score": 0.83,
      "frontmatter": "name: run-variance-analysis\ntype: skill\nversion: 1.2.0\n..."
    }
  ]
}
```

---

## Materialization

### `load_artifact`

```
GET /v1/load_artifact?id={id}&version={v}&session_id={uuid}
```

`version` is optional (default `latest`). `session_id` is optional; the first `latest` lookup within a session is recorded and reused for subsequent same-id lookups in the session, so the host sees a consistent snapshot.

A `HEAD` request revalidates the consumer's resolution cache: the registry returns the resolved content hash in the `X-Podium-Content-Hash` header (and the version in `X-Podium-Version`) with no body. A `GET` that carries a matching `If-None-Match` is answered `304 Not Modified`.

Response:

```json
{
  "id": "...",
  "type": "skill",
  "version": "1.2.0",
  "content_hash": "sha256:...",
  "manifest_body": "...",
  "resources": {
    "scripts/variance.py": "...inline bytes..."
  },
  "large_resources": {
    "assets/model.bin": { "presigned_url": "...", "content_hash": "sha256:...", "size": 5242880 }
  }
}
```

A resource at or below the inline cutoff (256 KB) is returned in `resources`, a map of package-relative path to inline bytes. A larger resource is returned in `large_resources`, a map of path to a presigned URL into object storage that the consumer fetches directly; the registry does not proxy the bytes. When any inline resource is binary, the whole `resources` map is base64-encoded and `resources_base64` is `true`. A canonical manifest above the cutoff is delivered the same way, as `manifest_body_url` with the inline `manifest_body` cleared. The `load_artifacts` batch endpoint below returns each artifact's resources as an array of objects rather than these maps.

### `load_artifacts` (bulk)

```
POST /v1/artifacts:batchLoad
```

Body:

```json
{
  "ids": [
    "finance/close-reporting/run-variance-analysis",
    "finance/close-reporting/policy-doc"
  ],
  "session_id": "...",
  "harness": "claude-code",
  "version_pins": { "finance/close-reporting/policy-doc": "1.0.0" }
}
```

Response: an array of per-item envelopes. Each item has its own `status` (`ok` or `error`) and either the manifest payload or an error envelope. Hard cap: 50 IDs per batch.

```json
[
  {
    "id": "finance/close-reporting/run-variance-analysis",
    "status": "ok",
    "version": "1.2.0",
    "content_hash": "sha256:...",
    "manifest_body": "...",
    "resources": [...]
  },
  {
    "id": "finance/restricted/payroll-runner",
    "status": "error",
    "error": { "code": "visibility.denied", "message": "..." }
  }
]
```

Visibility is identical to `load_artifact`: items the caller can't see come back as `status: "error"` with `visibility.denied`. No leak about whether the artifact exists in some hidden layer.

Not exposed as an MCP meta-tool; bulk loading is a programmatic-runtime concern.

---

## Catalog and sync

### `catalog`

```
GET /v1/catalog?scope={path}
```

Returns the caller's visible artifact catalog under the `scope` prefix as a flat ID list plus a lean per-artifact descriptor (`id`, `type`, and a short `summary`), visibility-filtered server-side. No manifest body rides along. The client-side `load_domain` merge resolves a workspace-local `DOMAIN.md`'s globs over this set.

```json
{
  "ids": ["finance/ap/pay-invoice", "..."],
  "artifacts": [
    { "id": "finance/ap/pay-invoice", "type": "skill", "summary": "..." }
  ]
}
```

### `sync/manifest`

```
GET /v1/sync/manifest
```

Returns the caller's full effective view as a flat artifact list under the `artifacts` key, visibility-filtered server-side. `podium sync` in server-source mode walks this to discover which artifacts to load, then materializes each via `load_artifact`. It carries no relevance ranking and no `top_k` cap, so a sync of more than 50 artifacts enumerates in one request.

### `dependents`

```
GET /v1/dependents?id={id}
```

Returns the cross-artifact dependency edges that point at the artifact, under the `edges` key. Each edge carries `from`, `to`, and `kind`.

### `domain/analyze`

```
GET /v1/domain/analyze?path={path}
```

Returns the per-subtree domain analysis report for the path (the same report `podium domain analyze` prints).

---

## Layer management

### Register a layer

```
POST /v1/layers
```

Body:

```json
{
  "id": "team-finance",
  "source_type": "git",
  "repo": "git@github.com:acme/podium-finance.git",
  "ref": "main",
  "root": "artifacts/",
  "groups": ["acme-finance"]
}
```

`id` and `source_type` are required. Visibility is set with the top-level `public`, `organization`, `groups`, and `users` fields. The response is `201 Created` with the stored layer and, for a `git` source, the webhook URL and HMAC secret to register on the source repo:

```json
{
  "layer": { "id": "team-finance", "source_type": "git", "...": "..." },
  "webhook_url": "https://registry.acme.com/v1/ingest/webhook/team-finance",
  "webhook_secret": "..."
}
```

### List layers

```
GET /v1/layers
```

### Reingest

```
POST /v1/layers/reingest?id={id}
```

Forces a fresh snapshot of the layer regardless of the trigger model. The body is optional and carries a break-glass override during a freeze window:

```json
{ "break_glass": true, "justification": "...", "approvers": ["...", "..."] }
```

### Reorder layers

```
POST /v1/layers/reorder
```

Body: `{ "order": ["layer-a", "layer-b", "layer-c"] }`. The `order` array re-sequences the named layers. Reordering an admin-defined layer requires admin authorization.

### Update a layer

```
POST /v1/layers/update?id={id}
PUT  /v1/layers/update?id={id}
```

Patches the layer. A non-zero body field replaces the corresponding value; a zero field leaves it unchanged. The patchable fields are visibility (`public`, `organization`, `groups`, `users`), `ref`, `root`, `local_path`, `owner`, `force_push_policy`, and a webhook-secret rotation (`rotate_webhook_secret`). The identifying fields (`id`, `source_type`) are immutable.

### Unregister

```
DELETE /v1/layers?id={id}
```

Soft-deletes the layer and the artifacts ingested from it, recoverable within the retention window.

### List soft-deleted layers and restore

```
GET  /v1/layers?deleted=true
POST /v1/layers/restore?id={id}
```

`GET /v1/layers?deleted=true` lists the soft-deleted layers still inside the recovery window. `POST /v1/layers/restore?id={id}` clears the tombstone and recovers the layer and its artifacts.

---

## Ingest webhook

```
POST /v1/ingest/webhook/{layer-id}
```

Receives Git provider webhooks. The registry validates the HMAC signature against the layer's secret, fetches the new commit, walks the diff, runs lint, validates the immutability invariant, hashes content, stores manifest + bundled resources, indexes metadata, and emits the corresponding outbound event.

Webhook signature verification failures return `ingest.webhook_invalid` and are logged but never reach the content store.

---

## Scope preview

```
GET /v1/scope/preview
```

Returns aggregated metadata for the calling identity's effective view, with no manifest bodies and no resource transfers.

```json
{
  "layers": ["admin-finance", "alice-personal", "workspace-overlay"],
  "artifact_count": 1234,
  "by_type": {
    "skill": 800,
    "agent": 200,
    "context": 200,
    "command": 30,
    "rule": 4
  },
  "by_sensitivity": { "low": 1100, "medium": 100, "high": 34 }
}
```

Gated by tenant config (`tenant.expose_scope_preview`). When `false`, returns `403 config.scope_preview_disabled`. Aggregate counts can hint at the existence of restricted content even when no individual artifact is leaked, so operators decide whether to expose this surface per tenant.

---

## Quota

```
GET /v1/quota
```

Returns the calling tenant's configured limits and current usage. Read-only and not admin-gated, since quota visibility is informational.

```json
{
  "tenant_id": "acme",
  "limits": { "...": "..." },
  "usage": { "storage_bytes": 1234567 }
}
```

---

## Events stream

```
GET /v1/events?type={event}&type={event}
```

Streams change events as NDJSON (`Content-Type: application/x-ndjson`). The connection stays open until the client disconnects. Repeat `type` to filter by event name; omit it to receive every event. The handler emits a `{"event":"_heartbeat"}` line every 30 seconds so a proxy-buffered consumer sees the connection stay alive. This is the wire surface the SDK `client.subscribe(events)` helper wraps.

---

## Object bytes

```
GET  /objects/{key}
HEAD /objects/{key}
```

Serves a large resource's bytes for the filesystem object-store backend. The `presigned_url` a `load_artifact` response returns for the filesystem backend points here. The `key` is the resource's content hash. Visibility is re-checked on every fetch, so a caller who has lost access to the artifact can no longer follow a previously-issued URL. `HEAD` reports the size without streaming the body. The S3 backend returns its own presigned URLs instead and does not use this route.

---

## Admin and operations

These routes require an authenticated admin caller (resolved through the admin-grant table) and are rejected in read-only mode with `registry.read_only`.

### Admin grants

```
POST   /v1/admin/grants    body: { "user_id": "alice@acme.com" }
DELETE /v1/admin/grants?user_id={id}
```

`POST` grants the admin role to the named user and returns `201 Created`. `DELETE` revokes it and returns `204 No Content`.

### Show effective visibility

```
GET /v1/admin/show-effective?user_id={id}&group={g}
```

Returns the per-layer visibility resolved for the named target identity, under the `layers` key. Repeat `group` to evaluate the target with additional group memberships. Admin-only because the visibility configuration is itself sensitive.

### Reembed

```
POST /v1/admin/reembed?artifact={id}&version={v}&only_missing={bool}&since={rfc3339}
```

Recomputes embeddings over the tenant. With no query parameters it reembeds every artifact. `artifact` (with a required `version`) scopes the run to one artifact; `only_missing=true` limits it to artifacts without a current embedding; `since` limits it to artifacts ingested at or after an RFC 3339 timestamp.

### Runtime signing keys

```
POST /v1/admin/runtime    body: { "issuer": "...", "algorithm": "...", "public_key_pem": "..." }
GET  /v1/admin/runtime
```

Registers and lists the trusted runtime signing keys the `injected-session-token` verifier consults. `POST` returns `201 Created`. `GET` returns the registered runtimes under the `runtimes` key without echoing the key material.

### Erase a user (GDPR)

```
POST /v1/admin/erase    body: { "user_id": "...", "salt": "..." }
```

Performs the right-to-erasure operation for the named user: it unregisters and soft-deletes every user-defined layer the user owns, redacts the user identity across the registry audit stream, and appends a `user.erased` event naming the invoking admin. Both `user_id` and `salt` are required.

---

## Outbound webhooks

The registry emits outbound webhooks for change events. Configure receivers per org (URL + HMAC secret).

| Event | When |
|:--|:--|
| `artifact.published` | A new `(artifact_id, version)` was ingested. |
| `artifact.deprecated` | An ingested manifest set `deprecated: true`. |
| `domain.published` | A `DOMAIN.md` was added or changed. |
| `layer.ingested` | A layer completed an ingest cycle. Fires once per cycle, so a CI job that runs `podium sync --config` against a `kind: marketplace` target subscribes to it (see [Marketplace publishing](../consuming/publishing)). |
| `layer.history_rewritten` | Force-push detected on a `git`-source layer. |

Schema:

```json
{
  "event": "artifact.published",
  "trace_id": "...",
  "timestamp": "...",
  "actor": { "...": "..." },
  "data": { "...": "..." }
}
```

The registry signs webhook deliveries with the receiver's configured HMAC secret.

### Receiver CRUD

```
GET    /v1/webhooks            list receivers
POST   /v1/webhooks            create a receiver
GET    /v1/webhooks/{id}       read one receiver
PUT    /v1/webhooks/{id}       update one receiver
DELETE /v1/webhooks/{id}       remove one receiver
```

`POST` accepts `{ "url": "...", "secret": "...", "event_filter": ["..."], "disabled": false }` and returns `201 Created` with the receiver including its secret, so the operator can record it. The registry generates a secret when the body omits one. `url` is required. `PUT` accepts the same fields and applies the ones present; re-enabling a receiver (`disabled: false`) clears its failure counter. `GET` and `DELETE` of a single receiver address it by `id`. List and single-read responses mask the secret as `***`. `DELETE` returns `204 No Content`. These routes are mounted only when the deployment configures an outbound webhook worker.

#### Triggering GitHub CI from a receiver (repository_dispatch relay)

A receiver cannot call GitHub's `repository_dispatch` endpoint directly: the registry signs each delivery with the receiver's HMAC secret (the `secret` above), and that signed body differs from the body the GitHub dispatch endpoint accepts. So an operator relay bridges the two. Register a receiver filtered to `layer.ingested` whose `url` points at the relay; the relay verifies the HMAC against the receiver secret and then issues `POST https://api.github.com/repos/<owner>/<repo>/dispatches` with a GitHub token and `{"event_type":"podium-layer-ingested"}`. The CI workflow listens on `repository_dispatch` and runs `podium sync --config`. The receiver `debounce` field that coalesces a burst into one batch delivery, and the receiver authorization on these CRUD endpoints, are specified in [proposal 0004 (webhook hardening)](https://github.com/lennylabs/podium/blob/main/proposals/0004-webhook-hardening.md). The relay functions without them, with the CI system's concurrency control collapsing a burst. See [Marketplace publishing](../consuming/publishing) for the worked patterns.

---

## Subscriptions (SDK)

The SDKs expose `client.subscribe(events)` for in-process consumers that don't want to run their own webhook receiver. The wire surface is the `/v1/events` streaming endpoint; the SDK abstracts the connection and reconnection logic.

Useful for sync watchers, downstream rebuild triggers, and eval pipelines reacting to new artifact versions.

---

## SCIM provisioning

```
/scim/v2/
```

A SCIM 2.0 receiver the configured identity provider pushes Users and Groups to. The visibility evaluator resolves `groups:` filters against the membership this endpoint records. The route is mounted only when the deployment configures a SCIM receiver.

---

## Metrics

```
GET /metrics
```

A Prometheus scrape endpoint. Mounted only when the deployment configures a metrics registry.

---

## Health

```
GET /healthz
```

Returns `{ "mode": "ready" | "read_only" | "public" }`. The endpoint is a liveness signal: a `200` status conveys liveness, and `mode` reports the serving state (`ready` by default, `read_only` when the registry has fallen back to a read replica, `public` in public mode). The body carries no readiness boolean and no `read_only` field; read-only is signaled by the `X-Podium-Read-Only` response header.

```
GET /readyz
```

Reports readiness for load-balancer rotation. The body is `{ "mode": "ready" | "read_only" | "not_ready", "replication_lag_seconds": <n> }`. A `ready` or `read_only` mode returns `200` so the registry stays in rotation; `not_ready` (a failing dependency probe) returns `503`.

---

## Cache modes

`PODIUM_CACHE_MODE` on the consumer side controls behavior when the registry is unreachable:

| Mode | Behavior |
|:--|:--|
| `always-revalidate` | Fresh calls return `{status: "offline", served_from_cache: true}` alongside cached results; if no cache, structured error `network.registry_unreachable`. |
| `offline-first` | No error; serve cached results silently. |
| `offline-only` | Never contact the registry; structured error if cache miss. |

Hosts can surface the offline status to the agent so it can adjust behavior (e.g., warn the user about staleness).

---

## Read-only mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode. Read endpoints continue to serve from the replica; write endpoints (ingest webhooks, layer admin operations, freeze toggles, admin grants, login-driven token issuance) are rejected with `registry.read_only`.

Read responses carry two headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>`: observed replication lag.

