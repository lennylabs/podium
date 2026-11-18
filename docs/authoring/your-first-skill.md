---
title: Your first skill
nav_order: 1
description: "Extend the quickstart's greet skill with a sharper description, Podium-only fields in an ARTIFACT.md overlay or in SKILL.md metadata, a referenced script, a runtime requirement, and a lint check before commit."
---

# Your first skill

This tutorial continues the [quickstart](../getting-started/quickstart) and extends its `greet` skill into a complete artifact. It sharpens the description, adds Podium-only fields in an `ARTIFACT.md` overlay, shows the `SKILL.md` `metadata` form of the fields that `metadata` can carry, points the body at the bundled script, declares a runtime requirement, and lints the result. For other artifact types, see [Your first command](your-first-command) and [Your first agent](your-first-agent).

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

The `description` decides whether the harness reaches for the skill. A vague description such as "Helper skill" gets ignored. A specific one that states what the skill does and when to use it matches the prompts users write. `podium lint` flags a thin description and two descriptions that overlap across the whole catalog, and the registry flags them within the layer it ingests, as [Step 6](#step-6-lint-before-you-commit) describes.

The version stays `1.0.0`, because a filesystem catalog keeps no ingest history. Once a registry server ingests the skill, `(artifact_id, version)` is immutable, so bump `metadata.version` whenever the content changes after that. [Frontmatter reference](frontmatter-reference) lists every field.

---

## Step 2: Add Podium-only fields in an ARTIFACT.md overlay

`SKILL.md` stays within the agentskills.io specification, so a Podium-only field does not sit at its top level, apart from the `when_to_use` that Claude Code defines (see [Claude Code's top-level `when_to_use`](frontmatter-reference#claude-codes-top-level-when_to_use)). Fields such as `when_to_use`, `tags`, and `sensitivity` can go under `SKILL.md` `metadata` as strings, or in an optional `ARTIFACT.md` beside `SKILL.md`, which the author writes by hand. The overlay carries every Podium-only field with its YAML type, and it is the only place for fields such as `runtime_requirements` that `metadata` does not carry. This step writes the overlay, and the end of the step shows the `metadata` form.

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

The same three fields can live under `SKILL.md` `metadata` instead, with no `ARTIFACT.md`. Each value is a string: `tags` is comma-separated, and `when_to_use` is a `|` literal block with one situation per line. This `SKILL.md` frontmatter sets the values the overlay above sets:

```markdown
---
name: greet
description: Greet the user by name and tell them today's date in a friendly format. Use when the user opens a session with a greeting or asks who you are.
license: MIT
metadata:
  version: "1.0.0"
  tags: "demo, hello-world, greeting"
  sensitivity: low
  when_to_use: |
    When the user opens a session with a greeting like 'hi' or 'hello'.
    When the user asks who you are at session start.
---
```

When both files set one of these fields, the `ARTIFACT.md` value takes precedence, and lint reports `lint.skill_artifact_field_mismatch` as an error when the `metadata` value differs from it. [Podium fields in SKILL.md metadata](frontmatter-reference#podium-fields-in-skillmd-metadata) lists the keys, their encodings, and the values lint ignores. The rest of this tutorial keeps the overlay, because Step 4 adds `runtime_requirements`, which only `ARTIFACT.md` carries.

---

## Step 3: Reference the bundled script

Every file in the artifact's directory other than the manifest files is a bundled resource, so `scripts/today.py` already ships with the skill. [Bundled resources](bundled-resources) covers the layout conventions and the size caps.

The quickstart's body asks for a friendly date format without mentioning the script. Replace the body of `SKILL.md` with these lines, so the agent knows to run it:

```markdown
Greet the user by their first name (ask if you don't know it).
Tell them today's date by running [scripts/today.py](scripts/today.py).
Keep it to one or two sentences.
```

Lint resolves inline markdown links and images in the body against the artifact's bundled files at ingest, so a link to a path the package does not contain fails the check. A path written as inline code and link syntax inside code are not checked. A skill's bundled files materialize inside the skill folder, so the link also resolves from the materialized `SKILL.md`.

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
- The catalog advisories, which warn on a thin description, on two descriptions that overlap, and on near-duplicate content in two artifacts.

When the catalog lives in a Git-source layer, CI runs the same checks on pull requests (see [Connecting layer sources](../deployment/content/layer-sources#git-repositories)). The registry runs the artifact checks again at ingest and runs the word-based catalog advisories over the layer it ingests, so an ingest does not report a word-based overlap with an artifact in another layer. `podium lint` reports overlaps across every layer it walks, and on a running registry the catalog overlap report (`podium overlap` or `GET /v1/overlap`) covers every layer the caller can see. When the semantic overlap check is on, an ingest also names peers in layers visible to `public` or `organization`. [Finding overlapping artifacts](../managing/overlap) describes each check.

Each diagnostic prints as `[<severity>] <artifact-id>: <message> (<rule-code>)`, for example `[error] personal/hello/greet: type is required (lint.required_field_missing)`. Info and warning diagnostics alone exit 0, and any error exits 1. With `--fail-on warning`, a warning also exits 1, and an info diagnostic still exits 0. [Skill lint checks](frontmatter-reference#skill-lint-checks) lists the checks that apply to a skill.

When lint passes, commit the artifact.

---

## Next steps

- **Write a slash command.** A skill whose `invocation` is `user` is a prompt template the user invokes by name. See [Your first command](your-first-command).
- **Write an agent.** An `agent` is a complete agent definition with its own instructions, dependencies, and optional bundled scripts. See [Your first agent](your-first-agent).
- **Learn the other built-in types.** [Artifact types](artifact-types) covers `rule`, `hook`, and `mcp-server` alongside skills and agents.
- **Organize multiple artifacts.** Group artifacts with `DOMAIN.md` files that add descriptions, keywords, and featured artifacts. See [Domains](domains).
- **Share the catalog with a team.** [Progressive adoption](../deployment/progressive-adoption) covers moving from a local catalog to a shared registry, and [Connecting layer sources](../deployment/content/layer-sources) covers publishing from a Git repository.
