---
title: Your first command
nav_order: 2
description: "Author a parameterized slash command that materializes into Claude Code's commands directory and accepts free-text arguments."
---

# Your first command

This tutorial builds a slash command from an empty directory to a working entry in Claude Code's slash menu. The example is `/standup`, a command that reformats a free-text recap of yesterday's work into a team-standard standup post.

A `command` is a parameterized prompt template that a human invokes, typically as a slash command in the harness UI. The harness substitutes the user's arguments into the template and sends the resulting prompt to the model. The agent never sees the template's source.

## Before you start

- Complete the [quickstart](../getting-started/quickstart). It installs `podium`, creates the `~/podium-artifacts/` catalog, and sets Claude Code as the default harness.
- Set `$EDITOR`, or substitute an editor in the commands below.

---

## Step 1: Create the artifact

A command has no `SKILL.md`. Its prose body lives directly in `ARTIFACT.md`.

Make the directory and open the manifest:

```bash
mkdir -p ~/podium-artifacts/personal/dev-loop/standup
$EDITOR ~/podium-artifacts/personal/dev-loop/standup/ARTIFACT.md
```

Write the frontmatter and the template body:

```markdown
---
type: command
name: standup
version: 1.0.0
description: Format a daily standup update from a free-text summary of yesterday's work.
when_to_use:
  - "At standup time, when summarizing yesterday's work into the team's standard format."
tags: [dev-loop, standup, daily]
sensitivity: low
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

Podium materializes the command body verbatim into the target harness's native command location. The harness's own argument convention therefore applies to the body. Claude Code, for example, reads `$ARGUMENTS` and `$1` from `.claude/commands/<name>.md`. Podium defines no argument syntax of its own, so write the body for the harness you target or stay within a portable form.

---

## Step 2: Materialize the command

Run `podium sync` from your project directory:

```bash
cd ~/projects/your-project
podium sync
```

Claude Code's adapter writes commands to `.claude/commands/<name>.md`. Confirm that the file exists:

```bash
ls .claude/commands/
# standup.md
```

Podium does not project commands through MCP. Both `podium sync` and the MCP server materialize the command to disk in the harness-native format, and the harness's slash-command system surfaces it. [Configure your harness](../consuming/configure-your-harness) lists the destination for every harness.

---

## Step 3: Run the command

Type the command and its arguments in Claude Code's prompt:

```
/standup yesterday I shipped the layer-composition PR, today I'm picking up
the dashboard work, blocked on review from Bob
```

Claude Code substitutes the trailing text into `$ARGUMENTS`, sends the resulting prompt to the model, and returns the formatted standup.

---

## Step 4: Lint the command

```bash
podium lint --registry ~/podium-artifacts/
```

Lint validates the frontmatter against the `command` schema. It also resolves prose references in the body, which are markdown links to bundled files or to other artifacts. A clean run prints `lint: no issues.` When the catalog lives in a Git-source layer, CI runs the same checks on pull requests (see [Connecting layer sources](../deployment/content/layer-sources#git-repositories)).

---

## Next steps

- **Move on to agents.** A command is a static prompt with a slot for user input. When the task calls tools, runs scripts, or chains reasoning, write an `agent` instead. See [Your first agent](your-first-agent).
- **Promote the command to a domain.** Commands shared across a team typically live in a tracked domain such as `engineering/dev-loop/`, with a `DOMAIN.md` that describes the set. See [Domains](domains).
