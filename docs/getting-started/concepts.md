---
layout: default
title: Concepts
parent: Getting Started
nav_order: 2
description: Vocabulary used throughout the docs: artifacts, domains, layers, harnesses, materialization, and meta-tools.
---

# Concepts

The terms below appear throughout the docs. Each term has a specific
meaning in Podium.

---

## Artifact

An **artifact** is a packaged authoring unit: a directory with an `ARTIFACT.md` file at its root, plus a `SKILL.md` if the artifact is a skill, and any number of bundled resources alongside (scripts, references, assets).

```
finance/close-reporting/run-variance-analysis/   # type: skill
├── SKILL.md              ← agent-facing prose + the agentskills.io frontmatter
├── ARTIFACT.md           ← Podium's structured frontmatter
├── scripts/
│   └── variance.py
└── references/
    └── variance-explained.md
```

Each manifest is markdown with YAML frontmatter. For skills, `SKILL.md` carries the standard's frontmatter (`name`, `description`, plus optional `license`, `compatibility`, `metadata`, `allowed-tools`) and the prose body that the agent reads. `ARTIFACT.md` carries Podium's structured frontmatter (`type`, `version`, `when_to_use`, `tags`, `sensitivity`, and the rest); for skills its body is empty. For non-skill types, `ARTIFACT.md` is the only manifest and carries both frontmatter and prose body.

The directory path is the artifact's **canonical ID**: `finance/close-reporting/run-variance-analysis` above. Other artifacts reference it by that ID, optionally with `@<semver>` or `@sha256:<hash>` for version pinning.

---

## Type

Every artifact declares a `type:`. Built-in artifact types include:

| Type | What it is |
|:--|:--|
| `skill` | Instructions (and optional scripts) loaded into the agent's context on demand. |
| `agent` | A complete agent definition meant to run as a delegated child. |
| `context` | Pure reference material: style guides, glossaries, API references. |
| `command` | Parameterized prompt templates a human invokes (typically as a slash command). |
| `rule` | Passive context the harness loads based on a `rule_mode` (`always`, `glob`, `auto`, `explicit`). |
| `hook` | A lifecycle observer with a declared `hook_event` and a shell `hook_action`. |
| `mcp-server` | An MCP server registration: name, endpoint, auth profile, description. |

Extension types register through the `TypeProvider` SPI. The type
determines indexing, lint rules, and how the harness adapter
translates the artifact at delivery time.

---

## Domain

A **domain** is a node in the catalog hierarchy. In practice, a
directory in the registry. `finance` is a top-level domain;
`finance/ap` is a subdomain; `finance/ap/pay-invoice` is the
canonical path of an artifact under it.

A domain folder can carry an optional `DOMAIN.md` that adds
description, keywords, featured artifacts, imports from elsewhere,
and discovery-rendering hints. Without `DOMAIN.md`, a domain still
works. The directory remains navigable without a manifest.

The rendering of a domain in `load_domain` output is governed by
configurable rules: `max_depth`, folding of sparse subdomains,
`notable_count`, a soft response-token budget. Tenant-level defaults
live in `registry.yaml`; per-domain overrides live in `DOMAIN.md`.

---

## Registry

The **registry** is the system of record for artifacts. It can be:

- A **directory tree on disk** (filesystem mode, with no daemon, no
  server, no auth).
- A **standalone single-binary server** running on one machine.
- A **standard deployment** with Postgres, S3, OIDC, multi-tenancy,
  and the full governance feature set.

All modes apply the same layer composition and serve the same
artifacts. Migration between modes is mechanical: the same shared
Go library does the parsing, composition, and adapter work in every
case.

---

## Layer

A **layer** is a unit of composition with a single source (a Git
repo, a local filesystem path, or a custom source via the
`LayerSourceProvider` SPI) and a visibility declaration. Layers
compose in a defined order. There's no fixed `org / team / user`
hierarchy. The ordering is whatever the registry config says.

A typical setup might have:

1. **Admin-defined layers**, in registry config order, e.g.,
   `org-defaults` (visibility: organization) and `team-finance`
   (visibility: groups: [finance]).
2. **User-defined layers**: personal layers an authenticated user
   registers for themselves, capped at three by default.
3. **Workspace local overlay**: a per-workspace `.podium/overlay/`
   directory the MCP server merges client-side, always at highest
   precedence.

When a caller asks for an artifact, Podium composes the caller's
**effective view** from every visible layer, in
precedence order. Higher-precedence layers override lower on
collisions; `extends:` lets a higher artifact inherit and refine
a lower one without forking.

---

## Visibility

Each layer declares its visibility independently:

| Field | Effect |
|:--|:--|
| `public: true` | Anyone, including unauthenticated callers. |
| `organization: true` | Any authenticated user in the tenant org. |
| `groups: [<oidc-group>, ...]` | Members of the listed OIDC groups. |
| `users: [<user-id>, ...]` | Listed user identifiers. |

Multiple fields combine as a union. Visibility is enforced at the
registry on every call. Git permissions and other source-side
controls are not consulted at request time.

Authoring rights are a separate concern. Whoever can merge to a
layer's tracked Git ref publishes there; whoever can write to a
`local`-source layer's filesystem path publishes there. Branch
protection, required reviewers, and signing requirements live in
the Git host. Podium does not duplicate them.

---

## Harness

A **harness** is the AI runtime hosting an agent: Claude Code,
Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode,
Pi, Hermes, or a custom runtime. Harnesses have different file
layouts, different frontmatter conventions, and different rule
semantics. The full roster with documentation links is in
[Configure your harness](../consuming/configure-your-harness#supported-harnesses).

The **harness adapter** is the translator. At materialization time,
the configured adapter takes the canonical artifact and writes it
into the harness's native format. The same source artifact can produce
a different on-disk layout for each harness. The capability matrix
(§6.7.1 of the spec) records which canonical fields each adapter maps natively
versus via fallback.

`PODIUM_HARNESS=none` writes the canonical layout as-is. This is useful
when raw output is needed for a custom runtime or evaluation pipeline.

---

## Materialization

**Materialization** is what happens when an artifact lands on a
host's filesystem. For `load_artifact`, the MCP server runs these
steps:

1. **Fetch**: download bytes (or read from cache).
2. **Verify**: signature, content hash, optional SBOM walk.
3. **Adapt**: run the harness adapter to translate to native format.
4. **Hook**: run any configured `MaterializationHook` plugins for
   per-file rewrites.
5. **Write**: atomic `.tmp + rename` write to the destination.

`podium sync` does the same thing in batch for the caller's whole
effective view.

---

## Meta-tools

The MCP server exposes these tools to harnesses that speak MCP:

| Tool | What it does |
|:--|:--|
| `load_domain(path?)` | Returns a map of a domain: subdomains, notable artifacts, keywords, the requested domain's description. The agent's primary navigation tool. |
| `search_domains(query)` | Hybrid retrieval over each domain's projection (description + keywords + truncated body). For when the agent doesn't know the right neighborhood. |
| `search_artifacts(query?, scope?, type?, tags?)` | Hybrid retrieval over artifact frontmatter. With a query, ranks by relevance; without, browses by filter (the canonical "list all artifacts in this domain" move). |
| `load_artifact(id)` | Loads a specific artifact by ID, runs the harness adapter, materializes bundled resources to disk. This is the expensive operation; call it after the artifact has been selected. |

These are the only tools Podium contributes to a session. Hosts add
their own runtime tools alongside.

The SDK consumers (`podium-py`, `podium-ts`) and the read CLI
(`podium domain show`, `podium domain search`, `podium search`,
`podium artifact show`) hit the same registry HTTP API and apply
the same identity, layer composition, and visibility filtering.

---

## Lazy versus eager loading

**Lazy** (MCP / SDK path): the session starts empty. The agent calls
`load_domain` to navigate, `search_artifacts` to query, and `load_artifact`
to materialize one specific artifact after selection. This keeps the
agent's context window small even when the catalog has thousands
of entries. Requires a server.

**Eager** (`podium sync` path): one-shot or `--watch` materialization
of the user's effective view (or a scope-filtered subset) onto disk.
The harness then uses its own native discovery: `.cursor/rules/`,
`.claude/agents/`, etc. This path is useful for pre-materialized
artifacts on disk when runtime discovery is unnecessary. It works against
either a server or a filesystem-source registry.

Both paths share the same registry, identity providers, layer
composition, and harness adapters.

---

## Extensibility

Podium's behavior is pluggable via SPIs covering storage, identity,
composition, signing, audit, layer source, and delivery. Plugins
compile into a registry build today. The SPIs are designed to
be wire-compatible with a future out-of-process plugin protocol.
See [Deployment → Extending](../deployment/extending) (and §9.3 of
the spec) for the constraints that make that transition
source-compatible.

---

## What's next

The next page, [How it works](how-it-works), shows how these pieces
fit together: the architecture, the deployment modes, where state
lives, and what's running on your machine versus on a server.
