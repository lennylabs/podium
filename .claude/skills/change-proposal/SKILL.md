---
name: change-proposal
description: Write an adversarially validated change proposal under proposals/, staging spec edits and/or core-product or test-infrastructure code changes, from an inline problem statement, or adversarially review and fix an existing proposal until it converges. Use when the user reports a spec or implementation defect, contradiction, or gap, asks for a fix or extension proposal, or asks to validate an existing proposal before sign-off. The proposal stages its changes for sign-off; it never modifies spec/, pkg/, or docs/ itself.
argument-hint: <problem statement | path to notes | path to proposals/*.md>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Change proposal writer and convergence loop

This skill produces a reviewed proposal document in `proposals/` and converges it against the spec and the code. A proposal may stage spec edits, core-product code changes, test-infrastructure changes, or a combination; the review loop validates whichever it stages. It has two modes sharing one workflow:

- **new**: the input is a problem statement. The workflow validates the problem's premises, drafts a change set, adversarially challenges each change, writes the proposal file, and then enters the review loop.
- **review**: the input is the path of an existing proposal under `proposals/`. The workflow enters the review loop directly.

The review loop is shared: rounds of multi-lens adversarial review, two-skeptic verification of every finding, and fixes, repeated until two consecutive rounds confirm zero findings.

## Hard constraints

- The run creates or edits exactly one file: the proposal (the new file in new mode, the given file in review mode). Nothing under `spec/`, `docs/`, `pkg/`, `cmd/`, `internal/`, or `sdks/` is modified. A proposal stages its changes (spec amendments and/or code or test changes) as quoted spec text and precise change descriptions to apply after sign-off.
- All problem input is inline (the argument and the conversation). The skill reads no tracking documents; evidence comes from `spec/`, `pkg/`, `cmd/`, `internal/`, `sdks/`, `docs/`, and git history. Progress-tracking and audit prose elsewhere in the repository are leads to verify, never evidence. The spec is the source of truth, and `make coverage-gate` (lint, `speccov-drift`, `matrix-audit`, `doccov-check`, `coverage-budget`) is the consistency gate.
- Prose follows `.claude/rules/doc-style.md`.
- New proposal file names are `NNNN-<kebab-slug>.md`, matching the existing files in `proposals/` (e.g. `0003-multi-harness-marketplace-publishing.md`). `NNNN` is the next free zero-padded number among existing numbered proposals. The draft still records a `kind` (a `fix` corrects or reconciles existing behavior, a `new` adds a capability the spec or implementation lacks) to frame the proposal, but the kind is not part of the filename.
- Review findings are real errors only: false citations, infeasible actor assignments, contradictions, missing edit sites, and broken mechanisms. Style preferences, optional improvements, and hypothetical hardening are excluded by construction (the materiality skeptic refuses them); conventions are handled by a dedicated one-shot pass outside the error loop.

## Proposal format conventions

The writer and reviewer agents receive these conventions. They are derived from the existing files in `proposals/`; when those files and this list disagree, the existing files win.

- Title line: `# Proposal NNNN: <Title>`.
- Header bullets: `- Issue:` (a number like `#19` or `(to be filed)`), `- Status:`, `- Date:` (the release-day `YYYY-MM-DD`).
- Status lifecycle: the writer creates the proposal with `Status: Draft`; the workflow replaces it with "Verified (<date>). Converged after N adversarial review rounds (M findings fixed); awaiting sign-off." when the review loop converges, and leaves it untouched otherwise. A human records sign-off by setting the status to `Approved`; `implement-proposal` acts on an approved proposal and then sets `Implemented`.
- Sections, omitting the ones with no content: `## Summary`; `## Current state and the gap` (or `## Decisions`); per-target staged spec edits as `## Spec amendment: §X.Y <topic>` sections; `## Proposed solution` describing the staged code and test changes when the proposal stages code; `## Open questions` for decisions that belong to the human reviewer; `## Documentation changes` when docs/ pages must follow the change; `## Relationship to proposal NNNN` when one exists; and `## Resolved in adversarial review`, which the review-loop fixer populates.
- The Summary and gap sections cite spec text by `§X.Y` and cite code by `pkg/...`, `internal/...`, or `cmd/...` paths. The quoted text inside a `## Spec amendment:` blockquote stays behavioral and references no code path, because that text lands verbatim in the spec.
- Each `## Spec amendment: §X.Y` section quotes the exact replacement or added spec text in a `>` blockquote and gives a precise anchor description ("Replace the §6.3 bullet …", "Append a new subsection §6.7.2 …"). The quoted spec text is behavioral prose only: it never references a source-code path and cross-references other spec content by `§N.M`, never by line number, because the spec itself follows those rules.
- `## Open questions` records the alternatives that were considered and dropped, with the reason.

## Why the review loop is built this way

These design points come from convergence runs on prior proposals in this repository. Keep them when editing the script.

- **Two consecutive clean rounds, not one.** Fix rounds introduce their own errors: fixers add predicate text that drifts from the design's invariants, and clean rounds have been followed by rounds with confirmed findings in fixer-written text. A single clean round demonstrates nothing about the text the previous fixer wrote.
- **Two skeptics per finding, both must confirm.** One re-derives the evidence from the files; one judges materiality assuming the evidence is true, with instructions to default to refuted. The split kills plausible-but-wrong findings and nitpicks separately.
- **Refuted findings are remembered and injected into later rounds.** Without the memory, a refuted finding resurfaces in a later round, wastes verification, and can block convergence.
- **Dedup before verification.** Independent lenses converge on the same root error under different phrasings; verifying duplicates multiplies cost for nothing.
- **Security, harness compatibility, performance, reliability, client-facing surfaces, and documentation alignment are always-on fixed lenses.** Every round runs the structural lenses (citations, feasibility, edit-sites, mechanism) plus the non-functional lenses that must be present every round: `security` (control regression AND the trust boundary of any security-bounding value), `harness` (every change is consistent and complete across all supported harnesses' capabilities and native layouts), `performance` (read/write rates against the stated load, and store-failure survival), `reliability` (recovery, retry, and idempotency correctness under crash, restart, redelivery, and store failover), `client-surface` (client-facing contract integrity across the HTTP API, SDKs, MCP meta-tools, and CLI), and `docs-alignment` (the docs/ tree reflects every changed behavior and never drives a spec or product decision). The `harness` lens is always-on because a single change to one harness's adapter, target path, or capability cell routinely needs a matching edit across the §6.7 path table, the §6.7.1 capability matrix in both `spec/06` and `pkg/adapter/capability.go`, the adapter implementation, the `test/materialization` golden files, the `matrix-audit` cells, and the per-harness docs; the structural lenses check internal edit completeness but not whether each harness's native format and the cross-harness core feature set still hold. The `reliability` lens bounds against its neighbors: the performance lens keeps the capacity and state-survival math, and the security lens keeps fail-closed on security paths and the trust boundary of security-bounding values, so reliability owns recovery-mechanism and idempotency correctness alone. The `client-surface` lens verifies that a change to one client-facing representation (the registry HTTP API, the MCP meta-tool schemas, the per-language SDKs, client-visible error codes and audit events, and the client-facing docs) is mirrored across every parallel representation, and that a name an external standard defines is not renamed while one client vocabulary is left half-renamed; the `edit-sites` lens checks internal edit completeness but not the parallel-client-representation and standard-vs-Podium-defined boundary this lens owns. The `docs-alignment` lens verifies that every behavior the proposal changes is mirrored in the docs/ pages that describe it, under the rule that docs follow the spec and the implementation and are never used to decide what the spec or product should be; a doc-described scenario may seed a test case only after the doc is verified against the spec. Do not demote any of them to rotating.
- **A rotating extra lens, on top of the fixed set.** The fixed lenses develop shared blind spots over rounds because they re-read the same document. One extra lens rotates per round (operational consistency, then fresh holistic read) and has found confirmed errors in rounds the fixed lenses passed.

## Error classes with a record of surviving verification

The lens prompts in the script enumerate these. They are the classes that have produced confirmed findings; extend the list when a new class surfaces.

- A named actor that does not exist, or an actor assigned an action it cannot perform under the §9.1 SPI ownership boundaries or the §9.3 forward-compatibility constraints (a method that is not context-aware, not wire-serializable, or relies on shared in-process state).
- A check placed at a component that cannot see the data it needs (an adapter, which runs at materialization time and writes project-level files only, evaluating identity or a registry-side concern; a client-side identity provider asked to verify a forwarded token that only a registry-process provider can).
- A data-flow direction asserted backwards (which side of a mirror is authoritative — the §6.7.1 capability matrix lives in both `spec/06` and `pkg/adapter/capability.go`; verify in the code, never from memory).
- A mandatory gate that one write path bypasses (a `target_harnesses:` capability check enforced at ingest-lint but not at materialization, or vice versa; a visibility filter a path skips).
- Deployment-mode divergence: a change that makes the standard, standalone, and filesystem-registry modes produce different materialized output, violating the §2.2 shared-library invariant and the §11 equivalence test.
- Build-phase ordering violations: a `spec/10` deliverable depending on artifacts a later phase introduces.
- Edits to a generated or mirrored artifact instead of its authoring source: a §6.7.1 capability cell changed in the spec without the `pkg/adapter/capability.go` mirror, or a harness output path changed without the `test/materialization` golden file.
- A recovery or retry mechanism that fails under the exact fault it handles: a non-idempotent ingest that violates the §4 immutability rule (same id+version, different content, must return `ingest.immutable_violation`); a webhook (at-least-once) consumer with no dedup or signature-verification guard; config-merge or inject reconciliation that duplicates or orphans Podium-owned entries on re-sync; atomic materialization that leaves a partial tree; a JWKS refresh that does not fail closed; an outbound call (Git fetch, IdP, object store, embedding provider) with no timeout or bounded backoff.
- Predicate drift: a condition stated with different conjuncts across the proposal's design prose, a summary table, the quoted spec text, `pkg/adapter/capability.go`, and the tests.
- Missing companion edit sites: an error code without its `§6.10` entry and its §6.10 `matrix-audit` cell; a harness/type/field/rule-mode/hook-event change without its §6.7 path table, §6.7.1 matrix, `capability.go` mirror, and golden file; a runnable doc example without its `tools/doccov/manifest.yaml` entry and doc-e2e test; a spec section with no test citing it (`// Spec: §X.Y`).
- A client-facing contract changed in one representation but not its parallels: a REST field missing from a language SDK (`sdks/podium-py`, `sdks/podium-ts`) or the docs; an MCP meta-tool schema change missing from an SDK or doc; a removed or renamed client-facing field still advertised by a served schema, an SDK, or a doc; a standard-defined name (an MCP primitive, an OAuth/OIDC claim, a harness's native config key) renamed, or one client vocabulary left half-renamed across the surface.
- A field defined in one spec section cross-referenced as living in another.
- An error code or audit event referenced by the proposal that no spec-defined surface emits.
- A behavior the proposal changes (a renamed or removed field, a changed default, a new error code, env var, CLI flag, meta-tool, harness output, or audit event) left undocumented or misdescribed in the `docs/` page that covers it. The fix is always a docs edit; docs follow the spec and never drive a spec or product change.

## Procedure

### Step 1: Determine the mode and assemble inputs (inline, before the workflow)

1. If the argument is the path of an existing file under `proposals/`, the mode is **review**. Otherwise the mode is **new** and the argument (plus the conversation) is the problem statement.
2. Common inputs:
   - `date`: today's date as `YYYY-MM-DD` (workflow scripts cannot call Date).
   - `repoRoot`: optional; the repository root, defaulting to the working directory. Omit it, or pass `.`, when running from the repo root.
   - `exemplar`: the path of the highest-numbered existing proposal (in review mode, excluding the proposal under review).
   - `maxReviewRounds`: default 12.
3. New mode only:
   - Read the spec sections and code paths the problem names so the dossier carries concrete citations rather than paraphrase.
   - `problem`: a problem dossier of one to three paragraphs stating the problem.
   - `context`: a block listing every citation gathered so far (spec `§X.Y`, code file:line, prior conversation conclusions). Distinguish established facts from unverified claims; the workflow re-verifies both.
   - `nextNumber`: list `proposals/`, take the highest `NNNN-` prefix among numbered files, add one, zero-pad to four digits. Ignore unnumbered files.
4. Review mode only:
   - `proposalPath`: the path of the proposal under review, relative to the repository root.
   - `context`: a short list of the spec sections and code packages the proposal touches, with approximate line anchors, gathered by grepping for the proposal's main identifiers. This focuses reviewers; they re-verify everything themselves.

### Step 2: Run the workflow

The workflow script lives at `.claude/workflows/change-proposal.js` and is invoked by name. Call `Workflow({name: "change-proposal", args: …})` with the mode-appropriate args:

```json
{
  "mode": "new",
  "problem": "<the problem dossier>",
  "context": "<citations and prior conclusions>",
  "date": "<YYYY-MM-DD>",
  "nextNumber": "<NNNN>",
  "exemplar": "proposals/<highest-numbered proposal>.md",
  "repoRoot": ".",
  "maxReviewRounds": 12
}
```

```json
{
  "mode": "review",
  "proposalPath": "proposals/<file>.md",
  "context": "<assembled notes, or empty>",
  "date": "<YYYY-MM-DD>",
  "exemplar": "proposals/<highest-numbered other proposal>.md",
  "repoRoot": ".",
  "maxReviewRounds": 12
}
```

Pass `args` as a JSON object value in the tool call. The script tolerates a JSON-encoded object string by parsing it; anything else aborts on the args guard.

Agents inherit the session model and effort level. Run this skill with the strongest available model at high effort; reviewer quality determines whether the loop converges on truth or on exhaustion.

### Step 3: Interruptions and non-convergence

- On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. Completed agents replay from the journal cache and the run continues live from the cut point.
- On hitting `maxReviewRounds` without two consecutive clean rounds: inspect the trajectory in the returned `review.history`. If the confirmed-finding counts are decreasing and the last round was clean, raise `maxReviewRounds` in the persisted script file's default or pass a larger value, and resume with `{scriptPath, resumeFromRunId}`; the edit does not invalidate the cached prefix. If counts are flat or oscillating, stop and report the recurring findings for a human decision instead of burning rounds.

### Step 4: Report

1. Run `git status --porcelain` and confirm the only created or modified file is the proposal. If anything under `spec/` or any other path changed, restore it and report the violation.
2. On `status: "written"` or `status: "reviewed"`: read the proposal, then report the file path, the title, the refuted premises and dropped changes with reasons (new mode), whether the loop converged, the rounds run, the findings fixed per round with their titles, and the findings the skeptics refuted. On convergence the workflow has set the proposal's Status bullet to "Verified (<date>) …; awaiting sign-off."; state that the next step is sign-off, which records an approved state, after which `implement-proposal` lands the staged edits in `spec/` and implements the code. Without convergence the Status bullet is unchanged; say so.
3. On `status: "not-viable"` or `status: "no-change-needed"`: no file is written. Report the refuting evidence so the user can correct or withdraw the problem statement.
4. Do not apply any staged edit to `spec/`, and do not commit, unless the user asks.

## Maintenance

The workflow script is canonical at `.claude/workflows/change-proposal.js`; this file carries the procedure, conventions, and rationale only. Other workflows invoke the script by name (`workflow("change-proposal", args)`), so script edits must keep the args contract stable. When a convergence run surfaces a confirmed error class this file does not list, add it to the error-class list here and, when it fits an existing lens, to that lens's prompt in the script. Keep the finding bar's DO-NOT-report list intact; it is what keeps the loop from converging on nitpicks. When the proposal format conventions and the existing files in `proposals/` disagree, the existing files win; update the conventions list here.
