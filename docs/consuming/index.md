---
title: Consuming
nav_order: 3
description: Deliver artifacts from a registry into a harness, narrow what a workspace receives, browse the catalog at runtime, build custom consumers, and verify what arrives.
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

A harness can also import a marketplace repository that a `kind: marketplace`
sync target renders from the catalog. Rendering and pushing that repository is an
operator task that
[Publishing marketplace repositories](../deployment/content/publishing) covers.

## Pages in this section

| Page | What it covers |
|:--|:--|
| [Configure your harness](configure-your-harness) | **Cross-harness delivery.** The harness roster, the adapter each one uses, where every artifact type lands on disk, authentication against a server registry, and the `podium sync` and MCP server setup per harness. |
| [Selective materialization](selective-materialization) | Syncing a subset of the catalog into a workspace with include, exclude, and type filters, and defining named profiles to switch between scopes. |
| [Browsing the catalog](browsing-the-catalog) | **Progressive discovery.** How an agent traverses domains and finds artifacts at runtime through `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact`, materializing artifacts and their bundled files only when it loads them. |
| [Handling artifact responses](handling-artifact-responses) | What a consumer does with the manifest and materialized files returned by `load_artifact`: how resource bytes travel, routing by hints, safety and trust constraints, capability checks, dependency walks, and external resources. |
| [Building custom consumers with the SDK](custom-via-sdk) | Building programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, and eval harnesses) with `podium-py` or `podium-ts`. |
| [Verifying deliveries](verifying-deliveries) | The checks a delivered artifact passes: the registry's stored-row check, the delivery hash and signature checks in `podium-mcp`, `podium sync`, and the SDKs, the verification key set, cached records after a key rotation, and stale `latest` protection. |

## Where to start

Start with [Configure your harness](configure-your-harness) to get artifacts
into a runtime. Then narrow the set with
[Selective materialization](selective-materialization).
