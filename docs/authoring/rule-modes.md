---
layout: default
title: Rule modes
parent: Authoring
nav_order: 5
description: The four rule_mode values (always, glob, auto, explicit) and how each harness honors them.
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

| Mode | claude-code | cursor | codex | opencode | gemini | pi | hermes | generic |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `always` | ✓ | ✓ | ✓ | ✓ | ⚠ | ✓ | ✓ | ✓ |
| `glob` | ⚠ | ✓ | ⚠ | ⚠ | ✗ | ⚠ | ✓ | ⚠ |
| `auto` | ⚠ | ✓ | ✗ | ✗ | ✗ | ✗ | ⚠ | ✗ |
| `explicit` | ✓ | ✓ | ✓ | ✓ | ⚠ | ✓ | ✓ | ✓ |

Legend: ✓ supported natively, ⚠ supported via fallback (lint warning), ✗ not supported (lint error or `target_harnesses:` opt-out required).

The full matrix and field-by-field detail is in [§6.7.1 of the spec](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#671-the-authors-burden).

---

## What each adapter writes

| Mode | Adapter output |
|:--|:--|
| **claude-code, `always`** | `.claude/rules/<name>.md` written, gets injected into `CLAUDE.md` between markers. |
| **claude-code, `glob`** | Falls back to always-loaded with a lint warning (Claude Code doesn't natively scope rules by file pattern). |
| **claude-code, `auto`** | `.claude/rules/<name>.md` with the `description` field; Claude's autoload heuristic uses it. |
| **claude-code, `explicit`** | Standalone `.claude/rules/<name>.md`, referenced manually. |
| **cursor, all modes** | `.cursor/rules/<name>.mdc` with `alwaysApply` / `globs` / `description` set per the mode. |
| **copilot, `always`** | `.github/instructions/<name>.instructions.md` with `applyTo: "**"`. |
| **copilot, `glob`** | `applyTo: "<globs>"`. |
| **copilot, `auto`** | `description: "..."` only (no `applyTo`); Copilot semantically matches against the description. |
| **copilot, `explicit`** | No `applyTo`, no `description`; user references manually. |
| **opencode, `always` and `explicit`** | Injected into root `AGENTS.md` between markers. |
| **opencode, `glob`** | Injected into the common-ancestor directory's `AGENTS.md` (warning issued; OpenCode lacks native glob semantics). |
| **opencode, `auto`** | Lint error; OpenCode lacks an autoload heuristic. |
| **codex, `always` and `explicit`** | Injected into root `AGENTS.md`. |
| **codex, `glob`** | Common-ancestor `AGENTS.md` fallback (warning). |
| **codex, `auto`** | Lint error. |
| **pi, `always` and `explicit`** | Injected into project-local `.pi/AGENTS.md` or root `AGENTS.md`. Explicit-mode rules at `.pi/rules/<name>.md`. |
| **pi, `glob`** | Common-ancestor `AGENTS.md` (warning). |
| **pi, `auto`** | Lint error. |
| **hermes, all modes** | `.claude/rules/<name>.md` written in cursor-`.mdc` shape (Hermes natively reads `.cursor/rules/*.mdc`, so the cursor adapter output works directly). |
| **gemini** | Limited rule support; most modes fall back or fail. |
| **generic** | AGENTS.md injection for `always`; common-ancestor for `glob`; standalone files referenced manually for `explicit`; `auto` not supported. |

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
