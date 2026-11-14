---
title: OIDC
nav_order: 1
description: "The oidc-jwt identity provider: configuration, settings, audiences, issuer and claim matching, and request outcomes, followed by the per-IdP cookbooks and common pitfalls."
---

# OIDC

Under the `oidc-jwt` identity provider, the registry verifies an IdP-signed JWT on every request. This page is the reference for `oidc-jwt`. The [cookbooks](#cookbooks) apply it to a specific identity provider (IdP), together with the device-code flow that developer machines use to acquire tokens. [Identity](../) compares `oidc-jwt` with the other providers.

## How OIDC identity works

- **Token acquisition.** A CLI, an SDK, or another API client acquires a token from the IdP through the `oauth-device-code` flow and caches it in the OS keychain. On a registry that enables the browser flow, the registry obtains a token for the browser through its own authorization-code exchange and returns it in the `__Host-podium_session` cookie. A gateway in front of the registry can also authenticate the caller and forward the caller's token.
- **Token verification.** The registry verifies each presented token against the issuer's JWKS, which it resolves from the discovery document at `<issuer>/.well-known/openid-configuration`. It selects the signing key by `kid`, accepts only asymmetric signing algorithms (RSA, ECDSA, and EdDSA), and validates `iss`, `aud`, `exp`, and `nbf`.
- **Claims.** The registry records the caller's subject (`sub` unless [`subject_claim`](#subject-claim) names another claim) and `email`, and reads group membership from the `groups` claim unless `groups_claim` names another. On a multi-tenant registry it reads the tenant from the `org_id` claim, as [Tenants and admins](../tenants) describes.
- **Group resolution.** The registry resolves group membership from the token's group claim through the `IdpGroupMapping` adapter, and from a directory the IdP pushes over SCIM 2.0. [Groups and SCIM](../groups) describes both.

The registry checks the token rather than the network path, so the same verification serves a registry that clients reach directly and one behind a gateway.

## Configure oidc-jwt

1. Decide where the token arrives. A client that reaches the registry directly presents it as `Authorization: Bearer <token>`. A gateway forwards it as `Bearer <token>` in `Authorization`, or in a custom header such as `X-Forwarded-Access-Token` that `token_header` names. The gateway does not need to strip a client-supplied token, because a forged token fails verification.
2. Add the `identity_provider:` block to `registry.yaml`. Every server key nests under the top-level `registry:` mapping, as [File layout](../../../reference/configuration#file-layout) describes.

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

## oidc-jwt settings

| Setting | Environment override | Default | Notes |
| --- | --- | --- | --- |
| `identity_provider.issuer` | `PODIUM_OAUTH_ISSUER` | required | Must use `https`. The registry derives the JWKS from `<issuer>/.well-known/openid-configuration`. |
| `identity_provider.audience` | `PODIUM_OAUTH_AUDIENCE` | required | Audience values this registry answers to. A token is accepted when its `aud` claim carries at least one configured value. The config-file key takes a string or a list of strings, and the environment variable takes a comma-separated list. |
| `identity_provider.token_header` | `PODIUM_OAUTH_TOKEN_HEADER` | `Authorization` | Header carrying the token, parsed as `Bearer <token>` for any header name. |
| `identity_provider.jwks_cache_ttl_seconds` | `PODIUM_OAUTH_JWKS_CACHE_TTL_SECONDS` | `300` | A `kid` absent from the cached set forces an earlier refresh. |
| `identity_provider.subject_claim` | `PODIUM_OAUTH_SUBJECT_CLAIM` | `(unset; sub)` | Claim read as the caller's subject. When it is set the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. AD FS access tokens carry `idsub` and no `sub`. |
| `identity_provider.groups_claim` | `PODIUM_OAUTH_GROUPS_CLAIM` | `(unset; groups)` | Claim read for group membership. AD FS issuance rules emit the full claim-type URI (`http://schemas.microsoft.com/ws/2008/06/identity/claims/groups`) unless authored with a short name. Single-value and multi-value forms are both accepted. |

## Multiple audiences

A deployment lists more than one audience value when the same registry is addressed by more than one audience from one trusted issuer.

- The OAuth device authorization grant is the case this covers. That request carries `client_id` and `scope` and cannot name a target resource. An IdP that stamps the client identifier on the resulting access token therefore issues a different `aud` than it issues for a token exchange that names the registry's API URI.
- Each listed value is a trust decision the operator makes at startup. The set never grows at runtime.
- The audience decides whether a token is verified at all, and it does not narrow what a caller may do. A caller admitted under any listed value has the same effective view, the same grants, and the same [audit](../../operations/observability#audit-log) identity. A deployment that needs two entrances to one catalog to differ in privilege runs two registries.
- A listed value that another resource server also accepts admits every token that the issuer mints for that server. A client identifier shared across applications has the same effect. List a client identifier only where it is assigned to a single application.
- A comma is legal in a URI and cannot be written in the environment form, which splits on commas. Write such a value in the config file, where a string is one audience verbatim.
- A client process that shares the registry's environment reads `PODIUM_OAUTH_AUDIENCE` as the one value it asks for, and it sends the comma-separated list verbatim. Give a co-located client its own `--audience` or its own environment.
- The registry asks the IdP for the first configured value when it initiates a flow itself, which is the browser sign-in redirect described under [Web UI](../#web-ui). A deployment that enables that flow lists the value its browser client is issued first. The order of the remaining entries does not affect verification.

## Issuer matching

The registry accepts a token's `iss` when it matches the configured issuer, or the `access_token_issuer` published by the discovery document that the issuer resolves. It trims a trailing slash from the configured issuer and from the token's `iss` before comparing them, so an issuer URL written either way is accepted.

AD FS needs the second form. It serves discovery under `https://<host>/adfs` and stamps the federation-service identifier `http://<host>/adfs/services/trust` on the access token, so no single configured value covers both roles. The signing keys still come from the `jwks_uri` in that same `https` document, so the set of trusted keys is unchanged. When the document publishes an `access_token_issuer` that differs from the configured issuer, the registry logs both accepted values at startup.

## Subject claim

A deployment that sets `identity_provider.subject_claim` lists values of the named claim in its `users:` entries, in `PODIUM_OPERATOR_ADMINS`, and in `PODIUM_BOOTSTRAP_ADMINS`. The instance-operator grant and the per-tenant admin grants match the recorded subject and have no email fallback. A `sub` value left in either variable grants nothing once the registry records another claim as the subject. The named claim must also identify one principal for the life of the deployment, because the recorded subject carries layer visibility, user-defined layer ownership, and admin and operator grants.

## Group membership under oidc-jwt

The registry reads group membership from the token's `groups` claim, or from the claim that `groups_claim` names, on every verified token. The `IdpGroupMapping` adapter rewrites the claim's values to registry-side group names, and it names no claim itself. A registry that also mounts the SCIM receiver matches SCIM-resolved membership in addition to the claim-derived groups. [Groups and SCIM](../groups) configures both.

## Request outcomes

| Request | Outcome |
| --- | --- |
| The token fails signature, `iss`, or `aud` validation | Rejected with `auth.untrusted_token`. |
| The token has expired | Rejected with `auth.token_expired`. |
| The configured token header carries no `Bearer` credential | Anonymous, and sees public visibility only. |
| The issuer's JWKS is unreachable | Verification fails closed, and the request is anonymous rather than rejected. |

On a registry that enables the browser flow (see [Web UI](../#web-ui)), a request without a `Bearer` credential is anonymous only when it also presents no valid `__Host-podium_session` cookie. The registry still reads the configured token header first. A registry with the browser flow disabled reads no cookie.

## Cookbooks

Each cookbook configures `oidc-jwt` on a registry already running at a known URL, for example `https://podium.acme.com`, and the device-code flow on developer machines.

| IdP | Cookbook | Notes |
| --- | --- | --- |
| Okta | [Okta](okta) | Native group claim support; SCIM available. |
| Microsoft Entra ID | [Microsoft Entra ID](entra-id) | Formerly Azure AD. The group claim emits group IDs instead of names, so `IdpGroupMapping` resolves them to names. |
| Google Workspace | [Google Workspace](google-workspace) | Google issues opaque access tokens, which `oidc-jwt` cannot verify. The cookbook recommends a gateway under `trusted-headers`. |
| Auth0 | [Auth0](auth0) | An Action adds the group claim under a namespaced path that `groups_claim` names. |
| Keycloak | [Keycloak](keycloak) | Self-hosted; a mapper adds the group claim. The Docker Compose evaluation stack uses Dex instead. |

## What each cookbook produces

Each cookbook ends with a `registry.yaml` `identity_provider:` block of this form:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<idp-issuer-url>           # must be https
    audience: https://podium.acme.com          # or api://podium, per IdP convention
```

Each cookbook includes a `podium login` run from a developer machine. Where the flow completes, the command prints the identity it decodes from the ID token: the `sub`, the `email`, and the groups the token carries.

Every cookbook except Google Workspace ends with a credential the registry accepts. For Google, `podium login` caches an opaque access token that the verifier cannot parse, so that cookbook routes callers through a gateway instead.

## Human callers and managed runtimes

The cookbooks configure `oidc-jwt` on the registry and the `oauth-device-code` flow for human callers on developer machines. The consumer acquires and caches the token, and the registry verifies the presented token on every call.

> [!WARNING]
> Do not set `oauth-device-code` as the registry's own provider. Startup stops with `config.identity_provider_unverified`, because the registry ships no request-time verifier for it.

Other callers use different paths:

| Caller | Provider | Where it is configured |
| --- | --- | --- |
| Managed runtime, such as Bedrock Agents or a custom orchestrator | `injected-session-token` | At the runtime and the registry, outside these cookbooks. The runtime issues a JWT signed by a key that the registry trusts at startup, and the registry verifies that signature on every call. See [Identity flow](../../modes/clustered#identity-flow). |
| Caller already authenticated by a gateway in front of the registry | `oidc-jwt` verifies the token the gateway forwards; `trusted-headers` reads the identity the gateway injects as headers | At the gateway and the registry. The Podium client sends no credential of its own, so the developer-machine steps in these cookbooks do not apply. See [Configure oidc-jwt](#configure-oidc-jwt) and [Gateway (trusted headers)](../trusted-headers). |

## Outside the scope of the cookbooks

- **TLS termination.** The load balancer or reverse proxy in front of the registry handles it.
- **Network reachability.** Developers must reach the IdP's verification endpoint from their browser to complete the device-code flow. The registry host must reach the issuer's discovery document and the JWKS it names, because the registry fetches both during startup and refuses to start when either fetch fails. The error begins `oidc-jwt: issuer "<issuer>" is unreachable at startup, refusing to start` and ends with the underlying fetch failure.
- **Registry HTTP-boundary enforcement.** `oidc-jwt` admits an anonymous request with public visibility. When the registry must restrict who can reach it at all, place it behind an upstream identity-aware proxy that authenticates the human caller. The cookbooks configure the IdP that the proxy and the device-code flow use.

## SAML identity providers

The `oidc-jwt` provider verifies OIDC tokens only. A deployment standardized on SAML reaches the registry in one of two ways:

- An upstream OIDC bridge translates SAML assertions into OIDC tokens, which the registry consumes like any other OIDC token.
- A gateway that authenticates the caller over SAML injects `X-Podium-User-Sub`, `X-Podium-User-Email`, `X-Podium-User-Groups`, and `X-Podium-User-Org`, which the registry reads under the `trusted-headers` provider. See [Gateway (trusted headers)](../trusted-headers).

## Common pitfalls

| Symptom | Cause | Fix |
| --- | --- | --- |
| Every token is rejected. | Audience mismatch. A token whose `aud` carries none of the values configured under `audience:` is rejected. | Configure the IdP to issue tokens whose `aud` carries one of the registry's `audience:` values. |
| The registry ignores the `identity_provider:` block and starts with no provider. | The block is not nested under `registry:`. A document that starts at `identity_provider:` parses to an empty config, and the registry reports no error. | Nest the block under `registry:`, as [File layout](../../../reference/configuration#file-layout) describes. |
| Layers scoped by `groups:` stay hidden. | Some IdPs emit groups as a JSON array of names; others emit IDs. | Map the raw group values to the names used in layer visibility with `IdpGroupMapping`. |
| The device-code flow does not complete. | Some corporate networks block the device-code verification URL. | Test with a developer on the corporate network before declaring success. |
| A caller on a multi-tenant registry sees an empty catalog and no error. | A registry started with `PODIUM_MULTI_TENANT=true` selects each request's tenant from the token's verified `org_id` claim. A token without that claim leaves the request on the registry's reserved no-data tenant. | Configure the IdP to emit `org_id` when the registry runs multi-tenant. |
| Requests are rejected with `auth.tenant_unknown`. | The token's `org_id` names no provisioned tenant. | Provision the tenant, or correct the `org_id` value the IdP emits. |

The [audit log](../../operations/observability#audit-log) records the caller identity the registry resolves for each call, which confirms what a token carried.
