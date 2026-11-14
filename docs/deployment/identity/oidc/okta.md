---
title: Okta
nav_order: 1
description: Configure a Podium registry to authenticate callers against Okta through the OIDC device-code flow.
---

# Okta

This cookbook configures a Podium registry to verify Okta-issued tokens under `oidc-jwt`, and configures developer machines to acquire them through the device-code flow. Okta emits group names in the `groups` claim natively and supports SCIM provisioning. Setup takes about 15 minutes with admin access. [OIDC](index) is the reference for the `oidc-jwt` settings these steps use.

## Prerequisites

- An Okta admin role.
- A Podium registry running and reachable from developers' browsers.
- An audience identifier for the registry. This cookbook uses `podium`.

## 1. Create the OIDC application

1. In the Okta admin console, open **Applications → Applications → Create App Integration**.
2. Set **Sign-in method** to **OIDC / OpenID Connect**.
3. Set **Application type** to **Native Application**. The device-code flow uses native-app conventions.
4. Set **App name** to Podium, or to the tenant name.
5. Under **Grant type**, enable **Device Authorization**. Device flow can also need enabling under **Security → API → Authorization Servers → default → Settings**.
6. Under **Sign-in redirect URIs**, enter `http://localhost/callback`. The device-code flow does not use it, but Okta requires at least one.
7. Under **Assignments**, assign the groups of users who use Podium, typically all employees or one team to start.
8. Save, and copy the **Client ID**. It is the `PODIUM_OAUTH_CLIENT_ID` value for clients.

## 2. Configure the audience and groups claim

1. In the admin console, open **Security → API → Authorization Servers → default**.
2. Under **Audiences**, add `podium`, or the audience identifier chosen in the prerequisites.
3. Under **Claims**, select **Add Claim** and set:
   - **Name**: `groups`
   - **Include in token type**: **ID Token** and **Access Token** (both)
   - **Value type**: **Groups**
   - **Filter**: **Matches regex** `.*`, or a narrower filter for specific groups
   - **Include in**: **Any scope**

Tokens now carry the user's group memberships under the `groups` claim as an array of group names.

## 3. Configure the registry

On the registry host, add the `identity_provider:` block to `registry.yaml`, nested under `registry:`. [File layout](../../../reference/configuration#file-layout) describes the nesting rule and which file the registry reads.

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<your-okta-domain>/oauth2/default   # must be https
    audience: podium
```

The registry verifies each presented token against the issuer's JWKS and requires its `aud` claim to carry `podium`. It reads group membership from the token's `groups` claim, and also from the SCIM directory when [SCIM provisioning](#scim-provisioning-optional) is configured.

Restart the registry.

## 4. Configure developer machines

On each developer machine, run:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id-from-step-1>
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<your-okta-domain>/oauth2/default/v1/device/authorize
export PODIUM_OAUTH_TOKEN_URL=https://<your-okta-domain>/oauth2/default/v1/token
podium login --scopes "openid profile email"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL. That produces `https://<your-okta-domain>/oauth2/default/v1/device/authorize/token`, and the token exchange fails.

Pass `--scopes "openid profile email"`. The default scope set is `openid profile email groups`, and the `default` authorization server defines no `groups` scope. The claim added in step 2 is emitted under any scope, so `openid profile email` is sufficient.

Expected result: `podium login` shows the device verification URL and code. After the in-browser flow completes, it prints the resolved `sub`, `email`, and `groups`.

## 5. Test group-based visibility

Configure an admin layer scoped to an Okta group in `registry.yaml`. [Declaring visibility](../../content/access-control#declaring-visibility) describes the other ways to set a layer's visibility.

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

The `groups:` entry matches the group names resolved from the token claim or the SCIM directory.

Expected result:

- A user in the `engineering` group sees the layer in `podium layer list` and its artifacts in `podium search`.
- A user outside `engineering` sees neither.

## SCIM provisioning (optional)

SCIM 2.0 push applies group-membership changes without waiting for the user's next login. Without SCIM, group membership reflects the token's `groups` claim at login time. SCIM is recommended for Okta, subject to the `PATCH` limitation below.

1. In Okta, open **Applications → \[the Podium app\] → Provisioning → To App → Enable SCIM**.
2. Set **SCIM Connector base URL** to the registry's SCIM endpoint, `https://podium.acme.com/scim/v2`.
3. Set **Authentication** to **HTTP Header**, with one of the bearer tokens listed in the registry's `PODIUM_SCIM_TOKENS` environment variable.
4. Test the connection.
5. Enable **Push Groups** and **Update User Attributes**.
6. Map each provisioned user's SCIM `userName` to the `sub` or `email` claim its token carries.
7. Map each pushed group's `displayName` to the name that the layer's `groups:` filter declares.

On the registry, enable the receiver with `PODIUM_SCIM_TOKENS` and `PODIUM_SCIM_STORE_PATH`, as [Enable the receiver](../groups#enable-the-receiver) describes. A `userName` that matches neither the caller's `sub` nor `email` resolves to no group visibility and reports no error, as [Matching users and groups](../groups#matching-users-and-groups) describes.

> [!WARNING]
> The registry's SCIM receiver serves `GET`, `POST`, `PUT`, and `DELETE` on `/Users` and `/Groups`, and answers every other method with HTTP 405. Okta sends group-membership changes and user deactivations as `PATCH`, so the registry rejects those requests, and the pushed directory keeps the state the create requests established. Confirm that a membership change reaches the registry before relying on SCIM for group visibility. The token's `groups` claim resolves membership as of each login.

## Next steps

- Declare visibility on the remaining layers with [Access control](../../content/access-control).
- Review the caller identities the registry records in the [audit log](../../operations/observability#audit-log).

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Tokens are rejected on every call. | The token's `aud` does not match the registry's `audience:`. | Confirm that step 2 added `podium` to the audience list, and that `audience: podium` is in the `identity_provider:` block. |
| The `groups` claim is missing. | The claim was not added to the right token type, or the user is not assigned to the app. | Check Okta's **Token Preview** under the authorization server. |
| The device flow returns "this app does not support device-code flow." | Device Authorization is not enabled for the app. | Enable **Device Authorization** under the app's grant types (step 1). |
| `podium login` hangs. | A corporate proxy can block the browser-side verification URL. | Run `podium login` with `--no-browser` and copy the URL into a different browser. |
