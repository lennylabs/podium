---
layout: default
title: OIDC cookbooks
parent: Deployment
nav_order: 7
has_children: true
description: Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak.
---

# OIDC integration cookbooks

Podium uses OIDC for identity in standard deployments. The registry does not ship its own user database. Consumers acquire a token from the identity provider through the `oauth-device-code` provider, and the token is cached in the OS keychain. The token carries the `sub`, `email`, and `groups` claims that determine the caller's effective view. Group membership is resolved registry-side, either through SCIM 2.0 push from the IdP or through the `IdpGroupMapping` adapter reading OIDC group claims from the token. When the deployment needs the registry HTTP boundary to enforce human OIDC, the registry sits behind an upstream identity-aware proxy that authenticates who can reach it.

These per-IdP guides cover the setup steps. Each guide assumes a Podium registry is already running at a known URL, for example `https://podium.acme.com`, and authentication is being configured for it.

## What's covered

| IdP | Guide | Notes |
| --- | --- | --- |
| Okta | [`okta.md`](okta.md) | Native group claim support; SCIM available. |
| Microsoft Entra ID | [`entra-id.md`](entra-id.md) | Formerly Azure AD. Group claim emits group _IDs_ rather than names, so `IdpGroupMapping` resolves them to names. |
| Google Workspace | [`google-workspace.md`](google-workspace.md) | No group claim natively in OIDC. Groups arrive via SCIM 2.0 push or as OIDC group claims mapped by `IdpGroupMapping`. |
| Auth0 | [`auth0.md`](auth0.md) | Group claim via custom action or rule. |
| Keycloak | [`keycloak.md`](keycloak.md) | Self-hosted; group claim via mapper. The Compose stack uses the sibling Dex for evaluation deployments. |

## What every guide produces

Each guide ends with a working `registry.yaml` `identity_provider:` block:

```yaml
identity_provider:
  type: oauth-device-code
  audience: https://podium.acme.com          # or api://podium, per IdP convention
  authorization_endpoint: https://<idp-issuer-url>
```

Group resolution is configured registry-side. Use SCIM 2.0 push from the IdP, or the `IdpGroupMapping` adapter that reads OIDC group claims from the token and maps them to group names.

Each guide also confirms that `podium login` against the registry from a developer machine completes the device-code flow and prints the resolved identity (`sub`, `email`, and groups).

## Human callers and managed runtimes

These guides configure the `oauth-device-code` provider for human callers on developer machines. The consumer acquires and caches the token, and the registry trusts the bearer presented to it.

Managed runtimes (for example Bedrock Agents or custom orchestrators) use the `injected-session-token` provider instead. The runtime issues a JWT signed by a key registered with the registry, and the registry verifies that signature on every call. That path is configured at the runtime and the registry rather than at the IdP, so it falls outside these per-IdP guides; see the [operator guide](../operator-guide) for the runtime-trust setup.

## What every guide does not cover

- **TLS termination**: handled by the load balancer or reverse proxy in front of the registry.
- **Network reachability**: developers must be able to reach the IdP's verification endpoint from their browser to complete the device-code flow.
- **Registry HTTP-boundary enforcement**: when the registry must enforce who can reach it, place it behind an upstream identity-aware proxy. The proxy authenticates the human caller; these guides configure the IdP that the proxy and the device-code flow use.

## Group resolution paths

Two mechanisms resolve group membership registry-side, and each guide names the one its IdP supports.

- **SCIM 2.0 push.** The IdP pushes user and group records to the registry's SCIM endpoint. The registry maintains a directory of `(user_id → groups)`. Group-membership changes apply without waiting for the user's next login.
- **OIDC group claims via `IdpGroupMapping`.** The registry reads the group claim from the token and maps the raw values to group names through a registry-side configuration. Group membership reflects what was in the token at login time.

## Common pitfalls (across IdPs)

- **Audience mismatch.** The IdP must issue tokens with `aud` matching the `audience:` configured for the registry. A token whose `aud` does not match is rejected.
- **Groups claim format.** Some IdPs emit groups as a JSON array of names; others emit IDs. The `IdpGroupMapping` adapter maps raw group values to the group names used in layer visibility.
- **Browser-mediated flows blocked.** Some corporate networks block the device-code verification URL. Test with a developer on the corporate network before declaring success.

## When to use SAML instead

When an organization standardizes on SAML rather than OIDC, the registry supports SAML through an OIDC bridge that translates SAML assertions into OIDC tokens. The per-IdP guides here assume OIDC.
