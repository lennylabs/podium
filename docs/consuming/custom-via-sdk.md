---
layout: default
title: Custom consumers via the SDK
parent: Consuming
nav_order: 3
description: Build programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses) with podium-py or podium-ts.
---

# Custom consumers via the SDK

Programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks) talk to the registry directly via thin language SDKs. The SDKs are HTTP clients backed by the same registry API the MCP server uses. They share identity providers, the content cache, layer composition, visibility filtering, and audit.

| SDK | Distribution | Use for |
|:--|:--|:--|
| `podium-py` | PyPI | Python orchestrators, LangChain consumers, OpenAI Assistants integrations, build/eval pipelines, notebooks. |
| `podium-ts` | npm | TypeScript / Node orchestrators, Bedrock Agents, custom Node-based agent runtimes, Edge runtime integrations. |

**The SDKs require a Podium server.** They speak HTTP and don't work against a filesystem-source registry. For filesystem-mode consumers, use `podium sync` directly.

---

## Initialization

```python
from podium import Client

# from_env reads PODIUM_REGISTRY, PODIUM_IDENTITY_PROVIDER,
# PODIUM_OVERLAY_PATH, etc. Constructor params override env values.
client = Client.from_env()

# Or pass explicitly:
client = Client(
    registry="https://podium.acme.com",
    identity_provider="oauth-device-code",
    overlay_path="./.podium/overlay/",   # workspace local overlay
)

# Authenticate (oauth-device-code path; the SDK raises DeviceCodeRequired
# with the URL and code if interaction is needed).
client.login()
```

For managed runtimes that issue their own session tokens, swap `identity_provider` to `injected-session-token` and configure `PODIUM_SESSION_TOKEN_FILE` or `PODIUM_SESSION_TOKEN_ENV`.

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

The same operations are exposed as read-only CLI commands (`podium search`, `podium domain show`, `podium domain search`, `podium artifact show`) for shell pipelines. See [Reference → CLI](../reference/cli) when that page lands.

---

## Loading and materializing

```python
# Load an artifact's manifest in memory
artifact = client.load_artifact("finance/close-reporting/run-variance-analysis")
print(artifact.manifest_body)

# Materialize bundled resources to disk via the harness adapter
artifact.materialize(to="./artifacts/", harness="claude-code")
```

`materialize()` runs the configured `HarnessAdapter` over the canonical artifact and writes the result to the destination path. Pass `harness="none"` to write the canonical layout as-is, which is useful when the consuming runtime reads `ARTIFACT.md` (and `SKILL.md` for skills) directly.

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
    harness="claude-code",        # optional per-call adapter override
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
deps = client.dependents_of("finance/ap/pay-invoice@1.2")
```

Returns the set of artifacts that depend on the given one via `extends:`, `delegates_to:`, or `mcpServers:` references. Useful before deprecating, when assessing blast radius, or when building a "what breaks if I change this?" check.

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
ids = [r.id for r in results if r.score > 0.5]

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
artifact = client.load_artifact("evals/regression-suite/run-week-42", harness="none")

# `none` writes the canonical layout — read ARTIFACT.md (and SKILL.md for skills) directly
manifest = artifact.frontmatter
body = artifact.manifest_body
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

`type: eval` is a registered extension type (see [Artifact types → Extension types](../authoring/artifact-types)); deployments that use it register the schema and lint rules through `TypeProvider`.

---

## Why programmatic consumers don't get the meta-tool semantics

The SDKs deliberately don't implement the MCP meta-tool semantics (the agent-driven lazy materialization). Programmatic consumers know what they want; they don't need an LLM-mediated browse interface. If a programmatic consumer wants lazy semantics, it can call `load_artifact` lazily in its own code.

Identity providers, the cache, visibility filtering, layer composition, and audit are all the same as in the MCP path. The SDK uses a different transport.

---

## Identity providers

Custom providers register through the same interface as the MCP server's. For most consumers, the built-in providers are enough:

- **`oauth-device-code`**: interactive device-code flow on first use; tokens cached in the OS keychain. The default for developer-machine consumers.
- **`injected-session-token`**: runtime-issued signed JWT, configured via `PODIUM_SESSION_TOKEN_ENV` or `PODIUM_SESSION_TOKEN_FILE`. The right choice for managed agent runtimes (Bedrock Agents, OpenAI Assistants, custom orchestrators) where the runtime issues credentials per session.

The runtime registers its signing key with the registry one-time at runtime onboarding. The registry verifies signatures on every call.
