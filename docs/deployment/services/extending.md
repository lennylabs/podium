---
title: Extending
nav_order: 14
description: Extend a Podium registry with tooling built on its HTTP API, SDKs, CLI, and webhooks, or with in-process plugins that implement its SPIs.
---

# Extending

This page covers the ways to extend a Podium registry, for operators and platform engineers who need behavior the built-in configuration does not provide. It describes the pluggable interfaces (SPIs), the extension patterns built on the registry's public surfaces, and the constraints a plugin author follows.

---

## Choosing an extension route

Podium is extensible at two layers:

- **External extensions** build on the registry's HTTP API, SDKs, CLI, and outbound webhooks. They leave the registry binary unchanged. Examples are curation scripts, webhook receivers, custom CI checks, and layer source bridges.
- **In-process plugins** change the registry's own behavior, such as the store, the identity provider, or the lint rules. A plugin implements a Go interface and is compiled into a registry build.

Start with an external extension. Write an SPI plugin only when the registry's own behavior must change.

| Goal | Route |
|:--|:--|
| Select artifacts with custom logic and materialize the selection | [Programmatic curation](#programmatic-curation-semantic-discovery--scoped-sync) |
| Notify a channel, open a ticket, or start a CI job when the catalog changes | [Webhook-driven integrations](#webhook-driven-integrations) |
| Enforce team-specific rules before a change merges | [Custom pre-merge CI](#custom-pre-merge-ci) |
| Import content from another system into a layer | [Layer source bridges](#layer-source-bridges) |
| Serve a runtime that the built-in consumers do not cover | [Custom consumer surfaces](#custom-consumer-surfaces) |
| Replace a store, search backend, identity provider, signer, or other registry component | An [SPI plugin](#spis) |

---

## SPIs

The registry's pluggable interfaces are listed below. A plugin implements one of them in Go.

| Interface | Purpose | Built-ins |
|:--|:--|:--|
| `RegistryStore` | Stores manifest metadata, dependency edges, layer config, and admin grants. | `sqlite`, `postgres` |
| `RegistryObjectStore` | Stores bundled resource bytes and issues presigned URLs. | The local filesystem and S3-compatible storage |
| `RegistrySearchProvider` | Runs hybrid retrieval for `search_artifacts`. [Vector backends](vector-backends) has the per-backend recipes. | `pgvector`, `sqlite-vec`, `pinecone`, `weaviate-cloud`, and `qdrant-cloud` |
| `EmbeddingProvider` | Generates embeddings for ingest text and query text. | `openai`, `voyage`, `cohere`, and `ollama` |
| `LocalSearchProvider` | Adds optional semantic backing to the local-overlay index. It uses the same SPI as `RegistrySearchProvider`. | The `RegistrySearchProvider` backends |
| `RegistryAuditSink` | Writes the registry's stream of catalog events. See [Audit and observability](observability#audit-destinations). | File, HTTP, OTLP, and stdout destinations |
| `LayerComposer` | Resolves the caller's effective view from the configured layer list and applies the collision rule. | The layer-list composer |
| `LayerSourceProvider` | Resolves and watches the source backing a layer. Custom backends include S3 versioned buckets, OCI registries, HTTP archives, and internal CMS bridges. | `git`, `local` |
| `GitProvider` | Verifies webhook signatures and implements Git fetch for the built-in `git` `LayerSourceProvider`. | GitHub, GitLab, and Bitbucket |
| `TypeProvider` | Defines a type: its frontmatter JSON Schema, lint rules, and adapter hints. | The built-in first-class types |
| `IngestLinter` | Validates manifests, checks resource references, and applies type-specific rules. It runs pre-merge in CI and again at registry ingest. | The built-in rule set |
| `IdentityProvider` | Attaches the caller's attested identity to every registry call. | `oauth-device-code`, `injected-session-token`, `oidc-jwt`, and `trusted-headers` |
| `LocalOverlayProvider` | Supplies the workspace-scoped local overlay layer. | The workspace filesystem (`.podium/overlay/`) |
| `LocalAuditSink` | Writes the local audit log of meta-tool calls, when configured. | A JSON Lines file at `~/.podium/audit.log` |
| `HarnessAdapter` | Translates canonical artifacts to a harness's native format at materialization time. | The built-in harness adapters |
| `MaterializationHook` | Transforms each materialized file before it is written. Uses include redacting secrets, rewriting paths, injecting team-specific headers, and enforcing content policy. | None |
| `NotificationProvider` | Delivers operational notifications. The registry sends one today when a layer ingest fails. See [Notifications](observability#notifications). | `log`, `webhook`, `email`, and `multi` |
| `SignatureProvider` | Signs and verifies artifacts. | `noop`, `registry-managed`, and `sigstore-keyless` |

### Configuration notes

- **`RegistryAuditSink`.** The `audit.Sink` interface is the seam. The stream is logically distinct from `RegistryStore`, separately mockable, and separately routable. An operator lists its destinations (file, HTTP, OTLP, and stdout) in `PODIUM_AUDIT_LOG_PATH`. The file destination `audit.FileSink` is the default in both deployment modes. [Audit events](../reference/audit-events) lists the events.
- **`LocalAuditSink`.** The default file is user-wide, with one file across all workspaces. Redirect it to a workspace-local path, or add HTTP and OTLP destinations, through `PODIUM_AUDIT_SINK`.
- **`IdentityProvider`.** The client-side built-ins acquire the token at the consumer: `oauth-device-code` and `injected-session-token`. The registry-process built-ins resolve identity from the request at the registry: `oidc-jwt` and `trusted-headers`. [Gateway-delegated identity](gateway-delegated-identity) covers the registry-process providers.
- **`HarnessAdapter`.** The adapter `Source` carries a plugin descriptor: the plugin name, an optional description, and the harness subtree prefix. A marketplace emitter uses it to render an artifact into a named plugin when `podium sync` renders a `kind: marketplace` target.
- **`NotificationProvider`.** `PODIUM_NOTIFICATION_PROVIDER` selects the provider. An unset variable and `noop` both wire no notifier. `email` delivers over SMTP and is also accepted as `smtp`. `multi` combines the log provider with whichever of webhook and email is configured.
- **`SignatureProvider`.** `podium sign`, `podium verify`, and `podium-mcp` read `PODIUM_SIGNATURE_PROVIDER` and fall back to `registry-managed`. `podium-mcp` accepts `registry-managed` and `noop` only, because every delivery signature it verifies is registry-managed. The `noop` provider refuses every signature it is asked to verify. The registry signs each accepted manifest at ingest with its registry-managed key by default, and `PODIUM_SIGN=none` or `--sign none` turns ingest signing off.

Marketplace publishing adds no SPI. The git workflow of a `kind: marketplace` sync target is a set of operator-configured shell commands, so there is no write-side git provider. The only SPI surface publishing touches is the `HarnessAdapter` `Source` plugin descriptor. See [Publishing marketplace repositories](../consuming/publishing).

---

## External extensions

The registry's HTTP API, SDKs, CLI, and outbound webhook stream compose into team-specific tooling without changes to the registry binary.

### Programmatic curation (semantic discovery + scoped sync)

A script selects artifacts by whatever context matters to the team: a semantic match against a query, the user's recent work, the active project, or an upstream ticket. The script then runs `podium sync` with `--include` flags to materialize the selected set.

The script owns the discovery logic. Podium owns the materialization, which covers visibility filtering, harness adaptation, and audit. The on-disk result is reproducible from the include list.

[Building custom consumers with the SDK](../consuming/custom-via-sdk#patterns) has a worked example.

### Webhook-driven integrations

Receivers for the outbound webhooks feed Slack channels, ticket trackers, deployment pipelines, and internal dashboards. The registry emits the events, and the receiver decides what to do with them.

The registry delivers these events to a receiver:

- `artifact.published`
- `artifact.deprecated`
- `domain.published`
- `layer.ingested`
- `layer.history_rewritten`

Read events such as `domains.searched` are recorded in the audit log and are never delivered to a receiver.

Common uses:

- Notify owners on `artifact.deprecated`.
- Page on-call on `layer.history_rewritten`.
- Start a downstream rebuild on `artifact.published` for artifacts that match certain paths.

#### Triggering CI from a webhook (repository_dispatch relay)

A common receiver starts a CI job from a registry event. Marketplace publishing is one case: a receiver filtered to `layer.ingested` starts a CI job that runs `podium sync --config` against a `kind: marketplace` target. [Publishing marketplace repositories](../consuming/publishing) covers that target.

GitHub starts a workflow from an external system only through its authenticated REST API (`repository_dispatch` or `workflow_dispatch`). A Podium receiver posts an HMAC-signed event body, which GitHub's dispatch endpoint does not accept. A small operator relay bridges the two:

1. The receiver posts the signed event to the relay.
2. The relay verifies the HMAC against the receiver secret.
3. The relay calls `POST https://api.github.com/repos/<owner>/<repo>/dispatches` with `{"event_type":"podium-layer-ingested"}` and a GitHub token.
4. The workflow, which listens on `repository_dispatch`, runs.

Set the receiver's `debounce` field to coalesce a burst of `layer.ingested` events into one batch delivery and one CI dispatch. With `debounce` unset, the relay fires once per event, and the CI system's own concurrency control collapses the redundant runs.

Registering the receiver requires the per-tenant admin role on the receiver CRUD endpoints. They return `auth.forbidden` to a caller without it. [HTTP API → Outbound webhooks](../reference/http-api#outbound-webhooks) documents the receiver fields, the SSRF policy on the receiver URL, and the batch envelope.

### Custom pre-merge CI

Each layer's source repository runs the CI checks the team chooses, such as naming conventions, sensitivity sign-off, banned dependencies, and structural rules. The checks combine `podium lint` with team-specific scripts. They are outside Podium's scope: they run as ordinary CI in the layer's source repository and are gated by branch protection.

### Layer source bridges

A bridge is a script that pulls content from another system, such as a vendor SaaS, an internal CMS, or a documentation generator. It writes that content into the filesystem path of a `local`-source layer. The registry ingests it on `podium layer reingest <id>`, which an operator runs manually or the bridge runs on its own schedule.

The bridge runs wherever the team chooses. Podium serves the content present in the layer's path at the time of ingest. [Connecting layer sources](layer-sources#local-paths) covers `local`-source layers.

For a fuller integration that handles its own ingest semantics (signature verification, content addressing, and fetch), implement `LayerSourceProvider` directly.

### Custom consumer surfaces

A runtime that does not fit the built-in consumers wraps the registry HTTP API directly. Examples are a specialized agent framework, an internal orchestrator, and an evaluation harness.

Identity attaches through the same OAuth flow the SDKs use. Visibility filtering and layer composition still happen on the registry. The custom consumer is responsible for caching and for any harness-native translation it needs.

---

## Building an SPI plugin

### Plugin distribution

Plugins ship as Go modules importable into a registry build. A deployment that needs a custom `IdentityProvider` or `GitProvider` builds a registry binary from source with the plugin imported.

### Forward compatibility for out-of-process plugins

Plugins today are in-process Go modules. A future release may add an out-of-process plugin protocol, such as a subprocess over stdin and stdout or gRPC. That protocol would let plugins ship as separate binaries: closed-source plugins, plugins written in other languages, and plugins distributed without a registry rebuild.

The SPIs are designed to make that transition source-compatible. A plugin that follows the constraints below can ship in-process now and out-of-process later, without changes to its interface contract.

Every SPI method meets these constraints:

- **Cancellable.** Every method takes a `context.Context` (or equivalent) as the first parameter. Long-running work checks for cancellation and respects deadlines.
- **Wire-serializable inputs and outputs.** Every argument and return value is structurally serializable: primitives, slices, maps, and structs whose fields are themselves serializable. Go channels, closures, `func` types, `interface{}` without a stable encoding, and opaque pointers to in-process state are excluded.
- **No shared in-process state across calls.** State the plugin needs across calls is passed explicitly per method, for example a session token, a snapshot ID, or a cursor. Plugins MUST NOT rely on package-level variables, singletons, or callbacks registered at init time.
- **Structured errors.** Failures use a structured envelope (`{code, message, retryable, details}`) rather than opaque Go error chains. Codes use the namespacing documented in [Error codes](../reference/error-codes).
- **Restartable long-lived operations.** Subscriptions, watchers, and streaming results follow a cursor-style protocol. The registry holds the cursor, so the plugin can be killed and respawned without losing its position. Pull-style polling or an explicit re-subscribe with a resume token replaces push-style callback registration.
- **Idempotent retries.** Methods are safe to retry on transient failure. A method with side effects accepts an idempotency key.
- **Bounded payloads.** Method arguments and return values declare size limits. A payload larger than the limit uses a content-addressed reference (a cache key or a presigned URL) rather than inline bytes.

The default implementations, such as `RegistryStore`, `HarnessAdapter`, and `LayerSourceProvider`, conform to these constraints today even though they run in-process. The purpose is forward compatibility rather than present-day distribution: when the out-of-process protocol lands, no built-in needs reworking.

Podium commits to keeping the SPIs wire-friendly. It does not commit to a specific transport (subprocess, gRPC, or Wasm) or to a timeline.

---

## Related pages

- [Configure your harness](../consuming/configure-your-harness) covers runtime-specific extension through `PODIUM_HARNESS=none` combined with custom consumer code.
- [Building custom consumers with the SDK](../consuming/custom-via-sdk) covers programmatic curation, eval pipelines, and custom consumer surfaces.
