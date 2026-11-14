---
title: Keycloak
nav_order: 5
description: Configure a Podium registry to authenticate callers against self-hosted Keycloak through the OIDC device-code flow.
---

# Keycloak

This cookbook configures a Podium registry to verify tokens from a self-hosted Keycloak under `oidc-jwt`, and configures developer machines to acquire them through the device-code flow. A self-hosted IdP stays under the operator's control, which suits air-gapped or sovereignty-constrained deployments. The Docker Compose evaluation stack uses Dex, and Keycloak serves the same role in production. Setup takes about 15 minutes. [OIDC](index) is the reference for the `oidc-jwt` settings these steps use.

## Prerequisites

- A Keycloak instance with admin access. Keycloak 20 or later is recommended.
- A Podium registry running and reachable from developers' browsers.
- A realm to host Podium users, often a shared `acme` realm, or a dedicated `podium` realm for stricter scoping.

## 1. Create the client

1. In the Keycloak admin console, open **\[the realm\] → Clients → Create client**.
2. Set **Client type** to OpenID Connect.
3. Set **Client ID** to `podium`.
4. Under the capability config, set **Client authentication** to Off. The device-code flow requires a public client.
5. Under **Authentication flow**, enable **OAuth 2.0 Device Authorization Grant**. Disable **Standard flow** and **Direct access grants** unless other use cases require them.
6. Save.

Keycloak creates the client with no redirect URI requirements. The client ID is `podium`.

## 2. Add the groups claim mapper

By default, Keycloak does not include group memberships in tokens.

1. Open **Clients → podium → Client scopes → podium-dedicated → Add mapper → By configuration → Group Membership**.
2. Set **Name** to groups.
3. Set **Token Claim Name** to `groups`.
4. Set **Full group path** to Off. Podium expects the group name rather than `/parent/child` paths.
5. Set **Add to ID token**, **Add to access token**, and **Add to userinfo** to On.
6. Save.

## 3. Add the audience mapper

Keycloak's default `aud` claim is the client ID. To use a custom audience such as `podium`, add an Audience mapper.

1. Open **Clients → podium → Client scopes → podium-dedicated → Add mapper → By configuration → Audience**.
2. Set **Name** to podium-audience.
3. Leave **Included Client Audience** empty.
4. Set **Included Custom Audience** to `podium`.
5. Set **Add to access token** to On.
6. Save.

Expected result: tokens include `podium` in their `aud` array.

## 4. Create groups and assign users

1. In the Keycloak admin console, open **Groups → Create group**.
2. Create the groups used in Podium layer config, for example `engineering`, `platform`, and `external-collaborators`.
3. Open **Users → \[user\] → Groups → Join Group** to assign each user.

## 5. Configure the registry

On the registry host, add the `identity_provider:` block to `registry.yaml`, nested under `registry:` as [File layout](../../../reference/configuration#file-layout) describes:

```yaml
registry:
  identity_provider:
    type: oidc-jwt
    issuer: https://<keycloak-host>/realms/<realm-name>   # must be https
    audience: podium
```

The issuer is the realm URL rather than the device-authorization endpoint. The registry resolves the realm's JWKS from `<issuer>/.well-known/openid-configuration`, verifies each presented token against it, and requires the token's `aud` claim to carry `podium`.

The Group Membership mapper from step 2 emits the `groups` claim with bare group names. The `IdpGroupMapping` adapter passes those names through, or the SCIM directory supplies membership when SCIM is configured.

Restart the registry.

## 6. Configure developer machines

On each developer machine, run:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=podium
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/auth/device
export PODIUM_OAUTH_TOKEN_URL=https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/token
podium login --scopes "openid profile email"
```

Set `PODIUM_OAUTH_TOKEN_URL` explicitly. With it unset, `podium login` rewrites the trailing `/device` of the device-authorization URL to `/token`. That produces `https://<keycloak-host>/realms/<realm-name>/protocol/openid-connect/auth/token`, and the token exchange fails.

Pass `--scopes "openid profile email"`. The default scope set adds `groups`, which the client does not declare. The Group Membership mapper from step 2 emits the claim without it.

Expected result: the verification URL is `https://<keycloak-host>/realms/<realm-name>/device`. After the flow completes, `podium login` prints the resolved identity.

## 7. Test group-based visibility

Configure an admin layer scoped to the `engineering` group in `registry.yaml`. [Declaring visibility](../../content/access-control#declaring-visibility) describes the other ways to set a layer's visibility.

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

Expected result: a member of the `engineering` group sees the layer, and a non-member does not.

## SCIM provisioning (optional)

For most Keycloak deployments, the OIDC `groups` claim is sufficient, and group changes apply on the user's next login.

Keycloak does not ship a built-in SCIM server, but extensions exist, for example `keycloak-scim-server`. When one is installed, configure it to push user and group records to the registry's SCIM endpoint at `https://podium.acme.com/scim/v2`. Authenticate with one of the bearer tokens listed in the registry's `PODIUM_SCIM_TOKENS` environment variable.

On the registry, enable the receiver as [Enable the receiver](../groups#enable-the-receiver) describes. The receiver answers `PATCH` with HTTP 405, so confirm which methods the extension sends, as [Endpoints](../groups#endpoints) describes.

## Next steps

- Declare visibility on the remaining layers with [Access control](../../content/access-control).
- Review the caller identities the registry records in the [audit log](../../operations/observability#audit-log).

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Every call is rejected with `auth.untrusted_token`. | The token's `aud` array does not carry the value configured under `audience:`. | Confirm that the Audience mapper from step 3 is attached to the dedicated client scope under **Clients → podium → Client scopes → podium-dedicated → Mappers**, and that its **Included Custom Audience** is the same string as `audience:` in `registry.yaml`. |
| The groups claim is missing. | The Group Membership mapper is not attached. | Check the mappers under the same path. |
| The issuer does not match. | Keycloak's issuer is `https://<host>/realms/<realm>`. The `https://<host>/auth/realms/<realm>` form is the pre-Quarkus URL, which Keycloak versions before 17 use. | Configure `issuer:` with the form that the Keycloak version issues. |
| `podium login` connects, but the token is rejected. | The token's `aud` does not match the registry's `audience:`, or a mapper is missing. | Confirm the `aud` value, and that the Audience and Group Membership mappers are attached to the dedicated client scope. |
