---
layout: default
title: Hooks
parent: Authoring
nav_order: 6
description: Lifecycle observers with hook_event and hook_action.
---

# Hooks

A `hook` artifact wires a shell action into a harness lifecycle event. Use it to log, notify, run a check, or otherwise observe and influence the agent loop.

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
  INPUT=$(cat)
  echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] session end: $INPUT" \
    >> ~/.podium/session-audit.log
---
```

`hook_event` is the lifecycle event name. Valid values are harness-defined; common ones are `stop`, `preCompact`, `sessionStart`, `sessionEnd`. The harness fires the event and passes a JSON payload on stdin to the action.

`hook_action` is a shell snippet executed when the event fires. Anything you can run from a shell works.

---

## Common events

The exact set of events varies by harness. The patterns below are common enough to be worth knowing.

| Event | Fires when |
|:--|:--|
| `sessionStart` | A new agent session begins. |
| `sessionEnd` / `stop` | The session completes (or is interrupted). |
| `preCompact` | The harness is about to compact the conversation context. |
| `postCompact` | After compaction. |
| `toolUse` | A tool call is about to run (typically with the tool name and arguments in the payload). |

Cursor's hook system, Claude Code's hook system, and similar harnesses each have their own event vocabulary. Check the harness's docs for the events it actually emits.

---

## Payload handling

The harness fires the event and passes a JSON payload on stdin. The schema varies by event. A typical payload for a `stop` event might be:

```json
{
  "conversation_id": "abc123",
  "hook_event_name": "stop",
  "workspace_roots": ["/Users/joan/projects/foo"],
  "duration_seconds": 142,
  "messages_count": 17
}
```

A simple action reads the payload as a string and uses it directly:

```bash
hook_action: |
  INPUT=$(cat)
  echo "$INPUT" >> ~/.podium/sessions.log
```

For structured handling, use `jq`:

```bash
hook_action: |
  INPUT=$(cat)
  CONV_ID=$(echo "$INPUT" | jq -r '.conversation_id')
  DURATION=$(echo "$INPUT" | jq -r '.duration_seconds')
  echo "$CONV_ID,$DURATION,$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    >> ~/.podium/session-stats.csv
```

Declare the dependency:

```yaml
runtime_requirements:
  system_packages: [jq]
```

The harness refuses to materialize when a system package isn't available.

---

## Per-harness support

Hook support varies. The capability matrix in [§6.7.1 of the spec](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#671-the-authors-burden) records which harnesses support `hook_event` natively.

| Harness | Hook support |
|:--|:--|
| `claude-code` | ✓ Native hook system. |
| `cursor` | ✓ Native hook system. |
| `codex` | ✗ No hook surface today. |
| `opencode` | ⚠ Partial; some events available. |
| `gemini` | ✗ No hook surface today. |
| `pi` | ⚠ Partial. |
| `hermes` | ⚠ Partial. |

For unsupported harnesses, lint rejects ingest unless `target_harnesses:` excludes the harness. For partial support, lint warns when the specific `hook_event` value isn't in the supported set for the harness.

---

## What the adapter writes

| Harness | Output |
|:--|:--|
| `claude-code` | `.claude/hooks/<name>.json` (or the equivalent native hook config). |
| `cursor` | `.cursor/hooks/<name>.json` with the action wrapped in Cursor's hook descriptor. |
| `opencode` | `AGENTS.md` injection where supported; standalone hook file otherwise. |
| Others | Per-harness; see the spec capability matrix. |

The shell action is preserved verbatim; the adapter wraps it in the harness's expected envelope (event name, file format, etc.).

---

## Authoring guidance

- **Hooks ship code.** A hook's `hook_action` runs on the host with the user's privileges. Treat hooks like any other script the catalog ships: review, sign, and consider sandboxing. The `sandbox_profile:` field applies; lint requires it for hooks at sensitivity ≥ medium.
- **Keep actions short.** A long shell action embedded in YAML gets ugly. Move complex logic into a bundled script (in `scripts/`) and have the action invoke it. The script lives alongside `ARTIFACT.md` and ships with the hook.
- **Make the description specific.** "Log session-end events" is fine. "Lifecycle observer" is too vague to surface in search. Authors who write good descriptions get used.
- **Don't depend on payload fields.** Harnesses change their payload schema over time. Use `jq` defaults (`jq -r '.field // empty'`) or guard against missing fields in shell.

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

CONV_ID=$(echo "$INPUT" | jq -r '.conversation_id // "unknown"')
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "[${TIMESTAMP}] session end: ${CONV_ID}" >> "${LOG_FILE}"
```

The hook is now testable in isolation (`scripts/log.sh < payload.json`), the logic is in one place, and the YAML stays readable.
