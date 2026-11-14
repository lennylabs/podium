---
title: Authoring
nav_order: 2
description: Write artifacts for a Podium registry, from a first skill to the full frontmatter schema, domain organization, and type-specific features.
---

# Authoring

This section covers writing artifacts for a Podium registry. An artifact is a directory with a manifest file and any files it ships, and the set of artifacts a registry holds is its catalog. Start with a tutorial, then use the topic pages as reference while the catalog grows.

## Tutorials

Each tutorial builds one artifact step by step and runs it in Claude Code.

| Page | What it builds |
|:--|:--|
| [Your first skill](your-first-skill) | Extends the [quickstart](../getting-started/quickstart) `greet` skill with fuller frontmatter, an `ARTIFACT.md` overlay, a reference to its bundled script, runtime requirements, watch mode, and lint. |
| [Your first command](your-first-command) | A parameterized slash command that substitutes the user's arguments and appears in the harness's slash menu. |
| [Your first agent](your-first-agent) | A minimal agent definition that runs end to end, then a runtime requirement, a bundled script, and a delegated artifact. |

## Topics

| Page | What it covers |
|:--|:--|
| [Artifact types](artifact-types) | The built-in types (`skill`, `agent`, `context`, `command`, `rule`, `hook`, and `mcp-server`) and what each is for. |
| [Frontmatter reference](frontmatter-reference) | Every frontmatter field, and which file holds it: `SKILL.md` and the optional `ARTIFACT.md` for a skill, or `ARTIFACT.md` for every other type. |
| [Domains](domains) | How folders define the catalog hierarchy, and how `DOMAIN.md` adds descriptions, keywords, featured artifacts, imports, and discovery settings. |
| [Rule modes](rule-modes) | The `rule_mode` values (`always`, `glob`, `auto`, and `explicit`) and how each harness honors them. |
| [Hooks](hooks) | Lifecycle observers that run a shell `hook_action` on a canonical `hook_event`. |
| [Bundled resources](bundled-resources) | Scripts, references, assets, and other files that ship with the manifest, plus external resources for large files. |
| [Hints](hints) | The advisory `effort_hint` and `model_class_hint` fields. |

To publish artifacts from a Git repository to a registry server, see [Connecting layer sources](../deployment/layer-sources#git-repositories).
