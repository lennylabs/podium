---
title: Error codes
nav_order: 4
description: The structured error envelope that Podium returns on failure, and the catalog of error codes by namespace.
---

# Error codes

This page lists the error codes that the registry, the MCP server, `podium sync`, the CLI, and the language SDKs return. Use it to look up what a code means, where it comes from, and what to do about it. Codes are grouped by namespace. [Troubleshooting](../deployment/operations/troubleshooting) indexes the same failures by symptom.

---

## Error envelope

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

The envelope reaches each consumer as follows:

- A harness that consumes Podium through the MCP server receives the code as an MCP error payload, mapped per the MCP specification.
- An SDK client raises a typed exception whose message and details mirror the envelope.

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
| `auth.untrusted_token` | An `oidc-jwt` token failed signature, `iss`, or `aud` validation against the accepted issuers and the issuer JWKS, in either accepted credential location. `details.token_iss` carries the rejected token's issuer. Correct a gateway-forwarded token at the gateway. Re-establish a browser session by signing in again. |
| `auth.tenant_unknown` | A verified `oidc-jwt` token's `org_id` names no provisioned tenant on a multi-tenant registry. `details.token_org_id` carries the unresolved organization. |
| `auth.token_expired` | The OAuth access token, or the injected or forwarded JWT, has passed its `exp`. The recovery depends on the provider; see [auth.token_expired recovery](#authtoken_expired-recovery). |
| `auth.forbidden` | The caller is not authorized for the operation. The code covers several authorization rules, listed in [auth.forbidden arms](#authforbidden-arms). |
| `auth.csrf_invalid` | Refused with `403`. Either a state-changing request carried cross-site browser-origin evidence, or a browser sign-in callback's single-use pre-authorization transaction is absent, expired, or carries a different `state`. The first case is refused before the handler runs, whatever credential authenticated the request. The [browser-origin gate](http-api#browser-origin-gate) states each predicate. |
| `auth.exchange_failed` | Refused with `502`. A browser sign-in callback's authorization-code exchange was answered and refused by the identity provider, for example with an `invalid_grant` response or because of a wrong client credential. The failure is permanent for that request, so the envelope carries `retryable: false`. An identity provider the registry could not reach, or whose token endpoint answered with a `5xx`, returns `registry.unavailable` instead. |

#### auth.token_expired recovery

| Provider | Recovery |
|:--|:--|
| `oauth-device-code` (MCP server) | The MCP server triggers a token refresh. |
| `oauth-device-code` (SDK client) | The SDK client runs its login again, because the SDK does not refresh tokens. |
| `injected-session-token` | The runtime refreshes the token. |
| `oidc-jwt` | A gateway forwards a new token. A browser session is renewed by signing in again. |

#### auth.forbidden arms

| Arm | Trigger | `details.constraint` |
|:--|:--|:--|
| Admin-only operation | A caller without the admin role attempts an admin-only operation. This includes a receiver CRUD call (`/v1/webhooks`) by a caller without the per-tenant admin role. | none |
| Layer write | A layer write that the [layer write authorization rule](http-api#layer-write-authorization) authorizes on neither arm. That is an admin-defined layer written by a caller without the per-tenant admin role, or a user-defined layer written by a caller who is neither its stored owner nor a tenant admin. A caller who resolves no subject is included. | none |
| Local source | A registration, a filesystem-path patch, a restore, or a reingest of a layer that names a filesystem path on the registry host, by a caller without the per-tenant admin role. The [local-source authorization rule](http-api#local-source-authorization) governs it. | `"local_source"` |
| Admin-only fields | A registration that asserts `owner`, `public`, `organization`, `groups`, or `users`, by a caller without the per-tenant admin role. These fields are read on a tenant admin's registration alone. | `"admin_only_fields"` |

Notes on the layer arms:

- **Deployments without authentication.** The layer write, local-source, and admin-only-fields arms apply on a registry that configures an identity provider. A registry started with no identity provider configured, or one started in public mode, authenticates no caller. It admits every caller on the admin arm, so these arms refuse nothing there.
- **Local-source triggers.** A request reaches the local-source arm in three ways: a `local` `source_type`, a `local_path` carried outside a `git` source, or a `repo` string that resolves to the Git file transport. The refusal names no filesystem path.
- **Local source and `ingest.source_unreachable`.** `ingest.source_unreachable` reports a source the registry tried to read and could not. The local-source arm reports a read the registry refused to attempt for that caller.
- **Admin-only field assertion.** A field is asserted by its value rather than by its presence. `public` or `organization` carrying false, an empty `groups` or `users`, an empty `owner`, and an `owner` naming the caller's own verified subject assert nothing. The refusal names the asserted fields in its message.
- **Registration class.** The registration class field is resolved rather than refused. A registration that asserts none of the admin-only fields is stored as a user-defined layer owned by the caller.
- **Evaluation order.** The registry evaluates the layer write arm first, then the local-source arm, then the admin-only-fields arm. A registration the layer write rule refuses carries a bare `auth.forbidden` with no `details.constraint`. A registration also on the local-source arm carries `details.constraint: "local_source"`. `admin_only_fields` is returned only where neither earlier rule refuses.

### config.*

| Code | When |
|:--|:--|
| `config.no_registry` | `defaults.registry` is unset across every config scope, and no `--registry` flag or `PODIUM_REGISTRY` env var is set. |
| `config.public_mode_with_idp` | Both `--public-mode` (or `PODIUM_PUBLIC_MODE`) and `PODIUM_IDENTITY_PROVIDER` are set. The two are mutually exclusive. |
| `config.public_bind_refused` | Public mode was engaged with a non-loopback bind address without `--allow-public-bind`. Public mode binds `127.0.0.1` unless the operator opts into a non-loopback bind. |
| `config.web_ui_public_bind_refused` | The web UI was enabled on a non-loopback bind, and either `--web-ui-allow-public-bind` or a configured identity provider is missing. A UI reachable beyond the loopback interface is served only by a registry that resolves each caller's identity and filters what it serves by that identity. |
| `config.web_ui_auth_unconfigured` | The browser flow (`--web-ui-auth` / `PODIUM_WEB_UI_AUTH`) was enabled on a configuration that fails one of its requirements. The message names the failed requirement. See [config.web_ui_auth_unconfigured requirements](#configweb_ui_auth_unconfigured-requirements). |
| `config.invalid_issuer_scheme` | `PODIUM_IDENTITY_PROVIDER=oidc-jwt` was given a non-`https` `PODIUM_OAUTH_ISSUER`. The registry fetches the discovery document and JWKS over this URL, so it must be `https`. |
| `config.oidc_jwt_audience_unset` | `PODIUM_IDENTITY_PROVIDER=oidc-jwt` with `PODIUM_OAUTH_AUDIENCE` resolving to no audience, because it is unset, empty, or blank after each entry is trimmed. The required `aud` claim cannot be verified. |
| `config.injected_token_audience_unset` | `PODIUM_IDENTITY_PROVIDER=injected-session-token` with `PODIUM_OAUTH_AUDIENCE` resolving to no audience, because it is unset, empty, or blank after each entry is trimmed. The variable must name at least one audience this registry answers to, so the required `aud` claim is verified on every token. |
| `config.runtime_keys_unavailable` | `PODIUM_IDENTITY_PROVIDER=injected-session-token` with no trusted runtime signing key: `PODIUM_RUNTIME_KEYS_PATH` is unset or names a file with no key. Also raised under any provider when the named file cannot be read or parsed. |
| `config.unknown_harness` | `PODIUM_HARNESS` (or `--harness`) names a harness with no registered adapter. |
| `config.invalid` | A `sync.yaml` marketplace or workspace target is malformed, or `podium-mcp` was started with an unsupported signature provider. See [config.invalid causes](#configinvalid-causes). |
| `config.trusted_headers_public_bind` | `trusted-headers` on a single-tenant registry bound to a non-loopback address without `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind`. |
| `config.trusted_headers_multitenant_no_secret` | `trusted-headers` on a multi-tenant registry without `PODIUM_TRUSTED_PROXY_SECRET`, which is required on every request regardless of bind. |
| `config.multitenant_no_verifier` | A registry started with `PODIUM_MULTI_TENANT` has no request-time identity verifier to select each request's tenant. This happens when it configures no identity provider, names a provider value the registry does not recognize, or runs in public mode with no identity provider. The message names the providers the registry verifies: `injected-session-token`, `oidc-jwt`, and `trusted-headers`. A configuration that `config.public_mode_with_idp`, `config.public_bind_refused`, `config.web_ui_public_bind_refused`, or `config.web_ui_auth_unconfigured` also refuses fails with that code instead. |
| `config.identity_provider_unverified` | A registered identity provider was selected without a request-time verifier wired, which would resolve every caller as anonymous-public. |
| `config.scope_preview_disabled` | `GET /v1/scope/preview` reached a tenant whose `expose_scope_preview` gate is `false`. Returned as `403`. |
| `config.not_found` | The named `sync.yaml` was not found at the given path. |
| `config.invalid_sign_mode` | `PODIUM_SIGN` (or `--sign`) carried a value other than `registry-key` or `none`. An unset variable means `registry-key`. |
| `config.layer_path_ambiguous` | A `--layer-path` root sets `multi_layer: true` in `.registry-config` while manifest files are also present at the top level, so the mode cannot be resolved. |
| `config.server_version_too_old` | The merged defaults or the active profile pin a `min_server_version` above the running binary. Upgrade Podium to run that profile. |
| `config.invalid_min_version` | A `min_server_version` pin (or the binary version) is not a comparable semver. |
| `config.signature_provider_unavailable` | The selected signature provider has no verification or signing material. The message names each source tried. Supply the material, or set `PODIUM_VERIFY_SIGNATURES=never` against a registry that does not sign. See [config.signature_provider_unavailable causes](#configsignature_provider_unavailable-causes). |
| `config.scim_store_unavailable` | `PODIUM_SCIM_STORE_PATH` is non-empty and names a store the registry cannot use. The message names the path and the cause. Fix the file or its directory, or unset the variable to keep the directory in memory. See [config.scim_store_unavailable causes](#configscim_store_unavailable-causes). |
| `config.audit_sink_unavailable` | The registry, `sign-stored-rows`, or `podium-mcp` cannot use its audit configuration, whatever the `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` setting. Fix the variable the message names. See [config.audit_sink_unavailable causes](#configaudit_sink_unavailable-causes). |
| `config.audit_anchor_key_unavailable` | Audit anchoring is on and the anchor key file at `PODIUM_AUDIT_SIGNING_KEY_PATH` is unusable. The message names the key file. Restore the file, or move it aside so the registry generates a new keypair. See [config.audit_anchor_key_unavailable causes](#configaudit_anchor_key_unavailable-causes). |
| `config.audit_anchor_key_shared` | Under the same anchoring conditions as `config.audit_anchor_key_unavailable`, and while registry signing is on (`PODIUM_SIGN` or `podium serve --sign` resolves to `registry-key`), the anchor public key equals the `public:` key or a `verify:` key of the `PODIUM_SIGN_KEY_PATH` file. The message names the shared `key_id` and both key paths. Generate a separate keypair for `PODIUM_AUDIT_SIGNING_KEY_PATH`. |
| `config.filesystem_registry_unsupported` | `PODIUM_REGISTRY` names a filesystem path while the MCP server requires an `http://` or `https://` source. Use `podium sync` to consume a filesystem registry. |

#### config.web_ui_auth_unconfigured requirements

The browser flow requires every condition below:

- The web UI is enabled.
- `PODIUM_IDENTITY_PROVIDER` is `oidc-jwt`.
- Public mode is off.
- `PODIUM_WEB_UI_OAUTH_CLIENT_ID`, `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, `PODIUM_WEB_UI_REDIRECT_URI`, `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, and `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` are each non-empty.
- `PODIUM_WEB_UI_REDIRECT_URI` is an `https` URL, or an `http` URL whose host is a loopback address.

#### config.invalid causes

`podium sync --config` rejects these `sync.yaml` problems at config validation:

- A `kind: marketplace` target's harness set names a non-publish-target harness. `opencode` and `none` have no git-repo distribution.
- A `kind: marketplace` target has a malformed plugin glob.
- A `kind: marketplace` target has a workflow command that declares neither `run:` nor `sh:`, or declares both.
- A `kind: workspace` target carries a marketplace field.
- A `kind: marketplace` target carries a workspace scope field.
- A `kind: marketplace` target is combined with `--watch`.

`podium-mcp` also returns `config.invalid` at startup when `PODIUM_SIGNATURE_PROVIDER` names a value other than `registry-managed` or `noop`. This includes `sigstore-keyless`, which only `podium sign` and `podium verify` accept.

#### config.signature_provider_unavailable causes

| Process | When it refuses |
|:--|:--|
| `podium-mcp` | It refuses to start under a policy above `never` when the selected provider resolves no material. `noop` verifies nothing, so it always resolves none. `registry-managed` resolves none when `PODIUM_SIGNATURE_VERIFY_KEY` is unset and the key file at `PODIUM_SIGN_KEY_PATH` yields no usable public key, including a missing key path or file. It also resolves none when `PODIUM_SIGNATURE_VERIFY_KEY` is set to a non-empty value with an entry that is empty or does not decode. |
| Server-source `podium sync` | It resolves its key set by the same rules and refuses under `always` when none resolves. It exits 2 before any registry request. |
| Language SDKs | They resolve their key set by the same rules and refuse under `always` when none resolves. An SDK client fails at its one resolution before its first registry request. The Python SDK also refuses with this code, naming the `podium-sdk[verify]` extra, when the policy is `always` and the extra is not installed. |
| `podium sign` and `podium verify` | Under the `registry-managed` provider, they exit non-zero when the key their command needs does not resolve. |
| Registry start and `sign-stored-rows` | They refuse, naming the key file, when the file at `PODIUM_SIGN_KEY_PATH` has no `private:` or `public:` line, carries a line that does not decode, or has a `public:` line that is not the public half of its `private:` line. |
| `sign-stored-rows` only | It also refuses when the key file is absent with signing on, or when `--include-unsigned` is given with `--dry-run` or `--plan-digest` while signing is off. |

#### config.scim_store_unavailable causes

The registry raises this code under every identity provider, and whether or not `PODIUM_SCIM_TOKENS` mounts the receiver. The store at `PODIUM_SCIM_STORE_PATH` is unusable when any of these holds:

- The file cannot be read for a reason other than its absence.
- The file is non-empty and does not parse as the directory document.
- The parent directory cannot be created.
- The registry cannot create a file in the parent directory.

#### config.audit_sink_unavailable causes

Destination list and file causes:

- An empty element in the destination list.
- An element whose `scheme://` prefix is not `http`, `https`, `otlp+http`, or `otlp+https`.
- An element with one of those prefixes that does not parse as a URL.
- Two file destinations, or an element named twice.
- Userinfo in a destination URL.
- `stdout` in the list while `PODIUM_TRACING=stdout` also writes to standard output.
- A file destination that cannot be opened, or that can be opened but cannot be opened for appending. A file destination cannot be opened when the path is unset and the home directory cannot be resolved, when the file's parent directory cannot be created, or when an existing file at the path cannot be read. Either file cause refuses startup even when the file is the only destination.

Transport and queue causes:

- A `PODIUM_AUDIT_HEADERS_FILE`, `PODIUM_AUDIT_TLS_CA_FILE`, `PODIUM_AUDIT_TLS_CERT_FILE`, or `PODIUM_AUDIT_TLS_KEY_FILE` setting that the [Configuration](configuration#audit) rows refuse.
- A non-empty `PODIUM_AUDIT_QUEUE_MAX_BYTES` that is not a positive base-10 integer.

`podium-mcp` also refuses `stdout` in `PODIUM_AUDIT_SINK`, because its standard output carries the MCP protocol. It reads the transport and queue variables only when `PODIUM_AUDIT_SINK` is set.

The message names the cause and the variable that holds the unusable setting. That variable is `PODIUM_AUDIT_LOG_PATH` or `PODIUM_AUDIT_SINK` for a list or file cause, and the transport or queue variable otherwise. The message names a destination by its scheme, host, and path, and never includes a header value or URL userinfo.

#### config.audit_anchor_key_unavailable causes

The code applies when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and the registry stream lists a file destination that opens. The anchor key file at `PODIUM_AUDIT_SIGNING_KEY_PATH` is then unusable when any of these holds:

- The file cannot be read.
- The file carries no `private:` line or no `public:` line, or repeats either line.
- A line does not decode, or the `public:` line is not the public half of the `private:` line.
- The keypair cannot be generated.

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
| `ingest.sandbox_profile_unenforceable` | With `PODIUM_ENFORCE_SANDBOX_PROFILE=true`, the registry rejects an artifact whose `sandbox_profile` the local host cannot honor. The host advertises its enforceable set via `PODIUM_HOST_SANDBOXES`. |
| `ingest.invalid_artifact` | The artifact is rejected and the rest of the ingest continues. See [ingest.invalid_artifact causes](#ingestinvalid_artifact-causes). |
| `ingest.collision` | Another registry-side layer already contributes this canonical artifact ID. Ingest drops the incoming artifact. Rename the artifact, or remove the other copy first and then ingest the replacement. A filesystem-source `podium sync` drops the higher-precedence copy, materializes the rest, and exits 1. |
| `ingest.sign_failed` | The configured signer rejected the artifact's content hash at ingest (§7.3.1). The rejection covers that one artifact, and the rest of the batch is accepted. |
| `ingest.resource_store_failed` | A bundled resource could not be persisted to the object store, so the manifest was not committed. |

#### ingest.invalid_artifact causes

| Cause | `details.constraint` |
|:--|:--|
| The manifest could not be decoded into an artifact record. | none |
| An `ARTIFACT.md` declares the removed `extends` key. The registry refuses it before lint. | `"extends_removed"` |
| A skill with no declared version has content that a stored or soft-deleted row of the artifact already holds under another version. Set `metadata.version` in `SKILL.md`, or change the content, to publish such a skill. | `"auto_version_duplicate_content"` |

A filesystem-source `podium sync` reports the same code for an `ARTIFACT.md` that declares `extends:`. It drops that artifact before the collision rule, materializes the rest, and exits 1.

### materialize.*

| Code | When |
|:--|:--|
| `materialize.signature_invalid` | Signature verification failed. See [materialize.signature_invalid causes](#materializesignature_invalid-causes). |
| `materialize.signature_missing` | The artifact carries no signature and one is required. `podium-mcp` and server-source `podium sync` return it under `PODIUM_VERIFY_SIGNATURES=always`, the default, for every artifact the response carries unsigned (§4.7.9). A language SDK returns it when its resolved policy is `always`. A keyed `podium sync` against a registry that runs without a signing key fails with it. The registry also returns it from its [stored-row admission check](#stored-row-admission-refusals). |
| `materialize.runtime_unavailable` | The host can't satisfy the artifact's `runtime_requirements:` (Python version, Node version, system package). |
| `materialize.content_hash_mismatch` | The delivery check failed, or a fetched body does not match its content hash. See [materialize.content_hash_mismatch causes](#materializecontent_hash_mismatch-causes). |
| `materialize.stale_resolution` | `podium-mcp` received a validly signed record for a `latest` load whose artifact revision is below one it already accepted, or is absent or malformed. See [materialize.stale_resolution details](#materializestale_resolution-details). |
| `materialize.hook_failed` | A materialization hook returned an error, so the artifact is not written. |
| `materialize.sandbox_violation` | A hook attempted an action its sandbox profile does not permit. |
| `materialize.untranslatable` | The selected harness adapter cannot translate the artifact's type, mode, or one or more of its fields onto that harness (a §6.7.1 ✗ cell). Use `harness: none` for raw output, or `target_harnesses:` to opt the artifact out of that harness. For example, a plugin-layout type (`skill`, `agent`, `command`, `rule`, `hook`, `mcp-server`) on `claude-cowork` fails on both `podium sync` and `load_artifact`. Cowork has no project-scope surface, so the artifact ships through the published Claude marketplace instead. |

#### materialize.signature_invalid causes

- The content was tampered with, or the signer is unknown.
- A Sigstore-keyless envelope does not verify or match: its certificate identity, OIDC issuer, timestamp, inclusion proof, checkpoint signature, or log entry fails, or its certificate was not valid at the timestamp's time.
- `podium-mcp`, server-source `podium sync`, or a language SDK, under any policy above `never`, received a signature that does not verify under the key set it resolved.
- The same consumers, under any policy above `never`, received a malformed `delivery_signature` envelope. An envelope is malformed when it is not a JSON object, has no string `signature`, has a non-string `key_id`, or has a `signature` that is not the canonical base64 encoding of 64 bytes.
- The registry's [stored-row admission check](#stored-row-admission-refusals) refused the row.

Under `never`, a consumer does not decode the envelope and returns neither refusal.

#### materialize.content_hash_mismatch causes

The §4.7.10 delivery check runs in `podium-mcp`, server-source `podium sync`, and the language SDKs. The consumer stops before anything is cached, returned, or written (§4.7.10, §6.6). The code applies when any of these holds:

- The received record does not reproduce the `delivery_hash` the registry served.
- The response carries no `delivery_hash`.
- A `load_artifact` response or batch item fails §4.7.10 steps 1 to 4: the JSON rule, the typed members, canonical base64, or distinct resource paths.
- A fetched manifest document, large-resource body, or inline batch body fails the §4.7.10 step 6 check against its `content_hash`.
- `podium-mcp` received a `resources_base64` value that is not canonical base64.
- The registry's [stored-row admission check](#stored-row-admission-refusals) refused the row.

A language SDK returns a batch item that fails the check with `status: "error"` and this code. [Verifying a response](http-api#verifying-a-response) states each step.

#### materialize.stale_resolution details

The load stops before anything is cached or written (§4.7.10, §6.5).

- **Reference value.** The `reference_revision` detail is the value the answer was compared with: the first revision accepted in the same session, or the revision mark. The comparison is per registry and artifact.
- **Scope.** A request that names a version, a range, or a content hash is never compared with the revision mark.
- **Retry.** The error is not retryable, and a retry in the same session repeats it.
- **Lagging replica or pinned session.** When the refusal came from a lagging replica while the registry was in read-only mode, or from a session pinned to an older version, start a new session. It loads the current `latest` once the registry is out of read-only mode. Pass a new `session_id`, or restart `podium-mcp` when the load passes none.
- **Revision rollback.** Reset the revision marks after any of these events: the registry is restored from an earlier backup, a lagging replica is promoted, or the newest version is purged, erased, retired under the [no-inheritance rule](../deployment/content/layers#inheritance), or leaves the caller's view. Stop every MCP server that uses the cache directory, then run `podium cache reset-revisions <id>`.

#### Stored-row admission refusals

The registry runs a stored-row admission check (§13.4) before it serves a row. When the check refuses a row, the registry returns one of the codes below:

- On `load_artifact`, with HTTP 500.
- On each `artifacts:batchLoad` item, as the item's error envelope (`status: "error"`, `retryable: false`) inside the batch's 200 response.

The refusal is not retryable on either path, and its message names only the requested artifact.

| Code | Row the check refuses |
|:--|:--|
| `materialize.signature_invalid` | On a signing registry, a row whose stored signature does not verify under the registry's key. |
| `materialize.signature_missing` | On a signing registry, a row stored without a signature. |
| `materialize.content_hash_mismatch` | A row whose stored bytes do not reproduce its stored content hash, or whose bundled-resource body is missing or does not match its stored hash and size. |

### mcp.*

| Code | When |
|:--|:--|
| `mcp.unsupported_version` | Host and MCP server can't agree on a compatible MCP protocol version. |
| `mcp.client_too_old` | The host caller's reported version is below the minimum the MCP binary serves. Update the host. |

### network.*

| Code | When |
|:--|:--|
| `network.registry_unreachable` | The MCP server or an SDK cannot reach the registry. The MCP server holds a content cache, so its `always-revalidate` mode returns this on a fresh-call miss, while `offline-first` serves cached results without raising. The SDKs hold no cache, so both modes raise. |
| `network.offline_cache_miss` | `offline-only` cache mode was asked for something the local cache does not hold, and the mode forbids contacting the registry. |

### quota.*

For `quota.search_qps_exceeded`, `quota.materialize_rate_exceeded`, and `quota.audit_volume_exceeded`, the limit is the tenant's own value. A zero tenant value takes the deployment default, and a negative tenant value disables the limit.

| Code | When |
|:--|:--|
| `quota.storage_exceeded` | Per-tenant storage limit hit. |
| `quota.search_qps_exceeded` | Per-tenant search QPS limit hit. |
| `quota.materialize_rate_exceeded` | Per-tenant materialization rate limit hit. The registry answers `load_artifact` with HTTP 429. On each `artifacts:batchLoad` item the rate refuses, it returns the code as the item's error envelope (`status: "error"`, `retryable: true`) inside the batch's 200 response. |
| `quota.audit_volume_exceeded` | Per-tenant audit volume limit hit. |
| `quota.layer_count_exceeded` | A user has hit the per-identity user-defined-layer cap. The rejected layer is not created. |
| `quota.artifact_count_exceeded` | Ingest would push the tenant past its artifact-count quota. The artifact is rejected. |

### registry.*

| Code | When |
|:--|:--|
| `registry.read_only` | The Postgres primary is unreachable, and the registry has fallen back to read-only mode. Write endpoints (ingest, layer admin, admin grants, tenant management) are rejected. Read endpoints continue to serve from the replica. |
| `registry.tenant_management_unavailable` | A `/v1/admin/tenants` request (or a `podium admin tenant` command) reached a registry that serves no tenant management. That is a single-tenant registry started without `PODIUM_MULTI_TENANT`. To manage tenants, start the registry in multi-tenant mode with `PODIUM_MULTI_TENANT` and an identity provider the registry verifies. |
| `registry.invalid_argument` | A request argument failed validation. Examples are `top_k > 50`, or an outbound webhook receiver URL that is non-`https` or resolves to a loopback, link-local, or private target outside `PODIUM_WEBHOOK_ALLOWED_TARGETS`. Both the SDK (client-side) and the registry (server-side) enforce validation. Most endpoints also return it for an unsupported HTTP method. |
| `registry.method_not_allowed` | `load_artifact` was called with a method other than `GET` or `HEAD`. |
| `registry.not_found` | The named artifact, layer, webhook receiver, object, or tenant quota record does not exist. Returned as `404`. |
| `registry.tenant_not_found` | A tenant lookup or write named an ID that is not provisioned. Returned as `404` by `PATCH` and `DELETE /v1/admin/tenants/{id}`. |
| `registry.unavailable` | The registry could not complete the request against its metadata store, object store, or another dependency. Returned as `500`. |
| `registry.unknown` | A batch-load item failed for a reason that maps to no more specific code. Carried in that item's error envelope. The batch itself stays `200`. |

### visibility.*

| Code | When |
|:--|:--|
| `visibility.denied` | The caller lacks visibility for the artifact, or a load grant does not cover the resolved record. The response mirrors a not-found result so it does not leak that a hidden artifact exists. A `load_artifacts` batch item carries this code. A single `load_artifact` call answers `404 registry.not_found`. |

---

## Adding namespaces

Custom plugins (extension types, source providers, harness adapters) can register their own error namespaces through the SPI. Plugin-registered codes follow the same envelope structure and are documented alongside the plugin.
