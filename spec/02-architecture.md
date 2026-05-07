# 2. Architecture

## 2.1 High-Level Component Map

The registry is the system of record for artifacts. It can be reached two ways — as an external HTTP service (§13.10) or as a local filesystem path (§13.11). The diagram below shows the HTTP-service shape. See §7.1 for the dispatch and what each shape supports.

Three consumer shapes read from the registry over HTTP: the Podium MCP server (in-process bridge for MCP-speaking hosts), `podium sync` (filesystem delivery for harnesses that load artifacts directly from disk), and the language SDKs (programmatic access for non-MCP runtimes). All three speak the same registry HTTP API, share identity providers, and apply the same layer composition and visibility filtering.

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

```
host         MCP server          registry        object storage
 │ load_artifact │                  │                    │
 │──────────────▶│ POST /artifacts  │                    │
 │               │ (id, identity)   │                    │
 │               │─────────────────▶│                    │
 │               │                  │ visibility + layer │
 │               │                  │ compose + version  │
 │               │ {manifest,       │                    │
 │               │  presigned URLs} │                    │
 │               │◀─────────────────│                    │
 │               │ GET <presigned>                       │
 │               │──────────────────────────────────────▶│
 │               │ resource bytes                        │
 │               │◀──────────────────────────────────────│
 │               │ verify + adapt + atomic write to host │
 │ {manifest,    │                                       │
 │  materialized}│                                       │
 │◀──────────────│                                       │
```

Two MCP deployment scenarios use the same MCP server binary:

- **Managed agent runtime.** The runtime spawns the MCP server as a co-located process. Identity is supplied via an injected session token (signed JWT); the registry endpoint is configured the same way. The workspace local overlay is unset.
- **Developer's host.** The host spawns one MCP server per workspace as a stdio subprocess. The MCP server uses an OAuth device-code flow on first use (surfaced via MCP elicitation) to obtain a registry token, stored in the OS keychain. The workspace local overlay reads from `${WORKSPACE}/.podium/overlay/`.

## 2.2 Component Responsibilities

**Podium Registry** _(centralized service)_. The system of record for artifacts. Composes the caller's effective view from the configured layer list per OAuth identity, applies per-layer visibility, indexes manifests, runs hybrid search, signs URLs for resource bytes, maintains the cross-type dependency graph, emits change events. Three persistent stores: Postgres + pgvector, object storage, HTTP/JSON API.

The registry's wire protocol is **HTTP/JSON**. All three consumer shapes speak the same HTTP API. Direct MCP access to the registry is not supported; MCP is one of three consumer surfaces that translate HTTP responses into a runtime-appropriate shape.

**Podium MCP server** _(in-process bridge for MCP-speaking hosts)_. Single binary. Exposes the four meta-tools. Holds no per-session server-side state — local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

**`podium sync`** _(filesystem delivery for harnesses that read artifacts directly from disk)_. CLI command (and library) that reads the user's effective view from the registry and writes it to a host-configured layout via the configured `HarnessAdapter`. One-shot or `--watch` mode (subscribes to registry change events). Reuses the same identity providers and content cache as the MCP server. See §7.5.

**Language SDKs (`podium-py`, `podium-ts`)** _(programmatic access for non-MCP runtimes)_. Thin clients over the registry HTTP API. Used by LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks. See §7.6.

Pluggable interfaces shared across all three consumer shapes:

- **IdentityProvider** — supplies the OAuth-attested identity attached to every registry call. Built-ins: `oauth-device-code` and `injected-session-token`. Additional implementations register through the interface.
- **LocalOverlayProvider** — optional. When configured, reads `ARTIFACT.md` packages from a workspace filesystem path and merges them as the workspace local overlay (§6.4). Available across all three consumer shapes.
- **HarnessAdapter** — translates canonical artifacts into harness-native format at delivery time (MCP materialization or `podium sync` write). Built-ins cover Claude Code, Claude Desktop, Cursor, Gemini, OpenCode, Codex; `none` (default) writes the canonical layout as-is. See §6.7. The SDKs accept a harness parameter on `materialize()`.

**Shared library code (Go).** All the spec-defined logic that operates on artifacts and domains — manifest parsers (`ARTIFACT.md`, `DOMAIN.md`), glob resolver, `LayerComposer`, `extends:` resolver, visibility evaluator, `HarnessAdapter` interface and built-in adapters, materialization (atomic write), lint rules — lives in a single Go module that every Go-built component imports. The registry binary embeds it behind the HTTP API. The MCP server and `podium sync` in server-source mode are thin HTTP clients that call the registry, then invoke the same module's materialization writer locally. `podium sync` in filesystem-source mode (§13.11) calls the module's parser, composer, and writer functions directly, skipping HTTP. There is one canonical implementation per concern, not three — same composer, same parsers, same merge, same `extends:` resolution, same harness output. This is the load-bearing reason migrations between deployment shapes (e.g., §13.11.6: filesystem source → standalone server pointed at the same directory) preserve behavior with no separate validation surface. The language SDKs (`podium-py`, `podium-ts`) are independent HTTP clients in their own languages and do not share this module; they only work against an HTTP server (§7.6).

Configuration: env vars, command-line flags, or a config file the host/user supplies. See §6.

**Hosts** _(not Podium components)_. Any system that consumes the catalog: MCP-speaking agent runtimes, file-based harnesses, programmatic runtimes. Hosts choose the consumer shape that fits their architecture.
