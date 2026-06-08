---
layout: default
title: Gateway-delegated identity
parent: Deployment
nav_order: 8
description: Run the registry behind a gateway that authenticates the caller, using the oidc-jwt and trusted-headers identity providers.
---

# Gateway-delegated identity

A deployment may run the registry behind a gateway that has already authenticated the caller: an OIDC ingress, an OAuth2 proxy, an identity-verifying sidecar, or a non-OIDC corporate SSO. Two registry-process identity providers let the registry consume that gateway-supplied identity and filter layer visibility by it, rather than running its own device-code flow.

Both are selected by the registry's `PODIUM_IDENTITY_PROVIDER`. They are registry-side values: a Podium client behind the gateway sends no credential of its own, because identity is supplied by the gateway. The MCP server's `PODIUM_IDENTITY_PROVIDER` continues to admit only `oauth-device-code` and `injected-session-token`, and rejects these two values. Both apply on a standalone or a standard backend, and both are mutually exclusive with public mode.

| Value | Behavior | Use when |
| --- | --- | --- |
| `oidc-jwt` | Verifies a gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request. | The gateway can forward a verifiable OIDC token. |
| `trusted-headers` | Trusts gateway-injected identity headers without verifying them. | The gateway authenticates by SAML or a bespoke SSO and forwards identity as headers alone. |

Prefer `oidc-jwt` where the gateway can forward a verifiable token. It trusts the issuer's signing key alone and no element of the network path, so the registry may be directly reachable without an authentication bypass.

## oidc-jwt

The gateway forwards the caller's IdP-signed JWT to the registry. The registry verifies the token on every request against the issuer's JWKS, which it resolves from the issuer's OIDC discovery document.

```yaml
# registry.yaml  (standalone or standard server, fronted by a gateway)
identity_provider:
  type: oidc-jwt
  issuer: https://acme.okta.com/oauth2/default   # must be https
  audience: https://podium.acme.com
  token_header: Authorization   # default; value parsed as "Bearer <token>" for any header name
  jwks_cache_ttl_seconds: 300   # default
```

| Setting | Environment override | Default | Notes |
| --- | --- | --- | --- |
| `identity_provider.issuer` | `PODIUM_OAUTH_ISSUER` | required | Must use `https`. The registry derives the JWKS from `<issuer>/.well-known/openid-configuration`. |
| `identity_provider.audience` | `PODIUM_OAUTH_AUDIENCE` | required | The registry validates the token's `aud` against this value. |
| `identity_provider.token_header` | `PODIUM_OAUTH_TOKEN_HEADER` | `Authorization` | Header carrying the forwarded JWT, parsed as `Bearer <token>` for any header name. |
| `identity_provider.jwks_cache_ttl_seconds` | `PODIUM_OAUTH_JWKS_CACHE_TTL_SECONDS` | `300` | A `kid` absent from the cached set forces an earlier refresh. |

The gateway's job: forward the caller's IdP-signed JWT in the configured header as `Bearer <token>`, whether the header is the default `Authorization` or a custom one such as `X-Forwarded-Access-Token`. Stripping client-supplied tokens is unnecessary, because a forged token fails verification.

Group resolution follows the same registry-side mechanisms as the device-code flow: SCIM 2.0 push or the `IdpGroupMapping` adapter (see the [OIDC cookbooks](oidc/)). SCIM is available on a standard backend; a standalone server resolves groups through `IdpGroupMapping` alone.

A token that fails signature, `iss`, or `aud` validation is rejected with `auth.untrusted_token`, and an expired token with `auth.token_expired`. A request carrying no token is anonymous and sees public visibility only. While the issuer's JWKS is unreachable, verification fails closed and the request is anonymous rather than rejected.

## trusted-headers

The gateway authenticates the caller by any means and injects the resolved identity as request headers. The registry reads them without verification.

```yaml
# registry.yaml  (standalone or standard server, fronted by a gateway)
identity_provider:
  type: trusted-headers
  # The proxy secret is read from PODIUM_TRUSTED_PROXY_SECRET, not stored here.
```

| Header | Carries |
| --- | --- |
| `X-Podium-User-Sub` | The caller's OIDC subject. |
| `X-Podium-User-Email` | The caller's email. |
| `X-Podium-User-Groups` | The caller's groups, comma-separated. |
| `X-Podium-User-Org` | The caller's organization (a multi-tenant registry routes by this value). |
| `X-Podium-Proxy-Secret` | The shared secret matched against `PODIUM_TRUSTED_PROXY_SECRET`. |

Groups come from `X-Podium-User-Groups` directly. SCIM and `IdpGroupMapping` are not consulted, because there is no token to read and the gateway is the source of truth. Provision groups at the gateway for a `trusted-headers` deployment.

The gateway's job: authenticate the caller, remove any client-supplied `X-Podium-User-*` headers, set the identity headers from the authenticated session, and, when a secret is configured, attach `X-Podium-Proxy-Secret`. A request without identity headers is anonymous and sees public visibility only; `trusted-headers` raises no authentication error.

### Bind restriction

`trusted-headers` reads identity from headers it cannot verify, so the identity it trusts is exactly the set of clients that can reach the bind address. The registry constrains the bind at startup.

- **Single-tenant registry.** A loopback bind (`127.0.0.0/8`, `::1`) is always allowed. A non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set.
- **Multi-tenant registry.** Because `X-Podium-User-Org` selects among tenants and a co-resident process can reach a loopback bind, the proxy secret is required on every request regardless of bind address; an unset secret fails to start with `config.trusted_headers_multitenant_no_secret`.

The proxy secret is the registry's only request-level control over header trust, because the registry serves HTTP and TLS terminates upstream. The `--allow-public-bind` flag records the operator's assumption that an upstream control the registry cannot verify, such as mutual TLS, a firewall, or a network policy, keeps the registry reachable only through the gateway.

## Single-tenant and multi-tenant

On a single-tenant registry (a standalone backend, or a standard backend with one org), the registry resolves every authenticated caller to its sole tenant and does not consult the organization value.

A multi-tenant registry routes each request to the tenant its organization names: the verified `org_id` claim under `oidc-jwt`, or the `X-Podium-User-Org` header under `trusted-headers`. Enable it with `PODIUM_MULTI_TENANT=true` and provision the orgs with `PODIUM_TENANTS=acme,globex` (the default org is always provisioned). The organization value is an org ID or an org-name alias, which the registry resolves to a tenant. Under `oidc-jwt`, a value that resolves to no provisioned tenant is rejected with `auth.tenant_unknown`; under `trusted-headers`, the request is left in no tenant and sees an empty view.

Tenants are provisioned at registry boot. The registry reads `PODIUM_TENANTS`, creates a tenant row for each named org in the store's `tenants` table, and persists it. The registry has no runtime tenant-provisioning API or CLI. Adding or removing a tenant requires editing `PODIUM_TENANTS` and restarting the registry. Provisioning is idempotent, so a restart with an unchanged list creates no duplicates, and removing a name from the list stops new requests from routing to that org without deleting its existing rows.

## Layer visibility default

Enabling either provider changes the resolved default layer visibility. On a standalone server without an identity provider, new layers default to `visibility: public`. Once a provider is enabled and `PODIUM_DEFAULT_LAYER_VISIBILITY` is unset, the resolved default is `private`, so admin layers are not public to every caller once the registry filters by identity. An explicit `PODIUM_DEFAULT_LAYER_VISIBILITY=public` is applied unchanged.

## Web UI

Under either provider the web UI is served by the same registry process behind the same gateway and carries no device-code flow of its own. The gateway authenticates the request and the registry resolves the caller's identity, exactly as for any other API request, so the UI inherits the request's resolved identity. A non-loopback web-UI bind under `trusted-headers` is also subject to the provider's bind restriction.

## Startup guards

The providers fail closed on misconfiguration rather than serving an unverifiable or forgeable registry. See the [error-code catalog](../reference/error-codes) for `config.invalid_issuer_scheme`, `config.oidc_jwt_audience_unset`, `config.trusted_headers_public_bind`, and `config.trusted_headers_multitenant_no_secret`.
