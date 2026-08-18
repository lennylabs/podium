---
title: Concepts
nav_order: 3
description: "Vocabulary used throughout the docs: artifacts, domains, layers, harnesses, profiles, materialization, and meta-tools."
---

# Concepts

Definitions live on this page. The terms below appear throughout the docs, and
other sections link here for them rather than restating them.

One catalog serves every harness. The table below maps each Podium feature to
the terms it is built from and to the page that covers it in operational detail.

| Feature | Terms | Covered in |
|:--|:--|:--|
| Cross-harness delivery | [Harness](#harness), [Materialization](#materialization) | [Configure your harness](../consuming/configure-your-harness) |
| Domains and subdomains | [Domain](#domain) | [Domains](../authoring/domains) |
| Selective materialization | [Profile](#profile) | [Selective materialization](../consuming/selective-materialization) |
| Progressive discovery | [Meta-tools](#meta-tools) | [Browsing the catalog](../consuming/browsing-the-catalog) |
| Layered composition | [Layer](#layer) | [Layered composition](../deployment/layers) |
| Access control | [Visibility](#visibility) | [Access control](../deployment/access-control) |

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

The unit is the directory rather than the manifest file. Everything beside the manifest is a **bundled resource**. A skill carries its resources inside the skill folder in every destination that ships the skill: a workspace tree written by `podium sync`, a lazily loaded artifact written by `load_artifact`, and a published marketplace repository. Workspace materialization of an `agent`, `command`, `context`, or `hook` artifact writes the resources to a bucket beside the native file, such as `.podium/resources/<id>/` or `.podium/context/<id>/`. Workspace materialization of a `type: rule` or `type: mcp-server` artifact writes only the rule file or the config-file entry, so those two types drop their bundled resources. [Bundled resources](../authoring/bundled-resources) covers the conventional subfolders, the size limits, and the handling of large files.

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
| `mcp-server` | An MCP server registration: the universal `name` and `description`, plus a `server_identifier` that resolves to the server's command or URL. |

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
`notable_count`, and a soft response-token budget. Tenant-level defaults
live in `registry.yaml`, and per-domain overrides live in `DOMAIN.md`.

[Domains](../authoring/domains) covers the `DOMAIN.md` schema and how to
structure a hierarchy.

---

## Registry

The **registry** is the system of record for artifacts. It runs in one of
three tiers:

- **Local**: a directory tree on disk, read by the CLI. There is no server
  process, no database, and no identity provider.
- **Single node**: one binary on one machine, with SQLite, sqlite-vec, and
  filesystem object storage embedded.
- **Clustered**: registry replicas behind a load balancer, with Postgres,
  S3-compatible object storage, OIDC, and multi-tenancy.

All three tiers apply the same layer composition and serve the same
artifacts. Migration between tiers is mechanical, because the same shared
Go library does the parsing, composition, and adapter work in every
case. [Deployment](../deployment/) covers the tiers and the migration steps.

---

## Layer

A **layer** is a unit of composition with a single source (a Git
repository, a local filesystem path, or a custom source via the
`LayerSourceProvider` SPI) and a visibility declaration. Layers
compose in a defined order. There is no fixed `org / team / user`
hierarchy. The ordering is whatever the registry config says.
[Layered composition](../deployment/layers) covers registering layers and
reading the composed result.

A typical setup might have:

1. **Admin-defined layers**, in registry config order, e.g.,
   `org-defaults` (visibility: organization) and `team-finance`
   (visibility: groups: [finance]).
2. **User-defined layers**: personal layers an authenticated user
   registers for themselves, capped at three by default.
3. **Workspace local overlay**: a per-workspace `.podium/overlay/`
   directory the consumer merges client-side (the MCP server, `podium
   sync`, or an SDK), always at highest precedence.

When a caller asks for an artifact, Podium composes the caller's
**effective view** from every visible layer, in
precedence order. Higher-precedence layers override lower on
collisions; `extends:` lets a higher artifact inherit and refine
a lower one without forking.

![Layer composition and precedence: an ordered layer stack on the left (lowest precedence at the bottom) composes into a single effective view for one caller identity on the right.](../assets/diagrams/layer-composition.svg)

<!--
ASCII fallback for the diagram above (layer composition and precedence):

  layer list (top = highest precedence)        |  effective view for alice@acme.com
                                               |
  4. workspace-overlay                         |  Higher layers override on collision.
     .podium/overlay/ - always highest         |
  3. alice-personal                             |  overlay   -> wins
     user-defined - visible to alice@ only      |  alice      -> if no overlay match
  2. team-finance                              |  finance   -> if alice is a member
     admin - groups: [finance]                 |  org       -> fallback for anyone
  1. org-defaults                              |
     admin - organization: true                |  Layers the caller cannot see are
                                               |  silently excluded; hidden parents
                                               |  merge server-side when a child
                                               |  declares extends:.
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

Multiple fields combine as a union. Visibility is enforced at the
registry on every call. Git permissions and other source-side
controls are not consulted at request time.
[Access control](../deployment/access-control) covers the enforcement
boundary and how to debug an effective view.

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
  caller cannot see are silently excluded; hidden parents merge
  server-side when a visible child declares extends:. Public mode
  and filesystem-source deployments short-circuit visibility to
  true for every layer.
-->


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
a different on-disk layout for each harness. A capability matrix
records which canonical fields each adapter maps natively
versus via fallback.

`PODIUM_HARNESS=none` writes the canonical layout as-is. This is useful
when raw output is needed for a custom runtime or evaluation pipeline.

---

## Materialization

**Materialization** is what happens when an artifact lands on a
host's filesystem. For `load_artifact`, the MCP server runs these
steps:

1. **Fetch**: download bytes (or read from cache).
2. **Verify**: signature and content hash. (Bundle contents are not introspected; vulnerability scanning is a CI/CD concern, not a registry one.)
3. **Adapt**: run the harness adapter to translate to native format.
4. **Hook**: run any configured `MaterializationHook` plugins for
   per-file rewrites.
5. **Write**: atomic `.tmp + rename` write to the destination.

`podium sync` runs the same steps in batch, over the caller's effective
view or over the subset an active scope selects.

The `load_artifact` response delivers the manifest body and the bundled
resources below the inline cutoff directly. A larger resource, and a
manifest above the cutoff, arrive as a URL into object storage: presigned
and time-limited with the S3 backend, and the registry's own
`/objects/<content-hash>` route, authorized by the caller's session token,
with the filesystem backend a single node uses by default. Materialization
is the write step that lands all of it on disk. Through the MCP server these
steps run during the call, so the agent receives the manifest body and the
file paths. The SDKs split them, returning the manifest in memory and
writing on a later `materialize()` call.

---

## Profile

A **profile** is a named scope stored in `sync.yaml`. A scope is a set of
include globs, exclude globs, and artifact types, evaluated against canonical
artifact IDs to select the subset of the catalog a target receives. `podium sync
--profile finance-team` materializes that subset, and `defaults.profile` makes
one profile the standing choice for a workspace. A profile may also pin the
target directory and the harness for runs that use it.

The same scope can be passed directly as `--include`, `--exclude`, and `--type`
flags for a one-off run. The profile exists so a scope a workspace uses
repeatedly has a name and can be shared through a committed `sync.yaml`.

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

These are the meta-tools. The MCP server advertises further entries in
`tools/list` alongside them. `health` reports registry connectivity, the
observed mode, cache size, and the last successful call. `scope_preview`
reports aggregate counts for the caller's effective view (total artifacts,
counts by type, and counts by sensitivity) for operators and reviewers, and
it returns `config.scope_preview_disabled` on a tenant whose
`expose_scope_preview` gate is off. Hosts add their own runtime tools
alongside all of them.

The SDK consumers (`podium-py`, `podium-ts`) and the read CLI
(`podium domain show`, `podium domain search`, `podium search`, and
`podium artifact show`) hit the same registry HTTP API and apply
the same identity, layer composition, and visibility filtering.
[Browsing the catalog](../consuming/browsing-the-catalog) covers each call,
what it returns, and what it costs.

---

## Progressive discovery and up-front sync

Artifacts reach a host along one of two paths.

**Progressive discovery** (the MCP and SDK path): the session starts empty. The
agent calls `load_domain` and `search_domains` to traverse the catalog,
`search_artifacts` to query it, and `load_artifact` to materialize one selected
artifact together with its bundled files. The agent's context window stays small
even when the catalog holds thousands of entries. This path requires a server.

**Up-front sync** (the `podium sync` path): one-shot or watch-mode
materialization of the caller's effective view, or of the subset an active
profile selects, onto disk before the session starts. The harness then uses its
own native discovery under `.cursor/rules/`, `.claude/agents/`, and the other
directories it reads. This path works against a server or a local directory.

Both paths share the same registry, identity providers, layer
composition, and harness adapters.

---

## Extensibility

Podium's behavior is pluggable via SPIs covering storage, identity,
composition, signing, audit, layer source, and delivery. Plugins
compile into a registry build today. The SPIs are designed to
be wire-compatible with a future out-of-process plugin protocol.
See [Deployment → Extending](../deployment/extending) for the constraints
that make that transition source-compatible.

---

## What's next

The next page, [How it works](how-it-works), shows how these pieces
fit together: the architecture, the deployment tiers, where state
lives, and what runs on a developer machine versus on a server.
