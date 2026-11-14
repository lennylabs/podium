---
title: Concepts
nav_order: 3
description: "The vocabulary used throughout the docs: registry, catalog, tenant, progressive discovery and up-front sync, artifacts, domains, layers, visibility, harnesses, materialization, profiles, and meta-tools."
---

# Concepts

This page defines the terms the rest of the documentation uses. Other pages
link here for a definition instead of restating it. The
[glossary](../reference/glossary) has a one-line entry for each term.

---

## Registry and catalog

The **registry** is the system of record for artifacts. It ingests artifacts
from their sources and serves them to consumers. The **catalog** is the set of
artifacts a registry holds. Podium is the registry, and a team's skills,
agents, and other artifacts form its catalog.

A consumer reads the catalog from one of two kinds of source:

- A **filesystem source** is a catalog directory on disk that the `podium`
  CLI reads directly, with no server process, database, or identity provider.
  This is the local deployment mode.
- A **server source** is a Podium registry server that consumers reach over
  HTTP. One process with embedded storage is the single-node deployment mode,
  and registry replicas with Postgres, object storage, and an OIDC identity
  provider are the clustered deployment mode.

Every mode applies the same layer composition and serves the same artifacts,
because the same shared Go library parses, composes, and adapts artifacts in
every case. [Deployment modes](../deployment/modes/) covers choosing a mode,
and [Moving between modes](../deployment/modes/migrating) covers the
migration steps.

### Tenant

A **tenant** is an organization (an org) that a registry server serves. Each
tenant has its own layer list, its own admins, its own audit stream, and its
own quotas. A registry serves one tenant, named `default`, unless it runs in
multi-tenant mode, where each request resolves to the tenant its caller's
organization names. [Tenants and admins](../deployment/identity/tenants)
covers both modes and the roles that manage them.

---

## Progressive discovery and up-front sync

Artifacts reach a host along one of two paths. Both paths share the same
registry, identity providers, layer composition, and harness adapters.

**Progressive discovery** is the MCP and SDK path. The session starts empty.
The agent calls `load_domain` and `search_domains` to traverse the catalog,
`search_artifacts` to query it, and `load_artifact` to materialize one selected
artifact together with its bundled files. The agent's context window stays
small even when the catalog holds thousands of entries. This path requires a
server source. [Meta-tools](#meta-tools) lists the calls.

**Up-front sync** is the `podium sync` path. It materializes the caller's
[effective view](#layer), or the subset an active [profile](#profile)
selects, onto disk before the session starts, once or in watch mode. The
harness then uses its own native discovery under `.claude/skills/`,
`.cursor/rules/`, and the other directories it reads. This path works against
a server source or a filesystem source.

---

## Artifact

An **artifact** is a packaged authoring unit: a directory with its manifest
files at its root and any number of bundled resources beside them, such as
scripts, references, and assets. A skill's directory holds a `SKILL.md` and may
hold an `ARTIFACT.md` overlay. Every other type's directory holds an
`ARTIFACT.md`.

```
finance/close-reporting/run-variance-analysis/   # type: skill
├── SKILL.md              ← agent-facing prose + the agentskills.io frontmatter
├── ARTIFACT.md           ← optional overlay: Podium's structured frontmatter
├── scripts/
│   └── variance.py
└── references/
    └── variance-explained.md
```

### Manifests

Each manifest is markdown with YAML frontmatter.

- **`SKILL.md`** carries the Agent Skills standard's frontmatter and the prose
  body that the agent reads. The frontmatter holds `name` and `description`,
  plus the optional `license`, `compatibility`, `metadata`, and
  `allowed-tools`.
- **`ARTIFACT.md`** carries Podium's structured frontmatter, such as `type`,
  `version`, `when_to_use`, `tags`, and `sensitivity`. For a skill, it is an
  optional overlay that the author writes by hand, and its body is empty. For
  every other type, it is the only manifest and carries both the frontmatter
  and the prose body.

### Canonical ID

The directory path is the artifact's **canonical ID**, such as
`finance/close-reporting/run-variance-analysis` above. Other artifacts
reference it by that ID. A reference can pin a version with `@<semver>`,
`@0.0.0-auto.<N>`, or `@sha256:<hash>`.

### Bundled resources

The unit is the directory rather than the manifest file. Everything beside the
manifest is a **bundled resource**, and where it lands depends on the type:

- A skill carries its resources inside the skill folder in every destination
  that ships the skill. These destinations are a workspace tree written by
  `podium sync`, a lazily loaded artifact written by `load_artifact`, and a
  published marketplace repository.
- Workspace materialization of an `agent`, `command`, `context`, or `hook`
  artifact writes the resources to a bucket beside the native file, such as
  `.podium/resources/<id>/` or `.podium/context/<id>/`.
- Workspace materialization of a `type: rule` or `type: mcp-server` artifact
  writes only the rule file or the config-file entry, so those two types drop
  their bundled resources.

[Bundled resources](../authoring/bundled-resources) covers the conventional
subfolders, the size limits, and the handling of large files.

---

## Type

Every artifact has a `type:`. A skill with no `ARTIFACT.md` has the type
`skill`. The built-in artifact types include the following:

| Type | What it is |
|:--|:--|
| `skill` | Instructions (and optional scripts) loaded into the agent's context on demand. |
| `agent` | A complete agent definition meant to run as a delegated child. |
| `context` | Pure reference material: style guides, glossaries, API references. |
| `command` | Parameterized prompt templates a human invokes (typically as a slash command). |
| `rule` | Passive context the harness loads based on a `rule_mode` (`always`, `glob`, `auto`, `explicit`). |
| `hook` | A lifecycle observer with a declared `hook_event` and a shell `hook_action`. |
| `mcp-server` | An MCP server registration: the universal `name` and `description`, plus a `server_identifier` that resolves to the server's command or URL. |

The type determines indexing, lint rules, and how the harness adapter
translates the artifact at delivery time. Extension types register through the
`TypeProvider` SPI. An **SPI** (service provider interface) is a Go interface
that a plugin implements to replace or extend one part of the registry's
behavior. [Extensibility](#extensibility) lists the areas the SPIs cover.

---

## Domain

A **domain** is a node in the catalog hierarchy, which in practice is a
directory in the registry. `finance` is a top-level domain, `finance/ap` is a
subdomain, and `finance/ap/pay-invoice` is the canonical path of an artifact
under it.

A domain folder can carry an optional `DOMAIN.md`. It adds a description,
keywords, featured artifacts, imports from elsewhere, and discovery-rendering
hints. A domain without `DOMAIN.md` still works, and the directory remains
navigable.

[Domains](../authoring/domains) covers the `DOMAIN.md` schema, how to
structure a hierarchy, and the settings that control how `load_domain` renders
a domain.

---

## Layer

A **layer** is a unit of composition with a single source and a visibility
declaration. The source is a Git repository, a local filesystem path, or a
custom source through the `LayerSourceProvider` SPI. Layers compose in the
order the registry config defines. Podium imposes no fixed `org / team / user`
hierarchy.

[Connecting layer sources](../deployment/content/layer-sources) covers Git and local
sources. [Layered composition](../deployment/content/layers) covers registering layers
and reading the composed result.

A typical setup has these layers, from lowest to highest precedence:

1. **Admin-defined layers**, in registry config order. Examples are
   `org-defaults` (visibility: organization) and `team-finance`
   (visibility: groups: [finance]).
2. **User-defined layers**: personal layers an authenticated user
   registers for themselves, capped at three by default.
3. **Workspace local overlay**: a per-workspace `.podium/overlay/`
   directory that the consumer merges client-side (the MCP server,
   `podium sync`, or an SDK). It always has the highest precedence.

When a caller asks for an artifact, Podium composes the caller's
**effective view** from every visible layer, in precedence order. Two
registry-side layers cannot contribute the same canonical ID. Ingest rejects
the second contribution with `ingest.collision`, and no artifact inherits from
another (see [Layered composition](../deployment/content/layers#inheritance)).

![Layer composition and precedence: an ordered layer stack on the left (lowest precedence at the bottom) composes into a single effective view for one caller identity on the right.](../assets/diagrams/layer-composition.svg)

<!--
ASCII fallback for the diagram above (layer composition and precedence):

  layer list (top = highest precedence)        |  effective view for alice@acme.com
                                               |
  4. workspace-overlay                         |  Precedence orders the composed view.
     .podium/overlay/ - always highest         |
  3. alice-personal                             |  overlay   -> highest
     user-defined - visible to alice@ only      |  alice     -> if visible
  2. team-finance                              |  finance   -> if alice is a member
     admin - groups: [finance]                 |  org       -> lowest, visible to anyone
  1. org-defaults                              |
     admin - organization: true                |  Layers the caller cannot see are
                                               |  silently excluded.

  Collisions between registry-side layers are rejected at ingest.
  Resolution runs on every registry call; the workspace overlay is
  merged in by the MCP server.
-->


---

## Visibility

Each layer declares its visibility independently:

| Field | Effect |
|:--|:--|
| `public: true` | Anyone, including unauthenticated callers. |
| `organization: true` | Any authenticated user in the tenant org. |
| `groups: [<oidc-group>, ...]` | Members of the listed OIDC groups. |
| `users: [<user-id>, ...]` | Listed user identifiers, by OIDC subject or email. |

Multiple fields combine as a union. The registry enforces visibility on every
call. It does not consult Git permissions or other source-side controls at
request time. [Access control](../deployment/content/access-control) covers the
enforcement boundary and how to debug an effective view.

![Identity and visibility flow: every registry call carries an OAuth token; the registry verifies it, resolves claims, then evaluates each layer against the caller's identity to produce the effective view.](../assets/diagrams/identity-visibility-flow.svg)

<!--
ASCII fallback for the diagram above (identity and visibility flow):

  caller (Alice + OAuth token)
       |
       v
  podium-server:
    1. Verify token (signature, expiry, audience)
       |
       v
    2. Resolve claims (subject, groups, tenant)
       |
       v
    3. Per-layer match (visible or hidden)
       |
       v
  composed view from visible layers

  Per-layer decision for Alice:
    layer            visibility rule          alice's claims     in view?
    org-defaults     organization: true       acme tenant member YES
    team-finance     groups: [finance]        not in finance     NO
    alice-personal   users: [alice@acme.com]  subject/email match YES
    marketing-public public: true             always visible     YES

  Visibility evaluation runs on every registry call. Layers the
  caller cannot see are silently excluded from the view. Public mode
  and filesystem-source deployments short-circuit visibility to
  true for every layer.
-->


### Authoring rights

Authoring rights are a separate concern from visibility. Whoever can merge to
a layer's tracked Git ref publishes there. Whoever can write to a
`local`-source layer's filesystem path publishes there. Branch protection,
required reviewers, and signing requirements live in the Git host, and Podium
does not duplicate them.

Registering a layer is governed separately from writing content into it.
[Who can change a layer](../deployment/content/access-control#who-can-change-a-layer)
covers the rules for declaring and changing layers.

---

## Signing and audit

A registry server signs each artifact version it ingests with its
registry-managed key. Signing is on by default. Consumers that fetch from a
server, such as the MCP server and `podium sync`, verify the registry's
signature before they write an artifact to disk. A local-mode catalog read
from disk is neither signed nor verified.

A registry server also records significant events in a hash-chained **audit
log**. Every `load_domain`, `search_domains`, `search_artifacts`, and
`load_artifact` call is an audit event, as are ingests, layer changes, and
admin grants. The audit stream can go to a file, stdout, HTTP receivers, or an
OpenTelemetry Collector. [Audit and observability](../deployment/operations/observability)
covers the destinations, metrics, tracing, and notifications.

---

## Harness

A **harness** is the AI runtime hosting an agent: Claude Code,
Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode,
Pi, Hermes, or a custom runtime. Harnesses differ in file layouts, frontmatter
conventions, and rule semantics. The full roster with documentation links is
in [Configure your harness](../consuming/configure-your-harness#supported-harnesses).

A **harness adapter** is the translator. At materialization time, the
configured adapter takes the canonical artifact and writes it into the
harness's native format. The same source artifact can produce a different
on-disk layout for each harness. A capability matrix records which canonical
fields each adapter maps natively and which it maps through a fallback.

`PODIUM_HARNESS=none` writes the canonical layout as-is. This setting produces
raw output for a custom runtime or an evaluation pipeline.

---

## Materialization

**Materialization** is the step that lands an artifact on a host's
filesystem. For `load_artifact`, the MCP server runs these steps:

1. **Fetch**: download bytes (or read from cache).
2. **Verify**: check the served record against the registry's signature.
   [Verifying deliveries](../consuming/verifying-deliveries) covers the check
   and the settings that control it.
3. **Adapt**: run the harness adapter to translate to native format.
4. **Hook**: run any configured `MaterializationHook` plugins for
   per-file rewrites.
5. **Write**: atomic `.tmp + rename` write to the destination.

Through the MCP server these steps run during the call, so the agent receives
the manifest body and the file paths. The SDKs split them. They return the
manifest in memory and write the files on a later `materialize()` call.
Bundled resources above an inline size cutoff arrive as URLs that the consumer
fetches rather than inside the response.
[Handling artifact responses](../consuming/handling-artifact-responses) covers
what each consumer receives.

`podium sync` runs the fetch, adapt, hook, and write steps in batch over the
caller's effective view or the subset an active scope selects. Against a
server source it runs the same verification as the MCP server. Against a
filesystem source it composes and materializes in one process and runs no
verification, because nothing is signed.

---

## Profile

A **profile** is a named scope stored in `sync.yaml`. A scope is a set of
include globs, exclude globs, and artifact types, evaluated against canonical
artifact IDs to select the subset of the catalog a target receives.
`podium sync --profile finance-team` materializes that subset, and
`defaults.profile` makes one profile the standing choice for a workspace. A
profile may also pin the target directory and the harness for runs that use
it.

The same scope can be passed directly as `--include`, `--exclude`, and `--type`
flags for a one-off run. A profile gives a repeatedly used scope a name, and a
committed `sync.yaml` shares it.

[Selective materialization](../consuming/selective-materialization) covers the
glob syntax, the file scopes profiles are merged from, and the commands that
capture and edit them.

---

## Meta-tools

The MCP server exposes these tools to harnesses that speak MCP:

| Tool | What it does |
|:--|:--|
| `load_domain(path?)` | Returns a map of a domain: its subdomains, notable artifacts, keywords, and description. It is the agent's primary navigation tool. |
| `search_domains(query)` | Runs hybrid retrieval over each domain's description, keywords, and truncated body. The agent calls it when it does not know which domain holds what it needs. |
| `search_artifacts(query?, scope?, type?, tags?)` | Runs hybrid retrieval over artifact frontmatter. With a query, it ranks by relevance. Without one, it lists the artifacts that match the filters, such as every artifact in a domain. |
| `load_artifact(id)` | Loads one artifact by ID, runs the harness adapter, and materializes the bundled resources to disk. It is the most expensive call, so the agent makes it after it has selected the artifact. |

The MCP server advertises further entries in `tools/list` beside the
meta-tools:

- `health` reports registry connectivity, the observed mode, cache size, and
  the last successful call.
- `scope_preview` reports aggregate counts for the caller's effective view
  (total artifacts, counts by type, and counts by sensitivity) for operators
  and reviewers. It returns `config.scope_preview_disabled` on a tenant whose
  `expose_scope_preview` gate is off.

Hosts add their own runtime tools beside all of them.

The SDK consumers (`podium-py` and `podium-ts`) and the read CLI
(`podium domain show`, `podium domain search`, `podium search`, and
`podium artifact show`) call the same registry HTTP API. They apply the same
identity, layer composition, and visibility filtering.
[Browsing the catalog](../consuming/browsing-the-catalog) covers each call,
what it returns, and what it costs.

---

## Extensibility

The SPIs make Podium's behavior pluggable. They cover storage, identity,
composition, signing, audit, layer sources, and delivery. Plugins compile into
a registry build today. The SPIs are designed to be wire-compatible with a
future out-of-process plugin protocol.
[Extending](../deployment/services/extending) covers the constraints that make that
transition source-compatible.

---

## What's next

[How it works](how-it-works) shows how these pieces fit together: the
architecture, the deployment modes, where state lives, and what runs on a
developer machine and what runs on a server.
