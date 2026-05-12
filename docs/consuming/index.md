---
layout: default
title: Consuming
nav_order: 3
has_children: true
description: Configure a harness to consume Podium artifacts, operate Podium from custom code, and understand how the catalog is browsed at runtime.
---

# Consuming artifacts

Reference for using Podium artifacts. Each page below covers one topic.

| Page | What it covers |
|:--|:--|
| [Configure your harness](configure-your-harness) | Per-harness setup for `podium sync` (filesystem materialization) and the MCP server (runtime discovery). Covers Claude Code, Claude Desktop, Cursor, OpenCode, Codex, Gemini, Pi, Hermes, and the generic/`none` adapter. |
| [Browsing the catalog](browsing-the-catalog) | How an agent navigates the catalog at runtime: `load_domain`, `search_domains`, `search_artifacts`, `load_artifact`. The discovery flow and what each call costs. |
| [Custom consumers via the SDK](custom-via-sdk) | Building programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses) with `podium-py` or `podium-ts`. |
| [Handling artifact responses](handling-artifact-responses) | What a consumer does with the manifest and materialized files returned by `load_artifact`: route by hints, honor safety constraints, verify requirements, register MCP servers, walk dependencies, fetch external resources. |

Pick the page that matches the consumer under configuration.
