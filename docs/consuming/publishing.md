---
layout: default
title: Marketplace publishing
parent: Consuming
nav_order: 5
description: Render the catalog into harness-native marketplace repositories and push them to git remotes with podium publish.
---

# Marketplace publishing

`podium publish` renders the catalog into harness-native git-repo distributions (plugin marketplaces, extensions, packages, and taps) and runs an operator-configured workflow to commit and push them (§7.8). A harness imports the published repository through its own install path.

Publishing is a derived, served output downstream of the registry. It reads the effective view over the same HTTP API as the other consumers, then renders and pushes to a git remote rather than writing a workspace tree like `podium sync`. Publishing a repository does not make it an authoring source; the registry remains the source of truth.

A harness with a git-repo distribution is a publish target: Claude (Code, Desktop, Cowork), Codex, Cursor, Gemini, Pi, and Hermes. OpenCode (npm only) and `none` (raw canonical output) have no git-repo distribution and are not publish targets. An output whose harness set names OpenCode or `none` is rejected at config validation with `config.invalid`.

---

## The publish model

A marketplace output is a named publishing destination: a git repository, a set of harnesses, a list of plugins, and a workflow. An operator declares one or more outputs in `publish.yaml` and runs `podium publish`. For each output, Podium runs a fixed pipeline:

```
prepare (operator commands)  ->  render (Podium)  ->  publish (operator commands)
```

- **prepare** places a checkout of the destination repository at the working directory. The common case is a `git clone`.
- **render** materializes each harness's distribution tree into the working directory through the materialization writer and the per-harness emitters.
- **publish** takes the rendered tree to the remote. The common case is `git add`, `git commit`, and `git push`.

The phases exist because the checkout must precede the render so the render reconciles against existing repository content, and the commit must follow it. The git interaction is configuration: Podium owns rendering, and the operator's commands own getting the repository to the working directory and the result to the remote. There is no embedded git library and no write-side git SPI.

### Plugins, harness sets, and repositories

- A **plugin** is a named bundle of selected artifacts, defined by a scope filter (`include`, `exclude`, `type`) over canonical artifact IDs. The filter reuses the `podium sync` scope machinery, so plugin selection and sync selection apply identical glob semantics. A plugin is the cross-harness unit: it renders into each harness's distribution layout in that harness's subtree.
- A **harness set** is the harnesses an output publishes for. Each listed harness contributes its format's manifest and its content subtree to the output repository, and harnesses that share a format contribute one shared manifest.
- Each marketplace output renders into one repository. The harness set is the grouping lever: an operator who cannot share one repository across two formats declares two outputs.

---

## `publish.yaml`

`publish.yaml` is an operator-authored client config that lives beside `sync.yaml` under `.podium/`. It uses the same three-scope resolution (`~/.podium/publish.yaml`, `<workspace>/.podium/publish.yaml`, `<workspace>/.podium/publish.local.yaml`) and the same precedence rules as `sync.yaml`. Its top-level keys are `defaults` and `marketplaces`.

```yaml
# .podium/publish.yaml
defaults:
  registry: https://podium.acme.com
  identity: publisher@acme.com          # the effective-view principal the render runs as
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
    harnesses: [claude-code, codex, cursor]   # three formats that coexist at distinct root paths
    commit_message: "Sync Podium catalog ({{.ChangedCount}} changes) {{.Timestamp}}"
    plugins:
      - name: finance-pack
        include: ["finance/**"]
        exclude: ["finance/experimental/**"]
        type: [skill, command, rule]
      - name: security-baseline
        include: ["security/baseline/**"]

  - id: acme-gemini
    git:
      remote: git@github.com:acme/gemini-extension.git
      branch: main
    harnesses: [gemini]                        # one extension per repo, so its own output
    plugins:
      - name: house-rules
        include: ["rules/**"]
```

### `defaults`

The `defaults` block holds the registry, the publishing identity, and a default workflow. Each marketplace inherits the defaults and may override them.

| Key | Meaning |
|:--|:--|
| `registry` | Registry URL the render reads the effective view from. `PODIUM_REGISTRY` overrides it. |
| `identity` | The §4.6 effective-view principal the render runs as. See [Publishing identity](#publishing-identity-and-the-effective-view). |
| `workflow` | The default `prepare` and `publish` command lists each marketplace inherits. |

### `marketplaces`

Each entry under `marketplaces` is one output.

| Key | Meaning |
|:--|:--|
| `id` | The output identifier, selected by `podium publish --output <id>`. |
| `git.remote` | The remote URL the workflow clones and pushes. Injected as `$PODIUM_GIT_REMOTE`. |
| `git.branch` | The branch the output writes. Injected as `$PODIUM_GIT_BRANCH`. |
| `harnesses` | The harness set. Each must be a publish target. |
| `commit_message` | A Go template rendered with the change count and timestamp into `$PODIUM_COMMIT_MESSAGE`. |
| `plugins` | The plugin list: a `name` plus a scope filter (`include`, `exclude`, `type`). |
| `workflow` | Optional. Replaces the default workflow for this output in full. |

### Workflow override

The `prepare` and `publish` command lists are grouped under a `workflow` key. A marketplace that declares `workflow` replaces the default workflow for that output in full. The output below pushes a branch and opens a pull request:

```yaml
  - id: acme-editors-pr
    git:
      remote: git@github.com:acme/editor-config.git
      branch: podium-sync
    harnesses: [cursor]
    plugins:
      - name: house-rules
        include: ["rules/**"]
    workflow:                                  # replaces the default workflow for this output
      prepare:
        - run: ["git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
        - run: ["git", "-C", "$PODIUM_WORKDIR", "checkout", "-B", "$PODIUM_GIT_BRANCH"]
      publish:
        - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
        - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
          skip_if_no_changes: true
        - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]
        - run: ["gh", "pr", "create", "--fill", "--base", "main", "--head", "$PODIUM_GIT_BRANCH"]
          continue_on_error: true
```

### Command form and execution semantics

A command is an argv list under `run:`, executed directly without a shell, or a string under `sh:`, executed through `sh -c`. The argv form is exec'd with no shell and no injection; the shell form is convenient for pipes. Per-command flags control failure handling:

| Flag | Effect |
|:--|:--|
| `skip_if_no_changes` | Skip the command when the render produced no diff against the checkout. |
| `continue_on_error` | Let the pipeline proceed past a non-zero exit. |
| `timeout` | Bound the command's wall-clock duration. Takes a duration string such as `"30s"`. |

Each phase also accepts an optional `prepare_on_error` or `publish_on_error` cleanup list, run when that phase fails, before the failure propagates. The pipeline inherits the ambient environment of the `podium publish` process and adds the injected variables, so git authentication relies on the ambient `SSH_AUTH_SOCK`, `GH_TOKEN`, and similar. The pipeline fails fast on the first non-zero exit, except where `continue_on_error` is set.

### Trust boundary

These commands run as subprocesses with the operator's privileges and ambient credentials. They are unrelated to the `MaterializationHook` SPI, which is sandboxed to forbid subprocesses, network, and writes outside the destination, and they are unrelated to the `hook` artifact type. The commands come from operator-authored `publish.yaml`, the same trust boundary as a Makefile or a CI script, so a catalog author cannot inject a command. This is why publishing runs in an operator CLI; a server-side publisher is out of scope.

The full config schema is documented in [Reference → publish.yaml](../reference/publish-yaml).

---

## Running `podium publish`

```
podium publish [--output <id>] [--config <path>] [--workdir <dir>] [--dry-run] [--check] [--json]
```

`podium publish` resolves the marketplace outputs and runs the fixed `prepare`, `render`, `publish` pipeline per output.

```bash
podium publish --config .podium/publish.yaml --output acme-agents
```

| Flag | Effect |
|:--|:--|
| `--output <id>` | Publish only the named output. Default: every output. |
| `--config <path>` | Read this `publish.yaml` instead of the merged config scopes. |
| `--workdir <dir>` | Render into an existing checkout. Single output only; pair with `--output`. |
| `--dry-run` | Render into a temporary directory and print each command with variables substituted; run no publish phase. |
| `--check` | Validate the config only; render and run nothing. |
| `--json` | Emit a structured JSON envelope on stdout. |

By default Podium allocates the working directory and the `prepare` phase clones into it. `--workdir <dir>` points the render at an existing checkout, for example a CI `actions/checkout`, in which case `prepare` configures git or pulls rather than clones. `$PODIUM_WORKDIR` names the per-output working and checkout directory, so `--workdir` names a single checkout and requires a single-output selection. A `--workdir` shared across multiple outputs would render them into one checkout where each output reconciles against the previous output's files, so that combination exits 2.

### Injected variables

Podium passes context to the commands through environment variables rather than by interpreting git state:

| Variable | Meaning |
|:--|:--|
| `$PODIUM_WORKDIR` | The per-output working and checkout directory. |
| `$PODIUM_OUTPUT_ID` | The marketplace output identifier. |
| `$PODIUM_GIT_REMOTE`, `$PODIUM_GIT_BRANCH` | From the output's `git:` block. |
| `$PODIUM_COMMIT_MESSAGE` | Rendered from `commit_message` with the change count and timestamp. |
| `$PODIUM_CHANGED` | Whether the render produced a diff against the checkout. |
| `$PODIUM_CHANGE_SUMMARY` | A path to a JSON file describing the changed artifacts. |
| `$PODIUM_REGISTRY`, `$PODIUM_IDENTITY`, `$PODIUM_HARNESSES` | The registry URL, the publishing identity, and the harness set. |

### Exit codes

`podium publish` mirrors `podium sync`. A flag error or a config error (a `config.invalid` config, an unknown `--output`, a missing `--config` path, or an unset registry `config.no_registry`) exits 2. A config-load failure (a malformed or unreadable config file) and a runtime failure (a fetch error, a render error, or a non-zero workflow command) exit 1. A successful run exits 0.

---

## Repository layout

A marketplace output renders into one repository. For the `acme-agents` output above, with the harness set `[claude-code, codex, cursor]`:

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

Each vendor manifest lists the same plugin set and points its entries at that vendor's subtree. The plugin content is per-harness because the per-plugin manifest filenames and the rule and MCP conventions differ across vendors. Each vendor manifest carries one entry per plugin keyed by the plugin name, contributed once per plugin rather than once per artifact, so an N-artifact plugin yields one plugin entry.

| Format | Root manifest | Per-plugin manifest | Components |
|:--|:--|:--|:--|
| Claude (Code, Desktop, Cowork) | `.claude-plugin/marketplace.json` | `.claude-plugin/plugin.json` | `skills/`, `agents/`, `commands/`, `hooks/hooks.json`, `.mcp.json` |
| Codex | `.agents/plugins/marketplace.json` | `.codex-plugin/plugin.json` | `skills/`, `hooks/hooks.json`, `.mcp.json` |
| Cursor | `.cursor-plugin/marketplace.json` | `.cursor-plugin/plugin.json` | `skills/`, `rules/*.mdc`, `mcp.json` |
| Gemini | `gemini-extension.json` (one per repository) | n/a | `commands/*.toml`, context file, `mcpServers` |
| Pi | root `package.json` (`pi-package` keyword, `pi.skills` array) | n/a | `skills/<name>/SKILL.md` |
| Hermes | none (skills discovered under root `skills/`) | n/a | `skills/<name>/SKILL.md` with `references/`, `scripts/`, `assets/` |

The Claude, Codex, and Cursor manifests sit at distinct fixed locations and coexist in one repository, each read only by its own harness. A Gemini output occupies the whole repository (one extension per repository) and a Hermes tap defaults to a root `skills/` directory, so those two take their own output. The Claude, Codex, and Cursor JSON manifests merge with the Podium-owned merge tag, so a stale plugin entry drops out on re-render. The Hermes tap and the Pi skills subtree carry no merged manifest and reconcile through the sync lock file.

---

## Publishing identity and the effective view

The published marketplace reflects the publishing identity's effective view intersected with the plugin scope filters. `publish.yaml` carries `identity` so the operator selects the principal whose visibility defines what reaches the marketplace. A principal that can see restricted layers would render them into the output, so the publishing identity is a security-relevant setting. Publish a public marketplace under an identity scoped to the artifacts intended for it.

Against a server-source registry, `podium publish` binds the declared `identity` to the resolved credential before reading the effective view. It decodes the credential's `sub` and `email` claims and requires one to equal `identity`; a credential that authenticates as a different principal fails closed with `publish.identity_mismatch` and renders nothing. The credential is `PODIUM_TOKEN` when set, otherwise the read-CLI session token or the `podium login` keychain token. A declared `identity` with no resolved credential also fails closed, because the render would otherwise reach the registry anonymously. A filesystem-source registry has no authenticated principal, so a declared `identity` is documentary there and the render reads the local view directly.

---

## Reconciliation

Re-rendering an output is idempotent. The materialization writer and the sync lock file remove files for artifacts that left the view, and the JSON manifests merge with the Podium-owned merge tag so stale entries drop out. The git diff after a render is the catalog delta, so `skip_if_no_changes` suppresses an empty commit when the delta is empty. A second `podium publish` against an unchanged catalog produces no second commit.

---

## Triggers

`podium publish` runs from an operator CLI or from a CI job. It has no watch mode. A server-side publisher inside the registry process is out of scope, because it would require storing per-repository push credentials and running operator-supplied commands inside a multi-tenant process.

The CI trigger is the existing `layer.ingested` webhook event, which the registry emits once per completed layer ingest cycle, so one source commit that changes many artifacts yields one event rather than one per changed artifact. A CI job subscribed to `layer.ingested` runs `podium publish`, so one source commit triggers one publish across the artifacts it changed. A `layer.ingested` event for a layer that contributes nothing to an output produces a render with no diff, which `skip_if_no_changes` suppresses, so an unrelated layer update does not produce an empty commit.

---

## GitHub Actions

A GitHub Actions deployment uses one of two patterns, because GitHub starts a workflow from an external system only through the authenticated REST API (`repository_dispatch` or `workflow_dispatch`), and a Podium webhook receiver posts an HMAC-signed event body that GitHub's dispatch endpoint does not accept.

### Pattern A: scheduled

A workflow in the marketplace repository runs `podium publish` on a cron. `skip_if_no_changes` makes an empty run a no-op, so a 5-to-15-minute poll is inexpensive. No webhook receiver is involved.

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
        run: podium publish --config .podium/publish.yaml --output acme-agents --workdir "$GITHUB_WORKSPACE"
```

The output's `workflow` overrides the default clone, because `actions/checkout` already placed the repository and authenticated `origin`:

```yaml
# .podium/publish.yaml, acme-agents output
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

### Pattern B: event-driven relay

A Podium webhook receiver filtered to `layer.ingested` posts to a small relay. The relay verifies the HMAC and calls GitHub `repository_dispatch`, which the workflow listens for. The receiver cannot call GitHub's dispatch endpoint directly, because the HMAC-signed receiver body differs from the body the dispatch endpoint accepts, so the relay is the bridge.

```bash
# register the receiver; the response carries the HMAC secret for the relay
curl -X POST https://podium.acme.com/v1/webhooks -H "Authorization: Bearer $PODIUM_TOKEN" \
  -d '{"url":"https://relay.acme.com/podium","event_filter":["layer.ingested"]}'
```

```yaml
# add to the workflow triggers
on:
  repository_dispatch: { types: [podium-layer-ingested] }
  workflow_dispatch: {}
```

The relay verifies the HMAC against the receiver secret, then calls `POST https://api.github.com/repos/acme/agent-marketplace/dispatches` with `Authorization: Bearer <token>` and `{"event_type":"podium-layer-ingested"}`. On dispatch, the workflow runs `podium publish`. A burst of `layer.ingested` events fires the relay per event, and the workflow's own concurrency control collapses the redundant runs. Receiver authorization and a per-receiver debounce window that coalesces a burst into one batch delivery are specified in [proposal 0004](https://github.com/lennylabs/podium/blob/main/proposals/0004-webhook-hardening.md); both publishing patterns function without them. The relay pattern is also documented in [Extending → Webhook-driven integrations](../deployment/extending#webhook-driven-integrations) and [HTTP API → Outbound webhooks](../reference/http-api#outbound-webhooks).

In both patterns the registry credential `PODIUM_TOKEN` carries the publishing identity, and the git push credential is GitHub's: `GITHUB_TOKEN` in Pattern A, or a deploy key the `prepare` clone uses when the workflow runs outside the marketplace repository. Podium never holds the git push credential.

---

## Where to learn more

- [Configure your harness](configure-your-harness): the per-harness distribution paths and install commands.
- [Reference → CLI](../reference/cli#podium-publish): the `podium publish` command surface.
- [Reference → publish.yaml](../reference/publish-yaml): the full config schema.
