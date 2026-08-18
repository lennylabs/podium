---
title: Keycloak
nav_order: 5
description: Configure Podium to authenticate against self-hosted Keycloak via OIDC device-code flow.
---

# Keycloak

This guide configures a Podium registry to authenticate against self-hosted Keycloak. Setup takes about 15 minutes. Self-hosting puts the IdP under your control, which suits air-gapped or sovereignty-constrained deployments. The Compose stack uses Dex for development; Keycloak is the production-equivalent IdP for the same niche.

## Prerequisites

- Keycloak instance with admin access (Keycloak ≥ 20 recommended).
- Podium registry running and reachable from developers' browsers.
- A realm to host Podium users (often a shared `acme` realm, or a dedicated `podium` realm for stricter scoping).

## 1. Create the client

Keycloak admin console: **\[your realm\] → Clients → Create client**.

- **Client type**: OpenID Connect.
- **Client ID**: `podium`.

Capability config:

- **Client authentication**: Off (public client, required for device-code flow).
- **Authentication flow**: enable **OAuth 2.0 Device Authorization Grant**. Disable **Standard flow** and **Direct access grants** unless other use cases require them.

Save. The client is created with no redirect URI requirements.

Note the **Client ID** (`podium` per above).

## 2. Configure the groups claim

By default Keycloak does not include group memberships in tokens. Add a mapper:

**Clients → podium → Client scopes → podium-dedicated → Add mapper → By configuration → Group Membership**.

- **Name**: groups.
- **Token Claim Name**: `groups`.
- **Full group path**: Off. Podium expects the group name rather than `/parent/child` paths.
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

Registry side (`registry.yaml`):

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<keycloak-host>/realms/<realm-name>   # must be https
    audience: podium
```

Every server-side key nests under the top-level `registry:` mapping. A document that starts at `identity_provider:` parses to an empty config and the registry ignores it without reporting an error.

`oidc-jwt` is the registry's side of the flow: it verifies each presented token against the realm's JWKS and validates the `aud` claim against `audience:`. The issuer is the realm URL rather than the device-authorization endpoint, and the registry resolves the JWKS from `<issuer>/.well-known/openid-configuration`. Developers obtain the token by completing the device-code flow from the CLI, which the next step configures. Setting `oauth-device-code` as the registry's own provider stops startup with `config.identity_provider_unverified`.

The Group Membership mapper from step 2 emits the `groups` claim with bare group names. The `IdpGroupMapping` adapter passes those names through, or the SCIM directory supplies membership when SCIM is configured. Restart the registry.

Developer side:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=podium
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/auth/device
export PODIUM_OAUTH_TOKEN_URL=https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/token
podium login --scopes "openid profile email"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` rewrites the trailing `/device` of the device-authorization URL to `/token`, which produces `https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/auth/token` and the token exchange fails. The default scope set adds `groups`, which the client does not declare; the Group Membership mapper from step 2 emits the claim without it.

The verification URL is `https://<keycloak-host>/realms/<realm-name>/device`. After completion, `podium login` prints the resolved identity.

## 5. Create groups and assign users

Keycloak admin console: **Groups → Create group**.

- Create the groups used in Podium layer config, for example `engineering`, `platform`, and `external-collaborators`.
- **Users → \[user\] → Groups → Join Group** to assign.

## 6. Test

Configure an admin layer scoped to the `engineering` group in `registry.yaml`. Group-scoped visibility is set in the registry layer config, or with `podium layer register --group <name>` (also `--public`, `--organization`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

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

A member of the `engineering` group sees the layer; a non-member does not.

## SCIM (optional)

Keycloak does not ship a built-in SCIM server, but extensions exist (for example `keycloak-scim-server`). When one is installed, configure it to push user and group records to the registry's SCIM endpoint at `https://podium.acme.com/scim/v2`, authenticating with one of the bearer tokens listed in the registry's `PODIUM_SCIM_TOKENS` environment variable. The registry mounts `/scim/v2/` only when that variable is set to a comma-separated list of accepted tokens, and returns 404 for every SCIM request otherwise. Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart.

For most Keycloak users, the OIDC `groups` claim is sufficient. Group changes apply on the user's next login.

## Troubleshooting

- **Token rejected with `auth.untrusted_token` on every call.** The token's `aud` array does not carry the value configured under `audience:`. Confirm the Audience mapper from step 3 is attached to the dedicated client scope under **Clients → podium → Client scopes → podium-dedicated → Mappers**, and that its **Included Custom Audience** is the same string as `audience:` in `registry.yaml`.
- **Groups claim is missing.** The Group Membership mapper was not attached. Check the same place.
- **Realm-issuer mismatch.** Keycloak's issuer is `https://<host>/realms/<realm>` rather than `https://<host>/auth/realms/<realm>`; the latter was the pre-Quarkus URL. Keycloak versions before 17 use the `/auth` prefix in the issuer URL.
- **`podium login` connects but the token is rejected.** Confirm the token's `aud` matches the registry's `audience:`, and that the Audience and Group Membership mappers are attached to the dedicated client scope.
