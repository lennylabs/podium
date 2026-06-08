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
| `quota.*` | Per-tenant limits (storage, QPS, materialization rate, audit volume, layer count, artifact count). |
| `registry.*` | Registry-wide operational states. |
| `visibility.*` | Caller visibility and scope enforcement on a load. |

---

## Catalog

### auth.*

| Code | When |
|:--|:--|
| `auth.untrusted_runtime` | An `injected-session-token` JWT was signed by a runtime whose signing key isn't registered with the registry. |
| `auth.untrusted_token` | A gateway-forwarded `oidc-jwt` token failed signature, `iss`, or `aud` validation against the configured issuer JWKS. `details.token_iss` carries the rejected token's issuer. |
| `auth.tenant_unknown` | A verified `oidc-jwt` token's `org_id` names no provisioned tenant on a multi-tenant registry. `details.token_org_id` carries the unresolved organization. |
| `auth.token_expired` | The OAuth access token (or injected/forwarded JWT) has passed its `exp`. The MCP server triggers refresh on `oauth-device-code`; the runtime refreshes on `injected-session-token`; the gateway forwards a new token on `oidc-jwt`. |
| `auth.forbidden` | An admin-only operation attempted by a non-admin caller. |

### config.*

| Code | When |
|:--|:--|
| `config.no_registry` | `defaults.registry` is unset across every config scope, and no `--registry` flag or `PODIUM_REGISTRY` env var is set. |
| `config.public_mode_with_idp` | Both `--public-mode` (or `PODIUM_PUBLIC_MODE`) and `PODIUM_IDENTITY_PROVIDER` are set; they're mutually exclusive. |
| `config.public_bind_refused` | Public mode was engaged with a non-loopback bind address without `--allow-public-bind`. Public mode binds `127.0.0.1` unless the operator opts into a non-loopback bind. |
| `config.web_ui_public_bind_refused` | The web UI was enabled on a non-loopback bind without `--web-ui-allow-public-bind` and a configured identity provider, which would expose an unauthenticated UI. |
| `config.invalid_issuer_scheme` | `PODIUM_IDENTITY_PROVIDER=oidc-jwt` was given a non-`https` `PODIUM_OAUTH_ISSUER`. The registry fetches the discovery document and JWKS over this URL, so it must be `https`. |
| `config.oidc_jwt_audience_unset` | `PODIUM_IDENTITY_PROVIDER=oidc-jwt` without `PODIUM_OAUTH_AUDIENCE`. The required `aud` claim cannot be verified. |
| `config.injected_token_audience_unset` | `PODIUM_IDENTITY_PROVIDER=injected-session-token` without `PODIUM_OAUTH_AUDIENCE` set to this registry's endpoint. The required `aud` claim cannot be verified on every token. |
| `config.unknown_harness` | `PODIUM_HARNESS` (or `--harness`) names a harness with no registered adapter. |
| `config.trusted_headers_public_bind` | `trusted-headers` on a single-tenant registry bound to a non-loopback address without `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind`. |
| `config.trusted_headers_multitenant_no_secret` | `trusted-headers` on a multi-tenant registry without `PODIUM_TRUSTED_PROXY_SECRET`, which is required on every request regardless of bind. |
| `config.identity_provider_unverified` | A registered identity provider was selected without a request-time verifier wired, which would resolve every caller as anonymous-public. |

### domain.*

| Code | When |
|:--|:--|
| `domain.not_found` | A `load_domain` path doesn't resolve to any visible domain. Paths that exist only under `unlisted: true` return the same error to avoid leaking the existence of unlisted folders. |

### ingest.*

| Code | When |
|:--|:--|
| `ingest.lint_failed` | Manifest lint rejected the artifact at ingest. |
| `ingest.history_rewritten` | A layer with `force_push_policy: strict` detected that the new ref no longer reaches the previously ingested ref. |
| `ingest.webhook_invalid` | Git provider webhook signature didn't validate against the layer's HMAC secret. |
| `ingest.immutable_violation` | Same `version:` ingested with different content. The author bumps the version. |
| `ingest.frozen` | A freeze window blocks ingest. Use `--break-glass` (with dual-signoff and justification) to override. |
| `ingest.source_unreachable` | The layer's source (Git repo, S3 prefix, etc.) couldn't be reached at ingest time. Existing served artifacts are unaffected. |
| `ingest.public_mode_rejects_sensitive` | Public-mode deployments reject ingest of `sensitivity: medium` and `sensitivity: high` artifacts. |
| `ingest.sandbox_profile_unenforceable` | With `PODIUM_ENFORCE_SANDBOX_PROFILE=true` the registry rejects an artifact whose `sandbox_profile` the local host cannot honor; the host advertises its enforceable set via `PODIUM_HOST_SANDBOXES`. |

### materialize.*

| Code | When |
|:--|:--|
| `materialize.signature_invalid` | Signature verification failed at materialization (tampered content, expired signature, unknown signer). |
| `materialize.signature_missing` | The artifact requires a signature (sensitivity `medium` or higher under the default policy) but none was provided. |
| `materialize.runtime_unavailable` | The host can't satisfy the artifact's `runtime_requirements:` (Python version, Node version, system package). |
| `materialize.untranslatable` | The selected harness adapter cannot translate one or more of the artifact's fields. Use `harness: none` for raw output. |

### mcp.*

| Code | When |
|:--|:--|
| `mcp.unsupported_version` | Host and MCP server can't agree on a compatible MCP protocol version. |
| `mcp.client_too_old` | The host caller's reported version is below the minimum the MCP binary serves. Update the host. |

### network.*

| Code | When |
|:--|:--|
| `network.registry_unreachable` | The MCP server (or SDK) can't reach the registry. In `always-revalidate` cache mode, fresh calls return this on miss; `offline-first` returns cached results without raising. |

### quota.*

| Code | When |
|:--|:--|
| `quota.storage_exceeded` | Per-tenant storage limit hit. |
| `quota.search_qps_exceeded` | Per-tenant search QPS limit hit. |
| `quota.materialize_rate_exceeded` | Per-tenant materialization rate limit hit. |
| `quota.audit_volume_exceeded` | Per-tenant audit volume limit hit. |
| `quota.layer_count_exceeded` | A user has hit the per-identity user-defined-layer cap. The rejected layer is not created. |
| `quota.artifact_count_exceeded` | Ingest would push the tenant past its artifact-count quota. The artifact is rejected. |

### registry.*

| Code | When |
|:--|:--|
| `registry.read_only` | Postgres primary unreachable; the registry has fallen back to read-only mode. Write endpoints (ingest, layer admin, freeze toggles, admin grants) are rejected. Read endpoints continue to serve from the replica. |
| `registry.invalid_argument` | A request argument failed validation (e.g., `top_k > 50`). Both the SDK (client-side) and the registry (server-side) enforce. |

### visibility.*

| Code | When |
|:--|:--|
| `visibility.denied` | The caller lacks visibility for the artifact, or a load grant does not cover the resolved record. The response mirrors a not-found result so it does not leak that a hidden artifact exists. |

---

## Adding namespaces

Custom plugins (extension types, source providers, harness adapters) can register their own error namespaces through the SPI. Plugin-registered codes follow the same envelope structure and are documented alongside the plugin.
