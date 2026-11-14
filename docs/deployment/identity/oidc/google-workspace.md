---
title: Google Workspace
nav_order: 3
description: Configure a Podium registry for callers who sign in with Google Workspace, through a gateway under trusted-headers or by presenting the Google ID token under oidc-jwt.
---

# Google Workspace

This cookbook configures a Podium registry for callers who sign in with Google Workspace. Workspace does not emit an OIDC `groups` claim natively, so group-based visibility comes from the gateway, from SCIM 2.0 push, or from a group claim that the `IdpGroupMapping` adapter maps. [OIDC](index) is the reference for the `oidc-jwt` settings these steps use.

## Choose how callers reach the registry

Google issues opaque access tokens. The `oidc-jwt` verifier parses each presented credential as a JWT signed by the issuer, so it rejects a Google access token with `auth.untrusted_token`. The rejection covers the access token that `podium login` caches and the access token that the registry's browser flow would return in its session cookie. The Google credential the registry can verify is the ID token, whose `iss` is `https://accounts.google.com` and whose `aud` is the OAuth client ID. `podium login` does not cache it.

These paths reach a verified identity:

- **A gateway under `trusted-headers` (recommended).** An identity-aware proxy in front of the registry authenticates the caller against Google, admits only Workspace-domain accounts, and injects the identity headers. [Gateway (trusted headers)](../trusted-headers) configures the registry side. Clients send no credential of their own, and group membership comes from the gateway's `X-Podium-User-Groups` header, so the group strategies in step 2 other than Option A do not apply.
- **The ID token under `oidc-jwt`.** Steps 1 through 5 configure the registry to verify the Google ID token, and a client supplies that token itself. Podium does not refresh a token supplied this way.

The remaining steps configure the second path.

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
| B: Workspace groups pushed through SCIM | Workspace does not push SCIM natively. A Workspace add-on or a sync script populates the registry's SCIM directory, and the registry resolves group membership from that directory. The sync-script approach is outside the scope of this cookbook. | Different layers visible to different Workspace groups. |
| C: OIDC group claims mapped with `IdpGroupMapping` | When a token carries a group claim, added through a custom OIDC configuration or a directory integration, the adapter reads the raw group values from the token and maps them to the group names used in layer visibility. The adapter reads claims already present in the token and does not call a Cloud Identity API. | Different layers visible to different Workspace groups. |

Option A is enough to start for most teams. Move to Option B or C when different layers need to be visible to different Workspace groups. [Groups and SCIM](../groups) configures both.

## 3. Configure the registry

On the registry host, add the `identity_provider:` block to `registry.yaml`, nested under `registry:` as [File layout](../../../reference/configuration#file-layout) describes:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://accounts.google.com
    audience: <client-id>.apps.googleusercontent.com
```

The registry verifies each presented token against Google's JWKS and requires its `aud` claim to carry the full client ID.

For Option C, set `PODIUM_IDP_GROUP_MAPPING` on the registry to a comma-separated list of `<token-value>=<group-name>` pairs, for example `PODIUM_IDP_GROUP_MAPPING=engineering@acme.com=engineering,platform@acme.com=platform`. A claim value with no entry passes through unchanged, and a malformed value fails startup with `config.invalid_idp_group_mapping`. [OIDC group claims with IdpGroupMapping](../groups#oidc-group-claims-with-idpgroupmapping) describes the format and the claim the registry reads.

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

Expected result: the verification URL is `https://www.google.com/device`. After the flow completes, `podium login` prints the `sub` and `email` it decodes from the ID token. The access token it caches is opaque, so the registry rejects it, and step 5 supplies the credential the registry verifies.

`podium login` sends no client secret on the device-authorization or token request, and it reads no client-secret environment variable. A Google OAuth client that requires the secret rejects the token exchange. The developer-side flow above therefore works only against a client configured to accept a public-client device grant. Where Google requires the secret, use the gateway path that [Choose how callers reach the registry](#choose-how-callers-reach-the-registry) describes.

## 5. Present the ID token

`podium login` caches the OAuth access token, and later calls present that cached value as `Authorization: Bearer <token>`. The registry verifies the Google ID token instead, so supply it to the client:

- Set `PODIUM_SESSION_TOKEN` to the ID token, or `PODIUM_SESSION_TOKEN_FILE` to a file that holds it.
- Leave `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` unset in that client's environment. The MCP server reads the keychain token instead of the supplied one whenever that endpoint is configured.

## 6. Test organization visibility

Configure an admin layer visible to the whole organization in `registry.yaml`. [Declaring visibility](../../content/access-control#declaring-visibility) describes the other ways to set a layer's visibility.

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
| Every call after `podium login` is rejected with `auth.untrusted_token`. | The cached credential is an opaque Google access token, which the verifier cannot parse. | Route callers through a gateway, or present the ID token as step 5 describes. |
| The token is rejected. | Google's `aud` claim is the full client ID with the `.apps.googleusercontent.com` suffix, and the registry's `audience:` differs from it. | Make `audience:` match the full client ID exactly. |
| Accounts outside the domain can authenticate. | The upstream identity-aware proxy enforces domain restriction, and it is missing or admits other domains. | Confirm the proxy is in front of the registry and its allow rule names the domain. |
| Group membership does not update. | With Option B, group changes propagate when the IdP pushes to the registry's SCIM endpoint. With Option C, group membership reflects the token's group claim when the token was issued. | With Option B, confirm the push reached the registry. With Option C, a changed membership applies once the caller presents a newly issued token. |
