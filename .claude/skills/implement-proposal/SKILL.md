---
name: implement-proposal
description: Implement an approved spec proposal end to end. Applies the proposal's staged spec amendments to spec/ and verifies them, then (by default) implements the spec change in code via the build subworkflow. Pass "spec-only" to land and verify the spec without touching code. Use after a proposal is approved.
argument-hint: <path to proposals/*.md> [spec-only]
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Implement proposal

This skill takes an approved spec proposal and carries it through to implementation. It applies the staged spec amendments to `spec/` and verifies exact alignment, then implements the spec change in code through the `implement-proposal-build` subworkflow (blast radius, ordered build sequence, step-by-step implementation with tests). The spec always lands and is verified before any code.

It is the implementation stage of the proposal pipeline: `change-proposal` writes and converges a proposal, a human approves it, and `implement-proposal` lands the spec and implements the code. The proposal is the unit of work, and `make coverage-gate` keeps the spec, the §6.7.1 capability matrix, the docs, and the rest of the surface consistent.

## Modes

- **Full (default)** — apply and verify the spec, then implement the code. Invoke with the proposal path alone.
- **Apply-only** — apply and verify the spec, commit it, and stop. Invoke with `spec-only` after the path. Use it when the code lands separately, or to review the applied spec before committing to the code phase.

## Hard constraints

- Spec first. The staged spec amendments are applied and verified, and committed as their own commit, before any code is written.
- Spec amendments are applied only through this skill's verified apply loop, never by hand: the code phase never touches `spec/`, and a re-run confirms the staged amendments are still present before implementing code.
- Spec content rules hold over verbatim application: the spec never references source code paths, and cross-references other spec content by section number, never line number. A new section or subsection is appended at the end of its level and numbered as the next ordinal, never inserted between existing siblings, so existing numbering and cross-references stay stable. The apply loop rephrases or drops staged text that would violate these and records the deviation.
- Code changes follow `.claude/rules/test-coverage.md` and `.claude/rules/doc-style.md`: every behavior traces to a spec section (cite it with a `// Spec: §X.Y` test comment), new and changed code reaches at least 85% line coverage measured across packages, tests run at the highest level the change reaches (up to end-to-end), and they pass.
- The implementation must leave `make coverage-gate` (lint, `speccov-drift`, `matrix-audit`, `doccov-check`, `coverage-budget`) green for the change: a new spec section needs a test citing it, a new matrix cell needs a `// Matrix:` annotated test, and a new runnable doc example needs its `tools/doccov/manifest.yaml` entry.

## Procedure

### Step 1: Preconditions (inline, before the workflow)

1. Resolve the proposal path and the optional `spec-only` modifier from the arguments. Read the proposal's Status bullet. It must begin "Approved" (or "Applied to spec" for an idempotent re-run). A "Draft" or "Verified" status is not approved: stop and report that it needs sign-off.
2. Run `git status --porcelain -- spec/`. The apply verification diffs the working tree against a clean `spec/` baseline, so `spec/` must be clean. If it is dirty, stop and report.
3. Compute `date` (today's `YYYY-MM-DD`; workflow scripts cannot call Date). `repoRoot` is optional and defaults to the working directory (the repo root); omit it or pass `.` when running from the repo root.

### Step 2: Run the workflow

The script lives at `.claude/workflows/implement-proposal.js` (subworkflow at `.claude/workflows/implement-proposal-build.js`), invoked by name:

```json
{
  "proposalPath": "proposals/<file>.md",
  "repoRoot": ".",
  "date": "<YYYY-MM-DD>",
  "implementCode": true
}
```

Set `implementCode` to `false` for spec-only; otherwise every test level each step and the final verify reach is run. Agents inherit the session model and effort level; run this skill with a strong model at high effort.

### Step 3: Interruptions

On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. The spec apply commits before the code phase and the build steps commit per step, so a resumed run re-plans against the current tree; confirm the plan accounts for work already committed before letting it re-run.

### Step 4: Report

1. Run `git status --porcelain` and `git log --oneline` for the run's commits. Confirm the spec landed as its own commit and the code changes are under `pkg/`, `cmd/`, `internal/`, `sdks/`, `test/`, and `docs/`.
2. On `status: "implemented"`: report the spec commit, the blast radius, the build steps with their commits, the final green status, the changed-line coverage, and that the design-conformance review came back clean. Suggest pushing; do not push unless asked.
3. On `status: "spec-only"`: the spec is landed, verified, and committed; report it and the `nonSpecStaged` changes left for the code stage.
4. On `status: "implemented-not-green"`: report which test levels failed, whether coverage fell below the floor, and any unresolved design-conformance findings (`reviewFindings`); the commits remain on the branch. The `resumeNote` says how to continue.
5. On `status: "build-step-stuck"`: a build step stayed red after `maxStepAttempts`, so the sequence aborted before its dependents. Report the `stuckStep`, the spec and partial code commits on the branch, and the `resumeNote`.
6. On `status: "not-approved"`, `"spec-not-clean"`, `"spec-applied-with-blockers"`, or `"not-aligned"`: report the reason. `spec-not-clean` and `spec-applied-with-blockers` leave the spec amendments in the working tree for inspection; `not-aligned` means a re-run found the spec drifted from the proposal.
7. Do not push or open a PR unless the user asks.

## The subworkflow

`implement-proposal-build` is the code-implementation engine, stored separately so it is reusable and matches the plan-then-build structure:

1. **Plan** — a planner reads the proposal in full (spec amendments already landed) and greps the codebase for every surface the change touches, producing the blast radius and an ordered build sequence. The blast radius and sequence cover the surfaces the proposal **removes** as well as those it adds: every eliminated mode, field, function, env var, error code, adapter value, or file gets an explicit removal step that also deletes the code, tests, golden files, and doc references orphaned by it, so no removed surface is left compiling-but-dead. When the change touches a harness, an artifact type, a field, a rule mode, a hook event, or a target path, the plan keeps every parallel representation consistent (the §6.7 path table, the `pkg/adapter/capability.go` matrix, the `test/materialization` golden files, the `tools/matrix/matrices.go` cells, and the per-harness docs). A completeness critic checks the plan covers the whole proposal (including the removals and the parallel representations), and sequences prerequisites first; the plan revises once if it finds gaps.
2. **Build** — each step is implemented in order (sequential, because later steps depend on earlier ones and share the working tree). Each step is **gated before the sequence advances** by an inner loop: an implementer writes the code and tests, an **independent agent verifies** the step's tests are green (the implementer's self-report is advisory), and an **adversarial design-conformance review** reads the step's own diff against the proposal through two lenses (design conformance, and named invariants and edge cases) scoped to that step — a surface this step adds for a later step to consume is out of scope. The step advances only when it is both green and review-clean; otherwise the loop fixes and re-checks, bounded by `maxStepAttempts` (default 50). Catching a divergence at the step that introduced it is cheaper than catching it in the final review and keeps a wrong foundation from propagating. If a step is still red or divergent after the cap, the sequence **aborts** rather than building dependent steps on it: the subworkflow returns `status: "build-step-stuck"` with the `stuckStep`, the reason, and a `resumeNote`, and the spec and completed steps stay committed. The plan is a prediction made before any code exists, so the Build phase re-checks it as reality lands: every `replanEvery` completed steps (default 4) and after any step that struggled (took at least `replanStruggleAttempts` attempts), a read-only critic checks whether the **remaining** plan still matches what was built — an unplanned surface that got touched, a removal that orphaned more than foreseen, a step now redundant or mis-sequenced. On evidenced drift it re-plans the remaining steps **forward-only**: completed steps are immutable, only the not-yet-built tail is replaced, bounded by `maxReplans` (default 6). This is distinct from the per-step review, which judges completed work rather than the correctness of the plan ahead.
3. **Verify** — a final pass runs the reached test levels across the whole change (`go build`, `go vet`, `golangci-lint`, the package tests, and the live-service levels via `make services-up` when needed), runs the project consistency gate `make coverage-gate` (lint, `speccov-drift`, `matrix-audit`, `doccov-check`, `coverage-budget`), reports changed-line coverage, and runs a **dead-code sweep** (grep for every removed identifier, mode, field, function, env var, error code, adapter value, and §6.7.1 capability cell and confirm none survives as a live reference; a surviving removed surface, orphaned caller, or stale golden file is a failure). It also applies a **coverage gate**: `green` requires every reached level to pass *and* changed-line coverage at the floor (`coverageFloor`, default 85%), with a behavior-preserving refactor exempt; below-floor coverage is a failure the fix loop closes by adding tests. It iterates fix-and-re-run until green, the sweep is clean, and coverage meets the floor, bounded by `maxVerifyRounds` (default 25, with an early exit when a non-green result lists no actionable failures). The standing dead-code rule is in the build subworkflow's injected rules and applies to every step.
4. **Review** — each step was already reviewed against its own diff during Build; this final pass reads the **whole change** against the pre-implementation baseline to catch what a step-scoped review cannot — cross-step interactions, a surface one step added and another was meant to consume, and whole-change completeness — through three lenses (design conformance, named invariants and edge cases, and blast-radius completeness). A fix round applies the findings and the loop re-reviews until clean, bounded by `maxReviewRounds` (default 50); a non-trivial review re-confirms green afterward. The run is `implemented` only when the build is green **and** the review is clean (`reviewClean`); otherwise it returns `implemented-not-green` with the unresolved `reviewFindings`.

## Maintenance

The workflow scripts are canonical at `.claude/workflows/implement-proposal.js` and `.claude/workflows/implement-proposal-build.js`; this file carries the procedure and rationale only. Keep the subworkflow description here in sync with the script. When the implementation surfaces a recurring planning or sequencing gap, strengthen the completeness-critic prompt in the subworkflow.
