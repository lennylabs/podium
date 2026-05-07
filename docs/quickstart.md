# Quickstart: Your First Skill in 5 Minutes

This walks you through installing Podium, authoring a skill, and using it in Claude Code. We will use a filesystem registry and the Podium CLI.

## What you'll need

- A terminal.
- Claude Code installed.

## 1. Install the binary

Install `podium` via your package manager or download the latest release binary from the project's releases page. Verify:

```bash
podium --version
```

## 2. Configure Podium

Tell Podium to use a local directory as its registry, and to materialize for Claude Code by default:

```bash
mkdir -p ~/podium-artifacts
podium init --global \
  --registry ~/podium-artifacts/ \
  --harness claude-code
```

That writes `~/.podium/sync.yaml` with `defaults.registry: ~/podium-artifacts/` (a filesystem path, so the client runs in-process with no server) and `defaults.harness: claude-code`. Verify:

```bash
podium config show
```

## 3. Author a skill

A skill is a directory with an `ARTIFACT.md` file. The first level under `~/podium-artifacts/` is treated as a layer; everything below is artifacts.

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

Greet the user by their first name (ask if you don't know it). Tell them
today's date in a friendly format. Be concise: one or two sentences.
EOF
```

## 4. Materialize into Claude Code's directory

```bash
cd ~/projects/your-project/   # or any directory with a .claude/ folder
podium sync --target .claude/
```

You should see something like:

```
Materialized 1 artifact to .claude/:
  personal/hello/greet@1.0.0 → .claude/agents/greet.md
```

## 5. Use it

Open Claude Code in that project. Type:

```
hello!
```

Claude Code natively discovers `.claude/agents/greet.md` (no MCP needed) and uses the skill. Greet the user by name with today's date.

## Watch mode (optional)

If you're authoring iteratively, run `podium sync --watch` instead. It uses `fsnotify` to watch the registry directory and re-materializes on every change. Save a `.peng.md`-style edit, see it land in `.claude/` immediately.

## What's next

- Add more artifacts in `~/podium-artifacts/<layer>/...`. The frontmatter `type:` field decides the kind (skill / agent / context / prompt / hook / command; see the spec).
- Use `podium config show` to inspect the merged config. This is useful as you add per-project configs.
- Pin per-project settings by running `podium init` (without `--global`) in a project workspace. Writes `<workspace>/.podium/sync.yaml` (committed to git) so teammates inherit your harness, target, and any profiles you set up.
- Outgrow filesystem-source mode? When you need progressive disclosure (Claude calls MCP meta-tools at runtime to load capabilities incrementally instead of materializing everything ahead of time), graduate to a standalone server: `podium serve --standalone --layer-path ~/podium-artifacts/`. The same artifact directory; just add a daemon. See [team-rollout.md](team-rollout.md) for the bigger-picture path.

## Troubleshooting

- **`config.no_registry` error.** `podium init` didn't run, or the resolved `defaults.registry` is empty. Run step 2 again.
- **`podium sync` says no artifacts.** Make sure your artifact lives under a layer subdirectory (`~/podium-artifacts/<layer-name>/...`), not directly in `~/podium-artifacts/`.
- **Claude Code doesn't see the skill.** Check that `.claude/agents/greet.md` exists; if it does, restart Claude Code so it re-reads the directory.
- **Skill is found but not loaded.** Check that `description:` is specific enough for Claude to recognize the skill matches the prompt. Vague descriptions don't get used; see §3.3 of the spec on description quality.
