---
layout: default
title: Entra ID
parent: OIDC cookbooks
grand_parent: Deployment
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

The token then carries group object IDs. Configure the `IdpGroupMapping` adapter registry-side to map each GUID to the group name used in layer visibility:

```yaml
# IdpGroupMapping (registry-side): raw Entra GUID -> group name
7c52a1d4-...: engineering
```

The layer config then references the readable name:

```yaml
visibility:
  groups: [engineering]
```

**Option B: group display names** (requires onPremisesSamAccountName or a custom claims mapping policy):

For groups synced from on-prem AD, Entra can emit `sAMAccountName` in the groups claim. Cloud-only groups require a custom claims mapping policy via PowerShell, which is outside the scope of this guide. When the token already carries names, the `IdpGroupMapping` entries pass them through unchanged.

Most teams find Option A faster; the GUID-versus-name tradeoff is a layer-config readability concern that `IdpGroupMapping` resolves.

## 3. Expose Podium as an API

Under **Expose an API**:

- **Set Application ID URI** → `api://podium` (or your tenant URI).
- **Add a scope**:
  - Name: `Podium.Use`
  - Who can consent: Admins and users
  - Display info as appropriate.

This gives you the audience the registry will validate against.

## 4. Configure Podium

On the registry host, edit `registry.yaml`:

```yaml
identity_provider:
  type: oauth-device-code
  audience: api://podium
  authorization_endpoint: https://login.microsoftonline.com/<tenant-id>/v2.0
```

Entra carries the user's email in the `preferred_username` claim. The `IdpGroupMapping` adapter resolves the group claim to group names (Option A above). Restart the registry.

On developer machines:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUDIENCE=api://podium
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/devicecode
podium login
```

The browser device-flow page is hosted at `https://microsoft.com/devicelogin`. After completion, `podium login` prints the `sub`, `email`, and groups.

## 5. Test group-based visibility

Configure an admin layer scoped to the group in the tenant's layer config. With Option A, the `IdpGroupMapping` entry maps the GUID to the name `engineering`, and the layer references that name. Group-scoped visibility is set in the registry layer config, or with `podium layer register --group <name>` (also `--public`, `--organization`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

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

Confirm a member of that group sees the layer; a non-member does not.

## SCIM (optional)

Entra pushes user and group records to the registry's SCIM endpoint via the **Provisioning** tab on the enterprise application:

1. **Enterprise applications → \[Podium\] → Provisioning → Get started**.
2. **Provisioning Mode: Automatic**.
3. **Tenant URL**: the registry's SCIM endpoint, `https://podium.acme.com/scim/v2`.
4. **Secret Token**: a bearer credential the registry accepts at its SCIM endpoint.
5. **Test connection**, then save and enable.
6. Configure attribute mappings (defaults usually work for `sub`, `email`, and `groups`).

## Troubleshooting

- **Tokens don't include the groups claim.** The user is in too many groups (Entra emits a `_claim_names` placeholder above ~150 groups). Either narrow the groups returned or use the Graph API fallback (out of scope here; Entra documents this as the "groups overage" pattern).
- **Token rejected.** Confirm the API URI from step 3 matches `audience:` in the `identity_provider:` block exactly, including the `api://` prefix.
- **Device-flow times out.** Some corporate networks block Microsoft's device-flow domain. Test with `--no-browser` and a developer's personal device on a different network.
- **Group object IDs are unreadable in layer config.** Map each GUID to a readable group name in the `IdpGroupMapping` configuration, then reference the readable name in `visibility:`. Entra's admin console shows both the GUID and the name.
