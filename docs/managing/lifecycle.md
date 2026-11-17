---
title: Changing and retiring artifacts
nav_order: 3
description: Change an artifact without breaking its dependents, deprecate one with a replacement, work within freeze windows, reingest, reorder, unregister, and restore layers, and check domain health.
---

# Changing and retiring artifacts

An artifact changes by publishing a new version through its layer's source. This page covers how versions resolve, how to find what depends on an artifact before changing it, how to deprecate an artifact, how freeze windows and break-glass work, how to manage the layers that hold artifacts, and how to check the health of a domain.

## Who can do this

| Task | Access |
|:--|:--|
| Publish a new version or deprecate an artifact | An author with write access to the layer's source repository. The next ingest of the layer publishes the change. |
| List an artifact's dependents with `podium impact` | Any caller. The result holds only edges whose artifacts the caller can see. |
| Run `podium domain analyze` | Any caller. The report covers the artifacts the caller can see. |
| Reingest, reorder, unregister, or restore a layer | A tenant admin for an admin-defined layer, and the owner or a tenant admin for a user-defined layer. A layer whose source is a filesystem path on the registry host needs the tenant `admin` role to reingest or restore. |
| Declare freeze windows | Operator, in `registry.yaml`. |
| Reingest during a freeze window with break-glass | A caller with reingest rights on the layer, naming two distinct approvers. |

[Who can change a layer](../deployment/content/access-control#who-can-change-a-layer) gives the full layer write rules.

---

## Versions and pinning

Each artifact version is semver. The author sets it in the manifest's `version` field, or for a skill in `SKILL.md` `metadata.version`, and a skill that declares no usable version receives `0.0.0-auto.<N>` from the registry. Once `(artifact_id, version)` is ingested, it is bit-for-bit immutable, and an ingest that changes its content at the same version is rejected with `ingest.immutable_violation`. Bump the version with every content change.

A reference in `delegates_to:` or `mcpServers:` names its target in one of these forms:

| Form | Resolves to |
|:--|:--|
| `<id>` | `latest`. |
| `<id>@1.2.3` | That exact version. |
| `<id>@1.2.x` or `<id>@1.x` | The highest version in the range. A range never selects a registry-assigned version. |
| `<id>@sha256:<hash>` | The version with that content hash. |
| `<id>@0.0.0-auto.<N>` | That registry-assigned version. |

The `version` argument of `load_artifact` and `podium artifact show --version` take the same forms after the `@`, and `latest` applies when the argument is absent.

`latest` is the most recently ingested non-deprecated version visible to the caller at resolution time. It orders by ingest time, so a backported fix ingested after a newer release line becomes `latest`. A host that passes a `session_id` gets the same `latest` for an ID on every load within the session. [Versioning](../getting-started/how-it-works#versioning) summarizes the model, and [Skill versions](../authoring/frontmatter-reference#skill-versions) covers registry-assigned versions.

## Check dependents before a change

Before changing or retiring an artifact, list the artifacts that reference it:

- `podium impact <artifact-id> --registry <url>` prints the artifacts that depend on it.
- `GET /v1/dependents?id=<artifact-id>` returns the same edges under `edges`, each with `from`, `to`, and `kind`. An edge's `kind` is `delegates_to` or `mcpServers`.
- The SDKs expose the query as `dependents_of()`.

Query by the canonical ID without an `@version` suffix, because a query that carries one matches no edge. An edge appears only when the caller can see both of its artifacts, and a query against an artifact the caller cannot see answers with an empty list. An empty result therefore does not prove that nothing depends on the artifact. Run the check as an identity whose view covers every layer that could reference it. [`dependents`](../reference/http-api#dependents) gives the response and its rules.

A dependent that pins an exact version or a content hash keeps that version after a change. A dependent that names `<id>` or a range follows the new version once it is ingested.

## Deprecate an artifact

Deprecation marks a version. Versions are immutable, so deprecate an artifact by publishing a new version that sets the flag:

1. Bump the version.
2. Set `deprecated: true`, and set `replaced_by` to the canonical ID of the artifact that supersedes it. A skill without an `ARTIFACT.md` sets them as `metadata.deprecated: "true"` and `metadata.replaced_by` in `SKILL.md`, in the encoding that [Podium fields in SKILL.md metadata](../authoring/frontmatter-reference#podium-fields-in-skillmd-metadata) defines.
3. Merge the change, and let the next ingest publish it or run `podium layer reingest`.

```yaml
# Changed fields in the ARTIFACT.md frontmatter of the new version
version: 2.1.0
deprecated: true
replaced_by: finance/ap/pay-invoice
```

A consumer sees the deprecation as follows:

- A `load_artifact` response for a deprecated version carries `deprecated: true`, `replaced_by`, and `deprecation_warning`, such as `artifact is deprecated; replaced_by finance/ap/pay-invoice`. Every load of an artifact whose manifest sets `replaced_by` returns it, whether or not the version is deprecated.
- Default `search_artifacts` results leave out an artifact whose resolved version is deprecated.
- The registry records `artifact.deprecated` in the audit log, emits it on the change-event stream, and delivers it to webhook receivers that subscribe to it. `podium sync --watch` re-syncs on it.
- A consumer decides what to do with the warning. [Handling artifact responses](../consuming/handling-artifact-responses) describes the recommended handling.

> [!WARNING]
> `latest` skips deprecated versions. When an artifact has earlier versions that are not deprecated, a load of `latest` and a search resolve to the newest of those earlier versions, which carries no deprecation warning. Only when every version of the artifact is deprecated does `latest` resolve to the newest version and return the warning. After the ingest, check which version `latest` serves with `podium artifact show <id> --json`, whose `version` member names it.

A store retention sweep purges a deprecated version `PODIUM_DEPRECATED_RETENTION_DAYS` days after it was deprecated, 90 by default. [Store retention](../reference/configuration#store-retention) lists the setting.

## Freeze windows and break-glass

A freeze window blocks ingest during a period such as a release cut or a year-end close. The operator declares each window under `registry.freeze_windows` in `registry.yaml`, with a `name`, a `start`, and an `end` as RFC 3339 timestamps. The registry reads the list at startup, so a new window takes effect after a restart, and every window applies to every layer of every tenant.

While a window is active, the registry refuses a manual `podium layer reingest`, each tick of `podium layer watch`, and each Git webhook delivery with `ingest.frozen`. A webhook delivery cannot carry an override, so a webhook-driven layer catches up with a manual reingest after the window ends or with a break-glass reingest during it.

A break-glass override runs one manual reingest during an active window. Run `podium layer reingest <layer-id> --break-glass --justification <text> --approver <id> --approver <id>` with a non-empty justification and two distinct approver identities. The caller who runs the command does not count as an approver. The registry records a `freeze.break_glass` audit event that names the window, the caller, the approvers, and the justification. Review those events after each window.

[Configuring freeze windows](../deployment/progressive-adoption#configuring-freeze-windows) and [Overriding a freeze](../deployment/progressive-adoption#overriding-a-freeze) give the keys and the override rules.

## Manage layers

A layer is the unit the registry ingests, orders, and removes. These commands take `--registry <url>`:

| Command | Effect |
|:--|:--|
| `podium layer reingest <id>` | Pulls a fresh snapshot of the layer's source and ingests it. Standard error lists each conflicted or rejected artifact with its error code, and the command exits 1 when the cycle dropped an artifact. |
| `podium layer reorder <id> [<id> ...]` | Sets precedence, lowest to highest. A layer declared in `registry.yaml` returns to its configured position at the next restart, so change its position in the file to make the order durable. |
| `podium layer unregister <id>` | Soft-deletes the layer and the artifacts ingested from it. |
| `podium layer list --deleted` | Lists the unregistered layers that are still restorable. |
| `podium layer restore <id>` | Restores an unregistered layer and its artifacts within the recovery window, `PODIUM_LAYER_RECOVERY_DAYS` days, 30 by default. A sweep purges the layer after the window. |

The reingest response reports lint diagnostics only as a count. Run `podium lint` against the source to read them. Each change to an admin-defined layer or to the layer order records `layer.config_changed`, and each change to a personal layer records `layer.user_registered`.

[Layer management](../reference/cli#layer-management) in the CLI reference gives every flag, [Keeping layers current](../deployment/content/layer-sources#keeping-layers-current) covers webhooks and polling, and [Changing the order](../deployment/content/layers#changing-the-order) covers precedence.

## Check domain health

`podium domain analyze [<path>] --registry <url>` reports on a domain subtree: the sparsity of each node, pass-through chains, and candidates for a split (many artifacts with a high spread of tags) or a fold (few artifacts). An empty path analyzes the root. The report covers the artifacts the caller can see, and it needs a registry server.

Act on the report in the layer's source. A split moves artifacts into subdomains, which changes their canonical IDs, so check their dependents first. A fold merges a sparse subdomain into its parent. `DOMAIN.md` descriptions, `keywords`, and `featured` lists change how `load_domain` presents a domain without moving artifacts. [Domains](../authoring/domains) covers the `DOMAIN.md` fields and the discovery settings.
