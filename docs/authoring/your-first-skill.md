---
title: Your first skill
nav_order: 1
description: "Extend the quickstart's greet skill with a sharper description, an ARTIFACT.md overlay, a referenced script, a runtime requirement, and a lint check before commit."
---

# Your first skill

This tutorial continues the [quickstart](../getting-started/quickstart) and extends its `greet` skill into a complete artifact. It sharpens the description, adds an `ARTIFACT.md` overlay for Podium-only fields, points the body at the bundled script, declares a runtime requirement, and lints the result. For other artifact types, see [Your first command](your-first-command) and [Your first agent](your-first-agent).

## Before you start

- Complete the [quickstart](../getting-started/quickstart). It installs `podium`, writes the `greet` skill, and sets Claude Code as the default harness with `podium init`.
- Set `$EDITOR`, or substitute an editor in the commands below.

The quickstart leaves the artifact directory like this:

```
~/podium-artifacts/personal/hello/greet/
├── SKILL.md
└── scripts/
    └── today.py
```

The artifact is a skill named `greet`. `SKILL.md` is the [agentskills.io](https://agentskills.io/specification) standard manifest and holds the agent-facing prose. `scripts/today.py` is the bundled script the quickstart added. A directory that holds `SKILL.md` is a complete skill, so the artifact has no `ARTIFACT.md` yet.

---

## Step 1: Sharpen the description

The quickstart's `SKILL.md` already declares `name`, `description`, `license`, and `metadata.version`. This step rewrites the `description` so that it names the situations in which the harness reaches for the skill.

Open `SKILL.md`:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/SKILL.md
```

Replace the frontmatter with this block. Only the `description` line differs from the quickstart:

```markdown
---
name: greet
description: Greet the user by name and tell them today's date in a friendly format. Use when the user opens a session with a greeting or asks who you are.
license: MIT
metadata:
  version: "1.0.0"
---
```

The `description` decides whether the harness reaches for the skill. A vague description such as "Helper skill" gets ignored. A specific one that states what the skill does and when to use it matches the prompts users write. The registry flags thin descriptions at ingest, and local `podium lint` does not run that check.

The version stays `1.0.0`, because a filesystem catalog keeps no ingest history. Once a registry server ingests the skill, `(artifact_id, version)` is immutable, so bump `metadata.version` whenever the content changes after that. [Frontmatter reference](frontmatter-reference) lists every field.

---

## Step 2: Add Podium-only fields in an ARTIFACT.md overlay

`SKILL.md` stays within the agentskills.io specification, so Podium-only fields such as `when_to_use`, `tags`, and `sensitivity` never go in it. They go in an optional `ARTIFACT.md` beside it, which you write by hand.

Create the overlay:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/ARTIFACT.md
```

Add the indexing and governance fields:

```markdown
---
type: skill
when_to_use:
  - "When the user opens a session with a greeting like 'hi' or 'hello'."
  - "When the user asks who you are at session start."
tags: [demo, hello-world, greeting]
sensitivity: low
---

<!-- Skill body lives in SKILL.md. -->
```

The overlay has these properties:

- `type: skill` marks the package as a skill. The body holds only the one-line HTML comment pointer, because the agent-facing prose lives in `SKILL.md`.
- Leave `version` out of the overlay, so the `metadata.version` in `SKILL.md` stays the skill's version. If you add a `version` to the overlay, give it the same value as `metadata.version`, or lint reports an error. [Skill versions](frontmatter-reference#skill-versions) gives the precedence rules.
- `when_to_use` lists concrete situations in which the skill applies. A registry with an embedding provider includes it in the embedding it computes at ingest. [`when_to_use` and search](frontmatter-reference#when_to_use-and-search) describes where the field counts.

---

## Step 3: Reference the bundled script

Every file in the artifact's directory other than the manifest files is a bundled resource, so `scripts/today.py` already ships with the skill. [Bundled resources](bundled-resources) covers the layout conventions and the size caps.

The quickstart's body asks for a friendly date format without mentioning the script. Replace the body of `SKILL.md` with these lines, so the agent knows to run it:

```markdown
Greet the user by their first name (ask if you don't know it).
Tell them today's date by running [scripts/today.py](scripts/today.py).
Keep it to one or two sentences.
```

Lint resolves markdown links in the body against the artifact's bundled files at ingest, so a link to a path the package does not contain fails the check. A path written as inline code is not checked. A skill's bundled files materialize inside the skill folder, so the link also resolves from the materialized `SKILL.md`.

---

## Step 4: Declare runtime requirements

The script needs Python. Declaring the requirement lets a consumer refuse the artifact up front instead of failing at execution time.

`runtime_requirements` is a Podium-only field, so add it to the `ARTIFACT.md` overlay:

```yaml
runtime_requirements:
  python: ">=3.10"
```

The overlay now reads as follows:

```markdown
---
type: skill
when_to_use:
  - "When the user opens a session with a greeting like 'hi' or 'hello'."
  - "When the user asks who you are at session start."
tags: [demo, hello-world, greeting]
sensitivity: low
runtime_requirements:
  python: ">=3.10"
---

<!-- Skill body lives in SKILL.md. -->
```

The directory holds `ARTIFACT.md` as the overlay beside `SKILL.md`:

```
~/podium-artifacts/personal/hello/greet/
├── SKILL.md
├── ARTIFACT.md
└── scripts/
    └── today.py
```

`podium sync` materializes the skill without checking the requirement. The Podium MCP server checks it when the host advertises its runtimes or opts into enforcement. [Execution model](bundled-resources#execution-model) describes each case.

---

## Step 5: Iterate with watch mode

Run the watcher from your project directory, as the quickstart's [Watch mode](../getting-started/quickstart#watch-mode-optional) section describes:

```bash
cd ~/projects/your-project
podium sync --watch
```

Edit the `SKILL.md` body in another window, and start a new Claude Code session to see the change. Press `Ctrl-C` to stop the watcher.

---

## Step 6: Lint before you commit

Run lint before committing or pushing:

```bash
podium lint --registry ~/podium-artifacts/
```

A clean run prints `lint: no issues.` and exits 0.

Lint runs these checks:

- The frontmatter in `SKILL.md`, and in `ARTIFACT.md` when present, against the type's schema.
- Prose references in `SKILL.md`, which must resolve to bundled files.
- The agentskills.io compliance checks on `SKILL.md`: name format, description constraints, and parent-directory match.
- The type-specific rules.

When the catalog lives in a Git-source layer, CI runs the same checks on pull requests, and the registry adds the thin-description check at ingest (see [Connecting layer sources](../deployment/content/layer-sources#git-repositories)).

Each diagnostic prints as `[<severity>] <artifact-id>: <message> (<rule-code>)`, for example `[error] personal/hello/greet: type is required (lint.required_field_missing)`. Warnings alone exit 0, and any error exits 1. [Skill lint checks](frontmatter-reference#skill-lint-checks) lists the checks that apply to a skill.

When lint passes, commit the artifact.

---

## Next steps

- **Write a slash command.** A `command` is a parameterized prompt template the user invokes by name. See [Your first command](your-first-command).
- **Write an agent.** An `agent` is a complete agent definition with its own instructions, dependencies, and optional bundled scripts. See [Your first agent](your-first-agent).
- **Learn the other built-in types.** [Artifact types](artifact-types) covers `rule`, `hook`, `context`, and `mcp-server` alongside skills, commands, and agents.
- **Organize multiple artifacts.** Group artifacts with `DOMAIN.md` files that add descriptions, keywords, and featured artifacts. See [Domains](domains).
- **Share the catalog with a team.** [Progressive adoption](../deployment/progressive-adoption) covers moving from a local catalog to a shared registry, and [Connecting layer sources](../deployment/content/layer-sources) covers publishing from a Git repository.
