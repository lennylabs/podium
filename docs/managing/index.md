---
title: Managing the catalog
nav_order: 4
description: Look after a Podium catalog once it is in use. Find overlapping artifacts, read how the catalog is used, change and retire artifacts, and keep descriptions and packages within their limits.
---

# Managing the catalog

This section covers the upkeep of a catalog after its artifacts are written and delivered. It is for team leads, platform owners, and tenant admins who look after a catalog, and for authors who check their own artifacts before and after they merge.

The pages describe tasks. Flags, response fields, and error codes live in [Reference](../reference/), and configuration lives in [Deployment](../deployment/). Each page links to the entry it relies on.

## Who does what

Catalog tasks need one of the following levels of access. Each page opens with a table that names the access each of its tasks needs.

| Access | Who holds it | Examples |
|:--|:--|:--|
| Any user | Any caller the registry admits, and any author with a checkout of a layer's source repository. | Run `podium lint` on a checkout, read the overlap report, list an artifact's dependents, publish a new version through the layer's source, and manage the caller's own personal layers. |
| Per-tenant `admin` role | An identity that holds the role, which `podium admin grant` assigns. | Reingest, reorder, unregister, and restore admin-defined layers, recompute semantic overlap records, register webhook receivers, and erase a user. |
| Operator | The person who runs the registry process and its deployment. | Set the environment variables and `registry.yaml` keys for the semantic overlap check, freeze windows, retention, and audit destinations, and grant access to the systems that hold the audit records. |

A registry started with no identity provider, or in public mode, authenticates no caller. A registry with no identity provider admits the `admin` operations to every caller. A registry in public mode admits the layer operations, the semantic overlap recomputation, and erasure only from the operator who holds `PODIUM_OPERATOR_TOKEN`. [Access control](../deployment/content/access-control#layer-writes-without-an-identity-provider) describes that case.

A [local](../deployment/modes/local) deployment runs no registry server. On a local catalog, `podium lint` and the listing budget check of `podium sync` apply, and the tasks that read a registry, such as the overlap report, dependents, audit records, and layer management, need a [single-node](../deployment/modes/single-node) or [clustered](../deployment/modes/clustered) deployment.

## Pages in this section

| Question | Page |
|:--|:--|
| Which artifacts duplicate each other, and how is the duplication resolved? | [Finding overlapping artifacts](overlap) |
| Which artifacts do agents load, how often, from which harnesses, and which go unused? | [Seeing how the catalog is used](usage) |
| How is an artifact changed, deprecated, or frozen without breaking what depends on it, and how are layers reingested, reordered, and restored? | [Changing and retiring artifacts](lifecycle) |
| Which lint checks belong in CI, and how does a workspace stay inside a harness's skill listing budget? | [Keeping the catalog lean](quality) |
