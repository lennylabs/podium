---
layout: default
title: Marketplace publishing
parent: Consuming
nav_order: 5
description: Render the catalog into harness-native marketplace repositories with a kind marketplace sync target and push them to git remotes with podium sync.
---

# Marketplace publishing

A `podium sync` target of `kind: marketplace` renders the catalog into harness-native git-repo distributions (plugin marketplaces, extensions, packages, and taps) and runs an operator-configured workflow to commit and push them (§7.8). A harness imports the published repository through its own install path.

Publishing is a derived, served output downstream of the registry. It reads the effective view over the same HTTP API as the other consumers, then renders the git-repo distribution layout and runs a workflow that pushes it to a git remote. A `kind: workspace` target writes a workspace tree the harness reads directly; a `kind: marketplace` target renders the same effective view into the git-repo distribution layout a harness imports. Publishing a repository does not make it an authoring source; the registry remains the source of truth.

A harness with a git-repo distribution is a publish target: Claude (Code, Desktop, Cowork), Codex, Cursor, Gemini, Pi, and Hermes. OpenCode (npm only) and `none` (raw canonical output) have no git-repo distribution and are not publish targets. A marketplace target whose harness set names OpenCode or `none` is rejected at config validation with `config.invalid`.

---

## The publish model

A marketplace target declares a git repository, a set of harnesses, a list of plugins, and a workflow. An operator adds one or more `kind: marketplace` entries to the `targets:` list in `sync.yaml` and runs `podium sync --config <path>`. For each marketplace target, Podium runs a fixed pipeline:

```
prepare (operator commands)  ->  render (Podium)  ->  publish (operator commands)
```

- **prepare** places a checkout of the destination repository at the target directory. The common case is a `git clone`.
- **render** materializes each harness's distribution tree into the target directory through the materialization writer and the per-harness emitters.
- **publish** takes the rendered tree to the remote. The common case is `git add`, `git commit`, and `git push`.

The phases exist because the checkout must precede the render so the render reconciles against existing repository content, and the commit must follow it. The git interaction is configuration: Podium owns rendering, and the operator's commands own getting the repository to the target directory and the result to the remote. There is no embedded git library and no write-side git SPI.

### Plugins, harness sets, and repositories

- A **plugin** is a named bundle of selected artifacts, defined by a scope filter (`include`, `exclude`, `type`) over canonical artifact IDs. The filter reuses the `podium sync` scope machinery, so plugin selection and sync selection apply identical glob semantics. A plugin is the cross-harness unit: it renders into each harness's distribution layout in that harness's subtree.
- A **harness set** is the harnesses a target publishes for. Each listed harness contributes its format's manifest and its content subtree to the output repository, and harnesses that share a format contribute one shared manifest.
- Each marketplace target renders into one repository. The harness set is the grouping lever: an operator who cannot share one repository across two formats declares two targets.

---

## The `kind: marketplace` target

A marketplace target is one entry under `targets:` in `sync.yaml`, alongside the `kind: workspace` targets the harness reads directly. The `kind:` field selects the output format: `workspace` (the default) materializes the project-files layout, and `marketplace` renders the git-repo distribution layout. The marketplace fields are valid only on a `kind: marketplace` entry, and the workspace scope fields (`profile`, `include`, `exclude`, `type`) and the watch mode are rejected on a marketplace entry. The ephemeral overrides (`podium sync override`) do not apply to a marketplace target: that command operates on a single target directory's lock file and renders the project-files layout, so it does not reach a marketplace entry.

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

### Marketplace target fields

| Key | Meaning |
|:--|:--|
| `kind` | `marketplace` selects the git-repo distribution output. |
| `target` | The working directory the `prepare` checkout lands in and the render writes into. |
| `git.remote` | The remote URL the workflow clones and pushes. Injected as `$PODIUM_GIT_REMOTE`. |
| `git.branch` | The branch the target writes. Injected as `$PODIUM_GIT_BRANCH`. |
| `harnesses` | The harness set. Each must be a publish target. |
| `commit_message` | A Go template rendered with the change count and timestamp into `$PODIUM_COMMIT_MESSAGE`. |
| `identity` | The §4.6 effective-view principal the render runs as. Inherited from `defaults.identity` when unset. See [Publishing identity](#publishing-identity-and-the-effective-view). |
| `plugins` | The plugin list: a `name` plus a scope filter (`include`, `exclude`, `type`). |
| `workflow` | The `prepare` and `publish` command lists Podium runs around the render. |

### The publishing identity default

`defaults.identity` carries the publishing identity that every marketplace target inherits when it declares none of its own. It is a marketplace-scoped default: a `kind: workspace` target renders under the caller's own identity and carries no identity field. The §4.6 effective-view rule stays enforceable from `sync.yaml` because the identity that defines what reaches the marketplace lives in the same config the workflow lives in.

### Command form and execution semantics

A command is an argv list under `run:`, executed directly without a shell, or a string under `sh:`, executed through `sh -c`. The argv form is exec'd with no shell and no injection; the shell form is convenient for pipes. Per-command flags control failure handling:

| Flag | Effect |
|:--|:--|
| `skip_if_no_changes` | Skip the command when the render produced no diff against the checkout. |
| `continue_on_error` | Let the pipeline proceed past a non-zero exit. |
| `timeout` | Bound the command's wall-clock duration. Takes a duration string such as `"30s"`. |

Each phase also accepts an optional `prepare_on_error` or `publish_on_error` cleanup list, run when that phase fails, before the failure propagates. The pipeline inherits the ambient environment of the `podium sync` process and adds the injected variables, so git authentication relies on the ambient `SSH_AUTH_SOCK`, `GH_TOKEN`, and similar. The pipeline fails fast on the first non-zero exit, except where `continue_on_error` is set.

### Trust boundary

These commands run as subprocesses with the operator's privileges and ambient credentials. They are unrelated to the `MaterializationHook` SPI, which is sandboxed to forbid subprocesses, network, and writes outside the destination, and they are unrelated to the `hook` artifact type. A `workflow` executes only on the `podium sync` CLI path and never inside the registry server or the MCP server. The commands live on `sync.yaml` target entries whose project-shared scope is committed to git, the same trust boundary as a Makefile or a CI script. A server-side publisher is out of scope.

The full `sync.yaml` schema and the marketplace target fields are documented in [Configure your harness](configure-your-harness) and [Reference → CLI](../reference/cli#podium-sync).

---

## Running `podium sync`

```
podium sync --config <path> [--dry-run] [--check] [--json]
```

`podium sync --config <path>` reads one `sync.yaml` and runs each `targets:` entry: a `kind: workspace` target materializes the project-files layout, and a `kind: marketplace` target runs the fixed `prepare`, `render`, `publish` pipeline.

```bash
podium sync --config .podium/sync.yaml
```

| Flag | Effect |
|:--|:--|
| `--config <path>` | Read this `sync.yaml` and run each `targets:` entry. |
| `--dry-run` | Render into a temporary directory and print each command with variables substituted; run no publish phase. |
| `--check` | Validate the config only; render and run nothing. |
| `--json` | Emit a structured JSON envelope on stdout. |

The marketplace fields have no single-target CLI-flag analog, so a marketplace target is reached through the `--config` path. By default the `prepare` phase clones into the target directory; a `prepare` phase may instead configure git against an existing checkout, for example a CI checkout step, in which case it configures git or pulls rather than clones.

### Injected variables

Podium passes context to the commands through environment variables rather than by interpreting git state:

| Variable | Meaning |
|:--|:--|
| `$PODIUM_WORKDIR` | The per-target working and checkout directory. |
| `$PODIUM_OUTPUT_ID` | The marketplace target identifier. |
| `$PODIUM_GIT_REMOTE`, `$PODIUM_GIT_BRANCH` | From the target's `git:` block. |
| `$PODIUM_COMMIT_MESSAGE` | Rendered from `commit_message` with the change count and timestamp. |
| `$PODIUM_CHANGED` | Whether the render produced a diff against the checkout. |
| `$PODIUM_CHANGE_SUMMARY` | A path to a JSON file describing the changed artifacts. |
| `$PODIUM_REGISTRY`, `$PODIUM_IDENTITY`, `$PODIUM_HARNESSES` | The registry URL, the publishing identity, and the harness set. |

### Exit codes

A flag error or a config error (a `config.invalid` config, a missing `--config` path, or an unset registry `config.no_registry`) exits 2. A config-load failure (a malformed or unreadable config file) and a runtime failure (a fetch error, a render error, or a non-zero workflow command) exit 1. A successful run exits 0.

---

## Repository layout

A marketplace target renders into one repository. For the `acme-agents` target above, with the harness set `[claude-code, codex, cursor]`:

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

The Claude, Codex, and Cursor manifests sit at distinct fixed locations and coexist in one repository, each read only by its own harness. A Gemini target occupies the whole repository (one extension per repository) and a Hermes tap defaults to a root `skills/` directory, so those two take their own target. The Claude, Codex, and Cursor JSON manifests merge with the Podium-owned merge tag, so a stale plugin entry drops out on re-render. The Hermes tap and the Pi skills subtree carry no merged manifest and reconcile through the sync lock file.

---

## Publishing identity and the effective view

The published marketplace reflects the publishing identity's effective view intersected with the plugin scope filters. A marketplace target carries `identity` (inherited from `defaults.identity`) so the operator selects the principal whose visibility defines what reaches the marketplace. A principal that can see restricted layers would render them into the output, so the publishing identity is a security-relevant setting. Publish a public marketplace under an identity scoped to the artifacts intended for it.

Against a server-source registry, the registry credential carries the publishing identity, and `podium sync` renders that credential's effective view. The credential is `PODIUM_TOKEN` when set, otherwise the read-CLI session token or the `podium login` keychain token. The `identity` field in `sync.yaml` records the principal the target publishes as. It is documentary: the render reflects the authenticated token, so set the credential to the principal whose visibility you intend for the marketplace. A filesystem-source registry has no authenticated principal, so the render reads the local view directly.

---

## Reconciliation

Re-rendering a marketplace target is idempotent. The materialization writer and the sync lock file remove files for artifacts that left the view, and the JSON manifests merge with the Podium-owned merge tag so stale entries drop out. The git diff after a render is the catalog delta, so `skip_if_no_changes` suppresses an empty commit when the delta is empty. A second `podium sync --config` against an unchanged catalog produces no second commit.

---

## Triggers

A marketplace target runs from an operator CLI or from a CI job. It has no watch mode. A server-side publisher inside the registry process is out of scope, because it would require storing per-repository push credentials and running operator-supplied commands inside a multi-tenant process.

The CI trigger is the existing `layer.ingested` webhook event, which the registry emits once per completed layer ingest cycle, so one source commit that changes many artifacts yields one event rather than one per changed artifact. A CI job subscribed to `layer.ingested` runs `podium sync --config <path>`, so one source commit triggers one publish across the artifacts it changed. A `layer.ingested` event for a layer that contributes nothing to a target produces a render with no diff, which `skip_if_no_changes` suppresses, so an unrelated layer update does not produce an empty commit.

---

## GitHub Actions

A GitHub Actions deployment uses one of two patterns, because GitHub starts a workflow from an external system only through the authenticated REST API (`repository_dispatch` or `workflow_dispatch`), and a Podium webhook receiver posts an HMAC-signed event body that GitHub's dispatch endpoint does not accept.

### Pattern A: scheduled

A workflow in the marketplace repository runs `podium sync --config <path>` on a cron. `skip_if_no_changes` makes an empty run a no-op, so a 5-to-15-minute poll is inexpensive. No webhook receiver is involved.

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
        run: podium sync --config .podium/sync.yaml
```

The target's `workflow` configures git against the existing checkout, because `actions/checkout` already placed the repository and authenticated `origin`:

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

The relay verifies the HMAC against the receiver secret, then calls `POST https://api.github.com/repos/acme/agent-marketplace/dispatches` with `Authorization: Bearer <token>` and `{"event_type":"podium-layer-ingested"}`. On dispatch, the workflow runs `podium sync --config <path>`. A burst of `layer.ingested` events fires the relay per event, and the workflow's own concurrency control collapses the redundant runs. Receiver authorization and a per-receiver debounce window that coalesces a burst into one batch delivery are specified in [proposal 0004](https://github.com/lennylabs/podium/blob/main/proposals/0004-webhook-hardening.md); both publishing patterns function without them. The relay pattern is also documented in [Extending → Webhook-driven integrations](../deployment/extending#webhook-driven-integrations) and [HTTP API → Outbound webhooks](../reference/http-api#outbound-webhooks).

In both patterns the registry credential `PODIUM_TOKEN` carries the publishing identity, and the git push credential is GitHub's: `GITHUB_TOKEN` in Pattern A, or a deploy key the `prepare` clone uses when the workflow runs outside the marketplace repository. Podium never holds the git push credential.

---

## Where to learn more

- [Configure your harness](configure-your-harness): the per-harness distribution paths and install commands.
- [Reference → CLI](../reference/cli#podium-sync): the `podium sync` command surface.
