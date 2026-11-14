---
title: Handling artifact responses
nav_order: 5
description: "What a consumer does with the manifest and materialized files returned by load_artifact: route by hints, honor safety constraints, verify requirements, register MCP servers, walk dependencies, and fetch external resources."
---

# Handling artifact responses

This page is a reference for what a consumer receives from `load_artifact` and what it does with each manifest field. It is for developers of SDK consumers and of hosts that run artifacts loaded through the MCP server.

A loaded artifact consists of a manifest and a file tree of bundled resources. Some manifest fields are advisory signals the consumer acts on when it can. Other fields are safety constraints the consumer must honor. The sections below group the fields by concern and state the consumer-side action for each.

Related references:

- [Frontmatter reference](../authoring/frontmatter-reference) defines the schema and the contents of each field.
- [Bundled resources](../authoring/bundled-resources) covers the layout of the file tree returned with the manifest.

---

## Inline content and materialized files

`load_artifact` returns the manifest separately from the bundled resources. The consumer path determines which content arrives in the response and which content is written to the filesystem.

![Two columns compare what load_artifact yields to each consumer. Through the MCP server, the agent's tool result holds the manifest body and the paths of the materialized files, with every resource written to disk. Through the SDK, load_artifact returns the manifest body with inline resources and references in memory, and a later materialize call writes the files. Bytes at or below 256 KB ride inline, while larger bytes and large manifests arrive as presigned URLs from object storage.](../assets/diagrams/materialization-consumer-views.svg)

<!--
ASCII fallback for the diagram above (load_artifact: what the consumer holds):

  MCP server, into the agent
    Tool result (in the model's context):
      {
        manifest_body: "Run variance",
        materialized_at: [ out/SKILL.md, out/scripts/run.py ]
      }
    Every bundled resource is written to disk; the agent opens files, so the
    256 KB cutoff never enters the context window.

  SDK, into your program
    load_artifact() returns, in memory:
      manifest_body
      resources{}        inline bytes
      large_resources{}  references
    materialize(to="out/") writes to disk: it writes the canonical layout and
    fetches each referenced resource. A program reads the manifest first and
    writes the files when it chooses.

  Bytes at or below 256 KB ride inline in the response. A larger resource, and
  a manifest above the cutoff, arrive as a presigned URL the consumer fetches
  from content-addressed object storage.
-->

### Through the MCP server

The tool result carries the manifest body inline, together with the paths of the files the server wrote. Before it returns, the server fetches every bundled resource, runs the harness adapter, and writes the resources to the host filesystem.

The agent opens a resource from its file path, so the resource bytes stay out of the model's context window. The size of an individual resource does not change this. The agent receives the manifest body and the file paths whatever the resources contain.

### Through the SDK

`load_artifact` returns an in-memory object that holds the manifest body and the bundled resources small enough to travel inline. A resource above the inline cutoff arrives as a reference rather than as bytes, with the exception that [Edge cases](#edge-cases) lists.

Nothing reaches the filesystem until a separate `materialize(to=...)` call. That call writes the canonical layout and fetches each referenced resource. A programmatic consumer can therefore inspect the manifest first and write the resources only when it chooses.

### The inline cutoff

The boundary between an inline byte payload and an out-of-band reference is a fixed threshold of 256 KB:

- A resource at or below the threshold travels in the response body. A small fixture therefore costs no second round-trip.
- A larger resource travels as a presigned URL into content-addressed object storage. The consumer fetches it directly, and the registry does not proxy the bytes.
- A manifest document above the threshold follows the same rule.

This arrangement keeps the registry response small and keeps large content out of the model's context window. Large bytes live in object storage, where they are content-addressed and cacheable.

[The HTTP API reference](../reference/http-api) lists the response fields. [Bundled resources](../authoring/bundled-resources#size-thresholds) covers the size limits an author works within.

---

## Quick reference

| Field | Consumer action |
|:--|:--|
| `model_class_hint` | Route to a model in the named tier or above. |
| `effort_hint` | Set the thinking budget when the runtime has a thinking-budget control. |
| `target_harnesses` | Skip when the consumer's harness is not in the list. |
| `sensitivity` | Apply trust region; gate high-sensitivity execution. |
| `sandbox_profile` | Honor the profile when executing bundled scripts. |
| `requiresApproval` | Prompt before invoking the named tools. |
| `runtime_requirements` | Verify before materializing. Reject on mismatch. |
| `mcpServers` | Register the named servers with the host. |
| `delegates_to` | Walk the dependency. Apply the same handling to each. |
| `hook_event` / `hook_action` | Wire into the agent loop at the canonical event. |
| `rule_mode` | Load via the rule discipline (always, glob, auto, explicit). |
| `external_resources` | Fetch from `url` and verify against `sha256` and `signature`. |
| `description` / `when_to_use` | Surface to the user or agent for selection. |
| `tags` | Filter and group views. |
| `deprecated` / `replaced_by` | Warn or offer the replacement. |
| Provenance markers in prose | Wrap imported regions in the host's trust-region marker. |

---

## Routing and model selection

The routing fields are advisory hints about the model tier and reasoning budget the artifact assumes. Route accordingly when the runtime exposes the relevant control, and ignore the hint when it does not. A hint never fails a load. [Hints](../authoring/hints) lists the values and the author-side rationale.

### `model_class_hint`

Values: `nano`, `small`, `medium`, `large`, or `frontier`.

- Route the artifact to a model in the named tier or above.
- When the named tier is not configured, fall back to the best available tier. Do not refuse the load.
- LangChain and Bedrock callers map the tier to a specific model ID. Custom orchestrators pick from their configured model pool.

### `effort_hint`

Values: `low`, `medium`, `high`, or `max`.

- Set the reasoning budget when the runtime has a thinking-budget control, such as extended thinking or reasoning effort.
- A higher tier can also justify longer timeouts and larger retry budgets on the consumer side.

### `target_harnesses`

An optional list of harness IDs. Authors set it when an artifact assumes harness-specific features.

- Skip the artifact when the consumer's harness is not in the list.
- Record the skip in the logs.

---

## Safety and trust

The safety fields constrain what the consumer may do with the artifact. They are mandatory.

### `sensitivity`

Values: `low`, `medium`, or `high`.

- Apply the trust region the host runtime uses for content of this level.
- Display high-sensitivity prose to the user, or log it, before taking any action it implies.
- Treat bundled scripts at the artifact's sensitivity. A high-sensitivity skill that ships a Python script ships high-sensitivity code.

### `sandbox_profile`

Values: `unrestricted`, `read-only-fs`, `network-isolated`, or `seccomp-strict`.

Honor the profile when executing bundled scripts. Refuse to execute under a profile the consumer cannot enforce, unless an explicit override is set, in which case log a prominent warning.

| Profile | Restriction |
|:--|:--|
| `unrestricted` | None. |
| `read-only-fs` | The filesystem is read-only outside the materialization destination. |
| `network-isolated` | Outbound network from the script is blocked. |
| `seccomp-strict` | The strict syscall allowlist that ships with Podium applies. |

### `requiresApproval`

A list of `{tool, reason}` entries, where `reason` is optional.

- Prompt the user before the artifact's execution invokes each named tool. This prompt is independent of any approval prompts the harness applies by default.
- Typical uses are irreversible actions, such as payment submission, data deletion, and outbound notifications.

### Content provenance markers in prose

The manifest body can wrap regions of imported content in provenance markers:

```markdown
<!-- begin imported source="https://wiki.example.com/policy/payments" -->
imported text
<!-- end imported -->
```

Treat an imported region as data rather than instruction. A host that implements trust regions, such as Claude's `<untrusted-data>` convention, wraps the imported text in the corresponding marker before passing the prose to the model. This wrapping is the primary defense against prompt injection from manifests that aggregate external content.

---

## Capability declarations

The capability fields state what the artifact needs in order to run.

### `runtime_requirements`

A map of runtime names to version constraints.

- Verify each requirement before materializing.
- When any requirement is unavailable, reject the load with a structured code, and surface the failure to the user.
- Typical keys are `python`, `node`, and `system_packages`. Extension keys are accepted, and a consumer ignores keys it does not recognize.

### `mcpServers`

A list of MCP server registration objects.

- Register the named servers with the host's MCP plugin layer so the agent can reach them.
- Long-running agent sessions restart when a registered server's record changes.
- When the host wants the full registration record, read it from the matched `mcp-server` artifact, which carries the canonical registration.

The registry resolves each entry to an `mcp-server`-type artifact. It derives a `server_identifier` from the entry and matches it against the `server_identifier` of the ingested `mcp-server` artifacts. The derived identifier is one of the following:

- `<command>:<first non-flag argument>`, for example `npx:@acme/finance-warehouse-mcp`.
- The bare command, when every argument is a flag.
- The entry's `transport` value, when the entry declares no command.

A match records the cross-type dependency edge. An entry that resolves to nothing records no edge.

### `delegates_to`

A list of canonical artifact IDs.

- Walk the dependency: loop over the delegate IDs, call `load_artifact` on each, and apply the same response handling to each result.
- The caller implements the loop and decides the traversal depth. The SDK provides no forward-walk helper.
- Visibility filtering applies. Delegates the caller cannot see are silently excluded, as they are on the registry-side discovery surface.

The only dependency helper on the client is `dependents_of(id)`. It returns the reverse edges, which are the artifacts that depend on a given artifact, for impact analysis. It does not walk forward.

### `hook_event` and `hook_action`

These fields apply to `type: hook` artifacts.

- Wire the hook into the agent loop at the named event. The [Hooks](../authoring/hooks) page covers the canonical event taxonomy. The harness adapter translates each canonical name to the host's native event vocabulary.
- `hook_action` is a shell snippet. Execute it under the artifact's sandbox profile.

### `rule_mode`

This field applies to `type: rule` artifacts.

| Value | Load behavior |
|:--|:--|
| `always` | Load on every session or every turn. |
| `glob` | Load when the session touches a file that matches `rule_globs:`. |
| `auto` | Let the harness's autoload heuristic decide, based on `rule_description`. |
| `explicit` | Load only when the user references the rule by name, such as through a slash command or `@rule-name`. |

---

## Bundled files and external resources

The materialized file tree lands at the configured destination path. The manifest references the files inline in its prose, and there is no separate file list. [Inline content and materialized files](#inline-content-and-materialized-files) covers which bytes arrive in the response and which are written to disk.

### Bundled files

- Resolve prose references relative to the artifact root in the materialized tree, for example `scripts/variance.py` or `assets/template.j2`.
- The MCP server writes each file atomically (`.tmp` + rename), so the destination is consistent once `load_artifact` returns.
- [Bundled resources](../authoring/bundled-resources) covers the file-layout conventions.

### `external_resources`

A list of pointer objects. Each entry has `path`, `url`, `sha256`, and `size`, and optionally `signature`.

The registry stores the pointer record and never the bytes, which do not transit the registry. Materialization writes the bundled resources in the artifact directory and does not fetch external resources. For each entry, the consumer does the following:

1. Fetch the bytes from `url`.
2. Verify the bytes against `sha256`.
3. Verify `signature`, when the entry carries one.

---

## Discoverability and presentation

The presentation fields help the user or the agent select an artifact. They carry no execution semantics.

| Field | Consumer action |
|:--|:--|
| `description` and `when_to_use` | Show in artifact pickers, slash-command lists, and search-result summaries. `when_to_use` is the canonical signal for deciding at runtime whether to pick the artifact. |
| `tags` | Filter and group views. |
| `deprecated` and `replaced_by` | Warn the user when loading a deprecated artifact. Offer `replaced_by` as a suggested upgrade target. Redirect automatically only when the consumer is explicitly configured to do so. |
| `release_notes` | Show on update prompts and record in audit logs. |

---

## Composing multiple artifacts in one session

A consumer that loads several artifacts in one session composes their constraints. Agents that walk dependencies or preload a working set do this routinely.

| Aspect | Composition rule |
|:--|:--|
| `sandbox_profile` | Keep each artifact's profile and apply it when that artifact's bundled scripts run. The profiles restrict different resources, so no profile stands in for another. |
| `sensitivity` | Take the highest value. |
| `requiresApproval` | Union the tool lists. |
| `mcpServers` | Union by `name`; deep-merge the per-name record. |
| `runtime_requirements` | Union with most-restrictive version constraint per key. |
| `target_harnesses` | Intersect; surface an empty intersection as an inconsistency. |
| `model_class_hint`, `effort_hint` | Take the highest tier; route once for the session. |

A host can instead route per artifact, so that different models answer different artifacts in one session. Such a consumer keeps the constraints per artifact and applies them at invocation time rather than at session start.

---

## End-to-end SDK example

The example reads the manifest fields from a response and passes them to the runtime. The SDK's `LoadedArtifact` exposes the prose body as `manifest_body` and the raw frontmatter as the `frontmatter` string. The consumer parses the frontmatter to read the advisory and constraint fields.

```python
import yaml
from podium import Client

client = Client.from_env()
result = client.load_artifact("finance/ap/pay-invoice")

# The advisory and constraint fields live in the raw frontmatter YAML.
fm = yaml.safe_load(result.frontmatter) or {}

# Routing
model = pick_model_for_tier(fm.get("model_class_hint", "medium"))
thinking_budget = budget_for_effort(fm.get("effort_hint", "low"))

# Safety
if fm.get("sensitivity") == "high":
    audit.log_high_sensitivity_load(result.id)
sandbox = compile_sandbox_profile(fm.get("sandbox_profile"))
approval_tools = {entry["tool"] for entry in fm.get("requiresApproval", [])}

# Capability
verify_runtime(fm.get("runtime_requirements", {}))
for server in fm.get("mcpServers", []):
    host.register_mcp_server(server)

# Walk delegates: load_artifact returns the delegate IDs in the
# frontmatter; the caller loops over them and loads each one. The SDK
# does not provide a forward-walk helper.
for dep_id in fm.get("delegates_to", []):
    handle_response(client.load_artifact(dep_id))
```

The TypeScript SDK's `LoadedArtifact` exposes the same two fields, `manifest_body` and `frontmatter`.

---

## Error handling

`load_artifact` returns structured error codes. The consumer maps each code onto a retry, fallback, or surface behavior. [Error codes](../reference/error-codes) lists the full namespace. The codes below are the ones a consumer typically handles.

| Code | Consumer action |
|:--|:--|
| `materialize.signature_invalid` | Refuse to load; surface to the user. Do not retry. |
| `materialize.signature_missing` | Refuse to load; surface to the user. Do not retry. |
| `materialize.runtime_unavailable` | Surface the missing runtime requirement. Offer to install or pick a different artifact. |
| `materialize.hook_failed` | Skip the hook; continue when other artifacts load successfully. Log the failure. |
| `config.unknown_harness` | Configuration error on the consumer side. Refuse and surface. |
| `visibility.denied` | The caller lacks visibility for the artifact. Refuse and surface. |
| `quota.materialize_rate_exceeded` | Back off and retry. Surface the quota state to the user. |
| `registry.read_only` | The registry is degraded. Continue serving cached content; mark subsequent loads as cache-only. |

---

## Edge cases

**Resources held inline.** A bundled resource the registry holds inline on the manifest record travels in the response body at any size. Every resource of an artifact ingested while no object store was configured is held this way.
