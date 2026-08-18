---
title: Your first skill
nav_order: 1
description: "From the quickstart's greet skill to a richer artifact with fuller frontmatter, a referenced script, runtime requirements, and a lint check before commit."
---

# Your first skill

This page picks up from the [quickstart](../getting-started/quickstart) and rounds out the same `greet` skill with fuller frontmatter, a body reference to its bundled script, watch-mode iteration, and a lint check. For non-skill walkthroughs, see [Your first command](your-first-command) and [Your first agent](your-first-agent).

---

## Starting point

The quickstart leaves the artifact directory like this:

```
~/podium-artifacts/personal/hello/greet/
├── SKILL.md
├── ARTIFACT.md
└── scripts/
    └── today.py
```

The artifact is a skill named `greet`. `SKILL.md` is the [agentskills.io](https://agentskills.io/specification) standard manifest with the agent-facing prose, `ARTIFACT.md` is Podium's structured frontmatter, and `scripts/today.py` is the bundled script the quickstart added.

---

## Add fuller frontmatter

The minimum frontmatter is enough for the registry to ingest: `name` and `description` in `SKILL.md`, plus `type` and `version` in `ARTIFACT.md`. The fields below pay off as the catalog grows.

Open `SKILL.md`:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/SKILL.md
```

The standard's frontmatter holds the discoverability fields:

```markdown
---
name: greet
description: Greet the user by name and tell them today's date in a friendly format. Use when the user opens a session with a greeting or asks who you are.
license: MIT
---
```

Then open `ARTIFACT.md`:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/ARTIFACT.md
```

Podium's structured frontmatter holds the indexing and governance fields:

```markdown
---
type: skill
version: 1.0.0
when_to_use:
  - "When the user opens a session with a greeting like 'hi' or 'hello'."
  - "When the user asks who you are at session start."
tags: [demo, hello-world, greeting]
sensitivity: low
---

<!-- Skill body lives in SKILL.md. -->
```

Notes on these fields:

- **`description`** (in `SKILL.md`) decides whether the harness reaches for this skill. A vague description like "Helper skill" gets ignored; a specific one like "Greet the user by name and tell them today's date" matches against actual user prompts. The registry flags thin descriptions at ingest time; local `podium lint` does not run that check.
- **`when_to_use`** (in `ARTIFACT.md`) is a list of explicit situations. Hybrid retrieval uses these as additional signal. Be concrete: "After month-end close" beats "When working on finance stuff."

The full frontmatter reference is in [Frontmatter reference](frontmatter-reference).

---

## Reference the bundled script

The quickstart placed `scripts/today.py` beside the two manifests. Anything in the artifact's directory other than the two manifest files is a bundled resource: Python scripts, Jinja templates, JSON schemas, eval datasets, and files as large as model weights. The agentskills.io spec recommends `scripts/`, `references/`, and `assets/` as conventional subfolders. The per-package soft cap is 10 MB; larger files use external resources, see [Bundled resources](bundled-resources).

Reference the script from the `SKILL.md` body:

```markdown
Greet the user by their first name (ask if you don't know it).
Tell them today's date by running [scripts/today.py](scripts/today.py).
Keep it to one or two sentences.
```

Lint resolves markdown links in the body against the artifact's bundled files at ingest, so a link to a path the package does not contain fails the check. A path written as inline code is not checked.

---

## Declare runtime requirements

The script needs Python. Declare the requirement so a host that advertises its runtime capabilities to the Podium MCP server refuses a `load_artifact` it cannot satisfy with `materialize.runtime_unavailable` instead of failing at execution time. A host that advertises no capabilities receives the requirement and proceeds, and `podium sync` materializes the artifact without checking it. Add this to `ARTIFACT.md`:

```yaml
runtime_requirements:
  python: ">=3.10"
```

Now the artifact's `ARTIFACT.md` looks like this:

```markdown
---
type: skill
version: 1.0.0
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

And the directory:

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

Lint checks the frontmatter against the type's schema in both files, validates that prose references in `SKILL.md` resolve to bundled files, runs the agentskills.io compliance checks on `SKILL.md` (name format, description constraints, parent-directory match), and runs type-specific rules. The thin-description check runs at registry ingest rather than in local `podium lint`. CI runs the same checks on PRs to a Git-source layer.

If lint passes, commit the artifact. Each diagnostic prints as `[<severity>] <artifact-id>: <message> (<rule-code>)`, for example `[error] personal/hello/greet: type is required (lint.required_field_missing)`. A clean run prints `lint: no issues.`. Warnings alone exit 0, and any error exits 1.

---

## What's next

- **Write a slash command.** A `command` is a parameterized prompt template the user invokes by name. See [Your first command](your-first-command).
- **Write an agent.** An `agent` is a complete agent definition with its own instructions, dependencies, and optional bundled scripts. See [Your first agent](your-first-agent).
- **Cover the other built-in types.** [Artifact types](artifact-types) covers `rule`, `hook`, `context`, and `mcp-server` alongside skills, commands, and agents.
- **Organize multiple artifacts.** As they accumulate, group them with `DOMAIN.md` files: descriptions, keywords, featured artifacts. See [Domains](domains).
- **Inherit from another artifact.** When two artifacts share most of their structure, `extends:` lets the second refine the first instead of duplicating it. See [Extends](extends).
- **Move from solo to team-shared.** [Deployment](../deployment/) walks the migration paths.
