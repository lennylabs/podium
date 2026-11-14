---
title: Auth0
nav_order: 4
description: Configure a Podium registry to authenticate callers against Auth0 through the OIDC device-code flow.
---

# Auth0

This cookbook configures a Podium registry to authenticate against Auth0. Group claims are not native to Auth0, so an Action adds them, or a legacy Rule does. Setup takes about 15 minutes. The [OIDC cookbooks overview](index) describes the identity model the steps rely on.

## Prerequisites

- An Auth0 tenant with admin access.
- A Podium registry running and reachable from developers' browsers.
- An audience identifier for the registry. This cookbook uses `https://podium.acme.com`, because Auth0 conventionally uses URL audiences.

## 1. Create the API

1. In the Auth0 dashboard, open **Applications → APIs → Create API**.
2. Set **Name** to Podium.
3. Set **Identifier** to `https://podium.acme.com`. This value is the `audience`.
4. Set **Signing Algorithm** to RS256.
5. Save.

Auth0 now issues tokens for this API.

## 2. Create the application

1. Open **Applications → Applications → Create Application**.
2. Set **Name** to Podium CLI.
3. Set **Type** to **Native**, which fits the device-code flow.
4. In the new application's settings, set **Token Endpoint Authentication Method** to None (public client).
5. Under **Grant Types**, enable **Device Code** and **Refresh Token**, and save.
6. Note the **Client ID** from the application's settings tab.
7. In the application's **APIs** tab, toggle **Authorized** for the Podium API. Leave the permissions at their defaults, because Podium uses identity claims rather than Auth0-issued permissions.

## 3. Add the groups claim with an Action

1. Open **Actions → Library → Build Custom**.
2. Set **Name** to Add groups to access token.
3. Set **Trigger** to Login / Post Login.
4. Enter the Action code. It reads the user's groups from `app_metadata` and adds them to the ID token and the access token under the namespaced claim `https://podium.acme.com/groups`:

```javascript
exports.onExecutePostLogin = async (event, api) => {
  const namespace = "https://podium.acme.com/";
  const groups = (event.user.app_metadata && event.user.app_metadata.groups) || [];
  api.idToken.setCustomClaim(`${namespace}groups`, groups);
  api.accessToken.setCustomClaim(`${namespace}groups`, groups);
};
```

5. Save and deploy the Action.
6. Attach the Action: open **Actions → Triggers → post-login**, drag the new Action into the flow, and select **Apply**.

The registry reads group membership from the `groups` claim unless `identity_provider.groups_claim` (`PODIUM_OAUTH_GROUPS_CLAIM`) names another claim. Auth0 restricts non-namespaced custom claims, so this Action emits membership under a namespaced path, and step 5 points `groups_claim` at that path. The `IdpGroupMapping` adapter rewrites group values and does not select the claim.

A tenant that emits a top-level `groups` array on the access token needs no `groups_claim` setting. The following Action emits that form. Confirm that the tenant accepts a non-namespaced claim before using it.

```javascript
exports.onExecutePostLogin = async (event, api) => {
  const groups = (event.user.app_metadata && event.user.app_metadata.groups) || [];
  api.accessToken.setCustomClaim("groups", groups);
};
```

A deployment that does not name the claim can use `users:` visibility instead, or resolve membership through SCIM instead of the token claim.

## 4. Populate user groups

The Action reads groups from each user's `app_metadata`. For small teams, edit `app_metadata` per user under **User Management → Users → \[user\] → Metadata → app_metadata**:

```json
{
  "groups": ["engineering", "platform"]
}
```

For larger setups, populate `app_metadata` through SCIM (Auth0 Enterprise) or a directory sync script.

## 5. Configure the registry

On the registry host, add the `identity_provider:` block to `registry.yaml`:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<your-tenant>.auth0.com/   # must be https; Auth0 issuers end in a slash
    audience: https://podium.acme.com
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config, and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow. It verifies each presented token against the issuer's JWKS and validates the `aud` claim against `audience:`. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

The Action from step 3 emits group membership under the namespaced claim path `https://podium.acme.com/groups`. Set `identity_provider.groups_claim` in the `identity_provider:` block, or `PODIUM_OAUTH_GROUPS_CLAIM` in the environment, to `https://podium.acme.com/groups` so the registry reads that path. Configure `IdpGroupMapping` as well when the claim values have to be rewritten to registry-side group names. The [`oidc-jwt` settings](index#oidc-jwt-settings) table lists both keys.

Restart the registry.

## 6. Configure developer machines

On each developer machine, run:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUDIENCE=https://podium.acme.com
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<your-tenant>.auth0.com/oauth/device/code
export PODIUM_OAUTH_TOKEN_URL=https://<your-tenant>.auth0.com/oauth/token
podium login
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL. That produces `https://<your-tenant>.auth0.com/oauth/device/code/token`, and the token exchange fails.

Expected result: the device-flow verification URL is `https://<your-tenant>.auth0.com/activate`. After the flow completes, `podium login` prints the resolved identity.

## 7. Test group-based visibility

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

Group-scoped visibility resolves when `identity_provider.groups_claim` names the namespaced path the Action emits (step 5).

Expected result: a user with `engineering` in `app_metadata.groups` sees the layer, and a user without it does not.

## Next steps

- Declare visibility on the remaining layers with [Access control](../../content/access-control).
- Review the caller identities the registry records in the [audit log](../../operations/observability#audit-log).

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| The groups claim is missing from the token. | The Action is not attached to the post-login trigger, or the user has no `groups` in `app_metadata`. | Check the Action's logs under **Actions → Library → \[Action\] → Logs**. |
| The token is rejected. | The token's `aud` does not match the registry's `audience:`. | Make the API identifier and `audience:` match exactly. |
| Auth0 reports a custom claim namespace error. | Auth0 rejects non-namespaced custom claims, so a bare `groups` claim is rejected. | Emit the claim under a namespaced path such as `https://your-namespace/groups`, and set `identity_provider.groups_claim` (`PODIUM_OAUTH_GROUPS_CLAIM`) to that path. |
| The token carries groups, but `groups:` layers stay hidden. | The registry reads the top-level `groups` claim, and the Action emits a namespaced claim. | Set `identity_provider.groups_claim` to the namespaced path (step 5). |
