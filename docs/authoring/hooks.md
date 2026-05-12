---
layout: default
title: Hooks
parent: Authoring
nav_order: 8
description: Lifecycle observers with a canonical hook_event taxonomy and a shell hook_action.
---

# Hooks

A `hook` artifact wires a shell action into a harness lifecycle event. Use it to log, notify, run a check, inject context, or otherwise observe and influence the agent loop.

```yaml
---
type: hook
version: 1.0.0
hook_event: stop
hook_action: |
  INPUT=$(cat)
  echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] session end: $INPUT" \
    >> ~/.podium/session-audit.log
---
```

`hook_event` is one of the canonical event names defined by Podium (see [Canonical events](#canonical-events) below). The harness adapter translates the canonical name into the harness's native event vocabulary at materialization time.

`hook_action` is a shell snippet executed when the event fires. The harness writes a JSON payload to the action's stdin.

---

## Canonical events

The canonical event taxonomy stays harness-agnostic. The adapter does the translation. Events are grouped by concern.

**Session lifecycle**

| `hook_event` | Fires when |
|:--|:--|
| `session_start` | An agent session begins or resumes. |
| `session_end` | An agent session terminates. |

**Prompt**

| `hook_event` | Fires when |
|:--|:--|
| `user_prompt_submit` | After the user submits a prompt, before the model processes it. Can inject context or block. |

**Tool calls (generic)**

These cover every tool call regardless of the underlying tool.

| `hook_event` | Fires when |
|:--|:--|
| `pre_tool_use` | Before any tool call executes. Can block. |
| `post_tool_use` | After any tool call succeeds. |
| `post_tool_use_failure` | After a tool call fails (error, timeout, denied). |

**Tool calls (subtypes)**

Subtype events target a specific kind of tool call. Use them when the action only applies to that category (e.g., a formatter on file edits, a secrets scanner on shell commands). The adapter wires the subtype to the harness's native subtype event when one exists, or installs a generic hook with a tool-name matcher otherwise.

| `hook_event` | Fires when |
|:--|:--|
| `pre_shell_execution` | Before a shell command tool call. |
| `post_shell_execution` | After a shell command tool call. |
| `pre_mcp_execution` | Before an MCP tool call. |
| `post_mcp_execution` | After an MCP tool call. |
| `pre_read_file` | Before the agent reads a file. |
| `post_file_edit` | After the agent edits a file. |

Don't declare both a generic hook (`pre_tool_use`) and the corresponding subtype hook (`pre_shell_execution`) for the same artifact; lint warns when this happens. Pick one level of specificity.

**Permission**

| `hook_event` | Fires when |
|:--|:--|
| `permission_request` | The harness requests user permission for a sensitive action. |
| `permission_denied` | A tool call is denied (by the user, by policy, or by an auto-deny classifier). |

**Subagent**

| `hook_event` | Fires when |
|:--|:--|
| `subagent_start` | A subagent (delegated child) is spawned. |
| `subagent_stop` | A subagent finishes. |

**Turn**

| `hook_event` | Fires when |
|:--|:--|
| `stop` | The agent finishes responding (end of turn). |

**Compaction**

| `hook_event` | Fires when |
|:--|:--|
| `pre_compact` | Before context compaction. |
| `post_compact` | After context compaction completes. |

**Notifications**

| `hook_event` | Fires when |
|:--|:--|
| `notification` | The harness sends a system notification (waiting for input, idle prompt, and similar). |

---

## Coverage varies by harness

Not every harness implements every event in the canonical list. When the configured harness adapter does not support the chosen event, lint rejects ingest unless `target_harnesses:` excludes the unsupported harness.

For the events a specific harness emits, refer to that harness's hook documentation. The harness's own docs are the source of truth, since each vendor's surface evolves independently. The full roster of supported harnesses (with adapter values and documentation links) is in [Configure your harness](../consuming/configure-your-harness#supported-harnesses).

---

## Payload handling

The harness writes a JSON payload to stdin. The schema is harness-defined and event-defined. Common fields appear across most harnesses (session identifier, working directory, tool name and arguments for tool events, prompt text for `user_prompt_submit`), but the exact field set varies.

A simple action reads the payload as a string:

```bash
hook_action: |
  INPUT=$(cat)
  echo "$INPUT" >> ~/.podium/sessions.log
```

For structured handling, use `jq` with defaults so the action stays portable across harness versions:

```bash
hook_action: |
  INPUT=$(cat)
  CONV_ID=$(echo "$INPUT" | jq -r '.session_id // .conversation_id // "unknown"')
  echo "$CONV_ID,$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    >> ~/.podium/session-stats.csv
```

Declare the dependency:

```yaml
runtime_requirements:
  system_packages: [jq]
```

The harness refuses to materialize when a system package isn't available.

---

## Authoring guidance

- **Hooks ship code.** A hook's `hook_action` runs on the host with the user's privileges. Treat hooks like any other script the catalog ships: review, sign, and consider sandboxing. The `sandbox_profile:` field applies; lint requires it for hooks at sensitivity ≥ medium.
- **Keep actions short.** A long shell action embedded in YAML gets ugly. Move complex logic into a bundled script (in `scripts/`) and have the action invoke it. The script lives alongside `ARTIFACT.md` and ships with the hook.
- **Make the description specific.** "Log session-end events to a local audit file." is fine. "Lifecycle observer." is too vague to surface in search.
- **Don't depend on payload fields.** Harnesses change their payload schema over time. Use `jq` defaults (`jq -r '.field // empty'`) or guard against missing fields in shell.
- **Pick the canonical event closest to the intent.** `pre_tool_use` covers shell, MCP, file-edit, and any other tool call uniformly; the adapter translates to whichever native event the harness emits. Selecting a more specific harness-native event by working around the canonical taxonomy makes the artifact non-portable.

---

## Example: bundled-script pattern

```
finance/audit/log-session-end/
├── ARTIFACT.md
└── scripts/
    └── log.sh
```

`ARTIFACT.md`:

```yaml
---
type: hook
name: log-session-end
version: 1.0.0
description: Log session-end events to a local audit file.
tags: [hook, audit]
sensitivity: low
hook_event: stop
hook_action: |
  scripts/log.sh
runtime_requirements:
  system_packages: [jq]
---
```

`scripts/log.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

INPUT=$(cat)
LOG_FILE="${HOME}/.podium/session-audit.log"

CONV_ID=$(echo "$INPUT" | jq -r '.session_id // .conversation_id // "unknown"')
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "[${TIMESTAMP}] session end: ${CONV_ID}" >> "${LOG_FILE}"
```

The hook is now testable in isolation (`scripts/log.sh < payload.json`), the logic is in one place, and the YAML stays readable.
