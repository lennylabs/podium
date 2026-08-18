---
title: Quickstart
nav_order: 2
description: Install Podium, write one skill, and see it load in Claude Code. The local tier uses the CLI without a daemon or authentication.
---

# Quickstart

This page shows the local setup. The catalog is a directory on disk, and
`podium sync` writes harness-native files into a project. The same catalog
serves any other harness by changing one setting, which is the point of the
walkthrough below. This path fits any team or individual whose catalog does not
require access control or progressive discovery, including solo work,
prototypes, first evaluation, and Git-shared team catalogs.

> [!NOTE]
> Podium is at 0.3.x, an early release. The surface and its behavior may
> still change before 1.0. See [Implementation status](../about/status) for
> what's shipped and what's still on the roadmap.

---

## Prerequisites

- A terminal.
- [Claude Code](https://www.anthropic.com/claude-code) installed
  (or any other harness Podium supports; see [Configure your
  harness](../consuming/configure-your-harness)). The walkthrough
  below uses Claude Code; the commands are identical for other
  harnesses with `--harness <name>` swapped.

---

## 1. Install the CLI

Pick the channel that matches your platform. Homebrew, Scoop, and the release archives install the `podium`, `podium-server`, and `podium-mcp` binaries on PATH. The source build below writes the `podium` binary only. Run `go build -o ~/.local/bin/ ./cmd/...` to build all three.

**macOS / Linux (Homebrew):**

```bash
brew tap lennylabs/tap
brew install podium
podium version
```

**Windows (Scoop):**

```powershell
scoop bucket add lennylabs https://github.com/lennylabs/scoop-bucket
scoop install podium
podium version
```

**Direct binary download:** grab `podium-<os>-<arch>` (or the `.tar.gz` / `.zip` bundle that includes every binary) from the [latest release](https://github.com/lennylabs/podium/releases/latest).

**From source** (Go 1.26+ required):

```bash
git clone https://github.com/lennylabs/podium.git
cd podium
go build -o ~/.local/bin/podium ./cmd/podium
```

The [development setup](../about/contributing#development-setup) has the full prerequisites and the SDK build steps.

---

## 2. Tell Podium where the catalog lives

Pick a directory for artifacts. The examples use `~/podium-artifacts/`.
From the project that will consume the artifacts, point Podium at that
directory and set Claude Code as the default harness:

```bash
mkdir -p ~/podium-artifacts/personal
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code
```

That writes `<workspace>/.podium/sync.yaml` with two defaults: a
registry pointing at the directory (so the client reads from disk
directly, with no server) and a harness telling Podium how to format
outputs for Claude Code. Verify:

```bash
podium config show
```

The merged config should show the registry path and the harness. To share
these defaults with teammates, commit
`.podium/sync.yaml`. For a per-developer config that follows you
across projects, run `podium init --global` instead.

---

## 3. Write your first skill

A skill is a directory with two manifest files at its root:
`SKILL.md` from the [agentskills.io](https://agentskills.io/specification)
standard and `ARTIFACT.md` for Podium metadata. The registry path is one
filesystem layer; artifacts and intermediate domain directories live
underneath. The example below creates a `greet` skill under a `personal/hello/`
domain path:

```bash
mkdir -p ~/podium-artifacts/personal/hello/greet

cat > ~/podium-artifacts/personal/hello/greet/SKILL.md <<'EOF'
---
name: greet
description: Greet the user by name and tell them today's date. Use when the user greets you or asks who you are.
license: MIT
---

Greet the user by their first name (ask if you don't know it).
Tell them today's date in a friendly format. Keep it to one or
two sentences.
EOF

cat > ~/podium-artifacts/personal/hello/greet/ARTIFACT.md <<'EOF'
---
type: skill
version: 1.0.0
when_to_use:
  - "When the user greets you or asks who you are."
tags: [demo, hello-world]
sensitivity: low
---

<!-- Skill body lives in SKILL.md. -->
EOF
```

Add a script the skill can call. Anything beside the two manifests is a bundled
resource, and `scripts/` is the convention for executable helpers:

```bash
mkdir -p ~/podium-artifacts/personal/hello/greet/scripts

cat > ~/podium-artifacts/personal/hello/greet/scripts/today.py <<'EOF'
"""Print today's date in the format the greet skill asks for."""

from datetime import date

print(date.today().strftime("%A, %-d %B %Y"))
EOF
```

The pair of files has these roles:

- **`SKILL.md` carries the agent-facing content.** The standard's required `name` and `description` sit in its frontmatter, and the prose body is what the agent reads.
- **`ARTIFACT.md` carries Podium's structured frontmatter.** `type`, `version`, `when_to_use`, `tags`, `sensitivity`, and the rest of Podium's schema live here. The body is empty (a one-line HTML comment pointer).
- **The directory path is the canonical artifact ID.** Above, that is `personal/hello/greet`. References from other artifacts use this ID.

The unit is the directory. Anything else placed in `greet/` is a bundled
resource: a `scripts/` folder the skill calls, a `references/` folder it reads,
or an `assets/` folder it templates from. Bundled files are materialized
wherever the artifact is materialized, so a script written once reaches Claude
Code, Cursor, and a published marketplace alike. See
[Bundled resources](../authoring/bundled-resources) for the conventions and the
size limits.

---

## 4. Materialize into Claude Code

From the project configured in step 2, run sync:

```bash
podium sync
```

Output:

```
adapter: claude-code
target:  /Users/alice/projects/your-project
artifacts:
  - personal/hello/greet  [podium-artifacts]
      .claude/skills/greet/SKILL.md
      .claude/skills/greet/scripts/today.py
```

Podium reads the registry, finds the artifact, runs the Claude Code
harness adapter on it, and writes the result to the path Claude Code
expects. The default sync target is the
current directory, and the adapter writes a skill into
`.claude/skills/<name>/SKILL.md` underneath. The bundled script travels with it,
because the unit is the directory.

The adapter decides those paths. Point the same command at another harness and
the same source artifact lands where that runtime expects it:

```bash
podium sync --harness cursor
```

```
adapter: cursor
target:  /Users/alice/projects/your-project
artifacts:
  - personal/hello/greet  [podium-artifacts]
      .cursor/skills/greet/SKILL.md
      .cursor/skills/greet/scripts/today.py
```

Nothing in the catalog changed between those two runs.

Each run reconciles the whole target against the lock file, so this one removed
the `.claude/` files the previous run wrote. Put them back before the next step:

```bash
podium sync
```

[Configure your harness](../consuming/configure-your-harness) has the roster and
the destination table for each artifact type.

---

## 5. Use it

Open Claude Code in that project. Type:

```
hello!
```

Claude Code natively discovers `.claude/skills/greet/SKILL.md`. The local tier
does not require MCP. Claude Code recognizes that the skill's description
matches the prompt and uses it to produce a greeting with the current date.

---

## Watch mode (optional)

For iterative authoring, run `podium sync --watch` instead of
`podium sync`. It watches the registry directory with `fsnotify` and
re-materializes on every change. A saved edit to `SKILL.md` or
`ARTIFACT.md` lands in `.claude/` immediately.

---

## What's next

After the local loop works, continue with one of these paths:

- **Deliver to a second harness.** The same catalog serves every supported
  runtime. Re-run the sync with a different `--harness` value, or set one per
  target, and the adapter writes the layout that runtime reads.
  [Configure your harness](../consuming/configure-your-harness) has the roster,
  the per-type destinations, and the MCP setup for each.
- **Add more artifacts.** Drop more directories under
  `~/podium-artifacts/personal/` with their own `ARTIFACT.md` files
  (and `SKILL.md` for skills).
  Try a different `type:`: `command`, `context`, `rule`, `hook`,
  `agent`, or `mcp-server`. The [authoring guide](../authoring/) has
  the field reference and recipes for each.
- **Materialize only part of the catalog.** As the catalog grows past what one
  workspace needs, narrow the sync with include, exclude, and type filters, and
  name the result as a profile so one flag switches between scopes. See
  [Selective materialization](../consuming/selective-materialization).
- **Share settings with teammates.** Commit the
  `<workspace>/.podium/sync.yaml` created above so teammates
  inherit the harness, the target, and any profiles defined there. For a
  per-developer config that follows you across projects, use
  `podium init --global`.
- **Browse the catalog from the agent.** As the registry grows, the
  agent can traverse domains with `load_domain` and find artifacts with
  `search_domains` and `search_artifacts`, then materialize one with
  `load_artifact`. Runtime browsing requires a server. See
  [Browsing the catalog](../consuming/browsing-the-catalog).
- **Split the catalog into multiple layers.** This quickstart uses a
  single-layer setup, one filesystem layer rooted at `~/podium-artifacts/`.
  To compose several layers from one directory (for example, a shared
  team layer alongside a personal layer), opt the directory into
  multi-layer mode by adding a `.registry-config` with
  `multi_layer: true`. See [Local](../deployment/local)
  for the layout and the `.registry-config` reference.
- **Outgrow the local tier.** When runtime discovery (agents loading
  capabilities mid-session) or a single audit log for a team becomes
  necessary, move to a single-node server:
  `podium serve --standalone --layer-path ~/podium-artifacts/`. The same
  directory and artifacts work behind the server. See
  [Single node](../deployment/single-node).

---

## Troubleshooting

**`config.no_registry` error.** `podium init` didn't run, or the
resolved `defaults.registry` is empty. Re-run step 2.

**`podium sync` says no artifacts.** Confirm that the artifact directory
contains both `ARTIFACT.md` and (for skills) `SKILL.md` at its immediate
root. The directory path beneath `~/podium-artifacts/` is the canonical
artifact ID; intermediate directories without manifest files are domain
nodes, not artifacts.

**Claude Code doesn't see the skill.** Check that
`.claude/skills/greet/SKILL.md` actually exists. If it does, restart Claude
Code so it re-reads its directory.

**Skill is found but not loaded.** Claude reads the `description:`
field to decide whether the skill matches your prompt. Vague
descriptions don't get used. The
[authoring guide](../authoring/) has more on description quality.
