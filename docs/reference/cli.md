---
layout: default
title: CLI
parent: Reference
nav_order: 1
description: Every podium subcommand: setup, server, sync, layer management, search, admin, signing, vulnerability tracking.
---

# CLI

Every `podium` subcommand grouped by purpose. This page is reference; for task-oriented guides, see [Quickstart](../getting-started/quickstart), [Authoring](../authoring/), [Consuming](../consuming/), and [Deployment](../deployment/).

The `podium` CLI is a single binary. Subcommands accept `--help` for inline detail.

---

## Setup and config

### `podium init`

Writes `sync.yaml` for client-side configuration.

```
podium init [--global | --local]
            [--registry <url-or-path>]
            [--harness <name>]
            [--target <path>]
            [--standalone]
            [--force]
```

| Scope flag | Path |
|:--|:--|
| (default) | `<workspace>/.podium/sync.yaml` (committed). |
| `--global` | `~/.podium/sync.yaml`. |
| `--local` | `<workspace>/.podium/sync.local.yaml` (gitignored). |

Value flags:

- `--registry <url-or-path>`: server URL (HTTP) or filesystem path.
- `--harness <name>`: `none`, `claude-code`, `claude-desktop`, `claude-cowork`, `cursor`, `codex`, `gemini`, `opencode`, `pi`, `hermes`. See [Configure your harness](../consuming/configure-your-harness#supported-harnesses) for the roster with documentation links.
- `--target <path>`: destination for materialization.
- `--standalone`: shortcut for `--registry http://127.0.0.1:8080`.
- `--force`: overwrite an existing file.

Workspace mode walks up from CWD to find `.podium/`; creates one in CWD if none exists. Adds `.podium/sync.local.yaml` and `.podium/overlay/` to `.gitignore` if not already present.

### `podium config show`

Prints the merged config with per-key provenance (which scope contributed each value).

```
podium config show [--explain <key>]
```

`--explain <key>` prints one key with its full resolution chain.

### `podium login` / `podium logout`

OAuth device-code flow against the resolved registry.

```
podium login [--registry <url>] [--no-browser]
podium logout
```

`--no-browser` skips auto-opening the verification URL. Tokens cache in the OS keychain keyed by registry URL; multiple registries can be authenticated simultaneously.

`podium login` is a no-op when the resolved registry is a filesystem path or a `--standalone` server (no auth in either).

---

## Server

### `podium serve`

Starts the registry server.

```
podium serve [--standalone] [--strict]
             [--config <path>] [--bind <addr>]
             [--layer-path <path>]
             [--public-mode] [--allow-public-bind]
```

| Flag | Effect |
|:--|:--|
| `--standalone` | Single-binary mode with embedded SQLite + sqlite-vec + bundled embedding model. Defaults to bind `127.0.0.1:8080`. |
| `--strict` | Refuse to start without an explicit config (no auto-standalone fallback). |
| `--config <path>` | Override the default config file location. |
| `--bind <addr>` | Bind address. |
| `--layer-path <path>` | For standalone: register a `local`-source layer rooted at this path. |
| `--public-mode` | Bypass authentication and visibility filtering. Mutually exclusive with an identity provider. |
| `--allow-public-bind` | Allow non-loopback bind in public mode (typically behind an authenticated reverse proxy). |

Zero-flag (`podium serve` alone) auto-enters standalone mode when no config is found at `~/.podium/registry.yaml`. Disable with `PODIUM_NO_AUTOSTANDALONE=1` or `--strict`.

### `podium status`

Prints registry status: bind address, mode (standalone / standard / public / read-only), connected layer sources.

```
podium status
```

---

## Authoring & validation

### `podium lint`

Validates manifests against the type's schema and runs type-specific rules. CI-friendly; runs the same checks the registry runs at ingest.

```
podium lint <path>
```

`<path>` can be an artifact directory (containing `ARTIFACT.md`, plus `SKILL.md` for skills), a single `ARTIFACT.md`, `SKILL.md`, or `DOMAIN.md` file, or a directory tree (recurses into all artifacts). Exits non-zero on lint errors.

---

## Sync and materialization

### `podium sync`

Materializes the user's effective view to disk via the configured harness adapter.

```
podium sync [--target <path>] [--harness <name>]
            [--profile <name>] [--config <path>]
            [--include <pattern>] [--exclude <pattern>] [--type <t1,t2>]
            [--watch] [--dry-run] [--json]
```

| Flag | Effect |
|:--|:--|
| `--target <path>` | Destination directory. Default: CWD. |
| `--harness <name>` | Override the configured harness. |
| `--profile <name>` | Use a named profile from `sync.yaml`. |
| `--config <path>` | Override the config path; with `targets:`, runs multi-target. |
| `--include <pattern>` | Glob to include (canonical artifact IDs). Repeatable. |
| `--exclude <pattern>` | Glob to exclude. Applied after include. Repeatable. |
| `--type <t1,t2,...>` | Restrict to a comma-separated list of artifact types. |
| `--watch` | Long-running. Re-materialize on registry change events or fsnotify in filesystem mode. |
| `--dry-run` | Print the resolved set; write nothing. |
| `--json` | Structured envelope output (pipe to `jq`). |

Lock file at `<target>/.podium/sync.lock`.

### `podium sync override`

On-the-fly toggling without touching `sync.yaml`. Toggles persist across watcher events and clear on the next manual `podium sync`.

```
podium sync override                              # TUI checklist
podium sync override --add <id>                   # repeatable
podium sync override --remove <id>                # repeatable
podium sync override --reset                      # clear all toggles
podium sync override --add <id> --dry-run
```

### `podium sync save-as`

Captures the current materialized set as a YAML profile in `sync.yaml`.

```
podium sync save-as --profile <name> [--update] [--dry-run]
```

`--update` overwrites an existing profile. After `save-as` succeeds, the lock file's toggles are cleared.

### `podium profile edit`

Permanent edits to entries in `sync.yaml`. Distinct from `podium sync override`, which is ephemeral.

```
podium profile edit                                       # TUI for the active profile
podium profile edit <name>                                # TUI for the named profile
podium profile edit <name> --add-include <pattern>
podium profile edit <name> --remove-include <pattern>
podium profile edit <name> --add-exclude <pattern>
podium profile edit <name> --remove-exclude <pattern>
podium profile edit <name> --add-include <pattern> --dry-run
```

Modifies `sync.yaml` in place, preserving formatting and comments around untouched keys.

---

## Read CLI

The read CLI maps 1:1 to the SDK's read operations and uses the same identity, cache, layer composition, and visibility filtering server-side.

### `podium search`

Hybrid search over artifacts.

```
podium search <query> [--type <t>] [--tags <tag1,tag2>]
                      [--scope <path>] [--top-k <n>]
                      [--json]
```

### `podium domain show`

Domain map for a path (or root when no path is given).

```
podium domain show [<path>] [--json]
```

### `podium domain search`

Hybrid search over domains.

```
podium domain search <query> [--scope <path>] [--top-k <n>] [--json]
```

### `podium domain analyze`

Operator command. Renders a quality report: sparsity per node, pass-through chains, candidates for split (high artifact count + tag-cluster entropy) or fold (low artifact count).

```
podium domain analyze [<path>]
```

### `podium artifact show`

Prints the manifest body and frontmatter to stdout. Does **not** materialize bundled resources.

```
podium artifact show <id> [--version <semver>]
                          [--session-id <uuid>]
                          [--json]
```

For materialization (writing files to disk), use `podium sync --include <id>`.

---

## Layer management

### `podium layer register`

Registers a new layer.

```
podium layer register --id <id> --repo <git-url> --ref <ref> [--root <subpath>]
podium layer register --id <id> --local <path>
```

For Git sources, the registry returns the webhook URL and HMAC secret to configure on the source repo. Without webhook configuration, the layer stays at its initial commit until the first manual reingest.

### `podium layer list`

Lists configured layers and their current state.

```
podium layer list
```

### `podium layer reorder`

Reorders user-defined layers. Admin layers are reordered through admin tooling; this command applies only to the caller's user-defined layers.

```
podium layer reorder <id> [<id> ...]
```

The argument order is precedence, lowest to highest.

### `podium layer unregister`

Removes a layer. Admin layers require admin rights; user-defined layers can be removed by the registrant.

```
podium layer unregister <id>
```

### `podium layer reingest`

Forces a re-pull of a layer's source.

```
podium layer reingest <id> [--break-glass --justification <text>]
```

During a freeze window, ingest is blocked unless `--break-glass` is passed with a justification. Break-glass requires dual-signoff (two admins), auto-expires after 24h, and queues for post-hoc security review.

### `podium layer watch`

Polls a layer's source for changes at a configured interval. Works against `local`-source layers and against `git`-source layers that do not have a webhook configured (for example, on a developer machine without a public ingress).

```
podium layer watch <id> [--interval <duration>]
```

`--interval` defaults to a sensible value per source type.

---

## Admin

Admin commands require the `admin` role on the tenant. Admin grants are recorded as `(identity, org_id, "admin")` rows; manage them via `podium admin grant` / `podium admin revoke`.

### `podium admin tenant create`

Creates a tenant.

```
podium admin tenant create <name> [--display-name "Human-readable name"]
```

### `podium admin grant` / `podium admin revoke`

```
podium admin grant --tenant <name> --user <id> --role admin
podium admin revoke --tenant <name> --user <id> --role admin
```

### `podium admin show-effective`

Surfaces the effective view for any identity. Useful for debugging visibility issues.

```
podium admin show-effective <user>
```

### `podium admin reembed`

Regenerates embeddings. Triggered automatically when the configured embedding model changes; this command is for ad-hoc re-embeds.

```
podium admin reembed [--all] [--since <timestamp>]
```

### `podium admin migrate`

Schema migrations. The expand-contract pattern means most upgrades don't require operator intervention; this command handles the rare cases.

```
podium admin migrate --finalize           # drop now-unused old columns/indexes
podium admin migrate --revert             # revert the most recent migration
```

### `podium admin migrate-to-standard`

Exports a standalone deployment to a standard one.

```
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
```

### `podium admin verify`

Operational sanity checks.

```
podium admin verify --check audit-chain
podium admin verify --check signatures
podium admin verify --check schema
```

`audit-chain` walks the hash-chain integrity. Run weekly via cron in production.

### `podium admin scim-sync`

Forces a SCIM refresh for an identity. Useful when an OIDC IdP doesn't push and a user's group membership changes.

```
podium admin scim-sync --user <id>
```

### `podium admin erase`

GDPR right-to-erasure. Unregisters user-defined layers, redacts audit identity (replaces `sub` with `redacted-<sha256(sub+salt)>`), preserves audit event sequencing.

```
podium admin erase <user_id>
```

Erasure is itself logged as a `user.erased` event.

---

## Vulnerability tracking

### `podium vuln list`

Lists artifacts affected by known CVEs (per the configured CVE feed and ingested SBOMs).

```
podium vuln list [--severity <level>]
```

### `podium vuln explain`

Shows the dependency path for a CVE-affected artifact.

```
podium vuln explain <cve> <artifact>
```

---

## Signing

### `podium sign`

Explicit signing outside the ingest flow.

```
podium sign <artifact>
```

Two key models exist per deployment configuration: Sigstore-keyless (preferred; OIDC-attested signature with transparency log entry) and registry-managed key (per-org key managed by the registry, rotated quarterly).

### `podium verify`

Ad-hoc signature verification.

```
podium verify <artifact>
```

The MCP server verifies signatures automatically on materialization for sensitivity ≥ medium (configurable per deployment).

---

## Cache and quota

### `podium cache prune`

Cleans up the content-addressed cache.

```
podium cache prune
```

The cache lives at `~/.podium/cache/` by default (override with `PODIUM_CACHE_DIR`). Content cache entries are immutable; safe to prune by age.

### `podium quota`

Shows current usage and limits per quota type.

```
podium quota
```

Quotas: storage, search QPS, materialization rate, audit volume, user-defined-layer cap.

---

## JSON output

Most read commands accept `--json` for piping into other tools. Schemas are stable and documented per command in [HTTP API](http-api).

```bash
podium search "month-end close OR variance" --type skill --top-k 15 --json \
  | jq -r '.results[] | select(.score > 0.5) | .id' \
  | xargs -I{} podium sync --harness claude-code --target ~/.claude/ --include {}
```

---

## Environment variables

| Variable | Purpose |
|:--|:--|
| `PODIUM_REGISTRY` | Registry source: URL or filesystem path. |
| `PODIUM_HARNESS` | Default harness adapter. |
| `PODIUM_OVERLAY_PATH` | Workspace local-overlay path. |
| `PODIUM_CACHE_DIR` | Content-addressed cache directory. Default `~/.podium/cache/`. |
| `PODIUM_CACHE_MODE` | `always-revalidate` (default), `offline-first`, `offline-only`. |
| `PODIUM_AUDIT_SINK` | Local audit destination. |
| `PODIUM_MATERIALIZE_ROOT` | Default destination for `load_artifact` materialization. |
| `PODIUM_PRESIGN_TTL_SECONDS` | Override for presigned URL TTL. |
| `PODIUM_VERIFY_SIGNATURES` | `never`, `medium-and-above` (default), `high-only`, `always`. |
| `PODIUM_IDENTITY_PROVIDER` | `oauth-device-code` (default), `injected-session-token`. |
| `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` | OAuth provider config. |
| `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE` | Injected-token sources. |
| `PODIUM_PUBLIC_MODE` | Equivalent of `--public-mode`. |
| `PODIUM_NO_AUTOSTANDALONE` | Disable zero-flag standalone fallback. |

The full list, including server-side backend selection vars (`PODIUM_VECTOR_BACKEND`, `PODIUM_EMBEDDING_PROVIDER`, etc.), is in [`spec/13-deployment.md` §13.12](https://github.com/lennylabs/podium/blob/main/spec/13-deployment.md#1312-backend-configuration-reference).
