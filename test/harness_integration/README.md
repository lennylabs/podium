# Real-harness integration tests

This package drives the **real agent-harness binaries** (Claude Code, Codex,
Gemini, OpenCode, …) against a project materialized by the real `podium sync`,
to confirm a harness actually accepts and discovers Podium's output. It realizes
the §6.7 conformance line's opt-in end-to-end check.

It is excluded from normal builds by the `harness_integration` build tag, so
`go test ./...` and CI never compile or run it. Run it explicitly, on a machine
with the harness CLIs installed:

```bash
go test -tags harness_integration ./test/harness_integration/ -v
```

Anything whose binary is not on `PATH` is skipped with a reason, so the suite is
safe to run anywhere; it exercises whatever harnesses happen to be installed.

## Tier A — config accept (default, deterministic, no API key)

`TestHarnessConfigAccept` materializes an `mcp-server` artifact for each harness
and runs the harness's own non-interactive MCP-config command over the synced
project, asserting the harness reads back the server Podium wrote (`warehouse`).
`HOME` and the harness-specific config directories are redirected to empty temp
dirs, so the only source of the server is the materialized project, not the
developer's global config. The harness `--version` is logged so the format is
recorded against a concrete version.

A harness that exposes no non-interactive config command (an IDE or a web
product) is listed and skipped with a reason rather than silently omitted.

## Tier C — agent smoke (opt-in, needs network + auth)

`TestHarnessAgentSmoke` runs one real headless agent turn against a project
carrying an always-loaded rule that instructs the agent to emit a marker
(`ZEBRA-7421`), and asserts the marker appears — a true end-to-end check that the
harness loaded and applied Podium's materialized rule. It is double-gated: the
`harness_integration` build tag, `PODIUM_HARNESS_AGENT=1`, and the harness being
authenticated (an API-key env var, or a stored CLI login the driver detects).
It runs with the real environment (so the stored login in `$HOME` works); the
unique marker makes a false positive from global config effectively impossible.
It is tolerant and may be flaky; real agents need network and are
nondeterministic.

```bash
# stored CLI logins (e.g. `cursor-agent login`) are detected automatically;
# API keys are an alternative for the harnesses that read them.
PODIUM_HARNESS_AGENT=1 \
  go test -tags harness_integration ./test/harness_integration/ -run TestHarnessAgentSmoke -v
```

Verified here: **cursor** (cursor-agent 2026.06.02, logged in) loads the
materialized `.cursor/rules/secret.mdc` and returns the marker.

## Coverage and verification status

| Harness | Tier A probe | Tier C exec | Notes |
|---|---|---|---|
| claude-code | `claude mcp list` | `claude -p` | **Verified** here against Claude Code 2.1.160 (Tier A passes: the synced `.mcp.json` is listed). |
| codex | `codex mcp list` | `codex exec` | Candidate command; verify against the installed `codex --help`. |
| gemini | `gemini mcp list` | `gemini -p` | Candidate command; verify against the installed `gemini --help`. |
| opencode | (none yet) | `opencode run` | Confirm whether OpenCode exposes a non-interactive MCP-list command; until then the config probe skips. |
| cursor | (approval-gated) | `cursor-agent --print --force` | `cursor-agent mcp list` reflects only *approved* servers (it has `mcp login`/`disable` approval commands), not the raw `.cursor/mcp.json`, so Tier A records the version and skips. **Verified end-to-end via Tier C** against `cursor-agent` 2026.06.02: with the CLI logged in, the agent loaded the materialized `.cursor/rules/secret.mdc` and returned the marker. Needs `--force` (accepts the workspace-trust prompt) and either a stored `cursor-agent login` or `CURSOR_API_KEY`. |
| claude-desktop | — | — | No project-level surface. Skipped. |
| claude-cowork | — | — | Web/marketplace; no local binary. Skipped. |
| pi | — | — | No MCP surface. Skipped. |
| hermes | — | — | No non-interactive config command. Skipped. |

The `driver` table in `integration_test.go` holds these commands. The candidate
rows (`codex`, `gemini`, `opencode`) were authored from each tool's documented
CLI and must be confirmed against the installed binary the first time the suite
runs where that binary is present; adjust the subcommand or demote `mcpProbe` to
`nil` (skip) if the command differs. The recorded `--version` makes drift easy to
spot.
