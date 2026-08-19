---
title: Custom consumers via the SDK
nav_order: 4
description: Build programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses) with podium-py or podium-ts.
---

# Custom consumers via the SDK

Programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks) talk to the registry directly via thin language SDKs. The SDKs are HTTP clients backed by the same registry API the MCP server uses. They reach the registry with the same identity, and the registry applies the same layer composition, visibility filtering, and audit it applies to the MCP path. The SDKs keep no content cache of their own: `always-revalidate` and `offline-first` both fetch on every call, and `offline-only` raises `network.offline_cache_miss` because there is nothing cached to serve.

| SDK | Install | Import | Use for |
|:--|:--|:--|:--|
| `podium-py` | `pip install podium-sdk` | `from podium import …` | Python orchestrators, LangChain consumers, OpenAI Assistants integrations, build/eval pipelines, notebooks. |
| `podium-ts` | `npm install @lennylabs/podium-sdk` | `import { Client } from "@lennylabs/podium-sdk"` | TypeScript / Node orchestrators, Bedrock Agents, custom Node-based agent runtimes, Edge runtime integrations. |

**The SDKs require a Podium server.** They speak HTTP and don't work against a filesystem-source registry. A consumer reading a filesystem-source registry uses `podium sync` directly.

---

## Initialization

```python
from podium import Client

# from_env reads PODIUM_REGISTRY (falling back to defaults.registry in the
# workspace .podium/sync.local.yaml, the workspace .podium/sync.yaml, then
# ~/.podium/sync.yaml), PODIUM_IDENTITY_PROVIDER, PODIUM_OVERLAY_PATH,
# PODIUM_SESSION_TOKEN, and PODIUM_CACHE_MODE. The direct constructor below
# reads no environment variable except PODIUM_OVERLAY_PATH, which an explicit
# overlay_path argument overrides.
client = Client.from_env()

# Or pass explicitly:
client = Client(
    registry="https://podium.acme.com",
    identity_provider="oauth-device-code",
    overlay_path="./.podium/overlay/",   # workspace local overlay
)

# Authenticate (oauth-device-code path). login() prints the verification URL
# and the user code to stderr, then blocks until the flow completes or the
# timeout (10 minutes by default) expires.
client.login()
```

For managed runtimes that issue their own session tokens, pass the token to the client (`Client(registry="https://podium.acme.com", token=...)`), or export `PODIUM_SESSION_TOKEN` and construct the client with `Client.from_env()`. The SDK attaches it as the `Authorization: Bearer` credential on every request. `PODIUM_SESSION_TOKEN_FILE` and `PODIUM_SESSION_TOKEN_ENV` are read by the MCP server and the CLI, and the SDKs do not read them.

---

## Discovery

```python
# Browse hierarchically
domains = client.load_domain("finance/close-reporting")

# Find candidate domains by query
candidates = client.search_domains("vendor payments", top_k=5)

# Find artifacts by query, with filters
results = client.search_artifacts(
    "variance analysis",
    type="skill",
    tags=["finance", "close"],
    scope="finance/close-reporting",
    top_k=10,
    session_id=session_id,
)

# Browse: no query, scope only — list artifacts in a domain
browse = client.search_artifacts(scope="finance/ap", top_k=50)
print(f"showing {len(browse.results)} of {browse.total_matched}")

# Type-specific lookups
agents = client.search_artifacts("payment workflow", type="agent")
contexts = client.search_artifacts("style guide", type="context")
mcp_servers = client.search_artifacts(type="mcp-server")
```

The same operations are exposed as read-only CLI commands (`podium search`, `podium domain show`, `podium domain search`, and `podium artifact show`) for shell pipelines. See [Reference → CLI](../reference/cli).

---

## Loading and materializing

```python
# Load an artifact's manifest in memory
artifact = client.load_artifact("finance/close-reporting/run-variance-analysis")
print(artifact.manifest_body)

# Write the artifact to disk in the canonical layout
artifact.materialize(to="./artifacts/")
```

`materialize()` writes the canonical layout under `<to>/<id>/`: `ARTIFACT.md` for every type, `SKILL.md` for a skill, and each bundled resource at its package-relative path. The SDK is an independent HTTP client that does not embed the harness adapters, so `materialize()` accepts a `harness` argument for forward compatibility and writes the canonical layout whatever its value. Run `podium sync --harness <name>` when the consumer needs harness-native files.

The SDK separates loading from writing. The MCP server writes every resource to disk during `load_artifact`; the SDK holds the result in memory and writes only when `materialize()` is called. `load_artifact` returns the manifest body and the bundled resources small enough to travel inline; a resource above the 256 KB inline cutoff arrives as a presigned reference. `materialize(to=...)` writes the canonical layout under `<to>/<id>/` and fetches each referenced resource. A manifest above the cutoff is fetched during `load_artifact`, so `artifact.manifest_body` is populated whatever the manifest's size. [Inline content and materialized files](handling-artifact-responses#inline-content-and-materialized-files) covers the model both consumer paths share.

The response carries manifest fields beyond the prose body (hints, sandbox profile, runtime requirements, MCP server registrations, dependency edges). [Handling artifact responses](handling-artifact-responses) walks through each field and what the consumer should do with it.

---

## Bulk fetch

`load_artifact` works one ID at a time. For consumers that need a known set up front (eval harnesses, batch workflows, custom orchestrators), `load_artifacts` is the bulk variant: one HTTP request, one auth check, one visibility composition pass, one transactional snapshot.

```python
artifacts = client.load_artifacts(
    ids=[
        "finance/close-reporting/run-variance-analysis",
        "finance/close-reporting/policy-doc",
        "finance/ap/pay-invoice",
    ],
    session_id=session_id,        # honors the same `latest`-resolution semantics
    harness="claude-code",        # recorded on the request; the response and materialize() are canonical
)

for result in artifacts:
    if result.status == "ok":
        result.materialize(to="./artifacts/")
    else:
        log.warning("skip %s: %s", result.id, result.error.code)
```

Hard cap: 50 IDs per batch. The SDK splits larger sets transparently. Visibility is identical to `load_artifact`: items the caller can't see come back as `status: "error"` with `visibility.denied` (no leak about whether the artifact exists in some hidden layer). Partial failure does not fail the batch; each item carries its own status.

The bulk endpoint is not exposed as an MCP meta-tool: bulk loading is a programmatic-runtime concern that doesn't belong in the agent's tool list.

---

## Subscriptions

For long-running consumers (sync watchers, downstream rebuild triggers), subscribe to registry change events:

```python
for event in client.subscribe(["artifact.published", "artifact.deprecated"]):
    handle_event(event)
```

The same events fire outbound webhooks; the subscription is the in-process equivalent for code that's already running.

---

## Cross-type dependency walks

For impact analysis and custom tooling:

```python
deps = client.dependents_of("finance/ap/pay-invoice")
```

Returns the set of artifacts that depend on the given one via `extends:`, `delegates_to:`, or `mcpServers:` references. Dependency edges key on the unpinned canonical ID, so a query carrying an `@version` suffix matches no edge. Useful before deprecating, when assessing blast radius, or when building a "what breaks if I change this?" check.

---

## Patterns

### Programmatic curation (semantic discovery + scoped sync)

A common pattern: a script picks artifacts based on context (current task, recent work, an upstream ticket, semantic match against a query), then invokes `podium sync` with `--include` flags to materialize the chosen set. The script owns the discovery logic; Podium owns the materialization (visibility filtering, `extends:` resolution, harness adaptation, audit). The on-disk result is reproducible from the include list.

```python
from podium import Client
import subprocess

client = Client.from_env()

# Discovery: whatever logic the team wants. Here, semantic match + a score floor.
results = client.search_artifacts(
    "month-end close OR variance analysis",
    type="skill",
    top_k=15,
)
ids = [r.id for r in results.results if r.score > 0.5]

# Materialization: hand the chosen ids to `podium sync` so the on-disk view is
# auditable and reproducible from the include list.
subprocess.run(
    [
        "podium", "sync",
        "--harness", "claude-code",
        "--target", "/Users/me/.claude/",
        *sum((["--include", artifact_id] for artifact_id in ids), []),
    ],
    check=True,
)
```

The script could read recent files in the workspace and search for related artifacts, follow `dependents_of()` from a starting artifact, or consult an external system (a ticket, a calendar) before deciding what to materialize. Whatever the script decides, `podium sync` performs the write.

This is the canonical answer to "I have thousands of artifacts but my harness only needs around 30 in context for this session." Curate, then sync.

### Custom consumer with no harness adapter

When a runtime doesn't fit any built-in harness (a specialized agent framework, an internal orchestrator, an evaluation harness), consume the registry directly:

```python
client = Client.from_env()
artifact = client.load_artifact("evals/regression-suite/run-week-42")

# Read the manifest in memory; nothing is written until materialize().
manifest = artifact.frontmatter
body = artifact.manifest_body

# Write the canonical layout when the runtime wants files on disk:
# ARTIFACT.md (and SKILL.md for skills) plus bundled resources.
artifact.materialize(to="./artifacts/", harness="none")
```

Identity, visibility, layer composition, and audit are unchanged. The custom consumer is responsible for caching and any runtime-native translation it needs.

### Eval pipeline

```python
suite = client.search_artifacts(type="eval", tags=["regression"], top_k=50)
for descriptor in suite.results:
    artifact = client.load_artifact(descriptor.id)
    artifact.materialize(to=f"./runs/{descriptor.id}/", harness="none")
    run_eval(f"./runs/{descriptor.id}/")
```

`type: eval` is an example extension type identifier (see [Artifact types → Extension types](../authoring/artifact-types)). Podium does not ship an implementation; a deployment that uses it registers the schema and lint rules through `TypeProvider`.

---

## Why programmatic consumers don't get the meta-tool semantics

The SDKs deliberately don't implement the MCP meta-tool semantics (the agent-driven lazy materialization). Programmatic consumers know what they want; they don't need an LLM-mediated browse interface. If a programmatic consumer wants lazy semantics, it can call `load_artifact` lazily in its own code.

Visibility filtering, layer composition, and audit are the same as in the MCP path, because the registry applies them. The SDK uses a different transport and keeps no content cache.

---

## Identity providers

Custom providers register through the same interface as the MCP server's. For most consumers, the built-in providers are enough:

- **`oauth-device-code`**: `Client.login()` runs the device-code flow, prints the verification URL and the user code to stderr, and keeps the returned access token on the client instance for the life of the process. The SDK does not persist it; `podium login` stores a token in the OS keychain.
- **`injected-session-token`**: a runtime-issued signed JWT. Pass it as `Client(registry=..., token=...)`, or export `PODIUM_SESSION_TOKEN` and construct the client with `Client.from_env()`. The right choice for managed agent runtimes (Bedrock Agents, OpenAI Assistants, custom orchestrators) where the runtime issues credentials per session.

The deployment configures the registry to trust the runtime's signing key at startup through `PODIUM_RUNTIME_KEYS_PATH`, which names a file written with `podium admin runtime register --keys-file`. The registry verifies signatures on every call.
