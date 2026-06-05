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

## Tier C — per-type agent behavior (opt-in, needs network + auth)

`TestHarnessArtifactTypes` materializes each artifact type through the real
`podium sync` and drives the real harness agent to confirm it loads and applies
the artifact. Each type carries a **unique marker**; a `rule` / `skill` /
`command` is verified by the marker in the agent's reply, a `hook` by the marker
in the side-effect file its command writes. A match can only come from Podium's
output, so it is a true end-to-end check per type.

Double-gated: the `harness_integration` build tag, `PODIUM_HARNESS_AGENT=1`, and
the harness being authenticated (an API-key env var, or a stored CLI login the
driver detects, e.g. `cursor-agent login`, `codex login`, `claude` login). It
runs with the real environment so the stored login in `$HOME` works; the synced
project supplies the artifact via the working directory.

```bash
PODIUM_HARNESS_AGENT=1 \
  go test -tags harness_integration ./test/harness_integration/ -run TestHarnessArtifactTypes -v -timeout 900s
```

Every `(harness, type)` is a subtest: it runs and asserts where the type is
supported, or skips with a reason otherwise. It is tolerant and may be flaky;
real agents need network and are nondeterministic.

## Targeted harness CLI versions

The goldens in `test/materialization/testdata/golden/*.golden` and the per-type
checks here assume the harness-native config formats produced by the CLI
versions below. These are the versions the suite was last verified against. They
are the reference point for drift detection: each Tier A run logs the installed
harness `--version`, and a logged version that differs from the value here means
the harness may have changed its native format and the goldens and findings need
re-validation.

| Harness | Targeted version | Probed with | Recorded by |
|---|---|---|---|
| claude-code | 2.1.160 | `claude --version` | this table |
| cursor-agent | 2026.06.02 (logged in) | `cursor-agent --version` | this table |
| codex-cli | 0.136.0 (logged in) | `codex --version` | this table |
| Gemini CLI | 0.44.1 (logged in) | `gemini --version` | this table |

OpenCode, claude-desktop, claude-cowork, pi, and hermes have no recorded version
because they expose no probe or ship no CLI on the verified machine.

The suite does not assert these versions automatically. `integration_test.go`
logs `--version` (`t.Logf`) but pins no constant and gates on no minimum, so a
harness that ships a new native format does not fail any check. The golden suite
in `test/materialization` runs the in-process adapters and never drives the real
CLI. Drift is detectable only by running the build-tagged suite by hand and
comparing the logged versions against this table. Keep this table and the inline version
references in the findings below in sync on every manual run.

## Coverage and verification status

Verified end-to-end against the versions in the table above. ✅ = the real
harness consumed Podium's materialized artifact; ✗ = skipped with the noted
reason.

| Type | claude-code | cursor | codex | gemini | How it is checked |
|---|---|---|---|---|---|
| `mcp-server` | ✅ | approval-gated | ✅ | ✅ | Tier A: `<harness> mcp list` reads back the synced server |
| `rule` | ✅ | ✅ | ✅ | ✅ | agent emits the always-rule marker |
| `skill` | ✅ | ✅ | ✅ | ✅ | agent loads SKILL.md on a triggering prompt |
| `command` | ✅ | ✅ | ✗ (by design) | ✅ | agent runs the slash command (`/ping`) |
| `hook` | ✅ | ✗ (headless runtime) | ✗ (exec runtime) | ✗ (headless runtime) | the stop hook's command writes a marker file |
| `agent` (subagent) | not covered | not covered | not covered | not covered | delegation is model-dependent; no deterministic marker |
| `context` | n/a | n/a | n/a | n/a | harness-neutral `.podium/context/`; no harness loads it natively |

Gemini needs `--skip-trust` for the agent runs (it gates project config, skills,
and hooks behind folder trust).

Findings worth noting:

- **mcp-server**: `cursor-agent mcp list` reflects only *approved* servers (it has
  `mcp login`/`disable` approval commands), not the raw `.cursor/mcp.json`, so
  Tier A records the cursor version and skips. Codex reads MCP config from
  `CODEX_HOME/config.toml`, not the project `.codex/config.toml`, so the probe
  points `CODEX_HOME` at the materialized `.codex` dir. Gemini reads the project
  `.gemini/settings.json` and lists the server. Gemini's `mcpServers` schema is
  strict, so the ownership of a map entry is recorded in the top-level `x-podium`
  index rather than an in-entry key (see the reconciliation note below).
- **hook**: Claude Code fires the materialized `.claude/settings.json` stop hook
  in `-p` mode. For cursor, codex, and gemini, the harness does not consume the
  materialized hook in its non-interactive path:
  - **cursor**: Podium writes `.cursor/hooks.json`, which matches cursor-agent's
    own `projectConfigPath` and recognized `stop` event. `cursor-agent --print`
    does not run the stop lifecycle hook in headless mode (re-tested with an
    absolute-path marker to rule out a working-directory artifact).
  - **codex**: codex reads hooks from a `hooks` table in `.codex/config.toml`
    (a `HooksToml` struct), not from a standalone JSON file. Podium previously
    wrote a `.codex/hooks.json` that codex never read; it now merges
    `[[hooks.<Event>]]` into `.codex/config.toml`, verified accepted by
    `codex --strict-config`. `codex exec` still does not fire config.toml
    lifecycle hooks in codex-cli 0.136.0 (confirmed: not even `SessionStart`
    fires under `--dangerously-bypass-hook-trust`), so the materialized hook is
    likely consumed only by the interactive TUI.
  - **gemini**: gemini has no `Stop` event; the agent-finished lifecycle point is
    `AfterAgent` (confirmed by `gemini hooks migrate --from-claude`), so Podium
    maps `stop -> AfterAgent`. The `.gemini/settings.json` hook materializes
    correctly (gemini tolerates the in-entry `x-podium-id` on hook arrays), but
    `gemini -p` does not fire the `AfterAgent` lifecycle hook in headless mode.
- **command**: `command` is `✗` for codex (§6.7.1), folded into skills. Gemini
  runs custom `.gemini/commands/<n>.toml` slash commands in headless `-p` mode.
- **config-merge reconciliation marker**: gemini's `.gemini/settings.json`
  `mcpServers` schema is strict and rejects unknown keys *inside* an entry, so
  Podium's `x-podium-id` tag previously made the config read as invalid.
  Ownership of a keyed-map entry (an `mcpServers`/`mcp` server) now lives in a
  top-level `x-podium` index (`artifact-id -> [container, key]`) that every
  harness loader tolerates, while array entries (a hook event's handler list, the
  Cowork plugin list) keep the in-entry `x-podium-id` tag (arrays have no stable
  key, and the harnesses tolerate an extra key in array elements). This fixed
  gemini `mcp-server`; gemini `hook` materializes correctly and is skipped only on
  the headless-runtime limitation.

### Tier A harness commands

| Harness | Tier A probe | Status |
|---|---|---|
| claude-code | `claude mcp list` | verified |
| codex | `codex mcp list` (with `CODEX_HOME` at the synced `.codex`) | verified |
| gemini | `gemini mcp list` (reads the project settings) | verified |
| opencode | (none yet) | confirm whether a non-interactive MCP-list exists |
| cursor | (approval-gated) | records version, skips |
| claude-desktop / claude-cowork / pi / hermes | — | skipped (no project surface / no CLI) |

## Manual cadence and drift detection

This suite needs the proprietary harness CLIs and cannot run in standard CI: the
`harness_integration` build tag excludes it from `go test ./...`, and Tier C also
needs an authenticated harness and network access. Drift detection is therefore a
recorded manual act on a machine with the CLIs installed. No automated gate runs
it. The golden suite in `test/materialization` runs on the PR lane and catches
Podium-side format regressions. This suite catches harness-side format drift, and
catches it only when a maintainer runs it by hand.

Run Tier A whenever an adapter's native output changes or a targeted harness
ships a new CLI version. It is deterministic and needs no API key. Anything not
on `PATH` skips with a reason, so it is safe to run with whatever subset of CLIs
is installed.

```bash
go test -tags harness_integration ./test/harness_integration/ -v
```

Run Tier C before a release that changes adapter materialization, and on a
periodic cadence (for example monthly, or when a targeted CLI bumps a minor
version) to refresh the verification matrix. It is network-dependent and flaky.

```bash
PODIUM_HARNESS_AGENT=1 \
  go test -tags harness_integration ./test/harness_integration/ -run TestHarnessArtifactTypes -v -timeout 900s
```

On each run, read the logged `--version` lines and update the targeted-versions
table and the inline version references in the findings together. Note any
harness whose native format changed (the kind of finding recorded above). This is
the act that keeps drift detectable; without a scheduled cadence the recorded
versions silently age.
