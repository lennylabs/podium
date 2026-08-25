---
title: Google Workspace
nav_order: 3
description: Configure Podium to authenticate against Google Workspace via OIDC device-code flow.
---

# Google Workspace

This guide configures a Podium registry to authenticate against Google Workspace. Workspace does not emit an OIDC `groups` claim natively. For group-based visibility, groups arrive either through SCIM 2.0 push or as OIDC group claims mapped by the `IdpGroupMapping` adapter.

## Prerequisites

- Google Workspace admin role (or a delegate with the right scopes).
- Podium registry running and reachable from developers' browsers.
- A Google Cloud project for the OAuth client registration.

## 1. Create the OAuth client

Google Cloud Console: **APIs & Services → Credentials → Create Credentials → OAuth client ID**.

- **Application type**: TVs and Limited Input devices (this is the path Google supports for the device-code flow).
- **Name**: Podium.

After creation, you get a **Client ID** and **Client secret**. Note both; Google's device-flow requires the client secret even though the OIDC spec doesn't strictly require it for public clients.

Configure the OAuth consent screen if it's not already configured:

- **User type**: Internal (within the Workspace org).
- **App name**: Podium.
- **Scopes**: `openid`, `email`, `profile`.

## 2. Decide your group-resolution strategy

Three options, in increasing complexity:

**Option A: no groups, email-only visibility.** Set layer visibility based on individual email addresses (`users: [alice@acme.com, bob@acme.com]`) or to `organization: true` (any authenticated user from the Workspace org). Works for small teams.

**Option B: push Workspace groups via SCIM.** Workspace does not push SCIM natively. A Workspace add-on or a sync script populates the registry's SCIM directory, and the registry resolves group membership from that directory. Maintainer-script approach; out of scope here.

**Option C: map OIDC group claims with `IdpGroupMapping`.** When a token carries a group claim (added through a custom OIDC configuration or a directory integration), the `IdpGroupMapping` adapter reads the raw group values from the token and maps them to the group names used in layer visibility. The adapter reads claims already present in the token; it does not call a Cloud Identity API.

For most teams, Option A is enough to start. Move to Option B or C when different layers need to be visible to different Workspace groups.

## 3. Configure Podium

Registry side (`registry.yaml`):

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://accounts.google.com
    audience: <client-id>.apps.googleusercontent.com
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow: it verifies each presented token against Google's JWKS and validates the `aud` claim against `audience:`. A CLI, an SDK, or another API client obtains that token by completing the device-code flow the next step configures, and on a registry that enables the browser flow a browser obtains it through the registry's own authorization-code exchange, which the registry returns in the `__Host-podium_session` cookie. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

For Option C, set `PODIUM_IDP_GROUP_MAPPING` on the registry to a comma-separated list of `<token-value>=<group-name>` pairs, for example `PODIUM_IDP_GROUP_MAPPING=engineering@acme.com=engineering,platform@acme.com=platform`. A raw value with no entry passes through unchanged, the registry reads the token's top-level `groups` claim only, and the mapping has no `registry.yaml` key. A malformed entry is logged and the whole mapping is dropped, so every group value then passes through unmapped. To restrict access to the Workspace domain, place the registry behind an upstream identity-aware proxy that authenticates the human caller and admits only accounts in the domain. Restart the registry.

Developer side:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>.apps.googleusercontent.com
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://oauth2.googleapis.com/device/code
export PODIUM_OAUTH_TOKEN_URL=https://oauth2.googleapis.com/token
podium login --scopes "openid email profile"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL, which produces `https://oauth2.googleapis.com/device/code/token` and the token exchange fails. Pass `--scopes` as shown so the request matches the scopes configured on the consent screen; the default set adds `groups`, which Google does not define.

The verification URL is `https://www.google.com/device`. After the flow completes, `podium login` prints the `sub` and `email`.

`podium login` caches the OAuth access token and later calls present that cached value as `Authorization: Bearer <token>`. The `oidc-jwt` verifier parses the presented credential as a JWT signed by the issuer, and Google issues opaque access tokens, so the cached credential fails to parse and the registry answers `auth.untrusted_token`. The Google credential the registry can verify is the ID token, whose `iss` is `https://accounts.google.com` and whose `aud` is the client ID configured as `audience:` above, and `podium login` does not cache it. Run the registry under [gateway-delegated identity](../gateway-delegated-identity) and have the proxy authenticate the caller against Google. Where a client presents the credential itself, supply the ID token through `PODIUM_SESSION_TOKEN` or `PODIUM_SESSION_TOKEN_FILE` and leave `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` unset, because the MCP bridge reads the keychain token instead of the injected one whenever that endpoint is configured.

`podium login` sends no client secret on the device-authorization or token request, and it reads no client-secret environment variable. A Google OAuth client that requires the secret rejects the token exchange, so the developer-side flow above works only against a client configured to accept a public-client device grant. Where Google requires the secret, terminate the browser flow at an upstream identity-aware proxy and run the registry under [gateway-delegated identity](../gateway-delegated-identity) instead.

## 4. Test

Configure an admin layer visible to the whole organization in `registry.yaml`. Organization-scoped visibility is set in the registry layer config, or with `podium layer register --organization` (also `--public`, `--group`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

```yaml
registry:
  layers:
    - id: team-shared
      source:
        git:
          repo: git@github.com:acme/podium-artifacts.git
          ref: main
      visibility:
        organization: true
```

A user from the Workspace domain sees the layer. To keep accounts outside the domain from reaching the registry at all, the upstream identity-aware proxy admits only Workspace-domain accounts.

## Troubleshooting

- **Token rejected.** Google's `aud` claim is the full client ID with the `.apps.googleusercontent.com` suffix. Confirm the registry's `audience:` matches it exactly.
- **Accounts outside the domain can authenticate.** Domain restriction is enforced by the upstream identity-aware proxy, configured to admit only Workspace-domain accounts. Confirm the proxy is in front of the registry and its allow rule names the domain.
- **Group membership does not update.** With Option B, group changes propagate when the IdP pushes to the registry's SCIM endpoint. With Option C, group membership reflects the token's group claim at login time, so a changed membership applies at the user's next login.
