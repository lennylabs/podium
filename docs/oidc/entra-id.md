# Microsoft Entra ID (formerly Azure AD)

Podium + Entra ID. ~20 minutes of setup. Slightly more involved than Okta because Entra emits group object IDs (GUIDs) instead of names by default.

## Prerequisites

- Entra ID Global Administrator or Application Administrator role.
- Podium registry running and reachable from your developers' browsers.
- A naming convention for groups in Podium layer config (you'll either use Entra group GUIDs directly, or set up a name-mapping — see step 2).

## 1. Register the OIDC application

Azure portal: **Microsoft Entra ID → App registrations → New registration**.

- **Name**: Podium.
- **Supported account types**: usually **Accounts in this organizational directory only** (single tenant). Multi-tenant only if Podium is a SaaS offering.
- **Redirect URI**: not used for device-code, but Entra requires one — set to **Public client/native** with `http://localhost`.

After registration, note from the app overview:

- **Application (client) ID** → `PODIUM_OAUTH_CLIENT_ID`.
- **Directory (tenant) ID** → used in the issuer URL.

Under **Authentication**:

- Enable **Allow public client flows: Yes**.
- Add platform: **Mobile and desktop applications**, with redirect `http://localhost`.

Under **API permissions**: ensure **Microsoft Graph → User.Read** is granted (default). Add **GroupMember.Read.All** if you want group-membership claims.

## 2. Configure the groups claim

Entra emits group object IDs by default. Two options:

**Option A — group object IDs in tokens** (simpler, GUIDs in your layer config):

- **Token configuration → Add groups claim**.
- Select **Security groups** (or **All groups** if you also use distribution lists).
- Customize for ID Token + Access Token: **Group ID** (the default).

Your layer config will then look like:

```yaml
visibility:
  groups: ["7c52a1d4-..."]   # Entra group object ID
```

**Option B — group display names** (requires onPremisesSamAccountName or a custom claims mapping policy):

For groups synced from on-prem AD, you can emit `sAMAccountName` in the groups claim. For cloud-only groups, this requires a custom claims mapping policy via PowerShell — outside the scope of this guide. If you go this route, set `groups_claim: groups` and use names in your visibility config.

Most teams find Option A faster; the GUID-vs-name tradeoff is just a layer-config readability concern.

## 3. Expose Podium as an API

Under **Expose an API**:

- **Set Application ID URI** → `api://podium` (or your tenant URI).
- **Add a scope**:
  - Name: `Podium.Use`
  - Who can consent: Admins and users
  - Display info as appropriate.

This gives you the audience the registry will validate against.

## 4. Configure Podium

On the registry host, edit `~/.podium/registry.yaml`:

```yaml
identity:
  provider: oidc
  issuer: https://login.microsoftonline.com/<tenant-id>/v2.0
  audience: api://podium
  jwks_uri: https://login.microsoftonline.com/<tenant-id>/discovery/v2.0/keys
  groups_claim: groups
  email_claim: preferred_username       # Entra uses preferred_username for email
  sub_claim: sub
```

Restart the registry.

On developer machines:

```bash
podium init --remote https://podium.your-org.example
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUDIENCE=api://podium
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/devicecode
podium login
```

The browser device-flow page is hosted at `https://microsoft.com/devicelogin`. After completion, `podium login` prints the `sub`, `email` (resolved from `preferred_username`), and groups.

## 5. Test group-based visibility

Using Option A (group object IDs):

```bash
podium layer register \
  --id engineering-only \
  --repo git@github.com:your-org/podium-engineering.git \
  --ref main \
  --visibility 'groups: ["7c52a1d4-..."]'
```

Confirm a member of that group sees the layer; a non-member doesn't.

## SCIM (optional)

Entra supports SCIM via the **Provisioning** tab on the enterprise application:

1. **Enterprise applications → \[Podium\] → Provisioning → Get started**.
2. **Provisioning Mode: Automatic**.
3. **Tenant URL**: `https://podium.your-org.example/v1/scim`.
4. **Secret Token**: from `podium admin scim-token issue`.
5. **Test connection**, then save and enable.
6. Configure attribute mappings (defaults usually work for `sub`, `email`, `groups`).

## Troubleshooting

- **Tokens don't include the groups claim.** The user is in too many groups (Entra emits a `_claim_names` placeholder above ~150 groups). Either narrow the groups returned or use the Graph API fallback (out of scope here; Entra documents this as the "groups overage" pattern).
- **`auth.signature_invalid` after a tenant key rotation.** The registry's JWKS cache is 5 minutes; wait or restart the registry to force a refresh.
- **`auth.audience_mismatch`.** Confirm the API URI you set in step 3 matches `audience:` in `registry.yaml` exactly, including the `api://` prefix.
- **Device-flow times out.** Some corporate networks block Microsoft's device-flow domain. Test with `--no-browser` and a developer's personal device on a different network.
- **Group object IDs are unreadable.** Maintain a separate doc (or a `DOMAIN.md` curation note) mapping the GUIDs you use in layer config to human-readable names. Entra's admin console shows both.
