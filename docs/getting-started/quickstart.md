---
layout: default
title: Quickstart
parent: Getting Started
nav_order: 1
description: Install Podium, write one skill, and see it load in Claude Code. The filesystem setup uses the CLI without a daemon or authentication.
---

# Quickstart

This page shows the filesystem setup. The catalog is a local directory,
and `podium sync` writes harness-native files into a project. This path
fits solo work, prototypes, and first evaluation.

{: .note }

> Podium is pre-release. No tagged binary has been published; build the
> `podium` CLI from source via the [development setup](../about/contributing#development-setup).
> See [Implementation status](../about/status) for the merge-and-release
> roadmap.

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

Build the `podium` binary from source until the first tagged release publishes packaged binaries:

```bash
git clone https://github.com/lennylabs/podium.git
cd podium
go build -o ~/.local/bin/podium ./cmd/podium
podium --version
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

The pair of files has these roles:

- **`SKILL.md` carries the agent-facing content.** The standard's required `name` and `description` sit in its frontmatter; the prose body is what the agent reads.
- **`ARTIFACT.md` carries Podium's structured frontmatter.** `type`, `version`, `when_to_use`, `tags`, `sensitivity`, and the rest of Podium's schema live here. The body is empty (a one-line HTML comment pointer).
- **The directory path is the canonical artifact ID.** Above, that's `personal/hello/greet`. References from other artifacts use this ID.

---

## 4. Materialize into Claude Code

From the project configured in step 2, run sync:

```bash
podium sync
```

Output:

```
Materialized 1 artifact to .:
  personal/hello/greet@1.0.0 → .claude/agents/greet.md
```

Podium reads the registry, finds the artifact, runs the Claude Code
harness adapter on it, and writes the result to the path Claude Code
expects. The default sync target is the
current directory; the adapter knows to write into `.claude/agents/`
underneath.

---

## 5. Use it

Open Claude Code in that project. Type:

```
hello!
```

Claude Code natively discovers `.claude/agents/greet.md`. Filesystem mode
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

- **Add more artifacts.** Drop more directories under
  `~/podium-artifacts/personal/` with their own `ARTIFACT.md` files
  (and `SKILL.md` for skills).
  Try a different `type:`: `command`, `context`, `rule`, `hook`,
  `agent`, `mcp-server`. The [authoring guide](../authoring/) has
  the field reference and recipes for each.
- **Share settings with teammates.** Commit the
  `<workspace>/.podium/sync.yaml` created above so teammates
  inherit your harness, target, and any [profiles](../authoring/)
  you set up. For a per-developer config that follows you across
  projects, use `podium init --global`.
- **Browse the catalog from the agent.** As your registry grows, the
  agent can call `load_domain`, `search_domains`, and
  `search_artifacts` to discover available artifacts. Runtime browsing
  requires a server. See [How it works](how-it-works) for the discovery
  meta-tools and when each applies.
- **Split the catalog into multiple layers.** This quickstart uses a
  single-layer setup (one filesystem layer rooted at `~/podium-artifacts/`).
  To compose several layers from one directory (for example, a shared
  team layer alongside a personal layer), opt the directory into
  filesystem-registry mode by adding a `.registry-config` with
  `multi_layer: true`. See [Solo / filesystem](../deployment/solo-filesystem)
  for the layout and `.registry-config` reference.
- **Outgrow filesystem mode.** When runtime discovery (agents loading
  capabilities mid-session) or a single audit log for a team becomes
  necessary, move to a standalone server:
  `podium serve --standalone --layer-path ~/podium-artifacts/`. The same
  directory and artifacts work behind the server. See
  [Deployment -> Small team](../deployment/).

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
`.claude/agents/greet.md` actually exists. If it does, restart Claude
Code so it re-reads its directory.

**Skill is found but not loaded.** Claude reads the `description:`
field to decide whether the skill matches your prompt. Vague
descriptions don't get used. The
[authoring guide](../authoring/) has more on description quality.
