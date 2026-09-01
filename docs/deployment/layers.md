---
title: Layered composition
nav_order: 5
description: Compose one catalog from several independent sources with deterministic merge and explicit precedence. Registering layers, ordering them, and reading the composed result.
---

# Layered composition

A catalog usually draws on several sources. An org keeps shared defaults in one repo, a team keeps its own artifacts in another, and an individual keeps work in progress on their own machine. Layered composition merges those sources into one view per caller, in an order the deployment states explicitly.

[Concepts → Layer](../getting-started/concepts#layer) defines the model. This page covers the operational task: putting layers in place, ordering them, and confirming what a caller sees.

---

## Precedence

Layers form an ordered list. Two layers holding the same artifact ID is a collision that Podium rejects rather than resolving silently: a server rejects the second contribution at ingest with `ingest.collision`, and `podium lint` reports the same collision on a local catalog. `extends:` is the sanctioned exception. It lets a higher-precedence artifact inherit and refine a lower one instead of replacing it, and it is documented in [Authoring → extends](../authoring/extends). The workspace local overlay is merged on the client and replaces a base artifact that carries the same ID.

The order is, from lowest precedence to highest:

1. **Admin-defined layers**, in the order the deployment's layer list gives them.
2. **User-defined layers**, personal layers an authenticated user registers for themselves. The default cap is 3 per identity and it is configurable per tenant.
3. **The workspace local overlay** at `<workspace>/.podium/overlay/`, always highest.

Every tier applies the same rules. What differs is where the ordered list lives and whether visibility filters it.

| Tier | Where the layer list lives | Visibility filtering |
|:--|:--|:--|
| [Local](local) | `layer_order:` in `<registry-path>/.registry-config`, defaulting to alphabetical order by subdirectory name | None. Every layer composes. |
| [Single node](single-node) | `registry.layers:` in `~/.podium/registry.yaml`, plus layers registered at runtime | Applied when an identity provider is configured |
| [Clustered](clustered) | The tenant's layer config | Always applied |

---

## Composing from a directory

On the local tier, each subdirectory of the registry path is a `local`-source layer, and the layer ID defaults to the subdirectory name. `.registry-config` states the order:

```yaml
# ~/podium-artifacts/.registry-config
multi_layer: true        # required; opts the directory into filesystem-registry mode
layer_order:             # optional; lowest-precedence first
  - team-shared
  - personal
```

[Local](local#directory-layout) covers the full directory layout.

The same directory serves a single-node deployment unchanged. `podium serve --standalone --layer-path ~/podium-artifacts/` ingests each subdirectory as a layer and keeps the same order.

---

## Registering layers against a server

A server-backed deployment registers each layer with its own source. Declare them in `registry.yaml`, under the top-level `registry:` mapping that every server-side key nests below:

```yaml
registry:
  layers:
    - id: org-defaults
      source:
        git:
          repo: git@github.com:acme/podium-org-defaults.git
          ref: main
          root: artifacts/
      visibility:
        organization: true

    - id: team-finance
      source:
        git:
          repo: git@github.com:acme/podium-finance.git
          ref: main
      visibility:
        groups: [acme-finance]
```

A document that starts at `layers:` parses to an empty config and the registry ignores it without reporting an error.

Or register them at runtime:

```bash
# A git-source layer. The registry returns a webhook URL and HMAC secret
# to configure on the source repo.
podium layer register --id org-defaults \
  --repo git@github.com:acme/podium-org-defaults.git --ref main \
  --organization

# A local-source layer, read from a path the registry process can see.
podium layer register --id team-artifacts \
  --local /var/podium/team-artifacts/ --group acme-engineering

# A personal layer. The registry derives the owner from the authenticated
# caller and gives the layer implicit users:[<owner>] visibility.
podium layer register --id alice-personal \
  --local /home/alice/podium-personal/ --user-defined
```

An authenticated caller without the tenant `admin` role registers a user-defined layer whether or not `--user-defined` is passed. The registry resolves the class from the caller's identity.

`podium layer list` prints the registered layers the caller can see, and their current state. A caller holding the tenant `admin` role, and every caller on a registry that authenticates none, sees every layer in the tenant. Any other authenticated caller sees the layers that caller's identity admits, including that caller's own user-defined layers. A caller the registry resolves as anonymous sees none, and a caller whose credential fails verification is refused on the terms the [HTTP API reference](../reference/http-api#list-layers) states. Whether presenting no credential is itself a verification failure is the configured identity provider's rule. The visibility flags are covered in [Access control](access-control), and the built-in source types are covered in [Server-side integrations](integrations#layer-sources).

---

## Changing the order

`podium layer reorder <id> [<id> ...]` re-sequences the named layers. The argument order is precedence, lowest to highest:

```bash
podium layer reorder alice-scratch alice-personal
```

A caller reorders their own user-defined layers without special rights. An argument list that names an admin-defined layer requires the tenant `admin` role, and the registry answers `auth.forbidden` otherwise. A caller whose credential fails verification under the configured identity provider's rule is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either arm is evaluated, so on this operation `auth.forbidden` names a caller the registry verified and did not authorize; the other layer write operations answer such a caller `auth.forbidden` as before. A deployment with no identity provider has no authenticated callers, so the local operator reorders any layer.

A layer declared in the `registry:` `layers:` list is re-seeded from that file in list order at every restart. Change its position in `registry.yaml` to make a new order durable.

---

## Keeping layers current

Each layer refreshes from its source independently.

| Mechanism | When to use it |
|:--|:--|
| Git webhook | A `git`-source layer whose host can reach the registry. The registry ingests on each merge to the tracked ref. Register the webhook URL that `podium layer register` returned. |
| `podium layer reingest <id>` | A manual or scheduled pull. Covers offline mirrors, internal Git that cannot reach the registry, and any host without a public ingress. |
| `podium layer watch --id <id>` | A polling loop against the layer's source at an interval set with `--interval` (default 1m). Works for `local` sources and for `git` sources with no webhook. |

`podium layer update --id <id>` patches a registered layer's mutable fields, including the tracked ref, the source path, and the visibility. Only the flags supplied are applied.

`podium layer unregister <id>` removes a layer, and `podium layer restore <id>` recovers one that was unregistered inside the recovery window. `podium layer list --deleted` shows what is still recoverable.

The full flag set for each command is in the [CLI reference](../reference/cli#layer-management).

---

## Reading the composed result

`podium sync --dry-run` prints the artifact set the current identity would materialize, without writing anything. `podium sync --preview` prints the aggregate counts instead.

On a server-backed deployment, `podium admin show-effective [--group <g>]... <user-id>` surfaces the per-layer result for any identity, which answers questions about why a given artifact did or did not appear. See [Access control](access-control#debugging-an-effective-view).

---

## Merge behavior worth knowing

- **Same-ID collisions across layers are rejected rather than shadowed.** A server rejects the second contribution at ingest with `ingest.collision` unless the higher-precedence artifact declares `extends:` against that ID. `podium lint` reports the same collision on a local catalog, and `podium sync` materializing one keeps the highest-precedence copy. The same layer list and the same identity always produce the same view.
- **A hidden parent still merges.** When a visible artifact declares `extends:` against an artifact in a layer the caller cannot see, the registry resolves the parent server-side and returns the merged result. The caller never sees the parent directly.
- **Layer order is declared, never inferred.** There is no fixed `org / team / user` hierarchy. The ordering is whatever the layer list says.
- **Authoring rights are separate.** Whoever can merge to a layer's tracked Git ref publishes there, and whoever can write to a `local`-source layer's path publishes there. Branch protection and required reviewers stay in the Git host.
