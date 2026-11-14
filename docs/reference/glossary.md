---
title: Glossary
nav_order: 6
description: Definitions of the terms used across the Podium documentation.
---

# Glossary

This page defines the terms used across the documentation, in alphabetical
order. [Concepts](../getting-started/concepts) explains the main terms in more
depth.

- **Artifact**: a packaged authoring unit: a skill, agent, context, command, rule, hook, MCP server registration, or extension type. The term is distinct from a build artifact or an ML artifact.
- **Canonical artifact ID**: the directory path under the registry root, such as `finance/ap/pay-invoice`. All references use this ID, optionally suffixed with `@<semver>` or `@sha256:<hash>`.
- **Catalog**: the set of artifacts a registry holds. Podium is the registry, and the catalog is its content. A caller sees the part of the catalog its effective view includes.
- **Deployment mode**: one of the ways to run a registry: local (a directory read by the CLI), single node (one server binary), or clustered (registry replicas with Postgres and object storage). See [Deployment](../deployment/).
- **Domain**: a node in the catalog hierarchy, defined by a directory. The term is distinct from a DNS domain or a DDD domain.
- **Effective view**: the composition of every layer visible to the caller's identity, in precedence order. It includes admin-defined layers, user-defined layers, and the workspace local overlay.
- **Effort hint**: advisory metadata (`effort_hint: low | medium | high | max`) that records the author's intent about reasoning budget.
- **Harness**: the AI runtime hosting an agent, such as Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, or Hermes. The term is used interchangeably with "host" when the runtime context matters. [Configure your harness](../consuming/configure-your-harness#supported-harnesses) has the full roster with documentation links.
- **Harness adapter**: the component that translates a canonical artifact into the native file layout of one harness at materialization time.
- **Host**: the MCP-speaking system that runs the Podium MCP server alongside its own runtime.
- **Layer**: a unit of composition with a single source and a visibility declaration. The source is a Git repository, a local filesystem path, or a custom source through `LayerSourceProvider`. A layer is admin-defined, user-defined, or the workspace local overlay.
- **Manifest**: the `ARTIFACT.md` file specifically. A skill also has a `SKILL.md` that carries the agentskills.io frontmatter and the agent-facing prose body. For a skill, "manifest" without qualification refers to `ARTIFACT.md`, and "skill manifest" refers to `SKILL.md`. A skill's `ARTIFACT.md` is optional. A skill without one has no manifest on disk, and its stored manifest is empty.
- **Marketplace**: a Git repository a harness imports to install plugins. It holds a vendor manifest at a fixed location and per-plugin content. The extension, package, and tap formats are the analogous repository distributions for Gemini, Pi, and Hermes.
- **Marketplace output**: a `podium sync` target of `kind: marketplace`, made of a Git repository, a harness set, a plugin list, and a workflow. See [Publishing marketplace repositories](../deployment/content/publishing).
- **Materialization**: the atomic write of a loaded artifact's content onto the host's filesystem. The content is the manifest and the bundled resources, after harness adapter translation.
- **MCP server (Podium MCP server)**: the in-process bridge binary the host runs.
- **Model class hint**: advisory metadata (`model_class_hint: nano | small | medium | large | frontier`) that records the author's intent about model capability tier.
- **Package**: the on-disk directory containing an artifact's manifest files (an `ARTIFACT.md`, a `SKILL.md`, or both) and its bundled resources.
- **Plugin (publishing)**: a named bundle of selected artifacts, defined by a scope filter and rendered into each harness's distribution layout. The term "plugin" for an in-process SPI extension keeps its separate meaning, and this entry names the publishing sense.
- **Registry**: the service that ingests artifacts from layer sources and serves the catalog. Podium is a registry. In the local deployment mode a folder on disk backs it, and in the single-node and clustered modes a Podium server runs it.
- **Rule mode**: the `rule_mode` field on a `rule` artifact (`always`, `glob`, `auto`, or `explicit`). It controls when the harness loads the rule.
- **Session ID**: an optional UUID the host generates per agent session. The registry uses it for `latest`-resolution consistency and learn-from-usage reranking.
- **Visibility**: the per-layer access declaration in the registry config: `public`, `organization`, OIDC `groups`, or explicit `users`. A user-defined layer is the exception. Its visibility is the implicit `users: [<registrant>]`, set from the authenticated identity at registration, and it cannot be widened.
- **Workspace local overlay**: the workspace-scoped layer that the `LocalOverlayProvider` reads from `${PODIUM_OVERLAY_PATH}`, or from `<workspace>/.podium/overlay/` when that directory exists. The consumer merges it client-side: the MCP server, `podium sync`, and the SDKs each apply it. It has the highest precedence in the caller's effective view.
