---
layout: default
title: Solo / filesystem
parent: Deployment
nav_order: 1
description: The lightest Podium setup — a folder of artifacts, the podium CLI, no daemon. For individual developers, prototypes, and CI build steps.
---

# Solo / filesystem

The lightest Podium shape. The catalog is a directory tree on disk; `podium sync` reads it directly and writes harness-native files. No daemon, no port, no auth. Suitable for solo work, evaluation, prototypes, CI build steps, and small teams that share the catalog via a Git repo.

---

## What's running

Just the `podium` CLI. No server process. No database. No identity provider.

`podium sync` runs the same shared Go library functions the server would run behind its HTTP API: parsers, glob resolver, layer composer, `extends:` resolver, harness adapters, lint rules, atomic materialization. The library is the single behavioral surface across deployment shapes; migration between shapes preserves output.

---

## What works

- **Layer composition** across the registry's layer subdirectories plus the workspace overlay.
- **Materialization** through the configured harness adapter.
- **Lock file** at `<target>/.podium/sync.lock`. `podium sync override` and `podium sync save-as` work the same way as in server mode.
- **Watch mode** via `podium sync --watch`. Uses fsnotify to re-materialize on registry-folder or overlay changes.
- **Multi-user via a shared directory.** Commit the registry folder to git (or share it via a network share / sync service); every developer runs `podium sync` against their copy. The shared git history doubles as the audit trail.

---

## What doesn't work in this shape

- The Podium MCP server (no HTTP API to back it). Use the standalone server shape if you want runtime discovery.
- The language SDKs (HTTP-only). Use the standalone server shape.
- The read CLI (`podium search`, `podium domain show`, `podium domain search`, `podium artifact show`) — SDK-backed.
- Outbound webhooks.
- Identity-based visibility filtering. The visibility evaluator short-circuits to `true` for every layer. `visibility:` declarations stay in layer config (artifacts remain portable to server-source deployments) but are not enforced at request time.
- `podium login`. There's no auth to perform.

When you need any of these, see [Small team](small-team) (standalone server) or [Organization](organization) (standard deployment).

---

## Setup

```bash
# Pick a directory for the catalog
mkdir -p ~/podium-artifacts/personal

# In your project, point Podium at the directory and pick a harness
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
├── team-shared/                # one layer
│   ├── DOMAIN.md
│   ├── finance/
│   │   └── close-reporting/
│   │       └── run-variance-analysis/
│   │           └── ARTIFACT.md
│   └── platform/
│       └── …
├── personal/                   # another layer
│   └── …
└── .layer-order                # optional; controls layer ordering
```

Each subdirectory of `<registry-path>` is treated as a `local`-source layer. Layer IDs default to the subdirectory name; layer order is alphabetical by name. An optional `<registry-path>/.layer-order` file overrides the order — one layer ID per line, lowest precedence to highest.

The workspace local overlay (`<workspace>/.podium/overlay/`) sits on top of the filesystem-registry layers, exactly as in server mode.

---

## Multi-user via a shared directory

The registry is just files. Sharing across developers means sharing the directory however you'd share any folder. Common choices:

- **Committed to git.** The registry directory is a git repo (or part of one); every developer who clones has the same catalog. Authoring goes through git PR + merge. Each developer's `git pull` is their ingest; the git history doubles as the audit trail. No shared-state coordination, no runtime conflicts.
- **Network share or sync service.** Dropbox, iCloud, OneDrive, an NFS mount. Works; less audit signal than git.
- **Periodic rsync.** A scheduled pull from a canonical location.

Per-project clone vs shared local clone:

- **Per-project clone.** Each consuming project has its own `.podium/registry/` (cloned from the shared repo, or vendored). Self-contained; the project repo carries everything.
- **Shared local clone.** Every developer clones the registry repo once into `~/podium-artifacts/`, and every consuming project's `<workspace>/.podium/sync.yaml` points at that path. Saves disk space; updates via `git pull`.

Either works. Per-project is simpler; shared local is lighter when the registry is large.

---

## Authoring loop

```bash
# Create or edit an artifact in the registry
$EDITOR ~/podium-artifacts/team-shared/finance/close-reporting/run-variance-analysis/ARTIFACT.md

# In your project
cd ~/projects/your-project
podium sync                      # one-shot
# or
podium sync --watch              # re-materialize on every save
```

Lint catches the common authoring errors. Run it before commit:

```bash
podium lint ~/podium-artifacts/team-shared/finance/close-reporting/run-variance-analysis/
```

When the catalog is shared via git, CI on the registry repo runs `podium lint` as a required check on PRs, so issues are caught before merge.

---

## Migration paths

**Adding a server in front of the same directory.** When you outgrow filesystem mode (typically because you want runtime discovery via the MCP server, or a single audit log for the team), point a standalone server at the same directory:

```bash
podium serve --standalone --layer-path ~/podium-artifacts/
```

Each developer's `<workspace>/.podium/sync.yaml` switches `defaults.registry` from the path to `http://podium.your-team.example` (or wherever the server lives). The directory layout and authoring loop are unchanged; the consumer paths gain MCP and SDK support.

The shared library does the same parsing, composition, and adapter work in both shapes, so output is bit-identical for the same target and profile.

**To a standard deployment.** When you need OIDC identity-based visibility, multi-tenancy, or production availability, follow [Small team](small-team) (or [Organization](organization)) and use `podium admin migrate-to-standard` to export the standalone state.

---

## Limits worth knowing

- Authoring rights for the catalog are whoever can write to the directory. Branch protection on a Git repo is the typical control.
- Visibility declarations in layer config are recorded but not enforced (artifacts remain portable to server deployments).
- Audit is the git history (when committed to git) or whatever the sharing mechanism preserves; there's no Podium-side audit stream.
- SBOM ingestion, CVE tracking, freeze windows, signing enforcement — all available only in server shapes.
