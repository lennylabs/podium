---
title: Consuming
nav_order: 3
description: Deliver artifacts into a harness, narrow what a workspace receives, browse the catalog at runtime, and build custom consumers.
---

# Consuming artifacts

One catalog serves every harness. These pages cover the paths artifacts take out
of the catalog and into a runtime: filesystem materialization through
`podium sync`, runtime discovery through the MCP meta-tools, programmatic access
through the SDKs, and git-repo distribution through a marketplace target.

| Page | What it covers |
|:--|:--|
| [Configure your harness](configure-your-harness) | **Cross-harness delivery.** The harness roster, the adapter each one uses, where every artifact type lands on disk, and the MCP and `podium sync` setup per harness. |
| [Selective materialization](selective-materialization) | Syncing a subset of the catalog into a workspace with include, exclude, and type filters, and defining named profiles to switch between scopes. |
| [Browsing the catalog](browsing-the-catalog) | **Progressive discovery.** How an agent traverses domains and finds artifacts at runtime through `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact`, materializing artifacts and their bundled files only when it loads them. |
| [Custom consumers via the SDK](custom-via-sdk) | Building programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, and eval harnesses) with `podium-py` or `podium-ts`. |
| [Handling artifact responses](handling-artifact-responses) | What a consumer does with the manifest and materialized files returned by `load_artifact`: which bytes arrive in the response and which are written to disk, routing by hints, safety and trust constraints, capability checks, dependency walks, and external resources. |
| [Marketplace publishing](publishing) | Rendering the catalog into harness-native git-repo distributions with a `kind: marketplace` sync target, and pushing them to a git remote. |

Start with [Configure your harness](configure-your-harness) to get artifacts
into a runtime, then narrow the set with
[Selective materialization](selective-materialization).
