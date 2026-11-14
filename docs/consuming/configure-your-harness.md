---
title: Configure your harness
nav_order: 1
description: Deliver registry artifacts into each supported harness through podium sync, the Podium MCP server, or a marketplace repository, and see where each artifact type lands on disk.
---

# Configure your harness

This page explains how to connect a harness to a Podium registry and where each
artifact type lands once it arrives. A harness is the AI runtime that hosts an
agent, such as Claude Code, Cursor, or Codex. The page is for developers and
platform engineers who set up a workspace or a harness to consume the catalog.

The page opens with the consumption paths and the harness roster, then gives the
setup procedures, a reference section per harness, and the signature and cache
behavior of the MCP server.

---

## How delivery works

Authors write each artifact once, in the canonical format. A harness adapter
translates it into the layout one runtime expects. The
`finance/close-reporting/run-variance-analysis` directory becomes
`.claude/skills/run-variance-analysis/SKILL.md` for Claude Code and
`.cursor/skills/run-variance-analysis/SKILL.md` for Cursor. The scripts and
references bundled in that directory travel with it into both layouts.

Switching a workspace to another harness changes the adapter. The catalog in the
registry stays the same.

### Consumption paths

A harness consumes artifacts through one or more of the following paths. Most
harnesses accept both filesystem materialization and runtime discovery.

| Path | What it does | Registry it needs |
|:--|:--|:--|
| Filesystem materialization (`podium sync`) | Writes harness-native files into a workspace ahead of the session. The harness's own filesystem discovery picks them up, and the session makes no runtime calls to Podium. | A filesystem-source registry or a running Podium server. |
| Runtime discovery (the Podium MCP server) | The agent calls `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` mid-session and materializes only what it needs. | A running Podium server. |
| Marketplace publishing (a `kind: marketplace` sync target) | Renders the catalog into a git-repo plugin marketplace, extension, package, or tap that the harness imports. | Either source. Available for Claude Code, Claude Desktop, Claude Cowork, Codex, Cursor, Gemini, Pi, and Hermes. |

[Browsing the catalog](browsing-the-catalog) covers the runtime discovery calls,
and [Publishing marketplace repositories](publishing) covers the marketplace path. To deliver
a subset of the catalog instead of the whole effective view, see
[Selective materialization](selective-materialization).

### Bundled files

Materialization delivers an artifact's bundled files wherever the target layout
has a place for them. [Bundled resources](../authoring/bundled-resources) covers
what an artifact directory may contain.

| Artifact type | Where its bundled files go under a harness adapter |
|:--|:--|
| `skill` | Inside the skill folder, in `scripts/`, `references/`, and `assets/`. |
| `agent`, `command`, `hook`, and `context` | The bucket the harness's artifact table names. |
| `rule` and `mcp-server` | Dropped. The adapter renders a `rule` as a single file or an injected block and an `mcp-server` as a config-merge fragment, so those layouts have no place for bundled files. |

With `--harness none`, every artifact type, including `rule` and `mcp-server`,
writes its bundled files under `<destination>/<artifact-id>/`.

A marketplace rendering carries bundled files for its `skill` and `hook`
components. It also carries them for a `rule` on the Claude marketplace, where a
rule ships as a plugin skill. Every other marketplace component carries none.

---

## Supported harnesses

The harnesses below ship with a built-in adapter. The adapter value is what
`PODIUM_HARNESS`, `--harness`, and the `harness:` key in `sync.yaml` accept. For
skills, hooks, plugins, and other harness-native concepts, the harness's own
documentation is the source of truth.

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

The following table shows which of the other two paths each harness accepts. The
[harness reference](#harness-reference) gives the details.

| Adapter value | Podium MCP server | Workspace `podium sync` |
|:--|:--|:--|
| `none` | Yes | Yes, in the canonical layout. |
| `claude-code` | Yes | Yes |
| `claude-desktop` | Yes | No project-level surface. |
| `claude-cowork` | Not applicable. | `context` artifacts only. |
| `cursor` | Yes | Yes |
| `codex` | Yes | Yes |
| `gemini` | Yes | Yes |
| `opencode` | Yes | Yes |
| `pi` | Not applicable. | Yes |
| `hermes` | Yes | `rule` and `context` artifacts only. |

Claude Code, Claude Desktop, and Claude Cowork read the same
`.claude-plugin/marketplace.json`, so one Claude marketplace serves all three.
The Claude, Codex, and Cursor manifests sit at distinct fixed locations and
coexist in one repository. A `kind: marketplace` sync target produces these
distributions, and [Publishing marketplace repositories](publishing) covers the model and the
workflow. OpenCode and `none` have no git-repo distribution and are not publish
targets.

The adapter set grows as new harnesses appear. Custom adapters register through
the `HarnessAdapter` SPI, as [Extending](../deployment/extending) describes.

---

## Set up the MCP server

The Podium MCP server, `podium-mcp`, is a stdio binary that the harness spawns
alongside its other MCP servers. It reads the same environment variables under
every harness.

### Before you start

- A running Podium server. The MCP server rejects a filesystem path as its
  registry.
- The `podium-mcp` binary on the `PATH` the harness uses. The
  [quickstart](../getting-started/quickstart) lists the install channels that
  include it.
- The registry's public verification key, unless the consumer runs against a
  [standalone registry](#standalone-registry) under the same user account.
  [Clustered](../deployment/clustered#2-deploy-the-registry) shows how to
  extract the key from the registry's key file.

### Steps

1. Open the harness's MCP configuration file. The
   [harness reference](#harness-reference) names the file for each harness.
2. Add a `podium` entry that launches `podium-mcp`, using the recipe for the
   harness.
3. Set `PODIUM_REGISTRY` to the server URL and `PODIUM_HARNESS` to the adapter
   value from [Supported harnesses](#supported-harnesses).
4. Set `PODIUM_SIGNATURE_PROVIDER` to `registry-managed` and
   `PODIUM_SIGNATURE_VERIFY_KEY` to the registry's public key. A
   [standalone registry](#standalone-registry) consumer skips this step.

When the harness spawns `podium-mcp`, the agent gains the meta-tools that
[Browsing the catalog](browsing-the-catalog) describes.

### Environment variables

| Variable | Purpose |
|:--|:--|
| `PODIUM_REGISTRY` | Registry source. The MCP server requires a server URL (`http://` or `https://`) and aborts startup with `config.filesystem_registry_unsupported` when given a filesystem path. `podium sync` accepts a URL or a filesystem path. |
| `PODIUM_HARNESS` | Harness adapter to use. Pass `none` for canonical raw output. |
| `PODIUM_OVERLAY_PATH` | Optional. Workspace local-overlay path. Falls back to `<workspace>/.podium/overlay/` when MCP roots resolve. |
| `PODIUM_IDENTITY_PROVIDER` | `oauth-device-code` (developer hosts, default) or `injected-session-token` (managed runtimes). |
| `PODIUM_VERIFY_SIGNATURES` | Signature policy: `always` (default) or `never`. |
| `PODIUM_SIGNATURE_PROVIDER` | Verifier: `registry-managed` (default) or `noop`. |
| `PODIUM_SIGNATURE_VERIFY_KEY` | The registry's verification key set: one base64 Ed25519 public key, or a comma-separated list of them during a key rotation. |
| `PODIUM_SIGN_KEY_PATH` | Optional. The registry key file that supplies the verification key set when `PODIUM_SIGNATURE_VERIFY_KEY` is unset. Default `~/.podium/standalone/registry-signing.key`. |

Server-source `podium sync` reads the same variables with the same defaults,
except `PODIUM_SIGNATURE_PROVIDER`. It does not read that variable because it
verifies with the registry-managed verifier.

[Signature verification](#signature-verification) gives the full rules for the
signature variables.

### Standalone registry

A standalone registry lets the MCP entry omit `PODIUM_REGISTRY` and the
signature variables. The MCP server resolves the registry from
`~/.podium/sync.yaml` when one of the following wrote
`defaults.registry: http://127.0.0.1:8080` there:

- `podium serve`, which bootstraps the file automatically.
- `podium init --global --standalone`, which writes it explicitly.

The verification key set also resolves without an override. The MCP server reads
the `public:` line and every `verify:` line of the registry key file at
`~/.podium/standalone/registry-signing.key`, which the standalone registry
wrote. A standalone recipe therefore omits `PODIUM_SIGNATURE_PROVIDER` and
`PODIUM_SIGNATURE_VERIFY_KEY`.

Two settings still need attention:

- **Harness.** The MCP server reads `PODIUM_HARNESS` and falls back to the
  `none` adapter. It does not read `defaults.harness` from `sync.yaml`, so a
  harness-native layout over the MCP path requires the variable.
- **Inherited signature provider.** The MCP server inherits
  `PODIUM_SIGNATURE_PROVIDER` from the environment that launches it. When the
  shell exports another value for `podium sign` or `podium verify`, such as
  `sigstore-keyless`, set `PODIUM_SIGNATURE_PROVIDER=registry-managed` in the
  MCP entry. Otherwise the MCP server refuses to start with `config.invalid`.

---

## Set up `podium sync`

`podium sync` writes harness-native files into a workspace. Its configuration
lives in `<workspace>/.podium/sync.yaml`, or in `~/.podium/sync.yaml` for
per-developer defaults.

1. Change into the workspace directory.
2. Run `podium init` with the registry and the adapter value. This writes the
   workspace's `sync.yaml`.
3. Run `podium sync`.

Each harness section in the [harness reference](#harness-reference) carries the
commands for that harness.

`podium sync` resolves the harness from the first of these that is set:
`--harness`, then `PODIUM_HARNESS`, then the active profile's `harness`, then
`defaults.harness`, then `none`. A workspace initialized with
`podium init --harness <name>` therefore needs neither the flag nor the
variable.

### Watch mode

`podium sync` runs once by default. With the watch flag, it runs in long-running
watch mode: it runs one sync at startup and then waits for change triggers.

A burst of triggers coalesces into a single run through a debounce window. That
run reruns the whole sync:

1. It re-reads the effective view.
2. It applies the active profile's scope and the toggles recorded in the lock
   file.
3. It writes every selected artifact.
4. It deletes the paths the prior run wrote that this run did not.
5. It rewrites the lock file.

No artifact is materialized per event. The trigger source differs between
server-source and filesystem-source registries, and the run itself is identical.

#### Server-source registry

The watcher subscribes to the registry change-event stream. The stream is a
long-lived `GET /v1/events` that requests the `artifact.published`,
`artifact.deprecated`, and `layer.config_changed` event types. It returns
newline-delimited JSON (`application/x-ndjson`), one event per line.

- The watcher reads each line's `event` field, skips the `_heartbeat`
  keepalive, and treats every other line as one trigger. The rest of the
  payload is not inspected.
- A dropped stream is reopened after 500 ms.
- The registry delivers an event only when the caller's identity can see the
  layer the event names. The caller's path-scoped OAuth scopes also narrow the
  artifact events.
- A `layer.config_changed` that removes a layer from the caller's view still
  reaches a caller that could see the layer before the change. The triggered
  run then deletes what it materialized from that layer.
- Each triggered run re-reads the caller's effective view.

The [Events stream](../reference/http-api#events-stream) section of the HTTP API
reference states the full delivery rule.

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

#### Filesystem-source registry

The catalog is a directory on disk. The watcher registers an `fsnotify` watch on
the registry path, the workspace overlay path, and every subdirectory beneath
them, and it adds new subdirectories as they appear.

- Any event under either tree is one trigger. The changed path is not
  inspected.
- When `fsnotify` cannot initialize, the watcher polls instead. It fingerprints
  both trees every 500 ms from each entry's path, modification time, and size,
  and it reruns when the fingerprint moves.
- Identity and visibility are not evaluated, because the directory is
  canonical.

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

## Harness reference

Each section below gives the MCP server recipe, the `podium sync` commands, where
each artifact type lands, and the behavior specific to that harness.
[Rule modes](../authoring/rule-modes) has the per-harness mapping for
`rule_mode` values, [Hooks](../authoring/hooks) covers the hook events, and
[Publishing marketplace repositories](publishing) covers the marketplace workflow for each
harness that has one.

### Claude Code

**MCP server.** Add the entry to the project-level `.mcp.json` at the repository
root or to the user-level `~/.claude.json`. `claude mcp add` writes either file.

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-code",
        "PODIUM_SIGNATURE_PROVIDER": "registry-managed",
        "PODIUM_SIGNATURE_VERIFY_KEY": "<registry public key, base64>",
        "PODIUM_OVERLAY_PATH": "${WORKSPACE}/.podium/overlay/"
      }
    }
  }
}
```

**Workspace sync.**

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

**Where artifacts land.**

| Type | Location |
|:--|:--|
| `skill` | `.claude/skills/<name>/SKILL.md` (folder per skill, agentskills.io layout) |
| `agent` | `.claude/agents/<name>.md` |
| `rule` | `.claude/rules/<name>.md` (optional `paths:` frontmatter for path-scoping) |
| `command` | `.claude/commands/<name>.md` |
| `hook` | Merged into `.claude/settings.json` under the `hooks` key, keyed by the artifact ID so a re-sync reconciles only Podium's entries. A hook's bundled scripts materialize to `.podium/resources/<artifact-id>/`, and the merged command references them there. |
| `context` | No native Claude Code concept. A `context` artifact lands at `.podium/context/<artifact-id>/`. Reference material that belongs to a skill ships in that skill's `references/`. |
| `mcp-server` | Merged into `.mcp.json` (project root) under `mcpServers`, keyed by the artifact's `name` field, falling back to the last segment of the artifact ID when `name` is unset. A top-level `x-podium` object records which entry each artifact ID owns, so a re-sync reconciles only Podium's entries. |
| Bundled resources (skill) | Inside the skill folder (`scripts/`, `references/`, `assets/`). |
| Bundled resources (non-skill) | An `agent` or extension-type artifact writes its resources under `.claude/podium/<artifact-id>/`. A `command` artifact writes its resources under `.podium/resources/<artifact-id>/`. |

**Behavior notes.**

- The rule file carries the prose with the Podium-internal fields dropped.
- `rule_mode: always` loads at launch, and `glob` writes the native `paths:`
  YAML list. Both modes map fully.
- `auto` and `explicit` fall back to a load-always file and draw a lint warning.
  `.claude/rules/` files have no description-attach or mention-only mode, and a
  rule without `paths:` loads on every turn.
- The adapter does not rewrite resource paths inside an `agent` or `command`
  file. A path the artifact's prose references keeps the registry-relative form
  the author wrote. Only a `hook`'s `hook_action` is rewritten to the
  materialized location.
- Claude Code has a native hook system.

### Claude Desktop

**MCP server.** Add the entry to
`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS, or
the equivalent file on Windows or Linux.

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-desktop",
        "PODIUM_SIGNATURE_PROVIDER": "registry-managed",
        "PODIUM_SIGNATURE_VERIFY_KEY": "<registry public key, base64>"
      }
    }
  }
}
```

**Workspace sync.** `podium sync` has no project-level surface for Claude
Desktop. Claude Desktop is a chat application with two on-disk install points:
the user-scope or OS-scope MCP config above (`claude_desktop_config.json`) and
Desktop Extension bundles (`.mcpb`). It has no native concept for `skill`,
`agent`, `context`, `command`, `rule`, or `hook`, and it does not read
project-level artifact files.

Register the Podium MCP server for runtime discovery, or package an MCP server as
a `.mcpb` bundle. For on-disk materialization of other artifact types, target a
coding harness instead.

**Behavior notes.**

- Every artifact type is `✗` for claude-desktop in the capability matrix,
  including `mcp-server`. Project materialization writes project-level files,
  and the Claude Desktop MCP config is user-scope or OS-scope, so it is
  configured out of band.
- Exclude the harness with `target_harnesses:`, or use a coding harness for
  materialization.

### Claude Cowork

Cowork is Anthropic's web product for organizations (claude.ai). Plugins reach
Cowork through a Git-hosted plugin marketplace that an organization admin
imports.

**Marketplace.** Marketplace publishing is the delivery path for Cowork. A
`kind: marketplace` sync target renders the Claude marketplace, which Cowork
imports along with Claude Code and Claude Desktop, because the three Claude
surfaces read the same `.claude-plugin/marketplace.json`.

1. Declare a marketplace target whose harness set names a Claude surface.
2. Run `podium sync --config`. The workflow commits and pushes the rendered
   repository.
3. The organization admin imports the repository URL through
   [Manage Cowork plugins](https://support.claude.com/en/articles/13837433-manage-claude-cowork-plugins-for-your-organization).

[Publishing marketplace repositories](publishing) covers the model, the marketplace target
schema, and the workflow.

**MCP server.** Not applicable. Cowork ingests plugins through its marketplace
import flow and does not spawn local MCP servers per session. Render the
marketplace with a `kind: marketplace` sync target, and deploy it with Cowork's
own admin tools.

**Workspace sync.** A `kind: workspace` target materializes the harness-neutral
`type: context` artifact for `claude-cowork` to `.podium/context/<artifact-id>/`.
It does not emit the plugin and marketplace layout. A workspace sync onto
`claude-cowork` for a plugin-layout type (`skill`, `agent`, `command`, `rule`,
`hook`, or `mcp-server`) fails with `materialize.untranslatable`, the same
outcome `load_artifact` returns. A Cowork user obtains those artifacts by
importing the published Claude marketplace.

**Behavior notes.**

- Cowork inherits Claude Code's plugin format, and skills follow the same
  `SKILL.md` standard.
- Organization admins control which plugins reach which users through Cowork's
  per-user provisioning.

### Cursor

**MCP server.** Add the entry under Settings → MCP, or in `~/.cursor/mcp.json`.

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "cursor",
        "PODIUM_SIGNATURE_PROVIDER": "registry-managed",
        "PODIUM_SIGNATURE_VERIFY_KEY": "<registry public key, base64>"
      }
    }
  }
}
```

**Workspace sync.**

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness cursor
podium sync
```

**Where artifacts land.**

| Type | Location |
|:--|:--|
| `rule` | `.cursor/rules/<name>.mdc` with `alwaysApply` / `globs` / `description` set per `rule_mode`. |
| `skill` | `.cursor/skills/<name>/SKILL.md` (folder per skill, with `SKILL.md`). |
| `agent` | `.cursor/agents/<name>.md` |
| `command` | `.cursor/commands/<name>.md` |
| `hook` | Merged into `.cursor/hooks.json` under the `hooks` key. A hook's bundled scripts materialize to `.podium/resources/<artifact-id>/`, and the merged command references them there. |
| `mcp-server` | Merged into `.cursor/mcp.json` under `mcpServers`. |
| `context` | No native Cursor concept (`@Docs` is URL-indexed). A `context` artifact lands at `.podium/context/<artifact-id>/`. |
| Bundled resources | Inside the skill folder for a `skill` (`scripts/`, `references/`, `assets/`). For an `agent`, `command`, or `hook`, under `.podium/resources/<artifact-id>/`. |

**Behavior notes.**

- Every `rule_mode` value maps natively to the `.mdc` frontmatter.
- The adapter rewrites the resource reference only for a `hook`, whose
  `hook_action` points at the `.podium/resources/` bucket. An `agent` and a
  `command` are written verbatim.
- Cursor's native hook system covers a subset of the canonical hook events. The
  adapter maps `user_prompt_submit`, `pre_shell_execution`,
  `pre_mcp_execution`, `pre_read_file`, `post_file_edit`, and `stop` onto
  Cursor's per-category hook events.
- Cursor has no native event for the remaining canonical events, including
  `session_start`, `session_end`, `pre_tool_use`, and `post_tool_use`. A `hook`
  artifact declaring one of them writes no file on Cursor, and its bundled
  scripts are dropped. Ingest warns for any `hook` artifact whose
  `target_harnesses:` names cursor.

**Marketplace.** Cursor has a team marketplace. A `kind: marketplace` sync target
writes `.cursor-plugin/marketplace.json` at the repository root and a per-plugin
`.cursor-plugin/plugin.json` with `skills/`, `rules/*.mdc`, and `mcp.json`
components. Import a GitHub, GitLab, or Bitbucket repository from the Cursor
dashboard.

### OpenCode

**MCP server.** Add the entry to `opencode.json` at the project root or to
`~/.config/opencode/opencode.json`.

```json
{
  "mcp": {
    "podium": {
      "type": "local",
      "command": ["podium-mcp"],
      "enabled": true,
      "environment": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "opencode",
        "PODIUM_SIGNATURE_PROVIDER": "registry-managed",
        "PODIUM_SIGNATURE_VERIFY_KEY": "<registry public key, base64>"
      }
    }
  }
}
```

**Workspace sync.**

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness opencode
podium sync
```

**Where artifacts land.** OpenCode uses plural component directories
(`.opencode/agents/`, `.opencode/commands/`, `.opencode/skills/`) and centers
rules on `AGENTS.md`. OpenCode has no `.opencode/rules/` directory.

| Type | Location |
|:--|:--|
| `skill` | `.opencode/skills/<name>/SKILL.md` (folder per skill). |
| `agent` | `.opencode/agents/<name>.md` |
| `command` | `.opencode/commands/<name>.md` (supports `$ARGUMENTS` and positional `$1`/`$2`). |
| `rule` | Injected into the project-root `AGENTS.md` between Podium-managed markers. |
| `mcp-server` | Merged into `opencode.json` under the `mcp` key. |
| `hook` | No declarative file. OpenCode hooks are JavaScript or TypeScript plugin modules (`.opencode/plugins/<name>.ts`), so `hook` artifacts are not materialized; exclude OpenCode with `target_harnesses:`. |
| `context` | No native concept. A `context` artifact lands at `.podium/context/<artifact-id>/`. |

**Behavior notes.**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded
  `AGENTS.md` block, because an injected block carries no per-file scoping.
  Ingest warns when an artifact's `target_harnesses:` names opencode.
- Custom instruction files in `opencode.json` can reference Podium-materialized
  files, which suits explicit-mode rules.
- `AGENTS.md` takes precedence over `CLAUDE.md` when both exist.

**Marketplace.** OpenCode distributes through npm packages only, so it has no
git-repo distribution.

### Codex

**MCP server.** Configure the entry according to OpenAI Codex's MCP config
conventions. The environment-variable contract is the same as for the other
harnesses, including `PODIUM_SIGNATURE_PROVIDER` and
`PODIUM_SIGNATURE_VERIFY_KEY`. Pass `PODIUM_HARNESS=codex`.

**Workspace sync.**

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness codex
podium sync
```

**Where artifacts land.** Codex consumes `AGENTS.md` for rules and has native
skill, subagent, and hook surfaces. Skills live at `.agents/skills/` rather than
`.codex/skills/`, and subagents are TOML files at `.codex/agents/<name>.toml`.

| Type | Location |
|:--|:--|
| `skill` | `.agents/skills/<name>/SKILL.md` (folder per skill; note the `.agents/` root rather than `.codex/`). |
| `agent` | `.codex/agents/<name>.toml` |
| `rule` | Injected into the root `AGENTS.md` between Podium-managed markers. |
| `hook` | Spliced into `.codex/config.toml` between Podium-managed markers keyed by the artifact ID, so a re-sync reconciles only Podium's entries. The hook's native event names the TOML table the block lands in (for example `[[hooks.Stop]]`), and two artifacts on one event produce two separate `[[hooks.<Event>]]` blocks. |
| `mcp-server` | Spliced into `.codex/config.toml` under `[mcp_servers]`, between Podium-managed markers keyed by the artifact ID. An entry whose name already exists in the file yields a second `[mcp_servers.<name>]` table. |
| `command` | No project-level target. Codex custom prompts are user-scope (`~/.codex/prompts/`) and deprecated in favor of skills; exclude Codex with `target_harnesses:` or author as `type: skill`. |
| `context` | No native concept. A `context` artifact lands at `.podium/context/<artifact-id>/`. |

**Behavior notes.**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded
  `AGENTS.md` block, because an injected block carries no per-file scoping.
  Ingest warns when an artifact's `target_harnesses:` names codex.
- Codex reads hooks from the `[hooks]` table in `.codex/config.toml`, so `hook`
  artifacts materialize instead of failing ingest.
- Codex runs these hooks in its interactive mode. `codex exec` does not fire
  lifecycle hooks in codex-cli 0.136.0.

**Marketplace.** Codex has a git-repo marketplace. A `kind: marketplace` sync
target writes `.agents/plugins/marketplace.json` at the repository root and a
per-plugin `.codex-plugin/plugin.json` with `skills/`, `hooks/hooks.json`, and
`.mcp.json` components. Install it with `codex plugin marketplace add owner/repo`.

### Gemini

**MCP server.** Configure the entry according to the Gemini CLI's MCP config
conventions. Pass `PODIUM_HARNESS=gemini`, and set `PODIUM_SIGNATURE_PROVIDER`
and `PODIUM_SIGNATURE_VERIFY_KEY` as the other recipes do.

**Workspace sync.**

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness gemini
podium sync
```

**Where artifacts land.**

| Type | Location |
|:--|:--|
| `skill` | `.gemini/skills/<name>/SKILL.md` (folder per skill). |
| `agent` | `.gemini/agents/<name>.md` |
| `command` | `.gemini/commands/<name>.toml` (TOML with a `prompt` key; `{{args}}` for arguments). |
| `rule` | Injected into `GEMINI.md` (the hierarchical context file) between Podium-managed markers. |
| `hook` | Merged into `.gemini/settings.json` under the `hooks` key. |
| `mcp-server` | Merged into `.gemini/settings.json` under `mcpServers`. |
| `context` | `.podium/context/<artifact-id>/` (harness-neutral; reference material that belongs to a skill ships in that skill's `references/`). |

**Behavior notes.**

- `rule_mode: always` maps to `GEMINI.md`. Other rule modes fall back with a
  lint warning per the per-harness capability matrix in
  [Rule modes](../authoring/rule-modes).
- Gemini commands are TOML and use the `{{args}}` placeholder. Positional
  arguments are not available.

**Marketplace.** Gemini distributes through an extension. A `kind: marketplace`
sync target writes `gemini-extension.json`, `commands/*.toml`, and the context
file at the repository root, and it collapses the target's plugin set into one
extension. A Gemini repository holds one extension, so a Gemini target takes its
own repository. Install it with `gemini extensions install owner/repo`.

### Pi

**MCP server.** Not applicable. Pi omits MCP by design, so the Podium MCP server
cannot run inside Pi. Use `podium sync` for filesystem materialization.

**Workspace sync.**

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness pi
podium sync
```

**Where artifacts land.** Pi loads `AGENTS.md` from the project tree, and
`~/.pi/agent/AGENTS.md` globally. Pi omits subagents, hooks, and MCP by design,
so those types have no native surface. Core Pi has no native `.pi/rules/`
directory.

| Type | Location |
|:--|:--|
| `skill` | `.pi/skills/<name>/SKILL.md` (folder per skill). |
| `command` | `.pi/prompts/<name>.md` (Pi calls these prompt templates; supports `$1`/`$@`/`{{var}}`). |
| `rule` | Injected into root `AGENTS.md` between Podium-managed markers. |
| `agent`, `hook`, `mcp-server` | Not supported. Pi omits subagents, on-disk hooks, and MCP; exclude Pi with `target_harnesses:`. |
| `context` | No native concept. A `context` artifact lands at `.podium/context/<artifact-id>/`. |

**Behavior notes.**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded
  `AGENTS.md` block, because an injected block carries no per-file scoping.
  Ingest warns when an artifact's `target_harnesses:` names pi.
- Pi also reads `SYSTEM.md` and `APPEND_SYSTEM.md` for system-prompt
  customization. Podium does not write to these files by default.

**Marketplace.** Pi distributes through a git package. A `kind: marketplace` sync
target writes a root `package.json` carrying the `pi-package` keyword and a
`pi.skills` array pointing at a skills subtree, with `skills/<name>/SKILL.md` per
skill. Install it with `pi install git:github.com/owner/repo`.

### Hermes

**MCP server.** Add the entry to `~/.hermes/config.yaml` under the
`mcp_servers:` key. The file is YAML.

```yaml
# ~/.hermes/config.yaml
mcp_servers:
  podium:
    command: podium-mcp
    env:
      PODIUM_REGISTRY: https://podium.acme.com
      PODIUM_HARNESS: hermes
      PODIUM_SIGNATURE_PROVIDER: registry-managed
      PODIUM_SIGNATURE_VERIFY_KEY: "<registry public key, base64>"
```

**Workspace sync.**

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness hermes
podium sync
```

**Where artifacts land.** Hermes natively reads project-level
`.cursor/rules/*.mdc`, root `AGENTS.md`, and `.cursorrules`. Its own skill,
command, hook, and MCP surfaces live under user-scope `~/.hermes/`, which
project-level materialization does not write, so those types are out of scope
for sync.

| Type | Location |
|:--|:--|
| `rule` | `.cursor/rules/<name>.mdc`, reusing the Cursor format. |
| `context` | `.podium/context/<artifact-id>/` (harness-neutral). |
| `skill`, `command`, `hook`, `mcp-server` | User-scope only (`~/.hermes/skills/`, `~/.hermes/config.yaml`, and similar). Not materialized at project level; exclude Hermes with `target_harnesses:` or configure these out of band. |

**Behavior notes.**

- Hermes reads the Cursor `.mdc` format, so every `rule_mode` value maps
  natively.

**Marketplace.** Hermes distributes through a skills tap. A `kind: marketplace`
sync target writes `skills/<name>/SKILL.md` per skill, with its `references/`,
`scripts/`, and `assets/`, under the tap's root `skills/` directory. It writes no
root manifest. The tap defaults to a root `skills/` directory, so a Hermes target
takes its own repository. Add it with `hermes skills tap add owner/repo`.

### Generic output (`none`)

Set `PODIUM_HARNESS=none` for a runtime without a dedicated adapter, or when the
canonical raw output is needed. The MCP server and `podium sync` write the
canonical layout as-is, with no harness-specific translation and no field
renaming. Runtimes, eval harnesses, and custom tooling read `ARTIFACT.md` (and
`SKILL.md` for skills) plus the bundled resources directly. Build pipelines and
evaluation harnesses that need the canonical artifact bytes without translation
also use `none`.

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "none",
        "PODIUM_SIGNATURE_PROVIDER": "registry-managed",
        "PODIUM_SIGNATURE_VERIFY_KEY": "<registry public key, base64>"
      }
    }
  }
}
```

```bash
podium init --registry ~/podium-artifacts/ --harness none
podium sync
```

The `none` layout writes `ARTIFACT.md`, with one exception. For a skill with no
`ARTIFACT.md`, the registry serves a synthesized document, and the `none` layout
does not write it, so the skill lands as a plain Agent Skills directory. An
authored `ARTIFACT.md` that is byte-identical to the synthesized document is
treated the same way.

---

## Signature verification

Under the default `PODIUM_VERIFY_SIGNATURES=always` policy, every artifact the
MCP server loads must carry a signature that verifies. A load of an unsigned
artifact fails with `materialize.signature_missing`. The `never` policy checks
nothing and fits only a registry running with `PODIUM_SIGN=none`.

### Signature settings

- **`PODIUM_SIGNATURE_PROVIDER`.** Under a policy above `never`, `noop` refuses
  the start, because it verifies nothing. Any other value, including
  `sigstore-keyless`, refuses the start with `config.invalid`. The registry
  signs every delivery record with its registry-managed key, and
  `sigstore-keyless` applies to `podium sign` and `podium verify` only.
- **`PODIUM_SIGNATURE_VERIFY_KEY`.** The `registry-managed` provider verifies
  with this key set. A delivery signature that verifies under any key of the set
  is accepted. Set to the empty string, the variable counts as unset. A
  non-empty value is authoritative, and under a policy above `never` an entry
  that is empty or does not decode refuses the start.
- **`PODIUM_SIGN_KEY_PATH`.** When `PODIUM_SIGNATURE_VERIFY_KEY` is unset, the
  `public:` line and every `verify:` line of this key file supply the
  verification key set. The MCP server reads the file and never writes it.

### Key set resolution

Under the default `always` policy, the MCP server resolves its verification key
set once at start. It refuses to start with
`config.signature_provider_unavailable` when it resolves none.

| Consumer | Variables to set |
|:--|:--|
| A consumer of a standard deployment | `PODIUM_SIGNATURE_PROVIDER` and `PODIUM_SIGNATURE_VERIFY_KEY`, as each recipe shows. |
| A standalone consumer running under the same user account as its registry | Neither. The set resolves from the registry's own key file. |
| A standalone consumer whose registry runs with a non-default `PODIUM_SIGN_KEY_PATH` | `PODIUM_SIGN_KEY_PATH`, set to the same path. |

During a registry key rotation, add the new key to `PODIUM_SIGNATURE_VERIFY_KEY`
as a comma-separated list before the registry signs under it.

### Cached records after a key rotation

The MCP server delivers a record from its cache only after the record passes the
same signature check as a live response. A cached delivery signature fails that
check when a key the consumer no longer trusts made it. One example is a record
cached before a rotation whose retired key has since left the set.

| `PODIUM_CACHE_MODE` | Outcome for a cached record that fails the check |
|:--|:--|
| `always-revalidate` | Cache miss. The MCP server refetches the artifact, verifies the response, and replaces the cached record. When the registry is unreachable, the load returns `network.registry_unreachable`. |
| `offline-first` | Cache miss, handled as above. When the registry is unreachable, the load returns the offline status with no artifact. |
| `offline-only` | Refused with `materialize.signature_invalid`. |

The MCP server never delivers the failing record. Before a retired key leaves the
set, an `offline-only` consumer either keeps the retired key in its set, or
clears its cache directory and refills it in `always-revalidate` or
`offline-first`.

---

## Stale `latest` protection

The MCP server refuses a `latest` load that resolves to an older revision than
one it already accepted. This stops a proxy or cache between the MCP server and
the registry from serving an older signed version.

### How the check works

- The MCP server records, per registry and artifact, the artifact revision of
  the newest `latest` load it accepted. The record lives in its index under
  `PODIUM_CACHE_DIR`.
- A later `latest` load whose revision is lower fails with
  `materialize.stale_resolution`.
- Within one session, the MCP server compares a `latest` load with the first one
  it accepted in that session, including one served from the cache after the
  registry confirmed it. A session-pinned version therefore still loads.
- An answer read through `resources/read` records the first accepted answer for
  the MCP server's own session only.
- Only `latest` loads answered by the registry are compared. A load that names a
  version, a range, or a content hash is never compared, and neither is a load
  served from the cache.
- A backport ingested after a newer version, and a new deprecated version, leave
  every honest `latest` load at or above the mark.
- The first `latest` load under an empty cache directory is accepted, and a
  registry change starts with no marks. A load that a later gate refuses leaves
  the mark unchanged.

### Limits

- The marks are not kept per organization. Identities in different
  organizations that use one registry URL should use separate cache
  directories.
- When another MCP server holds the index, the marks last only for the process,
  and the MCP server logs a warning.
- A stored mark that cannot be read is deleted with a warning.
- The mark gives no protection against a party that can write
  `PODIUM_CACHE_DIR`. Under `PODIUM_VERIFY_SIGNATURES=never`, it refuses only
  unmodified replays.

### Resetting the marks

Run `podium cache reset-revisions` after any of the following events:

- The registry is restored from a backup.
- A lagging replica is promoted.
- The newest version is purged, erased, retired under the
  [no-inheritance rule](../deployment/layers#inheritance), or leaves the
  caller's view.

### Refusals that a new session clears

Two refusals need no reset, and a new session clears both.

- **Read-only lag.** While the registry is in read-only mode, a replica that lags
  the primary can serve an older `latest`. The MCP server refuses it, and the
  registry pins that session to the older version. The session stays refused
  for that artifact after the registry leaves read-only mode. A session that did
  not load the artifact during the lag loads the current `latest` once read-only
  mode ends.
- **Restart with a reused session.** The MCP server keeps session state in
  memory. After it restarts, a host that reuses a `session_id` whose pinned
  version is older than a version another session has since loaded is refused
  for that artifact.

A new session loads the current `latest`. The host starts one by passing a new
`session_id`, and a load that passes none gets a new session when `podium-mcp`
restarts.

---

## Related pages

- [Selective materialization](selective-materialization) narrows which artifacts
  `podium sync` writes.
- [Browsing the catalog](browsing-the-catalog) describes the meta-tools the MCP
  server exposes.
- [Publishing marketplace repositories](publishing) covers `kind: marketplace` sync targets.
- [Rule modes](../authoring/rule-modes) has the per-harness mapping for
  `rule_mode` values.
- [CLI reference](../reference/cli#sync-and-materialization) lists every
  `podium sync` flag.
- [Connecting layer sources](../deployment/layer-sources) covers how the
  registry ingests the catalog from Git repositories and local paths.
