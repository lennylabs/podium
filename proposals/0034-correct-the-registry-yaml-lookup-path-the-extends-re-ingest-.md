# Proposal 0034: Correct the registry.yaml lookup path, the extends: re-ingest statement, and list the runtime-capability variables

- Issue: (to be filed)
- Status: Approved (2026-10-02). Signed off as staged. OQ-1 resolved: the relayed D5, C30, and C23 decisions belong to proposals 0038, 0035, and 0036 and do not apply here.
- Date: 2026-10-02

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §13.12: the spec states the `registry.yaml` lookup the code performs. The registry process reads the file `PODIUM_CONFIG_FILE` names, reads `~/.podium/registry.yaml` when the variable is unset, and reads no other path. `podium serve --config <path>` sets the variable for the process. `/etc/podium/registry.yaml` leaves the intro paragraph and the sample-file header (SPEC-1).
- §4.6 and §4.7.6: the spec states that a stored child version keeps its `extends:` pin, that a re-ingest of the child's unchanged bytes is idempotent, and that a child picks up a newer parent when it is published at a new `version:` (SPEC-2). The pin comment in `pkg/registry/ingest/ingest.go` says the same (CODE-1).
- §6.2 and §4.4.1: the §6.2 configuration table lists the runtime-capability variables `podium-mcp` reads (`PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE`, `PODIUM_HOST_PACKAGES`, `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`, and `PODIUM_IGNORE_RUNTIME_REQUIREMENTS`) with their parsing, precedence, and gate semantics, and §4.4.1 points at them (SPEC-3).
- Tests: an ingest unit test pins the idempotent re-ingest after a newer live parent lands (TEST-1), a stale e2e skip reason is corrected (TEST-2), unit, in-process, and e2e tests pin the runtime variables (TEST-3), and unit and in-process tests pin the `registry.yaml` lookup order and the refusal of a named missing file (TEST-4).
- Docs and changelog: `docs/reference/cli.md` gains the `PODIUM_CONFIG_FILE` row and the runtime-variable rows, `docs/authoring/bundled-resources.md` names the variables and links to the reference, `docs/deployment/vector-backends.md` stops saying that `podium-server` parses no flags (DOC-1), and `CHANGELOG.md` records the corrections under `### Documentation` (CL-1).

**Fixed decisions.**

- D13: `/etc/podium/registry.yaml` is removed from the spec. The lookup is `PODIUM_CONFIG_FILE`, then `~/.podium/registry.yaml`, and `podium serve --config` sets the variable. The code does not change.
- D13: §13.10 is not edited.
- C28: a stored child version keeps its pin and fold, a re-ingest of its unchanged bytes is idempotent, and a new child version resolves the reference again. The behavior does not change.
- C28: `docs/authoring/extends.md` is already correct and is not edited.
- C28: one new ingest unit test covers an unchanged re-ingest after a newer live parent lands, and the existing `TestLifecycle_ExtendsPinStabilityAndReingest` covers the new-version path. No new end-to-end test is added for C28 or for the `--config` override.
- OQ-3: §6.2 lists the runtime variables as environment-only rows. `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` takes precedence over `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` and has no effect while the gate is inactive. Both flags take effect only on the exact value `true`.
- The whitespace behavior of `PODIUM_HOST_PYTHON` and `PODIUM_HOST_NODE` is documented as it exists. No variable is trimmed by this proposal.
- The proposal adds no environment variable, flag, config key, error code, endpoint, or SPI. `materialize.runtime_unavailable` stays the only runtime-gate error.
- The relayed decisions on D5, C30, and C23 name items outside this proposal, which neither applies nor contradicts them. OQ-1 asks where they belong.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.
- **TEST-2's skip string names TEST-1's test.** Land them in one commit (step S5), or the skip string cites a test that does not exist.
- **TEST-1 needs a live newer parent.** The existing `TestExtends_RangeReingestStaysIdempotentAfterParentDeprecation` (`pkg/registry/ingest/extends_conformance_test.go:160`) adds a deprecated 1.0.1, which cannot change the pin. TEST-1 adds a non-deprecated 1.1.0 inside the `1.x` range. A deprecated parent in TEST-1 makes the test pass for the wrong reason.
- **`captureStderr` swaps the process-wide `os.Stderr`.** A test that calls it must not call `t.Parallel`, and must not be a subtest or variant of a test that does, per the comment at `cmd/podium-mcp/config_env_test.go:112-118`. TEST-3 (b) therefore stages the ignore-on-inactive-gate case as its own top-level function. The same holds for `captureStderr` in `cmd/podium/admin_migrate_test.go:29`.
- **Unparseable frontmatter never reaches the runtime gate.** `deliverLoadArtifact` runs the sandbox gate first (`cmd/podium-mcp/main.go:1733-1742`), so a client receives `materialize.sandbox_unsupported` for it. Do not document or test `materialize.runtime_unavailable` as the client-visible code for that case. The TEST-3 (b) malformed-frontmatter test pins only `enforceRuntimePolicy`'s own branch.
- **The existing `TestServeCmd_StandaloneFlagsSetEnv` sets `PODIUM_CONFIG_FILE` to a missing path and expects exit 1.** TEST-4 (b) also expects exit 1, so exit status alone cannot distinguish the flag override from the missing-file refusal. TEST-4 (b) asserts the variable's value and the absence of the `does not exist` message, and TEST-4 (c) pins the refusal itself by calling `loadBootConfig` directly.
- **Parts of `test/e2e` skip silently on macOS.** Confirm that the TEST-3 (d) subtests run in the Linux lane before counting them as coverage.
- **Do not trim `PODIUM_HOST_PYTHON` or `PODIUM_HOST_NODE` in `loadConfig`.** A whitespace-only value activates the gate and then fails every requirement for that runtime. SPEC-3 documents that, and trimming would be a behavior change outside this proposal.
- **The sandbox variables sit beside the runtime variables** at `cmd/podium-mcp/main.go:284-286`. They have the same documentation gap and are a non-goal.
- **`CHANGELOG.md:826` (0.4.0) says "a re-ingest re-pins onto the live version".** It is a historical entry and stays. CL-1 states that the C28 change is a spec correction with no behavior change.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §13.12 states the `PODIUM_CONFIG_FILE` lookup, the home fallback, and the `--config` override, and drops `/etc/podium/registry.yaml` from the intro and the sample header.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §4.6 and §4.7.6 state that a stored child version keeps its pin and that a new child version picks up a newer parent.
      Levels: —. Depends on: —
- [ ] **S3 · spec** — SPEC-3. §6.2 gains the runtime-variable rows, and §4.4.1 points at them.
      Levels: —. Depends on: —
- [ ] **S4 · code** — CODE-1. The `extends:` pin comment in `pkg/registry/ingest/ingest.go` matches the corrected §4.7.6.
      Levels: unit. Depends on: S2
- [ ] **S5 · test** — TEST-1, TEST-2. The ingest unit test for an idempotent re-ingest after a newer live parent, and the corrected e2e skip reason that cites it. Bundled because the skip string names the TEST-1 test.
      Levels: unit, e2e. Depends on: S2
- [ ] **S6 · test** — TEST-3. Unit, in-process, and e2e tests for the runtime variables.
      Levels: unit, integration, e2e. Depends on: S3
- [ ] **S7 · test** — TEST-4. Unit and in-process tests for the `registry.yaml` lookup order and the missing-file refusal.
      Levels: unit, integration. Depends on: S1
- [ ] **S8 · docs** — DOC-1. `docs/reference/cli.md` and `docs/authoring/bundled-resources.md` list the variables, and `docs/deployment/vector-backends.md` says that `podium-server` has no `--config` option.
      Levels: —. Depends on: S1, S3
- [ ] **S9 · docs** — CL-1. `## [Unreleased]` `### Documentation` entries for the corrections.
      Levels: —. Depends on: S1, S2, S3, S8

**Ordering constraints.** The spec steps are independent of one another. Each test step cites the spec text its spec step lands, so it follows that step. DOC-1 restates SPEC-1 and SPEC-3, and CL-1 summarizes all three spec steps and the DOC-1 reference rows.

## Current state and the gap

This proposal bundles three spec and documentation corrections that the user decided on 2026-10-02. None changes product behavior.

### D13: the registry.yaml lookup

§13.12 opens with "configured in `registry.yaml` (default `/etc/podium/registry.yaml` for standard deployments and `~/.podium/registry.yaml` for standalone; override via `--config <path>`)", and the sample file in §13.12 "Config file format" opens with the comment `# /etc/podium/registry.yaml (or ~/.podium/registry.yaml in standalone)`. No other passage in `spec/` names `/etc/podium`, and no code reads it.

- `internal/serverboot/yaml_config.go:286-293` (`readYAMLConfig`) reads `PODIUM_CONFIG_FILE` and falls back to `$HOME/.podium/registry.yaml`.
- `cmd/podium/serve.go:29` defines `--config` as "path to registry.yaml (overrides PODIUM_CONFIG_FILE)". `cmd/podium/serve.go:67-69` copies the flag value into `PODIUM_CONFIG_FILE` with `os.Setenv`, which overwrites an inherited value, so the flag wins over the variable.
- `cmd/podium-server/main.go:24-33` has no `--config` option. Its only argument handling is the `sign-stored-rows` subcommand (`:27-29`), whose `--include-unsigned`, `--dry-run`, and `--plan-digest` flags (`internal/serverboot/signpass.go:121-127`, §13.4) do not touch the config path. The serving path calls `serverboot.Run` (`:31`), which reaches the same `readYAMLConfig`, so the server binary locates `registry.yaml` through `PODIUM_CONFIG_FILE` and the `~/.podium/registry.yaml` fallback only.
- `internal/serverboot/serverboot.go:783-792` (`loadBootConfig`) fails startup when `PODIUM_CONFIG_FILE` names a file that does not exist.

The "standard versus standalone" split is circular, because the config file selects the mode. The §13.10 zero-flag rule checks only `~/.podium/registry.yaml` and already agrees with the code. The spec never names `PODIUM_CONFIG_FILE`. `docs/reference/cli.md:149` documents `--config` as overriding `PODIUM_CONFIG_FILE`, but the environment-variable table in the same page has no `PODIUM_CONFIG_FILE` row.

`docs/deployment/oidc/okta.md:49` already describes the code's lookup. `docs/deployment/vector-backends.md:73` describes the lookup correctly but says "The `podium-server` binary parses no flags", which is false because `podium-server sign-stored-rows` parses `--include-unsigned`, `--dry-run`, and `--plan-digest` (`cmd/podium-server/main.go:27-29`, `internal/serverboot/signpass.go:125-127`), and `docs/deployment/progressive-adoption.md:81` already tells operators to run that command with those flags. DOC-1 (c) corrects the sentence. The remaining `/etc/podium` references outside the spec need no change. Test fixtures use `/etc/podium` as a key-file path (`test/e2e/deployment_compose_test.go:216-222`, `internal/serverboot/identity_verify_test.go:353`, and `pkg/sign/keyfile_test.go:36`). `CHANGELOG.md:856` is a historical note. The Dockerfile (lines 64-71), the Helm chart (`deploy/helm/podium/values.yaml:105-108` tells the operator to mount a `registry.yaml` and point `PODIUM_CONFIG_FILE` at it, and the chart does not set the variable), and `docker-compose.yml` name no `/etc/podium` config path.

### C28: extends: re-ingest

§4.6 says "Parent version is resolved at the child's ingest time and pinned (parent updates do not silently propagate; the child must be re-ingested to pick up changes)." §4.7.6 "Inheritance and re-ingest" says "the child must be re-ingested (typically by bumping its `version:` and merging) to pick up changes", which implies that a same-version re-ingest could also pick them up. The comment at `pkg/registry/ingest/ingest.go:649-652` says "only re-ingesting the child does".

The code does not re-pin a stored version. Ingest recomputes the pin (`resolveExtendsPin`, `pkg/registry/ingest/ingest.go:654`, stored on `mr.ExtendsPin` at `:688`) and the fold (`foldExtendsParent`, `:718`) on every pass. The check at `pkg/registry/ingest/ingest.go:759-763` then compares the stored `ContentHash` with the new one. `ContentHash` covers only the child's authored `ARTIFACT.md`, `SKILL.md`, and resource bytes (`pkg/registry/ingest/ingest.go:1193`, `:1215`, and `:1291-1298`, and `pkg/version/version.go:291-305`), never the pin, and the fold leaves it untouched (`pkg/registry/ingest/ingest.go:1403-1411`). An unchanged child is counted `Idempotent` before the write, and the recomputed pin and fold are discarded. Changed bytes at the same version produce a conflict report (`pkg/registry/ingest/ingest.go:765-771`), as the §4.7 version immutability invariant requires. A stored version can still leave the store, through `PurgeDeprecatedManifests` (`pkg/store/memory.go:300`, `pkg/store/sqlite.go:639`, and `pkg/store/postgres.go:1035`) or layer unregistration (§8), and a later ingest of the same `(id, version)` is then a fresh insert that resolves the reference again.

`docs/authoring/extends.md:43` and `:141` already state the corrected behavior, and no other docs page repeats the old claim. Test coverage is partial. `TestLifecycle_ExtendsPinStabilityAndReingest` (`test/e2e/lifecycle_journeys_test.go:61`) pins that an old child version keeps the old fold and that a new child version picks up a newer live parent. No test re-ingests the unchanged child bytes after a newer non-deprecated in-range parent lands. The skip reason of `TestExtends_PinReingestPicksNewerParent` (`test/e2e/artifact_extends_test.go:168-173`) says the harness cannot build a multi-version layer fixture, which the lifecycle test now does.

### OQ-3: the runtime-capability variables

`cmd/podium-mcp/main.go:288-292` reads the runtime variables, and none appears in the §6.2 table or anywhere in `docs/`.

- `PODIUM_HOST_PYTHON` and `PODIUM_HOST_NODE` are read as raw strings without trimming.
- `PODIUM_HOST_PACKAGES` goes through `splitCSV` (`cmd/podium-mcp/main.go:2318-2330`), which trims each entry and drops empty ones.
- `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` and `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` take effect only when the value is exactly `true`.

Proposal 0032 described the gate by mechanism in §4.4.1 and in the §6.9 "Runtime requirement unsatisfiable" row, and its OQ-3 deferred the listing of the variables to a follow-up. `enforceRuntimePolicy` (`cmd/podium-mcp/main.go:2255-2283`) runs in this order:

1. Frontmatter that fails to parse is refused with an error wrapping `materialize.ErrRuntimeUnavailable` (`cmd/podium-mcp/main.go:2256-2260`). No client receives that error today. `enforceRuntimePolicy` has one caller, `deliverLoadArtifact`, which runs `enforceSandboxPolicy` first (`cmd/podium-mcp/main.go:1733-1742`). The sandbox gate parses the same frontmatter, fails closed on a parse error before it reads any variable (`cmd/podium-mcp/main.go:2201-2206`), and the caller prefixes that error with `materialize.sandbox_unsupported: `. A client therefore receives `materialize.sandbox_unsupported` for unparseable frontmatter, and neither `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` nor `PODIUM_IGNORE_SANDBOX` bypasses that refusal.
2. An artifact with no `runtime_requirements` passes.
3. The gate is inactive, and the artifact passes, unless the enforce flag is `true` or a host variable advertises a capability (`runtimeGateActive`, `cmd/podium-mcp/main.go:2288-2293`).
4. While the gate is active, the ignore flag writes a `WARN:` line to standard error that names the artifact and the advertised capabilities, and admits the artifact. The ignore flag therefore takes precedence over the enforce flag, and on its own it has no effect on an unconfigured host.
5. Otherwise `CheckRuntimeRequirements` (`pkg/materialize/atomic.go:47-76`) runs. An unadvertised runtime fails any requirement for it. `satisfiesVersion` (`pkg/materialize/atomic.go:111-125`) removes leading and trailing spaces and tabs from both the host value and the requirement, compares numeric dot parts for a `>=X` requirement, and requires any other requirement string to equal the host value. Package names match exactly and case-sensitively (`containsString`, `pkg/materialize/atomic.go:179-186`).

A whitespace-only `PODIUM_HOST_PYTHON` or `PODIUM_HOST_NODE` activates the gate, passes the untrimmed empty check at `pkg/materialize/atomic.go:59`, and then trims to empty in `satisfiesVersion`, so it fails every requirement for that runtime.

Test coverage has gaps. No test covers `PODIUM_HOST_NODE` at any level (`cmd/podium-mcp/runtime_policy_test.go` and `pkg/materialize/runtime_test.go` have no Node case). No test sets the enforce and ignore flags together, touches the malformed-frontmatter path, or pins the exact-`true` parsing. CSV splitting is covered by `TestSplitCSV` (`cmd/podium-mcp/main_helpers_test.go:32-47`) and by e2e runs that set `PODIUM_HOST_PYTHON` and `PODIUM_HOST_PACKAGES`.

## Decisions

- **D13 lookup.** The spec states the lookup the code performs: `podium serve --config <path>` sets `PODIUM_CONFIG_FILE` for the process and so overrides an inherited value, the registry process reads the file the variable names, it reads `~/.podium/registry.yaml` when the variable is unset, and it reads no other path. The `podium-server` binary has no `--config` option, so it locates `registry.yaml` through `PODIUM_CONFIG_FILE` and the `~/.podium/registry.yaml` fallback only. Evidence: `cmd/podium/serve.go:29` and `:67-69`, `internal/serverboot/yaml_config.go:286-293`, and `cmd/podium-server/main.go:24-33`.
- **D13 scope.** The code does not change. The Helm chart, the Dockerfile, and `docker-compose.yml` need no edit, because none names an `/etc/podium` config path. Test fixtures that use `/etc/podium` as a key-file path and the `CHANGELOG.md:856` note stay.
- **§13.10 stays.** Its zero-flag rule names only `~/.podium/registry.yaml` and is consistent once `/etc/podium` is gone. Its pre-existing phrasing gaps (a set `PODIUM_CONFIG_FILE` suppressing auto-bootstrap whether or not the file exists, and the Postgres skip at `internal/serverboot/standalone_bootstrap.go:18-20`) are out of scope.
- **C28 rule.** A stored child version keeps its pin and fold, and a re-ingest of its unchanged bytes is idempotent. A child picks up a newer parent when it is published at a new `version:`. The statement is scoped to stored versions, because a purged version ingested again resolves its reference again. This follows from the §4.7 version immutability invariant. Both spec passages and the `pkg/registry/ingest/ingest.go:649-652` comment are corrected, and no behavior changes.
- **C28 docs.** `docs/authoring/extends.md:43` and `:141` already state the corrected behavior, so no docs page changes for C28.
- **C28 tests.** One ingest unit test covers the uncovered path (unchanged child re-ingested after a newer live in-range parent). The existing `TestLifecycle_ExtendsPinStabilityAndReingest` covers the new-version path end to end and is reused rather than duplicated.
- **OQ-3 rows.** §6.2 lists the runtime variables with semantics taken from `cmd/podium-mcp/main.go:288-292` and `:2255-2293` and `pkg/materialize/atomic.go:47-186`. Each row is marked environment only, because `podium-mcp` has no flag or config-file form for these variables (`cmd/podium-mcp` reads them only in `loadConfig`). The marking follows the precedent of `PODIUM_SIGNATURE_VERIFY_KEY`, `PODIUM_SIGN_KEY_PATH`, and `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE`, which reconciles the rows with the table header's statement that flag and config-file equivalents are accepted.
- **OQ-3 surface.** The rows document existing surface. The proposal adds no variable, flag, error code, or behavior, and `materialize.runtime_unavailable` (already in §6.9 and §6.10) remains the only error.
- **Whitespace.** The rows state the existing behavior: a whitespace-only `PODIUM_HOST_PYTHON` or `PODIUM_HOST_NODE` activates the gate and fails every requirement for that runtime. Trimming either variable would be a behavior change and is not staged. Stating the behavior in the row closes the question the draft left open.
- **Unrelated decisions in the relayed request.** The relayed user request names D5 (option c), C30 (option c), and C23 (option b). None of those item IDs is among the items this proposal covers (D13, C28, and OQ-3), so this proposal neither applies nor contradicts those decisions. Open question OQ-1 asks where they belong.

## Spec amendment: §13.12 registry.yaml lookup

**SPEC-1.** Two anchors in `spec/13-deployment.md`, §13.12 "Backend Configuration Reference".

(a) The intro paragraph (line 390 at the time of writing). Replace:

> This section covers **server-side** configuration: the registry process's storage backends, vector backend, embedding provider, and identity provider, configured in `registry.yaml` (default `/etc/podium/registry.yaml` for standard deployments and `~/.podium/registry.yaml` for standalone; override via `--config <path>`).

with:

> This section covers **server-side** configuration: the registry process's storage backends, vector backend, embedding provider, and identity provider, configured in `registry.yaml`. The registry process reads the file that `PODIUM_CONFIG_FILE` names, and reads `~/.podium/registry.yaml` when the variable is unset. `podium serve --config <path>` sets `PODIUM_CONFIG_FILE` to `<path>` for the process, so the flag overrides a value inherited from the environment. The `podium-server` binary has no `--config` option, so it locates `registry.yaml` through `PODIUM_CONFIG_FILE` and the `~/.podium/registry.yaml` fallback only. The registry reads no other path. When `PODIUM_CONFIG_FILE` names a file that does not exist, the registry refuses to start (§13.10).

The next paragraph, beginning "For **client-side** configuration", is unchanged.

(b) The sample file under "### Config file format" (line 572 at the time of writing). Replace the first line of the YAML block:

```yaml
# /etc/podium/registry.yaml (or ~/.podium/registry.yaml in standalone)
```

with:

```yaml
# ~/.podium/registry.yaml, or the file PODIUM_CONFIG_FILE names
```

The rest of the block is unchanged. §13.10 is not edited.

## Spec amendment: §4.6 and §4.7.6 extends: re-ingest

**SPEC-2.** Two anchors in `spec/04-artifact-model.md`.

(a) §4.6, the paragraph after the collision bullets that begins "`extends:` is a single scalar" (line 677 at the time of writing). Replace:

> Parent version is resolved at the child's ingest time and pinned (parent updates do not silently propagate; the child must be re-ingested to pick up changes).

with:

> Parent version is resolved at the child's ingest time and pinned. A stored child version keeps its pin when the parent changes (§4.7.6).

The preceding sentences of the paragraph ("`extends:` is a single scalar (no multiple inheritance). Cycle detection at ingest time.") are unchanged.

(b) §4.7.6, the "**Inheritance and re-ingest.**" paragraph (line 889 at the time of writing). Replace:

> Parent updates do not silently propagate; the child must be re-ingested (typically by bumping its `version:` and merging) to pick up changes.

with:

> Parent updates do not propagate to a stored child version. Because a stored `(artifact_id, version)` is immutable (§4.7), a re-ingest of the child's unchanged bytes is idempotent and leaves the stored pin and the folded fields unchanged. To pick up a newer parent, publish the child at a new `version:`. Ingesting that version resolves the reference again against the parent's candidate versions.

The first sentence of the paragraph, "When a child manifest declares `extends: <parent>` (no version pin), the parent version is resolved at the child's ingest time and stored as a hard pin in the ingested manifest's resolved form.", is unchanged.

## Spec amendment: §6.2 and §4.4.1 runtime-capability variables

**SPEC-3.** Two anchors.

(a) `spec/06-mcp-server.md` §6.2 "Configuration" table (lines 21-36 at the time of writing). Insert these rows after the `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` row, before the paragraph "Provider-specific options are passed as additional env vars". Each row has the three columns Parameter, Description, and Default.

| Parameter | Description | Default |
| --- | --- | --- |
| `PODIUM_HOST_PYTHON` | Python version the host advertises to the §4.4.1 runtime gate, for example `3.11.4`. Any non-empty value activates the gate. While the gate is active, the host value and `runtime_requirements.python` are compared after leading and trailing spaces and tabs are removed from both. A requirement of the form `>=X` is satisfied when the host version is at least X by numeric dot-part comparison. Any other requirement string must equal the host value. While the gate is active, an unset value fails any Python requirement. A value that is only spaces and tabs activates the gate and fails any Python requirement. Environment only; no flag or config-file key | (unset → no Python advertised) |
| `PODIUM_HOST_NODE` | Node version the host advertises to the §4.4.1 runtime gate, for example `20.11.1`. Any non-empty value activates the gate. While the gate is active, the host value and `runtime_requirements.node` are compared after leading and trailing spaces and tabs are removed from both. A requirement of the form `>=X` is satisfied when the host version is at least X by numeric dot-part comparison. Any other requirement string must equal the host value. While the gate is active, an unset value fails any Node requirement. A value that is only spaces and tabs activates the gate and fails any Node requirement. Environment only; no flag or config-file key | (unset → no Node advertised) |
| `PODIUM_HOST_PACKAGES` | Comma-separated system packages the host advertises to the §4.4.1 runtime gate, for example `jq,curl`. Surrounding whitespace is removed from each entry and empty entries are dropped. A value that yields at least one entry activates the gate. Each name in `runtime_requirements.system_packages` must equal an advertised entry exactly, including case. Environment only; no flag or config-file key | (unset → no packages advertised) |
| `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` | Opts the host into the §4.4.1 runtime gate without advertising a capability. The gate is active when this variable is `true` or when `PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE`, or `PODIUM_HOST_PACKAGES` advertises a capability. While the gate is active, a runtime the host does not advertise fails any requirement for it, and an unsatisfied requirement refuses the `load_artifact` with `materialize.runtime_unavailable` (§6.9). Only the value `true` takes effect; any other value, including `TRUE` and `1`, leaves the option off. Environment only; no flag or config-file key | (unset → off) |
| `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` | Explicit host override for the §4.4.1 runtime gate. While the gate is active, the MCP server admits an artifact whose `runtime_requirements` the advertised capabilities do not satisfy, and writes a warning to standard error that names the artifact and the advertised capabilities. The override takes precedence over `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`. While the gate is inactive the variable has no effect. Only the value `true` takes effect; any other value leaves the override off. Environment only; no flag or config-file key | (unset → off) |

(b) `spec/04-artifact-model.md` §4.4.1 "Execution Model Contract", the paragraph that begins "Adapters surface these requirements to the host where supported." (line 365 at the time of writing). After the sentence "An explicit host override bypasses the refusal and logs a warning.", insert:

> §6.2 lists the environment variables through which a host advertises its runtime capabilities, opts into enforcement, and sets the override.

The final sentence of the paragraph, "`podium sync` does not evaluate runtime requirements.", is unchanged. The §6.9 "Runtime requirement unsatisfiable" row is not edited.

## Proposed solution

### CODE-1. Correct the extends: pin comment in ingest

`pkg/registry/ingest/ingest.go:649-652`. Replace the comment:

```go
		// §4.7.6 extends:-pin resolution. If the artifact extends a
		// parent reference, resolve the parent against existing
		// manifests and pin to an exact version. Parent updates do
		// not silently propagate; only re-ingesting the child does.
```

with:

```go
		// Spec: §4.7.6 extends:-pin resolution. If the artifact extends a
		// parent reference, resolve the parent against existing manifests and
		// pin to an exact version. The pin and the fold below are recomputed on
		// every pass, but an unchanged re-ingest of a stored version is
		// classified idempotent further down and discards them, because the
		// content hash covers only the child's authored bytes (§4.7). A child
		// picks up a newer parent only at a new version.
```

The edit changes only the comment. Run `gofmt` on the file.

### Test-only changes

TEST-1 through TEST-4 are described under Testing. D13 and OQ-3 change no production code.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| A file exists at `/etc/podium/registry.yaml` and `PODIUM_CONFIG_FILE` is unset | The registry ignores it and reads `~/.podium/registry.yaml`, or bootstraps standalone defaults when that file is missing (accepted) | §13.12 "The registry reads no other path." (SPEC-1), §13.10; `docs/reference/cli.md` `PODIUM_CONFIG_FILE` row (DOC-1) |
| `PODIUM_CONFIG_FILE` names a file that does not exist | Startup fails with `config file "<path>" does not exist` | §13.12 "the registry refuses to start (§13.10)" (SPEC-1); `docs/reference/cli.md` `PODIUM_CONFIG_FILE` row (DOC-1) |
| `podium serve --config A` with an inherited `PODIUM_CONFIG_FILE=B` | The registry reads A | §13.12 (SPEC-1); `docs/reference/cli.md` `--config` and `PODIUM_CONFIG_FILE` rows |
| An operator passes `--config` to `podium-server` | The binary has no `--config` option; the operator sets `PODIUM_CONFIG_FILE` (accepted) | §13.12 "The `podium-server` binary has no `--config` option, so it locates `registry.yaml` through `PODIUM_CONFIG_FILE` and the `~/.podium/registry.yaml` fallback only." (SPEC-1); `docs/reference/cli.md` `PODIUM_CONFIG_FILE` row (DOC-1) |
| `PODIUM_CONFIG_FILE` is set and the home directory cannot be resolved | The variable's file is read; the home fallback is not consulted | §13.12 (SPEC-1) |
| Neither `PODIUM_CONFIG_FILE` nor `~/.podium/registry.yaml` is present | The zero-flag standalone bootstrap applies | §13.10 (unchanged) |
| Re-ingest of an unchanged stored child after a newer live in-range parent lands | Counted idempotent; stored pin and fold unchanged | §4.7.6 (SPEC-2); `docs/authoring/extends.md:141` |
| Changed child bytes at a stored version | Conflict report; stored version unchanged | §4.7 version immutability invariant (unchanged); `docs/authoring/extends.md` |
| Child published at a new version after a newer parent lands | The new version resolves the reference again and folds the newer parent | §4.7.6 "To pick up a newer parent, publish the child at a new `version:`." (SPEC-2); `docs/authoring/extends.md:43` |
| A child version is purged (deprecation purge or layer unregistration) and the same `(id, version)` is ingested again | A fresh insert that resolves the reference again (accepted) | §4.7.6 scopes the idempotent rule to a stored version (SPEC-2); no docs sentence is staged, because the purge paths are documented in their own sections |
| `PODIUM_HOST_PYTHON` is whitespace only | The gate activates and every Python requirement fails with `materialize.runtime_unavailable` (accepted) | §6.2 `PODIUM_HOST_PYTHON` row (SPEC-3); `docs/reference/cli.md` row (DOC-1) |
| `PODIUM_HOST_PYTHON=v3.11.4` (a non-digit prefix) | The numeric comparison reads no digits, so `>=3.10` fails (accepted) | §6.2 "numeric dot-part comparison" and the `for example 3.11.4` form (SPEC-3); `docs/reference/cli.md` row gives the digit form (DOC-1) |
| A non-`>=` requirement such as `3.11` against host `3.11.4` | Refused, because the strings differ | §6.2 "Any other requirement string must equal the host value." (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS=TRUE` or `=1` | The option stays off | §6.2 "Only the value `true` takes effect" (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| Enforce `true`, no host variable, artifact requires Python | Refused with `materialize.runtime_unavailable` | §6.2 enforce row (SPEC-3), §6.9; `docs/reference/cli.md` (DOC-1) |
| Enforce and ignore both `true`, requirement unsatisfied | Admitted, with a `WARN:` line on standard error | §6.2 ignore row "takes precedence" (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| Ignore `true` on a host with no gate configuration | No effect and no warning; requirements are surfaced without a refusal | §6.2 ignore row "While the gate is inactive the variable has no effect." (SPEC-3), §4.4.1; `docs/reference/cli.md` (DOC-1) |
| Malformed frontmatter with ignore `true` | Refused by the sandbox gate with `materialize.sandbox_unsupported` before the runtime gate runs, so the ignore variable is never consulted (accepted) | No staged text, because the outcome belongs to the sandbox gate, whose variables are a non-goal; the order is `cmd/podium-mcp/main.go:1733-1742`, and `TestEnforceSandboxPolicy_MalformedFrontmatter` (`cmd/podium-mcp/policy_test.go:23`) pins the sandbox refusal |
| Requirement `jq`, host advertises `JQ` | Refused | §6.2 `PODIUM_HOST_PACKAGES` row "including case" (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| `podium sync` on an artifact with unsatisfied requirements | Materialized without a check | §4.4.1 (unchanged); `docs/authoring/bundled-resources.md:104` |

## Testing

**TEST-1 · unit, `pkg/registry/ingest/extends_conformance_test.go`.** Lands in S5.

Add `TestExtends_UnchangedChildReingestKeepsPinAfterNewerLiveParent` beside `TestExtends_RangeReingestStaysIdempotentAfterParentDeprecation` (line 160), annotated `// Spec: §4.7.6` and `// Spec: §4.7` (version immutability).

1. Ingest `agentParent("live")` at `shared/parent` in L1 (version 1.0.0).
2. Ingest `finance/child` at 2.0.0 in L2 with `extends: shared/parent@1.x`. Assert `Accepted == 1` and no rejections.
3. Ingest `agentVersion("1.1.0", "newer live")` at `shared/parent` in L1. It is not deprecated and is inside the `1.x` range.
4. Re-ingest the identical child bytes. Assert `Idempotent == 1`, `Accepted == 0`, `len(Rejected) == 0`, and `len(Conflicts) == 0`.
5. `GetManifest(ctx, "tenant-1", "finance/child", "2.0.0")` and assert `ExtendsPin == "shared/parent@1.0.0"`.

The test guards against a regression no current test catches: adding the pin to `ContentHash`, or re-folding on an idempotent re-ingest, would turn unchanged bytes into a conflict or a re-pin. `TestIngest_ExtendsChildReingestIsIdempotent` (line 436) already covers a discarded fold.

**IMPLEMENTOR'S CHOICE:** whether to also give the two parents distinct tags through inline frontmatter and assert that the 1.1.0 tag is absent from the stored child. The pin assertion in step 5 is required either way, and if tags are used the fixture is written inline rather than through a new shared helper.

**TEST-2 · e2e comment, `test/e2e/artifact_extends_test.go`.** Lands in S5 with TEST-1.

Edit only `TestExtends_PinReingestPicksNewerParent` (lines 168-173).

- Replace the comment with: `// publishing the child at a new version picks up the newer parent version. spec: docs/authoring/extends.md § "Pinning", last paragraph.`
- Replace the skip string with: `"a new child version re-resolving the pin to a newer live parent is exercised end to end in lifecycle_journeys_test.go TestLifecycle_ExtendsPinStabilityAndReingest; an unchanged re-ingest of a stored child version staying idempotent and keeping its pin is covered by pkg/registry/ingest TestExtends_UnchangedChildReingestKeepsPinAfterNewerLiveParent"`.

`TestExtends_PinNoSilentPropagation` (lines 162-166) is unchanged, because its skip reason names tests that exist. The stub stays as a pointer to the covering tests.

**TEST-3 · runtime variables.** Lands in S6. Tests that pin env parsing cite `// Spec: §6.2`; tests that pin gate behavior cite `// Spec: §4.4.1`.

(a) `pkg/materialize/runtime_test.go`, `// Spec: §4.4.1`: `TestCheckRuntimeRequirements_Node`, table-driven over `node: ">=20"`. Host `18.19.0` is refused, host `20.11.1` is admitted, and an empty host Node is refused with an error whose text names `node`. Refusals are checked with `errors.Is(err, ErrRuntimeUnavailable)`.

(b) `cmd/podium-mcp/runtime_policy_test.go`, `// Spec: §4.4.1`:

- `TestRuntimePolicy_IgnoreWinsOverEnforce`: `cfg{enforceRuntime: true, ignoreRuntime: true}` with an unsatisfied `python: ">=3.10"` requirement returns nil.
- `TestRuntimePolicy_MalformedFrontmatterRefusedEvenWithIgnore`: frontmatter that is not valid YAML with `cfg{ignoreRuntime: true, enforceRuntime: true}` returns an error satisfying `errors.Is(err, materialize.ErrRuntimeUnavailable)`. The test is a unit pin of `enforceRuntimePolicy`'s own fail-closed branch (`cmd/podium-mcp/main.go:2256-2260`). It does not describe what a client receives, because `deliverLoadArtifact` runs the sandbox gate first and returns `materialize.sandbox_unsupported` for the same input (`cmd/podium-mcp/main.go:1733-1742`). Its comment says so, and it carries no `// Spec: §6.2` annotation.
- `TestRuntimePolicy_IgnoreNoEffectWhenUnconfigured`, a new top-level test annotated `// Spec: §4.4.1` and `// Spec: §6.2` that does not call `t.Parallel`: `cfg{ignoreRuntime: true}` with a `python: ">=3.11"` requirement returns nil, and the `captureStderr` output (`cmd/podium-mcp/config_env_test.go:119`) contains no `WARN:` line. `TestRuntimePolicy_InactiveWhenUnconfigured` (line 31) is unchanged and keeps its `t.Parallel()` call. The ignore case is a separate function because a variant inside that parallel test would run alongside `TestRuntimePolicy_IgnoreOverridesRefusal` and `TestRuntimePolicy_IgnoreWinsOverEnforce`, which write the `WARN: PODIUM_IGNORE_RUNTIME_REQUIREMENTS` line, and would race on the swapped `os.Stderr`.
- `TestRuntimePolicy_WhitespaceHostPythonFailsRequirement`, `// Spec: §6.2`: `cfg{hostPython: "  "}` with `python: ">=3.10"` returns `ErrRuntimeUnavailable`.

No separate Node policy test and no unadvertised-runtime policy test are added. (a) covers the Node branch, and `TestRuntimePolicy_EnforceFlagFailsClosed` (line 75) covers an unadvertised runtime on an active gate.

(c) `cmd/podium-mcp/config_env_test.go`, `// Spec: §6.2`: `TestLoadConfig_RuntimeVariables`, a table run under `hermetic(t)` with `t.Setenv`.

- The enforce and ignore variables with value `true` set their fields. The values `TRUE`, `1`, and `yes` leave them false.
- `PODIUM_HOST_PYTHON` and `PODIUM_HOST_NODE` are copied verbatim, and a whitespace-only value is preserved.
- `PODIUM_HOST_PACKAGES=jq, curl,` yields `[jq curl]`.

(d) `test/e2e/bundled_resources_test.go`, `// Spec: §6.2` and `// Spec: §4.4.1`: one subprocess test beside `TestBundled_RuntimeUnavailablePython` (line 595) that reuses `brSkillArtifactPy`, with two subtests.

- With `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS=true` and no `PODIUM_HOST_*` runtime variable, the result carries `materialize.runtime_unavailable` and nothing is materialized.
- With both enforce and ignore set to `true`, the artifact materializes, and `res.Stderr` contains `PODIUM_IGNORE_RUNTIME_REQUIREMENTS bypassing runtime check`.

Confirm that both subtests run, rather than skip, in the Linux lane.

**IMPLEMENTOR'S CHOICE:** the name of the (d) test function. It must match the `TestBundled_Runtime` prefix so the existing run patterns select it.

**TEST-4 · registry.yaml lookup.** Lands in S7. Every TEST-4 test cites `// Spec: §13.12`, and (c) also cites `// Spec: §13.10`.

(a) `internal/serverboot/yaml_config_test.go`: `TestReadYAMLConfig_FallsBackToHome`. It calls `t.Setenv("PODIUM_CONFIG_FILE", "")` and `t.Setenv("HOME", tmp)`, writes `tmp/.podium/registry.yaml` containing `registry:\n  layer_path: /from/home`, and asserts that `readYAMLConfig` returns a config whose `LayerPath` is `/from/home`.

(b) `cmd/podium/serve_flags_test.go`: `TestServeCmd_ConfigFlagOverridesConfigFileEnv`, modelled on `TestServeCmd_StandaloneFlagsSetEnv`. It sets `PODIUM_CONFIG_FILE` to a missing path under `t.TempDir()`, `PODIUM_SIGN=none`, `PODIUM_REGISTRY_STORE=postgres`, and `PODIUM_POSTGRES_DSN=""`, and writes a valid `registry.yaml` at path A. It runs `serveCmd([]string{"--config", A})` inside `captureStderr` (`cmd/podium/admin_migrate_test.go:29`) and asserts:

- the exit code is 1 (validation fails on the empty DSN);
- `os.Getenv("PODIUM_CONFIG_FILE") == A`;
- the captured standard error does not contain `does not exist`, which proves the inherited missing path was never checked.

The test does not call `t.Parallel`. No new e2e test is added for the override. The in-process test pins the `os.Setenv` side effect and counts in the default coverage profile.

(c) `internal/serverboot/yaml_config_test.go`: `TestLoadBootConfig_MissingConfigFileRefuses`. It pins the §13.12 sentence that the registry refuses to start when `PODIUM_CONFIG_FILE` names a missing file, which `loadBootConfig` implements at `internal/serverboot/serverboot.go:786-792`. No existing test asserts that refusal: the tests that set a missing `PODIUM_CONFIG_FILE` (`cmd/podium/serve_flags_test.go:23` and `:45`, `internal/serverboot/selfembed_config_test.go:14`, `internal/serverboot/backend_config_test.go:203`) use it only to keep the developer's `~/.podium` out of the run. The test calls `t.Setenv("HOME", home)` and writes a valid `home/.podium/registry.yaml`, so a silent fallback to the home file would load a config and fail the test. It sets `PODIUM_CONFIG_FILE` to a path under `t.TempDir()` that does not exist, calls `loadBootConfig()`, and asserts that the returned config is nil and that the error contains both `does not exist` and the path. The test does not call `t.Parallel`, because `t.Setenv` forbids it.

**Run.** `go test ./pkg/registry/ingest/ ./pkg/materialize/ ./cmd/podium-mcp/ ./cmd/podium/ ./internal/serverboot/`, `go test -race ./cmd/podium-mcp/`, and `go test ./test/e2e/ -run 'TestBundled_Runtime|TestExtends_Pin|TestLifecycle_ExtendsPin'`. Then run `make coverage-gate`.

## Manual validation

No manual-validation scenario is staged, because no served byte, response, log line, or startup outcome changes.

## Documentation changes

**DOC-1.** Lands in S8.

(a) `docs/reference/cli.md`, "Environment variables" table (lines 808-836 at the time of writing). Insert after the `PODIUM_NO_AUTOSTANDALONE` row:

| Variable | Purpose |
|:--|:--|
| `PODIUM_CONFIG_FILE` | Registry-process boot setting. Path of the `registry.yaml` the registry reads. When it is unset, the registry reads `~/.podium/registry.yaml`. `podium serve --config <path>` sets it for the process, and `podium-server` has no `--config` option. A named file that does not exist aborts startup. |

Insert after the `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` row:

| Variable | Purpose |
|:--|:--|
| `PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE` | Read by `podium-mcp`, environment only. The Python and Node versions the host advertises to the runtime gate, given as dot-separated digits such as `3.11.4`. A non-empty value activates the gate. Leading and trailing spaces and tabs are ignored when the value is compared, so a value of only spaces activates the gate and fails every requirement for that runtime. A `>=X` requirement compares numeric parts; any other requirement must equal the value. |
| `PODIUM_HOST_PACKAGES` | Read by `podium-mcp`, environment only. Comma-separated system packages the host advertises, for example `jq,curl`. Entries are trimmed, and empty entries are dropped. A requirement matches an entry exactly, including case. A non-empty list activates the gate. |
| `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` | Read by `podium-mcp`, environment only. `true` activates the runtime gate without advertising a capability, so a requirement for an unadvertised runtime or package fails with `materialize.runtime_unavailable`. Any other value, including `TRUE` and `1`, leaves enforcement off. |
| `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` | Read by `podium-mcp`, environment only. `true` admits an artifact whose runtime requirements are unsatisfied and writes a `WARN:` line to standard error naming the artifact and the advertised capabilities. It applies only while the gate is active and has no effect otherwise. It takes precedence over `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`. Any value other than `true` leaves the override off. |

(b) `docs/authoring/bundled-resources.md`, the paragraph after the `runtime_requirements` example (line 104 at the time of writing), which ends "and `podium sync` materializes the artifact without checking it." Append:

> `PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE`, and `PODIUM_HOST_PACKAGES` advertise capabilities, `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` opts into enforcement, and `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` overrides the refusal; the [environment-variable reference](../reference/cli#environment-variables) gives each variable's format and precedence.

(c) `docs/deployment/vector-backends.md:73`, "Clustered with Pinecone". Replace "The `podium-server` binary parses no flags, so a clustered deployment" with "The `podium-server` binary has no `--config` option, so a clustered deployment". The rest of the sentence is unchanged. The wording matches the SPEC-1 sentence, because `podium-server sign-stored-rows` parses its own flags (`internal/serverboot/signpass.go:125-127`).

`docs/authoring/frontmatter-reference.md:158` is not edited, because its `runtime_requirements` row already links to Bundled resources. `docs/authoring/extends.md` and `docs/deployment/oidc/okta.md` are already correct.

**CL-1.** Lands in S9. Under `### Documentation` in `## [Unreleased]` of `CHANGELOG.md`, add:

```markdown
- §13.12 now states the `registry.yaml` lookup. The registry reads the file
  that `PODIUM_CONFIG_FILE` names, and reads `~/.podium/registry.yaml` when the
  variable is unset. `podium serve --config <path>` sets the variable for the
  process. The spec no longer names `/etc/podium/registry.yaml`, which no code
  reads. The CLI reference gains a `PODIUM_CONFIG_FILE` row.
- §4.6 and §4.7.6 now state that an `extends:` child picks up a newer parent
  when it is published at a new `version:`, and that a re-ingest of a stored
  child version's unchanged bytes is idempotent and keeps the stored pin and
  folded fields. This corrects the spec text and changes no behavior.
- §6.2, the CLI reference, and `docs/authoring/bundled-resources.md` list the
  runtime-gate variables `podium-mcp` reads: `PODIUM_HOST_PYTHON`,
  `PODIUM_HOST_NODE`, `PODIUM_HOST_PACKAGES`,
  `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`, and
  `PODIUM_IGNORE_RUNTIME_REQUIREMENTS`.
```

Do not edit the historical entries at `CHANGELOG.md:826` and `:856`.

## Open questions

**OQ-1. Decisions for items outside this proposal.** The relayed user request says "D5: go with option c", "C30: go with option c", and "C23: go with option b". None of those IDs matches the items this proposal covers (D13, C28, and OQ-3), and the computed task does not define them. Should those decisions be applied in a separate proposal, or was this proposal meant to cover different items?

## Non-goals

- Changing how the registry locates `registry.yaml`. That includes adding an `/etc/podium` fallback, adding a `--config` option to `podium-server`, and changing the hard error for a missing named file.
- Editing §13.10. Its pre-existing gaps stay out of scope: a set `PODIUM_CONFIG_FILE` suppresses auto-bootstrap whether or not the file exists, and a Postgres store skips the zero-flag path (`internal/serverboot/standalone_bootstrap.go:18-20`).
- Making a same-version re-ingest re-pin or re-fold an `extends:` child, or including the pin in `ContentHash`. The §4.7 version immutability invariant forbids either.
- Editing `docs/authoring/extends.md` or `docs/deployment/oidc/okta.md`, which already describe the code.
- Trimming `PODIUM_HOST_PYTHON` or `PODIUM_HOST_NODE`, accepting truthy spellings other than `true` for the enforce and ignore flags, or adding flag or config-file forms for the runtime variables.
- Listing the sandbox-profile variables (`PODIUM_ENFORCE_SANDBOX_PROFILE`, `PODIUM_HOST_SANDBOXES`, and `PODIUM_IGNORE_SANDBOX`, `cmd/podium-mcp/main.go:284-286`) in §6.2. They have the same gap, and the OQ-3 decision covers only the runtime variables.
- Editing the §6.9 runtime row or adding an error code. `materialize.runtime_unavailable` already covers the refusal.
- Rewriting the historical `CHANGELOG.md:856` note or the `/etc/podium` key-file paths in test fixtures.
- A new end-to-end test for `podium serve --config` overriding `PODIUM_CONFIG_FILE`. The in-process TEST-4 (b) pins the side effect, and a full standalone boot adds time and fragility without asserting anything further.
- A test for the new-version half of C28. `TestLifecycle_ExtendsPinStabilityAndReingest`, `TestExtends_ChildVersionBumpRepinsPastDeprecatedParent`, and `TestIngest_ExtendsUnpinnedPinsMostRecentlyIngestedParent` already cover it.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-02, automated)

- **Malformed frontmatter is refused by the sandbox gate with `materialize.sandbox_unsupported`.** `deliverLoadArtifact` runs `enforceSandboxPolicy` before `enforceRuntimePolicy` (`cmd/podium-mcp/main.go:1733-1742`), and the sandbox gate fails closed on a parse error (`:2201-2206`). The malformed-frontmatter sentence is removed from the SPEC-3 (a) ignore row and the DOC-1 (a) ignore row. The edge-case row now states the client-observed `materialize.sandbox_unsupported` outcome and cites `TestEnforceSandboxPolicy_MalformedFrontmatter`. The OQ-3 current-state step 1 describes both gates. TEST-3 (b) scopes `TestRuntimePolicy_MalformedFrontmatterRefusedEvenWithIgnore` as a unit pin of `enforceRuntimePolicy`'s own branch, and a Watch-out bullet records the trap.
- **`podium-server` parses the `sign-stored-rows` flags and reaches the home fallback.** The SPEC-1 (a) sentence, the D13 lookup decision, the edge-case row's quoted text, and the D13 current-state bullet now say that `podium-server` has no `--config` option and locates `registry.yaml` through `PODIUM_CONFIG_FILE` and the `~/.podium/registry.yaml` fallback only. The `cmd/podium-server/main.go` citation is corrected to `:24-33`. DOC-1 and CL-1 already used compatible wording and are unchanged.
- **The TEST-3 (b) ignore-on-inactive-gate variant ran inside a parallel test.** It is now the separate top-level, non-parallel `TestRuntimePolicy_IgnoreNoEffectWhenUnconfigured`, annotated `// Spec: §4.4.1` and `// Spec: §6.2`, and `TestRuntimePolicy_InactiveWhenUnconfigured` is unchanged. The Watch-out bullet on `captureStderr` cites the comment at `cmd/podium-mcp/config_env_test.go:112-118` and names the subtest case.
- **The Helm chart does not set `PODIUM_CONFIG_FILE`.** The D13 current-state parenthetical now says that `deploy/helm/podium/values.yaml:105-108` tells the operator to mount a `registry.yaml` and point `PODIUM_CONFIG_FILE` at it, and that the chart does not set the variable. The D13 scope decision still holds, because the chart names no `/etc/podium` config path.

### Pass 2 (2026-10-02, automated)

- **`docs/deployment/vector-backends.md:73` says that `podium-server` parses no flags.** `podium-server sign-stored-rows` parses `--include-unsigned`, `--dry-run`, and `--plan-digest` (`cmd/podium-server/main.go:27-29`, `internal/serverboot/signpass.go:125-127`). DOC-1 gains item (c), which replaces "parses no flags" with "has no `--config` option" to match SPEC-1. The D13 current-state paragraph, the DOC-1 closing sentence, and the Non-goals bullet no longer list the page as already correct, and the Summary and checklist step S8 name the page.
- **The SPEC-3 (a) `PODIUM_HOST_PYTHON` and `PODIUM_HOST_NODE` rows stated the unset-value refusal without the gate-active conjunct.** The refusal runs only after `runtimeGateActive` (`cmd/podium-mcp/main.go:2263-2265`, `:2288-2293`), and §4.4.1 admits requirements on an unconfigured host (`spec/04-artifact-model.md:365`). Each row now says "While the gate is active, an unset value fails any Python requirement" (or Node) and states separately that a value of only spaces and tabs activates the gate and fails every requirement for that runtime.
- **The `signpass.go` citation for the `sign-stored-rows` flags missed the `--plan-digest` line.** Line 124 of `internal/serverboot/signpass.go` is `fs.SetOutput(io.Discard)`, and the three flags are declared on lines 125 to 127. The D13 current-state paragraph, DOC-1 (c), and the first bullet of this pass now cite `internal/serverboot/signpass.go:125-127`.

### Pass 3 (2026-10-02, automated)

- **The `mr.ExtendsPin` assignment was cited at `pkg/registry/ingest/ingest.go:697`.** Line 697 is `parentID, parentVersion := splitRef(pin)`, and the assignment `mr.ExtendsPin = pin` is on line 688. The C28 current-state paragraph now cites `:688`.

### Pass 4 (2026-10-02, automated)

- **No test pinned the §13.12 refusal when `PODIUM_CONFIG_FILE` names a missing file.** `loadBootConfig` returns `config file %q does not exist` (`internal/serverboot/serverboot.go:786-792`), and the existing tests set a missing path only as a fixture. TEST-4 gains (c), `TestLoadBootConfig_MissingConfigFileRefuses` in `internal/serverboot/yaml_config_test.go`, annotated `// Spec: §13.12` and `// Spec: §13.10`. It points `HOME` at a directory holding a valid `registry.yaml` so a silent fallback fails the test, and asserts a nil config and an error naming the path and `does not exist`. The TEST-4 heading, the Summary tests bullet, checklist step S7, and the Watch-out bullet on `TestServeCmd_StandaloneFlagsSetEnv` name the refusal. The Run list already includes `./internal/serverboot/`.
