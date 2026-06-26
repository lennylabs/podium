---
layout: default
title: publish.yaml
parent: Reference
nav_order: 6
description: The publish.yaml config schema that drives podium publish: defaults, marketplaces, workflows, and the injected variables.
---

# publish.yaml

`publish.yaml` is the operator-authored client config that drives `podium publish` (§7.8). It lives beside `sync.yaml` under `.podium/` and resolves across the same three scopes with the same precedence as `sync.yaml`. This page is the schema reference; for the model and the worked examples, see [Marketplace publishing](../consuming/publishing).

## Resolution

`publish.yaml` resolves across three scopes, lowest precedence first:

| Scope | Path |
|:--|:--|
| User-global | `~/.podium/publish.yaml` |
| Project-shared | `<workspace>/.podium/publish.yaml` (committed) |
| Project-local | `<workspace>/.podium/publish.local.yaml` (gitignored) |

The `defaults` block merges per key: a higher-precedence non-empty value wins. The `marketplaces` list is replaced as a whole: the highest-precedence scope that declares a non-empty `marketplaces:` replaces the entire list. `podium publish --config <path>` reads one file directly and skips the scope merge. The registry resolves by the same ladder `sync.yaml` uses: `PODIUM_REGISTRY` wins over `defaults.registry`.

## Top-level keys

```yaml
defaults: { ... }
marketplaces: [ ... ]
```

## `defaults`

| Key | Type | Meaning |
|:--|:--|:--|
| `registry` | string | Registry URL the render reads the effective view from. `PODIUM_REGISTRY` overrides it. |
| `identity` | string | The effective-view principal the render runs as. The published marketplace reflects this principal's visibility (§4.6). |
| `workflow` | object | The default `prepare` and `publish` command lists each marketplace inherits unless it declares its own. |

## `marketplaces`

Each entry is one output.

| Key | Type | Meaning |
|:--|:--|:--|
| `id` | string | The output identifier, selected by `--output <id>`. |
| `git.remote` | string | The remote URL the workflow clones and pushes. Injected as `$PODIUM_GIT_REMOTE`. |
| `git.branch` | string | The branch the output writes. Injected as `$PODIUM_GIT_BRANCH`. |
| `harnesses` | list of string | The harness set. Each must be a publish target (Claude surfaces, `codex`, `cursor`, `gemini`, `pi`, `hermes`). Naming `opencode` or `none` fails validation with `config.invalid`. |
| `commit_message` | string | A Go template rendered with `{{.ChangedCount}}` and `{{.Timestamp}}` into `$PODIUM_COMMIT_MESSAGE`. |
| `plugins` | list | The plugin list. Each plugin is a `name` plus a scope filter. |
| `workflow` | object | Optional. Replaces the default workflow for this output in full. |

### `plugins`

A plugin is a named bundle of selected artifacts. The scope filter reuses the `sync.yaml` selection syntax over canonical artifact IDs.

| Key | Type | Meaning |
|:--|:--|:--|
| `name` | string | The plugin name. The vendor manifest keys the plugin entry by this name, contributed once per plugin. |
| `include` | list of glob | Artifact IDs to include. |
| `exclude` | list of glob | Artifact IDs to exclude. Applied after `include`. |
| `type` | list of string | Restrict the plugin to these artifact types. |

The pipeline assigns each selected artifact to its plugin by evaluating the plugin filters in declaration order.

## `workflow`

A workflow groups the `prepare` and `publish` command lists `podium publish` runs around the render phase. A marketplace that declares `workflow` replaces the default workflow for that output in full.

| Key | Type | Meaning |
|:--|:--|:--|
| `prepare` | list of command | Commands that place a checkout of the destination repository at the working directory. |
| `publish` | list of command | Commands that take the rendered tree to the remote. |
| `prepare_on_error` | list of command | Optional cleanup run when a `prepare` command fails, before the failure propagates. |
| `publish_on_error` | list of command | Optional cleanup run when a `publish` command fails, before the failure propagates. |

### Command

A command is an argv list under `run:` (executed directly without a shell) or a string under `sh:` (executed through `sh -c`). Declare exactly one of the two.

| Key | Type | Meaning |
|:--|:--|:--|
| `run` | list of string | Argv to exec directly. No shell, no injection. |
| `sh` | string | A shell command line run through `sh -c`. |
| `skip_if_no_changes` | bool | Skip this command when the render produced no diff against the checkout. |
| `continue_on_error` | bool | Let the pipeline proceed past a non-zero exit. |
| `timeout` | duration string | Bound the command's wall-clock duration, such as `"30s"` or `"5m"`. A bare integer is rejected so the unit is always explicit. |

## Injected variables

`podium publish` adds these variables to the ambient environment of every command:

| Variable | Meaning |
|:--|:--|
| `$PODIUM_WORKDIR` | The per-output working and checkout directory. |
| `$PODIUM_OUTPUT_ID` | The marketplace output identifier. |
| `$PODIUM_GIT_REMOTE`, `$PODIUM_GIT_BRANCH` | From the output's `git:` block. |
| `$PODIUM_COMMIT_MESSAGE` | Rendered from `commit_message`. |
| `$PODIUM_CHANGED` | `true` when the render produced a diff against the checkout, else `false`. |
| `$PODIUM_CHANGE_SUMMARY` | A path to a JSON file describing the changed artifacts. |
| `$PODIUM_REGISTRY`, `$PODIUM_IDENTITY`, `$PODIUM_HARNESSES` | The registry URL, the publishing identity, and the comma-joined harness set. |

## Example

```yaml
# .podium/publish.yaml
defaults:
  registry: https://podium.acme.com
  identity: publisher@acme.com
  workflow:
    prepare:
      - run: ["git", "clone", "--branch", "$PODIUM_GIT_BRANCH", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
    publish:
      - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
      - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
        skip_if_no_changes: true
      - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]

marketplaces:
  - id: acme-agents
    git:
      remote: git@github.com:acme/agent-marketplace.git
      branch: main
    harnesses: [claude-code, codex, cursor]
    commit_message: "Sync Podium catalog ({{.ChangedCount}} changes) {{.Timestamp}}"
    plugins:
      - name: finance-pack
        include: ["finance/**"]
        exclude: ["finance/experimental/**"]
        type: [skill, command, rule]
```
