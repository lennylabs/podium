---
title: OIDC cookbooks
nav_order: 12
description: Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak.
---

# OIDC integration cookbooks

Podium uses OIDC for identity on any server-backed deployment. The registry does not ship its own user database. Identity is configured in two places. A CLI, an SDK, or another API client acquires a token from the identity provider through the `oauth-device-code` provider and caches it in the OS keychain, and on a registry that enables the browser flow a browser obtains a token through the registry's own authorization-code exchange, which the registry returns in the `__Host-podium_session` cookie. The registry sets `oidc-jwt` and verifies each presented token against the issuer's JWKS on every call. The token carries the `sub`, `email`, and `groups` claims that determine the caller's effective view. Group membership is resolved registry-side, either through SCIM 2.0 push from the IdP or through the `IdpGroupMapping` adapter reading OIDC group claims from the token. When the deployment needs the registry HTTP boundary to enforce human OIDC, the registry sits behind an upstream identity-aware proxy that authenticates who can reach it.

These per-IdP guides cover the setup steps. Each guide assumes a Podium registry is already running at a known URL, for example `https://podium.acme.com`, and authentication is being configured for it.

## What's covered

| IdP | Guide | Notes |
| --- | --- | --- |
| Okta | [`okta.md`](okta.md) | Native group claim support; SCIM available. |
| Microsoft Entra ID | [`entra-id.md`](entra-id.md) | Formerly Azure AD. Group claim emits group _IDs_ rather than names, so `IdpGroupMapping` resolves them to names. |
| Google Workspace | [`google-workspace.md`](google-workspace.md) | No group claim natively in OIDC. Groups arrive via SCIM 2.0 push or as OIDC group claims mapped by `IdpGroupMapping`. |
| Auth0 | [`auth0.md`](auth0.md) | Group claim via custom action or rule, emitted under a namespaced path that `groups_claim` names. Auth0 issues `iss` with a trailing slash, which the verifier trims on both sides. |
| Keycloak | [`keycloak.md`](keycloak.md) | Self-hosted; group claim via mapper. The Compose stack uses the sibling Dex for evaluation deployments. |

## What every guide produces

Each guide ends with a working `registry.yaml` `identity_provider:` block:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<idp-issuer-url>           # must be https
    audience: https://podium.acme.com          # or api://podium, per IdP convention
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` or `layers:` parses to an empty config and the registry ignores it without reporting an error.

Group resolution is configured registry-side. Use SCIM 2.0 push from the IdP, or the `IdpGroupMapping` adapter that reads OIDC group claims from the token and maps them to group names.

Each guide includes a `podium login` run from a developer machine. Where the flow completes, the command prints the identity it decodes from the ID token: the `sub`, the `email`, and the groups the token carries. Every guide except Google Workspace ends with a credential the registry accepts. The credential `podium login` caches for Google is an opaque access token that the verifier cannot parse, so that guide routes callers through a gateway instead. The registry trims a trailing slash from both the configured issuer and the token's `iss` before comparing them, so an issuer URL written either way is accepted.

## Human callers and managed runtimes

These guides configure `oidc-jwt` on the registry and the `oauth-device-code` flow for human callers on developer machines. The consumer acquires and caches the token, and the registry verifies the presented token against the issuer's JWKS on every call. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`, because the registry ships no request-time verifier for it.

Managed runtimes (for example Bedrock Agents or custom orchestrators) use the `injected-session-token` provider instead. The runtime issues a JWT signed by a key the deployment configures the registry to trust at startup, and the registry verifies that signature on every call. That path is configured at the runtime and the registry rather than at the IdP, so it falls outside these per-IdP guides; see the [clustered deployment guide](../clustered#identity-flow) for the runtime-trust setup.

When a gateway in front of the registry has already authenticated the caller, the same `oidc-jwt` provider verifies the token the gateway forwards, and `trusted-headers` reads the identity the gateway injects as headers. In that arrangement the Podium client sends no credential of its own, so the developer-machine steps in these guides do not apply. See [gateway-delegated identity](../gateway-delegated-identity).

## What every guide does not cover

- **TLS termination**: handled by the load balancer or reverse proxy in front of the registry.
- **Network reachability**: developers must be able to reach the IdP's verification endpoint from their browser to complete the device-code flow. The registry host must also reach the issuer's discovery document at `<issuer>/.well-known/openid-configuration` and the JWKS it names. Under `oidc-jwt` the registry fetches both during startup and refuses to start when either fetch fails, reporting an error that begins `oidc-jwt: issuer "<issuer>" is unreachable at startup, refusing to start` and ends with the underlying fetch failure.
- **Registry HTTP-boundary enforcement**: when the registry must enforce who can reach it, place it behind an upstream identity-aware proxy. The proxy authenticates the human caller; these guides configure the IdP that the proxy and the device-code flow use.

## Group resolution paths

Two mechanisms resolve group membership registry-side, and each guide names the one its IdP supports.

- **SCIM 2.0 push.** The IdP pushes user and group records to the registry's SCIM endpoint. The registry maintains a directory of `(user_id → groups)`. A layer's `groups:` entry names a SCIM group by its `displayName`, and the registry grants visibility when that group holds a member whose SCIM `userName` equals the caller's `sub` or `email` claim. Provision each user under a `userName` that the caller's token also carries. A `userName` that matches neither claim resolves to no group visibility and reports no error, and the SCIM user's `emails` value is stored but never compared. Group-membership changes apply without waiting for the user's next login. The registry mounts `/scim/v2/` only when `PODIUM_SCIM_TOKENS` holds a comma-separated list of the bearer tokens it accepts there. With that variable unset, every SCIM request returns 404 and layer `groups:` filters resolve from the token claim alone. Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart. Neither variable has a `registry.yaml` key.
- **OIDC group claims via `IdpGroupMapping`.** The registry reads the top-level `groups` claim from the token and maps the raw values to group names through `PODIUM_IDP_GROUP_MAPPING`, a comma-separated list of `<token-value>=<group-name>` pairs. A value with no entry passes through unchanged. The claim key is fixed at `groups`, its value must be a JSON array of strings, and the mapping has no `registry.yaml` key. A `groups` claim encoded as a single delimited string is ignored and resolves to no groups. Group membership reflects what was in the token at login time.

## Common pitfalls (across IdPs)

- **Audience mismatch.** The IdP must issue tokens whose `aud` carries one of the values configured under `audience:` for the registry. A token whose `aud` carries none of them is rejected.
- **Groups claim format.** Some IdPs emit groups as a JSON array of names; others emit IDs. The `IdpGroupMapping` adapter maps raw group values to the group names used in layer visibility.
- **Browser-mediated flows blocked.** Some corporate networks block the device-code verification URL. Test with a developer on the corporate network before declaring success.
- **Missing `org_id` on a multi-tenant registry.** A registry started with `PODIUM_MULTI_TENANT=true` selects each request's tenant from the token's verified `org_id` claim. A token without that claim leaves the request on the registry's reserved no-data tenant, so the caller sees an empty catalog and no error. A token whose `org_id` names no provisioned tenant is rejected with `auth.tenant_unknown`. Configure the IdP to emit `org_id` when the registry runs multi-tenant.

## When to use SAML instead

The `oidc-jwt` provider verifies OIDC tokens only. An organization standardized on SAML can reach the registry through an upstream OIDC bridge that translates SAML assertions into OIDC tokens, which the registry then consumes like any other OIDC token. A gateway that authenticates the caller over SAML can instead inject `X-Podium-User-Sub`, `X-Podium-User-Email`, `X-Podium-User-Groups`, and `X-Podium-User-Org`, which the registry reads under the `trusted-headers` provider; see [gateway-delegated identity](../gateway-delegated-identity). The per-IdP guides here assume OIDC.
