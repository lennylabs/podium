---
layout: default
title: Implementation status
parent: About
nav_order: 2
description: Current phase, what's wired up, what's targeted, where to find the build sequence.
---

# Implementation status

Podium is in the **design phase**. The technical specification is the source of truth and the basis for a spec- and test-driven implementation. There is no shipped binary yet.

The documentation describes the v1 surface as specified. Where pages reference commands or behavior that's planned-not-shipped, this page is the canonical pointer for what's actually wired up at any given time.

---

## What contributions help most today

Design feedback. Read the [`spec/`](https://github.com/lennylabs/podium/tree/main/spec) and open issues or discussions with questions, disagreements, missing use cases, edge cases that aren't handled. Spec stress-testing is the single highest-leverage contribution at this phase.

Other useful contributions:

- **Sketch a harness adapter.** Prototyping an adapter for a specific harness against the [adapter contract](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#67-harness-adapters) helps surface gaps before they're expensive to fix.
- **Sketch a `LayerSourceProvider` plugin.** A custom source backend (S3, OCI, internal CMS) against the [SPI](https://github.com/lennylabs/podium/blob/main/spec/09-extensibility.md) helps validate that surface.
- **Comparisons and use cases.** Tell us where Podium does or doesn't fit your workflow.
- **Security review.** Threat-model the design; comment on the spec sections related to identity, audit, signing, and visibility.
- **Documentation fixes.** Small PRs for typos, broken links, or unclear passages are welcome anytime.

PR-level contributions against platform components open up once the core lands.

---

## Build sequence

The full build sequence lives in [`spec/10-mvp-build-sequence.md`](https://github.com/lennylabs/podium/blob/main/spec/10-mvp-build-sequence.md). The phases are directional; surface order and timing may shift as implementation surfaces new constraints.

### Initial phases (cover lightest deployment modes end-to-end)

| Phase | What | Why |
|:--|:--|:--|
| 0 | Filesystem-source `podium sync` + `podium serve --standalone` for the same artifact directory plus search and HTTP API. | Cover the lightest deployment modes; five-minute install for personal/small-team use. |
| 1 | Manifest schema + `podium lint` for `ARTIFACT.md`, `SKILL.md`, and `DOMAIN.md` + per-type lint rules (including agentskills.io compliance for skills) + signing. | Authors need a way to validate artifacts; lint is the early quality bar. |
| 2 | Registry HTTP API: `load_domain`, `search_domains`, `search_artifacts`, `load_artifact`. | The wire surface every server-source consumer talks to. |
| 3 | `podium sync` for `none`, `claude-code`, and `codex` adapters. | Filesystem delivery end-to-end with the full client config surface. |
| 4 | Podium MCP server core + `podium-py` SDK + read CLI. | Exercises MCP-speaking and programmatic runtimes against the catalog. |

### Enterprise phases

Phases 5+ add the standard-deployment capabilities: multi-tenant Postgres data model, GitProvider + webhook ingest, LayerComposer + visibility filtering + OIDC + SCIM, domain composition, versioning + immutability, layer CLI, identity providers, workspace overlay + local search, the remaining harness adapters + conformance suite, the TS SDK, dependency graph, audit log + integrity, vulnerability tracking, deployment artifacts (Helm chart, Grafana dashboard, runbook), and the example artifact registry.

Read the build sequence in the spec for the full list and rationale.

---

## How to track progress

- **Spec changes** are visible in the [spec/ commit history](https://github.com/lennylabs/podium/commits/main/spec).
- **Implementation status** lands here as work is wired up.
- **Open issues and discussions** at [github.com/lennylabs/podium](https://github.com/lennylabs/podium) capture the current conversation about design and direction.

This page will be updated as phases land in `main`.
