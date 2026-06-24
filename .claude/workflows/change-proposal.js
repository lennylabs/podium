export const meta = {
  name: "change-proposal",
  description:
    "Validate a problem, draft and write a change proposal — spec edits and/or core-product or test-infra code changes (new mode) — then adversarially review and fix it until two consecutive clean rounds",
  whenToUse:
    "Write a change proposal (spec and/or implementation: core product or test infra) from a problem statement, or converge an existing proposals/*.md before sign-off",
};

let input = args;
if (typeof input === "string") {
  input = JSON.parse(input);
}
if (!input || typeof input !== "object") {
  throw new Error(
    "args must be a JSON object or a JSON-encoded object string, received " +
      typeof args,
  );
}
for (const k of ["mode", "date", "exemplar"]) {
  if (!input[k]) throw new Error("args." + k + " is required and missing");
}
const mode = input.mode;
if (mode !== "new" && mode !== "review") {
  throw new Error('args.mode must be "new" or "review"');
}
if (mode === "new") {
  for (const k of ["problem", "nextNumber"]) {
    if (!input[k])
      throw new Error("args." + k + " is required in new mode and missing");
  }
} else if (!input.proposalPath) {
  throw new Error("args.proposalPath is required in review mode and missing");
}

const repo = input.repoRoot || ".";
const date = input.date;
const exemplar = input.exemplar;
const context = input.context || "none provided";
const maxRounds = input.maxReviewRounds || 12;
const CLEAN_TARGET = 2;

const READ_ONLY =
  "You are a read-only investigator. Do not create, edit, or delete any file. Cite evidence as file:line.";
const EVIDENCE =
  "Verify every claim directly against spec/, pkg/, cmd/, internal/, sdks/, docs/, and git history in the repository. Spec files are large; use Grep and targeted Read offsets, never read a whole spec file. Treat the problem statement itself and any progress-tracking or audit prose elsewhere in the repository as leads to verify rather than as evidence.";
const PRINCIPLES = [
  "Podium ships a single canonical implementation per concern; the shared Go library is the one behavioral surface across deployment modes (standard, standalone, filesystem registry) per spec §2.2.",
  "Podium is pre-1.0: a backward-incompatible change is acceptable and lands in a MINOR bump; do not add migration shims, legacy flags, or dual code paths for external compatibility.",
  "Prefer extending an existing spec surface, SPI (§9.1), or adapter mechanism over inventing a parallel one.",
  "Minimal new surface; every new env var, error code (§6.10), meta-tool, SPI, or harness/adapter value must survive the question of whether an existing surface already covers it.",
].join(" ");

const PREMISES = {
  type: "object",
  required: ["premises"],
  properties: {
    premises: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "statement", "kind", "loadBearing"],
        properties: {
          id: { type: "string" },
          statement: { type: "string" },
          kind: {
            type: "string",
            enum: [
              "spec-claim",
              "code-claim",
              "gap-claim",
              "consequence-claim",
            ],
          },
          loadBearing: { type: "boolean" },
        },
      },
    },
  },
};

const PREMISE_VERDICT = {
  type: "object",
  required: ["verdict", "evidence", "notes"],
  properties: {
    verdict: { type: "string", enum: ["confirmed", "refuted", "revised"] },
    revisedStatement: { type: "string" },
    evidence: { type: "array", items: { type: "string" } },
    notes: { type: "string" },
  },
};

const DRAFT = {
  type: "object",
  required: [
    "viable",
    "title",
    "kind",
    "problemRestatement",
    "decisions",
    "changes",
    "nonGoals",
  ],
  properties: {
    viable: { type: "boolean" },
    whyNotViable: { type: "string" },
    title: { type: "string" },
    kind: { type: "string", enum: ["new", "fix"] },
    problemRestatement: { type: "string" },
    decisions: { type: "array", items: { type: "string" } },
    changes: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "title", "targets", "rationale", "sketch"],
        properties: {
          id: { type: "string" },
          title: { type: "string" },
          targets: { type: "array", items: { type: "string" } },
          rationale: { type: "string" },
          sketch: { type: "string" },
        },
      },
    },
    nonGoals: { type: "array", items: { type: "string" } },
    openQuestions: { type: "array", items: { type: "string" } },
  },
};

const CHALLENGE = {
  type: "object",
  required: ["verdict", "reasons", "evidence"],
  properties: {
    verdict: { type: "string", enum: ["keep", "revise", "drop"] },
    reasons: { type: "string" },
    evidence: { type: "array", items: { type: "string" } },
    revision: { type: "string" },
  },
};

const FINDINGS = {
  type: "object",
  required: ["findings"],
  properties: {
    findings: {
      type: "array",
      items: {
        type: "object",
        required: [
          "title",
          "where",
          "claim",
          "why_wrong",
          "evidence",
          "suggested_fix",
        ],
        properties: {
          title: {
            type: "string",
            description: "Short unique title for the error",
          },
          where: {
            type: "string",
            description: "Location in the proposal (section, line)",
          },
          claim: {
            type: "string",
            description: "What the proposal asserts or proposes there",
          },
          why_wrong: {
            type: "string",
            description:
              "Why this makes the applied spec or implementation wrong",
          },
          evidence: {
            type: "string",
            description:
              "Exact file:line citations with short quotes for both the proposal claim and the contradicting source",
          },
          suggested_fix: { type: "string" },
        },
      },
    },
  },
};

const VERDICT = {
  type: "object",
  required: ["confirmed", "reason"],
  properties: {
    confirmed: { type: "boolean" },
    reason: { type: "string" },
  },
};

// ---- New mode: validate, draft, challenge, write ----

let path;
let draftTitle = null;
let premiseStats = null;
let keptTitles = [];
let droppedChanges = [];

if (mode === "new") {
  const problem = input.problem;
  const num = input.nextNumber;

  phase("Validate");
  log("Decomposing the problem into testable premises");
  const decomposition = await agent(
    "Decompose a reported spec problem into individually testable premises.\n\n" +
      "Problem:\n" +
      problem +
      "\n\nContext:\n" +
      context +
      "\n\n" +
      READ_ONLY +
      "\n" +
      "List every premise the problem rests on, including implicit ones (assumptions about process lifetimes, ownership, ordering, or who calls what). " +
      "Each premise is one falsifiable statement about what the spec says (spec-claim), what the code does (code-claim), what is missing (gap-claim), or what would go wrong (consequence-claim). " +
      "Mark loadBearing: true when refuting the premise would invalidate or materially redirect the problem. Cap the list at the ten most consequential premises.",
    { schema: PREMISES, label: "decompose" },
  );
  const premises = decomposition.premises.slice(0, 10);
  log(
    premises.length +
      " premises identified; dispatching one skeptic per premise",
  );

  const verdicts = (
    await parallel(
      premises.map(
        (p) => () =>
          agent(
            "Try to REFUTE this premise about the spec or implementation.\n\n" +
              "Premise (" +
              p.kind +
              "): " +
              p.statement +
              "\n\n" +
              "Original problem statement, for context only:\n" +
              problem +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Read the actual spec sections and code the premise is about. Return confirmed only when you found direct supporting evidence, refuted when the evidence contradicts the premise, and revised when the premise is directionally right but wrong in a detail that matters (provide revisedStatement). " +
              "Default to refuted when you cannot find supporting evidence.",
            {
              schema: PREMISE_VERDICT,
              label: "skeptic:" + p.id,
              phase: "Validate",
            },
          ).then((v) => ({ premise: p, ...v })),
      ),
    )
  ).filter(Boolean);

  const refuted = verdicts.filter((v) => v.verdict === "refuted");
  const standing = verdicts.filter((v) => v.verdict !== "refuted");
  premiseStats = { standing: standing.length, refuted: refuted.length };
  log(
    "Premises: " +
      standing.length +
      " standing, " +
      refuted.length +
      " refuted",
  );

  const loadBearing = verdicts.filter((v) => v.premise.loadBearing);
  if (
    loadBearing.length > 0 &&
    loadBearing.every((v) => v.verdict === "refuted")
  ) {
    return {
      mode,
      status: "not-viable",
      reason: "every load-bearing premise was refuted",
      verdicts,
    };
  }

  phase("Draft");
  const dossier = verdicts
    .map(
      (v) =>
        "- [" +
        v.verdict.toUpperCase() +
        "] " +
        (v.revisedStatement || v.premise.statement) +
        "\n  evidence: " +
        v.evidence.join("; ") +
        "\n  notes: " +
        v.notes,
    )
    .join("\n");

  const draft = await agent(
    "Draft a change proposal.\n\n" +
      "Problem:\n" +
      problem +
      "\n\n" +
      "Premise verdicts from independent skeptics (refuted premises are course corrections; the draft must not rest on them):\n" +
      dossier +
      "\n\n" +
      READ_ONLY +
      " Output the draft as structured data only; another agent writes the file.\n" +
      EVIDENCE +
      "\n" +
      "Project principles: " +
      PRINCIPLES +
      "\n" +
      "Read " +
      exemplar +
      " for the level of specificity expected, and read the spec sections each change targets. " +
      "Produce: a title; kind (fix corrects or reconciles existing behavior — spec text, core-product code, or test infrastructure; new adds a capability the spec or implementation lacks); a problem restatement grounded in the confirmed evidence; the review decisions that constrain the design; the change set (each change names its targets — spec files and sections, code packages and files, or test files — the rationale, and a concrete sketch of the staged edit); non-goals; open questions only for decisions that genuinely belong to the human reviewer. " +
      "Set viable: false with whyNotViable when the confirmed evidence shows no change is needed.",
    { schema: DRAFT, label: "draft" },
  );

  if (!draft.viable) {
    return { mode, status: "not-viable", reason: draft.whyNotViable, verdicts };
  }
  draftTitle = draft.title;
  log(
    'Draft "' + draft.title + '" proposes ' + draft.changes.length + " changes",
  );

  phase("Challenge");
  const challenged = (
    await parallel(
      draft.changes.map(
        (c) => () =>
          agent(
            "Adversarially challenge one proposed change. Your default posture is that the change is unnecessary.\n\n" +
              "Full draft for context:\n" +
              JSON.stringify(draft, null, 2) +
              "\n\n" +
              "Change under challenge: " +
              c.id +
              " — " +
              c.title +
              "\nTargets: " +
              c.targets.join(", ") +
              "\nRationale: " +
              c.rationale +
              "\nSketch: " +
              c.sketch +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Project principles: " +
              PRINCIPLES +
              "\n" +
              "Answer each question with evidence: (1) Does an existing spec surface, RPC, frame, field, or code path already cover this? (2) Is every factual premise under the change true in both the spec and the code, including process-lifetime and ownership assumptions? (3) Does the change contradict any other spec section? (4) Does it violate the project principles? (5) Is there a strictly smaller change that resolves the same problem? " +
              "Return drop when the change is unnecessary or rests on a false premise, revise with a concrete revision when the need is real but the change is wrong or oversized, and keep only when it survives all five questions.",
            {
              schema: CHALLENGE,
              label: "challenge:" + c.id,
              phase: "Challenge",
            },
          ).then((v) => ({ change: c, ...v })),
      ),
    )
  ).filter(Boolean);

  const kept = [];
  for (const r of challenged) {
    if (r.verdict === "drop")
      droppedChanges.push({
        id: r.change.id,
        title: r.change.title,
        reasons: r.reasons,
        evidence: r.evidence,
      });
    else if (r.verdict === "revise")
      kept.push({
        ...r.change,
        sketch: r.revision || r.change.sketch,
        challengeNotes: r.reasons,
      });
    else kept.push(r.change);
  }
  keptTitles = kept.map((c) => c.title);
  log(
    "Challenge: " +
      kept.length +
      " changes kept, " +
      droppedChanges.length +
      " dropped",
  );
  if (kept.length === 0) {
    return {
      mode,
      status: "no-change-needed",
      dropped: droppedChanges,
      verdicts,
    };
  }

  phase("Write");
  const slug = draft.title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);
  path = repo + "/proposals/" + num + "-" + slug + ".md";

  await agent(
    "Write a change proposal file.\n\n" +
      "HARD CONSTRAINT: the only file you may create or edit is " +
      path +
      ". Never modify anything under spec/, docs/, pkg/, cmd/, internal/, or sdks/. The proposal stages its changes — spec amendments, code changes, and test changes — as quoted spec text and precise change descriptions; it never applies them.\n\n" +
      "Draft (apply the challenge revisions in each sketch verbatim):\n" +
      JSON.stringify({ ...draft, changes: kept }, null, 2) +
      "\n\n" +
      "Dropped alternatives to record in Decisions or Open questions with their reasons:\n" +
      JSON.stringify(droppedChanges, null, 2) +
      "\n\n" +
      "Date: " +
      date +
      "\n" +
      "Format: follow the structure of " +
      exemplar +
      " exactly (read it first). A Podium proposal uses: an `# Proposal " +
      num +
      ": <Title>` heading; the `- Issue:` (a number or `(to be filed)`), `- Status: Draft`, and `- Date: " +
      date +
      "` bullets; a `## Summary`; a `## Current state and the gap` (or `## Decisions`); per-target staged edits as `## Spec amendment: §X.Y <topic>` sections, each quoting the exact replacement or added spec text in a `>` blockquote (behavioral spec prose only, no code-path references, cross-referencing other sections by §N.M) plus a precise anchor description for where it lands; a `## Proposed solution` describing the code and test changes when the proposal stages code; `## Open questions` only for decisions that genuinely belong to the human reviewer; `## Documentation changes` when docs/ pages need to follow the change; and a `## Resolved in adversarial review` section, initially noting that review rounds populate it. The Summary and gap sections cite spec text by §N.M and code by `pkg/...`, `internal/...`, or `cmd/...` paths; the quoted spec text inside a `## Spec amendment:` blockquote does not.\n" +
      "Prose rules: follow " +
      repo +
      "/.claude/rules/doc-style.md (read it first). " +
      "Read the spec sections each staged edit targets so anchors and surrounding text are quoted accurately.",
    { label: "write", phase: "Write" },
  );
  log("Proposal written to " + path);
} else {
  path = input.proposalPath.startsWith("/")
    ? input.proposalPath
    : repo + "/" + input.proposalPath;
}

// ---- Conventions pass (shared, one-shot, outside the error loop) ----

phase("Conventions");
await agent(
  "Check one proposal file against the written conventions and fix only violations.\n\n" +
    "HARD CONSTRAINT: the only file you may edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, cmd/, internal/, or sdks/.\n\n" +
    "The written rules: section structure and citation formats per the exemplar " +
    exemplar +
    " (read it first), and prose per " +
    repo +
    "/.claude/rules/doc-style.md (read it first). " +
    "Fix structural deviations and doc-style violations (fragments, missing list conjunctions, decorative em-dashes, marketing language). Do not change technical content, citations, or design decisions. If the file already conforms, change nothing and say so.",
  { label: "conventions" },
);

// ---- Review loop (shared): multi-lens review, two-skeptic verify, fix ----

const CONTEXT =
  "Repository: the repository root (your working directory). Podium is a Go registry for agentic AI artifacts plus tools that materialize them into each harness's native layout. The technical spec lives in spec/ (one file per top-level section, files are large; use Grep and targeted Read offsets, never read a whole spec file), implementation in pkg/, cmd/, and internal/, language SDKs in sdks/podium-py and sdks/podium-ts, docs in docs/.\n" +
  "The proposal under review: " +
  path +
  " (read it fully first).\n" +
  "Standing reference points (re-verify anything you rely on; line numbers drift):\n" +
  "- spec/02 §2.2: the component map (registry core, MCP server, CLI, SDKs) and the consumer surfaces; the shared Go library is the single behavioral surface, so standard, standalone, and filesystem-registry modes must materialize identical output (the §11 equivalence test).\n" +
  "- spec/09 §9.1: the SPI table (which pluggable interface owns which concern: RegistryStore, RegistrySearchProvider, LayerSourceProvider, IdentityProvider, HarnessAdapter, MaterializationHook, SignatureProvider, etc.) and §9.3 the forward-compatibility constraints every SPI method obeys. Every action a proposal assigns to a component must match an interface that can perform it.\n" +
  "- spec/06 §6.7 and §6.7.1: the harness adapters, the per-type target-path table (skill, agent, context, command, rule, hook, mcp-server across none/claude-code/claude-desktop/claude-cowork/cursor/codex/gemini/opencode/pi/hermes), the adapter output mechanisms (standalone file, bundled resources, inject, config-merge), the adapter sandbox contract (no network, no subprocess, no out-of-destination writes), and the §6.7.1 capability matrix. The code mirror is pkg/adapter (adapter.go DefaultRegistry lists the harnesses; capability.go encodes the matrix), checked by test/materialization (golden + validity) and test/harness_integration.\n" +
  "- spec/05 §5 the meta-tools (load_domain, search_domains, search_artifacts, load_artifact); spec/04 §4.3 manifest/types, §4.6 visibility, §4.7 tenancy; spec/08 audit events; §6.10 error codes.\n" +
  "- spec/10: phased MVP build sequence (initial vs enterprise phases); a deliverable cannot depend on an artifact a later phase introduces.\n" +
  "- spec/11: the verification suite (unit, integration, conformance, security, performance, soak, chaos); spec/13 deployment modes and §13.12 env vars.\n" +
  "- Traceability and gates (`make coverage-gate` = lint, speccov-drift, matrix-audit, doccov-check, coverage-budget): tests cite `// Spec: §X.Y` (speccov), matrix cells cite `// Matrix: §X.Y (...)` (matrix-audit covers §6.7.1, §6.10, §4.6, §4.3.5, §4.3), and runnable doc examples map to e2e tests via tools/doccov/manifest.yaml.\n" +
  "Notes from the orchestrator (leads, not evidence):\n" +
  context +
  "\n" +
  EVIDENCE;

const BAR =
  "REPORT ONLY REAL ERRORS. A finding qualifies only if at least one of these holds:\n" +
  "(a) A citation in the proposal is false: the cited file, line, or section does not say what the proposal claims, or the proposal attributes behavior to the wrong component.\n" +
  "(b) The proposal assigns an actor an action it cannot perform: it violates the §9.1 SPI ownership boundaries or the §9.3 forward-compatibility constraints, the §6.7 adapter sandbox contract or the materialization-writes-project-level-files-only rule, the §6.3 client-side-vs-registry-process identity split, the §2.2 shared-library deployment-mode parity (a change that makes standard, standalone, and filesystem-registry modes diverge), or the spec/10 build-phase ordering.\n" +
  "(c) The proposal contradicts the current spec, the current code, or itself, such that applying its edits would leave the spec internally inconsistent or the described implementation broken.\n" +
  "(d) The proposal misses an edit site: a spec/, docs/, sdks/, capability-matrix, or golden-file surface that would become wrong after the proposed edits are applied and that is absent from the proposal's edit lists. Editing a generated artifact instead of its authoring source, or changing a §6.7.1 capability cell in the spec without the pkg/adapter/capability.go mirror (or vice versa), counts.\n" +
  "(e) A described mechanism cannot work: race conditions, bypassable mandatory gates, unreachable states, wrong defaults, mismatched granularity, predicate drift between sections, config-merge or inject non-idempotency, or ordering problems.\n\n" +
  "DO NOT report: style or wording, documentation polish, optional improvements, extra test ideas, hypothetical hardening, redundancy, preferences between workable designs, or anything whose absence does not make the applied spec or implementation wrong. If you are unsure whether something meets the bar, do not report it. An empty findings list is a fully acceptable answer and is the expected answer for a converged proposal.\n\n" +
  'The proposal\'s "Resolved in adversarial review" section is a historical record of earlier passes; its descriptions of earlier drafts are not findings. Sections recording deliberately open decisions for the human reviewer are not findings.\n\n' +
  "Every finding MUST carry evidence: exact file paths with line numbers and short quotes for both the proposal's claim and the contradicting source. Read the files to verify line numbers; never cite from memory.";

const LENSES = [
  {
    key: "citations",
    text: 'Lens: citation audit. Extract every concrete citation in the proposal (file paths with line numbers, spec section references, quoted spec text, attributed behaviors such as "§X assigns Y to Z" or "function F does G"). Verify each one against the actual file content at the cited location. A citation whose target says something materially different, attributes the behavior to a different component, or does not exist is a finding. Off-by-a-few line drift on an otherwise accurate claim is NOT a finding unless the drift changes the meaning. Check data-flow directions (which side of a mirror is authoritative, for example the §6.7.1 capability matrix in spec versus pkg/adapter/capability.go) in the code itself.',
  },
  {
    key: "feasibility",
    text: "Lens: actor-action feasibility. For every action the proposal assigns to a component, verify the component exists under that name and can perform the action under the spec: the §9.1 SPI ownership boundaries (which interface owns the concern) and the §9.3 forward-compatibility constraints (context-aware, wire-serializable, no shared in-process state, structured errors, idempotent retries); the §6.7 adapter sandbox contract (no network, no subprocess, no out-of-destination writes) and the rule that materialization writes project-level files only; the §6.3 split between client-side identity providers (acquire a token at the consumer) and registry-process providers (verify or resolve identity at the registry); the §2.2 shared-library invariant (the registry core, the MCP server, the CLI, and the SDKs share one behavioral surface, so a behavior cannot live in one consumer and silently differ in another); and spec/10 phase ordering (a deliverable in an earlier phase must not require artifacts a later phase introduces). Any assignment the actor cannot fulfil is a finding.",
  },
  {
    key: "edit-sites",
    text: "Lens: edit-site completeness. Enumerate every identifier the proposal adds, changes, or removes (env vars `PODIUM_*`, error codes in §6.10, manifest frontmatter fields, meta-tool names, CLI commands and flags, adapter values / harness IDs, §6.7.1 capability-matrix cells, audit event names, config keys). Grep spec/, docs/, sdks/, pkg/, and the matrix sources (tools/matrix/matrices.go) for each one and for the concepts they replace. Any surface that becomes incorrect or internally inconsistent after the proposed edits are applied, and that is missing from the proposal's edit lists, is a finding. Check authored-vs-generated chains (a §6.7.1 capability cell lives in both spec/06 and pkg/adapter/capability.go; a harness output path lives in spec/06 §6.7, pkg/adapter, and the test/materialization golden files). Check companion pairs: an error code with its §6.10 entry and its §6.10 matrix cell; a harness/type/field/rule-mode/hook-event change with its §6.7 path table, §6.7.1 capability matrix, capability.go mirror, and golden file; a spec section with the test that cites it (`// Spec: §X.Y`); a runnable doc example with its tools/doccov/manifest.yaml entry.",
  },
  {
    key: "mechanism",
    text: "Lens: end-to-end mechanism. Trace each flow the proposal describes from origin to final effect and hunt for: the materialization pipeline (fetch → adapter → MaterializationHook → atomic write) producing wrong or non-idempotent output; config-merge and inject reconciliation that is not idempotent on re-sync (Podium-owned entries must be reconciled, not duplicated or orphaned); deployment-mode divergence (standard, standalone, and filesystem-registry modes must produce byte-identical materialized output per the §11 equivalence test); visibility filtering or layer composition (`extends:`) that resolves the wrong effective view; mandatory gates a write path bypasses; ingest-lint versus materialization enforcement of `target_harnesses:` and `✗`/`⚠` cells (§6.7, §6.9); triggers that can never fire; defaults that contradict stated behavior; granularity mismatches; and predicate drift (the same condition stated with different conjuncts across design prose, a summary table, quoted spec text, capability.go, and tests). Also verify the proposal's quoted replacement spec text is internally consistent with the rest of the spec it embeds in.",
  },
  {
    key: "security",
    text: "Lens: security. Always run. Two checks. (1) Regression of an established control — OAuth-attested identity required to reach the registry (§6.3); fail-closed visibility filtering (a caller without matching visibility on a layer sees nothing, §4.6); the adapter and MaterializationHook sandbox contract (no network, no subprocess, no out-of-destination writes, §6.7, §9.1); `oidc-jwt` token verification (signature, `iss`, `aud`, `exp`, JWKS fetched over https, §6.3.3); the `trusted-headers` controls (proxy secret, bind restriction, the multi-tenant secret requirement, §6.3.3); tenant isolation (§4.7); tokens held in the OS keychain; the hash-chained audit log and PII scrubbing of query text (§8); and Sigstore signature verification on materialization. A change that silently removes, bypasses, or feature-gates a mandatory control, or makes a security path fail open, is a finding. (2) Trust boundary of a security-bounding value — for every value that bounds a security property (an org/tenant selection, a visibility decision, a verified claim), confirm the authoritative source is a trusted component and not a caller-supplied or unverified input. The §6.3.3 analysis is the bar: an `oidc-jwt` `org_id` is trusted because the token is cryptographically verified, whereas a `trusted-headers` `X-Podium-User-Org` is trusted only because the gateway is assumed to have set it, which is why that mode constrains the bind and requires a proxy secret in multi-tenant mode. A new security-bounding value sourced from an unverified caller input, with no equivalent control, is a finding. Merely less strict than it could be is NOT a finding.",
  },
  {
    key: "harness",
    text: "Lens: harness compatibility. Always run. Judge the change against each and all of the supported harnesses' capabilities and their expected native layouts, inputs, and formats. The supported set is none, claude-code, claude-desktop, claude-cowork, cursor, codex, gemini, opencode, pi, and hermes (the source of truth is pkg/adapter/adapter.go DefaultRegistry and the §6.7 table). For any change that adds, changes, or removes a harness, an artifact type, a frontmatter field, a `rule_mode`, a `hook_event`, an adapter output mechanism, or a target path, verify it is consistent and COMPLETE across every parallel representation: (1) the §6.7 per-type target-path table and the output-mechanism notes (standalone file, bundled resources, inject, config-merge); (2) the §6.7.1 capability matrix in the spec (type materialization, frontmatter-field fidelity, rule modes, hook events) AND its code mirror pkg/adapter/capability.go (the `capabilityMatrix`); (3) the adapter implementation in pkg/adapter (adapter.go, none.go, claudecode.go, builtins.go, layout.go); (4) the golden files test/materialization/testdata/golden/<harness>.golden and the validity checks in test/materialization/validity_test.go; (5) the matrix-audit cells for §6.7.1, §4.3 (rule_mode × harness), and §4.3.5 (hook events); and (6) the per-harness docs (docs/consuming/<harness>.md) and docs/authoring. Then check each harness honors its native format: the correct file extension and location (`.md`, `.toml`, `.mdc`, `SKILL.md`, the `.json` config files), the harness's config schema (JSON keys, TOML tables, `.mdc` and `SKILL.md` frontmatter), the inject markers, and the documented partial or migrating surfaces (Codex `command` is `✗` and folds into skills; Claude Desktop has no project-level surface; Hermes reuses the Cursor `.mdc` rule format and is user-scope elsewhere; OpenCode and Pi `hook` is `✗`). Verify the cross-harness core feature set (§6.7.1, the cells that are `✓` everywhere) still holds for author-once/load-anywhere, that a non-portable feature is gated by `target_harnesses:` and graded `✗`/`⚠` consistently with ingest-lint versus materialization enforcement (§6.9), and that the adapter sandbox contract is preserved. A change that updates one harness's surface while leaving a parallel representation inconsistent, omits a capability cell for an added type/field/mode/event/harness, contradicts a harness's documented native layout or config schema, or silently breaks the core feature set is a finding; name the harness, the surface, and the concrete consequence.",
  },
  {
    key: "performance",
    text: "Lens: performance, scalability, and failure-mode reliability at the stated load. Always run. Quantify the read and write rates the proposal creates against the budgets the spec states: the §11 performance targets (1K QPS sustained for `search_artifacts`, 100 ingests/min, `load_artifact` p99 under the §7.1 SLO, cold-cache versus warm-cache materialization budgets). Hunt for per-request write amplification, N+1 or unbounded queries, the `search_artifacts` `top_k` cap (50) being bypassed, re-embedding on every query, content-addressed-cache and adapter memo-cache (5-minute TTL) misses the proposal introduces, and presigned-URL refresh storms; state the math against Postgres / SQLite, the object store (S3), the vector backend (pgvector / sqlite-vec / managed), and the embedding provider. Then test failure-mode reliability: trace what survives and what stalls during a Postgres failover, an object-storage stall, an IdP/JWKS outage (verification must fail closed), a layer source unreachable during ingest, and registry-offline (the cache serves and a miss reports an explicit offline status). A new bottleneck at the stated load, or a failure mode less reliable than the shipped behavior, is a finding; an absent SLO percentile that the spec leaves operationally tunable is not.",
  },
  {
    key: "reliability",
    text: "Lens: reliability and fault tolerance. Always run. Judge whether the recovery, retry, and idempotency mechanisms the proposal relies on are correct under crash, restart, redelivery, and store failover. This lens owns recovery-mechanism correctness; the performance lens owns the capacity and state-survival math and the security lens owns fail-closed on security paths, so do not re-file their findings here. Trace every operation the proposal adds or changes and hunt for: a non-idempotent ingest (the §4 immutability rule — same id+version with different content must return `ingest.immutable_violation`; a `git`-source force-push under the tolerant policy must preserve the previously-ingested bytes and emit `layer.history_rewritten`); a webhook ingest (at-least-once delivery) whose consumer has no dedup or signature-verification guard; a `podium sync` that is not idempotent or that corrupts `.podium/sync.lock`; config-merge or inject reconciliation that duplicates or orphans Podium-owned entries on re-sync instead of reconciling them; atomic materialization that leaves a partial tree on failure, or a partial-download or presigned-URL-expiry path with no recovery; an audit hash chain that cannot detect a gap or survive a restart; a JWKS refresh that does not fail closed while the key set is unavailable; and an outbound call (Git fetch, IdP, object store, embedding provider) with no timeout or bounded backoff so one hung dependency stalls the path. A recovery mechanism that loses, double-applies, leaks, or stalls under the exact failure it exists to handle is a finding; a design merely slower to recover than an alternative is not.",
  },
  {
    key: "client-surface",
    text: "Lens: client-facing surface integrity. Always run. Identify every externally-consumed contract the proposal adds, changes, or removes, and verify the change is intentional and complete across all of its parallel representations. The client-facing surfaces are the registry HTTP API (§7) and the language SDKs that wrap it (sdks/podium-py, sdks/podium-ts); the MCP meta-tools (§5: `load_domain`, `search_domains`, `search_artifacts`, `load_artifact`) and their input/output schemas; the CLI commands and flags (`podium ...`, §7 and §13); the manifest frontmatter schema and first-class types (§4); the harness-adapter outputs (§6.7, owned by the harness lens but cross-checked here for SDK/doc parity); the configuration and env vars (§6, §13.12); the error codes (§6.10); and the audit event names (§8). A change to one representation not mirrored in its parallels is a finding: a REST field missing from an SDK or the docs; an MCP tool-schema change missing from an SDK or doc; a removed or renamed field still advertised by a served schema, an SDK, or a doc; an error code or audit event changed without every emitter and consumer updated. Enforce the origin rule: a name an external standard defines (an MCP protocol primitive, an OAuth/OIDC claim, a harness's own native config key) must not be renamed, while Podium-defined surfaces may change. Podium is pre-1.0 with no backward-compatibility shims, so a deliberate, complete breaking change is not itself a finding; an incomplete or inconsistent one, or an internal surface changed while a parallel client surface still serves the old contract, is.",
  },
  {
    key: "docs-alignment",
    text: "Lens: documentation alignment. Always run. The docs/ tree is downstream of the spec and the implementation: docs follow the spec and the code and are never the source of truth for a spec or core-product decision. Identify every behavior the proposal changes — a spec edit, a code change, a renamed, removed, or added identifier, a changed default, error code, env var, CLI flag, meta-tool, harness output, or audit event — and verify it is reflected in a staged docs/ edit wherever docs/ currently describes that behavior, and that the staged docs edits leave docs/ internally consistent and consistent with the post-change spec. The docs surfaces are the guide pages (docs/getting-started, docs/authoring, docs/consuming/<harness>.md, docs/deployment), the reference pages (docs/reference), and the runnable examples that tools/doccov/manifest.yaml maps to e2e tests (a new or changed runnable example needs its manifest entry and its doc-e2e test). A docs/ page left describing superseded behavior, an added runnable example missing its doccov mapping, or a staged docs edit that contradicts the post-change spec, is a finding. Two hard guardrails: (1) never raise a finding that asks the spec or the implementation to change to match an existing doc; when a doc and the spec disagree the doc is the defect and is reconciled toward the spec, so a finding here is always a missing or wrong docs edit. (2) A doc-described scenario may be cited as a candidate test case only after that doc has been verified against the spec.",
  },
];

const EXTRAS = [
  {
    key: "operational",
    text: "Lens: operational consistency. Check that the audit events, error codes, config/env vars, and operator documentation the proposal touches stay mutually consistent after application: every error code the proposal references has a §6.10 entry and a real emitting surface; every audit event it references is emitted by a spec-defined surface (§8) and documented; every new config/env var (§13.12) is read by the code path the proposal describes and documented in docs/deployment and OPERATIONS.md; and the §6.7.1, §6.10, §4.6, and §4.3 matrices the proposal touches still enumerate exactly the cells the change implies (matrix-audit). An inconsistency that would mislead an operator about the system's actual behavior is a finding.",
  },
  {
    key: "fresh",
    text: "Lens: fresh holistic read. Read the proposal as the spec maintainer who must apply its staged edits verbatim tomorrow. Independently spot-check the assumptions the other lenses might share blind spots on, in whatever order seems most suspicious to you. Anything that would make the applied spec wrong, internally inconsistent, or unimplementable is a finding.",
  },
];

function reviewPrompt(lens, round, fixedTitles, rejected) {
  let history = "";
  if (fixedTitles.length > 0) {
    history +=
      "\n\nAlready found and fixed in earlier rounds (the current proposal text reflects these fixes; do not re-litigate them): " +
      fixedTitles.join("; ") +
      ".";
  }
  if (rejected.length > 0) {
    history +=
      "\n\nAlready examined and refuted in earlier rounds (do not re-report these or close variants):\n" +
      rejected
        .map((r) => "- " + r.title + ": refuted because " + r.reason)
        .join("\n");
  }
  return (
    "You are an adversarial reviewer in round " +
    round +
    " of an iterative convergence loop for a change proposal.\n\n" +
    CONTEXT +
    "\n\n" +
    READ_ONLY +
    "\n\n" +
    BAR +
    "\n\n" +
    lens.text +
    history +
    "\n\nWork method: read the proposal fully, then investigate the repository with Grep and targeted Reads to verify or refute its claims under your lens. Report your findings via the structured output (empty array if you find nothing that meets the bar)."
  );
}

function dedupPrompt(findings) {
  return (
    "You merge duplicate review findings. Below is a JSON array of findings from several independent reviewers examining the same proposal. Merge entries that describe the same root error (even if phrased differently or found at different citation points): keep one entry per root error, choose the clearest title, and combine the strongest evidence. Do not drop distinct errors. Do not add new findings. Do not judge validity. Return the merged list.\n\nFindings:\n" +
    JSON.stringify(findings, null, 2)
  );
}

function evidencePrompt(f) {
  return (
    "You are a skeptical evidence verifier. A reviewer claims the following error in the proposal " +
    path +
    ". Independently re-derive it: read the proposal at the claimed location and read every cited source file at the cited lines.\n\nConfirm ONLY if all three hold: (1) the proposal really says what the finding claims it says; (2) the cited sources really say what the finding claims they say; (3) the contradiction or infeasibility actually follows from (1) and (2). If any citation is wrong, a quote is out of context, the proposal already handles the issue elsewhere in its text, or the conclusion does not follow, refute with the specific reason.\n\n" +
    CONTEXT +
    "\n\n" +
    READ_ONLY +
    "\n\nFinding:\n" +
    JSON.stringify(f, null, 2)
  );
}

function materialityPrompt(f) {
  return (
    "You are a skeptical materiality judge for review findings on the proposal " +
    path +
    ". Assume the finding's evidence is factually accurate. Decide ONLY whether fixing it is required for correctness: confirm if leaving it unfixed would make the applied spec internally inconsistent, make a stated citation or attribution false, or make the described implementation not work. Refute if it is style or wording, documentation polish, an optional improvement or hardening, redundancy, a preference between workable designs, a test-coverage suggestion, or anything else whose absence does not make the spec or implementation wrong. Default to refuted when uncertain. You may read " +
    path +
    " for context.\n\nFinding:\n" +
    JSON.stringify(f, null, 2)
  );
}

function fixPrompt(confirmed, round) {
  return (
    "You are the fixer for round " +
    round +
    " of an iterative convergence loop on the proposal " +
    path +
    ".\n\n" +
    CONTEXT +
    "\n\nHARD CONSTRAINT: the only file you may edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, cmd/, internal/, or sdks/.\n\nApply EXACTLY the confirmed findings below using Edit (or Write for large restructures). Requirements:\n" +
    "- Before each edit, re-verify the relevant spec/code citations yourself with Grep/Read; every claim that remains in the proposal must be accurate and carry file:line evidence. Re-verify every citation in text you touch, including stale line numbers.\n" +
    "- Make the smallest change that corrects each finding. Do not expand scope. Do not change design decisions beyond what the findings require; when a finding forces a design choice, pick the option most consistent with the cited spec precedent and the project principles (" +
    PRINCIPLES +
    "), and record the rationale in the proposal.\n" +
    "- When a fix changes a trigger predicate or invariant, propagate the exact same predicate to every section that states it (design sections, summary tables, constant comments, proposed spec text, and tests) so no drift is introduced.\n" +
    "- Keep the proposed-changes section (however the proposal titles it) and any files-touched section consistent with your edits.\n" +
    '- Append a new subsection to the proposal\'s "Resolved in adversarial review" section titled "### Pass <N> (' +
    date +
    ', automated)", where <N> continues the existing pass numbering (read the section to determine it; create the section before the open-decisions section if it does not exist), with one bullet per finding fixed, matching the format of any existing entries.\n' +
    "- Follow the documentation style rules in " +
    repo +
    '/.claude/rules/doc-style.md: complete declarative sentences, no "X, not Y" rhythm, no decorative em-dashes, no marketing language, conjunctions in lists.\n\nConfirmed findings (JSON):\n' +
    JSON.stringify(confirmed, null, 2) +
    "\n\nReturn a short summary listing each finding and the exact edit you made for it."
  );
}

phase("Review");
const fixedTitles = [];
const rejected = [];
const history = [];
let cleanStreak = 0;
let round = 0;
let reviewersFailed = false;

while (round < maxRounds && cleanStreak < CLEAN_TARGET) {
  round++;
  const lenses = LENSES.concat([EXTRAS[(round - 1) % EXTRAS.length]]);
  log(
    "Round " +
      round +
      ": launching " +
      lenses.length +
      " reviewers (clean streak " +
      cleanStreak +
      "/" +
      CLEAN_TARGET +
      ")",
  );

  // Barrier: the dedup step needs every reviewer's findings at once.
  const results = (
    await parallel(
      lenses.map(
        (l) => () =>
          agent(reviewPrompt(l, round, fixedTitles, rejected), {
            label: "r" + round + ":review:" + l.key,
            phase: "Round " + round + ": review",
            schema: FINDINGS,
          }),
      ),
    )
  ).filter(Boolean);

  if (results.length === 0) {
    log("Round " + round + ": every reviewer failed; stopping");
    reviewersFailed = true;
    break;
  }
  const raw = results.flatMap((r) => r.findings);
  log("Round " + round + ": " + raw.length + " raw findings");

  if (raw.length === 0) {
    cleanStreak++;
    history.push({ round, raw: 0, deduped: 0, confirmed: 0 });
    continue;
  }

  let deduped = raw;
  if (raw.length > 1) {
    const d = await agent(dedupPrompt(raw), {
      label: "r" + round + ":dedup",
      phase: "Round " + round + ": review",
      schema: FINDINGS,
    });
    if (d && d.findings.length > 0) deduped = d.findings;
  }
  log(
    "Round " +
      round +
      ": " +
      deduped.length +
      " findings after dedup; verifying",
  );

  const verdicts = await parallel(
    deduped.map(
      (f) => () =>
        parallel([
          () =>
            agent(evidencePrompt(f), {
              label: "r" + round + ":verify-evidence",
              phase: "Round " + round + ": verify",
              schema: VERDICT,
            }),
          () =>
            agent(materialityPrompt(f), {
              label: "r" + round + ":verify-material",
              phase: "Round " + round + ": verify",
              schema: VERDICT,
            }),
        ]).then((vs) => ({ f, vs: vs.filter(Boolean) })),
    ),
  );

  const live = verdicts.filter(Boolean);
  const confirmed = live
    .filter((v) => v.vs.length === 2 && v.vs.every((x) => x.confirmed))
    .map((v) => v.f);
  live
    .filter((v) => !(v.vs.length === 2 && v.vs.every((x) => x.confirmed)))
    .forEach((v) => {
      rejected.push({
        title: v.f.title,
        reason:
          v.vs
            .filter((x) => !x.confirmed)
            .map((x) => x.reason)
            .join(" | ") || "verifier unavailable",
      });
    });
  log(
    "Round " +
      round +
      ": " +
      confirmed.length +
      "/" +
      deduped.length +
      " findings confirmed",
  );
  history.push({
    round,
    raw: raw.length,
    deduped: deduped.length,
    confirmed: confirmed.length,
    confirmedTitles: confirmed.map((f) => f.title),
  });

  if (confirmed.length === 0) {
    cleanStreak++;
    continue;
  }
  cleanStreak = 0;

  const fixSummary = await agent(fixPrompt(confirmed, round), {
    label: "r" + round + ":fix",
    phase: "Round " + round + ": fix",
  });
  confirmed.forEach((f) => fixedTitles.push(f.title));
  history[history.length - 1].fixSummary = fixSummary || "fixer unavailable";
}

const converged = cleanStreak >= CLEAN_TARGET && !reviewersFailed;
if (converged) {
  await agent(
    "Update one proposal's Status bullet to record verification.\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " +
      path +
      ". Never modify anything under spec/, docs/, pkg/, cmd/, internal/, or sdks/.\n\n" +
      'Read the proposal\'s header bullets. Replace the Status bullet\'s leading state (for example "Draft") with: "Verified (' +
      date +
      "). Converged after " +
      round +
      " adversarial review rounds (" +
      fixedTitles.length +
      ' findings fixed); awaiting sign-off." Preserve any later clauses of the bullet that remain true (for example a pointer to the pass-history section), drop clauses the new state supersedes, and follow ' +
      repo +
      "/.claude/rules/doc-style.md.",
    { label: "mark-verified", phase: "Review" },
  );
  log("Proposal marked Verified");
}

return {
  mode,
  status: mode === "new" ? "written" : "reviewed",
  path,
  title: draftTitle,
  premises: premiseStats,
  changes:
    mode === "new"
      ? { kept: keptTitles, dropped: droppedChanges.map((d) => d.title) }
      : undefined,
  review: {
    converged,
    reviewersFailed,
    rounds: round,
    cleanStreak,
    totalFixed: fixedTitles.length,
    fixedTitles,
    rejectedTitles: rejected.map((r) => r.title),
    history,
  },
};
