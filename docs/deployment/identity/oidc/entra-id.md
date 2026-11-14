---
title: Microsoft Entra ID
nav_order: 2
description: Configure a Podium registry to authenticate callers against Microsoft Entra ID (formerly Azure AD) through the OIDC device-code flow.
---

# Microsoft Entra ID

This cookbook configures a Podium registry to authenticate against Microsoft Entra ID, formerly Azure AD. By default Entra emits group object IDs (GUIDs) instead of names, so the `IdpGroupMapping` adapter resolves the GUIDs to group names. Setup takes about 20 minutes. The [OIDC cookbooks overview](index) describes the identity model the steps rely on.

## Prerequisites

- The Entra ID Global Administrator or Application Administrator role.
- A Podium registry running and reachable from developers' browsers.
- A naming convention for groups in Podium layer config. Use Entra group GUIDs directly, or map them to names as step 2 describes.

## 1. Register the OIDC application

1. In the Azure portal, open **Microsoft Entra ID → App registrations → New registration**.
2. Set **Name** to Podium.
3. Set **Supported account types**, usually to **Accounts in this organizational directory only** (single tenant). Choose multi-tenant only when Podium is a SaaS offering.
4. Set **Redirect URI** to **Public client/native** with `http://localhost`. The device-code flow does not use it, but Entra requires one.
5. After registration, note these values from the app overview:
   - **Application (client) ID**, which is `PODIUM_OAUTH_CLIENT_ID`.
   - **Directory (tenant) ID**, which forms part of the issuer URL.
6. Under **Authentication**, set **Allow public client flows** to **Yes**.
7. Under **Authentication**, add the **Mobile and desktop applications** platform with redirect `http://localhost`.
8. Under **API permissions**, confirm that **Microsoft Graph -> User.Read** is granted (the default). Add **GroupMember.Read.All** for group-membership claims.

## 2. Configure the groups claim

Entra emits group object IDs by default. Choose one of the options below. Option A requires less setup, and `IdpGroupMapping` resolves the readability difference between GUIDs and names.

### Option A: group object IDs mapped to names

1. Open **Token configuration → Add groups claim**.
2. Select **Security groups**, or **All groups** when distribution lists are also in use.
3. Under the ID Token and Access Token customization, keep **Group ID** (the default).
4. Set `PODIUM_IDP_GROUP_MAPPING` on the registry to map each GUID to the group name used in layer visibility. The value is a comma-separated list of `<token-value>=<group-name>` pairs:

```bash
PODIUM_IDP_GROUP_MAPPING=7c52a1d4-1111-2222-3333-444455556666=engineering,9ab3f0c2-7777-8888-9999-aaaabbbbcccc=platform
```

The mapping has no `registry.yaml` key. A non-empty value that carries a malformed entry, or that resolves to no `<token-value>=<group-name>` entry at all, fails startup with `config.invalid_idp_group_mapping`. The registry refuses the whole setting instead of starting with a partial table, so one malformed entry discards the well-formed entries beside it.

The layer config then references the readable name:

```yaml
# Extract from a `layers:` entry.
visibility:
  groups: [engineering]
```

### Option B: group display names

This option requires `onPremisesSamAccountName` or a custom claims mapping policy. For groups synced from on-premises AD, Entra can emit `sAMAccountName` in the groups claim. Cloud-only groups require a custom claims mapping policy set through PowerShell, which is outside the scope of this cookbook. When the token already carries names, `IdpGroupMapping` passes them through unchanged.

### Large group memberships

When a user belongs to more than about 200 groups, Entra omits the groups claim and emits an overage reference to Microsoft Graph instead. Podium does not call Graph, so such a user resolves to no groups from the token. Select **Groups assigned to the application** instead of **All groups** to keep the emitted set small, or resolve membership through [SCIM](#scim-provisioning-optional). [Troubleshooting](#troubleshooting) describes the symptom.

## 3. Expose Podium as an API

1. Under **Expose an API**, set **Application ID URI** to `api://podium`, or to the tenant URI.
2. Select **Add a scope** and set:
   - **Name**: `Podium.Use`
   - **Who can consent**: Admins and users
   - The display text as appropriate.
3. Under **Manage → Manifest**, set the access-token version to 2. In the AAD Graph manifest, set `accessTokenAcceptedVersion` to `2`. In the Microsoft Graph manifest, set `api.requestedAccessTokenVersion` to `2`.

The Application ID URI is the audience the registry validates against.

> [!IMPORTANT]
> With the default access-token version, Entra issues v1.0 access tokens whose `iss` is `https://sts.windows.net/<tenant-id>/`. That value does not match the issuer configured in step 4, and the registry rejects every such token with `auth.untrusted_token`.

## 4. Configure the registry

On the registry host, add the `identity_provider:` block to `registry.yaml`:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://login.microsoftonline.com/<tenant-id>/v2.0   # must be https
    audience: api://podium
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config, and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow. It verifies each presented token against the issuer's JWKS and requires the token's `aud` claim to carry one of the values configured under `audience:`.

- A CLI, an SDK, or another API client obtains the token through the device-code flow that the next step configures.
- On a registry that enables the browser flow, a browser obtains the token through the registry's own authorization-code exchange, and the registry returns it in the `__Host-podium_session` cookie.
- Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

Entra carries the user principal name in the `preferred_username` claim, which the registry does not read. The registry takes the caller's email from the `email` claim alone. When layer visibility lists callers by email address, add `email` under **Token configuration → Add optional claim → Access token**.

The `IdpGroupMapping` adapter resolves the group claim to group names (Option A in step 2). Restart the registry.

## 5. Configure developer machines

On each developer machine, run:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/devicecode
export PODIUM_OAUTH_TOKEN_URL=https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/token
podium login --scopes "openid profile email api://podium/Podium.Use"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL. That produces `https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/devicecode/token`, and the token exchange fails.

Request the API scope from step 3 in `--scopes`. The v2.0 endpoint sets the access token's `aud` from the resource that the requested scope names:

- Entra ignores the `audience` form parameter that `PODIUM_OAUTH_AUDIENCE` sets, so that variable has no effect against Entra.
- With the default scope set (`openid profile email groups`), the issued access token is not audienced to `api://podium`, and the registry rejects it on the `aud` check.
- The registry's `audience:` lists the resource identifiers Entra mints for the scopes the deployment's clients request. A client that must present a token with a different audience needs that value listed as well.

Expected result: the browser device-flow page opens at `https://microsoft.com/devicelogin`. After the flow completes, `podium login` prints the `sub`, `email`, and groups.

## 6. Test group-based visibility

Configure an admin layer scoped to the group in `registry.yaml`. With Option A, the `IdpGroupMapping` entry maps the GUID to the name `engineering`, and the layer references that name. Group-scoped visibility is set in the registry layer config, or with `podium layer register --group <name>` (also `--public`, `--organization`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

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

Expected result: a member of that group sees the layer, and a non-member does not.

## SCIM provisioning (optional)

Entra pushes user and group records to the registry's SCIM endpoint through the **Provisioning** tab on the enterprise application.

1. Open **Enterprise applications → \[Podium\] → Provisioning → Get started**.
2. Set **Provisioning Mode** to **Automatic**.
3. Set **Tenant URL** to the registry's SCIM endpoint, `https://podium.acme.com/scim/v2`.
4. Set **Secret Token** to one of the bearer tokens listed in the registry's `PODIUM_SCIM_TOKENS` environment variable.
5. Select **Test connection**, then save and enable provisioning.
6. Configure attribute mappings so each provisioned user's SCIM `userName` matches the `sub` or `email` claim its token carries.
7. Configure attribute mappings so each group's `displayName` matches the name used in the layer's `groups:` filter.

On the registry side:

- The registry mounts `/scim/v2/` only when `PODIUM_SCIM_TOKENS` is set to a comma-separated list of accepted tokens. Otherwise it returns 404 for every SCIM request.
- Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart. A set path that the registry cannot read, parse, or write refuses startup with `config.scim_store_unavailable`.
- The registry expands a `groups:` filter to the group's member `userName` values and compares them against the caller's `sub` and `email`.

> [!WARNING]
> The registry's SCIM receiver serves `GET`, `POST`, `PUT`, and `DELETE` on `/Users` and `/Groups`, and answers every other method with HTTP 405. Entra's provisioning service sends updates, including group-membership changes, as `PATCH`. The pushed directory therefore records what the create requests established, and the registry rejects later updates. Confirm that a membership change reaches the registry before relying on SCIM for group visibility.

## Next steps

- Declare visibility on the remaining layers with [Access control](../access-control).
- Review the caller identities the registry records in the [audit log](../observability#audit-log).

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Tokens do not include the groups claim. | The user belongs to more groups than Entra emits in a token, so the token carries a `_claim_names` and `_claim_sources` overage reference instead of the claim. The registry reads groups from the token's `groups` claim and makes no Microsoft Graph call, so a caller in overage resolves to no groups. | Select **Groups assigned to the application** in the groups-claim configuration, or resolve membership through SCIM. |
| Every token is rejected with `auth.untrusted_token`. | The access-token version is still 1, so the token's `iss` is `https://sts.windows.net/<tenant-id>/`. | Set the access-token version to 2 in the manifest (step 3). |
| Tokens are rejected. | The API URI from step 3 does not match `audience:` in the `identity_provider:` block. | Make them match exactly, including the `api://` prefix. |
| The device flow times out. | Some corporate networks block Microsoft's device-flow domain. | Test with `--no-browser` and a developer's personal device on a different network. |
| Group object IDs are unreadable in layer config. | The token carries GUIDs. | Map each GUID to a readable group name in the `IdpGroupMapping` configuration, then reference the readable name in `visibility:`. Entra's admin console shows both the GUID and the name. |
