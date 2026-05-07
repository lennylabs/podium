# Google Workspace

Podium + Google Workspace. The setup is straightforward but Workspace doesn't emit an OIDC `groups` claim natively — for group-based visibility you either resolve group membership server-side via the Cloud Identity API, or use a directory-sync pattern.

## Prerequisites

- Google Workspace admin role (or a delegate with the right scopes).
- Podium registry running and reachable from developers' browsers.
- A Google Cloud project where you'll register the OAuth client.

## 1. Create the OAuth client

Google Cloud Console: **APIs & Services → Credentials → Create Credentials → OAuth client ID**.

- **Application type**: TVs and Limited Input devices (this is the path Google supports for the device-code flow).
- **Name**: Podium.

After creation, you get a **Client ID** and **Client secret**. Note both — Google's device-flow requires the client secret even though the OIDC spec doesn't strictly require it for public clients.

Configure the OAuth consent screen if it's not already configured:

- **User type**: Internal (within your Workspace org).
- **App name**: Podium.
- **Scopes**: `openid`, `email`, `profile`.

## 2. Decide your group-resolution strategy

Three options, in increasing complexity:

**Option A — no groups, email-only visibility.** Set layer visibility based on individual email addresses (`users: [a@you.com, b@you.com]`) or to `organization: true` (any authenticated user from your Workspace org). Works fine for small teams.

**Option B — sync Workspace groups via SCIM.** Workspace doesn't natively push SCIM, but a pattern using a Workspace add-on or a small sync script can populate Podium's directory. Maintainer-script approach; out of scope here.

**Option C — resolve group membership at token-validation time.** The registry calls Google Cloud Identity's API to fetch the user's group memberships when the JWT arrives, caches for 5 minutes, treats those as the `groups` claim. Requires a service account in your Cloud project with `Cloud Identity Groups Reader` (`cloudidentity.googleapis.com/groups.readonly`). Configure on the registry side via the `IdpGroupMapping` adapter — see [§6.3.1 of the spec](../../spec/06-mcp-server.md#631-claim-derivation).

For most teams, Option A is enough to start. Move to Option C when you actually need different layers visible to different Workspace groups.

## 3. Configure Podium

Registry side (`~/.podium/registry.yaml`):

```yaml
identity:
  provider: oidc
  issuer: https://accounts.google.com
  audience: <your-client-id>.apps.googleusercontent.com
  jwks_uri: https://www.googleapis.com/oauth2/v3/certs
  email_claim: email
  sub_claim: sub
  # Option A: no groups_claim
  # Option C: configure IdpGroupMapping (see §6.3.1)
  hd_claim: hd                            # Google's hosted-domain claim — restricts tokens to your org
  hd_required: your-org.example           # rejects tokens issued for other Workspace domains
```

The `hd_claim` enforcement is critical for Workspace — without it, any Google account (gmail.com or another Workspace org) with the right client ID can authenticate. Setting `hd_required` rejects everything except your domain.

Restart the registry.

Developer side:

```bash
podium init --global --registry https://podium.your-org.example
export PODIUM_OAUTH_CLIENT_ID=<client-id>.apps.googleusercontent.com
export PODIUM_OAUTH_CLIENT_SECRET=<client-secret>
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://oauth2.googleapis.com/device/code
podium login
```

The verification URL is `https://www.google.com/device`. After the flow completes, `podium login` prints the `sub`, `email`, and `hd`.

## 4. Test

```bash
podium layer register \
  --id team-shared \
  --repo git@github.com:your-org/podium-artifacts.git \
  --ref main \
  --visibility 'organization: true'
```

A user from your Workspace domain sees the layer; a user from outside (even with a valid Google login) is rejected with `auth.hd_mismatch` before they even get to layer composition.

## Troubleshooting

- **`auth.hd_mismatch` for legitimate users.** Confirm the user is in your Workspace domain and not signed in to a personal Gmail. Sign out, sign back in with the work account.
- **Tokens issued but rejected as `auth.audience_mismatch`.** Google's `aud` claim is the full client ID with the `.apps.googleusercontent.com` suffix; make sure the registry's `audience:` matches exactly.
- **Group membership doesn't update.** With Option C, the registry caches group lookups for 5 minutes per user. After a group change, either wait for the cache to expire or invalidate via `podium admin claims-cache flush --user <sub>`.
- **Service account permissions denied (Option C).** The service account needs `Cloud Identity Groups Reader` and the Cloud Identity API enabled in your project.
