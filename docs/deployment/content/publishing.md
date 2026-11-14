---
title: Publishing marketplace repositories
nav_order: 4
description: Render the catalog into harness-native marketplace repositories with a kind marketplace sync target, and push them to git remotes with podium sync.
---

# Publishing marketplace repositories

This page describes how to publish the catalog as harness-native git repositories, such as plugin marketplaces, extensions, packages, and taps. It is for operators who distribute artifacts to users through a harness's own install path rather than through `podium sync` on each workstation.

A `podium sync` target of `kind: marketplace` renders the catalog into a git-repo distribution and runs an operator-configured workflow that commits and pushes it. A harness then imports the published repository through its own install path.

---

## How publishing works

Publishing is a derived output downstream of the registry. A marketplace target reads the effective view over the same HTTP API as the other consumers, renders the git-repo distribution layout, and runs a workflow that pushes the result to a git remote. The published repository is not an authoring source, and the registry remains the source of truth.

A marketplace target and a workspace target render the same effective view into different outputs:

| Target kind | Output | Consumer |
|:--|:--|:--|
| `kind: workspace` (the default) | A workspace tree in the project-files layout. | The harness reads the tree directly. |
| `kind: marketplace` | A git-repo distribution layout. | The harness imports the repository through its install path. |

### The publish pipeline

An operator adds one or more `kind: marketplace` entries to the `targets:` list in `sync.yaml` and runs `podium sync --config <path>`. For each marketplace target, Podium runs a fixed pipeline:

```
prepare (operator commands)  ->  render (Podium)  ->  publish (operator commands)
```

| Phase | Owner | Action |
|:--|:--|:--|
| `prepare` | Operator commands | Places a checkout of the destination repository at the target directory. The common case is a `git clone`. |
| `render` | Podium | Materializes each harness's distribution tree into the target directory through the materialization writer and the per-harness emitters. |
| `publish` | Operator commands | Takes the rendered tree to the remote. The common case is `git add`, `git commit`, and `git push`. |

The checkout precedes the render so that the render reconciles against the existing repository content. The commit follows the render. Podium owns rendering, and the operator's commands own the git interaction: getting the repository to the target directory and getting the result to the remote. Podium embeds no git library and defines no write-side git SPI.

### Plugins, harness sets, and repositories

- A **plugin** is a named bundle of selected artifacts. A scope filter (`include`, `exclude`, and `type`) over canonical artifact IDs defines it. The filter reuses the `podium sync` scope machinery, so plugin selection and sync selection apply identical glob semantics.
- A plugin is the cross-harness unit. It renders into each harness's distribution layout, in that harness's subtree.
- A **harness set** is the list of harnesses a target publishes for. Each listed harness contributes its format's manifest and its content subtree to the output repository. Harnesses that share a format contribute one shared manifest.
- Each marketplace target renders into one repository. The harness set is the grouping control: an operator who cannot share one repository across two formats declares two targets.

### Supported harnesses

A harness with a git-repo distribution is a publish target.

| Harness | Publish target |
|:--|:--|
| Claude (Code, Desktop, Cowork) | Yes. |
| Codex | Yes. |
| Cursor | Yes. |
| Gemini | Yes. |
| Pi | Yes. |
| Hermes | Yes. |
| OpenCode | No. OpenCode distributes through npm only. |
| `none` | No. It produces raw canonical output. |

Config validation rejects a marketplace target whose harness set names OpenCode or `none` with `config.invalid`.

### Reconciliation

Re-rendering a marketplace target is idempotent. The materialization writer and the sync lock file remove the files of artifacts that left the view. The JSON manifests merge with the Podium-owned merge tag, so stale entries drop out.

The git diff after a render is the catalog delta. When the delta is empty, `skip_if_no_changes` suppresses an empty commit. A second `podium sync --config` against an unchanged catalog therefore produces no second commit.

---

## Publishing identity and the effective view

The published marketplace contains the publishing identity's effective view, intersected with the plugin scope filters. A principal that can see restricted layers renders them into the output, so the publishing identity is a security-relevant setting. Publish a public marketplace under an identity scoped to the artifacts intended for it. [Access control](access-control) describes how the registry composes an effective view.

A marketplace target declares the principal it publishes as in its `identity` field. When the target sets no `identity`, it inherits `defaults.identity`. This default applies only to marketplace targets: a `kind: workspace` target renders under the caller's own identity and carries no identity field. Because the identity lives in the same `sync.yaml` as the workflow, the effective-view rule stays enforceable from that file.

The credential determines what the render contains:

- **Server-source registry.** The registry credential carries the publishing identity, and `podium sync` renders that credential's effective view. The credential is `PODIUM_TOKEN` when set, otherwise the read-CLI session token or the `podium login` keychain token.
- **The `identity` field.** The field is documentary. It records the principal the target publishes as, but the render reflects the authenticated token. Set the credential to the principal whose visibility the marketplace is meant to reflect.
- **Filesystem-source registry.** There is no authenticated principal, so the render reads the local view directly.

---

## Publish a marketplace repository

### Before you start

- A registry source: a Podium server URL or a filesystem path.
- For a server-source registry, a registry credential for the publishing identity. See [Publishing identity and the effective view](#publishing-identity-and-the-effective-view).
- For a server-source registry, the registry's verification key in `PODIUM_SIGNATURE_VERIFY_KEY`. Against a registry with signing off, set `PODIUM_VERIFY_SIGNATURES=never` instead. A run whose targets name a server registry verifies every record it loads.
- A destination git repository, and git credentials in the environment of `podium sync`, such as `SSH_AUTH_SOCK` or `GH_TOKEN`. Podium never holds the git push credential.

### Steps

1. Choose the publishing identity, and set the registry credential to that principal.

2. Add a `kind: marketplace` entry to the `targets:` list in `sync.yaml`. Set the destination repository in `git`, the harness set in `harnesses`, the plugins in `plugins`, and the `prepare` and `publish` commands in `workflow`. The following example declares two targets, because a Gemini extension occupies a whole repository:

```yaml
# .podium/sync.yaml
defaults:
  registry: https://podium.acme.com
  identity: publisher@acme.com          # the effective-view principal a marketplace target renders as

targets:
  - id: acme-agents
    kind: marketplace
    target: ./build/acme-agents          # the working directory the checkout lands in
    git:
      remote: git@github.com:acme/agent-marketplace.git
      branch: main
    harnesses: [claude-code, codex, cursor]   # three formats that coexist at distinct root paths
    commit_message: "Sync Podium catalog ({{.ChangedCount}} changes) {{.Timestamp}}"
    plugins:
      - name: finance-pack
        include: ["finance/**"]
        exclude: ["finance/experimental/**"]
        type: [skill, command, rule]
      - name: security-baseline
        include: ["security/baseline/**"]
    workflow:
      prepare:
        - run: ["git", "clone", "--branch", "$PODIUM_GIT_BRANCH", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
      publish:
        - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
        - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
          skip_if_no_changes: true
        - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]

  - id: acme-gemini
    kind: marketplace
    target: ./build/acme-gemini
    git:
      remote: git@github.com:acme/gemini-extension.git
      branch: main
    harnesses: [gemini]                        # one extension per repo, so its own target
    plugins:
      - name: house-rules
        include: ["rules/**"]
    workflow:
      prepare:
        - run: ["git", "clone", "--branch", "$PODIUM_GIT_BRANCH", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
      publish:
        - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
        - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
          skip_if_no_changes: true
        - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]
```

3. Validate the configuration with `podium sync --config <path> --check`. The check renders and runs nothing.

4. Preview the run with `podium sync --config <path> --dry-run`. For a marketplace target, Podium renders into a temporary directory, prints each command with its variables substituted, and runs no publish phase.

5. Run the publish:

```bash
podium sync --config .podium/sync.yaml
```

Podium runs the `prepare`, `render`, and `publish` pipeline for each marketplace target. With the example workflow, the remote branch receives a commit when the catalog changed. A successful run exits 0.

6. Import the repository in each harness through its install path. [Configure your harness](../../consuming/configure-your-harness) lists the per-harness distribution paths and install commands.

---

## Automating publishing

A marketplace target runs from an operator CLI or from a CI job. It has no watch mode.

A server-side publisher inside the registry process is out of scope. It would require storing per-repository push credentials and running operator-supplied commands inside a multi-tenant process.

### Triggering on layer ingests

The CI trigger is the `layer.ingested` webhook event. The registry emits it once per completed layer ingest cycle, so one source commit that changes many artifacts yields one event. A CI job subscribed to `layer.ingested` runs `podium sync --config <path>`, so one source commit triggers one publish across the artifacts it changed.

A `layer.ingested` event for a layer that contributes nothing to a target produces a render with no diff. `skip_if_no_changes` suppresses the commit, so an unrelated layer update produces no empty commit.

### GitHub Actions

A GitHub Actions deployment uses one of the following patterns. GitHub starts a workflow from an external system only through the authenticated REST API (`repository_dispatch` or `workflow_dispatch`). A Podium webhook receiver posts an HMAC-signed event body that GitHub's dispatch endpoint does not accept, so a direct webhook cannot start the workflow.

| Pattern | Trigger | Webhook receiver |
|:--|:--|:--|
| A: scheduled | A cron schedule in the marketplace repository. | None. |
| B: event-driven relay | `repository_dispatch`, sent by a relay that receives `layer.ingested`. | Required, plus a relay. |

In both patterns, the registry credential `PODIUM_TOKEN` carries the publishing identity, and the git push credential belongs to GitHub. Pattern A uses `GITHUB_TOKEN`. A workflow that runs outside the marketplace repository uses a deploy key in its `prepare` clone.

#### Pattern A: scheduled

A workflow in the marketplace repository runs `podium sync --config <path>` on a cron schedule. `skip_if_no_changes` makes an empty run a no-op, so a poll every 5 to 15 minutes is inexpensive.

1. Add the workflow to the marketplace repository:

```yaml
# .github/workflows/publish.yml in acme/agent-marketplace
on:
  schedule: [{ cron: "*/15 * * * *" }]
  workflow_dispatch: {}
permissions:
  contents: write                # GITHUB_TOKEN pushes to this repo; no deploy key needed
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: curl -fsSL https://podium.acme.com/install.sh | sh
      - env:
          PODIUM_REGISTRY: ${{ secrets.PODIUM_REGISTRY }}
          PODIUM_TOKEN:    ${{ secrets.PODIUM_TOKEN }}   # the publishing identity's registry credential
          PODIUM_SIGNATURE_VERIFY_KEY: ${{ secrets.PODIUM_SIGNATURE_VERIFY_KEY }}
        run: podium sync --config .podium/sync.yaml
```

2. Set the target's `workflow` to configure git against the existing checkout. `actions/checkout` has already placed the repository and authenticated `origin`, so the `prepare` phase does not clone:

```yaml
# .podium/sync.yaml, acme-agents target
workflow:
  prepare:
    - run: ["git", "-C", "$PODIUM_WORKDIR", "config", "user.name",  "podium-bot"]
    - run: ["git", "-C", "$PODIUM_WORKDIR", "config", "user.email", "podium-bot@acme.com"]
  publish:
    - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
    - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
      skip_if_no_changes: true
    - run: ["git", "-C", "$PODIUM_WORKDIR", "push"]
```

#### Pattern B: event-driven relay

A Podium webhook receiver filtered to `layer.ingested` posts to a small relay. The relay verifies the HMAC and calls GitHub `repository_dispatch`, which the workflow listens for. The relay bridges the HMAC-signed receiver body and the body the dispatch endpoint accepts.

1. Register the receiver with a credential that carries the per-tenant admin role. The response carries the HMAC secret for the relay.

```bash
# register the receiver; the response carries the HMAC secret for the relay.
# $PODIUM_TOKEN must carry the per-tenant admin role; the receiver CRUD
# endpoints reject a non-admin caller with auth.forbidden.
curl -X POST https://podium.acme.com/v1/webhooks -H "Authorization: Bearer $PODIUM_TOKEN" \
  -d '{"url":"https://relay.acme.com/podium","event_filter":["layer.ingested"],"debounce":"30s"}'
```

The receiver `url` must use `https` and must resolve to a public address. The registry rejects a loopback, link-local, or private target with `registry.invalid_argument`, unless the host or CIDR is listed in the registry's `PODIUM_WEBHOOK_ALLOWED_TARGETS` allowlist. An in-cluster relay needs an allowlist entry. The `debounce` field sets a trailing window, so a burst of `layer.ingested` events coalesces into one batch delivery and one CI dispatch.

2. Deploy the relay. The relay verifies the HMAC against the receiver secret, then calls `POST https://api.github.com/repos/acme/agent-marketplace/dispatches` with `Authorization: Bearer <token>` and `{"event_type":"podium-layer-ingested"}`.

3. Add the dispatch trigger to the workflow:

```yaml
# add to the workflow triggers
on:
  repository_dispatch: { types: [podium-layer-ingested] }
  workflow_dispatch: {}
```

On dispatch, the workflow runs `podium sync --config <path>`.

[HTTP API → Outbound webhooks](../../reference/http-api#outbound-webhooks) describes the receiver fields and the batch envelope. [Extending → Webhook-driven integrations](../operations/outbound-webhooks#webhook-driven-integrations) also describes the relay pattern.

---

## Configuration reference

A marketplace target is one entry under `targets:` in `sync.yaml`, alongside any `kind: workspace` targets. The `kind:` field selects the output format: `workspace` (the default) materializes the project-files layout, and `marketplace` renders the git-repo distribution layout. The full `sync.yaml` schema is in [Configure your harness](../../consuming/configure-your-harness) and [Reference → CLI](../../reference/cli#podium-sync).

### Marketplace target fields

| Key | Meaning |
|:--|:--|
| `kind` | `marketplace` selects the git-repo distribution output. |
| `target` | The working directory the `prepare` checkout lands in and the render writes into. |
| `git.remote` | The remote URL the workflow clones and pushes. Injected as `$PODIUM_GIT_REMOTE`. |
| `git.branch` | The branch the target writes. Injected as `$PODIUM_GIT_BRANCH`. |
| `harnesses` | The harness set. Each must be a publish target. |
| `commit_message` | A Go template rendered with the change count and timestamp into `$PODIUM_COMMIT_MESSAGE`. |
| `identity` | The effective-view principal the render runs as. Inherited from `defaults.identity` when unset. See [Publishing identity](#publishing-identity-and-the-effective-view). |
| `plugins` | The plugin list: a `name`, an optional `description` carried into the per-plugin manifest and the root marketplace entry, and a scope filter (`include`, `exclude`, and `type`). |
| `workflow` | The `prepare` and `publish` command lists Podium runs around the render. A `kind: workspace` target accepts `workflow` too, as [Workflows on workspace targets](#workflows-on-workspace-targets) describes. |

The marketplace fields (`harnesses`, `git`, `commit_message`, `identity`, and `plugins`) are valid only on a `kind: marketplace` entry. `workflow` is valid on either kind.

A marketplace entry rejects the following keys with `config.invalid`:

- The workspace target's `harness` key.
- The workspace scope fields: `profile`, `include`, `exclude`, and `type`.
- The watch mode.

### Workflow commands

A command takes one of the following forms:

| Form | Execution |
|:--|:--|
| An argv list under `run:` | Executed directly, with no shell and no injection. |
| A string under `sh:` | Executed through `sh -c`. Use it for pipes. |

Per-command flags control failure handling:

| Flag | Effect |
|:--|:--|
| `skip_if_no_changes` | Skip the command when `$PODIUM_CHANGED` is `false`. See [Injected variables](#injected-variables). |
| `continue_on_error` | Let the pipeline proceed past a non-zero exit. |
| `timeout` | Bound the command's wall-clock duration. Takes a duration string such as `"30s"`. |

The pipeline fails fast on the first non-zero exit, except where `continue_on_error` is set.

Each phase also accepts an optional cleanup list, `prepare_on_error` or `publish_on_error`. Podium runs the cleanup list when that phase fails, before the failure propagates. A cleanup list receives the injected variables of the phase it cleans up, so `publish_on_error` sees `$PODIUM_CHANGED` and `prepare_on_error` does not.

The pipeline inherits the ambient environment of the `podium sync` process and adds the injected variables. Git authentication therefore relies on the ambient `SSH_AUTH_SOCK`, `GH_TOKEN`, and similar variables. An injected variable replaces an ambient variable of the same name. A variable Podium does not inject for a phase keeps its ambient value.

### Workflows on workspace targets

A `kind: workspace` target accepts a `workflow`. `podium sync --config` runs its `prepare` phase before materialization and its `publish` phase after. The workspace workflow receives `$PODIUM_WORKDIR`, `$PODIUM_REGISTRY`, and `$PODIUM_TARGET_ID`, which is the workspace counterpart of `$PODIUM_OUTPUT_ID`. Its `publish` phase also receives `$PODIUM_CHANGED`. [Injected variables](#injected-variables) defines each variable.

### Injected variables

Podium passes context to the commands through environment variables and does not interpret git state.

| Variable | Meaning |
|:--|:--|
| `$PODIUM_WORKDIR` | For a marketplace target, the per-target working and checkout directory. For a `kind: workspace` target, the target directory made absolute against the working directory of `podium sync`. |
| `$PODIUM_OUTPUT_ID` | The marketplace target identifier. |
| `$PODIUM_TARGET_ID` | The `kind: workspace` target's `id`. A workspace target receives it in place of `$PODIUM_OUTPUT_ID`. |
| `$PODIUM_GIT_REMOTE`, `$PODIUM_GIT_BRANCH` | From the target's `git:` block. |
| `$PODIUM_COMMIT_MESSAGE` | Rendered from `commit_message` with the change count and timestamp. |
| `$PODIUM_CHANGED` | `true` when materialization changed the bytes on disk of a file Podium writes or removes, and `false` otherwise. For a marketplace target, the value equals whether the render produced a diff against the checkout. A `kind: workspace` target receives the variable with the same meaning in its `publish` phase. [Edge cases](#edge-cases) lists the details of the comparison. |
| `$PODIUM_CHANGE_SUMMARY` | A path to a JSON file describing the changed artifacts. |
| `$PODIUM_REGISTRY` | The registry source: the `--registry` flag, else the `PODIUM_REGISTRY` environment variable, else `defaults.registry`. The value is a URL or a filesystem path. A `kind: workspace` target receives it. |
| `$PODIUM_IDENTITY` | The publishing identity. Marketplace targets only. |
| `$PODIUM_HARNESSES` | The comma-separated harness set. Marketplace targets only. |

### `podium sync` options

```
podium sync --config <path> [--dry-run] [--check] [--json]
```

`podium sync --config <path>` reads one `sync.yaml` and runs each `targets:` entry. A `kind: workspace` target materializes the project-files layout, and a `kind: marketplace` target runs the `prepare`, `render`, and `publish` pipeline. The marketplace fields have no single-target CLI-flag equivalent, so a marketplace target runs only through the `--config` path.

| Flag | Effect |
|:--|:--|
| `--config <path>` | Read this `sync.yaml` and run each `targets:` entry. |
| `--dry-run` | For a marketplace target, render into a temporary directory and print each command with variables substituted, and run no publish phase. For a `kind: workspace` target, resolve the artifact set without writing, and run and print no workflow command. |
| `--check` | Validate the config only; render and run nothing. |
| `--json` | Emit a structured JSON envelope on stdout. |

### Exit codes

| Exit code | Meaning |
|:--|:--|
| 0 | The run succeeded. |
| 1 | A config-load failure, such as a malformed or unreadable config file. Or a runtime failure: a fetch error, a delivery-check refusal (`materialize.content_hash_mismatch`, `materialize.signature_invalid`, or `materialize.signature_missing`), a render error, or a non-zero workflow command. |
| 2 | A flag error or a config error: a `config.invalid` config, a missing `--config` path, an unset registry (`config.no_registry`), or `config.signature_provider_unavailable` when no usable verification key resolves under the `always` policy. |

---

## Repository layout

A marketplace target renders into one repository. For the `acme-agents` target in the example, with the harness set `[claude-code, codex, cursor]`, the repository contains the following tree:

```
acme-agents/
  .claude-plugin/marketplace.json        # Claude Code, Desktop, Cowork; references ./claude/<plugin>
  .agents/plugins/marketplace.json       # Codex; references ./codex/<plugin>
  .cursor-plugin/marketplace.json        # Cursor; references ./cursor/<plugin>
  claude/finance-pack/.claude-plugin/plugin.json + skills/ agents/ commands/ hooks/ .mcp.json
  codex/finance-pack/.codex-plugin/plugin.json   + skills/ hooks/ .mcp.json
  cursor/finance-pack/.cursor-plugin/plugin.json + skills/ rules/ mcp.json
  claude/security-baseline/...  codex/security-baseline/...  cursor/security-baseline/...
```

Each vendor manifest lists the same plugin set and points its entries at that vendor's subtree. The plugin content is per-harness, because the per-plugin manifest filenames and the rule and MCP conventions differ across vendors. Each vendor manifest carries one entry per plugin, keyed by the plugin name, so a plugin of any size yields one entry.

| Format | Root manifest | Per-plugin manifest | Components |
|:--|:--|:--|:--|
| Claude (Code, Desktop, Cowork) | `.claude-plugin/marketplace.json` | `.claude-plugin/plugin.json` | `skills/`, `agents/`, `commands/`, `hooks/hooks.json`, `.mcp.json` |
| Codex | `.agents/plugins/marketplace.json` | `.codex-plugin/plugin.json` | `skills/`, `hooks/hooks.json`, `.mcp.json` |
| Cursor | `.cursor-plugin/marketplace.json` | `.cursor-plugin/plugin.json` | `skills/`, `rules/*.mdc`, `mcp.json` |
| Gemini | `gemini-extension.json` (one per repository) | n/a | `commands/*.toml`, context file, `mcpServers` |
| Pi | root `package.json` (`pi-package` keyword, `pi.skills` array) | n/a | `skills/<name>/SKILL.md` |
| Hermes | none (skills discovered under root `skills/`) | n/a | `skills/<name>/SKILL.md` with `references/`, `scripts/`, `assets/` |

### Sharing a repository between formats

The Claude, Codex, and Cursor manifests sit at distinct fixed locations and coexist in one repository. Each harness reads only its own manifest. A Gemini target occupies the whole repository, because Gemini allows one extension per repository. A Hermes tap defaults to a root `skills/` directory. Gemini and Hermes therefore each take their own target.

### How manifests merge

The Claude, Codex, and Cursor JSON manifests merge with the Podium-owned merge tag, so a stale plugin entry drops out on re-render. The Hermes tap and the Pi skills subtree carry no merged manifest, and they reconcile through the sync lock file.

### The `owner` object

The Claude and Cursor manifests carry the root `owner` object those formats require. Both formats describe `owner.name` as the maintainer display name the harness shows, and both vendors publish their own marketplaces with an organization name in that field. Podium sets `owner.name` from the target `id` on every render, so the identifier `acme-agents` appears where the vendor examples carry an organization name. The Codex manifest carries no `owner`, because the Codex format defines none.

---

## Trust boundary

Workflow commands run as subprocesses with the operator's privileges and ambient credentials. They have the same trust boundary as a Makefile or a CI script:

- A `workflow` executes only on the `podium sync` CLI path. It never runs inside the registry server or the MCP server.
- The commands live on `sync.yaml` target entries, and the project-shared scope of `sync.yaml` is committed to git.
- The commands are unrelated to the `MaterializationHook` SPI, which is sandboxed to forbid subprocesses, network access, and writes outside the destination.
- The commands are unrelated to the `hook` artifact type.

A server-side publisher is out of scope.

---

## Edge cases

- **Ephemeral overrides.** `podium sync override` does not apply to a marketplace target. That command operates on a single target directory's lock file and renders the project-files layout, so it does not reach a marketplace entry.
- **Existing checkouts.** By default, the `prepare` phase clones into the target directory. A `prepare` phase can instead configure git against an existing checkout, such as one from a CI checkout step. In that case it configures git or pulls rather than clones.
- **The `$PODIUM_CHANGED` comparison.** The comparison starts after the `prepare` phase, so a file that `prepare` clones or pulls is part of the starting state. A file Podium restores after a hand edit or deletion counts as a change. A file Podium writes or removes that the run cannot read also reads `true`, such as a materialized file without read permission, or a stale path from an earlier run that the operator replaced with a directory. The sync lock file is not compared.
- **Hand edits to `owner`.** A hand-edited `owner.name` in the repository is replaced on the next publish. An `owner.email` or `owner.url` added by hand survives the merge.
- **Relay without `debounce`.** With `debounce` unset on the receiver, the relay fires once per event, and the workflow's own concurrency control collapses the redundant runs.

---

## Related pages

- [Configure your harness](../../consuming/configure-your-harness): the per-harness distribution paths and install commands.
- [Reference → CLI](../../reference/cli#podium-sync): the `podium sync` command surface.
- [HTTP API → Outbound webhooks](../../reference/http-api#outbound-webhooks): the webhook receiver fields and the batch envelope.
