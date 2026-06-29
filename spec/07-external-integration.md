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

1. Edit `ARTIFACT.md` (and `SKILL.md` for skills, plus bundled resources) in a checkout of the layer's Git repo. The CLI helper `podium artifact scaffold --type <type> <path>` writes lint-clean starting files for any §4.3 type; it is a pure filesystem operation and does not talk to the registry.
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
podium layer reorder <id> [<id> ...]            # admin-defined layers (admin auth) or your own user-defined layers
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

`layer.ingested` fires once per completed layer ingest cycle. A CI marketplace-publish job subscribes a receiver to `layer.ingested` (§7.8), so one source commit triggers one publish across the artifacts it changed. `artifact.published` is not used for this purpose, because it fires once per ingested `(artifact_id, version)` and would trigger one publish per changed artifact rather than one per source commit. Receiver authorization and the per-receiver debounce window with its batch delivery body are specified in proposal 0004 (webhook hardening).

### 7.3.3 Tenant Management

These are operator-level HTTP endpoints, parallel to the §7.3.1 layer-management endpoints (`GET /v1/layers`, `POST /v1/layers/reingest`, `DELETE /v1/layers`). Operator authorization is distinct from the per-tenant `admin` role defined in §4.7.2; the operator role is established per §4.7.1 (Operator role). The four endpoints are operator-only and audited (§8.1). The mutating endpoints (`POST`, `PATCH`, and `DELETE`) are write endpoints under §13.2.1, so a read-only registry rejects them with `registry.read_only`, consistent with the layer-admin and admin-grant endpoints; `GET /v1/admin/tenants` is a read and stays available in read-only mode. The registry checks this after operator authorization, mirroring the admin-grant handler.

- `POST /v1/admin/tenants` creates a tenant from `{name, quota?, expose_scope_preview?}`, deriving the org ID from the name, reusing the idempotent `CreateTenant`, and returning `201` with the tenant object `{id, name, quota, expose_scope_preview, active}` (or `200` with the same object when the name already names a provisioned tenant, so a re-create surfaces the existing tenant's `active` state).
- `GET /v1/admin/tenants` returns `{tenants: [...]}`, each element the tenant object `{id, name, quota, expose_scope_preview, active}`, the only cross-org read, operator-only. The tenants registry is under `FORCE ROW LEVEL SECURITY`, so the registry re-scopes the policy through an operator sentinel rather than by setting a per-org `org_id`: its FORCE'd policy admits the sentinel value `*operator-list*`, and the read runs on the registry's existing `PODIUM_POSTGRES_DSN` table-owner connection with `podium.org_id` set to that sentinel, returning every row. Every per-org tenant read keeps `podium.org_id` set to a real org ID and stays confined to its own org. The sentinel policy is created from the binary, because creating and altering a policy and forcing row-level security require only table ownership, which the owner connection holds; the schema setup adds no superuser step and the read holds no `BYPASSRLS` attribute, so the list works on managed Postgres (§13.1).
- `PATCH /v1/admin/tenants/{id}` is a partial update. The registry reads the current tenant and merges the supplied fields. At the top level it applies only `quota`, `expose_scope_preview`, or `active` when present. Within `quota`, the merge is per sub-field: it overwrites only the quota sub-fields present in the body (`storage_bytes`, `search_qps`, `materialize_rate`, `audit_volume_per_day`, `max_user_layers`) and preserves every omitted sub-field at its current value. Because a zero quota value is itself meaningful (§4.7.8), each quota sub-field is nullable in the request body: a present sub-field set to `0` is applied as zero, and an absent sub-field is preserved. Omitting `quota` entirely preserves the whole existing quota; omitting `expose_scope_preview` preserves the existing tri-state gate; omitting `active` leaves the active state unchanged. The registry writes the merged record via `UpdateTenant` and returns the merged tenant object `{id, name, quota, expose_scope_preview, active}`. The endpoint does not change the name, which is fixed at create. An unknown ID returns `404 registry.tenant_not_found`.
- `DELETE /v1/admin/tenants/{id}` deactivates the tenant (soft); an unknown ID returns `404 registry.tenant_not_found`.

The org ID and the org-name alias (§4.7.1) are both fixed at create. The org ID is the UUIDv5 derivation of the name (§6.3.1 resolves a request's org-name alias to its org ID through this derivation), so `PATCH` cannot change `name`: changing the stored name without re-keying the org would leave the tenant resolvable only under its creation-time name and unreachable under the new one. Rename is therefore out of scope; a tenant that needs a different name is provisioned anew under that name.

The tenant-management endpoints are available only on a multi-tenant standard backend (§13.1). A single-tenant backend, whether a standalone backend (§13.10) with its sole synthetic `default` org or a standard backend started without `PODIUM_MULTI_TENANT`, rejects every `/v1/admin/tenants` request with `registry.tenant_management_unavailable` (§6.10), because multi-tenancy is out of scope for standalone (§13.10) and the standalone routing does not consult tenants created through the API.

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

The sync model is type-agnostic: skills, agents, contexts, commands, rules, hooks, and `mcp-server` registrations all sync through the same path; the harness adapter decides where each type lands.

**`--dry-run`** resolves the artifact set against the current scope and prints it without writing. Default output is human-readable; `--json` produces a structured envelope (`{profile, target, harness, scope, artifacts: [{id, version, content_hash, type, layer}, ...]}`) for piping into `jq`. The per-artifact `content_hash` lets a pre-flight check verify the full §14.11 `(artifact_id, version, content_hash)` triple before the lock file is committed.

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
    kind: workspace # the default; materializes the project-files layout
    harness: claude-code
    target: ~/.claude/
    profile: project-default
  - id: codex-runbooks
    harness: codex
    target: ~/.codex/
    include: ["shared/runbooks/**"]

  - id: acme-agents
    kind: marketplace # renders the marketplace emitters (§7.8)
    harnesses: [claude-code, codex, cursor]
    target: ./build/acme-agents # the working directory the checkout lands in
    git:
      remote: git@github.com:acme/agent-marketplace.git
      branch: main
    commit_message: "Sync Podium catalog ({{.ChangedCount}}) {{.Timestamp}}"
    identity: publisher@acme.com # or inherited from defaults.identity
    plugins:
      - name: finance-pack
        include: ["finance/**"]
    workflow:
      prepare:
        - run: ["git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
      publish:
        - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
        - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
          skip_if_no_changes: true
        - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]
```

**Registry source.** `defaults.registry` accepts either a URL or a filesystem path; the client adapts:

- **URL** (`http://` / `https://`): the client speaks HTTP to that registry server. All consumer paths (MCP server, SDKs, `podium sync`, read CLI) work.
- **Filesystem path** (relative or absolute): `podium sync` reads the directory directly, applies layer composition (§4.6), and materializes through the configured harness adapter. The path is interpreted as either a filesystem-registry root (each subdirectory is a `local`-source layer; ordering follows §13.11.1) or a single-layer directory (one `local`-source layer rooted at the path); the choice is governed by `<registry-path>/.registry-config` (`multi_layer: true` opts into filesystem-registry mode). The same `multi_layer:` flag tells the standalone server's `--layer-path` (§13.10) to treat the directory as a filesystem-registry root rather than a single layer, so `podium sync` and the standalone server reach matching interpretations of the same path. **`podium sync` is the only consumer that works against a filesystem registry.** The MCP server and the SDKs require a server source. See §13.11 for the full filesystem-registry description.
- **Unset across all scopes**: `config.no_registry` error. The client points the user at `podium init` to configure one. There is no implicit workspace fallback; `podium sync` will not auto-detect `.podium/registry/` without explicit config.

Override an inherited URL with a filesystem path by setting `defaults.registry` explicitly at a higher-precedence scope (typically the project-shared file). Normal precedence applies; no magic-value semantics.

**Resolution rules.**

- **Profile lookup.** `--profile <name>` selects an entry under `profiles:`. The profile's fields are merged on top of `defaults:`.
- **CLI override.** Explicit CLI flags override the resolved profile (and defaults) for the same field. `--include` and `--exclude` on the CLI replace the profile's lists rather than appending; if you need additive composition, define a new profile.
- **Multi-target mode.** `podium sync --config <path>` (without `--profile`) iterates `targets:` and runs one sync per entry. Each entry can name a `profile:` (resolved as above) or specify `include`/`exclude`/`type` inline. Each target writes its own `<target>/.podium/sync.lock`; the multi-target invocation does not introduce shared state across targets.
- **Profile composition.** Profiles do not reference other profiles; nesting is intentionally absent. A team that wants an "extended" profile defines a new entry with the combined include/exclude lists.
- **Validation.** `podium sync --check` validates the merged config against the schema and reports unresolved profile references, malformed globs, target collisions, and profile-name collisions across scopes (warning, not error).

A `targets:` entry carries a `kind:` field that selects its output format. `kind: workspace` is the default and materializes the project-files layout into the target directory, the layout the harness reads directly. `kind: marketplace` renders the harness's git-repo distribution layout (§7.8) into the target directory as a working checkout. A `kind: workspace` entry carries the harness, the target directory, and the scope fields (`profile`, `include`, `exclude`, and `type`). A `kind: marketplace` entry carries a harness set (`harnesses`), a git remote and branch, a commit message, a plugin list, and a publishing identity. Both kinds may carry a `workflow:` of `prepare` and `publish` command lists. The marketplace fields are rejected on a `kind: workspace` entry, and the workspace scope fields, the watch mode (§7.5.4), and the ephemeral overrides (§7.5.5) are rejected on a `kind: marketplace` entry. The publishing identity is the §4.6 effective-view principal whose visibility defines what reaches the marketplace, and a marketplace target inherits it from an optional `defaults.identity` when it sets none of its own. A configuration that declares only `kind: workspace` targets, or omits `kind` entirely, behaves exactly as before, because `kind` defaults to `workspace`.

A `workflow:` runs only when `podium sync` materializes a target. The MCP server and the in-process bridge read `sync.yaml` for the registry, the profiles, and the version pin, and they never execute a target's `workflow`. A `workflow` command therefore runs with the privileges of the `podium sync` process and never inside the registry server.

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
target: /Users/alice/.claude/
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

Behavior: resolves the registry from the merged config (or `--registry` flag), prints the verification URL and code to stderr, attempts to open the URL in the system browser, polls the IdP's token endpoint until the user completes the flow or a 10-minute timeout elapses, caches the access + refresh tokens in the OS keychain (per `oauth-device-code` in §6.3), and prints the resolved identity (`sub`, `email`, OIDC groups) on success. Exits non-zero on timeout or denial. The `--no-browser` flag and the `PODIUM_NO_BROWSER` environment variable (truthy: `1`, `true`, `yes`, or `on`) both suppress the browser auto-open for headless and CI environments.

**Multi-endpoint behavior.** Tokens cache in the OS keychain keyed by registry URL. A developer logged into both `https://podium.acme.com` and `https://podium-finance.acme.com` keeps both tokens simultaneously; switching projects (or running `podium login` in any context) authenticates against whichever registry the merged config resolves to. No `podium logout` between project switches required.

`podium login` is a no-op when the resolved registry is a filesystem path (no auth) or points at a `--standalone` server (no auth). In both cases it prints a notice and exits.

## 7.8 Marketplace Publishing

A `podium sync` target of `kind: marketplace` (§7.5.2) renders the catalog into a harness-native marketplace repository and runs an operator-configured workflow that pushes it to a git remote. It is a derived, served output downstream of the registry, consistent with the §1.3 direction in which the registry is the served source of truth. Publishing a repository does not make that repository an authoring source: the catalog continues to enter the registry through the §7.3.1 ingest path, and the published repository is a rendered consequence of the effective view. A `kind: workspace` target writes the effective view into a local workspace directory the harness reads; a `kind: marketplace` target renders the same effective view into the git-repo distribution layout the harness imports (the marketplace, extension, package, or tap layout in §6.7), then runs the target's `workflow` to take the rendered tree to the remote.

### Concepts

- **Marketplace output.** A `kind: marketplace` target declared in `sync.yaml` (§7.5.2): a git repository, a harness set, a plugin list, and a workflow.
- **Plugin.** A named bundle of selected artifacts, defined by a scope filter (`include`, `exclude`, `type`), reusing the §7.5.1 selection. A plugin is the cross-harness unit: it renders into each harness's plugin layout in that harness's subtree. Plugin membership is a packaging decision the operator controls. It is not authored or versioned in the catalog.
- **Harness set.** The harnesses an output publishes for. Each listed harness contributes its format's manifest and its content subtree to the output repository, and harnesses that share a format contribute one shared manifest.

### Publish targets

A publish target is a harness with a git-repo distribution: the marketplace, extension, package, and tap formats described in §6.7. The targets are Claude (Code, Desktop, and Cowork), Codex, Cursor, Gemini, Pi, and Hermes. A harness without a git-repo distribution is not a publish target: OpenCode distributes through npm packages, and `none` writes raw canonical output, so both are excluded. A marketplace output whose harness set names an excluded harness is rejected at config validation.

Claude Code, Claude Desktop, and Claude Cowork read the same `.claude-plugin/marketplace.json`, so one Claude marketplace emitter serves all three, and a harness set that names more than one of them yields one Claude marketplace rather than a collision. The Claude, Codex, and Cursor manifests sit at distinct fixed locations, so they coexist in one repository, each read only by its own harness. The Gemini extension occupies the whole repository, and the Hermes tap defaults to a root `skills/` directory, so those two formats resist sharing a repository and an operator may give each its own output.

### Marketplace target schema

The marketplace-output schema (the git remote and branch, the harness set, the commit message, the plugins, and the workflow) is part of the §7.5.2 `sync.yaml` schema, declared on a `kind: marketplace` target entry. The publishing identity is a marketplace target field inherited from `defaults.identity` when the target sets none of its own. Per target the operator sets the destination (`git.remote`, `git.branch`), the harness set, and the plugin list. The git commands live in the target's `workflow` and reference injected variables.

### Plugin composition

A plugin is a named scope filter. The publishing pipeline assigns each selected artifact to its plugin by evaluating the plugin scope filters in declaration order, then renders the artifact's component files into that plugin under the harness subtree. The per-plugin manifest entry is contributed once per plugin keyed by the plugin name rather than once per artifact, so a plugin that bundles several artifacts yields one plugin entry rather than one per artifact. For the tap and package formats (Hermes, Pi), where the install unit is the individual skill, the plugin groups skills into subtrees without changing the install unit.

### Repository layout

A marketplace output renders into one repository. Each format's manifest lives at its fixed location, and per-harness plugin content lives in per-harness subtrees the manifests reference. For the `acme-agents` output above, with the harness set `[claude-code, codex, cursor]`:

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

Each vendor manifest lists the same plugin set and points its entries at that vendor's subtree through the relative-path or subdirectory plugin source the harness supports. The plugin content is per-harness because the per-plugin manifest filenames and the rule and MCP conventions differ across vendors. A Gemini output writes `gemini-extension.json`, `commands/`, and the context file at the repository root and collapses the plugin set into one extension, so it takes its own output. A Pi output writes a root `package.json` whose `pi.skills` array points at a skills subtree, and a Hermes output writes the skills under the tap's `skills/` directory; both render the `SKILL.md` the harnesses consume. OpenCode and `none` are not publish targets and continue to use `podium sync` for workspace files.

The harness set is the grouping lever: an operator who cannot or does not want to share one repository across two formats declares two outputs. Gemini (one extension per repository) and Hermes (root `skills/`) often take their own output, because each occupies a fixed repository-level location.

### Marketplace emitters

The publishing pipeline selects a marketplace emitter per harness rather than the project-files materialization:

- **Claude (Code, Desktop, Cowork).** One emitter writes `.claude-plugin/marketplace.json` at the repository root and `<subtree>/<plugin>/.claude-plugin/plugin.json` per plugin, with `skills/`, `agents/`, `commands/`, `hooks/`, and `.mcp.json` components. The three Claude surfaces consume the one emitted manifest.
- **Codex.** An emitter writes `.agents/plugins/marketplace.json` at the repository root and `<subtree>/<plugin>/.codex-plugin/plugin.json` per plugin, with `skills/`, `hooks/hooks.json`, and `.mcp.json` components.
- **Cursor.** An emitter writes `.cursor-plugin/marketplace.json` at the repository root and `<subtree>/<plugin>/.cursor-plugin/plugin.json` per plugin, with `skills/`, `rules/*.mdc`, and `mcp.json` components.
- **Gemini.** An emitter writes `gemini-extension.json` at the repository root, `commands/*.toml`, and the context file, treating the output's plugin set as one extension.
- **Pi.** An emitter writes a root `package.json` carrying the `pi-package` keyword and a `pi.skills` array pointing at a skills subtree, with `skills/<name>/SKILL.md` per skill.
- **Hermes.** An emitter writes the tap layout: `skills/<name>/SKILL.md` per skill with its `references/`, `scripts/`, and `assets/`, and no root manifest, matching the tap discovery rule.

Each emitter that carries a JSON manifest (Claude, Codex, Cursor) writes it through the Podium-owned merge so stale entries drop out on re-render, and contributes one marketplace entry per plugin keyed by the plugin name, contributed once per plugin rather than once per artifact. The Hermes tap and the Pi skills subtree carry no merged manifest and reconcile through the sync lock file.

### `podium sync` and the configurable workflow

The multi-target `podium sync --config <path>` run renders each `kind: marketplace` target through a fixed pipeline:

```
prepare (operator commands)  ->  render (Podium)  ->  publish (operator commands)
```

`prepare` is expected to place a checkout of the destination repository at the working directory. `render` materializes each harness's marketplace tree into that working directory through the marketplace emitters above. `publish` is expected to take the rendered tree to the remote. Podium owns config resolution, the effective view, plugin assignment, rendering, reconciliation, change detection, variable injection, command sequencing, logging, and dry-run. The operator's commands own getting the repository to the working directory and taking the result to the remote. The ordering is the reason the phases exist: the checkout must precede the render so the render reconciles against existing repository content, and the commit must follow it. By default Podium allocates the working directory and `prepare` clones into it; a `prepare` phase may instead configure git against an existing checkout, for example a CI checkout step, in which case it configures git or pulls rather than clones. `--dry-run` and `--check` retain their §7.5 meaning. The marketplace fields have no single-target CLI-flag analog, so a marketplace target is reached through the `--config` path (Open questions).

**Injected variables.** Podium passes context to the commands through environment variables rather than by interpreting git state:

- `$PODIUM_WORKDIR`: the per-output working and checkout directory.
- `$PODIUM_OUTPUT_ID`: the marketplace output identifier.
- `$PODIUM_GIT_REMOTE`, `$PODIUM_GIT_BRANCH`: from the output's `git:` block.
- `$PODIUM_COMMIT_MESSAGE`: rendered from `commit_message` with the change count and timestamp.
- `$PODIUM_CHANGED`: whether the render produced a diff against the checkout.
- `$PODIUM_CHANGE_SUMMARY`: a path to a JSON file describing the changed artifacts.
- The registry URL, the publishing identity, and the harness set.

**Execution semantics.** A command is an argv list under `run:`, executed directly without a shell, or a string under `sh:`, executed through `sh -c`. The pipeline inherits the ambient environment of the `podium sync` process and adds the injected variables, because git authentication relies on `SSH_AUTH_SOCK`, `GH_TOKEN`, and similar. The pipeline fails fast on the first non-zero exit, with per-command `continue_on_error`, `timeout`, and `skip_if_no_changes`, and an optional per-phase `on_error` cleanup list. `--dry-run` renders into a temporary directory and prints each command with variables substituted without running the `publish` phase. `--check` validates the config only.

**Trust boundary.** These commands run as subprocesses with the operator's privileges and ambient credentials. They are unrelated to the `MaterializationHook` SPI (§6.6), which is sandboxed to forbid subprocesses, network, and writes outside the destination, and they are unrelated to the `hook` artifact type. A `workflow` executes only on the `podium sync` CLI path and never inside the registry server or the MCP server (the §7.5.2 workflow-execution boundary). The commands live on `sync.yaml` target entries whose project-shared scope is committed to git (§7.5.2). Whether a project-shared `sync.yaml` workflow needs a trust gate is an open decision (Open question 1). A server-side publisher is out of scope.

### Rendering identity and effective view

The published marketplace reflects the publishing identity's effective view (§4.6) intersected with the plugin scope filters. A `kind: marketplace` target carries `identity` so the operator selects the principal whose visibility defines what reaches the marketplace. A principal that can see restricted layers would render them into the output, so the publishing identity is a security-relevant setting, and a public marketplace is published under an identity scoped to the artifacts intended for it.

### Reconciliation

Re-rendering an output is idempotent. The materialization writer and the sync lock file remove files for artifacts that left the view, and the JSON manifests merge with the Podium-owned tag so stale entries drop out. The git diff after a render is the catalog delta, so `skip_if_no_changes` suppresses an empty commit when the delta is empty.

### Triggers

The trigger is the `layer.ingested` event (§7.3.2). The ingest orchestrator emits it once per completed layer cycle, so a single source commit that changes many artifacts yields one event rather than one `artifact.published` per artifact. A CI job subscribes a webhook receiver to `layer.ingested` and runs `podium sync --config <path>`, so one source commit triggers one publish across the artifacts it changed. No new event is required, and a marketplace target has no watch mode.

A `layer.ingested` event for a layer that contributes no artifacts to an output produces a render with no diff, which `skip_if_no_changes` suppresses, so an unrelated layer update does not produce an empty commit.

A burst of `layer.ingested` events is coalesced by the registry's per-receiver webhook debounce window, specified in proposal 0004 (webhook hardening), rather than by a publishing config. Both publishing patterns below function without proposal 0004: the scheduled pattern uses no receiver, and the event-driven pattern works on the existing per-event delivery, where the CI system's own concurrency control collapses redundant runs.

A server-side publisher inside the registry process is out of scope, because it would require storing per-repository push credentials and running operator-supplied commands inside a multi-tenant process.

### GitHub Actions example

A GitHub Actions deployment uses one of two patterns, because GitHub starts a workflow from an external system only through the authenticated REST API (`repository_dispatch` or `workflow_dispatch`), and a Podium webhook receiver posts an HMAC-signed event body that GitHub's dispatch endpoint does not accept.

**Pattern A, scheduled (no bridge).** A workflow in the marketplace repository runs `podium sync --config <path>` on a cron. `skip_if_no_changes` makes an empty run a no-op, so a 5-to-15-minute poll is inexpensive. No webhook receiver is involved, and the debounce window is not used.

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

The marketplace target's `workflow` configures git against the existing checkout, because the checkout step already placed the repository and authenticated `origin`:

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

In both patterns the registry credential `PODIUM_TOKEN` carries the publishing identity, and the git push credential is GitHub's: `GITHUB_TOKEN` in Pattern A, or a deploy key the `prepare` clone uses when the workflow runs outside the marketplace repository. Podium never holds the git push credential.
