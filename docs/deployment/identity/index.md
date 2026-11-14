---
title: Identity
nav_order: 2
description: "How a registry server identifies its callers: the identity providers, the consumer and registry sides, choosing a provider, the default layer visibility, the web UI, and the startup guards."
---

# Identity

A registry server identifies each caller to filter layer visibility, to authorize admin and operator actions, and to record the caller in the audit log. The registry keeps no user database. It reads the caller's identity from a credential that an identity provider (IdP) issued, or from headers that a gateway in front of the registry injects.

This page describes the identity providers and how to choose one. The other pages in this section configure each provider, tenants, and group membership. A local deployment reads layers from the filesystem with no registry server, so it has no identity provider, and every caller sees every layer.

## Identity providers

The registry process selects its provider with `PODIUM_IDENTITY_PROVIDER`, or with `identity_provider.type` in `registry.yaml`.

| Registry provider | How the registry resolves the caller | Configured in |
| --- | --- | --- |
| Unset | It resolves no caller. Every request is anonymous and sees every layer. | [Running without a provider](#running-without-a-provider) |
| `oidc-jwt` | It verifies an IdP-signed JWT on every request against the issuer's JWKS. The client acquired the token, a gateway forwarded it, or the registry obtained it for a browser. | [OIDC](oidc/) |
| `trusted-headers` | It reads the identity that a fronting gateway injects as request headers, and does not verify it. | [Gateway (trusted headers)](trusted-headers) |
| `injected-session-token` | It verifies a per-session JWT that a managed runtime signs, against the runtime keys that `PODIUM_RUNTIME_KEYS_PATH` names. | [Clustered](../modes/clustered#identity-flow) |

`oauth-device-code` names the flow a client runs to acquire a token. The registry verifies those tokens under `oidc-jwt`, so setting `oauth-device-code` as the registry's provider stops startup with `config.identity_provider_unverified`.

Each provider applies on a single-node and on a clustered deployment, and each is mutually exclusive with public mode.

### Running without a provider

A registry server started with no identity provider resolves every caller as anonymous, and the visibility evaluator admits every layer for every caller. No caller can hold the tenant `admin` role, so [Layer writes without an identity provider](../content/access-control#layer-writes-without-an-identity-provider) describes how the layer write rules apply. This arrangement suits a single user, or a registry reachable only from the loopback interface.

Public mode (`--public-mode` or `PODIUM_PUBLIC_MODE`) also admits every caller to every layer. It records `system:public` as the audit caller, rejects the ingest of `medium` and `high` sensitivity artifacts, and binds to the loopback address unless `--allow-public-bind` is set. Setting it together with an identity provider fails startup with `config.public_mode_with_idp`. [Public mode](../modes/single-node#public-mode) describes it.

## Consumer side and registry side

Identity is configured in the client process and in the registry process, and each reads its own `PODIUM_IDENTITY_PROVIDER`.

- **Consumer side.** The client process (`podium`, `podium-mcp`, or an SDK) acquires a credential and presents it on each call. The MCP server admits `oauth-device-code`, the default, and `injected-session-token`, and it refuses `oidc-jwt` and `trusted-headers` at startup. Under `oauth-device-code`, `podium login`, or the MCP server on first use, runs the device-code flow and caches the access token in the OS keychain. Under `injected-session-token`, the host supplies the token in `PODIUM_SESSION_TOKEN` or in the file that `PODIUM_SESSION_TOKEN_FILE` names.
- **Registry side.** The registry process verifies the credential, or reads the gateway's headers, under the provider in the [Identity providers](#identity-providers) table.

The sides pair as follows.

| Caller | Consumer side | Registry side |
| --- | --- | --- |
| A developer's CLI, MCP server, or SDK | `oauth-device-code` | `oidc-jwt` |
| A browser on the web UI | The registry's browser flow | `oidc-jwt` with the browser flow enabled |
| A caller that a gateway has already authenticated | No credential. The gateway supplies the identity. | `trusted-headers`, or `oidc-jwt` when the gateway forwards a JWT |
| A managed runtime, such as Bedrock Agents or a custom orchestrator | `injected-session-token` | `injected-session-token` |

## Choosing a provider

- Use `oidc-jwt` when the IdP issues JWT access tokens that the registry can verify. The registry trusts the issuer's signing key alone and no element of the network path, so it can be reachable directly or through a gateway, and it carries no bind restriction. [OIDC](oidc/) configures it, and its cookbooks cover Okta, Entra ID, Auth0, Keycloak, and Google Workspace.
- Use `trusted-headers` when a gateway authenticates the caller and can forward identity only as headers. SAML, a bespoke corporate SSO, and an IdP whose access tokens are opaque, such as Google Workspace, fall in this case. The registry must be reachable only through the gateway. [Gateway (trusted headers)](trusted-headers) configures it.
- Use `injected-session-token` for a managed runtime that signs a token per session.
- Run without a provider, or in public mode, only when every caller may see every layer.

A multi-tenant registry selects each request's tenant from the verified identity, so it requires `oidc-jwt`, `trusted-headers`, or `injected-session-token`. [Tenants and admins](tenants) describes the tenant routing under each provider.

## Layer visibility default

Enabling an identity provider changes the default visibility of a new admin-defined layer. On a registry with no identity provider, a layer registered without a visibility declaration is `public`. With a provider enabled and `PODIUM_DEFAULT_LAYER_VISIBILITY` unset, the default is `private`, so an admin layer is not public to every caller once the registry filters by identity. The registry applies an explicit `PODIUM_DEFAULT_LAYER_VISIBILITY` value unchanged under every provider. See [Deployment defaults](../content/access-control#deployment-defaults).

## Web UI

The registry process serves the web UI when it starts with `--web-ui` or `PODIUM_WEB_UI=true`. The UI calls the registry's HTTP API, so each UI request resolves its caller exactly as any other API request does.

- **Behind a gateway.** The gateway authenticates the browser and forwards a token or injects identity headers. The UI carries no sign-in flow of its own.
- **Directly reachable under `oidc-jwt`.** The registry verifies the token each request presents. It can also sign the browser in itself when the browser flow is enabled with `--web-ui-auth` or `PODIUM_WEB_UI_AUTH`. The registry then redirects the browser to the IdP, exchanges the authorization code server-side, and returns the access token in the `__Host-podium_session` cookie. The [browser session](../../reference/http-api#browser-session) reference documents the cookies and routes, and [config.web_ui_auth_unconfigured requirements](../../reference/error-codes#configweb_ui_auth_unconfigured-requirements) lists the settings the flow requires.

With the browser flow disabled, a browser request whose configured token header carries no `Bearer` credential resolves as anonymous and sees public visibility only. With the flow enabled, such a request resolves as anonymous only when it also presents no valid session cookie. The registry reads the configured token header first in both cases.

The registry refuses to serve the UI on a non-loopback address unless `--web-ui-allow-public-bind` is set and an identity provider is configured. A non-loopback bind under `trusted-headers` is also subject to that provider's [bind restriction](trusted-headers#bind-restriction).

The registry refuses a state-changing request that carries cross-site browser-origin evidence with `403 auth.csrf_invalid`. A gateway that rewrites the `Host` header makes every write from the UI fail that check, as [A gateway that rewrites Host](trusted-headers#a-gateway-that-rewrites-host) describes.

## Startup guards

The registry fails closed on an identity misconfiguration, so it does not serve with an identity it cannot verify or a header it cannot trust. The [error-code catalog](../../reference/error-codes#config) describes each code.

| Code | Configuration the registry refuses |
| --- | --- |
| `config.public_mode_with_idp` | Public mode together with an identity provider. |
| `config.identity_provider_unverified` | A provider that the registry does not verify at request time, such as `oauth-device-code`. |
| `config.multitenant_no_verifier` | `PODIUM_MULTI_TENANT=true` with no verified identity provider. |
| `config.invalid_issuer_scheme` | An `oidc-jwt` issuer that is not an `https` URL. |
| `config.oidc_jwt_audience_unset` | `oidc-jwt` with no audience. |
| `config.trusted_headers_public_bind` | A single-tenant `trusted-headers` registry on a non-loopback bind with neither `PODIUM_TRUSTED_PROXY_SECRET` nor `--allow-public-bind`. |
| `config.trusted_headers_multitenant_no_secret` | A multi-tenant `trusted-headers` registry with no `PODIUM_TRUSTED_PROXY_SECRET`. |
| `config.injected_token_audience_unset` | `injected-session-token` with no audience. |
| `config.runtime_keys_unavailable` | A `PODIUM_RUNTIME_KEYS_PATH` file the registry cannot read or parse, or `injected-session-token` with no runtime key. |
| `config.web_ui_auth_unconfigured` | The browser flow enabled without the settings it requires. |
| `config.invalid_idp_group_mapping` | A non-empty `PODIUM_IDP_GROUP_MAPPING` that does not parse to a mapping table. |
| `config.scim_store_unavailable` | A `PODIUM_SCIM_STORE_PATH` that the registry cannot read, parse, or write. |

Under `oidc-jwt`, the registry also fetches the issuer's discovery document and JWKS at startup, and refuses to start when either fetch fails. That error begins `oidc-jwt: issuer "<issuer>" is unreachable at startup, refusing to start`.

## Pages in this section

| Page | Covers |
| --- | --- |
| [OIDC](oidc/) | The `oidc-jwt` settings, and cookbooks for Okta, Microsoft Entra ID, Google Workspace, Auth0, and Keycloak. |
| [Gateway (trusted headers)](trusted-headers) | The `trusted-headers` provider, its bind restriction, and gateway requirements. |
| [Tenants and admins](tenants) | Single-tenant and multi-tenant registries, the operator and admin roles, and tenant provisioning. |
| [Groups and SCIM](groups) | Where group membership comes from, OIDC group claims, and SCIM 2.0 push. |
