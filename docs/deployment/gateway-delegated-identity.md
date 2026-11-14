---
title: Gateway-delegated identity
nav_order: 8
description: Run the registry behind a gateway that authenticates the caller, using the oidc-jwt and trusted-headers identity providers.
---

# Gateway-delegated identity

This page covers running a registry behind a gateway that has already authenticated the caller. It is for operators of [single-node](single-node) and [clustered](clustered) deployments who front the registry with an OIDC ingress, an OAuth2 proxy, an identity-verifying sidecar, or a non-OIDC corporate SSO.

## How it works

Two registry-side identity providers let the registry consume the identity a gateway supplies and filter layer visibility by it. The registry runs no device-code flow of its own in this arrangement.

| Value | Behavior | Use when |
| --- | --- | --- |
| `oidc-jwt` | Verifies a gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request. | The gateway can forward a verifiable OIDC token. |
| `trusted-headers` | Trusts gateway-injected identity headers without verifying them. | The gateway authenticates by SAML or a bespoke SSO and forwards identity as headers alone. |

Prefer `oidc-jwt` where the gateway can forward a verifiable token. It trusts the issuer's signing key alone and no element of the network path, so the registry can be directly reachable without an authentication bypass. `oidc-jwt` also serves a directly reachable registry, where the registry verifies the caller's token itself. `trusted-headers` requires the fronting gateway.

These rules apply to both providers:

- The registry's `PODIUM_IDENTITY_PROVIDER` selects the provider.
- A Podium client behind the gateway sends no credential of its own, because the gateway supplies the identity.
- The MCP server's `PODIUM_IDENTITY_PROVIDER` admits only `oauth-device-code` and `injected-session-token`, and it rejects these two values.
- Both providers apply on a single-node or a clustered deployment, and both are mutually exclusive with public mode.

## oidc-jwt

The gateway forwards the caller's IdP-signed JWT to the registry. The registry verifies the token on every request against the issuer's JWKS, which it resolves from the issuer's OIDC discovery document.

The same provider covers a directly reachable registry. There, a CLI, an SDK, or another API client presents a token it acquired itself through the device-code flow. Where that registry enables the browser flow, a browser presents the token that the registry obtained for it through its own authorization-code exchange and returned in `__Host-podium_session`. The verification path is identical, because the registry checks the token rather than the network path. The [OIDC cookbooks](oidc/) configure that arrangement per IdP.

### Configure oidc-jwt

1. Configure the gateway to forward the caller's IdP-signed JWT as `Bearer <token>` in the header the registry reads. The header is `Authorization` by default, or a custom one such as `X-Forwarded-Access-Token`. The gateway does not need to strip client-supplied tokens, because a forged token fails verification.
2. Add the `identity_provider:` block to `registry.yaml`. Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config, and the registry ignores it without reporting an error.

```yaml
# registry.yaml  (single-node or clustered server, fronted by a gateway)
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://acme.okta.com/oauth2/default   # must be https
    audience: https://podium.acme.com
    # audience:                                # or a list, when one registry answers to several values
    #   - https://podium.acme.com
    #   - 8e1f0c3a-podium-cli
    token_header: Authorization   # default; value parsed as "Bearer <token>" for any header name
    jwks_cache_ttl_seconds: 300   # default
    # subject_claim: idsub                       # AD FS; default: sub
    # groups_claim: http://schemas.microsoft.com/ws/2008/06/identity/claims/groups   # AD FS; default: groups
```

3. Set `issuer` and `audience` for the IdP. The [settings table](#oidc-jwt-settings) lists every key and its environment override.
4. Restart the registry.

The registry then accepts each request that carries a valid token from the configured issuer, and resolves the caller's identity and groups from its claims. [Request outcomes](#request-outcomes) lists what happens to a request that does not.

### oidc-jwt settings

| Setting | Environment override | Default | Notes |
| --- | --- | --- | --- |
| `identity_provider.issuer` | `PODIUM_OAUTH_ISSUER` | required | Must use `https`. The registry derives the JWKS from `<issuer>/.well-known/openid-configuration`. |
| `identity_provider.audience` | `PODIUM_OAUTH_AUDIENCE` | required | Audience values this registry answers to. A token is accepted when its `aud` claim carries at least one configured value. The config-file key takes a string or a list of strings, and the environment variable takes a comma-separated list. |
| `identity_provider.token_header` | `PODIUM_OAUTH_TOKEN_HEADER` | `Authorization` | Header carrying the forwarded JWT, parsed as `Bearer <token>` for any header name. |
| `identity_provider.jwks_cache_ttl_seconds` | `PODIUM_OAUTH_JWKS_CACHE_TTL_SECONDS` | `300` | A `kid` absent from the cached set forces an earlier refresh. |
| `identity_provider.subject_claim` | `PODIUM_OAUTH_SUBJECT_CLAIM` | `(unset; sub)` | Claim read as the caller's subject. When it is set the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. AD FS access tokens carry `idsub` and no `sub`. |
| `identity_provider.groups_claim` | `PODIUM_OAUTH_GROUPS_CLAIM` | `(unset; groups)` | Claim read for group membership. AD FS issuance rules emit the full claim-type URI (`http://schemas.microsoft.com/ws/2008/06/identity/claims/groups`) unless authored with a short name. Single-value and multi-value forms are both accepted. |

### Multiple audiences

A deployment lists more than one audience value when the same registry is addressed by more than one audience from one trusted issuer.

- The OAuth device authorization grant is the case this covers. That request carries `client_id` and `scope` and cannot name a target resource. An IdP that stamps the client identifier on the resulting access token therefore issues a different `aud` than it issues for a token exchange that names the registry's API URI.
- Each listed value is a trust decision the operator makes at startup. The set never grows at runtime.
- The audience decides whether a token is verified at all, and it does not narrow what a caller may do. A caller admitted under any listed value has the same effective view, the same grants, and the same [audit](observability#audit-log) identity. A deployment that needs two entrances to one catalog to differ in privilege runs two registries.
- A listed value that another resource server also accepts admits every token that the issuer mints for that server. A client identifier shared across applications has the same effect. List a client identifier only where it is assigned to a single application.
- A comma is legal in a URI and cannot be written in the environment form, which splits on commas. Write such a value in the config file, where a string is one audience verbatim.
- A client process that shares the registry's environment reads `PODIUM_OAUTH_AUDIENCE` as the one value it asks for, and it sends the comma-separated list verbatim. Give a co-located client its own `--audience` or its own environment.
- The registry asks the IdP for the first configured value when it initiates a flow itself, which is the browser sign-in redirect described under [Web UI](#web-ui). A deployment that enables that flow lists the value its browser client is issued first. The order of the remaining entries does not affect verification.

### Group membership under oidc-jwt

The registry reads group membership from the token's `groups` claim on every verified token. When the IdP emits membership under another claim name, `identity_provider.groups_claim` (`PODIUM_OAUTH_GROUPS_CLAIM`) names that claim. The `IdpGroupMapping` adapter rewrites the values the claim carries to registry-side group names, and it names no claim itself.

A registry that also resolves membership through SCIM 2.0 push matches SCIM-resolved membership in addition to the claim-derived groups. The registry mounts the SCIM receiver at `/scim/v2/` when `PODIUM_SCIM_TOKENS` names at least one bearer token. That condition depends on the variable alone and applies to single-node and clustered deployments alike. `PODIUM_SCIM_STORE_PATH` persists the pushed directory across restarts. A set path that the registry cannot read, parse, or write refuses startup with `config.scim_store_unavailable`.

### Issuer matching

The registry accepts a token's `iss` when it matches the configured issuer, or the `access_token_issuer` published by the discovery document that the issuer resolves.

AD FS needs the second form. It serves discovery under `https://<host>/adfs` and stamps the federation-service identifier `http://<host>/adfs/services/trust` on the access token, so no single configured value covers both roles. The signing keys still come from the `jwks_uri` in that same `https` document, so the set of trusted keys is unchanged. When the document publishes an `access_token_issuer` that differs from the configured issuer, the registry logs both accepted values at startup.

### Subject claim

A deployment that sets `identity_provider.subject_claim` lists values of the named claim in its `users:` entries, in `PODIUM_OPERATOR_ADMINS`, and in `PODIUM_BOOTSTRAP_ADMINS`. The instance-operator grant and the per-tenant admin grants match the recorded subject and have no email fallback. A `sub` value left in either variable grants nothing once the registry records another claim as the subject. The named claim must also identify one principal for the life of the deployment.

### Request outcomes

| Request | Outcome |
| --- | --- |
| The token fails signature, `iss`, or `aud` validation | Rejected with `auth.untrusted_token`. |
| The token has expired | Rejected with `auth.token_expired`. |
| The configured token header carries no `Bearer` credential | Anonymous, and sees public visibility only. |
| The issuer's JWKS is unreachable | Verification fails closed, and the request is anonymous rather than rejected. |

Where the registry enables the browser flow (see [Web UI](#web-ui)), a request without a `Bearer` credential is anonymous only when it also presents no valid `__Host-podium_session` cookie. The registry still reads the configured token header first. The gateway-fronted deployment this page configures enables no browser flow, so the registry reads no cookie there.

## trusted-headers

The gateway authenticates the caller by any means and injects the resolved identity as request headers. The registry reads the headers without verifying them.

| Header | Carries |
| --- | --- |
| `X-Podium-User-Sub` | The caller's OIDC subject. |
| `X-Podium-User-Email` | The caller's email. |
| `X-Podium-User-Groups` | The caller's groups, comma-separated. |
| `X-Podium-User-Org` | The caller's organization (a multi-tenant registry routes by this value). |
| `X-Podium-Proxy-Secret` | The shared secret matched against `PODIUM_TRUSTED_PROXY_SECRET`. |

### Configure trusted-headers

1. Configure the gateway to authenticate the caller.
2. Configure the gateway to remove any client-supplied `X-Podium-User-*` headers.
3. Configure the gateway to set the identity headers from the authenticated session.
4. Set `PODIUM_TRUSTED_PROXY_SECRET` on the registry, and configure the gateway to attach the same value as `X-Podium-Proxy-Secret`. [Bind restriction](#bind-restriction) states when the secret is required.
5. Add the `identity_provider:` block to `registry.yaml`:

```yaml
# registry.yaml  (single-node or clustered server, fronted by a gateway)
registry:
  identity_provider:
    type: trusted-headers
    # The proxy secret is read from PODIUM_TRUSTED_PROXY_SECRET and has no config-file key.
```

6. Restart the registry.

The registry then resolves each caller from the injected headers. A request without identity headers is anonymous and sees public visibility only, because `trusted-headers` raises no authentication error.

### Group membership under trusted-headers

Groups come from `X-Podium-User-Groups` directly. `IdpGroupMapping` is not consulted, because there is no token to read and the gateway is the source of truth. Provision groups at the gateway for a `trusted-headers` deployment.

A directory pushed to the SCIM receiver resolves no layer visibility under this provider. A `groups:` filter matches the `X-Podium-User-Groups` value alone, and a SCIM membership naming the caller's `X-Podium-User-Sub` or `X-Podium-User-Email` value grants nothing. Setting `PODIUM_SCIM_TOKENS` still mounts the receiver, which keeps accepting and serving pushes. The registry reports the withheld expansion at startup with the line `SCIM group expansion in layer visibility: false (identity provider "trusted-headers")`. To let directory membership grant visibility, provision that membership at the gateway, or run `oidc-jwt`, where the registry resolves groups from a credential it verifies.

### Bind restriction

`trusted-headers` reads identity from headers it cannot verify, so the identity it trusts is exactly the set of clients that can reach the bind address. The registry constrains the bind at startup.

| Registry | Rule |
| --- | --- |
| Single-tenant | A loopback bind (`127.0.0.0/8`, `::1`) is always allowed. A non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set. |
| Multi-tenant | `X-Podium-User-Org` selects among tenants, and a co-resident process can reach a loopback bind. The proxy secret is therefore required on every request regardless of bind address, and an unset secret fails to start with `config.trusted_headers_multitenant_no_secret`. |

The proxy secret is the registry's only request-level control over header trust, because the registry serves HTTP and TLS terminates upstream. The `--allow-public-bind` flag records the operator's assumption that an upstream control the registry cannot verify, such as mutual TLS, a firewall, or a network policy, keeps the registry reachable only through the gateway.

## Single-tenant and multi-tenant

A single-tenant registry is a single-node or clustered registry started without `PODIUM_MULTI_TENANT`. It resolves every authenticated caller to its sole tenant and does not consult the organization value.

A multi-tenant registry routes each request to the tenant its organization names. Enable multi-tenant mode with `PODIUM_MULTI_TENANT=true`. The `default` org is always provisioned.

| Provider | Organization source | Value that resolves to no provisioned tenant |
| --- | --- | --- |
| `oidc-jwt` | The verified `org_id` claim | Rejected with `auth.tenant_unknown`. |
| `trusted-headers` | The `X-Podium-User-Org` header | Treated as anonymous, and sees public visibility only. |

The organization value is an org ID or an org-name alias, which the registry resolves to a tenant. The layer endpoints follow the same routing, so a caller whose `X-Podium-User-Org` names no tenant lists no layers and cannot write one.

### Provisioning tenants

An instance operator provisions tenants at runtime. Seed the first operator with `PODIUM_OPERATOR_ADMINS` (comma-separated identities), which grants the instance-operator role at boot. The operator role is distinct from the per-tenant `admin` role and from `PODIUM_BOOTSTRAP_ADMINS`. It authorizes the `/v1/admin/tenants` API and the `podium admin tenant` CLI, and it confers no per-tenant `admin` rights.

| Task | Command |
| --- | --- |
| Provision an org | `podium admin tenant create <name>`, or `POST /v1/admin/tenants` |
| List tenants | `podium admin tenant list` |
| Update a tenant's quota or active state | `podium admin tenant update <id>` |
| Deactivate a tenant | `podium admin tenant deactivate <id>` |
| Reactivate a tenant | `podium admin tenant update <id> --active true` |

Create is idempotent, so re-creating an existing name returns that tenant unchanged. Deactivation is soft: a deactivated tenant stops resolving while its data persists. Tenant management is available only in multi-tenant mode. A single-tenant registry rejects every `/v1/admin/tenants` request with `registry.tenant_management_unavailable`. The [CLI reference](../reference/cli#podium-admin-tenant) lists the full flag set.

## Layer visibility default

Enabling either provider changes the resolved default layer visibility. On a registry with no identity provider, new admin-defined layers default to `visibility: public`. Once a provider is enabled and `PODIUM_DEFAULT_LAYER_VISIBILITY` is unset, the resolved default is `private`, so admin layers are not public to every caller once the registry filters by identity. An explicit `PODIUM_DEFAULT_LAYER_VISIBILITY=public` is applied unchanged. See [Access control](access-control#deployment-defaults).

## Web UI

Under either provider, the same registry process serves the web UI, and the UI carries no device-code flow of its own.

- **Behind a gateway.** The gateway authenticates the request. The registry resolves the caller's identity from the forwarded token or the injected headers, exactly as for any other API request, so the UI inherits the request's resolved identity.
- **Directly reachable under `oidc-jwt`.** The registry verifies the token the caller presents itself. Such a registry can also enable the browser flow with `--web-ui-auth` or `PODIUM_WEB_UI_AUTH`. The registry then signs the browser in through the identity provider and returns the resulting token in the `__Host-podium_session` cookie, which the [HTTP API reference](../reference/http-api#browser-session) documents.

With the browser flow disabled, a browser request whose configured token header carries no `Bearer` credential resolves as anonymous and sees public visibility only. With the flow enabled, such a request resolves as anonymous only when it also presents no valid session cookie, and the registry still reads the configured token header first. A non-loopback web-UI bind under `trusted-headers` is also subject to the provider's [bind restriction](#bind-restriction).

### A gateway that rewrites Host

The registry refuses a state-changing request that carries cross-site browser-origin evidence with `403 auth.csrf_invalid`. It compares the request's `Origin` header against the request's own `Host` header, which keeps the check free of a configuration key for the registry's public origin.

A gateway that rewrites `Host` to an upstream service name breaks that comparison. A legitimate same-origin write from the UI then carries an `Origin` whose host differs from `Host`. Every write from the browser is refused with `403 auth.csrf_invalid`, while every CLI and SDK write keeps succeeding, because a non-browser client carries no browser-origin evidence. Configure the gateway to pass the browser-facing `Host` through unrewritten.

## Startup guards

The providers fail closed on misconfiguration, so the registry does not serve with an unverifiable or forgeable identity. The [error-code catalog](../reference/error-codes) describes `config.invalid_issuer_scheme`, `config.oidc_jwt_audience_unset`, `config.trusted_headers_public_bind`, and `config.trusted_headers_multitenant_no_secret`.
