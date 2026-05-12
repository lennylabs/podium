# 2. Architecture

## 2.1 High-Level Component Map

The registry is the system of record for artifacts. It can be reached two ways: as an external Podium server (§13.10) or as a local filesystem path (§13.11). The diagram below shows the server deployment. See §7.1 for the dispatch and what each mode covers.

The consumers read from the registry over HTTP: the Podium MCP server (in-process bridge for MCP-speaking hosts), `podium sync` (filesystem delivery for harnesses that load artifacts directly from disk), and the language SDKs (programmatic access for non-MCP runtimes). All speak the same registry HTTP API, share identity providers, and apply the same layer composition and visibility filtering.

`podium sync` is also the only consumer that works against a filesystem-source registry (eager materialization, no HTTP).

```
                          ┌───────────────────────────┐
                          │ PODIUM REGISTRY (service) │
                          │  control plane (HTTP/JSON)│
                          │  data plane (object store)│
                          │  Postgres + pgvector      │
                          │  layer composition +      │
                          │    visibility filtering   │
                          │  dependency graph         │
                          │  (centralized,            │
                          │   multi-tenant)           │
                          └───────────▲───────────────┘
                                      │
                       OAuth-attested │ identity (every call)
                                      │
              ┌───────────────────────┼───────────────────────────┐
              │                       │                           │
   ┌──────────┴──────────┐ ┌──────────┴──────────┐ ┌──────────────┴──────────┐
   │ Podium MCP server   │ │ podium sync         │ │ Language SDKs           │
   │   load_domain ·     │ │  one-shot or watch; │ │  podium-py, podium-ts   │
   │   search_domains ·  │ │  writes effective   │ │  thin client over the   │
   │   search_artifacts ·│ │  view to disk in    │ │  registry HTTP API      │
   │   load_artifact     │ │  harness-native     │ │ + IdentityProvider      │
   │ + IdentityProvider  │ │  layout             │ │                         │
   │ + LocalOverlayProv. │ │ + HarnessAdapter    │ │                         │
   │ + HarnessAdapter    │ │ + cache             │ │                         │
   │ + cache + materlz.  │ │                     │ │                         │
   └──────────▲──────────┘ └──────────▲──────────┘ └──────────▲──────────────┘
              │                       │                       │
              │ MCP (stdio)           │ filesystem            │ HTTP
              │                       │                       │
   ┌──────────┴──────────┐ ┌──────────┴──────────┐ ┌──────────┴──────────────┐
   │ MCP-speaking host   │ │ File-based harness  │ │ Non-MCP runtime         │
   │ (any agent runtime) │ │ or runtime          │ │ (LangChain, Bedrock,    │
   │                     │ │                     │ │  custom orchestrators,  │
   │                     │ │                     │ │  eval/build pipelines)  │
   └─────────────────────┘ └─────────────────────┘ └─────────────────────────┘
```

Sequence for `load_artifact` (MCP path):

![load_artifact request sequence: host calls load_artifact on the MCP server, which posts to the registry; the registry composes the view and returns the manifest with presigned URLs; the MCP server fetches resource bytes from object storage, verifies and adapts them, then returns to the host.](../docs/assets/diagrams/load-artifact-sequence.svg)

<!--
ASCII fallback for the diagram above (load_artifact request sequence):

  host           MCP server           registry            object storage
   |  load_artifact(id) |                  |                    |
   |==================>|                  |                    |
   |                    | POST /artifacts  |                    |
   |                    |  (id, identity)  |                    |
   |                    |================>|                    |
   |                    |                  | visibility +       |
   |                    |                  | layer compose      |
   |                    | {manifest,       |                    |
   |                    |  presigned URLs} |                    |
   |                    |<================|                    |
   |                    | GET (presigned)                       |
   |                    |======================================>|
   |                    | resource bytes                        |
   |                    |<======================================|
   |                    | verify + adapt +                      |
   |                    | atomic write to host                  |
   | {manifest,         |                                       |
   |  materialized}     |                                       |
   |<==================|                                       |
-->


Two deployment scenarios share the same MCP server binary:

- **Managed agent runtime.** The runtime spawns the MCP server as a co-located process. Identity is supplied via an injected session token (signed JWT); the registry endpoint is configured the same way. The workspace local overlay is unset.
- **Developer's host.** The host spawns one MCP server per workspace as a stdio subprocess. The MCP server uses an OAuth device-code flow on first use (surfaced via MCP elicitation) to obtain a registry token, stored in the OS keychain. The workspace local overlay reads from `${WORKSPACE}/.podium/overlay/`.

## 2.2 Component Responsibilities

**Podium Registry** _(centralized service)_. The system of record for artifacts. Composes the caller's effective view from the configured layer list per OAuth identity, applies per-layer visibility, indexes manifests, runs hybrid search, signs URLs for resource bytes, maintains the cross-type dependency graph, emits change events. Backed by Postgres + pgvector for metadata, object storage for resource bytes, and an HTTP/JSON API for callers.

The registry's wire protocol is **HTTP/JSON**. Every consumer speaks the same HTTP API. Direct MCP access to the registry is not supported; MCP is a consumer surface that translates HTTP responses into a runtime-appropriate format.

**Podium MCP server** _(in-process bridge for MCP-speaking hosts)_. Single binary. Exposes the meta-tools. Holds no per-session server-side state; local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

**`podium sync`** _(filesystem delivery for harnesses that read artifacts directly from disk)_. CLI command (and library) that reads the user's effective view from the registry and writes it to a host-configured layout via the configured `HarnessAdapter`. One-shot or `--watch` mode (subscribes to registry change events). Reuses the same identity providers and content cache as the MCP server. See §7.5.

**Language SDKs (`podium-py`, `podium-ts`)** _(programmatic access for non-MCP runtimes)_. Thin clients over the registry HTTP API. Used by LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks. See §7.6.

Pluggable interfaces shared across the consumers:

- **IdentityProvider**: supplies the OAuth-attested identity attached to every registry call. Built-ins: `oauth-device-code` and `injected-session-token`. Additional implementations register through the interface.
- **LocalOverlayProvider**: optional. When configured, reads artifact packages (`ARTIFACT.md` for every type, plus `SKILL.md` for skills) from a workspace filesystem path and merges them as the workspace local overlay (§6.4). Available across every consumer.
- **HarnessAdapter**: translates canonical artifacts into harness-native format at delivery time (MCP materialization or `podium sync` write). Built-ins cover Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, Hermes; `none` (default) writes the canonical layout as-is. See §6.7 for the full roster with documentation links. The SDKs accept a harness parameter on `materialize()`.

**Shared library code (Go).** All the spec-defined logic that operates on artifacts and domains lives in a single Go module that every Go-built component imports. This includes manifest parsers (`ARTIFACT.md`, `SKILL.md`, `DOMAIN.md`), the glob resolver, `LayerComposer`, the `extends:` resolver, the visibility evaluator, the `HarnessAdapter` interface and its built-in adapters, materialization (atomic write), and lint rules. The registry binary embeds the module behind the HTTP API. The MCP server and `podium sync` in server-source mode are thin HTTP clients that call the registry, then invoke the same module's materialization writer locally. `podium sync` in filesystem-source mode (§13.11) calls the module's parser, composer, and writer functions directly, skipping HTTP. There is a single canonical implementation per concern: the same composer, parsers, merge logic, `extends:` resolution, and harness output run in every deployment mode. This is the structural reason migrations between deployment modes (e.g., §13.11.6: filesystem source → standalone server pointed at the same directory) preserve behavior with no separate validation surface. The language SDKs (`podium-py`, `podium-ts`) are independent HTTP clients in their own languages and do not share this module; they only work against a Podium server (§7.6).

Configuration: env vars, command-line flags, or a config file the host/user supplies. See §6.

**Hosts** _(not Podium components)_. Any system that consumes the catalog: MCP-speaking agent runtimes, file-based harnesses, programmatic runtimes. Hosts choose the consumer that fits their architecture.
