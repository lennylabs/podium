---
title: Artifact types
nav_order: 4
description: The built-in artifact types (skill, agent, rule, hook, and mcp-server), what each is for, how each reaches a harness, and what replaced the removed command and context types.
---

# Artifact types

This page describes the built-in artifact types and shows a minimal manifest for each. Read it to choose a type for a new artifact.

Every artifact declares a `type:` in its frontmatter. The type determines how the registry indexes the artifact, which lint rules apply, and how the harness adapter translates it at delivery time.

| Type | Purpose | Manifest |
|:--|:--|:--|
| [`skill`](#skill) | Instructions the agent loads into its context on demand. | `SKILL.md`, plus an optional `ARTIFACT.md` |
| [`agent`](#agent) | A complete agent definition that runs as a delegated child. | `ARTIFACT.md` |
| [`rule`](#rule) | Passive context the harness loads according to a `rule_mode`. | `ARTIFACT.md` |
| [`hook`](#hook) | A lifecycle observer with a shell action. | `ARTIFACT.md` |
| [`mcp-server`](#mcp-server) | An MCP server registration. | `ARTIFACT.md` |

The types from `skill` through `hook` are first-class types, which Podium lints fully, tests in its conformance suite, and supports across the built-in adapters. `mcp-server` is a built-in [extension type](#extension-types), and a deployment registers further extension types through the `TypeProvider` SPI.

[Frontmatter reference](frontmatter-reference) lists every field each type accepts. `command` and `context` are no longer artifact types, and [Removed types](#removed-types) states what replaced them.

---

## skill

A `skill` is a set of instructions, optionally with bundled scripts, that the agent loads into its context when it decides the skill matches the situation.

A `SKILL.md` alone is a complete skill:

```markdown
---
name: run-variance-analysis
description: Flag unusual variance vs. forecast after month-end close. Use when reviewing financial performance for the most recent close period.
---

Compare actuals vs. forecast for the most recent close period.
For each line item, flag variances above the threshold defined in
your team's policy doc. Output a markdown table sorted by absolute
variance.
```

`SKILL.md` `metadata` can carry the Podium-only fields that [Podium fields in SKILL.md metadata](frontmatter-reference#podium-fields-in-skillmd-metadata) lists, as strings. An optional `ARTIFACT.md` overlay beside `SKILL.md` can carry every Podium-only field, and it is the only place for the fields that `metadata` does not carry, such as `runtime_requirements`. The author writes it by hand:

```markdown
---
type: skill
version: 1.0.0
when_to_use:
  - "After month-end close, when reviewing financial performance."
tags: [finance, close, variance]
sensitivity: low
runtime_requirements:
  python: ">=3.10"
---

<!-- Skill body lives in SKILL.md. -->
```

The agent loads a skill in one of two ways:

- Through the harness's native discovery. Claude Code reads `.claude/skills/`, Cursor reads `.cursor/skills/`, and other harnesses read their own directories.
- Through the MCP `load_artifact` call.

A skill can ship scripts that the host runtime executes. Declare `runtime_requirements:` so the host can refuse the skill when the runtime is unavailable. [Bundled resources](bundled-resources) covers the file layout, and [Hints](hints) covers the optional `effort_hint` and `model_class_hint` fields.

### User-invoked skills

A skill whose `invocation` is `user` is meant for a human to invoke, typically as a slash command. Set it as `SKILL.md` `metadata.invocation: "user"` or as the top-level `invocation: user` in an `ARTIFACT.md` overlay. `invocation` accepts `any` and `user`. `any` is the default, and it also applies to an absent value and to every value other than `user`, so only the exact value `user` makes a skill user-invoked.

```markdown
---
name: refactor-module
description: Guided module refactoring with configurable focus areas.
metadata:
  version: "1.0.0"
  invocation: "user"
---

# Refactor Module

## User input
$ARGUMENTS

## Instructions
Analyze the specified module and refactor according to the user's input.
```

Both `podium sync` and the MCP server materialize a user-invoked skill with the harness's user-only control, or as the harness's native command file on a harness whose skills have no such control:

| Harness | Output |
|:--|:--|
| Claude Code | `.claude/skills/<n>/SKILL.md` with `disable-model-invocation: true` |
| Cursor | `.cursor/skills/<n>/SKILL.md` with `disable-model-invocation: true` |
| Codex | `.agents/skills/<n>/SKILL.md` and `.agents/skills/<n>/agents/openai.yaml` with `policy.allow_implicit_invocation: false` |
| OpenCode | The command file `.opencode/commands/<n>.md` |
| Gemini | The command file `.gemini/commands/<n>.toml` |
| Pi | `.pi/skills/<n>/SKILL.md` with `disable-model-invocation: true` |
| Claude Desktop, Claude Cowork, and Hermes | Nothing. Materialization fails with `materialize.untranslatable`, because these harnesses have no project-scope skill surface. |

Podium writes the body unchanged and defines no argument syntax of its own, so the harness's own argument convention applies and a placeholder expands only on the harnesses whose convention it follows. Podium does not project a user-invoked skill through MCP as a prompt. The user-only control governs only whether the harness invokes the skill by itself, so `search_artifacts` lists the skill and `load_artifact` returns its body as for any other artifact. [Your first command](your-first-command) builds a user-invoked skill step by step.

### Reference material

Reference material, such as a style guide, a glossary, an API reference, or a knowledge base, travels with a skill. Place it in one of these locations:

- The `references/` directory of the skill that uses it. The agent reads the files on demand, and [Bundled resources](bundled-resources) covers the layout.
- A model-invocable skill whose description says that it carries reference material, when no single skill owns the material. The agent loads it like any other skill.
- A `DOMAIN.md` body, when the material describes a whole domain. [Domains](domains) covers the file.

---

## agent

An `agent` is a complete agent definition that runs in isolation as a delegated child. A skill is a piece of instructions an agent reaches for. An agent is its own runnable unit.

```markdown
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

The agent-specific fields:

- `input` and `output` declare the JSON Schemas the agent expects and produces.
- `delegates_to` lists the canonical IDs of the artifacts this agent can hand work to, usually other agents. The field is advisory: ingest accepts a target of any type and does not check that it resolves. The cross-type dependency graph uses these edges for impact analysis.

[Your first agent](your-first-agent) builds an agent step by step.

---

## rule

A `rule` is passive context that the harness loads automatically. The `rule_mode` field controls when: `always` (the default) loads it in every session, `glob` loads it when a matching file is involved, `auto` lets the harness decide from `rule_description`, and `explicit` loads it only when the user names it.

```markdown
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

The harness adapter translates the mode into the harness's native rule format, and some harnesses fall back to an always-loaded rule for the scoped modes. [Rule modes](rule-modes) describes each mode and what every harness writes for it.

---

## hook

A `hook` is a lifecycle observer that wires a shell action into a harness event.

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
  INPUT=$(cat)
  echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] session end: $INPUT" \
    >> ~/.podium/session-audit.log
---
```

The hook-specific fields:

- `hook_event` is one of the canonical event names, which the harness adapter translates to the harness's native event.
- `hook_action` is a shell snippet that runs when the event fires. The event payload arrives on stdin.

[Hooks](hooks) lists the canonical events, describes how coverage varies by harness, and gives authoring guidance.

---

## mcp-server

An `mcp-server` artifact registers an MCP server: its name, endpoint, auth profile, and description.

```markdown
---
type: mcp-server
name: finance-warehouse
version: 1.0.0
description: Read-only access to the finance data warehouse.
tags: [mcp-server, finance, warehouse]
sensitivity: medium
server_identifier: npx:@company/finance-warehouse-mcp
---

Read-only SQL access to the finance warehouse...
```

The `server_identifier` field is the registration's canonical identity. Each adapter that has an MCP surface derives the harness's native server entry from it, keyed by the artifact's `name`:

| Harness | Output |
|:--|:--|
| Claude Code | Merges `{"command": "npx", "args": ["@company/finance-warehouse-mcp"]}` under `mcpServers.finance-warehouse` into `.mcp.json`. |
| Cursor | Writes the same entry into `.cursor/mcp.json`. |
| Gemini | Writes the same entry into `.gemini/settings.json`. |
| OpenCode | Writes an equivalent entry under the `mcp` key in `opencode.json`. |
| Codex | Writes an `[mcp_servers.<name>]` table into `.codex/config.toml`. |
| Claude Desktop, Claude Cowork, Pi, and Hermes | Nothing. These harnesses have no project-level MCP surface. |

A declared `name` is 1 to 64 ASCII letters, digits, `_`, or `-`. Ingest refuses another value with `ingest.invalid_artifact` and `details.constraint: "mcp_server_name"`, because the name is the key of the server's entry in each harness's configuration file. An artifact that declares no `name` is keyed by the last segment of its ID, and the Codex adapter writes such a key as a quoted TOML key when it holds another character, such as `[mcp_servers."finance.warehouse"]`.

Materialization does not read an `mcpServers:` list in the registration's own frontmatter.

`server_identifier` also keys the reverse index. When any artifact declares an `mcpServers:` entry that resolves to the same identifier, Podium records the dependency edge.

### Discovery through the MCP bridge

The MCP bridge keeps `mcp-server` artifacts out of its results. Claude Desktop, Claude Code, Cursor, and similar harnesses fix their MCP server list at startup, so a server discovered mid-session cannot be connected. The bridge's `search_artifacts`, the `load_domain` notable list, and the `resources/list` mirror leave them out, and `load_artifact` refuses one with `registry.not_found`. The SDK and `podium sync` return them, and `podium sync` materializes them into the harness's on-disk config for the next launch.

---

## Extension types

Extension types are built-in or deployment-registered types. An extension type comes with a schema and lint rules. Podium's conformance suite does not test it, and any further guarantee about its behavior comes from the type's owner.

- `mcp-server` is the only extension type Podium ships built in.
- `dataset`, `model`, `eval`, and `policy` are example identifiers a deployment can adopt by registering its own `TypeProvider`. Podium ships no implementation for them.
- The `workflow` identifier is reserved for future multi-agent flows.

To register a type, supply a `TypeProvider`. The implementation declares its `ID()` and its `Type()`, and it implements `Validate()`. The linter surfaces the `Validate()` diagnostics under the `lint.type_provider` rule. [Extending](../deployment/services/extending) covers the SPI details.

---

## Removed types

`command` and `context` are not artifact types. A command is a skill whose `invocation` is `user` ([User-invoked skills](#user-invoked-skills)), and reference material travels with a skill or a domain ([Reference material](#reference-material)).

Ingest refuses an `ARTIFACT.md` whose `type` is `command` or `context` with `ingest.invalid_artifact` and `details.constraint: "type_removed"`, before lint. `podium lint` reports it as an error, and a filesystem-source `podium sync` drops it. A stored row of either type is retired: no read path serves it, and `latest` falls back to the newest other served version of its ID. `podium artifact scaffold` refuses `--type command` and `--type context`.

Podium ships no conversion command. [Upgrade notes](../deployment/operations/upgrade-notes#command-and-context-removal) states the procedure that converts a package by hand. The procedure publishes each converted skill at the next minor version, because the retired row keeps its version. It also keeps an `ARTIFACT.md` holding `type: skill` in a converted package that sits below another package, because a directory there that holds a `SKILL.md` and no `ARTIFACT.md` is a bundled resource of the enclosing package.
