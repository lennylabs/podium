---
title: Gateway (trusted headers)
nav_order: 2
description: "Run the registry behind a gateway that authenticates the caller and injects the identity as headers, using the trusted-headers identity provider."
---

# Gateway (trusted headers)

Under the `trusted-headers` identity provider, the registry runs behind a gateway that has already authenticated the caller, and it reads the caller's identity from headers that the gateway injects. The gateway can be an OIDC ingress, an OAuth2 proxy, an identity-verifying sidecar, or a SAML or other corporate SSO. The provider applies on [single-node](../modes/single-node) and [clustered](../modes/clustered) deployments.

The registry does not verify the headers, so `trusted-headers` depends on every request reaching the registry through the gateway. When the gateway can forward the caller's IdP-signed JWT instead, run the registry under `oidc-jwt`, which verifies the token itself and depends on no network path. [Configure oidc-jwt](oidc/#configure-oidc-jwt) covers a gateway that forwards a token, and [Choosing a provider](index#choosing-a-provider) compares the two.

## Identity headers

| Header | Carries |
| --- | --- |
| `X-Podium-User-Sub` | The caller's subject. A request without it is anonymous. |
| `X-Podium-User-Email` | The caller's email. |
| `X-Podium-User-Groups` | The caller's groups, comma-separated. |
| `X-Podium-User-Org` | The caller's organization, which a multi-tenant registry routes by. A single-tenant registry ignores it. |
| `X-Podium-Proxy-Secret` | The shared secret matched against `PODIUM_TRUSTED_PROXY_SECRET`. |

When `PODIUM_TRUSTED_PROXY_SECRET` is set, the registry honors the identity headers only on a request whose `X-Podium-Proxy-Secret` matches it under a constant-time comparison, and treats any other request as anonymous. When the secret is unset, the registry ignores `X-Podium-Proxy-Secret` and honors the identity headers on every request.

`trusted-headers` raises no authentication error. A request without identity headers, or with a mismatched secret, is anonymous and sees public visibility only. A Podium client behind the gateway sends no credential of its own.

## Configure trusted-headers

1. Configure the gateway to authenticate the caller.
2. Configure the gateway to remove any client-supplied `X-Podium-User-*` headers.
3. Configure the gateway to set the identity headers from the authenticated session.
4. Set `PODIUM_TRUSTED_PROXY_SECRET` on the registry, and configure the gateway to attach the same value as `X-Podium-Proxy-Secret`. [Bind restriction](#bind-restriction) states when the secret is required.
5. Add the `identity_provider:` block to `registry.yaml`, nested under `registry:` as [File layout](../../reference/configuration#file-layout) describes:

```yaml
# registry.yaml  (single-node or clustered server, fronted by a gateway)
registry:
  identity_provider:
    type: trusted-headers
    # The proxy secret is read from PODIUM_TRUSTED_PROXY_SECRET and has no config-file key.
```

6. Restart the registry.

The registry then resolves each caller from the injected headers. On a multi-tenant registry, an `X-Podium-User-Org` value that names no provisioned tenant makes the request anonymous, as [Tenants and admins](tenants#single-tenant-and-multi-tenant) describes.

## Group membership under trusted-headers

Groups come from `X-Podium-User-Groups` directly. The registry applies no `IdpGroupMapping`, because there is no token to read and the gateway is the source of truth. Provision groups at the gateway.

The registry does not consult a SCIM directory under this provider. A `groups:` filter matches the `X-Podium-User-Groups` value alone, even when `PODIUM_SCIM_TOKENS` mounts the receiver. [SCIM under trusted-headers](groups#scim-under-trusted-headers) describes the receiver's behavior and the startup log line that reports it.

## Bind restriction

`trusted-headers` reads identity from headers it cannot verify, so the identity it trusts is exactly the set of clients that can reach the bind address. The registry constrains the bind at startup.

| Registry | Rule |
| --- | --- |
| Single-tenant | A loopback bind (`127.0.0.0/8`, `::1`) is always allowed. A non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set. |
| Multi-tenant | `X-Podium-User-Org` selects among tenants, and a co-resident process can reach a loopback bind. The proxy secret is therefore required on every bind address, and an unset secret fails to start with `config.trusted_headers_multitenant_no_secret`. |

The proxy secret is the registry's only request-level control over header trust, because the registry serves HTTP and TLS terminates upstream. The `--allow-public-bind` flag, or `PODIUM_ALLOW_PUBLIC_BIND`, records the operator's assumption that an upstream control the registry cannot verify, such as mutual TLS, a firewall, or a network policy, keeps the registry reachable only through the gateway.

## A gateway that rewrites Host

The registry refuses a state-changing request that carries cross-site browser-origin evidence with `403 auth.csrf_invalid`. It compares the request's `Origin` header against the request's own `Host` header, which keeps the check free of a configuration key for the registry's public origin. The check runs on every registry, under `trusted-headers` and under `oidc-jwt` behind a gateway alike.

A gateway that rewrites `Host` to an upstream service name breaks that comparison. A legitimate same-origin write from the web UI then carries an `Origin` whose host differs from `Host`. Every write from the browser is refused with `403 auth.csrf_invalid`, while every CLI and SDK write keeps succeeding, because a non-browser client carries no browser-origin evidence. Configure the gateway to pass the browser-facing `Host` through unrewritten.
