---
layout: default
title: Your first artifact
parent: Authoring
nav_order: 1
description: From the quickstart's one-file skill to a richer artifact with a bundled script, runtime requirements, and a lint check before commit.
---

# Your first artifact

This page picks up from the [quickstart](../getting-started/quickstart) and adds a few additional pieces: a bundled script, fuller frontmatter, watch-mode iteration, and a lint check.

---

## Starting point

The quickstart leaves the artifact directory like this:

```
~/podium-artifacts/personal/hello/greet/
└── ARTIFACT.md
```

A single-file skill named `greet`.

---

## Add fuller frontmatter

The minimum frontmatter (`type`, `name`, `version`, `description`) is enough for the registry to ingest. The fields below pay off as the catalog grows.

Open the artifact:

```bash
$EDITOR ~/podium-artifacts/personal/hello/greet/ARTIFACT.md
```

Replace the frontmatter:

```yaml
---
type: skill
name: greet
version: 1.0.0
description: Greet the user by name and tell them today's date in a friendly format.
when_to_use:
  - "When the user opens a session with a greeting like 'hi' or 'hello'."
  - "When the user asks who you are at session start."
tags: [demo, hello-world, greeting]
sensitivity: low
---
```

Notes on these fields:

- **`description`** decides whether the harness reaches for this skill. A vague description like "Helper skill" gets ignored; a specific one like "Greet the user by name and tell them today's date" matches against actual user prompts. The lint rule on description quality flags the vague ones.
- **`when_to_use`** is a list of explicit situations. The harness uses these as additional retrieval signal. Be concrete: "After month-end close" beats "When working on finance stuff."

The full frontmatter reference is in [Frontmatter reference](frontmatter-reference).

---

## Add a bundled script

A skill can ship with files alongside `ARTIFACT.md`. Anything in the artifact's directory other than `ARTIFACT.md` is a bundled resource: Python scripts, Jinja templates, JSON schemas, eval datasets, all the way up to model weights. The per-package soft cap is 10 MB; larger files use external resources, see [Bundled resources](bundled-resources).

Add a script:

```bash
mkdir -p ~/podium-artifacts/personal/hello/greet/scripts
cat > ~/podium-artifacts/personal/hello/greet/scripts/today.py <<'EOF'
"""Print today's date in a friendly format."""
import datetime
print(datetime.date.today().strftime("%A, %B %-d, %Y"))
EOF
```

Reference the script from the prose body:

```markdown
Greet the user by their first name (ask if you don't know it).
Tell them today's date by running `scripts/today.py`. Keep it to
one or two sentences.
```

The reference is plain markdown. At ingest time, lint resolves the path against the artifact's bundled files. Broken paths fail the lint check.

---

## Declare runtime requirements

The script needs Python. Tell the harness so it can refuse to materialize if Python isn't available:

```yaml
runtime_requirements:
  python: ">=3.10"
```

Now the artifact looks like this:

```yaml
---
type: skill
name: greet
version: 1.0.0
description: Greet the user by name and tell them today's date in a friendly format.
when_to_use:
  - "When the user opens a session with a greeting like 'hi' or 'hello'."
  - "When the user asks who you are at session start."
tags: [demo, hello-world, greeting]
sensitivity: low
runtime_requirements:
  python: ">=3.10"
---

Greet the user by their first name (ask if you don't know it).
Tell them today's date by running `scripts/today.py`. Keep it to
one or two sentences.
```

And the directory:

```
~/podium-artifacts/personal/hello/greet/
├── ARTIFACT.md
└── scripts/
    └── today.py
```

---

## Iterate with watch mode

Re-running `podium sync` after every edit is fine, but watch mode is faster while authoring:

```bash
cd ~/projects/your-project
podium sync --watch
```

The watcher re-materializes on every save. Open Claude Code in another window; tweaks to the prose body show up on the next session.

`Ctrl-C` to stop.

---

## Lint before you commit

Before committing or pushing, run lint:

```bash
podium lint ~/podium-artifacts/personal/hello/greet/
```

Lint checks the frontmatter against the type's schema, validates that prose references in `ARTIFACT.md` resolve to bundled files, runs type-specific rules, and flags weak descriptions. CI runs the same checks on PRs to a Git-source layer.

If lint passes, you're good. If it warns or fails, the messages name the field and the file location.

---

## What's next

- **Try a different type.** Make a `command` (slash-invoked template), a `rule` (passive context the harness loads automatically), or a `hook` (lifecycle observer). [Artifact types](artifact-types) covers the first-class types.
- **Organize multiple artifacts.** As they accumulate, group them with `DOMAIN.md` files: descriptions, keywords, featured artifacts. See [Domains](domains).
- **Inherit from another artifact.** When two artifacts share most of their structure, `extends:` lets the second refine the first instead of duplicating it. See [Extends](extends).
- **Move from solo to team-shared.** [Deployment](../deployment/) walks the migration paths.
