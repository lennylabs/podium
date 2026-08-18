---
title: Entra ID
nav_order: 2
description: Configure Podium to authenticate against Microsoft Entra ID (formerly Azure AD) via OIDC device-code flow.
---

# Microsoft Entra ID (formerly Azure AD)

This guide configures a Podium registry to authenticate against Microsoft Entra ID. Setup takes about 20 minutes. Entra emits group object IDs (GUIDs) instead of names by default, so the `IdpGroupMapping` adapter resolves the GUIDs to group names.

## Prerequisites

- Entra ID Global Administrator or Application Administrator role.
- Podium registry running and reachable from your developers' browsers.
- A naming convention for groups in Podium layer config. Use Entra group GUIDs directly, or set up a name mapping; see step 2.

## 1. Register the OIDC application

Azure portal: **Microsoft Entra ID → App registrations → New registration**.

- **Name**: Podium.
- **Supported account types**: usually **Accounts in this organizational directory only** (single tenant). Multi-tenant only if Podium is a SaaS offering.
- **Redirect URI**: not used for device-code, but Entra requires one. Set to **Public client/native** with `http://localhost`.

After registration, note from the app overview:

- **Application (client) ID** → `PODIUM_OAUTH_CLIENT_ID`.
- **Directory (tenant) ID** → used in the issuer URL.

Under **Authentication**:

- Enable **Allow public client flows: Yes**.
- Add platform: **Mobile and desktop applications**, with redirect `http://localhost`.

Under **API permissions**: make sure **Microsoft Graph -> User.Read** is granted (default). Add **GroupMember.Read.All** for group-membership claims.

## 2. Configure the groups claim

Entra emits group object IDs by default. Two options:

**Option A: group object IDs in tokens** (simpler, GUIDs mapped to names registry-side):

- **Token configuration → Add groups claim**.
- Select **Security groups** (or **All groups** if you also use distribution lists).
- Customize for ID Token + Access Token: **Group ID** (the default).

The token then carries group object IDs. Set `PODIUM_IDP_GROUP_MAPPING` on the registry to map each GUID to the group name used in layer visibility. The value is a comma-separated list of `<token-value>=<group-name>` pairs:

```bash
PODIUM_IDP_GROUP_MAPPING=7c52a1d4-1111-2222-3333-444455556666=engineering,9ab3f0c2-7777-8888-9999-aaaabbbbcccc=platform
```

A malformed entry is logged and the whole mapping is dropped, so group values then pass through unchanged. There is no `registry.yaml` key for the mapping.

The layer config then references the readable name:

```yaml
# Extract from a `layers:` entry.
visibility:
  groups: [engineering]
```

**Option B: group display names** (requires onPremisesSamAccountName or a custom claims mapping policy):

For groups synced from on-prem AD, Entra can emit `sAMAccountName` in the groups claim. Cloud-only groups require a custom claims mapping policy via PowerShell, which is outside the scope of this guide. When the token already carries names, the `IdpGroupMapping` entries pass them through unchanged.

Most teams find Option A faster; the GUID-versus-name tradeoff is a layer-config readability concern that `IdpGroupMapping` resolves.

**Large group memberships (overage).** When a user belongs to more than about 200 groups, Entra omits the groups claim from the token and instead emits a `_claim_names` and `_claim_sources` overage reference that points at Microsoft Graph. Podium reads groups from the token claim and does not call Graph, so a user in overage resolves to no groups from the token. Keep the emitted set small by selecting **Groups assigned to the application** in the groups-claim configuration rather than **All groups**, or resolve membership through SCIM (below) instead of the token claim.

## 3. Expose Podium as an API

Under **Expose an API**:

- **Set Application ID URI** → `api://podium` (or your tenant URI).
- **Add a scope**:
  - Name: `Podium.Use`
  - Who can consent: Admins and users
  - Display info as appropriate.

This gives you the audience the registry will validate against.

Set the access-token version to 2 on the app registration. Under **Manage → Manifest**, set `accessTokenAcceptedVersion` to `2` in the AAD Graph manifest, or `api.requestedAccessTokenVersion` to `2` in the Microsoft Graph manifest. With the default value Entra issues v1.0 access tokens whose `iss` is `https://sts.windows.net/<tenant-id>/`, which does not match the issuer configured in step 4, and the registry rejects every such token with `auth.untrusted_token`.

## 4. Configure Podium

On the registry host, edit `registry.yaml`:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://login.microsoftonline.com/<tenant-id>/v2.0   # must be https
    audience: api://podium
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow: it verifies each presented token against the issuer's JWKS and validates the `aud` claim against `audience:`. Developers obtain that token by completing the device-code flow from the CLI, which the next step configures. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

Entra carries the user principal name in the `preferred_username` claim, which the registry does not read. The registry takes the caller's email from the `email` claim alone, so add `email` under **Token configuration → Add optional claim → Access token** when layer visibility lists callers by email address. The `IdpGroupMapping` adapter resolves the group claim to group names (Option A above). Restart the registry.

On developer machines:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/devicecode
export PODIUM_OAUTH_TOKEN_URL=https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/token
podium login --scopes "openid profile email api://podium/Podium.Use"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` derives the token endpoint by appending `/token` to the device-authorization URL, which produces `https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/devicecode/token` and the token exchange fails.

Request the API scope from step 3 in `--scopes`. The v2.0 endpoint sets the access token's `aud` from the resource named by the requested scope and ignores the `audience` form parameter that `PODIUM_OAUTH_AUDIENCE` sets, so that variable has no effect against Entra. With the default scope set (`openid profile email groups`) the issued access token is not audienced to `api://podium`, and the registry rejects it on the `aud` check.

The browser device-flow page is hosted at `https://microsoft.com/devicelogin`. After completion, `podium login` prints the `sub`, `email`, and groups.

## 5. Test group-based visibility

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

Confirm a member of that group sees the layer; a non-member does not.

## SCIM (optional)

Entra pushes user and group records to the registry's SCIM endpoint via the **Provisioning** tab on the enterprise application:

1. **Enterprise applications → \[Podium\] → Provisioning → Get started**.
2. **Provisioning Mode: Automatic**.
3. **Tenant URL**: the registry's SCIM endpoint, `https://podium.acme.com/scim/v2`.
4. **Secret Token**: one of the bearer tokens listed in the registry's `PODIUM_SCIM_TOKENS` environment variable. The registry mounts `/scim/v2/` only when that variable is set to a comma-separated list of accepted tokens, and returns 404 for every SCIM request otherwise. Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart.
5. **Test connection**, then save and enable.
6. Configure attribute mappings so each provisioned user's SCIM `userName` matches the `sub` or `email` claim its token carries, and each group's `displayName` matches the name used in the layer's `groups:` filter. The registry expands a `groups:` filter to the group's member `userName` values and compares them against the caller's `sub` and `email`.

The registry's SCIM receiver serves `GET`, `POST`, `PUT`, and `DELETE` on `/Users` and `/Groups`, and answers every other method with HTTP 405. Entra's provisioning service sends updates, including group-membership changes, as `PATCH`, so the pushed directory records what the create requests established and later updates are rejected. Confirm that a membership change reaches the registry before relying on SCIM for group visibility.

## Troubleshooting

- **Tokens don't include the groups claim.** The user belongs to more groups than Entra emits in a token, so the token carries a `_claim_names` overage reference instead of the claim. The registry reads groups from the token's `groups` claim and makes no Microsoft Graph call, so a caller in overage resolves to no groups. Narrow the emitted set by selecting **Groups assigned to the application** in the groups-claim configuration, or resolve membership through SCIM.
- **Token rejected.** Confirm the API URI from step 3 matches `audience:` in the `identity_provider:` block exactly, including the `api://` prefix.
- **Device-flow times out.** Some corporate networks block Microsoft's device-flow domain. Test with `--no-browser` and a developer's personal device on a different network.
- **Group object IDs are unreadable in layer config.** Map each GUID to a readable group name in the `IdpGroupMapping` configuration, then reference the readable name in `visibility:`. Entra's admin console shows both the GUID and the name.
