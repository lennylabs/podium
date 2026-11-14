---
title: Consuming
nav_order: 3
description: Deliver artifacts from a registry into a harness, narrow what a workspace receives, browse the catalog at runtime, and build custom consumers.
---

# Consuming artifacts

These pages cover how artifacts leave a Podium registry and reach the runtime
that uses them. They are for developers who connect a harness or a program to a
registry, and for platform engineers who distribute the catalog to teams.

## Consumption paths

An artifact reaches a runtime through one of the following paths. A harness
adapter translates each artifact into the format the target harness reads.

| Path | Mechanism | Page |
|:--|:--|:--|
| Filesystem materialization | `podium sync` writes harness-native files into a workspace before the session starts. | [Configure your harness](configure-your-harness) |
| Runtime discovery | The Podium MCP server exposes meta-tools that an agent calls mid-session to find and load artifacts. | [Browsing the catalog](browsing-the-catalog) |
| Programmatic access | The `podium-py` and `podium-ts` SDKs call the registry from custom code. | [Building custom consumers with the SDK](custom-via-sdk) |
| Git-repo distribution | A `kind: marketplace` sync target renders a marketplace repository that the harness imports. | [Publishing marketplace repositories](publishing) |

## Pages in this section

| Page | What it covers |
|:--|:--|
| [Configure your harness](configure-your-harness) | **Cross-harness delivery.** The harness roster, the adapter each one uses, where every artifact type lands on disk, and the MCP and `podium sync` setup per harness. |
| [Selective materialization](selective-materialization) | Syncing a subset of the catalog into a workspace with include, exclude, and type filters, and defining named profiles to switch between scopes. |
| [Browsing the catalog](browsing-the-catalog) | **Progressive discovery.** How an agent traverses domains and finds artifacts at runtime through `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact`, materializing artifacts and their bundled files only when it loads them. |
| [Building custom consumers with the SDK](custom-via-sdk) | Building programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, and eval harnesses) with `podium-py` or `podium-ts`. |
| [Handling artifact responses](handling-artifact-responses) | What a consumer does with the manifest and materialized files returned by `load_artifact`: which bytes arrive in the response and which are written to disk, routing by hints, safety and trust constraints, capability checks, dependency walks, and external resources. |
| [Publishing marketplace repositories](publishing) | Rendering the catalog into harness-native git-repo distributions with a `kind: marketplace` sync target, and pushing them to a git remote. |

## Where to start

Start with [Configure your harness](configure-your-harness) to get artifacts
into a runtime. Then narrow the set with
[Selective materialization](selective-materialization).
