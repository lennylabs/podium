---
title: Selective materialization
nav_order: 2
description: Sync a subset of the catalog into a workspace with include, exclude, and type filters, and save scopes as named profiles.
---

# Selective materialization

This page explains how to make `podium sync` write a chosen subset of the
catalog into a workspace. It is for developers and teams whose workspaces need
only part of what a registry holds.

The subset is a **scope**: a list of include globs, a list of exclude globs, and
a list of artifact types. A **profile** is a named scope stored in `sync.yaml`,
so a workspace can switch between scopes by name. Without a scope, `podium sync`
materializes the caller's whole [effective view](../reference/glossary).

Selective materialization decides which artifacts land in the workspace.
[Configure your harness](configure-your-harness) covers the format they land in.

---

## Scope

A scope narrows the caller's effective view before anything is written. Its
parts apply in this order:

1. `include` keeps only artifacts whose canonical ID matches at least one
   pattern. An empty include list keeps everything.
2. `exclude` drops artifacts whose canonical ID matches a pattern. Exclude runs
   after include, so an exclude pattern wins over an include pattern that
   matches the same ID.
3. `type` keeps only the listed artifact types.

A scope selects whole artifacts. A selected artifact brings the files bundled in
its directory, and the scope has no separate filter for them.
[Bundled files](configure-your-harness#bundled-files) covers where each harness
writes them.

### Pattern syntax

Patterns match the canonical artifact ID, which is the artifact's directory path
under the registry root (`finance/close-reporting/run-variance-analysis`). The
glob syntax is the following.

| Pattern | Matches |
|:--|:--|
| `*` | Exactly one path segment. |
| `**` | Zero or more segments in the middle of a pattern. A trailing `**` requires at least one segment, so `finance/**` matches artifacts under `finance` and does not match a bare `finance`. |
| `{a,b}` | Brace alternation. Braces do not nest. |

A `DOMAIN.md` `include:` list uses a separate matcher. That matcher reads a
trailing `**` as matching the bare prefix as well, which is the one difference
between the two syntaxes.

### Removal of out-of-scope files

A scope that resolves to fewer artifacts than the previous run also removes the
files the previous run wrote. Every sync reconciles the target against
`<target>/.podium/sync.lock`, deletes the paths that are no longer in scope, and
prunes the directories those paths leave empty.

The lock records a `file_hash` for each file sync writes whole: the SHA-256
digest of the file's bytes, written as `sha256:<hex>`. Sync deletes an
out-of-scope file only when its current bytes still match that hash. A file
edited after sync wrote it therefore stays in place when its artifact leaves the
scope, and sync names it on standard error. A target committed to Git changes
bytes the same way when Git converts line endings on checkout, for example
under `core.autocrlf=true`, so a stale file in such a checkout also stays in
place and is named. A shared config file that sync merges entries into, such as
`.mcp.json`, `.claude/settings.json`, or `AGENTS.md`, records no `file_hash`.
Sync never deletes such a file; it removes Podium's entries from it and leaves
the rest in place.

The removal treats the lock as untrusted, because a committed lock can come from
anyone who pushes to the repository. Sync skips a lock entry, names it on
standard error, and leaves it out of the next lock in these cases:

- The path leaves the target, names `.git`, or names Podium's own state under
  `.podium/`.
- The path reaches a symbolic link that resolves outside the target.
- The path names a symbolic link, a directory, or another file that is not a
  regular file.
- The entry records no `file_hash`, as an entry written by Podium 0.7.0 or
  earlier does.
- The file's current bytes do not match the entry's `file_hash`, or sync cannot
  read the file.

A skipped file stays on disk. Because the next lock omits its entry, later syncs
neither delete it nor name it again. A `kind: marketplace` render fails instead
when it would keep a file for one of the last two reasons; see
[Reconciliation](../deployment/content/publishing#reconciliation).

A forged lock entry cannot delete a file whose bytes its author cannot predict,
such as a file with local edits. A file with predictable content, such as an
empty file, a `.gitkeep`, or a standard license text, has a predictable hash,
so a forged entry can name it whether or not Git tracks it.

A directory that is a symbolic link to another directory inside the target is
followed, as it is when sync writes. A shared config file that is a symbolic
link to another file inside the target, such as `GEMINI.md` linked to
`AGENTS.md`, is updated through the link, and the link stays. Sync creates the
linked file when it does not exist yet and its directory already exists inside
the target. Sync never deletes
a directory with its contents, and it removes a parent directory only while the
directory is empty.

The lock lives in `<target>/.podium/`, which must be a directory. When `.podium`
is a symbolic link, for example one that shares `sync.yaml` across Git
worktrees, `podium sync` fails before it writes anything, and the error names
`.podium`. Replace the link with a directory that holds its own `sync.yaml` and
`sync.lock`.

A `sync.lock` that is a symbolic link is read when it names another file inside
`.podium/`. A lock that sync cannot read, such as a link to a file outside the
target, is named on standard error, and the run proceeds as if no prior lock
existed, so the files the earlier run wrote are not cleaned up.

---

## Sync a subset from the command line

Pass the scope to `podium sync` as flags:

```bash
# Only the finance domain.
podium sync --include 'finance/**'

# Two domains, minus one subtree.
podium sync --include 'finance/**' --include 'platform/observability/**' \
            --exclude 'finance/legacy/**'

# Only skills and rules, anywhere in the catalog.
podium sync --type skill,rule

# Skills under one domain.
podium sync --include 'finance/**' --type skill
```

`--include` and `--exclude` are repeatable and each takes one glob. `--type`
takes a comma-separated list. Quote the patterns so the shell does not expand
them.

Add `--dry-run` to print the resolved artifact set without writing anything:

```bash
podium sync --include 'finance/**' --dry-run
```

Against a server registry, `podium sync --preview` prints aggregate counts for
the caller's whole effective view: the visible layers, the artifact count, the
count by type, and the count by sensitivity. It writes nothing. It does not
apply the scope, so it reports what the identity can see across the whole
catalog.

The registry serves the preview, which limits where `--preview` runs:

- Against a filesystem registry, it exits with an error.
- A tenant that turns off `expose_scope_preview` refuses it.

---

## Profiles

A profile is a named scope in `sync.yaml`. Define profiles under the
`profiles:` key:

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
      - rule

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

A CLI list replaces the profile's corresponding list. It does not append to it.
`podium sync --profile finance-team --include 'platform/**'` materializes
`platform/**` with the profile's `exclude` and `type` lists still applied, and
the profile's `include` list unused. To extend a profile's scope permanently,
edit the profile.

### Where profiles live

`podium sync` merges the following configuration files, listed from lowest to
highest precedence:

| Scope | Path |
|:--|:--|
| User-global | `~/.podium/sync.yaml` |
| Project-shared | `<workspace>/.podium/sync.yaml` |
| Project-local | `<workspace>/.podium/sync.local.yaml` |

The workspace is the nearest ancestor directory holding a `.podium/` directory,
found the way `git` finds `.git`.

- Profiles from all the files form one set.
- A profile defined in more than one file is replaced whole by the
  highest-precedence definition. Invoking it prints a warning naming the files
  that defined it.
- `podium init` writes the project-shared file, and `podium init --global`
  writes the user-global one.

Commit the project-shared file to share profiles with teammates. Keep
machine-specific choices in `sync.local.yaml`, which `podium init` adds to
`.gitignore`.

---

## Choosing between a flag and a profile

| Situation | Use |
|:--|:--|
| A one-off run, a scripted CI step, or an experiment. | `--include`, `--exclude`, and `--type` flags. |
| A scope a workspace uses repeatedly, or one teammates should share. | A profile in the project-shared `sync.yaml`. |
| Switching between scopes several times a day. | Several profiles, selected with `--profile`. |
| Adding or dropping one artifact for the rest of the session. | `podium sync override`, described below. |
| Several scopes materialized in one command, each into its own directory. | A `targets:` list, described below. |

---

## Capturing and editing profiles

### Save the current scope as a profile

`podium sync save-as` writes the target's current scope into `sync.yaml` as a
profile. An arrangement reached with flags and toggles then becomes reusable:

```bash
podium sync --include 'finance/**' --exclude 'finance/legacy/**'
podium sync save-as --profile finance-team
```

The saved profile becomes the target's active profile. The target's ephemeral
toggles are folded into the profile's include and exclude lists and then
cleared.

- `save-as` refuses to overwrite an existing profile unless `--update` is
  passed.
- `--dry-run` prints the proposed profile without writing.

### Edit a profile

`podium profile edit` changes a profile's patterns in place:

```bash
podium profile edit finance-team --add-include 'platform/observability/**'
podium profile edit finance-team --remove-exclude 'finance/legacy/**'
```

- `--add-include`, `--remove-include`, `--add-exclude`, and `--remove-exclude`
  are each repeatable.
- `podium profile edit <name>` with no pattern flags opens an interactive
  checklist for that profile.
- Edits rewrite only the sequences they touch, so comments and formatting
  elsewhere in `sync.yaml` survive.
- `--dry-run` shows the result without writing.

---

## Temporary toggles

`podium sync override` adds or drops individual artifacts on top of the resolved
scope without editing `sync.yaml`:

```bash
podium sync override --add finance/ap/pay-invoice
podium sync override --remove platform/observability/trace-a-request
podium sync override --reset
```

Each toggle is recorded in `<target>/.podium/sync.lock`.

- `--add` materializes the artifact immediately, through the active harness
  adapter.
- `--remove` deletes the artifact's files, through the active harness adapter.
- Toggles survive watch-mode events. The next manual `podium sync` clears them.
- `podium sync override` with no flags opens an interactive checklist over the
  effective view.

---

## Several scopes in one run

A `targets:` list materializes more than one scope in a single command. Each
entry carries its own directory, harness, and scope, and each writes its own lock
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
or do both. An inline list replaces the profile's corresponding list, as a CLI
flag does.

Entries default to `kind: workspace`, which materializes the project-files
layout a harness reads. The other kind, `kind: marketplace`, renders a git-repo
distribution and is covered in [Publishing marketplace repositories](../deployment/content/publishing).

---

## Checking a scope before it writes

| Command | What it reports |
|:--|:--|
| `podium sync --dry-run` | The artifact set the current scope resolves to. Writes nothing. |
| `podium sync --check` | Validation warnings for the merged configuration: profile references that resolve to nothing, malformed globs, duplicate target ids, and profiles defined in more than one file. |
| `podium sync --preview` | Aggregate counts for the caller's whole effective view, from a server registry. The scope is not applied. |
| `podium sync --json` | The resolved run as a structured envelope, including `.profile`, `.scope.include`, and the per-artifact list. |

---

## Related pages

- [Configure your harness](configure-your-harness) for the format each harness receives.
- [Browsing the catalog](browsing-the-catalog) for the runtime path, where an agent selects artifacts one at a time instead of declaring a scope up front.
- [CLI reference](../reference/cli#sync-and-materialization) for every flag on `podium sync`, `podium sync override`, `podium sync save-as`, and `podium profile edit`.
