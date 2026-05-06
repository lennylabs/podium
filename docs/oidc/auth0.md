# Auth0

Podium + Auth0. ~15 minutes of setup. Group claims are not native to Auth0; they're added via an Action (the modern way) or a Rule (legacy).

## Prerequisites

- Auth0 tenant with admin access.
- Podium registry running and reachable from developers' browsers.
- Decide your audience identifier (suggestion: `https://podium.your-org.example` — Auth0 conventionally uses URL-shaped audiences).

## 1. Create the API in Auth0

Auth0 dashboard: **Applications → APIs → Create API**.

- **Name**: Podium.
- **Identifier**: `https://podium.your-org.example` (this is the `audience`).
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
- Permissions: leave default (Podium uses identity claims, not Auth0-issued permissions).

## 3. Add the groups claim via an Action

Dashboard: **Actions → Library → Build Custom**.

- **Name**: Add groups to access token.
- **Trigger**: Login / Post Login.

Action code:

```javascript
exports.onExecutePostLogin = async (event, api) => {
  const namespace = "https://podium.your-org.example/";
  const groups = (event.user.app_metadata && event.user.app_metadata.groups) || [];
  api.idToken.setCustomClaim(`${namespace}groups`, groups);
  api.accessToken.setCustomClaim(`${namespace}groups`, groups);
};
```

Save and deploy. Then attach the Action: **Actions → Triggers → post-login → drag the new Action into the flow → Apply**.

This reads the user's groups from `app_metadata` (where you populate them via your provisioning process — manually for small teams, or via SCIM for larger setups). The claim emitted is namespaced (`https://podium.your-org.example/groups`) — Auth0 disallows non-namespaced custom claims for security reasons.

## 4. Configure Podium

Registry side (`~/.podium/registry.yaml`):

```yaml
identity:
  provider: oidc
  issuer: https://<your-tenant>.auth0.com/
  audience: https://podium.your-org.example
  jwks_uri: https://<your-tenant>.auth0.com/.well-known/jwks.json
  groups_claim: "https://podium.your-org.example/groups"   # namespaced!
  email_claim: email
  sub_claim: sub
```

The trailing slash in `issuer` matters — Auth0's discovery returns the URL with the slash, and the registry's verification expects an exact match.

Restart the registry.

Developer side:

```bash
podium init --remote https://podium.your-org.example
export PODIUM_OAUTH_CLIENT_ID=<client-id>
export PODIUM_OAUTH_AUDIENCE=https://podium.your-org.example
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

```bash
podium layer register \
  --id engineering-only \
  --repo git@github.com:your-org/podium-engineering.git \
  --ref main \
  --visibility 'groups: ["engineering"]'
```

A user with `engineering` in their `app_metadata.groups` sees the layer; a user without it doesn't.

## Troubleshooting

- **Groups claim is missing from the token.** The Action wasn't attached to the post-login trigger, or the user has no `groups` in `app_metadata`. Check the Action's logs: **Actions → Library → \[Action\] → Logs**.
- **`auth.signature_invalid`.** Auth0 rotated keys; restart the registry to force a JWKS refresh, or wait 5 minutes.
- **`auth.audience_mismatch`.** The audience must match the API identifier *exactly*, including any trailing slash.
- **Custom claim namespace error.** Auth0 rejects non-namespaced custom claims. The claim must look like `https://your-namespace/groups`, not just `groups`.
