---
layout: default
title: Configure your harness
parent: Consuming
nav_order: 1
description: Per-harness setup. Configure podium sync for filesystem materialization or the Podium MCP server for runtime discovery.
---

# Configure your harness

A harness can consume Podium artifacts in either of two ways. Pick one (or both) per harness:

- **Filesystem materialization** via `podium sync`. Writes harness-native files to disk; the harness's own filesystem discovery picks them up. Works against either a filesystem-source registry or a running Podium server. No runtime calls.
- **Runtime discovery** via the Podium MCP server. The agent calls `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` mid-session and materializes only what it needs. Requires a Podium server.

Most harnesses support both. Use the per-harness section below.

---

## Common pieces

The Podium MCP server is a stdio binary the harness spawns alongside its other MCP servers. The same env-var contract applies regardless of harness:

| Variable | Purpose |
|:--|:--|
| `PODIUM_REGISTRY` | Registry source: URL (server) or filesystem path. |
| `PODIUM_HARNESS` | Harness adapter to use. Pass `none` for canonical raw output. |
| `PODIUM_OVERLAY_PATH` | Optional. Workspace local-overlay path; falls back to `<workspace>/.podium/overlay/` when MCP roots resolve. |
| `PODIUM_IDENTITY_PROVIDER` | `oauth-device-code` (developer hosts, default) or `injected-session-token` (managed runtimes). |

For `podium sync`, the same configuration lives in `<workspace>/.podium/sync.yaml` (or `~/.podium/sync.yaml` for per-developer defaults). See the per-harness sections for examples.

---

## Claude Code

**MCP server** (project-level `.claude/mcp.json` or user-level `~/.claude/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-code",
        "PODIUM_OVERLAY_PATH": "${WORKSPACE}/.podium/overlay/"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

**Where artifacts land:**

| Type | Location |
|:--|:--|
| `skill`, `agent` | `.claude/agents/<name>.md` |
| `rule` | `.claude/rules/<name>.md` |
| `command` | `.claude/commands/<name>.md` |
| `hook` | `.claude/hooks/<name>.json` (or the harness's hook config). |
| `context` | `.claude/context/<name>.md` |
| Bundled resources | `.claude/podium/<artifact-id>/` |

**Notes:**

- Native rule modes: `always` and `explicit` are fully supported. `glob` falls back to always-loaded with a lint warning. `auto` works via the `description` field.
- Native hook system available; see [Hooks](../authoring/hooks).

---

## Claude Desktop

**MCP server** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS; equivalents on Windows/Linux):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-desktop"
      }
    }
  }
}
```

**`podium sync`** writes a Claude Desktop extension layout: `manifest.json` derived from the canonical frontmatter, with bundled resources alongside. Target the Claude Desktop extensions directory.

**Notes:**

- Limited rule and hook surface. Most artifact types map to extension manifest entries.

---

## Cursor

**MCP server** (Settings → MCP, or `~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "cursor"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness cursor
podium sync
```

**Where artifacts land:**

| Type | Location |
|:--|:--|
| `rule` | `.cursor/rules/<name>.mdc` with `alwaysApply` / `globs` / `description` set per `rule_mode`. |
| `skill` | `.cursor/skills/<name>.md` |
| `command` | `.cursor/commands/<name>.md` |
| `hook` | `.cursor/hooks/<name>.json` |

**Notes:**

- Cursor has the most complete `rule_mode` support: all four values map natively to the `.mdc` frontmatter.
- Native hook system available.

---

## OpenCode

**MCP server** (`opencode.json` at the project root or `~/.config/opencode/opencode.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "opencode"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness opencode
podium sync
```

**Where artifacts land:**

OpenCode centers on `AGENTS.md`. Most artifact types inject into the appropriate `AGENTS.md`:

| Type | Location |
|:--|:--|
| `rule`, `skill`, `context` | Injected into `AGENTS.md` between Podium-managed XML markers. Project-root for `rule_mode: always`, common-ancestor directory for `rule_mode: glob`, standalone `.opencode/rules/<name>.md` for `rule_mode: explicit`. |
| `agent` | OpenCode-native agent definition. |
| `command`, `hook` | OpenCode-native locations. |

**Notes:**

- `rule_mode: auto` is not supported; ingest fails with a lint error unless `target_harnesses:` excludes opencode.
- Custom instruction files in `opencode.json` can reference Podium-materialized files; useful for explicit-mode rules.
- AGENTS.md takes precedence over CLAUDE.md when both exist.

---

## Codex

**MCP server**: configure per OpenAI Codex's MCP config conventions. The env-var contract is the same as the other harnesses; pass `PODIUM_HARNESS=codex`.

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness codex
podium sync
```

**Where artifacts land:**

Codex consumes `AGENTS.md`. Most artifact types inject into the appropriate `AGENTS.md`:

| Type | Location |
|:--|:--|
| `rule`, `skill`, `context` | Injected into root `AGENTS.md` (or common-ancestor for `rule_mode: glob`). |
| `agent` | Codex's native package layout. |

**Notes:**

- `rule_mode: auto` is not supported; ingest fails with a lint error.
- No native hook surface today; `hook`-type artifacts fail ingest unless `target_harnesses:` excludes codex.

---

## Gemini

**MCP server**: configure per the Gemini CLI's MCP config conventions. Pass `PODIUM_HARNESS=gemini`.

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness gemini
podium sync
```

**Notes:**

- Limited rule and hook surface today. `rule_mode: always` falls back to "best-effort always" with a lint warning. Other rule modes fail or fall back per the §6.7.1 capability matrix.
- See [Rule modes](../authoring/rule-modes) for the per-harness mapping.

---

## Pi

**MCP server** (`~/.pi/mcp.json` or project-local `.pi/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "pi"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness pi
podium sync
```

**Where artifacts land:**

Pi loads `AGENTS.md` from `~/.pi/agent/`, parent directories, and the CWD. Project-local `.pi/` overrides global `~/.pi/agent/`:

| Type | Location |
|:--|:--|
| `rule` (`always`) | Project-local `.pi/AGENTS.md` (or root `AGENTS.md`). Standalone files at `.pi/rules/<name>.md` for `rule_mode: explicit`. |
| `skill` | Pi's native skill location. |
| `command` | Pi's native command location. |

**Notes:**

- `rule_mode: auto` is not supported.
- Pi also reads `SYSTEM.md` and `APPEND_SYSTEM.md` for system-prompt customization; Podium does not write to these by default.

---

## Hermes

**MCP server** (`~/.config/hermes/mcp.json` or project-local `.hermes/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "hermes"
      }
    }
  }
}
```

**`podium sync`**:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness hermes
podium sync
```

**Where artifacts land:**

Hermes natively reads several rule formats — `.claude/rules/*.md`, `.cursor/rules/*.mdc`, root `AGENTS.md`, `.cursorrules`. The Hermes adapter writes the most-permissive format by default:

| Type | Location |
|:--|:--|
| `rule` | `.claude/rules/<name>.md` (team-shared) or `~/.claude/rules/<name>.md` (personal). Cursor-style `.mdc` is also accepted directly. |
| `skill`, `command` | Hermes's native locations. |

**Notes:**

- Hermes has the broadest rule-format compatibility of any harness Podium supports; all `rule_mode` values map cleanly via the cursor-style `.mdc` shape.

---

## Generic / `none`

For runtimes without a dedicated adapter, or when you want the canonical raw output, set `PODIUM_HARNESS=none`. The MCP server and `podium sync` write the canonical layout as-is — no harness-specific translation, no field renaming. Consumers (your runtime, an eval harness, custom tooling) read `ARTIFACT.md` and bundled resources directly.

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "none"
      }
    }
  }
}
```

```bash
podium init --registry ~/podium-artifacts/ --harness none
podium sync
```

This is also the right harness for build pipelines and evaluation harnesses that need the canonical artifact bytes without translation.

---

## Standalone (no env override)

When `podium serve` has auto-bootstrapped `~/.podium/sync.yaml` with `defaults.registry: http://127.0.0.1:8080`, or `podium init --global --standalone` has written it explicitly, the MCP server resolves the registry from there and the `PODIUM_REGISTRY` env var can be omitted. The harness still needs `PODIUM_HARNESS` set (or `--harness <name>` on the sync command).

---

## Capability matrix

The full per-harness capability matrix — which fields each adapter translates natively versus via fallback — is in [`spec/06-mcp-server.md` §6.7.1](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#671-the-authors-burden). [Rule modes](../authoring/rule-modes) has the per-harness mapping for the four `rule_mode` values.
