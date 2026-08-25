---
title: Error codes
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
  "suggested_action": "Add the runtime's signing key with 'podium admin runtime register --keys-file', then restart the registry."
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
| `auth.untrusted_runtime` | An `injected-session-token` JWT was signed by a runtime whose signing key is absent from the registry's trusted key set. The deployment adds the key to the file named by `PODIUM_RUNTIME_KEYS_PATH` and restarts the registry. |
| `auth.untrusted_token` | An `oidc-jwt` token failed signature, `iss`, or `aud` validation against the accepted issuers and the issuer JWKS, in either accepted credential location. `details.token_iss` carries the rejected token's issuer. A gateway-forwarded token is corrected at the gateway; a browser session is re-established by signing in again. |
| `auth.tenant_unknown` | A verified `oidc-jwt` token's `org_id` names no provisioned tenant on a multi-tenant registry. `details.token_org_id` carries the unresolved organization. |
| `auth.token_expired` | The OAuth access token (or injected/forwarded JWT) has passed its `exp`. The MCP server triggers refresh on `oauth-device-code`; the runtime refreshes on `injected-session-token`; under `oidc-jwt` a gateway forwards a new token, and a browser session is renewed by signing in again. |
| `auth.forbidden` | An admin-only operation attempted by a non-admin caller, including a receiver CRUD call (`/v1/webhooks`) by a caller without the per-tenant admin role. Also a layer write the [layer write authorization rule](http-api#layer-management) authorizes on neither arm: an admin-defined layer written by a caller without the per-tenant admin role, or a user-defined layer written by a caller who is neither its stored owner nor a tenant admin, including a caller who resolves no subject. |
| `auth.csrf_invalid` | A state-changing request that carried cross-site browser-origin evidence, refused with `403` before the handler runs and whatever credential authenticated it, or a browser sign-in callback whose single-use pre-authorization transaction is absent, expired, or carries a different `state`. The [browser-origin gate](http-api#browser-session) states each predicate. |
| `auth.exchange_failed` | A browser sign-in callback whose authorization-code exchange the identity provider answered and refused, such as an `invalid_grant` response or a refusal caused by a wrong client credential, refused with `502`. It is permanent for that request, so the envelope carries `retryable: false`. An identity provider the registry could not reach, and one whose token endpoint answered with a `5xx`, return `registry.unavailable` instead. |

### config.*

| Code | When |
|:--|:--|
| `config.no_registry` | `defaults.registry` is unset across every config scope, and no `--registry` flag or `PODIUM_REGISTRY` env var is set. |
| `config.public_mode_with_idp` | Both `--public-mode` (or `PODIUM_PUBLIC_MODE`) and `PODIUM_IDENTITY_PROVIDER` are set; they're mutually exclusive. |
| `config.public_bind_refused` | Public mode was engaged with a non-loopback bind address without `--allow-public-bind`. Public mode binds `127.0.0.1` unless the operator opts into a non-loopback bind. |
| `config.web_ui_public_bind_refused` | The web UI was enabled on a non-loopback bind without `--web-ui-allow-public-bind` and a configured identity provider, so a UI reachable beyond the loopback interface is served only by a registry that resolves a caller's identity and filters what it serves by that identity. |
| `config.web_ui_auth_unconfigured` | The browser flow (`--web-ui-auth` / `PODIUM_WEB_UI_AUTH`) was enabled on a configuration that fails one of its conjuncts, and the message names the failed one. The flow requires that the web UI is enabled, that `PODIUM_IDENTITY_PROVIDER` is `oidc-jwt`, that public mode is off, that `PODIUM_WEB_UI_OAUTH_CLIENT_ID`, `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, `PODIUM_WEB_UI_REDIRECT_URI`, `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, and `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` are each non-empty, and that `PODIUM_WEB_UI_REDIRECT_URI` is an `https` URL or an `http` URL whose host is a loopback address. |
| `config.invalid_issuer_scheme` | `PODIUM_IDENTITY_PROVIDER=oidc-jwt` was given a non-`https` `PODIUM_OAUTH_ISSUER`. The registry fetches the discovery document and JWKS over this URL, so it must be `https`. |
| `config.oidc_jwt_audience_unset` | `PODIUM_IDENTITY_PROVIDER=oidc-jwt` without `PODIUM_OAUTH_AUDIENCE`. The required `aud` claim cannot be verified. |
| `config.injected_token_audience_unset` | `PODIUM_IDENTITY_PROVIDER=injected-session-token` without `PODIUM_OAUTH_AUDIENCE` set to this registry's endpoint. The required `aud` claim cannot be verified on every token. |
| `config.runtime_keys_unavailable` | `PODIUM_IDENTITY_PROVIDER=injected-session-token` with no trusted runtime signing key: `PODIUM_RUNTIME_KEYS_PATH` is unset or names a file with no key. Also raised under any provider when the named file cannot be read or parsed. |
| `config.unknown_harness` | `PODIUM_HARNESS` (or `--harness`) names a harness with no registered adapter. |
| `config.invalid` | A `sync.yaml` `kind: marketplace` target is malformed: its harness set names a non-publish-target harness (`opencode` or `none` have no git-repo distribution), a plugin glob is malformed, or a workflow command declares neither `run:` nor `sh:` (or both). It also covers a marketplace field on a `kind: workspace` target, a workspace scope field on a `kind: marketplace` target, and a `kind: marketplace` target combined with `--watch`. `podium sync --config` rejects it at config validation. |
| `config.trusted_headers_public_bind` | `trusted-headers` on a single-tenant registry bound to a non-loopback address without `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind`. |
| `config.trusted_headers_multitenant_no_secret` | `trusted-headers` on a multi-tenant registry without `PODIUM_TRUSTED_PROXY_SECRET`, which is required on every request regardless of bind. |
| `config.identity_provider_unverified` | A registered identity provider was selected without a request-time verifier wired, which would resolve every caller as anonymous-public. |
| `config.scope_preview_disabled` | `GET /v1/scope/preview` reached a tenant whose `expose_scope_preview` gate is `false`. Returned as `403`. |
| `config.not_found` | The named `sync.yaml` was not found at the given path. |
| `config.invalid_sign_mode` | `PODIUM_SIGN` (or `--sign`) carried a value other than `registry-key`. |
| `config.layer_path_ambiguous` | A `--layer-path` root sets `multi_layer: true` in `.registry-config` while manifest files are also present at the top level, so the mode cannot be resolved. |
| `config.server_version_too_old` | The merged defaults or the active profile pin a `min_server_version` above the running binary. Upgrade Podium to run that profile. |
| `config.invalid_min_version` | A `min_server_version` pin (or the binary version) is not a comparable semver. |
| `config.signature_provider_unavailable` | The selected signature provider is not configured: no registry-managed key for `registry-managed`, or no Sigstore configuration for `sigstore-keyless`. |
| `config.filesystem_registry_unsupported` | `PODIUM_REGISTRY` names a filesystem path while the MCP server requires an `http://` or `https://` source. Use `podium sync` to consume a filesystem registry. |

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
| `ingest.invalid_artifact` | The manifest could not be decoded into an artifact record, or its `extends:` pin failed to resolve. The artifact is rejected; the rest of the ingest continues. |
| `ingest.collision` | Another layer already contributes this canonical artifact ID and the incoming manifest declares no `extends:`, so the overlay is not sanctioned. |
| `ingest.sign_failed` | The configured signer rejected the artifact's content hash at ingest. |
| `ingest.resource_store_failed` | A bundled resource could not be persisted to the object store, so the manifest was not committed. |

### materialize.*

| Code | When |
|:--|:--|
| `materialize.signature_invalid` | Signature verification failed at materialization (tampered content, expired signature, unknown signer). |
| `materialize.signature_missing` | The artifact requires a signature (sensitivity `medium` or higher under the default policy) but none was provided. |
| `materialize.runtime_unavailable` | The host can't satisfy the artifact's `runtime_requirements:` (Python version, Node version, system package). |
| `materialize.content_hash_mismatch` | The bytes the consumer received hash to a different value than the `content_hash` the registry served, so materialization stops before writing. |
| `materialize.hook_failed` | A materialization hook returned an error, so the artifact is not written. |
| `materialize.sandbox_violation` | A hook attempted an action its sandbox profile does not permit. |
| `materialize.untranslatable` | The selected harness adapter cannot translate the artifact's type, mode, or one or more of its fields onto that harness (a §6.7.1 ✗ cell). For example, a plugin-layout type (`skill`, `agent`, `command`, `rule`, `hook`, `mcp-server`) on `claude-cowork` fails on both `podium sync` and `load_artifact`, because Cowork has no project-scope surface and the artifact ships through the published Claude marketplace instead. Use `harness: none` for raw output, or `target_harnesses:` to opt the artifact out of that harness. |

### mcp.*

| Code | When |
|:--|:--|
| `mcp.unsupported_version` | Host and MCP server can't agree on a compatible MCP protocol version. |
| `mcp.client_too_old` | The host caller's reported version is below the minimum the MCP binary serves. Update the host. |

### network.*

| Code | When |
|:--|:--|
| `network.registry_unreachable` | The MCP server or an SDK cannot reach the registry. The MCP server holds a content cache, so its `always-revalidate` mode returns this on a fresh-call miss while `offline-first` serves cached results without raising. The SDKs hold no cache, so both modes raise. |
| `network.offline_cache_miss` | `offline-only` cache mode was asked for something the local cache does not hold, and the mode forbids contacting the registry. |

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
| `registry.read_only` | Postgres primary unreachable; the registry has fallen back to read-only mode. Write endpoints (ingest, layer admin, freeze toggles, admin grants, tenant management) are rejected. Read endpoints continue to serve from the replica. |
| `registry.tenant_management_unavailable` | A `/v1/admin/tenants` request (or a `podium admin tenant` command) reached a single-tenant or standalone registry, where multi-tenancy is out of scope. Start the registry in multi-tenant mode with `PODIUM_MULTI_TENANT` on a standard backend to manage tenants. |
| `registry.invalid_argument` | A request argument failed validation (e.g., `top_k > 50`, or an outbound webhook receiver URL that is non-`https` or resolves to a loopback, link-local, or private target outside `PODIUM_WEBHOOK_ALLOWED_TARGETS`). Both the SDK (client-side) and the registry (server-side) enforce. Most endpoints also return it for an unsupported HTTP method. |
| `registry.method_not_allowed` | `load_artifact` was called with a method other than `GET` or `HEAD`. |
| `registry.not_found` | The named artifact, layer, webhook receiver, object, or tenant quota record does not exist. Returned as `404`. |
| `registry.tenant_not_found` | A tenant lookup or write named an ID that is not provisioned. Returned as `404` by `PATCH` and `DELETE /v1/admin/tenants/{id}`. |
| `registry.unavailable` | The registry could not complete the request against its metadata store, object store, or another dependency. Returned as `500`. |
| `registry.unknown` | A batch-load item failed for a reason that maps to no more specific code. Carried in that item's error envelope; the batch itself stays `200`. |

### visibility.*

| Code | When |
|:--|:--|
| `visibility.denied` | The caller lacks visibility for the artifact, or a load grant does not cover the resolved record. The response mirrors a not-found result so it does not leak that a hidden artifact exists. |

---

## Adding namespaces

Custom plugins (extension types, source providers, harness adapters) can register their own error namespaces through the SPI. Plugin-registered codes follow the same envelope structure and are documented alongside the plugin.
