---
title: Your first skill
nav_order: 1
description: "From the quickstart's greet skill to a richer artifact with fuller frontmatter, an ARTIFACT.md overlay, a referenced script, runtime requirements, and a lint check before commit."
---

# Your first skill

This page picks up from the [quickstart](../getting-started/quickstart) and rounds out the same `greet` skill with fuller frontmatter, an `ARTIFACT.md` overlay for Podium-only fields, a body reference to its bundled script, watch-mode iteration, and a lint check. For non-skill walkthroughs, see [Your first command](your-first-command) and [Your first agent](your-first-agent).

---

## Starting point

The quickstart leaves the artifact directory like this:

```
~/podium-artifacts/personal/hello/greet/
├── SKILL.md
└── scripts/
    └── today.py
```

The artifact is a skill named `greet`. `SKILL.md` is the [agentskills.io](https://agentskills.io/specification) standard manifest with the agent-facing prose, and `scripts/today.py` is the bundled script the quickstart added. A directory that holds `SKILL.md` is a complete skill, so the artifact has no `ARTIFACT.md` yet.

---

## Add fuller frontmatter

The minimum frontmatter that the registry ingests is `name` and `description` in `SKILL.md`. A `metadata.version` in `major.minor.patch` form is recommended: a skill that declares no version receives one that the registry assigns, and `podium lint` warns about it. The fields below pay off as the catalog grows.

Open `SKILL.md`:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/SKILL.md
```

The standard's frontmatter holds the discoverability fields and the version:

```markdown
---
name: greet
description: Greet the user by name and tell them today's date in a friendly format. Use when the user opens a session with a greeting or asks who you are.
license: MIT
metadata:
  version: "1.0.0"
---
```

Notes on these fields:

- **`description`** decides whether the harness reaches for this skill. A vague description like "Helper skill" gets ignored; a specific one like "Greet the user by name and tell them today's date" matches against actual user prompts. The registry flags thin descriptions at ingest time; local `podium lint` does not run that check.
- **`license`** names the license the skill is distributed under.
- **`metadata.version`** is the skill's semver version. Bump it when the content changes, because an ingested version is immutable.

The full frontmatter reference is in [Frontmatter reference](frontmatter-reference).

---

## Add Podium-only fields with an ARTIFACT.md overlay

Podium-only fields such as `when_to_use`, `tags`, and `sensitivity` never go in `SKILL.md`, which stays within the agentskills.io specification. They go in an optional `ARTIFACT.md` beside it, written by hand:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/ARTIFACT.md
```

The overlay holds the indexing and governance fields:

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

Notes on the overlay:

- **`type: skill`** marks the package as a skill. The body is empty apart from the one-line HTML comment pointer, because the agent-facing prose lives in `SKILL.md`.
- **`version`** is absent, so the skill's version stays the `metadata.version` in `SKILL.md`. When the overlay declares a `version`, that value is the skill's version, and it must equal `metadata.version` whenever `metadata.version` is a `major.minor.patch` value.
- **`when_to_use`** is a list of explicit situations. Hybrid retrieval uses these as additional signal. Be concrete: "After month-end close" beats "When working on finance stuff."

---

## Reference the bundled script

The quickstart placed `scripts/today.py` beside `SKILL.md`. Anything in the artifact's directory other than the manifest files (`SKILL.md` and the optional `ARTIFACT.md`) is a bundled resource: Python scripts, Jinja templates, JSON schemas, eval datasets, and files as large as model weights. The agentskills.io spec recommends `scripts/`, `references/`, and `assets/` as conventional subfolders. The per-package soft cap is 10 MB; larger files use external resources, see [Bundled resources](bundled-resources).

Reference the script from the `SKILL.md` body:

```markdown
Greet the user by their first name (ask if you don't know it).
Tell them today's date by running [scripts/today.py](scripts/today.py).
Keep it to one or two sentences.
```

Lint resolves markdown links in the body against the artifact's bundled files at ingest, so a link to a path the package does not contain fails the check. A path written as inline code is not checked.

---

## Declare runtime requirements

The script needs Python. Declare the requirement so a host that advertises its runtime capabilities to the Podium MCP server refuses a `load_artifact` it cannot satisfy with `materialize.runtime_unavailable` instead of failing at execution time. A host that advertises no capabilities and does not opt into enforcement receives the requirement and proceeds, and `podium sync` materializes the artifact without checking it. `runtime_requirements` is a Podium-only field, so add it to the `ARTIFACT.md` overlay:

```yaml
runtime_requirements:
  python: ">=3.10"
```

Now the artifact's `ARTIFACT.md` overlay looks like this:

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

And the directory, with `ARTIFACT.md` as the overlay beside `SKILL.md`:

```
~/podium-artifacts/personal/hello/greet/
├── SKILL.md
├── ARTIFACT.md
└── scripts/
    └── today.py
```

---

## Iterate with watch mode

Watch mode avoids manual `podium sync` runs after each edit:

```bash
cd ~/projects/your-project
podium sync --watch
```

The watcher re-materializes on every save. Open Claude Code in another window; tweaks to the `SKILL.md` prose body show up on the next session.

`Ctrl-C` to stop.

---

## Lint before you commit

Before committing or pushing, run lint:

```bash
podium lint --registry ~/podium-artifacts/
```

Lint checks the frontmatter against the type's schema in `SKILL.md` and, when present, `ARTIFACT.md`, validates that prose references in `SKILL.md` resolve to bundled files, runs the agentskills.io compliance checks on `SKILL.md` (name format, description constraints, parent-directory match), and runs type-specific rules. The thin-description check runs at registry ingest rather than in local `podium lint`. CI runs the same checks on PRs to a Git-source layer.

If lint passes, commit the artifact. Each diagnostic prints as `[<severity>] <artifact-id>: <message> (<rule-code>)`, for example `[error] personal/hello/greet: type is required (lint.required_field_missing)`. A clean run prints `lint: no issues.`. Warnings alone exit 0, and any error exits 1. A skill that declares no version draws the `lint.skill_version_assigned` warning, because the registry assigns its version; set `metadata.version` in `SKILL.md` to clear it.

---

## What's next

- **Write a slash command.** A `command` is a parameterized prompt template the user invokes by name. See [Your first command](your-first-command).
- **Write an agent.** An `agent` is a complete agent definition with its own instructions, dependencies, and optional bundled scripts. See [Your first agent](your-first-agent).
- **Cover the other built-in types.** [Artifact types](artifact-types) covers `rule`, `hook`, `context`, and `mcp-server` alongside skills, commands, and agents.
- **Organize multiple artifacts.** As they accumulate, group them with `DOMAIN.md` files: descriptions, keywords, featured artifacts. See [Domains](domains).
- **Move from solo to team-shared.** [Deployment](../deployment/) walks the migration paths.
