---
layout: default
title: Auth0
parent: OIDC cookbooks
grand_parent: Deployment
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

This reads the user's groups from `app_metadata`. Populate `app_metadata` through your provisioning process: manually for small teams, or via SCIM for larger setups. The claim emitted is namespaced (`https://podium.acme.com/groups`) because Auth0 disallows non-namespaced custom claims.

## 4. Configure Podium

Registry side (`registry.yaml`):

```yaml
identity_provider:
  type: oauth-device-code
  audience: https://podium.acme.com
  authorization_endpoint: https://<your-tenant>.auth0.com
```

The registry reads the namespaced group claim (`https://podium.acme.com/groups`) and maps its values to group names through the `IdpGroupMapping` adapter configured registry-side. Configure the adapter to read the namespaced claim path that the Action emits. Restart the registry.

Developer side:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUDIENCE=https://podium.acme.com
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<your-tenant>.auth0.com/oauth/device/code
podium login
```

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

Configure an admin layer scoped to a group in the tenant's layer config. Group-scoped visibility is set in the registry layer config, or with `podium layer register --group <name>` (also `--public`, `--organization`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

```yaml
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
- **Custom claim namespace error.** Auth0 rejects non-namespaced custom claims. The claim must look like `https://your-namespace/groups`. A bare `groups` is rejected. Configure `IdpGroupMapping` to read the namespaced claim path.
