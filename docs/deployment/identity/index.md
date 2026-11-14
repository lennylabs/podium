---
title: Identity
nav_order: 2
description: "How a registry identifies callers: the identity providers, the consumer and registry sides, the default layer visibility, the web UI, and the startup guards."
---

# Identity

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

## Layer visibility default

Enabling either provider changes the resolved default layer visibility. On a registry with no identity provider, new admin-defined layers default to `visibility: public`. Once a provider is enabled and `PODIUM_DEFAULT_LAYER_VISIBILITY` is unset, the resolved default is `private`, so admin layers are not public to every caller once the registry filters by identity. An explicit `PODIUM_DEFAULT_LAYER_VISIBILITY=public` is applied unchanged. See [Access control](../content/access-control#deployment-defaults).

## Web UI

Under either provider, the same registry process serves the web UI, and the UI carries no device-code flow of its own.

- **Behind a gateway.** The gateway authenticates the request. The registry resolves the caller's identity from the forwarded token or the injected headers, exactly as for any other API request, so the UI inherits the request's resolved identity.
- **Directly reachable under `oidc-jwt`.** The registry verifies the token the caller presents itself. Such a registry can also enable the browser flow with `--web-ui-auth` or `PODIUM_WEB_UI_AUTH`. The registry then signs the browser in through the identity provider and returns the resulting token in the `__Host-podium_session` cookie, which the [HTTP API reference](../../reference/http-api#browser-session) documents.

With the browser flow disabled, a browser request whose configured token header carries no `Bearer` credential resolves as anonymous and sees public visibility only. With the flow enabled, such a request resolves as anonymous only when it also presents no valid session cookie, and the registry still reads the configured token header first. A non-loopback web-UI bind under `trusted-headers` is also subject to the provider's [bind restriction](trusted-headers#bind-restriction).

### A gateway that rewrites Host

The registry refuses a state-changing request that carries cross-site browser-origin evidence with `403 auth.csrf_invalid`. It compares the request's `Origin` header against the request's own `Host` header, which keeps the check free of a configuration key for the registry's public origin.

A gateway that rewrites `Host` to an upstream service name breaks that comparison. A legitimate same-origin write from the UI then carries an `Origin` whose host differs from `Host`. Every write from the browser is refused with `403 auth.csrf_invalid`, while every CLI and SDK write keeps succeeding, because a non-browser client carries no browser-origin evidence. Configure the gateway to pass the browser-facing `Host` through unrewritten.

## Startup guards

The providers fail closed on misconfiguration, so the registry does not serve with an unverifiable or forgeable identity. The [error-code catalog](../../reference/error-codes) describes `config.invalid_issuer_scheme`, `config.oidc_jwt_audience_unset`, `config.trusted_headers_public_bind`, and `config.trusted_headers_multitenant_no_secret`.
