---
title: Gateway (trusted headers)
nav_order: 2
description: Run the registry behind a gateway that authenticates the caller, using the oidc-jwt and trusted-headers identity providers.
---

# Gateway (trusted headers)

This page covers running a registry behind a gateway that has already authenticated the caller. It is for operators of [single-node](../modes/single-node) and [clustered](../modes/clustered) deployments who front the registry with an OIDC ingress, an OAuth2 proxy, an identity-verifying sidecar, or a non-OIDC corporate SSO.

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
