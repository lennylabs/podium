---
layout: default
title: Your first skill
parent: Authoring
nav_order: 1
description: "From the quickstart's two-file skill to a richer artifact with a bundled script, runtime requirements, and a lint check before commit."
---

# Your first skill

This page picks up from the [quickstart](../getting-started/quickstart) and rounds out the same `greet` skill with a bundled script, fuller frontmatter, watch-mode iteration, and a lint check. For non-skill walkthroughs, see [Your first command](your-first-command) and [Your first agent](your-first-agent).

---

## Starting point

The quickstart leaves the artifact directory like this:

```
~/podium-artifacts/personal/hello/greet/
├── SKILL.md
└── ARTIFACT.md
```

A skill named `greet`. `SKILL.md` is the [agentskills.io](https://agentskills.io/specification) standard manifest with the agent-facing prose; `ARTIFACT.md` is Podium's structured frontmatter.

---

## Add fuller frontmatter

The minimum frontmatter is enough for the registry to ingest: `name` and `description` in `SKILL.md`, plus `type` and `version` in `ARTIFACT.md`. The fields below pay off as the catalog grows.

Open `SKILL.md`:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/SKILL.md
```

The standard's frontmatter holds the discoverability fields:

```yaml
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

```yaml
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

- **`description`** (in `SKILL.md`) decides whether the harness reaches for this skill. A vague description like "Helper skill" gets ignored; a specific one like "Greet the user by name and tell them today's date" matches against actual user prompts. The lint rule on description quality flags the vague ones.
- **`when_to_use`** (in `ARTIFACT.md`) is a list of explicit situations. Hybrid retrieval uses these as additional signal. Be concrete: "After month-end close" beats "When working on finance stuff."

The full frontmatter reference is in [Frontmatter reference](frontmatter-reference).

---

## Add a bundled script

A skill can ship with files alongside `SKILL.md` and `ARTIFACT.md`. Anything in the artifact's directory other than the two manifest files is a bundled resource: Python scripts, Jinja templates, JSON schemas, eval datasets, all the way up to model weights. The agentskills.io spec recommends `scripts/`, `references/`, and `assets/` as conventional subfolders. The per-package soft cap is 10 MB; larger files use external resources, see [Bundled resources](bundled-resources).

Add a script:

```bash
mkdir -p ~/podium-artifacts/personal/hello/greet/scripts
cat > ~/podium-artifacts/personal/hello/greet/scripts/today.py <<'EOF'
"""Print today's date in a friendly format."""
import datetime
print(datetime.date.today().strftime("%A, %B %-d, %Y"))
EOF
```

Reference the script from the `SKILL.md` body:

```markdown
Greet the user by their first name (ask if you don't know it).
Tell them today's date by running `scripts/today.py`. Keep it to
one or two sentences.
```

The reference is plain markdown. At ingest time, lint resolves the path against the artifact's bundled files. Broken paths fail the lint check.

---

## Declare runtime requirements

The script needs Python. Tell the harness so it can refuse to materialize if Python isn't available. Add this to `ARTIFACT.md`:

```yaml
runtime_requirements:
  python: ">=3.10"
```

Now the artifact's `ARTIFACT.md` looks like this:

```yaml
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
podium lint ~/podium-artifacts/personal/hello/greet/
```

Lint checks the frontmatter against the type's schema in both files, validates that prose references in `SKILL.md` resolve to bundled files, runs the agentskills.io compliance checks on `SKILL.md` (name format, description constraints, parent-directory match), runs type-specific rules, and flags weak descriptions. CI runs the same checks on PRs to a Git-source layer.

If lint passes, commit the artifact. If lint warns or fails, the messages name the field and the file location.

---

## What's next

- **Write a slash command.** A `command` is a parameterized prompt template the user invokes by name. See [Your first command](your-first-command).
- **Write an agent.** An `agent` is a complete agent definition with its own instructions, dependencies, and optional bundled scripts. See [Your first agent](your-first-agent).
- **Cover the other built-in types.** [Artifact types](artifact-types) covers `rule`, `hook`, `context`, and `mcp-server` alongside skills, commands, and agents.
- **Organize multiple artifacts.** As they accumulate, group them with `DOMAIN.md` files: descriptions, keywords, featured artifacts. See [Domains](domains).
- **Inherit from another artifact.** When two artifacts share most of their structure, `extends:` lets the second refine the first instead of duplicating it. See [Extends](extends).
- **Move from solo to team-shared.** [Deployment](../deployment/) walks the migration paths.
