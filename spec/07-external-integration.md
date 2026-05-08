# 7. External Integration

## 7.1 The Registry: Podium Server or Local Filesystem

The registry is the system of record for artifacts. It can be reached two ways:

- **Podium server.** The registry runs as a server (standalone or standard deployment, §13.10) and clients reach it over HTTP/JSON.
- **Local filesystem.** The registry is a directory of artifacts on disk (§13.11). The Podium CLI reads it directly via `podium sync` for eager materialization.

Both modes apply layer composition (§4.6). The dispatch between them is governed by the value of `defaults.registry` in the merged `sync.yaml` (§7.5.2): a URL routes to a Podium server, a filesystem path routes to local filesystem.

The MCP server (§6), the language SDKs (§7.6), and identity-based visibility filtering require a Podium server; filesystem source does not provide them. The full list of what each mode covers is in §13.11.

### Latency budgets (SLO targets, server source)

- `load_domain`: p99 < 200 ms
- `search_domains`: p99 < 200 ms
- `search_artifacts`: p99 < 200 ms
- `load_artifact` (manifest only): p99 < 500 ms
- `load_artifact` (manifest + ≤10 MB resources from cache miss): p99 < 2 s

Server deployments that miss these should investigate.

## 7.2 Control Plane / Data Plane Split

The registry exposes two surfaces:

**Control plane (HTTP API).** Returns metadata: manifest bodies, descriptors, search results, domain maps. Synchronous. Audited. Every call carries the host's OAuth identity and is visibility-filtered.

**Data plane (object storage).** Holds bundled resources. The control plane never streams bytes for resources above the inline cutoff (256 KB). Instead, `load_artifact` returns presigned URLs that the Podium MCP server fetches directly from object storage.

Below the inline cutoff, resources are returned inline. This avoids round-trips for small fixtures.

## 7.3 Host Integration

Hosts and authors choose the integration that fits their context:

- **Programmatic runtimes** use `podium-py` or `podium-ts` to call the registry HTTP API directly. This is the most flexible path, preferred wherever a long-running process can host an HTTP client. Contract: the registry's HTTP API, with layer composition and visibility filtering applied server-side. See §7.6.
- **Hosts that can't run an SDK in-process** (Claude Desktop, Claude Code, Cursor, and similar) spawn the Podium MCP server alongside their own runtime tools. Contract: the meta-tools plus the materialization semantics described in §6.6.
- **Authors who prefer eager materialization** run `podium sync` (one-shot or watcher) and let the harness's native discovery take over from there, instead of mediating every load through MCP or the SDK at runtime. Contract: the registry's effective view written to a host-configured directory layout via the harness adapter. See §7.5.

Authoring uses Git as the source of truth (§4.6). The Podium CLI handles layer registration, manual reingests, cache management, and admin tasks; it does not push artifact content to the registry.

### 7.3.1 Authoring and Ingestion

Artifacts enter the registry by being merged into a tracked Git ref (or, for `local` source layers, by being written to the configured filesystem path). The registry mirrors what's in the source layer into its content-addressed store; once `(artifact_id, version)` is ingested, it is bit-for-bit immutable in the registry's store regardless of subsequent Git mutations.

**Author flow:**

1. Edit `ARTIFACT.md` (and `SKILL.md` for skills, plus bundled resources) in a checkout of the layer's Git repo.
2. Open a PR against the tracked ref. CI runs `podium lint` as a required check.
3. Reviewers approve per the team's branch protection rules.
4. Merge.
5. The Git provider fires a webhook to the registry. The registry fetches the new commit, walks the diff, runs lint as defense in depth, validates immutability, hashes content, stores manifest + bundled resources, indexes metadata, and emits the corresponding outbound event.

`local` source layers skip the Git steps: the author edits files in place and runs `podium layer reingest <id>`.

**Ingestion triggers.** Built-in source types use the paths below; custom `LayerSourceProvider` plugins (§9.1) declare their own trigger model (push notification, polling at a configured interval, etc.).

| Trigger              | Source                                                                                                                                       |
| -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Git provider webhook | Built-in `git` source. Configured at layer-creation time. The registry validates the webhook signature, fetches the new commit, ingests.     |
| Polling watcher      | Built-in `local` and `git` sources. `podium layer watch <id>` polls the source at a configurable interval, suitable for local filesystems and for Git refs without a configured webhook.  |
| Plugin-declared      | Custom `LayerSourceProvider` implementations. Webhook, polling, or push notification depending on the source; declared by the plugin.        |
| Manual reingest      | `podium layer reingest <id>` (admin or layer owner). Works for every source type: forces a fresh snapshot regardless of the trigger model.   |

`last_ingested_at` is exposed per layer for staleness monitoring.

**Ingest cases:**

| Case                                 | Behavior                                                                                                                                                                                                                    |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| New `(artifact_id, version)`         | Accepted; content hashed and stored.                                                                                                                                                                                        |
| Same version, identical content_hash | No-op. Handles webhook retries idempotently.                                                                                                                                                                                |
| Same version, different content_hash | Rejected as `ingest.immutable_violation`. The author bumps the version.                                                                                                                                                     |
| Lint failure                         | Rejected as `ingest.lint_failed`. The artifact remains at its previous version (if any).                                                                                                                                    |
| Freeze-window in effect              | Rejected as `ingest.frozen` unless `--break-glass` passed via the manual reingest path.                                                                                                                                     |
| Force-push detected                  | Tolerant by default; previously-ingested commits' bytes are preserved in the content store, and a `layer.history_rewritten` event is emitted. Strict mode is configurable per layer (`force_push_policy: strict` rejects).  |
| Source unreachable                   | Ingest fails; existing served artifacts are unaffected.                                                                                                                                                                     |

**Layer CLI.**

```
podium layer register --id <id> --repo <git-url> --ref <ref> [--root <subpath>]
podium layer register --id <id> --local <path>
podium layer list
podium layer reorder <id> [<id> ...]            # user-defined layers only
podium layer unregister <id>
podium layer reingest <id> [--break-glass --justification <text>]
podium layer watch <id> [--interval <duration>]
```

`podium layer register` returns the webhook URL and HMAC secret to register on the source repo. Registering a Git source without configuring the webhook leaves the layer at its initial commit until the first manual reingest.

**User-defined layers.** Authenticated users register their own layers via `podium layer register`. Each user-defined layer has implicit visibility `users: [<registrant>]`. Default cap: 3 user-defined layers per identity, configurable per tenant. Use `podium layer reorder` to change order.

**Errors.** Lint failures (`ingest.lint_failed`), webhook signature failures (`ingest.webhook_invalid`), same-version content conflicts (`ingest.immutable_violation`), freeze-window blocks (`ingest.frozen`), quota exhaustion (`quota.*`, including the user-defined-layer cap), source unreachable (`ingest.source_unreachable`), admin-only operations attempted by a non-admin (`auth.forbidden`).

### 7.3.2 Outbound Webhooks

The registry emits outbound webhooks for:

- `artifact.published`: a new `(artifact_id, version)` was ingested.
- `artifact.deprecated`: a manifest update flipped `deprecated: true`.
- `domain.published`: a `DOMAIN.md` was added or changed.
- `layer.ingested`: a layer completed an ingest cycle (with summary counts).
- `layer.history_rewritten`: force-push detected in a `git` layer.
- `vulnerability.detected`: a CVE matched an artifact's SBOM.

Schema:

```json
{
  "event": "artifact.published",
  "trace_id": "...",
  "timestamp": "...",
  "actor": {...},
  "data": {...}
}
```

Receivers are configured per org (URL + HMAC secret).

## 7.4 Degraded Network

When the registry is unreachable, the MCP server falls back to its content cache. Behavior depends on `PODIUM_CACHE_MODE`:

- `always-revalidate`: fresh calls return `{status: "offline", served_from_cache: true}` alongside cached results; if no cache, structured error `network.registry_unreachable`.
- `offline-first`: no error; serve cached results silently.
- `offline-only`: never contact the registry; structured error if cache miss.

Hosts can surface the offline status to the agent so it can adjust behavior (e.g., warn the user about staleness).

`podium sync` and the SDKs apply the same cache modes.

## 7.5 `podium sync` (Filesystem Delivery)

`podium sync` is the consumer for authors who want to materialize the user's effective view onto disk and let the harness's native discovery take over from there, instead of mediating every load through the MCP server or an SDK at runtime. It works for any harness with a filesystem-readable layout, including ones that also speak MCP. The choice is about authoring preference; it does not depend on whether the harness can talk to Podium.

The **target directory defaults to the current working directory.** Every workspace (target) holds its own state; multiple `podium sync` invocations from different folders run independently and don't interfere.

```bash
# One-shot: write the caller's effective view to the current directory
cd ~/.claude/ && podium sync --harness claude-code

# Explicit target
podium sync --harness claude-code --target ~/.claude/

# Watcher: re-sync on registry change events (long-running)
cd ~/.codex/ && podium sync --harness codex --watch

# Path-scoped: sync only artifacts under certain domain paths
podium sync --harness claude-code \
  --include "finance/invoicing/**" --include "shared/policies/*" \
  --exclude "finance/invoicing/legacy/**"

# Type-scoped: sync only artifacts of certain types (useful for split deployments)
podium sync --harness none --type skill,agent

# Profile-driven: load named scope from .podium/sync.yaml
podium sync --profile finance-team

# Dry run: print what would be synced without writing anything
podium sync --dry-run

# Multi-target: write to all configured destinations from sync.yaml
podium sync --config .podium/sync.yaml
```

The sync command reads the caller's effective view (the composed layer list after visibility filtering and `extends:` resolution), applies the requested scope filters, and writes each artifact through the configured `HarnessAdapter` to the target directory.

`podium sync` works against either kind of registry source: a server (URL) or a local filesystem (path); see §7.5.2 for dispatch and §13.11 for filesystem-specific behavior. Against a server source, sync uses the same identity providers as the MCP server, the same content cache, and the same harness adapters.

The sync model is type-agnostic: skills, agents, contexts, prompts, and `mcp-server` registrations all sync through the same path; the harness adapter decides where each type lands.

**`--dry-run`** resolves the artifact set against the current scope and prints it without writing. Default output is human-readable; `--json` produces a structured envelope (`{profile, target, harness, scope, artifacts: [{id, version, type, layer}, ...]}`) for piping into `jq`.

### 7.5.1 Scope Filters

Three filters narrow the materialized set:

| Flag                  | Repeated? | Effect                                                                                                                                                                                                                                     |
| --------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `--include <pattern>` | Yes       | Glob matched against canonical artifact IDs (the directory path under each layer's root, e.g., `finance/invoicing/run-variance-analysis`). When any `--include` is given, only artifacts matching at least one include pattern are synced. |
| `--exclude <pattern>` | Yes       | Glob matched against canonical artifact IDs. Applied after the include set; a matching pattern removes the artifact.                                                                                                                       |
| `--type <type>[,...]` | No        | Restricts to a comma-separated list of artifact types.                                                                                                                                                                                     |

Patterns use the same glob syntax as `DOMAIN.md include:` (§4.5.2): `*` matches a single path segment, `**` matches recursively, and brace expansion `{a,b}` works. A bare ID (`finance/invoicing/run-variance-analysis`) matches that artifact exactly.

Visibility is enforced before scope filtering. An artifact that the caller cannot see is not eligible to match an include pattern; this is symmetric with how `search_artifacts` behaves and prevents include patterns from leaking the existence of artifacts in invisible layers.

When neither `--include` nor `--profile` is given, the full effective view is the implicit scope (current behavior).

Path-scoped sync is the recommended way to keep a harness's working set small enough to avoid context rot. Two patterns that work well in practice:

- **Per-team profile.** Each team defines a profile that includes its domain plus shared utilities. Developers run `podium sync --profile <team>`.
- **Programmatic curation.** A script uses the SDK to pick artifacts based on context (the current task, semantic search, etc.), then invokes `podium sync --include <id> [--include <id> ...]` to materialize the chosen set. See §9.4.

### 7.5.2 Configuration (`sync.yaml`)

`sync.yaml` configures the registry source, profiles, defaults, and multi-target lists. The same schema is read from up to three file scopes; precedence resolves which value wins per key.

**File scopes.**

| Scope          | Path                                  | Typical content                                                                    |
| -------------- | ------------------------------------- | ---------------------------------------------------------------------------------- |
| User-global    | `~/.podium/sync.yaml`                 | per-developer defaults that follow them across projects (typical: harness, target) |
| Project-shared | `<workspace>/.podium/sync.yaml`       | per-project settings committed to git (typical: registry, profile)                 |
| Project-local  | `<workspace>/.podium/sync.local.yaml` | per-developer overrides on top of the project file; gitignored                     |

The schema is identical at every scope: every field that's valid at user-global is valid at project-shared and project-local. Placement is convention rather than enforcement: a project that wants to pin harness and target across teammates does so by putting those fields in the project-shared file.

**Workspace discovery.** Project-shared and project-local files are discovered by walking up from CWD until a `.podium/` directory is found, mirroring how `git` finds `.git`. The discovered `.podium/` directory is also the home for `overlay/` (workspace local overlay, §6.4) and `sync.lock` (§7.5.3).

**Precedence.** Resolved per-key, highest precedence first:

1. CLI flags
2. `PODIUM_*` env vars
3. `<workspace>/.podium/sync.local.yaml`
4. `<workspace>/.podium/sync.yaml`
5. `~/.podium/sync.yaml`
6. Built-in defaults

**Profile merge.** Profiles are additive across files (union by profile name). On name collision, the higher-precedence file's definition wins entirely (whole-profile overwrite, no field-level merge inside a profile). A stderr warning fires only when invoking a profile that has a collision (`podium sync --profile X` with multi-defined `X`); a sync against a non-colliding profile stays quiet. `podium config show` (§7.7) always surfaces collisions for debugging.

**Schema:**

```yaml
defaults:
  registry: https://podium.acme.com # see "Registry source" below — accepts URL or filesystem path
  harness: claude-code
  target: ~/.claude/
  profile: project-default # default profile when --profile is not passed

profiles:
  project-default:
    include:
      - "finance/**"
      - "shared/policies/*"
    exclude:
      - "finance/**/legacy/**"
    type: [skill, agent]
  oncall:
    include: ["platform/oncall/**", "shared/runbooks/*"]
    target: ~/.claude-oncall/ # overrides the default target

# Multi-target list selected via --config (without --profile).
# Each entry runs as a separate sync with its own scope and target.
targets:
  - id: claude-code
    harness: claude-code
    target: ~/.claude/
    profile: project-default
  - id: codex-runbooks
    harness: codex
    target: ~/.codex/
    include: ["shared/runbooks/**"]
```

**Registry source.** `defaults.registry` accepts either a URL or a filesystem path; the client adapts:

- **URL** (`http://` / `https://`): the client speaks HTTP to that registry server. All consumer paths (MCP server, SDKs, `podium sync`, read CLI) work.
- **Filesystem path** (relative or absolute): `podium sync` reads the directory directly, applies layer composition (§4.6), and materializes through the configured harness adapter. Each subdirectory of the path is a `local`-source layer; ordering is alphabetical (or governed by `<registry-path>/.layer-order`). **`podium sync` is the only consumer that works against a filesystem registry.** The MCP server and the SDKs require a server source. See §13.11 for the full filesystem-registry description.
- **Unset across all scopes**: `config.no_registry` error. The client points the user at `podium init` to configure one. There is no implicit workspace fallback; `podium sync` will not auto-detect `.podium/registry/` without explicit config.

Override an inherited URL with a filesystem path by setting `defaults.registry` explicitly at a higher-precedence scope (typically the project-shared file). Normal precedence applies; no magic-value semantics.

**Resolution rules.**

- **Profile lookup.** `--profile <name>` selects an entry under `profiles:`. The profile's fields are merged on top of `defaults:`.
- **CLI override.** Explicit CLI flags override the resolved profile (and defaults) for the same field. `--include` and `--exclude` on the CLI replace the profile's lists rather than appending; if you need additive composition, define a new profile.
- **Multi-target mode.** `podium sync --config <path>` (without `--profile`) iterates `targets:` and runs one sync per entry. Each entry can name a `profile:` (resolved as above) or specify `include`/`exclude`/`type` inline. Each target writes its own `<target>/.podium/sync.lock`; the multi-target invocation does not introduce shared state across targets.
- **Profile composition.** Profiles do not reference other profiles; nesting is intentionally absent. A team that wants an "extended" profile defines a new entry with the combined include/exclude lists.
- **Validation.** `podium sync --check` validates the merged config against the schema and reports unresolved profile references, malformed globs, target collisions, and profile-name collisions across scopes (warning, not error).

### 7.5.3 Lock File (`.podium/sync.lock`)

Every target directory holds its own state in `<target>/.podium/sync.lock`. The lock file is per-target. Multiple `podium sync` invocations against different targets run independently and don't share state. The cache (`~/.podium/cache/`) stays shared across targets (content-addressed); only sync state is per-target.

`.podium/sync.lock` is git-ignored by default. Teams that want a deterministic shared materialization commit it explicitly.

Schema:

```yaml
# <target>/.podium/sync.lock
version: 1
profile: finance-team # null when no profile was used
scope:
  include: ["finance/**", "shared/policies/*"]
  exclude: ["finance/**/legacy/**"]
  type: [skill, agent]
harness: claude-code
target: /Users/joan/.claude/
last_synced_at: 2026-05-05T14:30:00Z
last_synced_by: full # full | watch | override

# Currently materialized artifacts.
artifacts:
  - id: finance/ap/pay-invoice
    version: 1.2.0
    content_hash: sha256:abc123…
    layer: team-finance
    materialized_path: agents/pay-invoice.md
  - id: finance/close/run-variance
    version: 1.0.0
    content_hash: sha256:def456…
    layer: team-finance
    materialized_path: agents/run-variance.md

# Ephemeral overrides applied since the last full sync.
toggles:
  add:
    - id: finance/experimental/new-thing
      version: 0.1.0
      added_at: 2026-05-05T14:35:00Z
  remove:
    - id: finance/ap/legacy-vendor
      removed_at: 2026-05-05T14:36:00Z
```

The lock file is written atomically (`.tmp` + rename) on every sync, watch event, and override invocation. The `profile:` field is the **active profile** for that target. `override`, `save-as`, and `profile edit` use it as the default when no `--profile` flag is given. Concurrent writers against the same target's lock file (e.g., two `podium sync --watch` processes pointed at one directory) are undefined; operators are expected to keep a single sync owner per target.

The target directory is created if it doesn't exist. The same is true for `<target>/.podium/`. `podium sync` errors only when the target path exists but is not writable.

### 7.5.4 Watch Mode and Toggle Persistence

Manual `podium sync` and `podium sync --watch` treat the lock file's `toggles:` section differently:

- **Manual sync** (`podium sync`, no `--watch`): re-resolves the profile, rewrites the target, **clears `toggles` in the lock file**. A manual sync is the operator's "reset to baseline" gesture.
- **Watch mode** (`podium sync --watch`): long-running. On startup it materializes `profile + toggles from the lock file`, so any overrides from a previous session survive. On every registry change event (`artifact.published`, `artifact.deprecated`, `layer.config_changed`), the watcher re-resolves the profile, applies toggles on top, and updates the lock file. Toggles persist across events and across watcher restarts.
- **Watch scope.** Watchers honor the active scope filters; events for artifacts outside the scope are ignored.

Each watcher is workspace-local. Two `podium sync --watch` processes in two different folders run independently, each against its own lock file.

### 7.5.5 Ephemeral Override (`podium sync override`)

`podium sync override` is for on-the-fly toggling without touching `sync.yaml`. Toggles live in the target's `.podium/sync.lock` (`toggles.add` / `toggles.remove`) and are reset by the next manual `podium sync`. They survive watcher events.

Two modes on a single command:

```bash
# TUI: launches a checklist over the resolved set + everything else the caller can see
podium sync override

# Batch: --add and --remove are repeatable, exact IDs only
podium sync override --add finance/experimental/new-thing
podium sync override --remove finance/ap/legacy-vendor
podium sync override --add finance/foo --add finance/bar --remove finance/baz

# Preview without writing
podium sync override --add finance/foo --dry-run

# Clear all toggles in the current target
podium sync override --reset
```

**TUI mode** (no flags). Renders the caller's effective view as an expandable tree (domains as nodes, artifacts as leaf checkboxes). Each entry is annotated with its current state (already materialized, excluded by the profile's `exclude`, etc.) and the layer it comes from. The user toggles items; on quit, the TUI applies the diff to the target directory and updates the lock file.

**Batch mode** (with flags). `--add <id>` fetches and writes the artifact through the active harness adapter, just like a full sync would; the entry lands in `toggles.add`. `--remove <id>` deletes the artifact's materialized files from the target; the entry lands in `toggles.remove` (and is removed from `toggles.add` if it was there). Repeatable. The pair is idempotent: running `--add` on something already materialized is a no-op with a warning.

**Scope.** Override operates on any artifact the caller's identity can see, regardless of the active profile's include/exclude. Visibility filtering still applies; the caller can't `--add` something they can't see. This is the point of override: bring in (or drop) artifacts the profile didn't think of.

**`--reset`** clears `toggles` in the lock file and re-applies the profile's resolved set, dropping artifacts that were `add`ed and re-materializing artifacts that were `remove`d. Equivalent to running a manual `podium sync`.

### 7.5.6 Saving Toggles as a Profile (`podium sync save-as`)

After working with overrides for a while, an operator can capture the current materialized set as a YAML profile:

```bash
# Save the current materialized set as a new profile in .podium/sync.yaml
podium sync save-as --profile finance-team-v2

# Update an existing profile in place
podium sync save-as --profile finance-team --update

# Print the proposed YAML diff without writing
podium sync save-as --profile finance-team --update --dry-run
```

`save-as` reads the current lock file (`scope` + `toggles`), renders an equivalent `include` / `exclude` / `type` block, and writes it to `sync.yaml`. The mapping:

- Existing scope `include` and `exclude` carry over verbatim.
- Each `toggles.add` entry becomes an `include:` entry pinned to the exact ID.
- Each `toggles.remove` entry becomes an `exclude:` entry pinned to the exact ID.
- Type filter carries over.

After `save-as` succeeds, the target's lock file `toggles:` is cleared (the toggles are now part of the profile's scope). If `.podium/sync.yaml` doesn't exist yet, `save-as` creates it with the new profile and an empty `defaults:` block.

### 7.5.7 Editing Profiles Permanently (`podium profile edit`)

A separate command for permanent edits to entries in `sync.yaml`. Distinct from `podium sync override`, which is ephemeral.

```bash
# TUI for the active or named profile
podium profile edit
podium profile edit finance-team

# Batch: add/remove patterns to/from include or exclude
podium profile edit finance-team --add-include "finance/new-thing/**"
podium profile edit finance-team --remove-exclude "finance/old-deprecated/**"

# Print proposed YAML diff without writing
podium profile edit finance-team --add-include "finance/foo" --dry-run
```

`podium profile edit` modifies `sync.yaml` in place, preserving formatting and comments around the edited keys (round-trip via a comment-preserving YAML parser). It does not touch the target directory or any lock file. To apply the change to a workspace, run `podium sync` afterwards. If `.podium/sync.yaml` doesn't exist, `podium profile edit <name>` creates it with the named profile and an empty `defaults:` block; `podium profile edit` (no name) errors and asks the user to specify a name.

The flag names are distinct from `podium sync --include` / `--exclude` (ephemeral scope flags applied at sync time) and from `podium sync override --add` / `--remove` (ephemeral toggles on exact IDs). `podium profile edit` writes patterns into the profile YAML; the other two never touch `sync.yaml`.

## 7.6 Language SDKs

Two thin language SDKs are provided, both backed by the registry's HTTP API:

- **`podium-py`** (PyPI): for Python orchestrators. Used by LangChain consumers, OpenAI Assistants integrations, custom build/eval pipelines, and notebook environments.
- **`podium-ts`** (npm): for TypeScript / Node orchestrators. Used by Bedrock Agents, custom Node-based agent runtimes, and Edge runtime integrations.

**Require a server-source registry.** Both SDKs speak HTTP and do not work against a filesystem-source registry (§13.11).

Surface area:

```python
from podium import Client

# from_env reads PODIUM_REGISTRY, PODIUM_IDENTITY_PROVIDER,
# PODIUM_OVERLAY_PATH, etc. Constructor params override env values.
client = Client.from_env()

# Or pass explicitly:
client = Client(
    registry="https://podium.acme.com",
    identity_provider="oauth-device-code",
    overlay_path="./.podium/overlay/",   # workspace local overlay (§6.4)
)

# Discovery
domains = client.load_domain("finance/close-reporting")
candidates = client.search_domains("vendor payments", top_k=5)
results = client.search_artifacts("variance analysis", type="skill")

# Full filter surface: type, tags, scope, top_k, session_id
results = client.search_artifacts(
    "variance analysis",
    type="skill",
    tags=["finance", "close"],
    scope="finance/close-reporting",
    top_k=10,
    session_id=session_id,
)

# Type-specific lookups
agents = client.search_artifacts("payment workflow", type="agent")
contexts = client.search_artifacts("style guide", type="context")
mcp_servers = client.search_artifacts(type="mcp-server")

# Browse: no query, scope only — list artifacts in a domain
browse = client.search_artifacts(scope="finance/ap", top_k=50)
print(f"showing {len(browse.results)} of {browse.total_matched} artifacts in finance/ap")

# Load (in-memory or to disk)
artifact = client.load_artifact("finance/close-reporting/run-variance-analysis")
print(artifact.manifest_body)
artifact.materialize(to="./artifacts/", harness="claude-code")  # respects the harness adapter

# Streaming change events for sync use cases
for event in client.subscribe(["artifact.published", "artifact.deprecated"]):
    ...

# Cross-type dependency walks (for impact analysis in custom tooling)
deps = client.dependents_of("finance/ap/pay-invoice@1.2")
```

Identity providers, the cache, visibility filtering, layer composition, and audit are all the same as in the MCP path; the SDK is just a different transport. Identity provider plug-points are exposed; custom providers register through the same interface as the MCP server's.

The SDKs deliberately do not implement the MCP meta-tool semantics (the agent-driven lazy materialization). Programmatic consumers know what they want; they don't need an LLM-mediated browse interface. If a programmatic consumer wants lazy semantics, it can call `load_artifact` lazily in its own code.

### 7.6.1 Read CLI

For shell pipelines and language-agnostic scripts that don't want to take a Python or Node dependency just to read the catalog, the same read operations are exposed as `podium` subcommands. Each maps 1:1 to the corresponding SDK call and uses the same identity, cache, layer composition, and visibility filtering server-side.

| Command                       | Maps to                                    | Behavior                                                                                                                                                                         |
| ----------------------------- | ------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `podium search <query>`       | `Client.search_artifacts(...)`             | Hybrid search over artifacts. Flags `--type`, `--tags`, `--scope`, `--top-k` mirror the SDK args. Returns ranked descriptors.                                                    |
| `podium domain search <query>`| `Client.search_domains(...)`               | Hybrid search over domains. Flags `--scope`, `--top-k` mirror the SDK args. Returns ranked domain descriptors.                                                                   |
| `podium domain show [<path>]` | `Client.load_domain(path)`                 | Domain map for `<path>` (or root when no path is given).                                                                                                                         |
| `podium artifact show <id>`   | `Client.load_artifact(id)` (manifest only) | Prints the manifest body and frontmatter to stdout. **Does not materialize bundled resources**; for that, use `podium sync --include <id>`. Flags: `--version`, `--session-id`.  |

Output formats:

- **Default**: human-readable rendering. Search results are a ranked table prefixed with `Showing N of M results` so the user can tell when more matched than were rendered; domain trees are nested bullets; manifests are printed as the markdown body with frontmatter at the top.
- **`--json`**: structured envelope with stable keys, designed to be piped into `jq`. Schemas:

  ```json
  // podium search ... --json
  { "query": "...",
    "total_matched": 47,
    "results": [ { "id": "...", "type": "...", "version": "...",
                   "score": 0.83, "frontmatter": { ... } }, ... ] }

  // podium domain search <query> --json
  { "query": "...",
    "total_matched": 8,
    "results": [ { "path": "...", "name": "...",
                   "description": "...", "keywords": ["...", "..."],
                   "score": 0.87 }, ... ] }

  // podium domain show <path> --json
  { "path": "...",
    "description": "...",
    "keywords": ["...", "..."],
    "subdomains": [ { "path": "...", "name": "...", "description": "..." }, ... ],
    "notable": [ { "id": "...", "type": "...", "summary": "...",
                   "source": "featured|signal",
                   "folded_from": "<canonical subpath; omitted when not folded>" }, ... ],
    "note": "Notable list reduced from 10 to 4 to fit the response budget." }

  // podium artifact show <id> --json
  { "id": "...", "version": "...", "content_hash": "...",
    "frontmatter": { ... }, "body": "..." }
  ```

The CLI and SDK are intentionally interchangeable for these read operations. Pick whichever fits the surrounding code. Both defer to the same `RegistrySearchProvider`, `LayerComposer`, and cache paths server-side; output drift between them is treated as a bug.

Example: fully scripted curation without an SDK install.

```bash
podium search "month-end close OR variance" --type skill --top-k 15 --json \
  | jq -r '.results[] | select(.score > 0.5) | .id' \
  | xargs -I{} podium sync --harness claude-code --target ~/.claude/ --include {}
```

### 7.6.2 Bulk Fetch

`load_artifact` works one ID at a time. Programmatic consumers (eval harnesses, batch workflows, custom orchestrators) that need a known set of artifacts up front pay the per-request round-trip N times when iterating. `Client.load_artifacts` is the bulk variant: one HTTP request, one auth check, one visibility composition pass, one transactional snapshot.

```python
artifacts = client.load_artifacts(
    ids=[
        "finance/close-reporting/run-variance-analysis",
        "finance/close-reporting/policy-doc",
        "finance/ap/pay-invoice",
    ],
    session_id=session_id,        # honors the same `latest`-resolution semantics as load_artifact
    harness="claude-code",        # optional per-call adapter override
)

for result in artifacts:
    if result.status == "ok":
        result.materialize(to="./artifacts/")
    else:
        log.warning("skip %s: %s", result.id, result.error.code)
```

**Wire format.** `POST /v1/artifacts:batchLoad` with body `{ids: [...], session_id?, harness?, version_pins?: {<id>: <semver>}}`. Response is an array of per-item envelopes:

```json
[
  {
    "id": "finance/close-reporting/run-variance-analysis",
    "status": "ok",
    "version": "1.2.0",
    "content_hash": "sha256:...",
    "manifest_body": "...",
    "resources": [
      { "path": "...", "presigned_url": "...", "content_hash": "..." }
    ]
  },
  {
    "id": "finance/restricted/payroll-runner",
    "status": "error",
    "error": { "code": "visibility.denied", "message": "..." }
  }
]
```

**Semantics.**

- **Hard cap:** 50 IDs per batch. The SDK splits larger sets transparently.
- **Visibility:** identical to `load_artifact`. Items the caller cannot see come back as `status: "error"` with `visibility.denied`; no leak about whether the artifact exists in some hidden layer.
- **Session consistency:** with `session_id`, the first occurrence of each `(id, "latest")` in the batch freezes the resolved version for the rest of the batch and session.
- **Partial failure** does not fail the batch. Each item carries its own status.
- **Bandwidth:** large bundled resources travel via presigned URLs (§4.4) so the response body stays small; the SDK fetches resources concurrently after the response.

**Not exposed as an MCP meta-tool** (§5). The MCP path is agent-mediated and load-on-demand; bulk loading is a programmatic-runtime concern that doesn't belong in the agent's tool list. The MCP server uses this endpoint internally for cache warm-up when configured to prefetch.

## 7.7 Onboarding: `podium init`, `podium config show`, `podium login`

Three commands cover client-side lifecycle: `podium init` writes a `sync.yaml`; `podium config show` displays the merged result with provenance; `podium login` runs the OAuth flow when the resolved registry is a server. Server-side setup is handled separately by `podium serve` (§13.10), which auto-bootstraps standalone defaults on first run.

### `podium init`

Writes a `sync.yaml`. Default scope is workspace (`<ws>/.podium/sync.yaml`, committed to git); scope flags `--global` and `--local` target the user-global file or the gitignored project-local override file respectively. Idempotent. Refuses to overwrite an existing file without `--force`.

```bash
# Workspace (default) — writes <ws>/.podium/sync.yaml, committed
podium init                                              # interactive wizard
podium init --registry https://podium.acme.com           # set the project's registry
podium init --registry .podium/registry/                 # filesystem source — see §13.11
podium init --standalone                                  # shortcut for --registry http://127.0.0.1:8080
podium init --harness claude-code --target .claude/       # set per-project defaults at init time

# User-global — writes ~/.podium/sync.yaml
podium init --global                                      # interactive
podium init --global --registry https://podium.acme.com
podium init --global --standalone

# Workspace personal override — writes <ws>/.podium/sync.local.yaml (gitignored)
podium init --local
podium init --local --registry https://podium-staging.acme.com
```

Mental model: scope flags (`--global`, `--local`) decide _where the file goes_; value flags (`--registry`, `--standalone`, `--harness`, `--target`) decide _what's in it_. Default scope is workspace because that's the common case; making it implicit keeps the standard onboarding path short.

`--registry <url-or-path>` accepts either a URL (server source) or a filesystem path (filesystem source, §13.11). The flag's value type determines the registry source; there is no separate `--mode` flag.

`--standalone` is a shortcut for `--registry http://127.0.0.1:8080`. It is purely client-side (this command only writes `sync.yaml`); the server itself is started by `podium serve`. For the all-in-one server-and-client bootstrap, use `podium serve` zero-flag (§13.10), which writes both `registry.yaml` and `sync.yaml` and starts serving.

**Workspace mode behavior:**

1. Walks up from CWD to find an existing `.podium/` directory; if none, creates `.podium/` in CWD.
2. Writes `<workspace>/.podium/sync.yaml` with the chosen value flags as `defaults`.
3. Adds `.podium/sync.local.yaml` and `.podium/overlay/` entries to `.gitignore` (creating it if needed) if they aren't already present.
4. Prints next-step hints (commit `<ws>/.podium/sync.yaml`, run `podium sync` to materialize).

`--global` writes to `~/.podium/sync.yaml` regardless of CWD and does not touch `.gitignore`. `--local` writes to `<ws>/.podium/sync.local.yaml` and assumes the workspace is already initialized.

### `podium config show`

Prints the merged `sync.yaml` with per-key provenance:

```
$ podium config show
defaults.registry:   https://podium.acme.com         (from <ws>/.podium/sync.yaml)
defaults.harness:    claude-code                      (from ~/.podium/sync.yaml)
defaults.target:     .claude/                         (from <ws>/.podium/sync.yaml)
profiles.project-default:
  include:           ["finance/**", "shared/policies/*"]   (from <ws>/.podium/sync.yaml)
  exclude:           ["finance/**/legacy/**"]              (from <ws>/.podium/sync.yaml)
profiles.staging:                                      (defined in <ws>/.podium/sync.local.yaml)
  registry:          https://podium-staging.acme.com
…

Profile collisions: 1 (profiles.staging defined in both ~/.podium/sync.yaml and <ws>/.podium/sync.local.yaml; project-local wins)
```

`--explain <key>` prints just one key with its full resolution chain: which file each scope had, and which won. Useful when the merged output is large.

### `podium login`

Explicit OAuth device-code flow against the resolved registry. Useful when sync is being scripted (auth before the script runs), when re-authing after token expiry, or when the user wants to confirm their identity before doing anything destructive.

```bash
podium login                                    # uses the merged config to find the registry
podium login --registry https://podium.acme.com # override; useful for ad-hoc switches
podium login --no-browser                       # don't auto-open the verification URL
podium logout                                   # clears the cached token from the OS keychain
```

Behavior: resolves the registry from the merged config (or `--registry` flag), prints the verification URL and code to stderr, attempts to open the URL in the system browser, polls the IdP's token endpoint until the user completes the flow or a 10-minute timeout elapses, caches the access + refresh tokens in the OS keychain (per `oauth-device-code` in §6.3), and prints the resolved identity (`sub`, `email`, OIDC groups) on success. Exits non-zero on timeout, denial, or `auth.untrusted_runtime`.

**Multi-endpoint behavior.** Tokens cache in the OS keychain keyed by registry URL. A developer logged into both `https://podium.acme.com` and `https://podium-finance.acme.com` keeps both tokens simultaneously; switching projects (or running `podium login` in any context) authenticates against whichever registry the merged config resolves to. No `podium logout` between project switches required.

`podium login` is a no-op when the resolved registry is a filesystem path (no auth) or points at a `--standalone` server (no auth). In both cases it prints a notice and exits.
