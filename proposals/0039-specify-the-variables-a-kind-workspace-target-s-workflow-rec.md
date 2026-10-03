# Proposal 0039: Specify the variables a kind: workspace target's workflow receives in §7.5.2

- Issue: (to be filed)
- Status: Applied to spec (2026-10-03). The approval was decided on the user's behalf under the overnight authorization and signed off as staged. OQ-1: the staged first option (no absolute-path promise). OQ-2: option (1), CODE-1, so podium sync --config reads PODIUM_REGISTRY, matching the §7.5.2 precedence and the existing CI examples.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.5.2 gains a paragraph that defines the variables Podium injects into a `kind: workspace` target's workflow (`$PODIUM_WORKDIR`, `$PODIUM_TARGET_ID`, `$PODIUM_REGISTRY`, and `$PODIUM_CHANGED` for the `publish` phase), states that each on_error list receives its phase's variables, and states that `--dry-run` and `--check` neither run nor print a workspace workflow command. The existing `$PODIUM_CHANGED` paragraph scopes its `--dry-run` clause to `kind: marketplace` (SPEC-1).
- §7.8: the last bullet of the "Injected variables" list names `$PODIUM_REGISTRY`, `$PODIUM_IDENTITY`, and `$PODIUM_HARNESSES` and describes the registry as a registry source that is a URL or a filesystem path (SPEC-2).
- `cmd/podium/main.go`: `runMultiTargetSync` falls back to the `PODIUM_REGISTRY` environment variable when the `--registry` flag is empty, so `podium sync --config` resolves the registry source under the existing §7.5.2 precedence (flag, then `PODIUM_*` env var, then the config file) and the §7.8 Pattern A CI example works as written. The `PlanMultiTarget` doc comment in `pkg/sync/resolve.go` names the fallback (CODE-1).
- `test/e2e/publishing_test.go`: `TestPublishing_WorkspaceTargetRunsWorkflow` pins every injected value in both phases and the absence of a workflow run under `--dry-run` and `--check`, a new `TestPublishing_WorkspaceTargetOnErrorVariables` pins the on_error variables, and a new `TestPublishing_ConfigRegistrySourcePrecedence` pins the `--config` registry precedence (TEST-1, TEST-2, TEST-3). The fixture signature change reaches both existing callers of `writeSyncConfigWorkspaceWorkflow`.
- `docs/consuming/publishing.md` aligns its Injected variables table, its execution-semantics paragraph, and its `--dry-run` flag row with §7.5.2 (DOC-1).
- `CHANGELOG.md` gains `[Unreleased]` Documentation and Fixed entries (CL-1).

**Fixed decisions.**

- The only product-code change is CODE-1, which brings the `--config` registry resolution in line with the §7.5.2 precedence list. The variable map `runWorkspaceTarget` builds keeps its names, phases, and derivations, and the spec text describes that map. `$PODIUM_REGISTRY` takes the registry source CODE-1 resolves, so its value changes when `PODIUM_REGISTRY` is exported on a `--config` run (OQ-2).
- The definition lands in §7.5.2, placed before the existing `$PODIUM_CHANGED` paragraph. §7.8 changes in one bullet only.
- Variable names stay as the code spells them: `$PODIUM_TARGET_ID` for a workspace target and `$PODIUM_OUTPUT_ID` for a marketplace target. Neither is renamed.
- `$PODIUM_WORKDIR` for a workspace target is the target directory made absolute against the working directory of the `podium sync` process. The spec text does not say it resolves against the config workspace.
- `$PODIUM_REGISTRY` is the registry source the run resolved under the §7.5.2 precedence: the `--registry` flag, then the `PODIUM_REGISTRY` environment variable, then `defaults.registry`, with the §13.11.2 workspace-relative resolution applied to whichever source wins. The spec text does not promise an absolute path or a URL (OQ-1, OQ-2).
- The "no other variable" statement is scoped to what Podium injects and does not enumerate the marketplace-only variables. Commands inherit the ambient environment, and an injected value overrides an ambient one of the same name.
- Each on_error list receives the variables of the phase it cleans up, so `publish_on_error` receives `$PODIUM_CHANGED` and `prepare_on_error` does not.
- A workspace workflow runs only on a materializing run. `--dry-run` and `--check` neither run nor print its commands, and the existing `$PODIUM_CHANGED` dry-run clause is scoped to `kind: marketplace`.
- The tests extend the existing workspace-workflow test and its fixture. No near-duplicate fixture helper is added.
- The proposal adds no environment variable, flag, §6.10 error code, SPI, or matrix cell. CODE-1 reads the existing `PODIUM_REGISTRY` variable on one more path.

**Watch out for.**

- **The e2e harness inherits the developer's environment.** `runPodium` goes through `mergeEnv` (`test/e2e/helpers_test.go`), which copies `os.Environ()` and drops only the keys passed as overrides. A developer shell that exports `PODIUM_CHANGED` or `PODIUM_OUTPUT_ID` would leak into the `prepare` phase, where Podium injects neither. TEST-1 and TEST-2 pass empty overrides and print with the `${VAR:-unset}` form, which treats an empty value as unset. The no-colon form `${VAR-unset}` prints an empty line for an empty override and fails the assertion.
- **After CODE-1 an ambient `PODIUM_REGISTRY` reaches every `--config` e2e run.** A developer shell that exports `PODIUM_REGISTRY` would override `defaults.registry` in any `sync --config` e2e test that does not pin the variable, which the single-target e2e tests are already exposed to. TEST-1, TEST-2, and TEST-3 pin it explicitly. `firstNonEmpty` and the CODE-1 fallback both treat an empty value as unset, so an empty override restores the `defaults.registry` path.
- **The fixture has two existing callers.** `TestPublishing_WorkspaceTargetRunsWorkflow` and `TestPublishing_WorkspaceCollisionPublishesThenFails` both call `writeSyncConfigWorkspaceWorkflow`. `test/e2e` is one package, so a missed call site stops every e2e test from compiling.
- **`$PODIUM_CHANGED` is never empty when injected.** Podium writes it with `strconv.FormatBool` (`cmd/podium/main.go`), so the `:-` form cannot mask an injected value.
- **The relative-target resolution is easy to misdescribe.** `PlanMultiTarget` (`pkg/sync/resolve.go`) never joins a target to the workspace, and `runWorkspaceTarget` calls `filepath.Abs` against the process working directory. A spec or doc sentence saying "relative to the config" is wrong.
- **`--dry-run` behaves differently per kind.** A marketplace `--dry-run` prints each command with its variables substituted (§7.8). A workspace `--dry-run` runs and prints nothing (`runWorkflow := !check && !dryRun && ...`). Do not copy the marketplace sentence into the workspace text.
- **`filepath.Abs` does not resolve symlinks.** On darwin `t.TempDir()` lives under `/var`, a symlink to `/private/var`. The existing target equality holds because neither side is resolved. Compare the target and the registry without `filepath.EvalSymlinks`.
- **Spec line numbers are anchors at the time of writing.** Locate each §7.5.2 and §7.8 edit by its quoted current text.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §7.5.2 defines the workspace workflow variables and scopes the `$PODIUM_CHANGED` dry-run clause to `kind: marketplace`.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §7.8 names the registry, identity, and harness variables and calls the registry a registry source.
      Levels: —. Depends on: S1
- [ ] **S3 · test** — TEST-1. The fixture change (the `failPhase` parameter and the omitted-registry form), both existing call sites (`TestPublishing_WorkspaceTargetRunsWorkflow` and `TestPublishing_WorkspaceCollisionPublishesThenFails`, each passing `""`), and `TestPublishing_WorkspaceTargetRunsWorkflow` pinning every injected value in both phases and the skipped workflow under `--dry-run` and `--check`.
      Levels: e2e. Depends on: S1
- [ ] **S4 · test** — TEST-2. `TestPublishing_WorkspaceTargetOnErrorVariables` pins the variables each on_error list receives.
      Levels: e2e. Depends on: S3
- [ ] **S5 · code** — CODE-1 and TEST-3. `runMultiTargetSync` falls back to `PODIUM_REGISTRY`, and `TestPublishing_ConfigRegistrySourcePrecedence` pins the flag, env, and file order.
      Levels: e2e. Depends on: S1, S3
- [ ] **S6 · docs** — DOC-1. `docs/consuming/publishing.md` aligns the Injected variables table, the execution-semantics paragraph, and the `--dry-run` row with §7.5.2.
      Levels: —. Depends on: S1, S2
- [ ] **S7 · docs** — CL-1. The `[Unreleased]` Documentation and Fixed entries.
      Levels: —. Depends on: S1, S2, S5

**Ordering constraints.** S1 lands the text every later step cites, including the registry precedence CODE-1 implements. S2 cross-references the §7.5.2 definition of the registry source, so it follows S1. S4 and S5 reuse the fixture signature S3 introduces, and S5 also uses the fixture's omitted-registry form that S3 adds. S6 and S7 restate the committed spec text, and the CL-1 Fixed entry records the S5 behavior change.

## Current state and the gap

§7.5.2 says both target kinds may carry a `workflow:` of `prepare` and `publish` command lists ("Both kinds may carry a `workflow:` of `prepare` and `publish` command lists."). The only variable it defines is `$PODIUM_CHANGED`, which proposal 0036 added. Proposal 0036 deferred the other workspace variables to a follow-up in its OQ-1 and its non-goals.

The code already injects three more variables. `runWorkspaceTarget` (`cmd/podium/main.go`) builds one map:

- `PODIUM_WORKDIR`: `filepath.Abs(p.Target)`, resolved against the process working directory. `PlanMultiTarget` (`pkg/sync/resolve.go`) takes the target from the entry or from `defaults.target` and never joins it to the workspace.
- `PODIUM_TARGET_ID`: `p.ID`, the `targets:` entry `id` (`pkg/sync/resolve.go`, `pkg/sync/config.go`).
- `PODIUM_REGISTRY`: `p.Registry`, which `PlanMultiTarget` takes from the `--registry` flag or else from `defaults.registry` and passes through `ResolveRegistryPath` (`pkg/sync/config.go`). A URL or `file://` value passes through unchanged, an absolute path is cleaned, and a relative path is joined to the workspace. The workspace is the parent of the config file's `.podium` directory, so a relative `--config` path leaves the workspace, and a relative registry joined to it, relative (`cmd/podium/main.go`). The value can therefore be a filesystem path.

The `--config` path does not read the `PODIUM_REGISTRY` environment variable. `PlanMultiTarget` computes `firstNonEmpty(in.RegistryOverride, cfg.Defaults.Registry)` (`pkg/sync/resolve.go:393`), and `runMultiTargetSync` fills `RegistryOverride` from the `--registry` flag alone (`cmd/podium/main.go:495-499`; the flag at `cmd/podium/main.go:235` has no env default). The single-target path, by contrast, computes `firstNonEmpty(in.Registry, env("PODIUM_REGISTRY"), merged.Defaults.Registry)` (`pkg/sync/resolve.go:237`). The spec requires the env var: the §7.5.2 precedence list puts "`PODIUM_*` env vars" between CLI flags and the config files (`spec/07-external-integration.md:326-333`), §6 defines `PODIUM_REGISTRY` as the registry source (`spec/06-mcp-server.md:25`), and the §7.8 Pattern A example supplies the registry to `podium sync --config` only through `PODIUM_REGISTRY` (`spec/07-external-integration.md:924-928`, mirrored in `docs/consuming/publishing.md:248-251`). `docs/reference/error-codes.md:68` likewise says `config.no_registry` fires only when no `--registry` flag or `PODIUM_REGISTRY` env var is set. Without `defaults.registry` in the named file, the Pattern A run fails today with `config.no_registry` (`ErrNoRegistry`, `pkg/sync/sync.go:40`). The code is the defect.

`runWorkspaceTarget` passes the map to the `prepare` phase and adds `PODIUM_CHANGED` only before `publish`. `WorkflowRunner.Phase` hands the same map to the phase's on_error cleanup (`pkg/sync/workflow_phase.go`), so `prepare_on_error` lacks `PODIUM_CHANGED` and `publish_on_error` carries it. `commandEnv` appends the injected variables after `os.Environ()`, so an injected value overrides an ambient value with the same name. The workflow runs only on a materializing run (`runWorkflow := !check && !dryRun && !p.Workflow.IsZero()`), so a workspace `--dry-run` neither runs nor prints a workflow command. A §7.8 marketplace `--dry-run`, by contrast, prints each command with its variables substituted.

No spec text defines `$PODIUM_WORKDIR`, `$PODIUM_TARGET_ID`, or `$PODIUM_REGISTRY` for a workspace target, and `PODIUM_TARGET_ID` appears nowhere under `spec/`. The §7.8 "Injected variables" list is scoped to the marketplace pipeline, defines `$PODIUM_WORKDIR` as "the per-output working and checkout directory", and describes the registry only as "The registry URL". The marketplace pipeline injects the same `p.Registry` value (`pkg/sync/marketplace_run.go`, `baseVars`), so the §7.8 wording is inaccurate for a filesystem registry as well.

`docs/consuming/publishing.md` names the workspace variables in its `workflow` row and defers to its Injected variables table. That table has no `$PODIUM_TARGET_ID` row, describes `$PODIUM_WORKDIR` only as the per-target checkout directory, and calls `$PODIUM_REGISTRY` "The registry URL". The `--dry-run` row of its flag table describes the marketplace behavior for every target.

One e2e test, `TestPublishing_WorkspaceTargetRunsWorkflow` (`test/e2e/publishing_test.go`), pins `$PODIUM_WORKDIR` in the workspace `prepare` phase. It carries no `// Spec:` citation. No test pins `$PODIUM_TARGET_ID`, `$PODIUM_REGISTRY`, any publish-phase value, any on_error value, or the skipped workflow under `--dry-run` and `--check` for a workspace target.

## Decisions

- **The proposal documents the existing variable map.** The variable names, the phases that receive them, and every derivation other than the registry source stay as `runWorkspaceTarget` builds them. CODE-1 changes only the registry source that `$PODIUM_REGISTRY` carries. A spec that described other names, phases, or derivations would require code work this proposal does not stage.
- **The `--config` registry source follows the §7.5.2 precedence (CODE-1, OQ-2).** The spec already ranks `PODIUM_*` env vars between CLI flags and the config files, and its own §7.8 CI example depends on that order. CODE-1 changes the code to match. The alternative, a spec carve-out that drops `PODIUM_REGISTRY` from the `--config` path, would require rewriting the §7.5.2 precedence list, the §7.8 Pattern A example, the docs CI example, and `docs/reference/error-codes.md`, and would break the documented CI configuration.
- **§7.5.2 is the home.** §7.5.2 already owns the workflow contract for both target kinds, including `$PODIUM_CHANGED`. §7.8 keeps its marketplace list and is not restructured.
- **`$PODIUM_TARGET_ID` is the workspace counterpart of `$PODIUM_OUTPUT_ID`.** A workspace target does not receive `$PODIUM_OUTPUT_ID`. Unifying the two names would be a behavior change and is a non-goal.
- **`$PODIUM_WORKDIR` resolves against the process working directory.** The text says so because the target is never joined to the workspace, and a reader who runs `podium sync --config` from another directory sees a different absolute path.
- **`$PODIUM_REGISTRY` is the resolved registry source.** The flag wins over the `PODIUM_REGISTRY` env var, which wins over `defaults.registry`, and the §13.11.2 workspace-relative resolution applies to whichever source wins. The value is a URL or a filesystem path. SPEC-2 rewords the §7.8 bullet so both sections agree, and DOC-1 mirrors it.
- **The exclusion statement is scoped to injection.** The text says Podium injects no other variable into a workspace workflow. It does not enumerate the marketplace-only variables, because the list would go stale when §7.8 gains one. The ambient-environment rule is cited from §7.8 rather than restated.
- **on_error receives its phase's variables.** The behavior follows from the shared map and is observable, so §7.5.2 states it in one sentence.
- **`--dry-run` and `--check` run no workspace workflow command.** The new paragraph states this so readers do not carry over the §7.8 dry-run behavior. The existing `$PODIUM_CHANGED` paragraph's `--dry-run` clause is scoped to `kind: marketplace` to match.
- **The tests extend existing surfaces.** TEST-1 extends `TestPublishing_WorkspaceTargetRunsWorkflow` and its fixture. TEST-2 is one table-driven sibling that reuses the same fixture. `test/e2e/publishing_test.go` has no macOS skip beyond a `git not installed` skip that the workspace tests do not reach, so both tests run on darwin.
- **The proposal adds no new surface.** It adds no environment variable, flag, §6.10 error code, SPI, or matrix cell. CODE-1 reads the existing `PODIUM_REGISTRY` variable on one more path.

## Spec amendment: §7.5.2 workspace workflow variables

**SPEC-1.** Two edits in `spec/07-external-integration.md`, §7.5.2 "Configuration (`sync.yaml`)". Both land in one commit.

(a) Insert a new paragraph after the `kind:` paragraph that ends "A configuration that declares only `kind: workspace` targets, or omits `kind` entirely, behaves exactly as before, because `kind` defaults to `workspace`." (line 412 at the time of writing), and before the paragraph that begins "A workflow's `publish` phase receives `$PODIUM_CHANGED` for both target kinds, with one meaning." (line 414 at the time of writing):

> A `kind: workspace` target's `prepare` and `publish` commands, and each phase's on_error cleanup list, receive these injected variables: `$PODIUM_WORKDIR`, the target directory made absolute against the working directory of the `podium sync` process; `$PODIUM_TARGET_ID`, the target entry's `id`, which is the workspace counterpart of the marketplace `$PODIUM_OUTPUT_ID` (§7.8); and `$PODIUM_REGISTRY`, the registry source the run resolves under the precedence above: the `--registry` flag, then the `PODIUM_REGISTRY` environment variable, then `defaults.registry`. A relative filesystem path from any of these sources resolves against the workspace as §13.11.2 describes, and a URL passes through unchanged, so the value is a URL or a filesystem path. The `publish` phase and its on_error list also receive `$PODIUM_CHANGED`, defined below; the `prepare` phase and its on_error list do not. Podium injects no other variable into a workspace workflow. The commands inherit the ambient environment as §7.8 describes for the marketplace pipeline. A workspace target's workflow runs only on a materializing run, and `--dry-run` and `--check` neither run nor print its commands.

(b) In the `$PODIUM_CHANGED` paragraph, replace the sentence:

> The `prepare` phase runs before materialization and does not receive the variable, and the `prepare` commands that `--dry-run` prints do not substitute it.

with:

> The `prepare` phase runs before materialization and does not receive the variable, and the `prepare` commands that `--dry-run` prints for a `kind: marketplace` target (§7.8) do not substitute it.

The rest of the paragraph is unchanged.

## Spec amendment: §7.8 injected registry, identity, and harness variables

**SPEC-2.** One edit in `spec/07-external-integration.md`, §7.8, the "**Injected variables.**" list (line 874 at the time of writing). Replace the last bullet:

> - The registry URL, the publishing identity, and the harness set.

with:

> - `$PODIUM_REGISTRY`, `$PODIUM_IDENTITY`, `$PODIUM_HARNESSES`: the registry source (a URL or a filesystem path, as §7.5.2 defines), the publishing identity, and the comma-separated harness set.

The separator matches `baseVars` (`pkg/sync/marketplace_run.go`), which joins the harness set with `","` and no space. Make no other §7.8 edit.

## Code change: `--config` registry precedence

**CODE-1.** `cmd/podium/main.go`, `runMultiTargetSync` (`cmd/podium/main.go:484`). Today it builds `sync.PlanInput{RegistryOverride: registryOverride, ...}` from the `--registry` flag alone (`cmd/podium/main.go:495-499`). Compute the override as the flag value when it is non-empty and `os.Getenv("PODIUM_REGISTRY")` otherwise, and pass that as `RegistryOverride`. Add `// Spec: §7.5.2` with a one-line reason: the precedence list ranks `PODIUM_*` env vars between CLI flags and the config file. `PlanMultiTarget` keeps `firstNonEmpty(in.RegistryOverride, cfg.Defaults.Registry)` and its `ResolveRegistryPath` call unchanged, so a relative env value resolves against the workspace exactly as a relative flag value does.

- **State read.** The `--registry` flag value and the process environment variable `PODIUM_REGISTRY`. The operator sets the variable in the shell or the CI job, and Podium never writes it. An empty value counts as unset, matching `firstNonEmpty` on the single-target path.
- **Callers.** `runMultiTargetSync` is the only caller of `PlanMultiTarget` outside tests (`cmd/podium/main.go:496`). `PlanInput` keeps its fields, so the unit tests in `pkg/sync/resolve_test.go` and `pkg/sync/resolve_marketplace_test.go`, which set `RegistryOverride` directly, need no change. The resolved registry feeds both kinds: a workspace target's `$PODIUM_REGISTRY` and sync source, and a marketplace target's `baseVars` and render source (`pkg/sync/marketplace_run.go`).
- **When it does not fire.** With neither the flag nor the variable set, `PlanMultiTarget` falls through to `defaults.registry` and, when that is empty, returns `ErrNoRegistry`, which surfaces as `config.no_registry` with exit 2. That outcome matches `docs/reference/error-codes.md:68`.
- **Doc comments.** Update the `PlanMultiTarget` comment (`pkg/sync/resolve.go:366-369`) so `in.RegistryOverride` reads as "the `--registry` flag, else `PODIUM_REGISTRY`", and add the env fallback to the `runMultiTargetSync` comment.
- **Test.** TEST-3 pins the order at the e2e level, because the fallback runs inside the spawned `podium` binary.

## Proposed solution

The implementation consists of the two spec edits above, the CODE-1 change below, the test changes in [Testing](#testing), and the documentation changes in [Documentation changes](#documentation-changes).

### Fixture change shared by TEST-1 and TEST-2

`writeSyncConfigWorkspaceWorkflow` (`test/e2e/publishing_test.go:1141`) gains a `failPhase string` parameter. Its signature becomes `writeSyncConfigWorkspaceWorkflow(t *testing.T, workspace, registry, target, failPhase string) string`.

- With `registry == ""`, the fixture omits the `registry:` line under `defaults:`, so the config carries no `defaults.registry`. TEST-3 uses this form. Every other caller passes a registry path.
- With `failPhase == ""`, the target's `prepare` command writes the five variable lines below to `<workspace>/prepare-ran`, and its `publish` command writes them to `<workspace>/publish-ran`.
- With `failPhase == "prepare"` or `"publish"`, that phase's only command is `sh: "exit 3"`, and `prepare_on_error` or `publish_on_error` respectively carries one command that writes the five lines to `<workspace>/onerror-ran`. The other phase keeps the marker command from the `""` case.

Each marker command prints, one per line and in this order: `"$PODIUM_WORKDIR"`, `"$PODIUM_TARGET_ID"`, `"$PODIUM_REGISTRY"`, `"${PODIUM_CHANGED:-unset}"`, and `"${PODIUM_OUTPUT_ID:-unset}"`. The target entry keeps `id: claude-workspace`, `kind: workspace`, and `harness: claude-code`.

**IMPLEMENTOR'S CHOICE:** the YAML and shell quoting of the marker command, for example `printf '%s\n' ...` in a double-quoted `sh:` scalar. Any quoting must leave the marker holding exactly the five values, one per line, in the order above, with a trailing newline at most.

Both existing callers pass `""` as `failPhase`: `TestPublishing_WorkspaceTargetRunsWorkflow` (TEST-1, `test/e2e/publishing_test.go:544`) and `TestPublishing_WorkspaceCollisionPublishesThenFails` (`test/e2e/publishing_test.go:973`). The collision test keeps its `// Spec: §13.11.3` and `// Matrix: §6.10 (ingest.collision)` annotations and its assertions unchanged. Its `os.Stat(filepath.Join(ws, "publish-ran"))` check (`test/e2e/publishing_test.go:979`) still holds, because with `failPhase == ""` the `publish` command still writes `publish-ran`, now holding the five lines rather than `done`. The doc comment on the helper names the `failPhase` parameter, the omitted-registry form, and the marker files.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Relative `target:` in `sync.yaml` with `podium sync --config` run from a directory other than the workspace | `$PODIUM_WORKDIR` and the materialized tree follow the process working directory rather than the config location (accepted, existing behavior) | §7.5.2 "made absolute against the working directory of the `podium sync` process" (SPEC-1); `docs/consuming/publishing.md` Injected variables table (DOC-1) |
| Relative `--config` path with a relative `defaults.registry` | `$PODIUM_REGISTRY` stays a relative path joined to the relative workspace (accepted, existing behavior; OQ-1) | §7.5.2 "A relative filesystem path from any of these sources resolves against the workspace as §13.11.2 describes" (SPEC-1); DOC-1 states "a URL or a filesystem path" |
| `--registry` flag set on a `--config` run | `$PODIUM_REGISTRY` carries the flag value, resolved like `defaults.registry`; an ambient `PODIUM_REGISTRY` is ignored | §7.5.2 "the `--registry` flag, then the `PODIUM_REGISTRY` environment variable, then `defaults.registry`" (SPEC-1); TEST-3; `CHANGELOG.md` (CL-1) |
| `PODIUM_REGISTRY` set and no `--registry` flag on a `--config` run | The env value is the registry source for every target and wins over `defaults.registry`; with no `defaults.registry` the run succeeds where it failed with `config.no_registry` before CODE-1 (behavior change) | §7.5.2 precedence list and SPEC-1; CODE-1; TEST-3; `CHANGELOG.md` Fixed (CL-1) |
| No flag, no `PODIUM_REGISTRY`, and no `defaults.registry` on a `--config` run | `config.no_registry`, exit 2 (existing behavior) | `docs/reference/error-codes.md:68`; no new text |
| Ambient `PODIUM_WORKDIR` or `PODIUM_TARGET_ID` in the operator's shell, or an ambient `PODIUM_REGISTRY` that a flag outranks | The injected value overrides it inside every workflow command | §7.8 "inherits the ambient environment ... and adds the injected variables" cited from §7.5.2 (SPEC-1); DOC-1(2) states the override |
| Ambient `PODIUM_CHANGED` or `PODIUM_OUTPUT_ID` in the operator's shell | A `prepare` command, a `prepare_on_error` command, or any workspace command reading `$PODIUM_OUTPUT_ID` sees the ambient value, because Podium injects neither there (accepted) | §7.5.2 "Podium injects no other variable" and the §7.8 ambient-inheritance rule (SPEC-1); DOC-1(2) |
| `prepare` command fails | `prepare_on_error` runs with `$PODIUM_WORKDIR`, `$PODIUM_TARGET_ID`, and `$PODIUM_REGISTRY` and without `$PODIUM_CHANGED`; nothing is materialized; `podium sync` exits 1 | §7.5.2 "each phase's on_error cleanup list" (SPEC-1); DOC-1(2) |
| `publish` command fails | `publish_on_error` runs with the same variables plus `$PODIUM_CHANGED`; the materialized tree stays; `podium sync` exits 1 | §7.5.2 (SPEC-1); DOC-1(2) |
| An on_error command itself fails | Logged under the failing phase; the original phase failure is the reported error (existing behavior) | §7.8 "an optional per-phase `on_error` cleanup list"; no new text |
| `podium sync --config --dry-run` on a workspace target with a workflow | No workflow command runs or prints; the artifact set resolves without writing | §7.5.2 "`--dry-run` and `--check` neither run nor print its commands" (SPEC-1); DOC-1(3) |
| `podium sync --config --check` on a workspace target with a workflow | No workflow command runs or prints; no tree or lock is written | §7.5.2 (SPEC-1); DOC-1(3) |
| Workspace target with no `workflow:` | Nothing runs and nothing is injected | §7.5.2 "Both kinds may carry a `workflow:`" (existing) |

## Testing

The only product-code lines are the CODE-1 fallback in `runMultiTargetSync`. They run inside the spawned `podium` binary, so the in-process profile scores them as uncovered; TEST-3 covers them, and `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/ -run TestPublishing_ConfigRegistrySourcePrecedence` measures it per `.claude/rules/test-coverage.md`. The tests pin the newly specified behavior at the e2e level, because `runWorkspaceTarget` builds the variable map inside the spawned `podium` binary and only an end-to-end run observes it.

**TEST-1 · e2e, `test/e2e/publishing_test.go`, `TestPublishing_WorkspaceTargetRunsWorkflow`.** Lands in S3.

- Replace the doc comment. Drop the "(Decision 3)" framing, state that the test pins the literal values Podium injects into each phase of a workspace workflow and the skipped workflow under `--dry-run` and `--check`, and note that the `skip_if_no_changes` effect of `$PODIUM_CHANGED` stays pinned by `TestPublishing_WorkspaceTargetSkipIfNoChanges`. End the comment block with `// Spec: §7.5.2` directly above the `func` line.
- Build the config with `writeSyncConfigWorkspaceWorkflow(t, ws, reg, target, "")`.
- Pass `env := []string{"PODIUM_CHANGED=", "PODIUM_OUTPUT_ID=", "PODIUM_TARGET_ID=", "PODIUM_WORKDIR=", "PODIUM_REGISTRY="}` to every `runPodium` call, so the ambient shell cannot satisfy or break an assertion.
- Before the materializing run, run `sync --config <cfg> --dry-run` and then `sync --config <cfg> --check`. Assert each exits 0 and that neither `prepare-ran` nor `publish-ran` exists afterwards. Also assert that the stdout and stderr of both runs contain neither `prepare-ran` nor `publish-ran`, because a printed workflow command would carry the marker path. Together these pin the SPEC-1 sentence that `--dry-run` and `--check` neither run nor print a workspace workflow command.
- Run `sync --config <cfg> --json` and keep the existing exit, envelope, and `sync.lock` assertions.
- Read each marker, split with `strings.Split(strings.TrimRight(body, "\n"), "\n")`, and assert exact equality:
  - `prepare-ran` equals `[target, "claude-workspace", filepath.Clean(reg), "unset", "unset"]`.
  - `publish-ran` equals `[target, "claude-workspace", filepath.Clean(reg), "true", "unset"]`.
- On mismatch, report the full slice and the expected slice in the failure message.

The `"unset"` fourth line in `prepare-ran` pins that `prepare` lacks `$PODIUM_CHANGED`. The `"unset"` fifth lines pin that a workspace workflow lacks `$PODIUM_OUTPUT_ID`. `reg` from `writePublishRegistry` is an absolute `t.TempDir()` path, and `ResolveRegistryPath` cleans it.

**TEST-2 · e2e, `test/e2e/publishing_test.go`, new `TestPublishing_WorkspaceTargetOnErrorVariables`.** Lands in S4. Place it after `TestPublishing_WorkspacePublishFailureExits1`, with `// Spec: §7.5.2` as the last line of its comment block directly above the `func` line. The function is table-driven with `t.Parallel()` at the top and in each subtest:

| Case | `failPhase` | Expected `onerror-ran` lines | Expected stderr substring | Expected `sync.lock` |
|:--|:--|:--|:--|:--|
| `prepare` | `"prepare"` | `[target, "claude-workspace", filepath.Clean(reg), "unset", "unset"]` | `prepare[0]` | absent |
| `publish` | `"publish"` | `[target, "claude-workspace", filepath.Clean(reg), "true", "unset"]` | `publish[0]` | present |

Each case builds its own `t.TempDir()` workspace and registry, runs `sync --config <cfg>` with the same empty-override env as TEST-1, asserts `res.Exit == 1`, asserts the stderr substring, reads `onerror-ran` and compares it with the same split as TEST-1, and checks the lock file. The `publish` case runs a first sync into an empty target, so `$PODIUM_CHANGED` is `true`. The test does not re-test that `prepare_on_error` and `publish_on_error` are scoped to their own phase; `pkg/sync/marketplace_run_test.go` pins that for `WorkflowRunner`.

**TEST-3 · e2e, `test/e2e/publishing_test.go`, new `TestPublishing_ConfigRegistrySourcePrecedence`.** Lands in S5 with CODE-1. Place it after TEST-2, with `// Spec: §7.5.2` as the last line of its comment block directly above the `func` line. The function is table-driven with `t.Parallel()` at the top and in each subtest. Each case builds its own `t.TempDir()` workspace, calls `writePublishRegistry(t)` for `regA` and again for `regB`, and writes the config with `writeSyncConfigWorkspaceWorkflow(t, ws, <defaults>, target, "")`, where `<defaults>` is `""` (no `defaults.registry`) or `regB`. It runs `sync --config <cfg>` with the TEST-1 empty-override env, except that the `PODIUM_REGISTRY=` entry carries the case's value, and adds `--registry <flag>` when the case sets one.

| Case | `defaults.registry` | `PODIUM_REGISTRY` | `--registry` | Expected third `publish-ran` line |
|:--|:--|:--|:--|:--|
| `env-only` | omitted | `regA` | unset | `filepath.Clean(regA)` |
| `env-over-file` | `regB` | `regA` | unset | `filepath.Clean(regA)` |
| `flag-over-env` | omitted | `filepath.Join(ws, "missing")` | `regA` | `filepath.Clean(regA)` |

Each case asserts `res.Exit == 0`, that `<target>/.podium/sync.lock` exists, and that the third line of `publish-ran` equals the expected value. The `env-only` case pins the Pattern A configuration, which exits 2 with `config.no_registry` before CODE-1. The `flag-over-env` case points the env var at a missing directory, so a run that read it would fail.

Run all three with `go test ./test/e2e/ -run 'TestPublishing_Workspace|TestPublishing_ConfigRegistrySourcePrecedence' -v` and confirm none reports `SKIP`. Run `make speccov-drift` to confirm the citations register against §7.5.2.

## Documentation changes

**DOC-1.** `docs/consuming/publishing.md`. Lands in S6. Add no new table column and no standalone paragraph. Leave the `workflow` row of the marketplace target table unchanged. Follow `doc-style.md`: no counts and no "X, not Y" constructions.

(1) Injected variables table. Replace the `$PODIUM_WORKDIR` row:

> | `$PODIUM_WORKDIR` | The per-target working and checkout directory. |

with:

> | `$PODIUM_WORKDIR` | For a marketplace target, the per-target working and checkout directory. For a `kind: workspace` target, the target directory made absolute against the working directory of `podium sync`. |

Insert after the `$PODIUM_OUTPUT_ID` row:

> | `$PODIUM_TARGET_ID` | The `kind: workspace` target's `id`. A workspace target receives it in place of `$PODIUM_OUTPUT_ID`. |

Replace the last row:

> | `$PODIUM_REGISTRY`, `$PODIUM_IDENTITY`, `$PODIUM_HARNESSES` | The registry URL, the publishing identity, and the harness set. |

with:

> | `$PODIUM_REGISTRY`, `$PODIUM_IDENTITY`, `$PODIUM_HARNESSES` | The registry source, which is the `--registry` flag, else the `PODIUM_REGISTRY` environment variable, else `defaults.registry`, and is a URL or a filesystem path; the publishing identity; and the comma-separated harness set. A `kind: workspace` target receives `$PODIUM_REGISTRY` and neither of the others. |

(2) Execution-semantics paragraph under "Command form and execution semantics". Replace:

> Each phase also accepts an optional `prepare_on_error` or `publish_on_error` cleanup list, run when that phase fails, before the failure propagates. The pipeline inherits the ambient environment of the `podium sync` process and adds the injected variables, so git authentication relies on the ambient `SSH_AUTH_SOCK`, `GH_TOKEN`, and similar.

with:

> Each phase also accepts an optional `prepare_on_error` or `publish_on_error` cleanup list, run when that phase fails, before the failure propagates. A cleanup list receives the injected variables of the phase it cleans up, so `publish_on_error` sees `$PODIUM_CHANGED` and `prepare_on_error` does not. The pipeline inherits the ambient environment of the `podium sync` process and adds the injected variables, so git authentication relies on the ambient `SSH_AUTH_SOCK`, `GH_TOKEN`, and similar. An injected variable replaces an ambient variable of the same name, and a variable Podium does not inject for a phase keeps its ambient value.

(3) The `--dry-run` row of the flag table under "Running `podium sync`". Replace:

> | `--dry-run` | Render into a temporary directory and print each command with variables substituted; run no publish phase. |

with:

> | `--dry-run` | For a marketplace target, render into a temporary directory and print each command with variables substituted, and run no publish phase. For a `kind: workspace` target, resolve the artifact set without writing, and run and print no workflow command. |

The `--check` row ("Validate the config only; render and run nothing.") already holds for both kinds and is unchanged.

**CL-1.** `CHANGELOG.md`, `[Unreleased]`, the existing `### Documentation` group. Lands in S7. Append a plain declarative bullet, wrapped like its neighbours, with no bold label:

> - §7.5.2 defines the variables Podium injects into a `kind: workspace` target's workflow: `$PODIUM_WORKDIR`, the absolute target directory; `$PODIUM_TARGET_ID`, the target entry's `id`; and `$PODIUM_REGISTRY`, the registry source. The `publish` phase also receives `$PODIUM_CHANGED`, and each `on_error` list receives the variables of its phase. A workspace workflow runs no command under `--dry-run` or `--check`. §7.8 and `docs/consuming/publishing.md` describe `$PODIUM_REGISTRY` as a URL or a filesystem path. The variable names and phases are unchanged; the Fixed entry below records the registry-source precedence change.

In the `[Unreleased]` existing `### Fixed` group, append:

> - `podium sync --config` reads the `PODIUM_REGISTRY` environment variable when no `--registry` flag is set, ahead of `defaults.registry`, as the §7.5.2 precedence requires. A CI job that supplies the registry only through `PODIUM_REGISTRY`, as the §7.8 scheduled-publish example does, no longer fails with `config.no_registry`, and a `PODIUM_REGISTRY` exported alongside a config that sets `defaults.registry` now takes precedence over it.

## Open questions

**OQ-1. Relative `$PODIUM_REGISTRY` under a relative `--config` path.** `podium sync --config .podium/sync.yaml` with `defaults.registry: ./.podium/registry/` leaves the workspace, and therefore `$PODIUM_REGISTRY`, relative (`cmd/podium/main.go` derives the workspace with `filepath.Dir(filepath.Dir(configPath))`). The draft says the value "resolves against the workspace as §13.11.2 describes" and does not promise an absolute path. The alternative is to state the relative case explicitly in §7.5.2, which commits the spec to the current behavior. A third option, making the value absolute, is a code change and a non-goal here. The draft stages the first option.

**OQ-2. Whether `podium sync --config` reads `PODIUM_REGISTRY`.** The code skips the variable on the `--config` path (`pkg/sync/resolve.go:393`, `cmd/podium/main.go:495-499`), while the §7.5.2 precedence list (`spec/07-external-integration.md:326-333`), the §7.8 Pattern A example (`spec/07-external-integration.md:924-928`), the docs CI example (`docs/consuming/publishing.md:248-251`), and `docs/reference/error-codes.md:68` all assume it reads it. Option (1) changes the code to read it between the flag and `defaults.registry` (CODE-1, TEST-3) and leaves those texts as they are. Option (2) keeps the code and writes a `--config` carve-out into §7.5.2, then rewrites the Pattern A example, the docs CI example, and the error-codes row, and adds a DOC-1 statement that `--config` ignores the variable. The draft stages option (1), because the spec is the source of truth and option (1) is the change that leaves the documented CI configuration working. Either answer must leave §7.5.2 with a single precedence rule for the `--config` registry source, and every example that runs `podium sync --config` must configure the registry through a source that rule honors.

## Non-goals

- Changing any injected variable's derivation, phase, or name in `cmd/podium` or `pkg/sync` beyond the CODE-1 registry precedence, including renaming `$PODIUM_TARGET_ID` to `$PODIUM_OUTPUT_ID` or the reverse.
- Making a workspace `--dry-run` print or substitute workflow commands the way the §7.8 marketplace dry-run does.
- Injecting marketplace-only variables (the publishing identity, the harness set, the git remote and branch, the commit message, or the change summary) into a workspace workflow.
- Making `$PODIUM_REGISTRY` always absolute or always a URL. A relative `--config` path can leave it relative, and this proposal documents the value as it is.
- Applying the other §7.5.2 file scopes (`sync.local.yaml`, `~/.podium/sync.yaml`) to the `--config` path. `runMultiTargetSync` reads only the named file, and CODE-1 does not change that.
- Restructuring the §7.8 injected-variable list beyond the one bullet SPEC-2 rewords.
- Re-specifying `$PODIUM_CHANGED`, which proposal 0036 defined.
- A manual-validation scenario. The documentation edits alter no observable output, the CODE-1 change is an environment-variable lookup that TEST-3 drives through the compiled binary, and TEST-1 and TEST-2 read the injected values directly.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-03, automated)

- **Fixture signature change missed the second caller.** `writeSyncConfigWorkspaceWorkflow` is also called by `TestPublishing_WorkspaceCollisionPublishesThenFails` (`test/e2e/publishing_test.go:973`). The fixture subsection now names both callers passing `""`, states that the collision test's `publish-ran` existence check (`:979`) still holds and its annotations stay, the S3 checklist step names both call sites, and Watch out for records the single-package compile risk.
- **The `--config` registry rule skipped `PODIUM_REGISTRY`, contradicting the §7.5.2 precedence and the §7.8 and docs CI examples.** Staged option (1) of the finding and recorded the choice as OQ-2: new CODE-1 section (`runMultiTargetSync` falls back to `PODIUM_REGISTRY`), new TEST-3 with a flag/env/file precedence table, checklist step S5 (DOC-1 and CL-1 move to S6 and S7), and the fixture's omitted-registry form. SPEC-1, the Summary, Fixed decisions, Decisions, Current state, the edge-case table, DOC-1(1), CL-1 (new Fixed bullet), Testing, and Non-goals now state the order flag, then `PODIUM_REGISTRY`, then `defaults.registry`. The non-goal that accepted the skip is replaced by one that keeps the named-file-only reading.
- **Statements that the injected values are unchanged contradicted CODE-1.** CODE-1 changes the value `runWorkspaceTarget` injects as `$PODIUM_REGISTRY` (`cmd/podium/main.go:558`) when `PODIUM_REGISTRY` is exported alongside `defaults.registry`, as the CL-1 Fixed bullet and the TEST-3 `env-over-file` case record. The Fixed decisions bullet now says the spec describes the variable map and that `$PODIUM_REGISTRY` takes the CODE-1 registry source. The first Decisions bullet is scoped to names, phases, and derivations other than the registry source. The CL-1 Documentation bullet now ends by deferring the precedence change to the Fixed entry.
