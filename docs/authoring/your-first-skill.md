---
title: Your first skill
nav_order: 1
description: "Extend the quickstart's greet skill with fuller frontmatter, an ARTIFACT.md overlay, a referenced script, runtime requirements, and a lint check before commit."
---

# Your first skill

This tutorial continues the [quickstart](../getting-started/quickstart) and extends its `greet` skill into a complete artifact. It adds fuller frontmatter, an `ARTIFACT.md` overlay for Podium-only fields, a body reference to the bundled script, a runtime requirement, watch-mode iteration, and a lint check. For other artifact types, see [Your first command](your-first-command) and [Your first agent](your-first-agent).

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

## Step 1: Add fuller frontmatter

The registry ingests a skill whose `SKILL.md` frontmatter has only `name` and `description`. Add a `metadata.version` in `major.minor.patch` form as well. A skill that declares no version receives a registry-assigned one, and `podium lint` warns about it.

Open `SKILL.md`:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/SKILL.md
```

Replace the frontmatter with the standard's discoverability fields and a version:

```markdown
---
name: greet
description: Greet the user by name and tell them today's date in a friendly format. Use when the user opens a session with a greeting or asks who you are.
license: MIT
metadata:
  version: "1.0.0"
---
```

The fields in this block have these roles:

| Field | Role |
|:--|:--|
| `description` | Decides whether the harness reaches for the skill. A vague description such as "Helper skill" gets ignored. A specific one such as "Greet the user by name and tell them today's date" matches actual user prompts. The registry flags thin descriptions at ingest time, and local `podium lint` does not run that check. |
| `license` | Names the license the skill is distributed under. |
| `metadata.version` | The skill's semver version. An ingested version is immutable, so bump it whenever the content changes. |

[Frontmatter reference](frontmatter-reference) lists every field.

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
- The overlay has no `version`, so the skill's version stays the `metadata.version` in `SKILL.md`. When the overlay declares a `version`, that value is the skill's version. It must then equal `metadata.version` whenever `metadata.version` is a `major.minor.patch` value.
- `when_to_use` lists explicit situations, and hybrid retrieval uses them as additional signal. Write concrete situations: "After month-end close" matches better than "When working on finance stuff."

---

## Step 3: Reference the bundled script

Every file in the artifact's directory other than the manifest files (`SKILL.md` and the optional `ARTIFACT.md`) is a bundled resource. Bundled resources include Python scripts, Jinja templates, JSON schemas, eval datasets, and files as large as model weights. The agentskills.io specification recommends `scripts/`, `references/`, and `assets/` as conventional subfolders. The per-package soft cap is 10 MB, and larger files use external resources (see [Bundled resources](bundled-resources)).

Reference the script from the `SKILL.md` body so the agent knows to run it:

```markdown
Greet the user by their first name (ask if you don't know it).
Tell them today's date by running [scripts/today.py](scripts/today.py).
Keep it to one or two sentences.
```

Lint resolves markdown links in the body against the artifact's bundled files at ingest, so a link to a path the package does not contain fails the check. A path written as inline code is not checked.

---

## Step 4: Declare runtime requirements

The script needs Python. Declaring the requirement lets a host refuse the artifact up front instead of failing at execution time.

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

How the requirement is enforced depends on the consumer:

- A host that advertises its runtime capabilities to the Podium MCP server refuses a `load_artifact` it cannot satisfy, with `materialize.runtime_unavailable`.
- A host that advertises no capabilities and does not opt into enforcement receives the requirement and proceeds.
- `podium sync` materializes the artifact without checking the requirement.

---

## Step 5: Iterate with watch mode

Watch mode re-materializes on every save, so a manual `podium sync` after each edit is unnecessary.

Start the watcher from your project directory:

```bash
cd ~/projects/your-project
podium sync --watch
```

Open Claude Code in another window. Edits to the `SKILL.md` prose body appear in the next session. Press `Ctrl-C` to stop the watcher.

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

The thin-description check runs at registry ingest rather than in local `podium lint`. When the catalog lives in a Git-source layer, CI runs the same checks on pull requests (see [Connecting layer sources](../deployment/content/layer-sources#git-repositories)).

Each diagnostic prints as `[<severity>] <artifact-id>: <message> (<rule-code>)`, for example `[error] personal/hello/greet: type is required (lint.required_field_missing)`. Warnings alone exit 0, and any error exits 1. A skill that declares no version draws the `lint.skill_version_assigned` warning, because the registry assigns its version. Set `metadata.version` in `SKILL.md` to clear it.

When lint passes, commit the artifact.

---

## Next steps

- **Write a slash command.** A `command` is a parameterized prompt template the user invokes by name. See [Your first command](your-first-command).
- **Write an agent.** An `agent` is a complete agent definition with its own instructions, dependencies, and optional bundled scripts. See [Your first agent](your-first-agent).
- **Learn the other built-in types.** [Artifact types](artifact-types) covers `rule`, `hook`, `context`, and `mcp-server` alongside skills, commands, and agents.
- **Organize multiple artifacts.** Group artifacts with `DOMAIN.md` files that add descriptions, keywords, and featured artifacts. See [Domains](domains).
- **Share the catalog with a team.** [Progressive adoption](../deployment/progressive-adoption) covers moving from a local catalog to a shared registry, and [Connecting layer sources](../deployment/content/layer-sources) covers publishing from a Git repository.
