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

The page covers the consumption paths and the harness roster, the setup of
`podium sync` and its listing budget, authentication against a server registry,
the setup of the Podium MCP server, and a reference section per harness.
[Verifying deliveries](verifying-deliveries) covers the signature and freshness
checks the MCP server runs on each load.

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
and [Publishing marketplace repositories](../deployment/content/publishing)
covers the marketplace path. To deliver a subset of the catalog instead of the
whole effective view, see [Selective materialization](selective-materialization).

### Bundled files

Materializing an artifact materializes its directory. When `podium sync` or
`load_artifact` writes an artifact, the scripts, references, and assets bundled
in its directory are written with the manifest wherever the target layout has a
place for them. A sync scope has no separate filter for bundled files, and it
cannot keep a manifest while dropping the files bundled with it.
[Bundled resources](../authoring/bundled-resources) covers what an artifact
directory may contain.

| Artifact type | Where its bundled files go under a harness adapter |
|:--|:--|
| `skill` | Inside the skill folder, in `scripts/`, `references/`, and `assets/`. A skill whose `invocation` is `user` materializes on OpenCode and Gemini as a command file, and its bundled files go to `.podium/resources/<artifact-id>/`. |
| `agent` and `hook` | The bucket the harness's artifact table names. |
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

| Adapter value | Workspace `podium sync` | Podium MCP server |
|:--|:--|:--|
| `none` | Yes, in the canonical layout. | Yes |
| `claude-code` | Yes | Yes |
| `claude-desktop` | No project-level surface. | Yes |
| `claude-cowork` | No project-scope output. | Not applicable. |
| `cursor` | Yes | Yes |
| `codex` | Yes | Yes |
| `gemini` | Yes | Yes |
| `opencode` | Yes | Yes |
| `pi` | Yes | Not applicable. |
| `hermes` | `rule` artifacts only. | Yes |

Claude Code, Claude Desktop, and Claude Cowork read the same
`.claude-plugin/marketplace.json`, so one Claude marketplace serves each of them.
The Claude, Codex, and Cursor manifests sit at distinct fixed locations and
coexist in one repository. A `kind: marketplace` sync target produces these
distributions, and
[Publishing marketplace repositories](../deployment/content/publishing) covers
the model and the workflow. OpenCode and `none` have no git-repo distribution
and are not publish targets.

The adapter set grows as new harnesses appear. Custom adapters register through
the `HarnessAdapter` SPI, as [Extending](../deployment/services/extending)
describes.

---

## Set up `podium sync`

`podium sync` writes harness-native files into a workspace. Its configuration
lives in `<workspace>/.podium/sync.yaml`, or in `~/.podium/sync.yaml` for
per-developer defaults.

1. Change into the workspace directory.
2. Run `podium init` with the registry and the adapter value. This writes the
   workspace's `sync.yaml`.
3. When the registry is a server that authenticates its callers, run
   `podium login`, as
   [Authenticate to a server registry](#authenticate-to-a-server-registry)
   describes.
4. Run `podium sync`.

Each harness section in the [harness reference](#harness-reference) carries the
commands for that harness.

`podium sync` resolves the harness from the first of these that is set:
`--harness`, then `PODIUM_HARNESS`, then the active profile's `harness`, then
`defaults.harness`, then `none`. A workspace initialized with
`podium init --harness <name>` therefore needs neither the flag nor the
variable.

Server-source `podium sync` checks each delivery the way the MCP server does. It
reads `PODIUM_VERIFY_SIGNATURES`, `PODIUM_SIGNATURE_VERIFY_KEY`, and
`PODIUM_SIGN_KEY_PATH` with the same defaults, and it does not read
`PODIUM_SIGNATURE_PROVIDER`, because every delivery signature is
registry-managed. [Delivery verification](../reference/cli#delivery-verification)
in the CLI reference lists how a failed check surfaces in each mode.

### Watch mode

`podium sync` runs once by default. With `--watch`, it runs one sync at startup
and then waits for change triggers. A burst of triggers coalesces into a single
run through a debounce window, and that run reruns the whole sync:

1. It re-reads the effective view.
2. It applies the active profile's scope and the toggles recorded in the lock
   file.
3. It writes every selected artifact.
4. It deletes the paths the prior run wrote that this run did not.
5. It rewrites the lock file.

No artifact is materialized per event. Against a server registry, the watcher
subscribes to the registry's change-event stream and receives only the events
the caller's identity can see, as the
[Events stream](../reference/http-api#events-stream) section of the HTTP API
reference describes. Against a filesystem registry, it watches the registry path
and the workspace overlay with `fsnotify`, and it polls both trees every 500 ms
when `fsnotify` cannot start.

A save that leaves an `ARTIFACT.md` or `SKILL.md` half-edited makes its
frontmatter unparseable. A filesystem-source sync drops that artifact, and by
default it keeps the files and lock entry the prior run wrote for it, so step 4
does not delete them. It keeps them only while they still belong in the target:
the prior run used the same harness, the artifact passes the run's scope and
toggles, and no other artifact of the run takes them over. An artifact takes
them over when it writes a file the kept output holds whole, or a
configuration-merge entry under the same key, such as an `mcpServers` server of
the same name. Another artifact's entry in a shared file such as `.mcp.json`
does not end the keep.
`--on-unparseable remove`, `PODIUM_SYNC_ON_UNPARSEABLE`,
or `defaults.on_unparseable` in `sync.yaml` deletes them instead.
[Previous output of an unparseable artifact](../reference/cli#previous-output-of-an-unparseable-artifact)
in the CLI reference gives the precedence and the exit status.

---

## Listing budget

Some harnesses place the name and description of each installed skill in a
listing in the model's context and limit the listing's size. Above the limit, a
harness can shorten descriptions or omit entries without reporting an error.
`podium sync` estimates the listing that each `kind: workspace` target
contributes and compares the estimate with a budget that `sync.yaml` configures
per adapter.

### What the estimate counts

The estimate covers the artifacts the run materializes, after the profile's
scope, the toggles, the workspace overlay, and the `target_harnesses` exclusion.
Each artifact whose type is in `types` and counts for the target's adapter is
one entry, except a skill whose `invocation` is `user`, which is not an entry
on any adapter. OpenCode and Gemini receive such a skill as a command file, and
the other adapters write it with the harness's user-only control, so leaving it
out keeps the estimate a lower bound. An entry's cost is the code points of its
name, the code points of its description, and `entry_overhead_chars`. The name
is the last segment of the artifact's ID, which for a skill equals its
`SKILL.md` `name`. The description is the `SKILL.md` `description` for a skill
and the `ARTIFACT.md` `description` for any other type. The listing cost is the sum over the entries.

A type counts for an adapter unless the capability matrix marks the adapter ✗
for it. The matrix reads a `rule` from its `rule_mode: always` row and a `hook`
from its `hook_event` row. A type the matrix does not
grade, such as an extension type, counts for every adapter. With the default
`types: [skill]`, no type counts for `claude-desktop`, `claude-cowork`, or
`hermes`, so those targets get no estimate.

The harness's listing can also hold skills from outside the target, such as
user-level skill directories and plugins, so the estimate is a lower bound on
the harness's listing. The report compares Podium's estimate with the configured
budget, and it does not predict which entries a harness shortens or omits.

The estimate covers the artifact set the run resolves. A server-source sync
reflects the caller's view, so a run with an injected session token
(`PODIUM_SESSION_TOKEN`, `PODIUM_SESSION_TOKEN_FILE`, or the variable
`PODIUM_SESSION_TOKEN_ENV` names) whose identity carries a team's group
memberships checks that team's view. The estimate does not cover a
`kind: marketplace` target, or the skills that `load_artifact` writes into a
directory one at a time during a session.

### Configuration

The top-level `listing_budgets:` map in `sync.yaml` is keyed by adapter value.
Entries merge by adapter across the `sync.yaml` scopes, and each key
resolves by the usual precedence. A key that no scope sets takes its default:

| Key | Meaning | Default |
|:--|:--|:--|
| `max_chars` | Listing budget, in code points. | `8000` |
| `entry_overhead_chars` | Code points counted per entry for the harness's separators and formatting. | `5` |
| `max_entry_chars` | Description length, in code points, above which an entry is reported. | `1536` for `claude-code`; unset (no per-entry report) for every other adapter |
| `types` | Artifact types that count as entries. | `[skill]` |
| `check` | `off`, `warning`, or `error`. | `warning` |

The defaults are the same for every adapter except `max_entry_chars`. The
`claude-code` default for `max_entry_chars` is the length at which Claude Code
shortens an entry's text in its listing, so a description above 1536 code points
draws a report by default. The `none` adapter feeds no harness listing and gets
no estimate.

```yaml
# Harness listing budgets, keyed by adapter value.
listing_budgets:
  claude-code:
    max_chars: 40000
    max_entry_chars: 1024
    check: error # off | warning | error
```

An entry is invalid when it names an adapter this binary does not register or
names `none`, sets `max_chars`, `entry_overhead_chars`, or `max_entry_chars` to
an integer below 1, sets an empty `types` list, or sets `check` to a string
other than `off`, `warning`, or `error`. An invalid entry for the run's harness
makes `podium sync` exit 2, and a run that sets no harness runs the `none`
adapter, so a `listing_budgets.none` entry fails it. An invalid entry for
another adapter does not affect the run, and `podium sync --check` reports it as
a warning.

A value of the wrong YAML type, such as `max_chars: 1.5`, `max_chars: abc`,
`check: 1`, or `types: skill`, makes the file that holds it fail to load in
`podium sync`, which exits 1. The MCP server does not decode `listing_budgets`,
so it still reads the registry, the version pin, and the signature policy from
that file.

### Choosing a budget

Derive `max_chars` from the budget the harness documents, and set it for the
adapter. The following harnesses illustrate the arithmetic:

- Claude Code allots 1% of the model's context window to the skill listing by
  default. For a 200,000-token window at about four characters per token, 1% is
  about 2,000 tokens, or 8,000 characters, which is the default. A
  1,000,000-token window gives about 40,000 characters, so a workspace on such a
  model sets `max_chars: 40000` for `claude-code`.
- Codex allots about 2% of the context window, and 8,000 characters when the
  window is unknown, which is also the default.

The `max_chars` default corresponds to the Claude Code listing for a model with a
200,000-token window. The budgets of the other harnesses are estimates, so set
`max_chars` for each of them from that harness's documentation.

A harness's budget can change between versions and with the user's settings, so
re-check the harness documentation when the harness or the model changes.

### Check modes

- `check: off` computes no estimate.
- `check: warning`, the default, writes each excess and each oversized entry on
  standard error and leaves the exit status unchanged.
- `check: error` writes the same lines and fails the run after it writes the
  target. A one-shot sync exits 1, a `--watch` cycle counts toward exit status 1
  on interrupt, and a `--config` target counts as failed.

An offline-first run that leaves the existing output in place computes no
estimate under any check mode. A run that computes no estimate, for this reason
or for one of the reasons above, carries `listing: null` in its `--json`
envelope.

The check never changes what `podium sync` writes. The
[Listing budget](../reference/cli#listing-budget) section of the CLI reference
gives the report lines, the `listing` member of the `--json` envelope, and the
outcome under `--dry-run`, `--config`, and `podium sync override`.

---

## Authenticate to a server registry

A server registry that verifies its callers needs a credential from each
consumer. The following registries need none:

- A filesystem registry.
- A standalone registry at `http://127.0.0.1:8080` or `http://localhost:8080`.
  `podium login` reports that the registry needs no authentication and exits.
- A registry behind a gateway that authenticates the caller. The gateway
  supplies the identity, and the consumer sends no credential of its own.

On a developer machine, authenticate once with `podium login`:

1. Run `podium login`. It resolves the registry from `--registry`, then
   `PODIUM_REGISTRY`, then `defaults.registry` in the merged `sync.yaml`, so a
   workspace that `podium init` configured needs no flag.
2. When the registry URL serves no OAuth authorization-server metadata at
   `/.well-known/oauth-authorization-server`, set
   `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` to the identity provider's
   device-authorization endpoint and `PODIUM_OAUTH_TOKEN_URL` to its token
   endpoint, or pass `--issuer` and `--token-url`. The registry process does not
   serve that document itself, so the variables are needed unless a proxy in
   front of the registry publishes it.
3. Open the verification URL the command prints, enter the user code, and
   complete the sign-in at the identity provider.

`podium login` caches the access token and the refresh token in the OS keychain
under the registry URL. The consumers use the cached token as follows:

- Server-source `podium sync` and the read commands send the cached token for
  the registry URL they resolve. They do not refresh it. When a command fails
  with `auth.token_expired`, run `podium login` again.
- The MCP server reads the cached token only when
  `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` is set in its environment. It then
  refreshes an expired token, and when no token is cached, it runs the
  device-code flow itself and shows the URL and the code through the harness.
  Without the variable, it sends an injected session token when one is
  configured, and otherwise no credential.
- Set `PODIUM_REGISTRY` in the MCP entry to the URL `podium login` used, because
  the token is cached under that exact URL.

The [`podium login` reference](../reference/cli#podium-login--podium-logout)
lists the client ID, audience, and scope options, and each
[OIDC provider cookbook](../deployment/identity/oidc/) gives the values for one
identity provider. A managed runtime that issues its own credentials sets
`PODIUM_IDENTITY_PROVIDER=injected-session-token` and supplies the token through
`PODIUM_SESSION_TOKEN`, `PODIUM_SESSION_TOKEN_FILE`, or the variable
`PODIUM_SESSION_TOKEN_ENV` names.

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
  [Clustered](../deployment/modes/clustered#2-deploy-the-registry) shows how to
  extract the key from the registry's key file.

### Steps

1. Open the harness's MCP configuration file. The
   [harness reference](#harness-reference) names the file for each harness.
2. Add a `podium` entry that launches `podium-mcp`, using the recipe for the
   harness.
3. Set `PODIUM_REGISTRY` to the server URL and `PODIUM_HARNESS` to the adapter
   value from [Supported harnesses](#supported-harnesses).
4. Set `PODIUM_MATERIALIZE_ROOT` to the directory `load_artifact` writes into,
   usually the workspace root.
5. Set `PODIUM_SIGNATURE_PROVIDER` to `registry-managed` and
   `PODIUM_SIGNATURE_VERIFY_KEY` to the registry's public key. A
   [standalone registry](#standalone-registry) consumer skips this step.
6. When the registry authenticates its callers, set
   `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` so the MCP server uses the token
   `podium login` cached, as
   [Authenticate to a server registry](#authenticate-to-a-server-registry)
   describes.

When the harness spawns `podium-mcp`, the agent gains the meta-tools that
[Browsing the catalog](browsing-the-catalog) describes.

### Where `load_artifact` writes

`load_artifact` writes an artifact's files under the `destination` argument of
the call, or under `PODIUM_MATERIALIZE_ROOT` when the call passes none. The
harness adapter's paths are relative to that root, so with `claude-code` and the
root set to the workspace, a skill lands at
`<workspace>/.claude/skills/<name>/SKILL.md`. A relative root resolves against
the working directory of the `podium-mcp` process, and the `materialized_at`
paths the tool returns are absolute.

Several artifacts can write into one shared file: a rule injects a block into
`AGENTS.md` or `GEMINI.md`, and a hook merges an entry into the harness's
settings file. Each `load_artifact` call replaces only the loaded artifact's
block or entry in such a file. The blocks and entries that earlier loads of
other artifacts wrote stay in place, and so does the operator's own content. On
the `gemini` harness, loading the rule `acme/beta` after the rule `acme/alpha`
leaves both blocks in `GEMINI.md`, and loading `acme/alpha` again at a new
version replaces its block and leaves one copy of it. `podium sync` writes its
whole selection at once and removes the blocks and entries of artifacts that
left the selection.

`load_artifact` never removes another artifact's block or entry. A block or
entry stays in the file after its artifact is deleted, leaves the caller's
view, or stops writing to that file. To remove it, run a `podium sync` into the
same directory whose selection writes into that file: the sync rebuilds every
Podium block and entry in the file from its selection. Otherwise, delete the
entry, or the block between its `podium:begin` and `podium:end` markers, by
hand.

When neither the argument nor the variable is set, `load_artifact` returns the
manifest body and writes no file. The recipes in the
[harness reference](#harness-reference) omit the variable, so add it to the
entry when the agent needs the bundled files on disk.

### Environment variables

| Variable | Purpose |
|:--|:--|
| `PODIUM_REGISTRY` | Registry source. The MCP server requires a server URL (`http://` or `https://`) and aborts startup with `config.filesystem_registry_unsupported` when given a filesystem path. `podium sync` accepts a URL or a filesystem path. |
| `PODIUM_HARNESS` | Harness adapter to use. Pass `none` for canonical raw output. |
| `PODIUM_MATERIALIZE_ROOT` | Optional. Default directory `load_artifact` writes into. A per-call `destination` argument overrides it. Unset, a call without `destination` writes no file. |
| `PODIUM_OVERLAY_PATH` | Optional. Workspace local-overlay path. Falls back to `<workspace>/.podium/overlay/` when MCP roots resolve. |
| `PODIUM_IDENTITY_PROVIDER` | `oauth-device-code` (developer hosts, default) or `injected-session-token` (managed runtimes). |
| `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_OAUTH_TOKEN_URL` | Optional. The identity provider's device-authorization and token endpoints. Under `oauth-device-code`, the MCP server uses a cached `podium login` token, or runs the device-code flow, only when the authorization endpoint is set. The token endpoint defaults to the authorization endpoint followed by `/token`. |
| `PODIUM_VERIFY_SIGNATURES` | Signature policy: `always` (default) or `never`. |
| `PODIUM_SIGNATURE_PROVIDER` | Verifier: `registry-managed` (default) or `noop`. |
| `PODIUM_SIGNATURE_VERIFY_KEY` | The registry's verification key set: one base64 Ed25519 public key, or a comma-separated list of them during a key rotation. |
| `PODIUM_SIGN_KEY_PATH` | Optional. The registry key file that supplies the verification key set when `PODIUM_SIGNATURE_VERIFY_KEY` is unset. Default `~/.podium/standalone/registry-signing.key`. |

[Verifying deliveries](verifying-deliveries) gives the rules for the signature
variables and the cache settings that affect verification. The
[environment variable reference](../reference/cli#environment-variables) lists
every variable the MCP server reads, including the cache and runtime-requirement
settings.

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

The following settings still need attention:

- **Harness.** The MCP server reads `PODIUM_HARNESS` and falls back to the
  `none` adapter. It does not read `defaults.harness` from `sync.yaml`, so a
  harness-native layout over the MCP path requires the variable.
- **Inherited signature provider.** The MCP server inherits
  `PODIUM_SIGNATURE_PROVIDER` from the environment that launches it. When the
  shell exports another value for `podium sign` or `podium verify`, such as
  `sigstore-keyless`, set `PODIUM_SIGNATURE_PROVIDER=registry-managed` in the
  MCP entry. Otherwise the MCP server refuses to start with `config.invalid`.

---

## Harness reference

Each section below gives the MCP server recipe, the `podium sync` commands, where
each artifact type lands, and the behavior specific to that harness.
[Rule modes](../authoring/rule-modes) has the per-harness mapping for
`rule_mode` values, [Hooks](../authoring/hooks) covers the hook events, and
[Publishing marketplace repositories](../deployment/content/publishing) covers the marketplace workflow for each
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
| `skill` (`invocation: user`) | `.claude/skills/<name>/SKILL.md` with the top-level key `disable-model-invocation: true`. |
| `agent` | `.claude/agents/<name>.md` |
| `rule` | `.claude/rules/<name>.md` (optional `paths:` frontmatter for path-scoping) |
| `hook` | Merged into `.claude/settings.json` under the `hooks` key, keyed by the artifact ID so a re-sync reconciles only Podium's entries. A hook's bundled scripts materialize to `.podium/resources/<artifact-id>/`, and the merged command references them there. |
| `mcp-server` | Merged into `.mcp.json` (project root) under `mcpServers`, keyed by the artifact's `name` field, falling back to the last segment of the artifact ID when `name` is unset. A top-level `x-podium` object records which entry each artifact ID owns, so a re-sync reconciles only Podium's entries. |
| Bundled resources (skill) | Inside the skill folder (`scripts/`, `references/`, `assets/`). |
| Bundled resources (non-skill) | An `agent` or extension-type artifact writes its resources under `.claude/podium/<artifact-id>/`. |

**Behavior notes.**

- For a skill whose `invocation` is `user`, the adapter writes
  `disable-model-invocation: true` as the YAML boolean. An authored value that
  is already the boolean `true` stays as written. Any other authored value,
  including `false` and the quoted string `"true"`, is replaced with the
  boolean, and lint reports nothing for it.
- The rule file carries the prose with the Podium-internal fields dropped.
- `rule_mode: always` loads at launch, and `glob` writes the native `paths:`
  YAML list. Both modes map fully.
- `auto` and `explicit` fall back to a load-always file and draw a lint warning.
  `.claude/rules/` files have no description-attach or mention-only mode, and
  Claude Code reads a rule file without `paths:` at launch, as it reads
  `CLAUDE.md`.
- The adapter does not rewrite resource paths inside an `agent` file. A path
  the artifact's prose references keeps the registry-relative form the author
  wrote. Only a `hook`'s `hook_action` is rewritten to the
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
`agent`, `rule`, or `hook`, and it does not read project-level artifact files.

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
imports along with Claude Code and Claude Desktop, because the Claude
surfaces read the same `.claude-plugin/marketplace.json`.

1. Declare a marketplace target whose harness set names a Claude surface.
2. Run `podium sync --config`. The workflow commits and pushes the rendered
   repository.
3. The organization admin imports the repository URL through
   [Manage Cowork plugins](https://support.claude.com/en/articles/13837433-manage-claude-cowork-plugins-for-your-organization).

[Publishing marketplace repositories](../deployment/content/publishing) covers the model, the marketplace target
schema, and the workflow.

**MCP server.** Not applicable. Cowork ingests plugins through its marketplace
import flow and does not spawn local MCP servers per session. Render the
marketplace with a `kind: marketplace` sync target, and deploy it with Cowork's
own admin tools.

**Workspace sync.** `claude-cowork` has no project-scope output. A
`kind: workspace` target writes nothing for it and does not emit the plugin and
marketplace layout. A workspace sync onto `claude-cowork` fails with
`materialize.untranslatable` for every artifact type, the same outcome
`load_artifact` returns. A Cowork user obtains the artifacts by importing the
published Claude marketplace.

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
| `skill` (`invocation: user`) | `.cursor/skills/<name>/SKILL.md` with the top-level key `disable-model-invocation: true`. |
| `agent` | `.cursor/agents/<name>.md` |
| `hook` | Merged into `.cursor/hooks.json` under the `hooks` key. A hook's bundled scripts materialize to `.podium/resources/<artifact-id>/`, and the merged command references them there. |
| `mcp-server` | Merged into `.cursor/mcp.json` under `mcpServers`. |
| Bundled resources | Inside the skill folder for a `skill` (`scripts/`, `references/`, `assets/`). For an `agent` or `hook`, under `.podium/resources/<artifact-id>/`. |

**Behavior notes.**

- For a skill whose `invocation` is `user`, the adapter writes
  `disable-model-invocation: true` as the YAML boolean. An authored value that
  is already the boolean `true` stays as written. Any other authored value,
  including `false` and the quoted string `"true"`, is replaced with the
  boolean, and lint reports nothing for it.
- Every `rule_mode` value maps natively to the `.mdc` frontmatter.
- The adapter rewrites the resource reference only for a `hook`, whose
  `hook_action` points at the `.podium/resources/` bucket. An `agent` is written
  verbatim.
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
| `skill` (`invocation: user`) | The command file `.opencode/commands/<name>.md`, which is the skill's `SKILL.md` with the derived `compatibility` line. OpenCode expands `$ARGUMENTS` and the positional `$1` and `$2` in it. Bundled resources go to `.podium/resources/<artifact-id>/`. |
| `rule` | Injected into the project-root `AGENTS.md` between Podium-managed markers. |
| `mcp-server` | Merged into `opencode.json` under the `mcp` key. |
| `hook` | No declarative file. OpenCode hooks are JavaScript or TypeScript plugin modules (`.opencode/plugins/<name>.ts`), so `hook` artifacts are not materialized; exclude OpenCode with `target_harnesses:`. |

**Behavior notes.**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded
  `AGENTS.md` block, because an injected block carries no per-file scoping.
  Ingest warns when an artifact's `target_harnesses:` names opencode.
- OpenCode skills have no user-only control, so a skill whose `invocation` is
  `user` becomes a command file. The capability matrix grades this fallback ⚠.
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
| `skill` (`invocation: user`) | `.agents/skills/<name>/SKILL.md` and, beside it, `.agents/skills/<name>/agents/openai.yaml` with `policy.allow_implicit_invocation: false`. |
| `agent` | `.codex/agents/<name>.toml` |
| `rule` | Injected into the root `AGENTS.md` between Podium-managed markers. |
| `hook` | Spliced into `.codex/config.toml` between Podium-managed markers keyed by the artifact ID, so a re-sync reconciles only Podium's entries. The hook's native event names the TOML table the block lands in (for example `[[hooks.Stop]]`), and two artifacts on one event produce two separate `[[hooks.<Event>]]` blocks. |
| `mcp-server` | Spliced into `.codex/config.toml` under `[mcp_servers]`, between Podium-managed markers keyed by the artifact ID. An entry whose name already exists in the file yields a second `[mcp_servers.<name>]` table. |

**Behavior notes.**

- `rule_mode: glob`, `auto`, and `explicit` degrade to the always-loaded
  `AGENTS.md` block, because an injected block carries no per-file scoping.
  Ingest warns when an artifact's `target_harnesses:` names codex.
- Codex reads hooks from the `[hooks]` table in `.codex/config.toml`, so `hook`
  artifacts materialize instead of failing ingest.
- Codex runs these hooks in its interactive mode. `codex exec` does not fire
  lifecycle hooks in codex-cli 0.136.0.
- Codex custom prompts are user-scope (`~/.codex/prompts/`), so Codex has no
  project-scope command file. A user-invoked skill reaches Codex as a skill
  with its `agents/openai.yaml`.
- For a skill whose `invocation` is `user`, the adapter keeps every other key
  of an authored `agents/openai.yaml` and sets
  `policy.allow_implicit_invocation` to the YAML boolean `false`. A `policy`
  value that is not a mapping becomes a mapping that holds only that key. An
  authored file that does not parse as YAML, repeats a mapping key, or is not a
  YAML mapping is replaced with a file that holds only that policy, so a
  malformed file fails no sync or publish.
- For a skill whose `invocation` is `any`, an authored `agents/openai.yaml` is
  copied unchanged, and the adapter writes none when the author wrote none.

**Marketplace.** Codex has a git-repo marketplace. A `kind: marketplace` sync
target writes `.agents/plugins/marketplace.json` at the repository root and a
per-plugin `.codex-plugin/plugin.json` with `skills/`, `hooks/hooks.json`, and
`.mcp.json` components. A skill whose `invocation` is `user` carries
`skills/<name>/agents/openai.yaml` with `policy.allow_implicit_invocation: false`.
Install it with `codex plugin marketplace add owner/repo`.

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
| `skill` (`invocation: user`) | The command file `.gemini/commands/<name>.toml` (TOML with a `description` key and a `prompt` key holding the skill body; `{{args}}` for arguments). Bundled resources go to `.podium/resources/<artifact-id>/`. |
| `rule` | Injected into `GEMINI.md` (the hierarchical context file) between Podium-managed markers. |
| `hook` | Merged into `.gemini/settings.json` under the `hooks` key. |
| `mcp-server` | Merged into `.gemini/settings.json` under `mcpServers`. |

**Behavior notes.**

- `rule_mode: always` maps to `GEMINI.md`. Other rule modes fall back with a
  lint warning per the per-harness capability matrix in
  [Rule modes](../authoring/rule-modes).
- Gemini skills have no user-only control, so a skill whose `invocation` is
  `user` becomes a TOML command file. The capability matrix grades this
  fallback ⚠.
- Gemini commands use the `{{args}}` placeholder. Positional arguments are not
  available, and Podium writes the body unchanged, so a placeholder of another
  harness does not expand.

**Marketplace.** Gemini distributes through an extension. A `kind: marketplace`
sync target writes `gemini-extension.json`, `commands/*.toml`, and the context
file at the repository root, and it collapses the target's plugin set into one
extension. Each skill whose `invocation` is `user` becomes
`commands/<name>.toml`, and its bundled resources have no extension location
and are omitted. A Gemini repository holds one extension, so a Gemini target
takes its own repository. Install it with `gemini extensions install owner/repo`.

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
| `skill` (`invocation: user`) | `.pi/skills/<name>/SKILL.md` with the top-level key `disable-model-invocation: true`. |
| `rule` | Injected into root `AGENTS.md` between Podium-managed markers. |
| `agent`, `hook`, `mcp-server` | Not supported. Pi omits subagents, on-disk hooks, and MCP; exclude Pi with `target_harnesses:`. |

**Behavior notes.**

- For a skill whose `invocation` is `user`, the adapter writes
  `disable-model-invocation: true` as the YAML boolean. An authored value that
  is already the boolean `true` stays as written. Any other authored value,
  including `false` and the quoted string `"true"`, is replaced with the
  boolean, and lint reports nothing for it.
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
hook, and MCP surfaces live under user-scope `~/.hermes/`, which
project-level materialization does not write, so those types are out of scope
for sync.

| Type | Location |
|:--|:--|
| `rule` | `.cursor/rules/<name>.mdc`, reusing the Cursor format. |
| `skill`, `hook`, `mcp-server` | User-scope only (`~/.hermes/skills/`, `~/.hermes/config.yaml`, and similar). Not materialized at project level; exclude Hermes with `target_harnesses:` or configure these out of band. |

**Behavior notes.**

- Hermes reads the Cursor `.mdc` format, so every `rule_mode` value maps
  natively.
- A workspace sync or `load_artifact` of a skill onto `hermes` fails with
  `materialize.untranslatable`. For a skill whose `invocation` is `user`, the
  message names `invocation: user` beside `type: skill`, as it does on
  `claude-desktop` and `claude-cowork`.
- The skills tap that a marketplace target renders omits a skill whose
  `invocation` is `user`, because the tap's skills are model-invocable.

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

## Related pages

- [Selective materialization](selective-materialization) narrows which artifacts
  `podium sync` writes.
- [Browsing the catalog](browsing-the-catalog) describes the meta-tools the MCP
  server exposes.
- [Publishing marketplace repositories](../deployment/content/publishing) covers `kind: marketplace` sync targets.
- [Rule modes](../authoring/rule-modes) has the per-harness mapping for
  `rule_mode` values.
- [CLI reference](../reference/cli#sync-and-materialization) lists every
  `podium sync` flag.
- [Connecting layer sources](../deployment/content/layer-sources) covers how the
  registry ingests the catalog from Git repositories and local paths.
