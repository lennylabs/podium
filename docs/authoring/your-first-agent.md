---
layout: default
title: Your first agent
parent: Authoring
nav_order: 3
description: "Start with a minimal agent that runs end to end against Claude Code, then add a runtime requirement, a bundled script, and a delegated artifact."
---

# Your first agent

An `agent` is a complete agent definition: prose instructions plus optional bundled resources and dependency edges to other artifacts. Where a skill is a piece of context an agent reaches for, an `agent` artifact is its own runnable unit, materialized by harnesses that spawn subagents.

This page starts with a minimal agent that materializes and runs against Claude Code. Subsequent sections layer on common author concerns: declaring a runtime requirement, shipping a bundled script, and delegating to another artifact. Each section is self-contained; stop at the minimal version when that is enough.

---

## Part 1: a minimal agent end to end

The example is a `commit-message-writer` agent: given the currently staged diff, draft a conventional-commit message. The minimal version is one file with prose instructions, no scripts, no dependencies.

Make the directory and write `ARTIFACT.md`. Agents do not use a `SKILL.md`; the prose body lives directly in `ARTIFACT.md`.

```bash
mkdir -p ~/podium-artifacts/personal/dev-loop/commit-message-writer
$EDITOR ~/podium-artifacts/personal/dev-loop/commit-message-writer/ARTIFACT.md
```

```yaml
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

That is the whole agent. One file. Materialize and verify:

```bash
cd ~/projects/your-project
podium sync

ls .claude/agents/
# commit-message-writer.md
```

Claude Code's adapter writes agents to `.claude/agents/<name>.md`. The full per-harness destination table is in [Configure your harness](../consuming/configure-your-harness).

Use the agent by delegating to it from the main session:

```
> Use the commit-message-writer agent on my staged changes.
```

Claude Code spawns `commit-message-writer` as a subagent, the subagent reads the staged diff with its own shell access, and the draft commit message comes back.

---

## Part 2: declare a runtime requirement

The agent assumes `git` is on the host's PATH. Declare the requirement so the host can refuse to materialize when `git` is missing instead of failing at execution time:

```yaml
runtime_requirements:
  system_packages: [git]
```

Add the line to the frontmatter. The next `podium sync` re-materializes; on a host without `git`, the registry surfaces `materialize.runtime_unavailable` and the consumer reports which package was missing.

---

## Part 3: ship a helper script

Reading the staged diff is the same shell snippet every time the agent runs. Move it into a bundled script the agent can invoke verbatim. The script lands alongside the materialized agent and is referenced by path from the prose body.

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

Reference it from the prose body:

```markdown
Read the staged diff by running `scripts/staged-diff.sh`.
```

Lint resolves the prose reference against the artifact's bundled files at ingest. Broken paths fail the lint check.

The script materializes under `.claude/podium/personal/dev-loop/commit-message-writer/scripts/staged-diff.sh` and the agent invokes it from there. See [Bundled resources](bundled-resources) for the file-layout conventions.

---

## Part 4: delegate to another artifact

Suppose you already wrote a `conventional-commits` reference skill that describes the type vocabulary, scope conventions, and house style (see [Your first skill](your-first-skill) for how to write a skill). Point the agent at it so the agent consults the reference at runtime instead of restating the conventions inline:

```yaml
delegates_to:
  - personal/dev-loop/conventional-commits@1.x
```

The registry validates that the target exists, that the version range resolves, and that the type combination is sensible at ingest. The dependency graph picks up the edge; impact analysis flags this agent when the reference is updated. At runtime the harness fetches the delegated artifact through the same `load_artifact` path the host uses for top-level loads.

---

## The full agent

```yaml
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

Read the staged diff by running `scripts/staged-diff.sh`. Identify the dominant change type and the affected area or module. Consult the `conventional-commits` reference for type meanings and scope conventions.

Output one commit message in the form:

    <type>(<scope>): <summary>

    <wrapped body if more than a one-liner is warranted>

Constraints:

- Subject line is at most 72 characters.
- Use imperative mood.
- Body wraps at 72 characters and describes the motivation for the change.
- Omit co-author tags unless the diff clearly indicates pair work.
```

Directory:

```
~/podium-artifacts/personal/dev-loop/commit-message-writer/
├── ARTIFACT.md
└── scripts/
    └── staged-diff.sh
```

---

## What's next

- **Register an MCP server.** When the agent needs to talk to a service such as a ticket system or a CI dashboard, declare an `mcpServers:` entry pointing at an `mcp-server`-type artifact. See the `mcp-server` and `agent` sections in [Artifact types](artifact-types).
- **Constrain execution with a sandbox profile.** Set `sandbox_profile: read-only-fs` or stricter so hosts that honor profiles refuse to widen the agent's filesystem access. See [Bundled resources](bundled-resources).
- **Cap reasoning budget.** When the agent does not need extended thinking, set `effort_hint: low` so consumers route accordingly. See [Hints](hints).
- **Inherit a base agent.** When two agents share most of their structure, `extends:` lets the second refine the first instead of duplicating it. See [Extends](extends).
- **Understand what the consumer does with the response.** [Handling artifact responses](../consuming/handling-artifact-responses) covers how SDK callers and harness adapters interpret each frontmatter field.
