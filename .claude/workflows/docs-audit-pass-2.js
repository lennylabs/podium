export const meta = {
  name: 'docs-audit-pass-2',
  description:
    'Second correctness audit of the documentation, partitioned across directories so each page is read beside different neighbours, with every finding confirmed by two independent verifiers before it is applied',
  whenToUse:
    'A follow-up documentation audit after an earlier pass has already fixed the obvious errors. The partition cuts across the directory tree, so contradictions between pages that live in different sections surface instead of hiding inside a section.',
  phases: [
    { title: 'Find', detail: 'one fresh reviewer per journey group' },
    { title: 'Verify', detail: 'two independent skeptics per group, both must confirm' },
    { title: 'Fix', detail: 'apply only doubly-confirmed findings, per group' },
    { title: 'Consistency', detail: 'one pass for contradictions between groups' },
    { title: 'Report', detail: 'consolidate' },
  ],
}

// The first pass partitioned by directory, so a reviewer saw docs/authoring/
// together and never compared it against docs/reference/. This partition
// follows what a reader does instead, which puts pages from different
// directories in front of the same reviewer. Groups still own disjoint files,
// so the fix stages cannot collide.
const GROUPS = [
  {
    key: 'first-contact',
    pages:
      'README.md, docs/overview.md, docs/getting-started/index.md, docs/getting-started/why-podium.md, docs/getting-started/quickstart.md, docs/deployment/local.md',
    focus:
      'The path from install to a materialized artifact. Every command must run as written, in the order written, and produce the output shown. The local tier claims here must match what docs/deployment/local.md says about the same tier.',
  },
  {
    key: 'model-and-vocabulary',
    pages:
      'docs/getting-started/concepts.md, docs/getting-started/how-it-works.md, docs/authoring/artifact-types.md, docs/reference/glossary.md, docs/reference/frontmatter-schema.md',
    focus:
      'These five define the same nouns. A term defined one way in concepts.md and another way in glossary.md is a finding even when neither is wrong on its own. Check every type and every field against pkg/manifest.',
  },
  {
    key: 'authoring-walkthroughs',
    pages:
      'docs/authoring/index.md, docs/authoring/your-first-skill.md, docs/authoring/your-first-command.md, docs/authoring/your-first-agent.md, docs/authoring/hints.md, docs/authoring/bundled-resources.md',
    focus:
      'Runnable authoring steps. Every file the reader is told to create, every field in it, and the layout that results after a sync. Check the size limits and the resource conventions against the code that enforces them.',
  },
  {
    key: 'authoring-semantics',
    pages:
      'docs/authoring/frontmatter-reference.md, docs/authoring/domains.md, docs/authoring/rule-modes.md, docs/authoring/hooks.md, docs/authoring/extends.md',
    focus:
      'Semantics rather than syntax: merge and precedence order, what extends inherits and what it overrides, hook event names, and which rule mode each adapter actually emits. Check against pkg/manifest, pkg/lint, and pkg/adapter.',
  },
  {
    key: 'delivery',
    pages:
      'docs/consuming/index.md, docs/consuming/configure-your-harness.md, docs/consuming/selective-materialization.md, docs/consuming/browsing-the-catalog.md, docs/consuming/custom-via-sdk.md, docs/consuming/handling-artifact-responses.md, docs/consuming/publishing.md, docs/reference/cli.md',
    focus:
      'Per-harness destination tables against pkg/adapter, every SDK call against the real signatures in sdks/, and every flag in cli.md against its command. A flag documented in a consuming page but absent from cli.md, or the reverse, is a finding.',
  },
  {
    key: 'serving',
    pages:
      'docs/deployment/index.md, docs/deployment/single-node.md, docs/deployment/clustered.md, docs/deployment/layers.md, docs/deployment/access-control.md, docs/deployment/integrations.md, docs/deployment/progressive-adoption.md, docs/reference/http-api.md, docs/reference/error-codes.md',
    focus:
      'Which tier provides which capability, every PODIUM_* variable and its default against internal/serverboot, and every route, method, status code, and error code against pkg/registry/server. A capability claimed for a tier in deployment/index.md must match the tier page for that tier.',
  },
  {
    key: 'identity',
    pages:
      'docs/deployment/oidc/index.md, docs/deployment/oidc/okta.md, docs/deployment/oidc/entra-id.md, docs/deployment/oidc/google-workspace.md, docs/deployment/oidc/auth0.md, docs/deployment/oidc/keycloak.md, docs/deployment/gateway-delegated-identity.md',
    focus:
      'Identity configuration against pkg/identity and internal/serverboot. Which provider values the registry accepts, which settings each provider requires, what each claim mapping reads, and whether the per-provider walkthroughs agree with one another where they configure the same thing.',
  },
  {
    key: 'operations-and-meta',
    pages:
      'docs/deployment/operator-guide.md, docs/deployment/extending.md, docs/deployment/vector-backends.md, docs/testing/index.md, docs/testing/live-vector-backends.md, docs/about/index.md, docs/about/status.md, docs/about/contributing.md, docs/about/governance.md, docs/about/changelog.md, docs/reference/index.md, docs/rfc/README.md',
    focus:
      'Operational commands with the flags shown, metric names, backend selection against pkg/vector and pkg/embedding, and every "shipped" or "planned" claim in about/status.md against what is actually in the tree.',
  },
]

const GROUND_RULES = `
## Sources of truth

1. \`spec/\` is authoritative for intent. It is **read-only**: never edit it.
2. The code is authoritative for what ships: \`cmd/\`, \`pkg/\`, \`internal/\`, \`sdks/\`.

Where the spec and the code disagree, that is a finding in its own right. Report it with \`specCodeGap: true\`; never silently pick a side.

## The documentation describes what ships today, including its defects

An earlier pass verified the items below. They are known, they are recorded, and they are **not** being fixed in this pass. Documentation that describes the current behaviour accurately is **correct** and must be left alone. Rewriting a page to describe the behaviour these will have once fixed would make the page wrong.

- \`deploy/helm/podium/values.yaml\` defaults to \`oauth-device-code\`, defaults the embedding provider to \`openai\`, and sets a \`config.bind\` no template renders. \`docs/deployment/clustered.md\` documents the three workarounds on purpose.
- \`pkg/identity/oidc_jwt.go\` trims a trailing slash from the configured issuer but not from the token claim, so an issuer URL ending in \`/\` cannot verify.
- \`/v1/admin/runtime\` is not admin-gated in the production wiring, and \`/v1/admin/reembed\` is authenticated but not admin-gated.
- The spec's \`spec/13-deployment.md:467\` and \`:547\` describe device-code support the code does not provide.

Do not report these. Report anything else you find of the same kind.

## Facts already established, so you need not re-derive them

- \`oauth-device-code\` is a client-side provider. Setting it as the registry's \`PODIUM_IDENTITY_PROVIDER\` aborts startup with \`config.identity_provider_unverified\`. The registry verifies \`injected-session-token\`, \`oidc-jwt\`, and \`trusted-headers\`.
- There is no \`--marketplace\` flag and no \`podium publish\` command. Marketplace rendering goes through a \`kind: marketplace\` entry under \`targets:\`, reached with \`--config\`.
- \`registry.yaml\` nests every key under a top-level \`registry:\`.
- No embedding model ships in the binary. A single node defaults the provider to \`ollama\` and a Postgres-backed deployment to \`openai\`; neither is self-contained. \`PODIUM_NO_EMBEDDINGS=true\` opts out to keyword search, which is what the repository's \`docker-compose.yml\` now sets.
- A local catalog composes ordered layers from disk through \`.registry-config\`.
- Cursor skills go to \`.cursor/skills/<name>/SKILL.md\`; only rules become \`.mdc\`. Codex skills live under \`.agents/\`. Claude Code MCP servers go to \`.mcp.json\`.
- For \`type: rule\` and \`type: mcp-server\`, no adapter calls \`appendResources\`, so bundled files are dropped.
- \`podium sync\` reconciles the whole target against the lock file, so a run with a different \`--harness\` removes what the previous run wrote.

## Vocabulary

The deployment tiers are Local, Single node, and Clustered. These must not appear: "filesystem mode", "standalone mode", "standard mode", "server mode" as a tier name, "skill pack", "Solo / filesystem", "Small team", "Organization". \`--standalone\` is a real flag and may be named.

Never reference anything under \`proposals/\`, or use the word "proposal".
`

const FINDINGS_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['findings'],
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['id', 'file', 'line', 'claim', 'reality', 'evidence', 'fix', 'severity'],
        properties: {
          id: { type: 'string', description: 'short kebab-case slug, unique in this group' },
          file: { type: 'string' },
          line: { type: 'number' },
          claim: { type: 'string', description: 'what the doc says today, quoted' },
          reality: { type: 'string', description: 'what the code or spec actually says' },
          evidence: { type: 'string', description: 'file:line citations that prove it' },
          fix: { type: 'string', description: 'the exact replacement text' },
          severity: { type: 'string', enum: ['breaking', 'wrong', 'incomplete', 'style'] },
          specCodeGap: { type: 'boolean', description: 'true when spec and code disagree' },
          crossPage: {
            type: 'boolean',
            description: 'true when two pages contradict each other rather than the code',
          },
        },
      },
    },
  },
}

const VERDICTS_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['verdicts'],
  properties: {
    verdicts: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['id', 'confirmed', 'reason'],
        properties: {
          id: { type: 'string' },
          confirmed: { type: 'boolean' },
          reason: { type: 'string', description: 'the evidence you re-derived yourself' },
          correctedFix: {
            type: 'string',
            description:
              'a better replacement when the finding is real but its fix is wrong; otherwise empty',
          },
        },
      },
    },
  },
}

const FIX_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['applied', 'skipped', 'summary'],
  properties: {
    applied: { type: 'number' },
    skipped: { type: 'number' },
    summary: { type: 'string' },
    details: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['id', 'outcome'],
        properties: {
          id: { type: 'string' },
          outcome: { type: 'string', enum: ['fixed', 'skipped'] },
          note: { type: 'string' },
        },
      },
    },
  },
}

phase('Find')

const results = await pipeline(
  GROUPS,

  // Stage 1 — a fresh reviewer per group. No reviewer sees another group.
  (group) =>
    agent(
      `Review these Podium documentation pages against the code and the specification, in /Users/joan/projects/podium.

This is a second audit. An earlier pass already corrected the obvious errors, so the remaining defects are the ones that survive a careful read: a default that changed, a branch the first reviewer did not follow, a table row that is right for one harness and wrong for another, and two pages that each look right alone but disagree with each other.

## Your pages

${group.pages}

Read every one of them line by line. Do not review any page outside this list; other reviewers own those.

## What to look hardest at

${group.focus}

${GROUND_RULES}

## What counts as a finding

A factual error a reader would act on: a flag or command that does not exist, a wrong path, a wrong default, a wrong type or field name, a capability attributed to the wrong tier or component, a config example that would not parse or would be silently ignored, or an output block that does not match what the code prints.

Two of your pages stating incompatible things about the same subject is a finding even when you cannot tell which one is wrong from the pages alone. Go to the code, decide which is right, and set \`crossPage: true\`.

Style problems are findings only when they change meaning. Do not report preferences.

For every finding, give the exact replacement text in \`fix\`. Another agent will apply it verbatim, so it must be correct and complete.

Be precise about evidence. Every finding needs a \`file:line\` citation in the code or the spec that a skeptic can check independently. A finding without one will be thrown out.

Report only. Do not edit any file.`,
      { label: `find:${group.key}`, phase: 'Find', schema: FINDINGS_SCHEMA },
    ).then((r) => ({ group, findings: (r && r.findings) || [] })),

  // Stage 2 — two independent skeptics. A finding survives only if both agree.
  (found) => {
    if (found.findings.length === 0) {
      log(`${found.group.key}: no findings`)
      return { ...found, confirmed: [] }
    }

    const brief = (angle) =>
      `You are verifying documentation findings for the Podium repository at /Users/joan/projects/podium.

Someone else reviewed ${found.group.pages} and reported the findings below. Your job is to decide which are real. Assume some are wrong: a reviewer who reads one function and misses a branch produces a confident, false finding, and acting on it makes the documentation worse than leaving it alone.

This is a second audit over documentation an earlier pass already corrected. That raises the base rate of false findings, because the text a reviewer finds surprising is now more often the text that is carefully right.

${angle}

For each finding:

1. **Re-derive the evidence yourself from the code.** Do not trust the finding's citation, its quotation, or its reasoning. Open the file. If the quoted doc text does not appear as quoted, or the code does not say what is claimed, the finding is refuted.
2. **Check the proposed fix.** A finding can be real while its \`fix\` is wrong or incomplete. When that happens, confirm the finding and supply a better replacement in \`correctedFix\`.
3. **Default to refuted when uncertain.** A missed defect survives to the next pass. A confirmed wrong "correction" ships.

${GROUND_RULES}

Findings to verify:

${JSON.stringify(found.findings, null, 2)}`

    return parallel([
      () =>
        agent(
          brief(
            'Your angle: **the code**. Work from the implementation outward. Trace the actual call path, check the branch the finding depends on, and confirm the behaviour is what is claimed at runtime. For a finding about a default, find where the default is applied and what overrides it.',
          ),
          { label: `verify-code:${found.group.key}`, phase: 'Verify', schema: VERDICTS_SCHEMA },
        ),
      () =>
        agent(
          brief(
            'Your angle: **the specification and the reader**. Check the finding against `spec/`, and ask whether the proposed replacement is actually true, complete, and consistent with how the rest of the corpus describes the same thing. A fix that is locally right but contradicts another page is not confirmed. For a cross-page finding, check that the replacement resolves the contradiction rather than moving it.',
          ),
          { label: `verify-spec:${found.group.key}`, phase: 'Verify', schema: VERDICTS_SCHEMA },
        ),
    ]).then((votes) => {
      const [codeVote, specVote] = votes
      const byId = (v) => {
        const map = {}
        for (const x of (v && v.verdicts) || []) map[x.id] = x
        return map
      }
      const a = byId(codeVote)
      const b = byId(specVote)

      const confirmed = []
      let rejected = 0
      for (const f of found.findings) {
        const va = a[f.id]
        const vb = b[f.id]
        if (va && vb && va.confirmed && vb.confirmed) {
          // A verifier may improve the replacement text; prefer a corrected one.
          const better =
            (vb.correctedFix && vb.correctedFix.length > 0 && vb.correctedFix) ||
            (va.correctedFix && va.correctedFix.length > 0 && va.correctedFix) ||
            f.fix
          confirmed.push({ ...f, fix: better, codeReason: va.reason, specReason: vb.reason })
        } else {
          rejected += 1
        }
      }
      log(
        `${found.group.key}: ${confirmed.length} confirmed by both, ${rejected} rejected of ${found.findings.length}`,
      )
      return { ...found, confirmed, rejected }
    })
  },

  // Stage 3 — one fixer per group. Groups own disjoint files, so no collisions.
  (verified) => {
    if (verified.confirmed.length === 0) {
      return { ...verified, fixReport: { applied: 0, skipped: 0, summary: 'nothing confirmed' } }
    }
    return agent(
      `Apply these documentation corrections in /Users/joan/projects/podium.

Every finding below was independently confirmed by two verifiers who each re-derived the evidence from the code. Apply them.

## Your files

Only these pages: ${verified.group.pages}. Do not edit anything else. Do not edit \`spec/\`, code, or tests.

## How to apply

For each finding, make the smallest edit that resolves it, using the \`fix\` text. If the \`fix\` does not apply cleanly because the surrounding text has moved, re-read the file and adapt it, keeping the correction identical in substance.

Skip a finding, and say why, only if applying it would contradict the file's surrounding content or would be plainly wrong. You are the last check before it lands.

${GROUND_RULES}

\`.claude/rules/doc-style.md\` governs every word you write.

## Verify when done

\`\`\`
cd site && npm run check
\`\`\`

Findings:

${JSON.stringify(verified.confirmed, null, 2)}`,
      { label: `fix:${verified.group.key}`, phase: 'Fix', schema: FIX_SCHEMA },
    ).then((fixReport) => ({ ...verified, fixReport }))
  },
)

// One pass over the seams. Every reviewer above was confined to its own group,
// so a claim that is stated one way in `delivery` and another way in `serving`
// is the one class of defect this partition cannot catch on its own.
phase('Consistency')

const CONSISTENCY_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['contradictions'],
  properties: {
    contradictions: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['subject', 'pageA', 'pageB', 'disagreement', 'correct', 'evidence'],
        properties: {
          subject: { type: 'string' },
          pageA: { type: 'string' },
          pageB: { type: 'string' },
          disagreement: { type: 'string' },
          correct: { type: 'string', description: 'which one the code supports, and why' },
          evidence: { type: 'string' },
        },
      },
    },
  },
}

const consistency = await agent(
  `Find contradictions between Podium documentation pages that live in different sections, in /Users/joan/projects/podium.

Eight reviewers just audited this corpus, each confined to one group of pages. A statement that is right within its own group but disagrees with a page in another group is what none of them could see. That is all you are looking for.

The groups were:

${GROUPS.map((g) => `- **${g.key}**: ${g.pages}`).join('\n')}

Concentrate on the subjects that appear in more than one group:

- What each deployment tier can do, stated in docs/deployment/index.md, in each tier page, in docs/getting-started/why-podium.md, and in README.md.
- Where each harness adapter writes each artifact type, stated in docs/consuming/configure-your-harness.md and again in the authoring walkthroughs.
- Frontmatter fields, requiredness, and defaults, stated in docs/authoring/frontmatter-reference.md and again in docs/reference/frontmatter-schema.md.
- Command flags, stated in docs/reference/cli.md and again wherever a page shows the command.
- Terms defined in docs/reference/glossary.md and used elsewhere with a different meaning.
- What is described as shipped, in docs/about/status.md against claims made anywhere else.

For each contradiction, read the code and say which page is right. Cite \`file:line\`. If both are wrong, say so in \`correct\`.

Report only. Do not edit any file. Do not report a page disagreeing with the code, which the other reviewers own; report only two pages disagreeing with each other.

${GROUND_RULES}`,
  { label: 'cross-group-consistency', phase: 'Consistency', schema: CONSISTENCY_SCHEMA },
)

phase('Report')

const groups = results.filter(Boolean)
const totals = groups.reduce(
  (acc, g) => ({
    raised: acc.raised + (g.findings ? g.findings.length : 0),
    confirmed: acc.confirmed + (g.confirmed ? g.confirmed.length : 0),
    applied: acc.applied + ((g.fixReport && g.fixReport.applied) || 0),
    skipped: acc.skipped + ((g.fixReport && g.fixReport.skipped) || 0),
  }),
  { raised: 0, confirmed: 0, applied: 0, skipped: 0 },
)

const specGaps = groups.flatMap((g) =>
  (g.confirmed || [])
    .filter((f) => f.specCodeGap)
    .map((f) => ({
      group: g.group.key,
      file: f.file,
      line: f.line,
      claim: f.claim,
      reality: f.reality,
      evidence: f.evidence,
    })),
)

return {
  totals,
  rejectedByVerification: totals.raised - totals.confirmed,
  perGroup: groups.map((g) => ({
    group: g.group.key,
    raised: g.findings ? g.findings.length : 0,
    confirmed: g.confirmed ? g.confirmed.length : 0,
    applied: (g.fixReport && g.fixReport.applied) || 0,
    summary: g.fixReport && g.fixReport.summary,
  })),
  specCodeGaps: specGaps,
  crossGroupContradictions: (consistency && consistency.contradictions) || [],
}
