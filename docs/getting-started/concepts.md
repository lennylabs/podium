---
title: Concepts
nav_order: 3
description: "The vocabulary used throughout the docs: registry, catalog, artifacts, domains, layers, visibility, harnesses, profiles, materialization, and meta-tools."
---

# Concepts

This page defines the terms the rest of the documentation uses. Other pages
link here for a definition instead of restating it. The
[glossary](../reference/glossary) has a one-line entry for each term.

The table maps each Podium feature to the terms it is built from and to the
page that covers it in operational detail.

| Feature | Terms | Covered in |
|:--|:--|:--|
| Domains and subdomains | [Domain](#domain) | [Domains](../authoring/domains) |
| Selective materialization | [Profile](#profile), [Materialization](#materialization) | [Selective materialization](../consuming/selective-materialization) |
| Progressive discovery | [Meta-tools](#meta-tools) | [Browsing the catalog](../consuming/browsing-the-catalog) |
| Layered composition | [Layer](#layer) | [Connecting layer sources](../deployment/layer-sources), [Layered composition](../deployment/layers) |
| Security and access control | [Visibility](#visibility), [Signing and audit](#signing-and-audit) | [Access control](../deployment/access-control) |
| Observability | [Signing and audit](#signing-and-audit) | [Audit and observability](../deployment/observability) |
| Cross-harness delivery | [Harness](#harness), [Materialization](#materialization) | [Configure your harness](../consuming/configure-your-harness) |

---

## Registry and catalog

The **registry** is the system of record for artifacts. It ingests artifacts
from their sources and serves them to consumers. The **catalog** is the set of
artifacts a registry holds. Podium is the registry, and a team's skills,
agents, and other artifacts form its catalog.

A registry runs in one of the deployment modes below:

| Mode | What runs |
|:--|:--|
| Local | A directory tree on disk, read by the CLI. There is no server process, no database, and no identity provider. |
| Single node | One binary on one machine, with SQLite, sqlite-vec, and filesystem object storage embedded. |
| Clustered | Registry replicas behind a load balancer, with Postgres, S3-compatible object storage, and OIDC. |

Every mode applies the same layer composition and serves the same artifacts.
Migration between modes is mechanical, because the same shared Go library
parses, composes, and adapts artifacts in every case.
[Deployment](../deployment/) covers the modes and the migration steps.

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
`TypeProvider` SPI.

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

Configurable rules govern how `load_domain` renders a domain: `max_depth`,
folding of sparse subdomains, `notable_count`, and a soft response-token
budget. Tenant-level defaults live in `registry.yaml`, and per-domain overrides
live in `DOMAIN.md`.

[Domains](../authoring/domains) covers the `DOMAIN.md` schema and how to
structure a hierarchy.

---

## Layer

A **layer** is a unit of composition with a single source and a visibility
declaration. The source is a Git repository, a local filesystem path, or a
custom source through the `LayerSourceProvider` SPI. Layers compose in the
order the registry config defines. Podium imposes no fixed `org / team / user`
hierarchy.

[Connecting layer sources](../deployment/layer-sources) covers Git and local
sources. [Layered composition](../deployment/layers) covers registering layers
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
another (see [Layered composition](../deployment/layers#inheritance)).

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
request time. [Access control](../deployment/access-control) covers the
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

Declaring a layer is a separate rule from writing content into it. On a
server-backed deployment mode, only a tenant admin may declare a layer that
makes the registry read a given filesystem path. The registry process reads
that path with its own rights rather than with the registrant's. See
[Who may register a local-source layer](../deployment/layer-sources#who-may-register-a-local-source-layer).

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
OpenTelemetry Collector. [Audit and observability](../deployment/observability)
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
2. **Verify**: recompute the delivery hash over the served record, then check
   the registry's signature over it. Bundle contents are not introspected,
   because vulnerability scanning is a CI/CD concern rather than a registry
   one.
3. **Adapt**: run the harness adapter to translate to native format.
4. **Hook**: run any configured `MaterializationHook` plugins for
   per-file rewrites.
5. **Write**: atomic `.tmp + rename` write to the destination.

Through the MCP server these steps run during the call, so the agent receives
the manifest body and the file paths. The SDKs split them. They return the
manifest in memory and write the files on a later `materialize()` call.

### Materialization with `podium sync`

`podium sync` runs the fetch, adapt, hook, and write steps in batch. It covers
the caller's effective view or the subset an active scope selects.

- **Server source.** `podium sync` runs the same delivery check and signature
  policy that `podium-mcp` runs, under `PODIUM_VERIFY_SIGNATURES` and the same
  key resolution. It also checks every fetched manifest-body document and
  bundled-resource body against the content hash the registry served for it,
  and fails with `materialize.content_hash_mismatch` when a body does not
  match.
- **Filesystem source.** `podium sync` composes and materializes in one
  process and runs no delivery check.

A signing registry verifies each stored row before it serves it to any reader,
`podium sync` included.

### Large resources

The `load_artifact` response delivers the manifest body and the bundled
resources below the inline cutoff directly. The rest depends on how the
registry stores them:

- On a registry with an object store, a larger resource, and a manifest above
  the cutoff, arrive as a URL into object storage.
- A resource the registry holds inline on the manifest record is returned
  inline at any size. That includes every resource of an artifact ingested
  while no object store was configured.
- With the S3 backend, an object-storage URL is presigned and time-limited.
- With the filesystem backend a single node uses by default, the URL is the
  registry's own `/objects/<content-hash>` route, authorized by the caller's
  session token.

Materialization is the write step that lands all of it on disk.

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
| `load_domain(path?)` | Returns a map of a domain: subdomains, notable artifacts, keywords, the requested domain's description. The agent's primary navigation tool. |
| `search_domains(query)` | Hybrid retrieval over each domain's projection (description + keywords + truncated body). For when the agent doesn't know the right neighborhood. |
| `search_artifacts(query?, scope?, type?, tags?)` | Hybrid retrieval over artifact frontmatter. With a query, ranks by relevance; without, browses by filter (the canonical "list all artifacts in this domain" move). |
| `load_artifact(id)` | Loads a specific artifact by ID, runs the harness adapter, materializes bundled resources to disk. This is the expensive operation; call it after the artifact has been selected. |

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

## Progressive discovery and up-front sync

Artifacts reach a host along one of two paths. Both paths share the same
registry, identity providers, layer composition, and harness adapters.

**Progressive discovery** is the MCP and SDK path. The session starts empty.
The agent calls `load_domain` and `search_domains` to traverse the catalog,
`search_artifacts` to query it, and `load_artifact` to materialize one selected
artifact together with its bundled files. The agent's context window stays
small even when the catalog holds thousands of entries. This path requires a
server.

**Up-front sync** is the `podium sync` path. It materializes the caller's
effective view, or the subset an active profile selects, onto disk before the
session starts, once or in watch mode. The harness then uses its own native
discovery under `.cursor/rules/`, `.claude/agents/`, and the other directories
it reads. This path works against a server or a local directory.

---

## Extensibility

SPIs make Podium's behavior pluggable. They cover storage, identity,
composition, signing, audit, layer sources, and delivery. Plugins compile into
a registry build today. The SPIs are designed to be wire-compatible with a
future out-of-process plugin protocol.
[Extending](../deployment/extending) covers the constraints that make that
transition source-compatible.

---

## What's next

[How it works](how-it-works) shows how these pieces fit together: the
architecture, the deployment modes, where state lives, and what runs on a
developer machine and what runs on a server.
