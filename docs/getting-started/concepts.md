---
layout: default
title: Concepts
parent: Getting Started
nav_order: 2
description: The vocabulary you'll see everywhere — artifacts, domains, layers, harnesses, materialization, the four meta-tools.
---

# Concepts

The terms below show up in every other section. None of them are
hard, but they have specific meanings in Podium and reading them
once up front saves time later.

---

## Artifact

An **artifact** is a packaged authoring unit — a directory with an
`ARTIFACT.md` file at its root and any number of bundled resources
alongside (scripts, templates, schemas, anything).

```
finance/close-reporting/run-variance-analysis/
├── ARTIFACT.md           ← the manifest
├── scripts/
│   └── variance.py
└── templates/
    └── report.md.j2
```

The manifest is markdown with YAML frontmatter. The frontmatter is
what Podium indexes; the prose body is what an agent reads when the
artifact is loaded.

The directory path is the artifact's **canonical ID** —
`finance/close-reporting/run-variance-analysis` above. Other
artifacts reference it by that ID, optionally with `@<semver>` or
`@sha256:<hash>` for version pinning.

---

## Type

Every artifact declares a `type:`. The seven first-class types are:

| Type | What it is |
|:--|:--|
| `skill` | Instructions (and optional scripts) loaded into the agent's context on demand. |
| `agent` | A complete agent definition meant to run as a delegated child. |
| `context` | Pure reference material — style guides, glossaries, API references. |
| `command` | Parameterized prompt templates a human invokes (typically as a slash command). |
| `rule` | Passive context the harness loads based on a `rule_mode` (`always`, `glob`, `auto`, `explicit`). |
| `hook` | A lifecycle observer with a declared `hook_event` and a shell `hook_action`. |
| `mcp-server` | An MCP server registration — name, endpoint, auth profile, description. |

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
works — it's just a navigable directory of artifacts.

The shape of a domain in `load_domain` output is governed by
configurable rules: `max_depth`, folding of sparse subdomains,
`notable_count`, a soft response-token budget. Tenant-level defaults
live in `registry.yaml`; per-domain overrides live in `DOMAIN.md`.

---

## Registry

The **registry** is the system of record for artifacts. It can be:

- A **directory tree on disk** (filesystem mode — no daemon, no
  server, no auth).
- A **standalone single-binary server** running on one machine.
- A **standard deployment** with Postgres, S3, OIDC, multi-tenancy,
  and the full governance feature set.

All three apply the same layer composition and serve the same
artifacts. Migration between shapes is mechanical — same shared Go
library does the parsing, composition, and adapter work in every
case.

---

## Layer

A **layer** is a unit of composition with a single source (a Git
repo, a local filesystem path, or a custom source via the
`LayerSourceProvider` SPI) and a visibility declaration. Layers
compose in a defined order. There's no fixed `org / team / user`
hierarchy — the ordering is whatever the registry config says.

A typical setup might have:

1. **Admin-defined layers**, in registry config order — e.g.,
   `org-defaults` (visibility: organization) and `team-finance`
   (visibility: groups: [finance]).
2. **User-defined layers** — personal layers an authenticated user
   registers for themselves, capped at three by default.
3. **Workspace local overlay** — a per-workspace `.podium/overlay/`
   directory the MCP server merges client-side, always at highest
   precedence.

When a caller asks for an artifact, Podium composes their
**effective view** from every layer they're allowed to see, in
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
registry on every call — Git permissions and other source-side
controls are not consulted at request time.

Authoring rights are a separate concern. Whoever can merge to a
layer's tracked Git ref publishes there; whoever can write to a
`local`-source layer's filesystem path publishes there. Branch
protection, required reviewers, and signing requirements live in
the Git host, not in Podium.

---

## Harness

A **harness** is the AI runtime hosting an agent — Claude Code,
Cursor, OpenCode, Codex, Gemini, Pi, Hermes, Claude Desktop, or a
custom runtime. Harnesses have different file layouts, different
frontmatter conventions, and different rule semantics.

The **harness adapter** is the translator. At materialization time,
the configured adapter takes the canonical artifact and writes it
into the harness's native format. Same source artifact, different
on-disk shape per harness. The capability matrix (§6.7.1 of the
spec) records which canonical fields each adapter supports natively
versus via fallback.

`PODIUM_HARNESS=none` writes the canonical layout as-is, useful
when you want raw output for a custom runtime or evaluation pipeline.

---

## Materialization

**Materialization** is what happens when an artifact lands on a
host's filesystem. For `load_artifact`, the MCP server runs five
steps:

1. **Fetch** — download bytes (or read from cache).
2. **Verify** — signature, content hash, optional SBOM walk.
3. **Adapt** — run the harness adapter to translate to native shape.
4. **Hook** — run any configured `MaterializationHook` plugins for
   per-file rewrites.
5. **Write** — atomic `.tmp + rename` write to the destination.

`podium sync` does the same thing in batch for the caller's whole
effective view.

---

## The four meta-tools

The MCP server exposes four tools to harnesses that speak MCP:

| Tool | What it does |
|:--|:--|
| `load_domain(path?)` | Returns a map of a domain — subdomains, notable artifacts, keywords, the requested domain's description. The agent's primary navigation tool. |
| `search_domains(query)` | Hybrid retrieval over each domain's projection (description + keywords + truncated body). For when the agent doesn't know the right neighborhood. |
| `search_artifacts(query?, scope?, type?, tags?)` | Hybrid retrieval over artifact frontmatter. With a query, ranks by relevance; without, browses by filter (the canonical "list all artifacts in this domain" move). |
| `load_artifact(id)` | Loads a specific artifact by ID, runs the harness adapter, materializes bundled resources to disk. The expensive operation — only call it when you actually need the artifact. |

These are the only tools Podium contributes to a session. Hosts add
their own runtime tools alongside.

The SDK consumers (`podium-py`, `podium-ts`) and the read CLI
(`podium domain show`, `podium domain search`, `podium search`,
`podium artifact show`) hit the same registry HTTP API and apply
the same identity, layer composition, and visibility filtering.

---

## Lazy versus eager loading

**Lazy** (MCP / SDK path): the session starts empty. The agent calls
`load_domain` to navigate, `search_artifacts` to query, `load_artifact`
to materialize one specific artifact when it's actually needed. Keeps
the agent's context window small even when the catalog has thousands
of entries. Requires a server.

**Eager** (`podium sync` path): one-shot or `--watch` materialization
of the user's effective view (or a scope-filtered subset) onto disk.
The harness then uses its own native discovery — `.cursor/rules/`,
`.claude/agents/`, etc. Useful when you want pre-materialized
artifacts on disk and don't need runtime discovery. Works against
either a server or a filesystem-source registry.

Both paths share the same registry, identity providers, layer
composition, and harness adapters.

---

## Extensibility

Podium's behavior is pluggable via 17 SPIs covering storage,
identity, composition, signing, audit, layer source, and delivery.
Plugins compile into a registry build today. The SPI shapes are
designed to be wire-compatible with a future out-of-process plugin
protocol — see [Deployment →
Extending](../deployment/extending) (and §9.3 of the spec) for the
constraints that make that transition source-compatible.

---

## What's next

The next page, [How it works](how-it-works), shows how these pieces
fit together — the architecture, the three deployment shapes, where
state lives, and what's running on your machine versus on a server.
