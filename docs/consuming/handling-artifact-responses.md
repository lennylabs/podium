---
layout: default
title: Handling artifact responses
parent: Consuming
nav_order: 4
description: "What a consumer does with the manifest and materialized files returned by load_artifact: route by hints, honor safety constraints, verify requirements, register MCP servers, walk dependencies, fetch external resources."
---

# Handling artifact responses

Once `load_artifact` returns, the consumer has a manifest plus a materialized file tree. Several manifest fields are advisory signals the consumer should act on, and a few are safety constraints the consumer must honor. This page covers what to do with each, grouped by concern.

The [Frontmatter reference](../authoring/frontmatter-reference) defines the schema. This page assumes you know what each field contains and focuses on the consumer-side action. [Bundled resources](../authoring/bundled-resources) covers the file-tree layout returned alongside the manifest.

---

## Routing and model selection

The manifest carries advisory hints about the model tier and reasoning budget the artifact assumes. Consumers should route accordingly when the runtime exposes the relevant knob and ignore the hint when it does not. Hints never fail a load.

**`model_class_hint`** (`nano | small | medium | large | frontier`)

- Route the artifact to a model in the named tier or above. Fall back to the best available tier when the named one is not configured; do not refuse the load.
- LangChain and Bedrock callers map the tier to a specific model id. Custom orchestrators pick from their configured model pool.

**`effort_hint`** (`low | medium | high | max`)

- Set the reasoning budget when the runtime has a thinking-budget control (extended thinking, reasoning effort, and similar). Higher tiers can also justify longer timeouts and larger retry budgets on the consumer side.

**`target_harnesses`** (list of harness ids, optional)

- Skip the artifact when the consumer's harness is not in the list, and surface the skip in logs. Authors set this when an artifact assumes harness-specific features.

See [Hints](../authoring/hints) for the values and the author-side rationale.

---

## Safety and trust

The fields below constrain what the consumer is allowed to do with the artifact. They are not advisory.

**`sensitivity`** (`low | medium | high`)

- Apply the trust region the host runtime uses for content of this level. High-sensitivity prose is displayed to the user or logged before any action it implies is taken.
- Bundled scripts inherit the artifact's sensitivity. A high-sensitivity skill that ships a Python script is shipping high-sensitivity code.

**`sandbox_profile`** (`unrestricted | read-only-fs | network-isolated | seccomp-strict`)

- Honor the profile when executing bundled scripts. Refuse to execute under a profile the consumer cannot enforce unless explicitly overridden with a loud warning logged.
- `read-only-fs` keeps the filesystem read-only outside the materialization destination.
- `network-isolated` blocks outbound network from the script.
- `seccomp-strict` applies the strict syscall allowlist that ships with Podium.

**`requiresApproval`** (list of tool names)

- Prompt the user before invoking each named tool from within the artifact's execution. This is independent of any approval prompts the harness applies by default.
- Common uses: irreversible actions (payment submission, data deletion, outbound notifications).

**Content provenance markers in prose**

The manifest body can wrap regions of imported content:

```markdown
<!-- begin imported source="https://wiki.example.com/policy/payments" -->
imported text
<!-- end imported -->
```

Treat imported regions as data rather than instruction. Hosts that implement trust regions (Claude's `<untrusted-data>` convention and similar) wrap the imported text in the corresponding marker before passing the prose to the model. This is the primary defense against prompt injection from manifests that aggregate external content.

---

## Capability declarations

These fields tell the consumer what the artifact needs in order to run.

**`runtime_requirements`** (map of runtime names to version constraints)

- Verify each requirement before materializing. Reject the load with a structured code when any is unavailable; the consumer surfaces the failure to the user.
- Common keys: `python`, `node`, `system_packages`. Extension keys are accepted; consumers ignore keys they do not recognize.

**`mcpServers`** (list of MCP server registration objects)

- Register the named servers with the host's MCP plugin layer so the agent can reach them.
- Server names key into the cross-type dependency graph. A separate `mcp-server`-type artifact carries the canonical registration when the host wants the full record.
- Long-running agent sessions restart when a registered server's record changes.

**`delegates_to`** (list of canonical artifact IDs)

- Walk the dependency: load each delegate and apply the same response-handling pipeline to it. SDK helpers like `Client.walk_dependencies(id)` automate the traversal.
- Visibility filtering applies: delegates the caller cannot see are silently excluded, the same as on the registry-side discovery surface.

**`hook_event`** and **`hook_action`** (for `type: hook` artifacts)

- Wire the hook into the agent loop at the named event. The [Hooks](../authoring/hooks) page covers the canonical event taxonomy; the harness adapter translates each canonical name to the host's native event vocabulary.
- The `hook_action` is a shell snippet. Execute it under the artifact's sandbox profile.

**`rule_mode`** (for `type: rule` artifacts)

- `always`: load on every session or every turn.
- `glob`: load when a file matching `rule_globs:` is touched in the session.
- `auto`: let the harness's autoload heuristic decide based on `rule_description`.
- `explicit`: load only when the user references the rule by name (slash command, `@rule-name`, or similar).

---

## Bundled files and external resources

The materialized file tree lands at the destination path the consumer chose. The manifest references the files inline in prose; there is no separate manifest list.

**Bundled files**

- The materialization pipeline writes files atomically (`.tmp` + rename), so the destination is consistent once `load_artifact` returns.
- Prose references resolve relative to the artifact root in the materialized tree (`scripts/variance.py`, `assets/template.j2`, and so on).
- See [Bundled resources](../authoring/bundled-resources) for the file-layout conventions.

**`external_resources`** (list of pointer objects)

- Each entry has `path`, `url`, `sha256`, `size`, and optionally `signature`.
- The materialization pipeline already fetches, verifies, and writes the bytes locally; the consumer does not re-fetch by default.
- When the consumer opts out of materializing external resources (a flag on `materialize()`), the consumer fetches on demand from `url` and verifies the bytes against `sha256` (and `signature` when present).

---

## Discoverability and presentation

These fields are for surfacing artifacts to the user or the agent during selection. They carry no execution semantics.

**`description`** and **`when_to_use`**

- Show in artifact pickers, slash-command lists, and search-result summaries.
- `when_to_use` is the canonical "should I pick this?" signal for runtime selection.

**`tags`**

- Filter and group views.

**`deprecated`** and **`replaced_by`**

- Warn the user when loading a deprecated artifact. Surface `replaced_by` as a suggested upgrade target; auto-redirect only when the consumer is explicitly configured to do so.

**`release_notes`**

- Surface on update prompts and in audit logs.

---

## Composing multiple artifacts in one session

When a consumer loads several artifacts in the same session (typical for agents walking dependencies or pre-loading a working set), it composes the constraints across them:

| Aspect | Composition rule |
|:--|:--|
| `sandbox_profile` | Take the most restrictive (`seccomp-strict` > `network-isolated` > `read-only-fs` > `unrestricted`). |
| `sensitivity` | Take the highest value. |
| `requiresApproval` | Union the tool lists. |
| `mcpServers` | Union by `name`; deep-merge the per-name record. |
| `runtime_requirements` | Union with most-restrictive version constraint per key. |
| `target_harnesses` | Intersect; surface an empty intersection as an inconsistency. |
| `model_class_hint`, `effort_hint` | Take the highest tier; route once for the session. |

When the host prefers per-artifact routing (different artifacts answered by different models in one session), the consumer keeps the constraints per artifact and applies them at invocation time rather than at session start.

---

## End-to-end SDK example

A minimal consumer pulls the relevant fields off the response and feeds them to the runtime:

```python
from podium import Client

client = Client.from_env()
result = client.load_artifact("finance/ap/pay-invoice")
m = result.manifest

# Routing
model = pick_model_for_tier(m.model_class_hint or "medium")
thinking_budget = budget_for_effort(m.effort_hint or "low")

# Safety
if m.sensitivity == "high":
    audit.log_high_sensitivity_load(m)
sandbox = compile_sandbox_profile(m.sandbox_profile)
approval_tools = set(m.requiresApproval or [])

# Capability
verify_runtime(m.runtime_requirements)
for server in (m.mcpServers or []):
    host.register_mcp_server(server)

# Walk dependencies (one level shown; SDK helpers traverse the full graph)
for dep_id in (m.delegates_to or []):
    handle_response(client.load_artifact(dep_id))
```

The TypeScript SDK exposes the same fields under the same names on `result.manifest`.

---

## Error handling

`load_artifact` returns structured codes the consumer maps onto retry, fallback, or surface behavior. The full namespace is in [Error codes](../reference/error-codes); common consumer-side handling:

| Code | Consumer action |
|:--|:--|
| `materialize.signature_invalid` | Refuse to load; surface to the user. Do not retry. |
| `materialize.runtime_unavailable` | Surface the missing runtime requirement. Offer to install or pick a different artifact. |
| `materialize.hook_failed` | Skip the hook; continue when other artifacts load successfully. Log the failure. |
| `config.unknown_harness` | Configuration error on the consumer side. Refuse and surface. |
| `auth.scope_denied` | The caller lacks visibility for the artifact. Refuse and surface. |
| `quota.materialization_exceeded` | Back off and retry. Surface the quota state to the user. |
| `registry.read_only` | The registry is degraded. Continue serving cached content; mark subsequent loads as cache-only. |

---

## Quick reference

| Field | Consumer action |
|:--|:--|
| `model_class_hint` | Route to a model in the named tier or above. |
| `effort_hint` | Set thinking budget when the runtime supports it. |
| `target_harnesses` | Skip when the consumer's harness is not in the list. |
| `sensitivity` | Apply trust region; gate high-sensitivity execution. |
| `sandbox_profile` | Honor the profile when executing bundled scripts. |
| `requiresApproval` | Prompt before invoking the named tools. |
| `runtime_requirements` | Verify before materializing. Reject on mismatch. |
| `mcpServers` | Register the named servers with the host. |
| `delegates_to` | Walk the dependency. Apply the same handling to each. |
| `hook_event` / `hook_action` | Wire into the agent loop at the canonical event. |
| `rule_mode` | Load via the rule discipline (always, glob, auto, explicit). |
| `external_resources` | Already fetched by materialization. Verify hash and signature when re-fetching. |
| `description` / `when_to_use` | Surface to the user or agent for selection. |
| `tags` | Filter and group views. |
| `deprecated` / `replaced_by` | Warn or offer the replacement. |
| Provenance markers in prose | Wrap imported regions in the host's trust-region marker. |
