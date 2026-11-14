---
title: Your first agent
nav_order: 3
description: "Build a minimal agent that runs end to end in Claude Code, then add a runtime requirement, a bundled script, and a delegated artifact."
---

# Your first agent

This tutorial builds a minimal agent that materializes and runs in Claude Code, then extends it step by step. The later steps declare a runtime requirement, ship a bundled script, and delegate to another artifact. Each later step is self-contained, so stop after step 2 when the minimal agent is enough.

An `agent` is a complete agent definition: prose instructions plus optional bundled resources and dependency edges to other artifacts. A skill is context an agent reaches for. An `agent` artifact is its own runnable unit, and harnesses that spawn subagents materialize it.

## Before you start

- Complete the [quickstart](../getting-started/quickstart). It installs `podium`, creates the `~/podium-artifacts/` catalog, and sets Claude Code as the default harness.
- Have `git` on your PATH and a project with staged changes to try the agent on.
- Set `$EDITOR`, or substitute an editor in the commands below.

---

## Step 1: Write a minimal agent

The example is a `commit-message-writer` agent. Given the currently staged diff, it drafts a conventional-commit message. The minimal version is one file of prose instructions, with no scripts and no dependencies.

An agent has no `SKILL.md`. Its prose body lives directly in `ARTIFACT.md`. Make the directory and open the manifest:

```bash
mkdir -p ~/podium-artifacts/personal/dev-loop/commit-message-writer
$EDITOR ~/podium-artifacts/personal/dev-loop/commit-message-writer/ARTIFACT.md
```

Write the frontmatter and the instructions:

```markdown
---
type: agent
name: commit-message-writer
version: 1.0.0
description: Draft a conventional-commit message from the currently staged diff.
when_to_use:
  - "Right before committing, when the staged diff needs a tight message."
tags: [git, commit, dev-loop]
sensitivity: low
---

You write conventional-commit messages.

Read the staged diff using your shell tools. Identify the dominant change type (`feat`, `fix`, `refactor`, `docs`, `test`, `chore`) and the affected area or module.

Output one commit message in the form:

    <type>(<scope>): <summary>

    <wrapped body if more than a one-liner is warranted>

Constraints:

- Subject line is at most 72 characters.
- Use imperative mood ("add", "fix", "refactor").
- Body wraps at 72 characters and describes the motivation for the change.
- Omit co-author tags unless the diff clearly indicates pair work.
```

This single file is the complete minimal agent.

---

## Step 2: Materialize and run the agent

Sync the project and confirm that the agent file exists:

```bash
cd ~/projects/your-project
podium sync

ls .claude/agents/
# commit-message-writer.md
```

Claude Code's adapter writes agents to `.claude/agents/<name>.md`. [Configure your harness](../consuming/configure-your-harness) lists the destination for every harness.

Delegate to the agent from the main Claude Code session:

```
> Use the commit-message-writer agent on my staged changes.
```

Claude Code spawns `commit-message-writer` as a subagent. The subagent reads the staged diff with its own shell access and returns the draft commit message.

---

## Step 3: Declare a runtime requirement

The agent assumes `git` is on the host's PATH. Declare the requirement so the host can refuse the agent when `git` is missing, instead of failing at execution time.

Add these lines to the frontmatter:

```yaml
runtime_requirements:
  system_packages: [git]
```

Re-run `podium sync`. The requirement ships with the artifact, and consumers treat it as follows:

- A host that advertises its runtime capabilities to the Podium MCP server refuses a `load_artifact` it cannot satisfy, with `materialize.runtime_unavailable` naming the missing package.
- A host that advertises no capabilities and does not opt into enforcement receives the requirement and proceeds.

---

## Step 4: Ship a helper script

The agent runs the same shell snippet every time it reads the staged diff. Move the snippet into a bundled script the agent invokes.

Create the script:

```bash
mkdir -p ~/podium-artifacts/personal/dev-loop/commit-message-writer/scripts
cat > ~/podium-artifacts/personal/dev-loop/commit-message-writer/scripts/staged-diff.sh <<'EOF'
#!/usr/bin/env bash
# Print the staged diff, ignoring whitespace and lock-file noise.
git diff --cached --ignore-all-space -- ':!**/*.lock' ':!**/package-lock.json'
EOF
chmod +x ~/podium-artifacts/personal/dev-loop/commit-message-writer/scripts/staged-diff.sh
```

Reference the script from the prose body by its package-relative path:

```markdown
Read the staged diff by running [scripts/staged-diff.sh](scripts/staged-diff.sh).
```

Lint resolves markdown links in the body against the artifact's bundled files at ingest, so a link to a path the package does not contain fails the check. A path written as inline code is not checked.

The script materializes into the harness's bundled-resource location rather than next to the agent file. For Claude Code, the path is `.claude/podium/personal/dev-loop/commit-message-writer/scripts/staged-diff.sh`.

Materialization writes every bundled file with mode 0644, so the executable bit set on the registry copy does not carry over. The agent therefore runs the script through an interpreter: `bash .claude/podium/personal/dev-loop/commit-message-writer/scripts/staged-diff.sh`. [Bundled resources](bundled-resources) covers the file-layout conventions.

---

## Step 5: Delegate to another artifact

This step assumes a `conventional-commits` reference skill that describes the type vocabulary, scope conventions, and house style. [Your first skill](your-first-skill) shows how to write one. Point the agent at it, so the agent consults the reference at runtime instead of restating the conventions inline:

```yaml
delegates_to:
  - personal/dev-loop/conventional-commits@1.x
```

`delegates_to` is advisory:

- The target may be any artifact type, and ingest does not check that the reference resolves.
- The registry records the dependency edge, so impact analysis flags this agent when the reference is updated.
- The host decides whether to follow the reference, and Podium resolves nothing automatically at load time.
- A host that follows the reference loads the target with the same `load_artifact` call it uses for a top-level load.

---

## The finished agent

After all the steps, `ARTIFACT.md` reads as follows. The version is bumped to `1.1.0`, because an ingested version is immutable.

```markdown
---
type: agent
name: commit-message-writer
version: 1.1.0
description: Draft a conventional-commit message from the currently staged diff.
when_to_use:
  - "Right before committing, when the staged diff needs a tight message."
tags: [git, commit, dev-loop]
sensitivity: low
runtime_requirements:
  system_packages: [git]
delegates_to:
  - personal/dev-loop/conventional-commits@1.x
---

You write conventional-commit messages.

Read the staged diff by running [scripts/staged-diff.sh](scripts/staged-diff.sh). Identify the dominant change type and the affected area or module. Consult the `conventional-commits` reference for type meanings and scope conventions.

Output one commit message in the form:

    <type>(<scope>): <summary>

    <wrapped body if more than a one-liner is warranted>

Constraints:

- Subject line is at most 72 characters.
- Use imperative mood.
- Body wraps at 72 characters and describes the motivation for the change.
- Omit co-author tags unless the diff clearly indicates pair work.
```

The artifact directory:

```
~/podium-artifacts/personal/dev-loop/commit-message-writer/
├── ARTIFACT.md
└── scripts/
    └── staged-diff.sh
```

---

## Next steps

- **Register an MCP server.** When the agent talks to a service such as a ticket system or a CI dashboard, declare an `mcpServers:` entry that points at an `mcp-server` artifact. See the `mcp-server` and `agent` sections in [Artifact types](artifact-types).
- **Constrain execution with a sandbox profile.** Set `sandbox_profile: read-only-fs` or stricter, so hosts that honor profiles refuse to widen the agent's filesystem access. See [Bundled resources](bundled-resources#execution-model).
- **Cap the reasoning budget.** When the agent does not need extended thinking, set `effort_hint: low` so consumers can route accordingly. See [Hints](hints).
- **Learn how consumers read the response.** [Handling artifact responses](../consuming/handling-artifact-responses) covers how SDK callers and harness adapters interpret each frontmatter field.
