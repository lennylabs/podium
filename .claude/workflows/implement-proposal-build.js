// Implementation subworkflow for implement-proposal. Given an applied spec
// proposal, it identifies the blast radius, plans an ordered build
// sequence, then implements the sequence one step at a time. Each step is
// gated before the sequence advances: an implementer writes the code and
// tests, an independent agent verifies the step's tests are green, and an
// adversarial review checks the step's diff conforms to the proposal's
// design. A step that does not reach green-and-conformant within
// maxStepAttempts aborts the sequence so a divergent or red step does not
// compound into its dependents. Periodically (every replanEvery steps, or
// after a step that struggled) a read-only critic checks whether the
// remaining plan still matches what landed and, on evidenced drift,
// re-plans the remaining steps forward-only (completed steps immutable).
// After the build, a final verification loop fixes and re-runs until the
// reached test levels are green and changed-line coverage meets the floor
// (coverageFloor, default 85% per .claude/rules/test-coverage.md), and a
// final cross-step design-conformance review of the cumulative diff catches
// anything the per-step reviews could not see, fixing any divergence before
// returning. It does NOT verify the spec is applied — the implement-proposal
// parent does that around this call.
//
// Invoked as a sub-step: workflow("implement-proposal-build", { proposalPath,
// date, repoRoot }). Runs only agents (no nested workflow()).
//
// MAINTENANCE: the implement-proposal skill (.claude/skills/implement-proposal)
// documents this subworkflow; keep its description in sync.

export const meta = {
  name: "implement-proposal-build",
  description:
    "Plan the blast radius and build sequence of an applied spec proposal, then implement it one step at a time with tests, then verify",
  phases: [
    { title: "Plan", detail: "blast radius + ordered build sequence, completeness-checked" },
    { title: "Build", detail: "implement each step in order; verify its tests and adversarially review its diff before advancing; periodically re-plan the remaining steps on drift; abort if a step stays red or divergent" },
    { title: "Verify", detail: "run the reached test levels across the whole change, fixing until green and coverage meets the floor" },
    { title: "Review", detail: "final cross-step design-conformance review of the cumulative diff against the proposal, fix divergences" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.proposalPath) {
  throw new Error("args.proposalPath is required");
}
const repo = input.repoRoot || ".";
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const maxPlanRounds = input.maxPlanRounds || 2;
const maxStepAttempts = input.maxStepAttempts || 50;
const maxVerifyRounds = input.maxVerifyRounds || 25;
const maxReviewRounds = input.maxReviewRounds || 50;
// New/changed code reaches 85% line coverage per .claude/rules/test-coverage.md.
const coverageFloor = input.coverageFloor || 85;
// Periodic plan-drift re-check during Build: every replanEvery completed
// steps (and after any step that took at least replanStruggleAttempts
// attempts), a read-only critic checks whether the remaining plan still
// matches reality; on evidenced drift, the remaining steps are re-planned
// (forward-only, completed steps immutable), bounded by maxReplans.
const replanEvery = input.replanEvery || 4;
const maxReplans = input.maxReplans || 6;
const replanStruggleAttempts = input.replanStruggleAttempts || 4;

const RULES =
  "Follow the project rules in " +
  repo +
  "/.claude/rules/test-coverage.md (new and changed code carries tests at the highest level its behavior reaches, up to end-to-end; ≥85% line coverage on new code measured across packages with `go test -coverpkg=./... -coverprofile=cover.out ./...`; boot-path and CLI behavior covered by an end-to-end test under test/e2e/) and " +
  repo +
  "/.claude/rules/doc-style.md for any prose or comments. Write idiomatic Go that matches the surrounding code: small single-purpose functions, reuse over duplication, wrapped errors, context propagation, fail-closed security, and no backward-compat shims (Podium is pre-1.0). REMOVE DEAD CODE: when the proposal eliminates a surface (a mode, field, function, struct, env var, error code, meta-tool, adapter value, §6.7.1 capability cell, or whole file), delete it together with every code path, helper, test, fixture, golden file, doc reference, and identifier that becomes unreferenced as a result; never leave a removed surface compiling-but-dead or two implementations side by side. A removal is part of the change, not a follow-up. Do not hand-edit files under spec/ — the spec is already applied and committed, and the code phase never touches spec/." +
  "\n\nPER-STEP VERIFICATION — TEST the step, avoid the 180s no-progress watchdog, and keep test runs scoped. Podium is a Go project tested with `go test` plus the Makefile targets. DO test every step; do NOT skip tests. Follow these: (1) DEFAULT to scoped FOREGROUND verification — do not use `run_in_background` for tests unless a single suite will genuinely exceed the 180s watchdog without output (see rule (4)). (2) SCOPE every command to the changed packages, not the whole repo: `go build ./<changed-pkg>/...`, `go vet ./<changed-pkg>/...`, `golangci-lint run ./<changed-pkg>/...` (or `make lint` whole-repo when fast enough), `go test ./<changed-pkg>/... -count=1`. Scoped runs finish well under 180s; do NOT pipe through `| tail`/`| head` (let output stream). (3) LIVE-SERVICE TESTS: tests that need a real Postgres or S3/MinIO take the live path only when the `PODIUM_POSTGRES_DSN` / `PODIUM_S3_*` env vars point at running services. Start the local docker-compose stack with `make services-up`, then run the live path with `make test-live` (it maps the local-service `LIVE_*` Make variables into the `PODIUM_*` env vars the tests read and serializes package runs with `-p 1`), and `make services-down` when done. A bare `go test` after `services-up` still SKIPS the live tests unless those `PODIUM_*` vars are set, as `make test-live` and a local `test.env` provide; to run one live package in the foreground, set the `PODIUM_POSTGRES_DSN` / `PODIUM_S3_*` vars yourself. The standalone (SQLite/filesystem) paths need no services. A pre-existing whole-repo failure unrelated to this step's changed packages is not this step's failure. (4) LONG-SILENT SUITE EXCEPTION: if and only if a single suite will genuinely run longer than ~150s without emitting any output, launch it with `run_in_background: true` to a log file and poll for completion using the READ TOOL on that log file path — do NOT use a Bash `tail -f`/`until` loop. A Bash poll loop depends on the task `.output` file remaining on disk; if the 180s watchdog kills the foreground Bash wrapper, the harness deletes that `.output` file and the poll loop hangs forever. The Read tool reads the log file directly, immune to that cleanup. (5) HARNESS-ADAPTER STEPS: a step that changes pkg/adapter, pkg/materialize, or the §6.7.1 capability matrix must run the materialization golden and validity tests (`go test ./test/materialization/... -count=1`, no external deps) and the matrix audit (`make matrix-audit`); when the harness-native output legitimately changes, regenerate the golden files with `UPDATE_GOLDEN=1 go test ./test/materialization/` and confirm the diff is exactly the intended change. test/harness_integration (build tag `harness_integration`, gated on a harness binary on PATH) is opt-in and normally skipped." +
  "\n\nBRANCH SAFETY (critical): you MUST stay on the current feature branch and commit ONLY to it. NEVER run `git checkout <branch-or-commit>`, `git switch`, `git reset --hard`, or `git branch -f` — switching the checkout has caused build commits to land on the wrong branch (a real, damaging failure). To inspect a historical commit or the pre-implementation baseline, use `git diff <SHA>..HEAD`, `git diff <SHA> -- <path>`, or `git show <SHA>:<path>`, which read history WITHOUT changing the working tree. Immediately before every `git commit`, confirm `git rev-parse --abbrev-ref HEAD` prints the feature branch (not `HEAD`/detached, and not `main` or any base branch); if it does not, `git checkout <feature-branch>` to return before committing.";

// One build step, shared by the initial plan and the tail re-plan.
const STEP_ITEM = {
  type: "object",
  required: ["id", "title", "work", "targets", "tiers"],
  properties: {
    id: { type: "string", description: "stable short id, e.g. S1" },
    title: { type: "string" },
    work: { type: "string", description: "what to implement in this step" },
    targets: { type: "array", items: { type: "string" }, description: "files or packages" },
    dependsOn: { type: "array", items: { type: "string" } },
    tiers: {
      type: "array",
      items: { type: "string" },
      description:
        "test levels this step must create and run (e.g. unit package tests, integration in test/integration, conformance in test/conformance, materialization golden+validity in test/materialization, e2e in test/e2e)",
    },
    specRefs: { type: "array", items: { type: "string" } },
  },
};

const PLAN = {
  type: "object",
  required: ["blastRadius", "steps"],
  properties: {
    blastRadius: {
      type: "array",
      description:
        "every code, chart, SDK, doc, or test surface the proposal touches, each as 'path or package — what changes and why'",
      items: { type: "string" },
    },
    steps: {
      type: "array",
      description: "ordered build sequence; earlier steps are prerequisites of later ones",
      items: STEP_ITEM,
    },
    risks: { type: "array", items: { type: "string" } },
  },
};

// Plan-drift critic verdict: does the REMAINING plan still match reality?
const DRIFT = {
  type: "object",
  required: ["drift"],
  properties: {
    drift: { type: "boolean" },
    reasons: {
      type: "array",
      description:
        "concrete, evidenced ways the remaining plan no longer matches reality (a touched-but-unplanned surface, an orphaned removal, a now-redundant step, a backwards dependency)",
      items: { type: "string" },
    },
  },
};

// Forward-only re-plan of the remaining steps; completed steps are immutable.
const TAIL = {
  type: "object",
  required: ["steps"],
  properties: {
    steps: {
      type: "array",
      description: "the revised ordered sequence for the REMAINING work only (steps not yet built)",
      items: STEP_ITEM,
    },
    blastRadiusAdditions: {
      type: "array",
      description: "surfaces discovered during the build that the original blast radius missed",
      items: { type: "string" },
    },
    notes: { type: "string" },
  },
};

const CRITIQUE = {
  type: "object",
  required: ["complete", "gaps"],
  properties: {
    complete: { type: "boolean" },
    gaps: {
      type: "array",
      description: "parts of the proposal's blast radius the plan fails to implement, or missing test levels",
      items: { type: "string" },
    },
  },
};

const STEP = {
  type: "object",
  required: ["implemented", "testsPassed", "tiersRun"],
  properties: {
    implemented: { type: "boolean" },
    filesChanged: { type: "array", items: { type: "string" } },
    testsAddedOrModified: { type: "array", items: { type: "string" } },
    tiersRun: { type: "array", items: { type: "string" } },
    testsPassed: { type: "boolean" },
    commit: { type: "string" },
    notes: { type: "string" },
  },
};

const VERIFY = {
  type: "object",
  required: ["green", "tiersRun"],
  properties: {
    green: { type: "boolean" },
    tiersRun: { type: "array", items: { type: "string" } },
    changedLineCoverage: { type: "string" },
    failures: { type: "array", items: { type: "string" } },
    notes: { type: "string" },
  },
};

const REVIEW = {
  type: "object",
  required: ["findings"],
  properties: {
    findings: {
      type: "array",
      items: {
        type: "object",
        required: ["title", "where", "divergence", "fix"],
        properties: {
          title: { type: "string" },
          where: { type: "string", description: "file:line and the proposal section it diverges from" },
          divergence: { type: "string", description: "how the landed code diverges from the proposal's design" },
          fix: { type: "string" },
        },
      },
    },
  },
};

const SHA = {
  type: "object",
  required: ["sha"],
  properties: { sha: { type: "string" } },
};

// ---- Plan: blast radius + ordered build sequence, completeness-checked ----

phase("Plan");
log("Planning the blast radius and build sequence for " + proposal);
let plan = await agent(
  "Plan the code implementation of an applied spec proposal.\n\n" +
    "You are a read-only planner; do not edit any file. Work in the repository root.\n\n" +
    "Proposal: " +
    proposal +
    ". The proposal's spec amendments are ALREADY applied to spec/. Read the proposal in full — every section, especially Summary, Current state and the gap, Decisions, Proposed solution, the '## Spec amendment: §X.Y' sections (now landed in spec/), Documentation changes, and any Testing notes. The proposal defines the complete change.\n\n" +
    "Then map the full blast radius: grep spec/, pkg/, cmd/, internal/, sdks/, test/, tools/, and deploy/ for every existing surface the change touches (call sites, the registry core and HTTP handlers, the MCP server, the CLI commands, the harness adapters in pkg/adapter and the materialization pipeline in pkg/materialize, the §6.7.1 capability matrix in pkg/adapter/capability.go, the SPI implementations, the language SDKs, the spec-matrix definitions in tools/matrix/matrices.go, the doccov manifest, the golden files in test/materialization/testdata/golden, and the Helm chart and deployment manifests under deploy/) and every new surface it adds. " +
    "The blast radius and the build sequence MUST include the surfaces the proposal REMOVES, not only those it adds or changes: for every mode, field, function, env var, error code, meta-tool, adapter value, or whole file the proposal eliminates, include an explicit removal step that deletes it plus the code paths, tests, fixtures, golden files, and doc references orphaned by its removal, sequenced so the removal lands without breaking the build (remove consumers before the surface, or in the same step). A surface the proposal eliminates that has no removal step is a planning gap. " +
    "When the change touches a harness, an artifact type, a frontmatter field, a rule mode, a hook event, or a target path, the blast radius MUST keep every parallel representation consistent: the §6.7 path table is in spec/06, but the code-side surfaces are the adapter in pkg/adapter (adapter.go, none.go, claudecode.go, builtins.go, layout.go), the §6.7.1 matrix mirror in pkg/adapter/capability.go, the golden files and validity checks in test/materialization, the matrix-audit cells in tools/matrix/matrices.go, and the per-harness docs in docs/consuming. " +
    "Produce blastRadius (one entry per surface) and an ORDERED build sequence of steps where each step is independently implementable once its dependencies are done. For each step give the work, the target files or packages, dependsOn (earlier step ids), the test levels it must create and run (per " +
    repo +
    "/.claude/rules/test-coverage.md), and the spec sections it implements. Sequence so foundational changes (shared types, schemas, the capability matrix) come before the code that consumes them, and tests for each step land within that step.\n\n" +
    "NOTE ON RESUMING: if a prefix of this proposal is already implemented and committed on the current branch (run `git log --oneline -40` and grep the current code to confirm), begin the plan at the FIRST surface that is NOT yet implemented and cover only the remainder; grep-confirm each step targets a surface genuinely absent from the current code before emitting it, and skip any already present.",
  { schema: PLAN, label: "plan", phase: "Plan" },
);

for (let round = 1; round < maxPlanRounds; round++) {
  const critique = await agent(
    "Adversarially check whether a build plan covers the entire blast radius of an applied spec proposal.\n\n" +
      "You are a read-only critic; do not edit any file. Work in the repository root.\n\nProposal: " +
      proposal +
      " (read every section). Plan under review:\n" +
      JSON.stringify(plan, null, 2) +
      "\n\nReport complete=false with specific gaps when the plan omits any part of the proposal's blast radius (a field, error code, gate, schema, adapter, capability cell, or call site the proposal specifies), omits a removal step for a surface the proposal eliminates (a removed mode, field, function, env var, error code, adapter value, or file left in the tree, or code, tests, golden files, and doc references orphaned by a removal), leaves a parallel harness representation inconsistent (a §6.7.1 capability cell in spec/06 without its pkg/adapter/capability.go mirror, or an adapter output change without its test/materialization golden update), sequences a consumer before its prerequisite, or omits a test level the change reaches per " +
      repo +
      "/.claude/rules/test-coverage.md. Report complete=true with an empty gaps array when the plan fully implements the proposal. Do not invent scope the proposal does not contain.",
    { schema: CRITIQUE, label: "plan-critique:r" + round, phase: "Plan" },
  );
  if (!critique || critique.complete || critique.gaps.length === 0) {
    log("Plan complete after " + round + " critique round(s)");
    break;
  }
  log("Plan revision " + round + ": closing " + critique.gaps.length + " gap(s)");
  plan = await agent(
    "Revise a build plan to close the gaps a completeness critic found.\n\n" +
      "You are a read-only planner; do not edit any file. Work in the repository root.\n\nProposal: " +
      proposal +
      ". Current plan:\n" +
      JSON.stringify(plan, null, 2) +
      "\n\nGaps to close:\n" +
      critique.gaps.map((g) => "- " + g).join("\n") +
      "\n\nReturn the full revised plan (blast radius + ordered steps), incorporating the gaps and preserving the parts that were correct. Re-sequence if a gap is a prerequisite-ordering problem.",
    { schema: PLAN, label: "plan-revise:r" + round, phase: "Plan" },
  );
}

log("Build sequence: " + plan.steps.length + " steps");

// ---- Build: implement each step in order, gated by per-step verify + review ----
// Sequential: later steps depend on earlier ones and share the working
// tree, so steps must not run concurrently. Each step runs an inner loop —
// implement/fix, then an INDEPENDENT verify of the step's tests, then an
// adversarial design-conformance review of the step's own diff — and only
// advances once the step is both green and review-clean. Catching a
// divergence at the step that introduced it is cheaper than catching it in
// the final whole-change review, and stops a wrong foundation from
// propagating into its dependents.

// Per-step review lenses, scoped to the single step's diff. Whole-change
// completeness is left to the final Review; here a surface this step adds
// for a later step to consume is explicitly out of scope.
const STEP_REVIEW_LENSES = [
  {
    key: "conformance",
    text: "Lens: design conformance. Check that what this step builds matches the proposal's design for the sections it implements — the right component owning each write, the gates and predicates the design names, field placement on the correct object, the ordering the design requires, and defaults that agree with it. Passing tests do not excuse a divergence.",
  },
  {
    key: "invariants",
    text: "Lens: invariants and edge cases. Check that this step enforces the invariants, ordering rules, and failure-mode handling the proposal names for its sections (a precondition-fenced write, a fail-closed gate, a one-writer rule, a crash-recovery path), and that each spec-named edge case for this step has a corresponding code path. Report any the code omits or implements incorrectly.",
  },
];

phase("Build");
// Capture the pre-implementation HEAD so Verify (coverage diff) and Review
// (design-conformance diff) measure exactly this run's changes. This SHA is
// embedded literally in every `git diff <baseRef>..HEAD` below, so it must be
// a real ref — fall back to a prose string and every diff is malformed. Retry
// a couple of times, then fail fast rather than proceed with broken diffs.
let baseRef = null;
for (let b = 0; b < 3 && !baseRef; b++) {
  const baseline = await agent(
    "Print the current git HEAD commit SHA in the repository (run `git rev-parse HEAD`). Do not edit anything. Return it as {sha}.",
    { schema: SHA, label: b === 0 ? "baseline" : "baseline:retry" + b, phase: "Build" },
  );
  if (baseline && baseline.sha) baseRef = baseline.sha.trim();
}
if (!baseRef) {
  throw new Error("could not capture the pre-build HEAD SHA; aborting so coverage and review diffs are not run against a malformed ref");
}

const stepResults = [];
let priorContext = "";
let replanCount = 0;
for (let i = 0; i < plan.steps.length; i++) {
  const step = plan.steps[i];
  const stepHeader =
    "Step " +
    step.id +
    " (" +
    (i + 1) +
    " of " +
    plan.steps.length +
    "): " +
    step.title +
    "\nWork: " +
    step.work +
    "\nTargets: " +
    (step.targets || []).join(", ") +
    "\nTest levels to create and run: " +
    (step.tiers || []).join(", ") +
    "\nSpec sections: " +
    (step.specRefs || []).join(", ");

  // HEAD at the start of this step, so the per-step verify and review see
  // only this step's commits and not the prior steps'.
  const stepStart = await agent(
    "Print the current git HEAD commit SHA in the repository (`git rev-parse HEAD`). Do not edit anything. Return {sha}.",
    { schema: SHA, label: "build:" + step.id + ":base", phase: "Build" },
  );
  const stepRef = (stepStart && stepStart.sha) || baseRef;

  let res = null; // last implement/fix STEP result
  let stepGreen = false;
  let stepReviewClean = false;
  let stepFindings = [];
  let issues = ""; // carried failures or divergences for the next attempt
  let attempt = 0;
  // Inner loop: implement/fix → verify → review, until green-and-conformant
  // or the attempt cap. Each iteration is one fix attempt.
  while (attempt < maxStepAttempts && !(stepGreen && stepReviewClean)) {
    attempt++;
    res = await agent(
      attempt === 1
        ? "Implement one step of a build sequence for an applied spec proposal.\n\n" +
            "HARD CONSTRAINT: implement only this step. Do not start later steps. Do not edit spec/. Work in the repository root.\n\n" +
            "Proposal (authoritative for the change): " +
            proposal +
            ". Its spec edits are already in spec/; read the relevant sections.\n\n" +
            stepHeader +
            priorContext +
            "\n\nImplement the code for this step, create or modify its tests at the listed levels (and any other level this step reaches per the test-coverage rule), and RUN them: always `go build ./...`, `go vet ./<changed-pkg>/...`, and `golangci-lint run ./<changed-pkg>/...` (or `make lint`), plus `go test ./<changed-pkg>/... -count=1` for the changed packages and each listed higher level (for a test that needs live services, run `make services-up` then `make test-live`, or set the `PODIUM_POSTGRES_DSN` / `PODIUM_S3_*` env vars so a scoped `go test` takes the live path; the standalone SQLite/filesystem paths need none). Fix the code until the tests pass. Then commit this step on the current branch with a message in the repository's convention (read `git log --oneline -5`). " +
            RULES +
            "\n\nReturn whether you implemented it, the files changed, the tests added or modified, the levels you ran, whether they passed, and the commit SHA. If a level genuinely cannot run here (a cloud-only resource), say so in notes and set testsPassed from the levels that can run."
        : "Continue one build step of an applied spec proposal that is not yet green-and-conformant.\n\n" +
            "HARD CONSTRAINT: work only on this step. Do not start later steps. Do not edit spec/. Work in the repository root.\n\n" +
            "Proposal (authoritative): " +
            proposal +
            " (spec edits already in spec/).\n\n" +
            stepHeader +
            "\n\nThe prior attempt left this step not done. Address this:\n" +
            issues +
            "\n\nFix the code (add or correct tests where the issue is a missing or wrong test; change the code to match the proposal's design where the issue is a divergence), run `go build ./...`, `go vet`, the linter, and this step's listed test levels to green (skip only a level that genuinely needs a cloud-only resource, noting it), then commit on the current branch. " +
            RULES +
            "\n\nReturn the step result with testsPassed reflecting the levels that can run here.",
      { schema: STEP, label: "build:" + step.id + (attempt > 1 ? ":fix" + attempt : ""), phase: "Build" },
    );

    // A skipped or dead implementer (agent() === null) committed nothing. An
    // empty diff would otherwise pass the independent verify (the step diff is
    // empty) and the review (no diff to find fault with) and masquerade as a
    // done step — then the success log would deref res.commit on null. Treat it
    // as a failed attempt and retry; if it persists, the step aborts cleanly
    // below.
    if (!res) {
      stepGreen = false;
      stepReviewClean = false;
      stepFindings = [];
      issues =
        "The implementer agent returned no result (it was skipped or errored). Re-implement this step from the proposal and its tests at the listed levels, run them to green, then commit.";
      log("Step " + step.id + " attempt " + attempt + "/" + maxStepAttempts + ": implementer returned no result");
      continue;
    }

    // Independent verify: a different agent re-runs the step's tests and
    // gates green. The implementer's self-report is advisory.
    const sv = await agent(
      "Independently verify ONE just-implemented build step of an applied spec proposal. You did not write this code.\n\n" +
        "Work in the repository root. Do not edit code; only run tests and report. Proposal: " +
        proposal +
        ".\n\n" +
        stepHeader +
        "\n\nThis step's changes are everything since " +
        stepRef +
        " (`git diff " +
        stepRef +
        "..HEAD`). Run SCOPED static checks (`go build ./<changed-pkg>/...`, `go vet ./<changed-pkg>/...`, `golangci-lint run ./<changed-pkg>/...`) and unit tests (`go test ./<changed-pkg>/... -count=1`) for the changed packages, plus each higher test level this step must run: " +
        ((step.tiers || []).join(", ") || "(none beyond build/vet/lint and unit)") +
        ". Podium is a Go project tested with `go test` plus Makefile targets. No-stall: DEFAULT to running every command in the FOREGROUND, one at a time, SCOPED to the changed packages — do not use `run_in_background` for tests unless a single suite will exceed the 180s watchdog without output (see below). Run the scoped commands above directly (no `| tail`; they finish quickly, streaming so no watchdog stall). For a test that needs a real Postgres or S3/MinIO, run `make services-up` then `make test-live`, or set the `PODIUM_POSTGRES_DSN` / `PODIUM_S3_*` env vars so a scoped `go test` takes the live path; the standalone SQLite/filesystem paths need none. LONG-SILENT SUITE EXCEPTION: if and only if a single suite will genuinely exceed ~150s without any output, run it with `run_in_background: true` to a log file path — but then poll by reading that log file with the Read tool (not with a Bash `tail`/`until` loop; a Bash poll loop can hang forever if the harness deletes its `.output` file after the 180s watchdog fires). If this step changed pkg/adapter, pkg/materialize, or the §6.7.1 capability matrix, also run `go test ./test/materialization/... -count=1` and `make matrix-audit`. Set green=true only if every level that can run here passes (skip only a level that genuinely needs a cloud-only resource, noting it); a pre-existing whole-repo failure unrelated to this step's changed packages is not this step's failure. List any real failures precisely so they can be fixed. Coverage is checked once over the whole change at the end, not here. BRANCH SAFETY: never `git checkout` a branch or commit to compare against a baseline — use `git diff <SHA>..HEAD` or `git show <SHA>:<path>` (you only run tests and report; do not change the checkout or the current branch).",
      { schema: VERIFY, label: "verify:" + step.id + ":r" + attempt, phase: "Build" },
    );
    stepGreen = !!(sv && sv.green);
    if (!stepGreen) {
      stepReviewClean = false;
      stepFindings = [];
      issues =
        "This step's tests are not green. Failures:\n" +
        (((sv && sv.failures) || []).map((f) => "- " + f).join("\n") || "- (verify reported not green without listing failures)") +
        ((sv && sv.notes) ? "\nVerifier notes: " + sv.notes : "");
      log("Step " + step.id + " attempt " + attempt + "/" + maxStepAttempts + ": tests not green");
      continue;
    }

    // Adversarial design-conformance review of THIS step's diff only.
    const reviewResults = await parallel(
      STEP_REVIEW_LENSES.map((l) => () =>
        agent(
          "Adversarially review ONE just-implemented build step against the proposal's design.\n\n" +
            "Read the proposal at " +
            proposal +
            " (its spec edits are applied), focusing on the sections this step implements (" +
            ((step.specRefs || []).join(", ") || "the sections relevant to this step's work") +
            "), and read ONLY this step's diff: `git diff " +
            stepRef +
            "..HEAD` in the repository. You are read-only; report findings only.\n\n" +
            "Scope: judge only what THIS step is responsible for (its work: " +
            step.work +
            "). A surface this step ADDS that a LATER step is meant to consume or wire up is NOT a divergence here — do not flag 'unused' or 'never called' for something a later step will use. A finding is a place this step's landed code diverges from the proposal's design for the sections it implements. Not a style preference, not new scope the proposal does not contain, not a coverage gap. Cite file:line and the proposal section. Report an empty findings array when this step conforms.\n\n" +
            l.text,
          {
            schema: REVIEW,
            label: "review:" + step.id + ":" + l.key + ":r" + attempt,
            phase: "Build",
          },
        ),
      ),
    );
    // Fail closed: a reviewer that died (null) is not evidence of conformance.
    // Only declare the step review-clean when every reviewer ran and none
    // found a divergence.
    const liveReviews = reviewResults.filter(Boolean);
    const allReviewersRan = liveReviews.length === STEP_REVIEW_LENSES.length;
    stepFindings = liveReviews.flatMap((r) => r.findings);
    stepReviewClean = stepFindings.length === 0 && allReviewersRan;
    log(
      "Step " +
        step.id +
        " attempt " +
        attempt +
        ": green, " +
        stepFindings.length +
        " design-conformance finding(s)" +
        (allReviewersRan ? "" : " (a reviewer did not return; not treated as clean)"),
    );
    if (!stepReviewClean) {
      issues =
        stepFindings.length > 0
          ? "This step builds and its tests pass, but the design-conformance review found divergences from the proposal. Fix the code to match the proposal's design for this step (do not change scope, do not touch spec/), keeping the tests green:\n" +
            JSON.stringify(stepFindings, null, 2)
          : "This step builds and its tests pass, but a design-conformance reviewer did not return, so conformance is unconfirmed. Re-check that this step's code matches the proposal's design for its sections; fix any divergence and keep the tests green.";
    }
  }

  stepResults.push({
    step: step.id,
    title: step.title,
    attempts: attempt,
    stepGreen,
    reviewClean: stepReviewClean,
    findings: stepReviewClean ? [] : stepFindings,
    ...(res || { implemented: false, testsPassed: false, tiersRun: [], notes: "agent failed" }),
  });
  // Abort the sequence if the step did not reach green-and-conformant within
  // maxStepAttempts: its dependents would build on a broken or divergent
  // foundation. Stop here; the spec and the completed steps are already
  // committed for inspection and resume.
  if (!(stepGreen && stepReviewClean)) {
    const remaining = plan.steps.length - i - 1;
    const reason = !stepGreen ? "tests not green" : "design-conformance divergences outstanding";
    log(
      "Step " +
        step.id +
        " stuck (" +
        reason +
        ") after " +
        attempt +
        " attempt(s); aborting the build sequence (" +
        remaining +
        " dependent step(s) not attempted)",
    );
    return {
      status: "step-stuck",
      stuckStep: step.id,
      blastRadius: plan.blastRadius,
      steps: stepResults,
      commits: stepResults.map((s) => s.commit).filter(Boolean),
      green: false,
      reviewClean: false,
      reviewFindings: stepReviewClean ? [] : stepFindings,
      failures: [
        "build aborted at step " +
          step.id +
          " (" +
          step.title +
          ") after " +
          attempt +
          " attempts: " +
          reason +
          "; " +
          remaining +
          " dependent step(s) not attempted",
      ],
      resumeNote:
        "The spec is applied and committed; build steps before " +
        step.id +
        " are committed green and review-clean. " +
        (!stepGreen
          ? "Step " + step.id + "'s tests did not reach green. "
          : "Step " + step.id + " is green but diverges from the proposal's design (see reviewFindings). ") +
        "Fix step " +
        step.id +
        " by hand or re-run implement-proposal, which re-plans against the current tree.",
    };
  }
  log(
    "Step " +
      step.id +
      " done in " +
      attempt +
      " attempt(s) (commit " +
      ((res && res.commit) || "?") +
      "), green + review-clean.",
  );
  // Carry a short tail of prior-step outcomes so each implementer knows
  // what already landed without re-deriving it.
  priorContext =
    "\n\nAlready completed in this sequence:\n" +
    stepResults
      .map((s) => "- " + s.step + ": " + s.title + (s.commit ? " (" + s.commit + ")" : ""))
      .join("\n");

  // Periodic plan-drift check, forward-only. The plan was a prediction made
  // before any code existed; as steps land, reality drifts (an unplanned
  // surface gets touched, a removal orphans more than foreseen, a later step
  // becomes redundant or mis-sequenced). The per-step review judges completed
  // work, not the remaining plan — so here, every replanEvery completed steps
  // (and after a step that struggled), a read-only critic checks whether the
  // remaining plan still holds against what actually landed. On evidenced
  // drift, the remaining steps are re-planned. Completed steps are immutable:
  // only indices after i ever change.
  const completed = i + 1;
  const hasRemaining = i < plan.steps.length - 1;
  const triggerReplan = (completed % replanEvery === 0) || attempt >= replanStruggleAttempts;
  if (hasRemaining && replanCount < maxReplans && triggerReplan) {
    const remainingSteps = plan.steps.slice(i + 1);
    const drift = await agent(
      "Check whether the REMAINING build plan still matches reality after part of a build sequence has landed.\n\n" +
        "You are a read-only critic; do not edit any file. Work in the repository root.\n\nProposal (authoritative, spec edits already applied): " +
        proposal +
        ". Read the sections still to be built.\n\nCompleted so far (immutable, already committed):\n" +
        stepResults.map((s) => "- " + s.step + ": " + s.title).join("\n") +
        "\n\nWhat actually landed: `git diff " +
        baseRef +
        "..HEAD` in the repo.\n\nRemaining planned steps (not yet built):\n" +
        JSON.stringify(remainingSteps, null, 2) +
        "\n\nReport drift=true ONLY with concrete, evidenced reasons that the remaining plan no longer correctly or completely implements the rest of the proposal given what landed: a surface the proposal requires that the completed work touched but no remaining step covers; a removal the completed work began that orphaned code no remaining step deletes; a remaining step now redundant because an earlier step already satisfied it; a remaining step whose prerequisites changed so it must be re-sequenced; or a file the completed work changed that forces a different approach in a remaining step. Be conservative: default to drift=false. Do NOT report design-conformance nits (handled per step), style, or scope the proposal does not contain. Return drift=false with an empty reasons array when the remaining plan still holds.",
      { schema: DRIFT, label: "replan-check:after-" + step.id, phase: "Build" },
    );
    if (drift && drift.drift && (drift.reasons || []).length > 0) {
      log(
        "Plan drift after step " +
          step.id +
          " (" +
          drift.reasons.length +
          " reason(s)); re-planning the remaining " +
          remainingSteps.length +
          " step(s) [re-plan " +
          (replanCount + 1) +
          "/" +
          maxReplans +
          "]",
      );
      const tail = await agent(
        "Re-plan the REMAINING steps of a build sequence for an applied spec proposal, given what has already landed.\n\n" +
          "You are a read-only planner; do not edit any file. Work in the repository root.\n\nProposal (authoritative, spec edits applied): " +
          proposal +
          ".\n\nCompleted steps are IMMUTABLE — already committed; do not include them, re-order them, or plan to redo them:\n" +
          stepResults
            .map((s) => "- " + s.step + ": " + s.title + (s.commit ? " (" + s.commit + ")" : ""))
            .join("\n") +
          "\n\nWhat actually landed: `git diff " +
          baseRef +
          "..HEAD`.\n\nCurrent remaining plan:\n" +
          JSON.stringify(remainingSteps, null, 2) +
          "\n\nDrift the critic found:\n" +
          drift.reasons.map((r) => "- " + r).join("\n") +
          "\n\nReturn the revised ORDERED sequence for the REMAINING work only. Preserve the id, title, and content of any remaining step that is still valid and unchanged so the logs stay coherent; add steps for newly discovered work, drop steps already satisfied by the completed work, and re-sequence where a prerequisite emerged. Each step keeps the same fields (id, title, work, targets, dependsOn, tiers, specRefs); give new steps fresh ids that do not collide with completed or surviving ids. Cover the rest of the proposal completely, including any removal the completed work left orphaned. In blastRadiusAdditions, list any surface the original plan missed that you discovered. Do not re-plan or duplicate completed work.",
        { schema: TAIL, label: "replan:after-" + step.id, phase: "Build" },
      );
      // Count the re-plan attempt regardless of its outcome: a re-plan that
      // keeps returning empty must still be bounded by maxReplans, otherwise
      // the drift+replan agent pair re-runs on every cadence hit unbounded.
      replanCount++;
      if (tail && tail.steps && tail.steps.length > 0) {
        // Reassign any new-tail id that collides with a completed step id, so
        // stepResults and the drift/replan prompts do not key two different
        // steps under the same id. dependsOn is informational here (steps run
        // in array order), but remap it too for coherence.
        const completedIds = new Set(stepResults.map((s) => s.step));
        const idRemap = {};
        for (const s of tail.steps) {
          if (completedIds.has(s.id)) {
            idRemap[s.id] = s.id + "-r" + replanCount;
            s.id = idRemap[s.id];
          }
        }
        for (const s of tail.steps) {
          if (Array.isArray(s.dependsOn)) s.dependsOn = s.dependsOn.map((d) => idRemap[d] || d);
        }
        // Splice the new tail in place of the old remaining steps. Only
        // indices after i change; the for-loop picks up the new steps as
        // plan.steps.length updates.
        plan.steps.splice(i + 1, plan.steps.length - (i + 1), ...tail.steps);
        if (tail.blastRadiusAdditions && tail.blastRadiusAdditions.length > 0) {
          plan.blastRadius = plan.blastRadius.concat(tail.blastRadiusAdditions);
        }
        log("Re-planned tail: " + tail.steps.length + " remaining step(s) now queued");
      } else {
        log("Re-plan returned no steps; keeping the existing tail");
      }
    }
  }
}

// ---- Verify: run the reached test levels across the whole change ----

// Clean-checkout compile guard. The Verify and recheck agents run tests
// against the WORKING TREE, so a valid fix left uncommitted reads as green even
// though HEAD does not contain it — which has shipped a non-compiling branch
// tip (verification passing over code that was never committed). Before
// trusting green, assert the tree carries no uncommitted tracked source change
// and that HEAD compiles from that clean state. Returns {clean, compiles,
// committed, details}; the caller ANDs clean && compiles into green.
const GUARD = {
  type: "object",
  required: ["clean", "compiles"],
  properties: {
    clean: { type: "boolean", description: "git status --porcelain lists no tracked source change (every change committed)" },
    compiles: { type: "boolean", description: "`go build ./...` exits 0 on the committed tree" },
    committed: { type: "string", description: "SHA of a commit made to clean the tree, or empty if nothing needed committing" },
    details: { type: "string", description: "what was uncommitted and what was done, or the build error if it does not compile" },
  },
};

const GUARD_PROMPT =
  "CLEAN-CHECKOUT COMPILE GUARD for the implementation in the repository.\n\nThe verification runs tests against the WORKING TREE, so a valid fix left UNCOMMITTED reads as green even though HEAD does not contain it (this has shipped a non-compiling branch tip). Enforce that green reflects COMMITTED code:\n" +
  "1. Run `git status --porcelain`. A tracked modified/added SOURCE file (Go, a schema or golden file under test/, an SDK file under sdks/, a doc under docs/) is part of this change and MUST be committed: first confirm the whole tree builds with `go build ./...` and the affected packages' tests pass, then stage and commit those files on the current feature branch with a descriptive message. Do NOT commit build outputs, coverage files, logs, or unrelated scratch artifacts — leave git-ignored or clearly-external untracked files in place; they do not block clean.\n" +
  "2. After the tree carries no uncommitted tracked source change, run `go build ./...` and confirm it exits 0 (HEAD compiles from the committed state).\n" +
  "Set clean=true only when `git status --porcelain` lists no tracked source change, and compiles=true only when `go build ./...` exits 0. If a tracked source file cannot be committed because it does not build or breaks tests, set clean=false and explain in details rather than committing a broken tree. " +
  "Run `go build ./...` and any scoped package tests in the FOREGROUND. " +
  "BRANCH SAFETY: confirm `git rev-parse --abbrev-ref HEAD` prints the feature branch before any commit; never checkout/switch/reset/branch -f.";

// Runs the guard and returns its verdict (or null if the agent died).
async function runCompileGuard(label, phaseName) {
  return await agent(GUARD_PROMPT, { schema: GUARD, label: "compile-guard:" + label, phase: phaseName });
}

phase("Verify");
const tierSet = Array.from(new Set(plan.steps.flatMap((s) => s.tiers || [])));
let verify = await agent(
  "Verify the completed implementation of an applied spec proposal builds and its tests pass across every level the change reached.\n\n" +
    "Work in the repository root. Proposal: " +
    proposal +
    ". The implementation just landed across these commits: " +
    stepResults.map((s) => s.commit).filter(Boolean).join(", ") +
    ".\n\nRun the static checks and unit tests for the changed packages, plus each higher test level the change reached: " +
    (tierSet.join(", ") || "as determined from the diff") +
    ". Podium is a Go project tested with `go test` plus Makefile targets. No-stall: run each level in the FOREGROUND, one at a time, and stream output (no `| tail`); do not use `run_in_background` for tests unless a single suite will exceed the 180s watchdog without output (then launch it to a log file and poll by reading that log file with the Read tool, never a Bash `tail`/`until` loop that hangs when the harness deletes its `.output` file). RUN THE STATIC CHECKS DIRECTLY as separate foreground commands so each failure is visible independently: `go build ./...`, `go vet ./...`, and `golangci-lint run ./...` (or `make lint`). For any level that needs live services, run `make services-up` then `make test-live` (it points the tests at the local Postgres/MinIO via the `PODIUM_*` env vars); the standalone SQLite/filesystem paths need none. " +
    "Run the project consistency gate `make coverage-gate` (lint, `speccov-drift`, `matrix-audit`, `doccov-check`, `coverage-budget`); a new spec section needs a test citing it (`// Spec: §X.Y`), a new matrix cell needs a test annotated `// Matrix: §X.Y (...)`, and a new runnable doc example needs its tools/doccov/manifest.yaml entry, so a `speccov-drift`/`matrix-audit`/`doccov-check` failure attributable to this change is a failure to fix. " +
    "Measure changed-line coverage the way the test-coverage rule does — `go test -coverpkg=./... -coverprofile=cover.out ./...` then `go tool cover -func=cover.out` for new code reached in-process, plus `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...` and `go tool covdata textfmt` for boot-path/CLI code that runs only in the spawned `podium` binary — and report it. COVERAGE GATE: green=true requires BOTH that every reached level passes AND that changed-line coverage is at least " +
    coverageFloor +
    "%; if coverage is below " +
    coverageFloor +
    "%, set green=false and add a failure entry naming the under-covered new or changed files and lines so the fix loop adds tests for them (per the test-coverage rule, the floor is on new code; a pure behavior-preserving refactor is exempt — note it instead of failing). Run a DEAD-CODE SWEEP: grep the whole tree for every identifier, mode value, field, function, env var, error code, adapter value, and §6.7.1 capability cell the proposal removes, and confirm none survives as a live reference; the `unused` linter catches unused package-level symbols, but also check for orphaned exported symbols, whole unreferenced files, stale test fixtures or golden files, and dangling matrix or doccov entries. Treat a surviving removed surface or an orphaned caller as a failure (list it precisely so the fix loop deletes it). List any failures precisely.",
  { schema: VERIFY, label: "verify", phase: "Verify" },
);

// Iterate fix-and-re-run until every reached level is green, bounded by
// maxVerifyRounds. The loop stops early when a round reports green, or when
// a non-green result lists no actionable failures (nothing to fix).
let vround = 0;
while (
  verify &&
  !verify.green &&
  verify.failures &&
  verify.failures.length > 0 &&
  vround < maxVerifyRounds
) {
  vround++;
  log("Verify round " + vround + "/" + maxVerifyRounds + ": fixing " + verify.failures.length + " failure(s)");
  await agent(
    "Fix the test failures from the verification of an applied spec proposal's implementation.\n\n" +
      "Work in the repository root. Proposal: " +
      proposal +
      ". Failures:\n" +
      verify.failures.map((f) => "- " + f).join("\n") +
      "\n\nFix the code (not the spec) so every reached level passes; add or correct tests where the failure is a missing or wrong test. Commit the fixes and keep the change minimal. " +
      RULES,
    { label: "verify-fix:r" + vround, phase: "Verify" },
  );
  verify = await agent(
    "Re-run the reached test levels for the implementation in the repository and report whether everything is now green. Levels: " +
      (tierSet.join(", ") || "from the diff") +
      ". Run each level in the FOREGROUND, scoped to the changed packages, one at a time (no `run_in_background` for tests; stream output, no `| tail`); for any level that needs live services, run `make services-up` then `make test-live`. Re-run `make coverage-gate` (lint, speccov-drift, matrix-audit, doccov-check, coverage-budget). Apply the same coverage gate: green=true requires every reached level to pass AND changed-line coverage (`go test -coverpkg=./... -coverprofile=cover.out ./...` then `go tool cover -func=cover.out`) at least " +
      coverageFloor +
      "%, with a behavior-preserving refactor exempt. Report green, the levels run, the changed-line coverage, and any remaining failures precisely.",
    { schema: VERIFY, label: "verify-rerun:r" + vround, phase: "Verify" },
  );
}

let green = !!(verify && verify.green);
if (!green) {
  log(
    vround >= maxVerifyRounds
      ? "Verify cap (" + maxVerifyRounds + " rounds) reached; still not green"
      : "Verify not green with no actionable failures listed; stopping",
  );
} else {
  // Verify reported green against the working tree; confirm that green
  // reflects COMMITTED code and that HEAD compiles from a clean tree.
  const guard = await runCompileGuard("postverify", "Verify");
  if (!guard || !guard.clean || !guard.compiles) {
    green = false;
    log(
      "Clean-checkout compile guard FAILED: " +
        (guard
          ? "clean=" + guard.clean + ", compiles=" + guard.compiles + (guard.details ? " — " + guard.details : "")
          : "guard agent returned no result"),
    );
  } else if (guard.committed) {
    log("Clean-checkout compile guard committed outstanding work: " + guard.committed);
  }
}

// ---- Review: final cross-step design-conformance review of the cumulative diff ----
// Each step was already reviewed against its own diff during Build. This
// final pass reads the WHOLE change against baseRef to catch what a
// step-scoped review cannot: cross-step interactions, a surface one step
// added and another was meant to consume but does not, and whole-change
// completeness. Three lenses report divergences; a fix round applies them
// and the loop re-reviews until clean (or the cap). Skipped when the build
// is not green (no point reviewing a red tree). Findings here are design
// conformance, not new scope.

const REVIEW_LENSES = [
  {
    key: "conformance",
    text: "Lens: design conformance. Read the proposal's Decisions, Proposed solution, and the '## Spec amendment: §X.Y' sections (now landed in spec/), then read the cumulative diff. Report where the landed code does something other than what the design specifies: a different component owning a write, a missing or wrong gate, a predicate that does not match the design, a field in the wrong place, an ordering that violates the design, or a default that contradicts it. Passing tests do not excuse a divergence.",
  },
  {
    key: "invariants",
    text: "Lens: named invariants and edge cases. List the invariants, races, ordering rules, and failure-mode handling the proposal explicitly calls out (for example a fail-closed visibility or verification gate, an idempotent ingest, config-merge reconciliation, a crash- or partial-write recovery path, deployment-mode parity), and verify the code implements each. Report any invariant the code does not enforce or enforces incorrectly, and any spec-named edge case with no corresponding code path.",
  },
  {
    key: "completeness",
    text: "Lens: blast-radius completeness. Cross-check the proposal's Proposed solution and Documentation-changes sections against the diff: every code, SDK, doc, and test change the proposal specifies is present, every harness change is mirrored across the §6.7 path table, the pkg/adapter/capability.go matrix, the test/materialization golden files, and the matrix-audit cells, and every surface the proposal removes is gone (no orphaned caller or compiling-but-dead path). Report any specified change missing from the diff or any removal left incomplete.",
  },
];

const REVIEW_RULES =
  "Read the proposal at " +
  proposal +
  " (its spec edits are applied) and the cumulative implementation diff (`git diff " +
  baseRef +
  "..HEAD`) in the repository. You are read-only; report findings only. A finding is a place the landed code diverges from the proposal's design — not a style preference, not new scope the proposal does not contain, and not a test gap (coverage is handled separately). Cite file:line and the proposal section. Report an empty array when the code conforms.";

let reviewClean = false;
let reviewRound = 0;
let lastReviewFindings = [];
let reviewFixApplied = false;
if (green) {
  phase("Review");
  while (reviewRound < maxReviewRounds && !reviewClean) {
    reviewRound++;
    const reviewResults = await parallel(
      REVIEW_LENSES.map((l) => () =>
        agent(REVIEW_RULES + "\n\n" + l.text, {
          schema: REVIEW,
          label: "review:" + l.key + ":r" + reviewRound,
          phase: "Review",
        }),
      ),
    );
    // Fail closed: a reviewer that died (null) is not evidence of conformance.
    // Only declare clean when every lens ran and none found a divergence.
    const liveReviews = reviewResults.filter(Boolean);
    const allReviewersRan = liveReviews.length === REVIEW_LENSES.length;
    const findings = liveReviews.flatMap((r) => r.findings);
    lastReviewFindings = findings;
    log(
      "Review round " +
        reviewRound +
        ": " +
        findings.length +
        " design-conformance finding(s)" +
        (allReviewersRan ? "" : " (a reviewer did not return; not treated as clean)"),
    );
    if (findings.length === 0) {
      if (allReviewersRan) {
        reviewClean = true;
        break;
      }
      // No findings, but a reviewer did not run — re-review rather than
      // concluding clean. Nothing to fix, so skip the fix agent.
      continue;
    }
    reviewFixApplied = true;
    await agent(
      "Fix design-conformance divergences between the landed implementation and the proposal at " +
        proposal +
        ".\n\nWork in the repository root. The proposal's spec edits are applied; do not edit spec/. Apply each finding so the code matches the proposal's design, add or correct tests for the corrected behavior, run `go build ./...`, `go vet`, the linter, and the test levels the change reaches to green, and commit. " +
        RULES +
        "\n\nFindings to fix:\n" +
        JSON.stringify(findings, null, 2),
      { label: "review-fix:r" + reviewRound, phase: "Review" },
    );
  }
  if (!reviewClean) {
    log(
      reviewRound >= maxReviewRounds
        ? "Review cap (" + maxReviewRounds + " rounds) reached with divergences outstanding"
        : "Review stopped with divergences outstanding",
    );
  }
} else {
  log("Build not green; skipping design-conformance review");
}

// A review fix can perturb tests; re-confirm green once whenever the review
// applied any fix, so the returned green reflects the post-fix tree. Gating on
// reviewFixApplied (not on outstanding findings) is the point: a review that
// found and fixed a divergence then converged clean still changed the tree.
let finalGreen = green;
if (green && reviewFixApplied) {
  const recheck = await agent(
    "Re-run the reached test levels for the implementation in the repository after the design-conformance fixes and report whether everything is still green. Levels: " +
      (tierSet.join(", ") || "from the diff") +
      ". Run each level in the FOREGROUND, scoped to the changed packages, one at a time (no `run_in_background` for tests; stream output, no `| tail`); for any level that needs live services, run `make services-up` then `make test-live`. Re-run `make coverage-gate`. Apply the coverage gate (changed-line coverage at least " +
      coverageFloor +
      "% via `go test -coverpkg=./... -coverprofile=cover.out ./...` then `go tool cover -func=cover.out`, refactors exempt). Report green, the levels run, the coverage, and any remaining failures.",
    { schema: VERIFY, label: "verify-postreview", phase: "Review" },
  );
  finalGreen = !!(recheck && recheck.green);
  if (recheck) verify = recheck;
  // The review-fix agents commit their own fixes, but a fix left uncommitted
  // in the working tree would pass the recheck while leaving HEAD red. Re-run
  // the clean-checkout compile guard so finalGreen reflects committed code.
  if (finalGreen) {
    const guard = await runCompileGuard("postreview", "Review");
    if (!guard || !guard.clean || !guard.compiles) {
      finalGreen = false;
      log(
        "Clean-checkout compile guard FAILED after review fixes: " +
          (guard
            ? "clean=" + guard.clean + ", compiles=" + guard.compiles + (guard.details ? " — " + guard.details : "")
            : "guard agent returned no result"),
      );
    } else if (guard.committed) {
      log("Clean-checkout compile guard committed outstanding review work: " + guard.committed);
    }
  }
}

return {
  status: finalGreen && reviewClean ? "implemented" : "implemented-not-green",
  blastRadius: plan.blastRadius,
  steps: stepResults,
  commits: stepResults.map((s) => s.commit).filter(Boolean),
  green: finalGreen,
  reviewClean,
  reviewRounds: reviewRound,
  reviewFindings: reviewClean ? [] : lastReviewFindings,
  verifyRounds: vround,
  changedLineCoverage: verify ? verify.changedLineCoverage : undefined,
  failures: verify ? verify.failures || [] : ["final verification did not run"],
  resumeNote:
    finalGreen && reviewClean
      ? undefined
      : "Spec and code commits are on the branch. " +
        (!finalGreen ? "Tests/coverage are not green. " : "") +
        (!reviewClean ? "Design-conformance review found unresolved divergences (see reviewFindings). " : "") +
        "Re-run implement-proposal to continue, or resolve by hand.",
};
