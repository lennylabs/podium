---
layout: default
title: CLI
parent: Reference
nav_order: 1
description: "Every podium subcommand: setup, server, sync, layer management, search, admin, signing."
---

# CLI

Every `podium` subcommand grouped by purpose. This page is reference; for task-oriented guides, see [Quickstart](../getting-started/quickstart), [Authoring](../authoring/), [Consuming](../consuming/), and [Deployment](../deployment/).

The `podium` CLI is a single binary.

## Top-level flags

- `podium --help` (or `-h`, or `podium help`): print the command list.
- `podium --version` (or `-v`, or `podium version`): print the build version.

## Subcommand help

Every subcommand and subcommand group accepts `--help` (and the short forms `-h` and `help`). Leaf subcommands print a one-line description followed by their flag list:

```
$ podium serve --help
podium serve - Run the standalone registry server in-process.

Flags:
  -bind string
        address to listen on (overrides PODIUM_BIND)
  -config string
        path to registry.yaml (overrides PODIUM_CONFIG_FILE)
  -layer-path string
        filesystem registry root to ingest at startup (§13.10; overrides PODIUM_LAYER_PATH)
  -public-mode
        run in public mode (overrides PODIUM_PUBLIC_MODE)
  -standalone
        alias for the zero-flag standalone bootstrap
```

Dispatcher groups (`admin`, `cache`, `config`, `domain`, `artifact`, `layer`, `profile`, `admin runtime`) print their subcommand list. `sync` also dispatches the `override` and `save-as` subcommands when one is the first argument, and otherwise runs materialization directly:

```
$ podium admin --help
podium admin - Administer the registry: grants, audit, runtime keys, migration.

Subcommands:
  grant                Grant tenant admin role to a user.
  revoke               Revoke tenant admin role from a user.
  show-effective       Print the per-layer visibility for a user identity.
  erase                GDPR right-to-be-forgotten on the local audit log.
  retention            Apply audit retention policies to the local audit log.
  reembed              Re-run vector embeddings against the configured registry.
  runtime              Manage trusted runtime signing keys.
  migrate-to-standard  Pump standalone state into a standard deployment.
```

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

Prints the merged client `sync.yaml` with per-key provenance (which scope contributed each value).

```
podium config show [--explain <key>] [--server] [--json]
```

- `--explain <key>` prints one key with its full resolution chain.
- `--server` prints the resolved server configuration (env var, `registry.yaml`, or default per value) instead of the client `sync.yaml`. API keys and DSNs are redacted.
- `--json` emits the output as JSON.

### `podium login` / `podium logout`

OAuth device-code flow against the resolved registry.

```
podium login [--registry <url>] [--no-browser] [--json]
             [--issuer <url>] [--token-url <url>]
             [--client-id <id>] [--audience <aud>] [--scopes <space-separated>]
podium logout [--registry <url>]
```

| Flag | Effect |
|:--|:--|
| `--registry <url>` | Registry URL. Resolved from the merged config when unset. |
| `--no-browser` | Skip auto-opening the verification URL. |
| `--json` | Suppress the human prompt and emit a structured `auth.device_code_pending` event on stderr. |
| `--issuer <url>` | OAuth device-authorization endpoint, overriding registry discovery. Defaults to `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`. |
| `--token-url <url>` | OAuth token endpoint. Defaults to `PODIUM_OAUTH_TOKEN_URL`; synthesized from `--issuer` when unset. |
| `--client-id <id>` | OAuth client ID. Defaults to `PODIUM_OAUTH_CLIENT_ID`, then `podium-cli`. |
| `--audience <aud>` | Audience claim for the issued token. Defaults to `PODIUM_OAUTH_AUDIENCE`. |
| `--scopes <list>` | Space-separated OAuth scopes. Default: `openid profile email groups`. |

When `--issuer` is unset, `podium login` discovers the device-authorization and token endpoints from the registry's RFC 8414 metadata at `/.well-known/oauth-authorization-server`. Setting `PODIUM_NO_BROWSER` to a truthy value (`1`, `true`, `yes`, or `on`) has the same effect as `--no-browser` for headless and CI environments. Tokens cache in the OS keychain keyed by registry URL; multiple registries can be authenticated simultaneously.

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
             [--no-embeddings] [--presign-ttl-seconds <n>]
             [--sign registry-key]
             [--web-ui] [--web-ui-allow-public-bind]
```

Each flag overrides the matching `PODIUM_*` env var for the duration of the process.

| Flag | Effect |
|:--|:--|
| `--standalone` | Single-binary mode with embedded SQLite + sqlite-vec + bundled embedding model. Defaults to bind `127.0.0.1:8080`. |
| `--strict` | Refuse to start without an explicit config (no auto-standalone fallback). Same effect as `PODIUM_NO_AUTOSTANDALONE`. |
| `--config <path>` | Override the default config file location. Overrides `PODIUM_CONFIG_FILE`. |
| `--bind <addr>` | Bind address. Overrides `PODIUM_BIND`. |
| `--layer-path <path>` | For standalone: register layers rooted at this path. The path is polymorphic. When `<path>/.registry-config` exists with `multi_layer: true` (and no top-level manifest files are present), each subdirectory becomes a `local`-source layer per the filesystem-registry layout. Otherwise the path is registered as a single `local`-source layer. Equivalent to `PODIUM_LAYER_PATH` or the `layers.path` key in `registry.yaml`; precedence is CLI flag > env var > config file. |
| `--public-mode` | Bypass authentication and visibility filtering. Mutually exclusive with an identity provider. Overrides `PODIUM_PUBLIC_MODE`. |
| `--allow-public-bind` | Allow non-loopback bind in public mode or with trusted headers (typically behind an authenticated reverse proxy). Overrides `PODIUM_ALLOW_PUBLIC_BIND`. |
| `--no-embeddings` | Disable embeddings and fall back to BM25-only search. Overrides `PODIUM_NO_EMBEDDINGS`. |
| `--presign-ttl-seconds <n>` | Presigned-URL TTL in seconds. Overrides `PODIUM_PRESIGN_TTL_SECONDS` and the `object_store.presign_ttl_seconds` key in `registry.yaml`. |
| `--sign registry-key` | Enable registry-managed-key signing on ingest. The only accepted value is `registry-key`. Overrides `PODIUM_SIGN`. |
| `--web-ui` | Mount the bundled web UI at `/ui/`. Overrides `PODIUM_WEB_UI`. |
| `--web-ui-allow-public-bind` | Allow the web UI on a non-loopback bind when an identity provider is configured. Overrides `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND`. |

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
podium lint --registry <path>
```

`--registry <path>` is required and points at a filesystem registry root. The command walks every artifact under that root, validating each `ARTIFACT.md` (plus `SKILL.md` for skills) and any `DOMAIN.md` against the type's schema. To lint a single artifact, point `--registry` at a root that resolves that artifact's canonical ID. Exits 2 when `--registry` is absent, exits 1 on lint errors, and exits 0 when the registry is clean. Pass `--offline` to skip the URL HEAD check and validate only bundled-file references.

### `podium import`

Converts a directory tree of standalone skill files (each skill in its own subdirectory with a `SKILL.md` inside) into a Podium-shaped filesystem layer where each artifact has an `ARTIFACT.md`, a `SKILL.md`, and any bundled resources. Filesystem-only; the command never modifies the source.

```
podium import --source <dir> --target <dir> [--type <type>] [--version <semver>] [--dry-run]
```

| Flag | Effect |
|:--|:--|
| `--source <dir>` | Directory of skill subdirectories. Each immediate subdirectory name becomes the artifact ID. Required. |
| `--target <dir>` | Destination layer directory. Required. |
| `--type <type>` | Artifact type written into `ARTIFACT.md`. Default: `skill`. |
| `--version <semver>` | Artifact version written into `ARTIFACT.md`. Default: `1.0.0`. |
| `--dry-run` | Report the plan; write nothing. |

---

## Sync and materialization

### `podium sync`

Materializes the user's effective view to disk via the configured harness adapter. `podium sync` is also a dispatcher: a first argument of `override` or `save-as` runs the corresponding subcommand below.

```
podium sync [--registry <url-or-path>] [--target <path>] [--harness <name>]
            [--profile <name>] [--config <path>]
            [--include <pattern>] [--exclude <pattern>] [--type <t1,t2>]
            [--overlay <path>]
            [--watch] [--dry-run] [--preview] [--check] [--json]
```

| Flag | Effect |
|:--|:--|
| `--registry <url-or-path>` | Registry server URL or filesystem path. Defaults to the merged `sync.yaml`. |
| `--target <path>` | Destination directory. Default: CWD. |
| `--harness <name>` | Override the configured harness. |
| `--profile <name>` | Use a named profile from `sync.yaml`. |
| `--config <path>` | Run one sync per entry in a `sync.yaml` `targets:` list. |
| `--include <pattern>` | Glob to include (canonical artifact IDs). Repeatable. |
| `--exclude <pattern>` | Glob to exclude. Applied after include. Repeatable. |
| `--type <t1,t2,...>` | Restrict to a comma-separated list of artifact types. |
| `--overlay <path>` | Workspace overlay path watched alongside the registry. |
| `--watch` | Long-running. Re-materialize on registry change events or fsnotify in filesystem mode. |
| `--dry-run` | Print the resolved set; write nothing. |
| `--preview` | Print the scope-preview aggregate counts and exit; write nothing. |
| `--check` | Validate the merged `sync.yaml` and report warnings (unresolved profiles, malformed globs, target/profile collisions). |
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

### `podium artifact scaffold`

Writes a new artifact directory at the given path with valid starting frontmatter for the chosen `--type`. Filesystem-only; the command does not talk to the registry. The last component of `<path>` becomes the artifact name; preceding components form the §4.2 domain hierarchy.

```
podium artifact scaffold --type <type> --description <text>
                         [--tags <a,b,c>]
                         [--sensitivity <low|medium|high>]
                         [--license <spdx>]
                         [--when-to-use <a,b,c>]
                         [--version <semver>]
                         [--extends <id>]
                         [type-specific flags]
                         [--force] [--yes]
                         <path>
```

`--type` is required; it accepts any of the spec §4.3 first-class types:

| Type | Files written | Type-specific flags |
|---|---|---|
| `skill` | ARTIFACT.md + SKILL.md (per §4.3.4 field allocation) | — |
| `agent` | ARTIFACT.md | `--input-schema`, `--output-schema`, `--delegates-to` |
| `context` | ARTIFACT.md | — |
| `command` | ARTIFACT.md | — |
| `rule` | ARTIFACT.md | `--rule-mode` (default `always`), `--rule-globs`, `--rule-description` |
| `hook` | ARTIFACT.md | `--hook-event` (required), `--hook-action` |
| `mcp-server` | ARTIFACT.md | `--server-identifier` (required) |

Extension types (anything outside the first-class enum) are accepted with a warning; the scaffolder writes a generic ARTIFACT.md and leaves the extension's bespoke fields for the author to add.

**Non-interactive example:**

```bash
podium artifact scaffold \
    --type skill \
    --description "Draft release notes from a list of ticket keys." \
    --tags "release,workflow" \
    --license MIT \
    --yes \
    finance/release/release-notes
```

This writes `finance/release/release-notes/ARTIFACT.md` and `SKILL.md` (intermediate domain directories are created). Per spec §4.3.4, `name`, `description`, and `license` live in `SKILL.md`; `ARTIFACT.md` carries Podium's structured fields and an empty-body marker.

**Conditional requirements when `--yes` is set:**

- `--description` is required for every type.
- `--rule-globs` is required when `--rule-mode glob` is set.
- `--rule-description` is required when `--rule-mode auto` is set.
- `--hook-event` is required for `--type hook`.
- `--server-identifier` is required for `--type mcp-server`.

Without `--yes`, the command prompts for missing values. `--force` overwrites an existing directory.

### `podium impact`

Lists the artifacts that depend on a given artifact, by querying the registry's reverse-dependency edges. Use it before changing or removing an artifact to see what it would affect.

```
podium impact <artifact-id> [--registry <url>]
```

`--registry` defaults to `PODIUM_REGISTRY`.

---

## Layer management

### `podium layer register`

Registers a new layer.

```
podium layer register --id <id> --repo <git-url> --ref <ref> [--root <subpath>] [--force-push-policy <tolerant|strict>]
podium layer register --id <id> --local <path>
                      [--user-defined] [--owner <oidc-sub>]
                      [--public | --organization]
                      [--group <oidc-group>]... [--user <oidc-sub-or-email>]...
```

For Git sources, the registry returns the webhook URL and HMAC secret to configure on the source repo. Without webhook configuration, the layer stays at its initial commit until the first manual reingest.

`--force-push-policy` sets the per-layer force-push handling for a Git source. The default (`tolerant`) preserves previously-ingested commits and emits a `layer.history_rewritten` event; `strict` rejects an ingest whose history was rewritten. The policy is also settable with `podium layer update --force-push-policy` and through the registry.yaml `source.git.force_push_policy` key.

Visibility flags set who can see the layer:

- `--user-defined` registers a personal layer; pair it with `--owner` to name the owning OIDC subject.
- `--public` sets public visibility; `--organization` sets organization-wide visibility.
- `--group` grants visibility to an OIDC group (repeatable).
- `--user` grants visibility to an OIDC subject or email (repeatable).

### `podium layer list`

Lists configured layers and their current state.

```
podium layer list [--deleted]
```

`--deleted` lists soft-deleted layers still recoverable within the recovery window (see `podium layer restore`).

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

### `podium layer restore`

Recovers a layer (and its artifacts) that was unregistered within the recovery window.

```
podium layer restore <id>
```

### `podium layer reingest`

Forces a re-pull of a layer's source.

```
podium layer reingest <id> [--break-glass --justification <text> --approver <id> --approver <id>]
```

During a freeze window, ingest is blocked unless `--break-glass` is passed with a justification. Break-glass requires dual-signoff, so supply two distinct approver identities with repeated `--approver` flags. A grant auto-expires after 24h and queues for post-hoc security review.

### `podium layer update`

Patches a registered layer's mutable fields. Only the flags supplied are applied; every other field keeps its prior value. At least one mutable field is required.

```
podium layer update --id <id>
                    [--ref <ref>] [--root <subpath>] [--local <path>]
                    [--force-push-policy <tolerant|strict>]
                    [--rotate-webhook-secret]
                    [--owner <oidc-sub>] [--public] [--organization]
                    [--group <oidc-group>]... [--user <oidc-sub-or-email>]...
```

`--rotate-webhook-secret` regenerates the Git layer's HMAC webhook secret and prints the new value.

### `podium layer watch`

Polls a layer's source for changes at a configured interval. Works against `local`-source layers and against `git`-source layers that do not have a webhook configured (for example, on a developer machine without a public ingress).

```
podium layer watch <id> [--interval <duration>]
```

`--interval` defaults to a sensible value per source type.

---

## Admin

Admin commands require the `admin` role on the tenant. Admin grants are recorded as `(identity, org_id, "admin")` rows; manage them via `podium admin grant` / `podium admin revoke`.

### `podium admin tenant`

Manages tenants at runtime on a multi-tenant registry. The group is authorized by the instance-operator role, which is distinct from the per-tenant `admin` role: an operator is seeded at boot through `PODIUM_OPERATOR_ADMINS` (see [CLI environment variables](#environment-variables)) and the operator authenticates as any caller does. The commands are available only when the registry runs in multi-tenant mode (`PODIUM_MULTI_TENANT`); a single-tenant or standalone registry rejects them with `registry.tenant_management_unavailable`. `--registry` is required on each command (defaults to `PODIUM_REGISTRY`).

```
podium admin tenant create <name> [--storage-bytes N] [--search-qps N] [--materialize-rate N] [--audit-volume-per-day N] [--max-user-layers N] [--expose-scope-preview true|false] --registry <url>
podium admin tenant list [--json] --registry <url>
podium admin tenant update <id> [--storage-bytes N] [--search-qps N] [--materialize-rate N] [--audit-volume-per-day N] [--max-user-layers N] [--expose-scope-preview true|false] [--active true|false] --registry <url>
podium admin tenant deactivate <id> --registry <url>
```

| Command | Effect |
|:--|:--|
| `create <name>` | Provisions a tenant, deriving the org ID from the name. Create is idempotent: re-creating an existing name returns that tenant unchanged. The quota and scope-preview flags set the tenant's initial values; an omitted flag takes the deployment default. |
| `list` | Lists every tenant. `--json` emits the wire array for scripting. |
| `update <id>` | Sends only the flags passed, so an omitted flag leaves that field unchanged. `--active true` reactivates a deactivated tenant; `--active false` deactivates it. The command cannot change the name, which is fixed at create. |
| `deactivate <id>` | Soft-deactivates the tenant. A deactivated tenant stops resolving while its data persists; `update <id> --active true` reactivates it. |

| Flag | Effect |
|:--|:--|
| `--storage-bytes N` | Per-tenant storage budget in bytes. `0` disables the budget. |
| `--search-qps N` | Per-tenant search QPS budget. `0` disables the budget. |
| `--materialize-rate N` | Per-tenant materialization rate budget. `0` disables the budget. |
| `--audit-volume-per-day N` | Per-tenant audit-volume budget per day. `0` disables the budget. |
| `--max-user-layers N` | Per-identity cap on user-defined layers. `0` selects the deployment default; a negative value disables the cap. |
| `--expose-scope-preview true\|false` | Whether the tenant exposes aggregate scope-preview counts. |
| `--active true\|false` | `update` only. Sets the tenant's active state. |

### `podium admin grant` / `podium admin revoke`

Grant or revoke the tenant `admin` role for a user. The user identity is positional; `--registry` is required (defaults to `PODIUM_REGISTRY`).

```
podium admin grant <user-id> --registry <url>
podium admin revoke <user-id> --registry <url>
```

### `podium admin show-effective`

Surfaces the effective per-layer visibility for any identity. Useful for debugging visibility issues. `--group` is repeatable and supplies OIDC group claims to evaluate; `--registry` is required.

```
podium admin show-effective <user-id> [--group <g>]... --registry <url>
```

### `podium admin reembed`

Regenerates embeddings. Triggered automatically when the configured embedding model changes; this command is for ad-hoc re-embeds. `--registry` is required (defaults to `PODIUM_REGISTRY`).

```
podium admin reembed [--artifact <id> --version <semver>]
                     [--only-missing] [--since <rfc3339>]
                     --registry <url>
```

| Flag | Effect |
|:--|:--|
| `--artifact <id>` | Re-embed one specific artifact. Requires `--version`. |
| `--version <semver>` | The version to re-embed; required with `--artifact`. |
| `--only-missing` | Skip artifacts that already have a vector. Scopes a tenant-wide pass. |
| `--since <rfc3339>` | Re-embed only artifacts ingested at or after this RFC3339 timestamp. Scopes a tenant-wide pass. |

With no `--artifact`, the command runs a tenant-wide pass; `--only-missing` and `--since` compose to scope it.

### `podium admin migrate-to-standard`

Pumps a standalone deployment's state (SQLite metadata plus the filesystem object store) into a standard deployment (Postgres plus S3). The source flags default to the standalone layout under `~/.podium`, so the short form runs verbatim on a standalone host. The granular `--target-*` flags remain available for advanced S3 configuration.

```
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
                                 [--source-sqlite <path>] [--source-objects <path>]
                                 [--source-audit-log <path>] [--target-audit-log <path>]
                                 [--dry-run]
```

| Flag | Effect |
|:--|:--|
| `--postgres <dsn>` | Target Postgres DSN. Implies `--target-store=postgres`. |
| `--object-store <url>` | Target object store. Either `file:///path` (filesystem) or `s3://[key:secret@]endpoint/bucket[?region=R&ssl=false]` (S3). |
| `--source-sqlite <path>` | Source SQLite path. Default: `~/.podium/standalone/podium.db`. |
| `--source-objects <path>` | Source filesystem object store path. Default: `~/.podium/standalone/objects`. |
| `--source-audit-log <path>` | Source audit log file. Default: `~/.podium/audit.log`. |
| `--target-audit-log <path>` | Target audit log file. The audit history is copied only when this is set; otherwise the command warns that it was not copied. |
| `--dry-run` | Report the source plan (tenant, manifest, layer-config, and admin-grant counts); migrate nothing. |

Manifests, layer configs, admin grants, and content blobs are copied. Dependency edges are regenerated by the next ingest. Granular target overrides (`--target-store`, `--target-postgres-dsn`, `--target-sqlite`, `--target-objects`, `--target-objects-type`, and the `--target-s3-*` family) are available for non-default destinations.

### Verifying integrity

There is no `podium admin verify` command. Artifact signature verification is the top-level `podium verify <artifact>`. Audit-chain integrity is verified automatically by the registry on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule.

### SCIM provisioning

There is no SCIM sync command. SCIM is a server-side push from the identity provider to `/scim/v2/`; the IdP sends group and membership updates.

### `podium admin erase`

GDPR right-to-erasure. The user identity is positional and `--salt` is required (an empty salt yields a guessable tombstone). The default form calls the registry, which unregisters and purges the user's owned layers and redacts the registry audit stream; the authenticated session identifies the invoking admin.

```
podium admin erase <user-id> --salt <salt> --registry <url>
podium admin erase <user-id> --salt <salt> --local --operator <admin-id> [--audit-path <path>]
```

| Mode | Effect |
|:--|:--|
| Registry (default) | Calls `/v1/admin/erase`. Requires `--registry` (defaults to `PODIUM_REGISTRY`). Purges owned layers and redacts the registry audit stream. |
| Local (`--local` or `--audit-path`) | Redacts the local MCP audit log directly (default `~/.podium/audit.log`). Requires `--operator` to record the invoking admin. |

Redaction replaces `sub` with `redacted-<sha256(sub+salt)>` and preserves audit event sequencing. Erasure is itself logged as a `user.erased` event.

---

## Signing

### `podium sign`

Explicit signing outside the ingest flow. The `<artifact>` form resolves the artifact's canonical content hash through the registry, then signs it. The `--content-hash` form signs a raw hash without resolving an artifact. Pass exactly one of the two.

```
podium sign <artifact> [--registry <url>] [--provider <name>]
podium sign --content-hash sha256:<hex> [--provider <name>]
```

| Flag | Effect |
|:--|:--|
| `--registry <url>` | Registry URL used to resolve the `<artifact>` form. Defaults to `PODIUM_REGISTRY`. |
| `--content-hash sha256:<hex>` | Sign this content hash directly, instead of resolving an artifact. |
| `--provider <name>` | Signature provider: `noop`, `registry-managed`, or `sigstore-keyless`. Defaults to `PODIUM_SIGNATURE_PROVIDER`, then `noop`. |

The `registry-managed` provider uses a per-org key managed by the registry. The `sigstore-keyless` provider produces an OIDC-attested signature with a transparency-log entry, configured through the `PODIUM_SIGSTORE_*` env vars.

### `podium verify`

Ad-hoc signature verification. The `<artifact>` form resolves the artifact's content hash and stored signature through the registry; an explicit `--signature` overrides the stored envelope. The `--content-hash` plus `--signature` form verifies an explicit pair. Exits 0 on a valid signature and 1 on a mismatch or other error.

```
podium verify <artifact> [--registry <url>] [--provider <name>] [--signature <envelope>]
podium verify --content-hash sha256:<hex> --signature <envelope> [--provider <name>]
```

| Flag | Effect |
|:--|:--|
| `--registry <url>` | Registry URL used to resolve the `<artifact>` form. Defaults to `PODIUM_REGISTRY`. |
| `--content-hash sha256:<hex>` | Verify against this content hash directly, instead of resolving an artifact. |
| `--signature <envelope>` | Signature envelope to verify. Pairs with `--content-hash`; overrides the stored signature in the `<artifact>` form. |
| `--provider <name>` | Signature provider: `noop`, `registry-managed`, or `sigstore-keyless`. Defaults to `PODIUM_SIGNATURE_PROVIDER`, then `noop`. |

The MCP server verifies signatures automatically on materialization for sensitivity at or above medium (configurable per deployment).

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
| `PODIUM_VERIFY_SIGNATURES` | `never`, `medium-and-above` (default), `always`. |
| `PODIUM_IDENTITY_PROVIDER` | `oauth-device-code` (default), `injected-session-token`. |
| `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` | OAuth provider config. |
| `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE` | Injected-token sources. |
| `PODIUM_PUBLIC_MODE` | Equivalent of `--public-mode`. |
| `PODIUM_NO_AUTOSTANDALONE` | Disable zero-flag standalone fallback. |
| `PODIUM_MULTI_TENANT` | Registry-process boot setting. When `true`, the registry runs in multi-tenant mode and routes each request to the tenant its organization names; the `podium admin tenant` commands and the `/v1/admin/tenants` endpoints are available. When unset, every request binds to the single `default` org and tenant management is rejected. |
| `PODIUM_OPERATOR_ADMINS` | Registry-process boot setting. Comma-separated identities granted the instance-operator role at boot. The operator role authorizes the `podium admin tenant` commands and the `/v1/admin/tenants` endpoints; it confers no per-tenant `admin` rights. Distinct from `PODIUM_BOOTSTRAP_ADMINS`, which seeds per-tenant `admin` grants. |

Server-side backend selection variables (`PODIUM_VECTOR_BACKEND`, `PODIUM_EMBEDDING_PROVIDER`, etc.) are documented alongside the corresponding backend in [Extending](../deployment/extending).
