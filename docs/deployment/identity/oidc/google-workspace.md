---
title: Google Workspace
nav_order: 3
description: Configure a Podium registry to authenticate callers against Google Workspace through the OIDC device-code flow.
---

# Google Workspace

This cookbook configures a Podium registry to authenticate against Google Workspace. Workspace does not emit an OIDC `groups` claim natively. For group-based visibility, groups arrive either through SCIM 2.0 push or as OIDC group claims mapped by the `IdpGroupMapping` adapter. The [OIDC cookbooks overview](index) describes the identity model the steps rely on.

> [!IMPORTANT]
> The credential that `podium login` caches for Google is an opaque access token, and the registry cannot verify it. Step 5 describes how callers reach the registry instead: through [gateway-delegated identity](../trusted-headers), or by presenting the Google ID token directly.

## Prerequisites

- A Google Workspace admin role, or a delegate with the required scopes.
- A Podium registry running and reachable from developers' browsers.
- A Google Cloud project for the OAuth client registration.

## 1. Create the OAuth client

1. In the Google Cloud Console, open **APIs & Services → Credentials → Create Credentials → OAuth client ID**.
2. Set **Application type** to **TVs and Limited Input devices**. Google supports the device-code flow through this application type.
3. Set **Name** to Podium.
4. After creation, note the **Client ID** and the **Client secret**. Google's device flow requires the client secret, although the OIDC specification does not require it for public clients.
5. If the OAuth consent screen is not configured yet, configure it:
   - **User type**: Internal (within the Workspace org).
   - **App name**: Podium.
   - **Scopes**: `openid`, `email`, and `profile`.

## 2. Choose a group-resolution strategy

The options below are listed in increasing order of setup effort.

| Option | How groups resolve | When it fits |
| --- | --- | --- |
| A: no groups, email-only visibility | Layer visibility lists individual email addresses (`users: [alice@acme.com, bob@acme.com]`), or uses `organization: true` for any authenticated user from the Workspace org. | Small teams. |
| B: Workspace groups pushed through SCIM | Workspace does not push SCIM natively. A Workspace add-on or a sync script populates the registry's SCIM directory, and the registry resolves group membership from that directory. The maintainer-script approach is outside the scope of this cookbook. | Different layers visible to different Workspace groups. |
| C: OIDC group claims mapped with `IdpGroupMapping` | When a token carries a group claim, added through a custom OIDC configuration or a directory integration, the adapter reads the raw group values from the token and maps them to the group names used in layer visibility. The adapter reads claims already present in the token and does not call a Cloud Identity API. | Different layers visible to different Workspace groups. |

Option A is enough to start for most teams. Move to Option B or C when different layers need to be visible to different Workspace groups.

## 3. Configure the registry

On the registry host, add the `identity_provider:` block to `registry.yaml`:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://accounts.google.com
    audience: <client-id>.apps.googleusercontent.com
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config, and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow. It verifies each presented token against Google's JWKS and validates the `aud` claim against `audience:`.

- A CLI, an SDK, or another API client obtains the token through the device-code flow that the next step configures.
- On a registry that enables the browser flow, a browser obtains the token through the registry's own authorization-code exchange, and the registry returns it in the `__Host-podium_session` cookie.
- Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

For Option C, set `PODIUM_IDP_GROUP_MAPPING` on the registry to a comma-separated list of `<token-value>=<group-name>` pairs, for example `PODIUM_IDP_GROUP_MAPPING=engineering@acme.com=engineering,platform@acme.com=platform`.

- A claim value with no table entry passes through unchanged.
- The registry reads the `groups` claim unless `PODIUM_OAUTH_GROUPS_CLAIM` or `identity_provider.groups_claim` names another claim.
- The mapping has no `registry.yaml` key.
- A non-empty value that carries a malformed entry, or that resolves to no `<token-value>=<group-name>` entry at all, fails startup with `config.invalid_idp_group_mapping`. The registry refuses the whole setting instead of starting with a partial table, so one malformed entry discards the well-formed entries beside it.

To restrict access to the Workspace domain, place the registry behind an upstream identity-aware proxy that authenticates the human caller and admits only accounts in the domain.

Restart the registry.

## 4. Configure developer machines

On each developer machine, run:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>.apps.googleusercontent.com
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://oauth2.googleapis.com/device/code
export PODIUM_OAUTH_TOKEN_URL=https://oauth2.googleapis.com/token
podium login --scopes "openid email profile"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL. That produces `https://oauth2.googleapis.com/device/code/token`, and the token exchange fails.

Pass `--scopes` as shown so the request matches the scopes configured on the consent screen. The default set adds `groups`, which Google does not define.

Expected result: the verification URL is `https://www.google.com/device`. After the flow completes, `podium login` prints the `sub` and `email`.

`podium login` sends no client secret on the device-authorization or token request, and it reads no client-secret environment variable. A Google OAuth client that requires the secret rejects the token exchange. The developer-side flow above therefore works only against a client configured to accept a public-client device grant. Where Google requires the secret, terminate the browser flow at an upstream identity-aware proxy and run the registry under [gateway-delegated identity](../trusted-headers) instead.

## 5. Route callers to a verifiable credential

`podium login` caches the OAuth access token, and later calls present that cached value as `Authorization: Bearer <token>`. The `oidc-jwt` verifier parses the presented credential as a JWT signed by the issuer. Google issues opaque access tokens, so the cached credential fails to parse, and the registry answers `auth.untrusted_token`.

The Google credential the registry can verify is the ID token. Its `iss` is `https://accounts.google.com`, and its `aud` is the client ID configured as `audience:` in step 3. `podium login` does not cache it. Choose one of these paths:

- Run the registry under [gateway-delegated identity](../trusted-headers), and have the proxy authenticate the caller against Google.
- Where a client presents the credential itself, supply the ID token through `PODIUM_SESSION_TOKEN` or `PODIUM_SESSION_TOKEN_FILE`, and leave `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` unset. The MCP bridge reads the keychain token instead of the injected one whenever that endpoint is configured.

## 6. Test organization visibility

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

Expected result: a user from the Workspace domain sees the layer. To keep accounts outside the domain from reaching the registry at all, configure the upstream identity-aware proxy to admit only Workspace-domain accounts.

## Next steps

- Declare visibility on the remaining layers with [Access control](../../content/access-control).
- Review the caller identities the registry records in the [audit log](../../operations/observability#audit-log).

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Every call after `podium login` is rejected with `auth.untrusted_token`. | The cached credential is an opaque Google access token, which the verifier cannot parse. | Route callers through a gateway or present the ID token, as step 5 describes. |
| The token is rejected. | Google's `aud` claim is the full client ID with the `.apps.googleusercontent.com` suffix, and the registry's `audience:` differs from it. | Make `audience:` match the full client ID exactly. |
| Accounts outside the domain can authenticate. | The upstream identity-aware proxy enforces domain restriction, and it is missing or admits other domains. | Confirm the proxy is in front of the registry and its allow rule names the domain. |
| Group membership does not update. | With Option B, group changes propagate when the IdP pushes to the registry's SCIM endpoint. With Option C, group membership reflects the token's group claim at login time. | With Option B, confirm the push reached the registry. With Option C, a changed membership applies at the user's next login. |
