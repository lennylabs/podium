---
title: Catalog content
nav_order: 3
description: "Connect layer sources, compose layers, control who sees each layer, and publish marketplace repositories."
---

# Catalog content

The pages in this section cover the content a server-backed registry serves: where its artifacts come from, how the registry combines several sources into one catalog, which callers see each part of it, and how to publish the catalog as harness marketplace repositories. They are for operators of a [single-node](../modes/single-node) or [clustered](../modes/clustered) deployment.

| Page | What it covers |
|:--|:--|
| [Connecting layer sources](layer-sources) | Registering Git repositories, private remotes, local paths, and Agent Skills repositories as layers, keeping each layer current with webhooks, polling, or a manual reingest, and who may register a layer. |
| [Layered composition](layers) | How the registry merges layers in an explicit precedence order, the collision and no-inheritance rules, the workspace overlay, and how to read the composed result. |
| [Access control](access-control) | The `visibility:` fields that decide who reads a layer, the rules that decide who may change one, and how to debug a caller's effective view. |
| [Publishing marketplace repositories](publishing) | Rendering the catalog into harness marketplace repositories with a `kind: marketplace` sync target, and running the publish from a CI job. |

[Identity](../identity/) covers the identity providers that layer visibility depends on.
