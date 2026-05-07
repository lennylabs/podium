# Okta

Podium + Okta. ~15 minutes of setup if you have admin access.

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

On the registry host, edit `~/.podium/registry.yaml` (or `/etc/podium/registry.yaml`):

```yaml
identity:
  provider: oidc
  issuer: https://<your-okta-domain>/oauth2/default
  audience: podium
  jwks_uri: https://<your-okta-domain>/oauth2/default/v1/keys
  groups_claim: groups
  email_claim: email
  sub_claim: sub
```

Restart the registry.

On developer machines, set up the client:

```bash
podium init --global --registry https://podium.your-org.example
export PODIUM_OAUTH_CLIENT_ID=<client-id-from-step-1>
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<your-okta-domain>/oauth2/default/v1/device/authorize
podium login
```

You should see the device verification URL and code, and after completing the flow in-browser, `podium login` prints your `sub`, `email`, and `groups`.

## 4. Test group-based visibility

Create a layer scoped to a specific Okta group:

```bash
podium layer register \
  --id engineering-only \
  --repo git@github.com:your-org/podium-engineering.git \
  --ref main \
  --visibility 'groups: ["engineering"]'
```

Confirm:

- A user in the `engineering` group sees the layer in `podium layer list` and its artifacts in `podium search`.
- A user not in `engineering` sees neither.

## SCIM (optional but recommended)

For group-membership changes to apply without waiting for the user's next login, configure SCIM:

1. In Okta: **Applications → \[your Podium app\] → Provisioning → To App → Enable SCIM**.
2. SCIM Connector base URL: `https://podium.your-org.example/v1/scim`.
3. Authentication: **HTTP Header**, with the SCIM token from `podium admin scim-token issue`.
4. Test the connection. Enable: **Push Groups**, **Update User Attributes**.

Group changes now propagate within seconds.

## Troubleshooting

- **`auth.audience_mismatch` on every call.** The token's `aud` doesn't match what the registry expects. Confirm step 2 added `podium` to the audience list, and that `audience: podium` is in `registry.yaml`.
- **`groups` claim is missing.** The claim wasn't added to the right token type, or the user wasn't assigned to the app. Check Okta's **Token Preview** under the authorization server.
- **Device-flow returns "this app does not support device-code flow."** Enable **Device Authorization** under the app's grant types (step 1).
- **`podium login` hangs.** The browser-side verification URL might be blocked by a corporate proxy. Try `--no-browser` and copy the URL into a different browser.
