---
title: Local
nav_order: 1
description: The tier with no server-side deployment. A folder of artifacts, the podium CLI, and user-driven sync. Fits any team or individual whose catalog does not require access control or progressive discovery.
---

# Local

The local tier has no server-side deployment. The catalog is a directory tree on disk, and `podium sync` reads it directly and writes harness-native files. Suitable for any team or individual whose catalog does not require access control or progressive discovery. Covers solo work, evaluation, prototypes, CI build steps, and team-shared catalogs in a Git repo.

---

## What's running

The only running component is the `podium` CLI. There is no server process, no database, and no identity provider.

`podium sync` runs the same shared Go library functions the server would run behind its HTTP API: parsers, glob resolver, layer composer, `extends:` resolver, harness adapters, and atomic materialization. `podium lint` runs the same ingest lint rules the server applies. The library is the single behavioral surface across the tiers, so moving to a server preserves output.

---

## What works

- **Layer composition** across the registry's layer subdirectories plus the workspace overlay. See [Layered composition](layers).
- **Materialization** through the configured harness adapter.
- **Profiles and scoping.** `--include`, `--exclude`, and `--type` narrow the materialized set, `podium sync save-as --profile <name>` captures the current set as a named profile in `sync.yaml`, and `podium sync --profile <name>` replays it.
- **Lock file** at `<target>/.podium/sync.lock`. `podium sync override` and `podium sync save-as` work the same way as they do against a server.
- **Watch mode** via `podium sync --watch`. Uses fsnotify to re-materialize on registry-folder or overlay changes.
- **Multi-user via a shared directory.** Commit the registry folder to git (or share it via a network share or a sync service); every developer runs `podium sync` against their copy. The shared git history doubles as the audit trail.

---

## What a filesystem-source catalog does not do

- The Podium MCP server has no HTTP API to back it. Run a server for runtime discovery.
- The language SDKs speak HTTP only. Run a server for them.
- The SDK-backed read CLI (`podium search`, `podium domain show`, `podium domain search`, and `podium artifact show`) is unavailable for the same reason.
- Outbound webhooks are unavailable.
- Identity-based visibility filtering does not run. The visibility evaluator short-circuits to `true` for every layer. `visibility:` declarations stay in layer config so artifacts remain portable to server-backed tiers, and they are not enforced at request time. See [Access control](access-control).
- `podium login` has no authentication to perform.

When any of these capabilities is required, see [Single node](single-node) or [Clustered](clustered).

---

## Setup

```bash
# Pick a directory for the catalog
mkdir -p ~/podium-artifacts/personal

# In the consuming project, point Podium at the directory and pick a harness
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code
```

`podium init` writes `<workspace>/.podium/sync.yaml` with the registry path and harness as defaults. Commit `.podium/sync.yaml` to share defaults with teammates; commit `.gitignore` entries that the init step adds for `.podium/sync.local.yaml` and `.podium/overlay/`.

For a per-developer config that follows you across projects, use `podium init --global` instead; it writes `~/.podium/sync.yaml`.

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
│   │       └── run-variance-analysis/   # type: skill — SKILL.md + ARTIFACT.md
│   │           ├── SKILL.md
│   │           └── ARTIFACT.md
│   └── platform/
│       └── …
└── personal/                   # another layer
    └── …
```

Each subdirectory of `<registry-path>` is treated as a `local`-source layer. Layer IDs default to the subdirectory name. Layer order is alphabetical by subdirectory name unless overridden by `layer_order:` in `.registry-config`. The file is YAML:

```yaml
# <registry-path>/.registry-config
multi_layer: true        # required; opts the directory into filesystem-registry mode
layer_order:             # optional; lowest-precedence first
  - team-shared
  - personal
```

When `.registry-config` is absent (or sets `multi_layer: false`), the directory is treated as a single-layer setup instead: one `local`-source layer rooted at the path. The same dispatch applies whether the consumer is `podium sync` reading the filesystem source or `podium serve --standalone --layer-path` pointed at the same directory.

A layer directory may also hold an optional `.layer-config` file declaring that layer's visibility (`public`, `organization`, `groups`, or `users`). In multi-layer mode the file sits inside each layer subdirectory; in single-layer mode it sits at `<registry-path>/.layer-config`:

```yaml
# <registry-path>/team-shared/.layer-config
visibility:
  groups: [acme-finance]
```

`podium sync` ignores it, because filesystem-source visibility is bypassed. It takes effect once a server serves the same directory through `podium serve --standalone --layer-path`: a layer with a non-empty `visibility:` block boots with that visibility, and a layer with no `.layer-config` or an empty block takes the deployment default (`PODIUM_DEFAULT_LAYER_VISIBILITY`). See [Access control](access-control).

The workspace local overlay (`<workspace>/.podium/overlay/`) sits on top of the filesystem-registry layers, at the same precedence it has against a server.

---

## Multi-user via a shared directory

The registry is files. Sharing across developers means sharing the directory through a normal file-distribution path. Common choices:

- **Committed to git.** The registry directory is a git repo (or part of one); every developer who clones has the same catalog. Authoring goes through git PR and merge. Each developer's `git pull` is their ingest; the git history doubles as the audit trail. There is no shared-state coordination and no runtime conflicts.
- **Network share or sync service.** Dropbox, iCloud, OneDrive, an NFS mount. Works; less audit signal than git.
- **Periodic rsync.** A scheduled pull from a canonical location.

Per-project clone vs shared local clone:

- **Per-project clone.** Each consuming project has its own `.podium/registry/` (cloned from the shared repo, or vendored). Self-contained; the project repo carries everything.
- **Shared local clone.** Every developer clones the registry repo once into `~/podium-artifacts/`, and every consuming project's `<workspace>/.podium/sync.yaml` points at that path. Saves disk space; updates via `git pull`.

Either works. Per-project is simpler; shared local is lighter when the registry is large.

---

## Authoring loop

```bash
# Create or edit a skill artifact in the registry (skills have both SKILL.md and ARTIFACT.md)
$EDITOR ~/podium-artifacts/team-shared/finance/close-reporting/run-variance-analysis/SKILL.md
$EDITOR ~/podium-artifacts/team-shared/finance/close-reporting/run-variance-analysis/ARTIFACT.md

# In the consuming project
cd ~/projects/your-project
podium sync                      # one-shot
# or
podium sync --watch              # re-materialize on every save
```

Lint catches the common authoring errors. Run it before commit:

```bash
podium lint --registry ~/podium-artifacts/
```

When the catalog is shared via git, CI on the registry repo runs `podium lint` as a required check on PRs, so issues are caught before merge.

---

## Migration paths

**To single node.** When the local tier no longer fits, typically because runtime discovery via the MCP server, identity-based visibility filtering, or a single audit log for the team is needed, point a server at the same directory:

```bash
podium serve --standalone --layer-path ~/podium-artifacts/
```

Each developer's `<workspace>/.podium/sync.yaml` switches `defaults.registry` from the path to `http://podium.your-team.example` (or wherever the server lives). The directory layout and authoring loop are unchanged, and the consumer paths gain MCP and SDK support. See [Single node](single-node).

The shared library does the same parsing, composition, and adapter work in both tiers, so output is bit-identical for the same target and profile.

**To clustered.** When multi-tenancy, SCIM group sync, signing with a transparency log, or production availability is required, follow [Clustered](clustered) and use `podium admin migrate-to-standard` to export the single-node state.

---

## Limits worth knowing

- Authoring rights for the catalog belong to whoever can write to the directory. Branch protection on a Git repo is the typical control. That statement is about writing content into a catalog directory the CLI already reads. Which caller may declare a layer naming a host path is a question only a server-backed tier answers, and [Layers](layers#who-may-register-a-local-source-layer) covers it.
- Visibility declarations in layer config are recorded and not enforced. Artifacts remain portable to server-backed tiers.
- Audit is the git history (when committed to git) or whatever the sharing mechanism preserves. There is no Podium-side audit stream.
- Freeze windows and signing enforcement require a server.
