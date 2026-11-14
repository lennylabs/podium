---
title: Tenants and admins
nav_order: 3
description: "Single-tenant and multi-tenant registries, the operator and admin roles, and how to provision tenants."
---

# Tenants and admins

## Single-tenant and multi-tenant

A single-tenant registry is a single-node or clustered registry started without `PODIUM_MULTI_TENANT`. It resolves every authenticated caller to its sole tenant and does not consult the organization value.

A multi-tenant registry routes each request to the tenant its organization names. Enable multi-tenant mode with `PODIUM_MULTI_TENANT=true`. The `default` org is always provisioned.

| Provider | Organization source | Value that resolves to no provisioned tenant |
| --- | --- | --- |
| `oidc-jwt` | The verified `org_id` claim | Rejected with `auth.tenant_unknown`. |
| `trusted-headers` | The `X-Podium-User-Org` header | Treated as anonymous, and sees public visibility only. |

The organization value is an org ID or an org-name alias, which the registry resolves to a tenant. The layer endpoints follow the same routing, so a caller whose `X-Podium-User-Org` names no tenant lists no layers and cannot write one.

### Provisioning tenants

An instance operator provisions tenants at runtime. Seed the first operator with `PODIUM_OPERATOR_ADMINS` (comma-separated identities), which grants the instance-operator role at boot. The operator role is distinct from the per-tenant `admin` role and from `PODIUM_BOOTSTRAP_ADMINS`. It authorizes the `/v1/admin/tenants` API and the `podium admin tenant` CLI, and it confers no per-tenant `admin` rights.

| Task | Command |
| --- | --- |
| Provision an org | `podium admin tenant create <name>`, or `POST /v1/admin/tenants` |
| List tenants | `podium admin tenant list` |
| Update a tenant's quota or active state | `podium admin tenant update <id>` |
| Deactivate a tenant | `podium admin tenant deactivate <id>` |
| Reactivate a tenant | `podium admin tenant update <id> --active true` |

Create is idempotent, so re-creating an existing name returns that tenant unchanged. Deactivation is soft: a deactivated tenant stops resolving while its data persists. Tenant management is available only in multi-tenant mode. A single-tenant registry rejects every `/v1/admin/tenants` request with `registry.tenant_management_unavailable`. The [CLI reference](../../reference/cli#podium-admin-tenant) lists the full flag set.
