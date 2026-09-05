---
title: HTTP API
nav_order: 2
description: "The Podium registry's HTTP/JSON API: discovery, materialization, layer management, ingest webhooks, scope preview, and health."
---

# HTTP API

The Podium registry exposes an HTTP/JSON API. Every consumer speaks this API: the MCP server, the language SDKs, `podium sync` against a server, and the read CLI. Direct MCP access to the registry is not supported; the MCP server is a consumer surface that translates HTTP responses into MCP messages.

---

## Authentication

Every call carries an OAuth-attested identity. The registry validates the JWT signature, reads claims (`sub`, `email`, `groups`), and composes the caller's effective view.

| Header | Value |
|:--|:--|
| `Authorization` | `Bearer <jwt>` |

The registry resolves the caller through the provider named by its `PODIUM_IDENTITY_PROVIDER`. The registry-process values are `injected-session-token`, `oidc-jwt`, and `trusted-headers`.

- `injected-session-token`: runtime-issued signed JWT. The deployment configures the registry to trust the runtime's signing key at startup through `PODIUM_RUNTIME_KEYS_PATH`, which names a file written with `podium admin runtime register --keys-file`, and the registry verifies the signature on every call.
- `oidc-jwt`: an IdP-signed token the registry verifies itself. The registry verifies the signature, `iss`, and `aud` against the issuer's JWKS. The token reaches the registry in one of two locations: in the configured token header, whether a gateway forwarded it or a CLI, an SDK, or another API client presents one it acquired through the device-code flow, or in the `__Host-podium_session` cookie, where the registry obtained it for a browser through the sign-in routes below. The registry reads the configured token header first, and it reads the cookie only where that header carries no `Bearer` credential and the browser flow is enabled on that registry. A request that carries a bearer credential in neither location is anonymous rather than rejected and sees public visibility only.
- `trusted-headers`: an authenticating reverse proxy asserts the identity in `X-Podium-User-Sub`, `X-Podium-User-Email`, `X-Podium-User-Groups`, and `X-Podium-User-Org`, optionally authenticated with `X-Podium-Proxy-Secret`.

`oauth-device-code` is the consumer-side provider. The MCP server and the SDKs acquire the token through an interactive device-code flow on first use, cache it in the OS keychain, and refresh it transparently. The registry ships no request-time verifier for it, so setting it on the registry process aborts startup with `config.identity_provider_unverified`.

In public-mode deployments, the OAuth flow is skipped; the registry serves anonymously. The audit log records `caller.identity = "system:public"`.

### Browser session

Where the registry serves the web UI and the browser flow is enabled (`--web-ui-auth` / `PODIUM_WEB_UI_AUTH`), the registry signs the browser in itself. It redirects the browser to the identity provider, performs the authorization-code exchange server-side, and returns the resulting IdP-signed token to the browser in a cookie. That token is the same credential `oidc-jwt` accepts in the configured token header, so the browser flow adds no credential kind and the registry keeps no session record. The flow sets the two cookies below and no others. Both carry the `__Host-` prefix, `HttpOnly`, `Secure`, `Path=/`, and `SameSite=Lax`, so neither is readable from the page.

| Cookie | Carries | Lifetime |
|:--|:--|:--|
| `__Host-podium_session` | the access token the callback obtained | the token's own `exp`, so the cookie carries no `Max-Age` |
| `__Host-podium_auth` | the single-use pre-authorization transaction: the `state` and the PKCE `code_verifier` the sign-in route minted | the configured transaction TTL (`PODIUM_WEB_UI_AUTH_TRANSACTION_TTL`, 10 minutes by default) |

The registry registers the three routes below only where the browser flow is enabled. A registry that boots with the flow disabled serves none of them, and a request for one of those paths is answered as any path the registry does not register is answered on that deployment.

```
GET  /v1/ui/auth/sign-in     mint the transaction and redirect to the identity provider
GET  /v1/ui/auth/callback    exchange the authorization code and set the session cookie
POST /v1/ui/auth/sign-out    clear both cookies
```

`GET /v1/ui/auth/sign-in` mints the `state` and the PKCE `code_verifier` for one transaction, returns both in `__Host-podium_auth`, and redirects the browser to the configured authorization endpoint.

`GET /v1/ui/auth/callback` compares the returned `state` against `__Host-podium_auth` before inspecting anything else in the query. A callback whose cookie is absent, expired, or carries a different `state` is refused with `403 auth.csrf_invalid`. A query carrying the identity provider's `error` parameter runs no exchange, returns the browser to `/app/` without establishing or replacing a session, and takes no error code. Otherwise the callback exchanges the code at the configured token endpoint: an identity provider the registry cannot reach, and one whose token endpoint answers with a `5xx`, are each refused with `500 registry.unavailable`, and an exchange the identity provider answers and refuses is refused with `502 auth.exchange_failed`. On success the callback returns the access token in `__Host-podium_session`. Every response the callback emits clears `__Host-podium_auth`, which is what makes the transaction single-use.

`POST /v1/ui/auth/sign-out` clears both cookies on every request it serves.

**Browser-origin gate.** The registry refuses a state-changing request that carries cross-site browser-origin evidence with `403 auth.csrf_invalid`, before the handler runs and whatever credential authenticated the request. A request is state-changing when its method is other than `GET`, `HEAD`, or `OPTIONS`. Browser-origin evidence is cross-site when the request carries a `Sec-Fetch-Site` header whose value is other than `same-origin` or `none`, or an `Origin` header whose host and port differ from the host and port the request's own `Host` header names. The scheme is not compared, because an HTTP `Host` header carries none and a registry behind a TLS-terminating gateway cannot observe the browser-facing one. A state-changing request carrying neither header carries no browser-origin evidence and is admitted, which is what a CLI, an SDK, or any other non-browser client sends. The sign-in and callback routes sit outside the gate: each answers on `GET`, and a browser that already holds `__Host-podium_session` sends it on both, so a gate covering them would refuse every re-sign-in. The gate reads the request rather than the deployment, so it runs on a registry that enables no browser flow and on one that serves no web UI.

### Session posture

```
GET /v1/ui/session
```

Reports the deployment's identity posture, the caller's own resolved subject, and what that caller may do on the layer operations in [Layer management](#layer-management). The registry registers it wherever it serves the web UI, whether or not the browser flow is enabled. It requires no credential and refuses no request for lack of one; a request that carries one has it verified so the response can report `subject` and `email` and evaluate `layer_capabilities`, and for no other purpose, and a request that resolves no subject is answered `200` with `subject` absent.

```json
{
  "identity_provider_configured": true,
  "public_mode": false,
  "browser_auth": {
    "enabled": true,
    "sign_in_path": "/v1/ui/auth/sign-in",
    "sign_out_path": "/v1/ui/auth/sign-out"
  },
  "subject": "alice@acme.com",
  "email": "alice@acme.com",
  "layer_capabilities": {
    "manage_any_layer": false
  }
}
```

`identity_provider_configured` reports whether an identity provider is configured and never names which one. `public_mode` reports whether public mode is engaged. `browser_auth.enabled` reports whether the browser flow is enabled on this deployment, and `sign_in_path` and `sign_out_path` are present only when it is, because the flow's routes are registered only then. `subject` is the verified subject of the request that asked, present only when one resolves. `email` is the requesting caller's own email as the configured identity provider recorded it, present only where one resolves and is non-empty, and absent otherwise. It belongs to the caller that asked and to no other caller.

`layer_capabilities` reports what the requesting caller may do on the layer operations. It carries `manage_any_layer`, a boolean reporting whether this deployment's layer endpoints admit this caller on the `admin` arm, which is the arm that decides a write on a layer the caller does not own and every operation the [local-source authorization rule](#layer-management) governs. On a registry started with no identity provider configured, or one started in public mode, those endpoints admit every caller on that arm, so the member is true there, including on a request that resolves no subject. The object and its member are always present, and where the deployment determines no capability for the request the member is false. The object predicts a server decision rather than reporting a grant: it is a snapshot taken when the read was answered, an operation a client offers on the strength of it can still be refused, and the envelope the operation's own endpoint returns remains the authority.

The response carries no other field, and in particular no issuer, client identifier, endpoint, filesystem path, or other configuration value, and no subject, email, or authorization belonging to any caller other than the one that asked. A registry started without the web UI never registers this path and answers a request for it as it answers any path it does not register.

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
GET /v1/load_domain?path={path}&depth={n}
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
GET /v1/search_domains?query={q}&scope={path}&top_k={n}
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
GET /v1/search_artifacts?query={q}&type={type}&tags={tag1},{tag2}&scope={path}&top_k={n}&session_id={uuid}&as_admin={bool}
```

Hybrid retrieval over artifact frontmatter. Every argument is optional. When `query` is omitted, the endpoint returns artifacts matching the filters in default order, the browse call. `top_k` defaults to 10.

`as_admin=1` (or `as_admin=true`) requests the admin diagnostic visibility override, which searches across every layer regardless of the caller's visibility. A caller without the admin role is rejected with `403 auth.forbidden`.

Each result's `frontmatter` is the artifact's stored YAML frontmatter as a string. For an artifact that declares `extends:`, the `extends` key is removed before the block is returned, so the result does not surface the parent. That block is re-encoded from the remaining keys, which normalizes comments, quoting, and indentation, and the result carries no `frontmatter` key when the child's stored frontmatter cannot be read, rewritten, or re-encoded, or when the rewritten block still resolves a parent, which is the case for a child that supplies `extends:` through a YAML merge key or anchors its value.

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
GET /v1/load_artifact?id={id}&version={v}&session_id={uuid}&as_admin={bool}
```

`version` is optional (default `latest`). `session_id` is optional; the first `latest` lookup within a session is recorded and reused for subsequent same-id lookups in the session, so the host sees a consistent snapshot. `as_admin=1` (or `as_admin=true`) requests the admin diagnostic visibility override; a caller without the admin role is rejected with `403 auth.forbidden`.

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

**Layer write authorization.** The write operations in this section, meaning `POST /v1/layers`, `DELETE /v1/layers`, `/v1/layers/update`, `/v1/layers/restore`, `/v1/layers/reorder`, and `/v1/layers/reingest`, are gated. On a stored layer that is user-defined, the operation is authorized to that layer's stored `owner` or to a caller holding the per-tenant `admin` role. On a stored layer that is admin-defined, it is authorized to a tenant admin alone, whatever that layer's stored `owner` field names, because on an admin-defined layer that field is supplied by the requesting caller and names no authorized subject. On `POST /v1/layers` the gate applies when the request's `id` names a layer that already exists in the tenant, and the arm taken is decided by the stored layer's class and stored owner rather than by anything in the request body; a layer that is soft-deleted and still inside its recovery window is a layer that exists for this rule. A registration whose `id` names no stored layer is authorized to a caller the admin arm admits or to a caller who resolves a verified subject, and where that registration resolves to a user-defined layer and a subject resolves, the stored `owner` is that subject. A caller authorized by neither arm is refused with `403 auth.forbidden`, whether that caller resolves a different subject or resolves none at all. On `POST /v1/layers/reorder` a caller whose credential fails verification under the configured identity provider's rule is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either authorization arm is evaluated, so on that operation `auth.forbidden` names a caller the registry verified and did not authorize; the other layer write operations answer such a caller `auth.forbidden` as before. A registration whose existence lookup fails is refused with `500 registry.unavailable` and writes nothing. A registry started with no identity provider configured, or one started in public mode, authenticates no caller, so no caller can hold the admin role or resolve as an owner and these endpoints admit the request there.

**Local-source authorization.** Registering a layer whose source type is `local` or whose registration names a filesystem path on the registry host, patching a stored layer's filesystem path, restoring a stored layer that names one, and reingesting one are authorized to a caller holding the per-tenant `admin` role. A registry started with no identity provider configured, or one started in public mode, authenticates no caller, so no caller can hold the admin role and these operations are admitted there, on the same reading the layer write authorization rule above states for its own arms. Any other caller is refused with `403 auth.forbidden` carrying `details.constraint: "local_source"`, and the refusal names no filesystem path. The rule applies to a user-defined and to an admin-defined layer alike, and it is evaluated on each of those operations rather than against the stored layer list, so a layer stored before this rule was in force is refused at its next such operation rather than at startup. An inbound webhook delivery triggers a reingest and is governed by this rule on the same arm: the delivery carries the per-layer secret rather than a caller the registry can place on the admin arm, so on a registry that authenticates its callers a webhook-triggered reingest of a layer that names a filesystem path is refused. `DELETE /v1/layers` and `POST /v1/layers/reorder` name no filesystem path and re-read none, so the rule does not reach them. A `git` source whose `repo` names a network endpoint is fetched through a network transport and yields tree objects rather than host files, so the rule does not reach it; a `git` source is placed on this rule's arm by its `repo` string alone, so a `local_path` stored beside a `git` source never places a restore, a reingest, or a webhook-triggered reingest on the arm. A `git` source whose `repo` resolves to the Git file transport names a repository path on the registry host and takes the same arm, and a `repo` string the registry cannot place as a network endpoint is treated as naming a host path.

**The layer object.** Every endpoint in this section that returns a layer returns the same object, with lower snake_case field names:

| Field | Type | Notes |
|:--|:--|:--|
| `id` | string | The layer identifier, unique within the tenant. |
| `source_type` | string | `git` or `local`. |
| `repo` | string | The Git remote for a `git` source. |
| `ref` | string | The tracked ref for a `git` source. |
| `root` | string | An optional subpath within the source. |
| `local_path` | string | The filesystem path on the registry host for a `local` source. |
| `order` | number | Precedence within the tenant; a lower value composes first. |
| `user_defined` | boolean | True for a personal layer, false for an admin-defined one. |
| `owner` | string | The verified subject that owns a user-defined layer. |
| `public` | boolean | Visibility. |
| `organization` | boolean | Visibility. |
| `groups` | array | Visibility. `null` when the layer grants no group. |
| `users` | array | Visibility. `null` when the layer grants no user. |
| `git_provider` | string | The provider whose signature scheme verifies this layer's inbound deliveries. Empty resolves to `github`. |
| `force_push_policy` | string | `tolerant` or `strict`. Present on a layer that sets a policy and absent on one that does not. |
| `last_ingested_at` | string | RFC 3339 timestamp of the most recent completed ingest. Absent until the layer has ingested once. |
| `last_ingested_ref` | string | The commit SHA of the most recent completed ingest for a `git` source. |
| `created_at` | string | RFC 3339 timestamp of the layer's registration. |
| `deleted_at` | string | The soft-delete tombstone, or `null` on a live layer. A reader computes the remaining recovery window from it. |

The object carries no tenant identifier, because these endpoints serve one tenant. It carries the layer's inbound webhook HMAC secret under no name: that credential is returned once, in the `webhook_secret` field of the registration response and of an update that requests a rotation. Timestamps are RFC 3339 in UTC.

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
  "git_provider": "github",
  "groups": ["acme-finance"]
}
```

`id` and `source_type` are required. Visibility is set with the top-level `public`, `organization`, `groups`, and `users` fields. `git_provider` names the Git provider whose webhook signature scheme verifies this layer's inbound deliveries; it applies to a `git` source alone, and a value naming no registered provider, or any value on a `local` source, is refused with `400 registry.invalid_argument`. Omitting it resolves the layer to `github`. A request whose `id` names a layer that already exists in the tenant is a write against that layer and is authorized against it under the rule above, so a caller neither arm authorizes is refused with `403 auth.forbidden` rather than overwriting it.

A registration also falls under the local-source rule above when its `source_type` is `local`, when it carries a `local_path` and its `source_type` is not `git`, or when its `repo` resolves to the Git file transport. A `git` registration is placed by its `repo` string alone, so a `local_path` sent beside a `git` `repo` naming a network endpoint does not place it on the arm. A registration the rule places on the arm and whose caller does not hold the `admin` role is refused with `403 auth.forbidden` carrying `details.constraint: "local_source"`.

`owner`, `public`, `organization`, `groups`, and `users` are read on a tenant admin's registration alone. A caller without the `admin` role that asserts any of them is refused with `403 auth.forbidden` carrying `details.constraint: "admin_only_fields"`, and the refusal names the asserted fields in its message. A field is asserted by its value rather than by its presence: `public` or `organization` carrying false, an empty `groups` or `users`, an empty `owner`, and an `owner` naming the caller's own verified subject assert nothing. A registration that asserts none of them is resolved to a user-defined layer owned by the caller, as before. A registry started with no identity provider configured, or one started in public mode, authenticates no caller and admits every caller on the admin arm, so the rule refuses nothing there. The registry evaluates this rule after the layer write authorization rule and after the local-source rule, so a registration either of those refuses keeps its own envelope: the layer write refusal carries a bare `403 auth.forbidden` with no `details.constraint`, including for a caller who resolves no subject, and a registration on the local-source arm carries `details.constraint: "local_source"`. The `admin_only_fields` refusal is returned only where neither earlier rule refuses.

The response is `201 Created` with the stored layer and, for a `git` source, the webhook URL and HMAC secret to register on the source repo:

```json
{
  "layer": {
    "id": "team-finance",
    "source_type": "git",
    "repo": "git@github.com:acme/podium-finance.git",
    "ref": "main",
    "root": "artifacts/",
    "local_path": "",
    "order": 10,
    "user_defined": false,
    "owner": "",
    "public": false,
    "organization": false,
    "groups": ["acme-finance"],
    "users": null,
    "git_provider": "github",
    "last_ingested_ref": "",
    "created_at": "2026-09-04T10:15:00Z",
    "deleted_at": null
  },
  "webhook_url": "https://registry.acme.com/v1/ingest/webhook/team-finance",
  "webhook_secret": "..."
}
```

The registration has not ingested yet, so `last_ingested_at` is absent, and this registration sets no force-push policy, so `force_push_policy` is absent. A `local` registration, and an update that requests no secret rotation, return the layer alone without `webhook_url` and `webhook_secret`.

### List layers

```
GET /v1/layers
```

Returns the layers the caller can read, as an array of the layer object under the `layers` key:

```json
{
  "layers": [
    {
      "id": "org-defaults",
      "source_type": "git",
      "repo": "git@github.com:acme/podium-org-defaults.git",
      "ref": "main",
      "root": "",
      "local_path": "",
      "order": 10,
      "user_defined": false,
      "owner": "",
      "public": false,
      "organization": true,
      "groups": null,
      "users": null,
      "git_provider": "github",
      "force_push_policy": "strict",
      "last_ingested_at": "2026-09-04T09:00:00Z",
      "last_ingested_ref": "9f1c2b7d4e5a6081c3d2e4f5a6b7c8d9e0f1a2b3",
      "created_at": "2026-08-30T12:00:00Z",
      "deleted_at": null
    }
  ]
}
```

A caller who can read no layer receives `{"layers":[]}`.

**Layer read visibility.** A caller holding the per-tenant `admin` role receives the tenant's whole layer list. Any other authenticated caller receives the layers that caller can see under the visibility rules, which include that caller's own user-defined layers through their implicit `users: [<registrant>]` visibility. A caller whose credential fails verification is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime`, the same refusal the registry answers on any other route that verifies the same credential. Whether presenting no credential is itself a verification failure is the configured identity provider's rule. A caller the registry resolves as anonymous rather than as a verification failure receives an empty list rather than a refusal. A layer the rule withholds is absent from the `200` response rather than refused with an error code, so the read discloses no identifier, source location, owner subject, or visibility declaration for it. A registry started with no identity provider configured, or one started in public mode, authenticates no caller, so the read returns the tenant's whole layer list there.

### Reingest

```
POST /v1/layers/reingest?id={id}
```

Forces a fresh snapshot of the layer regardless of the trigger model. Reingesting an admin-defined layer is authorized to a tenant admin, and reingesting a user-defined layer to its owner or a tenant admin, under the rule above; a caller authorized by neither arm is rejected with `auth.forbidden` before any Git fetch runs. On a layer that names a filesystem path on the registry host, the local-source rule above additionally requires the `admin` role, and any other caller is refused with `403 auth.forbidden` carrying `details.constraint: "local_source"`. The body is optional and carries a break-glass override during a freeze window:

```json
{ "break_glass": true, "justification": "...", "approvers": ["...", "..."] }
```

### Reorder layers

```
POST /v1/layers/reorder
```

Body: `{ "order": ["layer-a", "layer-b", "layer-c"] }`. The `order` array re-sequences the named layers. Reordering an admin-defined layer is authorized to a tenant admin, and reordering a user-defined layer to its owner or a tenant admin, under the rule above; a caller authorized by neither arm is rejected with `auth.forbidden`. A caller whose credential fails verification under the configured identity provider's rule is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either arm is evaluated, so here `auth.forbidden` names a caller the registry verified and did not authorize; the other layer write operations answer such a caller `auth.forbidden` as before. The response reports the same set of layers the list read reports for that caller.

### Update a layer

```
POST /v1/layers/update?id={id}
PUT  /v1/layers/update?id={id}
```

Patches the layer. A non-zero body field replaces the corresponding value; a zero field leaves it unchanged. The patchable fields are visibility (`public`, `organization`, `groups`, `users`), `ref`, `root`, `local_path`, `owner`, `git_provider`, `force_push_policy`, and a webhook-secret rotation (`rotate_webhook_secret`). A `git_provider` naming no registered provider, and any `git_provider` on a layer whose stored source type is not `git`, are refused with `400 registry.invalid_argument`. On a stored user-defined layer the `owner` and visibility fields are fixed at registration, so a patch asserting `owner`, `public`, `organization`, `groups`, or `users` against such a layer is refused with `400 registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`, and the refusal names the asserted fields. The refusal rejects the whole request, so no other field the same patch carries is applied, no webhook secret is minted, and the stored configuration is unchanged. A field is asserted by its value rather than by its presence: `public` or `organization` set to true, a non-empty `groups`, a non-empty `users` differing from the layer's stored `users`, and an `owner` naming a subject other than the layer's stored owner each assert the field, while a false boolean, an empty array, an empty string, and a value restating what the layer stores assert nothing, so a client that reads a layer object and returns it unchanged is admitted. The comparison against a stored value is exact, element for element and byte for byte, so a value differing from the stored one only in element order or in surrounding whitespace is an assertion and is refused. The rule reads the stored layer's class rather than the caller, so it binds every caller the layer write rule authorizes, a tenant admin included, and a registry started with no identity provider configured and one started in public mode refuse on the same terms. A tenant admin who needs the layer visible more widely re-registers its ID through `POST /v1/layers` as an admin-defined layer with the visibility they declare. The remaining fields apply as described. The identifying fields (`id`, `source_type`) are immutable.

A patch is classified by the fields it carries. A patch carrying `local_path` names a filesystem path on the registry host and falls under the local-source rule above, whatever the stored layer's source type, so a caller without the `admin` role is refused with `403 auth.forbidden` carrying `details.constraint: "local_source"`. The refusal rejects the whole request rather than one field of it, so the stored configuration is unchanged and no other field the same patch carries is applied. A patch carrying no `local_path` is not reached by that rule, because the handler applies neither `source_type` nor a repository string here: `source_type` is immutable and there is no patchable `repo` field. The immutable visibility rule is evaluated after the layer write authorization rule and after the local-source rule, so a patch either of those refuses keeps its own `auth.forbidden` envelope, and the `immutable_visibility` refusal is returned only where neither earlier rule refuses.

### Unregister

```
DELETE /v1/layers?id={id}
```

Soft-deletes the layer and the artifacts ingested from it, recoverable within the retention window. Unregistering an admin-defined layer is authorized to a tenant admin, and unregistering a user-defined layer to its owner or a tenant admin, under the rule above; a caller authorized by neither arm is rejected with `auth.forbidden`.

### List soft-deleted layers and restore

```
GET  /v1/layers?deleted=true
POST /v1/layers/restore?id={id}
```

`GET /v1/layers?deleted=true` lists the soft-deleted layers still inside the recovery window, filtered on the terms the layer read visibility rule under [List layers](#list-layers) states. `POST /v1/layers/restore?id={id}` clears the tombstone and recovers the layer and its artifacts. Restoring an admin-defined layer is authorized to a tenant admin, and restoring a user-defined layer to its owner or a tenant admin, under the rule above; a caller authorized by neither arm is rejected with `auth.forbidden`. On a layer that names a filesystem path on the registry host, the local-source rule above additionally requires the `admin` role, and any other caller is refused with `403 auth.forbidden` carrying `details.constraint: "local_source"`.

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
  "limits": {
    "storage_bytes": 10737418240,
    "search_qps": 20,
    "materialize_rate": 60,
    "audit_volume_per_day": 1073741824,
    "max_user_layers": 3
  },
  "usage": { "storage_bytes": 1234567 }
}
```

`limits` reports the tenant's configured budget under the same five field names `GET /v1/admin/tenants` reports for the same numbers. A zero `max_user_layers` selects the deployment-configured cap, which is 3 unless the deployment sets one, and a negative value disables the cap. A deployment that configures the cap explicitly applies it ahead of this per-tenant value, so the enforced cap on such a deployment is the configured one whatever this field reports.

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

These routes require an authenticated admin caller, resolved through the admin-grant table. The tenant-management routes are the exception: they take the instance-operator role instead. The mutating routes are rejected in read-only mode with `registry.read_only`; the read routes continue to serve.

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

A registry started with no identity provider configured, or one started in public mode, authenticates no caller, so no caller can hold the admin role and this endpoint admits the request there. The layer write endpoints admit the request on such a registry for the same reason, which the layer write authorization rule under [Layer management](#layer-management) states.

### Erase a user (GDPR)

```
POST /v1/admin/erase    body: { "user_id": "...", "salt": "..." }
```

Performs the right-to-erasure operation for the named user: it unregisters and soft-deletes every user-defined layer the user owns, redacts the user identity across the registry audit stream, and appends a `user.erased` event naming the invoking admin. Both `user_id` and `salt` are required.

### Tenant management

```
GET    /v1/admin/tenants
POST   /v1/admin/tenants        body: { "name": "...", "quota": {...}, "expose_scope_preview": true }
PATCH  /v1/admin/tenants/{id}   body: { "quota": {...}, "expose_scope_preview": true, "active": false }
DELETE /v1/admin/tenants/{id}
```

These routes are authorized by the instance-operator role rather than the per-tenant admin-grant table, and they are available only on a multi-tenant registry. A request on a single-tenant or standalone registry is rejected with `404 registry.tenant_management_unavailable`, before operator authorization. A caller without the operator role is rejected with `403 auth.forbidden`. `GET` serves in read-only mode; `POST`, `PATCH`, and `DELETE` are rejected with `registry.read_only`.

`GET` returns the tenants under the `tenants` key. Every other method returns one tenant object:

```json
{
  "id": "acme",
  "name": "Acme Corp",
  "quota": {
    "storage_bytes": 0,
    "search_qps": 0,
    "materialize_rate": 0,
    "audit_volume_per_day": 0,
    "max_user_layers": 3
  },
  "expose_scope_preview": true,
  "active": true
}
```

`POST` requires `name` and derives the tenant ID from it. Creation is idempotent: a new tenant returns `201 Created`, and re-creating an already-provisioned name returns `200 OK` with the existing tenant unchanged.

`PATCH` overlays only the keys present in the body, so an omitted key keeps its current value. A present `expose_scope_preview: null` clears the gate. `active` reactivates (`true`) or deactivates (`false`) the tenant. The name is fixed at creation and cannot be patched. `DELETE` deactivates the tenant and returns `204 No Content`; the data persists while the tenant stops resolving. A `PATCH` or `DELETE` naming an unknown tenant returns `404 registry.tenant_not_found`.

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

Single-event schema:

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

Every method on these routes requires the per-tenant admin role and returns `auth.forbidden` for a non-admin caller, because a receiver is an org-level configuration. The mutating methods are also rejected in read-only mode with `registry.read_only`. A standalone or no-auth deployment follows the same authorization path as the admin-grant endpoints: receiver registration requires an admin grant plus a token rather than remaining open.

`POST` accepts `{ "url": "...", "secret": "...", "event_filter": ["..."], "debounce": "30s", "disabled": false }` and returns `201 Created` with the receiver including its secret, so the operator can record it. The registry generates a secret when the body omits one. `url` is required. `PUT` accepts the same fields and applies the ones present; re-enabling a receiver (`disabled: false`) clears its failure counter. `GET` and `DELETE` of a single receiver address it by `id`. The list response returns the receivers under the `receivers` key. List, single-read, and `PUT` responses mask the secret as `***`. `DELETE` returns `204 No Content`. The registry wires the outbound webhook worker at startup, so these routes are mounted on every deployment. Receivers are held in memory unless `PODIUM_WEBHOOK_STORE_PATH` names a file, in which case the store reloads them on restart.

**The receiver object.** Every method that returns a receiver returns the same object, with lower snake_case field names:

| Field | Type | Notes |
|:--|:--|:--|
| `id` | string | The receiver identifier. |
| `url` | string | The delivery target. |
| `secret` | string | The HMAC secret on the creating response, and `***` on the list, the single read, and the update. |
| `event_filter` | array | The event names the receiver matches. `null` when the receiver matches every event. |
| `disabled` | boolean | Whether delivery is suspended. |
| `failure_count` | number | Consecutive delivery failures. |
| `last_delivery` | string | RFC 3339 timestamp in UTC of the last delivery attempt. |
| `last_failure` | string | RFC 3339 timestamp in UTC of the last failed delivery. |
| `created_at` | string | RFC 3339 timestamp in UTC of the receiver's registration. |
| `debounce` | string | The trailing window as a duration string, in the form the request body accepts, so a read feeds straight back into a `PUT`. Absent on a receiver that sets no window. |

```json
{
  "id": "rcv-4f2a",
  "url": "https://relay.acme.com/podium",
  "secret": "***",
  "event_filter": ["layer.ingested"],
  "disabled": false,
  "failure_count": 0,
  "last_delivery": "2026-09-04T09:00:12Z",
  "last_failure": "0001-01-01T00:00:00Z",
  "created_at": "2026-08-30T12:00:00Z",
  "debounce": "1m0s"
}
```

The object carries no tenant identifier, because these endpoints serve one tenant. A receiver that has never been delivered to, and one that has never failed, reports the zero timestamp for the corresponding field.

#### Receiver URL policy (SSRF)

The registry originates the delivery request, so it validates `url` at registration and re-checks it at delivery. By default the registry requires the `https` scheme and rejects a URL that resolves to a loopback, link-local, or private address (for example `127.0.0.0/8`, `::1`, `169.254.0.0/16`, and the RFC 1918 ranges), and it does not follow a redirect to such a target. A rejected target returns `registry.invalid_argument` with a message naming the disallowed host. A deployment whose receiver is legitimately internal, such as an in-cluster relay, sets an allowlist of permitted hosts or CIDRs through the `PODIUM_WEBHOOK_ALLOWED_TARGETS` registry config, which the validation consults.

#### Debounce and batch delivery

The optional `debounce` field is a duration string (for example `"30s"`). A receiver with `debounce` unset receives one delivery per matched event. A receiver with `debounce` set holds the events it matches in a trailing window that opens on the first matched event, deduplicates them by event type and key, and on window expiry delivers them as one batch. The key is the artifact ID for `artifact.published` and `artifact.deprecated`, the layer ID for `layer.ingested` and `layer.history_rewritten`, and the domain path for `domain.published`. The registry delivers the batch directly to that one buffered receiver and signs it with the receiver's HMAC secret, so the batch inherits the same retry, backoff, concurrency limit, and signing as a single-event delivery. The debounce buffer is in-process, and a registry restart mid-window may drop a buffered batch, consistent with the best-effort delivery the subsystem provides through retries.

The batch delivery body is an envelope:

```json
{
  "event": "batch",
  "trace_id": "...",
  "timestamp": "...",
  "window": { "start": "...", "end": "..." },
  "count": 12,
  "events": [
    { "event": "layer.ingested", "trace_id": "...", "timestamp": "...", "actor": {}, "data": { "layer": "team-shared" } }
  ]
}
```

Each element of `events` is the complete single-event body, carrying the same `event`, `trace_id`, `timestamp`, `actor`, and `data` fields as the single-event schema above. The top-level `trace_id` identifies the batch delivery, and each element's `trace_id` identifies its coalesced event. The batch body is additive and scoped to receivers that set `debounce`; the single-event body is unchanged for a receiver without a debounce window.

#### Triggering GitHub CI from a receiver (repository_dispatch relay)

A receiver cannot call GitHub's `repository_dispatch` endpoint directly: the registry signs each delivery with the receiver's HMAC secret (the `secret` above), and that signed body differs from the body the GitHub dispatch endpoint accepts. So an operator relay bridges the two. Register a receiver filtered to `layer.ingested` whose `url` points at the relay; the relay verifies the HMAC against the receiver secret and then issues `POST https://api.github.com/repos/<owner>/<repo>/dispatches` with a GitHub token and `{"event_type":"podium-layer-ingested"}`. The CI workflow listens on `repository_dispatch` and runs `podium sync --config`. Set the receiver's `debounce` field to coalesce a burst of `layer.ingested` events into one batch delivery and one CI dispatch; with `debounce` unset the relay fires per event, and the CI system's concurrency control collapses the burst. Registering the receiver requires the admin role on these CRUD endpoints. See [Marketplace publishing](../consuming/publishing) for the worked patterns.

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

A Prometheus scrape endpoint. Mounted by default; `PODIUM_METRICS=false` removes the endpoint and the per-request recording.

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

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to read-only mode. Read endpoints continue to serve from the replica; write endpoints (ingest webhooks, layer admin operations, freeze toggles, admin grants, and tenant management) are rejected with `registry.read_only`.

Read responses carry two headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>`: observed replication lag.

