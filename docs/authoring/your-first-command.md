---
layout: default
title: Your first command
parent: Authoring
nav_order: 2
description: "Author a parameterized slash command that materializes into Claude Code's commands directory and accepts free-text arguments."
---

# Your first command

A `command` is a parameterized prompt template a human invokes, typically as a slash command in the harness UI. The harness substitutes the user's arguments into the template and sends the resulting prompt to the model; the agent never sees the template's source.

This page walks through writing one slash command end to end: from a blank directory to the command showing up in Claude Code's slash menu. The example is `/standup`: a command that reformats a free-text recap of yesterday's work into a team-standard standup post.

---

## Create the artifact

Commands do not use a `SKILL.md`. The prose body lives directly in `ARTIFACT.md`. Make the directory and write the manifest:

```bash
mkdir -p ~/podium-artifacts/personal/dev-loop/standup
$EDITOR ~/podium-artifacts/personal/dev-loop/standup/ARTIFACT.md
```

```yaml
---
type: command
name: standup
version: 1.0.0
description: Format a daily standup update from a free-text summary of yesterday's work.
when_to_use:
  - "At standup time, when summarizing yesterday's work into the team's standard format."
tags: [dev-loop, standup, daily]
sensitivity: low
expose_as_mcp_prompt: true
---

# Daily standup

## User input

$ARGUMENTS

## Instructions

Reformat the user's free-text input into the team's standup format:

**Yesterday.** One to three bullets in past tense with action verbs.

**Today.** One to three bullets in present tense with action verbs.

**Blockers.** One bullet per blocker, or "None." when there are no blockers.

Rules:

- Keep each bullet to one line.
- Drop generic filler like "worked on stuff" or "made progress".
- Surface concrete artifacts (PR numbers, ticket IDs, build URLs) when the user mentions them.
- When the user did not mention blockers, write "None."
```

A few field notes:

- **`$ARGUMENTS`** is the substitution slot the harness fills with whatever follows the slash command. Different harnesses use slightly different placeholders natively; the canonical name in Podium frontmatter is `$ARGUMENTS`, and adapters translate as needed.
- **`expose_as_mcp_prompt: true`** advertises the command via MCP's `prompts/get` endpoint so harnesses with a slash menu (Claude Code, Cursor, OpenCode, and similar) can surface it directly from the catalog. When `false`, the command still materializes for harnesses that read commands from disk, but it does not appear in MCP-driven slash menus.

---

## Materialize

```bash
cd ~/projects/your-project
podium sync
```

Claude Code's adapter writes commands to `.claude/commands/<name>.md`. Verify:

```bash
ls .claude/commands/
# standup.md
```

The full per-harness destination table is in [Configure your harness](../consuming/configure-your-harness).

---

## Use it

In Claude Code's prompt:

```
/standup yesterday I shipped the layer-composition PR, today I'm picking up
the dashboard work, blocked on review from Bob
```

Claude Code substitutes the trailing text into `$ARGUMENTS`, sends the resulting prompt to the model, and returns the formatted standup.

---

## Lint

```bash
podium lint ~/podium-artifacts/personal/dev-loop/standup/
```

Lint validates frontmatter against the `command` schema, checks the prose body for unresolved placeholders, and flags vague descriptions. CI runs the same checks on PRs.

---

## What's next

- **Add named template variables.** Beyond `$ARGUMENTS`, commands can declare `variables:` with defaults; the harness exposes them as parameters in the slash menu. See the `command` section in [Artifact types](artifact-types).
- **Move on to agents.** Commands are static prompts with a slot for user input. When the task needs to call tools, run scripts, or chain reasoning, write an `agent` instead. See [Your first agent](your-first-agent).
- **Promote the command to a domain.** Commands shared across a team typically live in a tracked domain such as `engineering/dev-loop/` with a `DOMAIN.md` describing the set. See [Domains](domains).
