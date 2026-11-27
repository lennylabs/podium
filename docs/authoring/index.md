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
| [Your first skill](your-first-skill) | Extends the [quickstart](../getting-started/quickstart) `greet` skill with a sharper description, an `ARTIFACT.md` overlay, a reference to its bundled script, a runtime requirement, and lint. |
| [Your first command](your-first-command) | A user-invoked skill that a human starts as a slash command, which substitutes the user's arguments and appears in the harness's slash menu. |
| [Your first agent](your-first-agent) | A minimal agent definition that runs end to end, then a runtime requirement, a bundled script, and a delegated artifact. |

## Topics

| Page | What it covers |
|:--|:--|
| [Artifact types](artifact-types) | The built-in types (`skill`, `agent`, `rule`, `hook`, and `mcp-server`), what each is for, and what replaced the removed `command` and `context` types. |
| [Frontmatter reference](frontmatter-reference) | Every frontmatter field, and which file holds it: `SKILL.md` and the optional `ARTIFACT.md` for a skill, or `ARTIFACT.md` for every other type. |
| [Bundled resources](bundled-resources) | Scripts, references, assets, and other files that ship with the manifest, where they land, how hosts run them, and external resources for large files. |
| [Domains](domains) | How folders define the catalog hierarchy, and how `DOMAIN.md` adds descriptions, keywords, featured artifacts, imports, and discovery settings. |
| [Rule modes](rule-modes) | The `rule_mode` values (`always`, `glob`, `auto`, and `explicit`) and how each harness honors them. |
| [Hooks](hooks) | Lifecycle observers that run a shell `hook_action` on a canonical `hook_event`. |
| [Hints](hints) | The advisory `effort_hint` and `model_class_hint` fields. |
| [Signing with GitHub Actions](signing-with-github-actions) | The workflow that `podium attest init` writes, which signs a layer on each push, and the registration with `--trust-preset github-actions`. |

To publish artifacts from a Git repository to a registry server, see [Connecting layer sources](../deployment/content/layer-sources#git-repositories).
