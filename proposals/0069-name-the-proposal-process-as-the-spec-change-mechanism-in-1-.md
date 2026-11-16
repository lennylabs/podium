# Proposal 0069: Name the proposal process as the spec-change mechanism in §1.6 and the governance pages, and remove the unused RFC process

- Issue: (to be filed)
- Status: Approved (2026-11-16). Verified after 6 adversarial review rounds (4 findings fixed).
- Date: 2026-11-16

This document stages the proposed spec, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change alters no product behavior. It removes one published documentation route, `/rfc/README.md`, and the CHANGELOG records the removal under Documentation.

## Summary

**What changes.**

- **Spec §1.6 (SPEC-1).** The Governance bullet names "a proposal process for spec changes" in place of the RFC process and keeps `GOVERNANCE.md` as the pointer.
- **Governance pages (DOC-1, DOC-2).** `GOVERNANCE.md` and `docs/about/governance.md` describe the proposal process: the numbered files in `proposals/`, what a proposal stages, the `Status` stages from `Draft` through `Implemented`, `Withdrawn`, and how a proposal lands. Every RFC mention on both pages is removed.
- **The RFC index (DOC-3).** `docs/rfc/README.md` is deleted, which removes `docs/rfc/`, and the `/rfc/README.md` line leaves `site/test/fixtures/published-routes.json` in the same commit.
- **Remaining RFC mentions (DOC-4).** `docs/getting-started/why-podium.md`, `.github/ISSUE_TEMPLATE/config.yml`, and `.claude/workflows/docs-audit-pass-2.js` drop their project-process RFC wording or their reference to the deleted page.
- **The §1.6 conformance test (TEST-1).** `test/conformance/governance_docs_test.go` is rewritten to pin the proposal process and the `proposals/` directory, and both tests keep their `// Spec: §1.6` citation.
- **CHANGELOG (DOC-5).** A Documentation entry under `[Unreleased]` records the governance text change and the removed page.

**Fixed decisions.**

- D-1: §1.6 reads "Maintainer model and a proposal process for spec changes; see `GOVERNANCE.md`." It names no repository directory and no agent skill.
- D-2: the two governance pages carry the full process description with the same facts. `GOVERNANCE.md` links `proposals/` relatively, and the docs page links the GitHub tree URL.
- D-3: neither governance page names the `change-proposal` or `implement-proposal` skills or links `.claude/`.
- D-4: every project-process RFC mention is removed. "RFC reviews" becomes "proposal reviews". IETF RFC citations and the Cloudflare agent-skills-discovery-rfc row in `docs/getting-started/why-podium.md` stay.
- D-5: `docs/rfc/README.md` is deleted with no redirect and no `proposals/README.md` replacement.
- D-6: GitHub Discussions and issues remain the venue for raising a problem before a proposal exists, and the Discussion and steering-committee voting bullets are unchanged. A decision's rationale is recorded in the proposal's Decisions section, and the `Status` bullet records the state and the reason for a withdrawal. The lifecycle has no rejected state; a proposal that is not adopted is `Withdrawn`.
- D-7: the rewritten test pins the new §1.6 phrase, the proposal-process phrases on both pages, and the `proposals/` link and directory. It bans only the retired phrases "RFC process" and "docs/rfc" on the pages.
- D-8: S1 commits the spec edit alone and leaves the old test red; S2 restores a green tree in one commit.
- D-9: the change is a documentation and test fix with no product behavior change.

**Watch out for.**

- **The spec commit turns the suite red.** `TestGovernanceDocs_UseRFCProcessTerminology` anchors on the old §1.6 text and fails with `Fatalf` once S1 lands. The spec must still land first and alone (`.claude/skills/implement-proposal/SKILL.md`, the spec-first constraint), and S2 is the first green commit after it.
- **The site route fixture moves with the deletion.** `site/test/routes.test.ts` ("still emits every route the previous build published") fails when a route listed in `site/test/fixtures/published-routes.json` disappears. Deleting `docs/rfc/README.md` without removing its fixture line breaks `npm test` in `site/`.
- **The docs page renders from the docs tree.** A relative `proposals/` link on `docs/about/governance.md` resolves to `docs/about/proposals/`, which does not exist, and the repository-root `proposals/` lies outside the tree the site publishes. Use the `https://github.com/lennylabs/podium/tree/main/proposals` URL there, as the page already does for `LICENSE`.
- **`.DS_Store` and other untracked files can appear in `proposals/`.** The directory test checks only `.md` entries against the numbering pattern, so a Finder-created file on macOS does not fail it.
- **The acceptance grep matches the negative guards.** The rewritten test and the new CHANGELOG entry contain "RFC process" or "docs/rfc" on purpose. The S4 grep excludes both files, and neither is edited to clear it.
- **Historical text stays.** Earlier proposals, among them `proposals/0007-react-documentation-site.md`, name `docs/rfc/README.md`. They are a record of their time and are excluded from the grep.
- **Line numbers drift.** Anchor each edit by its quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The §1.6 Governance bullet names a proposal process for spec changes. This commit leaves `TestGovernanceDocs_UseRFCProcessTerminology` failing until S2.
      Levels: conformance. Depends on: —
- [ ] **S2 · test** — TEST-1, DOC-1, DOC-2, DOC-3. The rewritten conformance test, both governance pages, the deletion of `docs/rfc/README.md`, and the fixture line land in one commit, because the new test passes only when both pages and the spec are present and the site fixture must move with the deletion. Separating them leaves a red commit at each split.
      Levels: conformance, unit (the `site/` suite). Depends on: S1
- [ ] **S3 · docs** — DOC-4. `why-podium.md`, the issue-template contact link, and the docs-audit workflow page list drop their RFC wording or their reference to the deleted page.
      Levels: unit (the `site/` suite). Depends on: S2
- [ ] **S4 · docs** — DOC-5. The CHANGELOG Documentation entry lands, and the acceptance grep and `make coverage-gate` run over the finished tree.
      Levels: conformance, unit (the `site/` suite, whose build reads `CHANGELOG.md` for the changelog page). Depends on: S2, S3

## Current state and the gap

### §1.6 names a process the project does not use

§1.6 Project Model (`spec/01-overview.md:115`) states:

> - **Governance.** Maintainer model + RFC process for spec changes; see `GOVERNANCE.md`.

No other text in `spec/` names a project RFC process. Every other RFC mention in `spec/` cites an IETF standard by number.

### The process the project uses

Spec changes go through the proposal pipeline that `.claude/rules/spec-driven-development.md` describes ("When the spec is silent, wrong, or contradictory"). A numbered proposal under `proposals/` (`NNNN-<slug>.md`, `0001` through `0068` today) stages the spec edits and the code, test, and documentation changes that follow from them. The proposal goes through adversarial review, and a human approves it by setting its `Status` bullet to `Approved`. `implement-proposal` then lands and verifies the spec edits in their own commits before any code is written.

Each proposal carries `- Issue:`, `- Status:`, and `- Date:` header bullets (`proposals/0068-carry-a-skill-s-string-valued-podium-fields-under-skill-md-m.md`). The `Status` states are `Draft`, `Verified`, `Approved`, `Applied to spec`, and `Implemented` (`.claude/skills/change-proposal/SKILL.md`, the status lifecycle convention; `.claude/skills/implement-proposal/SKILL.md`, the preconditions and the spec-first constraint), plus `Withdrawn` (`proposals/0062-resolve-a-same-id-extends-overlay-s-parent-from-the-nearest-.md`). Each status carries free text after the state word. Decision rationale lives in each proposal's Decisions section and in its "Resolved in adversarial review" section, and only a `Withdrawn` status carries a reason.

The lifecycle is the normal sequence rather than a fixed one. All but one proposal carry `- Issue: (to be filed)`; only `proposals/0001-server-side-request-authentication.md` names an issue. Proposals 0007, 0011, and 0053 stage no spec amendment (`proposals/0007-react-documentation-site.md:6`, `proposals/0011-merge-chain-builds-the-served-record-from-the-root-parent.md:7`, and `proposals/0053-split-a-git-layer-s-url-credential-from-repo-at-the-store-bo.md:7`), and 0007 reached `Implemented` without a `Verified` status. Proposals 0003, 0004, 0005, and 0025 sit at `Applied to spec` on `main`. Current practice lands a proposal on one branch with its spec and implementation commits and merges them in one pull request (proposal 0068 in commits `1393573f` and `6cb35f8e`, merged in `4ee474b6`, PR #185).

### The RFC index and the pages that repeat the claim

`docs/rfc/` has only ever held `README.md`. That file describes an RFC format, numbering, and a draft, accepted, rejected, and withdrawn lifecycle that no file has used. Commit `b8528688` created it so the docs would match the §1.6 wording. The governance pages repeat the claim:

- `GOVERNANCE.md:12` and `docs/about/governance.md:20` make "RFC reviews" a steering-committee criterion.
- `docs/about/governance.md:9` names "the RFC process for spec changes" in the page introduction.
- `GOVERNANCE.md:19` and `docs/about/governance.md:31` say spec changes follow an RFC process with documents in `docs/rfc/`.
- `GOVERNANCE.md:21` and `docs/about/governance.md:33` say proposals are filed as GitHub Discussions or draft RFCs in `docs/rfc/`.
- `GOVERNANCE.md:23` and `docs/about/governance.md:35` say decisions are recorded "in the relevant RFC or issue".

The frontmatter description of `docs/about/governance.md` already says "the proposal process", so the page body contradicts its own summary.

The claim also appears outside the governance pages. `docs/getting-started/why-podium.md:304` says "Maintainer model with an RFC process for spec changes", and `.github/ISSUE_TEMPLATE/config.yml:5` describes Discussions as hosting "RFC-style design conversations".

### Tests and tooling that pin the RFC text

`test/conformance/governance_docs_test.go` holds the repository's only `// Spec: §1.6` citations:

- `TestGovernanceDocs_UseRFCProcessTerminology` requires the spec substring "RFC process for spec changes; see `GOVERNANCE.md`" (`Fatalf`) and "RFC process" on both pages.
- `TestGovernanceDocs_RFCDirectoryExists` requires `GOVERNANCE.md` to mention `docs/rfc/`, requires `docs/rfc` to be a directory, and requires `docs/rfc/README.md` to contain "RFC", "Numbering", and "Format".

`site/test/fixtures/published-routes.json:118` lists `"/rfc/README.md"`, and `site/test/routes.test.ts` fails when a listed route disappears. `.claude/workflows/docs-audit-pass-2.js:74` lists `docs/rfc/README.md` in its operations-and-meta page group.

The maintainer wants every project-process RFC mention removed rather than renamed into a synonym, and the spec, governance pages, test, and tooling to name the proposal process.

## Decisions

- **D-1. §1.6 wording.** §1.6 names the proposal process and keeps `GOVERNANCE.md` as the pointer. It names no repository directory and no agent skill. The spec names no source or tooling paths, and §1.6 already describes the reference registry without a path. Leaving `proposals/` and the skills out of §1.6 keeps the apply loop from rephrasing or dropping the edit and keeps the test anchor to one phrase. The `+` becomes `and` per `.claude/rules/doc-style.md`.
- **D-2. Where the process is described.** `GOVERNANCE.md` and `docs/about/governance.md` hold the full description: the `proposals/` location, the `NNNN-<slug>.md` numbering, what a proposal stages, the rule that a proposal does not edit `spec/` itself, the `Status` stages, `Withdrawn`, and how a proposal lands. The pages carry the same facts in their own register. `GOVERNANCE.md` links `proposals/` relatively, and the docs page links the GitHub tree URL the way it already links `LICENSE`.
- **D-3. No tooling names.** Neither governance page names the `change-proposal` or `implement-proposal` skills or links `.claude/`. The pages describe the stages a proposal passes through, and the skills are one way to carry them out.
- **D-4. Removal scope.** Every project-process RFC mention is removed. The steering criterion "RFC reviews" becomes "proposal reviews" (`GOVERNANCE.md:12`, `docs/about/governance.md:20`). The removal also covers the docs page introduction, `why-podium.md:304`, and `.github/ISSUE_TEMPLATE/config.yml:5`. External RFC references stay: IETF RFC citations and the Cloudflare agent-skills-discovery-rfc row at `why-podium.md:297`.
- **D-5. The RFC index.** `docs/rfc/README.md` is deleted, and with it `docs/rfc/`. No `proposals/README.md` replaces it, because the governance pages carry the format and lifecycle. No redirect is added. The page has no frontmatter and is published as a copied static `.md` route with no navigation entry (proposal 0007, the corpus scan), and `docs/_redirects.yaml` has no `rfc` entry. The `"/rfc/README.md"` fixture line is removed in the same commit as the deletion.
- **D-6. Venues and records.** GitHub Discussions and issues remain the venue for raising a problem and for open discussion before a proposal exists. The Discussion bullet and the steering-committee voting bullet are unchanged. The Proposals bullet and the Decisions bullet are rewritten. A decision's rationale is recorded in the proposal's Decisions section, and the `Status` bullet records whether the proposal was approved, implemented, or withdrawn, with the reason for a withdrawal. A proposal that is not adopted is set to `Withdrawn` and stays in `proposals/`. The lifecycle defines no rejected state, so the pages name none.
- **D-7. Rewrite the §1.6 test.** `test/conformance/governance_docs_test.go` is rewritten to pin the proposal process. Both tests keep a `// Spec: §1.6` citation, because they are the repository's only §1.6 citations. No gate fails when they go: `speccov drift` flags only citations of missing sections, and `speccov uncovered` is non-blocking (`.github/workflows/spec-coverage.yml`). The spec-driven-development rule still requires the citation. The rewrite drops the `docs/adr/` and draft-ADR guards, which protected against text that no longer exists. On the pages it bans the retired phrases "RFC process" and "docs/rfc" and leaves a later IETF RFC citation possible.
- **D-8. Commit order.** `implement-proposal` commits the §1.6 edit on its own first. That commit leaves `TestGovernanceDocs_UseRFCProcessTerminology` failing at its spec anchor, because a test change cannot precede the spec change without code leading the spec. S2 makes the tree green again in one commit: the test rewrite, both governance pages, the deletion, and the fixture edit. The new test passes only when all of them are present, and the fixture has to move with the deletion. The remaining text edits follow. No commit after S2 is red.
- **D-9. Release impact.** The change is a documentation and test fix with no product behavior change. It needs no MINOR-bump justification beyond the CHANGELOG Documentation entry, which records that the `/rfc/README.md` page is gone.

## Spec amendment: §1.6 Project Model, the Governance bullet

**SPEC-1.** Anchor: in `spec/01-overview.md`, under the heading `## 1.6 Project Model`, the bullet that begins `- **Governance.**`.

Replace:

> - **Governance.** Maintainer model + RFC process for spec changes; see `GOVERNANCE.md`.

With:

> - **Governance.** Maintainer model and a proposal process for spec changes; see `GOVERNANCE.md`.

No other §1.6 bullet changes. No other spec file names a project RFC process, so this is the only spec edit.

## Proposed solution

The proposal stages no product code. Its one code change is the conformance test.

### TEST-1. Rewrite `test/conformance/governance_docs_test.go`

Replace the file with the following. `repoRoot` and `readRepoFile` keep their current bodies. The imports gain `regexp`. Format with `gofmt` and `goimports` before committing.

```go
// Package conformance holds the cross-cutting suites that any built-in
// or community implementation runs against. This suite verifies that the
// governance documentation matches the project-model claim in spec §1.6:
// the proposal process for spec changes and the proposals/ directory that
// GOVERNANCE.md points readers to.
package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the absolute path to the repository root, derived from
// this test file's location so the test runs from any working directory.
func repoRoot(t testing.TB) string {
	// unchanged
}

func readRepoFile(t testing.TB, rel string) string {
	// unchanged
}

// governancePages are the pages §1.6 points readers to: GOVERNANCE.md and
// its published counterpart on the documentation site.
var governancePages = []string{
	"GOVERNANCE.md",
	filepath.Join("docs", "about", "governance.md"),
}

// proposalFileName matches a numbered proposal file, NNNN-<slug>.md.
var proposalFileName = regexp.MustCompile(`^\d{4}-[a-z0-9-]+\.md$`)

// Spec: §1.6 Project Model — "Governance. Maintainer model and a proposal
// process for spec changes; see GOVERNANCE.md." The pages the spec points to
// must describe the proposal process, including the approval state and the
// state in which the spec edits have landed. The retired RFC process and its
// docs/rfc/ directory must not reappear on them.
func TestGovernanceDocs_DescribeProposalProcess(t *testing.T) {
	t.Parallel()

	spec := readRepoFile(t, filepath.Join("spec", "01-overview.md"))
	if !strings.Contains(spec, "proposal process for spec changes; see `GOVERNANCE.md`") {
		t.Fatalf("spec §1.6 governance claim changed; update this conformance test to match")
	}

	for _, rel := range governancePages {
		body := readRepoFile(t, rel)
		for _, want := range []string{"proposal process", "proposals/", "`Approved`", "Applied to spec"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not contain %q; spec §1.6 requires it to describe the proposal process", rel, want)
			}
		}
		for _, retired := range []string{"RFC process", "docs/rfc"} {
			if strings.Contains(body, retired) {
				t.Errorf("%s contains the retired phrase %q; spec §1.6 names the proposal process", rel, retired)
			}
		}
	}
}

// Spec: §1.6 Project Model — GOVERNANCE.md directs contributors to the
// proposals/ directory. The link must resolve to a directory of numbered
// proposal files. Only .md entries are checked, so an untracked file such as
// a Finder-created .DS_Store does not fail the test.
func TestGovernanceDocs_ProposalDirectoryExists(t *testing.T) {
	t.Parallel()

	governance := readRepoFile(t, "GOVERNANCE.md")
	if !strings.Contains(governance, "](proposals/)") {
		t.Fatalf("GOVERNANCE.md does not link proposals/; spec §1.6 points readers there for the proposal process")
	}

	entries, err := os.ReadDir(filepath.Join(repoRoot(t), "proposals"))
	if err != nil {
		t.Fatalf("read proposals/: %v", err)
	}
	numbered := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		if !proposalFileName.MatchString(e.Name()) {
			t.Errorf("proposals/%s does not follow the NNNN-<slug>.md numbering GOVERNANCE.md describes", e.Name())
			continue
		}
		numbered++
	}
	if numbered == 0 {
		t.Fatalf("proposals/ holds no NNNN-<slug>.md file")
	}
}
```

The `// unchanged` bodies above stand for the current bodies of `repoRoot` and `readRepoFile`, which the implementor keeps verbatim.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| A bookmark or an external link to the published `/rfc/README.md` page | The documentation site answers not found. No redirect is added (D-5). | No spec text. The CHANGELOG Documentation entry (DOC-5) states that the page is removed, and `docs/about/changelog.md` renders it once the release is cut. |
| A proposal that stages no spec edit | It skips `Applied to spec` and moves from `Approved` to `Implemented`. | No spec text; §1.6 names only the process. `GOVERNANCE.md` and `docs/about/governance.md`: "A proposal that stages no spec edit skips this stage." (DOC-1, DOC-2) |
| A proposal that departs from the normal sequence, such as one that skipped `Verified` (0007) or that stays at `Applied to spec` (0004) | Its `Status` bullet records the state it reached. The governance pages describe the stages as the normal sequence. | Both governance pages: "which normally moves through these stages" and "normally lands on the same branch" (DOC-1, DOC-2). |
| A proposal written without an issue | Its `Issue` bullet reads `(to be filed)`. | Both governance pages: "a proposal's `Issue` bullet links the issue when one exists" (DOC-1, DOC-2). |
| A proposal that is not adopted | Its status is set to `Withdrawn` with the reason, and the file stays in `proposals/`. No rejected state exists. | Both governance pages (DOC-1, DOC-2). |
| Earlier proposals that name `docs/rfc/README.md`, such as `proposals/0007-react-documentation-site.md` | They keep the reference as a record of their time. The S4 grep excludes `proposals/`. | Non-goals of this proposal. No spec or docs page. |
| A later edit that reintroduces "RFC reviews" or "draft RFC" on a governance page | The conformance test passes, because it bans only "RFC process" and "docs/rfc" (D-7). The S4 grep catches it only when it runs. | Accepted. The test's doc comment names the phrases it bans. |
| An untracked non-markdown file in `proposals/`, such as `.DS_Store` | `TestGovernanceDocs_ProposalDirectoryExists` ignores it. A misnamed `.md` file fails the test. | The test's doc comment (TEST-1). |

## Testing

Both tests are conformance tests in `test/conformance/governance_docs_test.go`, the level the change reaches. The change touches no `pkg/`, `cmd/`, or `internal/` code, so it adds no line-coverage obligation.

- **`TestGovernanceDocs_DescribeProposalProcess`** (`// Spec: §1.6`). It pins the new §1.6 phrase, requires "proposal process", "proposals/", "`Approved`", and "Applied to spec" on both pages, and fails when either page contains "RFC process" or "docs/rfc". Its non-happy paths are a §1.6 edit that drops the phrase (the `Fatalf`), a page that lacks one required phrase, and a page that reintroduces a retired phrase.
- **`TestGovernanceDocs_ProposalDirectoryExists`** (`// Spec: §1.6`). It requires the `](proposals/)` link in `GOVERNANCE.md`, reads `proposals/`, fails on a `.md` entry outside the `NNNN-<slug>.md` pattern, ignores non-markdown entries, and fails on an empty directory. Its boundary cases are a directory with no numbered file and a stray non-markdown file.

**IMPLEMENTOR'S CHOICE:** the mutations used to check the guards during S2 — show that each negative path of both tests fails for its stated reason (a retired phrase on a governance page, a missing required phrase, and a misnamed `.md` file in `proposals/`) and that a non-markdown file in `proposals/` passes. Apply each mutation alone against an otherwise-final S2 tree, revert it before applying the next, and commit none of them.

Commands per step:

- S1: `go test ./test/conformance/ -run TestGovernanceDocs -v` fails at the spec anchor of `TestGovernanceDocs_UseRFCProcessTerminology`. This failure is expected.
- S2: `go test ./test/conformance/ -run TestGovernanceDocs -v` passes, and `npm test` in `site/` passes, including `site/test/routes.test.ts`.
- S3: `npm test` and `npm run check` in `site/` pass.
- S4: `git grep -n -i -E 'rfc process|docs/rfc|draft rfc|rfc review|rfc-style' -- . ':!proposals/' ':!CHANGELOG.md' ':!test/conformance/governance_docs_test.go'` returns no hit. In `CHANGELOG.md`, the only hit is the new Documentation entry under `[Unreleased]`. In `test/conformance/governance_docs_test.go`, the only hits are the negative guards that assert the RFC text and `docs/rfc/` are absent. `make coverage-gate` passes, and `npm test` in `site/` still passes.

The change alters no consumer or operator surface of the registry, the CLI, or the MCP server, so it stages no `test/manual-validation.md` scenario.

## Non-goals

- Correcting the statement in `.claude/rules/spec-driven-development.md` that `speccov-drift` fails when a section loses its last citation. `tools/speccov/main.go` does not do this, and the inaccuracy is a separate tooling-rule defect.
- Changing IETF RFC citations anywhere (`spec/`, docs, code, tests, and SDKs), the `Makefile` RFC 8628 comment, or the external Cloudflare agent-skills-discovery-rfc row in `docs/getting-started/why-podium.md`.
- Editing historical text: earlier proposals (for example `proposals/0007-react-documentation-site.md`, which mentions `docs/rfc/README.md`), released CHANGELOG sections, or commit `b8528688`.
- Adding a `proposals/README.md` index, a redirect for `/rfc/README.md`, or a navigation entry. The governance pages carry the format and lifecycle.
- Naming the `change-proposal` or `implement-proposal` skills, or the `.claude/` directory, in §1.6 or in either governance page.
- Changing the BDfN model, the steering-committee criteria beyond the RFC wording, the voting rule, the license and DCO sections, `CONTRIBUTING.md`, or `docs/about/contributing.md`. The last two only link the governance pages.
- Changing the `change-proposal` or `implement-proposal` skills, the `Status` lifecycle they write, or the guard hook.
- Deleting the §1.6 tests outright. They are the repository's only §1.6 citations, and the spec-driven-development rule requires every behavioral section to keep one.
- Banning every "RFC" string on the governance pages. Such a ban encodes no §1.6 requirement and would fail a later IETF RFC citation on those pages.
- Asserting in the test that `docs/rfc` does not exist. The assertion encodes no §1.6 claim, and the deletion is already covered by the fixture change and the S4 grep.

## Documentation changes

### DOC-1. `GOVERNANCE.md` describes the proposal process

Edit 1. Replace the steering-committee criterion:

```markdown
- Participated in architectural discussions or RFC reviews, and
```

With:

```markdown
- Participated in architectural discussions or proposal reviews, and
```

Edit 2. Replace the entire body of `## Decision-making` (`GOVERNANCE.md:19-24` today), from the paragraph that begins "Spec changes follow an RFC process." through the bullet that begins "- After the steering committee is formed", which is everything between the heading and `## License and DCO`, with the following. The block reproduces the "- **Discussion**" bullet and the "- After the steering committee is formed" bullet with their current text, so each appears once after the edit.

```markdown
Spec changes follow a proposal process. A proposal is a markdown file in [`proposals/`](proposals/) named `NNNN-<slug>.md`, where `NNNN` is the next free four-digit number. A proposal states the problem, the decisions that constrain the design and their rationale, and the changes it stages. Those changes are spec edits, code, tests, and documentation, or any combination of them. A proposal does not edit `spec/` itself.

The proposal's `Status` bullet records its state, which normally moves through these stages:

1. **Draft.** The proposal is written.
2. **Verified.** Adversarial review rounds check the proposal against the spec and the code until a complete review pass finds no confirmed defect.
3. **Approved.** A maintainer records sign-off by setting the status to `Approved`.
4. **Applied to spec.** The staged spec edits land in `spec/`, are verified against the proposal, and are committed on their own before any code that depends on them. A proposal that stages no spec edit skips this stage.
5. **Implemented.** The code, tests, and documentation are implemented against the updated spec.

A proposal that is not adopted is set to `Withdrawn`, with the reason in its status, and stays in `proposals/`. A proposal file normally lands on the same branch as its spec and implementation commits and merges to `main` through a pull request.

- **Proposals** are written as numbered files in [`proposals/`](proposals/). A problem can be raised first as an issue or a GitHub Discussion, and a proposal's `Issue` bullet links the issue when one exists.
- **Discussion** happens in the open, and anyone can participate.
- **Decisions** are recorded with their rationale in the proposal's Decisions section. The `Status` bullet records whether the proposal was approved, implemented, or withdrawn, and gives the reason for a withdrawal.
- After the steering committee is formed, consensus is the preferred mode, and simple-majority voting is the fallback.
```

After the edit, `GOVERNANCE.md` contains no "RFC" string.

### DOC-2. `docs/about/governance.md` describes the proposal process

Edit 1. In the introduction paragraph under `# Governance`, replace "and the RFC process for spec changes." with "and the proposal process for spec changes." The rest of the paragraph and the frontmatter are unchanged.

Edit 2. Replace the criterion:

```markdown
- Participated in architectural discussions or RFC reviews.
```

With:

```markdown
- Participated in architectural discussions or proposal reviews.
```

Edit 3. Replace the entire body of `## Decision-making` (`docs/about/governance.md:31-36` today), from the paragraph that begins "Spec changes follow an RFC process." through the bullet that begins "- After the steering committee forms", which is everything between the heading and `## License and DCO`, with the following. The block reproduces the "- **Discussion**" bullet and the "- After the steering committee forms" bullet with their current text, so each appears once after the edit.

```markdown
Spec changes follow a proposal process. A proposal is a markdown file in [`proposals/`](https://github.com/lennylabs/podium/tree/main/proposals) named `NNNN-<slug>.md`, where `NNNN` is the next free four-digit number. A proposal states the problem, the decisions that constrain the design and their rationale, and the changes it stages. Those changes are spec edits, code, tests, and documentation, or any combination of them. A proposal does not edit `spec/` itself.

The proposal's `Status` bullet records its state, which normally moves through these stages:

1. **Draft.** The proposal is written.
2. **Verified.** Adversarial review rounds check the proposal against the spec and the code until a complete review pass finds no confirmed defect.
3. **Approved.** A maintainer records sign-off by setting the status to `Approved`.
4. **Applied to spec.** The staged spec edits land in `spec/`, are verified against the proposal, and are committed on their own before any code that depends on them. A proposal that stages no spec edit skips this stage.
5. **Implemented.** The code, tests, and documentation are implemented against the updated spec.

A proposal that is not adopted is set to `Withdrawn`, with the reason in its status, and stays in `proposals/`. A proposal file normally lands on the same branch as its spec and implementation commits and merges to `main` through a pull request.

- **Proposals** are written as numbered files in [`proposals/`](https://github.com/lennylabs/podium/tree/main/proposals). A problem can be raised first as an issue or a GitHub Discussion, and a proposal's `Issue` bullet links the issue when one exists.
- **Discussion** happens in the open, and anyone can participate.
- **Decisions** are recorded with their rationale in the proposal's Decisions section. The `Status` bullet records whether the proposal was approved, implemented, or withdrawn, and gives the reason for a withdrawal.
- After the steering committee forms, consensus is the preferred decision mode, and a simple-majority vote is the fallback.
```

After the edit, the page contains no "RFC" string and does not link `.claude/` or name the skills.

### DOC-3. Delete `docs/rfc/README.md` and its route fixture line

Run `git rm docs/rfc/README.md`. It is the only tracked file in `docs/rfc/`, so the directory goes with it. In `site/test/fixtures/published-routes.json`, delete the line `  "/rfc/README.md",` and keep the surrounding entries and the JSON valid. Alternatively, regenerate the fixture with `node site/test/fixtures/capture-routes.mjs docs > site/test/fixtures/published-routes.json` and confirm the diff is exactly that one line. Add no redirect entry.

### DOC-4. Remove the remaining project-process RFC mentions

`docs/getting-started/why-podium.md`, under `## Project model`, replace the Governance bullet with:

```markdown
- **Governance.** Maintainer model with a proposal process for spec changes. See [Governance](../about/governance).
```

Leave the Agent Skills discovery index row, which links the external Cloudflare RFC, unchanged.

`.github/ISSUE_TEMPLATE/config.yml`, the `about:` line of the "Questions and design discussions" entry, becomes:

```yaml
    about: Open-ended questions, design conversations ahead of a spec proposal, and general feedback.
```

The security entry is unchanged.

`.claude/workflows/docs-audit-pass-2.js`, in the `operations-and-meta` group's `pages` string, remove `, docs/rfc/README.md` from the end, so the string ends `... docs/about/changelog.md, docs/reference/index.md'`.

### DOC-5. CHANGELOG Documentation entry

Under `## [Unreleased]`, add a `### Documentation` group after `### Fixed`, which follows the group order of the 0.6.0 section. If the group already exists when S4 runs, append to it. Add this entry:

```markdown
- **Governance names the proposal process for spec changes** (§1.6): `GOVERNANCE.md` and the Governance page describe the numbered proposals under `proposals/`, their review and approval states, and the rule that spec edits land before the code that depends on them. The unused `docs/rfc/` index and its published page are removed.
```

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft challenge (2026-11-16)

- **TEST-1 scope.** The draft test repeated the §1.6 anchor with a separate "no RFC on the Governance line" check, banned every "RFC" string on the pages, asserted that `docs/rfc` does not exist, and told the implementor to fall back to a prefix check if the numbering pattern failed. The staged test drops the repeated check and the existence assertion, bans only "RFC process" and "docs/rfc", and keeps the pattern, which matches every current entry. It checks only `.md` entries, so an untracked `.DS_Store` on macOS does not fail it.
- **Governance page facts.** The draft pages stated that proposals start from an issue or a Discussion, that decision rationale lives in the `Status` bullet, that a rejected proposal stays in `proposals/`, and that every proposal edits the spec. Proposals almost all carry `Issue: (to be filed)`, rationale lives in the Decisions section, no rejected state exists, and several proposals stage no spec edit. DOC-1 and DOC-2 now state the issue link as optional, place rationale in the Decisions section, name only `Withdrawn`, add the skip sentence for a proposal with no spec edit, and describe the stages and the one-branch landing as the normal sequence. The DOC-2 challenge sketch kept "rationale in the proposal's `Status` bullet" in its Decisions bullet, which contradicts the DOC-1 finding, so both pages carry the DOC-1 wording, and D-6 states the same rule.
- **Verified stage.** The draft said review stops when a round finds no new defect. Convergence requires a complete review pass with no confirmed finding, and the staged text says so.
- **Acceptance grep.** The draft S3 grep, excluding only `proposals/` and released CHANGELOG sections, matched the rewritten test's negative guards and the new `[Unreleased]` entry. The S4 grep now excludes both files and states the hits each is allowed to carry.

### Pass 1 (2026-11-16, automated)

- **Proposal 0010 cited as staging no spec edit.** "The process the project uses" listed 0010 among the proposals that stage no spec amendment, but 0010 stages SPEC-1 and SPEC-2 (`proposals/0010-refuse-an-extends-pin-onto-a-deprecated-parent.md:48`), and spec commit `b2fb4f31` landed them. The list now names 0007, 0011, and 0053, each with the line that states it stages no spec edit. The skip sentence on the governance pages and the edge-case row are unchanged, because those three proposals still exercise the path.
- **Decision-making replacement range.** DOC-1 Edit 2 and DOC-2 Edit 3 ended the replaced range at the Decisions bullet, while the staged blocks end with the steering-committee bullet, so a literal application left that bullet twice. Both edits now replace the entire section body through the steering-committee bullet, everything between `## Decision-making` and `## License and DCO` (`GOVERNANCE.md:19-24`, `docs/about/governance.md:31-36`), and state that the block reproduces the Discussion and steering-committee bullets with their current text.

### Pass 2 (2026-11-16, automated)

- **S2 guard check ordering.** The Testing section added `proposals/notes.md` and then expected `TestGovernanceDocs_ProposalDirectoryExists` to pass after adding `proposals/.DS_Store`, but `notes.md` was still present and fails the `proposalFileName` check, so the step gave a false red. The procedure now removes `proposals/notes.md` before adding `proposals/.DS_Store`, then removes `.DS_Store`.
- **S2 page restore ordering.** The corrected procedure still kept the reverted governance page in place through the `proposals/` checks. When the reverted page is `GOVERNANCE.md`, its RFC-text Decision-making section has no `](proposals/)` link, because both staged links sit in that section (DOC-1 Edit 2), so `TestGovernanceDocs_ProposalDirectoryExists` stops at the link `Fatalf` before it reads the directory. The `notes.md` check then fails with the link message, and the `.DS_Store` check fails instead of passing. The procedure now restores the reverted page immediately after the `TestGovernanceDocs_DescribeProposalProcess` check and before it adds `proposals/notes.md`.

### Prune (2026-11-16)

- **S2 guard procedure.** The Testing section scripted the S2 guard checks as one sequence of reverts, file additions, and removals. Each step interacted with the state the previous step left, and both Pass 2 findings were ordering defects in that script. The script is replaced by an IMPLEMENTOR'S CHOICE blank whose constraint requires each negative path of both tests to fail for its stated reason, requires a non-markdown file in `proposals/` to pass, and requires each mutation to run alone against an otherwise-final S2 tree. The tests' doc comments name the paths, so the constraint carries what the script carried. The Pass 2 bullets above remain as the record of the defects.
- **Applied-to-spec example.** "The process the project uses" named only proposal 0004 at `Applied to spec`. It now names 0003, 0004, 0005, and 0025, every proposal at that status on `main`, so the current-state statement is complete. The edge-case row keeps 0004 as its example.
