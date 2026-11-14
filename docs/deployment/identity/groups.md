---
title: Groups and SCIM
nav_order: 4
description: "Where the registry gets a caller's group membership for layer visibility: OIDC group claims mapped by IdpGroupMapping, and a directory the identity provider pushes over SCIM 2.0."
---

# Groups and SCIM

A layer's `groups:` visibility filter admits a caller who belongs to one of the listed groups. This page describes where the registry gets a caller's group membership and how to configure each source. [Access control](../content/access-control) describes the visibility fields themselves.

## Group resolution paths

The registry resolves group membership from the group claim in the caller's token and from a directory that the identity provider (IdP) pushes over SCIM 2.0. A caller satisfies a `groups:` filter when either source places the caller in a listed group. Which sources apply depends on the registry's identity provider.

| Registry provider | Token group claim | SCIM directory |
| --- | --- | --- |
| `oidc-jwt` | The `groups` claim, or the claim that `groups_claim` names, rewritten by `IdpGroupMapping`. | Consulted when the receiver is mounted. |
| `injected-session-token` | The `groups` claim, rewritten by `IdpGroupMapping`. | Consulted when the receiver is mounted. |
| `trusted-headers` | The gateway's `X-Podium-User-Groups` header replaces the claim, and `IdpGroupMapping` is not applied. | Not consulted. |

A registry with no identity provider, or in public mode, admits every caller to every layer and evaluates no `groups:` filter. Each [OIDC cookbook](oidc/) names the paths its IdP covers.

## OIDC group claims with IdpGroupMapping

Under `oidc-jwt`, the registry reads group membership from the token's `groups` claim, or from the claim that `PODIUM_OAUTH_GROUPS_CLAIM` (`identity_provider.groups_claim`) names. Under `injected-session-token` it reads the `groups` claim.

- The claim value is a JSON array of strings, or a single string when the IdP emits one group. A single string yields one group whose name is the whole value, and the registry does not split it on any separator.
- Membership reflects the token as the IdP issued it. A membership change applies once the caller presents a newly issued token, after the next login or token refresh.

The `IdpGroupMapping` adapter rewrites claim values to the group names that layer visibility uses. Set `PODIUM_IDP_GROUP_MAPPING` to a comma-separated list of `<claim-value>=<group-name>` pairs:

```text
PODIUM_IDP_GROUP_MAPPING=7c52a1d4-1111-2222-3333-444455556666=engineering,9ab3f0c2-7777-8888-9999-aaaabbbbcccc=platform
```

- A claim value with no entry passes through unchanged, so an IdP that already emits the layer group names needs no mapping.
- Whitespace around each name is trimmed, and a blank entry is dropped.
- A non-empty value that carries an entry without `=`, or with an empty name on either side, or that resolves to no entry at all, fails startup with `config.invalid_idp_group_mapping`. The registry refuses the whole setting instead of starting with a partial table, so one malformed entry discards the well-formed entries beside it.
- The mapping is environment-only and has no `registry.yaml` key.

## SCIM 2.0 push

The IdP pushes user and group records to the registry's SCIM 2.0 receiver, and the registry keeps a directory of users and their groups. A membership change applies as soon as the IdP pushes it, without waiting for the user's next login.

### Enable the receiver

1. Set `PODIUM_SCIM_TOKENS` on the registry to a comma-separated list of the bearer tokens the receiver accepts. Each entry is trimmed, blank entries are dropped, and any listed token authorizes every request to the receiver. The registry mounts the receiver at `/scim/v2/` only when at least one token is listed. With none, every SCIM request returns 404.
2. Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the directory survives a restart. With the variable unset, the directory is held in memory and is lost on restart. With it set, the registry creates the parent directory at startup and the file on the first push, and a missing or empty file loads as an empty directory.
3. Restart the registry.
4. In the IdP's provisioning settings, set the SCIM base URL to the registry's receiver, for example `https://podium.acme.com/scim/v2`, and authenticate with one of the listed tokens as an HTTP bearer token.

Neither variable has a `registry.yaml` key. A set `PODIUM_SCIM_STORE_PATH` that the registry cannot read, parse, or write refuses startup with `config.scim_store_unavailable`, under every identity provider and whether or not `PODIUM_SCIM_TOKENS` mounts the receiver. [config.scim_store_unavailable causes](../../reference/error-codes#configscim_store_unavailable-causes) lists the conditions, and [Troubleshooting](../operations/troubleshooting) describes the recovery. Include the store file in [backups](../operations/backup-restore).

### Matching users and groups

- A layer's `groups:` entry names a SCIM group by its `displayName`. The `IdpGroupMapping` table does not apply to SCIM group names.
- The registry grants visibility when that group holds a member whose SCIM `userName` equals the caller's subject or `email`.
- Provision each user under a `userName` that the caller's token also carries. A `userName` that matches neither the subject nor the email resolves to no group visibility and reports no error. The SCIM user's `emails` value is stored but never compared.

### Endpoints

The receiver accepts the following requests. A request without one of the listed bearer tokens returns HTTP 401.

| Path | Methods |
| --- | --- |
| `/scim/v2/Users` | `GET` (list, with an optional filter) and `POST` |
| `/scim/v2/Users/{id}` | `GET`, `PUT`, and `DELETE` |
| `/scim/v2/Groups` | `GET` (list, with an optional filter) and `POST` |
| `/scim/v2/Groups/{id}` | `GET`, `PUT`, and `DELETE` |

- A list filter takes the form `<attribute> <operator> "<value>"` with the `eq`, `sw`, `co`, or `pr` operator.
- Any other method on these paths, including `PATCH`, returns HTTP 405. An IdP that sends membership changes or deactivations as `PATCH` therefore leaves the directory in the state its create and replace requests established. The Okta and Entra ID cookbooks describe this limitation for those IdPs.
- `/scim/v2/ServiceProviderConfig` returns HTTP 501, and any other path returns HTTP 404.

The [HTTP API reference](../../reference/http-api#scim-provisioning) lists the route with the other registry endpoints.

### Where the directory is held

The directory belongs to the registry process. The process holds it in memory, reads the store file once at startup, and rewrites the whole file from its in-memory copy on every push. The directory is not stored in the metadata store, which has these consequences:

- Each replica of a clustered deployment holds its own directory. A push reaches only the replica that the load balancer routes it to, so group visibility that depends on the directory differs between replicas. On a deployment with more than one replica, resolve membership from the token's group claim.
- Two processes must not share one store file, because each overwrites the file with its own copy.
- The directory carries no tenant. On a multi-tenant registry, every tenant's `groups:` filters expand against the same directory, so a group `displayName` resolves to the same members in every tenant.

### SCIM under trusted-headers

Under `trusted-headers`, the registry still mounts the receiver when `PODIUM_SCIM_TOKENS` is set, and it keeps accepting and serving pushes, but no visibility decision consults the directory. A `groups:` filter matches the `X-Podium-User-Groups` value alone, and a directory entry naming the caller's `X-Podium-User-Sub` or `X-Podium-User-Email` value grants nothing. The registry logs the withheld expansion at startup:

```text
SCIM group expansion in layer visibility: false (identity provider "trusted-headers")
```

To let directory membership grant visibility, provision the membership at the gateway, or run `oidc-jwt`, where the registry resolves groups from a credential it verifies.
