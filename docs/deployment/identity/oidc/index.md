---
title: OIDC
nav_order: 1
description: Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak.
---

# OIDC

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
- **Group resolution.** The registry resolves group membership itself, either through SCIM 2.0 push from the IdP or through the `IdpGroupMapping` adapter reading OIDC group claims from the token. [Group resolution paths](../groups#group-resolution-paths) describes both.
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
| Managed runtime, such as Bedrock Agents or a custom orchestrator | `injected-session-token` | At the runtime and the registry, outside these cookbooks. The runtime issues a JWT signed by a key that the registry is configured to trust at startup, and the registry verifies that signature on every call. See the [clustered deployment guide](../../modes/clustered#identity-flow). |
| Caller already authenticated by a gateway in front of the registry | `oidc-jwt` verifies the token the gateway forwards; `trusted-headers` reads the identity the gateway injects as headers | At the gateway and the registry. The Podium client sends no credential of its own, so the developer-machine steps in these cookbooks do not apply. See [Gateway-delegated identity](../trusted-headers). |

## Outside the scope of the cookbooks

- **TLS termination.** The load balancer or reverse proxy in front of the registry handles it.
- **Network reachability.** Developers must reach the IdP's verification endpoint from their browser to complete the device-code flow. The registry host must reach the issuer's discovery document at `<issuer>/.well-known/openid-configuration` and the JWKS it names. Under `oidc-jwt`, the registry fetches both during startup and refuses to start when either fetch fails. The error begins `oidc-jwt: issuer "<issuer>" is unreachable at startup, refusing to start` and ends with the underlying fetch failure.
- **Registry HTTP-boundary enforcement.** When the registry must enforce who can reach it, place it behind an upstream identity-aware proxy. The proxy authenticates the human caller. The cookbooks configure the IdP that the proxy and the device-code flow use.

## SAML identity providers

The `oidc-jwt` provider verifies OIDC tokens only. A deployment standardized on SAML reaches the registry in one of two ways:

- An upstream OIDC bridge translates SAML assertions into OIDC tokens, which the registry consumes like any other OIDC token.
- A gateway that authenticates the caller over SAML injects `X-Podium-User-Sub`, `X-Podium-User-Email`, `X-Podium-User-Groups`, and `X-Podium-User-Org`, which the registry reads under the `trusted-headers` provider. See [Gateway-delegated identity](../trusted-headers).

The cookbooks assume OIDC.

## Common pitfalls

| Symptom | Cause | Fix |
| --- | --- | --- |
| Every token is rejected. | Audience mismatch. A token whose `aud` carries none of the values configured under `audience:` is rejected. | Configure the IdP to issue tokens whose `aud` carries one of the registry's `audience:` values. |
| Layers scoped by `groups:` stay hidden. | Some IdPs emit groups as a JSON array of names; others emit IDs. | Map the raw group values to the names used in layer visibility with `IdpGroupMapping`. |
| The device-code flow does not complete. | Some corporate networks block the device-code verification URL. | Test with a developer on the corporate network before declaring success. |
| A caller on a multi-tenant registry sees an empty catalog and no error. | A registry started with `PODIUM_MULTI_TENANT=true` selects each request's tenant from the token's verified `org_id` claim. A token without that claim leaves the request on the registry's reserved no-data tenant. | Configure the IdP to emit `org_id` when the registry runs multi-tenant. |
| Requests are rejected with `auth.tenant_unknown`. | The token's `org_id` names no provisioned tenant. | Provision the tenant, or correct the `org_id` value the IdP emits. |

The [audit log](../../operations/observability#audit-log) records the caller identity the registry resolves for each call, which helps confirm what a token carried.

## oidc-jwt

The gateway forwards the caller's IdP-signed JWT to the registry. The registry verifies the token on every request against the issuer's JWKS, which it resolves from the issuer's OIDC discovery document.

The same provider covers a directly reachable registry. There, a CLI, an SDK, or another API client presents a token it acquired itself through the device-code flow. Where that registry enables the browser flow, a browser presents the token that the registry obtained for it through its own authorization-code exchange and returned in `__Host-podium_session`. The verification path is identical, because the registry checks the token rather than the network path. The [OIDC cookbooks](index) configure that arrangement per IdP.

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
- The audience decides whether a token is verified at all, and it does not narrow what a caller may do. A caller admitted under any listed value has the same effective view, the same grants, and the same [audit](../../operations/observability#audit-log) identity. A deployment that needs two entrances to one catalog to differ in privilege runs two registries.
- A listed value that another resource server also accepts admits every token that the issuer mints for that server. A client identifier shared across applications has the same effect. List a client identifier only where it is assigned to a single application.
- A comma is legal in a URI and cannot be written in the environment form, which splits on commas. Write such a value in the config file, where a string is one audience verbatim.
- A client process that shares the registry's environment reads `PODIUM_OAUTH_AUDIENCE` as the one value it asks for, and it sends the comma-separated list verbatim. Give a co-located client its own `--audience` or its own environment.
- The registry asks the IdP for the first configured value when it initiates a flow itself, which is the browser sign-in redirect described under [Web UI](../#web-ui). A deployment that enables that flow lists the value its browser client is issued first. The order of the remaining entries does not affect verification.

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

Where the registry enables the browser flow (see [Web UI](../#web-ui)), a request without a `Bearer` credential is anonymous only when it also presents no valid `__Host-podium_session` cookie. The registry still reads the configured token header first. The gateway-fronted deployment this page configures enables no browser flow, so the registry reads no cookie there.
