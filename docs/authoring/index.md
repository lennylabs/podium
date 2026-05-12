---
layout: default
title: Authoring
nav_order: 2
has_children: true
description: Reference for writing artifacts. Field schema, type list, domain organization, and cross-cutting features.
---

# Authoring artifacts

Reference for writing artifacts. Each page below covers one topic.

| Page | What it covers |
|:--|:--|
| [Your first skill](your-first-skill) | Step-by-step from blank directory to a materialized skill. Picks up from the [quickstart](../getting-started/quickstart): bundled scripts, runtime requirements, watch mode, lint. |
| [Your first command](your-first-command) | A parameterized slash command the user invokes by name. Argument substitution and how the command lands in the harness's slash menu. |
| [Your first agent](your-first-agent) | A complete agent definition: minimal end-to-end version first, then runtime requirements, a bundled script, and a delegated artifact. |
| [Artifact types](artifact-types) | Built-in types: `skill`, `agent`, `context`, `command`, `rule`, `hook`, `mcp-server`. |
| [Frontmatter reference](frontmatter-reference) | Field-by-field reference for the YAML frontmatter in `ARTIFACT.md` (and, for skills, `SKILL.md`). |
| [Domains](domains) | How folders and subfolders structure the catalog, and how `DOMAIN.md` adds descriptions, keywords, featured artifacts, the prose body, and discovery rendering knobs. |
| [Rule modes](rule-modes) | The `rule_mode` values (`always`, `glob`, `auto`, `explicit`) and how each harness honors them. |
| [Hooks](hooks) | Lifecycle observers with `hook_event` and `hook_action`. |
| [Extends](extends) | Cross-layer inheritance with `extends:`. |
| [Bundled resources](bundled-resources) | Scripts, references, assets, and other files that ship alongside `ARTIFACT.md` (and `SKILL.md` for skills). |
| [Hints](hints) | Advisory metadata: `effort_hint` and `model_class_hint`. |

The three "Your first ..." walkthroughs are the foundation. The pages that follow are topic-specific reference.
