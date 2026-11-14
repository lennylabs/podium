---
title: Tenants and admins
nav_order: 3
description: "Single-tenant and multi-tenant registries, the instance operator, bootstrap admin, and tenant admin roles, and how to provision tenants."
---

# Tenants and admins

The tenant boundary is the org. Each org has its own layer list, admin grants, audit stream, and quotas. An org ID is a UUID, and an org name is a human-readable alias for it. This page describes how a registry assigns requests to tenants, the roles that administer tenants, and how an operator provisions them.

## Single-tenant and multi-tenant

A single-tenant registry is a single-node or clustered registry started without `PODIUM_MULTI_TENANT`. It binds every request to the `default` org and does not consult the caller's organization value. It rejects every tenant-management request with `registry.tenant_management_unavailable`, so it holds no other tenant.

A multi-tenant registry routes each request to the tenant its caller's organization names. Enable multi-tenant mode with `PODIUM_MULTI_TENANT=true`. The registry always provisions the `default` org, and an instance operator provisions every other tenant at runtime.

| Provider | Organization source | Value that resolves to no provisioned tenant |
| --- | --- | --- |
| `oidc-jwt` | The verified `org_id` claim | Rejected with `auth.tenant_unknown`. |
| `trusted-headers` | The `X-Podium-User-Org` header | Treated as anonymous, and sees public visibility only. |

- The organization value is an org ID or an org-name alias, which the registry resolves to a tenant.
- A request that carries no organization value resolves to a reserved tenant that holds no data, so the caller sees an empty catalog and no error.
- A deactivated tenant resolves as unprovisioned.
- The layer endpoints follow the same routing, so a caller whose organization names no tenant lists no layers and cannot write one.

On the clustered Postgres backend each org has its own schema. [Multi-tenancy and quotas](../modes/clustered#multi-tenancy-and-quotas) describes the isolation and the per-tenant quotas.

### Multi-tenant startup refusals

A multi-tenant registry selects each request's tenant from the verified identity, so it requires `oidc-jwt`, `trusted-headers`, or `injected-session-token`.

- A registry started with `PODIUM_MULTI_TENANT=true` and no identity provider, an unrecognized provider value, or public mode fails to start with `config.multitenant_no_verifier`.
- A multi-tenant `trusted-headers` registry with no `PODIUM_TRUSTED_PROXY_SECRET` fails to start with `config.trusted_headers_multitenant_no_secret`, as [Bind restriction](trusted-headers#bind-restriction) describes.

On the Helm chart, the combination of `config.publicMode`, `config.identityProvider.type`, and `config.allowPublicBind` decides which code a refused start reports, as [Multi-tenant startup refusals](../modes/helm-chart#multi-tenant-startup-refusals) lists.

## Roles

| Role | Scope | Granted by | Authorizes |
| --- | --- | --- | --- |
| Instance operator | The whole registry | `PODIUM_OPERATOR_ADMINS` at startup | The `/v1/admin/tenants` API and the `podium admin tenant` commands. |
| Tenant admin | One tenant | `PODIUM_BOOTSTRAP_ADMINS` at startup for the `default` tenant, or `podium admin grant` by an existing admin of the tenant | Admin-defined layers, admin grants in the tenant, reingests, re-embeds, and the diagnostic visibility override. |

No admin command sets freeze windows. The registry reads them from the `freeze_windows:` list in `registry.yaml` at startup.

The roles are independent. The operator role confers no tenant admin rights, and a tenant admin grant confers no operator rights.

Both roles match the caller's recorded subject, which is the `sub` claim, the claim that `identity_provider.subject_claim` names, or the `X-Podium-User-Sub` header. They have no email fallback, so list subject values in both variables. [Subject claim](oidc/#subject-claim) describes a deployment that reads the subject from another claim.

### Instance operator

Set `PODIUM_OPERATOR_ADMINS` to a comma-separated list of identities. The registry grants each one the instance-operator role at startup. The grant is held outside every tenant, so an operator manages tenants regardless of the tenant the operator's own organization names. The operator authenticates as any caller does, for example with `podium login`.

### Bootstrap admin

The first tenant admin cannot be granted over the API, because granting requires an existing admin. Set `PODIUM_BOOTSTRAP_ADMINS` to a comma-separated list of identities, and the registry grants each one the tenant `admin` role in the `default` tenant at startup. On the Helm chart, `config.bootstrapAdmins` sets the variable.

On a registry with no identity provider no caller is authenticated, so the grant takes effect only once a provider is configured. On a multi-tenant registry the bootstrap admins administer the `default` tenant, which a caller reaches when its organization value is `default`.

### Tenant admin

A tenant admin grants the `admin` role to another identity with `podium admin grant <user-id> --registry <url>`, and revokes it with `podium admin revoke <user-id> --registry <url>`. Both act in the tenant that the admin's own request resolves to.

A caller without the `admin` role in that tenant is refused with `auth.forbidden`. The registry seeds no admin grant in a tenant that an operator provisions at runtime, and the operator role confers no admin rights there. [`podium admin grant` / `podium admin revoke`](../../reference/cli#podium-admin-grant--podium-admin-revoke) lists the flags, and [Who can change a layer](../content/access-control#who-can-change-a-layer) describes what the role authorizes on layers.

## Provisioning tenants

An instance operator provisions tenants at runtime on a multi-tenant registry.

| Task | Command |
| --- | --- |
| Provision an org | `podium admin tenant create <name>`, or `POST /v1/admin/tenants` |
| List tenants | `podium admin tenant list` |
| Update a tenant's quota or active state | `podium admin tenant update <id>` |
| Deactivate a tenant | `podium admin tenant deactivate <id>` |
| Reactivate a tenant | `podium admin tenant update <id> --active true` |

- `create` derives the org ID from the name, and it is idempotent, so re-creating an existing name returns that tenant unchanged.
- `update` changes only the fields whose flags it receives, and it cannot rename a tenant.
- Deactivation is soft. A deactivated tenant stops resolving while its data, including its audit history, persists.
- A request to the tenant-management API without the operator role is rejected with `auth.forbidden`. The API is exempt from per-request tenant selection, so the first operator can create the first tenant beyond `default`.

The [CLI reference](../../reference/cli#podium-admin-tenant) lists the full flag set, including the per-tenant quota flags.
