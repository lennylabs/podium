---
layout: default
title: Marketplace publishing
parent: Consuming
nav_order: 2
description: Render the catalog into harness-native marketplace repositories and push them to git remotes with podium publish.
---

# Marketplace publishing

`podium publish` renders the catalog into one or more harness-native marketplace repositories and runs an operator-configured workflow that pushes each repository to a git remote (§7.8). A harness with a git-repo distribution is a publish target: Claude (Code, Desktop, Cowork), Codex, Cursor, Gemini, Pi, and Hermes. OpenCode and `none` have no git-repo distribution and are not publish targets.

A marketplace output is a named publishing destination declared under `marketplaces:` in `publish.yaml`: a git repository, a harness set, a plugin list, and a workflow. A plugin is a named bundle of selected artifacts defined by a scope filter (`include`, `exclude`, `type`), the same selection `podium sync` uses. Each plugin renders into every harness's distribution layout in that harness's subtree.

## publish.yaml

`publish.yaml` lives beside `sync.yaml` under `.podium/` and resolves across the same three scopes with the same precedence as `sync.yaml`. Its top-level keys are `defaults` and `marketplaces`. The `defaults` block holds the registry, the publishing identity whose effective view the render reflects, and a default `workflow` each marketplace inherits unless it declares its own.

```yaml
# .podium/publish.yaml
defaults:
  registry: https://podium.acme.com
  identity: publisher@acme.com
  workflow:
    prepare:
      - run: ["git", "clone", "--branch", "$PODIUM_GIT_BRANCH", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"]
    publish:
      - run: ["git", "-C", "$PODIUM_WORKDIR", "add", "-A"]
      - run: ["git", "-C", "$PODIUM_WORKDIR", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"]
        skip_if_no_changes: true
      - run: ["git", "-C", "$PODIUM_WORKDIR", "push", "origin", "$PODIUM_GIT_BRANCH"]

marketplaces:
  - id: acme-agents
    git:
      remote: git@github.com:acme/agent-marketplace.git
      branch: main
    harnesses: [claude-code, codex, cursor]
    plugins:
      - name: finance-pack
        include: ["finance/**"]
```

## Repository layout

A marketplace output renders into one repository. Each vendor manifest sits at its fixed root location, and per-harness plugin content lives under `<harness>/<plugin>/`. The Claude, Codex, and Cursor manifests occupy distinct fixed paths and coexist in one repository, each read only by its own harness.

```
acme-agents/
  .claude-plugin/marketplace.json        # Claude Code, Desktop, Cowork
  .agents/plugins/marketplace.json       # Codex
  .cursor-plugin/marketplace.json        # Cursor
  claude/finance-pack/.claude-plugin/plugin.json + skills/ agents/ commands/ hooks/ .mcp.json
  codex/finance-pack/.codex-plugin/plugin.json   + .app.json .mcp.json skills/ hooks/
  cursor/finance-pack/.cursor-plugin/plugin.json + skills/ rules/ mcp.json
```

A multi-artifact plugin is listed once per harness manifest, keyed by the plugin name. A re-render reconciles the listing through the `OpMergeJSON` Podium-owned merge, so an artifact that leaves the effective view drops out of the manifest and its files are removed.

## Running podium publish

`podium publish` resolves the marketplace outputs and runs a fixed pipeline per output: the `prepare` commands place a checkout at the working directory, Podium renders the marketplace tree into it, and the `publish` commands take the rendered tree to the remote.

```bash
podium publish --config .podium/publish.yaml --output acme-agents
```

The flags are:

- `--output <id>` publishes only the named output. The default publishes every output.
- `--config <path>` reads an explicit `publish.yaml` instead of the merged config scopes.
- `--workdir <dir>` renders into an existing checkout, such as a CI `actions/checkout`, for a single output.
- `--dry-run` renders into a temporary directory and prints each command with its variables substituted, running no operator command and no publish phase.
- `--check` validates the config only and renders nothing.
- `--json` emits a structured JSON envelope on stdout.

Podium passes context to the commands through environment variables, including `$PODIUM_WORKDIR`, `$PODIUM_GIT_REMOTE`, `$PODIUM_GIT_BRANCH`, `$PODIUM_COMMIT_MESSAGE`, and `$PODIUM_CHANGED`. A command marked `skip_if_no_changes` is skipped when the render produced no diff against the checkout, so an unchanged catalog yields no commit.

The published marketplace reflects the publishing identity's effective view intersected with each plugin's scope filter. A principal that can see restricted layers would render them, so a public marketplace is published under an identity scoped to the intended artifacts.
