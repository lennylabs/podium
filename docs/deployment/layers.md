---
title: Layered composition
nav_order: 6
description: How a registry composes one catalog from several independent layers, in an explicit precedence order, and how to read the composed result.
---

# Layered composition

A catalog usually draws on several sources. An organization keeps shared defaults in one repository, a team keeps its own artifacts in another, and an individual keeps work in progress on their own machine. Layered composition merges those sources into one view per caller, in an order the deployment states explicitly.

This page explains how layers combine: precedence, collisions, the workspace overlay, and the no-inheritance rule. [Concepts → Layer](../getting-started/concepts#layer) defines the model. [Connecting layer sources](layer-sources) covers connecting Git repositories and local paths as layers and keeping them current.

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
| [Local](local) | `layer_order:` in `<registry-path>/.registry-config`, defaulting to alphabetical order by subdirectory name | None. Every layer composes. |
| [Single node](single-node) | `registry.layers:` in `~/.podium/registry.yaml`, plus layers registered at runtime | Applied when an identity provider is configured |
| [Clustered](clustered) | The tenant's layer config | Always applied |

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

[Edge cases](#edge-cases) covers an overlay artifact that carries an `extends` key and what `podium lint` reports for the overlay.

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

An earlier release stored rows from an `ARTIFACT.md` that declared `extends:`. The registry no longer serves those rows. [Upgrading from an earlier release](#upgrading-from-an-earlier-release) describes what a reader sees and how to recover.

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

[Local → Directory layout](local#directory-layout) covers the full directory layout.

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

A layer declared in the `registry:` `layers:` list is re-seeded from that file in list order at every restart. Change its position in `registry.yaml` to make a new order durable.

---

## Reading the composed result

`podium sync --dry-run` prints the artifact set the current identity would materialize, without writing anything. `podium sync --preview` prints the aggregate counts instead.

On a server-backed deployment, `podium admin show-effective [--group <g>]... <user-id>` reports the per-layer result for any identity. It answers why a given artifact did or did not appear. See [Access control → Debugging an effective view](access-control#debugging-an-effective-view).

---

## Edge cases

- **Overlay artifact with `extends`.** An overlay `ARTIFACT.md` that carries an `extends` key is served as authored, and the key has no effect.
- **Lint and the overlay.** `podium lint` reports an `ARTIFACT.md` that the no-inheritance rule refuses. `podium lint` contacts no registry, so it does not report a collision with an artifact that only a registry holds.
- **Reorder by a caller whose credential fails verification.** Such a caller is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either authorization arm is evaluated. On this operation, `auth.forbidden` therefore names a caller the registry verified and did not authorize. The other layer write operations answer such a caller `auth.forbidden` as before.

---

## Upgrading from an earlier release

These facts apply to a store that holds rows an earlier release ingested from an `ARTIFACT.md` that declares `extends:`.

### What readers see

- No read path serves such a row. `latest` resolves to the artifact's most recent other version.
- An ID whose retired row overlaid a lower layer's artifact serves the lower layer's copy when no other layer holds a served row of the ID.
- An artifact with no other version answers `registry.not_found`.
- A retired version's `delegates_to` and `mcpServers` references stay in impact analysis while another version of the artifact is served.

### Ingesting the ID again

A retired row is not a collision:

- When only one layer holds a served row of the ID, that layer ingests new versions of the artifact ID without `ingest.collision`.
- An ID with no served row is ingested by any layer without `ingest.collision`.
- The retired row still holds its `(id, version)`, so other bytes at that version are rejected with `ingest.immutable_violation`.

### Copies admitted from several layers

An earlier release also admitted a plain copy of the ID from a further layer while an overlay row was stored. When served rows of the ID remain in two or more layers, `latest` serves the most recently ingested of them. Every later ingest of the ID from those layers is rejected with `ingest.collision`, including a re-ingest of unchanged bytes.

To recover, keep one of those layers:

1. Remove the declaration of each layer to drop from the registry configuration. A declared layer is re-seeded at each start.
2. Unregister all but one of those layers with `podium layer unregister`. Unregistering soft-deletes every row of each unregistered layer, so its other artifacts stop serving too.
3. Wait until the recovery window ends and the purge removes the soft-deleted rows.
4. Register a removed source again, without the artifact, and declare it again if it was declared.

Before the purge, the soft-deleted rows still hold their artifacts' versions. A registration under any layer ID then serves none of the source's artifacts at an unchanged version, even when the ingest reports them accepted, and it rejects a skill with no declared version as `auto_version_duplicate_content`.

### Clients that loaded a retired version

Retiring an artifact's newest rows lowers the version `latest` resolves to. A `podium-mcp` that loaded a retired version as `latest` refuses the older one with `materialize.stale_resolution`.

1. Stop every MCP server that uses the cache directory.
2. Run `podium cache reset-revisions <id>`.
3. For a `podium-mcp` in `offline-first` or `offline-only` that cached a merged artifact, also run `podium cache prune --days 0`. That command leaves the revision marks in place.
