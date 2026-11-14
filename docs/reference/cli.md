---
title: CLI
nav_order: 1
description: "Every podium subcommand with its usage, flags, defaults, and exit codes: setup, server, sync, layer management, search, admin, and signing."
---

# CLI

This page lists every `podium` subcommand, grouped by purpose, with its usage, flags, defaults, and exit behavior. It is a lookup reference. For task-oriented guides, see [Quickstart](../getting-started/quickstart), [Authoring](../authoring/), [Consuming](../consuming/), and [Deployment](../deployment/).

The `podium` CLI is a single binary.

## Top-level flags

| Flag | Effect |
|:--|:--|
| `podium --help`, `-h`, or `podium help` | Print the command list. |
| `podium --version`, `-v`, or `podium version` | Print the build version. |

## Subcommand help

Every subcommand and subcommand group accepts `--help` and the short form `-h`. A subcommand group also accepts the bare `help` token. A leaf subcommand prints a one-line description followed by its flag list:

```
$ podium lint --help
podium lint - Validate manifests in a filesystem-source registry.

Flags:
  -offline
        skip the §4.4 URL HEAD check (validate bundled files only)
  -registry string
        filesystem registry path (required)
```

A dispatcher group prints its subcommand list. The dispatcher groups are `admin`, `cache`, `config`, `domain`, `artifact`, `layer`, `profile`, `admin runtime`, `admin signing-key`, and `admin tenant`. `podium sync` dispatches the `override` and `save-as` subcommands when one of them is the first argument, and otherwise runs materialization directly:

```
$ podium admin --help
podium admin - Administer the registry: grants, audit, runtime keys, migration.

Subcommands:
  grant                Grant tenant admin role to a user.
  revoke               Revoke tenant admin role from a user.
  show-effective       Print the per-layer visibility for a user identity.
  erase                GDPR right-to-erasure: purge a user's layers and redact their audit identity.
  retention            Apply audit retention policies to the local audit log.
  reembed              Re-run vector embeddings against the configured registry.
  runtime              Manage trusted runtime signing keys.
  tenant               Manage tenants (operator role).
  migrate-to-standard  Pump standalone state into a standard deployment.
  sign-stored-rows     Rewrite stored rows under the §13.4 rules, signing under the registry signing key when signing is on.
  signing-key          Generate or rotate the registry signing key file.
```

---

## Setup and config

### `podium init`

Writes a `sync.yaml` file for client-side configuration.

```
podium init [--global | --local]
            [--registry <url-or-path>]
            [--harness <name>]
            [--target <path>]
            [--standalone]
            [--force]
```

The scope flag selects the file the command writes:

| Scope flag | Path |
|:--|:--|
| (default) | `<workspace>/.podium/sync.yaml` (committed). |
| `--global` | `~/.podium/sync.yaml`. |
| `--local` | `<workspace>/.podium/sync.local.yaml` (gitignored). |

The value flags set the file's contents:

| Flag | Effect |
|:--|:--|
| `--registry <url-or-path>` | Server URL (HTTP) or filesystem path. |
| `--harness <name>` | Harness adapter: `none`, `claude-code`, `claude-desktop`, `claude-cowork`, `cursor`, `codex`, `gemini`, `opencode`, `pi`, or `hermes`. [Configure your harness](../consuming/configure-your-harness#supported-harnesses) lists each harness with documentation links. |
| `--target <path>` | Destination for materialization. |
| `--standalone` | Shortcut for `--registry http://127.0.0.1:8080`. |
| `--force` | Overwrite an existing file. |

Notes:

- Workspace mode walks up from the current directory to find `.podium/`. When none exists, the command creates one in the current directory.
- The command adds `.podium/sync.local.yaml` and `.podium/overlay/` to `.gitignore` when they are not already present.

### `podium config show`

Prints the merged client `sync.yaml` with per-key provenance, which names the scope that contributed each value.

```
podium config show [--explain <key>] [--server] [--json]
```

| Flag | Effect |
|:--|:--|
| `--explain <key>` | Print one key with its full resolution chain. |
| `--server` | Print the resolved server configuration instead of the client `sync.yaml`. Each value shows its source: an environment variable, `registry.yaml`, or the default. API keys and DSNs are redacted. |
| `--json` | Emit the output as JSON. |

### `podium login` / `podium logout`

`podium login` runs the OAuth device-code flow against the resolved registry and caches the token. `podium logout` removes the cached token.

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
| `--token-url <url>` | OAuth token endpoint. Defaults to `PODIUM_OAUTH_TOKEN_URL`. When both are unset, the command synthesizes the endpoint from `--issuer`. |
| `--client-id <id>` | OAuth client ID. Defaults to `PODIUM_OAUTH_CLIENT_ID`, then `podium-cli`. |
| `--audience <aud>` | Audience claim for the issued token. Defaults to `PODIUM_OAUTH_AUDIENCE`. |
| `--scopes <list>` | Space-separated OAuth scopes. Default: `openid profile email groups`. |

Notes:

- When `--issuer` is unset, `podium login` probes the resolved registry URL for RFC 8414 authorization-server metadata at `/.well-known/oauth-authorization-server`. It reads the device-authorization and token endpoints from that document.
- The registry process does not serve that document itself. Discovery succeeds only when a fronting proxy or gateway publishes it. Otherwise, pass `--issuer` or set `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`.
- Setting `PODIUM_NO_BROWSER` to a truthy value (`1`, `true`, `yes`, or `on`) has the same effect as `--no-browser`, for headless and CI environments.
- Tokens are cached in the OS keychain, keyed by registry URL, so several registries can be authenticated at the same time.
- `podium login` is a no-op when the resolved registry is a filesystem path or one of the loopback defaults, `http://127.0.0.1:8080` or `http://localhost:8080`. It reports that the registry needs no authentication and exits.
- A single-node server published at any other URL runs the full device-code flow, so a deployment that configures `oidc-jwt` authenticates the CLI through this command.
- Where the registry enables the browser flow, the registry signs a browser in through its own authorization-code exchange instead.

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
             [--sign registry-key|none]
             [--web-ui] [--web-ui-allow-public-bind]
             [--web-ui-auth] [--web-ui-auth-transaction-ttl <duration>]
```

Each flag overrides the matching `PODIUM_*` environment variable for the lifetime of the process.

| Flag | Effect |
|:--|:--|
| `--standalone` | Run the single-binary configuration with embedded SQLite and sqlite-vec. Binds `127.0.0.1:8080` by default. The embedding provider defaults to `ollama` at `http://localhost:11434`. No model ships in the binary, so search runs BM25 over manifest text whenever that endpoint is unreachable. |
| `--strict` | Refuse to start without an explicit config, with no automatic standalone fallback. Same effect as `PODIUM_NO_AUTOSTANDALONE`. |
| `--config <path>` | Config file location. Overrides `PODIUM_CONFIG_FILE`. |
| `--bind <addr>` | Bind address. Overrides `PODIUM_BIND`. |
| `--layer-path <path>` | Standalone: register layers rooted at this path, as described in the notes. Equivalent to `PODIUM_LAYER_PATH` and to the `layer_path` key under the top-level `registry:` mapping in `registry.yaml`. Precedence: CLI flag, then environment variable, then config file. |
| `--public-mode` | Bypass authentication and visibility filtering. Mutually exclusive with an identity provider. Overrides `PODIUM_PUBLIC_MODE`. |
| `--allow-public-bind` | Allow a non-loopback bind in public mode or with trusted headers, typically behind an authenticated reverse proxy. Overrides `PODIUM_ALLOW_PUBLIC_BIND`. |
| `--no-embeddings` | Disable embeddings and fall back to BM25-only search. Overrides `PODIUM_NO_EMBEDDINGS`. |
| `--presign-ttl-seconds <n>` | Presigned-URL TTL in seconds. Overrides `PODIUM_PRESIGN_TTL_SECONDS` and the `object_store.presign_ttl_seconds` key in `registry.yaml`. |
| `--sign <mode>` | Ingest signing mode: `registry-key` (the default) or `none`. Overrides `PODIUM_SIGN`. The notes describe each mode. |
| `--web-ui` | Mount the bundled web UI at `/app/`, and redirect `GET /` to it. Overrides `PODIUM_WEB_UI`. |
| `--web-ui-allow-public-bind` | Allow the web UI on a non-loopback bind when an identity provider is configured. A UI reachable beyond the loopback interface is then served only by a registry that resolves each caller's identity and filters what it serves by that identity. Overrides `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND`. |
| `--web-ui-auth` | Sign the browser in through the registry with the OAuth authorization-code flow. Overrides `PODIUM_WEB_UI_AUTH`. The notes list its requirements. |
| `--web-ui-auth-transaction-ttl <duration>` | Sign-in window as a Go duration, `10m` by default. It is the `Max-Age` of the pre-authorization cookie the sign-in route sets. Overrides `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL`. |

Notes:

- Zero-flag `podium serve` enters the standalone configuration automatically when no config is found at `~/.podium/registry.yaml`. Disable the fallback with `PODIUM_NO_AUTOSTANDALONE=1` or `--strict`.
- `--layer-path` is polymorphic. When `<path>/.registry-config` exists with `multi_layer: true` and no top-level manifest files are present, each subdirectory becomes a `local`-source layer per the filesystem-registry layout. Otherwise the path is registered as a single `local`-source layer.
- `--sign registry-key` signs each accepted manifest with the registry-managed key at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`). The registry generates the keypair on first run.
- `--sign none` turns ingest signing off and generates no key file. A `podium-mcp` consumer or a server-source `podium sync` on that machine then sets `PODIUM_VERIFY_SIGNATURES=never`. Without that setting, on a home that carries no key file, the bridge refuses to start and the sync exits with status 2, both with `config.signature_provider_unavailable`.
- `--web-ui-auth` requires `--web-ui`, `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, public mode off, and the browser-flow acquisition values that [Configuration](configuration#web-ui) lists. `PODIUM_WEB_UI_REDIRECT_URI` must be an `https` URL or an `http` URL whose host is a loopback address. A configuration that fails one of these conditions aborts startup with `config.web_ui_auth_unconfigured`. The [error-code catalog](error-codes) states the whole guard.

### `podium status`

Prints a diagnostic summary of the client setup.

```
podium status [--registry <url>]
```

| Flag | Effect |
|:--|:--|
| `--registry <url>` | Registry for this run. When unset, the registry resolves from `PODIUM_REGISTRY` and then the merged `sync.yaml`. |

The summary covers the resolved registry, harness, cache directory, cache mode, overlay path, identity provider, masked session token, and tenant. For a server-source registry, the command also probes `/healthz` and prints the following:

- reachability;
- the registry mode the health response reports;
- the scope-preview aggregate counts;
- whether a keychain token is present.

---

## Authoring & validation

### `podium lint`

Validates manifests against their type's schema and runs the type-specific rules. It runs the same checks the registry runs at ingest, so it fits in a CI pipeline.

```
podium lint --registry <path>
```

| Flag | Effect |
|:--|:--|
| `--registry <path>` | Filesystem registry root. Required. |
| `--offline` | Skip the URL HEAD check and validate only bundled-file references. |

The command walks every artifact under the root and validates each `SKILL.md`, each `ARTIFACT.md`, and any `DOMAIN.md` against the type's schema. To lint a single artifact, point `--registry` at a root that resolves that artifact's canonical ID.

| Exit code | Meaning |
|:--|:--|
| `0` | The registry is clean. |
| `1` | Lint errors, or an `ARTIFACT.md` that declares `extends:`. |
| `2` | `--registry` is absent. |

An `ARTIFACT.md` that declares `extends:` is rejected. The command prints a `rejected: <id> (ingest.invalid_artifact): <reason>` line on standard error for each one, lints the remaining artifacts, and exits 1.

### `podium import`

Normalizes a directory of skills into Agent Skills directories. The command is optional, because a directory of skill directories is already a valid layer.

```
podium import --source <dir> --target <dir> [--version <semver>] [--dry-run]
```

| Flag | Effect |
|:--|:--|
| `--source <dir>` | Directory of skill subdirectories. Each immediate subdirectory name becomes the artifact ID. Required. |
| `--target <dir>` | Destination layer directory. Required. |
| `--version <semver>` | Version written to `SKILL.md` `metadata.version`. Default: `1.0.0`. |
| `--dry-run` | Report the plan; write nothing. |

The command produces the following layout:

- Each immediate subdirectory of `--source` becomes a directory under `--target` with the same name.
- That directory holds a `SKILL.md` and every other file as a bundled resource. The `SKILL.md` is taken from the source `SKILL.md` or, when there is none, from the single root-level `.md` file.
- A subdirectory with no root-level `.md` file is skipped.
- `--version` is written to `SKILL.md` `metadata.version`.
- The command writes no `ARTIFACT.md` and never modifies the source.

The command exits 1 and names the directory in these cases:

- A source directory holds an `ARTIFACT.md`.
- An existing `SKILL.md` carries a `metadata.version` different from `--version`.
- The chosen file has no frontmatter block.
- The root holds several `.md` files and no `SKILL.md`.

`--dry-run` runs the same checks before it reports the plan.

---

## Sync and materialization

### `podium sync`

Materializes the caller's effective view to disk through the configured harness adapter. `podium sync` is also a dispatcher: a first argument of `override` or `save-as` runs the matching subcommand below.

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
| `--target <path>` | Destination directory. Default: the current directory. |
| `--harness <name>` | Override the configured harness. |
| `--profile <name>` | Use a named profile from `sync.yaml`. |
| `--config <path>` | Run one sync per entry in a `sync.yaml` `targets:` list. Each entry is `kind: workspace` (the default, a project-files layout) or `kind: marketplace` (a git-repo distribution Podium renders). A marketplace target's `workflow` supplies the operator commands that clone and push. |
| `--include <pattern>` | Glob to include, matched against canonical artifact IDs. Repeatable. |
| `--exclude <pattern>` | Glob to exclude. Applied after include. Repeatable. |
| `--type <t1,t2,...>` | Restrict to a comma-separated list of artifact types. |
| `--overlay <path>` | Workspace overlay path watched alongside the registry. |
| `--watch` | Run continuously. Re-materialize on registry change events, or on fsnotify events against a filesystem source. |
| `--dry-run` | Print the resolved set; write nothing. |
| `--preview` | Print the scope-preview aggregate counts and exit; write nothing. Requires a server-source registry, because the counts come from `GET /v1/scope/preview`. |
| `--check` | Validate the merged `sync.yaml` and report warnings: unresolved profiles, malformed globs, and target/profile collisions. With `--config`, validate every target in the named file and materialize none. |
| `--json` | Structured envelope output, for piping to `jq`. |

The lock file is written to `<target>/.podium/sync.lock`.

#### Exit status

| Status | Cause |
|:--|:--|
| `1` | The run dropped an artifact (see [Dropped artifacts](#dropped-artifacts)), a delivery check failed on a one-shot sync, or a `--config` target failed. Under `--watch`, the command exits 1 on interrupt when any cycle it reported failed or dropped an artifact. |
| `2` | The signature policy or the verification key set did not resolve (see [Delivery verification](#delivery-verification)). |

A drop adds a cause for exit status 1 and leaves the command's other exit statuses unchanged.

#### Config targets

`--config` runs each target in the named file's `targets:` list in turn.

- Combined with `--watch`, `--config` starts no watch loop. Each `kind: workspace` target syncs once and the command exits.
- A `kind: marketplace` target under `--watch` fails the run with `config.invalid` before any target renders.
- A `kind: marketplace` target renders the harness-native git-repo distribution into its `target` directory through the fixed `prepare`, `render`, `publish` pipeline. Podium owns the `render` phase. The target's `workflow` supplies the `prepare` and `publish` commands, which clone the repository into the working directory and push the rendered result to the remote.
- The marketplace fields are reachable only through the `--config` path: the git remote and branch, the harness set, the commit message, the plugins, and the publishing identity.

See [Publishing marketplace repositories](../deployment/content/publishing) for the model and the worked examples.

#### Dropped artifacts

Against a local catalog, `podium sync` drops two kinds of artifact and reports each one on standard error, in the format `podium layer reingest` uses:

| Cause | Artifact dropped | Report line |
|:--|:--|:--|
| An `ARTIFACT.md` declares `extends:` | The declaring artifact. | `rejected: <id> (ingest.invalid_artifact): <reason>` |
| Two registry layers contribute the same ID (a collision) | The higher-precedence copy. | `rejected: <id> (ingest.collision): <reason>` |

The rules below apply to both kinds of drop:

- The command materializes every other artifact, writes the lock file without the dropped artifact, and exits 1.
- `--dry-run` reports the drop and exits 1 without writing.
- An `extends:` drop happens before the collision rule. A dropped artifact collides with no other artifact, so the other copy of its ID is materialized and no `ingest.collision` line is printed for it.
- Under `--config`, a target that dropped an artifact counts as a failed target, and the command exits 1. This includes a `kind: workspace` target under `--check`. A target's workflow still runs before the target is marked failed.
- `--check` without `--config` validates `sync.yaml` only and reports no drop. A `kind: marketplace` target under `--check` renders nothing.
- Under `--json`, the report stays on standard error.
- Under `--watch`, each cycle prints its report. A cycle still running when the interrupt arrives can finish without a report, and the next `podium sync` reports its drops.

A sync against a server reports no drop, because the server rejected the artifact at ingest.

#### Delivery verification

Against a server source, `podium sync` runs the delivery check on every record it loads:

- It recomputes `delivery_hash` from the bytes it received.
- It checks every fetched manifest document and bundled-resource body against its `content_hash`.
- It applies its signature policy to `delivery_signature`.

[Verifying a response](http-api#verifying-a-response) states the procedure. A filesystem-source sync runs no delivery check and needs no key material.

**Policy and key resolution.** Sync resolves its signature policy from `PODIUM_VERIFY_SIGNATURES`, then `defaults.verify_signatures` in `sync.yaml`, then the default `always`. It resolves its verification key set from `PODIUM_SIGNATURE_VERIFY_KEY`, and otherwise from the `public:` and `verify:` lines of the key file at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`). A variable set to the empty string counts as unset.

Sync verifies with the registry-managed verifier. It does not read `PODIUM_SIGNATURE_PROVIDER`, which `podium sign`, `podium verify`, and `podium-mcp` read.

| Registry | Setup the sync needs |
|:--|:--|
| A standalone registry on the same machine | None. Sync resolves the registry's key file. |
| Any other registry | Set `PODIUM_SIGNATURE_VERIFY_KEY`. |
| A registry with signing off | Set `PODIUM_VERIFY_SIGNATURES=never`. |

**When resolution happens.** An invocation resolves the policy and the verification key set at most once, immediately before its first request to a server source for the caller's effective view. It resolves them whether or not that view lists any artifact. An invocation that sends no such request resolves nothing and reads no signing variable. `podium sync --preview` is one such invocation: it requests the scope preview, which returns aggregate counts and no artifact of the view.

**Resolution failure.** Sync refuses to continue when the policy is `always` and no usable key resolves, which the message names as `config.signature_provider_unavailable`. It also refuses when the policy value is neither `never` nor `always`. In both cases sync sends no request to the registry, prints `error: <message>`, and exits with status 2. What the invocation completed before that point stands:

| Run | Effect of the refusal |
|:--|:--|
| `--config` | Stops at the target whose load resolved, after the earlier targets have run and after any operator `prepare` phase that target runs before its load. Prints `target <id>: error: <message>`. |
| `podium sync override` | Keeps the toggle change it recorded in the lock. |
| `--watch` | Exits without subscribing to change events. |

**Check failure.** A record that fails the check fails that target's materialization with `materialize.content_hash_mismatch`, `materialize.signature_invalid`, or `materialize.signature_missing`. Sync writes no file and no lock for the target.

| Run | Effect of a failed check |
|:--|:--|
| One-shot sync | Exits with status 1. |
| `--config` | Counts the target as failed and continues with the remaining targets. |
| `--watch` | Reports the failed cycle and keeps watching. Exits with status 1 on interrupt. |
| `podium sync override` | Records its toggle change in the lock before it materializes, so a refused record leaves that toggle change in the lock. It writes no artifact file and no other lock change. |

**Routes that resolve.** The routes below resolve the policy and key set against a server source. A route that does not resolve reads no signing variable.

| Route (server-source registry) | Resolves |
|:--|:--|
| `podium sync`, one-shot or `--dry-run`, cache mode other than `offline-only` | Yes |
| `podium sync`, cache mode `offline-only` | No; the run fails with `network.offline_cache_miss` |
| `podium sync --preview` | No |
| `podium sync --check` without `--config` | No |
| `podium sync --watch`, cache mode other than `offline-only` | Yes, on the first cycle; a refusal opens no event stream |
| `podium sync --watch`, cache mode `offline-only` | No; each cycle fails with `network.offline_cache_miss` and the event stream opens |
| `--config`, `kind: workspace` target, cache mode other than `offline-only` (`--check` and `--dry-run` included) | Yes |
| `--config`, `kind: workspace` target, `offline-only` | No |
| `--config`, `kind: marketplace` target, live or `--dry-run`, any cache mode | Yes |
| `--config`, `kind: marketplace` target, `--check` | No |
| Batch `podium sync override` without `--dry-run`, with a registry, any cache mode | Yes, when it re-materializes, after it writes the lock |
| Batch `podium sync override --dry-run` | No |
| Interactive `podium sync override`, with or without `--dry-run`, any cache mode | Yes, before the checklist |
| Any route against a filesystem source, `podium sync save-as`, `podium profile edit`, or `podium config show` | No |

### `podium sync override`

Toggles artifacts on or off in a materialized target without editing `sync.yaml`. Toggles persist across watcher events and clear on the next manual `podium sync`.

```
podium sync override                              # TUI checklist
podium sync override --add <id>                   # repeatable
podium sync override --remove <id>                # repeatable
podium sync override --reset                      # clear all toggles
podium sync override --add <id> --dry-run
```

| Flag | Effect |
|:--|:--|
| `--add <id>` | Artifact ID to materialize on top of the profile. Repeatable. |
| `--remove <id>` | Artifact ID to drop from the profile. Repeatable. |
| `--reset` | Clear all toggles. |
| `--dry-run` | Resolve and report. Writes nothing, runs no re-materialization, and reports no drop. |
| `--target <path>` | Materialized directory. Default: the current directory. |
| `--registry <url-or-path>` | Registry for the toggle's materialization. When unset: `PODIUM_REGISTRY`, then the merged `sync.yaml`. |
| `--harness <name>` | Adapter for the toggle's materialization. When unset: `PODIUM_HARNESS`, then the harness recorded in the target's lock file, then the merged `sync.yaml`, then the built-in `none` adapter. |

With no flags, the command opens an interactive checklist.

When a registry is configured, override re-materializes the target through the same composition as `podium sync`. Against a local catalog, a collision between two registry layers or an `ARTIFACT.md` that declares `extends:` drops the artifact. The command then prints the same `rejected: <id> (ingest.collision): <reason>` or `rejected: <id> (ingest.invalid_artifact): <reason>` line for each dropped artifact, keeps the recorded toggles, and exits 1.

### `podium sync save-as`

Captures the current materialized set as a YAML profile in `sync.yaml`.

```
podium sync save-as --profile <name> [--target <path>] [--update] [--dry-run]
```

| Flag | Effect |
|:--|:--|
| `--profile <name>` | Profile name. Required. |
| `--target <path>` | Materialized directory. Default: the current directory. |
| `--update` | Overwrite an existing profile. |
| `--dry-run` | Print the proposed YAML diff and write nothing. |

After `save-as` succeeds, the command clears the lock file's toggles.

### `podium profile edit`

Makes permanent edits to a profile in `sync.yaml`. `podium sync override` makes ephemeral changes instead.

```
podium profile edit <name>                                # TUI for the named profile
podium profile edit <name> --add-include <pattern>
podium profile edit <name> --remove-include <pattern>
podium profile edit <name> --add-exclude <pattern>
podium profile edit <name> --remove-exclude <pattern>
podium profile edit <name> --add-include <pattern> --dry-run
podium profile edit <name> --target <path> --add-include <pattern>
```

| Flag or argument | Effect |
|:--|:--|
| `<name>` | Profile name. Required. With no name, the command exits 2 and asks for one. |
| `--add-include <pattern>` | Include pattern to add. Repeatable. |
| `--remove-include <pattern>` | Include pattern to remove. Repeatable. |
| `--add-exclude <pattern>` | Exclude pattern to add. Repeatable. |
| `--remove-exclude <pattern>` | Exclude pattern to remove. Repeatable. |
| `--dry-run` | Print the proposed YAML diff and write nothing. |
| `--target <path>` | Directory holding `.podium/sync.yaml`. Default: the current directory. |

Passing none of the add and remove flags opens the interactive editor. The command modifies `sync.yaml` in place and preserves formatting and comments around untouched keys.

---

## Read CLI

The read CLI maps one-to-one to the SDK's read operations. It uses the same identity, cache, layer composition, and server-side visibility filtering.

Each command that queries the registry takes `--registry <url>`, which defaults to `PODIUM_REGISTRY` and is required. These commands are `search`, `domain show`, `domain search`, `domain analyze`, `artifact show`, and `impact`. `artifact scaffold` works on the filesystem only and takes no `--registry`.

### `podium search`

Runs a hybrid search over artifacts.

```
podium search <query> [--type <t>] [--tags <tag1,tag2>]
                      [--scope <path>] [--top-k <n>]
                      [--json]
```

| Flag | Effect |
|:--|:--|
| `--type <t>` | Filter by artifact type. |
| `--tags <tag1,tag2>` | Comma-separated tag filter. |
| `--scope <path>` | Constrain results to a path prefix. |
| `--top-k <n>` | Maximum number of results. Default: `10`. |
| `--json` | JSON output. See [JSON output](#json-output). |

### `podium domain show`

Prints the domain map for a path, or for the root when no path is given.

```
podium domain show [<path>] [--json]
```

### `podium domain search`

Runs a hybrid search over domains.

```
podium domain search <query> [--scope <path>] [--top-k <n>] [--json]
```

| Flag | Effect |
|:--|:--|
| `--scope <path>` | Constrain results to a path. |
| `--top-k <n>` | Maximum number of results. Default: `10`. |
| `--json` | JSON output. See [JSON output](#json-output). |

### `podium domain analyze`

Renders a quality report for a domain subtree. It is an operator command. The report covers sparsity per node, pass-through chains, and candidates for a split (high artifact count and high tag-cluster entropy) or a fold (low artifact count).

```
podium domain analyze [<path>] [--path <path>]
```

| Flag or argument | Effect |
|:--|:--|
| `<path>` | Subtree to analyze. An empty path analyzes the root. |
| `--path <path>` | Alternative to the positional argument. It wins over the positional argument when both are given. |

### `podium artifact show`

Prints an artifact's manifest body and frontmatter to standard output. It does **not** materialize bundled resources.

```
podium artifact show <id> [--version <semver>]
                          [--session-id <uuid>]
                          [--json]
```

| Flag | Effect |
|:--|:--|
| `--version <semver>` | Version to show. Default: the latest version. |
| `--session-id <uuid>` | Session ID, for consistent resolution of `latest`. |
| `--json` | JSON output. See [JSON output](#json-output). |

To write an artifact's files to disk, use `podium sync --include <id>`.

### `podium artifact scaffold`

Writes a new artifact directory at `<path>` with valid starting frontmatter for the chosen `--type`. The command works on the filesystem only and does not contact the registry. The last component of `<path>` becomes the artifact name, and the preceding components form the [domain hierarchy](../authoring/domains).

```
podium artifact scaffold --type <type> --description <text>
                         [--tags <a,b,c>]
                         [--sensitivity <low|medium|high>]
                         [--license <spdx>]
                         [--when-to-use <a,b,c>]
                         [--version <semver>]
                         [type-specific flags]
                         [--force] [--yes]
                         <path>
```

| Flag | Effect |
|:--|:--|
| `--type <type>` | Artifact type. Required. The type table below lists the accepted values. |
| `--description <text>` | One-line description. Required; without `--yes`, the command prompts for it. |
| `--tags <a,b,c>` | Comma-separated tags. Refused for `--type skill`. |
| `--sensitivity <low\|medium\|high>` | Sensitivity. Default: `low` for types other than `skill`. An explicitly passed value is refused for `--type skill`. |
| `--license <spdx>` | SPDX license identifier. |
| `--when-to-use <a,b,c>` | Comma-separated `when_to_use` entries. Refused for `--type skill`. |
| `--version <semver>` | Starting version. Default: `0.1.0`. |
| `--force` | Overwrite an existing directory. |
| `--yes` | Fail instead of prompting when a required value is missing. |

For `--type skill`, the command refuses `--tags`, `--when-to-use`, and an explicitly passed `--sensitivity` with exit 2. These are Podium-only fields that belong in a hand-written `ARTIFACT.md` overlay (see the [frontmatter reference](../authoring/frontmatter-reference)).

`--type` accepts the first-class artifact types and `mcp-server`, the extension type Podium ships built-in:

| Type | Files written | Type-specific flags |
|---|---|---|
| `skill` | `SKILL.md` (with `metadata.version`) | — |
| `agent` | ARTIFACT.md | `--input-schema`, `--output-schema`, `--delegates-to` |
| `context` | ARTIFACT.md | — |
| `command` | ARTIFACT.md | — |
| `rule` | ARTIFACT.md | `--rule-mode` (default `always`), `--rule-globs`, `--rule-description` |
| `hook` | ARTIFACT.md | `--hook-event` (required), `--hook-action` |
| `mcp-server` | ARTIFACT.md | `--server-identifier` (required) |

| Type-specific flag | Effect |
|:--|:--|
| `--input-schema <path>` | `agent`: relative path to the input JSON Schema. |
| `--output-schema <path>` | `agent`: relative path to the output JSON Schema. |
| `--delegates-to <ids>` | `agent`: comma-separated delegation targets. |
| `--rule-mode <mode>` | `rule`: `always` (default), `glob`, `auto`, or `explicit`. |
| `--rule-globs <globs>` | `rule`: glob patterns. Required when `--rule-mode glob` is set. |
| `--rule-description <text>` | `rule`: description. Required when `--rule-mode auto` is set. |
| `--hook-event <event>` | `hook`: canonical event name. Required. |
| `--hook-action <snippet>` | `hook`: shell snippet body. |
| `--server-identifier <id>` | `mcp-server`: canonical server identifier. Required. |

The command accepts an extension type (any type outside the first-class enum) with a warning. It writes a generic ARTIFACT.md and leaves the extension's bespoke fields for the author to add.

When `--yes` is set, missing values fail the command instead of prompting:

- `--description` is required for every type.
- `--rule-globs` is required when `--rule-mode glob` is set.
- `--rule-description` is required when `--rule-mode auto` is set.
- `--hook-event` is required for `--type hook`.
- `--server-identifier` is required for `--type mcp-server`.

Without `--yes`, the command prompts for missing values.

The following command scaffolds a skill without prompting:

```bash
podium artifact scaffold \
    --type skill \
    --description "Draft release notes from a list of ticket keys." \
    --license MIT \
    --yes \
    finance/release/release-notes
```

It writes `finance/release/release-notes/SKILL.md` and creates the intermediate domain directories. The `SKILL.md` carries `name`, `description`, `license`, and `metadata.version`. The command writes no `ARTIFACT.md`.

### `podium impact`

Lists the artifacts that depend on a given artifact, by querying the registry's reverse-dependency edges. Run it before changing or removing an artifact to see what the change affects.

```
podium impact <artifact-id> [--registry <url>]
```

`--registry` defaults to `PODIUM_REGISTRY`.

---

## Layer management

The `podium layer` commands register, list, order, update, and remove the layers a registry composes. [Connecting layer sources](../deployment/content/layer-sources) covers Git and local sources as a task, and [Layered composition](../deployment/content/layers) covers precedence and visibility.

Every `podium layer` subcommand takes `--registry <url>`, which defaults to `PODIUM_REGISTRY` and is required. `register`, `reingest`, and `watch` also fall back to `defaults.registry` in the merged `sync.yaml`.

Several layer commands refer to these authorization terms:

| Term | Meaning |
|:--|:--|
| Per-tenant `admin` role | The tenant administrator role that `podium admin grant` assigns. |
| Local-source rule | A request that names a filesystem path on the registry host requires the per-tenant `admin` role. A caller without it is rejected with `auth.forbidden` carrying `details.constraint: "local_source"`. A Git `--repo` value that resolves to the Git file transport also names a host path and falls under the same rule. |
| Unauthenticated registry | A registry started with no identity provider configured, or one started in public mode. It authenticates no caller. Each command below states what such a registry admits. |

### `podium layer register`

Registers a new layer from a Git repository or a local path.

```
podium layer register --id <id> --repo <git-url> --ref <ref> [--root <subpath>] [--force-push-policy <tolerant|strict>] [--repo-username <name>] [--repo-token-file <path|->] [--clear-repo-credential]
podium layer register --id <id> --local <path>
                      [--user-defined] [--owner <oidc-sub>]
                      [--public | --organization]
                      [--group <oidc-group>]... [--user <oidc-sub-or-email>]...
```

| Flag | Effect |
|:--|:--|
| `--id <id>` | Layer ID. Required. |
| `--repo <git-url>` | Git repository URL, for a `git` source. |
| `--ref <ref>` | Git ref. |
| `--root <subpath>` | Subpath under the repository. |
| `--local <path>` | Filesystem path on the registry host, for a `local` source. Requires the per-tenant `admin` role under the local-source rule. |
| `--force-push-policy <tolerant\|strict>` | Force-push handling for a Git source. `tolerant` (the default) preserves previously ingested commits and emits a `layer.history_rewritten` event. `strict` rejects an ingest whose history was rewritten. |
| `--repo-username <name>` | Username of a `git` layer's repository credential. `--repo-username ''` sends an empty username. |
| `--repo-token-file <path\|->` | File holding the write-only repository token, or `-` for standard input. The command removes one trailing `\n` or `\r\n`. |
| `--clear-repo-credential` | Store the `--repo` with no credential, including when it equals the remote `podium layer list` reports. |
| `--user-defined` | Register a personal layer. The registry derives its owner from the authenticated caller and sets its visibility to that owner alone. |
| `--owner <oidc-sub>` | OIDC subject of the layer's owner. |
| `--public` | Public visibility. |
| `--organization` | Organization-wide visibility. |
| `--group <oidc-group>` | Grant visibility to an OIDC group. Repeatable. |
| `--user <oidc-sub-or-email>` | Grant visibility to an OIDC subject or email. Repeatable. |

No flag takes the token as an argument value. The force-push policy is also settable with `podium layer update --force-push-policy` and through the `registry.yaml` key `source.git.force_push_policy`.

#### Webhooks

For Git sources, the registry returns the webhook URL and HMAC secret to configure on the source repository. On a multi-tenant registry, the webhook URL carries the tenant ID: `/v1/ingest/webhook/<tenant-id>/<layer-id>`. Without a webhook, the layer stays at its initial commit until the first manual reingest. [Keeping layers current](../deployment/content/layer-sources#keeping-layers-current) covers webhooks, polling, and manual reingest.

#### Repository credentials

The command exits 2 before it sends a request in these cases:

| Case | Message |
|:--|:--|
| A non-empty `--repo-username` without `--repo-token-file` | `error: --repo-username requires --repo-token-file` |
| `--repo-token-file` with `--clear-repo-credential` | `error: --repo-token-file cannot be combined with --clear-repo-credential` |
| A credential flag without `--repo` | `error: --repo-username, --repo-token-file, and --clear-repo-credential apply to a --repo registration` |
| An empty token file | `error: --repo-token-file names an empty token` |
| An unreadable token file | `error: layer: read --repo-token-file: <cause>` |

A `--repo` that carries a credential in its URL is refused with `details.constraint: "url_credential"` (see the [HTTP API reference](http-api#layer-management)). [Re-registering a private layer](../deployment/content/layer-sources#re-registering-a-private-layer) states when a re-registration with the remote `podium layer list` reports keeps the stored credential, and [ssh:// remotes and the listed value](../deployment/content/layer-sources#ssh-remotes-and-the-listed-value) states why an `ssh://` layer is re-registered with its login name.

#### Visibility and ownership

Visibility flags set who can see the layer. They apply to an admin-defined layer. A user-defined layer takes the fixed visibility `users: [<owner>]` instead.

- `--public` sets public visibility, and `--organization` sets organization-wide visibility.
- `--group` grants visibility to an OIDC group, and `--user` grants visibility to an OIDC subject or email. Both are repeatable.
- On a registry that authenticates callers, `--public`, `--organization`, `--group`, and `--user` each require the per-tenant `admin` role.

The registry reads `--owner`, `--public`, `--organization`, `--group`, and `--user` on a tenant admin's registration alone. This is the admin-only fields rule:

- On a registry that authenticates callers, a caller without the `admin` role that sends `--public`, `--organization`, `--group`, `--user`, or an `--owner` naming another subject is rejected. The rejection is `auth.forbidden` carrying `details.constraint: "admin_only_fields"`, and it names the asserted fields.
- A field is asserted by the value it carries. An omitted `--public` or `--organization`, a `--group` or `--user` given no occurrence, and an `--owner` that is empty or names the caller's own verified subject assert nothing.
- An unauthenticated registry admits every caller on the admin arm, so the rule refuses nothing there. On such a registry, `--owner` is the mechanism that names a layer's owner.
- The rule applies on the register path alone. On the update path, `podium layer update` applies the `immutable_visibility` rule to a stored user-defined layer instead, whichever caller runs it (see [`podium layer update`](#podium-layer-update)).

#### Authorization order

The registry evaluates three rules on a registration, in this order:

1. The layer write authorization rule. A registration it rejects carries a bare `auth.forbidden` with no `details.constraint`.
2. The local-source rule. A registration on its arm carries `details.constraint: "local_source"`.
3. The admin-only fields rule. Its `admin_only_fields` rejection is returned only where neither earlier rule rejects.

An unauthenticated registry admits the registration, including a `--local` one.

### `podium layer list`

Lists the configured layers the caller's identity can see, with their current state.

```
podium layer list [--deleted]
```

| Flag | Effect |
|:--|:--|
| `--deleted` | List soft-deleted layers that are still recoverable within the recovery window (see `podium layer restore`). |

The caller's identity determines which layers appear:

| Caller | Layers listed |
|:--|:--|
| A caller holding the per-tenant `admin` role | Every layer in the tenant. |
| Any other authenticated caller | The layers that caller's identity admits, including that caller's own user-defined layers. |
| A caller the registry resolves as anonymous | None. |
| A caller whose credential fails verification | None. The request is refused rather than answered with an empty list. |
| Any caller of an unauthenticated registry | Every layer in the tenant. |

Whether presenting no credential is itself a verification failure is the configured identity provider's rule.

The command prints the registry's response: an object whose `layers` member holds the layer objects. A layer whose credential `--repo-token-file` can set carries `repo_credential`, which reports whether a repository credential is stored. No member carries the credential's value.

### `podium layer reorder`

Re-sequences the layer list.

```
podium layer reorder <id> [<id> ...]
```

The argument order is precedence, lowest to highest.

| Layer | Authorized callers |
|:--|:--|
| User-defined | The layer's stored owner, with no admin role required, or a caller holding the per-tenant `admin` role. |
| Admin-defined | A caller holding the per-tenant `admin` role. |

Errors:

- A caller authorized on neither arm is rejected with `auth.forbidden`. This includes a caller without the `admin` role that reorders an admin-defined layer.
- A caller whose credential fails verification under the configured identity provider's rule is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either arm is evaluated. On this operation, `auth.forbidden` therefore names a caller the registry verified and did not authorize. The other layer write operations answer a caller whose credential fails verification with `auth.forbidden`.
- An ID that names no configured layer returns `registry.not_found`.

An unauthenticated registry admits the request.

### `podium layer unregister`

Removes a layer.

```
podium layer unregister <id>
```

| Layer | Authorized callers |
|:--|:--|
| Admin-defined | A caller holding the per-tenant `admin` role. |
| User-defined | The layer's stored owner, or a tenant admin. |

A caller authorized on neither arm is rejected with `auth.forbidden`. An unauthenticated registry admits the request.

### `podium layer restore`

Recovers a layer, and its artifacts, that was unregistered within the recovery window.

```
podium layer restore <id>
```

### `podium layer reingest`

Forces a re-pull of a layer's source.

```
podium layer reingest <id> [--break-glass --justification <text> --approver <id> --approver <id>]
```

| Flag | Effect |
|:--|:--|
| `--break-glass` | Override an active freeze window. Requires `--justification`. |
| `--justification <text>` | Reason for the break-glass override. |
| `--approver <id>` | Break-glass approver identity. Repeatable. |

During a freeze window, ingest is blocked unless `--break-glass` is passed with a justification. Break-glass requires dual sign-off, so supply two distinct approver identities with repeated `--approver` flags. A grant expires automatically after 24 hours and queues for post-hoc security review.

The command reports each artifact of the cycle:

- Standard output carries one line per accepted and unchanged artifact.
- Standard error carries one line per conflicted or rejected artifact, with that artifact's identifier, its error code, and its reason.
- Standard error also carries a `lint failures: <n>` line with the number of lint diagnostics the cycle raised.
- When the registry answers the request with an error, the command prints that error response on standard error instead of the per-artifact lines.

| Exit code | Meaning |
|:--|:--|
| `0` | None of the conditions below applies. |
| `1` | The cycle dropped at least one artifact, a lint diagnostic rejected one, or the request failed. |
| `2` | Usage error. |

A non-blocking advisory and an artifact whose embedding call failed are reported without changing the exit status, and the artifact is stored and served. The reingest response reports lint diagnostics only as a count. To read the per-artifact diagnostics, run `podium lint --registry <path>` against the source.

### `podium layer update`

Patches a registered layer's mutable fields. Only the flags supplied are applied, and every other field keeps its prior value. At least one mutable field is required.

```
podium layer update --id <id>
                    [--ref <ref>] [--root <subpath>] [--local <path>]
                    [--force-push-policy <tolerant|strict>]
                    [--rotate-webhook-secret]
                    [--repo-username <name>] [--repo-token-file <path|->] [--clear-repo-credential]
                    [--owner <oidc-sub>] [--public[=false]] [--organization[=false]]
                    [--group <oidc-group>]... [--user <oidc-sub-or-email>]...
                    [--clear-groups] [--clear-users]
```

| Flag | Effect |
|:--|:--|
| `--id <id>` | Layer ID. Required. |
| `--ref <ref>` | Git ref. |
| `--root <subpath>` | Git subpath. |
| `--local <path>` | Filesystem path on the registry host. Requires the per-tenant `admin` role under the local-source rule. |
| `--force-push-policy <tolerant\|strict>` | Force-push handling for a Git source. |
| `--rotate-webhook-secret` | Regenerate the Git layer's HMAC webhook secret and print the new value. |
| `--repo-username <name>` | Username of the repository credential. Applies only with `--repo-token-file`. |
| `--repo-token-file <path\|->` | Replace the stored repository credential without the URL. Keeps the stored username unless `--repo-username` is also passed, `--repo-username ''` included. |
| `--clear-repo-credential` | Remove the stored credential. |
| `--owner <oidc-sub>` | OIDC subject of the layer's owner. |
| `--public[=false]` | Grant public visibility, or withdraw it with `=false`. Omitting the flag keeps the stored value. |
| `--organization[=false]` | Grant organization-wide visibility, or withdraw it with `=false`. Omitting the flag keeps the stored value. |
| `--group <oidc-group>` | Replace the stored group list with the values given on this invocation. Repeatable. |
| `--user <oidc-sub-or-email>` | Replace the stored user list with the values given on this invocation. Repeatable. |
| `--clear-groups` | Empty the group list. |
| `--clear-users` | Empty the user list. |

#### Repository credential updates

- Omitting both `--repo-token-file` and `--clear-repo-credential` keeps the stored credential.
- The exit-2 refusals of `podium layer register` apply, apart from the `--repo` rule.
- `--repo-token-file` and `--clear-repo-credential` each satisfy "at least one mutable field".
- `--repo-username` applies only with `--repo-token-file`, so `--repo-username ''` alone sends no field. It exits 2 with `error: at least one mutable field must be provided`.
- A stored username from a credential an earlier release accepted inside `--repo` can hold a character that `repo_username` may not carry. The registry then refuses the update with `400 registry.invalid_argument` naming `repo_username`, and the update must pass `--repo-username` with the token.

#### Withdrawing visibility

The visibility flags withdraw as well as grant, because the update endpoint applies the visibility members the command sends and leaves the ones it omits.

- `--public=false` withdraws the public axis, and `--organization=false` withdraws the organization axis.
- A repeated `--group` or `--user` naming fewer members narrows the list.
- `--clear-groups` and `--clear-users` empty their list, which the repeatable flags cannot express.
- Combining `--clear-groups` with `--group`, or `--clear-users` with `--user`, is refused before any request is sent. The command prints `error: --group cannot be combined with --clear-groups, and --user cannot be combined with --clear-users` and exits 2.
- Withdrawing every axis leaves a layer that the visibility evaluator reports visible to no caller the registry resolves to a subject. Re-granting one axis restores it.
- An unauthenticated registry keeps every layer visible, so a withdrawal applied there takes effect once an identity provider is configured.
- A layer declared in `registry.yaml` is re-seeded from that declaration at every start, visibility and repository credential included. A withdrawal or a credential change applied to a declared layer reverts at the next start. Make the change in the declaration to make it durable.

#### User-defined layers

`--owner`, `--public`, `--organization`, `--group`, `--user`, `--clear-groups`, and `--clear-users` apply to an admin-defined layer. A stored user-defined layer has its owner and its implicit `users: [<owner>]` visibility fixed at registration. Against such a layer, the command's patch is checked flag by flag: the registry compares the value each flag would store with the value the layer holds, whichever caller runs it.

| Flag outcome | Flags |
|:--|:--|
| Refused with `registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`, because the flag would change the stored value | `--public`, `--organization`, `--group`, a `--user` naming anyone other than the owner, an `--owner` naming another subject, and `--clear-users`, which empties the stored list. |
| Admitted, because the value restates what the layer holds and asserts nothing | `--public=false`, `--organization=false`, `--user <owner>`, `--owner <owner>`, and `--clear-groups` against a layer storing no group. |

The refusal rejects the whole patch, so no other flag the same command carries is applied. To widen such a layer, an administrator re-registers its ID as an admin-defined layer.

#### Local path updates

`--local` patches the layer's filesystem path on the registry host and falls under the local-source rule. A patch carrying it from a caller without the per-tenant `admin` role is rejected with `auth.forbidden` carrying `details.constraint: "local_source"`, whatever the layer's stored source type. A patch that does not carry `--local` is not subject to that rule. An unauthenticated registry admits the patch.

### `podium layer watch`

Polls a layer's source for changes at a fixed interval and triggers a reingest on each tick. It works against `local`-source layers and against `git`-source layers with no webhook configured, for example on a developer machine without a public ingress. Each tick posts to `/v1/layers/reingest`, and the command runs until interrupted.

```
podium layer watch <id> [--interval <duration>]
```

| Flag or argument | Effect |
|:--|:--|
| `<id>` | Layer ID. |
| `--id <id>` | Names the layer as the positional `<id>` does. |
| `--interval <duration>` | Go duration string, such as `30s` or `1h`. Default: `1m`. A non-positive value is rejected. |

The command exits 2 before it sends any request in these cases:

- Both `<id>` and `--id` are given with different values. The command prints `error: layer id "<positional>" conflicts with --id "<flag>"`.
- A second positional argument is given. The command prints the usage line.

On a registry that authenticates its callers, some watch loops fall under the local-source rule described in the [layer management](#layer-management) terms. A watch loop over a `local`-source layer, or over a `git` layer whose repository string resolves to the Git file transport, drives a reingest that the rule authorizes only to a caller holding the per-tenant `admin` role. Any other caller is refused on each tick with `auth.forbidden` carrying `details.constraint: "local_source"`. An unauthenticated registry admits each tick. See [Who may register a local-source layer](../deployment/content/layer-sources#who-may-register-a-local-source-layer).

---

## Admin

Admin commands require the `admin` role on the tenant. Admin grants are recorded as `(identity, org_id, "admin")` rows. Manage them with `podium admin grant` and `podium admin revoke`.

### `podium admin tenant`

Manages tenants at runtime on a multi-tenant registry.

- The instance-operator role authorizes the group. It is distinct from the per-tenant `admin` role. An operator is seeded at boot through `PODIUM_OPERATOR_ADMINS`, which [Configuration](configuration#tenancy-and-administrators) describes, and authenticates as any caller does.
- The commands are available only when the registry runs in multi-tenant mode (`PODIUM_MULTI_TENANT`). A single-tenant registry rejects them with `registry.tenant_management_unavailable`.
- `--registry` is required on each command and defaults to `PODIUM_REGISTRY`.

```
podium admin tenant create <name> [--storage-bytes N] [--search-qps N] [--materialize-rate N] [--audit-volume-per-day N] [--max-user-layers N] [--expose-scope-preview true|false] --registry <url>
podium admin tenant list [--json] --registry <url>
podium admin tenant update <id> [--storage-bytes N] [--search-qps N] [--materialize-rate N] [--audit-volume-per-day N] [--max-user-layers N] [--expose-scope-preview true|false] [--active true|false] --registry <url>
podium admin tenant deactivate <id> --registry <url>
```

| Command | Effect |
|:--|:--|
| `create <name>` | Provisions a tenant and derives the org ID from the name. Create is idempotent: re-creating an existing name returns that tenant unchanged. The quota and scope-preview flags set the tenant's initial values, and an omitted flag takes the deployment default. |
| `list` | Lists every tenant. `--json` prints the registry response verbatim: an object whose `tenants` key holds the array. |
| `update <id>` | Sends only the flags passed, so an omitted flag leaves that field unchanged. `--active true` reactivates a deactivated tenant, and `--active false` deactivates it. The command cannot change the name, which is fixed at create. |
| `deactivate <id>` | Soft-deactivates the tenant. A deactivated tenant stops resolving while its data persists. `update <id> --active true` reactivates it. |

| Flag | Effect |
|:--|:--|
| `--storage-bytes N` | Per-tenant storage budget in bytes. `0` disables the budget. |
| `--search-qps N` | Per-tenant search QPS budget. `0` selects the deployment default (`PODIUM_QUOTA_SEARCH_QPS`). A negative value disables the budget for the tenant. |
| `--materialize-rate N` | Per-tenant materialization rate budget. `0` selects the deployment default (`PODIUM_QUOTA_MATERIALIZE_RATE`). A negative value disables the budget for the tenant. |
| `--audit-volume-per-day N` | Per-tenant audit-volume budget per day. `0` selects the deployment default (`PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY`). A negative value disables the budget for the tenant. |
| `--max-user-layers N` | Per-identity cap on user-defined layers. A positive value is enforced ahead of the deployment default. `0` selects the deployment default (`PODIUM_MAX_USER_LAYERS`, or 3 when that is unset). A negative value disables the cap. |
| `--expose-scope-preview true\|false` | Whether the tenant exposes aggregate scope-preview counts. |
| `--active true\|false` | `update` only. Sets the tenant's active state. |

### `podium admin grant` / `podium admin revoke`

Grants or revokes the tenant `admin` role for a user.

```
podium admin grant <user-id> --registry <url>
podium admin revoke <user-id> --registry <url>
```

The user identity is positional. `--registry` is required and defaults to `PODIUM_REGISTRY`.

### `podium admin show-effective`

Prints the effective per-layer visibility for any identity, for debugging visibility issues.

```
podium admin show-effective <user-id> [--group <g>]... --registry <url>
```

| Flag | Effect |
|:--|:--|
| `--group <g>` | OIDC group claim to evaluate. Repeatable. |
| `--registry <url>` | Registry URL. Required. |

### `podium admin reembed`

Regenerates embeddings. The registry re-embeds automatically when the configured embedding model changes; this command is for ad-hoc re-embeds.

```
podium admin reembed [--artifact <id> --version <semver>]
                     [--only-missing] [--since <rfc3339>]
                     --registry <url>
```

| Flag | Effect |
|:--|:--|
| `--artifact <id>` | Re-embed one specific artifact. Requires `--version`. |
| `--version <semver>` | The version to re-embed. Required with `--artifact`. |
| `--only-missing` | Skip artifacts that already have a vector. Scopes a tenant-wide pass. |
| `--since <rfc3339>` | Re-embed only artifacts ingested at or after this RFC3339 timestamp. Scopes a tenant-wide pass. |
| `--registry <url>` | Registry URL. Required; defaults to `PODIUM_REGISTRY`. |

With no `--artifact`, the command runs a tenant-wide pass, and `--only-missing` and `--since` compose to scope it.

### `podium admin runtime`

Writes a trusted runtime signing key into the keys file that the `injected-session-token` verifier reads at startup. Like `podium admin erase --local`, this is a local form: it edits a file on the host rather than calling a registry. The registry exposes no request-time registration endpoint.

```
podium admin runtime register --keys-file <path> --issuer <name> --algorithm <alg> --public-key-file <path>
```

| Flag | Effect |
|:--|:--|
| `--keys-file <path>` | Path to the registry's runtime keys file, the same path the registry process reads from `PODIUM_RUNTIME_KEYS_PATH`. Required; it takes no environment default. |
| `--issuer <name>` | Issuer name the runtime puts in the token's `iss` claim. Required. |
| `--algorithm <alg>` | JWS algorithm the runtime signs with (`RS256`, `ES256`, `EdDSA`, and so on). Required. |
| `--public-key-file <path>` | Path to the PEM-encoded public key. Required. The command reads the file and parses it against `--algorithm`, so a mismatched key fails here instead of at the registry's next start. |

Notes:

- The command reads the existing records and rewrites the whole file. The keys file therefore has a single writer, and the result of concurrent `register` invocations is undefined.
- The registry loads the new record at its next start.
- Read the file back with `cat` or `jq`. It holds public keys alone.

### `podium admin signing-key`

Writes the registry-managed key file that the registry reads at `PODIUM_SIGN_KEY_PATH`. Like `podium admin runtime register`, this is a local form: it edits a file on the host, calls no registry, and opens no store.

```
podium admin signing-key generate --key-file <path>
podium admin signing-key rotate --key-file <path> [--staged-out <path>]
```

| Subcommand | Effect |
|:--|:--|
| `generate` | Writes a new signing keypair, with mode `0600`, as a `private:` line and a `public:` line. Refuses to replace an existing file. |
| `rotate` | Reads the existing file and writes a new signing keypair in its place. Keeps the previous public key and every previous `verify:` key as `verify:` lines, so the registry keeps admitting every row they signed. |

| Flag | Effect |
|:--|:--|
| `--key-file <path>` | Path to the registry key file. Required. It takes no environment default, so a bare invocation never resolves `PODIUM_SIGN_KEY_PATH` or `~/.podium/standalone/registry-signing.key`. |
| `--staged-out <path>` | `rotate` only. Also writes the previous key file plus a `verify:` line for the new key to this path. A multi-replica rotation deploys that file to every replica before the rotated file. It must name a file other than `--key-file`. |

A key file carries zero or more `verify:` lines, each a base64 Ed25519 public key trusted for verification only. The `public:` key and the `verify:` keys form the registry's verification key set.

Both subcommands print the following:

1. The verification key set, as a comma-separated list of base64 keys with the signing key first. This is the value a consumer's `PODIUM_SIGNATURE_VERIFY_KEY` takes.
2. One line per key: `key_id=<hex> role=signing` for the signing key and `key_id=<hex> role=verify` for each `verify:` key. The `key_id` is the lowercase hex of the first 8 bytes of the SHA-256 digest of the public key.

| Exit code | Meaning |
|:--|:--|
| `0` | Success. |
| `1` | Any other error. |
| `2` | Usage error, including a missing `--key-file`. |

The registry loads the key file once, at start, so a running registry picks a rotated key up at its next start. [Rotating the signing key](../deployment/operations/signing-keys#rotating-the-signing-key) gives the procedure.

### `podium admin sign-stored-rows`

Rewrites stored rows under the §13.4 rules, signing under the registry signing key when signing is on. The same command runs as `podium-server sign-stored-rows`, which is the form the container image carries, and both forms share one implementation. It reads the configuration a registry start reads, opens the same store and object storage, binds no listen address, and exits.

```
podium admin sign-stored-rows [--include-unsigned] [--dry-run | --plan-digest=<digest>]
podium-server sign-stored-rows [--include-unsigned] [--dry-run | --plan-digest=<digest>]
```

| Flag | Effect |
|:--|:--|
| `--include-unsigned` | Also sign every stored row that carries no signature. The flag is the operator's attestation for every unsigned row the reviewed dry run lists, because an unsigned row carries no evidence of who stored it. Outside `--dry-run` it requires `--plan-digest`. With signing off, the command refuses it. |
| `--dry-run` | Print every write the command would make and make none. The output format is under [Dry-run output](#dry-run-output). |
| `--plan-digest=<digest>` | Bind the run to the plan a reviewed `--dry-run` printed. Required with `--include-unsigned`, and optional otherwise. See [Plan digest](#plan-digest). |

#### Dry-run output

A dry run prints these lines, in order:

1. One `dry-run:` line per planned row, in ascending order of tenant, artifact ID, and version: `<tenant>/<artifact>@<version> class=<class> target=<hash> write=<bool> sign=<bool> signed_by=<state> stored=<hash>`.
2. A header line: `dry-run: plan mode=<mode> include_unsigned=<bool> signing_key=<key_id> verify_keys=<key_id,...>`.
3. The totals a run would report.
4. Last, `dry-run: plan digest sha256:<hex> over <K> row(s)`.

In the row lines, `signed_by` is the `key_id` that verifies the stored envelope, `unsigned`, `unverified`, or, with signing off, `unchecked`. `stored` is the row's stored content hash. `K` counts the rows the digest covers, which are every planned row other than one reported `class=migrated`.

A dry run writes no row and no record of the rewrite's completion. Opening the store still applies the store's schema, which on Postgres creates the empty `data_migrations` table when it is absent.

#### Plan digest

The `--plan-digest` value is `sha256:` followed by 64 lowercase hexadecimal digits, taken from the dry run's last line. Before it writes anything, the command computes the digest of the plan it builds. When that digest differs, the command writes no row, no audit event, and no completion record. It prints its own plan as `plan:` lines in the dry-run format and exits with status 3.

These combinations are usage errors, which the command refuses before it opens the store:

- `--plan-digest` given with `--dry-run`.
- A malformed digest.
- `--include-unsigned` given without `--dry-run` and without `--plan-digest`.

The digest depends on the completion-record mode, the `--include-unsigned` setting, the signing `key_id` and the verification-only `key_id`s, and each covered row's tenant, ID, version, stored hash, outcome, target, sign decision, and signature state. A store, DSN, bucket, or Secret change that leaves those values identical does not change it.

| Change between the dry run and the run | Effect |
|:--|:--|
| A row is stored after the command builds its plan. | The row is absent from that plan, and the command leaves it as it is. |
| A signing registry ingests a row. | The row is at the current hash and signed under the current key, so it is reported `class=migrated` and leaves the digest unchanged. |
| A row is purged, or its object-held body is deleted. | The digest changes when the digest covered that row. |
| The dry run and the run use different releases. | The digest's byte encoding belongs to the release that computes it, so the two can disagree, and the run then refuses. |

#### Signing behavior

- The command re-signs under the signing key every row whose stored signature a `verify:` key verifies and whose stored bytes reproduce its stored hash or the previous release's digest.
- It never signs a row whose envelope fails under every key of the verification key set. It leaves a row already signed under the signing key at the current hash untouched.
- It signs each unsigned row the first start would sign, which it does only when the store is the SQLite store in the key file's directory. Beyond those rows, it signs a row that carries no signature only under `--include-unsigned`. Without the flag, such a row is moved to the new digest when its bytes reproduce the previous release's digest, and is left unsigned.
- With signing on, it refuses with `config.signature_provider_unavailable`, writing nothing, when the key file at `PODIUM_SIGN_KEY_PATH` is absent. It never generates a key. A malformed key file, or one whose `public:` line is not the public half of its `private:` line, refuses it with the same code.
- With signing off (`PODIUM_SIGN=none`), it rewrites without signing. It moves each reproducible unsigned row to the new digest, signs and re-signs no row, and records completion under the same rule. It refuses `--include-unsigned` given with `--dry-run` or `--plan-digest` with `config.signature_provider_unavailable`, writing nothing. `--include-unsigned` given alone stays a usage error.
- Whether or not the completion record is present, the command and its `--dry-run` refuse, writing nothing, when no signer is configured and a signed row's stored bytes reproduce the previous release's digest. The rewrite would strand that row's signature.
- It is subject to the same refusal as a registry start for a signing key that is not persisted with the store.

#### Running beside registry processes

- When the store records that the first-start rewrite of stored content hashes has completed, the command may run while registry processes on this release serve the store. Each write replaces a row only when its stored content hash and stored signature are the ones the command read.
- When the store holds no such record, the command performs that rewrite in place of the first start and records its completion. It then runs only while no registry process on the previous release serves the store. The command binds nothing and cannot detect such a process, so stop every one first.
- While no completion is recorded, a registry start over a store that holds a manifest row is refused in either signing mode. The refused start rewrites no manifest row, signs no row, and records no completion. The refusal names this command, which performs the rewrite in place of that start.
- The exception to that refusal is the SQLite store in the directory of the key file at `PODIUM_SIGN_KEY_PATH`, or at the default key location when that variable is unset.
- A store that holds no manifest row records completion at its first start.

#### Output and exit codes

The command prints the rewrite's summary line, `rehash: <n> rewritten, ...`, in both signing modes. With signing on, it then prints `rehash: <n> unsigned left`, and `rehash: verify key <key_id>: <n> row(s) still signed under it` for each verification-only key, including a key with a count of zero. With signing off, it prints neither.

The per-key count covers every row that key verifies and that the run did not re-sign. The count supports removing the key from the verification key set only when the run exits 0.

| Exit code | Meaning |
|:--|:--|
| `0` | Success, or `--help`, which prints the usage text. |
| `1` | The command is refused, a row holds the completion record back, or a write fails. |
| `2` | Usage error. The command prints its usage text. |
| `3` | The plan differs from the one `--plan-digest` names. |

The command appends an `artifact.signed` audit event for each row it signs to the destinations `PODIUM_AUDIT_LOG_PATH` lists. When the list names `stdout`, standard output carries those audit JSON lines beside the `plan:`, `dry-run:`, and `rehash:` summary lines. Each audit record is one complete JSON line that begins with `{`, so a collector that parses JSON separates the two. When the list names the file destination of a registry that is serving, the command and the registry both append to that file, as the shared-file item of [Loss accounting](audit-events#loss-accounting) describes.

### `podium admin migrate-to-standard`

Pumps a standalone deployment's state (SQLite metadata plus the filesystem object store) into a standard deployment (Postgres plus S3). The source flags default to the standalone layout under `~/.podium`, so the short form runs verbatim on a standalone host.

```
podium admin migrate-to-standard --postgres <dsn> --object-store <url>
                                 [--source-sqlite <path>] [--source-objects <path>]
                                 [--source-audit-log <path>] [--target-audit-log <path>]
                                 [--dry-run]
```

| Flag | Effect |
|:--|:--|
| `--postgres <dsn>` | Target Postgres DSN. Implies `--target-store=postgres`. |
| `--object-store <url>` | Target object store: `file:///path` (filesystem) or `s3://[key:secret@]endpoint/bucket[?region=R&ssl=false]` (S3). |
| `--source-sqlite <path>` | Source SQLite path. Default: `~/.podium/standalone/podium.db`. |
| `--source-objects <path>` | Source filesystem object store path. Default: `~/.podium/standalone/objects`. |
| `--source-audit-log <path>` | Source audit log file. Default: `~/.podium/audit.log`. |
| `--target-audit-log <path>` | Target audit log file. The audit history is copied only when this is set; otherwise the command warns that it was not copied. |
| `--dry-run` | Report the source plan (tenant, manifest, layer-config, and admin-grant counts); migrate nothing. |

Granular target overrides are available for non-default destinations and for advanced S3 configuration: `--target-store`, `--target-postgres-dsn`, `--target-sqlite`, `--target-objects`, `--target-objects-type`, and the `--target-s3-*` family.

#### What the command copies

- Every tenant in the source store, active or deactivated: each tenant's record (its name, quotas, `expose_scope_preview` setting, and active state) and its manifests, layer configs, and admin grants.
- The content blobs, whole.
- The whole audit log, when `--target-audit-log` is set.

It does not copy instance-operator grants; the target seeds them from its own `PODIUM_OPERATOR_ADMINS`. Dependency edges are regenerated by the next ingest.

When the target store already holds a tenant under a source tenant's ID, the command writes the source's quotas, `expose_scope_preview` setting, and active state over it. Apart from its record of the rewrite of stored content hashes, which the next section describes, the command removes nothing from the target store.

A tenant other than the `default` org receives requests on the target only when the target runs in multi-tenant mode with an identity provider it verifies (see `PODIUM_MULTI_TENANT` in [Configuration](configuration#tenancy-and-administrators)).

#### Content hashes and signing

The command copies each row as the source stores it and clears the target store's record of the rewrite of stored content hashes, so the rewrite runs again over the copied rows.

- Where the target is the SQLite store in the key file's directory, the target registry's next start runs the rewrite.
- For any other target, such as a Postgres store, the target registry's start is refused in either signing mode until `sign-stored-rows` runs against the target store. That means a `--dry-run`, a review of its report, and a run with its `--plan-digest`. With signing on, the run repeats the dry run's `--include-unsigned` setting. With signing off, it runs with `PODIUM_SIGN=none` in its environment.

A `git` layer's remote is an exception to the row-for-row copy. The command reads and writes layers through the store, so the target holds each layer's URL credential in the form the release that runs the command writes ([Rollback floor for the layer credential columns](../deployment/operations/upgrade-notes#rollback-floor-for-the-layer-credential-columns)).

The target registry signs with the source's key. Before the target's first start after the migration, place the source's registry signing key at the target's `PODIUM_SIGN_KEY_PATH`, replacing any key file already there, or create the chart's signing Secret from it. [Migrating to clustered](../deployment/modes/migrating#single-node-to-clustered) gives the copy step. The failure cases are these:

| Target key state | Result |
|:--|:--|
| No key file | The target is refused at start. |
| Any key other than the source's | Every copied signed row stays untouched, and the `sign-stored-rows` dry run such a target requires reports each of them `class=signature_unverified`. |

With the wrong key in place, two kinds of row fail:

- A row the source had already rewritten is refused with `materialize.signature_invalid`. Placing the source's key and restarting the target repairs it.
- A row that a source that never started on this release still held at the previous content hash is refused with `materialize.content_hash_mismatch`. It needs the target store recreated empty and the command run again with the source's key in place. Where the target is outside the SQLite store in the key file's directory, `sign-stored-rows` then runs before the target's start.

#### Running the migration

- No registry process runs on the target store while the command runs. Stop a target registry that is already running first, and start or restart it only after a run of the command that succeeds.
- Recreate the target store empty before the command runs again when a run failed with the immutability error.
- Also recreate it when a registry started on the target store, or the source registry started on the new version, at any point after the command's first run against it began. A re-run into that store fails with the immutability error at the first copied row the two stores hold at different hashes.

### Verifying integrity

There is no `podium admin verify` command. Artifact signature verification is the top-level [`podium verify <artifact>`](#podium-verify).

When `PODIUM_AUDIT_LOG_PATH` lists a file destination, the registry verifies audit-chain integrity automatically on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule. When it lists none, as with the Helm chart's `stdout` default, the receiving system applies the `prev_hash` check to the shipped stream (see the [Receiver checks](audit-events#receiver-checks) section of the audit events reference).

### SCIM provisioning

There is no SCIM sync command. SCIM is a server-side push from the identity provider to `/scim/v2/`, and the IdP sends group and membership updates. [Groups](../deployment/identity/groups) describes the receiver and its settings.

### `podium admin erase`

Performs a GDPR right-to-erasure for one user: it purges the user's owned layers and redacts the user's identity in audit records.

```
podium admin erase <user-id> --salt <salt> --registry <url>
podium admin erase <user-id> --salt <salt> --local --operator <admin-id> [--audit-path <path>]
```

| Flag or argument | Effect |
|:--|:--|
| `<user-id>` | User identity to erase. |
| `--salt <salt>` | Salt for the tombstone hash. Required, because an empty salt yields a guessable tombstone. |
| `--registry <url>` | Registry for the default form. Defaults to `PODIUM_REGISTRY`. |
| `--local` | Run the local form. |
| `--audit-path <path>` | Audit log file for the local form. Default: `~/.podium/audit.log`. Passing it selects the local form. |
| `--operator <admin-id>` | Invoking admin, recorded by the local form. Required with the local form. |

| Mode | Effect |
|:--|:--|
| Registry (default) | Calls `/v1/admin/erase`. Requires `--registry`. The registry unregisters and purges the user's owned layers. When the registry's audit list names a file destination, it also redacts the user identity in the audit records of the tenant the request acts in. The authenticated session identifies the invoking admin. |
| Local (`--local` or `--audit-path`) | Rewrites an audit log file directly. Requires `--operator`. Every tombstone uses the one `--salt` supplied. |

Registry form:

- On a registry started with `PODIUM_MULTI_TENANT=true`, the registry form acts in the tenant the caller's organization selects and redacts only that tenant's audit records.
- Records that carry no tenant are left unchanged, and the response reports nothing about them. When the list names a file destination, the local form redacts them in that file. Otherwise the receiving system owns their redaction (see [Retention and erasure](audit-events#retention-and-erasure)).
- When `PODIUM_AUDIT_LOG_PATH` lists no file destination, such as the Helm chart's `stdout` default, the registry rewrites no record and records only `user.erased`. The receiving system redacts the records already delivered to it.

Local form:

- `--audit-path` may name the file destination that `PODIUM_AUDIT_LOG_PATH` lists. The local form then redacts every record in it, whatever its tenant.
- The local form's `user.erased` event is written to that file only and reaches no other destination.
- Stop the registry first, because a running registry's next write would break the rewritten chain.

Redaction replaces `sub` with `redacted-<sha256(sub+salt)>` and preserves audit event sequencing. Erasure is itself logged as a `user.erased` event.

### `podium admin retention`

Applies per-event-type retention policies to the local audit log and drops events older than each policy's window. The command operates on the local log file; the registry-wide retention pass runs server-side.

```
podium admin retention --policy <type>=<duration> [--policy <type>=<duration>]... [--audit-path <path>]
```

| Flag | Effect |
|:--|:--|
| `--policy <type>=<duration>` | Maximum age for one event type, for example `artifacts.searched=720h`. Repeatable, and at least one is required. The duration is a Go duration string, and an `Nd` form such as `30d` is also accepted. |
| `--audit-path <path>` | Audit log path. Default: `~/.podium/audit.log`. |

The command prints the number of audit events dropped.

---

## Signing

### `podium sign`

Signs an artifact's content hash, or a raw content hash, outside the ingest flow.

```
podium sign <artifact> [--registry <url>] [--provider <name>]
podium sign --content-hash sha256:<hex> [--provider <name>]
```

The `<artifact>` form resolves the artifact's canonical content hash through the registry and then signs it. The `--content-hash` form signs a raw hash without resolving an artifact. Pass exactly one of the two.

| Flag | Effect |
|:--|:--|
| `--registry <url>` | Registry URL used to resolve the `<artifact>` form. Defaults to `PODIUM_REGISTRY`. |
| `--content-hash sha256:<hex>` | Sign this content hash directly, instead of resolving an artifact. |
| `--provider <name>` | Signature provider: `registry-managed`, `sigstore-keyless`, or `noop`. Defaults to `PODIUM_SIGNATURE_PROVIDER`, then `registry-managed`. |

| Provider | Behavior |
|:--|:--|
| `registry-managed` | Signs with the deployment's Ed25519 signing keypair at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`). See the notes below. |
| `sigstore-keyless` | Signs with a short-lived Sigstore certificate and records the signature in a transparency log. See [Sigstore keyless signing](#sigstore-keyless-signing). |
| `noop` | Signs a placeholder that `podium verify` always refuses. |

The `registry-managed` provider uses one Ed25519 signing keypair per registry deployment. Every process serving that store, and every tenant it serves, shares the keypair. `podium sign` takes the `private:` line of the key file and does not read `PODIUM_SIGNATURE_VERIFY_KEY`. Its envelope carries the signing key's `key_id`, as every registry-managed envelope does. A `registry-managed` invocation that cannot resolve the key it needs exits non-zero naming `config.signature_provider_unavailable`.

`podium verify <artifact> --signature <envelope>` checks the envelope the `<artifact>` form prints against the artifact's resolved content hash.

#### Sigstore keyless signing

The `sigstore-keyless` provider signs in three steps:

1. It obtains a short-lived certificate from the Fulcio endpoint at `PODIUM_SIGSTORE_FULCIO_URL`, with the OIDC token in `PODIUM_SIGSTORE_OIDC_TOKEN`, and signs the content hash.
2. It obtains an RFC 3161 timestamp over the signature from `PODIUM_SIGSTORE_TSA_URL`.
3. It records the signature in the Rekor v2 transparency log at `PODIUM_SIGSTORE_REKOR_URL`.

The three endpoints default to the Sigstore public-good instances. A Rekor v1 URL fails the signing. The envelope carries the log entry, its inclusion proof, the signed checkpoint, and the timestamp, so `podium verify` can check them without contacting any service.

Notes:

- `podium sign` sends each request once and waits for each no longer than `PODIUM_SIGSTORE_REQUEST_TIMEOUT` (60 seconds by default).
- A request that does not complete within that time fails the command with `sign failed:` followed by the service (`fulcio:`, `tsa:`, or `rekor:`), the endpoint URL, and `no response within <deadline>`.
- A request that misses its deadline can still have reached the service. A certificate or a log entry can therefore exist for a signing that printed no envelope, and running `podium sign` again records new ones.
- When `PODIUM_SIGSTORE_OIDC_TOKEN` is unset, `podium sign` exits non-zero with `sign failed: sign: sigstore-keyless not configured` before it contacts any service. The message names no error code.

### `podium verify`

Verifies a signature ad hoc.

```
podium verify <artifact> [--registry <url>] [--provider <name>] [--signature <envelope>]
podium verify --content-hash sha256:<hex> --signature <envelope> [--provider <name>]
```

| Form | What it verifies |
|:--|:--|
| `<artifact>` without `--signature` | Resolves the artifact's delivery hash and delivery signature through the registry and verifies that pair, which is the pair `podium-mcp` verifies on a load. It uses the verification key set the resolution order below finds. It refuses a response that carries no delivery hash or no delivery signature. |
| `<artifact>` with `--signature` | Verifies the explicit envelope against the artifact's resolved content hash, which is what `podium sign <artifact>` signs. |
| `--content-hash` with `--signature` | Verifies an explicit pair. |

| Flag | Effect |
|:--|:--|
| `--registry <url>` | Registry URL used to resolve the `<artifact>` form. Defaults to `PODIUM_REGISTRY`. |
| `--content-hash sha256:<hex>` | Verify against this content hash directly, instead of resolving an artifact. |
| `--signature <envelope>` | Signature envelope to verify. Pairs with `--content-hash`. In the `<artifact>` form, it is verified against the resolved content hash in place of the served delivery signature. |
| `--provider <name>` | Signature provider: `registry-managed`, `sigstore-keyless`, or `noop`. Defaults to `PODIUM_SIGNATURE_PROVIDER`, then `registry-managed`. |

| Exit code | Meaning |
|:--|:--|
| `0` | The signature is valid. |
| `1` | Mismatch or other error. |

#### Registry-managed verification

- The provider resolves the verification key set from `PODIUM_SIGNATURE_VERIFY_KEY` when that variable is set, as one base64 key or a comma-separated list. Otherwise it reads the `public:` line and every `verify:` line of the key file at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`).
- A signature that verifies under any key of the set is accepted. The envelope's `key_id` selects the key tried first, without refusing an envelope another key of the set verifies.
- A set variable with an entry that is empty or does not decode is an error naming it.
- `podium verify` reads no private key. An invocation that cannot resolve the key exits non-zero naming `config.signature_provider_unavailable`.
- `--provider noop` refuses every envelope.
- The delivery signature is always a registry-managed envelope. The form without `--signature` therefore verifies with `--provider registry-managed`, and `--provider sigstore-keyless` refuses it.

#### Sigstore keyless verification

Under `--provider sigstore-keyless`, `podium verify` checks the following against `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`:

- The timestamp verifies under a timestamp authority in the file, is signed by a certificate whose extended key usage lists time stamping, and covers the signature.
- The inclusion proof leads from the log entry to a checkpoint signed by a transparency-log key in the file.
- The log entry records the content hash, the signature, and the envelope's certificate.
- The certificate chains to a certificate authority in the file, lists code signing in its extended key usage, and was valid at the timestamp's time.
- The signature verifies.
- One of the certificate's email or URI subject alternative names appears in `PODIUM_SIGSTORE_CERT_IDENTITY`, and the certificate's OIDC issuer equals `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`.

Notes:

- With either policy variable unset, every envelope is refused.
- Validity is checked at the timestamp's time, so an envelope keeps verifying after its certificate expires.
- The command checks no witness cosignature or consistency proof, and it makes no network call.
- An envelope that carries no log entry or no timestamp, including every envelope produced before this format, is refused.
- A certificate whose identity is compromised after it signed keeps verifying, because Sigstore-keyless certificates carry no revocation.

#### Verification elsewhere

The MCP server verifies the delivery signature on every artifact it loads under the default `PODIUM_VERIFY_SIGNATURES=always`, after it recomputes the delivery hash. A signing registry verifies each stored signature before it serves the row.

---

## Cache and quota

### `podium cache prune`

Cleans up the content-addressed cache.

```
podium cache prune [--dir <path>] [--days <n>] [--dry-run]
```

| Flag | Effect |
|:--|:--|
| `--dir <path>` | Cache directory. Defaults to `PODIUM_CACHE_DIR`, then `~/.podium/cache`. |
| `--days <n>` | Remove buckets last accessed more than `n` days ago. Default: `30`. `0` removes every bucket older than now. |
| `--dry-run` | Report what would be removed; remove nothing. |

The cache lives at `~/.podium/cache/` by default; override it with `PODIUM_CACHE_DIR`. Content cache entries are immutable, so pruning by age is safe.

### `podium cache reset-revisions`

Deletes the revision marks `podium-mcp` keeps for `latest` loads.

```
podium cache reset-revisions [--dir <path>] [<artifact-id>...]
```

| Flag or argument | Effect |
|:--|:--|
| `--dir <path>` | Cache directory. Defaults to `PODIUM_CACHE_DIR`, then `~/.podium/cache`. |
| `<artifact-id>...` | Delete only the marks for these artifact IDs, under every registry. With no IDs, delete every mark. |

| Condition | Output | Exit code |
|:--|:--|:--|
| The cache directory holds no marks. | `cache: no revision marks under <path>` | `0` |
| The command deleted marks. | `cache: reset <n> revision mark(s)`, with `<n>` 0 when no mark matches the given IDs | `0` |
| A running `podium-mcp` holds the cache's index. | An error | `1` |

Notes:

- When the command exits 1, stop every MCP server that uses the cache directory and run it again.
- A `podium-mcp` that could not open the index holds its marks in memory, and they are cleared when that process exits.
- `podium cache prune` does not touch revision marks.

### `podium quota`

Shows current usage and limits per quota type: storage, search QPS, materialization rate, audit volume, and the user-defined-layer cap.

```
podium quota [--registry <url>]
```

`--registry` defaults to `PODIUM_REGISTRY` and is required.

The search QPS, materialization rate, and audit volume limits are the values the registry enforces against the caller's tenant, after the deployment default replaces a zero tenant value.

---

## JSON output

Most read commands accept `--json` for piping into other tools. The read CLI's envelopes differ from the raw wire responses documented in [HTTP API](http-api):

| Command | Difference from the wire response |
|:--|:--|
| `podium domain search --json` | Keys the ranked domains under `results`. The wire response uses `domains`. |
| `podium search --json` and `podium artifact show --json` | Deliver `frontmatter` as a parsed object. The wire response delivers a raw string. |
| `podium artifact show --json` | Renames the wire's `manifest_body` to `body`. |

The following pipeline searches for skills, keeps the results that score above 0.5, and syncs each one into a Claude Code target:

```bash
podium search "month-end close OR variance" --type skill --top-k 15 --json \
  | jq -r '.results[] | select(.score > 0.5) | .id' \
  | xargs -I{} podium sync --harness claude-code --target ~/.claude/ --include {}
```

---

## Environment variables

The tables below list the variables that client processes read: the CLI, `podium-mcp`, and the language SDKs. They are grouped by concern. [Configuration](configuration) lists the variables and `registry.yaml` keys of the registry process, including the backend selection variables such as `PODIUM_VECTOR_BACKEND` and `PODIUM_EMBEDDING_PROVIDER`, the web UI settings, the audit settings, and the quotas.

### Client

| Variable | Purpose |
|:--|:--|
| `PODIUM_REGISTRY` | Registry source: URL or filesystem path. |
| `PODIUM_HARNESS` | Default harness adapter. |
| `PODIUM_OVERLAY_PATH` | Workspace local-overlay path. |
| `PODIUM_CACHE_DIR` | Content-addressed cache directory. Default `~/.podium/cache/`. |
| `PODIUM_CACHE_MODE` | `always-revalidate` (default), `offline-first`, or `offline-only`. |
| `PODIUM_MATERIALIZE_ROOT` | Default destination for `load_artifact` materialization. |

### Signing and verification

| Variable | Purpose |
|:--|:--|
| `PODIUM_VERIFY_SIGNATURES` | `never` or `always`. Read by `podium-mcp`, server-source `podium sync`, and the language SDKs. The default is `always` for `podium-mcp` and server-source `podium sync`. The SDK default is `always` with a verification key configured and `never` otherwise. Set to the empty string, it counts as unset. Under `always`, a load whose artifact carries no signature fails with `materialize.signature_missing`. Under either value above `never`, a signature the response carries is verified. `never` checks nothing. |
| `PODIUM_SIGNATURE_VERIFY_KEY` | The verification key set the `registry-managed` provider verifies with: one base64 Ed25519 public key or a comma-separated list of them. Read by `podium-mcp`, `podium verify`, server-source `podium sync`, and the language SDKs. Set to the empty string, it counts as unset. A non-empty value is authoritative. An entry that is empty or does not decode is an error naming it in `podium verify`, and in `podium-mcp`, server-source `podium sync`, and the SDKs under a policy above `never`. When unset, the set comes from the `public:` line and every `verify:` line of the key file at `PODIUM_SIGN_KEY_PATH`. |
| `PODIUM_SIGN_KEY_PATH` | Registry signing key file, default `~/.podium/standalone/registry-signing.key`. It carries the signing keypair on `private:` and `public:` lines and zero or more verification-only `verify:` lines. Set to the empty string, it counts as unset. See the readers below. |

`PODIUM_SIGN_KEY_PATH` has several readers:

- The registry reads the file when signing is on, and generates it on first run.
- `sign-stored-rows` reads it and never generates it.
- `podium admin signing-key` takes its path from `--key-file` and does not read this variable.
- `podium sign` reads its `private:` line.
- `podium verify`, `podium-mcp`, server-source `podium sync`, and the language SDKs read its `public:` line and every `verify:` line when `PODIUM_SIGNATURE_VERIFY_KEY` is unset.

### Sigstore

| Variable | Purpose |
|:--|:--|
| `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` | Trust root for the `sigstore-keyless` provider: a Sigstore `trusted_root.json` carrying the certificate authorities, the transparency-log keys, and the timestamp authorities. Sigstore publishes the file through its TUF repository. Podium does not fetch the file or verify TUF metadata. Verification uses each entry only within its `validFor` window. `podium sign` and `podium verify` read it under `--provider sigstore-keyless` whatever the policy. An unset or unreadable value does not refuse the command. `podium verify` then refuses each envelope as `materialize.signature_invalid`. It does the same when the file does not parse or lacks a certificate authority, a usable ECDSA P-256 or Ed25519 log key, or a timestamp authority. |
| `PODIUM_SIGSTORE_CERT_IDENTITY` | The signer identities `podium verify --provider sigstore-keyless` accepts: one or more email addresses or URIs, separated by commas. Each is compared byte for byte with the certificate's email and URI subject alternative names. A certificate whose only subject alternative name is an otherName, such as a Fulcio username identity, is refused: Fulcio marks that extension critical, so the chain check refuses it with `cert chain`. Unset, every keyless envelope is refused. `podium-mcp` does not read it. |
| `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` | The OIDC issuer URL `podium verify --provider sigstore-keyless` accepts. It is compared byte for byte with the issuer the certificate records, after surrounding whitespace is removed from the value. Unset or blank, every keyless envelope is refused. `podium-mcp` does not read it. |
| `PODIUM_SIGSTORE_FULCIO_URL` | Fulcio endpoint `podium sign --provider sigstore-keyless` requests the signing certificate from. Unset or empty, it takes `https://fulcio.sigstore.dev`. |
| `PODIUM_SIGSTORE_REKOR_URL` | Rekor v2 log `podium sign --provider sigstore-keyless` records the entry in. Unset or empty, it takes `https://log2025-1.rekor.sigstore.dev`. A Rekor v1 URL fails the signing. |
| `PODIUM_SIGSTORE_TSA_URL` | RFC 3161 timestamp authority `podium sign --provider sigstore-keyless` requests the timestamp from. Unset or empty, it takes `https://timestamp.sigstore.dev/api/v1/timestamp`. |
| `PODIUM_SIGSTORE_REQUEST_TIMEOUT` | Read by `podium sign --provider sigstore-keyless`; environment only. Deadline on each request it makes to Fulcio, the timestamp authority, and Rekor, as a Go duration with a unit such as `60s`. Each request has its own deadline, which covers sending the request and reading the whole response. Unset or blank, it takes `60s`. A value that does not parse as a positive duration, including a bare number such as `60`, makes `podium sign` exit non-zero with `error: config.invalid` naming the variable, before it contacts any Sigstore endpoint. `podium verify` does not read it. |

### Runtime requirements

These variables control the `podium-mcp` runtime gate, which checks an artifact's runtime requirements against the capabilities the host advertises. All of them are environment only.

| Variable | Purpose |
|:--|:--|
| `PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE` | The Python and Node versions the host advertises to the runtime gate, as dot-separated digits such as `3.11.4`. A non-empty value activates the gate. Leading and trailing spaces and tabs are ignored when the value is compared, so a value of only spaces activates the gate and fails every requirement for that runtime. A `>=X` requirement compares numeric parts; any other requirement must equal the value. |
| `PODIUM_HOST_PACKAGES` | Comma-separated system packages the host advertises, for example `jq,curl`. Entries are trimmed, and empty entries are dropped. A requirement matches an entry exactly, including case. A non-empty list activates the gate. |
| `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` | `true` activates the runtime gate without advertising a capability, so a requirement for an unadvertised runtime or package fails with `materialize.runtime_unavailable`. Any other value, including `TRUE` and `1`, leaves enforcement off. |
| `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` | `true` admits an artifact whose runtime requirements are unsatisfied and writes a `WARN:` line to standard error naming the artifact and the advertised capabilities. It applies only while the gate is active and has no effect otherwise. It takes precedence over `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`. Any value other than `true` leaves the override off. |

### Identity and sign-in

| Variable | Purpose |
|:--|:--|
| `PODIUM_IDENTITY_PROVIDER` | Consumer side (MCP server and SDKs): `oauth-device-code` (default) or `injected-session-token`. The registry process reads the same variable for its own provider, as [Configuration](configuration#identity) lists. |
| `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` | OAuth provider configuration. See the notes below the table. |
| `PODIUM_SESSION_TOKEN`, `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE` | Injected-token sources. `PODIUM_SESSION_TOKEN` is the default variable the CLI, the MCP server, and the SDKs read. `PODIUM_SESSION_TOKEN_ENV` names a different variable to read it from, and `PODIUM_SESSION_TOKEN_FILE` names a file to read it from. |
| `PODIUM_TOKEN` | Registry credential a `kind: marketplace` sync target renders under. When set, it takes precedence over the session token and the `podium login` keychain token for that render. |

OAuth provider notes:

- `PODIUM_OAUTH_AUDIENCE` carries the audience both acquisition flows send. The device-code flow sends the value the client resolves, and the registry's browser sign-in redirect sends the first value the registry resolved.
- The registry process reads `PODIUM_OAUTH_AUDIENCE` as a comma-separated set of audiences it accepts. A client process sends the value verbatim as the one audience it asks for. A client that shares the registry's environment therefore needs `--audience` or an environment of its own.
- `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` is the device-authorization endpoint of the device-code flow, and the browser flow does not read it. The browser flow redirects to `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` alone, which [Configuration](configuration#web-ui) lists with the other browser-flow settings.

### Local audit stream

`podium-mcp` writes a local audit stream, separate from the registry's stream. [Audit and observability](../deployment/operations/observability#audit-destinations) covers destination setup, and [Audit events](audit-events) is the format reference.

| Variable | Purpose |
|:--|:--|
| `PODIUM_AUDIT_SINK` | Read by `podium-mcp`. The local audit stream's destination list: a comma-separated list of file paths, `http://` and `https://` URLs, `otlp+http://` and `otlp+https://` URLs, and the token `default`, which names the file `~/.podium/audit.log`. Set without a value, it names that file. Unset, it disables the local stream. The `--audit-sink` flag and the `audit-sink` config-file key take the same list. See the list below the table for the values that refuse startup. |

When `PODIUM_AUDIT_SINK` is set, `podium-mcp` also reads the audit transport and queue variables that [Configuration](configuration#audit) lists: `PODIUM_AUDIT_HEADERS_FILE`, `PODIUM_AUDIT_TLS_CA_FILE`, `PODIUM_AUDIT_TLS_CERT_FILE`, `PODIUM_AUDIT_TLS_KEY_FILE`, and `PODIUM_AUDIT_QUEUE_MAX_BYTES`.

`PODIUM_AUDIT_SINK` refuses `podium-mcp` startup with `config.audit_sink_unavailable` in these cases:

- The list contains `stdout`, because standard output carries the MCP protocol.
- The list is one that [Audit events](audit-events#destinations) does not admit.
- A transport or queue setting is one that the rows in [Configuration](configuration#audit) refuse.
- A file destination cannot be opened, or cannot be opened for appending.
