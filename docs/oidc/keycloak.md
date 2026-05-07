# Keycloak

Podium + Keycloak. ~15 minutes of setup. Self-hosted, so you control the IdP, which is useful for air-gapped or sovereignty-constrained deployments. Compose-stack development uses Dex (a sibling project), but the production-equivalent IdP for the same niche is Keycloak.

## Prerequisites

- Keycloak instance with admin access (Keycloak ≥ 20 recommended).
- Podium registry running and reachable from developers' browsers.
- A realm to host Podium users (often a shared `your-org` realm; or a dedicated `podium` realm for stricter scoping).

## 1. Create the client

Keycloak admin console: **\[your realm\] → Clients → Create client**.

- **Client type**: OpenID Connect.
- **Client ID**: `podium`.

Capability config:

- **Client authentication**: Off (public client, required for device-code flow).
- **Authentication flow**: enable **OAuth 2.0 Device Authorization Grant**. Disable **Standard flow** and **Direct access grants** unless you need them for other use cases.

Save. The client is created with no redirect URI requirements.

Note the **Client ID** (`podium` per above).

## 2. Configure the groups claim

By default Keycloak does not include group memberships in tokens. Add a mapper:

**Clients → podium → Client scopes → podium-dedicated → Add mapper → By configuration → Group Membership**.

- **Name**: groups.
- **Token Claim Name**: `groups`.
- **Full group path**: Off (you want just the group name, not `/parent/child` paths).
- **Add to ID token**, **Add to access token**, **Add to userinfo**: all On.

Save.

## 3. Expose Podium as an audience

Keycloak's default `aud` claim is the client ID. To use a custom audience like `podium`, add an Audience mapper:

**Clients → podium → Client scopes → podium-dedicated → Add mapper → By configuration → Audience**.

- **Name**: podium-audience.
- **Included Client Audience**: leave empty.
- **Included Custom Audience**: `podium`.
- **Add to access token**: On.

Save. Tokens now include `podium` in their `aud` array.

## 4. Configure Podium

Registry side (`~/.podium/registry.yaml`):

```yaml
identity:
  provider: oidc
  issuer: https://<keycloak-host>/realms/<realm-name>
  audience: podium
  jwks_uri: https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/certs
  groups_claim: groups
  email_claim: email
  sub_claim: sub
```

Restart the registry.

Developer side:

```bash
podium init --global --registry https://podium.your-org.example
export PODIUM_OAUTH_CLIENT_ID=podium
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/auth/device
podium login
```

The verification URL is `https://<keycloak-host>/realms/<realm-name>/device`. After completion, `podium login` prints the resolved identity.

## 5. Create groups and assign users

Keycloak admin console: **Groups → Create group**.

- Create the groups you'll use in Podium layer config (e.g., `engineering`, `platform`, `external-collaborators`).
- **Users → \[user\] → Groups → Join Group** to assign.

## 6. Test

```bash
podium layer register \
  --id engineering-only \
  --repo git@github.com:your-org/podium-engineering.git \
  --ref main \
  --visibility 'groups: ["engineering"]'
```

A member of the `engineering` group sees the layer; a non-member doesn't.

## SCIM (optional)

Keycloak doesn't ship a built-in SCIM server, but extensions exist (e.g., `keycloak-scim-server`). If you install one:

1. Configure the SCIM endpoint at `https://<keycloak-host>/realms/<realm>/scim/v2`.
2. In Podium: `podium admin scim configure --endpoint <url> --token <bearer>`.

For most Keycloak users, the OIDC `groups` claim is sufficient. Group changes apply on next login.

## Troubleshooting

- **Token's `aud` is `podium` (the client ID), not the audience you wanted.** The Audience mapper from step 3 wasn't added or wasn't attached to the dedicated client scope. Check **Clients → podium → Client scopes → podium-dedicated → Mappers**.
- **Groups claim is missing.** The Group Membership mapper wasn't attached. Check the same place.
- **Realm-issuer mismatch.** Keycloak's issuer is `https://<host>/realms/<realm>`, *not* `https://<host>/auth/realms/<realm>` (the latter was the pre-Quarkus URL). If you're on Keycloak < 17, your issuer URL has the `/auth` prefix; adjust accordingly.
- **`podium login` connects but token is rejected.** Confirm the registry's JWKS endpoint is reachable from the registry host (`curl https://<keycloak-host>/realms/<realm>/protocol/openid-connect/certs`). Self-signed Keycloak certs need to be added to the registry's trust store.
