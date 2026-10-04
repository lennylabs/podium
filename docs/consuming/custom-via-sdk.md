---
title: Custom consumers via the SDK
nav_order: 4
description: Build programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses) with podium-py or podium-ts.
---

# Custom consumers via the SDK

Programmatic consumers (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks) talk to the registry directly via thin language SDKs. The SDKs are HTTP clients backed by the same registry API the MCP server uses. They reach the registry with the same identity, and the registry applies the same layer composition, visibility filtering, and audit it applies to the MCP path. The SDKs keep no content cache of their own: `always-revalidate` and `offline-first` both fetch on every call, and `offline-only` raises `network.offline_cache_miss` because there is nothing cached to serve.

| SDK | Install | Import | Use for |
|:--|:--|:--|:--|
| `podium-py` | `pip install 'podium-sdk[verify]'` | `from podium import …` | Python orchestrators, LangChain consumers, OpenAI Assistants integrations, build/eval pipelines, notebooks. |
| `podium-ts` | `npm install @lennylabs/podium-sdk` | `import { Client } from "@lennylabs/podium-sdk"` | TypeScript / Node orchestrators, Bedrock Agents, custom Node-based agent runtimes, Edge runtime integrations. |

**The SDKs require a Podium server.** They speak HTTP and don't work against a filesystem-source registry. A consumer reading a filesystem-source registry uses `podium sync` directly.

---

## Initialization

```python
from podium import Client

# from_env reads PODIUM_REGISTRY (falling back to defaults.registry in the
# workspace .podium/sync.local.yaml, the workspace .podium/sync.yaml, then
# ~/.podium/sync.yaml), PODIUM_IDENTITY_PROVIDER, PODIUM_OVERLAY_PATH,
# PODIUM_SESSION_TOKEN, PODIUM_CACHE_MODE, PODIUM_VERIFY_SIGNATURES,
# PODIUM_SIGNATURE_VERIFY_KEY, and PODIUM_SIGN_KEY_PATH. The direct constructor
# below reads PODIUM_OVERLAY_PATH, which an explicit overlay_path argument
# overrides, and also resolves the signature policy and key material, which
# the verify_signatures and verify_keys arguments override.
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

`materialize()` writes the canonical layout under `<to>/<id>/`: `ARTIFACT.md` for every type, `SKILL.md` for a skill, and each bundled resource at its package-relative path. The SDK is an independent HTTP client that does not embed the harness adapters, so `materialize()` writes only the canonical layout, the output of the `none` adapter, and its `harness` argument accepts only `none`. Any other value raises an argument error (`ValueError` in Python, `Error` in TypeScript) before a file is written. Run `podium sync --harness <name>`, or load through the MCP server, when the consumer needs harness-native files.

The SDK separates loading from writing. The MCP server writes every resource to disk during `load_artifact`; the SDK holds the result in memory and writes only when `materialize()` is called. `load_artifact` returns the manifest body and the bundled resources small enough to travel inline; a resource above the 256 KB inline cutoff arrives as a presigned reference, unless the registry holds it inline on the manifest record, as it does for every resource of an artifact ingested while no object store was configured, in which case it arrives inline at any size. `materialize(to=...)` writes the canonical layout under `<to>/<id>/` and fetches each referenced resource. A manifest above the cutoff is fetched during `load_artifact`, so `artifact.manifest_body` is populated whatever the manifest's size. [Inline content and materialized files](handling-artifact-responses#inline-content-and-materialized-files) covers the model both consumer paths share.

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
)

for result in artifacts:
    if result.status == "ok":
        result.materialize(to="./artifacts/")
    else:
        log.warning("skip %s: %s", result.id, result.error.code)
```

Hard cap: 50 IDs per batch. The SDK splits larger sets transparently. Visibility is identical to `load_artifact`: items the caller can't see come back as `status: "error"` with `visibility.denied` (no leak about whether the artifact exists in some hidden layer). Partial failure does not fail the batch; each item carries its own status. Each item counts as one load against the tenant's materialization rate. An item the rate refuses, and every item after it in the same request, comes back as `status: "error"` with `quota.materialize_rate_exceeded`, and its error carries `retryable: true`. The SDK still posts the remaining 50-ID chunks after a refusal, and their items are charged and reported on their own.

The bulk endpoint is not exposed as an MCP meta-tool: bulk loading is a programmatic-runtime concern that doesn't belong in the agent's tool list.

---

## Delivery verification

Both SDKs run the delivery check that `podium-mcp` and server-source `podium sync` run. The SDK recomputes `delivery_hash` from the bytes it received on every `load_artifact` response and every `ok` batch item, under every signature policy including `never`, and refuses a response whose recomputed hash differs, or that carries no `delivery_hash`, with `materialize.content_hash_mismatch`. It then applies its signature policy to `delivery_signature` with the registry-managed verifier. A response loaded from the [workspace overlay](../authoring/extends#workspace-overlay) comes from the local filesystem and runs no check. [Verifying a response](../reference/http-api#verifying-a-response) states the procedure.

The SDK resolves its policy in this order:

1. The `verify_signatures` argument in Python, or `verifySignatures` in TypeScript.
2. `PODIUM_VERIFY_SIGNATURES`.
3. `defaults.verify_signatures` in `sync.yaml`.
4. The SDK default.

The policy takes `never` or `always`. Any other argument value fails construction with `ValueError` in Python and `Error` in TypeScript. A `PODIUM_VERIFY_SIGNATURES` or `defaults.verify_signatures` value other than `never` or `always` fails resolution with an error that names the value and its source, and the SDK never treats it as unset. The resolved policy is exposed as `client.verify_signatures` in Python and `client.verifySignatures` in TypeScript.

The SDK default is `always` when a verification key is configured and `never` otherwise. A verification key is configured when the client is given a key list, when `PODIUM_SIGNATURE_VERIFY_KEY` is set, when `PODIUM_SIGN_KEY_PATH` is set, or when a file is present at the default key path `~/.podium/standalone/registry-signing.key`. The default does not depend on whether the configured material decodes, so `PODIUM_SIGNATURE_VERIFY_KEY=not-base64` selects `always` and the client then refuses. A key file present at the default path counts as configured whether or not it holds the key of the registry the client reaches. A machine that keeps a key file from an earlier standalone registry therefore resolves `always` with an unrelated key, and every signed load fails with `materialize.signature_invalid` until the client is given the right key or `never`. A variable set to the empty string counts as unset.

The key-list argument is `verify_keys` in Python and `verifyKeys` in TypeScript, and it takes the `PODIUM_SIGNATURE_VERIFY_KEY` syntax: one or more base64 Ed25519 public keys separated by commas. A key list replaces the variable. Without either, the SDK reads the `public:` and `verify:` lines of the key file at `PODIUM_SIGN_KEY_PATH` or the default path.

```python
client = Client(
    registry="https://podium.acme.com",
    verify_signatures="always",
    verify_keys="MCowBQYDK2VwAyEA...",
)
```

The Python SDK verifies Ed25519 signatures through the `cryptography` package, which the `podium-sdk[verify]` extra installs. Hash recomputation needs no extra. A client on the standalone registry's machine resolves `always` from the default key file, so it needs the extra. Install the SDK with `pip install 'podium-sdk[verify]'`. The TypeScript SDK computes SHA-256 and verifies Ed25519 through the Web Crypto API (`globalThis.crypto.subtle`) on the Node versions its `engines` field names (Node 20 and later), and adds no dependency.

The SDK resolves the policy and key set once, before its first registry request. The Python client resolves in its constructor, `Client.from_env()` included. The TypeScript client resolves in `Client.fromEnv()`, or, when constructed directly, before the first method call that sends a registry request, so `new Client(...)` reads no file. When the policy is `always` and no usable key resolves, the Python constructor raises, and `fromEnv()` or the first registry call in TypeScript rejects, with `config.signature_provider_unavailable` before any request. The Python client fails the same way, naming the `podium-sdk[verify]` extra, when the policy is `always` and the extra is not installed.

A single load that fails the check raises a `RegistryError` that carries the error code. A batch item that fails the check is returned with `status: "error"` and the code, and the other items load. A batch response body that is not well-formed JSON under the procedure fails the whole `load_artifacts` call with `materialize.content_hash_mismatch` and returns no items. `materialize()` fetches every large resource, checks each against its link's `content_hash`, and writes no file of the artifact when any of them fails with `materialize.content_hash_mismatch`.

A manifest document above the inline cutoff is fetched during `load_artifact` and verified as the fetched bytes. The SDK returns it as text decoded as UTF-8 with each invalid sequence replaced by U+FFFD, and `materialize()` writes that text. For a document that is not valid UTF-8, the SDK's file therefore differs from the bytes `podium sync` writes.

---

## Subscriptions

For long-running consumers (sync watchers, downstream rebuild triggers), subscribe to registry change events:

```python
for event in client.subscribe(["artifact.published", "artifact.deprecated"]):
    handle_event(event)
```

The subscription delivers only the events whose layer the client's identity can see, narrowed by the client's path-scoped OAuth scopes for artifact and domain events. Outbound webhooks carry the same event types to the receivers of the event's tenant, which a tenant admin configures, and within that tenant a receiver's event filter is the only narrowing applied to it.

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

- **`oauth-device-code`**: `Client.login()` runs the device-code flow, prints the verification URL and the user code to stderr, and keeps the returned access token on the client instance for the life of the client. The SDK does not persist it; `podium login` stores a token in the OS keychain. `Client.start_login()` and `Client.finish_login()` split the same flow for callers that show the URL and code in their own interface, as the next section describes.
- **`injected-session-token`**: a runtime-issued signed JWT. Pass it as `Client(registry=..., token=...)`, or export `PODIUM_SESSION_TOKEN` and construct the client with `Client.from_env()`. The right choice for managed agent runtimes (Bedrock Agents, OpenAI Assistants, custom orchestrators) where the runtime issues credentials per session.

The deployment configures the registry to trust the runtime's signing key at startup through `PODIUM_RUNTIME_KEYS_PATH`, which names a file written with `podium admin runtime register --keys-file`. The registry verifies signatures on every call.

### Non-blocking login

`Client.start_login()` (Python) and `client.startLogin()` (TypeScript) request a
device code and return a pending-login handle. The start call prints nothing
and does not poll. The handle carries the verification URL, the complete
verification URL when the IdP supplies one, the user code, the code's lifetime,
and the initial poll interval. Show the URL and the code in the application's
own interface, then call the finish call.

```python
import threading
from podium import Client, DeviceCodeError

client = Client(registry="https://podium.acme.com")
pending = client.start_login()
show_in_ui(pending.verification_uri, pending.user_code)

cancel = threading.Event()   # set from another thread to stop waiting
try:
    client.finish_login(pending, timeout=300, cancel=cancel)
except DeviceCodeError as err:
    report(err.reason)
```

```ts
import { Client, DeviceCodeError } from "@lennylabs/podium-sdk";

const client = new Client({ registry: "https://podium.acme.com" });
const pending = await client.startLogin();
showInUi(pending.verificationUri, pending.userCode);

const controller = new AbortController();
try {
  await client.finishLogin(pending, { timeoutMs: 300_000, signal: controller.signal });
} catch (err) {
  if (err instanceof DeviceCodeError) report(err.reason);
}
```

The finish call follows these rules:

- A handle is single-use. The first finish call consumes it whatever its
  outcome, and a later finish call fails with reason `consumed`.
- The code's lifetime runs from the start call. A finish call on an expired
  handle fails with reason `expired` and sends no token request.
- The timeout runs from the finish call and defaults to 10 minutes. The SDK
  checks it, the code's lifetime, and cancellation between token requests.
- Python cancels through a `threading.Event`, and TypeScript through an
  `AbortSignal`. A cancelled finish call fails with reason `cancelled`. Python
  stops at the next check and does not interrupt a token request in flight.
- `DeviceCodeError.reason` is `denied`, `expired`, `timeout`, `cancelled`,
  `consumed`, or `failed`. The `failed` reason covers discovery, device
  authorization, transport, and unrecognized IdP errors.
- On success the finish call stores the access token on the client it was
  called on. The token stays in memory for the life of the client, and the
  SDK neither persists nor refreshes it. Finish a handle on the client that
  started it.
- Until a finish call succeeds, the client sends anonymous requests and sees
  public visibility only. The SDK does not refresh the access token, so when
  it expires (15 minutes by default) requests fail with `auth.token_expired`.
  Run the pair or `login()` again to obtain a new token.

`client.login()` runs both calls, prints the URL and the code to stderr, and
blocks until the flow ends.
