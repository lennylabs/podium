---
layout: default
title: Artifact types
parent: Authoring
nav_order: 2
description: Built-in artifact types (skill, agent, context, command, rule, hook, mcp-server) and what each is for.
---

# Artifact types

Every artifact declares a `type:` in its frontmatter. The type decides how the registry indexes it, what lint rules apply, and how the harness adapter translates it at delivery time. Built-in types include:

- [`skill`](#skill): instructions loaded into the agent's context on demand.
- [`agent`](#agent): a complete agent definition meant to run as a delegated child.
- [`context`](#context): pure reference material.
- [`command`](#command): parameterized templates a human invokes.
- [`rule`](#rule): passive context loaded by the harness based on a `rule_mode`.
- [`hook`](#hook): lifecycle observers with a shell action.
- [`mcp-server`](#mcp-server): MCP server registrations.

Extension types register through the `TypeProvider` SPI; they get schemas and lint rules but no conformance commitment beyond what the type owner specifies.

---

## skill

A `skill` is a chunk of instructions, optionally with bundled scripts, that the agent loads into its context when it decides the skill matches the situation.

```yaml
---
type: skill
name: run-variance-analysis
version: 1.0.0
description: Flag unusual variance vs. forecast after month-end close.
when_to_use:
  - "After month-end close, when reviewing financial performance."
tags: [finance, close, variance]
sensitivity: low
runtime_requirements:
  python: ">=3.10"
---

Compare actuals vs. forecast for the most recent close period.
For each line item, flag variances above the threshold defined in
your team's policy doc. Output a markdown table sorted by absolute
variance.
```

The agent loads a skill via the harness's native discovery (Claude Code reads `.claude/agents/`, Cursor reads `.cursor/skills/`, etc.) or via the MCP `load_artifact` call. Skills can ship scripts that the host runtime executes; declare `runtime_requirements:` so the host can refuse to materialize when the runtime isn't available. See [Bundled resources](bundled-resources) for the file layout and [Hints](hints) for the optional `effort_hint` and `model_class_hint` fields.

---

## agent

An `agent` is a complete agent definition, intended to run in isolation as a delegated child. Where a skill is a piece of instructions an agent reaches for, an agent is its own runnable unit.

```yaml
---
type: agent
name: vendor-compliance-check
version: 2.1.0
description: Verify a vendor against compliance and credit checks.
when_to_use:
  - "Before issuing a payment to a vendor not seen in the past 6 months."
tags: [finance, procurement, compliance]
sensitivity: medium
input: { $ref: ./schemas/input.json }
output: { $ref: ./schemas/output.json }
delegates_to:
  - finance/credit/credit-check@1.x
  - finance/compliance/sanctions-screen@1.x
---

You are a vendor compliance reviewer. Given a vendor record...
```

`input` and `output` declare JSON Schemas the agent expects and produces. `delegates_to` lists other agents this one can call (constrained to `agent`-type targets at lint time). The cross-type dependency graph uses these edges for impact analysis.

---

## context

A `context` artifact contains pure reference material such as style guides, glossaries, API references, and large knowledge bases.

```yaml
---
type: context
name: company-glossary
version: 1.4.0
description: Internal terminology and acronyms used at the company.
tags: [reference, glossary]
sensitivity: low
---

# Glossary

**ACV.** Annual Contract Value. The yearly subscription value...

**ARR.** Annual Recurring Revenue...
```

`context` does not need the same safety review as a `skill` because instructions are more dangerous than reference data. The lint rules are correspondingly lighter.

---

## command

A `command` is a parameterized prompt template a human invokes, typically as a slash command in the harness UI.

```yaml
---
type: command
name: refactor-module
version: 1.0.0
description: Guided module refactoring with configurable focus areas.
tags: [command, refactoring]
sensitivity: low
expose_as_mcp_prompt: true
variables:
  FOCUS: all
  PRESERVE_API: "true"
---

# Refactor Module

## User Input
$ARGUMENTS

## Instructions
Analyze the specified module and refactor with focus on: **{{FOCUS}}**.

...
```

Setting `expose_as_mcp_prompt: true` exposes the command via MCP's `prompts/get` so harnesses with slash-menu support can surface it directly to users. The wire field name keeps MCP's word for slash-menu templates, which MCP itself calls "prompts."

---

## rule

A `rule` is passive context that the harness loads automatically, controlled by a `rule_mode`:

| Mode | Loaded when… |
|:--|:--|
| `always` | The agent session starts (or every turn). |
| `glob` | A file matching the glob pattern is touched in the session. |
| `auto` | The harness's autoload heuristic decides the rule's `description` matches. |
| `explicit` | The user references it by name. |

```yaml
---
type: rule
name: payment-style
version: 1.0.0
description: Style and review checks for payment-handling code.
tags: [style, payments]
sensitivity: low
rule_mode: glob
rule_globs: "**/payment_*.py,**/billing/**"
---

When reviewing or generating payment-handling code:

- Always log the request id.
- Never store PANs in plaintext...
```

Each harness handles rule modes differently. The harness adapter does the translation (Cursor's `.mdc` frontmatter, Copilot's `.instructions.md` frontmatter, AGENTS.md injection for generic). [Rule modes](rule-modes) has the full per-harness mapping.

---

## hook

A `hook` is a lifecycle observer that wires a shell action into a harness event.

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

`hook_event` is one of the canonical event names (session lifecycle, user prompts, tool calls in generic and subtype forms, permission events, subagent lifecycle, turn end, compaction, and notifications). The harness adapter translates the canonical name to the harness's native event. `hook_action` is a shell snippet executed when the event fires; the event payload comes in on stdin. See [Hooks](hooks) for the full event taxonomy.

Hook support varies by harness, and not every harness implements every canonical event. When the configured harness adapter does not support the chosen event, lint rejects ingest unless `target_harnesses:` excludes the unsupported harness. For the events a specific harness emits, refer to the harness's own hook documentation. See [Hooks](hooks) for the full event taxonomy and authoring guidance.

---

## mcp-server

An `mcp-server` artifact registers an MCP server: name, endpoint, auth profile, description.

```yaml
---
type: mcp-server
name: finance-warehouse
version: 1.0.0
description: Read-only access to the finance data warehouse.
tags: [mcp-server, finance, warehouse]
sensitivity: medium
server_identifier: npx:@company/finance-warehouse-mcp
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-warehouse-mcp"]
---

Read-only SQL access to the finance warehouse...
```

The `server_identifier` field keys the reverse index: when a `skill` references `mcpServers:` with the same identifier, Podium tracks the dependency edge.

`mcp-server` artifacts are filtered out of MCP-bridge results because Claude Desktop, Claude Code, Cursor, and similar harnesses fix their MCP server list at startup. Surfacing them through `search_artifacts` from the bridge would only add planning noise. They remain visible through the SDK and through `podium sync` (which materializes them into the harness's on-disk config for the next launch).

---

## Extension types

Beyond the first-class types, Podium supports extension types: built-in or deployment-registered types with schemas and lint rules but no conformance commitment beyond what the type owner specifies. `mcp-server` is the only extension type Podium ships built-in. Names like `dataset`, `model`, `eval`, and `policy` are example identifiers a deployment can adopt by registering its own `TypeProvider`. The `workflow` identifier is reserved for future multi-agent flows.

Registering a type means supplying a `TypeProvider`: a JSON Schema for the frontmatter, lint rules, adapter hints, and field-merge semantics. See [Extending](../deployment/extending) for the SPI details.
