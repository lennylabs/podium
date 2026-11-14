---
title: Building custom consumers with the SDK
nav_order: 5
description: Build programmatic registry consumers, such as LangChain, Bedrock, and OpenAI Assistants integrations, custom orchestrators, and eval harnesses, with podium-py or podium-ts.
---

# Building custom consumers with the SDK

This page shows how to build a programmatic consumer of a Podium registry with the Python SDK (`podium-py`) or the TypeScript SDK (`podium-ts`). It is for developers who connect the registry to LangChain, Bedrock, or OpenAI Assistants applications, custom orchestrators, eval harnesses, build pipelines, or notebooks.

---

## How the SDKs work

The SDKs are thin HTTP clients for the same registry API the MCP server uses. A client reaches the registry with the same identity, and the registry applies the same layer composition, visibility filtering, and audit it applies to the MCP path.

> [!IMPORTANT]
> The SDKs require a Podium server. They speak HTTP and do not work against a filesystem-source registry. A consumer that reads a filesystem-source registry uses `podium sync` directly.

The SDKs differ from the MCP server in these ways:

- **Loading and writing are separate.** The MCP server writes every resource to disk during `load_artifact`. The SDK holds the result in memory and writes files only when the program calls `materialize()`.
- **The output is the canonical layout.** The SDK does not embed the harness adapters, so `materialize()` writes the same files as the `none` adapter. Use `podium sync --harness <name>`, or load through the MCP server, when the consumer needs harness-native files.
- **There are no meta-tool semantics.** The SDKs do not implement the agent-driven lazy materialization of the MCP meta-tools. A programmatic consumer knows which artifacts it wants and does not need an LLM-mediated browse interface. A consumer that wants lazy loading calls `load_artifact` lazily in its own code.
- **There is no content cache.** The consumer is responsible for any caching it needs. [Edge cases and troubleshooting](#edge-cases-and-troubleshooting) lists how the cache modes behave.

---

## Install the SDK

| SDK | Install | Import | Use for |
|:--|:--|:--|:--|
| `podium-py` | `pip install 'podium-sdk[verify]'` | `from podium import …` | Python orchestrators, LangChain consumers, OpenAI Assistants integrations, build and eval pipelines, and notebooks. |
| `podium-ts` | `npm install @lennylabs/podium-sdk` | `import { Client } from "@lennylabs/podium-sdk"` | TypeScript and Node orchestrators, Bedrock Agents, custom Node-based agent runtimes, and Edge runtime integrations. |

The `verify` extra of the Python package installs the library that checks signatures. [Verification dependencies](#verification-dependencies) explains when a client needs it.

---

## TypeScript differences

The examples on this page use the Python SDK. The TypeScript SDK exposes the same operations with these differences:

- Every method that calls the registry returns a `Promise`, and `materialize()` is asynchronous as well.
- `Client.fromEnv()` is asynchronous, and the constructor takes one options object, such as `new Client({ registry, token, overlayPath, verifySignatures, verifyKeys })`.
- Method names and option names are camelCase. The keyword arguments of a Python method become an options object.
- `loadArtifact` takes the version as its second positional argument, where Python takes `version=` as a keyword.
- `subscribe()` returns an async iterable, which a consumer reads with `for await`.
- Response objects keep the wire field names, such as `manifest_body`, `frontmatter`, and `total_matched`.

| Python | TypeScript |
|:--|:--|
| `Client.from_env()` | `await Client.fromEnv()` |
| `client.load_domain(path, depth)` | `client.loadDomain(path, depth)` |
| `client.search_domains(query, scope=..., top_k=...)` | `client.searchDomains(query, { scope, topK })` |
| `client.search_artifacts(query, type=..., tags=..., scope=..., top_k=..., session_id=...)` | `client.searchArtifacts(query, { type, tags, scope, topK, sessionID })` |
| `client.load_artifact(id, version=..., session_id=...)` | `client.loadArtifact(id, version, { sessionID })` |
| `client.load_artifacts(ids, session_id=..., version_pins=...)` | `client.loadArtifacts(ids, { sessionID, versionPins })` |
| `artifact.materialize(to=..., harness="none")` | `artifact.materialize(to, { harness: "none" })` |
| `client.dependents_of(id)` | `client.dependentsOf(id)` |
| `client.subscribe(event_types)` | `client.subscribe(eventTypes)` |
| `client.start_login()` and `client.finish_login(pending)` | `client.startLogin()` and `client.finishLogin(pending)` |

---

## Create a client

Construct a client from the environment, or pass the settings explicitly. The comments in the example list the variables and files `Client.from_env()` reads.

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

---

## Authenticate

Custom identity providers register through the same interface as the MCP server's. Most consumers use one of the built-in providers.

| Provider | How it works | Use for |
|:--|:--|:--|
| `oauth-device-code` | `Client.login()` runs the device-code flow, prints the verification URL and the user code to stderr, and keeps the returned access token on the client instance for the life of the client. The SDK does not persist the token. `Client.start_login()` and `Client.finish_login()` split the same flow, as [Non-blocking login](#non-blocking-login) describes. | Interactive programs and notebooks. |
| `injected-session-token` | The runtime issues a signed JWT, and the client sends it on every request. | Managed agent runtimes, such as Bedrock Agents, OpenAI Assistants, and custom orchestrators, where the runtime issues credentials per session. |

The SDK does not persist the token. `podium login` stores its token in the OS keychain.

### Session tokens from a managed runtime

Pass the runtime's token to the client in either of these ways:

- Construct the client with the token: `Client(registry="https://podium.acme.com", token=...)`.
- Export `PODIUM_SESSION_TOKEN` and construct the client with `Client.from_env()`.

The SDK attaches the token as the `Authorization: Bearer` credential on every request. The registry verifies the token's signature on every call.

The registry trusts a runtime's signing key only when the deployment configures it at startup. `PODIUM_RUNTIME_KEYS_PATH` names the trusted-keys file, which `podium admin runtime register --keys-file` writes.

### Non-blocking login

Use the non-blocking pair when the application shows the verification URL and the user code in its own interface. `Client.start_login()` in Python and `client.startLogin()` in TypeScript request a device code and return a pending-login handle. The start call prints nothing and does not poll.

The handle carries the following values:

- The verification URL.
- The complete verification URL, when the IdP supplies one.
- The user code.
- The code's lifetime.
- The initial poll interval.

Show the URL and the code in the application's interface, then call the finish call.

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

- A handle is single-use. The first finish call consumes it whatever its outcome.
- The code's lifetime runs from the start call. A finish call on an expired handle sends no token request.
- The timeout runs from the finish call and defaults to 10 minutes. The SDK checks the timeout, the code's lifetime, and cancellation between token requests.
- Python cancels through a `threading.Event`, and TypeScript cancels through an `AbortSignal`. Python stops at the next check and does not interrupt a token request in flight.
- On success, the finish call stores the access token on the client it was called on. Finish a handle on the client that started it.
- The token stays in memory for the life of the client. The SDK neither persists nor refreshes it.
- Until a finish call succeeds, the client sends anonymous requests and sees public visibility only.

A failed finish call raises `DeviceCodeError`, and its `reason` names the cause:

| `reason` | Cause |
|:--|:--|
| `denied` | The IdP reports that the user denied the request. |
| `expired` | The code's lifetime ran out before the flow completed. |
| `timeout` | The finish call's timeout elapsed. |
| `cancelled` | The `threading.Event` was set or the `AbortSignal` fired. |
| `consumed` | An earlier finish call already used the handle. |
| `failed` | Discovery, device authorization, or transport failed, or the IdP returned an unrecognized error. |

The access token expires after 15 minutes by default. Requests then fail with `auth.token_expired`. Run the pair or `login()` again to obtain a new token.

`client.login()` runs both calls, prints the URL and the code to stderr, and blocks until the flow ends.

---

## Discover artifacts

The client exposes the discovery operations of the registry: browsing a domain, searching domains, and searching artifacts with filters. A search with a scope and no query lists the artifacts in a domain.

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

The same operations are available to shell pipelines as read-only CLI commands: `podium search`, `podium domain show`, `podium domain search`, and `podium artifact show`. See [Reference → CLI](../reference/cli).

---

## Load and materialize an artifact

Load an artifact to read it in memory, then call `materialize()` to write it to disk.

```python
# Load an artifact's manifest in memory
artifact = client.load_artifact("finance/close-reporting/run-variance-analysis")
print(artifact.manifest_body)

# Write the artifact to disk in the canonical layout
artifact.materialize(to="./artifacts/")
```

### What `load_artifact` returns

`load_artifact` returns the manifest body and the bundled resources that travel inline. A resource that travels by URL arrives as a reference, which `materialize()` fetches. [Resource transport](handling-artifact-responses#resource-transport) states which resources take each route. A manifest that travels by URL is fetched during `load_artifact`, so `artifact.manifest_body` is populated whatever the manifest's size. [Inline content and materialized files](handling-artifact-responses#inline-content-and-materialized-files) describes the model both consumer paths share.

The response also carries manifest fields beyond the prose body, such as hints, the sandbox profile, runtime requirements, MCP server registrations, and dependency edges. [Handling artifact responses](handling-artifact-responses) describes each field and what the consumer does with it.

### What `materialize()` writes

`materialize(to=...)` writes the canonical layout under `<to>/<id>/` and fetches each referenced resource. The layout contains the following files:

- `SKILL.md`, for a skill.
- `ARTIFACT.md`, unless the registry synthesized it (see below).
- Each bundled resource, at its package-relative path.

The registry synthesizes an `ARTIFACT.md` for a skill that has none. `materialize()` does not write that synthesized document, and it also skips an authored file that is byte-identical to it. Such a skill materializes as a plain Agent Skills directory.

The `harness` argument of `materialize()` accepts only `none`. Any other value raises an argument error before a file is written: `ValueError` in Python and `Error` in TypeScript.

---

## Load artifacts in bulk

`load_artifact` loads one ID at a time. `load_artifacts` loads a known set in one HTTP request, with one auth check, one visibility composition pass, and one transactional snapshot. Eval harnesses, batch workflows, and custom orchestrators use it to fetch their working set up front.

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

The bulk call behaves as follows:

- A request carries at most 50 IDs. The SDK splits a larger set into 50-ID chunks transparently.
- Each item carries its own status, and a partial failure does not fail the batch.
- Visibility is identical to `load_artifact`. An item the caller cannot see comes back as `status: "error"` with `visibility.denied`, which reveals nothing about whether the artifact exists in a hidden layer.
- Each item counts as one load against the tenant's materialization rate.

The bulk endpoint is not an MCP meta-tool. Bulk loading serves programmatic runtimes and does not belong in an agent's tool list.

---

## Subscribe to change events

Long-running consumers, such as sync watchers and downstream rebuild triggers, subscribe to registry change events:

```python
for event in client.subscribe(["artifact.published", "artifact.deprecated"]):
    handle_event(event)
```

The subscription delivers only the events whose layer the client's identity can see. For artifact and domain events, the client's path-scoped OAuth scopes narrow the set further.

Outbound webhooks carry the same event types to the receivers of the event's tenant, which a tenant admin configures. Within that tenant, a receiver's event filter is the only narrowing applied to it.

---

## Find dependent artifacts

`dependents_of()` returns the artifacts that depend on a given artifact through `delegates_to:` or `mcpServers:` references. Use it for impact analysis before deprecating an artifact, or to build a "what breaks if I change this?" check.

```python
deps = client.dependents_of("finance/ap/pay-invoice")
```

Dependency edges key on the unpinned canonical ID. Pass the ID without an `@version` suffix, because a query that carries one matches no edge.

---

## Patterns

### Programmatic curation (semantic discovery + scoped sync)

In this pattern, a script selects artifacts based on context and then runs `podium sync` with `--include` flags to materialize the selection. The context can be the current task, recent work, an upstream ticket, or a semantic match against a query. The script owns the discovery logic. Podium owns the materialization, which covers visibility filtering, harness adaptation, and audit. The on-disk result is reproducible from the include list.

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

The selection logic can take other inputs. A script can read recent files in the workspace and search for related artifacts, follow `dependents_of()` from a starting artifact, or consult an external system such as a ticket tracker or a calendar. In every case, `podium sync` performs the write.

This pattern serves a catalog of thousands of artifacts when a harness needs only around 30 of them in context for a session. The script curates the set, and `podium sync` materializes it.

### Custom consumer with no harness adapter

A runtime that fits no built-in harness, such as a specialized agent framework, an internal orchestrator, or an evaluation harness, consumes the registry directly:

```python
client = Client.from_env()
artifact = client.load_artifact("evals/regression-suite/run-week-42")

# Read the manifest in memory; nothing is written until materialize().
manifest = artifact.frontmatter
body = artifact.manifest_body

# Write the canonical layout when the runtime wants files on disk:
# SKILL.md for skills, ARTIFACT.md unless the registry synthesized it,
# and bundled resources.
artifact.materialize(to="./artifacts/", harness="none")
```

Identity, visibility, layer composition, and audit are unchanged. The custom consumer is responsible for caching and for any translation into its runtime's native format.

### Eval pipeline

```python
suite = client.search_artifacts(type="eval", tags=["regression"], top_k=50)
for descriptor in suite.results:
    artifact = client.load_artifact(descriptor.id)
    artifact.materialize(to=f"./runs/{descriptor.id}/", harness="none")
    run_eval(f"./runs/{descriptor.id}/")
```

`type: eval` is an example extension type identifier (see [Artifact types → Extension types](../authoring/artifact-types)). Podium does not ship an implementation. A deployment that uses it registers the schema and lint rules through `TypeProvider`.

---

## Verify deliveries

Both SDKs run the same delivery check that `podium-mcp` and server-source `podium sync` run. For every `load_artifact` response and every `ok` batch item, the SDK performs these steps:

1. It recomputes `delivery_hash` from the bytes it received, under every signature policy including `never`. It refuses a response whose recomputed hash differs, or that carries no `delivery_hash`, with `materialize.content_hash_mismatch`.
2. It applies its signature policy to `delivery_signature` with the registry-managed verifier.

A response loaded from the [workspace overlay](../deployment/content/layers#workspace-local-overlay) comes from the local filesystem, and the SDK runs no check on it. [Verifying a response](../reference/http-api#verifying-a-response) states the full procedure.

The SDKs keep no revision marks, so the stale `latest` protection that [Verifying deliveries](verifying-deliveries#stale-latest-protection) describes applies to `podium-mcp` only.

### Signature policy

The policy takes `never` or `always`. The SDK resolves it from the first of these sources that is set:

1. The `verify_signatures` argument in Python, or `verifySignatures` in TypeScript.
2. `PODIUM_VERIFY_SIGNATURES`.
3. `defaults.verify_signatures` in `sync.yaml`.
4. The SDK default.

The SDK default is `always` when a verification key is configured, and `never` otherwise. The resolved policy is exposed as `client.verify_signatures` in Python and `client.verifySignatures` in TypeScript.

### Verification keys

A verification key is configured when any of the following holds:

- The client is given a key list.
- `PODIUM_SIGNATURE_VERIFY_KEY` is set.
- `PODIUM_SIGN_KEY_PATH` is set.
- A file is present at the default key path, `~/.podium/standalone/registry-signing.key`.

The key-list argument is `verify_keys` in Python and `verifyKeys` in TypeScript. It takes the `PODIUM_SIGNATURE_VERIFY_KEY` syntax: one or more base64 Ed25519 public keys separated by commas. A key list replaces the variable. Without either, the SDK reads the `public:` and `verify:` lines of the key file at `PODIUM_SIGN_KEY_PATH` or at the default path.

```python
client = Client(
    registry="https://podium.acme.com",
    verify_signatures="always",
    verify_keys="MCowBQYDK2VwAyEA...",
)
```

### Verification dependencies

The Python SDK verifies Ed25519 signatures through the `cryptography` package, which the `podium-sdk[verify]` extra installs. Hash recomputation needs no extra. A client on the standalone registry's machine resolves `always` from the default key file, so it needs the extra. Install the SDK with `pip install 'podium-sdk[verify]'`.

The TypeScript SDK computes SHA-256 and verifies Ed25519 through the Web Crypto API (`globalThis.crypto.subtle`). It adds no dependency on the Node versions its `engines` field names, which are Node 20 and later.

### When the policy resolves

The SDK resolves the policy and the key set once, before its first registry request:

- The Python client resolves in its constructor, including `Client.from_env()`.
- The TypeScript client resolves in `Client.fromEnv()`. A directly constructed client resolves before the first method call that sends a registry request, so `new Client(...)` reads no file.

When the policy is `always` and no usable key resolves, the client fails with `config.signature_provider_unavailable` before any request. The Python constructor raises, and in TypeScript `fromEnv()` or the first registry call rejects. The Python client fails the same way when the policy is `always` and the `podium-sdk[verify]` extra is not installed, and the error names the extra.

### How verification failures surface

| Operation | Result of a failed check |
|:--|:--|
| `load_artifact` | Raises a `RegistryError` that carries the error code. |
| `load_artifacts` item | The item is returned with `status: "error"` and the code. The other items load. |
| `materialize()` | Fetches every large resource and checks each against its link's `content_hash`. When any check fails with `materialize.content_hash_mismatch`, it writes no file of the artifact. |

---

## Edge cases and troubleshooting

### Cache modes

The SDKs keep no content cache of their own, so the cache modes behave as follows:

| Cache mode | SDK behavior |
|:--|:--|
| `always-revalidate` | Fetches from the registry on every call. |
| `offline-first` | Fetches from the registry on every call. |
| `offline-only` | Raises `network.offline_cache_miss`, because there is no cached content to serve. |

### Configuration edge cases

- `PODIUM_SESSION_TOKEN_FILE` and `PODIUM_SESSION_TOKEN_ENV` are read by the MCP server and the CLI. The SDKs do not read them.
- A `verify_signatures` or `verifySignatures` argument other than `never` or `always` fails construction with `ValueError` in Python and `Error` in TypeScript.
- A `PODIUM_VERIFY_SIGNATURES` or `defaults.verify_signatures` value other than `never` or `always` fails resolution with an error that names the value and its source. The SDK never treats an invalid value as unset.
- A variable set to the empty string counts as unset.
- The default policy does not depend on whether the configured key material decodes. `PODIUM_SIGNATURE_VERIFY_KEY=not-base64` selects `always`, and the client then refuses.

### Bulk loading and the materialization rate

The tenant's materialization rate can refuse an item in a `load_artifacts` request. That item, and every item after it in the same request, comes back as `status: "error"` with `quota.materialize_rate_exceeded`, and its error carries `retryable: true`. The SDK still posts the remaining 50-ID chunks after a refusal, and their items are charged and reported on their own.

### Resources and manifests

- A manifest document above the inline cutoff is fetched during `load_artifact` and verified as the fetched bytes. The SDK returns it as text decoded as UTF-8, with each invalid sequence replaced by U+FFFD, and `materialize()` writes that text. For a document that is not valid UTF-8, the SDK's file therefore differs from the bytes `podium sync` writes.
- A batch response body that is not well-formed JSON under the verification procedure fails the whole `load_artifacts` call with `materialize.content_hash_mismatch` and returns no items.

### Troubleshooting

| Symptom | Cause | Fix |
|:--|:--|:--|
| Every signed load fails with `materialize.signature_invalid`. | A key file at the default path, `~/.podium/standalone/registry-signing.key`, is left over from an earlier standalone registry. It counts as a configured key whether or not it holds the key of the registry the client reaches, so the policy resolves to `always` with an unrelated key. | Give the client the registry's key through `verify_keys` or `PODIUM_SIGNATURE_VERIFY_KEY`, or set the policy to `never`. |
| The client fails with `config.signature_provider_unavailable` before any request. | The policy is `always` and no usable key resolves, or the Python `podium-sdk[verify]` extra is missing. | Configure a verification key, or install the SDK with `pip install 'podium-sdk[verify]'`. |
| Requests fail with `auth.token_expired`. | The device-code access token expired. The SDK does not refresh it. | Run `login()`, or the start and finish pair, again. |
| `finish_login` fails with reason `consumed`. | A handle is single-use, and an earlier finish call used it. | Call `start_login()` again to obtain a new handle. |

---

## Related pages

- [Handling artifact responses](handling-artifact-responses): what the consumer does with each manifest field.
- [Browsing the catalog](browsing-the-catalog): the MCP meta-tools that agents use for discovery.
- [Reference → HTTP API](../reference/http-api): the registry endpoints the SDKs call.
