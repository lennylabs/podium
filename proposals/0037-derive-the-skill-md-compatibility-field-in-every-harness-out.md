# Proposal 0037: Derive the SKILL.md compatibility field in every harness output that writes SKILL.md

- Issue: (to be filed)
- Status: Applied to spec (2026-10-02). Signed off as staged. OQ-1: no harness rejects the key (OpenCode and Pi recognize compatibility; Hermes ignores unknown fields), so the derivation ships for all with no exception. OQ-2: marketplace emitters are in scope.
- Date: 2026-10-02

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §4.3.4: the `compatibility` row of the SKILL.md field-allocation table drops the undefined qualifier "for harnesses that consume only the agentskills.io subset". It states that every harness adapter that writes a skill's `SKILL.md` (§6.7) and every marketplace emitter that writes one (§7.8) derives the field, and that `none` derives nothing (SPEC-1).
- `pkg/adapter`: `skillOut` in `pkg/adapter/layout.go` calls the existing `deriveSkillCompatibility`, which reaches the Cursor, Codex, Gemini, OpenCode, and Pi adapters and the Codex, Cursor, Pi, and Hermes marketplace emitters. `claudePluginSkill` in `pkg/adapter/emitter.go` calls it on its authored-`SKILL.md` branch. The doc comments in `compatibility.go` and `claudecode.go` that quote the old spec sentence follow SPEC-1, and `none.go` gains a comment that `none` derives nothing (CODE-1).
- Tests: a table-driven unit test over every SKILL.md writer, a `none` negative test, emitter tests, a ClaudeMarketplace fallback test, a golden fixture extension, a §11 equivalence arm for `cursor`, an end-to-end `--harness cursor` run, and a real-harness fixture change (TEST-1, TEST-2, TEST-3, TEST-4, VER-1).
- Docs and changelog: three doc lines that state the Claude-only scope are corrected, and an `## [Unreleased]` `### Changed` bullet records the output change (DOC-1, CL-1). Manual-validation scenario S78 is added (MV-1).

**Fixed decisions.**

- Option (a) is settled (user decision, 2026-10-02). Every harness output that writes a skill's `SKILL.md` derives `compatibility` through the existing `deriveSkillCompatibility`. No second derivation implementation is added.
- The scope is the project-scope adapters claude-code, cursor, codex, opencode, gemini, and pi, plus the §7.8 marketplace emitters for claude, codex, cursor, pi, and hermes (pending OQ-2).
- The Hermes sync adapter, claude-desktop, claude-cowork, and the GeminiExtension emitter write no skill `SKILL.md` and stay unchanged.
- The `none` adapter writes `SKILL.md` unchanged and derives nothing.
- The ClaudeMarketplace fallbacks for an artifact without `SKILL.md` (the synthesized rule body and the copied `ARTIFACT.md`) derive nothing.
- An authored `compatibility` is preserved byte for byte.
- The derivation lives in `skillOut`, whose every call site is a `type: skill` branch, and `claudePluginSkill` gets its own call. `ClaudeCode.Adapt` keeps its own call, because it also applies the §4.4.2 provenance rewrite and does not use `skillOut`.
- No §6.7.1 capability row, matrix-audit cell, error code, flag, environment variable, or opt-out is added.
- Lock `content_hash` values and the §7.5 dry-run `content_hash` do not move.
- §6.7 and §7.8 are not edited. §4.3.4 is the single statement of the rule.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate the SPEC-1 row by its quoted current text.
- **`claude-code.golden` already derives.** Extending the `team/hello` fixture changes `claude-code.golden` and `none.golden` before CODE-1 lands, because `ClaudeCode.Adapt` derives today (`pkg/adapter/claudecode.go:48`). S2 lands the fixture first so the S4 regeneration diff shows exactly the CODE-1 effect on the other five goldens.
- **Codex, OpenCode, and Pi fail on the §11 reference fixture.** Each hits a §6.9 untranslatable cell (`commands/quick-variance`, `hooks/audit-payment`, `finance/ap/pay-invoice`), so `sync.Run` errors before any comparison. Add only `cursor` (and optionally `gemini`) to the equivalence loop.
- **A byte-equality equivalence test passes on two trees that both lack the line.** TEST-3 asserts the line is present before it compares trees.
- **The `skillOut` call sites are all skill branches.** `skillOut` is reached only from `type: skill` cases (`pkg/adapter/builtins.go:65`, `:151`, `:180`, `:208`; `pkg/adapter/codex.go:22`; `pkg/adapter/emitter.go:228`, `:267`, `:360`, `:405`). Do not move the derivation into `appendResources` or another helper that non-skill types reach.
- **`claudePluginSkill` derives only on the authored-`SKILL.md` path.** Apply the call directly after `body := src.SkillBytes` and before the fallback branch reassigns `body`, guarded by `len(body) > 0`. A derivation over the `ARTIFACT.md` fallback body would inject a line into an `ARTIFACT.md` copy.
- **`sandbox_profile` on a skill is untranslatable for codex and pi.** `UsedCapabilities` emits a `sandbox_profile` capability for any artifact type (`pkg/adapter/capability.go:195-197`), the §6.7.1 `sandbox_profile` cell is ✗ for codex, pi, and hermes (`pkg/adapter/capability.go:97`; `spec/06-mcp-server.md:379`), and `podium sync` runs the §6.9 guard before `Adapt` (`pkg/sync/sync.go:290`). A skill that sets `sandbox_profile` therefore fails `podium sync --harness codex` or `--harness pi` with `materialize.untranslatable`, and the `; sandbox: <profile>` clause never reaches a synced codex or pi `SKILL.md`. Every fixture a sync-driven or golden test feeds to codex or pi declares `runtime_requirements` only. The golden test calls `Adapt` directly without the guard (`test/materialization/golden_test.go:170`), so a `sandbox_profile` in its fixture would pin output no real sync produces.
- **The real-harness suite is opt-in.** VER-1 runs only under the `harness_integration` build tag with the harness CLIs installed. A green default `go test` says nothing about it.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §4.3.4 states the derivation scope for every SKILL.md-writing adapter and emitter, and the `none` exception.
      Levels: —. Depends on: —
- [ ] **S2 · test** — TEST-2. The `team/hello` golden fixture gains `runtime_requirements` (and no `sandbox_profile`), and the goldens are regenerated before CODE-1.
      Levels: materialization. Depends on: S1
      Interleave: S2 lands before the code step so the claude-code and none golden diffs are attributable to the fixture alone.
- [ ] **S3 · code** — CODE-1. `skillOut` and `claudePluginSkill` derive `compatibility`, and the comments in `compatibility.go`, `claudecode.go`, and `none.go` follow SPEC-1.
      Levels: unit, materialization. Depends on: S1, S2
- [ ] **S4 · test** — TEST-1. Unit tests for every SKILL.md writer, the `none` adapter, the emitters, and the ClaudeMarketplace fallbacks, plus the post-CODE-1 golden regeneration.
      Levels: unit, materialization. Depends on: S3
- [ ] **S5 · test** — TEST-3. The §11 equivalence test gains a `cursor` arm with a presence assertion.
      Levels: integration. Depends on: S3
- [ ] **S6 · test** — TEST-4. The end-to-end skill tutorial test runs `--harness cursor`, and the integration comment drops the Claude-only framing.
      Levels: integration, e2e. Depends on: S3
- [ ] **S7 · test** — VER-1. The real-harness skill fixture carries `runtime_requirements` (and no `sandbox_profile`), and the Tier C skill subtest asserts the derived line.
      Levels: e2e (opt-in `harness_integration`). Depends on: S3
- [ ] **S8 · docs** — DOC-1. Three doc lines state the new scope.
      Levels: —. Depends on: S3
- [ ] **S9 · docs** — CL-1. The `## [Unreleased]` `### Changed` bullet.
      Levels: —. Depends on: S3
- [ ] **S10 · docs** — MV-1. Manual-validation scenario S78.
      Levels: manual. Depends on: S3

**Ordering constraints.** S2 precedes S3 so that the S2 regeneration changes only `claude-code.golden` and `none.golden`, and the S4 regeneration adds one line to each of `codex.golden`, `cursor.golden`, `gemini.golden`, `opencode.golden`, and `pi.golden`. S4 depends on S3 because its golden step and its assertions fail before CODE-1 lands.

## Current state and the gap

§4.3.4, the SKILL.md field-allocation table, says that when `compatibility` is not authored, "the Podium adapter derives it from `runtime_requirements` and `sandbox_profile` at materialization time for harnesses that consume only the agentskills.io subset". No other spec sentence scopes the derivation. §4.1, §6.7, §6.7.1, and §7.8 never mention it. The spec does not define "consume only the subset" and lists no harness that does, so the qualifier is ambiguous. Its literal reading arguably excludes Claude Code, which reads extra frontmatter fields.

The code derives the field in one place. `deriveSkillCompatibility` (`pkg/adapter/compatibility.go:22`) has a single call site, `ClaudeCode.Adapt` (`pkg/adapter/claudecode.go:48`). The other project-scope adapters that write a skill's `SKILL.md` are Cursor (`pkg/adapter/builtins.go:65`), Gemini (`:151`), OpenCode (`:180`), Pi (`:208`), and Codex (`pkg/adapter/codex.go:22`). Each calls `skillOut`, which writes `src.SkillBytes` unchanged (`pkg/adapter/layout.go:54-62`).

The §7.8 marketplace emitters also skip derivation. `CodexMarketplace` (`pkg/adapter/emitter.go:228`), `CursorMarketplace` (`:267`), `PiPackage` (`:360`), and `HermesTap` (`:405`) call `skillOut`. `ClaudeMarketplace` routes skills through `claudePluginSkill` (`:139`, `:167-182`), which copies `SkillBytes`, so Claude's own marketplace output also lacks the derived line. claude-desktop (`pkg/adapter/builtins.go:27-29`), claude-cowork (`:47-52`), and the Hermes sync adapter (`:229-241`; the §6.7 skill row marks hermes ✗) write no `SKILL.md`. The `GeminiExtension` emitter (`pkg/adapter/emitter.go:310`) has no skill case. The `none` adapter writes `SKILL.md` verbatim (`pkg/adapter/none.go:29-33`), as §6.7 requires ("`none` writes the canonical layout ... without translation"), and §6.6 makes the adapter step a no-op for `none`.

The derivation exists because a harness reads `SKILL.md` rather than `ARTIFACT.md`, so the runtime constraints declared in `ARTIFACT.md` are otherwise invisible to it (`pkg/adapter/compatibility.go:16-18`). That holds for every harness target, because the §6.7 skill row materializes `SKILL.md` and bundled resources and no `ARTIFACT.md`. The user decided on 2026-10-02 (option a, not to be reopened) that every adapter that writes a skill's `SKILL.md` derives the field. Three doc pages state the current Claude-only scope: `docs/reference/frontmatter-schema.md:109`, `docs/authoring/frontmatter-reference.md:31`, and `docs/authoring/frontmatter-reference.md:118`.

Several facts were checked and need no change.

- No §6.7.1 capability cell or matrix-audit cell covers the field. `pkg/adapter/capability.go:81-117` and `tools/matrix/matrices.go:46-73` have no compatibility row, and the `sandbox_profile` row grades agent-output fidelity.
- Lock content hashes do not move. `content_hash` is `version.CanonicalContentHash` over the authored bytes (`pkg/version/version.go:291`; `pkg/registry/core/admit.go:72`; `pkg/sync/sync.go:317`, `:433-449`) and is never computed over adapter output.
- Filesystem and server sync pass identical `SKILL.md` and `ARTIFACT.md` bytes to the shared adapter (`pkg/sync/server.go:146`, `:155-161`; `pkg/registry/ingest/ingest.go:1228`, `:1233`; `manifest.SerializeMerged` for `extends:` children). `deriveSkillCompatibility` is a pure function of those bytes, so §11 equivalence holds by construction.
- The §11 equivalence test runs only `none` and `claude-code` (`test/integration/sync_equivalence_test.go:42`). Its reference fixture already carries a derivable skill: `testdata/registries/reference/team-finance/finance/close/run-variance/ARTIFACT.md` declares `runtime_requirements.python` and `sandbox_profile`, and its `SKILL.md` omits `compatibility`.
- The golden fixture `team/hello` (`test/materialization/golden_test.go:42-47`) declares neither field, so no current golden contains a compatibility line, and the goldens would not show the change unless the fixture is extended.

## Decisions

- **Every SKILL.md writer derives.** Option (a) is settled. The derivation reuses `deriveSkillCompatibility` and `buildSkillCompatibility` unchanged.
- **The marketplace emitters are in scope.** `pkg/adapter/emitter.go:11-16` states that the emitters reuse the §6.7 layout helpers so the two modes translate a type the same way, and §7.8 describes the emitters as rendering the `SKILL.md` the harnesses consume. A harness reads the same `SKILL.md` whether it arrives through sync or through a published marketplace. OQ-2 asks the reviewer to confirm this.
- **Hermes is in scope only through `HermesTap`.** The Hermes sync adapter writes no project-scope skills, and this proposal does not change that.
- **`none` stays raw.** §6.7 says `none` writes the canonical layout without translation, the §6.7 adapter table calls it "No harness-specific translation", and §6.6 makes the adapter step a no-op for it. Injecting a derived line would be a translation. The `none` output also includes `ARTIFACT.md` (`pkg/adapter/none.go:23-28`), which carries `runtime_requirements` and `sandbox_profile` as top-level fields, so a reader of the raw output sees the constraints without derivation.
- **Only authored skill `SKILL.md` files derive.** §4.3.4 governs skill artifacts, and a rule has no SKILL.md compatibility allocation. The `ruleSkillBody` fallback and the `ARTIFACT.md`-as-body fallback in `claudePluginSkill` stay unchanged.
- **An authored value is kept.** This is unchanged behavior (`pkg/adapter/compatibility.go:27-28`).
- **The derivation lives in `skillOut`.** Every `skillOut` call site is a `type: skill` branch, so one edit reaches all of them. `claudePluginSkill` gets its own call. `ClaudeCode.Adapt` keeps its call.
- **No capability row and no matrix cell.** The field is a derived frontmatter line, uniform across every SKILL.md writer, and grades no per-harness fidelity.
- **§4.3.4 is the single statement.** §6.7 and §7.8 gain no sentence (see Non-goals).
- **Hashes stay stable while materialized bytes change.** Only materialized `SKILL.md` bytes change, and only for a non-Claude-Code output or any marketplace output of a skill whose `ARTIFACT.md` yields a non-empty derived string (`runtime_requirements` with `python`, `node`, or `system_packages`, or a set `sandbox_profile`) and whose `SKILL.md` omits `compatibility`, opens with a `---` delimiter, and parses.
- **No new configuration surface.** Podium is pre-1.0, so the output change lands without an opt-out flag or a dual code path.

## Spec amendment: §4.3.4 SKILL.md compatibility derivation

**SPEC-1.** `spec/04-artifact-model.md`, §4.3.4, the field-allocation table, the `compatibility` row (line 266 at the time of writing). The current row is:

```
| `compatibility` | Top-level (per spec; ≤ 500 chars; human-readable string) | Omitted; if not authored, the Podium adapter derives it from `runtime_requirements` and `sandbox_profile` at materialization time for harnesses that consume only the agentskills.io subset |
```

Replace the ARTIFACT.md cell (the third cell) with:

> Omitted. When `SKILL.md` does not author it, every harness adapter that writes a skill's `SKILL.md` (§6.7) and every marketplace emitter that writes one (§7.8) derives it from `runtime_requirements` and `sandbox_profile` at materialization time and adds it to the materialized `SKILL.md`. An authored value is kept unchanged. The `none` adapter writes `SKILL.md` without translation (§6.7) and derives nothing.

The SKILL.md cell (the second cell) is unchanged. No other row changes.

## Proposed solution

### CODE-1. Derive in `skillOut` and in the Claude marketplace skill branch

Lands in S3. Run `gofmt`, `goimports`, and `make lint`.

- `pkg/adapter/layout.go`, `skillOut` (lines 52-62). Replace `Content: src.SkillBytes` with `Content: deriveSkillCompatibility(src.SkillBytes, src.ArtifactBytes)`, preceded by `// Spec: §4.3.4: every adapter that writes a skill's SKILL.md derives compatibility when the author omitted it.` Rewrite the doc comment to: `skillOut materializes a skill folder at dir: SKILL.md, with compatibility derived per §4.3.4 when the author omitted it, plus the bundled scripts/, references/, and assets/ resources alongside it.`
- `pkg/adapter/emitter.go`, `claudePluginSkill` (lines 163-182). Immediately after `body := src.SkillBytes`, add:

  ```go
  if len(body) > 0 {
  	// Spec: §4.3.4, §7.8: the plugin's SKILL.md carries the derived
  	// compatibility line. The rule and ARTIFACT.md fallbacks below derive
  	// nothing, because §4.3.4 allocates the field only to a skill's SKILL.md.
  	body = deriveSkillCompatibility(body, src.ArtifactBytes)
  }
  ```

  The `ruleSkillBody` and `ArtifactBytes` fallbacks stay unchanged.
- `pkg/adapter/compatibility.go`, the `deriveSkillCompatibility` doc comment (lines 13-21). Replace the quoted sentence with the SPEC-1 wording: every harness adapter that writes a skill's `SKILL.md` and every marketplace emitter that writes one derives the field, and `none` derives nothing. Keep the reason that harnesses read `SKILL.md` rather than `ARTIFACT.md`, and keep the list of unchanged-return conditions.
- `pkg/adapter/claudecode.go`, the comment at lines 41-45. Remove "Claude Code consumes only the agentskills.io subset" and state that the derivation follows §4.3.4 for every SKILL.md-writing adapter and runs before the §4.4.2 provenance rewrite, so the rewrite sees the derived line.
- `pkg/adapter/none.go`, `None.Adapt`. Add a comment beside the `SKILL.md` write stating that `none` writes `SKILL.md` without deriving `compatibility` (§6.6, §6.7). The code is unchanged.

`pkg/adapter/capability.go`, `tools/matrix/matrices.go`, and `pkg/sync` stay unchanged.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Skill with `runtime_requirements.python` or `sandbox_profile` and no authored `compatibility`, synced to claude-code, cursor, opencode, or gemini, or a skill with `runtime_requirements.python` and no `sandbox_profile` synced to codex or pi | `SKILL.md` gains `compatibility: "<derived>"` as the first frontmatter line | §4.3.4 (SPEC-1); `docs/reference/frontmatter-schema.md`, `docs/authoring/frontmatter-reference.md` (DOC-1) |
| Skill that sets `sandbox_profile`, synced to codex or pi | Sync refuses the artifact with `materialize.untranslatable` before `Adapt` runs, so no `SKILL.md` is written (unchanged behavior) | §6.9 and the §6.7.1 `sandbox_profile` row (✗ for codex and pi; `pkg/sync/sync.go:290`, `pkg/adapter/capability.go:97`); no doc change |
| The same skill published through the claude, codex, cursor, pi, or hermes marketplace emitter | `skills/<n>/SKILL.md` gains the same line | §4.3.4 (SPEC-1), pending OQ-2; DOC-1 marketplace sentence, pending OQ-2 |
| The same skill synced to `none` | `SKILL.md` is byte-identical to the authored file; `ARTIFACT.md` carries the fields | §4.3.4 (SPEC-1), §6.7; DOC-1 |
| Authored `compatibility` present | Output `SKILL.md` is byte-identical to the authored file on every adapter | §4.3.4 (SPEC-1) "An authored value is kept unchanged"; DOC-1 |
| `ARTIFACT.md` declares neither field, or `runtime_requirements` with no `python`, `node`, or `system_packages` | No line added; output byte-identical | §4.3.4 (SPEC-1) "derives it from `runtime_requirements` and `sandbox_profile`"; `docs/reference/frontmatter-schema.md` |
| `SKILL.md` lacks a leading `---` delimiter, or `SKILL.md` or `ARTIFACT.md` fails to parse | No line added; output byte-identical (accepted). Ingest lint reports the parse failure separately | Accepted behavior of `deriveSkillCompatibility`; no spec sentence is staged, because a malformed manifest is already a lint error under §4.3.4 |
| Derived string longer than 500 characters | Truncated to 500 characters and trimmed (unchanged) | §4.3.4 "≤ 500 chars"; `docs/reference/frontmatter-schema.md` |
| ClaudeMarketplace: a `type: rule` artifact shipped as a skill, or a non-rule artifact without `SKILL.md` | Synthesized rule body or copied `ARTIFACT.md`; no compatibility line | §4.3.4 allocates the field to a skill's `SKILL.md` only; no doc sentence, as the fallback is not documented as a skill |
| Hermes sync adapter, claude-desktop, claude-cowork, GeminiExtension emitter | No `SKILL.md` written; unchanged | §6.7 skill row (✗); `docs/consuming/configure-your-harness.md` |
| A target harness rejects an unknown `compatibility` key | That harness fails to load the skill (accepted, unverified for OpenCode, Pi, and Hermes). The same exposure exists today for an authored value | agentskills.io lists `compatibility` as optional (§4.1); OQ-1 |
| Re-sync of an existing target after upgrade | Affected `SKILL.md` files are rewritten; lock `content_hash` values are unchanged | §4.7.6; `CHANGELOG.md` (CL-1) |
| Filesystem-source and server-source sync of the same skill | Byte-identical `SKILL.md`, including the derived line | §11 (unchanged); TEST-3 |

## Testing

**TEST-1 · unit and materialization, `pkg/adapter/compatibility_test.go` and `pkg/adapter/emitter_test.go`.** Lands in S4.

In `pkg/adapter/compatibility_test.go`, replace the three ClaudeCode-only tests at :57, :91, and :113 with one table-driven test, `TestAdapters_SkillCompatibilityDerivation`, annotated `// Spec: §4.3.4` and `// Spec: §6.7`.

- Rows: the adapter IDs claude-code, cursor, codex, opencode, gemini, and pi, each resolved through `DefaultRegistry().Get(id)`.
- Shared input: `ARTIFACT.md` declares `runtime_requirements: {python: ">=3.10"}` and no `sandbox_profile`. A `sandbox_profile` on a skill is a §6.7.1 ✗ cell for codex and pi, so `podium sync` refuses that input for those harnesses before `Adapt` (see Watch out for), and a codex or pi row fed it would pin output no real sync produces.
- Subcase (a), compatibility omitted: parse the output `SKILL.md` with `manifest.ParseSkill` and assert `Compatibility == "Requires Python >=3.10"`. Also assert that the original name and body survive.
- Subcase (e), only for the rows claude-code, cursor, opencode, and gemini (the harnesses whose §6.7.1 `sandbox_profile` cell is ✓): `ARTIFACT.md` additionally declares `sandbox_profile: read-only-fs`, and the test asserts `Compatibility == "Requires Python >=3.10; sandbox: read-only-fs"`.
- Subcase (b), compatibility authored: assert the output `SKILL.md` is byte-identical to the input.
- Subcase (c), `ARTIFACT.md` has no `runtime_requirements` and no `sandbox_profile`: assert the output is byte-identical to the input.
- Subcase (d), `SKILL.md` has no leading `---` frontmatter: assert the output is byte-identical to the input.

Separately, add `TestNone_DoesNotDeriveCompatibility`. Feed `None{}.Adapt` the derivable input from subcase (a) and assert that `SKILL.md` is byte-identical to `SkillBytes`. Annotate it `// Spec: §6.7`.

In `pkg/adapter/emitter_test.go`, add `TestEmitters_DeriveSkillCompatibility`, annotated `// Spec: §4.3.4` and `// Spec: §7.8`.

- Rows: the harness IDs claude-code, codex, cursor, pi, and hermes, each resolved through `EmitterForHarness(id)`. The lookup proves that every publish-target harness ID routes to a deriving emitter.
- Give each row a `finPlugin` prefix and use the inputs from subcases (a) and (b). Look up the `skills/<name>/SKILL.md` file and assert the derived value in (a) and byte identity in (b).

Add a second test, `TestClaudeMarketplace_FallbackBodiesDoNotDerive`, with two cases. In both, `ARTIFACT.md` carries `runtime_requirements` and `sandbox_profile`, and `SkillBytes` is empty.

- A `type: rule` artifact: assert that the synthesized `SKILL.md` contains no `compatibility:` line.
- A non-rule artifact: assert that the `SKILL.md` content equals `ArtifactBytes` byte for byte.

Reuse the existing helpers `skillContent` and `fileByPath`. Do not add a new test file.

The S4 step also regenerates the goldens after CODE-1 (`UPDATE_GOLDEN=1 go test ./test/materialization/`) and confirms the diff is exactly one added line, `compatibility: "Requires Python >=3.10"`, directly after the opening `---` of the hello `SKILL.md` block in `codex.golden`, `cursor.golden`, `gemini.golden`, `opencode.golden`, and `pi.golden`.

**TEST-2 · materialization, `test/materialization/golden_test.go` and `test/materialization/testdata/golden/*.golden`.** Lands in S2.

Extend the `team/hello` entry of `canonicalArtifacts` (lines 42-47) so its `ARTIFACT.md` declares `runtime_requirements: {python: ">=3.10"}`. The fixture declares no `sandbox_profile`: the golden test calls `Adapt` directly without the §6.9 guard (`test/materialization/golden_test.go:170`), and a `sandbox_profile` would put a `; sandbox:` clause into `codex.golden` and `pi.golden` that `podium sync` never writes, because the §6.7.1 `sandbox_profile` cell is ✗ for both. TEST-1 subcase (e) covers the sandbox clause. The hello `SKILL.md` keeps no `compatibility`. Extending the existing fixture adds the smallest diff, because a new fixture would add complete new `SKILL.md` and resource blocks to every golden.

- (a) The expected `none.golden` diff is two added lines in `team/hello/ARTIFACT.md`, placed after `version: 1.0.0`: `runtime_requirements:` and `  python: ">=3.10"`. `team/hello/SKILL.md` gets no compatibility line.
- (b) Regenerate before CODE-1. At that point only `claude-code.golden` and `none.golden` change, because `ClaudeCode.Adapt` already derives the field (`pkg/adapter/claudecode.go:48`). The second regeneration, after CODE-1, is part of S4. If the commit history does not separate the two steps, state in the commit message that `claude-code.golden` changed only because of the fixture.
- (c) Update the `canonicalArtifacts` doc comment (`golden_test.go:37-40`) so its list of translated fields includes `runtime_requirements`, through the §4.3.4 derived `SKILL.md` compatibility line.

`hermes.golden`, `claude-desktop.golden`, and `claude-cowork.golden` stay unchanged, and `validity_test.go` must still pass.

**TEST-3 · integration, `test/integration/sync_equivalence_test.go`.** Lands in S5.

In `TestSyncEquivalence_FilesystemVsServerByteIdentical` (line 42), extend the adapter list to `[]string{"none", "claude-code", "cursor"}`. Adding `"gemini"` is optional. Do not add codex, opencode, or pi. On the reference fixture each of them hits a §6.9 untranslatable cell: codex on `commands/quick-variance` (`type: command`), opencode on `hooks/audit-payment` (`hook_event`), and pi on `finance/ap/pay-invoice` (`type: agent`). `sync.Run` then returns an error before any comparison runs. Codex equivalence is already covered by `TestSyncEquivalence_SharedMergeTargetsAreByteIdentical`.

For the cursor arm, and for claude-code, which already derives the line, assert before `assertTreesEqual` that `fsTree[".cursor/skills/run-variance/SKILL.md"]` (respectively `".claude/skills/run-variance/SKILL.md"`) contains `compatibility: "Requires Python >=3.10; sandbox: read-only-fs"`. That assertion keeps the byte comparison from passing on two trees that both lack the line. Keep the `// Spec: §11` annotation and add `// Spec: §4.3.4`. Make no fixture change.

**TEST-4 · e2e and integration, `test/e2e/skill_tutorial_test.go` and `test/integration/skill_lint_test.go`.** Lands in S6.

- Rename `TestSkillTutorial_ClaudeCodeDerivesCompatibility` (lines 351-377) to `TestSkillTutorial_HarnessesDeriveCompatibility`. Add a `--harness cursor` run that asserts the exit code is 0 and that `.cursor/skills/greet/SKILL.md` contains `compatibility:` and `Python >=3.10`. A `--harness codex` run asserting `.agents/skills/greet/SKILL.md` is optional. Keep the claude-code assertion and the `none` negative assertion. Rewrite the comment block to cite `// Spec: §4.3.4` and `// Spec: §6.7` and to drop the Claude-only framing.
- In `test/integration/skill_lint_test.go:118-121`, replace "(which consumes only the agentskills.io subset)" with "like every adapter that writes a skill's SKILL.md".
- `test/e2e/frontmatter_schema_test.go:282-303` asserts lint and the MCP response, and needs no change.

Before claiming end-to-end verification, grep the run output for `SKIP`, because parts of `test/e2e` skip silently on macOS.

**VER-1 · opt-in real-harness e2e, `test/harness_integration/integration_test.go`.** Lands in S7.

Change the `skills/weather/ARTIFACT.md` fixture in `skillRegistry` (lines 62-67; the fixture is at :64) so that CODE-1 derives `compatibility: "Requires Python >=3.10"`:

```
---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: ">=3.10"\n---\n\nWeather skill.\n
```

The fixture declares no `sandbox_profile`. codex is in the skill behavior's `run` list (`test/harness_integration/integration_test.go:377-379`) and the `TestHarnessArtifactTypes` loop (:419), the §6.7.1 `sandbox_profile` cell is ✗ for codex (`pkg/adapter/capability.go:97`), and `podium sync` runs the §6.9 guard before `Adapt` (`pkg/sync/sync.go:290`). A `sandbox_profile` would make `podium sync --harness codex` exit 1 with `materialize.untranslatable`, and `syncProject` would fail the codex skill subtest through `t.Fatalf` (:96-98) before the agent turn.

Leave `SKILL.md` without `compatibility`. Add a Go assertion in the Tier C skill subtest that checks the synced `SKILL.md` contains `compatibility:` before the agent turn, so that a pass shows the harness loaded a skill carrying the derived key. Cite `// Spec: §4.3.4` and `// Spec: §6.7`.

The coverage limits are explicit:

- This confirms acceptance only for claude-code, cursor, codex, and gemini, which are the harnesses in the behaviors skill `run` list and the `TestHarnessArtifactTypes` loop.
- The suite does not drive OpenCode, Pi, or Hermes (through `HermesTap`). Their acceptance rests on agentskills.io listing `compatibility` as an optional field (§4.1).
- The same exposure already exists today for an authored `compatibility`, which `skillOut` passes through verbatim.
- The suite exercises the `Requires ...` clause only. The `; sandbox: <profile>` clause is covered below the real-harness level by TEST-1 subcase (e) and by TEST-3, whose reference fixture declares `sandbox_profile` and runs only on harnesses whose cell is ✓.

A documentation check of each harness's skill loader is a main-loop pre-landing check recorded in OQ-1. It gates CODE-1 only if a run of this suite fails.

**Coverage.** Run `go test -coverpkg=./... -coverprofile=cover.out ./pkg/adapter/... ./test/materialization/... ./test/integration/...` and confirm the changed lines in `layout.go` and `emitter.go` reach 85% with `go tool cover -func=cover.out`.

## Manual validation

**MV-1.** Add scenario S78 to `test/manual-validation.md` after S77, following the existing conventions. Lands in S10.

~~~~markdown
## S78: Every harness output carries the derived skill compatibility line

**Goal.** Validate that `podium sync` writes a derived `compatibility` line
into a skill's `SKILL.md` for a non-Claude harness, keeps an authored value
unchanged, and leaves the `none` output untouched.

**Covers.** The §4.3.4 derivation scope, the §6.7 `none` exception, and the
lock-hash stability the changelog states.

**Why by hand.** The unit and end-to-end tests parse the frontmatter. What
they do not read is the file a developer opens in the harness directory: that
the line sits at the top of the frontmatter, that it reads as a sentence an
agent can act on, and that the lock file does not churn.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then create a
   registry with one skill that declares runtime constraints and omits
   `compatibility`, and one skill that authors it.

   ```bash
   mkdir -p "$WORK/reg/tools/greet" "$WORK/reg/tools/authored"
   printf -- '---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: ">=3.10"\nsandbox_profile: read-only-fs\n---\n\nGreet.\n' \
     > "$WORK/reg/tools/greet/ARTIFACT.md"
   printf -- '---\nname: greet\ndescription: Greets the user.\n---\n\nSay hello.\n' \
     > "$WORK/reg/tools/greet/SKILL.md"
   cp "$WORK/reg/tools/greet/ARTIFACT.md" "$WORK/reg/tools/authored/ARTIFACT.md"
   printf -- '---\nname: authored\ndescription: Authored compatibility.\ncompatibility: Needs a GPU.\n---\n\nRun it.\n' \
     > "$WORK/reg/tools/authored/SKILL.md"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and the four
   files exist.

2. Sync to Cursor.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/cur" --harness cursor; echo "exit=$?"
   head -4 "$WORK/cur/.cursor/skills/greet/SKILL.md"
   cat "$WORK/cur/.cursor/skills/authored/SKILL.md"
   ```

   **Expect.** `exit=0`. The greet `SKILL.md` opens with `---` followed by
   `compatibility: "Requires Python >=3.10; sandbox: read-only-fs"`, then
   `name: greet`. The authored `SKILL.md` is identical to the source file and
   carries `compatibility: Needs a GPU.` only once. A greet file without the
   line is the shipped behavior this scenario exists to catch.

3. Sync to `none` and compare.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/raw" --harness none; echo "exit=$?"
   diff "$WORK/reg/tools/greet/SKILL.md" "$WORK/raw/tools/greet/SKILL.md" && echo identical
   ```

   **Expect.** `exit=0` and `identical`. A `compatibility:` line in the `none`
   output is a defect.

4. Record the lock hashes, re-sync Cursor, and compare.

   ```bash
   grep content_hash "$WORK/cur/.podium/sync.lock" > "$WORK/h1.txt"
   podium sync --registry "$WORK/reg" --target "$WORK/cur" --harness cursor; echo "exit=$?"
   grep content_hash "$WORK/cur/.podium/sync.lock" | diff "$WORK/h1.txt" - && echo stable
   ```

   **Expect.** `exit=0` and `stable`. A changed `content_hash` means the hash
   was computed over adapter output, which is a defect.
~~~~

## Documentation changes

**DOC-1.** Lands in S8. Apply `.claude/rules/doc-style.md`. No `tools/doccov/manifest.yaml` change is needed.

- (a) `docs/reference/frontmatter-schema.md:109`, the `compatibility` row, last cell. Replace "When it is omitted, the Claude Code adapter derives a string from `runtime_requirements` and `sandbox_profile` at materialization time; the other adapters copy `SKILL.md` unchanged and leave the field absent." with: "When it is omitted, every harness adapter that writes a skill's `SKILL.md` derives a string from `runtime_requirements` and `sandbox_profile` at materialization time and adds it to the materialized `SKILL.md`. The `none` adapter copies `SKILL.md` unchanged and leaves the field absent."
- (b) `docs/authoring/frontmatter-reference.md:31`, the last cell of the `compatibility` row. Replace "— (the Claude Code adapter derives from `runtime_requirements` and `sandbox_profile`)" with "— (every adapter that writes a skill's `SKILL.md`, except `none`, derives it from `runtime_requirements` and `sandbox_profile`)".
- (c) `docs/authoring/frontmatter-reference.md:118`. Replace the last two sentences ("When it is omitted, the Claude Code adapter ... reaches Claude Code output only.") with: "When it is omitted, every harness adapter that writes a skill's `SKILL.md` derives a compatibility string from `runtime_requirements` and `sandbox_profile` and adds it to the materialized `SKILL.md`. The `none` adapter copies `SKILL.md` unchanged."
- Append "Published marketplace output carries the same line." to (a) and (c) only if OQ-2 confirms that the §7.8 emitters are in scope. Otherwise omit it.

`docs/consuming/configure-your-harness.md` is not edited, because it never mentions `compatibility`. `docs/consuming/custom-via-sdk.md:94` describes the SDK's canonical layout and needs no edit.

**CL-1.** Lands in S9. Append one bullet to the existing `### Changed` list under `## [Unreleased]` (`CHANGELOG.md:153`). Do not add a second `### Changed` heading, because `release.yml` extracts the section verbatim.

```markdown
- **Every harness adapter derives the skill `compatibility` field** (§4.3.4, §6.7, §7.8): when a skill's `SKILL.md` omits `compatibility`, the Cursor, Codex, Gemini, OpenCode, and Pi adapters and the Claude, Codex, Cursor, Pi, and Hermes marketplace emitters now derive it from `runtime_requirements` and `sandbox_profile`, as the Claude Code adapter already did. An authored value is kept unchanged, and the `none` adapter still writes `SKILL.md` unchanged. A re-sync or re-publish rewrites the affected `SKILL.md` files. Lock-file `content_hash` values do not change, because they hash the authored artifact bytes rather than adapter output.
```

The emitter clause, the §7.8 citation, and "or re-publish" are contingent on OQ-2. If marketplace output is ruled out of scope, remove all three.

## Open questions

**OQ-1. A harness that rejects the key.** If a target harness rejects an unknown or extra `compatibility` frontmatter key, should that harness be excluded from the derivation, which adds a per-harness exception to SPEC-1, or should the derivation ship for all harnesses with the incompatibility recorded as a harness defect? The proposal assumes no harness rejects the key, because it is an agentskills.io optional field. VER-1 checks claude-code, cursor, codex, and gemini. A main-loop documentation check of the OpenCode, Pi, and Hermes skill loaders before landing is recommended, since subagents cannot reach the web.

**OQ-2. Marketplace emitters.** The proposal extends the derivation to the §7.8 marketplace emitters (claude, codex, cursor, pi, and hermes) as well as the project-scope adapters. The user's decision names every harness adapter that writes `SKILL.md`. Confirm that published marketplace output is in scope, or narrow SPEC-1 by removing "and every marketplace emitter that writes one (§7.8)", drop the `claudePluginSkill` half of CODE-1 and the `skillOut` derivation for the emitter call sites, drop `TestEmitters_DeriveSkillCompatibility`, and remove the emitter clauses from DOC-1 and CL-1.

## Non-goals

- Changing the derived string's format, its 500-character cap, or the fields it reads. `buildSkillCompatibility` is unchanged.
- Deriving `compatibility` in the `none` adapter or in the SDK `materialize()` canonical-layout output (`docs/consuming/custom-via-sdk.md:94`), which writes no harness translation.
- Materializing project-scope skills for Hermes, claude-desktop, or claude-cowork, or adding a skill case to the `GeminiExtension` emitter.
- Deriving `compatibility` for the Claude marketplace rule-as-skill fallback (`ruleSkillBody`).
- Adding a §6.7.1 capability-matrix row, a matrix-audit cell, an error code, a flag, an environment variable, or an opt-out of the derivation.
- Making ingest lint require or warn on an absent `compatibility`.
- Editing §11, which already requires byte-identical harness-adapter output across deployment modes.
- **A §6.7 sentence naming the derived line (dropped SPEC-2).** After SPEC-1, §4.3.4 states the whole rule: the writers, the condition, the inputs, preservation of an authored value, and the `none` exception. A §6.7 restatement adds no rule and creates a second place that a later edit (an OQ-1 exemption or an OQ-2 scope change) can leave disagreeing with §4.3.4. The §6.7 sentence "`none` writes the canonical layout ... without translation" and §6.6 already fix the `none` side, and the §6.7 skill row already shows that no adapter writes `ARTIFACT.md`. Traceability needs no §6.7 sentence, because `speccov-drift` accepts any section and the code already cites §4.3.4 (`pkg/adapter/claudecode.go:44`). A restatement that says "every other adapter" would also read as a second obligation on custom `HarnessAdapter` SPI implementations (§9.1), which SPEC-1 already carries. If a pointer for §6.7 readers is wanted later, the smallest form is one clause appended to the existing `none` sentence: "A harness adapter's `SKILL.md` carries the derived `compatibility` field (§4.3.4)."
- **A §7.8 sentence for marketplace output (dropped SPEC-3).** SPEC-1 already names "every marketplace emitter that writes one (§7.8)". The premise that §7.8 sets the emitters apart from materialization is false: §7.8 says `render` "materializes each harness's marketplace tree ... through the marketplace emitters" and calls the render "the materialization writer", and it sets apart only the project-files layout. `pkg/adapter/emitter.go:11-16` states the emitters reuse the §6.7 layout helpers. The proposed insertion point, the §7.8 sentence "both render the `SKILL.md` the harnesses consume", concerns Pi and Hermes only, so a sentence there would read as part of that aside although the Claude, Codex, and Cursor emitters also write `SKILL.md` (`pkg/adapter/emitter.go:139`, `:228`, `:267`). If OQ-2 narrows the scope, narrowing SPEC-1 handles it without a §7.8 edit.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-02, automated)

- **VER-1 fixture adds `sandbox_profile`, so the codex Tier C skill sync fails with `materialize.untranslatable`.** `UsedCapabilities` emits `sandbox_profile` for a skill (`pkg/adapter/capability.go:195-197`), the §6.7.1 cell is ✗ for codex and pi (`pkg/adapter/capability.go:97`; `spec/06-mcp-server.md:379`), and `podium sync` runs the §6.9 guard before `Adapt` (`pkg/sync/sync.go:290`), so the VER-1 fixture would fail the codex skill subtest through `syncProject`'s `t.Fatalf`. Fixed in VER-1 (the weather fixture declares `runtime_requirements` only and states why), TEST-1 (the shared input is `runtime_requirements` only with expected value `Requires Python >=3.10`, and the new subcase (e) checks the sandbox clause only for claude-code, cursor, opencode, and gemini), TEST-2 (the `team/hello` fixture drops `sandbox_profile`, the `none.golden` diff and the S4 golden line follow, because the golden test calls `Adapt` without the guard), the S2 and S7 checklist steps, a new Watch-out-for bullet, and a new edge-case row for a `sandbox_profile` skill synced to codex or pi.
