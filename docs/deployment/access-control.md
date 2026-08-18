---
title: Access control
nav_order: 6
description: "Declare who can see each layer: public, organization-wide, scoped to OIDC groups, or scoped to named users. Covers the enforcement boundary, the deployment defaults, and how to debug an effective view."
---

# Access control

Access control in Podium is declared per layer. A layer states who can see it, the registry evaluates that declaration against the caller's identity on every call, and the caller's effective view is composed from the layers that matched.

[Concepts → Visibility](../getting-started/concepts#visibility) defines the model. This page covers the operational task: setting visibility on a layer, understanding what the deployment enforces, and finding out why a caller sees what they see.

---

## The visibility fields

| Field | Who matches |
|:--|:--|
| `public: true` | Anyone, including unauthenticated callers. |
| `organization: true` | Any authenticated user in the tenant org. |
| `groups: [<oidc-group>, ...]` | Members of the listed OIDC groups. |
| `users: [<user-id>, ...]` | The listed identifiers, matched by OIDC subject or email. |

Multiple fields combine as a union. A layer with `groups: [acme-finance]` and `users: [security-lead@acme.com]` is visible to every member of `acme-finance` and to that one additional identity.

---

## Declaring visibility

In `registry.yaml`, each layer carries its own `visibility:` block. Every server-side key nests under the top-level `registry:` mapping, and a document that starts at `layers:` parses to an empty config that the registry ignores without reporting an error:

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

At runtime, the same declarations are flags on `podium layer register` and `podium layer update`:

```bash
podium layer register --id team-finance \
  --repo git@github.com:acme/podium-finance.git --ref main \
  --group acme-finance --group acme-finance-leads

podium layer update --id team-finance --user security-lead@acme.com
```

`--group` and `--user` are repeatable. `podium layer update` patches only the fields supplied, so every other field keeps its prior value. The [CLI reference](../reference/cli#layer-management) has the full flag set.

On the [local](local) tier, a layer directory declares its visibility in an optional `.layer-config` file. `podium sync` ignores it, and it takes effect once a server serves the same directory.

---

## What enforces it

Visibility is enforced at the registry, on every call. The `visibility.denied` outcome mirrors a not-found result so a rejected call does not reveal that a hidden artifact exists, and the audit log records the denial. The `podium_visibility_denied_total` metric counts them.

Enforcement is bypassed in these cases:

- **No identity provider.** A registry that boots without `PODIUM_IDENTITY_PROVIDER` treats every caller as anonymous, and the evaluator admits every layer. Configure an identity provider to make visibility meaningful. See [Server-side integrations](integrations#identity).
- **A filesystem-source catalog.** `podium sync` reading a directory has no registry process to enforce anything, so the evaluator short-circuits to `true` for every layer. Declarations stay in layer config so the same catalog is portable to a server-backed tier.
- **Public mode.** [Public mode](single-node#public-mode) bypasses both authentication and the visibility model. It refuses ingest of `sensitivity: medium` and `sensitivity: high` artifacts and records `caller.identity = "system:public"` in the audit log, so the audit trail shows that anonymous access was intended.

---

## Deployment defaults

`PODIUM_DEFAULT_LAYER_VISIBILITY` sets the visibility an admin-defined layer takes when it registers without a `visibility:` block. It accepts `public`, `organization`, or `private`. Without an explicit setting, the value follows whether identity is configured:

| Registry state | Default for an admin-defined layer with no declaration |
|:--|:--|
| No identity provider | `public` |
| An identity provider is configured | `private`, meaning no visibility filters, so only an explicit grant reaches it |

The flip exists so that turning identity on does not leave admin-defined layers open by accident.

A user-defined layer is a separate case. Its visibility is implicitly `users: [<registrant>]`, derived from the authenticated caller at registration, and it cannot be widened. The deployment default does not apply to it.

---

## Where group membership comes from

Layer visibility references OIDC group names. They reach the registry through either of these paths:

- **The OIDC `groups` claim.** The token carries group membership and the registry reads it directly. IdPs that emit group identifiers rather than names need `PODIUM_IDP_GROUP_MAPPING` to translate them, which the [OIDC cookbooks](oidc/) cover per IdP.
- **SCIM 2.0 push.** The IdP pushes membership to the registry. SCIM is available on the [clustered](clustered) tier and is recommended once group-based visibility gates artifacts that matter.

---

## Debugging an effective view

`podium admin show-effective` surfaces the per-layer decision for any identity, which is the direct answer to "why can this person not see that artifact":

```bash
podium admin show-effective \
  --group acme-engineering \
  --registry https://podium.acme.com \
  alice@acme.com
```

`--group` is repeatable and supplies the group claims to evaluate against.

When a caller reports a missing artifact, work through these in order:

1. Confirm the artifact's layer is registered and current with `podium layer list`.
2. Confirm the layer's visibility matches the caller's identity with `podium admin show-effective`.
3. Confirm the caller's token carries the expected `groups` claim. A claim that arrives as an opaque identifier needs `PODIUM_IDP_GROUP_MAPPING`.
4. Check the audit log for `visibility.denied` entries against that identity.

An artifact can also be present in the view yet absent from search results. `search_visibility: direct-only` in the artifact's frontmatter keeps it out of `search_artifacts` while `load_artifact` still returns it to an entitled caller. That is an authoring choice rather than an access-control decision, and [Authoring → frontmatter reference](../authoring/frontmatter-reference) documents it.

---

## Adjacent controls

Layer visibility answers who can read. The related concerns below are decided elsewhere.

| Concern | Where it lives |
|:--|:--|
| Who can publish to a layer | The layer's source. Branch protection and required reviewers on the Git ref, or filesystem permissions on a `local` path. Podium does not duplicate them. |
| Who can administer the registry | The tenant `admin` role, managed with `podium admin grant` and `podium admin revoke`. Instance-operator rights for tenant management are separate and seeded through `PODIUM_OPERATOR_ADMINS`. |
| How sensitive an artifact is | The `sensitivity:` frontmatter field. `PODIUM_VERIFY_SIGNATURES` reads it to decide which artifacts require a valid signature at materialization. [Progressive adoption](progressive-adoption) covers rolling it out. |
