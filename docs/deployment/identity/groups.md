---
title: Groups and SCIM
nav_order: 4
description: "Resolve group membership for visibility rules from OIDC group claims or a SCIM 2.0 push."
---

# Groups and SCIM

## Group resolution paths

Two mechanisms resolve group membership on the registry. Each cookbook names the one its IdP supports.

### SCIM 2.0 push

The IdP pushes user and group records to the registry's SCIM endpoint. The registry maintains a directory of `(user_id → groups)`.

- A layer's `groups:` entry names a SCIM group by its `displayName`. The registry grants visibility when that group holds a member whose SCIM `userName` equals the caller's `sub` or `email` claim.
- Provision each user under a `userName` that the caller's token also carries. A `userName` that matches neither claim resolves to no group visibility and reports no error. The SCIM user's `emails` value is stored but never compared.
- Group-membership changes apply without waiting for the user's next login.

To enable the receiver:

- Set `PODIUM_SCIM_TOKENS` to a comma-separated list of the bearer tokens the registry accepts. The registry mounts `/scim/v2/` only when this variable is set. With it unset, every SCIM request returns 404, and layer `groups:` filters resolve from the token claim alone.
- Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart. A set path that the registry cannot read, parse, or write refuses startup with [`config.scim_store_unavailable`](../../reference/error-codes#config).

Neither variable has a `registry.yaml` key.

### OIDC group claims with IdpGroupMapping

Under `oidc-jwt`, the registry reads group membership from the token's `groups` claim, or from the claim that `PODIUM_OAUTH_GROUPS_CLAIM` (`identity_provider.groups_claim`) names. It maps the raw values to group names through `PODIUM_IDP_GROUP_MAPPING`, a comma-separated list of `<token-value>=<group-name>` pairs.

- A value with no mapping entry passes through unchanged.
- The mapping has no `registry.yaml` key.
- The claim value is a JSON array of strings, or a single string when the IdP emits one group. A single string yields one group whose name is the whole claim value, and the registry does not split it on any separator.
- Group membership reflects what was in the token at login time.
