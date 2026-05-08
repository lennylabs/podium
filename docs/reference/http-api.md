---
layout: default
title: HTTP API
parent: Reference
nav_order: 2
description: The Podium registry's wire surface: discovery endpoints, materialization, ingest webhooks, scope preview, health.
---

# HTTP API

The Podium registry exposes an HTTP/JSON API. Every consumer (the MCP server, language SDKs, `podium sync` in server mode, the read CLI) speaks this API. Direct MCP access to the registry is not supported; the MCP server is a consumer surface that translates HTTP responses into MCP messages.

This page covers the public surface. For the authoritative wire-level detail, see [`spec/07-external-integration.md`](https://github.com/lennylabs/podium/blob/main/spec/07-external-integration.md).

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
GET /v1/domains/{path}?depth={n}&session_id={uuid}
```

Returns the map for a path. Empty path returns the registry root.

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
GET /v1/domains/search?query={q}&scope={path}&top_k={n}&session_id={uuid}
```

Hybrid retrieval over each domain's projection (frontmatter `description` + `keywords` + truncated body). `top_k` defaults to 10, max 50.

Response:

```json
{
  "query": "vendor payments",
  "total_matched": 8,
  "results": [
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
GET /v1/artifacts/search?query={q}&type={type}&tags={tag1},{tag2}&scope={path}&top_k={n}&session_id={uuid}
```

Hybrid retrieval over artifact frontmatter. All args optional. When `query` is omitted, returns artifacts matching the filters in default order: the canonical "browse" call.

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
      "frontmatter": { "...": "..." }
    }
  ]
}
```

---

## Materialization

### `load_artifact`

```
POST /v1/artifacts/load
```

Body:

```json
{
  "id": "finance/close-reporting/run-variance-analysis",
  "version": "1.2.0",
  "session_id": "...",
  "harness": "claude-code"
}
```

`version` is optional (default `latest`). `session_id` is optional; the first `latest` lookup within a session is recorded and reused for subsequent same-id lookups in the session, so the host sees a consistent snapshot.

Response:

```json
{
  "id": "...",
  "version": "1.2.0",
  "content_hash": "sha256:...",
  "manifest_body": "...",
  "resources": [
    { "path": "scripts/variance.py", "presigned_url": "...", "content_hash": "..." }
  ]
}
```

Bytes below the inline cutoff (256 KB) are returned in the response body. Above, presigned URLs deliver them. The MCP server fetches resources directly from object storage.

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

## Layer management

### Register a layer

```
POST /v1/layers
```

Body:

```json
{
  "id": "team-finance",
  "source": {
    "git": {
      "repo": "git@github.com:acme/podium-finance.git",
      "ref": "main",
      "root": "artifacts/"
    }
  },
  "visibility": { "groups": ["acme-finance"] }
}
```

Response includes the webhook URL and HMAC secret to register on the source repo.

### List layers

```
GET /v1/layers
```

### Reingest

```
POST /v1/layers/{id}:reingest
```

Optional body for break-glass during a freeze window:

```json
{ "break_glass": true, "justification": "..." }
```

### Reorder user-defined layers

```
POST /v1/layers/user:reorder
```

Body: `{ "ids": ["layer-a", "layer-b", "layer-c"] }`.

### Unregister

```
DELETE /v1/layers/{id}
```

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
  "layers": ["admin-finance", "joan-personal", "workspace-overlay"],
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

Gated by tenant config (`tenant.expose_scope_preview`). When `false`, returns `403 scope_preview_disabled`. Aggregate counts can hint at the existence of restricted content even when no individual artifact is leaked, so operators decide whether to expose this surface per tenant.

---

## Outbound webhooks

The registry emits outbound webhooks for change events. Configure receivers per org (URL + HMAC secret).

| Event | When |
|:--|:--|
| `artifact.published` | A new `(artifact_id, version)` was ingested. |
| `artifact.deprecated` | An ingested manifest set `deprecated: true`. |
| `domain.published` | A `DOMAIN.md` was added or changed. |
| `layer.ingested` | A layer completed an ingest cycle. |
| `layer.history_rewritten` | Force-push detected on a `git`-source layer. |
| `vulnerability.detected` | A CVE matched an artifact's SBOM. |

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

Receivers are configured per org. The registry signs webhook deliveries with the configured HMAC secret.

---

## Subscriptions (SDK)

The SDKs expose `client.subscribe(events)` for in-process consumers that don't want to run their own webhook receiver. The wire surface is a streaming HTTP endpoint; the SDK abstracts the connection and reconnection logic.

Useful for sync watchers, downstream rebuild triggers, eval pipelines reacting to new artifact versions.

---

## Health

```
GET /healthz
```

Returns `{ "status": "ok", "mode": "standalone" | "standard" | "public", "read_only": false }`.

Used for liveness/readiness probes. In read-only mode, returns `read_only: true` and the response carries the `X-Podium-Read-Only` header on every read endpoint.

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

---

## Spec source

- Wire surface and contracts: [`spec/07-external-integration.md`](https://github.com/lennylabs/podium/blob/main/spec/07-external-integration.md).
- Discovery semantics: [`spec/03-disclosure-surface.md`](https://github.com/lennylabs/podium/blob/main/spec/03-disclosure-surface.md) and [`spec/05-meta-tools.md`](https://github.com/lennylabs/podium/blob/main/spec/05-meta-tools.md).
- Audit events: [`spec/08-audit-and-observability.md`](https://github.com/lennylabs/podium/blob/main/spec/08-audit-and-observability.md).
