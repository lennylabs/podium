---
title: Okta
nav_order: 1
description: Configure Podium to authenticate against Okta via OIDC device-code flow.
---

# Okta

This guide configures a Podium registry to authenticate against Okta. Setup takes about 15 minutes with admin access.

## Prerequisites

- Okta admin role.
- Podium registry running and reachable from your developers' browsers.
- Decide your audience identifier (suggestion: `podium`).

## 1. Create the OIDC application in Okta

In the Okta admin console: **Applications → Applications → Create App Integration**.

- Sign-in method: **OIDC / OpenID Connect**.
- Application type: **Native Application** (the device-code flow uses native-app conventions).

Configuration:

- **App name**: Podium (or your tenant name).
- **Grant type**: enable **Device Authorization** (you may need to enable device-flow under **Security → API → Authorization Servers → default → Settings**).
- **Sign-in redirect URIs**: not needed for device-code flow, but Okta requires at least one. Use `http://localhost/callback` as a placeholder.
- **Assignments**: assign the groups of users who should be able to use Podium (typically all employees, or a specific team to start).

Save. Copy the **Client ID**; that's `PODIUM_OAUTH_CLIENT_ID` for clients.

## 2. Configure the audience and groups claim

Still in the admin console: **Security → API → Authorization Servers → default**.

- **Audiences**: add `podium` (or whatever you chose).
- **Claims** → **Add Claim**:
  - Name: `groups`
  - Include in token type: **ID Token**, **Access Token** (both)
  - Value type: **Groups**
  - Filter: **Matches regex** `.*` (or narrow to specific groups)
  - Include in: **Any scope**

This makes the user's group memberships available in the JWT under the `groups` claim as an array of group names.

## 3. Configure Podium

On the registry host, edit `registry.yaml`. The registry reads `~/.podium/registry.yaml` unless `PODIUM_CONFIG_FILE` names another path, and `podium serve --config <path>` sets that variable. A file at `/etc/podium/registry.yaml` is read only when the server is started with `--config /etc/podium/registry.yaml` or with `PODIUM_CONFIG_FILE` set to it:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<your-okta-domain>/oauth2/default   # must be https
    audience: podium
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow: it verifies each presented token against the issuer's JWKS and validates the `aud` claim against `audience:`. Developers obtain that token by completing the device-code flow from the CLI, which the next step configures. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

The registry reads group membership from the token's `groups` claim, mapped to group names by the `IdpGroupMapping` adapter, or from the SCIM directory when SCIM is configured (see below). Restart the registry.

On developer machines, set up the client:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id-from-step-1>
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<your-okta-domain>/oauth2/default/v1/device/authorize
export PODIUM_OAUTH_TOKEN_URL=https://<your-okta-domain>/oauth2/default/v1/token
podium login --scopes "openid profile email"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL, which produces `https://<your-okta-domain>/oauth2/default/v1/device/authorize/token` and the token exchange fails. The default scope set is `openid profile email groups` and the `default` authorization server defines no `groups` scope, while the claim added in step 2 is emitted under any scope, so `openid profile email` is sufficient.

The device verification URL and code appear, and after the in-browser flow completes, `podium login` prints the resolved `sub`, `email`, and `groups`.

## 4. Test group-based visibility

Configure an admin layer scoped to a specific Okta group in `registry.yaml`. Group-scoped visibility is set in the registry layer config, or with `podium layer register --group <name>` (also `--public`, `--organization`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

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

The `groups:` entry matches the group names resolved from the token claim or the SCIM directory. Confirm:

- A user in the `engineering` group sees the layer in `podium layer list` and its artifacts in `podium search`.
- A user not in `engineering` sees neither.

## SCIM (optional but recommended)

SCIM 2.0 push applies group-membership changes without waiting for the user's next login. Without SCIM, group membership reflects the token's `groups` claim at login time. Configure SCIM:

1. In Okta: **Applications → \[your Podium app\] → Provisioning → To App → Enable SCIM**.
2. SCIM Connector base URL: the registry's SCIM endpoint, `https://podium.acme.com/scim/v2`.
3. Authentication: **HTTP Header**, with one of the bearer tokens listed in the registry's `PODIUM_SCIM_TOKENS` environment variable. The registry mounts `/scim/v2/` only when that variable is set to a comma-separated list of accepted tokens, and returns 404 for every SCIM request otherwise. Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart.
4. Test the connection. Enable **Push Groups** and **Update User Attributes**. Map each provisioned user's SCIM `userName` to the `sub` or `email` claim its token carries, and each pushed group's `displayName` to the name the layer's `groups:` filter declares. The registry expands a `groups:` filter to the group's member `userName` values and compares them against the caller's `sub` and `email`, so a `userName` that matches neither claim resolves to no group visibility and reports no error.

The registry's SCIM receiver serves `GET`, `POST`, `PUT`, and `DELETE` on `/Users` and `/Groups`, and answers every other method with HTTP 405. Okta sends group-membership changes and user deactivations as `PATCH`, so those requests are rejected and the pushed directory keeps the state the create requests established. Confirm that a membership change reaches the registry before relying on SCIM for group visibility. The token's `groups` claim resolves membership as of each login.

## Troubleshooting

- **Tokens rejected on every call.** The token's `aud` does not match the registry's `audience:`. Confirm step 2 added `podium` to the audience list, and that `audience: podium` is in the `identity_provider:` block.
- **`groups` claim is missing.** The claim was not added to the right token type, or the user was not assigned to the app. Check Okta's **Token Preview** under the authorization server.
- **Device-flow returns "this app does not support device-code flow."** Enable **Device Authorization** under the app's grant types (step 1).
- **`podium login` hangs.** A corporate proxy may block the browser-side verification URL. Run with `--no-browser` and copy the URL into a different browser.
