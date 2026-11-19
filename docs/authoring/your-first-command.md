---
title: Your first command
nav_order: 2
description: "Author a user-invoked skill that a human starts as a slash command, materialize it into Claude Code's skills directory with the user-only control, and pass it free-text arguments."
---

# Your first command

This tutorial builds a slash command from an empty directory to a working entry in Claude Code's slash menu. The example is `/standup`, a command that reformats a free-text recap of yesterday's work into a team-standard standup post.

In Podium, a command is a skill whose `invocation` is `user`. A human starts it, typically as a slash command in the harness UI, and the agent does not load it on its own. The harness substitutes the user's arguments into the skill body and sends the resulting prompt to the model.

## Before you start

- Complete the [quickstart](../getting-started/quickstart). It installs `podium`, creates the `~/podium-artifacts/` catalog, and sets Claude Code as the default harness.
- Set `$EDITOR`, or substitute an editor in the commands below.

---

## Step 1: Create the artifact

A user-invoked skill is a skill package. Its prose body lives in `SKILL.md`, and `SKILL.md` `metadata.invocation` marks it as user-invoked.

Scaffold the package with `--invocation user`:

```bash
podium artifact scaffold --type skill \
  --description "Format a daily standup update from a free-text summary of yesterday's work." \
  --tags dev-loop,standup,daily \
  --sensitivity low \
  --invocation user \
  --version 1.0.0 \
  ~/podium-artifacts/personal/dev-loop/standup
```

The scaffolder writes `SKILL.md` and no `ARTIFACT.md`. Open the file:

```bash
$EDITOR ~/podium-artifacts/personal/dev-loop/standup/SKILL.md
```

Replace the placeholder body with the template:

```markdown
---
name: standup
description: "Format a daily standup update from a free-text summary of yesterday's work."
metadata:
  version: "1.0.0"
  tags: "dev-loop, standup, daily"
  sensitivity: "low"
  invocation: "user"
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

Only the value `user` makes a skill user-invoked. An absent `invocation` and every other value resolve to `any`, which leaves the skill model-invocable. [Frontmatter reference](frontmatter-reference#podium-fields-in-skillmd-metadata) states the encoding.

Podium writes the skill body unchanged and adds no argument layer of its own. The harness's own argument convention therefore applies to the body. Claude Code, for example, expands `$ARGUMENTS` in a skill body. A placeholder expands only on the harnesses whose convention it follows, so write the body for the harness you target or stay within a portable form.

---

## Step 2: Materialize the skill

Run `podium sync` from your project directory:

```bash
cd ~/projects/your-project
podium sync
```

Claude Code's adapter writes skills to `.claude/skills/<name>/SKILL.md`. Confirm that the file exists:

```bash
ls .claude/skills/standup/
# SKILL.md
```

The materialized `SKILL.md` carries the Claude Code user-only control `disable-model-invocation: true` as a top-level key, so Claude Code lists the skill in the slash menu and does not invoke it by itself:

```markdown
---
disable-model-invocation: true
name: standup
description: "Format a daily standup update from a free-text summary of yesterday's work."
metadata:
  version: "1.0.0"
  tags: "dev-loop, standup, daily"
  sensitivity: "low"
  invocation: "user"
---
```

Each harness receives its own control. Cursor and Pi receive the same key, Codex receives an `agents/openai.yaml` beside the skill, and OpenCode and Gemini receive their native command file instead of a skill. [Configure your harness](../consuming/configure-your-harness) lists the destination for every harness.

Podium does not project a user-invoked skill through MCP as a prompt. The user-only control governs only whether the harness invokes the skill by itself, so the meta-tools still list the skill in `search_artifacts` and return its body from `load_artifact`.

---

## Step 3: Run the command

Type the command and its arguments in Claude Code's prompt:

```
/standup yesterday I shipped the layer-composition PR, today I'm picking up
the dashboard work, blocked on review from Bob
```

Claude Code substitutes the trailing text into `$ARGUMENTS`, sends the resulting prompt to the model, and returns the formatted standup.

---

## Step 4: Lint the skill

```bash
podium lint --registry ~/podium-artifacts/
```

Lint validates `SKILL.md` against the skill rules. It warns on an `invocation` value other than `any` or `user`, and it resolves prose references in the body, which are markdown links to bundled files or to other artifacts. A clean run prints `lint: no issues.` When the catalog lives in a Git-source layer, CI runs the same checks on pull requests (see [Connecting layer sources](../deployment/content/layer-sources#git-repositories)).

---

## Next steps

- **Move on to agents.** A user-invoked skill is a static prompt with a slot for user input. When the task calls tools, runs scripts, or chains reasoning, write an `agent` instead. See [Your first agent](your-first-agent).
- **Promote the skill to a domain.** User-invoked skills shared across a team typically live in a tracked domain such as `engineering/dev-loop/`, with a `DOMAIN.md` that describes the set. See [Domains](domains).
