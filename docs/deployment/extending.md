---
layout: default
title: Extending
parent: Deployment
nav_order: 6
description: Plugin SPIs, the forward-compatibility constraints that keep out-of-process plugins on the table, and external-extension patterns built on the HTTP API.
---

# Extending

Podium is extensible at two layers:

- **In-process plugins.** Swap or augment the registry's own behavior (different stores, different identity providers, different lint rules) by implementing a Go interface and being compiled into a registry build. See [SPIs](#spis).
- **External extensions.** Build on the registry's HTTP API, SDKs, and CLI without changing the registry itself: programmatic curation scripts, webhook receivers, custom CI checks, layer source bridges. See [External extensions](#external-extensions).

Most teams reach for external extensions first. SPI plugins are for cases where the registry's own behavior needs to change.

---

## SPIs

The registry's pluggable interfaces:

| Interface | Purpose |
|:--|:--|
| `RegistryStore` | Manifest metadata, dependency edges, layer config, admin grants, registry-side audit. Postgres (standard) / SQLite (standalone) by default. |
| `RegistryObjectStore` | Bundled resource bytes, presigned URLs. S3-compatible (filesystem in standalone). |
| `RegistrySearchProvider` | Hybrid retrieval for `search_artifacts`. Built-ins: `pgvector`, `sqlite-vec`, `pinecone`, `weaviate-cloud`, `qdrant-cloud`. |
| `EmbeddingProvider` | Generates embeddings for ingest text and query text. Built-ins: `embedded-onnx`, `openai`, `voyage`, `cohere`, `ollama`. |
| `LocalSearchProvider` | Optional semantic backing for the local-overlay index. Same SPI as `RegistrySearchProvider`. |
| `RegistryAuditSink` | Stream for catalogue events; logically distinct from `RegistryStore`, separately mockable, separately routable. |
| `LayerComposer` | Resolves the caller's effective view from the configured layer list; applies merge semantics and `extends:` resolution. |
| `LayerSourceProvider` | Resolves and watches the source backing a layer. Built-ins: `git`, `local`. Custom backends: S3 versioned buckets, OCI registries, HTTP archives, internal CMS bridges. |
| `GitProvider` | Webhook signature verification and Git fetch semantics, used by the built-in `git` `LayerSourceProvider`. Built-in support for GitHub, GitLab, Bitbucket. |
| `TypeProvider` | Type definitions: frontmatter JSON Schema + lint rules + adapter hints + field-merge semantics. |
| `IngestLinter` | Manifest validation, resource-reference checks, type-specific rules; runs pre-merge in CI and again at registry ingest. |
| `IdentityProvider` | Attaches OAuth-attested identity to every registry call. Built-ins: `oauth-device-code`, `injected-session-token`. |
| `LocalOverlayProvider` | Source for the workspace-scoped local overlay layer. Default: workspace filesystem (`.podium/overlay/`). |
| `LocalAuditSink` | Local audit log for meta-tool calls (when configured). Default: JSON Lines file at `~/.podium/audit.log`. |
| `HarnessAdapter` | Translates canonical artifacts to the harness's native format at materialization time. |
| `MaterializationHook` | Per-file pre-write transformation of materialized output. Use cases: redact secrets, rewrite paths, inject team-specific headers, enforce content policy. |
| `NotificationProvider` | Delivery for vulnerability alerts and ingest-failure notifications. Default: email + webhook. |
| `SignatureProvider` | Artifact signing and verification. Default: Sigstore-keyless. |

---

## Plugin distribution

Plugins ship as Go modules importable into a registry build. A deployment that needs a custom `IdentityProvider` or `GitProvider` builds a registry binary from source with the plugin imported.

A community plugin registry is hosted at the project's public URL.

---

## Forward compatibility for out-of-process plugins

Plugins today are in-process Go modules. A future release may add an out-of-process plugin protocol (subprocess over stdin/stdout, gRPC, or similar) so plugins can ship as separate binaries: closed-source plugins, plugins written in other languages, plugins distributed without a registry rebuild.

The SPIs are designed today to make that transition source-compatible. Plugin authors who follow the constraints below will be able to ship the same plugin in-process now and out-of-process later, without code changes to the plugin's interface contract.

**Constraints on every SPI method:**

- **Cancellable.** Every method takes a `context.Context` (or equivalent) as the first parameter. Long-running work checks for cancellation; deadlines are respected.
- **Wire-serializable inputs and outputs.** Every argument and return value is structurally serializable: primitives, slices, maps, and structs whose fields are themselves serializable. No Go channels, no closures, no `func` types, no `interface{}` without a stable encoding, and no opaque pointers to in-process state.
- **No shared in-process state across calls.** State the plugin needs across calls is passed explicitly per method (e.g., a session token, a snapshot ID, a cursor). Plugins MUST NOT rely on package-level variables, singletons, or registered callbacks set at init time.
- **Structured errors.** Failures use a structured envelope (`{code, message, retryable, details}`) rather than opaque Go error chains. Codes use the namespacing in §6.10 of the spec.
- **Restartable long-lived operations.** Subscriptions, watchers, and streaming results are modeled as cursor-style protocols (the registry holds the cursor; the plugin can be killed and respawned without losing track of where it was). Push-style callback registration is avoided in favor of pull-style polling or explicit re-subscribe with a resume token.
- **Idempotent retries.** Methods are safe to retry on transient failure. Where a method has side effects, it accepts an idempotency key.
- **Bounded payloads.** Method arguments and return values declare reasonable size limits. Payloads larger than the limit use a content-addressed reference (cache key, presigned URL) rather than inline bytes.

The default implementations (`RegistryStore`, `HarnessAdapter`, `LayerSourceProvider`, etc.) conform to these constraints today, even though they run in-process. The motivation is forward compatibility rather than present-day distribution: when the out-of-process protocol lands, no built-in needs reshaping.

This section commits to keeping SPIs wire-friendly. It does not commit to a specific transport (subprocess, gRPC, Wasm) or a timeline.

---

## External extensions

The registry's HTTP API, SDKs, CLI, and outbound webhook stream are designed to be composed into team-specific tooling without touching the registry binary.

### Programmatic curation (semantic discovery + scoped sync)

A script picks artifacts based on whatever context is meaningful (semantic match against a query, the user's recent work, the active project, an upstream ticket) and then invokes `podium sync` with `--include` flags to materialize the selected set. The script owns the discovery logic; Podium owns the materialization (visibility filtering, `extends:` resolution, harness adaptation, audit). The on-disk result is reproducible from the include list.

See [Custom consumers via the SDK → Programmatic curation](../consuming/custom-via-sdk#patterns) for a worked example.

### Webhook-driven integrations

Receivers for the outbound webhooks (`artifact.published`, `artifact.deprecated`, `domain.published`, `layer.ingested`, `layer.history_rewritten`, `vulnerability.detected`, `domains.searched` audit-stream consumers, etc.) feed Slack channels, ticket trackers, deployment pipelines, internal dashboards. The registry emits the events; the receiver decides what to do.

Common targets:

- Notify owners on `artifact.deprecated`.
- Post to a channel on `vulnerability.detected`.
- Kick off a downstream rebuild on `artifact.published` matching certain paths.

### Custom pre-merge CI

Each layer's source repo runs whatever CI checks the team wants (naming conventions, sensitivity sign-off, banned dependencies, structural rules) using `podium lint` plus team-specific scripts. These checks are out of Podium's scope; they're ordinary CI in the layer's source repository, gated by branch protection.

### Layer source bridges

A script that pulls content from another system (a vendor SaaS, an internal CMS, a documentation generator) and writes it into a `local`-source layer's filesystem path. The registry ingests via `podium layer reingest <id>` (manually or on a schedule the bridge controls). The bridge runs wherever the team wants; Podium serves what's in the layer's path at the time of ingest.

For a fuller integration that handles its own ingest semantics (signature verification, content addressing, fetch), implement `LayerSourceProvider` directly.

### Custom consumer surfaces

A runtime that doesn't fit the built-in consumers (a specialized agent framework, an internal orchestrator, an evaluation harness) wraps the registry HTTP API directly. Identity attaches via the same OAuth flow used by the SDKs; visibility filtering and layer composition still happen server-side. The custom consumer is responsible for caching and any harness-native translation it needs.

---

## Where to learn more

- [`spec/09-extensibility.md`](https://github.com/lennylabs/podium/blob/main/spec/09-extensibility.md): the full SPI table and the forward-compatibility constraints.
- [Configure your harness](../consuming/configure-your-harness): for runtime-specific extension via `PODIUM_HARNESS=none` plus custom consumer code.
- [Custom consumers via the SDK](../consuming/custom-via-sdk): patterns for programmatic curation, eval pipelines, and custom consumer surfaces.
