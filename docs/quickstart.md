# Quickstart — your first skill in 5 minutes

This walks you through installing Podium, authoring a skill, and loading it from Claude Code. Standalone mode: no Postgres, no S3, no IdP, no auth. Single binary. Works offline.

## What you'll need

- A terminal.
- Claude Code installed.

## 1. Install the binary

Install `podium` via your package manager or download the latest release binary from the project's releases page. Verify:

```bash
podium --version
```

## 2. Start the server

Standalone mode, zero config:

```bash
podium serve
```

You'll see something like:

```
No config found at ~/.podium/registry.yaml — starting in standalone mode at http://127.0.0.1:8080.
Run `podium serve --strict` to require explicit setup.
Created ~/podium-artifacts/ for the default layer.
Listening on 127.0.0.1:8080.
```

Leave it running. Open a second terminal.

## 3. Author a skill

A skill is a directory with an `ARTIFACT.md` file at its root. Drop one into `~/podium-artifacts/`:

```bash
mkdir -p ~/podium-artifacts/hello/greet
cat > ~/podium-artifacts/hello/greet/ARTIFACT.md <<'EOF'
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
today's date in a friendly format. Be concise — one or two sentences.
EOF
```

Find the layer id and tell Podium to ingest:

```bash
podium layer list
podium layer reingest <layer-id>
```

You should see `New artifacts: 1 (hello/greet@1.0.0)`.

## 4. Wire Claude Code to Podium

Add Podium's MCP server to `~/.claude/mcp.json` (create the file if it doesn't exist):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY_ENDPOINT": "http://127.0.0.1:8080",
        "PODIUM_HARNESS": "claude-code"
      }
    }
  }
}
```

Restart Claude Code.

## 5. Use the skill

Open Claude Code in any project. Type:

```
hello!
```

Claude will:

1. Call `search_artifacts` looking for greeting-related skills.
2. Find `hello/greet`.
3. Call `load_artifact` to get the prose body and any bundled resources.
4. Greet you by name with today's date.

That's the loop. You authored once, the registry served it lazily, and the agent loaded it on demand.

## Where to go from here

- Add more skills, agents, contexts, or prompts. The frontmatter `type:` field decides what Podium treats them as.
- Browse what you've authored: `podium serve --web-ui` exposes a SPA at `http://127.0.0.1:8080/ui` — see the [Web UI section of §13.10](../spec/spec.md#1310-standalone-deployment) of the spec.
- When you outgrow standalone — multiple users, governance needs, multiple harnesses — read the [team rollout guide](team-rollout.md).
- The [spec](../spec/spec.md) is the full reference.

## Troubleshooting

- **"No config found" repeats every restart.** That's expected on first run. Subsequent runs find `~/.podium/registry.yaml` and start without the message.
- **Claude Code doesn't see the MCP server.** Confirm `podium-mcp` is on your `PATH` (it's a separate binary from `podium`), restart Claude Code, and check Claude Code's MCP logs for the connection error.
- **`podium layer list` says no layers.** The default layer is created on the registry's first run — make sure step 2 actually started successfully (look for the "Listening on 127.0.0.1:8080" line).
- **Skill works in `podium search` but Claude doesn't load it.** Check that `description:` is specific enough for `search_artifacts` to surface it under the prompts you're typing. Vague descriptions don't get loaded — see §3.3 of the spec on description quality.
