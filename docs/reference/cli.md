---
title: CLI
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

Every subcommand and subcommand group accepts `--help` and the short form `-h`; a subcommand group additionally accepts the bare `help` token. Leaf subcommands print a one-line description followed by their flag list:

```
$ podium lint --help
podium lint - Validate manifests in a filesystem-source registry.

Flags:
  -offline
        skip the §4.4 URL HEAD check (validate bundled files only)
  -registry string
        filesystem registry path (required)
```

Dispatcher groups (`admin`, `cache`, `config`, `domain`, `artifact`, `layer`, `profile`, `admin runtime`, `admin signing-key`, `admin tenant`) print their subcommand list. `sync` also dispatches the `override` and `save-as` subcommands when one is the first argument, and otherwise runs materialization directly:

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

When `--issuer` is unset, `podium login` probes the resolved registry URL for RFC 8414 authorization-server metadata at `/.well-known/oauth-authorization-server` and reads the device-authorization and token endpoints from it. The registry process does not serve that document itself, so discovery succeeds only when a fronting proxy or gateway publishes it; otherwise pass `--issuer` (or set `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`). Setting `PODIUM_NO_BROWSER` to a truthy value (`1`, `true`, `yes`, or `on`) has the same effect as `--no-browser` for headless and CI environments. Tokens cache in the OS keychain keyed by registry URL; multiple registries can be authenticated simultaneously.

`podium login` is a no-op when the resolved registry is a filesystem path or one of the loopback defaults, `http://127.0.0.1:8080` or `http://localhost:8080`. It reports that the registry needs no authentication and exits. A single-node server published at any other URL runs the full device-code flow, so a deployment that configures `oidc-jwt` authenticates the CLI through this command. Where the registry enables the browser flow, the registry signs a browser in through its own authorization-code exchange instead.

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

Each flag overrides the matching `PODIUM_*` env var for the duration of the process.

| Flag | Effect |
|:--|:--|
| `--standalone` | Single-binary configuration with embedded SQLite and sqlite-vec. The embedding provider defaults to `ollama` at `http://localhost:11434`; no model ships in the binary, so search runs BM25 over manifest text whenever that endpoint is unreachable. Defaults to bind `127.0.0.1:8080`. |
| `--strict` | Refuse to start without an explicit config (no auto-standalone fallback). Same effect as `PODIUM_NO_AUTOSTANDALONE`. |
| `--config <path>` | Override the default config file location. Overrides `PODIUM_CONFIG_FILE`. |
| `--bind <addr>` | Bind address. Overrides `PODIUM_BIND`. |
| `--layer-path <path>` | For standalone: register layers rooted at this path. The path is polymorphic. When `<path>/.registry-config` exists with `multi_layer: true` (and no top-level manifest files are present), each subdirectory becomes a `local`-source layer per the filesystem-registry layout. Otherwise the path is registered as a single `local`-source layer. Equivalent to `PODIUM_LAYER_PATH` or the `layer_path` key under the top-level `registry:` mapping in `registry.yaml`; precedence is CLI flag > env var > config file. |
| `--public-mode` | Bypass authentication and visibility filtering. Mutually exclusive with an identity provider. Overrides `PODIUM_PUBLIC_MODE`. |
| `--allow-public-bind` | Allow non-loopback bind in public mode or with trusted headers (typically behind an authenticated reverse proxy). Overrides `PODIUM_ALLOW_PUBLIC_BIND`. |
| `--no-embeddings` | Disable embeddings and fall back to BM25-only search. Overrides `PODIUM_NO_EMBEDDINGS`. |
| `--presign-ttl-seconds <n>` | Presigned-URL TTL in seconds. Overrides `PODIUM_PRESIGN_TTL_SECONDS` and the `object_store.presign_ttl_seconds` key in `registry.yaml`. |
| `--sign <mode>` | Ingest signing mode, `registry-key` or `none`. `registry-key`, the default, signs each accepted manifest with the registry-managed key at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`), generating the keypair on first run. `none` turns ingest signing off and generates no key file, so a `podium-mcp` consumer on that machine sets `PODIUM_VERIFY_SIGNATURES=never`; on a home that carries no key file the bridge otherwise refuses to start with `config.signature_provider_unavailable`. Overrides `PODIUM_SIGN`. |
| `--web-ui` | Mount the bundled web UI at `/app/`, and redirect `GET /` to it. Overrides `PODIUM_WEB_UI`. |
| `--web-ui-allow-public-bind` | Allow the web UI on a non-loopback bind when an identity provider is configured, so a UI reachable beyond the loopback interface is served only by a registry that resolves a caller's identity and filters what it serves by that identity. Overrides `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND`. |
| `--web-ui-auth` | Sign the browser in through the registry with the OAuth authorization-code flow. Requires `--web-ui`, `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, public mode off, and the browser-flow acquisition values in the environment-variable table below, including `PODIUM_WEB_UI_REDIRECT_URI`, which must be an `https` URL or an `http` URL whose host is a loopback address. A configuration that fails one of those conjuncts aborts startup with `config.web_ui_auth_unconfigured`, and the [error-code catalog](error-codes) states the whole guard. Overrides `PODIUM_WEB_UI_AUTH`. |
| `--web-ui-auth-transaction-ttl <duration>` | Sign-in window as a Go duration, `10m` by default. It is the `Max-Age` of the pre-authorization cookie the sign-in route sets. Overrides `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL`. |

Zero-flag (`podium serve` alone) auto-enters the standalone configuration when no config is found at `~/.podium/registry.yaml`. Disable with `PODIUM_NO_AUTOSTANDALONE=1` or `--strict`.

### `podium status`

Prints a diagnostic summary of the client setup: the resolved registry, harness, cache directory, cache mode, overlay path, identity provider, masked session token, and tenant. For a server-source registry it also probes `/healthz` and prints reachability, the registry mode the health response reports, the scope-preview aggregate counts, and whether a keychain token is present.

```
podium status [--registry <url>]
```

`--registry` overrides the resolved registry for this run. Without it, the registry resolves from `PODIUM_REGISTRY` and then the merged `sync.yaml`.

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
| `--config <path>` | Run one sync per entry in a `sync.yaml` `targets:` list. Each entry is `kind: workspace` (the default, a project-files layout) or `kind: marketplace` (a git-repo distribution Podium renders, with the target's `workflow` supplying the operator commands that clone and push). |
| `--include <pattern>` | Glob to include (canonical artifact IDs). Repeatable. |
| `--exclude <pattern>` | Glob to exclude. Applied after include. Repeatable. |
| `--type <t1,t2,...>` | Restrict to a comma-separated list of artifact types. |
| `--overlay <path>` | Workspace overlay path watched alongside the registry. |
| `--watch` | Long-running. Re-materialize on registry change events, or on fsnotify against a filesystem source. Combined with `--config` it starts no watch loop: each `kind: workspace` target syncs once and the command exits. A `kind: marketplace` target under `--watch` fails the run with `config.invalid` before any target renders. |
| `--dry-run` | Print the resolved set; write nothing. |
| `--preview` | Print the scope-preview aggregate counts and exit; write nothing. Requires a server-source registry, because the counts come from `GET /v1/scope/preview`. |
| `--check` | Validate the merged `sync.yaml` and report warnings (unresolved profiles, malformed globs, target/profile collisions). Combined with `--config`, it validates every target in the named file and materializes none. |
| `--json` | Structured envelope output (pipe to `jq`). |

Lock file at `<target>/.podium/sync.lock`.

Against a local catalog, two registry layers that contribute the same ID, where the higher-precedence copy declares no `extends:`, are a collision. The command drops the higher-precedence copy and prints `rejected: <id> (ingest.collision): <reason>` on standard error for each dropped artifact, in the format `podium layer reingest` uses. It materializes every other artifact, writes the lock file without the dropped artifact, and exits 1. `--dry-run` reports the drop and exits 1 without writing. Under `--config`, a target that dropped an artifact counts as a failed target, and the command exits 1; this includes a `kind: workspace` target under `--check`. A target's workflow still runs before the target is marked failed. `--check` without `--config` validates `sync.yaml` only and reports no drop, and a `kind: marketplace` target under `--check` renders nothing. Under `--json` the report stays on standard error. Under `--watch` each cycle prints its report, and the command exits 1 on interrupt when any cycle it reported failed or dropped an artifact. A cycle still running when the interrupt arrives can finish without a report, and the next `podium sync` reports its drops. A sync against a server reports no drop, because the server rejected the artifact at ingest. A drop adds a cause for exit status 1 and leaves the command's other exit statuses unchanged.

A `kind: marketplace` target renders the harness-native git-repo distribution into its `target` directory through the fixed `prepare`, `render`, `publish` pipeline. Podium owns the `render` phase, and the target's `workflow` supplies the `prepare` and `publish` commands that clone the repository into the working directory and push the rendered result to the remote. The marketplace fields (the git remote and branch, the harness set, the commit message, the plugins, and the publishing identity) are reached only through the `--config` path. See [Marketplace publishing](../consuming/publishing) for the model and the worked examples.

### `podium sync override`

On-the-fly toggling without touching `sync.yaml`. Toggles persist across watcher events and clear on the next manual `podium sync`.

```
podium sync override                              # TUI checklist
podium sync override --add <id>                   # repeatable
podium sync override --remove <id>                # repeatable
podium sync override --reset                      # clear all toggles
podium sync override --add <id> --dry-run
```

`--target <path>` selects the materialized directory and defaults to the current directory. `--registry <url-or-path>` and `--harness <name>` override the resolved registry and adapter for the toggle's materialization. An unset `--registry` resolves from `PODIUM_REGISTRY`, then the merged `sync.yaml`. An unset `--harness` resolves from `PODIUM_HARNESS`, then the harness recorded in the target's lock file, then the merged `sync.yaml`, then the built-in `none` adapter.

When a registry is configured, override re-materializes the target through the same composition as `podium sync`. Against a local catalog with a collision between two registry layers, it prints the same `rejected: <id> (ingest.collision): <reason>` line for each dropped artifact, keeps the recorded toggles, and exits 1. `--dry-run` writes nothing, runs no re-materialization, and reports no drop.

### `podium sync save-as`

Captures the current materialized set as a YAML profile in `sync.yaml`.

```
podium sync save-as --profile <name> [--target <path>] [--update] [--dry-run]
```

`--profile` is required. `--target <path>` selects the materialized directory and defaults to the current directory. `--update` overwrites an existing profile. After `save-as` succeeds, the lock file's toggles are cleared.

### `podium profile edit`

Permanent edits to entries in `sync.yaml`. Distinct from `podium sync override`, which is ephemeral.

```
podium profile edit <name>                                # TUI for the named profile
podium profile edit <name> --add-include <pattern>
podium profile edit <name> --remove-include <pattern>
podium profile edit <name> --add-exclude <pattern>
podium profile edit <name> --remove-exclude <pattern>
podium profile edit <name> --add-include <pattern> --dry-run
podium profile edit <name> --target <path> --add-include <pattern>
```

The profile name is a required positional argument; `podium profile edit` with no name exits 2 and asks for one. `--target <path>` selects the directory holding `.podium/sync.yaml` and defaults to the current directory. The add and remove flags are repeatable, and passing none of them opens the interactive editor.

Modifies `sync.yaml` in place, preserving formatting and comments around untouched keys.

---

## Read CLI

The read CLI maps 1:1 to the SDK's read operations and uses the same identity, cache, layer composition, and visibility filtering server-side.

Each command that queries the registry (`search`, `domain show`, `domain search`, `domain analyze`, `artifact show`, and `impact`) takes `--registry <url>`, which defaults to `PODIUM_REGISTRY` and is required. `artifact scaffold` is filesystem-only and takes no `--registry`.

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
podium domain analyze [<path>] [--path <path>]
```

The subtree is given positionally. The `--path` flag is accepted as an alternative and wins over the positional argument. An empty path analyzes the root.

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

`--sensitivity` defaults to `low` and `--version` defaults to `0.1.0`. `--type` is required. It accepts the first-class artifact types and `mcp-server`, the extension type Podium ships built-in:

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

Every `podium layer` subcommand takes `--registry <url>`, which defaults to `PODIUM_REGISTRY` and is required. `register`, `reingest`, and `watch` additionally fall back to `defaults.registry` in the merged `sync.yaml`.

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

`--local` names a filesystem path on the registry host and requires the per-tenant `admin` role. A caller without it is rejected with `auth.forbidden` carrying `details.constraint: "local_source"`. A `--repo` value that resolves to the Git file transport also names a host path and takes the same arm. A registry started with no identity provider configured, or one started in public mode, authenticates no caller and admits the registration.

`--owner`, `--public`, `--organization`, `--group`, and `--user` set fields the registry reads on a tenant admin's registration alone. On a registry that authenticates callers, a caller without the `admin` role that sends `--public`, `--organization`, `--group`, `--user`, or an `--owner` naming another subject is rejected with `auth.forbidden` carrying `details.constraint: "admin_only_fields"`, and the refusal names the asserted fields. A field is asserted by the value it carries, so an omitted `--public` or `--organization`, a `--group` or `--user` given no occurrence, and an `--owner` that is empty or names the caller's own verified subject assert nothing. A registry started with no identity provider configured, or one started in public mode, authenticates no caller and admits every caller on the admin arm, so the rule refuses nothing there, and `--owner` is the mechanism that names a layer's owner on such a registry. The registry evaluates this rule after the layer write authorization rule and after the local-source rule, so a registration the layer write rule rejects carries a bare `auth.forbidden` with no `details.constraint`, a registration also on the local-source rule's arm carries `details.constraint: "local_source"`, and the `admin_only_fields` rejection is returned only where neither earlier rule rejects.

`--force-push-policy` sets the per-layer force-push handling for a Git source. The default (`tolerant`) preserves previously-ingested commits and emits a `layer.history_rewritten` event; `strict` rejects an ingest whose history was rewritten. The policy is also settable with `podium layer update --force-push-policy` and through the registry.yaml `source.git.force_push_policy` key.

Visibility flags set who can see the layer. They apply to an admin-defined layer; a user-defined layer takes the fixed visibility `users: [<owner>]` instead.

- `--user-defined` registers a personal layer. The registry derives its owner from the authenticated caller and sets its visibility to that owner alone. On a registry that authenticates callers, a caller without the `admin` role that sends `--public`, `--organization`, `--group`, `--user`, or an `--owner` naming another subject is rejected with `auth.forbidden` carrying `details.constraint: "admin_only_fields"`; the paragraph above states the deployments where no flag is refused, the precedence the layer write and local-source rejections take over this one, and that `--owner` is the mechanism there. On the update path the rule is a different one: `podium layer update` refuses `--owner`, `--public`, `--organization`, `--group`, and `--user` against a stored user-defined layer with `registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`, whichever caller runs it, while the `admin_only_fields` refusal above is returned on the register path alone.
- `--public` sets public visibility; `--organization` sets organization-wide visibility. Both require the per-tenant `admin` role on a registry that authenticates callers.
- `--group` grants visibility to an OIDC group (repeatable), and requires the per-tenant `admin` role on a registry that authenticates callers.
- `--user` grants visibility to an OIDC subject or email (repeatable), and requires the per-tenant `admin` role on a registry that authenticates callers.

### `podium layer list`

Lists the configured layers the caller's identity can see, and their current state. A caller holding the per-tenant `admin` role sees every layer in the tenant. Any other authenticated caller sees the layers that caller's identity admits, including that caller's own user-defined layers. A caller the registry resolves as anonymous sees none. A caller whose credential fails verification is refused rather than shown an empty list, and whether presenting no credential is itself a verification failure is the configured identity provider's rule. A registry started with no identity provider configured, or one started in public mode, authenticates no caller, so every layer in the tenant is listed there.

```
podium layer list [--deleted]
```

`--deleted` lists soft-deleted layers still recoverable within the recovery window (see `podium layer restore`).

### `podium layer reorder`

Re-sequences the layer list. Reordering a user-defined layer requires no admin role: it is authorized to that layer's stored owner or to a caller holding the per-tenant `admin` role. Reordering an admin-defined layer requires the per-tenant `admin` role, and a caller without it is rejected with `auth.forbidden`, as is a caller authorized on neither arm. A caller whose credential fails verification under the configured identity provider's rule is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either arm is evaluated, so on this operation `auth.forbidden` names a caller the registry verified and did not authorize; the other layer write operations answer such a caller `auth.forbidden` as before. A registry started with no identity provider configured, or one started in public mode, authenticates no caller and admits the request. An id that names no configured layer returns `registry.not_found`.

```
podium layer reorder <id> [<id> ...]
```

The argument order is precedence, lowest to highest.

### `podium layer unregister`

Removes a layer. An admin-defined layer is removed by a caller holding the per-tenant `admin` role. A user-defined layer is removed by its stored owner or by a tenant admin, and a caller authorized on neither arm is rejected with `auth.forbidden`. A registry started with no identity provider configured, or one started in public mode, authenticates no caller and admits the request.

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

The command prints one line per accepted and unchanged artifact on standard output. It prints one line per conflicted or rejected artifact on standard error, carrying that artifact's identifier, its error code, and its reason, and a `lint failures: <n>` line reporting the number of lint diagnostics the cycle raised. On a request the registry answers with an error it prints that error response on standard error instead of the per-artifact lines. It exits 1 when the cycle dropped at least one artifact, when a lint diagnostic rejected one, or when the request failed. It exits 2 on a usage error and 0 otherwise. A non-blocking advisory and an artifact whose embedding call failed are reported without changing the exit status: the artifact is stored and served. Run `podium lint --registry <path>` against the source to read the per-artifact lint diagnostics, which the reingest response reports only as a count.

### `podium layer update`

Patches a registered layer's mutable fields. Only the flags supplied are applied; every other field keeps its prior value. At least one mutable field is required.

```
podium layer update --id <id>
                    [--ref <ref>] [--root <subpath>] [--local <path>]
                    [--force-push-policy <tolerant|strict>]
                    [--rotate-webhook-secret]
                    [--owner <oidc-sub>] [--public[=false]] [--organization[=false]]
                    [--group <oidc-group>]... [--user <oidc-sub-or-email>]...
                    [--clear-groups] [--clear-users]
```

`--rotate-webhook-secret` regenerates the Git layer's HMAC webhook secret and prints the new value.

The visibility flags withdraw as well as grant, because the update endpoint applies the visibility members the command sends and leaves the ones it omits. `--public=false` withdraws the public axis and `--organization=false` withdraws the organization axis, while omitting the flag keeps the stored value. `--group` and `--user` replace the stored list with the values given on that invocation, so a repeated flag naming fewer members narrows the list. `--clear-groups` and `--clear-users` empty their list, which the repeatable flags cannot express. Combining `--clear-groups` with `--group`, or `--clear-users` with `--user`, is refused before any request is sent: the command prints `error: --group cannot be combined with --clear-groups, and --user cannot be combined with --clear-users` and exits 2. Withdrawing every axis leaves a layer the visibility evaluator reports visible to no caller the registry resolves to a subject, and re-granting one restores it. A registry started with no identity provider configured, and one started in public mode, authenticates no caller and keeps every layer visible there, so a withdrawal applied on such a registry takes effect once an identity provider is configured. A layer declared in `registry.yaml` is re-seeded from that declaration at every start, visibility included, so a withdrawal applied to a declared layer reverts at the next start; withdraw it in the declaration to make it durable.

`--owner`, `--public`, `--organization`, `--group`, `--user`, `--clear-groups`, and `--clear-users` apply to an admin-defined layer. Against a stored user-defined layer, whose owner and whose implicit `users: [<owner>]` visibility are fixed at registration, the refusal reads the value each flag would store against the value the layer holds. A flag that would change the stored value is refused with `registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`: `--public`, `--organization`, `--group`, a `--user` naming anyone other than the owner, an `--owner` naming another subject, and `--clear-users`, which empties the stored list. The refusal rejects the whole patch, so no other flag the same command carries is applied. A flag whose value restates what the layer holds asserts nothing and is admitted, including `--public=false`, `--organization=false`, `--user <owner>`, `--owner <owner>`, and `--clear-groups` against a layer storing no group. An administrator widens such a layer by re-registering its ID as an admin-defined layer.

`--local` patches the layer's filesystem path on the registry host and requires the per-tenant `admin` role. A patch carrying it is rejected with `auth.forbidden` carrying `details.constraint: "local_source"` for a caller without that role, whatever the layer's stored source type. A patch that does not carry `--local` is not reached by that rule. A registry started with no identity provider configured, or one started in public mode, authenticates no caller and admits the patch.

### `podium layer watch`

Polls a layer's source for changes at a configured interval. Works against `local`-source layers and against `git`-source layers that do not have a webhook configured (for example, on a developer machine without a public ingress). Each tick posts to `/v1/layers/reingest`; the command runs until interrupted.

```
podium layer watch <id> [--interval <duration>]
```

`--id <id>` names the layer as the positional `<id>` does. Giving both with different values is refused before any request is sent: the command prints `error: layer id "<positional>" conflicts with --id "<flag>"` and exits 2. A second positional prints the usage line and exits 2.

`--interval` takes a Go duration string (`30s`, `1h`) and defaults to `1m`. A non-positive value is rejected.

On a registry that authenticates its callers, a watch loop over a `local`-source layer, or over a `git` layer whose repository string resolves to the Git file transport, drives a reingest that the local-source rule described under [`podium layer register`](#podium-layer-register) authorizes to a caller holding the per-tenant `admin` role. Any other caller is refused on each tick with `auth.forbidden` carrying `details.constraint: "local_source"`. A registry started with no identity provider configured, or one started in public mode, authenticates no caller and admits each tick. See [Who may register a local-source layer](../deployment/layers#who-may-register-a-local-source-layer).

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
| `list` | Lists every tenant. `--json` prints the registry response verbatim: an object whose `tenants` key holds the array. |
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

### `podium admin runtime`

Writes a trusted runtime signing key into the keys file the `injected-session-token` verifier reads at startup. Like `podium admin erase --local`, this is a local form: it edits a file on the host rather than calling a registry, and the registry exposes no request-time registration endpoint.

```
podium admin runtime register --keys-file <path> --issuer <name> --algorithm <alg> --public-key-file <path>
```

| Flag | Effect |
|:--|:--|
| `--keys-file <path>` | Path to the registry's runtime keys file, the same path the registry process reads from `PODIUM_RUNTIME_KEYS_PATH`. Required; it takes no environment default. |
| `--issuer <name>` | Issuer name the runtime puts in the token's `iss` claim. Required. |
| `--algorithm <alg>` | JWS algorithm the runtime signs with (`RS256`, `ES256`, `EdDSA`, and so on). Required. |
| `--public-key-file <path>` | Path to the PEM-encoded public key. Required; the command reads the file and parses it against `--algorithm`, so a mismatched key fails here instead of at the registry's next start. |

The command reads the existing records and rewrites the whole file, so the keys file has a single writer and the result of concurrent `register` invocations is undefined. The registry loads the new record at its next start. Read the file back with `cat` or `jq`; it holds public keys alone.

### `podium admin signing-key`

Writes the registry-managed key file that the registry reads at `PODIUM_SIGN_KEY_PATH`. Like `podium admin runtime register`, this is a local form: it edits a file on the host, calls no registry, and opens no store.

```
podium admin signing-key generate --key-file <path>
podium admin signing-key rotate --key-file <path> [--staged-out <path>]
```

| Flag | Effect |
|:--|:--|
| `--key-file <path>` | Path to the registry key file. Required; it takes no environment default, so a bare invocation never resolves `PODIUM_SIGN_KEY_PATH` or `~/.podium/standalone/registry-signing.key`. |
| `--staged-out <path>` | `rotate` only. Also writes the previous key file plus a `verify:` line for the new key to this path, which a multi-replica rotation deploys to every replica before the rotated file. It must name a file other than `--key-file`. |

`generate` writes a new signing keypair, with mode `0600`, as a `private:` line and a `public:` line, and refuses to replace an existing file. `rotate` reads the existing file, writes a new signing keypair in its place, and keeps the previous public key and every previous `verify:` key as `verify:` lines, so the registry keeps admitting every row they signed. A key file carries zero or more `verify:` lines, each a base64 Ed25519 public key trusted for verification only, and the `public:` key and the `verify:` keys form the registry's verification key set.

Both subcommands print the verification key set on their first line as a comma-separated list of base64 keys, signing key first, which is the value a consumer's `PODIUM_SIGNATURE_VERIFY_KEY` takes. One line per key follows, `key_id=<hex> role=signing` for the signing key and `key_id=<hex> role=verify` for each `verify:` key, where `key_id` is the lowercase hex of the first 8 bytes of the SHA-256 digest of the public key. Each subcommand exits 0 on success, 2 on a usage error, including a missing `--key-file`, and 1 on any other error. The registry loads the key file once, at start, so a running registry picks a rotated key up at its next start. [Rotating the signing key](../deployment/operator-guide#rotating-the-signing-key) gives the procedure.

### `podium admin sign-stored-rows`

Rewrites stored rows under the §13.4 rules, signing under the registry signing key when signing is on. The same command runs as `podium-server sign-stored-rows`, which is the form the container image carries, and both forms share one implementation. It reads the configuration a registry start reads, opens the same store and object storage, binds no listen address, and exits.

```
podium admin sign-stored-rows [--include-unsigned] [--dry-run | --plan-digest=<digest>]
podium-server sign-stored-rows [--include-unsigned] [--dry-run | --plan-digest=<digest>]
```

| Flag | Effect |
|:--|:--|
| `--include-unsigned` | Also sign every stored row that carries no signature. The flag is the operator's attestation for every unsigned row the reviewed dry run lists, because an unsigned row carries no evidence of who stored it. Outside `--dry-run` it requires `--plan-digest`. With signing off the command refuses it. |
| `--dry-run` | Print every write the command would make and make none. It prints one `dry-run:` line per planned row, in ascending order of tenant, artifact ID, and version: `<tenant>/<artifact>@<version> class=<class> target=<hash> write=<bool> sign=<bool> signed_by=<state> stored=<hash>`, where `signed_by` is the `key_id` that verifies the stored envelope, `unsigned`, `unverified`, or, with signing off, `unchecked`, and `stored` is the row's stored content hash. A `dry-run: plan mode=<mode> include_unsigned=<bool> signing_key=<key_id> verify_keys=<key_id,...>` header line follows, then the totals a run would report, and last `dry-run: plan digest sha256:<hex> over <K> row(s)`. `K` counts the rows the digest covers, which are every planned row other than one reported `class=migrated`. It writes no row and no record of the rewrite's completion. Opening the store still applies the store's schema, which on Postgres creates the empty `data_migrations` table when it is absent. |
| `--plan-digest=<digest>` | Bind the run to the plan a reviewed `--dry-run` printed. The value is `sha256:` followed by 64 lowercase hexadecimal digits, taken from the dry run's last line. Before it writes anything, the command computes the plan digest of the plan it builds. When that digest differs, it writes no row, no audit event, and no completion record, prints its own plan as `plan:` lines in the dry-run format, and exits with status 3. Required with `--include-unsigned`, and optional otherwise. |

`--plan-digest` given with `--dry-run`, a malformed digest, and `--include-unsigned` given without `--dry-run` and without `--plan-digest` are usage errors, which the command refuses before it opens the store. The digest depends on the completion-record mode, the `--include-unsigned` setting, the signing `key_id` and the verification-only `key_id`s, and each covered row's tenant, ID, version, stored hash, outcome, target, sign decision, and signature state. A store, DSN, bucket, or Secret change that leaves those values identical does not change it. A row stored after the command builds its plan is absent from that plan, and the command leaves it as it is. A row a signing registry ingests between the dry run and the run is at the current hash and signed under the current key, so it is reported `class=migrated` and leaves the digest unchanged. A row purged, or its object-held body deleted, between the dry run and the run changes the digest when the digest covered it. The digest's byte encoding belongs to the release that computes it, so a dry run and a run of different releases can disagree, and the run then refuses.

The command re-signs under the signing key every row whose stored signature a `verify:` key verifies and whose stored bytes reproduce its stored hash or the previous release's digest. It never signs a row whose envelope fails under every key of the verification key set, and it leaves a row already signed under the signing key at the current hash untouched. With signing on, it refuses with `config.signature_provider_unavailable`, writing nothing, when the key file at `PODIUM_SIGN_KEY_PATH` is absent, and it never generates a key. A malformed key file, or one whose `public:` line is not the public half of its `private:` line, refuses it with the same code. With signing off (`PODIUM_SIGN=none`), the command rewrites without signing: it moves each reproducible unsigned row to the new digest, signs and re-signs no row, and records completion under the same rule. It then refuses `--include-unsigned` given with `--dry-run` or `--plan-digest` with `config.signature_provider_unavailable`, writing nothing, while `--include-unsigned` given alone stays a usage error. Whether or not the completion record is present, the command and its `--dry-run` refuse, writing nothing, when no signer is configured and a signed row's stored bytes reproduce the previous release's digest, because the rewrite would strand that row's signature. It is subject to the same refusal as a registry start for a signing key that is not persisted with the store.

When the store records that the first-start rewrite of stored content hashes has completed, the command may run while registry processes on this release serve the store, because each write replaces a row only when its stored content hash and stored signature are the ones the command read. When the store holds no such record, the command performs that rewrite in place of the first start and records its completion, so it runs only while no registry process on the previous release serves the store. The command binds nothing and cannot detect such a process, so stop every one first. While no completion is recorded, a registry start over a store that holds a manifest row is refused in either signing mode, rewriting no manifest row, signing no row, and recording no completion, unless the store is the SQLite store in the directory of the key file at `PODIUM_SIGN_KEY_PATH`, or at the default key location when that variable is unset. The refusal names this command, and this command performs the rewrite in place of that start. A store that holds no manifest row records completion at its first start. The command also signs each unsigned row the first start would sign, which it does only when the store is the SQLite store in the key file's directory. Beyond those rows, it signs a row that carries no signature only under `--include-unsigned`; without the flag, such a row is moved to the new digest when its bytes reproduce the previous release's digest and is left unsigned.

The command prints the rewrite's summary line, `rehash: <n> rewritten, ...`, in both signing modes. With signing on, it then prints `rehash: <n> unsigned left` and `rehash: verify key <key_id>: <n> row(s) still signed under it` for each verification-only key, including a key with a count of zero; with signing off it prints neither. The count covers every row that key verifies and that the run did not re-sign, and it supports removing the key from the verification key set only when the run exits 0. The command exits 0 on success, 0 with its usage text for `--help`, 2 with its usage text on a usage error, 3 when its plan differs from the one `--plan-digest` names, and 1 when it is refused, when a row holds the completion record back, or when a write fails.

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

The command copies each row as the source stores it and clears the target store's record of the rewrite of stored content hashes, so the rewrite runs again over the copied rows. Where the target is the SQLite store in the key file's directory, the target registry's next start runs it. For any other target, such as a Postgres store, the target registry's start is refused in either signing mode until `sign-stored-rows` runs against the target store: a `--dry-run`, a review of its report, and a run with its `--plan-digest`, which with signing on repeats the dry run's `--include-unsigned` setting and with signing off runs with `PODIUM_SIGN=none` in its environment.

The target registry signs with the source's key. Before the target's first start after the migration, place the source's registry signing key at the target's `PODIUM_SIGN_KEY_PATH`, replacing any key file already there, or create the chart's signing Secret from it; [Single node](../deployment/single-node#migrating-to-clustered) gives the copy step. A target with no key file is refused at start. A target holding any other key leaves every copied signed row untouched, and the `sign-stored-rows` dry run such a target requires reports each of them `class=signature_unverified`. A row the source had already rewritten is then refused with `materialize.signature_invalid`, and placing the source's key and restarting the target repairs it. A row a source that never started on this release still held at the previous content hash is refused with `materialize.content_hash_mismatch`, and it needs the target store recreated empty and the command run again with the source's key in place, followed by `sign-stored-rows` before the target's start where the target is outside the SQLite store in the key file's directory. The same section states both cases.

No registry process runs on the target store while the command runs, so a target registry that is already running is stopped first, and the target registry is started, or restarted, only after a run of the command that succeeds. Recreate the target store empty before the command runs again when a run failed with the immutability error, or when a registry started on the target store, or the source registry started on the new version, at any point after the command's first run against it began, because a re-run into that store fails with the immutability error at the first copied row the two stores hold at different hashes.

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

### `podium admin retention`

Applies per-event-type retention policies to the local audit log, dropping events older than the policy's window. The command operates on the local log file; the registry-wide retention pass runs server-side.

```
podium admin retention --policy <type>=<duration> [--policy <type>=<duration>]... [--audit-path <path>]
```

| Flag | Effect |
|:--|:--|
| `--policy <type>=<duration>` | Maximum age for one event type, for example `artifacts.searched=720h`. Repeatable, and at least one is required. The duration is a Go duration string; an `Nd` form such as `30d` is also accepted. |
| `--audit-path <path>` | Audit log path. Default: `~/.podium/audit.log`. |

The command prints the number of audit events dropped.

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
| `--provider <name>` | Signature provider: `registry-managed`, `sigstore-keyless`, or `noop`. Defaults to `PODIUM_SIGNATURE_PROVIDER`, then `registry-managed`. |

The `registry-managed` provider uses one Ed25519 signing keypair per registry deployment, at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`), shared by every process serving that store and across every tenant it serves. `podium sign` takes the `private:` line of that key file and does not read `PODIUM_SIGNATURE_VERIFY_KEY`, and its envelope carries the signing key's `key_id` as every registry-managed envelope does. The `sigstore-keyless` provider obtains a short-lived certificate from the Fulcio endpoint at `PODIUM_SIGSTORE_FULCIO_URL` with the OIDC token in `PODIUM_SIGSTORE_OIDC_TOKEN` and signs the content hash. It then obtains an RFC 3161 timestamp over the signature from `PODIUM_SIGSTORE_TSA_URL` and records the signature in the Rekor v2 transparency log at `PODIUM_SIGSTORE_REKOR_URL`. The three endpoints default to the Sigstore public-good instances. When `PODIUM_SIGSTORE_OIDC_TOKEN` is unset, `podium sign` exits non-zero with `sign failed: sign: sigstore-keyless not configured` before it contacts any service, and the message names no error code. A Rekor v1 URL fails the signing. The envelope carries the log entry, its inclusion proof, the signed checkpoint, and the timestamp, so `podium verify` can check them without contacting any service. `--provider noop` signs a placeholder that `podium verify` always refuses. A `registry-managed` invocation that cannot resolve the key it needs exits non-zero naming `config.signature_provider_unavailable`. `podium verify <artifact> --signature <envelope>` checks the envelope the `<artifact>` form prints against the artifact's resolved content hash.

### `podium verify`

Ad-hoc signature verification. The `<artifact>` form without `--signature` resolves the artifact's delivery hash and delivery signature through the registry and verifies that pair, which is the pair `podium-mcp` verifies on a load. It verifies the pair under the verification key set the resolution order below finds, and it refuses a response that carries no delivery hash or no delivery signature. With `--signature`, the `<artifact>` form verifies the explicit envelope against the artifact's resolved content hash, which is what `podium sign <artifact>` signs. The `--content-hash` plus `--signature` form verifies an explicit pair. Exits 0 on a valid signature and 1 on a mismatch or other error.

```
podium verify <artifact> [--registry <url>] [--provider <name>] [--signature <envelope>]
podium verify --content-hash sha256:<hex> --signature <envelope> [--provider <name>]
```

| Flag | Effect |
|:--|:--|
| `--registry <url>` | Registry URL used to resolve the `<artifact>` form. Defaults to `PODIUM_REGISTRY`. |
| `--content-hash sha256:<hex>` | Verify against this content hash directly, instead of resolving an artifact. |
| `--signature <envelope>` | Signature envelope to verify. Pairs with `--content-hash`. In the `<artifact>` form it is verified against the resolved content hash in place of the served delivery signature. |
| `--provider <name>` | Signature provider: `registry-managed`, `sigstore-keyless`, or `noop`. Defaults to `PODIUM_SIGNATURE_PROVIDER`, then `registry-managed`. |

The `registry-managed` provider resolves the verification key set from `PODIUM_SIGNATURE_VERIFY_KEY` when that variable is set, as one base64 key or a comma-separated list, and otherwise from the `public:` line and every `verify:` line of the key file at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`). A signature that verifies under any key of the set is accepted, and the envelope's `key_id` selects the key tried first without refusing an envelope another key of the set verifies. A set variable with an entry that is empty or does not decode is an error naming it. `podium verify` reads no private key. An invocation that cannot resolve the key exits non-zero naming `config.signature_provider_unavailable`. `--provider noop` refuses every envelope. The delivery signature is always a registry-managed envelope, so the form without `--signature` verifies with `--provider registry-managed`, and `--provider sigstore-keyless` refuses it.

Under `--provider sigstore-keyless`, `podium verify` checks the following against `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`:

- The timestamp verifies under a timestamp authority in the file, is signed by a certificate whose extended key usage lists time stamping, and covers the signature.
- The inclusion proof leads from the log entry to a checkpoint signed by a transparency-log key in the file.
- The log entry records the content hash, the signature, and the envelope's certificate.
- The certificate chains to a certificate authority in the file, lists code signing in its extended key usage, and was valid at the timestamp's time.
- The signature verifies.
- One of the certificate's email or URI subject alternative names appears in `PODIUM_SIGSTORE_CERT_IDENTITY`, and the certificate's OIDC issuer equals `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`.

With either policy variable unset, every envelope is refused. Validity is checked at the timestamp's time, so an envelope keeps verifying after its certificate expires. The command checks no witness cosignature or consistency proof, and it makes no network call. An envelope that carries no log entry or no timestamp, including every envelope produced before this format, is refused. A certificate whose identity is compromised after it signed keeps verifying, because Sigstore-keyless certificates carry no revocation.

The MCP server verifies the delivery signature on every artifact it loads under the default `PODIUM_VERIFY_SIGNATURES=always`, after it recomputes the delivery hash, and a signing registry verifies each stored signature before it serves the row.

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

The cache lives at `~/.podium/cache/` by default (override with `PODIUM_CACHE_DIR`). Content cache entries are immutable; safe to prune by age.

### `podium cache reset-revisions`

Deletes the revision marks `podium-mcp` keeps for `latest` loads.

```
podium cache reset-revisions [--dir <path>] [<artifact-id>...]
```

| Flag or argument | Effect |
|:--|:--|
| `--dir <path>` | Cache directory. Defaults to `PODIUM_CACHE_DIR`, then `~/.podium/cache`. |
| `<artifact-id>...` | Delete only the marks for these artifact IDs, under every registry. With no IDs, delete every mark. |

When the cache directory holds no marks, the command prints `cache: no revision marks under <path>` and exits 0. Otherwise it prints `cache: reset <n> revision mark(s)` and exits 0, with `<n>` 0 when no mark matches the given IDs. While a running `podium-mcp` holds the cache's index, the command exits 1; stop every MCP server that uses the cache directory and run it again. A `podium-mcp` that could not open the index holds its marks in memory, and they are cleared when that process exits. `podium cache prune` does not touch revision marks.

### `podium quota`

Shows current usage and limits per quota type.

```
podium quota [--registry <url>]
```

`--registry` defaults to `PODIUM_REGISTRY` and is required.

Quotas: storage, search QPS, materialization rate, audit volume, user-defined-layer cap.

---

## JSON output

Most read commands accept `--json` for piping into other tools. The read CLI's envelopes are not the raw wire responses documented in [HTTP API](http-api): `podium domain search --json` keys the ranked domains under `results` where the wire response uses `domains`, `podium search --json` and `podium artifact show --json` deliver `frontmatter` as a parsed object where the wire response delivers a raw string, and `podium artifact show --json` renames the wire's `manifest_body` to `body`.

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
| `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` | Registry-process boot setting, environment only and no config-file key. Deadline on each object-storage read made by the first-start rewrite of stored content hashes, by `sign-stored-rows`, and by the registry when it admits a stored row before serving it, 30 seconds by default. An unset, unparsable, or non-positive value takes the default, so no configuration removes the bound. |
| `PODIUM_VERIFY_SIGNATURES` | `never` or `always` (default). Read by `podium-mcp`. Under `always`, a load whose artifact carries no signature fails with `materialize.signature_missing`; under either value above `never`, a signature the response carries is verified. `never` checks nothing. |
| `PODIUM_SIGNATURE_VERIFY_KEY` | The verification key set the `registry-managed` provider verifies with, in `podium-mcp` and `podium verify`: one base64 Ed25519 public key or a comma-separated list of them. When set it is authoritative, and an entry that is empty or does not decode is an error naming it in `podium verify`, and in `podium-mcp` under a policy above `never`. When unset, the set comes from the `public:` line and every `verify:` line of the key file at `PODIUM_SIGN_KEY_PATH`. |
| `PODIUM_SIGN_KEY_PATH` | Registry signing key file, default `~/.podium/standalone/registry-signing.key`. It carries the signing keypair on `private:` and `public:` lines and zero or more verification-only `verify:` lines. The registry reads it, or generates it on first run, when signing is on, and `sign-stored-rows` reads it and never generates it. `podium admin signing-key` takes its path from `--key-file` and does not read this variable. `podium sign` reads its `private:` line, and `podium verify` and `podium-mcp` read its `public:` line and every `verify:` line when `PODIUM_SIGNATURE_VERIFY_KEY` is unset. |
| `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` | Trust root for the `sigstore-keyless` provider: a Sigstore `trusted_root.json` carrying the certificate authorities, the transparency-log keys, and the timestamp authorities, which Sigstore publishes through its TUF repository. Podium does not fetch the file or verify TUF metadata. Verification uses each entry only within its `validFor` window. `podium sign` and `podium verify` read it under `--provider sigstore-keyless` whatever the policy. An unset or unreadable value does not refuse the command. `podium verify` then refuses each envelope as `materialize.signature_invalid`, and it does the same when the file does not parse or lacks a certificate authority, a usable ECDSA P-256 or Ed25519 log key, or a timestamp authority. |
| `PODIUM_SIGSTORE_CERT_IDENTITY` | The signer identities `podium verify --provider sigstore-keyless` accepts: one or more email addresses or URIs, separated by commas, each compared byte for byte with the certificate's email and URI subject alternative names. A certificate whose only subject alternative name is an otherName, such as a Fulcio username identity, is refused: Fulcio marks that extension critical, so the chain check refuses it with `cert chain`. Unset, every keyless envelope is refused. `podium-mcp` does not read it. |
| `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` | The OIDC issuer URL `podium verify --provider sigstore-keyless` accepts, compared byte for byte with the issuer the certificate records after surrounding whitespace is removed from the value. Unset or blank, every keyless envelope is refused. `podium-mcp` does not read it. |
| `PODIUM_SIGSTORE_FULCIO_URL` | Fulcio endpoint `podium sign --provider sigstore-keyless` requests the signing certificate from. Unset or empty, it takes `https://fulcio.sigstore.dev`. |
| `PODIUM_SIGSTORE_REKOR_URL` | Rekor v2 log `podium sign --provider sigstore-keyless` records the entry in. Unset or empty, it takes `https://log2025-1.rekor.sigstore.dev`. A Rekor v1 URL fails the signing. |
| `PODIUM_SIGSTORE_TSA_URL` | RFC 3161 timestamp authority `podium sign --provider sigstore-keyless` requests the timestamp from. Unset or empty, it takes `https://timestamp.sigstore.dev/api/v1/timestamp`. |
| `PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE` | Read by `podium-mcp`, environment only. The Python and Node versions the host advertises to the runtime gate, given as dot-separated digits such as `3.11.4`. A non-empty value activates the gate. Leading and trailing spaces and tabs are ignored when the value is compared, so a value of only spaces activates the gate and fails every requirement for that runtime. A `>=X` requirement compares numeric parts; any other requirement must equal the value. |
| `PODIUM_HOST_PACKAGES` | Read by `podium-mcp`, environment only. Comma-separated system packages the host advertises, for example `jq,curl`. Entries are trimmed, and empty entries are dropped. A requirement matches an entry exactly, including case. A non-empty list activates the gate. |
| `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` | Read by `podium-mcp`, environment only. `true` activates the runtime gate without advertising a capability, so a requirement for an unadvertised runtime or package fails with `materialize.runtime_unavailable`. Any other value, including `TRUE` and `1`, leaves enforcement off. |
| `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` | Read by `podium-mcp`, environment only. `true` admits an artifact whose runtime requirements are unsatisfied and writes a `WARN:` line to standard error naming the artifact and the advertised capabilities. It applies only while the gate is active and has no effect otherwise. It takes precedence over `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`. Any value other than `true` leaves the override off. |
| `PODIUM_IDENTITY_PROVIDER` | Consumer side (MCP server and SDKs): `oauth-device-code` (default) or `injected-session-token`. Registry process: `injected-session-token`, `oidc-jwt`, or `trusted-headers`. `oauth-device-code` has no server-side verifier, so setting it on the registry aborts startup with `config.identity_provider_unverified`. |
| `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` | OAuth provider config. `PODIUM_OAUTH_AUDIENCE` carries the audience both acquisition flows send: the device-code flow sends the value the client resolves, and the registry's browser sign-in redirect sends the first value the registry resolved. The registry process reads the variable as a comma-separated set of audiences it accepts, while a client process sends the value verbatim as the one audience it asks for, so a client sharing the registry's environment needs `--audience` or an environment of its own. `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` is the device-authorization endpoint of the device-code flow, and the browser flow does not read it; the browser flow redirects to `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` alone, and a configuration that sets the device-code key and leaves the web-UI one empty aborts startup with `config.web_ui_auth_unconfigured`. |
| `PODIUM_WEB_UI_OAUTH_CLIENT_ID`, `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, `PODIUM_WEB_UI_REDIRECT_URI`, `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` | Registry-process boot settings, environment only and no `podium serve` flag. The browser flow's acquisition values: the OAuth client identifier and credential the registry presents, the callback URL the IdP returns the browser to, and the IdP endpoints the sign-in route redirects to and the callback exchanges the code at. Each is required where `--web-ui-auth` is set. |
| `PODIUM_WEB_UI_OAUTH_SCOPES` | Registry-process boot setting, environment only. Space-delimited scope set the browser sign-in redirect sends. Default `openid profile email groups`, which is the set both shipped acquisition paths default to, because a token issued without the scope carrying the group claim narrows every group-scoped visibility decision for that caller. |
| `PODIUM_WEB_UI_OAUTH_EXCHANGE_TIMEOUT` | Registry-process boot setting, environment only. Deadline on the callback's token-endpoint request, 10 seconds by default. An unset, unparsable, or non-positive value takes the default, so no configuration removes the bound. |
| `PODIUM_SESSION_TOKEN`, `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE` | Injected-token sources. `PODIUM_SESSION_TOKEN` is the default variable the CLI, the MCP server, and the SDKs read; `PODIUM_SESSION_TOKEN_ENV` names a different variable to read it from, and `PODIUM_SESSION_TOKEN_FILE` names a file to read it from. |
| `PODIUM_TOKEN` | Registry credential a `kind: marketplace` sync target renders under. When set it takes precedence over the session token and the `podium login` keychain token for that render. |
| `PODIUM_PUBLIC_MODE` | Equivalent of `--public-mode`. |
| `PODIUM_NO_AUTOSTANDALONE` | Disable zero-flag standalone fallback. |
| `PODIUM_CONFIG_FILE` | Registry-process boot setting. Path of the `registry.yaml` the registry reads. When it is unset, the registry reads `~/.podium/registry.yaml`. `podium serve --config <path>` sets it for the process, and `podium-server` has no `--config` option. A named file that does not exist aborts startup. |
| `PODIUM_MULTI_TENANT` | Registry-process boot setting. When `true`, the registry runs in multi-tenant mode and routes each request to the tenant its organization names; the `podium admin tenant` commands and the `/v1/admin/tenants` endpoints are available. When unset, every request binds to the single `default` org and tenant management is rejected. |
| `PODIUM_RUNTIME_KEYS_PATH` | Registry-process boot setting. Path to the JSON file holding the trusted runtime signing keys, written with `podium admin runtime register --keys-file`. The registry reads it before it binds a listener and never writes it. A key added to the file takes effect at the next process start. A file the registry cannot read or parse aborts startup with `config.runtime_keys_unavailable` under every identity provider; under `injected-session-token` an unset path, or a path naming a file that carries no key, aborts startup with the same code. |
| `PODIUM_SCIM_TOKENS` | Registry-process boot setting, environment only and no config-file key. Comma-separated list of the bearer tokens the SCIM 2.0 receiver at `/scim/v2/` accepts. Each entry is trimmed and blank entries are dropped. With no token listed, the registry mounts no receiver. Whether the pushed directory feeds layer `groups:` filters depends on the identity provider, as [OIDC identity](../deployment/oidc/) states. |
| `PODIUM_SCIM_STORE_PATH` | Registry-process boot setting, environment only and no config-file key. Path to the JSON file that persists the SCIM directory across restarts; unset, the directory is held in memory and lost on restart. The registry creates the parent directory at startup and the file on the first push. A missing or empty file loads as an empty directory. A file the registry cannot read for a reason other than its absence, a non-empty file that does not parse, a parent directory it cannot create, and a parent directory in which it cannot create a file abort startup with `config.scim_store_unavailable`, under every identity provider and whether or not the receiver is mounted. |
| `PODIUM_AUDIT_LOG_PATH` | Registry-process boot setting, environment only and no config-file key. The registry audit sink: a file path, or an `http://` or `https://` endpoint. Unset, the registry writes `~/.podium/audit.log`. `PODIUM_AUDIT_SINK` is the separate `podium-mcp` local sink and has no effect on the registry. A file path whose parent directory cannot be created, or whose existing file cannot be read, leaves the registry with no audit sink and a startup warning, or aborts startup with `config.audit_sink_unavailable` when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0. |
| `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` | Registry-process boot setting, environment only and no config-file key. Interval in seconds between local chain-head anchors. `0`, the default, disables anchoring, and a negative or non-integer value is treated as `0`. Anchoring runs only when the registry audit sink named by `PODIUM_AUDIT_LOG_PATH` is a file path or unset. An `http(s)` value disables anchoring with a startup warning. A file sink the registry cannot open aborts startup with `config.audit_sink_unavailable` while anchoring is enabled, and is logged as a warning while it is disabled. `PODIUM_AUDIT_SINK` does not affect anchoring. |
| `PODIUM_AUDIT_SIGNING_KEY_PATH` | Registry-process boot setting, environment only and no config-file key. Path to the anchor key file, default `~/.podium/standalone/audit.key`, generated when anchoring runs and the file is absent. Read only when anchoring runs. A file that cannot be read, parsed, or generated aborts startup with `config.audit_anchor_key_unavailable`. While registry signing is on (`PODIUM_SIGN` or `podium serve --sign` resolves to `registry-key`), an anchor key that the `PODIUM_SIGN_KEY_PATH` file also carries as its `public:` key or a `verify:` key aborts startup with `config.audit_anchor_key_shared`. |
| `PODIUM_OPERATOR_ADMINS` | Registry-process boot setting. Comma-separated identities granted the instance-operator role at boot. The operator role authorizes the `podium admin tenant` commands and the `/v1/admin/tenants` endpoints; it confers no per-tenant `admin` rights. Distinct from `PODIUM_BOOTSTRAP_ADMINS`, which seeds per-tenant `admin` grants. |

Server-side backend selection variables (`PODIUM_VECTOR_BACKEND`, `PODIUM_EMBEDDING_PROVIDER`, etc.) are documented alongside the corresponding backend in [Extending](../deployment/extending).
