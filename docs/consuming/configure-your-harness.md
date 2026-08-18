---
title: Configure your harness
nav_order: 1
description: Cross-harness delivery. The harness roster, where each artifact type lands on disk, and the podium sync and MCP server setup per harness.
---

# Configure your harness

This page documents cross-harness delivery. Artifacts are authored once in the canonical format, and a harness adapter translates each one into the layout its runtime expects. The same `finance/close-reporting/run-variance-analysis` directory becomes `.claude/skills/run-variance-analysis/SKILL.md` for Claude Code and `.cursor/skills/run-variance-analysis/SKILL.md` for Cursor, and the scripts and references bundled in that directory ride along into both. Nothing in the catalog changes when a workspace switches harness; the adapter changes.

A harness can consume Podium artifacts in three ways. Pick the ones that fit per harness:

- **Filesystem materialization** via `podium sync`. Writes harness-native files into a workspace; the harness's own filesystem discovery picks them up. Works against either a filesystem-source registry or a running Podium server. No runtime calls.
- **Runtime discovery** via the Podium MCP server. The agent calls `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` mid-session and materializes only what it needs. Requires a Podium server.
- **Marketplace publishing** via a `podium sync` target of `kind: marketplace`. Renders the catalog into a git-repo plugin marketplace, extension, package, or tap that the harness imports. Available for the harnesses with a git-repo distribution: Claude Code, Claude Desktop, Claude Cowork, Codex, Cursor, Gemini, Pi, and Hermes. See [Publishing](publishing).

Most harnesses handle both filesystem materialization and runtime discovery. Use the per-harness section below.

Materialization delivers an artifact's bundled files wherever the target layout has a place for them. A skill's `scripts/`, `references/`, and `assets/` ride inside the skill folder, and the files bundled with an `agent`, `command`, `hook`, or `context` artifact land in the bucket the per-harness table names. A harness adapter renders a `rule` artifact as a single file or an injected block and an `mcp-server` artifact as a config-merge fragment, so files bundled in those two directories are dropped; `--harness none` writes them under `<destination>/<artifact-id>/` along with every other type. A marketplace rendering carries bundled files for its `skill` and `hook` components, and for a `rule` on the Claude marketplace, where a rule ships as a plugin skill. Every other marketplace component carries none. The per-harness tables below name the destination for each artifact type and for its bundled files. [Bundled resources](../authoring/bundled-resources) covers what an artifact directory may contain. To deliver a subset of the catalog into a workspace instead of the whole effective view, see [Selective materialization](selective-materialization).

---

## Common pieces

The Podium MCP server is a stdio binary the harness spawns alongside its other MCP servers. The same env-var contract applies regardless of harness:

| Variable | Purpose |
|:--|:--|
| `PODIUM_REGISTRY` | Registry source. The MCP server requires a server URL (`http://` or `https://`) and aborts startup with `config.filesystem_registry_unsupported` when given a filesystem path. `podium sync` accepts a URL or a filesystem path. |
| `PODIUM_HARNESS` | Harness adapter to use. Pass `none` for canonical raw output. |
| `PODIUM_OVERLAY_PATH` | Optional. Workspace local-overlay path; falls back to `<workspace>/.podium/overlay/` when MCP roots resolve. |
| `PODIUM_IDENTITY_PROVIDER` | `oauth-device-code` (developer hosts, default) or `injected-session-token` (managed runtimes). |

For `podium sync`, the same configuration lives in `<workspace>/.podium/sync.yaml` (or `~/.podium/sync.yaml` for per-developer defaults). See the per-harness sections for examples.

`podium sync` runs in one-shot mode by default and in long-running watch mode when invoked with the watch flag. Watch mode runs one sync at startup and then waits for change triggers. A burst of triggers coalesces into a single run through a debounce window, and that run reruns the whole sync: it re-reads the effective view, applies the active profile's scope and the toggles recorded in the lock file, writes every selected artifact, deletes the paths the prior run wrote that this run did not, and rewrites the lock file. No artifact is materialized per event. The trigger source differs between server-source and filesystem-source registries, and the run itself is identical.

**Server-source registry.** The watcher subscribes to the registry change-event stream, a long-lived `GET /v1/events` that requests the `artifact.published`, `artifact.deprecated`, and `layer.config_changed` event types and returns newline-delimited JSON (`application/x-ndjson`), one event per line. The watcher reads each line's `event` field, skips the `_heartbeat` keepalive, and treats every other line as one trigger. The rest of the payload is not inspected, and a dropped stream is reopened after 500 ms. The stream itself applies no per-caller filtering; layer visibility applies when the triggered run re-reads the caller's effective view.

![podium sync watch mode against a server-source registry: the watcher subscribes to the registry change-event stream, debounces the triggers it receives, and reruns the whole sync, which resolves the caller's effective view, adapts each artifact, writes atomically, deletes stale paths, and rewrites the lock file.](../assets/diagrams/sync-watch.svg)

<!--
ASCII fallback for the diagram above (podium sync watch mode, server-source registry):

  podium server
    GET /v1/events                      the watcher reconnects 500 ms
    newline-delimited JSON,             after a dropped stream
    one line per event
             |
             |  only the event type is read
             v
  +----------------------------+
  | Debounce                   |   one timer for the whole stream;
  | a burst of events          |   a burst becomes one run
  +-------------+--------------+
                |
                v
  +---------------------------------------------------------+
  | one full sync run                                        |
  |   1 Resolve   the caller's effective view over HTTP      |
  |   2 Adapt     each selected artifact, harness writer     |
  |   3 Write     every file staged .tmp, then renamed       |
  |   4 Delete    every prior path this run did not write    |
  |   5 Lock      one .podium/sync.lock entry per path       |
  +----------------------------+----------------------------+
                               |
                               v
  target tree:
    .claude/agents/, .cursor/rules/, etc.

  The run then awaits the next trigger, which re-arms the debounce.
  Every run applies the active profile's scope and re-reads the lock
  file's toggles. A deprecated artifact keeps materializing; a file
  leaves the target when its artifact leaves the effective view.
-->

**Filesystem-source registry.** The catalog is a directory on disk. The watcher registers an `fsnotify` watch on the registry path, the workspace overlay path, and every subdirectory beneath them, adding new subdirectories as they appear. Any event under either tree is one trigger, and the changed path is not inspected. When `fsnotify` cannot initialize, the watcher polls instead: it fingerprints both trees every 500 ms from each entry's path, modification time, and size, and reruns when the fingerprint moves. Identity and visibility are not evaluated because the directory is canonical.

![podium sync watch mode against a filesystem-source registry: fsnotify over the registry path and the workspace overlay path, with a fingerprint-poll fallback, feeds a debounce that triggers one full sync run, which resolves the local layers and the overlay, adapts each artifact, writes atomically, deletes stale paths, and rewrites the lock file.](../assets/diagrams/sync-watch-filesystem.svg)

<!--
ASCII fallback for the diagram above (podium sync watch mode, filesystem-source registry):

  filesystem catalog
    ~/podium-artifacts/ + .podium/overlay/    poll fallback when fsnotify
    fsnotify over both trees, recursively     cannot start: it fingerprints
                                              both trees every 500 ms
             |
             |  the changed path is not inspected
             v
  +----------------------------+
  | Debounce                   |   one timer for both trees;
  | a burst of edits           |   a burst becomes one run
  +-------------+--------------+
                |
                v
  +---------------------------------------------------------+
  | one full sync run                                        |
  |   1 Resolve   the local layers, then the overlay         |
  |   2 Adapt     each selected artifact, harness writer     |
  |   3 Write     every file staged .tmp, then renamed       |
  |   4 Delete    every prior path this run did not write    |
  |   5 Lock      one .podium/sync.lock entry per path       |
  +----------------------------+----------------------------+
                               |
                               v
  target tree:
    .claude/agents/, .cursor/rules/, etc.

  The run then awaits the next trigger, which re-arms the debounce.
  Every run applies the active profile's scope and re-reads the lock
  file's toggles. The directory is canonical, so the run evaluates no
  caller identity and no layer visibility.
-->


---

## Supported harnesses

The harnesses below ship with a built-in adapter. For per-harness specifics about skills, hooks, plugins, and other harness-native concepts, refer to the harness's own documentation; the harness's documentation is the source of truth.

| Adapter value    | Harness | Git-repo distribution (`kind: marketplace`) | Documentation |
|:-----------------|:--------|:--|:--------------|
| `none`           | Generic / raw output. No harness-specific translation. | none (raw canonical output) | n/a |
| `claude-code`    | Anthropic Claude Code (CLI). | Claude marketplace (`.claude-plugin/marketplace.json`) | [code.claude.com/docs](https://code.claude.com/docs/) |
| `claude-desktop` | Anthropic Claude Desktop (desktop chat app). | Claude marketplace (`.claude-plugin/marketplace.json`) | [claude.com/download](https://claude.com/download), [Skills in Claude](https://support.claude.com/en/articles/12512180-use-skills-in-claude) |
| `claude-cowork`  | Anthropic Claude Cowork (web product for organizations, claude.ai). | Claude marketplace (`.claude-plugin/marketplace.json`) | [claude.com/plugins](https://claude.com/plugins), [Manage Cowork plugins](https://support.claude.com/en/articles/13837433-manage-claude-cowork-plugins-for-your-organization) |
| `cursor`         | Cursor IDE. | Cursor team marketplace (`.cursor-plugin/marketplace.json`) | [cursor.com/docs](https://cursor.com/docs) |
| `codex`          | OpenAI Codex (CLI and IDE). | Codex marketplace (`.agents/plugins/marketplace.json`) | [developers.openai.com/codex](https://developers.openai.com/codex) |
| `gemini`         | Google Gemini CLI. | Gemini extension (`gemini-extension.json`; one extension per repository) | [geminicli.com/docs](https://geminicli.com/docs) |
| `opencode`       | OpenCode. | none (npm packages only) | [opencode.ai/docs](https://opencode.ai/docs) |
| `pi`             | Pi (pi-mono coding agent). | Pi git package (root `package.json` with a `pi.skills` array) | [github.com/badlogic/pi-mono](https://github.com/badlogic/pi-mono/tree/main/packages/coding-agent) |
| `hermes`         | Hermes Agent (Nous Research). | Hermes skills tap (skills discovered under root `skills/`) | [hermes-agent.nousresearch.com/docs](https://hermes-agent.nousresearch.com/docs/) |

Claude Code, Claude Desktop, and Claude Cowork read the same `.claude-plugin/marketplace.json`, so one Claude marketplace serves all three. The Claude, Codex, and Cursor manifests sit at distinct fixed locations and coexist in one repository. A `kind: marketplace` sync target produces these distributions; [Publishing](publishing) covers the model and the workflow. OpenCode and `none` have no git-repo distribution and are not publish targets.

The adapter set grows as new harnesses appear. Custom adapters register through the `HarnessAdapter` SPI; see [Extending](../deployment/extending).

---

## Claude Code

**MCP server** (project-level `.mcp.json` at the repository root, or user-level `~/.claude.json`; `claude mcp add` writes either):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-code",
        "PODIUM_OVERLAY_PATH": "${WORKSPACE}/.podium/overlay/"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

**Where artifacts land:**

| Type | Location |
|:--|:--|
| `skill` | `.claude/skills/<name>/SKILL.md` (folder per skill, agentskills.io layout) |
| `agent` | `.claude/agents/<name>.md` |
| `rule` | `.claude/rules/<name>.md` (optional `paths:` frontmatter for path-scoping) |
| `command` | `.claude/commands/<name>.md` |
| `hook` | Merged into `.claude/settings.json` under the `hooks` key, keyed by the artifact ID so a re-sync reconciles only Podium's entries. A hook's bundled scripts materialize to `.podium/resources/<artifact-id>/`, and the merged command references them there. |
| `context` | No native Claude Code concept. A `context` artifact lands at `.podium/context/<artifact-id>/`; reference material that belongs to a skill ships in that skill's `references/`. |
| `mcp-server` | Merged into `.mcp.json` (project root) under `mcpServers`, keyed by the artifact's `name` field, falling back to the last segment of the artifact ID when `name` is unset. A top-level `x-podium` object records which entry each artifact ID owns, so a re-sync reconciles only Podium's entries. |
| Bundled resources (skill) | Inside the skill folder (`scripts/`, `references/`, `assets/`). |
| Bundled resources (non-skill) | An `agent` or extension-type artifact writes its resources under `.claude/podium/<artifact-id>/`. A `command` artifact writes its resources under `.podium/resources/<artifact-id>/`. The adapter does not rewrite resource paths inside an `agent` or `command` file, so a path the artifact's prose references keeps the registry-relative form the author wrote; only a `hook`'s `hook_action` is rewritten to the materialized location. |

**Notes:**

- The rule file carries the prose with the Podium-internal fields dropped. `always` loads at launch and `glob` writes the native `paths:` YAML list, both fully supported. `auto` and `explicit` fall back to a load-always file and draw a lint warning, because `.claude/rules/` files have no description-attach or mention-only mode (a rule without `paths:` loads on every turn).
- Native hook system available; see [Hooks](../authoring/hooks).

---

## Claude Desktop

**MCP server** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS; equivalents on Windows/Linux):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-desktop"
      }
    }
  }
}
```

**`podium sync`** has no project-level surface for Claude Desktop. Claude Desktop is a chat application whose only on-disk install points are the user/OS-scope MCP config above (`claude_desktop_config.json`) and Desktop Extension bundles (`.mcpb`); it has no native concept for `skill`, `agent`, `context`, `command`, `rule`, or `hook`, and it does not read project-level artifact files. Register the Podium MCP server (above) for runtime discovery, or package an MCP server as a `.mcpb` bundle. For on-disk materialization of other artifact types, target a coding harness instead.

**Notes:**

- Every artifact type is `✗` for claude-desktop in the capability matrix, including `mcp-server`. Project materialization writes project-level files, and the Claude Desktop MCP config is user/OS-scope, so it is configured out of band. Exclude the harness with `target_harnesses:`, or use a coding harness for materialization.

---

## Claude Cowork

Cowork is Anthropic's web product for organizations (claude.ai). Plugin distribution to Cowork uses a Git-hosted plugin marketplace that an org admin imports.

**Marketplace publishing** is the path for Cowork. A `kind: marketplace` sync target renders the Claude marketplace, which Cowork imports along with Claude Code and Claude Desktop, because the three Claude surfaces read the same `.claude-plugin/marketplace.json`. Declare a marketplace target whose harness set names a Claude surface and run `podium sync --config`; the workflow commits and pushes the rendered repository, and the org admin imports the repository URL via [Manage Cowork plugins](https://support.claude.com/en/articles/13837433-manage-claude-cowork-plugins-for-your-organization). See [Publishing](publishing) for the model, the marketplace target schema, and the workflow.

**`podium sync`** for a `kind: workspace` target materializes the harness-neutral `type: context` artifact for `claude-cowork` to `.podium/context/<artifact-id>/`. It does not emit the plugin and marketplace layout. A workspace sync onto `claude-cowork` for a plugin-layout type (`skill`, `agent`, `command`, `rule`, `hook`, or `mcp-server`) fails with `materialize.untranslatable` per §6.9, the same outcome `load_artifact` returns. A cowork user obtains those artifacts by importing the published Claude marketplace.

**MCP server** is not applicable: Cowork ingests plugins via its marketplace import flow rather than spawning local MCP servers per session. Use a `kind: marketplace` sync target to render the marketplace, and Cowork's own admin tools to deploy.

**Notes:**

- Cowork inherits Claude Code's plugin format; skills follow the same `SKILL.md` standard.
- Org admins control which plugins reach which users via Cowork's per-user provisioning.

---

## Cursor

**MCP server** (Settings → MCP, or `~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "cursor"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness cursor
podium sync
```

**Where artifacts land:**

| Type | Location |
|:--|:--|
| `rule` | `.cursor/rules/<name>.mdc` with `alwaysApply` / `globs` / `description` set per `rule_mode`. |
| `skill` | `.cursor/skills/<name>/SKILL.md` (folder per skill, with `SKILL.md`). |
| `agent` | `.cursor/agents/<name>.md` |
| `command` | `.cursor/commands/<name>.md` |
| `hook` | Merged into `.cursor/hooks.json` under the `hooks` key. A hook's bundled scripts materialize to `.podium/resources/<artifact-id>/`, and the merged command references them there. |
| `mcp-server` | Merged into `.cursor/mcp.json` under `mcpServers`. |
| `context` | No native Cursor concept (`@Docs` is URL-indexed). A `context` artifact lands at `.podium/context/<artifact-id>/`. |
| Bundled resources | Inside the skill folder for a `skill` (`scripts/`, `references/`, `assets/`). For an `agent`, `command`, or `hook`, under `.podium/resources/<artifact-id>/`. The adapter rewrites the reference only for a `hook`, whose `hook_action` points at that bucket; an `agent` and a `command` are written verbatim. |

**Notes:**

- Every `rule_mode` value maps natively to the `.mdc` frontmatter.
- Native hook system available for a subset of the canonical hook events. The adapter maps `user_prompt_submit`, `pre_shell_execution`, `pre_mcp_execution`, `pre_read_file`, `post_file_edit`, and `stop` onto Cursor's per-category hook events. Cursor has no native event for the remaining canonical events, including `session_start`, `session_end`, `pre_tool_use`, and `post_tool_use`, so a `hook` artifact declaring one of them writes no file on Cursor and its bundled scripts are dropped. Ingest warns for any `hook` artifact whose `target_harnesses:` names cursor. See [Hooks](../authoring/hooks).
- Cursor also has a team marketplace. A `kind: marketplace` sync target writes `.cursor-plugin/marketplace.json` at the repository root and a per-plugin `.cursor-plugin/plugin.json` with `skills/`, `rules/*.mdc`, and `mcp.json` components. Import a GitHub, GitLab, or Bitbucket repository from the Cursor dashboard. See [Publishing](publishing).

---

## OpenCode

**MCP server** (`opencode.json` at the project root or `~/.config/opencode/opencode.json`):

```json
{
  "mcp": {
    "podium": {
      "type": "local",
      "command": ["podium-mcp"],
      "enabled": true,
      "environment": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "opencode"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness opencode
podium sync
```

**Where artifacts land:**

OpenCode uses plural component directories (`.opencode/agents/`, `.opencode/commands/`, `.opencode/skills/`) and centers rules on `AGENTS.md`. OpenCode has no `.opencode/rules/` directory.

| Type | Location |
|:--|:--|
| `skill` | `.opencode/skills/<name>/SKILL.md` (folder per skill). |
| `agent` | `.opencode/agents/<name>.md` |
| `command` | `.opencode/commands/<name>.md` (supports `$ARGUMENTS` and positional `$1`/`$2`). |
| `rule` | Injected into the project-root `AGENTS.md` between Podium-managed markers. |
| `mcp-server` | Merged into `opencode.json` under the `mcp` key. |
| `hook` | No declarative file. OpenCode hooks are JavaScript or TypeScript plugin modules (`.opencode/plugins/<name>.ts`), so `hook` artifacts are not materialized; exclude OpenCode with `target_harnesses:`. |
| `context` | No native concept. A `context` artifact lands at `.podium/context/<artifact-id>/`. |

**Notes:**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded `AGENTS.md` block, because an injected block carries no per-file scoping. Ingest warns when an artifact's `target_harnesses:` names opencode.
- Custom instruction files in `opencode.json` can reference Podium-materialized files; useful for explicit-mode rules.
- AGENTS.md takes precedence over CLAUDE.md when both exist.

---

## Codex

**MCP server**: configure per OpenAI Codex's MCP config conventions. The env-var contract is the same as the other harnesses; pass `PODIUM_HARNESS=codex`.

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness codex
podium sync
```

**Where artifacts land:**

Codex consumes `AGENTS.md` for rules and now has native skill, subagent, and hook surfaces.

| Type | Location |
|:--|:--|
| `skill` | `.agents/skills/<name>/SKILL.md` (folder per skill; note the `.agents/` root, not `.codex/`). |
| `agent` | `.codex/agents/<name>.toml` |
| `rule` | Injected into the root `AGENTS.md` between Podium-managed markers. |
| `hook` | Merged into the `[hooks]` table in `.codex/config.toml`, keyed by the native event (for example `[[hooks.Stop]]`). |
| `mcp-server` | Merged into `.codex/config.toml` under `[mcp_servers]`. |
| `command` | No project-level target. Codex custom prompts are user-scope (`~/.codex/prompts/`) and deprecated in favor of skills; exclude Codex with `target_harnesses:` or author as `type: skill`. |
| `context` | No native concept. A `context` artifact lands at `.podium/context/<artifact-id>/`. |

**Notes:**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded `AGENTS.md` block, because an injected block carries no per-file scoping. Ingest warns when an artifact's `target_harnesses:` names codex.
- Codex reads hooks from the `[hooks]` table in `.codex/config.toml`, so `hook` artifacts materialize rather than failing ingest. Codex runs these hooks in its interactive mode; `codex exec` does not fire lifecycle hooks in codex-cli 0.136.0.
- Skills live at `.agents/skills/`, not `.codex/skills/`. Subagents are TOML at `.codex/agents/<name>.toml`.
- Codex also has a git-repo marketplace. A `kind: marketplace` sync target writes `.agents/plugins/marketplace.json` at the repository root and a per-plugin `.codex-plugin/plugin.json` with `skills/`, `hooks/hooks.json`, and `.mcp.json` components. Install with `codex plugin marketplace add owner/repo`. See [Publishing](publishing).

---

## Gemini

**MCP server**: configure per the Gemini CLI's MCP config conventions. Pass `PODIUM_HARNESS=gemini`.

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness gemini
podium sync
```

**Where artifacts land:**

| Type | Location |
|:--|:--|
| `skill` | `.gemini/skills/<name>/SKILL.md` (folder per skill). |
| `agent` | `.gemini/agents/<name>.md` |
| `command` | `.gemini/commands/<name>.toml` (TOML with a `prompt` key; `{{args}}` for arguments). |
| `rule` | Injected into `GEMINI.md` (the hierarchical context file) between Podium-managed markers. |
| `hook` | Merged into `.gemini/settings.json` under the `hooks` key. |
| `mcp-server` | Merged into `.gemini/settings.json` under `mcpServers`. |
| `context` | `.podium/context/<artifact-id>/` (harness-neutral; reference material that belongs to a skill ships in that skill's `references/`). |

**Notes:**

- `rule_mode: always` maps to `GEMINI.md`. Other rule modes fall back with a lint warning per the per-harness capability matrix.
- Gemini commands are TOML and use the `{{args}}` placeholder; positional arguments are not supported.
- See [Rule modes](../authoring/rule-modes) for the per-harness mapping.
- Gemini distributes through an extension. A `kind: marketplace` sync target writes `gemini-extension.json`, `commands/*.toml`, and the context file at the repository root, collapsing the target's plugin set into one extension. A Gemini repository holds one extension, so a Gemini target takes its own repository. Install with `gemini extensions install owner/repo`. See [Publishing](publishing).

---

## Pi

**MCP server**: not applicable. Pi deliberately omits MCP, so the Podium MCP server cannot run inside Pi. Use `podium sync` for filesystem materialization.

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness pi
podium sync
```

**Where artifacts land:**

Pi loads `AGENTS.md` from the project tree (and `~/.pi/agent/AGENTS.md` globally). Pi deliberately omits subagents, hooks, and MCP, so those types have no native surface. There is no native `.pi/rules/` directory in core Pi.

| Type | Location |
|:--|:--|
| `skill` | `.pi/skills/<name>/SKILL.md` (folder per skill). |
| `command` | `.pi/prompts/<name>.md` (Pi calls these prompt templates; supports `$1`/`$@`/`{{var}}`). |
| `rule` | Injected into root `AGENTS.md` between Podium-managed markers. |
| `agent`, `hook`, `mcp-server` | Not supported. Pi omits subagents, on-disk hooks, and MCP; exclude Pi with `target_harnesses:`. |
| `context` | No native concept. A `context` artifact lands at `.podium/context/<artifact-id>/`. |

**Notes:**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded `AGENTS.md` block, because an injected block carries no per-file scoping. Ingest warns when an artifact's `target_harnesses:` names pi.
- Pi also reads `SYSTEM.md` and `APPEND_SYSTEM.md` for system-prompt customization; Podium does not write to these by default.
- Pi distributes through a git package. A `kind: marketplace` sync target writes a root `package.json` carrying the `pi-package` keyword and a `pi.skills` array pointing at a skills subtree, with `skills/<name>/SKILL.md` per skill. Install with `pi install git:github.com/owner/repo`. See [Publishing](publishing).

---

## Hermes

**MCP server** (`~/.hermes/config.yaml` under the `mcp_servers:` key; YAML, not JSON):

```yaml
# ~/.hermes/config.yaml
mcp_servers:
  podium:
    command: podium-mcp
    env:
      PODIUM_REGISTRY: https://podium.acme.com
      PODIUM_HARNESS: hermes
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness hermes
podium sync
```

**Where artifacts land:**

Hermes natively reads project-level `.cursor/rules/*.mdc`, root `AGENTS.md`, and `.cursorrules`. Its own skill, command, hook, and MCP surfaces live under user-scope `~/.hermes/`, which project-level materialization does not write, so those types are out of scope for sync.

| Type | Location |
|:--|:--|
| `rule` | `.cursor/rules/<name>.mdc`, reusing the Cursor format. |
| `context` | `.podium/context/<artifact-id>/` (harness-neutral). |
| `skill`, `command`, `hook`, `mcp-server` | User-scope only (`~/.hermes/skills/`, `~/.hermes/config.yaml`, and similar). Not materialized at project level; exclude Hermes with `target_harnesses:` or configure these out of band. |

**Notes:**

- Hermes reads the Cursor `.mdc` format, so every `rule_mode` value maps natively.
- Hermes distributes through a skills tap. A `kind: marketplace` sync target writes `skills/<name>/SKILL.md` per skill with its `references/`, `scripts/`, and `assets/`, under the tap's root `skills/` directory, and writes no root manifest. The tap defaults to a root `skills/` directory, so a Hermes target takes its own repository. Add it with `hermes skills tap add owner/repo`. See [Publishing](publishing).

---

## Generic / `none`

For runtimes without a dedicated adapter, or when canonical raw output is needed, set `PODIUM_HARNESS=none`. The MCP server and `podium sync` write the canonical layout as-is, with no harness-specific translation and no field renaming. Consumers such as runtimes, eval harnesses, and custom tooling read `ARTIFACT.md` (and `SKILL.md` for skills) plus bundled resources directly.

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "none"
      }
    }
  }
}
```

```bash
podium init --registry ~/podium-artifacts/ --harness none
podium sync
```

This is also the right harness for build pipelines and evaluation harnesses that need the canonical artifact bytes without translation.

---

## Standalone (no env override)

When `podium serve` has auto-bootstrapped `~/.podium/sync.yaml` with `defaults.registry: http://127.0.0.1:8080`, or `podium init --global --standalone` has written it explicitly, the MCP server resolves the registry from there and the `PODIUM_REGISTRY` env var can be omitted. The harness resolves separately. The MCP server reads `PODIUM_HARNESS` and falls back to the `none` adapter, and it does not read `defaults.harness` from `sync.yaml`, so a harness-native layout over the MCP path requires the variable. `podium sync` resolves `--harness`, then `PODIUM_HARNESS`, then the active profile's `harness`, then `defaults.harness`, then `none`, so a workspace initialized with `podium init --harness <name>` needs neither the flag nor the variable.

---

## Capability matrix

[Rule modes](../authoring/rule-modes) has the per-harness mapping for `rule_mode` values.
