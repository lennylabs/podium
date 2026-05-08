# 6. MCP Server

## 6.1 The Bridge

The Podium MCP server is a thin in-process bridge. It exposes the meta-tools to the host's runtime over MCP and forwards calls to the registry. It holds no per-session server-side state. Local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

A single Go binary serves every deployment context. The host configures it via env vars, command-line flags, or a config file.

**Requires a server-source registry.** The MCP server speaks HTTP and does not work against a filesystem-source registry (§13.11).

## 6.2 Configuration

Top-level configuration parameters (env-var form shown; `--flag` and config-file equivalents are accepted):

| Parameter                    | Description                                                                                                                      | Default                                                                                       |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `PODIUM_REGISTRY`            | Registry source: URL (HTTP) or filesystem path. See §7.5.2 for dispatch.                                                         | (read from `sync.yaml`'s `defaults.registry` per §7.5.2 if unset; required if neither is set) |
| `PODIUM_IDENTITY_PROVIDER`   | Selected identity provider implementation                                                                                        | `oauth-device-code`                                                                           |
| `PODIUM_HARNESS`             | Selected harness adapter                                                                                                         | `none` (write canonical layout as-is)                                                         |
| `PODIUM_OVERLAY_PATH`        | Workspace path for the `local` overlay                                                                                           | (unset → layer disabled)                                                                      |
| `PODIUM_CACHE_DIR`           | Content-addressed cache directory                                                                                                | `~/.podium/cache/`                                                                            |
| `PODIUM_CACHE_MODE`          | `always-revalidate` / `offline-first` / `offline-only`                                                                           | `always-revalidate`                                                                           |
| `PODIUM_AUDIT_SINK`          | Local audit destination (path or external endpoint). When set without a value (or set to `default`), uses `~/.podium/audit.log`. | (unset → registry audit only)                                                                 |
| `PODIUM_MATERIALIZE_ROOT`    | Default destination root for `load_artifact`                                                                                     | (host specifies per call)                                                                     |
| `PODIUM_PRESIGN_TTL_SECONDS` | Override for presigned URL TTL                                                                                                   | 3600                                                                                          |
| `PODIUM_VERIFY_SIGNATURES`   | Verify artifact signatures on materialization                                                                                    | `medium-and-above`                                                                            |

Provider-specific options are passed as additional env vars (e.g., `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_ENV`).

## 6.3 Identity Providers

Identity providers attach the caller's OAuth-attested identity to every registry call.

- **`oauth-device-code`** _(default)_. Interactive device-code flow on first use; tokens cached in the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux). Refreshes transparently. Defaults: access-token TTL 15 min, refresh-token TTL 7 days, revocation propagation ≤60s. Options: `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_TOKEN_KEYCHAIN_NAME`.

  How the verification URL surfaces depends on the consumer:
  - **MCP server** uses MCP elicitation; the host displays the URL and code in the agent UI.
  - **`podium sync`, `podium login`, and other CLI commands** print the URL and code to stderr, attempt to open the URL in the system browser (via `open` on macOS, `xdg-open` on Linux, `start` on Windows), and poll the IdP's token endpoint until the user completes the flow or a 10-minute timeout elapses. `--no-browser` skips the auto-open. Output is suppressed under `--json`; the prompt is replaced with a structured `auth.device_code_pending` event emitted on stderr.
  - **SDK** raises `DeviceCodeRequired` with the URL and code; calling code is responsible for surfacing it to the user. `Client.login()` performs the same blocking poll-until-completion the CLI uses.

- **`injected-session-token`**. The MCP server reads a signed JWT from an env var or file path configured by the runtime. The runtime is responsible for token issuance and refresh. Options: `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE`.
- **(Extensible.)** Additional implementations register through the `IdentityProvider` interface (§9).

### 6.3.1 Claim Derivation

The IdP returns a JWT with claims `{sub, org_id, email, exp, iss, aud}`. Team membership is resolved registry-side via SCIM 2.0 push from the IdP; the registry maintains a directory of `(user_id → teams)`.

For IdPs without SCIM, the `IdpGroupMapping` adapter reads OIDC group claims from the token and maps them to team names per a registry-side configuration.

Tested IdPs: Okta, Entra ID, Auth0, Google Workspace, Keycloak. SAML supported via OIDC bridge.

Fine-grained narrowing via OAuth scope claims (e.g., `podium:read:finance/*`, `podium:load:finance/ap/pay-invoice@1.x`); narrow scopes intersect with the caller's layer visibility, and the smaller surface wins.

### 6.3.2 Runtime Trust Model (`injected-session-token`)

The injected token is a JWT signed by a runtime-specific signing key registered with the registry one-time at runtime onboarding. The registry verifies the signature on every call. Required claims:

- `iss`: runtime identifier (must match a registered runtime).
- `aud`: registry endpoint.
- `sub`: user id the runtime is acting on behalf of.
- `act`: actor (the runtime itself).
- `exp`: expiry.

Without a registered signing key, the registry rejects with `auth.untrusted_runtime`.

#### 6.3.2.1 Token Rotation Contract

- Env-var change is observed at next registry call (no signal needed; the MCP server reads fresh on every call).
- SIGHUP triggers a forced re-read.
- `PODIUM_SESSION_TOKEN_FILE` is watched via fsnotify and re-read on change.

Token rotation is the runtime's responsibility; the MCP server's only obligation is to read fresh on every call. Recommended TTLs: ≤15 min. Prefer `PODIUM_SESSION_TOKEN_FILE` over env var when the runtime can write to a file with restrictive permissions.

## 6.4 Workspace Local Overlay

The workspace local overlay is a per-developer set of artifact packages (`ARTIFACT.md` for every type, plus `SKILL.md` for skills) and `DOMAIN.md` files that merge as the **highest-precedence layer in the caller's effective view** (§4.6). It's the path most teams use for in-progress work that isn't ready to share.

**Path resolution.** Every consumer (MCP server, `podium sync`, language SDKs) honors the same lookup:

1. `PODIUM_OVERLAY_PATH` if set (`Client(overlay_path=...)` on the SDK takes precedence over the env var).
2. The MCP server falls back to MCP roots when available: the `roots/list` response identifies the workspace, and the overlay defaults to `<workspace>/.podium/overlay/` if that directory exists.
3. `podium sync` and the SDK fall back to `<CWD>/.podium/overlay/` if that directory exists.
4. Otherwise: layer disabled.

The MCP server watches the resolved path via fsnotify and re-indexes on change. `podium sync` reads it once per invocation and again on each watcher event when `--watch` is set. The SDK reads it on each `Client.search_artifacts` and `Client.load_artifact` call (cached for the duration of a `session_id`).

Format: same `ARTIFACT.md` (plus `SKILL.md` for skills) and frontmatter as the registry; merge semantics are identical to registry-side layers.

The workspace local overlay is **orthogonal to the registry-side `local` source type** (§4.6): the workspace overlay is merged in by the consumer (MCP server, sync, or SDK) and is visible only to the developer running it, while a registry-side `local`-source layer is read by the registry process and surfaced to whichever identities the layer's visibility declaration allows.

To promote a workspace artifact to a shared layer, copy it into the appropriate Git repo (or registry-side `local` path), commit, and merge.

### 6.4.1 Local Search Index

When `LocalOverlayProvider` is configured, the MCP server maintains a local BM25 index over local-overlay manifest text. `search_artifacts` calls fan out to both the registry and the local index; the MCP server fuses results via reciprocal rank fusion before returning.

The default is BM25-only. Local artifacts have lower recall on semantic queries than registry artifacts, which is acceptable for the developer iteration loop where the goal is "find my draft," not "outrank everything else." Authors who want better local recall can configure the MCP server with an external embedding provider and a vector store via the `LocalSearchProvider` SPI (§9.1). Backends include `sqlite-vec` (embedded, single-file; matching the standalone registry's default in §13.10), a local pgvector instance, or a managed service (Pinecone, Weaviate Cloud, Qdrant Cloud). Cost and identity for any external service are the operator's to manage.

## 6.5 Cache

Disk cache at `${PODIUM_CACHE_DIR}/<sha256>/`. Two cache layers:

- **Resolution cache.** Maps `(id, "latest")` to `semver`. TTL 30s by default. Revalidated via HEAD on hit when `PODIUM_CACHE_MODE=always-revalidate`.
- **Content cache.** Maps `content_hash` to manifest bytes + bundled resources. Forever (immutable by definition).

Cache modes (set at server startup via `PODIUM_CACHE_MODE`):

- `always-revalidate` (default): HEAD-revalidate the resolution cache on every call.
- `offline-first`: use cached resolution and content if present; only call the registry on miss.
- `offline-only`: never call the registry; cache only.

Index DB: BoltDB or SQLite. `podium cache prune` for cleanup.

In contexts where the home directory is ephemeral, the host points `PODIUM_CACHE_DIR` at an ephemeral or shared volume.

## 6.6 Materialization

On `load_artifact(<id>)`, the registry returns the canonical manifest body inline (or via presigned URL if above the inline cutoff) and presigned URLs for bundled resources. Materialization on the MCP server runs in five steps:

1. **Fetch.** The MCP server downloads each resource (or reads it from the cache) into a temporary staging area. On 403/expired during fetch, retries with a fresh URL set (max 3 attempts, exponential backoff).
2. **Verify.** Signature verification (per `PODIUM_VERIFY_SIGNATURES`); content_hash match; SBOM walk if vulnerability tracking is enabled.
3. **Adapt.** The configured `HarnessAdapter` (§6.7) translates the canonical artifact into the harness's native layout (file names, frontmatter conventions, directory structure) without changing the underlying bytes of bundled resources unless the adapter declares it needs to.
4. **Hook.** Configured `MaterializationHook` plugins (§9.1) run in declared order over the adapter's output, with read+rewrite access to per-file bytes plus the manifest for context. Each hook can rewrite a file, drop it, or emit warnings; the next hook receives the previous hook's output. No-op when no hooks are configured. Hooks share the adapter sandbox contract (§6.7): no network, no subprocess, no writes outside the materialization destination.
5. **Write.** The MCP server writes the adapted output atomically to a host-configured destination path (`.tmp` + rename), ensuring the destination either contains a complete copy or nothing.

The destination path comes from the host, either via `PODIUM_MATERIALIZE_ROOT` or per-call in the `load_artifact` arguments.

When `PODIUM_HARNESS=none` (the default), step 3 is a no-op: the canonical layout is written directly. Hosts that want raw artifacts (build pipelines, evaluation harnesses, custom scripts) leave the adapter unset. The hook step (4) runs whether or not an adapter is configured.

## 6.7 Harness Adapters

The `HarnessAdapter` translates a canonical artifact into the format a specific harness expects. It runs at materialization time on the MCP server, between fetch and write.

**Supported harnesses.** The harnesses below ship with a built-in adapter. Each adapter value is selected via `PODIUM_HARNESS` (or via the per-call `harness:` argument). For per-harness specifics about skills, hooks, plugins, and other harness-native concepts, refer to the harness's own documentation; the harness's documentation is the source of truth.

| Adapter value    | Harness | Documentation |
| ---------------- | --- | --- |
| `none`           | Generic / raw output. No harness-specific translation. | n/a |
| `claude-code`    | Anthropic Claude Code (CLI). | [code.claude.com/docs](https://code.claude.com/docs/) |
| `claude-desktop` | Anthropic Claude Desktop (desktop chat app). | [claude.com/download](https://claude.com/download), [Skills in Claude](https://support.claude.com/en/articles/12512180-use-skills-in-claude) |
| `claude-cowork`  | Anthropic Claude Cowork (web product for organizations, claude.ai). | [claude.com/plugins](https://claude.com/plugins), [Manage Cowork plugins](https://support.claude.com/en/articles/13837433-manage-claude-cowork-plugins-for-your-organization) |
| `cursor`         | Cursor IDE. | [cursor.com/docs](https://cursor.com/docs) |
| `codex`          | OpenAI Codex (CLI and IDE). | [developers.openai.com/codex](https://developers.openai.com/codex) |
| `gemini`         | Google Gemini CLI. | [geminicli.com/docs](https://geminicli.com/docs) |
| `opencode`       | OpenCode. | [opencode.ai/docs](https://opencode.ai/docs) |
| `pi`             | Pi (pi-mono coding agent). | [github.com/badlogic/pi-mono](https://github.com/badlogic/pi-mono/tree/main/packages/coding-agent) |
| `hermes`         | Hermes Agent (Nous Research). | [hermes-agent.nousresearch.com/docs](https://hermes-agent.nousresearch.com/docs/) |

The adapter set grows as new harnesses appear. Custom adapters register through the `HarnessAdapter` SPI (§9.1).

**Adapter outputs.** What each built-in adapter writes, by artifact type:

| Adapter          | Target                                                                                                                                                                  |
| ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `none`           | _(default)_ Writes the canonical layout as-is. |
| `claude-code`    | Writes `.claude/agents/<name>.md` (frontmatter + composed prompt) and `.claude/rules/<name>.md` for `type: rule`. Places skill packages at `.claude/skills/<name>/SKILL.md` (per the agentskills.io standard). Places bundled resources for non-skill types under `.claude/podium/<artifact-id>/`. |
| `claude-desktop` | Writes a Claude Desktop extension layout (`manifest.json` derived from canonical frontmatter; resources alongside). |
| `claude-cowork`  | Writes a Claude Cowork plugin layout (`marketplace.json` plus per-plugin folders containing skills, commands, agents, hooks, and MCP server registrations). The output directory tree is intended to be committed to a private GitHub repo that the org admin imports as a private marketplace. |
| `cursor`         | Writes Cursor's native agent / extension format. For `type: rule`, writes `.cursor/rules/<name>.mdc` with `alwaysApply` / `globs` / `description` per `rule_mode`. |
| `codex`          | Writes Codex's native package layout. For `type: rule`, injects into `AGENTS.md` between markers. |
| `gemini`         | Writes Gemini CLI's native package layout. |
| `opencode`       | Writes OpenCode's native package layout. For `type: rule`, injects into `AGENTS.md` between markers (or writes `.opencode/rules/<name>.md` for `rule_mode: explicit`). |
| `pi`             | Writes Pi's native layout. For `type: rule`, injects into `AGENTS.md` (project-local `.pi/AGENTS.md` or root `AGENTS.md`); explicit-mode rules at `.pi/rules/<name>.md`. |
| `hermes`         | Writes Hermes Agent's native layout. For `type: rule`, writes `.claude/rules/<name>.md` (Hermes also reads `.cursor/rules/*.mdc` natively, so cursor-format output works too). |

**What an adapter does.** Mechanical translation:

- Frontmatter mapping (canonical fields → harness equivalents)
- Prose body composition (canonical body → harness's system-prompt section)
- Resource layout (bundled resources → paths the harness expects)
- Type-specific behavior (`type: skill` → skill; `type: agent` → agent definition)

**What an adapter does not do.** Adapters do not invent semantics. Fields the harness has no equivalent for are left out (or carried in an `x-podium-*` extension namespace if the harness tolerates one).

**Configuration per call.** Hosts can override the harness for a single `load_artifact` call by passing `harness: <value>` in the call arguments.

**Adapter sandbox contract.** Adapters MUST be no-network, MUST NOT write outside the materialization destination, MUST NOT spawn subprocesses. Enforced where Go runtime restrictions allow; documented as the contract for community adapters; conformance suite includes negative tests.

**Cache behavior.** The cache stores canonical artifact bytes (§6.5). Adapter output is regenerated on each materialization by default. An optional in-memory memo cache keyed on `(content_hash, harness)` with 5-minute TTL is enabled for sessions that load the same artifact repeatedly.

**Conformance test suite.** Every built-in adapter passes the same set of tests (§11): load a canonical fixture, produce the harness-native output, verify the harness can spawn an agent that uses the materialized artifact end-to-end.

**Versioning.** Adapter behavior is versioned alongside the MCP server binary. Profile and harness combinations that need a newer adapter behavior pin a minimum MCP server version; older binaries refuse to start.

### 6.7.1 The Author's Burden

Adapters can only translate features the target harness supports. Authors who use harness-specific features will get degraded materializations elsewhere.

Two mitigations:

1. **Core feature set.** A documented subset of canonical fields and patterns that all built-in adapters support. Authors writing to the core feature set get true "author once, load anywhere."
2. **Capability matrix.** Per-(field, harness) compatibility table maintained alongside the adapters. Ingest-time lint surfaces capability mismatches: "field `X` is used but adapter `cursor` cannot translate it."

Authors who must use a non-portable feature can declare `target_harnesses:` in frontmatter to opt out of cross-harness materialization for that artifact.

**Capability matrix (excerpt; maintained in sync with adapter implementations).** Legend: ✓ supported natively, ⚠ supported via fallback (lint warning), ✗ not supported (lint error or `target_harnesses:` opt-out required).

| Field                      | claude-code | claude-desktop | claude-cowork | cursor | codex | opencode | gemini | pi  | hermes |
| -------------------------- | ----------- | -------------- | ------------- | ------ | ----- | -------- | ------ | --- | ------ |
| `description`              | ✓           | ✓              | ✓             | ✓      | ✓     | ✓        | ✓      | ✓   | ✓      |
| `mcpServers`               | ✓           | ✓              | ✓             | ✓      | ✓     | ✓        | ✓      | ✓   | ✓      |
| `delegates_to` (subagents) | ✓           | ⚠              | ✓             | ✗      | ⚠     | ✓        | ✗      | ✓   | ✓      |
| `requiresApproval`         | ✓           | ⚠              | ✓             | ✗      | ✓     | ✓        | ✗      | ⚠   | ⚠      |
| `sandbox_profile`          | ✓           | ⚠              | ⚠             | ✗      | ✓     | ✓        | ✗      | ⚠   | ⚠      |
| `expose_as_mcp_prompt`     | ✓           | ✓              | ✓             | ✓      | ✓     | ✓        | ✓      | ✓   | ✓      |
| `rule_mode: always`        | ✓           | ✓              | ✓             | ✓      | ✓     | ✓        | ⚠      | ✓   | ✓      |
| `rule_mode: glob`          | ⚠           | ✗              | ⚠             | ✓      | ⚠     | ⚠        | ✗      | ⚠   | ✓      |
| `rule_mode: auto`          | ⚠           | ✗              | ⚠             | ✓      | ✗     | ✗        | ✗      | ✗   | ⚠      |
| `rule_mode: explicit`      | ✓           | ✓              | ✓             | ✓      | ✓     | ✓        | ⚠      | ✓   | ✓      |
| `hook_event` (any)         | ✓           | ✗              | ⚠             | ✓      | ✓     | ⚠        | ⚠      | ⚠   | ⚠      |

The `hook_event` row summarizes hook support at the field level. Per-event coverage (which canonical events from §4.3.5 each adapter translates) is tracked in the adapter implementation rather than in this spec; the row above marks ✓ when the adapter supports the common events (`session_start`, `session_end`, `pre_tool_use`, `post_tool_use`, `stop`, `pre_compact`) and ⚠ when only a subset of canonical events translate. For the harness's own current event surface, refer to the harness's documentation.

## 6.8 Process Model

The MCP server is a stdio subprocess spawned by its host. The host is responsible for lifecycle (spawn, signal handling, shutdown).

- **Developer hosts.** One subprocess per workspace, spawned when the workspace opens and torn down when the workspace closes.
- **Managed agent runtimes.** One subprocess per session, spawned by the runtime's bootstrap glue alongside the agent.

## 6.9 Failure Modes

| Failure                                       | Behavior                                                                                                                                                    |
| --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Registry offline                              | Serve from cache; return explicit "offline" status on fresh `load_domain` / `search_artifacts`.                                                             |
| Workspace overlay path missing                | Skip the workspace local overlay; warn once.                                                                                                                |
| Auth token expired (`oauth-device-code`)      | Trigger refresh; if interactive refresh required, surface in tool response with reauth instructions via MCP elicitation.                                    |
| Auth token expired (`injected-session-token`) | Surface "token expired"; the host's runtime is responsible for refresh.                                                                                     |
| Untrusted runtime (`injected-session-token`)  | Reject with `auth.untrusted_runtime`. Runtime must register signing key with registry.                                                                      |
| Visibility denial on a call                   | Return a structured error naming the unreachable resource (without leaking the layer's existence); log to the registry audit stream as `visibility.denied`. |
| Materialization destination unwritable        | Fail the `load_artifact` call with a structured error; nothing partial is left on disk.                                                                     |
| Signature verification failure                | Fail with `materialize.signature_invalid`; do not write to disk.                                                                                            |
| Unknown `PODIUM_HARNESS` value                | Refuse to start; CLI lists the available adapter values.                                                                                                    |
| Adapter cannot translate an artifact          | Fail with structured error naming the missing translation; suggest `harness: none` for raw output.                                                          |
| Binary version mismatch with host caller      | Refuse to start; host's CLI prompts an update.                                                                                                              |
| MCP protocol version mismatch                 | Negotiate down to host's max supported MCP version; if no compatible version, fail with `mcp.unsupported_version`.                                          |
| Quota exhausted                               | Structured error (`quota.storage_exceeded` etc.); operation rejected.                                                                                       |
| Runtime requirement unsatisfiable             | Fail with `materialize.runtime_unavailable`; lists the unsatisfied requirement.                                                                             |

## 6.10 Error Model

All errors use a structured envelope:

```json
{
  "code": "auth.untrusted_runtime",
  "message": "Runtime 'managed-runtime-x' is not registered with the registry.",
  "details": { "runtime_iss": "managed-runtime-x" },
  "retryable": false,
  "suggested_action": "Register the runtime's signing key via 'podium admin runtime register'."
}
```

Codes are namespaced (`auth.*`, `ingest.*`, `materialize.*`, `quota.*`, `mcp.*`, `network.*`, `registry.*`, `domain.*`). Mapped to MCP error payloads per the MCP spec.

## 6.11 Host Configuration Recipes

The Podium MCP server is a stdio binary the host spawns alongside its other MCP servers. Each host has its own MCP config format; the snippets below show what to add for the common harnesses. All three reuse the same env-var contract from §6.2.

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS; equivalents on Windows/Linux):

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

**Claude Code** (project-level `.claude/mcp.json` or user-level `~/.claude/mcp.json`):

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

**Cursor** (Settings → MCP, or `~/.cursor/mcp.json`):

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

**OpenCode** (`opencode.json` at the project root or `~/.config/opencode/opencode.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "opencode"
      }
    }
  }
}
```

**Pi** (`~/.pi/mcp.json` or project-local `.pi/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "pi"
      }
    }
  }
}
```

**Hermes** (`~/.config/hermes/mcp.json` or project-local `.hermes/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "hermes"
      }
    }
  }
}
```

**Standalone (no env override).** When `podium serve` has auto-bootstrapped `~/.podium/sync.yaml` with `defaults.registry: http://127.0.0.1:8080` (§13.10), or `podium init --global --standalone` has written it explicitly (§7.7), the MCP server resolves the registry from there and the env var can be omitted.

For other MCP-speaking hosts (custom runtimes, non-major harnesses), the same snippet pattern applies; `PODIUM_HARNESS=none` writes the canonical layout when no harness-specific adapter is configured.
