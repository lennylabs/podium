---
title: Hooks
nav_order: 9
description: Hook artifacts run a shell hook_action when a canonical hook_event fires in the harness.
---

# Hooks

This page covers `hook` artifacts: the canonical event names, how to write a hook action, and how harness coverage differs.

A `hook` artifact wires a shell action into a harness lifecycle event. Use it to log, notify, run a check, inject context, or otherwise observe and influence the agent loop.

```markdown
---
type: hook
version: 1.0.0
hook_event: session_end
hook_action: |
  INPUT=$(cat)
  echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] session end: $INPUT" \
    >> ~/.podium/session-audit.log
---
```

A hook has two required fields:

- `hook_event` is one of the canonical event names that Podium defines (see [Canonical events](#canonical-events)). The harness adapter translates the canonical name into the harness's native event vocabulary at materialization time.
- `hook_action` is a shell snippet that runs when the event fires. The harness writes a JSON payload to the action's stdin.

---

## Canonical events

The canonical event names are harness-agnostic, and the adapter translates them. The tables below group the events by concern.

### Session lifecycle

| `hook_event` | Fires when |
|:--|:--|
| `session_start` | An agent session begins or resumes. |
| `session_end` | An agent session terminates. |

### Prompt

| `hook_event` | Fires when |
|:--|:--|
| `user_prompt_submit` | After the user submits a prompt, before the model processes it. Can inject context or block. |

### Tool calls (generic)

These events cover every tool call regardless of the underlying tool.

| `hook_event` | Fires when |
|:--|:--|
| `pre_tool_use` | Before any tool call executes. Can block. |
| `post_tool_use` | After any tool call succeeds. |
| `post_tool_use_failure` | After a tool call fails (error, timeout, denied). |

### Tool calls (subtypes)

Subtype events target one kind of tool call. Use them when the action applies only to that category, such as a formatter on file edits or a secrets scanner on shell commands.

| `hook_event` | Fires when |
|:--|:--|
| `pre_shell_execution` | Before a shell command tool call. |
| `post_shell_execution` | After a shell command tool call. |
| `pre_mcp_execution` | Before an MCP tool call. |
| `post_mcp_execution` | After an MCP tool call. |
| `pre_read_file` | Before the agent reads a file. |
| `post_file_edit` | After the agent edits a file. |

The adapter maps a subtype in one of two ways:

- When the harness exposes a native subtype event, the adapter wires the subtype straight onto it.
- When the harness exposes only the generic tool events, the adapter maps the subtype onto the generic native event and installs a tool-name matcher. Only tool calls in that category then fire the action.

The mappings for specific harnesses:

- **Cursor** has native events. `pre_shell_execution`, `pre_mcp_execution`, `pre_read_file`, and `post_file_edit` map onto `beforeShellExecution`, `beforeMCPExecution`, `beforeReadFile`, and `afterFileEdit`.
- **Claude Code** receives a `PreToolUse` or `PostToolUse` entry with a matcher: `^Bash$` for a shell subtype, `^mcp__` for an MCP subtype, `^Read$` for `pre_read_file`, and `^(Edit|Write|NotebookEdit)$` for `post_file_edit`.
- **Codex** matches its shell subtypes on `^Bash$`.
- **Gemini** matches its shell subtypes on `^run_shell_command$`.

The matcher narrows by tool name alone. An action that depends on the arguments of the call still reads them from the payload on stdin.

### Permission

| `hook_event` | Fires when |
|:--|:--|
| `permission_request` | The harness requests user permission for a sensitive action. |
| `permission_denied` | A tool call is denied (by the user, by policy, or by an auto-deny classifier). |

### Subagent

| `hook_event` | Fires when |
|:--|:--|
| `subagent_start` | A subagent (delegated child) is spawned. |
| `subagent_stop` | A subagent finishes. |

### Turn

| `hook_event` | Fires when |
|:--|:--|
| `stop` | The agent finishes responding (end of turn). |

### Compaction

| `hook_event` | Fires when |
|:--|:--|
| `pre_compact` | Before context compaction. |
| `post_compact` | After context compaction completes. |

### Notifications

| `hook_event` | Fires when |
|:--|:--|
| `notification` | The harness sends a system notification (waiting for input, idle prompt, and similar). |

---

## Reading the payload

The harness writes a JSON payload to stdin, and the harness and the event define its schema. Most harnesses share common fields, such as the session identifier, the working directory, the tool name and arguments for tool events, and the prompt text for `user_prompt_submit`. The exact field set varies.

A simple action reads the payload as a string:

```bash
hook_action: |
  INPUT=$(cat)
  echo "$INPUT" >> ~/.podium/sessions.log
```

For structured handling, use `jq` with defaults, so the action stays portable across harness versions:

```bash
hook_action: |
  INPUT=$(cat)
  CONV_ID=$(echo "$INPUT" | jq -r '.session_id // .conversation_id // "unknown"')
  echo "$CONV_ID,$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    >> ~/.podium/session-stats.csv
```

Declare the `jq` dependency:

```yaml
runtime_requirements:
  system_packages: [jq]
```

[Execution model](bundled-resources#execution-model) describes which consumers check the requirement.

---

## Moving the action into a bundled script

A long shell action embedded in YAML is hard to read. Move complex logic into a bundled script in `scripts/` and have the action invoke it by its package-relative path. The script lives alongside `ARTIFACT.md` and ships with the hook.

```
finance/audit/log-session-end/
├── ARTIFACT.md
└── scripts/
    └── log.sh
```

`ARTIFACT.md`:

```markdown
---
type: hook
name: log-session-end
version: 1.0.0
description: Log session-end events to a local audit file.
tags: [hook, audit]
sensitivity: low
hook_event: session_end
hook_action: |
  bash scripts/log.sh
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

The logic now lives in one place, the YAML stays readable, and the hook is testable in isolation with `bash scripts/log.sh < payload.json`.

The adapter installs the action into the harness's hook configuration, where it resolves from the project root and `scripts/log.sh` does not exist. Each adapter that writes a hook therefore installs the hook's bundled files under `.podium/resources/<artifact-id>/` and rewrites every occurrence of a bundled file's package-relative path in `hook_action` to the installed path. On Claude Code, the action above becomes `bash .podium/resources/finance/audit/log-session-end/scripts/log.sh` in `.claude/settings.json`. `hook_action` is the only field the adapters rewrite this way; the prose of an `agent` or `command` keeps the paths the author wrote. Write the script's path in `hook_action` exactly as it appears in the package, so the rewrite matches it.

Bundled resources materialize with mode 0644, so the action invokes the interpreter explicitly rather than executing the script path directly.

---

## Authoring guidance

- **Treat hooks as shipped code.** A hook's `hook_action` runs on the host with the user's privileges. Review hooks in the source repository's pre-merge CI like any other script the catalog ships. The registry signs each artifact version at ingest by default. The signature shows that the bytes are the ones the registry ingested and says nothing about whether the action is safe. Set `sandbox_profile:` on a sensitive hook, so a host with sandbox capability can constrain the action.
- **Keep actions short.** Move anything longer than a few lines into a bundled script, as shown in [Moving the action into a bundled script](#moving-the-action-into-a-bundled-script).
- **Write a specific description.** "Log session-end events to a local audit file." surfaces in search. "Lifecycle observer." is too vague to surface.
- **Guard against missing payload fields.** Harnesses change their payload schema over time. Use `jq` defaults (`jq -r '.field // empty'`) or guard against missing fields in shell.
- **Pick the canonical event closest to the intent.** `pre_tool_use` covers shell, MCP, file-edit, and any other tool call uniformly, and the adapter translates it to whichever native event the harness emits. Selecting a harness-native event by working around the canonical names makes the artifact non-portable.

---

## Coverage varies by harness

Not every harness implements every canonical event, and coverage is graded per harness rather than per event.

| Grade | Harnesses | Lint, when `target_harnesses:` names the harness |
|:--|:--|:--|
| ✗ | `claude-desktop`, `claude-cowork`, `opencode`, `pi`, and `hermes` | Error. |
| ⚠ | `cursor` | Warning. |

When `target_harnesses:` is absent, ingest stays permissive, and materialization catches a ✗ harness: a `load_artifact` onto it fails with `materialize.untranslatable`.

A harness graded ✓ or ⚠ that has no native mapping for a specific canonical event writes no hook and reports no error. Confirm the event against the harness's own hook documentation before relying on a less common one. Each vendor's hook surface evolves independently, so the harness's documentation is the source of truth for the events it emits. [Configure your harness](../consuming/configure-your-harness#supported-harnesses) lists every supported harness with its adapter value and documentation link.

The `sandbox_profile` row of the [capability matrix in §6.7.1 of the specification](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#671-the-authors-burden) is ✗ for `codex`. A hook that sets `sandbox_profile:` therefore fails materialization onto Codex with `materialize.untranslatable`, even though Codex translates the `hook_event` itself.
