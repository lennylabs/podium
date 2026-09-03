---
title: Gateway-delegated identity
nav_order: 11
description: Run the registry behind a gateway that authenticates the caller, using the oidc-jwt and trusted-headers identity providers.
---

# Gateway-delegated identity

A deployment may run the registry behind a gateway that has already authenticated the caller: an OIDC ingress, an OAuth2 proxy, an identity-verifying sidecar, or a non-OIDC corporate SSO. Two registry-process identity providers let the registry consume that gateway-supplied identity and filter layer visibility by it, rather than running its own device-code flow. `oidc-jwt` also serves a directly reachable registry, where the registry verifies the caller's token itself; `trusted-headers` requires the fronting gateway.

Both are selected by the registry's `PODIUM_IDENTITY_PROVIDER`. They are registry-side values: a Podium client behind a gateway that has already authenticated the caller, under either provider, sends no credential of its own, because identity is supplied by the gateway. The MCP server's `PODIUM_IDENTITY_PROVIDER` continues to admit only `oauth-device-code` and `injected-session-token`, and rejects these two values. Both apply on a [single-node](single-node) or a [clustered](clustered) deployment, and both are mutually exclusive with public mode.

| Value | Behavior | Use when |
| --- | --- | --- |
| `oidc-jwt` | Verifies a gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request. | The gateway can forward a verifiable OIDC token. |
| `trusted-headers` | Trusts gateway-injected identity headers without verifying them. | The gateway authenticates by SAML or a bespoke SSO and forwards identity as headers alone. |

Prefer `oidc-jwt` where the gateway can forward a verifiable token. It trusts the issuer's signing key alone and no element of the network path, so the registry may be directly reachable without an authentication bypass.

## oidc-jwt

The gateway forwards the caller's IdP-signed JWT to the registry. The registry verifies the token on every request against the issuer's JWKS, which it resolves from the issuer's OIDC discovery document.

`oidc-jwt` covers a directly-reachable registry as well, where a CLI, an SDK, or another API client presents a token it acquired itself through the device-code flow, and, where that registry enables the browser flow, where a browser presents the token the registry obtained for it through its own authorization-code exchange and returned in `__Host-podium_session`. The verification path is identical, because the registry checks the token rather than the network path. The [OIDC cookbooks](oidc/) configure that arrangement per IdP.

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

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config and the registry ignores it without reporting an error.

| Setting | Environment override | Default | Notes |
| --- | --- | --- | --- |
| `identity_provider.issuer` | `PODIUM_OAUTH_ISSUER` | required | Must use `https`. The registry derives the JWKS from `<issuer>/.well-known/openid-configuration`. |
| `identity_provider.audience` | `PODIUM_OAUTH_AUDIENCE` | required | Audience values this registry answers to. A token is accepted when its `aud` claim carries at least one configured value. The config-file key takes a string or a list of strings, and the environment variable takes a comma-separated list. |
| `identity_provider.token_header` | `PODIUM_OAUTH_TOKEN_HEADER` | `Authorization` | Header carrying the forwarded JWT, parsed as `Bearer <token>` for any header name. |
| `identity_provider.jwks_cache_ttl_seconds` | `PODIUM_OAUTH_JWKS_CACHE_TTL_SECONDS` | `300` | A `kid` absent from the cached set forces an earlier refresh. |
| `identity_provider.subject_claim` | `PODIUM_OAUTH_SUBJECT_CLAIM` | `(unset; sub)` | Claim read as the caller's subject. When it is set the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. AD FS access tokens carry `idsub` and no `sub`. |
| `identity_provider.groups_claim` | `PODIUM_OAUTH_GROUPS_CLAIM` | `(unset; groups)` | Claim read for group membership. AD FS issuance rules emit the full claim-type URI (`http://schemas.microsoft.com/ws/2008/06/identity/claims/groups`) unless authored with a short name. Single-value and multi-value forms are both accepted. |

A deployment lists more than one value when the same registry is addressed by more than one audience from one trusted issuer. The OAuth device authorization grant is the case this covers: that request carries `client_id` and `scope` and cannot name a target resource, so an IdP that stamps the client identifier on the resulting access token issues a different `aud` than the same IdP issues for a token exchange that names the registry's API URI. Each listed value is a trust decision the operator makes at startup, and the set never grows at runtime. The audience decides whether a token is verified at all and does not narrow what a caller may do: a caller admitted under any listed value has the same effective view, the same grants, and the same audit identity. A deployment that needs two entrances to one catalog to differ in privilege runs two registries. A listed value that another resource server also accepts, or a client identifier shared across applications, admits every token that issuer mints for it, so list a client identifier only where it is assigned to a single application. A comma is legal in a URI and cannot be written in the environment form, which splits on commas; write such a value in the config file, where a string is one audience verbatim. A client process that shares the registry's environment reads `PODIUM_OAUTH_AUDIENCE` as the one value it asks for and sends the comma-separated list verbatim, so give a co-located client its own `--audience` or its own environment. The first configured value is the one the registry asks the identity provider for when it initiates a flow itself, which is the browser sign-in redirect described under [Web UI](#web-ui), so a deployment that enables that flow lists the value its browser client is issued first. The order of the remaining entries does not affect verification.

The gateway's job: forward the caller's IdP-signed JWT in the configured header as `Bearer <token>`, whether the header is the default `Authorization` or a custom one such as `X-Forwarded-Access-Token`. Stripping client-supplied tokens is unnecessary, because a forged token fails verification.

The registry reads group membership from the token's `groups` claim on every verified token, or from the claim named by `identity_provider.groups_claim` (`PODIUM_OAUTH_GROUPS_CLAIM`) when a deployment's IdP emits membership under another claim name. The `IdpGroupMapping` adapter rewrites the values that claim carries to registry-side group names, and it names no claim itself. A registry that also resolves membership through SCIM 2.0 push matches SCIM-resolved membership in addition to the claim-derived groups. The registry mounts the SCIM receiver at `/scim/v2/` when `PODIUM_SCIM_TOKENS` names at least one bearer token, which is keyed on that variable alone and applies to a single-node and a clustered deployment alike, and `PODIUM_SCIM_STORE_PATH` persists the pushed directory across restarts.

The token's `iss` is accepted when it matches the configured issuer or the `access_token_issuer` published by the discovery document that issuer resolves. AD FS serves discovery under `https://<host>/adfs` and stamps the federation-service identifier `http://<host>/adfs/services/trust` on the access token, so no single configured value covers both roles. The signing keys still come from the `jwks_uri` in that same `https` document, so the set of trusted keys is unchanged. When the document publishes an `access_token_issuer` that differs from the configured issuer, the registry logs both accepted values at startup.

A deployment that sets `identity_provider.subject_claim` lists values of the named claim in its `users:` entries, in `PODIUM_OPERATOR_ADMINS`, and in `PODIUM_BOOTSTRAP_ADMINS`. The instance-operator grant and the per-tenant admin grants match the recorded subject and have no email fallback, so a `sub` value left in either variable grants nothing once the registry records another claim as the subject. The named claim must also identify one principal for the life of the deployment.

A token that fails signature, `iss`, or `aud` validation is rejected with `auth.untrusted_token`, and an expired token with `auth.token_expired`. A request whose configured token header carries no `Bearer` credential is anonymous rather than rejected and sees public visibility only. Where the registry enables the browser flow (see [Web UI](#web-ui)), such a request is anonymous only when it also presents no valid `__Host-podium_session` cookie, and the configured token header is still read first. The gateway-fronted deployment this page configures enables no browser flow, so the registry reads no cookie there. While the issuer's JWKS is unreachable, verification fails closed and the request is anonymous rather than rejected.

## trusted-headers

The gateway authenticates the caller by any means and injects the resolved identity as request headers. The registry reads them without verification.

```yaml
# registry.yaml  (single-node or clustered server, fronted by a gateway)
registry:
  identity_provider:
    type: trusted-headers
    # The proxy secret is read from PODIUM_TRUSTED_PROXY_SECRET and has no config-file key.
```

| Header | Carries |
| --- | --- |
| `X-Podium-User-Sub` | The caller's OIDC subject. |
| `X-Podium-User-Email` | The caller's email. |
| `X-Podium-User-Groups` | The caller's groups, comma-separated. |
| `X-Podium-User-Org` | The caller's organization (a multi-tenant registry routes by this value). |
| `X-Podium-Proxy-Secret` | The shared secret matched against `PODIUM_TRUSTED_PROXY_SECRET`. |

Groups come from `X-Podium-User-Groups` directly, and `IdpGroupMapping` is not consulted, because there is no token to read and the gateway is the source of truth. Provision groups at the gateway for a `trusted-headers` deployment. When the registry also mounts the SCIM receiver, a layer's `groups:` filter still expands against the pushed directory: the registry grants visibility when the named group holds a member whose SCIM `userName` equals the caller's `X-Podium-User-Sub` or `X-Podium-User-Email` value. Leave `PODIUM_SCIM_TOKENS` unset under `trusted-headers` when the gateway is meant to be the only source of group membership.

The gateway's job: authenticate the caller, remove any client-supplied `X-Podium-User-*` headers, set the identity headers from the authenticated session, and, when a secret is configured, attach `X-Podium-Proxy-Secret`. A request without identity headers is anonymous and sees public visibility only; `trusted-headers` raises no authentication error.

### Bind restriction

`trusted-headers` reads identity from headers it cannot verify, so the identity it trusts is exactly the set of clients that can reach the bind address. The registry constrains the bind at startup.

- **Single-tenant registry.** A loopback bind (`127.0.0.0/8`, `::1`) is always allowed. A non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set.
- **Multi-tenant registry.** Because `X-Podium-User-Org` selects among tenants and a co-resident process can reach a loopback bind, the proxy secret is required on every request regardless of bind address; an unset secret fails to start with `config.trusted_headers_multitenant_no_secret`.

The proxy secret is the registry's only request-level control over header trust, because the registry serves HTTP and TLS terminates upstream. The `--allow-public-bind` flag records the operator's assumption that an upstream control the registry cannot verify, such as mutual TLS, a firewall, or a network policy, keeps the registry reachable only through the gateway.

## Single-tenant and multi-tenant

On a single-tenant registry, which covers a single-node deployment and a clustered one with a single org, the registry resolves every authenticated caller to its sole tenant and does not consult the organization value.

A multi-tenant registry routes each request to the tenant its organization names: the verified `org_id` claim under `oidc-jwt`, or the `X-Podium-User-Org` header under `trusted-headers`. Enable multi-tenant mode with `PODIUM_MULTI_TENANT=true` (the `default` org is always provisioned). The organization value is an org ID or an org-name alias, which the registry resolves to a tenant. Under `oidc-jwt`, a value that resolves to no provisioned tenant is rejected with `auth.tenant_unknown`; under `trusted-headers`, the request is treated as anonymous and sees public visibility only.

Tenants are provisioned at runtime by an instance operator. Seed the first operator with `PODIUM_OPERATOR_ADMINS` (comma-separated identities), which grants the instance-operator role at boot. The operator role is distinct from the per-tenant `admin` role and from `PODIUM_BOOTSTRAP_ADMINS`: it authorizes the `/v1/admin/tenants` API and the `podium admin tenant` CLI, and it confers no per-tenant `admin` rights. The operator provisions each org with `podium admin tenant create <name>` (or `POST /v1/admin/tenants`), lists tenants with `podium admin tenant list`, updates a tenant's quota or active state with `podium admin tenant update <id>`, and deactivates one with `podium admin tenant deactivate <id>`. Create is idempotent, so re-creating an existing name returns that tenant unchanged. Deactivation is soft: a deactivated tenant stops resolving while its data persists, and `podium admin tenant update <id> --active true` reactivates it. Tenant management is available only in multi-tenant mode. A single-tenant registry rejects every `/v1/admin/tenants` request with `registry.tenant_management_unavailable`. See the [CLI reference](../reference/cli#podium-admin-tenant) for the full flag set.

## Layer visibility default

Enabling either provider changes the resolved default layer visibility. On a registry with no identity provider, new admin-defined layers default to `visibility: public`. Once a provider is enabled and `PODIUM_DEFAULT_LAYER_VISIBILITY` is unset, the resolved default is `private`, so admin layers are not public to every caller once the registry filters by identity. An explicit `PODIUM_DEFAULT_LAYER_VISIBILITY=public` is applied unchanged. See [Access control](access-control#deployment-defaults).

## Web UI

Under either provider the web UI is served by the same registry process and carries no device-code flow of its own. Where a gateway fronts the registry, the gateway authenticates the request and the registry resolves the caller's identity from the forwarded token or the injected headers, exactly as for any other API request, so the UI inherits the request's resolved identity. Where the registry is directly reachable under `oidc-jwt`, the registry verifies the token the caller presents itself. Such a registry may also enable the browser flow (`--web-ui-auth` / `PODIUM_WEB_UI_AUTH`), in which case the registry signs the browser in through the identity provider and returns the resulting token in the `__Host-podium_session` cookie, which the [HTTP API reference](../reference/http-api#browser-session) documents. With the browser flow disabled, a browser request whose configured token header carries no `Bearer` credential resolves as anonymous and sees public visibility only; with it enabled, such a request resolves as anonymous only when it also presents no valid session cookie, and the configured token header is still read first. A non-loopback web-UI bind under `trusted-headers` is also subject to the provider's bind restriction.

### A gateway that rewrites Host

The registry refuses a state-changing request that carries cross-site browser-origin evidence with `403 auth.csrf_invalid`, and it compares the request's `Origin` header against the request's own `Host` header, which keeps the check free of a configuration key for the registry's public origin. A gateway that rewrites `Host` to an upstream service name breaks that comparison. A legitimate same-origin write from the UI then carries an `Origin` whose host differs from `Host`, so every write from the browser is refused with `403 auth.csrf_invalid` while every CLI and SDK write keeps succeeding, because a non-browser client carries no browser-origin evidence at all. The remedy is to pass the browser-facing `Host` through unrewritten.

## Startup guards

The providers fail closed on misconfiguration rather than serving an unverifiable or forgeable registry. See the [error-code catalog](../reference/error-codes) for `config.invalid_issuer_scheme`, `config.oidc_jwt_audience_unset`, `config.trusted_headers_public_bind`, and `config.trusted_headers_multitenant_no_secret`.
