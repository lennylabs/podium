---
title: Access control
nav_order: 3
description: "Declare who can read each layer and who can change it, configure the deployment defaults, and debug a caller's effective view."
---

# Access control

This page covers how a server-backed registry decides which layers a caller can read and which layer operations a caller can perform. It is for operators who register layers and configure identity. [Concepts → Visibility](../../getting-started/concepts#visibility) defines the underlying model.

---

## Visibility model

Access control in Podium is declared per layer. Each layer states who can see it. The registry evaluates that declaration against the caller's identity on every call, and it composes the caller's effective view from the layers that matched.

### Visibility fields

A layer's `visibility:` block takes one or more of these fields.

| Field | Who matches |
|:--|:--|
| `public: true` | Anyone, including unauthenticated callers. |
| `organization: true` | Any authenticated user in the tenant org. |
| `groups: [<oidc-group>, ...]` | Members of the listed OIDC groups. |
| `users: [<user-id>, ...]` | The listed identifiers, matched by OIDC subject or email. |

Multiple fields combine as a union. A layer with `groups: [acme-finance]` and `users: [security-lead@acme.com]` is visible to every member of `acme-finance` and to that one additional identity.

### Enforcement

The registry enforces visibility on every call. A call that visibility rejects returns the `visibility.denied` outcome, which mirrors a not-found result, so the rejection does not reveal that a hidden artifact exists. The [audit log](../operations/observability#audit-log) records each denial, and the `podium_visibility_denied_total` metric counts them.

> [!IMPORTANT]
> A registry with no identity provider, a catalog read from a filesystem source, and a registry in public mode do not enforce visibility. [When visibility is not enforced](#when-visibility-is-not-enforced) lists each case.

### Where group membership comes from

Layer visibility references group names. Group membership reaches the registry through the OIDC `groups` claim of the caller's token, or through a SCIM 2.0 push from the IdP. An IdP that emits group identifiers instead of names needs `PODIUM_IDP_GROUP_MAPPING` to translate them. Under `trusted-headers`, the registry does not expand `groups:` filters against a SCIM directory, as [Gateway (trusted headers)](../identity/trusted-headers) describes. [Groups and SCIM](../identity/groups) covers both paths and their settings.

SCIM is recommended once group-based visibility gates artifacts that matter, because a claim reflects membership at login time and a push applies a change at once.

### Deployment defaults

`PODIUM_DEFAULT_LAYER_VISIBILITY` sets the visibility an admin-defined layer takes when it registers without a `visibility:` block. It accepts `public`, `organization`, or `private`. Without an explicit setting, the value follows whether identity is configured:

| Registry state | Default for an admin-defined layer with no declaration |
|:--|:--|
| No identity provider | `public` |
| An identity provider is configured | `private`, meaning no visibility filters, so only an explicit grant reaches it |

The default changes when identity is turned on so that admin-defined layers do not stay open by accident.

A user-defined layer is a separate case. Its visibility is implicitly `users: [<registrant>]`, derived from the authenticated caller at registration, and it cannot be widened. The deployment default does not apply to it.

---

## Declaring visibility

Declare visibility in `registry.yaml` for layers that the configuration file defines, or with the CLI for layers registered at runtime.

### In registry.yaml

Each layer in `registry.yaml` carries its own `visibility:` block. Every server-side key nests under the top-level `registry:` mapping. A document that starts at `layers:` parses to an empty config, and the registry ignores it without reporting an error.

```yaml
registry:
  layers:
    - id: org-defaults
      source:
        git: { repo: git@github.com:acme/podium-org-defaults.git, ref: main }
      visibility:
        organization: true

    - id: team-finance
      source:
        git: { repo: git@github.com:acme/podium-finance.git, ref: main }
      visibility:
        groups: [acme-finance, acme-finance-leads]

    - id: public-marketing
      source:
        git: { repo: git@github.com:acme/podium-public.git, ref: main }
      visibility:
        public: true
```

[Connecting layer sources](layer-sources#git-repositories) covers the `source:` block for Git repositories.

### With the CLI

At runtime, the same declarations are flags on `podium layer register` and `podium layer update`:

```bash
podium layer register --id team-finance \
  --repo git@github.com:acme/podium-finance.git --ref main \
  --group acme-finance --group acme-finance-leads

podium layer update --id team-finance --user security-lead@acme.com
```

`--group` and `--user` are repeatable. `podium layer update` patches only the fields supplied, and every other field keeps its prior value. The [CLI reference](../../reference/cli#layer-management) lists the full flag set.

### On a local deployment

On the [local](../modes/local) deployment mode, a layer directory declares its visibility in an optional `.layer-config` file. `podium sync` ignores the file. The declaration takes effect once a server serves the same directory.

---

## Debugging an effective view

`podium admin show-effective` reports the per-layer decision for any identity. It answers the question of why a given caller cannot see a given artifact.

```bash
podium admin show-effective \
  --group acme-engineering \
  --registry https://podium.acme.com \
  alice@acme.com
```

`--group` is repeatable and supplies the group claims to evaluate against.

When a caller reports a missing artifact, work through these checks in order:

1. Confirm the artifact's layer is registered and current with `podium layer list`.
2. Confirm the layer's visibility matches the caller's identity with `podium admin show-effective`.
3. Confirm the caller's token carries the expected `groups` claim. A claim that arrives as an opaque identifier needs `PODIUM_IDP_GROUP_MAPPING`.
4. Check the [audit log](../operations/observability#audit-log) for `visibility.denied` entries against that identity.

An artifact can also be present in the view yet absent from search results. `search_visibility: direct-only` in the artifact's frontmatter keeps it out of `search_artifacts`, while `load_artifact` still returns it to an entitled caller. That setting is an authoring choice rather than an access-control decision, and the [frontmatter reference](../../authoring/frontmatter-reference) documents it.

---

## Who can change a layer

Visibility decides who reads a layer. Layer write authorization decides who changes one. The layer write operations are `register`, `unregister`, `update`, `restore`, `reorder`, and `reingest`. The registry authorizes each one against the stored layer:

| Stored layer | Callers authorized to write it |
|:--|:--|
| Admin-defined | A caller holding the tenant `admin` role. |
| User-defined | The layer's stored owner, or a tenant admin. |

A caller authorized on neither rule is refused with `auth.forbidden`.

These rules govern a write against a layer that already exists, including a `register` request whose `id` names a stored layer. A registration whose `id` names no stored layer is authorized to a caller the admin rule admits, or to a caller who resolves a verified subject. In the second case, the registry stores the layer as a user-defined layer owned by that subject.

### Local-source rule

A second rule covers layers whose source is a filesystem path on the registry host. Only a tenant admin can perform these operations, whether the layer is admin-defined or user-defined:

- Register a layer whose source names a host path.
- Patch that path.
- Restore such a layer.
- Reingest such a layer.

Any other caller is refused with `auth.forbidden` carrying `details.constraint: "local_source"`, and the refusal names no path. A `git` source whose repository string resolves to the Git file transport names a host path and falls under the same rule. A `git` source that names a network endpoint does not. The rule exists because the registry process reads the path with its own rights rather than with the registrant's, so the path is an admin decision even when the layer belongs to the registrant.

### Operations without the admin role

| Operation | Without the `admin` role |
|:--|:--|
| Register a `git` layer on a network repository | Permitted for a caller who resolves a verified subject, when the registration asserts none of `owner`, `public`, `organization`, `groups`, and `users`. The registry resolves it to a user-defined layer owned by that subject. A caller who resolves no subject is refused with `auth.forbidden`. |
| Register a layer asserting an owner, `public`, `organization`, `groups`, or `users` | Refused with `auth.forbidden`, `details.constraint: "admin_only_fields"`. |
| Register a layer naming a host path, by `--local` or by a repository string on the Git file transport | Refused with `auth.forbidden`, `details.constraint: "local_source"`. |
| List layers | Permitted, narrowed to the layers that caller's identity admits. |
| Update, unregister, restore, reorder, or reingest a layer the caller owns | Permitted, except as the next row states. |
| Patch a layer's filesystem path, or restore or reingest a layer that names one | Refused with `auth.forbidden`, `details.constraint: "local_source"`, including on the caller's own layer. |
| Any write on an admin-defined layer, or on another caller's user-defined layer | Refused with `auth.forbidden`. |

[Edge cases](#edge-cases) describes which refusal a registration receives when it breaks more than one rule.

---

## Adjacent controls

Layer visibility decides who can read. The related concerns below are decided elsewhere.

| Concern | Where it lives |
|:--|:--|
| Who can publish to a layer | The layer's source. Branch protection and required reviewers on the Git ref, or filesystem permissions on a `local` path. Podium does not duplicate them. |
| Who can administer the registry | The tenant `admin` role, managed with `podium admin grant` and `podium admin revoke`. Instance-operator rights for tenant management are separate and seeded through `PODIUM_OPERATOR_ADMINS`. |
| How sensitive an artifact is | The `sensitivity:` frontmatter field. `search_artifacts` and `load_artifact` responses expose it, and [public mode](#when-visibility-is-not-enforced) refuses ingest of `medium` and `high`. It grants or withholds no visibility, and signature verification does not read it. Reviewer requirements based on it belong in the Git host's branch protection. |
| Whether a load requires a valid signature | `PODIUM_VERIFY_SIGNATURES` on each `podium-mcp` consumer, `always` by default, where every artifact the bridge loads requires a valid signature. A signing registry also verifies each stored signature before it serves the row. [Progressive adoption](../progressive-adoption) covers rolling it out. |

---

## When visibility is not enforced

The registry skips visibility enforcement in these cases:

| Case | Behavior |
|:--|:--|
| No identity provider | A registry that boots without `PODIUM_IDENTITY_PROVIDER` treats every caller as anonymous, and the evaluator admits every layer. Configure an identity provider to make visibility meaningful. See [Identity](../identity/). |
| Filesystem-source catalog | `podium sync` reading a directory has no registry process to enforce anything, so the evaluator returns `true` for every layer. The declarations stay in layer config, so the same catalog is portable to a server-backed deployment mode. |
| Public mode | [Public mode](../modes/single-node#public-mode) bypasses both authentication and the visibility model. It refuses ingest of `sensitivity: medium` and `sensitivity: high` artifacts. It records `caller.identity = "system:public"` in the [audit log](../operations/observability#audit-log), so the audit trail shows that anonymous access was intended. |

---

## Edge cases

### Layer writes without an identity provider

A registry with no identity provider, and a registry in public mode, authenticate no caller. No caller can hold the `admin` role on such a registry, and the registry admits every caller on the admin rule. The layer write rule, the local-source rule, and the admin-only-fields rule therefore admit every request.

### Refusal precedence for registrations

The registry evaluates the admin-only-fields rule after the other layer authorization rules. A registration that breaks more than one rule receives the refusal of the rule evaluated first:

- A registration whose `id` names another caller's user-defined layer is refused with `auth.forbidden` carrying no `details.constraint`.
- A registration that also falls under the local-source rule is refused with `details.constraint: "local_source"`.

### Web UI layer controls

The web UI reads the caller's own capabilities from the session posture read, `GET /v1/ui/session`. The response reports `layer_capabilities.manage_any_layer` and `email` for the requesting caller alone. The [HTTP API reference](../../reference/http-api#session-posture) documents the body.

- `manage_any_layer` covers a write on a layer the caller does not own, and every operation the local-source rule governs.
- The web UI renders a layer write control only where `manage_any_layer`, together with the target layer's class, stored owner, source type, and stored filesystem path, settles that the layer rules admit this caller on that operation.
- On a multi-tenant registry, the value reports the admin role in the tenant that the caller's organization selects. It is false for a caller whose organization selects no tenant.
- Such a caller's layer requests are refused with `401 auth.tenant_unknown` when the registry rejects that caller with that code on its other endpoints. Any other such caller lists no layers.
- The web UI still offers an authenticated caller of either kind the registration control, because the session posture reports its subject. The registry refuses that registration.

The value predicts a server decision and grants nothing. The layer endpoint's own refusal remains the authority.
