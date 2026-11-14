---
title: Quickstart
nav_order: 2
description: Install Podium, write one skill in a local catalog, and load it in Claude Code, with no daemon or authentication.
---

# Quickstart

This quickstart installs the Podium CLI, writes one skill into a local catalog,
and materializes it into Claude Code. It uses the local deployment mode, where
the catalog is a directory on disk and no server runs. That mode fits any
catalog that needs no access control or progressive discovery.

> [!NOTE]
> Podium is an early release. The surface and its behavior may
> still change before 1.0. See [Implementation status](../about/status) for
> what's shipped and what's still on the roadmap.

---

## Prerequisites

- A terminal.
- [Claude Code](https://www.anthropic.com/claude-code), or any other harness
  listed in [Configure your harness](../consuming/configure-your-harness). The
  steps use Claude Code. For another harness, pass its name as
  `--harness <name>`.

---

## 1. Install the CLI

Install Podium through the channel that matches the platform. Homebrew, Scoop,
and the release archives put the `podium`, `podium-server`, and `podium-mcp`
binaries on PATH.

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

**Direct binary download:** download `podium-<os>-<arch>`, or the `.tar.gz` or `.zip` bundle that includes every binary, from the [latest release](https://github.com/lennylabs/podium/releases/latest).

**From source** (Go 1.26+ required):

```bash
git clone https://github.com/lennylabs/podium.git
cd podium
go build -o ~/.local/bin/podium ./cmd/podium
```

The source build writes the `podium` binary only. Run
`go build -o ~/.local/bin/ ./cmd/...` to build all three binaries. The
[development setup](../about/contributing#development-setup) has the full
prerequisites and the SDK build steps.

`podium version` prints the installed version.

---

## 2. Point Podium at the catalog

Create a directory for artifacts, then configure the project that consumes
them. The examples use `~/podium-artifacts/` and set Claude Code as the default
harness:

```bash
mkdir -p ~/podium-artifacts/personal
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code
```

`podium init` writes `<workspace>/.podium/sync.yaml` with two defaults. The
registry default points at the directory, so the client reads from disk with
no server. The harness default tells Podium to format output for Claude Code.

Confirm the configuration:

```bash
podium config show
```

The merged configuration shows the registry path and the harness.

---

## 3. Write a skill

A skill is a directory with a `SKILL.md` from the
[agentskills.io](https://agentskills.io/specification) standard at its root.
The registry path is one filesystem layer, and artifacts and intermediate
domain directories live underneath it.

Create a `greet` skill under the `personal/hello/` domain path:

```bash
mkdir -p ~/podium-artifacts/personal/hello/greet

cat > ~/podium-artifacts/personal/hello/greet/SKILL.md <<'EOF'
---
name: greet
description: Greet the user by name and tell them today's date. Use when the user greets you or asks who you are.
license: MIT
metadata:
  version: "1.0.0"
---

Greet the user by their first name (ask if you don't know it).
Tell them today's date in a friendly format. Keep it to one or
two sentences.
EOF
```

Add a script the skill can call. Anything beside the manifest files is a
bundled resource, and `scripts/` is the convention for executable helpers:

```bash
mkdir -p ~/podium-artifacts/personal/hello/greet/scripts

cat > ~/podium-artifacts/personal/hello/greet/scripts/today.py <<'EOF'
"""Print today's date in the format the greet skill asks for."""

from datetime import date

print(date.today().strftime("%A, %-d %B %Y"))
EOF
```

The files have these roles:

- **`SKILL.md`** carries the agent-facing content. Its frontmatter holds the
  standard's required `name` and `description` and the version in
  `metadata.version`. Its prose body is what the agent reads.
- **An optional, hand-written `ARTIFACT.md`** beside it carries Podium-only
  fields such as `when_to_use`, `tags`, and `sensitivity`. See the
  [frontmatter reference](../authoring/frontmatter-reference).
- **The directory path** is the canonical artifact ID, here
  `personal/hello/greet`. References from other artifacts use this ID.
- **Everything else in `greet/`** is a bundled resource, such as `scripts/`,
  `references/`, or `assets/`. Bundled files are materialized wherever the
  artifact is. See [Bundled resources](../authoring/bundled-resources).

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

Podium runs the Claude Code harness adapter on the artifact and writes the
result where Claude Code expects it. The default sync target is the current
directory, and a skill lands in `.claude/skills/<name>/SKILL.md` with its
bundled script beside it.

---

## 5. Materialize into a second harness

The adapter decides the destination paths. Sync the same catalog for Cursor:

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

Nothing in the catalog changed between the two runs. Each run reconciles the
whole target against the lock file, so this run removed the `.claude/` files
the previous run wrote. Restore them before the next step:

```bash
podium sync
```

[Configure your harness](../consuming/configure-your-harness) has the roster and
the destination table for each artifact type.

---

## 6. Use the skill

Open Claude Code in the project and type:

```
hello!
```

Claude Code discovers `.claude/skills/greet/SKILL.md` natively, so the local
mode needs no MCP server. Claude Code matches the skill's description to the
prompt and replies with a greeting and the current date.

---

## Watch mode (optional)

For iterative authoring, run `podium sync --watch` instead of `podium sync`. It
watches the registry directory with `fsnotify` and re-materializes on every
change. A saved edit to `SKILL.md`, or to an `ARTIFACT.md` overlay, lands in
`.claude/` immediately.

---

## Next steps

- **Share settings with teammates.** Commit `<workspace>/.podium/sync.yaml` so
  teammates inherit the harness, the target, and any profiles defined there.
  For a per-developer configuration that applies across projects, run
  `podium init --global` instead.
- **Add more artifacts.** Add directories under `~/podium-artifacts/personal/`,
  each with a `SKILL.md` for a skill or an `ARTIFACT.md` for a `command`,
  `context`, `rule`, `hook`, `agent`, or `mcp-server`. See the
  [authoring guide](../authoring/).
- **Materialize part of the catalog.** Narrow the sync with include, exclude,
  and type filters, and save the result as a profile. See
  [Selective materialization](../consuming/selective-materialization).
- **Split the catalog into layers.** This quickstart uses one filesystem layer
  rooted at `~/podium-artifacts/`. To compose several layers from one
  directory, such as a shared team layer and a personal layer, add a
  `.registry-config` with `multi_layer: true`. See [Local](../deployment/modes/local).
- **Move to a server.** Runtime discovery, where an agent finds artifacts with
  `load_domain`, `search_domains`, and `search_artifacts` and loads one with
  `load_artifact`, requires a server. So does a single audit log for a team.
  Start a single-node server on the same directory with
  `podium serve --standalone --layer-path ~/podium-artifacts/`. See
  [Single node](../deployment/modes/single-node) and
  [Browsing the catalog](../consuming/browsing-the-catalog).

---

## Troubleshooting

| Symptom | Cause | Fix |
|:--|:--|:--|
| `config.no_registry` error | `podium init` did not run, or the resolved `defaults.registry` is empty. | Re-run step 2. |
| `podium sync` reports no artifacts | The artifact directory does not hold `SKILL.md` (for a skill) or `ARTIFACT.md` (for other types) at its immediate root. Intermediate directories without manifest files are domain nodes rather than artifacts. | Move the manifest to the root of the artifact directory. The directory path beneath `~/podium-artifacts/` is the canonical artifact ID. |
| Claude Code does not see the skill | `.claude/skills/greet/SKILL.md` is missing, or Claude Code has not re-read its directory. | Confirm the file exists, then restart Claude Code. |
| The skill is found but not used | Claude Code reads the `description:` field to decide whether the skill matches the prompt, and a vague description does not match. | Make the description specific. The [authoring guide](../authoring/) covers description quality. |
