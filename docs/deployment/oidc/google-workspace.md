---
layout: default
title: Google Workspace
parent: OIDC cookbooks
grand_parent: Deployment
nav_order: 3
description: Configure Podium to authenticate against Google Workspace via OIDC device-code flow.
---

# Google Workspace

This guide configures a Podium registry to authenticate against Google Workspace. Workspace does not emit an OIDC `groups` claim natively. For group-based visibility, groups arrive either through SCIM 2.0 push or as OIDC group claims mapped by the `IdpGroupMapping` adapter.

## Prerequisites

- Google Workspace admin role (or a delegate with the right scopes).
- Podium registry running and reachable from developers' browsers.
- A Google Cloud project for the OAuth client registration.

## 1. Create the OAuth client

Google Cloud Console: **APIs & Services → Credentials → Create Credentials → OAuth client ID**.

- **Application type**: TVs and Limited Input devices (this is the path Google supports for the device-code flow).
- **Name**: Podium.

After creation, you get a **Client ID** and **Client secret**. Note both; Google's device-flow requires the client secret even though the OIDC spec doesn't strictly require it for public clients.

Configure the OAuth consent screen if it's not already configured:

- **User type**: Internal (within the Workspace org).
- **App name**: Podium.
- **Scopes**: `openid`, `email`, `profile`.

## 2. Decide your group-resolution strategy

Three options, in increasing complexity:

**Option A: no groups, email-only visibility.** Set layer visibility based on individual email addresses (`users: [alice@acme.com, bob@acme.com]`) or to `organization: true` (any authenticated user from the Workspace org). Works for small teams.

**Option B: push Workspace groups via SCIM.** Workspace does not push SCIM natively. A Workspace add-on or a sync script populates the registry's SCIM directory, and the registry resolves group membership from that directory. Maintainer-script approach; out of scope here.

**Option C: map OIDC group claims with `IdpGroupMapping`.** When a token carries a group claim (added through a custom OIDC configuration or a directory integration), the `IdpGroupMapping` adapter reads the raw group values from the token and maps them to the group names used in layer visibility. The adapter reads claims already present in the token; it does not call a Cloud Identity API.

For most teams, Option A is enough to start. Move to Option B or C when different layers need to be visible to different Workspace groups.

## 3. Configure Podium

Registry side (`registry.yaml`):

```yaml
identity_provider:
  type: oauth-device-code
  audience: <client-id>.apps.googleusercontent.com
  authorization_endpoint: https://accounts.google.com
```

For Option C, configure the `IdpGroupMapping` adapter registry-side to map the token's group values to group names. To restrict access to the Workspace domain, place the registry behind an upstream identity-aware proxy that authenticates the human caller and admits only accounts in the domain. Restart the registry.

Developer side:

```bash
podium init --global --registry https://podium.acme.com
export PODIUM_OAUTH_CLIENT_ID=<client-id>.apps.googleusercontent.com
export PODIUM_OAUTH_CLIENT_SECRET=<client-secret>
export PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=https://oauth2.googleapis.com/device/code
podium login
```

The verification URL is `https://www.google.com/device`. After the flow completes, `podium login` prints the `sub` and `email`.

## 4. Test

Configure an admin layer visible to the whole organization in the tenant's layer config. Organization-scoped visibility is set in the registry layer config, or with `podium layer register --organization` (also `--public`, `--group`, and `--user`). Layers registered with `--user-defined` are private to the registrant and cannot be widened.

```yaml
layers:
  - id: team-shared
    source:
      git:
        repo: git@github.com:acme/podium-artifacts.git
        ref: main
    visibility:
      organization: true
```

A user from the Workspace domain sees the layer. To keep accounts outside the domain from reaching the registry at all, the upstream identity-aware proxy admits only Workspace-domain accounts.

## Troubleshooting

- **Token rejected.** Google's `aud` claim is the full client ID with the `.apps.googleusercontent.com` suffix. Confirm the registry's `audience:` matches it exactly.
- **Accounts outside the domain can authenticate.** Domain restriction is enforced by the upstream identity-aware proxy, configured to admit only Workspace-domain accounts. Confirm the proxy is in front of the registry and its allow rule names the domain.
- **Group membership does not update.** With Option B, group changes propagate when the IdP pushes to the registry's SCIM endpoint. With Option C, group membership reflects the token's group claim at login time, so a changed membership applies at the user's next login.
