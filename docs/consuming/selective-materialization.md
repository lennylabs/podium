---
title: Selective materialization
nav_order: 2
description: Sync a subset of the catalog into a workspace with include, exclude, and type filters, and define named profiles to switch between scopes.
---

# Selective materialization

This page documents selective materialization. A catalog holds more than any single
workspace needs, and `podium sync` materializes a declared subset of it rather
than the whole effective view. The subset is a **scope**: a list of include
globs, a list of exclude globs, and a list of artifact types. A named **profile**
stores a scope in `sync.yaml` so a workspace can switch between scopes by name.

Cross-harness delivery decides which format the files land in and is covered in
[Configure your harness](configure-your-harness). Selective materialization
decides which artifacts land at all.

---

## Scope

A scope narrows the caller's effective view before anything is written. It has
three parts, applied in this order:

1. `include` keeps only artifacts whose canonical ID matches at least one
   pattern. An empty include list keeps everything.
2. `exclude` drops artifacts whose canonical ID matches a pattern. Exclude runs
   after include, so an exclude pattern wins over an include pattern that
   matches the same ID.
3. `type` keeps only the listed artifact types.

Patterns match the canonical artifact ID, which is the artifact's directory path
under the registry root (`finance/close-reporting/run-variance-analysis`). The
glob syntax is the following. A `DOMAIN.md` `include:` list uses a separate
matcher that reads a trailing `**` as matching the bare prefix as well, which is
the one difference between them.

| Pattern | Matches |
|:--|:--|
| `*` | Exactly one path segment. |
| `**` | Zero or more segments in the middle of a pattern. A trailing `**` requires at least one segment, so `finance/**` matches artifacts under `finance` and does not match a bare `finance`. |
| `{a,b}` | Brace alternation. Braces do not nest. |

A scope that resolves to fewer artifacts than the previous run also removes the
files the previous run wrote. Every sync reconciles the target against
`<target>/.podium/sync.lock`, deletes the paths that are no longer in scope, and
prunes the directories those paths leave empty.

---

## Sync a subset from the command line

`podium sync` takes the scope directly as repeatable flags:

```bash
# Only the finance domain.
podium sync --include 'finance/**'

# Two domains, minus one subtree.
podium sync --include 'finance/**' --include 'platform/observability/**' \
            --exclude 'finance/legacy/**'

# Only skills and commands, anywhere in the catalog.
podium sync --type skill,command

# Skills under one domain.
podium sync --include 'finance/**' --type skill
```

`--include` and `--exclude` are repeatable and each takes one glob. `--type`
takes a comma-separated list. Quote the patterns so the shell does not expand
them.

Add `--dry-run` to print the resolved artifact set and write nothing:

```bash
podium sync --include 'finance/**' --dry-run
```

Against a server registry, `podium sync --preview` prints aggregate counts for
the caller's whole effective view: the visible layers, the artifact count, the
count by type, and the count by sensitivity. It writes nothing and it does not
apply the scope, so it reports what the identity can see across the whole
catalog. The registry serves the preview, so `--preview` against a
filesystem registry exits with an error, and a tenant that turns off
`expose_scope_preview` refuses it.

---

## Profiles

A profile is a named scope in `sync.yaml`. Define one under `profiles:`:

```yaml
defaults:
  registry: https://podium.acme.com
  harness: claude-code
  profile: finance-team

profiles:
  finance-team:
    include:
      - "finance/**"
      - "platform/observability/**"
    exclude:
      - "finance/legacy/**"
    type:
      - skill
      - command

  rules-only:
    type:
      - rule

  demo:
    include:
      - "personal/hello/**"
    target: /Users/alice/demos/podium-walkthrough
    harness: cursor
```

Each profile accepts these keys:

| Key | Effect |
|:--|:--|
| `include` | Include globs over canonical artifact IDs. |
| `exclude` | Exclude globs, applied after `include`. |
| `type` | Artifact types to keep. |
| `target` | Destination directory for runs under this profile. |
| `harness` | Harness adapter for runs under this profile. |
| `min_server_version` | Minimum `podium` version required to run this profile. An older binary refuses the run with `config.server_version_too_old`. |

### Selecting a profile

Pass `--profile` to select one for a single run:

```bash
podium sync --profile finance-team
```

Set `defaults.profile` to make one profile the standing choice for the
workspace. The configuration above does this for `finance-team`, so a run
without `--profile` uses it.

An explicit `--profile` naming a profile that no file defines is an error, and
the run stops. A stale `defaults.profile` is ignored instead, so a workspace
whose default profile was deleted still syncs its full effective view.

### Flags override a profile per list

A CLI list replaces the profile's corresponding list rather than appending to
it. `podium sync --profile finance-team --include 'platform/**'` materializes
`platform/**` with the profile's `exclude` and `type` lists still applied, and
the profile's `include` list unused. To extend a profile's scope permanently,
edit the profile.

### Where profiles live

`podium sync` merges three configuration files, listed here from lowest to
highest precedence:

| Scope | Path |
|:--|:--|
| User-global | `~/.podium/sync.yaml` |
| Project-shared | `<workspace>/.podium/sync.yaml` |
| Project-local | `<workspace>/.podium/sync.local.yaml` |

The workspace is the nearest ancestor directory holding a `.podium/` directory,
found the way `git` finds `.git`. Profiles from all three files form one set. A
profile defined in more than one file is replaced whole by the
highest-precedence definition, and invoking it prints a warning naming the files
that defined it. `podium init` writes the project-shared file, and
`podium init --global` writes the user-global one. Commit the project-shared
file to share profiles with teammates, and keep machine-specific choices in
`sync.local.yaml`, which `podium init` adds to `.gitignore`.

---

## Choosing between a flag and a profile

| Situation | Reach for |
|:--|:--|
| A one-off run, a scripted CI step, or an experiment. | `--include`, `--exclude`, and `--type` flags. |
| A scope a workspace uses repeatedly, or one teammates should share. | A profile in the project-shared `sync.yaml`. |
| Switching between scopes several times a day. | Several profiles, selected with `--profile`. |
| Adding or dropping one artifact for the rest of the session. | `podium sync override`, described below. |
| Several scopes materialized in one command, each into its own directory. | A `targets:` list, described below. |

---

## Capturing and editing profiles

`podium sync save-as` writes the target's current scope into `sync.yaml` as a
profile, so an arrangement reached with flags and toggles becomes reusable:

```bash
podium sync --include 'finance/**' --exclude 'finance/legacy/**'
podium sync save-as --profile finance-team
```

The saved profile becomes the target's active profile, and the target's
ephemeral toggles are folded into its include and exclude lists and then
cleared. `save-as` refuses to overwrite an existing profile unless `--update` is
passed, and `--dry-run` prints the proposed profile without writing.

`podium profile edit` changes a profile's patterns in place:

```bash
podium profile edit finance-team --add-include 'platform/observability/**'
podium profile edit finance-team --remove-exclude 'finance/legacy/**'
```

`--add-include`, `--remove-include`, `--add-exclude`, and `--remove-exclude` are
each repeatable. Running `podium profile edit <name>` with no pattern flags
opens an interactive checklist for that profile. Edits rewrite only the
sequences they touch, so comments and formatting elsewhere in `sync.yaml`
survive. Add `--dry-run` to see the result without writing.

---

## Temporary toggles

`podium sync override` adds or drops individual artifacts on top of the resolved
scope without editing `sync.yaml`:

```bash
podium sync override --add finance/ap/pay-invoice
podium sync override --remove platform/observability/trace-a-request
podium sync override --reset
```

Each toggle is recorded in `<target>/.podium/sync.lock`. An `--add` materializes
the artifact immediately and a `--remove` deletes its files, both through the
active harness adapter. Toggles survive watch-mode events and are cleared by the
next manual `podium sync`. Running `podium sync override` with no flags opens an
interactive checklist over the effective view.

---

## Several scopes in one run

A `targets:` list materializes more than one scope in a single command. Each
entry carries its own directory, harness, and scope, and writes its own lock
file:

```yaml
defaults:
  registry: https://podium.acme.com

targets:
  - id: web-app
    harness: claude-code
    target: ./apps/web
    profile: finance-team

  - id: etl-service
    harness: cursor
    target: ./services/etl
    include:
      - "platform/**"
    type:
      - rule
```

Give each entry its own `target` directory. A target writes its lock to
`<target>/.podium/sync.lock`, so two entries pointed at one directory overwrite
each other's record of what they materialized.

Run the list with `--config`:

```bash
podium sync --config .podium/sync.yaml
```

An entry may name a `profile`, declare `include`, `exclude`, and `type` inline,
or do both. An inline list replaces the profile's corresponding list, matching
the CLI behavior above. Entries default to `kind: workspace`, which materializes
the project-files layout a harness reads. The other kind, `kind: marketplace`,
renders a git-repo distribution and is covered in
[Marketplace publishing](publishing).

---

## Checking a scope before it writes

| Command | What it reports |
|:--|:--|
| `podium sync --dry-run` | The artifact set the current scope resolves to. Writes nothing. |
| `podium sync --check` | Validation warnings for the merged configuration: profile references that resolve to nothing, malformed globs, duplicate target ids, and profiles defined in more than one file. |
| `podium sync --preview` | Aggregate counts for the caller's whole effective view, from a server registry. The scope is not applied. |
| `podium sync --json` | The resolved run as a structured envelope, including `.profile`, `.scope.include`, and the per-artifact list. |

---

## Bundled resources follow the artifact

Selecting an artifact selects its directory. When a scope keeps
`finance/close-reporting/run-variance-analysis`, the scripts, references, and
assets bundled in that directory are materialized alongside its manifest, in
the layout the harness adapter assigns them. A `rule` or `mcp-server` artifact
is the exception: a harness adapter renders it as a single file, an injected
block, or a config-merge fragment, so files bundled in those directories are
not written. The canonical layout (`--harness none`) writes them under
`<destination>/<artifact-id>/`. There is no separate filter for bundled files,
and no way for a scope to keep a manifest while dropping its
dependencies. [Bundled resources](../authoring/bundled-resources) covers what
ships and where each harness puts it.

---

## Related pages

- [Configure your harness](configure-your-harness) for the format each harness receives.
- [Browsing the catalog](browsing-the-catalog) for the runtime path, where an agent selects artifacts one at a time instead of declaring a scope up front.
- [CLI reference](../reference/cli#sync-and-materialization) for every flag on `podium sync`, `podium sync override`, `podium sync save-as`, and `podium profile edit`.
