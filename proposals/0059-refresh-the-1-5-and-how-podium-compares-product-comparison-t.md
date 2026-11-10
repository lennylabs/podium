# Proposal 0059: Refresh the §1.5 and "How Podium compares" product comparison to November 2026

- Issue: (to be filed)
- Status: Implemented (2026-11-09). Verified on 2026-11-09 after 4 adversarial review rounds (8 findings fixed).
- Date: 2026-11-09

This document stages spec and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change is prose-only and changes no behavior.

## Summary

**What changes.**

- §1.5 *Where Podium Fits* replaces its alternatives table with five categories that match the docs page, drops Continue Hub and SkillReg, renames the AWS, Cursor, Claude, and agent-skill-creator entries, and adds the registries, vendor marketplaces, gateways, and installers that have appeared since May 2026 (SPEC-1, `spec/01-overview.md`).
- The closing sentence of §1.5 states that the skill manifest follows the Agent Skills specification, which AAIF governs, and that `ARTIFACT.md` and the other first-class types are specified in §4 (SPEC-2, `spec/01-overview.md`).
- The "How Podium compares" section of `docs/getting-started/why-podium.md` is rewritten to the same five categories, followed by an open-specifications table that states what Podium implements of each specification. The section is dated November 2026 (DOC-1).
- `CHANGELOG.md` records the refresh under `[Unreleased]` > `Documentation` for 0.6.0 (DOC-3).

**Fixed decisions.**

- D-1: kind is fix; the spec commit lands before the docs commit.
- D-2: one category set, with identical names and order in both files; the open-specifications table lives on the docs page only.
- D-3: the removed, renamed, added, and omitted products; SkillReg is removed on the 2026-11-09 result of verification step 1.
- D-4: agentregistry carries no CNCF claim; the AWS and Cursor wording and links.
- D-5: the MCP server registry products stay, merged into the gateways category; the pre-approval run of verification step 1 confirms them.
- D-6: Podium's gaps are present facts on the docs page, with no roadmap wording; scanning is a §1.1 exclusion.
- D-7: the SEP-2640 text states the read-only resource mirror and the absent skills extension.
- D-8: this proposal owns the docs Agent Skills row; its one cell text holds under §4.3.4 on main and under the form proposal 0058 stages, so the row needs no edit in either landing order. A repository check at S2 confirms that §4.3.4 states one of the two forms (DOC-1 implementation note 7).
- D-9: the §1.5 closing text claims no venue for `ARTIFACT.md`.
- D-10: no claim that another harness reads Podium's output.
- D-11: only the docs section carries a date.
- D-12: no maintainer re-verification note is added.
- D-13: no runnable fenced block is added to `why-podium.md`.
- D-14: §1.1, §1.3.1, §1.6, and `README.md` stay unchanged.
- D-15: the CHANGELOG entry goes under `Documentation` for 0.6.0.
- D-16: each Podium claim cites the surface that ships it.

**Watch out for.**

- **Verification step 1 runs before approval, in the main session.** A subagent's web fetch is denied, and the pipeline applies the spec and builds the docs in subagents, so no implementation step runs a web check. The 2026-11-09 results (Verification, *Step 1 results*) are already written into SPEC-1, SPEC-2, DOC-1, and DOC-3, and the staged text depends on no web fact at apply time. Before the Status changes to Approved, the main session checks the links that the 2026-11-09 results do not cover. A product whose page no longer matches its row is removed from SPEC-1 and DOC-1 in this proposal, in one edit.
- **`tmp/research/why-podium-refresh.md` is an earlier draft.** It still names Bifrost, ruler, and wshobson/agents, calls agentregistry a CNCF project, uses "Not yet supported", says Copilot reads the directories Podium writes, and describes the Gemini, Pi, and Hermes outputs as separate repositories. Apply the text staged here, and do not copy from that draft.
- **A green gate says nothing about accuracy.** No test cites §1.5 and no test parses the comparison. `make coverage-gate` passes with any table in §1.5, so verification steps 2 and 3 carry the consistency check.
- **The two files must state the same Podium claims.** Each "When Podium wins" cell in SPEC-1 has a matching "Where Podium applies" paragraph in DOC-1. S2 never edits `spec/`. A mismatch found during S2 is corrected on the docs side to match the text S1 committed, or recorded as a deviation and left to a later change through the pipeline.
- **The §1.1 Bedrock mention stays.** `spec/01-overview.md:11` names Bedrock among the SDK runtimes. It is accurate and outside §1.5, so the stale-name grep in step 2 excludes the bare token `Bedrock`.
- **Proposal 0058 is a draft that would make `ARTIFACT.md` optional, and it stages no `why-podium.md` edit.** Its Non-goals leave §1.5 to this proposal (`proposals/0058-ingest-sync-materialize-and-serve-a-plain-agent-skills-direc.md:1255`), and none of its docs edits names `why-podium.md`. This proposal owns the docs Agent Skills row. Its cell text states only what §4.3.4 says on main and in the 0058 form alike, so the row stays correct whichever proposal lands first, and no edit to the row is owed by either proposal. DOC-1 implementation note 7 confirms at S2 that §4.3.4 states one of the two forms. The SPEC-2 sentence holds in either order.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1, SPEC-2. The reconciled §1.5 table and the closing sentence land in one commit, because both replace adjacent text in §1.5 and one reviewer reads them together. Entry criterion: verification step 1 has run in the main session before approval, and its results are written into the staged text, so S1 applies SPEC-1 and SPEC-2 as written.
      Levels: unit (`make coverage-gate`). Depends on: —
- [x] **S2 · docs** — DOC-1. The "How Podium compares" section of `docs/getting-started/why-podium.md` is rewritten to the SPEC-1 categories, with the open-specifications table and the November 2026 date.
      Levels: unit (`make coverage-gate`, `cd site && npm run build`). Depends on: S1
- [x] **S3 · docs** — DOC-3. The `CHANGELOG.md` bullet under `[Unreleased]` > `Documentation`.
      Levels: unit (`make coverage-gate`, `cd site && npm run build`, because the site's changelog page includes `CHANGELOG.md`). Depends on: S1, S2

## Current state and the gap

Spec §1.5 *Where Podium Fits* (`spec/01-overview.md:96-114`) and the "How Podium compares" section of `docs/getting-started/why-podium.md` (lines 130-194) describe the adjacent product landscape as of May 2026. The §1.5 text was last overhauled in 20db6c1e (2026-05-07). The November 2026 research notes (`tmp/research/why-podium-reverify-2026-11-09.md`, `tmp/research/why-podium-refresh.md`, and `tmp/research/sources.md`) record these changes:

- Continue Hub (spec:109; docs:144, 146) was shut down after Cursor acquired Continue [S65].
- "AWS Bedrock Registry with AgentCore" (spec:110; docs:158) is now AWS Agent Registry, generally available since 2026-08-31 [S16].
- "Cursor Team Rules" (spec:110; docs:157) has been superseded by Cursor team marketplaces.
- The Claude entry (spec:110; docs:156) is now Claude private plugin marketplaces governed by managed settings [S66, S67].
- agent-skill-creator (spec:111; docs:167) is now agent-skills-platform [S61].
- SkillReg (spec:108; docs:142, 146) is unconfirmed. The 2026-11-09 reverify found only a Product Hunt listing and recommends dropping it unless a primary site is confirmed (`tmp/research/why-podium-reverify-2026-11-09.md:18`), while `tmp/research/notes-raw.md:70` records a PyPI release dated 2026-09-09 and `sources.md:68` [S64] cites a page on skillreg.dev. The 2026-11-09 main-session check confirmed no primary site, so D-3 removes it.

Neither file names Google Cloud Agent Registry, JFrog AI Catalog, agentregistry, the LiteLLM Skills Gateway, Codex workspace plugins, or Copilot enterprise-managed plugins. Neither file mentions the open specifications that now cover this space: Agent Skills, Agent Plugins 1.0, the MCP Skills Extension (SEP-2640), and the Agent Skills discovery index. The closing sentence of §1.5 (spec:114) says the canonical format awaits "the right venue". The skill manifest already complies with Agent Skills (§4.3.4, `spec/04-artifact-model.md:9`), which the research notes record as donated to AAIF. `ARTIFACT.md` and the other first-class types (§4) have no external venue.

The two files also group the categories differently. The spec has nine rows (spec:104-112). The docs merge four of the spec's registry and hub rows into one "Catalogs" table (docs:134-148), add a "Per-harness skill systems" row that the spec lacks (docs:159), and fold the spec's `AGENTS.md` sync-script row and the curated skill libraries into an installers section (docs:161-173). A refresh therefore reconciles both files beyond replacing product names.

Podium's own claims must match what main ships:

- The built-in adapters are claude-code, claude-desktop, claude-cowork, cursor, codex, gemini, opencode, pi, hermes, and none (`pkg/adapter/adapter.go:194-210`). No code refers to Copilot or Antigravity.
- Marketplace emitters exist for Claude, Codex, Cursor, Gemini, Pi, and Hermes and for no other harness (`pkg/adapter/emitter.go:429-438`). §7.8 states that the Claude, Codex, and Cursor manifests coexist in one repository, that the Gemini extension occupies the whole repository, and that the Hermes tap defaults to a root `skills/` directory, so Gemini and Hermes often take their own output (`spec/07-external-integration.md:854`, `:881`). §7.8 states no repository grouping for Pi. The Pi emitter writes its skills under a root `skills/` directory, the same root the Hermes tap uses (`pkg/adapter/emitter.go:348`, `:392`).
- Not every built-in adapter writes project files for every type. `claude-desktop` writes no project output (`pkg/adapter/builtins.go:26-28`; `spec/06-mcp-server.md:326`), `claude-cowork` writes only `type: context` at project scope (`pkg/adapter/builtins.go:31-35`; `spec/06-mcp-server.md:344`), and `hermes` writes only rules and contexts (`pkg/adapter/builtins.go:229-236`). The §6.7.1 type-materialization matrix carries further `✗` cells, such as Codex `command` and Pi `agent` and `mcp-server` (`spec/06-mcp-server.md:382-388`), and §6.7.1 restricts author-once, load-anywhere materialization to the harnesses with a project-level surface (`spec/06-mcp-server.md:373`).
- Rule injection into `AGENTS.md` comes from the codex (`pkg/adapter/codex.go:26`), opencode (`pkg/adapter/builtins.go:186`), and pi (`pkg/adapter/builtins.go:212`) adapters (§6.7). Gemini injects into `GEMINI.md`, and hermes writes `.cursor/rules/`.
- The MCP server advertises `{tools, sessionCorrelation, resources}` and mirrors artifact bodies as `podium://artifact/` resources (`cmd/podium-mcp/main.go:1185-1189`, `cmd/podium-mcp/resources.go:12`, §5.0). No code serves the SEP-2640 `io.modelcontextprotocol/skills` extension, writes Agent Plugins packages, or publishes `/.well-known/agent-skills/index.json`.
- Signing (`pkg/sign`), audit (`pkg/audit`), layers and visibility (`pkg/layer`), and impact analysis (`pkg/registry/core/dependents.go`, `/v1/dependents`, `podium impact`) ship. Consumer-side signature verification ships as `PODIUM_VERIFY_SIGNATURES` in podium-mcp (`cmd/podium-mcp/main.go:278`) and `defaults.verify_signatures` in sync (`pkg/sync/config.go:48`).
- Podium performs no vulnerability or malware scanning, and §1.1 excludes scanning on purpose. `pkg/lint` validates manifests only.
- A skill artifact requires both `ARTIFACT.md` and `SKILL.md` (§4.3.4; `pkg/lint/lint.go:304`; `pkg/registry/ingest/ingest.go:1039`).

§1.3.1 (`spec/01-overview.md:63-75`) and the README positioning (`README.md:124-126`) name no competing product. No test cites §1.5 or parses the comparison text. `why-podium.md` has no doccov manifest entry and no runnable fenced block (`tools/doccov/manifest.yaml`). §1.5 has no maintenance text.

## Decisions

- **D-1. Kind is fix.** The change corrects the accuracy of spec and docs prose. It changes no behavior, so it creates no code, test, matrix, or speccov obligation. §1.5 is a positioning section and no test cites it. It edits `spec/`, so it goes through the pipeline. SPEC-1 and SPEC-2 land in one spec commit, verified with `make coverage-gate`. DOC-1 and DOC-3 land after it in docs commits, following the precedent of d17fee09 and proposal 0033.
- **D-2. One category set governs both files, with identical names and order.** The order is: Self-hosted and managed registries; Harness vendors' private marketplaces; Gateways and MCP server registries; Cross-harness installers and generators; Git repository with harness-native directories. The spec carries a table row per category, and the docs give each category a `###` subsection. Each product in one file appears under the same category in the other. The "When Podium wins" cells in the spec state the same Podium claims as the "Where Podium applies" paragraphs in the docs. The open-specifications block, with its Agent Plugins 1.0, SEP-2640, and discovery-index gap statements, appears only on the docs page, after the categories. The spec states the venue fact in the §1.5 closing sentence (SPEC-2).
- **D-3. Products removed, renamed, added, and omitted.**
  - Removed: Continue Hub (shut down after the Cursor acquisition); SkillReg, because the 2026-11-09 run of verification step 1 found only a Product Hunt listing and no primary site; the docs-only "Per-harness skill systems" row, the named curated GitHub skill libraries, and the separate "Sync script with AGENTS.md" row. That row's pattern folds into the Git repository category.
  - Renamed: "AWS Bedrock Registry with AgentCore" to AWS Agent Registry, which moves to the registries category; "Cursor Team Rules" to Cursor team marketplaces; "Anthropic Cowork private plugin marketplaces" to Claude private plugin marketplaces with managed settings; and agent-skill-creator to agent-skills-platform [S61].
  - Added: Google Cloud Agent Registry, JFrog AI Catalog, agentregistry, OpenAI Codex workspace plugins, GitHub Copilot enterprise-managed plugins, the LiteLLM Skills Gateway, `gh skill`, and rulesync.
  - Omitted: Bifrost, which primary sources show has no skills marketplace; Microsoft Agent 365, which is an agent inventory and holds no authored artifacts; Chainguard and ruler, because `tmp/research/sources.md` has no source for either; and wshobson/agents, which is a content library rather than a tool.
- **D-4. agentregistry, AWS, and Cursor wording.** agentregistry is described as Apache 2.0 and self-hosted, with no CNCF claim. The LICENSE file says Apache 2.0, and the CNCF sandbox list has no entry for agentregistry. The AWS row keeps "approval workflow" and "approved-only discovery", both confirmed against the AWS GA announcement and the AWS ML blog. The Cursor row cites cursor.com/docs/plugins for the per-plugin install modes Default Off, Default On, and Required.
- **D-5. The MCP server registry products stay.** Kong, TrueFoundry, agentic-community/mcp-gateway-registry, Docker MCP Catalog, MACH Alliance, the GitHub Copilot internal MCP registry, and ServiceNow stay, merged into the gateways category, because no evidence marks any of them stale. The 2026-11-09 results cover agentic-community/mcp-gateway-registry and none of the other products, so the pre-approval run of verification step 1 checks each of the others. A product whose primary page no longer describes an MCP registry or gateway is removed from the SPEC-1 third row and the DOC-1 gateways table in this proposal before approval, so S1 and S2 apply the same product set and neither step decides it.
- **D-6. Podium's gaps are stated as present facts on the docs page.** The docs use no "not yet" or roadmap wording. Podium has no GitHub Copilot adapter, so it writes no Copilot custom agents, hooks, or plugin marketplaces. It writes no Agent Plugins packages. Its MCP server does not implement SEP-2640, and the registry publishes no Agent Skills discovery index. Scanning is described as the §1.1 exclusion: Podium does not scan artifacts, and scanning runs in the CI/CD pipeline of the Git repository that a layer ingests from. The docs name no Antigravity gap, because no category lists an Antigravity product (DOC-1 implementation note 5). The spec states none of these gaps.
- **D-7. SEP-2640 wording.** The text does not say Podium lacks MCP resources. It states that podium-mcp mirrors artifact bodies as read-only resources (§5.0) and does not implement the skills extension.
- **D-8. This proposal owns the docs Agent Skills row, and the row follows §4.3.4 as committed.** On main a skill directory contains both `SKILL.md` and `ARTIFACT.md`, and Podium-specific fields such as `when_to_use` and `sensitivity` stay in `ARTIFACT.md` (§4.3.4; `spec/04-artifact-model.md:257`, `:259`). Proposal 0058 would make `ARTIFACT.md` optional for a skill (`proposals/0058-ingest-sync-materialize-and-serve-a-plain-agent-skills-direc.md:255`) and keeps those fields in the overlay (`proposals/0058-ingest-sync-materialize-and-serve-a-plain-agent-skills-direc.md:162`, `:261`), but it stages no `why-podium.md` edit and leaves §1.5 to this proposal (`proposals/0058-ingest-sync-materialize-and-serve-a-plain-agent-skills-direc.md:1255`). This proposal cannot stage an edit inside 0058, so the DOC-1 cell states only what both forms of §4.3.4 say: the `SKILL.md` follows the specification, and Podium-only fields live in an `ARTIFACT.md` beside it. The cell neither requires `ARTIFACT.md` nor calls it optional, so it needs no edit whichever proposal lands first. DOC-1 implementation note 7 confirms at S2, with a repository check a build subagent can run, that §4.3.4 states one of the two forms.
- **D-9. The §1.5 closing text claims no venue for `ARTIFACT.md`.** It states that the skill manifest follows Agent Skills, which AAIF governs, and that `ARTIFACT.md` and the other first-class types have no external specification and are specified in §4. It stays consistent with the §1.6 standards-engagement bullet, which is unchanged. The 2026-11-09 run of verification step 1 confirmed the donation, so every file states that AAIF governs the specification.
- **D-10. No claims about other harnesses reading Podium's output.** Examples are "Copilot reads `.claude/skills` and `.claude/rules`" and "several agents read `.agents/skills`". Those are external facts the repository cannot verify, and they would read as Podium coverage of harnesses it has no adapter for.
- **D-11. Only the docs section carries a date.** Its intro says the product details are current as of November 2026. The spec carries no date.
- **D-12. No maintainer re-verification note.** The dated intro (D-11) tells readers when the facts were checked, and the verification steps in this proposal cover the apply-time check. A recurring re-verification step belongs in `.claude/rules/release-process.md` and `RELEASING.md` in a separate change (Non-goals).
- **D-13. No runnable fenced block.** The edit adds no fenced code block to `why-podium.md`, so the page stays non-runnable for doccov and needs no manifest entry. Commands such as `npx skills` and `gh skill` appear only as inline code.
- **D-14. §1.1, §1.3.1, §1.6, and `README.md:124-126` stay unchanged.** Each was checked against the new text. §1.3.1 and the README name no products. The §1.1 "Vercel skills.sh" example stays accurate because skills.sh appears in the installers category. §1.6 already names AAIF-governed standards.
- **D-15. CHANGELOG entry.** The entry goes under `[Unreleased]` > `Documentation`, because the comparison is user-facing. The release this lands in is 0.6.0.
- **D-16. Shipped Podium claims cite their surfaces.** Marketplace repositories (§7.8): the Claude, Codex, Cursor, Gemini, Pi, and Hermes emitters render from the same catalog (`pkg/adapter/emitter.go:429-438`). The Claude, Codex, and Cursor marketplaces coexist in one repository, a Gemini extension occupies a repository of its own, and a Hermes tap defaults to a root `skills/` directory and usually takes its own repository (`spec/07-external-integration.md:854`, `:881`). The text makes no grouping claim for Pi, which §7.8 does not state. OpenCode is not a publish target. Native project files (§6.7, §6.7.1): the claim covers the types each harness's project-level surface carries. `claude-desktop` has no project-level output, `claude-cowork` writes only contexts at project scope, and `hermes` writes only rules and contexts (Current state), so the text never says that every built-in adapter delivers native files. Rule injection into `AGENTS.md` comes from the codex, opencode, and pi adapters (§6.7); gemini injects into `GEMINI.md`, and hermes writes `.cursor/rules/`. Signature verification at consumption comes from podium-mcp and `podium sync`. SCIM and multi-tenant mode run on every registry server (CHANGELOG `[Unreleased]` `Documentation`).

## Spec amendment: §1.5 Where Podium Fits, the alternatives table

**SPEC-1.** `spec/01-overview.md`, §1.5, the alternatives table. One edit, in one commit with SPEC-2 (D-1).

**Rationale.** The current table describes the product landscape as of May 2026. It names Continue Hub, which has shut down, lists several products under former names, and omits the registries, vendor marketplaces, gateways, and installers that have appeared since. Its rows also group the categories differently from the docs page, so the replacement uses the category set that D-2 fixes for both files.

**Anchor.** In `spec/01-overview.md` §1.5, replace the table that begins with the header row `| Alternative | Overlap | When it wins | When Podium wins |` and ends with the row that begins `| **MCP server registries and gateways** (Kong MCP Registry,` (about lines 102-112). Keep the two paragraphs above the table, which begin "Podium overlaps with several adjacent product categories." and "The categories below are scoped to products that catalog or distribute", unchanged.

**Replacement.**

> | Alternative | Overlap | When it wins | When Podium wins |
> | --- | --- | --- | --- |
> | **Self-hosted and managed registries** (iFlytek SkillHub, agentregistry, AWS Agent Registry, Google Cloud Agent Registry, JFrog AI Catalog, Tessl) | Central storage and governance of skills and related artifacts, with access control; approval, scanning, or evaluation in several products | A catalog of skills and MCP servers (or Tessl tiles) covers the wanted scope; a managed or SaaS service is acceptable, or the organization already runs the vendor's platform (AWS, Google Cloud, JFrog Artifactory, or Kubernetes); or built-in scanning or evaluation is required. | Agents, contexts, commands, rules, and hooks in the same catalog as skills and MCP server registrations; ordered-layer composition with `extends:` across several sources; per-OIDC-group visibility composed at request time; native project files through the built-in adapters for each type a harness's project-level surface carries (§6.7, §6.7.1); signing, with signature verification at consumption; and MIT-licensed self-hosting in every deployment mode. |
> | **Harness vendors' private marketplaces** (Claude private plugin marketplaces with managed settings, Cursor team marketplaces, OpenAI Codex workspace plugins, GitHub Copilot enterprise-managed plugins) | Admin-managed distribution of plugin bundles (skills, agents, rules, hooks, and MCP servers) inside one vendor's coding agent | The organization sanctions a single coding agent and wants native integration and vendor-managed infrastructure. | Several coding agents served from one source under one visibility policy; marketplace repositories for Claude, Codex, Cursor, Gemini, Pi, and Hermes rendered from the same catalog (§7.8); and on-prem deployment. |
> | **Gateways and MCP server registries** (LiteLLM Skills Gateway, agentic-community/mcp-gateway-registry, Kong AI Registry, TrueFoundry MCP Gateway, MACH Alliance MCP Registry, Docker MCP Catalog, GitHub Copilot internal MCP registry, ServiceNow MCP Registry) | MCP server registration, discovery, OAuth, and observability; LiteLLM and mcp-gateway-registry also store skills and hand them to coding agents | The catalog holds MCP server registrations alone, or a gateway already routes coding-agent traffic. | Agents, contexts, commands, rules, and hooks under the same governance as MCP server registrations; cross-type dependency edges (`mcpServers:` from skills and agents resolving to `mcp-server` artifacts); and ordered-layer composition. |
> | **Cross-harness installers and generators** (`npx skills` with the Vercel skills.sh registry, `gh skill`, rulesync, agent-skills-platform) | One source copied or converted into several coding agents' formats on a developer machine or in CI, without a server | A single team or a library author with a modest catalog and no per-group visibility requirement. | Composition of several sources into one effective view; per-OIDC-group visibility composed at request time; audit; and lazy MCP-mediated discovery for catalogs too large to install in full. |
> | **Git repository with harness-native directories** | Versioning, history, and repository permissions; a pre-commit hook or CI script that copies one `AGENTS.md` or rule file into harness-native locations (`CLAUDE.md`, `.cursorrules`, `.windsurfrules`, `.github/copilot-instructions.md`, and similar) | A single team with one or a few harnesses, one or two artifact types, a modest catalog, and no formal governance requirements. It requires no infrastructure beyond Git. | Composition of several repositories into one effective view with deterministic merge; per-layer visibility for cross-team catalogs; per-harness translation of each artifact type into each harness whose project-level surface carries it (§6.7, §6.7.1); cross-type dependency-aware impact analysis; and lazy discovery at scale. |

**Row mapping.** The old Git monorepo row (spec:104) and the "Sync script with AGENTS.md" row (spec:105) merge into the last row. The public (106), self-hosted (107), and closed-SaaS (108) rows merge into the first row, without SkillReg (D-3). Row 110 becomes the second row (vendor marketplaces). Row 112 becomes the third row (gateways). Row 111 becomes the fourth row (installers). Continue Hub (109) is removed. Within row 106, Vercel skills.sh moves to the fourth row beside `npx skills`, and the curated GitHub-hosted skill libraries are removed (D-3).

## Spec amendment: §1.5 Where Podium Fits, the closing sentence

**SPEC-2.** `spec/01-overview.md`, §1.5, the paragraph after the table. One edit, in one commit with SPEC-1 (D-1).

**Rationale.** The current sentence says the canonical artifact format awaits the right venue. The skill manifest already follows Agent Skills (§4.3.4), which the research notes record as donated to AAIF, and `ARTIFACT.md` and the other first-class types have no external venue. The replacement states both facts and claims no venue for `ARTIFACT.md` (D-9).

**Anchor.** In `spec/01-overview.md` §1.5, replace the paragraph after the table (about line 114) that reads:

> The canonical artifact format is intended for upstream contribution to an MCP-adjacent or AAIF-governed standard once the right venue exists; until then, it is specified here.

**Replacement.**

> The skill manifest (`SKILL.md`) follows the Agent Skills specification, which the Agentic AI Foundation (AAIF) governs (§4.3.4). `ARTIFACT.md` and the other first-class types have no external specification and are specified here (§4). Rule injection into `AGENTS.md` is described in §6.7, and the read-only MCP resource mirror in §5.0.

**AAIF status.** `tmp/research/sources.md:132` [S128] records a pending AAIF governing-board vote, while the 2026-11-09 reverify records a donation on 2026-10-22 (`tmp/research/why-podium-reverify-2026-11-09.md:9`). The 2026-11-09 run of verification step 1 confirmed the donation from Anthropic's announcement, so the replacement states AAIF governance without qualification. The Agent Skills row in DOC-1 and the CHANGELOG bullet in DOC-3 state the same status.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| A named product changes or shuts down after November 2026 | The comparison stays as written until a later change refreshes it. No automated check reads the comparison. Accepted | The docs intro "Product details are current as of November 2026." (DOC-1); §1.5 carries no date (D-11); `docs/getting-started/why-podium.md` |
| A product page fails the pre-approval run of verification step 1 | The main session removes the product from SPEC-1 and DOC-1 in this proposal before approval. S1 and S2 apply the same product set, and S2 never edits `spec/` | D-5; SPEC-1; `docs/getting-started/why-podium.md` |
| Proposal 0058 lands before or after S2 and makes `ARTIFACT.md` optional | The SPEC-2 sentence and the docs Agent Skills cell both still hold, and neither file needs an edit. Before S2, DOC-1 implementation note 7 finds the 0058 form of §4.3.4 and S2 writes the cell as staged | §1.5 "The skill manifest (`SKILL.md`) follows the Agent Skills specification" (SPEC-2); §4.3.4 *Field allocation*; `docs/getting-started/why-podium.md` *Open specifications* |
| §4.3.4 states neither the main form nor the 0058 form when S2 applies | S2 stops and records a deviation, because the cell's premise has changed (DOC-1 implementation note 7) | §4.3.4; `docs/getting-started/why-podium.md` *Open specifications* |
| Podium later gains a Copilot adapter, Agent Plugins output, SEP-2640 serving, or a discovery index | The change that adds it removes the matching gap sentence from the docs page. The spec states no gap, so it needs no §1.5 edit | D-2, D-6; `docs/getting-started/why-podium.md` *Harness vendors' private marketplaces* and *Open specifications* |
| A reader asks whether Podium scans artifacts | The docs state that Podium does not scan artifacts and that scanning runs in the layer's Git pipeline. The spec states the exclusion in §1.1 | §1.1 "Not a security scanner." (unchanged); `docs/getting-started/why-podium.md` *Self-hosted and managed registries* |

## Testing

This change adds no test. It changes no behavior, §1.5 is a positioning section with no behavioral requirement, and no test cites §1.5 today, so `speccov-drift` gains no obligation. `why-podium.md` gains no runnable block, so `doccov-check` gains no obligation (D-13).

### Verification

Step 1 runs in the main session before approval, and steps 2 through 5 run during implementation. None of them edits `spec/` or `docs/`.

1. **Web checks, in the main session, before approval.** This step is the S1 entry criterion and runs before the Status changes to Approved, because the pipeline runs S1 and S2 in subagents whose web fetch is denied. Confirm that every link in DOC-1 resolves and that the linked page describes the product as the row states. Write each result into this proposal's staged text, in SPEC-1 and DOC-1 together, so the pipeline applies unconditional text. The step stages no edit to `spec/` or `docs/` itself.

   **Step 1 results (2026-11-09, main session).**
   - SkillReg: only a Product Hunt listing was found, and no primary site. SkillReg is removed from SPEC-1 and DOC-1 (D-3).
   - Agent Skills: Anthropic donated the name, the GitHub organization, and agentskills.io to AAIF on 2026-10-22, and the specification text is unchanged (anthropic.com/news/donating-agent-skills-to-the-agentic-ai-foundation). SPEC-2, DOC-1, and DOC-3 state AAIF governance (D-9).
   - GitHub Copilot: the enterprise-managed plugins changelog is `https://github.blog/changelog/2026-05-06-enterprise-managed-plugins-in-github-copilot-cli-are-now-in-public-preview/`, which DOC-1 links.
   - Google Cloud Agent Registry: generally available since 2026-06-18; agent skill governance in preview since 2026-07-23 with semantic search; skill vulnerability scanning (docs.cloud.google.com/agent-registry/release-notes). The delivery cell reads "Discovery through the registry's search API".
   - OpenAI Codex workspace plugins: Admin > Plugins imports a public or private GitHub marketplace with daily sync and sets a per-role installation policy (help.openai.com/en/articles/20001256), which DOC-1 links.
   - Confirmed as their rows state: iFlytek SkillHub, agentregistry (Apache 2.0 LICENSE file; no CNCF listing), AWS Agent Registry, JFrog AI Catalog, Tessl, Claude private plugin marketplaces, Cursor team marketplaces, the LiteLLM Skills Gateway, agentic-community/mcp-gateway-registry, `npx skills`, `gh skill`, rulesync, Agent Skills, `AGENTS.md`, Agent Plugins 1.0, SEP-2640, and the Agent Skills discovery index RFC.
   - Pre-approval run (2026-11-09, main session), applied to SPEC-1 and DOC-1:
     - Kong: the linked page names the product Kong AI Registry and describes governed discovery of MCP servers, tools, models, agents, and skills. It does not describe OAuth or observability. The row is renamed and its coverage cell restated.
     - TrueFoundry MCP Gateway: confirmed (MCP server registry and discovery, OAuth 2.0, federated identity, request tracing).
     - MACH Alliance MCP Registry: confirmed as a vendor-neutral directory of MCP servers (machalliance.org/mach-alliance-mcp-registry).
     - Docker MCP Catalog: the page describes a catalog of containerized MCP servers "built by the community, powered by Docker" that accepts submissions, so it is not vendor-curated. Its row says so.
     - GitHub's internal MCP registry: organization and enterprise admins set a registry URL in the Copilot policies for discovery and allowlisting (docs.github.com configure-mcp-server-access). It is named the GitHub Copilot internal MCP registry, because Copilot clients beyond VS Code consume it.
     - ServiceNow MCP Registry: confirmed as a private registry of approved MCP servers built on the MCP Registry specification (servicenow.com docs, mcp-registry).
     - The four curated catalogs now take one row each, with a link.
     - Vercel skills.sh, agent-skills-platform (repository live under that name, MIT), and the Google Cloud `concepts` page: confirmed as their rows state.
2. **Stale-name grep.** Run `grep -n 'SkillReg\|Continue Hub\|Bedrock Registry\|AgentCore\|Team Rules\|Cowork private\|agent-skill-creator\|right venue' spec/01-overview.md docs/getting-started/why-podium.md`. The grep returns nothing. The bare token `Bedrock` is excluded because `spec/01-overview.md:11` names it accurately in §1.1.
3. **Consistency between the files.** For each category, list the product names in the SPEC-1 row and in the DOC-1 subsection, and confirm that the sets are identical. Compare each SPEC-1 "When Podium wins" cell with the matching DOC-1 "Where Podium applies" paragraph claim by claim. Re-read the DOC-3 bullet against the final text of both files. Re-run the two DOC-1 implementation note 7 greps against `spec/04-artifact-model.md`, confirm that exactly one returns 1, and confirm that the Agent Skills "What Podium implements" cell in `why-podium.md` reads exactly the text DOC-1 stages.
4. **HTML comments.** Confirm that the text between each `<!--` opener and its `-->` closer in `why-podium.md` contains no `--` sequence. DOC-1 adds no HTML comment, so the check confirms that none was introduced.
5. **Gates.** Run `make coverage-gate`, then `cd site && npm run build`, and confirm both pass.

## Documentation changes

### DOC-1. `docs/getting-started/why-podium.md`: rewrite "How Podium compares"

**Anchor.** Replace everything from the heading `## How Podium compares` (about line 130) through the paragraph that begins `**Where Podium applies.** Podium applies when multi-source composition with deterministic merge across multiple Git repositories` (about line 194). Keep the `---` rule and the `## Project model` section that follow.

**Replacement.**

```markdown
## How Podium compares

The adjacent landscape splits into categories. The comparisons below cover products that catalog or distribute authored AI agent artifacts into the harnesses an organization runs. LLM application development frameworks, evaluation suites, observability and tracing products, prompt-version-as-program-artifact tools, RAG-over-corpora knowledge bases, and agent runtimes solve different problems and are out of scope. Product details are current as of November 2026.

### Self-hosted and managed registries

These products store and govern agent artifacts centrally. They differ in artifact scope, delivery into harnesses, composition, and hosting.

| Product | License and hosting | Artifact types | Delivery into harnesses | Composition across sources | Access control |
|:--|:--|:--|:--|:--|:--|
| **iFlytek SkillHub** ([repo](https://github.com/iflytek/skillhub)) | Apache 2.0; self-hosted | Skills | CLI download and install | None | Team namespaces with roles; review before promotion to global scope |
| **agentregistry** ([repo](https://github.com/agentregistry-dev/agentregistry)) | Apache 2.0; self-hosted | MCP servers, agents, skills, and prompts | Generated client configuration; deployment to Kubernetes; MCP routing through agentgateway | None | Curation and publishing controls |
| **AWS Agent Registry** ([announcement](https://aws.amazon.com/about-aws/whats-new/2026/08/aws-agent-registry-generally-available/)) | Managed (AWS) | MCP servers, A2A agent cards, skills, and custom records | Discovery from MCP-compatible IDEs | None | IAM; approval workflow; approved-only discovery |
| **Google Cloud Agent Registry** ([docs](https://docs.cloud.google.com/agent-registry/concepts)) | Managed (Google Cloud) | Agents, MCP servers, and skills (skill governance in preview) | Discovery through the registry's search API | None | Skill governance (preview); vulnerability scanning of skills |
| **JFrog AI Catalog** ([skills registry](https://jfrog.com/ai-catalog/skills-registry/)) | Commercial; SaaS or self-hosted | Skills, plugins, MCP servers, and models | CLI and MCP | None | Policy-driven approval; scanning; signing of approved skills |
| **Tessl** ([tessl.io](https://tessl.io/registry)) | Commercial; SaaS | Skills, docs, rules, and commands in tiles | Package manager; MCP tools to install | None | Workspace-scoped; evaluation of skill effect |
| **Podium** | MIT; self-hosted in the local, single-node, or clustered deployment mode | Skills, agents, contexts, commands, rules, hooks, MCP server registrations, and extension types | Native project files through the built-in adapters, for each type a harness's project-level surface carries; marketplace repositories; MCP discovery tools; SDKs | Ordered layers with `extends:` | Per-OIDC-group and per-user visibility evaluated per request; SCIM; multi-tenant mode |

**Where each applies.**

- SkillHub fits an organization that wants a self-hosted skill registry with team namespaces and a review step.
- agentregistry fits a Kubernetes platform team that deploys MCP servers and agents and wants a catalog beside them.
- AWS Agent Registry and Google Cloud Agent Registry fit an organization standardized on that cloud provider.
- JFrog AI Catalog fits an organization that already governs packages in Artifactory and wants scanning and approval for skills.
- Tessl fits an organization that accepts SaaS and wants evaluation of each skill's effect on agent tasks.

**Where Podium applies.** Podium applies when the catalog holds agents, contexts, commands, rules, and hooks beside skills and MCP server registrations, when ordered layers and `extends:` compose several sources, when visibility is evaluated per OIDC group at request time, when the built-in adapters write native project files for each type a harness's project-level surface carries, when consumers verify the signature of what they receive, or when the registry is self-hosted under the MIT license in any deployment mode. Podium does not scan artifacts. Scanning runs in the CI/CD pipeline of the Git repository that a layer ingests from.

### Harness vendors' private marketplaces

The coding agents below offer plugin bundles and an admin-managed private marketplace. An organization that sanctions one coding agent gets a shared, governed catalog from that vendor.

| Product | Coverage |
|:--|:--|
| **Claude** private plugin marketplaces with managed settings ([docs](https://code.claude.com/docs/en/plugins/org)) | Claude Code, Claude Desktop, and Claude Cowork; admin allowlists of marketplace sources through managed settings |
| **Cursor** team marketplaces ([docs](https://cursor.com/docs/plugins)) | Cursor; a team marketplace imported from a GitHub repository; per-plugin install modes Default Off, Default On, and Required |
| **OpenAI Codex** workspace plugins ([help article](https://help.openai.com/en/articles/20001256)) | Codex and ChatGPT; a public or private GitHub marketplace imported with daily sync; per-role installation policy |
| **GitHub Copilot** enterprise-managed plugins ([changelog](https://github.blog/changelog/2026-05-06-enterprise-managed-plugins-in-github-copilot-cli-are-now-in-public-preview/)) | Copilot CLI; marketplaces defined in the enterprise's `.github-private` repository; installation on sign-in; plugins that bundle agents, hooks, skills, and MCP servers (public preview) |

**Where they apply.** A vendor marketplace fits an organization that sanctions a single coding agent.

**Where Podium applies.** Podium applies when an organization sanctions more than one coding agent and wants one source and one visibility policy across them, or when the catalog must be deployed on-prem. Podium renders marketplace repositories for Claude, Codex, Cursor, Gemini, Pi, and Hermes from the same catalog (§7.8 in the spec). The Claude, Codex, and Cursor marketplaces coexist in one repository, a Gemini extension occupies a repository of its own, and a Hermes tap defaults to a root `skills/` directory and usually takes its own repository. Podium has no GitHub Copilot adapter, so it writes no Copilot custom agents, hooks, or plugin marketplaces.

### Gateways and MCP server registries

These products overlap with Podium's `mcp-server` type and with the governance around MCP. Some gateways also store skills and hand them to coding agents.

| Product | License | Coverage |
|:--|:--|:--|
| **LiteLLM** Skills Gateway ([docs](https://docs.litellm.ai/docs/skills_gateway)) | Open source with a commercial enterprise edition | Imports skills from Git, grants private skills per key or team, and generates a Claude Code marketplace |
| **agentic-community/mcp-gateway-registry** ([repo](https://github.com/agentic-community/mcp-gateway-registry)) | OSS | Registers MCP servers, agents, and skills, with Keycloak or Entra OAuth, dynamic discovery, and scanning at registration |
| **Kong AI Registry** ([konghq.com](https://konghq.com/products/mcp-registry)) | Commercial | Governed discovery of MCP servers, tools, models, agents, and skills |
| **TrueFoundry MCP Gateway** ([truefoundry.com](https://www.truefoundry.com/mcp-gateway)) | Commercial | MCP catalog, OAuth, federated identity, and request tracing |
| **MACH Alliance MCP Registry** ([machalliance.org](https://machalliance.org/mach-alliance-mcp-registry)) | Industry alliance | A vendor-neutral directory of MCP servers that agents and tools query |
| **Docker MCP Catalog** ([hub.docker.com](https://hub.docker.com/mcp)) | Run by Docker, with community submissions | A catalog of containerized MCP servers |
| **GitHub Copilot internal MCP registry** ([docs](https://docs.github.com/en/copilot/how-tos/administer-copilot/manage-mcp-usage/configure-mcp-server-access)) | Hosted by the organization, configured in Copilot policies | An organization's registry of MCP servers for discovery and allowlisting in Copilot clients |
| **ServiceNow MCP Registry** ([docs](https://www.servicenow.com/docs/r/integrate-applications/mcp-registry.html)) | Commercial (ServiceNow application) | A private registry of approved MCP servers |

**Where they apply.** A gateway or MCP registry fits an organization whose catalog holds MCP server registrations alone, or that already routes coding-agent traffic through a gateway.

**Where Podium applies.** Podium applies when agents, contexts, commands, rules, and hooks need the same governance as MCP server registrations, when `mcpServers:` references from skills and agents must resolve to `mcp-server` artifacts as dependency edges, or when ordered layers compose several sources. A gateway and Podium can run together: the gateway brokers model and tool traffic, and Podium catalogs the authored artifacts.

### Cross-harness installers and generators

These tools copy or convert one source into several coding agents' formats on a developer machine or in CI, without a server.

| Tool | What it does |
|:--|:--|
| **`npx skills`** and the **Vercel skills.sh** registry ([skills.sh](https://skills.sh/), [repo](https://github.com/vercel-labs/skills)) | Installs skills from the public skills.sh registry, a Git repository, or a discovery index into many coding agents |
| **`gh skill`** ([changelog](https://github.blog/changelog/2026-04-16-manage-agent-skills-with-github-cli/)) | Searches, installs, pins, and updates skills from GitHub repositories |
| **rulesync** ([repo](https://github.com/dyoshikawa/rulesync)) | Generates rules, commands, MCP configuration, subagents, and skills for many coding agents from one source |
| **agent-skills-platform** ([repo](https://github.com/FrancyJGLisboa/agent-skills-platform)) | Builds tested skills from workflows and publishes them to a Git-hosted team marketplace |

**Where they apply.** These tools fit a single team or a library author with a modest catalog and no per-group visibility requirement.

**Where Podium applies.** Podium applies when several sources compose into one effective view, when visibility is evaluated per OIDC group at request time, when loads must be audited, or when the catalog is too large to install in full and needs lazy discovery through the MCP server.

### Git repository with harness-native directories

A Git repository that holds each harness's native directories is the baseline. A common variant keeps one `AGENTS.md` or rule file and runs a pre-commit hook or CI script that copies it into each harness's location, such as `CLAUDE.md`, `.cursorrules`, `.windsurfrules`, or `.github/copilot-instructions.md`.

**Where it applies.** This model applies to a single team with one or a few harnesses, one or two artifact types, a modest catalog, and no formal governance requirements. It requires no infrastructure beyond Git.

**Where Podium applies.** Podium applies when several repositories compose into one effective view with deterministic merge, when visibility differs per layer across teams, when each artifact type needs translation into the native format of each harness whose project-level surface carries it, when impact analysis must follow dependency edges across types, or when the catalog is too large to load in full and needs lazy discovery.

### Open specifications

Several open specifications define parts of this space. The table states what Podium implements of each.

| Specification | Status | What it defines | What Podium implements |
|:--|:--|:--|:--|
| **Agent Skills** ([specification](https://agentskills.io/specification)) | Specification governed by the Agentic AI Foundation (AAIF) | The `SKILL.md` skill directory format | A `type: skill` artifact carries a `SKILL.md` that follows the specification. Podium-only fields, such as `when_to_use` and `sensitivity`, live in an `ARTIFACT.md` beside it. |
| **AGENTS.md** ([agents.md](https://agents.md/)) | Open convention | Repository-wide instructions for coding agents | The Codex, OpenCode, and Pi adapters inject rule artifacts into `AGENTS.md` between Podium-managed markers. |
| **Agent Plugins 1.0** ([agent-plugins.org](https://agent-plugins.org/)) | Published specification, version 1.0.0 | A portable package format for skills, tools, and other agent components | Podium writes no Agent Plugins packages. |
| **MCP Skills Extension, SEP-2640** ([SEP](https://modelcontextprotocol.io/seps/2640-skills-extension)) | Final MCP extension (`io.modelcontextprotocol/skills`) | Serving skills from MCP servers | The Podium MCP server mirrors artifact bodies as read-only MCP resources and does not implement the skills extension. |
| **Agent Skills discovery index** ([RFC](https://github.com/cloudflare/agent-skills-discovery-rfc)) | Proposal (RFC) | A public `/.well-known/agent-skills/index.json` listing | The registry publishes no discovery index. |
```

**Implementation notes.**

1. The Copilot row links the github.blog changelog entry, and the Codex row links the OpenAI help article, that verification step 1 confirmed on 2026-11-09 (*Step 1 results*).
2. The "Where Podium applies" paragraphs mirror the SPEC-1 "When Podium wins" cells claim by claim. Verification step 3 compares them.
3. The vendor intro sentence names "the coding agents below" and makes no claim about vendors outside the table.
4. The Google Cloud Agent Registry delivery cell reads "Discovery through the registry's search API", which the 2026-11-09 step 1 result (semantic search in the skill-governance preview) supports.
5. The page names no Antigravity gap, because no category lists an Antigravity product (D-6).
6. The AGENTS.md row names the adapters by product name (Codex, OpenCode, and Pi), matching how the page names harnesses.
7. The Agent Skills "What Podium implements" cell holds under both forms of §4.3.4 (D-8): the main form, where a skill directory contains both files, and the form proposal 0058 stages, where `ARTIFACT.md` is optional. In both, the field-allocation table marks `when_to_use` and `sensitivity` "Not present" in `SKILL.md` and top-level in `ARTIFACT.md`. At S2, run these two greps:
   - `grep -c 'A skill artifact directory contains both .SKILL.md. and .ARTIFACT.md.' spec/04-artifact-model.md`, which matches the main *Two manifest files* sentence (`spec/04-artifact-model.md:257`).
   - `grep -c 'A skill artifact directory contains a .SKILL.md. and may contain an .ARTIFACT.md.' spec/04-artifact-model.md`, which matches the *Manifest files* sentence proposal 0058 stages (`proposals/0058-ingest-sync-materialize-and-serve-a-plain-agent-skills-direc.md:255`).
   - When exactly one grep returns 1, and the field-allocation table still places `when_to_use` and `sensitivity` in `ARTIFACT.md` alone, write the cell as staged. Otherwise stop S2 and record a deviation, because the cell's premise has changed. Verification step 3 re-runs both greps.
8. S2 applies the gateways table as staged. The product set is settled before approval (D-5), and S2 never edits `spec/`.
9. The block contains no fenced code block and no HTML comment (D-12, D-13).

### DOC-3. CHANGELOG

Append to `CHANGELOG.md` under `## [Unreleased]` > `### Documentation`, after the existing bullets:

```markdown
- **The product comparison in §1.5 and on the Why Podium page is current as of November 2026** (§1.5): both use one set of categories and list the same products. They drop Continue Hub, which has shut down. They use the current names AWS Agent Registry, Cursor team marketplaces, Claude private plugin marketplaces, and agent-skills-platform, and list AWS Agent Registry among the registries. They add Google Cloud Agent Registry, JFrog AI Catalog, agentregistry, Codex workspace plugins, Copilot enterprise-managed plugins, the LiteLLM Skills Gateway, `gh skill`, and rulesync. The closing text of §1.5 now states that the skill manifest follows Agent Skills, which AAIF governs, and that `ARTIFACT.md` is specified in §4. The Why Podium page adds an open-specifications table that states what Podium implements of Agent Skills, `AGENTS.md`, Agent Plugins 1.0, the MCP Skills Extension (SEP-2640), and the Agent Skills discovery index. It also states that Podium has no GitHub Copilot adapter and does not scan artifacts.
```

Verification step 3 re-reads the bullet against the final text of both files.

## Open questions

None. Verification step 1 ran in full on 2026-11-09 in the main session, and the staged text states each result (*Step 1 results*).

## Non-goals

- Editing §1.1, §1.3.1, §1.6, or `README.md`. Each was checked for consistency, and none needs a change.
- Any change under `pkg/`, `cmd/`, `internal/`, `sdks/`, or `web/`. The change is prose-only.
- Changing §4.3.4 or any skill-packaging behavior. The DOC-1 Agent Skills cell holds under either form of §4.3.4, and proposal 0058 owns the change to §4.3.4 itself.
- Changing how the docs name Podium's own harnesses, such as Gemini CLI, outside the comparison section.
- Adding Microsoft Agent 365, Chainguard, Bifrost, ruler, or wshobson/agents to the comparison.
- Adding a re-verification step to `RELEASING.md` or `.claude/rules/release-process.md`.
- Editing the landing page (`site/src/content/landing.ts`) or the page's frontmatter description.
- **A maintainer re-verification note as an HTML comment in `why-podium.md` (the draft's DOC-2).** Dropped for these reasons:
  - The dated intro sentence in DOC-1 (D-11) tells readers when the facts were checked, and the verification steps in this proposal perform the apply-time link and consistency check that the comment would ask maintainers to repeat.
  - Every HTML comment under `docs/` today is an ASCII diagram fallback, a placeholder inside an example artifact (`docs/overview.md:123`, `docs/authoring/extends.md:269`, `docs/getting-started/quickstart.md:130`), or an imported example (`docs/authoring/frontmatter-reference.md:264`). No page carries a maintainer note, so the comment would start a new convention.
  - The comment's "before each MINOR release, re-verify" instruction is a recurring release obligation. `.claude/rules/release-process.md` is the operative release checklist, and its Maintenance section keeps release steps there and in `RELEASING.md`. A step placed only in a docs comment sits outside that checklist, and nothing in `make coverage-gate`, doccov, or the site build reads it.
  - The request assigns a one-time refresh to 0.6.0 and asks for no recurring process. A recurring re-verification belongs in `.claude/rules/release-process.md` and `RELEASING.md`, in a separate change that touches those files.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-09)

- SPEC-1 dropped SkillReg on the strength of one search that found only a Product Hunt listing, while `tmp/research/notes-raw.md:70`, `sources.md:68` [S64], and `why-podium-refresh.md` record an active product. SkillReg stays, subject to verification step 1, and D-3, DOC-1, DOC-3, and the step 2 grep follow.
- SPEC-1's row-mapping sentence put installers third and gateways fourth, against the D-2 order. The sentence now follows D-2, and a clarifying sentence places skills.sh and the curated libraries from row 106.
- SPEC-2 staged an open-specifications table in the spec, which duplicated the docs table and placed fast-moving external facts and negative claims in the normative spec. SPEC-2 is now a prose edit of the closing sentence, the table lives on the docs page alone (D-2), and the AAIF status depends on verification step 1 because the research notes disagree on it.
- DOC-1 described the Gemini, Pi, and Hermes outputs as separate repositories, against §7.8. DOC-1 and D-16 now state the §7.8 grouping. The Pi clause this fix first added is corrected in Pass 1.
- DOC-1's "Where Podium applies" paragraphs stated different claims from the SPEC-1 cells. Each paragraph now mirrors its cell, and verification step 3 compares them.
- DOC-1 used a superlative ("the simplest choice"), an overgeneralization ("each major coding agent"), and "supported harness". Each is reworded. The Google Cloud delivery cell described discovery, and the Antigravity gap sentence referred to a product no row introduces; the cell is corrected and the sentence is dropped (D-6).
- DOC-3 claimed that both files state the Copilot, Antigravity, and scanning gaps and that both carry an open-specifications table. The bullet now attributes each to the page that states it and omits SkillReg.
- CHECK-1 moved out of the change list into the Testing section's verification steps. Its grep no longer matches the accurate §1.1 Bedrock mention, and its comment check reads the comment body.
- DOC-2 was dropped and recorded in Non-goals.

### Pass 1 (2026-11-09, automated)

- The proposal named 0058 as the owner of the docs Agent Skills row, but 0058 stages no `why-podium.md` edit and its Non-goals leave §1.5 to this proposal (`proposals/0058-ingest-sync-materialize-and-serve-a-plain-agent-skills-direc.md:1255`). This proposal now owns the row. DOC-1 implementation note 7 stages both cell texts and selects one with a grep of the §4.3.4 *Two manifest files* sentence at S2, which a build subagent can run. Q-1 records the edit 0058 must add if it lands after S2. D-8, the Summary D-8 line, the Watch-out bullet, the edge-case rows, and Non-goals follow.
- SPEC-1 and DOC-1 said every built-in adapter delivers native files, against §6.7 and §6.7.1: `claude-desktop` writes no project output, `claude-cowork` writes only contexts, `hermes` writes only rules and contexts, and the matrix carries further `✗` cells. The SPEC-1 first and last "When Podium wins" cells, the DOC-1 Podium delivery cell, and the registries and Git-repository "Where Podium applies" paragraphs now scope the claim to the types each harness's project-level surface carries (§6.7, §6.7.1). Current state and D-16 cite `pkg/adapter/builtins.go` and `spec/06-mcp-server.md` for it.
- D-16 and DOC-1 said a Pi package shares the Claude, Codex, and Cursor marketplace repository, which §7.8 does not state, and the Pi emitter uses the same root `skills/` directory as the Hermes tap (`pkg/adapter/emitter.go:348`, `:392`). Both now restate §7.8 exactly (`spec/07-external-integration.md:854`, `:881`) and make no grouping claim for Pi. The DOC-1 paragraph names the marketplace harnesses to mirror the SPEC-1 second row. Current state cites the §7.8 lines and the emitter constants.
- Verification step 1 picked between staged alternatives at apply time, which the pipeline's subagents cannot do because their web fetch is denied. Step 1 is now the S1 entry criterion and runs in the main session before approval. Its 2026-11-09 results are recorded under Verification and written into the staged text: SkillReg is removed (D-3), the AAIF donation is confirmed (D-9, SPEC-2), and the Copilot and Codex links are filled (DOC-1 note 1). The SkillReg and AAIF alternatives, the COPILOT_LINK placeholder and its IMPLEMENTOR'S CHOICE marker, and the conditional DOC-1 notes and DOC-3 sentence are removed. The products the 2026-11-09 results do not cover are listed for the pre-approval run.
- DOC-1 note 9 asked S2 to remove a dropped gateway product from SPEC-1 in the docs commit, which the pipeline forbids once the spec is applied, and SPEC-1 staged no alternative for it. D-5 now settles the gateway product set before approval by editing this proposal. DOC-1 note 8 (formerly 9) applies the table as staged, the Watch-out bullet says S2 never edits `spec/`, and the edge-case row for a dropped product describes the pre-approval edit.
- Correction: the §7.8 grouping citation pointed at `spec/07-external-integration.md:874`, a line inside the §7.8 repository-layout code block. The sentence that says Gemini and Hermes often take their own output is at `:881`. Current state, D-16, and the bullet above now cite `:854` and `:881`.
- Correction: the Pass 1 fix for the docs Agent Skills row covered only the order in which 0058 lands first. If 0058 landed after S2, the row needed an edit that Q-1 assigned to 0058, which stages none, and this proposal cannot edit 0058. The DOC-1 cell now states only what §4.3.4 says on main and in the 0058 form alike: the `SKILL.md` follows the specification, and Podium-only fields such as `when_to_use` and `sensitivity` live in an `ARTIFACT.md` beside it (`spec/04-artifact-model.md:257`, `:259`; `proposals/0058-ingest-sync-materialize-and-serve-a-plain-agent-skills-direc.md:162`, `:255`, `:261`). The row needs no edit in either landing order, so Q-1 and the late-order edge-case row are removed. DOC-1 implementation note 7 now greps for both forms of the §4.3.4 sentence and stops S2 when neither matches. D-8, the Summary D-8 line, the Watch-out bullet, the edge-case rows, and Non-goals follow.
- Correction: no verification step checked the note 7 selection. Verification step 3 now re-runs both note 7 greps, confirms that exactly one returns 1, and confirms that the written Agent Skills cell matches the staged text.
