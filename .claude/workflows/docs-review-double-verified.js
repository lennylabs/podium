export const meta = {
  name: 'docs-review-double-verified',
  description:
    'Review every documentation page against the code and the spec, confirm each finding with two independent verifiers, and fix only what both confirm',
  whenToUse:
    'A thorough documentation correctness pass where a wrong "fix" costs more than a missed defect. Partitions the corpus so no page is reviewed by an agent that has seen another page group.',
  phases: [
    { title: 'Find', detail: 'one fresh reviewer per page group' },
    { title: 'Verify', detail: 'two independent skeptics per group, both must confirm' },
    { title: 'Fix', detail: 'apply only doubly-confirmed findings, per group' },
    { title: 'Report', detail: 'consolidate' },
  ],
}

// Pages are partitioned so each group is owned by exactly one reviewer and one
// fixer. Groups never share a file, so the fix stages cannot collide.
const GROUPS = [
  {
    key: 'getting-started',
    pages: 'docs/getting-started/ (index, why-podium, quickstart, concepts, how-it-works)',
    focus:
      'The broadest claims in the corpus. Every capability attributed to a tier, every command in the quickstart, and every definition against spec/glossary.md.',
  },
  {
    key: 'authoring-basics',
    pages: 'docs/authoring/ (index, your-first-skill, your-first-command, your-first-agent, artifact-types)',
    focus:
      'The artifact model an author writes against. Every frontmatter key, every destination path, and every lint claim.',
  },
  {
    key: 'authoring-reference',
    pages: 'docs/authoring/ (frontmatter-reference, domains, rule-modes, hooks, extends, bundled-resources, hints)',
    focus:
      'Field names, types, requiredness, defaults, merge semantics, hook event names, and size limits, against pkg/manifest and pkg/lint.',
  },
  {
    key: 'consuming',
    pages: 'docs/consuming/ (index, configure-your-harness, selective-materialization, browsing-the-catalog, custom-via-sdk, handling-artifact-responses, publishing)',
    focus:
      'Per-harness destination tables against pkg/adapter, and every SDK call against the real signatures in sdks/.',
  },
  {
    key: 'deployment-tiers',
    pages: 'docs/deployment/ (index, local, single-node, clustered, integrations, layers, access-control)',
    focus:
      'Which tier provides which capability, every PODIUM_* variable and its default, and every YAML config example against internal/serverboot.',
  },
  {
    key: 'deployment-ops',
    pages: 'docs/deployment/ (operator-guide, extending, vector-backends, gateway-delegated-identity, progressive-adoption)',
    focus:
      'Operational commands that must exist with the flags shown, metric names, and backend selection against pkg/vector and pkg/embedding.',
  },
  {
    key: 'deployment-oidc',
    pages: 'docs/deployment/oidc/ (index, okta, entra-id, google-workspace, auth0, keycloak)',
    focus:
      'Identity configuration against pkg/identity and internal/serverboot. Which provider values the registry accepts, and what each claim mapping actually reads.',
  },
  {
    key: 'reference-and-meta',
    pages: 'docs/reference/ (cli, http-api, frontmatter-schema, error-codes, glossary, index), docs/about/, docs/testing/, docs/overview.md, README.md',
    focus:
      'The densest factual surface. Every flag, route, status code, and error code, plus every "shipped" claim in about/status.md.',
  },
]

const GROUND_RULES = `
## Sources of truth

1. \`spec/\` is authoritative for intent. It is **read-only**: never edit it.
2. The code is authoritative for what ships: \`cmd/\`, \`pkg/\`, \`internal/\`, \`sdks/\`.

Where the spec and the code disagree, that is a finding in its own right. Report it; never silently pick a side.

## Facts already established, so you need not re-derive them

- \`oauth-device-code\` is a client-side provider. Setting it as the registry's \`PODIUM_IDENTITY_PROVIDER\` aborts startup with \`config.identity_provider_unverified\`. The registry verifies \`injected-session-token\`, \`oidc-jwt\`, and \`trusted-headers\`.
- There is no \`--marketplace\` flag and no \`podium publish\` command. Marketplace rendering goes through a \`kind: marketplace\` entry under \`targets:\`, reached with \`--config\`.
- \`registry.yaml\` nests every key under a top-level \`registry:\`.
- No embedding model ships in the binary. A single node defaults the provider to \`ollama\` and a Postgres-backed deployment to \`openai\`; neither is self-contained.
- A local catalog composes ordered layers from disk through \`.registry-config\`.
- Cursor skills go to \`.cursor/skills/<name>/SKILL.md\`; only rules become \`.mdc\`. Codex skills live under \`.agents/\`. Claude Code MCP servers go to \`.mcp.json\`.
- For \`type: rule\` and \`type: mcp-server\`, no adapter calls \`appendResources\`, so bundled files are dropped.

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
            description: 'a better replacement when the finding is real but its fix is wrong; otherwise empty',
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

## Your pages

${group.pages}

Read every one of them line by line. Do not review any page outside this list; other reviewers own those.

## What to look hardest at

${group.focus}

${GROUND_RULES}

## What counts as a finding

A factual error a reader would act on: a flag or command that does not exist, a wrong path, a wrong default, a wrong type or field name, a capability attributed to the wrong tier or component, a config example that would not parse or would be silently ignored, or an output block that does not match what the code prints.

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
            'Your angle: **the code**. Work from the implementation outward. Trace the actual call path, check the branch the finding depends on, and confirm the behaviour is what is claimed at runtime.',
          ),
          { label: `verify-code:${found.group.key}`, phase: 'Verify', schema: VERDICTS_SCHEMA },
        ),
      () =>
        agent(
          brief(
            'Your angle: **the specification and the reader**. Check the finding against `spec/`, and ask whether the proposed replacement is actually true, complete, and consistent with how the rest of the corpus describes the same thing. A fix that is locally right but contradicts another page is not confirmed.',
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
  (g.confirmed || []).filter((f) => f.specCodeGap).map((f) => ({
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
}
