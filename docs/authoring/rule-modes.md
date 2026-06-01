---
layout: default
title: Rule modes
parent: Authoring
nav_order: 7
description: The rule_mode values (always, glob, auto, explicit) and how each harness honors them.
---

# Rule modes

A `rule` artifact is passive context the harness loads automatically. The `rule_mode` field controls when:

| Mode | Loaded when… | Required field |
|:--|:--|:--|
| `always` | The agent session starts (or every turn). | none |
| `glob` | A file matching the glob pattern is touched in the session. | `rule_globs` |
| `auto` | The harness's autoload heuristic decides this rule's `description` matches. | `rule_description` |
| `explicit` | The user references the rule by name. | none |

Default is `always` if you don't set the field.

---

## When to use each mode

**`always`** for project-wide guidance every session needs. Style guides, security policies, "you are a senior X engineer" framing.

```yaml
type: rule
name: house-style
rule_mode: always
```

**`glob`** for type-specific guidance. Load the React rule only when `*.tsx` is involved; load the SQL rule only when `*.sql` is touched.

```yaml
type: rule
name: react-style
rule_mode: glob
rule_globs: "src/**/*.tsx,src/**/*.ts"
```

**`auto`** for domain rules where the trigger is fuzzy. The harness's autoload heuristic uses the `description` to score relevance against the current situation.

```yaml
type: rule
name: db-migration-checks
rule_mode: auto
rule_description: "Apply when working with database migrations or schema changes"
```

**`explicit`** for rules so specific the user wants to invoke them deliberately. The harness materializes them but doesn't auto-load; the user references the rule by name (slash command, `@rule-name`, etc.).

```yaml
type: rule
name: incident-response
rule_mode: explicit
```

---

## How each harness honors them

The harness adapter does the translation at materialization time. Each adapter writes the rule into the harness's native format using the closest equivalent it has. When a harness can't honor a mode natively, the adapter falls back (with a lint warning) or refuses (with a lint error), per the capability matrix.

| Mode | claude-code | claude-desktop | claude-cowork | cursor | codex | opencode | gemini | pi | hermes |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `always` | ✓ | ✗ | ⚠ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `glob` | ✓ | ✗ | ⚠ | ✓ | ⚠ | ⚠ | ⚠ | ⚠ | ✓ |
| `auto` | ⚠ | ✗ | ⚠ | ✓ | ⚠ | ⚠ | ⚠ | ⚠ | ✓ |
| `explicit` | ⚠ | ✗ | ⚠ | ✓ | ⚠ | ⚠ | ⚠ | ⚠ | ✓ |

Legend: ✓ supported natively, ⚠ supported via fallback (lint warning), ✗ not supported (lint error or `target_harnesses:` opt-out required). This table mirrors the `rule_mode` rows of the §6.7.1 capability matrix.

---

## What each adapter writes

| Adapter | Output |
|:--|:--|
| **claude-code** | A standalone `.claude/rules/<name>.md` for every mode, carrying the prose with the Podium-internal fields dropped. `always` loads at launch and `glob` writes the native `paths:` YAML list, both native. `auto` and `explicit` fall back to a load-always file (no scoping frontmatter) and draw a lint warning, because Claude Code's `.claude/rules/` files have no description-attach or mention-only mode. |
| **cursor** | `.cursor/rules/<name>.mdc` for every mode, with the native key set per the mode: `always` writes `alwaysApply: true`, `glob` writes `globs: <rule_globs>`, `auto` writes `description: <rule_description>`, and `explicit` writes no auto-apply key. |
| **hermes** | `.cursor/rules/<name>.mdc` in the Cursor `.mdc` format for every mode. Hermes natively reads `.cursor/rules/*.mdc`, root `AGENTS.md`, and `.cursorrules`; it does not read `.claude/rules/`. |
| **codex, opencode, pi** | The rule body injects into root `AGENTS.md` between Podium-managed markers. `always` maps natively; `glob`, `auto`, and `explicit` fall back to always-loaded with a lint warning, because an injected block carries no per-file scoping. |
| **gemini** | The rule body injects into root `GEMINI.md` between Podium-managed markers, with the same `always`-native, non-`always`-fallback behavior as the `AGENTS.md` harnesses. |
| **claude-cowork** | A Cowork plugin has no native rule component, so the rule ships as a skill (`plugins/<id>/skills/<name>/SKILL.md`). Every mode is a fallback. |
| **claude-desktop** | No project-level surface, so a rule produces no Claude Desktop output. |

---

## Authoring guidance

- **Default to `always`** for guidance you'd want loaded every time. The cost is a few hundred tokens per session; the benefit is consistent behavior.
- **Use `glob` for type-specific guidance** that doesn't need to be loaded for unrelated work. Sharper context, smaller working set.
- **Use `auto` carefully.** It depends on the harness's autoload heuristic, which varies by harness and isn't something you can test deterministically. Write the `rule_description` as a clear trigger statement rather than a summary.
- **Use `explicit` for rules with sharp boundaries**: incident response procedures, regulatory compliance checklists, anything you'd rather have the user invoke deliberately than have the harness load opportunistically.

---

## Lint behavior

Lint enforces the field requirements per mode:

- `rule_mode: glob` requires `rule_globs`. Missing `rule_globs` is an ingest error.
- `rule_mode: auto` requires `rule_description`. Missing `rule_description` is an ingest error.
- `rule_mode: glob` with `rule_description` set: lint warning ("rule-mode 'glob' uses globs only; rule-description is ignored").
- `rule_mode: auto` with `rule_globs` set: lint warning ("rule-mode 'auto' uses description only; rule-globs is ignored").
- A type other than `rule` with `rule_mode` set: lint warning ("rule-mode is only applicable to type: rule").

When ingest crosses an unsupported (mode, harness) cell from the capability matrix, the lint surfaces the mismatch. Authors who must use a non-portable mode can declare `target_harnesses:` in frontmatter to opt out of cross-harness materialization for that artifact.
