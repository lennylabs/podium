---
title: Local
nav_order: 1
description: The deployment mode with no server. A directory of artifacts, the podium CLI, and user-driven sync, for any team or individual whose catalog does not require access control or progressive discovery.
---

# Local

The Local deployment mode has no server-side component. The catalog is a directory tree on disk, and `podium sync` reads it directly and writes harness-native files. Local fits any team or individual whose catalog does not require access control or progressive discovery. Typical uses are solo work, evaluation, prototypes, CI build steps, and team-shared catalogs in a Git repository.

---

## What's running

The only running component is the `podium` CLI. There is no server process, no database, and no identity provider.

`podium sync` runs the same shared Go library functions the server runs behind its HTTP API: the parsers, the glob resolver, the layer composer, the harness adapters, and atomic materialization. `podium lint` runs the same ingest lint rules the server applies. The library is the single behavioral surface across the deployment modes, so moving to a server preserves output.

---

## What works

- **Layer composition** across the registry's layer subdirectories plus the workspace overlay. See [Layered composition](layers).
- **Materialization** through the configured harness adapter.
- **Profiles and scoping.** `--include`, `--exclude`, and `--type` narrow the materialized set. `podium sync save-as --profile <name>` captures the current set as a named profile in `sync.yaml`, and `podium sync --profile <name>` replays it.
- **Lock file** at `<target>/.podium/sync.lock`. `podium sync override` and `podium sync save-as` work the same way as they do against a server.
- **Watch mode** through `podium sync --watch`, which uses fsnotify to re-materialize on registry-folder or overlay changes.
- **Multi-user use through a shared directory.** Commit the registry folder to Git, or share it through a network share or a sync service, and each developer runs `podium sync` against their copy. The shared Git history doubles as the audit trail.

---

## What a filesystem-source catalog does not do

- The Podium MCP server has no HTTP API to back it. Run a server for runtime discovery.
- The language SDKs speak HTTP only. Run a server for them.
- The SDK-backed read CLI (`podium search`, `podium domain show`, `podium domain search`, and `podium artifact show`) is unavailable for the same reason.
- Outbound webhooks are unavailable.
- Identity-based visibility filtering does not run, because the visibility evaluator short-circuits to `true` for every layer. `visibility:` declarations stay in layer config so artifacts remain portable to server-backed deployment modes, and they are not enforced at request time. See [Access control](access-control).
- `podium login` has no authentication to perform.

When any of these capabilities is required, see [Single node](single-node) or [Clustered](clustered).

---

## Setup

Create a directory for the catalog, then point the consuming project at it:

```bash
# Pick a directory for the catalog
mkdir -p ~/podium-artifacts/personal

# In the consuming project, point Podium at the directory and pick a harness
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code
```

`podium init` writes `<workspace>/.podium/sync.yaml` with the registry path and the harness as defaults.

- Commit `.podium/sync.yaml` to share the defaults with teammates.
- Commit the `.gitignore` entries the init step adds for `.podium/sync.local.yaml` and `.podium/overlay/`.

For a per-developer config that applies across projects, run `podium init --global` instead. It writes `~/.podium/sync.yaml`.

---

## Directory layout

A filesystem registry rooted at `<registry-path>` is a directory of layer directories:

```
<registry-path>/
├── .registry-config            # required; opts the directory into filesystem-registry mode
├── team-shared/                # one layer
│   ├── .layer-config           # optional; per-layer visibility
│   ├── DOMAIN.md
│   ├── finance/
│   │   └── close-reporting/
│   │       └── run-variance-analysis/   # type: skill — SKILL.md, with an optional ARTIFACT.md
│   │           ├── SKILL.md
│   │           └── ARTIFACT.md
│   └── platform/
│       └── …
└── personal/                   # another layer
    └── …
```

[Skills that declare no version](#skills-that-declare-no-version) states the version each deployment mode records for a skill whose `SKILL.md` declares none.

### Layers and their order

Each subdirectory of `<registry-path>` is a `local`-source layer, and the layer ID defaults to the subdirectory name. Layer order is alphabetical by subdirectory name unless `layer_order:` in `.registry-config` overrides it. The file is YAML:

```yaml
# <registry-path>/.registry-config
multi_layer: true        # required; opts the directory into filesystem-registry mode
layer_order:             # optional; lowest-precedence first
  - team-shared
  - personal
```

When `.registry-config` is absent, or sets `multi_layer: false`, the directory is a single-layer setup instead: one `local`-source layer rooted at the path. The same dispatch applies whether the consumer is `podium sync` reading the filesystem source or `podium serve --standalone --layer-path` pointed at the same directory.

### Per-layer visibility

A layer directory may hold an optional `.layer-config` file that declares the layer's visibility (`public`, `organization`, `groups`, or `users`). In multi-layer mode the file sits inside each layer subdirectory. In single-layer mode it sits at `<registry-path>/.layer-config`.

```yaml
# <registry-path>/team-shared/.layer-config
visibility:
  groups: [acme-finance]
```

`podium sync` ignores the file, because filesystem-source visibility is bypassed. The file takes effect once a server serves the same directory through `podium serve --standalone --layer-path`. A layer with a non-empty `visibility:` block boots with that visibility. A layer with no `.layer-config`, or an empty block, takes the deployment default (`PODIUM_DEFAULT_LAYER_VISIBILITY`). See [Access control](access-control).

### Overlay and collisions

The workspace local overlay (`<workspace>/.podium/overlay/`) sits on top of the filesystem-registry layers, at the same precedence it has against a server. An overlay artifact replaces a registry artifact with the same ID without a collision error; see [Workspace local overlay](layers#workspace-local-overlay). A collision between two registry layers fails `podium sync` instead; see [Collisions](layers#collisions).

---

## Multi-user via a shared directory

The registry is a set of files, so sharing it across developers means sharing the directory through a normal file-distribution path. Common choices:

| Method | How it works |
|:--|:--|
| Committed to Git | The registry directory is a Git repository, or part of one, and every developer who clones it has the same catalog. Authoring goes through a pull request and a merge. Each developer's `git pull` is their ingest, and the Git history doubles as the audit trail. There is no shared-state coordination and there are no runtime conflicts. |
| Network share or sync service | Dropbox, iCloud, OneDrive, or an NFS mount. This works, with less audit signal than Git. |
| Periodic rsync | A scheduled pull from a canonical location. |

A Git-hosted registry can live in each project or in one shared clone:

| Layout | How it works |
|:--|:--|
| Per-project clone | Each consuming project has its own `.podium/registry/`, cloned from the shared repository or vendored. The project repository carries everything. |
| Shared local clone | Each developer clones the registry repository once into `~/podium-artifacts/`, and every consuming project's `<workspace>/.podium/sync.yaml` points at that path. This saves disk space, and `git pull` updates every project. |

Either layout works. Per-project is simpler, and a shared local clone is lighter when the registry is large.

---

## Authoring loop

Edit artifacts in the registry directory, then sync the consuming project:

```bash
# Create or edit a skill artifact in the registry
$EDITOR ~/podium-artifacts/team-shared/finance/close-reporting/run-variance-analysis/SKILL.md
# Edit the optional ARTIFACT.md overlay when the skill carries Podium-only fields
$EDITOR ~/podium-artifacts/team-shared/finance/close-reporting/run-variance-analysis/ARTIFACT.md

# In the consuming project
cd ~/projects/your-project
podium sync                      # one-shot
# or
podium sync --watch              # re-materialize on every save
```

Lint catches the common authoring errors. Run it before each commit:

```bash
podium lint --registry ~/podium-artifacts/
```

When the catalog is shared through Git, run `podium lint` in CI on the registry repository as a required check on pull requests, so issues are caught before merge.

---

## Migration paths

### To single node

Single node becomes necessary when the team needs runtime discovery through the MCP server, identity-based visibility filtering, or a single audit log. Point a server at the same directory:

```bash
podium serve --standalone --layer-path ~/podium-artifacts/
```

Each developer's `<workspace>/.podium/sync.yaml` then switches `defaults.registry` from the path to `http://podium.your-team.example`, or wherever the server lives. The directory layout and the authoring loop are unchanged, and the consumer paths gain MCP and SDK support. See [Single node](single-node) and [Connecting layer sources](layer-sources).

The shared library does the same parsing, composition, and adapter work in both deployment modes, so output is bit-identical for the same target and profile. Skills that declare no version are the exception that [Skills that declare no version](#skills-that-declare-no-version) describes.

### To clustered

When production availability or Postgres-backed tenant isolation is required, follow [Clustered](clustered) and use `podium admin migrate-to-standard` to export the single-node state.

### Skills that declare no version

A filesystem source records a skill that declares no version as `0.0.0-auto.0`. A registry ingesting the same directory for the first time assigns the same value. It assigns a higher N once it has ingested changed bytes for the skill, so the lock-file versions then differ between the deployment modes. While the registry serves the directory's current bytes as the skill's latest row, the content hash and every harness's output stay identical.

A revert is the case where output differs. A registry rejects a change of such a skill back to content it already stored under another version, and it keeps serving its newer version, while a filesystem source serves the reverted content. After a revert, the content hash, the body, and every harness's output differ between the deployment modes until the author sets `metadata.version` in `SKILL.md` or changes the content.

---

## Limits worth knowing

- Authoring rights for the catalog belong to whoever can write to the directory. Branch protection on a Git repository is the typical control. That statement covers writing content into a catalog directory the CLI already reads. Which caller may declare a layer naming a host path is a question only a server-backed deployment mode answers, and [Who may register a local-source layer](layer-sources#who-may-register-a-local-source-layer) covers it.
- Visibility declarations in layer config are recorded and not enforced. Artifacts remain portable to server-backed deployment modes.
- Audit is the Git history, when the catalog is committed to Git, or whatever the sharing mechanism preserves. There is no Podium-side audit stream.
- Freeze windows and signing enforcement require a server.
