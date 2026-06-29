# Proposal 0003: Multi-Harness Marketplace Publishing

- Issue: (to be filed)
- Status: Applied to spec (2026-06-25). Converged after 9 adversarial review rounds (14 findings fixed).
- Date: 2026-06-24

## Summary

Podium materializes the catalog into harness-native files through the `HarnessAdapter` SPI (§6.7), and `podium sync` writes those files to a local directory (§7.5). It has no way to publish the catalog as a git-repo plugin marketplace. The one adapter that emits a marketplace layout, `claude-cowork`, writes one plugin per artifact (`pkg/adapter/layout.go:437`, `coworkPlugin`), assembles a single fixed marketplace named `podium` (`coworkMarketplaceFragment`, `pkg/adapter/layout.go:507`), and leaves the git operations to the operator (the §6.7 Cowork docs show a manual `git add`, `git commit`, and `git push`). Git is an input source only (`pkg/layer/source/git.go`); the registry and the CLI never write to a repo.

This proposal adds marketplace publishing. It introduces an operator-authored `publish.yaml` that declares one or more marketplace outputs, a plugin grouping primitive that bundles a selected set of artifacts under a named plugin, a marketplace emitter per harness that has a git-repo distribution path, and a `podium publish` command that renders each output and runs an operator-configured workflow of shell commands to clone, commit, and push. The git interaction is configuration rather than embedded Go code: Podium owns rendering and runs the operator's `prepare` and `publish` commands around it. Publishing covers every harness in Podium's adapter roster (`pkg/adapter/adapter.go:162`) whose harness has a git-repo marketplace, extension, package, or tap; a harness without one is not a publish target. Rendering reuses the materialization writer (`pkg/materialize/atomic.go:205`), the scope-filter machinery (`pkg/sync/scope.go`), and the existing reconciliation through the sync lock file and the `PodiumOwnedKey` merge tag. Publishing is driven by a CI job on the existing `layer.ingested` event (`pkg/registry/ingest/orchestrator.go:172`), which fires once per completed layer ingest cycle, or by an operator running `podium publish`; a server-side publisher is out of scope for this proposal. Proposal 0004 (webhook hardening) adds the receiver authorization and the debounce window that the event-driven trigger relies on, and both publishing patterns function without it. `spec/` is read-only, so this proposal carries the amendments to §2 (§2.1 and §2.2), §6.7, §7, §9.1, and the glossary, as proposals 0001 and 0002 did.

## Current state and the gap

### Output today

A consumer reaches the catalog over three runtime paths (§2.1, §7): the language SDKs, the MCP server, and `podium sync`. The MCP server and `podium sync` both run the configured `HarnessAdapter` and write to a local destination through `materialize.Write(dest, files)` (`pkg/materialize/atomic.go:205`), atomically per file (`writeAtomic`, `pkg/materialize/atomic.go:348`). `podium sync` already supports multiple outputs from one config: `sync.yaml` carries a `targets:` list (`SyncConfig`, `pkg/sync/config.go`), and `podium sync --config` runs one materialization per entry, each with its own `{id, harness, target, profile, include, exclude, type}` (`TargetEntry`, `pkg/sync/config.go`). Selection within a target is a `ScopeFilter` of `include`, `exclude`, and `type` globs over canonical artifact IDs (`pkg/sync/scope.go`).

Git is an input source. `pkg/layer/source/git.go` clones a layer's tracked ref and reads manifests; the read-side `GitProvider` SPI (§9.1) verifies webhooks and fetches from GitHub, GitLab, and Bitbucket. There is no write path: no clone-and-push, no commit, and no `GitProvider` analog for output. The registry's only outbound effects are webhooks (`pkg/webhook/webhook.go`, §7.3.2) and operational notifications.

### The cowork marketplace is one plugin per artifact

`coworkPlugin` (`pkg/adapter/layout.go:437`) sets `pluginRoot := path.Join("plugins", id)` where `id` is the artifact ID, so each artifact becomes its own plugin under `plugins/<artifact-id>/` with its own `.claude-plugin/plugin.json`. `coworkMarketplaceFragment` (`pkg/adapter/layout.go:507`) emits one `OpMergeJSON` fragment per artifact into the repository-root `.claude-plugin/marketplace.json`, under the fixed marketplace name `podium`, tagged with `PodiumOwnedKey` so a re-render reconciles the listing. There is no concept of a plugin that bundles several artifacts, and no operator-chosen marketplace name. The adapter contract is the reason: `HarnessAdapter.Adapt(ctx, Source)` (`pkg/adapter/adapter.go:94`) receives one artifact at a time, so a multi-artifact grouping cannot be expressed inside an adapter and must be supplied above it.

### The adapters and docs predate the harnesses' marketplace formats

The `codex`, `cursor`, `gemini`, `pi`, and `hermes` adapters emit project-level files (`.codex/`, `.cursor/`, `.gemini/`, `.pi/`, `AGENTS.md`, `GEMINI.md`), which is correct for a workspace that consumes the files directly through `podium sync`. None emits a marketplace, extension, package, or tap layout, because these harnesses gained that distribution path after the adapters were written. The §6.7 prose describes Codex commands as user-scope only (`spec/06-mcp-server.md:207`) and frames Cowork as the only marketplace target (`spec/06-mcp-server.md:203`), and `docs/consuming/configure-your-harness.md` repeats both, which is now inaccurate. The §6.7.1 capability matrix grades type materialization, frontmatter-field fidelity, and rule and hook coverage per harness, and carries no marketplace or distribution column.

## Harness distribution formats (verified 2026-06-24)

The table records each harness's git-repo distribution path, verified against vendor documentation on 2026-06-24. A harness with such a path is a publish target; one without is excluded from publishing.

| Harness | Git-repo distribution | Manifest (fixed location) | Per-plugin manifest | Components | Publish target |
| --- | --- | --- | --- | --- | --- |
| `claude-code`, `claude-desktop`, `claude-cowork` | marketplace, shared format | `.claude-plugin/marketplace.json` (root) | `.claude-plugin/plugin.json` | `skills/`, `agents/`, `commands/`, `hooks/`, `.mcp.json` | yes (one Claude marketplace) |
| `codex` | marketplace | `.agents/plugins/marketplace.json` (root) | `.codex-plugin/plugin.json` | `skills/`, `hooks/hooks.json`, `.mcp.json` | yes |
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

The Claude, Codex, and Cursor manifests sit at distinct fixed locations, so they do not collide and can coexist in one repository, each read only by its own harness. The Gemini extension occupies the whole repository, and the Hermes tap defaults to a root `skills/` directory, so those two formats resist sharing a repository and an operator may give each its own output (Open questions).

## Decisions

These were settled during design and are recorded here as the basis for the specification below.

1. **Publish covers every supported harness with a git-repo distribution.** The publish targets are the marketplace, extension, package, and tap formats above: Claude (Code, Desktop, Cowork), Codex, Cursor, Gemini, Pi, and Hermes. A harness without a git-repo distribution, namely OpenCode (npm only) and `none` (raw output), is not a publish target. A publish output whose harness set names an excluded harness is rejected at config validation.
2. **The Claude surfaces share one marketplace, and `podium sync` no longer emits the plugin and marketplace layout.** Claude Code, Claude Desktop, and Claude Cowork read the same `.claude-plugin/marketplace.json`, so one Claude marketplace emitter serves all three, and a harness set that names more than one of them yields one Claude marketplace rather than a collision. The plugin and marketplace layout moves out of `podium sync`: the `claude-cowork` plugin and marketplace cells of the §6.7 target-path table at `spec/06-mcp-server.md:194` (the `skill`, `agent`, `command`, `rule`, `hook`, and `mcp-server` rows) are cleared, the matching §6.7.1 type-materialization cells (`spec/06-mcp-server.md:248-252`) drop cowork from ✓ to ✗ for `skill`, `agent`, `command`, and `mcp-server`, the §6.7.1 cells that the dropped layout was the sole producer of regrade to ✗ as well (the four `rule_mode` rows at `spec/06-mcp-server.md:270-273`, the `hook_event` row at `spec/06-mcp-server.md:274`, and the five frontmatter-field fidelity rows at `spec/06-mcp-server.md:258-262`, since `rule` and `hook` are graded by their dedicated rows and the field rows are measured on a `type: agent` carrier that cowork no longer materializes), and the cowork-specific marketplace sentence inside the paragraph at `spec/06-mcp-server.md:203` (the sentence beginning "Claude Cowork paths are relative to a plugin directory") is removed, while the `none` canonical-layout sentence and the config-merge target-resolution sentence in that same paragraph stay in §6.7. `publish` produces the Claude marketplace instead. The `claude-cowork` adapter today routes a `type: context` artifact to the harness-neutral `.podium/context/<id>/` directory (`contextOut`, `pkg/adapter/layout.go:41`) and routes every other type to the plugin layout (`coworkPlugin`, `pkg/adapter/layout.go:437`), per the dispatch in `ClaudeCowork.Adapt` (`pkg/adapter/builtins.go:41`). This proposal changes that dispatch: the cowork adapter retains the `type: context` branch and no longer emits the plugin layout for `skill`, `agent`, `command`, `rule`, `hook`, or `mcp-server`. Both runtime consumers run the one canonical `Adapt` (§2.2 `spec/02-architecture.md:103`): `podium sync` calls it directly (`pkg/sync/sync.go:257`) and the MCP server runs it for `load_artifact` through `adapter.DefaultRegistry()` (`cmd/podium-mcp/main.go:577,1673`). After the regrade a plugin-layout type on `claude-cowork` is a `✗` cell. `load_artifact` already fails a `✗`-cell artifact per §6.9 because it calls `adapter.TranslationError` before adapting (`cmd/podium-mcp/main.go:1659,1897`). `podium sync` does not enforce `✗` cells today: it calls `a.Adapt` directly with no guard (`pkg/sync/sync.go:257`), and an adapter returns `nil, nil` for a type it does not materialize, so without a change `podium sync` would silently produce no output for a `✗`-cell cowork type while `load_artifact` fails it. To keep the two canonical-`Adapt` consumers at parity, this proposal adds the §6.9 untranslatable guard to `podium sync` (§6.7 amendment item 1, the `podium sync` guard bullet), so after the change both fail a plugin-layout type on `claude-cowork` per §6.9 (`spec/06-mcp-server.md:242`), the enforcement spec §6.7 already mandates at materialization. A cowork user obtains those artifacts by importing the published Claude marketplace, which is the harness's native install path. The `type: context` materialization to `.podium/context/<id>/` stays in `podium sync` and `load_artifact`, identical across every adapter (§6.7 `spec/06-mcp-server.md:190`, §6.7.1 context cell `spec/06-mcp-server.md:250`). So `claude-cowork` is removed from the §6.7 sync type-target table for the plugin-layout types, retains the `type: context` project-scope row, and is retained as a consumer of the Claude marketplace produced by `publish`.
3. **Repository topology: one git repository per marketplace output, shared across that output's harness set.** Each format's manifest lives at its fixed location, and per-harness plugin content lives in per-harness subtrees the manifests reference. The harness set is the grouping lever: an operator who cannot or does not want to share one repository across two formats declares two outputs. Gemini (one extension per repository) and Hermes (root `skills/`) often take their own output, because each occupies a fixed repository-level location.
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

A plugin that bundles several artifacts cannot be expressed by the current per-artifact adapter call, so this proposal adds the target plugin to the adapter's input. The `Source` passed to `HarnessAdapter.Adapt` (`pkg/adapter/adapter.go:94`) gains a plugin descriptor: the plugin name, an optional description, and the harness subtree prefix. A marketplace emitter uses it to write under `<harness>/<plugin>/...` and to contribute the correct manifest fragment, replacing the artifact-keyed `plugins/<artifact-id>/` path the cowork adapter uses today. The publishing pipeline assigns each selected artifact to its plugin by evaluating the plugin scope filters in declaration order, then invokes the adapter per artifact with the resolved plugin descriptor for the artifact's component files. The per-plugin manifest entry is contributed once per plugin keyed by the plugin name rather than once per artifact, so an N-artifact plugin yields one plugin entry rather than N duplicates under the array-concatenating merge (Marketplace emitters below). This is a contract change to the `HarnessAdapter` SPI, which is why it goes through this proposal. For the tap and package formats (Hermes, Pi), where the install unit is the individual skill, the plugin descriptor groups skills into subtrees but does not change the install unit.

### Marketplace emitters per harness

The publishing pipeline selects a marketplace emitter per harness rather than the project-files adapter:

- **Claude (Code, Desktop, Cowork).** One emitter, derived from the existing Cowork marketplace layout (`pkg/adapter/layout.go:437`), changed so the plugin root is the resolved plugin name under the harness subtree and the marketplace name is the output's operator-chosen identifier. The marketplace fragment carries one entry per plugin keyed by plugin name, contributed once per plugin rather than once per artifact, because several artifacts now map to one plugin. The `PodiumOwnedKey` tag moves from the artifact ID (`coworkMarketplaceFragment`, `pkg/adapter/layout.go:507`) to the plugin name, and the emitter renders the plugin entry from the plugin descriptor (§"Plugin grouping and the HarnessAdapter contract") rather than from each artifact, so the single render produces one plugin entry per plugin. This is required because the `OpMergeJSON` merge concatenates same-key arrays without deduplication within a render (`deepMerge`, `pkg/materialize/merge.go:238`), so a per-artifact fragment would emit N duplicate entries for an N-artifact plugin. The three Claude surfaces consume the one emitted `.claude-plugin/marketplace.json`.
- **Codex.** A new emitter writes `.agents/plugins/marketplace.json` at the repository root and `<subtree>/<plugin>/.codex-plugin/plugin.json` per plugin, with `skills/`, `hooks/hooks.json`, and `.mcp.json` components.
- **Cursor.** A new emitter writes `.cursor-plugin/marketplace.json` at the repository root and `<subtree>/<plugin>/.cursor-plugin/plugin.json` per plugin, with `skills/`, `rules/*.mdc`, and `mcp.json` components.
- **Gemini.** A new emitter writes `gemini-extension.json` at the repository root, `commands/*.toml`, and the context file, treating the output's plugin set as one extension.
- **Pi.** A new emitter writes a root `package.json` carrying the `pi-package` keyword and a `pi.skills` array pointing at a skills subtree, with `skills/<name>/SKILL.md` per skill.
- **Hermes.** A new emitter writes the tap layout: `skills/<name>/SKILL.md` per skill with its `references/`, `scripts/`, and `assets/`, and no root manifest, matching the tap discovery rule.

Each emitter that carries a JSON manifest (Claude, Codex, Cursor) writes it with the `OpMergeJSON` Podium-owned merge so stale entries drop out on re-render, matching the cowork reconciliation. Each contributes one marketplace entry per plugin keyed by the plugin name, contributed once per plugin rather than once per artifact, because the merge concatenates same-key arrays without deduplication within a render (`deepMerge`, `pkg/materialize/merge.go:238`); a per-artifact entry would duplicate an N-artifact plugin N times in the manifest's `plugins` array. The Hermes tap and the Pi skills subtree reconcile through the sync lock file, because they carry no merged manifest.

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

**Trust boundary.** These commands run as subprocesses with the operator's privileges and ambient credentials. They are unrelated to the `MaterializationHook` SPI (§6.6), which is sandboxed to forbid subprocesses, network, and writes outside the destination, and they are unrelated to the `hook` artifact type. The commands come from operator-authored `publish.yaml`, the same trust boundary as a Makefile or a CI script, so a catalog author cannot inject a command. This is why publishing runs in an operator CLI and a server-side publisher is deferred.

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

1. Record that an adapter has a project-files mode (consumed by `podium sync` into a workspace and by the MCP server `load_artifact`) and that a harness with a git-repo distribution also has a marketplace, extension, package, or tap mode reached through marketplace publishing (§7.8). State that the cowork project-files materialization no longer emits the plugin and marketplace layout, and that the `claude-cowork` plugin and marketplace layout moves to publishing. The §6.7 target-path table, the §6.7.1 capability matrix (the type-materialization rows plus the frontmatter-field, `rule_mode`, and `hook_event` cells that the dropped layout produced), and the code mirror change in lockstep, because the target-path table and the §6.7.1 grades are normatively coupled (`spec/06-mcp-server.md:188`: "The §6.7.1 capability matrix grades each `✗` cell") and `pkg/adapter/capability.go` mirrors the §6.7.1 grades:
   - Clear the `claude-cowork` cells for the plugin-layout types in the per-type target-path table (`spec/06-mcp-server.md:194`) (the `skill`, `agent`, `command`, `rule`, `hook`, and `mcp-server` rows), because the cowork adapter no longer routes those types to a project-scope layout.
   - Drop `claude-cowork` from ✓ to ✗ in the §6.7.1 type-materialization matrix (`spec/06-mcp-server.md:248-252`) for `skill`, `agent`, `command`, and `mcp-server`. The `context` row stays ✓.
   - Regrade the §6.7.1 cells that the dropped plugin layout was the sole producer of, so the matrix stays consistent with the adapter and the matrix tests pass. `rule` and `hook` carry no type row; they are graded only by the `rule_mode` (`spec/06-mcp-server.md:270-273`) and `hook_event` (`spec/06-mcp-server.md:274`) rows, whose `claude-cowork` cells are ⚠ for the four `rule_mode` modes and ✓ for `hook_event`. The frontmatter-field fidelity rows (`spec/06-mcp-server.md:258-262`) are measured on a `type: agent` carrier, and the `claude-cowork` cells are ✓ for `description`, `mcpServers`, `delegates_to`, `requiresApproval`, and `sandbox_profile`. The sole producer of cowork output for all of these is the dropped `coworkPlugin` branch (`pkg/adapter/layout.go:437`): the rule fallback synthesizes a skill SKILL.md (`layout.go:446-461`), the hook writes `hooks/hooks.json` (`layout.go:467-470`), and the agent carrier writes `agents/<name>.md` (`layout.go:463-464`) that the field rows inspect. With the branch dropped, the cowork carrier produces no output, so drop `claude-cowork` from ✓/⚠ to ✗ in all four `rule_mode` rows, the `hook_event` row, and the five frontmatter-field rows, matching the §6.7.1 convention that "a harness with no project-level agent surface ... drops the row" (`spec/06-mcp-server.md:264`). Update the prose: remove `claude-cowork` from the pass-through `.md` agent list and add it to the "drops the row" list at `spec/06-mcp-server.md:264`, and delete the "Claude Cowork ships rules as skills, a fallback for every mode" sentence at `spec/06-mcp-server.md:276`. Also amend the migrating-surfaces note at `spec/06-mcp-server.md:207` ("Cursor and Cowork are likewise folding command files into skills; authors targeting those harnesses should prefer `type: skill`."): remove `Cowork` and the plural so it reads as a Cursor-only statement ("Cursor is likewise folding command files into skills; authors targeting it should prefer `type: skill`."), because after the regrade `type: skill` on `claude-cowork` is itself a ✗ cell, so the note would otherwise advise cowork authors toward a project-scope path the proposal removes. A cowork author obtains command and skill artifacts by importing the published Claude marketplace instead. After the regrade every `claude-cowork` cell for the plugin-layout types (`skill`, `agent`, `command`, `rule`, `hook`, `mcp-server`) is ✗, so the type, `rule_mode`, and `hook_event` rows the §6.9 untranslatable guard reads (`UsedCapabilities`, `pkg/adapter/capability.go:170-203`) all return ✗ for the carrier types, and `TranslationError` (`pkg/adapter/capability.go:279`) fails them, with the one exception the `UsedCapabilities` fix below closes for an unset `rule_mode`.
   - Update the `pkg/adapter/capability.go` mirror and its test mirror in lockstep: the third rune (the `claude-cowork` column) changes from `N` to `X` in the type rows `skill`/`agent`/`command`/`mcp-server` (`capability.go:84-88`), the five frontmatter-field rows (`capability.go:93-97`), the four `rule_mode` rows (`capability.go:106-109`), and the `hook_event` row (`capability.go:115`). The `context` type row (`capability.go:86`) is unchanged. The expected grid in `pkg/adapter/capability_test.go:18-32` mirrors the same runes and changes identically. `tools/matrix/matrices.go` lists `claude-cowork` only as a harness in the §6.7.1 axis (`matrices.go:54`) and audits the frontmatter-field, `rule_mode`, and `hook_event` rows rather than the type rows (`matrices.go:58-63`: "The five type-materialization rows ... are graded by `TestCapabilityMatrix_Types` and are not audited here"), so the axis of `matrices.go` does not change. The audited `claude-cowork` cell grades do change from ✓/⚠ to ✗, so the matrix-audit stub assertions for those cells in `pkg/adapter/capability_matrix_test.go` (the `// Matrix: §6.7.1 (claude-cowork, ...)` cases at `capability_matrix_test.go:132,149,167,185,203,229,254,279,304,331`) assert empty output or an absent marker for cowork, and `TestCapabilityMatrix_Types` exercises the cowork type-row change.
   - Close the unset-`rule_mode` hole in `UsedCapabilities` so the §6.9 guard fires for the default-mode rule. `UsedCapabilities` emits a `rule_mode` capability only when `art.RuleMode != ""` (`pkg/adapter/capability.go:196`), but an unset `rule_mode` is a valid ingest: ingest lint leaves an empty value to the §4.3 default `always` (`pkg/lint/required_fields.go:103-105`; `spec/04-artifact-model.md:204`) and the adapter defaults it to `always` only later, inside the adapter (`pkg/adapter/builtins.go:98-101`), after the guard has run. For such an artifact `UsedCapabilities` returns no capability, `TranslationError` finds no ✗ cell, and the §6.9 guard does not fire on either path, so the proposed `ClaudeCowork.Adapt` returns `nil, nil` and the rule is dropped silently with no file and no structured error. This is the common case (the default `always` mode), and it is a regression: today the `coworkPlugin` rule branch (`pkg/adapter/layout.go:446-461`) synthesizes a fallback `SKILL.md` regardless of `rule_mode`. Amend `UsedCapabilities` (`pkg/adapter/capability.go:196`) so a `type: rule` artifact with an empty `RuleMode` emits `Capability{Field: "rule_mode", Value: "always"}`, mirroring the adapter default at `pkg/adapter/builtins.go:98-101`, so an unset-mode rule presents the same `rule_mode: always` cell (now ✗ for cowork) as an explicit `rule_mode: always`. This reuses the existing `rule_mode` matrix row this proposal already regrades and adds no new matrix row, capability field, or SPI. The hook path is already safe because §4.3 requires `hook_event` (`pkg/lint/required_fields.go:45-47`), so `HookEvent` is non-empty and the `hook_event` capability always emits. Add a `UsedCapabilities` unit test for a `type: rule` artifact with no `rule_mode` that asserts a `rule_mode: always` capability, and extend the `podium sync` and `load_artifact` end-to-end coverage so a cowork rule with no `rule_mode` fails with `materialize.untranslatable` rather than producing no output.
   - Remove only the cowork-specific marketplace sentence from the paragraph at `spec/06-mcp-server.md:203` (the sentence "Claude Cowork paths are relative to a plugin directory inside a marketplace repository: ... `.claude-plugin/plugin.json` manifest."), relocating the `.claude-plugin/marketplace.json` root, the `plugins/<plugin>/` layout, and the `.claude-plugin/plugin.json` manifest description into the new §7.8 publishing subsection. Retain the other two sentences of that paragraph in §6.7: the `none` canonical-layout sentence ("`none` writes the canonical layout ... without translation.") and the config-merge target-resolution sentence ("The config-merge targets resolve to the harness's project-scope config file: `.claude/settings.json`, `.cursor/hooks.json` and `.cursor/mcp.json`, `.codex/config.toml`, `.gemini/settings.json`, and root `opencode.json`."), which disambiguate the bare config-merge cells at `spec/06-mcp-server.md:200-201` and apply to every config-merge harness rather than to cowork.
   - Retain the `claude-cowork` `type: context` materialization to the harness-neutral `.podium/context/<artifact-id>/` directory through `podium sync` and `load_artifact`, identical across every adapter, so leave the §6.7 context paragraph (`spec/06-mcp-server.md:190`) and the §6.7.1 context cell (`spec/06-mcp-server.md:250`) unchanged.
   - Change the cowork adapter dispatch in lockstep with the spec: `ClaudeCowork.Adapt` (`pkg/adapter/builtins.go:41`) keeps the `type: context` branch and drops the `coworkPlugin` branch for the other types, and the materialization golden fixture `test/materialization/testdata/golden/claude-cowork.golden` is regenerated. The 209-line fixture holds the marketplace listing at `claude-cowork.golden:1-57`, the `.podium/context/` output at `claude-cowork.golden:58-66`, and the per-plugin layout subtrees at `claude-cowork.golden:67-209`. The regeneration removes the marketplace listing (`1-57`) and the plugin subtrees (`67-209`) and retains only the `.podium/context/` block (`58-66`). The publishing emitter (§"Marketplace emitters per harness") carries forward the plugin and marketplace rendering that `coworkPlugin` and `coworkMarketplaceFragment` performed.
   - Add the §6.9 untranslatable enforcement to `podium sync` so it reaches parity with `load_artifact`. `load_artifact` calls `adapter.TranslationError(harnessID, art)` before adapting (`cmd/podium-mcp/main.go:1659,1897`) and fails a `✗`-cell artifact with `materialize.untranslatable`, but `podium sync` calls `a.Adapt` directly with no such guard (`pkg/sync/sync.go:257`), and a built-in adapter returns `nil, nil` for a type it does not materialize (for example `ClaudeDesktop.Adapt`, `pkg/adapter/builtins.go:27-28`). Without a guard, the proposed `ClaudeCowork.Adapt` would likewise return `nil, nil` for a plugin-layout type, so `podium sync` would silently produce no output while `load_artifact` returns a structured §6.9 error, and the two canonical-`Adapt` consumers would diverge in violation of §2.2 (`spec/02-architecture.md:103`). Spec §6.7 already mandates this enforcement at materialization for both paths (`spec/06-mcp-server.md:188`: the `✗` cell "is enforced at materialization (§6.9) against the harness the artifact loads onto"; `spec/06-mcp-server.md:242`: "materialization onto that harness otherwise fails per §6.9"); `podium sync` is a materialization path that does not yet implement it. Amend `pkg/sync/sync.go` to call `adapter.TranslationError(a.ID(), rec.Artifact)` per record before `a.Adapt` (mirroring the `load_artifact` call sites) and return its `materialize.untranslatable` error through the existing failure path (`pkg/sync/sync.go:263-264`), after the existing `target_harnesses:` opt-out skip (`pkg/sync/sync.go:253`). The guard reuses the existing §6.10 `materialize.untranslatable` code and introduces no new error code or SPI. A new end-to-end test asserts `podium sync` onto `claude-cowork` with a plugin-layout artifact and no `target_harnesses:` opt-out fails with `materialize.untranslatable`, matching `load_artifact`.

   State that `claude-cowork` is a publish consumer of the Claude marketplace whose only remaining project-scope materialization output is the `type: context` directory, and that a plugin-layout type on `claude-cowork` is a `✗` cell that both `podium sync` and `load_artifact` fail per §6.9. The `podium sync` failure depends on the §6.9 `podium sync` guard the bullet above adds, because `podium sync` does not enforce `✗` cells today. The failure for a `type: rule` artifact with an unset `rule_mode` depends on the `UsedCapabilities` fix above, because without it an empty `rule_mode` emits no capability and the guard finds no ✗ cell on either path.
2. Add a "Harness distribution formats" table to §6.7 as a new prose table, parallel to the existing per-type target-path table (`spec/06-mcp-server.md:194`) and distinct from the §6.7.1 capability matrices (`spec/06-mcp-server.md:246`, `:256`, `:268`), which carry no marketplace or distribution column. The new table records each harness's git-repo distribution from the table in this proposal: the Claude, Codex, and Cursor marketplace manifests, the Gemini extension manifest, the Pi package manifest, and the Hermes tap, each at its fixed location, with OpenCode marked as npm-distributed and `none` as raw output, both out of scope for publishing. This table records the distribution location per harness and is not graded, so it adds no new axis to the §6.7.1 capability matrix and no new column to `pkg/adapter/capability.go` or to `tools/matrix/matrices.go` (the matrix-audit mirror). The only change to those code mirrors is the `claude-cowork` cell regrade in `pkg/adapter/capability.go` from amendment item 1 above (the type, frontmatter-field, `rule_mode`, and `hook_event` rows); the addition of this distribution table introduces no further change to either file.
3. State that Claude Code, Claude Desktop, and Claude Cowork share the `.claude-plugin/marketplace.json` format, so one Claude marketplace serves all three, and that the Claude, Codex, and Cursor manifests sit at distinct fixed locations and can coexist in one repository.
4. Document the `HarnessAdapter` contract change: the adapter `Source` carries a plugin descriptor (name, optional description, harness subtree prefix) so an emitter can render an artifact into a named plugin.

## Spec amendment: §7.8 marketplace publishing

Add a subsection §7.8 (Marketplace Publishing) under §7 (External Integration) in `spec/07-external-integration.md`, after the existing §7.7 (Onboarding), parallel to §7.5 (`podium sync`). §7.7 (Onboarding) is the current last subsection of §7 (`spec/07-external-integration.md:597`), and three live cross-references resolve to it (`spec/07-external-integration.md:236`, `spec/06-mcp-server.md:471`, `spec/13-deployment.md:227`), so the new subsection takes the next free number §7.8 and the existing §7.7 and its cross-references are left untouched. The subsection defines:

- The marketplace output, the plugin, and the harness set (Concepts above), and the rule that publish targets are the harnesses with a git-repo distribution, with OpenCode and `none` excluded.
- The `publish.yaml` schema, its three-scope resolution and precedence mirroring §7.5.2, the `defaults` and `marketplaces` keys, the `workflow` grouping of `prepare` and `publish`, and the per-marketplace workflow override.
- The `podium publish` fixed pipeline (`prepare`, `render`, `publish`), the `--workdir` flag for rendering into an existing checkout, the injected variables, the execution semantics, and the trust boundary that the commands run with the operator's privileges from operator-authored config.
- The repository layout, the per-harness subtrees, the shared Claude marketplace, and the Gemini and Hermes whole-repository constraints.
- The publishing identity and effective-view rule (§4.6), and the reconciliation guarantee.
- The triggers: a CI job on the `layer.ingested` event (§7.3.2) and the operator CLI, with no watch mode. Burst coalescing is the registry's per-receiver webhook debounce, specified in proposal 0004, rather than a publishing config. Pattern B's relay is the accepted bridge, and a native dispatch mode in the registry is out of scope. A server-side publisher is named as out of scope.
- The GitHub Actions worked example, with the scheduled and event-driven patterns and the relay that bridges a Podium webhook to a GitHub `repository_dispatch`.

The subsection states that marketplace publishing is a derived, served output downstream of the registry, consistent with the §1.3 direction in which the registry is the served source of truth, and that it does not make a published repository an authoring source.

## Spec amendment: §7.3.2 trigger event

`spec/07-external-integration.md` §7.3.2 lists the outbound webhook events, including `layer.ingested`. Amend §7.3.2 to note that `layer.ingested`, which fires once per completed layer ingest cycle, is the event a CI marketplace-publish job subscribes to (§7.8), so one source commit triggers one publish across the artifacts it changed, and `artifact.published` is not used for this purpose. The receiver authorization and the per-receiver debounce window with its batch delivery body are specified in proposal 0004 (webhook hardening), which also amends §7.3.2.

## Spec amendment: §9.1 SPI table

`spec/09-extensibility.md` §9.1 lists the `HarnessAdapter` SPI. Amend the `HarnessAdapter` row's Purpose cell to note the adapter receives a plugin descriptor for marketplace rendering, and add a sentence that marketplace publishing introduces no new SPI, because the git workflow is operator-configured shell commands rather than a pluggable interface. No write-side git provider is added.

## Spec amendment: §2.1 and §2.2 consumer surfaces

The closed enumeration of consumer surfaces lives in §2.1. `spec/02-architecture.md` §2.1 line 7 names the three consumers in one sentence ("the Podium MCP server (...), `podium sync` (...), and the language SDKs (...)"), and the §2.1 component diagram (`spec/02-architecture.md:28-38`) shows the same three as columns. §2.2 (`spec/02-architecture.md:85-95`) lists each component under its own bold header and carries no single closed enumeration sentence. The §1.3 principle "One registry, multiple consumer paths" (`spec/01-overview.md:56`) names the same closed three-item list.

Apply the amendment to all three locations so §2 and §1.3 stay consistent:

- §2.1 line 7: extend the consumer enumeration sentence and the surrounding "read from the registry over HTTP" framing so it accounts for `podium publish` as a derived, served output that renders marketplace repositories and runs an operator workflow. `podium publish` reads the effective view over the same HTTP API as the other consumers, then renders and pushes to a git remote rather than writing a workspace tree like `podium sync`.
- §2.1 component diagram (`spec/02-architecture.md:28-38`): note that the diagram gains `podium publish` as a consumer alongside the MCP server, `podium sync`, and the SDKs, or carries a caption that `podium publish` is a further consumer surface defined in §7.8.
- §2.2 (`spec/02-architecture.md:85-95`): add a component-responsibilities paragraph for `podium publish`, parallel to the `podium sync` paragraph, describing it as the CLI and library that renders marketplace outputs through the marketplace emitters and runs an operator-configured workflow (§7.8), distinct from `podium sync`, which writes a workspace tree.
- §1.3 principle (`spec/01-overview.md:56`): the "One registry, multiple consumer paths" principle names the runtime consumer paths (SDKs, MCP server, and `podium sync`) that load the effective view at runtime. `podium publish` is a derived, served output downstream of those paths (§7.8) rather than a runtime consumer path, so leave the §1.3 closed list naming the three runtime paths and reference `podium publish` only in the §7.8 publishing subsection. This keeps the §1.3 principle consistent with the §1.3 framing in which the registry is the served source of truth (proposal §7.8 amendment).

## Spec amendment: glossary

Add to `spec/glossary.md`:

- **Marketplace output.** A named publishing destination declared in `publish.yaml`: a git repository, a harness set, a plugin list, and a workflow.
- **Plugin (publishing).** A named bundle of selected artifacts, defined by a scope filter, rendered into each harness's distribution layout.
- **Marketplace.** A git repository a harness imports to install plugins, holding a vendor manifest at a fixed location and per-plugin content. The extension, package, and tap formats are the analogous repository distributions for Gemini, Pi, and Hermes.

The existing "plugin" usage for the in-process SPI extensions (§9) is unchanged; the glossary entry distinguishes the publishing sense.

## Documentation changes

The `spec/` amendments above are normative. The non-normative documentation site under `docs/` also needs changes on acceptance:

- `docs/consuming/configure-your-harness.md`: correct the harness table and per-harness sections. Record that Codex, Cursor, Gemini, Pi, and Hermes have git-repo distribution; that Claude Code, Desktop, and Cowork share the `.claude-plugin/marketplace.json` format; and that marketplaces are produced by `podium publish` rather than `podium sync`. Remove the `podium sync --harness claude-cowork` marketplace instructions and point to the publishing guide. Record that `podium sync` onto `claude-cowork` for a plugin-layout type now fails with `materialize.untranslatable` per §6.9, the same as `load_artifact`, and that a cowork user obtains those artifacts by importing the published Claude marketplace.
- A new publishing guide (for example `docs/consuming/publishing.md` or a `docs/publishing/` section): the publish model, the `publish.yaml` schema, the `prepare` and `publish` workflow with the per-marketplace override, `podium publish` and its flags including `--workdir`, the per-harness repository layout, the publishing identity and effective-view rule, and the GitHub Actions worked example with both patterns.
- `docs/getting-started/how-it-works.md`: add `podium publish` as an output path alongside `podium sync` and the MCP server, and update the architecture description and its diagram.
- `docs/deployment/extending.md`: record that publishing adds no new SPI, because the workflow is operator-configured commands, and that the `HarnessAdapter` `Source` gains a plugin descriptor.
- The webhook-receiver documentation (the §7.3.2 consumer pages): document the `repository_dispatch` relay pattern for triggering CI from a Podium webhook. The receiver `debounce` field and the batch delivery body are documented under proposal 0004.
- `docs/reference/`: add the `podium publish` CLI reference and the `publish.yaml` reference, and add any new error code to `docs/reference/error-codes.md`, such as the rejection of a harness set that names a non-publish harness.
- `docs/assets/diagrams/`: a publish-flow diagram covering source change, ingest, `layer.ingested`, the CI trigger, `podium publish`, the push, and the harness import, following `doc-diagram-style.md` with an ASCII fallback.
- `docs/about/status.md`: record the feature status.
- `README.md`: mention marketplace publishing where it summarizes the consumer paths and harness support.

The new and edited prose follows `doc-style.md`, and the new diagram follows `doc-diagram-style.md`. New doc pages that make runnable claims carry a feature-named end-to-end test under the project's doc-testing convention.

## Resolved in adversarial review

### Pass 1 (2026-06-25, automated)

- **§7.7 collision with Onboarding.** The new subsection was numbered §7.7, which already names Onboarding, the last subsection of §7 (`spec/07-external-integration.md:597`), with three live cross-references resolving to it (`spec/07-external-integration.md:236`, `spec/06-mcp-server.md:471`, `spec/13-deployment.md:227`). Renumbered the new subsection to §7.8 and updated every §7.7 reference in the proposal (the §6.7 amendment item 1, the subsection heading and intro, and the §7.3.2 amendment) to §7.8, leaving the existing §7.7 and its cross-references untouched.
- **Nonexistent "marketplace column" in the capability matrix.** §6.7 amendment item 2 said to "replace the capability matrix's marketplace column," but neither the §6.7 target-path table (`spec/06-mcp-server.md:194`) nor the §6.7.1 capability matrices (`spec/06-mcp-server.md:246`, `:256`, `:268`) carry a marketplace or distribution column, and the code mirror (`pkg/adapter/capability.go`) and the matrix-audit gate (`tools/matrix/matrices.go`) have no such axis. Reworded item 2 to add a new, ungraded "Harness distribution formats" prose table to §6.7 instead of replacing a column, and stated that the code mirror and the matrix-audit gate are unchanged. Corrected the supporting framing to attribute the "Cowork as only marketplace target" claim to the §6.7 prose (`spec/06-mcp-server.md:203`) and docs rather than to the capability matrix.
- **Cowork sync output left undefined.** Removing the cowork marketplace from `podium sync` left the §6.7 cowork target-path column and the line-203 prose describing marketplace output that `sync` no longer produces, with no defined cowork sync mode. The cowork adapter emits only the plugin and marketplace layout (`coworkPlugin`, `pkg/adapter/layout.go:437`), so Decision 2 and §6.7 amendment item 1 now make `claude-cowork` publish-only: the cowork column is removed from the §6.7 target-path table, the cowork marketplace-repository paragraph (`spec/06-mcp-server.md:203`) moves into the §7.8 publishing subsection, and `claude-cowork` is retained as a consumer of the Claude marketplace that `publish` produces.
- **Per-artifact marketplace fragment duplicated the plugin entry.** With several artifacts mapping to one plugin and the `OpMergeJSON` merge concatenating same-key arrays without deduplication within a render (`deepMerge`, `pkg/materialize/merge.go:238`), a per-artifact marketplace fragment would emit N duplicate plugin entries for an N-artifact plugin. Specified that each JSON-manifest emitter (Claude, Codex, Cursor) contributes one marketplace entry per plugin keyed by the plugin name, contributed once per plugin rather than once per artifact, with `PodiumOwnedKey` moved from the artifact ID to the plugin name, and removed the claim that `OpMergeJSON` and the `PodiumOwnedKey` tag are unchanged.

### Pass 2 (2026-06-25, automated)

- **Consumer-surface enumeration attributed to §2.2 instead of §2.1 and §1.3.** The proposal's only §2 amendment targeted §2.2 and quoted a closed enumeration ("SDKs, MCP server, `podium sync`") that does not appear in §2.2. The closed enumeration sentence lives at §2.1 line 7 (`spec/02-architecture.md:7`) and the §2.1 component diagram (`spec/02-architecture.md:28-38`) repeats it as three columns, while §2.2 (`spec/02-architecture.md:85-95`) uses separate bold per-component headers with no single enumeration sentence. The §1.3 principle "One registry, multiple consumer paths" (`spec/01-overview.md:56`) carries the same closed three-item list. Retitled the amendment to "§2.1 and §2.2 consumer surfaces" and rewrote it to extend the §2.1 enumeration sentence and the §2.1 diagram, add a §2.2 component-responsibilities paragraph for `podium publish`, and keep the §1.3 closed list naming the three runtime consumer paths while referencing `podium publish` as a derived, served output in §7.8.
- **`claude-cowork` `type: context` sync output contradicted the publish-only claim.** Decision 2 and §6.7 amendment item 1 said the cowork adapter "emits only the plugin and marketplace layout" and "produces no `podium sync` project-scope output", but `ClaudeCowork.Adapt` (`pkg/adapter/builtins.go:41`) routes a `type: context` artifact to the harness-neutral `.podium/context/<id>/` directory (`contextOut`, `pkg/adapter/layout.go:41`), which is a genuine `podium sync` project-scope materialization. The proposal left the §6.7 context paragraph (`spec/06-mcp-server.md:190`), the §6.7.1 context cell (`spec/06-mcp-server.md:250`), and the golden fixture (`test/materialization/testdata/golden/claude-cowork.golden:58`) unedited, so the unqualified claim contradicted the unchanged spec and code. Narrowed both claims to the plugin and marketplace layout (`skill`, `agent`, `command`, `rule`, `hook`, `mcp-server`): only that layout moves to publishing, and `claude-cowork` retains the `type: context` materialization to `.podium/context/<id>/` through `podium sync`. The §6.7 target-path table clears the cowork plugin-layout cells while keeping the context row, and the §6.7 context paragraph and §6.7.1 context cell stay unchanged.

### Pass 3 (2026-06-25, automated)

- **Cleared §6.7 cowork cells left the coupled §6.7.1 matrix and its `capability.go` mirror grading cowork ✓.** §6.7 amendment item 1 cleared the `claude-cowork` plugin-layout cells in the §6.7 target-path table, but the §6.7.1 type-materialization matrix (`spec/06-mcp-server.md:248-252`) still graded cowork ✓ for `skill`, `agent`, `command`, and `mcp-server`, and item 2 asserted that `pkg/adapter/capability.go` and `tools/matrix/matrices.go` were unchanged. The two tables are normatively coupled (`spec/06-mcp-server.md:188`: "The §6.7.1 capability matrix grades each `✗` cell"), and `capability.go:84-88` mirrors the §6.7.1 grades with cowork as the third column rune. Because `podium sync` (`pkg/sync/sync.go:257`) and the MCP server `load_artifact` (`cmd/podium-mcp/main.go:577,1673`) both run the one canonical `ClaudeCowork.Adapt` (§2.2 `spec/02-architecture.md:103`), keeping the §6.7.1 ✓ grade while the adapter stops emitting the layout would contradict the spec, and leaving the layout in place would falsify the "no longer emits" claim. Made the change a coordinated edit: §6.7 amendment item 1 now also drops cowork from ✓ to ✗ in the §6.7.1 type-materialization matrix for `skill`, `agent`, `command`, and `mcp-server` (the `rule` and `hook` types have no type row; the `context` row stays ✓), updates the matching `capability.go:84-88` runes from `N` to `X`, changes the `ClaudeCowork.Adapt` dispatch to drop the `coworkPlugin` branch, and regenerates the `claude-cowork.golden` fixture. Item 2 now states that `matrices.go` lists cowork only as a harness in the §6.7.1 axis and audits the frontmatter, `rule_mode`, and `hook_event` rows rather than the type rows, so its axis does not change, and removed the blanket "capability.go ... unchanged" claim. (The §2.2 parity statement in this entry was corrected in Pass 4: `TranslationError` fires only on the `load_artifact` path, never in `podium sync`, so the existing enforcement did not give the two paths parity; Pass 4 adds the §6.9 guard to `podium sync` and regrades the `rule_mode`, `hook_event`, and frontmatter-field cowork cells so the regrade is complete.)
- **§6.7 amendment removed a mixed paragraph, deleting general config-merge and `none`-output prose.** Decision 2 and §6.7 amendment item 1 directed removing the whole paragraph at `spec/06-mcp-server.md:203`, but that paragraph holds three sentences: the `none` canonical-layout sentence, the cowork marketplace sentence to relocate, and the config-merge target-resolution sentence that disambiguates the bare config-merge cells at `spec/06-mcp-server.md:200-201` (the only place the fully-qualified `.claude/settings.json`, `.codex/config.toml`, and `.gemini/settings.json` paths appear). Removing the whole paragraph would orphan the config-merge cells for claude-code, cursor, codex, gemini, and opencode. Narrowed both instructions to remove only the cowork-specific sentence and explicitly retain the `none` and config-merge sentences in §6.7. Also corrected the supporting citation at the current-state section: the "Codex commands are user-scope" statement is at `spec/06-mcp-server.md:207`, not `:203`, so the citation now points at `:207` for that claim and at `:203` for the Cowork-marketplace framing.

### Pass 4 (2026-06-25, automated)

- **Golden-fixture line citations mislocated the cowork plugin layout.** §6.7 amendment item 1 cited the plugin and marketplace entries at `claude-cowork.golden:1-57` and the surviving `.podium/context/` output at `claude-cowork.golden:58`. In the 209-line fixture, lines 1-57 hold only the `.claude-plugin/marketplace.json` listing, lines 58-66 hold the `.podium/context/` block, and lines 67-209 hold the per-plugin layout subtrees (`plugins/team/<plugin>/...`). An implementer who removed 1-57 and kept 58 would leave the plugin subtrees at 67-209 in the golden and fail the materialization assertion. Corrected the citations: the regeneration removes the marketplace listing (`1-57`) and the plugin subtrees (`67-209`) and retains only the `.podium/context/` block (`58-66`).
- **`podium sync` does not enforce §6.9, so the "both fail per §6.9 / stay at parity" claim was wrong.** Decision 2, §6.7 amendment item 1, and the Pass 3 resolution attributed `podium sync`/`load_artifact` parity to the existing `✗`-cell enforcement. `adapter.TranslationError` is invoked only on the `load_artifact` path (`cmd/podium-mcp/main.go:1659,1897`); `pkg/sync` has no reference to it, `pkg/sync/sync.go:257` calls `a.Adapt` directly, and an adapter returns `nil, nil` for an unsupported type, so the proposed `ClaudeCowork.Adapt` would make `podium sync` silently no-op while `load_artifact` failed, diverging the two canonical-`Adapt` consumers in violation of §2.2. Added a §6.9 `podium sync` guard bullet to §6.7 amendment item 1: `pkg/sync/sync.go` calls `adapter.TranslationError(a.ID(), rec.Artifact)` per record before `a.Adapt`, after the `target_harnesses:` opt-out skip, returning `materialize.untranslatable` through the existing failure path (`pkg/sync/sync.go:263-264`), with a new end-to-end test. Spec §6.7 already mandates this enforcement at materialization (`spec/06-mcp-server.md:188`, `:242`), and the guard reuses the existing §6.10 code, so no new error code or SPI is introduced. Removed the false Pass 3 statement that `TranslationError` already fails a `✗`-cell type for `podium sync`.
- **`rule` and `hook` on `claude-cowork` are not `✗` type cells, so the §6.9 gate never fired for them.** The proposal cleared the cowork `rule` and `hook` target-path cells and dropped the `coworkPlugin` branch (the sole producer of cowork rule and hook output, `pkg/adapter/layout.go:446-461,467-470`), but `rule` and `hook` carry no type row; they are graded only by the `rule_mode` (⚠) and `hook_event` (✓) rows, which the proposal left unchanged. `TranslationError` fires only on a `SupportUnsupported` cell (`pkg/adapter/capability.go:285`), so a cowork rule or hook would produce no file and no error in either path: a silent drop that contradicts the unchanged §6.7.1 grades and the "✗ cell ... fail per §6.9" claim. Extended §6.7 amendment item 1 to regrade the four `rule_mode` rows (`spec/06-mcp-server.md:270-273`) and the `hook_event` row (`spec/06-mcp-server.md:274`) for `claude-cowork` to ✗, flip the matching runes to `X` in `pkg/adapter/capability.go:106-109,115` and the test mirror `pkg/adapter/capability_test.go:28-32`, and delete the "Claude Cowork ships rules as skills" sentence at `spec/06-mcp-server.md:276`, so `TranslationError` fails a cowork rule (via `rule_mode`) and hook (via `hook_event`) per §6.9. (The "fails a cowork rule via `rule_mode`" claim holds for an explicit `rule_mode` only; Pass 5 closes the unset-`rule_mode` case, where `UsedCapabilities` emits no `rule_mode` capability for the default `always` mode (`pkg/adapter/capability.go:196`) so the guard found no ✗ cell, by emitting a `rule_mode: always` capability for an empty `RuleMode`.)
- **Dropping cowork agent materialization left the §6.7.1 frontmatter-field cells stale and broke the matrix tests.** The five frontmatter-field fidelity rows (`spec/06-mcp-server.md:258-262`) are measured on a `type: agent` carrier that cowork stops materializing, so per the §6.7.1 convention at `spec/06-mcp-server.md:264` ("a harness with no project-level agent surface ... drops the row") cowork should drop those rows, yet the proposal kept them ✓ and asserted the capability mirror changed only in the type rows. `assertFieldCell` (`pkg/adapter/capability_matrix_test.go:54-70`) and `assertTypeCell` (`:75-91`) materialize the carrier through `Adapt` and require a present marker or non-empty output for a non-✗ grade, so the cowork field, `rule_mode`, and `hook_event` cells would have left the suite red. Extended §6.7 amendment item 1 to drop cowork from ✓ to ✗ in the five frontmatter-field rows, flip the matching runes to `X` in `pkg/adapter/capability.go:93-97` and the test mirror, update the pass-through and drops-the-row lists at `spec/06-mcp-server.md:264`, and acknowledge that the `TestCapabilityMatrix_*` field, rule, and hook expectations for cowork move to asserting empty output or an absent marker. Corrected the claim that the cowork code-mirror change is type-row-only: the axis of `tools/matrix/matrices.go` is unchanged, but the audited cowork cell grades (field, `rule_mode`, `hook_event`) change from ✓/⚠ to ✗, so their matrix-audit stub assertions change.

### Pass 5 (2026-06-25, automated)

- **Stale §6.7 migrating-surfaces note advised cowork authors toward a now-✗ `type: skill`.** §6.7 amendment item 1 enumerated the §6.7 prose edits (the marketplace sentence at `spec/06-mcp-server.md:203`, the pass-through and drops-the-row lists at `spec/06-mcp-server.md:264`, and the "Claude Cowork ships rules as skills" sentence at `spec/06-mcp-server.md:276`) but omitted the migrating-surfaces note at `spec/06-mcp-server.md:207` ("Cursor and Cowork are likewise folding command files into skills; authors targeting those harnesses should prefer `type: skill`."). After the regrade dropped cowork `skill` and `command` to ✗, that note advised cowork authors toward a project-scope path the proposal removes: a `type: skill` artifact on `claude-cowork` is now a ✗ cell that fails per §6.9. Added a §6.7 prose edit to amendment item 1 that removes `Cowork` and the plural from the line-207 note so it reads as a Cursor-only statement, and recorded that a cowork author obtains command and skill artifacts by importing the published Claude marketplace.
- **A cowork rule with an unset (default) `rule_mode` was dropped silently rather than failed per §6.9.** §6.7 amendment item 1 and the Pass 4 resolution claimed that `TranslationError`, reading the regraded `rule_mode` rows, fails a cowork rule per §6.9. `UsedCapabilities` emits a `rule_mode` capability only when `art.RuleMode != ""` (`pkg/adapter/capability.go:196`), but an unset `rule_mode` ingests successfully and defaults to `always` only inside the adapter (`pkg/adapter/builtins.go:98-101`; `pkg/lint/required_fields.go:103-105`), after the §6.9 guard has run. For such an artifact `UsedCapabilities` returns no capability, `TranslationError` returns nil, and the proposed `ClaudeCowork.Adapt` returns `nil, nil`, so the default-mode rule (the common case) produced no file and no structured error on either path: a regression from today's `coworkPlugin` fallback `SKILL.md` (`pkg/adapter/layout.go:446-461`). Added a `UsedCapabilities` fix bullet to §6.7 amendment item 1 that emits `Capability{Field: "rule_mode", Value: "always"}` for a `type: rule` artifact with an empty `RuleMode`, mirroring the adapter default, so an unset-mode rule presents the same ✗ `rule_mode: always` cell as an explicit one and the guard fires on both paths. The fix reuses the existing `rule_mode` matrix row and adds no new capability field or SPI; the hook path is already safe because §4.3 requires `hook_event`. Qualified the "both fail per §6.9" statement at amendment item 1 and the Pass 4 resolution to depend on this fix for the unset-`rule_mode` case, and added the unit and end-to-end coverage for it.

## Open questions

1. **Command form default.** Argv list (`run:`) versus shell string (`sh:`) as the default. The argv form is exec'd directly with no shell and no injection; the shell form is convenient for pipes. Both are supported; the question is which the scaffold and docs default to. Argv is the safer default.
2. **Workflow override granularity.** A per-marketplace `workflow` replaces the default workflow in full. An alternative is a per-phase override (a marketplace `workflow.publish` that replaces only that phase). Full replacement is proposed; per-phase override is noted for review.
3. **Scaffolded default workflow.** Whether `podium init`-style generation writes a working git `defaults.workflow` block, so the common case carries no hand-written commands while the git logic stays in config rather than Go. Proposed yes.
4. **Change detection ownership.** Podium-evaluated `skip_if_no_changes` and `$PODIUM_CHANGED` versus relying on git exit codes (`git diff --quiet`). Both are exposed; the default guard is the open point.
5. **Shared-repository coexistence.** The Claude, Codex, and Cursor manifests are structurally compatible in one repository, because each importer reads only its own fixed path. No vendor doc blesses mixing them, so an import test on each harness should validate it before the shared-repository topology is committed. Gemini (one extension per repository) and Hermes (root `skills/` by default) resist sharing a repository, so an operator may give each its own output. A fallback is one repository per harness.
6. **Per-output layer filter.** Whether an output declares the layers it draws from, so a `layer.ingested` for an unrelated layer skips the render entirely rather than rendering a no-op and relying on `skip_if_no_changes`.
7. **Pi and Hermes layout confirmation.** The Pi `package.json` `pi.skills` path and the Hermes tap path are configurable; the emitters need a confirmed default layout, and the Hermes default tap path (`skills/`) interacts with the shared-repository question above.
8. **Catalog-native plugins.** This proposal declares plugins in output config. A later proposal could add a versioned, visibility-scoped plugin type to the catalog (via the `TypeProvider` SPI) when plugin membership needs to be authored and versioned like an artifact. The plugin metadata that a harness displays (name, description, version, author) is sourced from `publish.yaml` for now; a catalog-native plugin would source it from the catalog.
9. **Server-side publisher.** A managed publisher inside the registry process is deferred, because it would need per-repository credential storage, a per-output publish identity scoped per tenant, a durable job queue with debounce and retry, and outbound git egress from the registry, which the operator CLI avoids.
