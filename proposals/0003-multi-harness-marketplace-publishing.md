# Proposal 0003: Multi-Harness Marketplace Publishing

- Issue: (to be filed)
- Status: Draft
- Date: 2026-06-24

## Summary

Podium materializes the catalog into harness-native files through the `HarnessAdapter` SPI (§6.7), and `podium sync` writes those files to a local directory (§7.5). It has no way to publish the catalog as a git-repo plugin marketplace. The one adapter that emits a marketplace layout, `claude-cowork`, writes one plugin per artifact (`pkg/adapter/layout.go:437`, `coworkPlugin`), assembles a single fixed marketplace named `podium` (`coworkMarketplaceFragment`, `pkg/adapter/layout.go:507`), and leaves the git operations to the operator (the §6.7 Cowork docs show a manual `git add`, `git commit`, and `git push`). Git is an input source only (`pkg/layer/source/git.go`); the registry and the CLI never write to a repo.

This proposal adds marketplace publishing. It introduces an operator-authored `publish.yaml` that declares one or more marketplace outputs, a plugin grouping primitive that bundles a selected set of artifacts under a named plugin, a marketplace emitter per harness that has a git-repo distribution path, and a `podium publish` command that renders each output and runs an operator-configured workflow of shell commands to clone, commit, and push. The git interaction is configuration rather than embedded Go code: Podium owns rendering and runs the operator's `prepare` and `publish` commands around it. Publishing covers every harness in Podium's adapter roster (`pkg/adapter/adapter.go:162`) whose harness has a git-repo marketplace, extension, package, or tap; a harness without one is not a publish target. Rendering reuses the materialization writer (`pkg/materialize/atomic.go:205`), the scope-filter machinery (`pkg/sync/scope.go`), and the existing reconciliation through the sync lock file and the `PodiumOwnedKey` merge tag. Publishing is driven by a CI job on the existing `layer.ingested` event (`pkg/registry/ingest/orchestrator.go:172`), which fires once per completed layer ingest cycle, or by an operator running `podium publish`; a server-side publisher is out of scope for this proposal. Proposal 0004 (webhook hardening) adds the receiver authorization and the debounce window that the event-driven trigger relies on, and both publishing patterns function without it. `spec/` is read-only, so this proposal carries the amendments to §6.7, §7, §9.1, and the glossary, as proposals 0001 and 0002 did.

## Current state and the gap

### Output today

A consumer reaches the catalog over three paths (§2.2, §7): the language SDKs, the MCP server, and `podium sync`. The MCP server and `podium sync` both run the configured `HarnessAdapter` and write to a local destination through `materialize.Write(dest, files)` (`pkg/materialize/atomic.go:205`), atomically per file (`writeAtomic`, `pkg/materialize/atomic.go:348`). `podium sync` already supports multiple outputs from one config: `sync.yaml` carries a `targets:` list (`SyncConfig`, `pkg/sync/config.go`), and `podium sync --config` runs one materialization per entry, each with its own `{id, harness, target, profile, include, exclude, type}` (`TargetEntry`, `pkg/sync/config.go`). Selection within a target is a `ScopeFilter` of `include`, `exclude`, and `type` globs over canonical artifact IDs (`pkg/sync/scope.go`).

Git is an input source. `pkg/layer/source/git.go` clones a layer's tracked ref and reads manifests; the read-side `GitProvider` SPI (§9.1) verifies webhooks and fetches from GitHub, GitLab, and Bitbucket. There is no write path: no clone-and-push, no commit, and no `GitProvider` analog for output. The registry's only outbound effects are webhooks (`pkg/webhook/webhook.go`, §7.3.2) and operational notifications.

### The cowork marketplace is one plugin per artifact

`coworkPlugin` (`pkg/adapter/layout.go:437`) sets `pluginRoot := path.Join("plugins", id)` where `id` is the artifact ID, so each artifact becomes its own plugin under `plugins/<artifact-id>/` with its own `.claude-plugin/plugin.json`. `coworkMarketplaceFragment` (`pkg/adapter/layout.go:507`) emits one `OpMergeJSON` fragment per artifact into the repository-root `.claude-plugin/marketplace.json`, under the fixed marketplace name `podium`, tagged with `PodiumOwnedKey` so a re-render reconciles the listing. There is no concept of a plugin that bundles several artifacts, and no operator-chosen marketplace name. The adapter contract is the reason: `HarnessAdapter.Adapt(ctx, Source)` (`pkg/adapter/adapter.go:94`) receives one artifact at a time, so a multi-artifact grouping cannot be expressed inside an adapter and must be supplied above it.

### The adapters and docs predate the harnesses' marketplace formats

The `codex`, `cursor`, `gemini`, `pi`, and `hermes` adapters emit project-level files (`.codex/`, `.cursor/`, `.gemini/`, `.pi/`, `AGENTS.md`, `GEMINI.md`), which is correct for a workspace that consumes the files directly through `podium sync`. None emits a marketplace, extension, package, or tap layout, because these harnesses gained that distribution path after the adapters were written. The §6.7 capability matrix and `docs/consuming/configure-your-harness.md` describe Codex commands as user-scope only and treat Cowork as the only marketplace target, which is now inaccurate.

## Harness distribution formats (verified 2026-06-24)

The table records each harness's git-repo distribution path, verified against vendor documentation on 2026-06-24. A harness with such a path is a publish target; one without is excluded from publishing.

| Harness | Git-repo distribution | Manifest (fixed location) | Per-plugin manifest | Components | Publish target |
| --- | --- | --- | --- | --- | --- |
| `claude-code`, `claude-desktop`, `claude-cowork` | marketplace, shared format | `.claude-plugin/marketplace.json` (root) | `.claude-plugin/plugin.json` | `skills/`, `agents/`, `commands/`, `hooks/`, `.mcp.json` | yes (one Claude marketplace) |
| `codex` | marketplace | `.agents/plugins/marketplace.json` (root) | `.codex-plugin/plugin.json` | `skills/`, `hooks/hooks.json`, `.app.json`, `.mcp.json` | yes |
| `cursor` | team marketplace | `.cursor-plugin/marketplace.json` (root) | `.cursor-plugin/plugin.json` | `skills/`, `rules/*.mdc`, `mcp.json` | yes |
| `gemini` | extension | `gemini-extension.json` (root); one extension per repo | n/a | `commands/*.toml`, `GEMINI.md` or `contextFileName`, `mcpServers` | yes |
| `pi` | git package | root `package.json` with a `pi.skills` array and a `pi-package` keyword | n/a | `skills/<name>/SKILL.md` | yes |
| `hermes` | skills tap | no root manifest; skills discovered under `skills/` (default, configurable per tap) | n/a | `skills/<name>/SKILL.md` (with `references/`, `scripts/`, `assets/`) | yes |
| `opencode` | none (npm packages only) | npm `package.json`, installed via the `opencode.json` `plugin` array | n/a | TypeScript or JavaScript plugin modules | no |
| `none` | none (raw canonical output) | n/a | n/a | `ARTIFACT.md`, `SKILL.md`, resources | no |

Install paths verified: `/plugin marketplace add owner/repo` and the Cowork private import for Claude; `codex plugin marketplace add owner/repo` for Codex; a dashboard import of a GitHub, GitLab, or Bitbucket repo for Cursor; `gemini extensions install owner/repo` for Gemini; `pi install git:github.com/owner/repo` for Pi; `hermes skills tap add owner/repo` for Hermes.

## Marketplace formats and harness mapping

A publish target is a marketplace format, and a harness maps to one format. Several harnesses share a format: Claude Code, Claude Desktop, and Claude Cowork all consume the same `.claude-plugin/marketplace.json`, so they map to one Claude marketplace. The formats fall into two groups:

- **Plugin marketplaces** (Claude, Codex, Cursor) carry a root marketplace manifest plus a per-plugin manifest, and the plugin is the install unit. A Podium plugin renders into one plugin entry.
- **Extension, package, and tap formats** (Gemini, Pi, Hermes) carry a single repository-level manifest or none, and the install unit is the extension or the individual skill. A Podium plugin maps to an organizational grouping within the repository; for Gemini the plugin set collapses into one extension, and for Pi and Hermes the skills install individually.

The Claude, Codex, and Cursor manifests sit at distinct fixed locations, so they do not collide and can coexist in one repository, each read only by its own harness. The Gemini extension occupies the whole repository, and the Hermes tap defaults to a root `skills/` directory, so those two formats place the tightest constraints on a shared repository (Open questions).

## Decisions

These were settled during design and are recorded here as the basis for the specification below.

1. **Publish covers every supported harness with a git-repo distribution.** The publish targets are the marketplace, extension, package, and tap formats above: Claude (Code, Desktop, Cowork), Codex, Cursor, Gemini, Pi, and Hermes. A harness without a git-repo distribution, namely OpenCode (npm only) and `none` (raw output), is not a publish target. A publish output whose harness set names an excluded harness is rejected at config validation.
2. **The Claude surfaces share one marketplace, and `podium sync` no longer emits marketplaces.** Claude Code, Claude Desktop, and Claude Cowork read the same `.claude-plugin/marketplace.json`, so one Claude marketplace emitter serves all three, and a harness set that names more than one of them yields one Claude marketplace rather than a collision. Marketplace emission moves out of `podium sync`: the `claude-cowork` marketplace path in `sync` is removed, so `sync` materializes workspace project files and `publish` produces marketplaces. The `claude-cowork` harness ID is retained as a consumer of the Claude marketplace.
3. **Repository topology: one git repository per marketplace output, shared across that output's harness set.** Each format's manifest lives at its fixed location, and per-harness plugin content lives in per-harness subtrees the manifests reference. The harness set is the grouping lever: an operator who cannot or does not want to share one repository across two formats declares two outputs. Gemini (one extension per repository) and Hermes (root `skills/`) are the formats most likely to take their own output.
4. **Plugin composition lives in output config.** A plugin is a named `ScopeFilter` declared in `publish.yaml`, reusing the `pkg/sync/scope.go` selection. Plugin membership is a packaging decision the operator controls. It is not authored or versioned in the catalog. A catalog-native plugin type is deferred (Open questions).
5. **Git publishing runs operator-configured commands.** `podium publish` renders the marketplace tree and runs an operator-configured workflow of shell commands to clone, commit, and push. There is no embedded git library and no write-side git SPI.
6. **Triggers.** A CI job runs `podium publish` on the existing `layer.ingested` event, which fires once per completed layer ingest cycle, so one source commit yields one publish rather than one per changed artifact. The CLI path runs `podium publish` directly. `podium publish` has no watch mode. A server-side publisher inside the registry process is out of scope, because it would require storing per-repository push credentials and running operator-supplied commands inside a multi-tenant process.
7. **Webhook hardening is split into proposal 0004.** The webhook receiver CRUD is not authorization-gated today, and the registry delivers one webhook per event. Receiver authorization with SSRF hardening, and a per-receiver debounce window with a batch payload, are general webhook-subsystem concerns specified in proposal 0004 (webhook hardening). Both publishing patterns function without 0004: Pattern A uses no receiver, and Pattern B works on the existing per-event delivery. Pattern B relies on 0004 for a hardened receiver surface and for coalescing a burst into one CI dispatch. The debounce is a registry config rather than a `publish.yaml` value or a client watch mode.

## Proposed solution

### Concepts

- **Marketplace output.** A named publishing destination: a git repository, a set of harnesses, a list of plugins, and a workflow. Declared as one entry under `marketplaces:` in `publish.yaml`.
- **Plugin.** A named bundle of selected artifacts, defined by a `ScopeFilter` (`include`, `exclude`, `type`). A plugin is the cross-harness unit: it renders into each harness's plugin layout in that harness's subtree.
- **Harness set.** The harnesses an output publishes for. Each listed harness contributes its format's manifest and its content subtree to the output repository, and harnesses that share a format contribute one shared manifest.

### `publish.yaml`

`publish.yaml` is an operator-authored client config that lives beside `sync.yaml` under `.podium/`, with the same three-scope resolution (`~/.podium/`, `<workspace>/.podium/`, `<workspace>/.podium/publish.local.yaml`) and the same precedence rules as `sync.yaml` (§7.5.2). Its top-level keys are `defaults` and `marketplaces`. The `defaults` block holds the registry, the publishing identity, and a default `workflow`; each marketplace inherits the defaults and may override them. The `prepare` and `publish` command lists are grouped under a `workflow` key, and a marketplace that declares `workflow` replaces the default workflow for that output.

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

Per output the operator sets the destination (`git.remote`, `git.branch`), the harness set, and the plugin list. The git commands are inherited from `defaults.workflow` and reference injected variables, so the common case carries no per-output commands. The third output above overrides the default workflow to push a branch and open a pull request.

### Repository layout

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

Each vendor manifest lists the same plugin set and points its entries at that vendor's subtree through the relative-path or subdirectory plugin source the harness supports. The plugin content is per-harness because the per-plugin manifest filenames and the rule and MCP conventions differ across vendors. A Gemini output writes `gemini-extension.json`, `commands/`, and the context file at the repository root and collapses the plugin set into one extension, so it takes its own output. A Pi output writes a root `package.json` whose `pi.skills` array points at a skills subtree, and a Hermes output writes the skills under the tap's `skills/` directory; both render the agentskills.io `SKILL.md` the harnesses consume. OpenCode and `none` are not publish targets and continue to use `podium sync` for workspace files.

### Plugin grouping and the HarnessAdapter contract

A plugin that bundles several artifacts cannot be expressed by the current per-artifact adapter call, so this proposal adds the target plugin to the adapter's input. The `Source` passed to `HarnessAdapter.Adapt` (`pkg/adapter/adapter.go:94`) gains a plugin descriptor: the plugin name, an optional description, and the harness subtree prefix. A marketplace emitter uses it to write under `<harness>/<plugin>/...` and to contribute the correct manifest fragment, replacing the artifact-keyed `plugins/<artifact-id>/` path the cowork adapter uses today. The publishing pipeline assigns each selected artifact to its plugin by evaluating the plugin scope filters in declaration order, then invokes the adapter per artifact with the resolved plugin descriptor. This is a contract change to the `HarnessAdapter` SPI, which is why it goes through this proposal. For the tap and package formats (Hermes, Pi), where the install unit is the individual skill, the plugin descriptor groups skills into subtrees but does not change the install unit.

### Marketplace emitters per harness

The publishing pipeline selects a marketplace emitter per harness rather than the project-files adapter:

- **Claude (Code, Desktop, Cowork).** One emitter, derived from the existing Cowork marketplace layout (`pkg/adapter/layout.go:437`), changed so the plugin root is the resolved plugin name under the harness subtree and the marketplace name is the output's operator-chosen identifier. The `OpMergeJSON` reconciliation and the `PodiumOwnedKey` tag are unchanged. The three Claude surfaces consume the one emitted `.claude-plugin/marketplace.json`.
- **Codex.** A new emitter writes `.agents/plugins/marketplace.json` at the repository root and `<subtree>/<plugin>/.codex-plugin/plugin.json` per plugin, with `skills/`, `hooks/hooks.json`, and `.mcp.json` components.
- **Cursor.** A new emitter writes `.cursor-plugin/marketplace.json` at the repository root and `<subtree>/<plugin>/.cursor-plugin/plugin.json` per plugin, with `skills/`, `rules/*.mdc`, and `mcp.json` components.
- **Gemini.** A new emitter writes `gemini-extension.json` at the repository root, `commands/*.toml`, and the context file, treating the output's plugin set as one extension.
- **Pi.** A new emitter writes a root `package.json` carrying the `pi-package` keyword and a `pi.skills` array pointing at a skills subtree, with `skills/<name>/SKILL.md` per skill.
- **Hermes.** A new emitter writes the tap layout: `skills/<name>/SKILL.md` per skill with its `references/`, `scripts/`, and `assets/`, and no root manifest, matching the tap discovery rule.

Each emitter that carries a JSON manifest writes it with the `OpMergeJSON` Podium-owned merge so stale entries drop out on re-render, matching the cowork reconciliation. The Hermes tap and the Pi skills subtree reconcile through the sync lock file, because they carry no merged manifest.

### `podium publish` and the configurable workflow

`podium publish [--output <id>] [--config <path>] [--workdir <dir>] [--dry-run] [--check] [--json]` resolves the marketplace outputs and runs a fixed pipeline per output:

```
prepare (operator commands)  ->  render (Podium)  ->  publish (operator commands)
```

`prepare` is expected to place a checkout of the destination repository at the working directory. `render` materializes each harness's marketplace tree into that working directory through the materialization writer and the emitters above. `publish` is expected to take the rendered tree to the remote. Podium owns config resolution, the effective view, plugin assignment, rendering, reconciliation, change detection, variable injection, command sequencing, logging, and dry-run. The operator's commands own getting the repository to the working directory and taking the result to the remote. The ordering is the reason the phases exist: the checkout must precede the render so the render reconciles against existing repository content, and the commit must follow it. By default Podium allocates the working directory and `prepare` clones into it. `--workdir <dir>` points the render at an existing checkout, for example a CI `actions/checkout`, in which case `prepare` configures git or pulls rather than clones.

**Injected variables.** Podium passes context to the commands through environment variables rather than by interpreting git state:

- `$PODIUM_WORKDIR`: the per-output working and checkout directory.
- `$PODIUM_OUTPUT_ID`: the marketplace output identifier.
- `$PODIUM_GIT_REMOTE`, `$PODIUM_GIT_BRANCH`: from the output's `git:` block.
- `$PODIUM_COMMIT_MESSAGE`: rendered from `commit_message` with the change count and timestamp.
- `$PODIUM_CHANGED`: whether the render produced a diff against the checkout.
- `$PODIUM_CHANGE_SUMMARY`: a path to a JSON file describing the changed artifacts.
- The registry URL, the publishing identity, and the harness set.

**Execution semantics.** A command is an argv list under `run:`, executed directly without a shell, or a string under `sh:`, executed through `sh -c`. The pipeline inherits the ambient environment of the `podium publish` process and adds the injected variables, because git authentication relies on `SSH_AUTH_SOCK`, `GH_TOKEN`, and similar. The pipeline fails fast on the first non-zero exit, with per-command `continue_on_error`, `timeout`, and `skip_if_no_changes`, and an optional per-phase `on_error` cleanup list. `--dry-run` renders into a temporary directory and prints each command with variables substituted without running the `publish` phase. `--check` validates the config only.

**Trust boundary.** These commands run real subprocesses with the operator's privileges and ambient credentials. They are unrelated to the `MaterializationHook` SPI (§6.6), which is sandboxed to forbid subprocesses, network, and writes outside the destination, and they are unrelated to the `hook` artifact type. The commands come from operator-authored `publish.yaml`, the same trust boundary as a Makefile or a CI script, so a catalog author cannot inject a command. This is why publishing runs in an operator CLI and a server-side publisher is deferred.

### Triggers

The trigger is the existing `layer.ingested` event. The ingest orchestrator emits it once per completed layer cycle (`pkg/registry/ingest/orchestrator.go:172`; the comment at `:168` reads "one event per completed layer cycle"), so a single source commit that changes many artifacts yields one event rather than one `artifact.published` per artifact (`pkg/registry/ingest/ingest.go:790`). A CI job subscribes a webhook receiver to `layer.ingested` and runs `podium publish`, so one source commit triggers one publish across the artifacts it changed. No new event is required, and `podium publish` has no watch mode.

A `layer.ingested` event for a layer that contributes no artifacts to an output produces a render with no diff, which `skip_if_no_changes` suppresses, so an unrelated layer update does not produce an empty commit. An optional per-output layer filter that skips the render entirely for an unrelated layer is an open question.

### Webhook hardening (proposal 0004)

Pattern B registers a webhook receiver, which raises two webhook-subsystem concerns that proposal 0004 (webhook hardening) addresses. The receiver CRUD endpoints are not authorization-gated today (`pkg/registry/server/webhooks.go`), and a burst of `layer.ingested` events delivers one webhook each. Proposal 0004 adds receiver authorization with SSRF hardening, and a per-receiver debounce window that coalesces a burst into one batch delivery.

Both publishing patterns work without 0004. Pattern A uses no receiver. Pattern B works on the existing per-event delivery, where a burst produces one dispatch per event and the CI system's own concurrency control collapses the redundant runs; 0004 replaces that with one coalesced dispatch and a hardened receiver surface.

### GitHub Actions example

A GitHub Actions deployment uses one of two patterns, because GitHub starts a workflow from an external system only through the authenticated REST API (`repository_dispatch` or `workflow_dispatch`), and a Podium webhook receiver posts an HMAC-signed event body (`pkg/webhook/webhook.go:159`) that GitHub's dispatch endpoint does not accept.

**Pattern A, scheduled (no bridge).** A workflow in the marketplace repository runs `podium publish` on a cron. `skip_if_no_changes` makes an empty run a no-op, so a 5-to-15-minute poll is inexpensive. No webhook receiver is involved, and the debounce window is not used.

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

**Pattern B, event-driven (relay).** A Podium webhook receiver filtered to `layer.ingested`, with an optional debounce window (proposal 0004), posts to a small relay. The relay verifies the HMAC and calls GitHub `repository_dispatch`, which the workflow listens for.

```bash
# register the receiver; the response carries the HMAC secret for the relay
curl -X POST https://podium.acme.com/v1/webhooks -H "Authorization: Bearer $PODIUM_TOKEN" \
  -d '{"url":"https://relay.acme.com/podium","event_filter":["layer.ingested"],"debounce":"60s"}'
```

```yaml
# add to the workflow triggers
on:
  repository_dispatch: { types: [podium-layer-ingested] }
  workflow_dispatch: {}
```

The relay calls `POST https://api.github.com/repos/acme/agent-marketplace/dispatches` with `Authorization: Bearer <token>` and `{"event_type":"podium-layer-ingested"}`. With the proposal 0004 debounce window a burst of `layer.ingested` collapses into one batch delivery, so the relay fires one dispatch; without it the relay fires per event and the workflow's concurrency control collapses the redundant runs. The relay is the accepted bridge for Pattern B. A Podium receiver cannot call `repository_dispatch` directly because the auth and body differ, and teaching the registry a native dispatch mode is out of scope, because it would place a GitHub credential and host-specific egress in the registry.

In both patterns the registry credential `PODIUM_TOKEN` carries the publishing identity (see below), and the git push credential is GitHub's: `GITHUB_TOKEN` in Pattern A, or a deploy key the `prepare` clone uses when the workflow runs outside the marketplace repository. Podium never holds the git push credential.

### Rendering identity and effective view

The published marketplace reflects the publishing identity's effective view (§4.6) intersected with the plugin scope filters. `publish.yaml` carries `identity` so the operator selects the principal whose visibility defines what reaches the marketplace. A principal that can see restricted layers would render them into the output, so the publishing identity is a security-relevant setting, and a public marketplace is published under an identity scoped to the artifacts intended for it.

### Reconciliation

Re-rendering an output is idempotent. The materialization writer and the sync lock file remove files for artifacts that left the view, and the JSON manifests merge with the `PodiumOwnedKey` tag so stale entries drop out. The git diff after a render is the catalog delta, so `skip_if_no_changes` suppresses an empty commit when the delta is empty.

## Spec amendment: §6.7 harness adapters and marketplace emitters

`spec/06-mcp-server.md` §6.7 enumerates the harness adapters and a capability matrix, and describes Cowork as the marketplace target. The section predates the Codex, Cursor, Gemini, Pi, and Hermes distribution formats. Amend §6.7 to:

1. Record that an adapter has a project-files mode (consumed by `podium sync` into a workspace) and that a harness with a git-repo distribution also has a marketplace, extension, package, or tap mode reached through marketplace publishing (§7.7). State that `podium sync` no longer emits marketplaces, and that the `claude-cowork` marketplace path moves to publishing.
2. Replace the capability matrix's marketplace column with the distribution formats from the table in this proposal: the Claude, Codex, and Cursor marketplace manifests, the Gemini extension manifest, the Pi package manifest, and the Hermes tap, each at its fixed location, with OpenCode marked as npm-distributed and `none` as raw output, both out of scope for publishing.
3. State that Claude Code, Claude Desktop, and Claude Cowork share the `.claude-plugin/marketplace.json` format, so one Claude marketplace serves all three, and that the Claude, Codex, and Cursor manifests sit at distinct fixed locations and can coexist in one repository.
4. Document the `HarnessAdapter` contract change: the adapter `Source` carries a plugin descriptor (name, optional description, harness subtree prefix) so an emitter can render an artifact into a named plugin.

## Spec amendment: §7.7 marketplace publishing

Add a subsection §7.7 (Marketplace Publishing) under §7 (External Integration) in `spec/07-external-integration.md`, after §7.6 (SDKs), parallel to §7.5 (`podium sync`). The subsection defines:

- The marketplace output, the plugin, and the harness set (Concepts above), and the rule that publish targets are the harnesses with a git-repo distribution, with OpenCode and `none` excluded.
- The `publish.yaml` schema, its three-scope resolution and precedence mirroring §7.5.2, the `defaults` and `marketplaces` keys, the `workflow` grouping of `prepare` and `publish`, and the per-marketplace workflow override.
- The `podium publish` fixed pipeline (`prepare`, `render`, `publish`), the `--workdir` flag for rendering into an existing checkout, the injected variables, the execution semantics, and the trust boundary that the commands run with the operator's privileges from operator-authored config.
- The repository layout, the per-harness subtrees, the shared Claude marketplace, and the Gemini and Hermes whole-repository constraints.
- The publishing identity and effective-view rule (§4.6), and the reconciliation guarantee.
- The triggers: a CI job on the `layer.ingested` event (§7.3.2) and the operator CLI, with no watch mode. Burst coalescing is the registry's per-receiver webhook debounce, specified in proposal 0004, rather than a publishing config. Pattern B's relay is the accepted bridge, and a native dispatch mode in the registry is out of scope. A server-side publisher is named as out of scope.
- The GitHub Actions worked example, with the scheduled and event-driven patterns and the relay that bridges a Podium webhook to a GitHub `repository_dispatch`.

The subsection states that marketplace publishing is a derived, served output downstream of the registry, consistent with the §1.3 direction in which the registry is the served source of truth, and that it does not make a published repository an authoring source.

## Spec amendment: §7.3.2 trigger event

`spec/07-external-integration.md` §7.3.2 lists the outbound webhook events, including `layer.ingested`. Amend §7.3.2 to note that `layer.ingested`, which fires once per completed layer ingest cycle, is the event a CI marketplace-publish job subscribes to (§7.7), so one source commit triggers one publish across the artifacts it changed, and `artifact.published` is not used for this purpose. The receiver authorization and the per-receiver debounce window with its batch delivery body are specified in proposal 0004 (webhook hardening), which also amends §7.3.2.

## Spec amendment: §9.1 SPI table

`spec/09-extensibility.md` §9.1 lists the `HarnessAdapter` SPI. Amend the `HarnessAdapter` row's Purpose cell to note the adapter receives a plugin descriptor for marketplace rendering, and add a sentence that marketplace publishing introduces no new SPI, because the git workflow is operator-configured shell commands rather than a pluggable interface. No write-side git provider is added.

## Spec amendment: §2.2 consumer surfaces

`spec/02-architecture.md` §2.2 enumerates the consumer surfaces (SDKs, MCP server, `podium sync`). Add `podium publish` as a consumer surface that renders marketplace outputs and runs an operator workflow, distinct from `podium sync`, which writes a workspace tree.

## Spec amendment: glossary

Add to `spec/glossary.md`:

- **Marketplace output.** A named publishing destination declared in `publish.yaml`: a git repository, a harness set, a plugin list, and a workflow.
- **Plugin (publishing).** A named bundle of selected artifacts, defined by a scope filter, rendered into each harness's distribution layout.
- **Marketplace.** A git repository a harness imports to install plugins, holding a vendor manifest at a fixed location and per-plugin content. The extension, package, and tap formats are the analogous repository distributions for Gemini, Pi, and Hermes.

The existing "plugin" usage for the in-process SPI extensions (§9) is unchanged; the glossary entry distinguishes the publishing sense.

## Documentation changes

The `spec/` amendments above are normative. The non-normative documentation site under `docs/` also needs changes on acceptance:

- `docs/consuming/configure-your-harness.md`: correct the harness table and per-harness sections. Record that Codex, Cursor, Gemini, Pi, and Hermes have git-repo distribution; that Claude Code, Desktop, and Cowork share the `.claude-plugin/marketplace.json` format; and that marketplaces are produced by `podium publish` rather than `podium sync`. Remove the `podium sync --harness claude-cowork` marketplace instructions and point to the publishing guide.
- A new publishing guide (for example `docs/consuming/publishing.md` or a `docs/publishing/` section): the publish model, the `publish.yaml` schema, the `prepare` and `publish` workflow with the per-marketplace override, `podium publish` and its flags including `--workdir`, the per-harness repository layout, the publishing identity and effective-view rule, and the GitHub Actions worked example with both patterns.
- `docs/getting-started/how-it-works.md`: add `podium publish` as an output path alongside `podium sync` and the MCP server, and update the architecture description and its diagram.
- `docs/deployment/extending.md`: record that publishing adds no new SPI, because the workflow is operator-configured commands, and that the `HarnessAdapter` `Source` gains a plugin descriptor.
- The webhook-receiver documentation (the §7.3.2 consumer pages): document the `repository_dispatch` relay pattern for triggering CI from a Podium webhook. The receiver `debounce` field and the batch delivery body are documented under proposal 0004.
- `docs/reference/`: add the `podium publish` CLI reference and the `publish.yaml` reference, and add any new error code to `docs/reference/error-codes.md`, such as the rejection of a harness set that names a non-publish harness.
- `docs/assets/diagrams/`: a publish-flow diagram covering source change, ingest, `layer.ingested`, the CI trigger, `podium publish`, the push, and the harness import, following `doc-diagram-style.md` with an ASCII fallback.
- `docs/about/status.md`: record the feature status.
- `README.md`: mention marketplace publishing where it summarizes the consumer paths and harness support.

The new and edited prose follows `doc-style.md`, and the new diagram follows `doc-diagram-style.md`. New doc pages that make runnable claims carry a feature-named end-to-end test under the project's doc-testing convention.

## Open questions

1. **Command form default.** Argv list (`run:`) versus shell string (`sh:`) as the default. The argv form is exec'd directly with no shell and no injection; the shell form is convenient for pipes. Both are supported; the question is which the scaffold and docs default to. Argv is the safer default.
2. **Workflow override granularity.** A per-marketplace `workflow` replaces the default workflow in full. An alternative is a per-phase override (a marketplace `workflow.publish` that replaces only that phase). Full replacement is proposed; per-phase override is noted for review.
3. **Scaffolded default workflow.** Whether `podium init`-style generation writes a working git `defaults.workflow` block, so the common case carries no hand-written commands while the git logic stays in config rather than Go. Proposed yes.
4. **Change detection ownership.** Podium-evaluated `skip_if_no_changes` and `$PODIUM_CHANGED` versus relying on git exit codes (`git diff --quiet`). Both are exposed; the default guard is the open point.
5. **Shared-repository coexistence.** The Claude, Codex, and Cursor manifests are structurally compatible in one repository, because each importer reads only its own fixed path. No vendor doc blesses mixing them, so a real import on each should validate it before the shared-repository topology is committed. Gemini (one extension per repository) and Hermes (root `skills/` by default) place the tightest constraints, so an operator may give each its own output. A fallback is one repository per harness.
6. **Per-output layer filter.** Whether an output declares the layers it draws from, so a `layer.ingested` for an unrelated layer skips the render entirely rather than rendering a no-op and relying on `skip_if_no_changes`.
7. **Pi and Hermes layout confirmation.** The Pi `package.json` `pi.skills` path and the Hermes tap path are configurable; the emitters need a confirmed default layout, and the Hermes default tap path (`skills/`) interacts with the shared-repository question above.
8. **Catalog-native plugins.** This proposal declares plugins in output config. A later proposal could add a versioned, visibility-scoped plugin type to the catalog (via the `TypeProvider` SPI) when plugin membership needs to be authored and versioned like an artifact. The plugin metadata that a harness displays (name, description, version, author) is sourced from `publish.yaml` for now; a catalog-native plugin would source it from the catalog.
9. **Server-side publisher.** Deferred. A managed publisher inside the registry process would need per-repository credential storage, a per-output publish identity scoped per tenant, a durable job queue with debounce and retry, and outbound git egress from the registry, which the operator CLI avoids.
