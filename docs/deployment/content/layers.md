---
title: Layered composition
nav_order: 2
description: How a registry composes one catalog from several independent layers, in an explicit precedence order, and how to read the composed result.
---

# Layered composition

A catalog usually draws on several sources. An organization keeps shared defaults in one repository, a team keeps its own artifacts in another, and an individual keeps work in progress on their own machine. Layered composition merges those sources into one view per caller, in an order the deployment states explicitly.

This page explains how layers combine: precedence, collisions, the workspace overlay, and the no-inheritance rule. [Concepts → Layer](../../getting-started/concepts#layer) defines the model. [Connecting layer sources](layer-sources) covers connecting Git repositories and local paths as layers and keeping them current.

---

## Precedence

Layers form an ordered list. From lowest precedence to highest, the list holds:

1. **Admin-defined layers**, in the order the deployment's layer list gives them.
2. **User-defined layers**, which are personal layers an authenticated user registers for themselves. The default cap is 3 per identity, and it is configurable per tenant.
3. **The workspace local overlay** at `<workspace>/.podium/overlay/`, which is always highest.

Layer order is declared and never inferred. There is no fixed `org / team / user` hierarchy, and the ordering is whatever the layer list says. The same layer list and the same identity always produce the same view.

Every deployment mode applies the same rules. The deployment modes differ in where the ordered list lives and whether visibility filters it.

| Deployment mode | Where the layer list lives | Visibility filtering |
|:--|:--|:--|
| [Local](../modes/local) | `layer_order:` in `<registry-path>/.registry-config`, defaulting to alphabetical order by subdirectory name | None. Every layer composes. |
| [Single node](../modes/single-node) | `registry.layers:` in `~/.podium/registry.yaml`, plus layers registered at runtime | Applied when an identity provider is configured |
| [Clustered](../modes/clustered) | The tenant's layer config | Always applied |

---

## Collisions

Two registry-side layers that hold the same artifact ID collide, and Podium rejects the collision rather than resolving it silently.

- A server rejects the second contribution at ingest with `ingest.collision`. The rejection names the artifact and directs the author to rename it or to remove the other copy.
- `podium lint` reports the same collision on a local catalog.
- `podium sync` against a local catalog drops the higher-precedence copy, names it on standard error, materializes the rest, and exits 1. It therefore materializes the copy that a server started on the same directory serves.

Replacing an artifact that another layer contributes takes two explicit steps. First remove the lower-precedence copy or rename the higher-precedence one, and then ingest the replacement.

The workspace local overlay is the exception. It is merged on the client and replaces a registry-side artifact that carries the same ID, as the next section describes.

---

## Workspace local overlay

The workspace local overlay (`<workspace>/.podium/overlay/`) holds a developer's own artifacts on top of the registry's layers. An overlay artifact replaces the registry-side artifact with the same ID in `podium-mcp`, in `podium sync`, and in the SDKs, and no consumer reports the replacement.

- The consumer applies no ingest rule to an overlay artifact and serves it as authored.
- The overlay is per-developer and is never ingested or shared, so a replacement affects only the developer who wrote it.
- Copying an overlay artifact into a shared layer subjects it to the collision rule and to the [no-inheritance rule](#inheritance).

[Edge cases](#edge-cases) covers an overlay artifact that carries an `extends` key or a removed type and what `podium lint` reports for the overlay.

---

## Inheritance

No artifact inherits frontmatter, prose, or resources from another artifact.

Ingest rejects an `ARTIFACT.md` whose frontmatter carries a top-level `extends` key, whatever its value:

- The refusal is `ingest.invalid_artifact` with `details.constraint: "extends_removed"`.
- The refusal runs before lint, so it reports the artifact whatever lint rule the artifact also fails.
- A filesystem-source `podium sync` drops such an artifact and exits 1, and `podium lint` reports it as an error.
- An `extends` key in a skill's `SKILL.md` is an ordinary key and is not refused.

A refused artifact is not stored, which has two consequences:

- Another artifact of the same reingest whose prose links to the refused artifact fails `lint.prose_reference`.
- A refused artifact collides with no other artifact. The other copy of its ID is materialized or ingested, and no `ingest.collision` line is printed for it.

Release 0.6.0 removed `extends:`. A store that an earlier release wrote can still hold rows ingested from an `ARTIFACT.md` that declared it, and the registry no longer serves those rows. [Upgrade notes](../operations/upgrade-notes#what-readers-see) describes what a reader sees and how to recover.

---

## Removed types

`command` and `context` are removed artifact types. Ingest rejects an `ARTIFACT.md` whose `type` is either of them:

- The refusal is `ingest.invalid_artifact` with `details.constraint: "type_removed"`.
- The refusal runs before lint and after the no-inheritance check, so an `ARTIFACT.md` that declares both `extends:` and a removed type reports `extends_removed`.
- A filesystem-source `podium sync` drops such an artifact and exits 1, and `podium lint` reports it as an error.

A refused artifact is not stored, with the same consequences as an artifact the no-inheritance rule refuses.

Release 0.7.0 removed both types. A store that an earlier release wrote can still hold rows of either type, and the registry no longer serves those rows. `latest` resolves to the newest other served version of the ID, and an ID with no other served row answers `registry.not_found`. [Artifact types](../../authoring/artifact-types#removed-types) names the replacements, and [Upgrade notes](../operations/upgrade-notes#command-and-context-removal) describes the conversion and the recovery steps.

---

## Unparseable manifests

Ingest refuses an artifact whose `ARTIFACT.md` or `SKILL.md` frontmatter does not parse. Frontmatter does not parse when the file has none, when it is not valid YAML, when its root is not a mapping, or when a field holds a value of a kind its definition does not allow, such as a `SKILL.md` `name` or `description` that is a mapping. A sequence or a mapping under a `SKILL.md` `metadata` key does not make the frontmatter unparseable, as [Values Podium ignores or keeps](../../authoring/frontmatter-reference#values-podium-ignores-or-keeps) describes. An artifact whose `ARTIFACT.md` declares `type: skill` and whose directory holds no `SKILL.md` is refused under the same rule, and its reason names the missing `SKILL.md`.

- The refusal is `ingest.invalid_artifact` with `details.constraint: "manifest_unparseable"`. Its reason names the file and the parse error.
- The refusal runs before every other per-artifact rule, including the no-inheritance and removed-type checks, because neither can be evaluated without the parsed frontmatter.
- The refusal covers that artifact alone. Ingest stores the other artifacts of the layer, `podium lint` lints them, and a filesystem-source `podium sync` writes them.
- A filesystem-source `podium sync` drops such an artifact before the collision rule, names it on standard error, and exits 1. `podium lint` lists it among its refused artifacts and exits 1.
- By default, a filesystem-source `podium sync` keeps the files and lock entry an earlier run wrote for the refused artifact and notes that on its standard-error line, which matches a server that keeps serving the earlier version. It keeps them only when the earlier run used the same harness, the artifact passes the run's scope and toggles, and no other artifact of the run takes them over, and otherwise removes them. An artifact takes them over when it writes a file the kept output holds whole, or a configuration-merge entry under the same key, such as an `mcpServers` server of the same name. Another artifact's entry in a shared file such as `.mcp.json` does not end the keep. `--on-unparseable remove`, `PODIUM_SYNC_ON_UNPARSEABLE`, or `defaults.on_unparseable` in `sync.yaml` removes them instead, as [Previous output of an unparseable artifact](../../reference/cli#previous-output-of-an-unparseable-artifact) describes.
- A row already stored is not parsed again under this rule.

A refused artifact is not stored, with the same consequences as an artifact the no-inheritance rule refuses.

A directory that cannot hold an artifact at all still fails the whole layer. A manifest at the layer root has no canonical ID, so a layer whose root directory holds an `ARTIFACT.md` or a `SKILL.md` fails to ingest, and `podium lint` and `podium sync` fail on it.

---

## Composing from a directory

On the Local deployment mode, each subdirectory of the registry path is a `local`-source layer, and the layer ID defaults to the subdirectory name. `.registry-config` states the order:

```yaml
# ~/podium-artifacts/.registry-config
multi_layer: true        # required; opts the directory into filesystem-registry mode
layer_order:             # optional; lowest-precedence first
  - team-shared
  - personal
```

[Local → Directory layout](../modes/local#directory-layout) covers the full directory layout.

The same directory serves a single-node deployment unchanged. `podium serve --standalone --layer-path ~/podium-artifacts/` ingests each subdirectory as a layer and keeps the same order.

---

## Registering layers against a server

A server-backed deployment registers each layer with its own source. Declare layers in `registry.yaml`, or register them at runtime with `podium layer register`. [Connecting layer sources](layer-sources) covers each task:

- [Git repositories](layer-sources#git-repositories), declared or registered at runtime.
- [Private repositories](layer-sources#private-repositories) and their credentials.
- [Keeping layers current](layer-sources#keeping-layers-current) with webhooks, polling, or a manual reingest.
- [Local paths](layer-sources#local-paths) on the registry host.
- [Agent Skills repositories](layer-sources#agent-skills-repositories) registered without an `ARTIFACT.md` in any skill.

Authoring rights stay with the source. Whoever can merge to a layer's tracked Git ref, or write to a `local` layer's path, publishes there. [Registration rights](layer-sources#registration-rights) states who may register a layer and who may name a host path.

---

## Changing the order

`podium layer reorder <id> [<id> ...]` re-sequences the named layers. The argument order is precedence, lowest to highest:

```bash
podium layer reorder alice-scratch alice-personal
```

- A caller reorders their own user-defined layers without special rights.
- An argument list that names an admin-defined layer requires the tenant `admin` role, and the registry answers `auth.forbidden` otherwise.
- A deployment with no identity provider has no authenticated callers, so the local operator reorders any layer.
- A deployment in public mode reorders a layer only for a request that carries `PODIUM_OPERATOR_TOKEN`, as [Public mode](../modes/single-node#public-mode) describes.

A layer declared in the `registry.layers` list of `registry.yaml` is re-seeded from that file in list order at every restart, so a reorder of it reverts. Change its position in `registry.yaml` to make a new order durable.

---

## Reading the composed result

`podium sync --dry-run` prints the artifact set the current identity would materialize, without writing anything. `podium sync --preview` prints the aggregate counts instead.

On a server-backed deployment, `podium admin show-effective [--group <g>]... <user-id>` reports the per-layer result for any identity. It answers why a given artifact did or did not appear. See [Access control → Debugging an effective view](access-control#debugging-an-effective-view).

---

## Edge cases

- **Overlay artifact with `extends`.** An overlay `ARTIFACT.md` that carries an `extends` key is served as authored, and the key has no effect.
- **Overlay artifact of a removed type.** An overlay `ARTIFACT.md` whose `type` is `command` or `context` is served as authored. The `none` adapter writes the canonical layout, the `claude-code` adapter writes it and its bundled resources under `.claude/podium/<artifact-id>/`, and every other adapter writes nothing for it.
- **Lint and the overlay.** `podium lint` reports an artifact that the unparseable-manifest rule, the no-inheritance rule, or the removed-type rule refuses. `podium lint` contacts no registry, so it does not report a collision with an artifact that only a registry holds.
- **Reorder by a caller whose credential fails verification.** Such a caller is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either authorization arm is evaluated. On this operation, `auth.forbidden` therefore names a caller the registry verified and did not authorize. The other layer write operations answer such a caller `auth.forbidden`.

---
