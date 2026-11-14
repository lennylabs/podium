---
title: OIDC cookbooks
nav_order: 9
description: Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak.
---

# OIDC cookbooks

These cookbooks configure OIDC identity for a server-backed Podium registry, one identity provider (IdP) per page. Each cookbook assumes a Podium registry is already running at a known URL, for example `https://podium.acme.com`, and configures authentication for it. This page describes the identity model the cookbooks share, the group-resolution paths, and the pitfalls common to every IdP.

## Cookbooks

| IdP | Cookbook | Notes |
| --- | --- | --- |
| Okta | [Okta](okta) | Native group claim support; SCIM available. |
| Microsoft Entra ID | [Microsoft Entra ID](entra-id) | Formerly Azure AD. The group claim emits group IDs instead of names, so `IdpGroupMapping` resolves them to names. |
| Google Workspace | [Google Workspace](google-workspace) | No native group claim in OIDC. Groups arrive through SCIM 2.0 push or as OIDC group claims mapped by `IdpGroupMapping`. |
| Auth0 | [Auth0](auth0) | An Action or a legacy Rule adds the group claim under a namespaced path that `groups_claim` names. Auth0 issues `iss` with a trailing slash, which the verifier trims on both sides. |
| Keycloak | [Keycloak](keycloak) | Self-hosted; a mapper adds the group claim. The Compose stack uses the sibling Dex for evaluation deployments. |

## How OIDC identity works in Podium

Podium uses OIDC for identity on any server-backed deployment. The registry does not ship its own user database.

- **Token acquisition.** A CLI, an SDK, or another API client acquires a token from the IdP through the `oauth-device-code` provider and caches it in the OS keychain. On a registry that enables the browser flow, a browser obtains a token through the registry's own authorization-code exchange, and the registry returns it in the `__Host-podium_session` cookie.
- **Token verification.** The registry runs the `oidc-jwt` provider and verifies each presented token against the issuer's JWKS on every call.
- **Claims.** The token carries the `sub`, `email`, and `groups` claims that determine the caller's effective view.
- **Group resolution.** The registry resolves group membership itself, either through SCIM 2.0 push from the IdP or through the `IdpGroupMapping` adapter reading OIDC group claims from the token. [Group resolution paths](#group-resolution-paths) describes both.
- **Boundary enforcement.** When the deployment needs the registry's HTTP boundary to enforce human OIDC, the registry sits behind an upstream identity-aware proxy that authenticates who can reach it.

## What each cookbook produces

Each cookbook ends with a working `registry.yaml` `identity_provider:` block:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<idp-issuer-url>           # must be https
    audience: https://podium.acme.com          # or api://podium, per IdP convention
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` or `layers:` parses to an empty config, and the registry ignores it without reporting an error.

The registry trims a trailing slash from both the configured issuer and the token's `iss` before comparing them, so an issuer URL written either way is accepted.

Each cookbook includes a `podium login` run from a developer machine. Where the flow completes, the command prints the identity it decodes from the ID token: the `sub`, the `email`, and the groups the token carries.

Every cookbook except Google Workspace ends with a credential the registry accepts. For Google, `podium login` caches an opaque access token that the verifier cannot parse, so that cookbook routes callers through a gateway instead.

## Human callers and managed runtimes

The cookbooks configure two pieces: `oidc-jwt` on the registry, and the `oauth-device-code` flow for human callers on developer machines. The consumer acquires and caches the token, and the registry verifies the presented token against the issuer's JWKS on every call.

> [!WARNING]
> Do not set `oauth-device-code` as the registry's own provider. Startup stops with `config.identity_provider_unverified`, because the registry ships no request-time verifier for it.

Other callers use different paths:

| Caller | Provider | Where it is configured |
| --- | --- | --- |
| Managed runtime, such as Bedrock Agents or a custom orchestrator | `injected-session-token` | At the runtime and the registry, outside these cookbooks. The runtime issues a JWT signed by a key that the registry is configured to trust at startup, and the registry verifies that signature on every call. See the [clustered deployment guide](../clustered#identity-flow). |
| Caller already authenticated by a gateway in front of the registry | `oidc-jwt` verifies the token the gateway forwards; `trusted-headers` reads the identity the gateway injects as headers | At the gateway and the registry. The Podium client sends no credential of its own, so the developer-machine steps in these cookbooks do not apply. See [Gateway-delegated identity](../gateway-delegated-identity). |

## Group resolution paths

Two mechanisms resolve group membership on the registry. Each cookbook names the one its IdP supports.

### SCIM 2.0 push

The IdP pushes user and group records to the registry's SCIM endpoint. The registry maintains a directory of `(user_id → groups)`.

- A layer's `groups:` entry names a SCIM group by its `displayName`. The registry grants visibility when that group holds a member whose SCIM `userName` equals the caller's `sub` or `email` claim.
- Provision each user under a `userName` that the caller's token also carries. A `userName` that matches neither claim resolves to no group visibility and reports no error. The SCIM user's `emails` value is stored but never compared.
- Group-membership changes apply without waiting for the user's next login.

To enable the receiver:

- Set `PODIUM_SCIM_TOKENS` to a comma-separated list of the bearer tokens the registry accepts. The registry mounts `/scim/v2/` only when this variable is set. With it unset, every SCIM request returns 404, and layer `groups:` filters resolve from the token claim alone.
- Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart. A set path that the registry cannot read, parse, or write refuses startup with [`config.scim_store_unavailable`](../../reference/error-codes#config).

Neither variable has a `registry.yaml` key.

### OIDC group claims with IdpGroupMapping

Under `oidc-jwt`, the registry reads group membership from the token's `groups` claim, or from the claim that `PODIUM_OAUTH_GROUPS_CLAIM` (`identity_provider.groups_claim`) names. It maps the raw values to group names through `PODIUM_IDP_GROUP_MAPPING`, a comma-separated list of `<token-value>=<group-name>` pairs.

- A value with no mapping entry passes through unchanged.
- The mapping has no `registry.yaml` key.
- The claim value is a JSON array of strings, or a single string when the IdP emits one group. A single string yields one group whose name is the whole claim value, and the registry does not split it on any separator.
- Group membership reflects what was in the token at login time.

## Outside the scope of the cookbooks

- **TLS termination.** The load balancer or reverse proxy in front of the registry handles it.
- **Network reachability.** Developers must reach the IdP's verification endpoint from their browser to complete the device-code flow. The registry host must reach the issuer's discovery document at `<issuer>/.well-known/openid-configuration` and the JWKS it names. Under `oidc-jwt`, the registry fetches both during startup and refuses to start when either fetch fails. The error begins `oidc-jwt: issuer "<issuer>" is unreachable at startup, refusing to start` and ends with the underlying fetch failure.
- **Registry HTTP-boundary enforcement.** When the registry must enforce who can reach it, place it behind an upstream identity-aware proxy. The proxy authenticates the human caller. The cookbooks configure the IdP that the proxy and the device-code flow use.

## SAML identity providers

The `oidc-jwt` provider verifies OIDC tokens only. A deployment standardized on SAML reaches the registry in one of two ways:

- An upstream OIDC bridge translates SAML assertions into OIDC tokens, which the registry consumes like any other OIDC token.
- A gateway that authenticates the caller over SAML injects `X-Podium-User-Sub`, `X-Podium-User-Email`, `X-Podium-User-Groups`, and `X-Podium-User-Org`, which the registry reads under the `trusted-headers` provider. See [Gateway-delegated identity](../gateway-delegated-identity).

The cookbooks assume OIDC.

## Common pitfalls

| Symptom | Cause | Fix |
| --- | --- | --- |
| Every token is rejected. | Audience mismatch. A token whose `aud` carries none of the values configured under `audience:` is rejected. | Configure the IdP to issue tokens whose `aud` carries one of the registry's `audience:` values. |
| Layers scoped by `groups:` stay hidden. | Some IdPs emit groups as a JSON array of names; others emit IDs. | Map the raw group values to the names used in layer visibility with `IdpGroupMapping`. |
| The device-code flow does not complete. | Some corporate networks block the device-code verification URL. | Test with a developer on the corporate network before declaring success. |
| A caller on a multi-tenant registry sees an empty catalog and no error. | A registry started with `PODIUM_MULTI_TENANT=true` selects each request's tenant from the token's verified `org_id` claim. A token without that claim leaves the request on the registry's reserved no-data tenant. | Configure the IdP to emit `org_id` when the registry runs multi-tenant. |
| Requests are rejected with `auth.tenant_unknown`. | The token's `org_id` names no provisioned tenant. | Provision the tenant, or correct the `org_id` value the IdP emits. |

The [audit log](../observability#audit-log) records the caller identity the registry resolves for each call, which helps confirm what a token carried.
