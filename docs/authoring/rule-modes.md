---
title: Rule modes
nav_order: 7
description: The rule_mode values (always, glob, auto, and explicit), when to use each, and how each harness honors them.
---

# Rule modes

This page explains the `rule_mode` field of a `rule` artifact: what each mode does, when to choose it, and how each harness translates it.

A `rule` artifact is passive context that the harness loads automatically. The `rule_mode` field controls when the harness loads it:

| Mode | Loaded when | Required field |
|:--|:--|:--|
| `always` | The agent session starts (or every turn). | none |
| `glob` | A file matching the glob pattern is touched in the session. | `rule_globs` |
| `auto` | The harness's autoload heuristic decides this rule's `description` matches. | `rule_description` |
| `explicit` | The user references the rule by name. | none |

A rule that does not set the field uses `always`.

---

## When to use each mode

### always

Use `always` for project-wide guidance every session needs, such as style guides, security policies, and "you are a senior X engineer" framing. Default to `always` for guidance that belongs in every session. It costs a few hundred tokens per session and gives consistent behavior.

```yaml
type: rule
name: house-style
rule_mode: always
```

### glob

Use `glob` for guidance tied to file types, so it stays out of unrelated work. For example, load the React rule only when `*.tsx` is involved, and the SQL rule only when `*.sql` is touched. The working set stays smaller and the context sharper.

```yaml
type: rule
name: react-style
rule_mode: glob
rule_globs: "src/**/*.tsx,src/**/*.ts"
```

### auto

Use `auto` for domain rules whose trigger is fuzzy. The harness's autoload heuristic scores the `rule_description` against the current situation. The heuristic varies by harness and cannot be tested deterministically, so use this mode with care. Write the `rule_description` as a clear trigger statement rather than a summary.

```yaml
type: rule
name: db-migration-checks
rule_mode: auto
rule_description: "Apply when working with database migrations or schema changes"
```

### explicit

Use `explicit` for rules with sharp boundaries that the user invokes deliberately, such as incident response procedures and regulatory compliance checklists. The harness materializes the rule but does not load it automatically. The user references the rule by name, for example with a slash command or `@rule-name`.

```yaml
type: rule
name: incident-response
rule_mode: explicit
```

---

## How each harness honors them

The harness adapter translates the mode at materialization time. Each adapter writes the rule in the harness's native format, using the closest equivalent the harness has. When a harness has no native equivalent for a mode, the adapter falls back to an always-loaded block.

| Mode | claude-code | claude-desktop | claude-cowork | cursor | codex | opencode | gemini | pi | hermes |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `always` | ✓ | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `glob` | ✓ | ✗ | ✗ | ✓ | ⚠ | ⚠ | ⚠ | ⚠ | ✓ |
| `auto` | ⚠ | ✗ | ✗ | ✓ | ⚠ | ⚠ | ⚠ | ⚠ | ✓ |
| `explicit` | ⚠ | ✗ | ✗ | ✓ | ⚠ | ⚠ | ⚠ | ⚠ | ✓ |

| Symbol | Meaning | Lint, when `target_harnesses:` names the harness | Materialization |
|:--|:--|:--|:--|
| ✓ | Native | No diagnostic. | Writes the native form. |
| ⚠ | Fallback | Warning. | Writes the degraded fallback. |
| ✗ | Unavailable | Error. | Fails with `materialize.untranslatable`. |

This table mirrors the `rule_mode` rows of the [capability matrix in §6.7.1 of the specification](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#671-the-authors-burden). The `claude-cowork` cells are ✗ for every mode, because Cowork has no project-scope rule surface. A Cowork user obtains a rule through the published Claude marketplace ([Publishing](../consuming/publishing)).

---

## What each adapter writes

| Adapter | Output |
|:--|:--|
| **claude-code** | A standalone `.claude/rules/<name>.md` for every mode, carrying the prose with the Podium-internal fields dropped. `always` loads at launch, and `glob` writes the native `paths:` YAML list; both are native. `auto` and `explicit` fall back to a load-always file with no scoping frontmatter and draw a lint warning, because Claude Code's `.claude/rules/` files have no description-attach or mention-only mode. |
| **cursor** | `.cursor/rules/<name>.mdc` for every mode, with the native key set per mode: `always` writes `alwaysApply: true`, `glob` writes `globs: <rule_globs>`, `auto` writes `description: <rule_description>`, and `explicit` writes no auto-apply key. |
| **hermes** | `.cursor/rules/<name>.mdc` in the Cursor `.mdc` format for every mode. Hermes natively reads `.cursor/rules/*.mdc`, the root `AGENTS.md`, and `.cursorrules`. It does not read `.claude/rules/`. |
| **codex, opencode, pi** | The rule body, injected into the root `AGENTS.md` between Podium-managed markers. `always` maps natively. `glob`, `auto`, and `explicit` fall back to always-loaded with a lint warning, because an injected block carries no per-file scoping. |
| **gemini** | The rule body, injected into the root `GEMINI.md` between Podium-managed markers. The behavior matches the `AGENTS.md` harnesses: `always` is native, and the other modes fall back. |
| **claude-cowork** | No project-scope rule surface. A `type: rule` artifact on `claude-cowork` is a ✗ cell and fails materialization with `materialize.untranslatable`, on both `podium sync` and `load_artifact`, including the default `always` mode. A Cowork user obtains the rule through the published Claude marketplace ([Publishing](../consuming/publishing)). |
| **claude-desktop** | No project-scope rule surface. Every `rule_mode` cell is ✗, so a `type: rule` artifact on `claude-desktop` fails materialization with `materialize.untranslatable`, on both `podium sync` and `load_artifact`, including the default `always` mode. |

---

## Lint behavior

Lint enforces the field requirements for each mode:

| Condition | Result |
|:--|:--|
| `rule_mode: glob` without `rule_globs` | Ingest error. |
| `rule_mode: auto` without `rule_description` | Ingest error. |
| `rule_mode: glob` with `rule_description` set | Warning: "rule-mode 'glob' uses globs only; rule-description is ignored". |
| `rule_mode: auto` with `rule_globs` set | Warning: "rule-mode 'auto' uses description only; rule-globs is ignored". |
| `rule_mode` set on a type other than `rule` | Warning: "rule-mode is only applicable to type: rule". |
| A `rule_mode` value outside `always`, `glob`, `auto`, and `explicit` on a `type: rule` artifact | Ingest error (`lint.unknown_rule_mode`). |

Portability checks depend on `target_harnesses:`:

- When an artifact declares `target_harnesses:`, ingest lint reports a mismatch for any named harness whose cell for the chosen mode is ⚠ (warning) or ✗ (error).
- When `target_harnesses:` is absent, ingest stays permissive. Materialization catches an unsupported mode when the rule loads onto that harness.

An author who needs a non-portable mode declares `target_harnesses:` in frontmatter to opt the artifact out of cross-harness materialization.
