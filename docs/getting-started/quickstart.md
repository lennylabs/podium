---
layout: default
title: Quickstart
parent: Getting Started
nav_order: 1
description: Install Podium, write one skill, see it load in Claude Code. Five minutes, no daemon, no auth.
---

# Quickstart

Five minutes from a blank terminal to "the skill I just wrote is
running in Claude Code." Filesystem mode — no daemon, no port, no
auth. Just a CLI, a folder, and your harness.

This is the lightest possible Podium setup. It's the right starting
point for solo work, prototypes, and anyone evaluating Podium for
the first time. When you outgrow it, [the deployment
guide](../deployment/) walks the next steps.

---

## Prerequisites

- A terminal.
- [Claude Code](https://www.anthropic.com/claude-code) installed
  (or any other harness Podium supports — see [Configure your
  harness](../consuming/configure-your-harness)). The walkthrough
  below uses Claude Code; the commands are identical for other
  harnesses with `--harness <name>` swapped.

That's the entire prerequisite list.

---

## 1. Install the CLI

Install the `podium` binary via your package manager, or download a
release from the project's releases page.

```bash
podium --version
```

{: .note }

> Podium is in design phase — there's no shipped binary yet. The
> commands below describe the target experience and run against the
> first released drop. See [Status](../about/status) for what's
> wired up today.

---

## 2. Tell Podium where the catalog lives

Pick a directory for your artifacts (anywhere — `~/podium-artifacts/`
is a fine default), and tell Podium that's your registry, with
Claude Code as the default harness:

```bash
mkdir -p ~/podium-artifacts/personal
podium init --global \
  --registry ~/podium-artifacts/ \
  --harness claude-code
```

That writes `~/.podium/sync.yaml` with two defaults: a registry
pointing at the directory (so the client reads from disk directly —
no server) and a harness telling Podium how to format outputs for
Claude Code. Verify:

```bash
podium config show
```

You should see the registry path and the harness in the merged
config.

---

## 3. Write your first skill

A skill is a directory with an `ARTIFACT.md` file at its root. The
top-level directories under your registry path are *layers*; everything
below is the artifact namespace. Make a skill called `greet` under
the `personal` layer:

```bash
mkdir -p ~/podium-artifacts/personal/hello/greet
cat > ~/podium-artifacts/personal/hello/greet/ARTIFACT.md <<'EOF'
---
type: skill
name: greet
version: 1.0.0
description: Greet the user by name and tell them today's date.
when_to_use:
  - "When the user greets you or asks who you are."
tags: [demo, hello-world]
sensitivity: low
---

Greet the user by their first name (ask if you don't know it).
Tell them today's date in a friendly format. Keep it to one or
two sentences.
EOF
```

Two things worth knowing about that file:

- **Frontmatter is metadata**, parsed by Podium for indexing,
  search, and harness-adapter translation. The body below the
  frontmatter is the prose the agent actually reads.
- **The directory path is the canonical artifact ID.** Above, that's
  `personal/hello/greet`. References from other artifacts use this ID.

---

## 4. Materialize into Claude Code

In a project where you use Claude Code (any directory with a
`.claude/` folder, or where you'd like one):

```bash
cd ~/projects/your-project/
podium sync --target .claude/
```

Output:

```
Materialized 1 artifact to .claude/:
  personal/hello/greet@1.0.0 → .claude/agents/greet.md
```

Podium walked your registry, found the one artifact you authored,
ran the Claude Code harness adapter on it, and wrote the result to
the path Claude Code expects.

---

## 5. Use it

Open Claude Code in that project. Type:

```
hello!
```

Claude Code natively discovers `.claude/agents/greet.md` (no MCP
needed for filesystem mode), recognizes that the skill's description
matches your prompt, and uses it. You should see Claude greet you
and tell you today's date.

---

## Watch mode (optional)

If you're authoring iteratively, run `podium sync --watch` instead
of `podium sync`. It watches the registry directory with `fsnotify`
and re-materializes on every change. Save a tweak to `ARTIFACT.md`
and see it land in `.claude/` immediately.

---

## What's next

Now that the loop works, here's where to go:

- **Add more artifacts.** Drop more directories under
  `~/podium-artifacts/personal/` with their own `ARTIFACT.md` files.
  Try a different `type:` — `command`, `context`, `rule`, `hook`,
  `agent`, `mcp-server`. The [authoring guide](../authoring/) has
  the field reference and recipes for each.
- **Pin per-project settings.** Run `podium init` (without
  `--global`) inside a project to write
  `<workspace>/.podium/sync.yaml` so teammates inherit your harness,
  target, and any [profiles](../authoring/) you set up. Commit the
  file.
- **Browse the catalog from the agent.** As your registry grows, the
  agent can call `load_domain`, `search_domains`, and
  `search_artifacts` to discover what's available — but that needs
  a server. See [How it works](how-it-works) for the four discovery
  meta-tools and when each fires.
- **Outgrow filesystem mode.** When you want runtime discovery
  (agents loading capabilities mid-session) or a single audit log
  for a team, graduate to a standalone server: `podium serve
  --standalone --layer-path ~/podium-artifacts/`. Same directory,
  same artifacts — just add a daemon. See [Deployment →
  Small team](../deployment/).

---

## Troubleshooting

**`config.no_registry` error.** `podium init` didn't run, or the
resolved `defaults.registry` is empty. Re-run step 2.

**`podium sync` says no artifacts.** The artifact must live under a
*layer* subdirectory (`~/podium-artifacts/<layer-name>/...`), not
directly in `~/podium-artifacts/`. Layer names are the top level;
artifacts go below.

**Claude Code doesn't see the skill.** Check that
`.claude/agents/greet.md` actually exists. If it does, restart Claude
Code so it re-reads its directory.

**Skill is found but not loaded.** Claude reads the `description:`
field to decide whether the skill matches your prompt. Vague
descriptions don't get used. The
[authoring guide](../authoring/) has more on description quality.
