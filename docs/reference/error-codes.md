---
layout: default
title: Error codes
parent: Reference
nav_order: 4
description: The structured error envelope and the full namespace catalog.
---

# Error codes

Every Podium error is a structured envelope:

```json
{
  "code": "auth.untrusted_runtime",
  "message": "Runtime 'managed-runtime-x' is not registered with the registry.",
  "details": { "runtime_iss": "managed-runtime-x" },
  "retryable": false,
  "suggested_action": "Register the runtime's signing key via 'podium admin runtime register'."
}
```

| Field | Meaning |
|:--|:--|
| `code` | Namespaced identifier. See the catalog below. |
| `message` | Human-readable summary. |
| `details` | Per-code structured context (caller, layer, artifact, etc.). |
| `retryable` | Whether retrying the same call may succeed. |
| `suggested_action` | A concrete next step where one applies. |

Codes map to MCP error payloads per the MCP spec for harnesses that consume Podium through the MCP bridge. SDK clients raise typed exceptions whose message and details mirror the envelope.

---

## Namespaces

| Namespace | What it covers |
|:--|:--|
| `auth.*` | Identity provider, token validation, runtime trust. |
| `config.*` | Config-file resolution and validation at process startup. |
| `domain.*` | Domain lookup and discovery. |
| `ingest.*` | Webhook receipt, lint, immutability, freeze windows. |
| `materialize.*` | Signature verification, runtime requirements, sandbox profile. |
| `mcp.*` | MCP protocol-level mismatches. |
| `network.*` | Registry reachability from the consumer side. |
| `quota.*` | Per-tenant limits (storage, QPS, materialization rate, audit volume). |
| `registry.*` | Registry-wide operational states. |

---

## Catalog

### auth.*

| Code | When |
|:--|:--|
| `auth.untrusted_runtime` | An `injected-session-token` JWT was signed by a runtime whose signing key isn't registered with the registry. |
| `auth.token_expired` | The OAuth access token (or injected JWT) has passed its `exp`. The MCP server triggers refresh on `oauth-device-code`; the runtime is responsible for refresh on `injected-session-token`. |
| `auth.forbidden` | An admin-only operation attempted by a non-admin caller. |

### config.*

| Code | When |
|:--|:--|
| `config.no_registry` | `defaults.registry` is unset across every config scope, and no `--registry` flag or `PODIUM_REGISTRY` env var is set. |
| `config.public_mode_with_idp` | Both `--public-mode` (or `PODIUM_PUBLIC_MODE`) and `PODIUM_IDENTITY_PROVIDER` are set; they're mutually exclusive. |

### domain.*

| Code | When |
|:--|:--|
| `domain.not_found` | A `load_domain` path doesn't resolve to any visible domain. Paths that exist only under `unlisted: true` return the same error to avoid leaking the existence of unlisted folders. |

### ingest.*

| Code | When |
|:--|:--|
| `ingest.lint_failed` | Manifest lint rejected the artifact at ingest. |
| `ingest.webhook_invalid` | Git provider webhook signature didn't validate against the layer's HMAC secret. |
| `ingest.immutable_violation` | Same `version:` ingested with different content. The author bumps the version. |
| `ingest.frozen` | A freeze window blocks ingest. Use `--break-glass` (with dual-signoff and justification) to override. |
| `ingest.source_unreachable` | The layer's source (Git repo, S3 prefix, etc.) couldn't be reached at ingest time. Existing served artifacts are unaffected. |
| `ingest.public_mode_rejects_sensitive` | Public-mode deployments reject ingest of `sensitivity: medium` and `sensitivity: high` artifacts. |

### materialize.*

| Code | When |
|:--|:--|
| `materialize.signature_invalid` | Signature verification failed at materialization (tampered content, expired signature, unknown signer). |
| `materialize.runtime_unavailable` | The host can't satisfy the artifact's `runtime_requirements:` (Python version, Node version, system package). |

### mcp.*

| Code | When |
|:--|:--|
| `mcp.unsupported_version` | Host and MCP server can't agree on a compatible MCP protocol version. |

### network.*

| Code | When |
|:--|:--|
| `network.registry_unreachable` | The MCP server (or SDK) can't reach the registry. In `always-revalidate` cache mode, fresh calls return this on miss; `offline-first` returns cached results without raising. |

### quota.*

| Code | When |
|:--|:--|
| `quota.storage_exceeded` | Per-tenant storage limit hit. |
| `quota.search_qps_exceeded` | Per-tenant search QPS limit hit. |
| `quota.materialization_rate_exceeded` | Per-tenant materialization rate limit hit. |
| `quota.audit_volume_exceeded` | Per-tenant audit volume limit hit. |
| `quota.user_layer_cap_exceeded` | A user has hit the per-identity user-defined-layer cap. |

### registry.*

| Code | When |
|:--|:--|
| `registry.read_only` | Postgres primary unreachable; the registry has fallen back to read-only mode. Write endpoints (ingest, layer admin, freeze toggles, admin grants) are rejected. Read endpoints continue to serve from the replica. |
| `registry.invalid_argument` | A request argument failed validation (e.g., `top_k > 50`). Both the SDK (client-side) and the registry (server-side) enforce. |

---

## Adding namespaces

Custom plugins (extension types, source providers, harness adapters) can register their own error namespaces through the SPI. Plugin-registered codes follow the same envelope structure and are documented alongside the plugin.

The full list of base namespaces is recorded in [`spec/06-mcp-server.md` §6.10](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#610-error-model).
