---
title: Auth0
nav_order: 4
description: Configure Podium to authenticate against Auth0 via OIDC device-code flow.
---

# Auth0

This guide configures a Podium registry to authenticate against Auth0. Setup takes about 15 minutes. Group claims are not native to Auth0; an Action adds them, or a legacy Rule does.

## Prerequisites

- Auth0 tenant with admin access.
- Podium registry running and reachable from developers' browsers.
- Decide your audience identifier (suggestion: `https://podium.acme.com`; Auth0 conventionally uses URL-shaped audiences).

## 1. Create the API in Auth0

Auth0 dashboard: **Applications → APIs → Create API**.

- **Name**: Podium.
- **Identifier**: `https://podium.acme.com` (this is the `audience`).
- **Signing Algorithm**: RS256.

Save. The API is now what tokens are issued for.

## 2. Create the application

Dashboard: **Applications → Applications → Create Application**.

- **Name**: Podium CLI.
- **Type**: **Native** (best fit for the device-code flow).

In the new application's settings:

- **Token Endpoint Authentication Method**: None (public client).
- **Grant Types**: enable **Device Code** and **Refresh Token**.
- Save.

Note from the app's settings tab: **Client ID**.

Connect the application to the API:

- **APIs** tab in the application → toggle **Authorized** for the Podium API.
- Permissions: leave default. Podium uses identity claims rather than Auth0-issued permissions.

## 3. Add the groups claim via an Action

Dashboard: **Actions → Library → Build Custom**.

- **Name**: Add groups to access token.
- **Trigger**: Login / Post Login.

Action code:

```javascript
exports.onExecutePostLogin = async (event, api) => {
  const namespace = "https://podium.acme.com/";
  const groups = (event.user.app_metadata && event.user.app_metadata.groups) || [];
  api.idToken.setCustomClaim(`${namespace}groups`, groups);
  api.accessToken.setCustomClaim(`${namespace}groups`, groups);
};
```

Save and deploy. Then attach the Action: **Actions → Triggers → post-login → drag the new Action into the flow → Apply**.

This reads the user's groups from `app_metadata`. Populate `app_metadata` through your provisioning process: manually for small teams, or via SCIM for larger setups.

The registry reads group membership from the top-level `groups` claim only. The claim path is not configurable, and `IdpGroupMapping` rewrites group values rather than redirecting the registry to another claim. A namespaced claim such as `https://podium.acme.com/groups` therefore never reaches the visibility evaluator. Emit the claim as a top-level `groups` array on the access token:

```javascript
exports.onExecutePostLogin = async (event, api) => {
  const groups = (event.user.app_metadata && event.user.app_metadata.groups) || [];
  api.accessToken.setCustomClaim("groups", groups);
};
```

Auth0 restricts non-namespaced custom claims, so confirm against your tenant that the access token carries a top-level `groups` array before relying on group-scoped layers. Where it does not, use `users:` visibility, or resolve membership through SCIM instead of the token claim.

## 4. Configure Podium

Registry side (`registry.yaml`):

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<your-tenant>.auth0.com/   # must be https; Auth0 issuers end in a slash
    audience: https://podium.acme.com
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow: it verifies each presented token against the issuer's JWKS and validates the `aud` claim against `audience:`. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

The registry strips trailing slashes from the configured `issuer:` and compares the token's `iss` claim against the stripped value without stripping the claim. Auth0 issues every token with an `iss` that ends in a slash, so `https://<your-tenant>.auth0.com/` and `https://<your-tenant>.auth0.com` reduce to the same stripped value and every Auth0 token is rejected with `auth.untrusted_token` and the message `iss "https://<your-tenant>.auth0.com/" does not match the configured issuer "https://<your-tenant>.auth0.com"`. No `issuer:` value changes that comparison, so `oidc-jwt` cannot verify an Auth0 token.

Authenticate Auth0 callers through a gateway that completes the Auth0 login and injects `X-Podium-User-Sub`, `X-Podium-User-Email`, `X-Podium-User-Groups`, and `X-Podium-User-Org`, with the registry's identity provider set to `trusted-headers`; see [gateway-delegated identity](../gateway-delegated-identity). Under `trusted-headers` the gateway supplies the caller's groups, so the Action in step 3 and the developer-side device-code commands below do not apply.

The registry reads the top-level `groups` claim and maps its values to the names layer visibility uses through `PODIUM_IDP_GROUP_MAPPING`, a comma-separated list of `<token-value>=<group-name>` pairs. A value with no entry passes through unchanged, so a token that already carries the layer group names needs no mapping. Restart the registry.

Developer side:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUDIENCE=https://podium.acme.com
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<your-tenant>.auth0.com/oauth/device/code
export PODIUM_OAUTH_TOKEN_URL=https://<your-tenant>.auth0.com/oauth/token
podium login
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL, which produces `https://<your-tenant>.auth0.com/oauth/device/code/token` and the token exchange fails.

The device-flow verification URL is `https://<your-tenant>.auth0.com/activate`. After completion, `podium login` prints the resolved identity.

## 5. Populate user groups

For small teams, edit `app_metadata` per user manually:

```json
{
  "groups": ["engineering", "platform"]
}
```

(Dashboard: **User Management → Users → \[user\] → Metadata → app_metadata**.)

For larger setups, populate via SCIM (Auth0 Enterprise) or via a directory sync script.

## 6. Test

Configure an admin layer scoped to a group in `registry.yaml`. Group-scoped visibility is set in the registry layer config, or with `podium layer register --group <name>` (also `--public`, `--organization`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

```yaml
registry:
  layers:
    - id: engineering-only
      source:
        git:
          repo: git@github.com:acme/podium-engineering.git
          ref: main
      visibility:
        groups: [engineering]
```

A user with `engineering` in `app_metadata.groups` sees the layer; a user without it does not.

## Troubleshooting

- **Groups claim is missing from the token.** The Action was not attached to the post-login trigger, or the user has no `groups` in `app_metadata`. Check the Action's logs: **Actions → Library → \[Action\] → Logs**.
- **Token rejected.** The token's `aud` must match the registry's `audience:`. Confirm the API identifier and `audience:` match exactly.
- **Token rejected with `auth.untrusted_token` naming an `iss` mismatch.** The registry strips trailing slashes from the configured `issuer:` and compares the token's `iss` claim against the stripped value without stripping the claim. Auth0 issues tokens whose `iss` ends in a slash, so the comparison never matches and no `issuer:` value repairs it. Authenticate through a gateway under the `trusted-headers` provider instead, as step 4 describes; see [gateway-delegated identity](../gateway-delegated-identity).
- **Groups arrive in the token but every group-scoped layer is invisible.** The claim is namespaced. The registry reads the top-level `groups` claim only and has no setting for a different claim path, so a namespaced claim resolves to no groups. Inspect a real access token and confirm the claim key is exactly `groups`.
