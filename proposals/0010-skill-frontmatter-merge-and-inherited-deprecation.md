# Proposal 0010: Skill frontmatter merge and inherited deprecation

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-19

This document stages no changes yet. It records two defects on the `extends:` read path that proposal 0009 removed from its open questions, with the evidence gathered for each and the decisions a reviewer has to take. It has not been through the adversarial review loop, so its staged changes are absent by design rather than by omission: the review loop writes them once the decisions below are settled.

Both defects share one property that is the reason for bundling them. Each is a disagreement between what `search_artifacts` reports for an artifact and what `load_artifact` reports for the same artifact, on the `extends:` path, and neither is caused by the indexing gap proposal 0009 closes. Fixing them together keeps one reviewer looking at one merge path.

## Defect 1: `mergeChain` empties a skill's description and serves it under its parent's name

### What happens

A skill's `name` and `description` live in `SKILL.md`; `ARTIFACT.md` omits them (§4.3.4). `manifestRecordFor` (`pkg/registry/ingest/ingest.go`) reads them out of `SKILL.md` into the record's `Name` and `Description` columns, with a comment stating that it does so precisely because the §4.7 projection needs them.

The read path does not know that. `mergeChain` folds the chain by parsing each record's stored `ARTIFACT.md` frontmatter through `parsedArtifact` (`pkg/registry/core/core.go`), which for a skill yields an empty `Description`, and then assigns `out.Description = merged.Description`. The assignment overwrites the column that held the authored value. The fallback inside `parsedArtifact` does not rescue it, because that branch runs only when `ParseArtifact` returns an error, and a valid skill frontmatter parses successfully with an empty description.

`Name` is worse. `out` starts as `chain[0]`, the root parent, and no later assignment restores the child's name, so the merged record carries the parent's.

Reproduced on 2026-08-19 by calling `mergeChain` over a two-record skill chain, each record carrying its own `SKILL.md`-derived columns and an `ARTIFACT.md` frontmatter that omits them:

```
merged Description = ""                (child column was "Pays an approved invoice")
merged Name        = "Invoice Base"    (child column was "Pay Invoice")
```

`search_artifacts` reads the column directly through `descriptorOf` and reports the correct description, so the two surfaces disagree for every skill that declares `extends:`.

### Scope

The `Description` half reaches every backend, because `description` is a persisted column on both SQL stores.

The `Name` half is memory-store only. The Postgres `manifests` table has no `name` column, so `rec.Name` is empty when read back and the parent's name cannot leak; on the memory store it round-trips and does leak. That is a §2.2 deployment-mode divergence on its own, and a reviewer should decide whether the fix is expected to make the two modes agree.

Serving the child under the parent's name is also reachable as a §4.6 "Hidden parents" violation: the requester may hold no access to the parent's layer and still sees the parent's name.

### Why it survived

Every skill fixture in `test/e2e/artifact_extends_test.go` uses an `ARTIFACT.md` that omits `name` and `description`, which is correct authoring, and no case asserts the served description against the `SKILL.md` value for a skill that also declares `extends:`.

### The decisions

1. Whether `mergeChain` falls back to the record's indexed `Name` and `Description` when the parsed frontmatter carries none, which is the skill case by construction, or whether the skill path is special-cased explicitly on `Type == skill`. The fallback is narrower to write and covers a non-skill record whose frontmatter failed to parse; the explicit branch says what it means.
2. Whether the merged record's `Name` takes the child's rather than `chain[0]`'s in every case, which is a fix independent of the skill question.
3. Whether §4.3.4 or §4.6 gains a sentence stating that a skill's `SKILL.md`-derived `name` and `description` participate in the §4.6 merge, or whether the existing §4.3.4 sentence already settles it and this is a code defect alone. The proposal pipeline needs this answered before any spec edit is staged.
4. Whether the fix is expected to close the memory-versus-Postgres divergence on `Name`, which would mean persisting a `name` column, or whether it is enough that neither backend surfaces the parent's name.

## Defect 2: an inherited `deprecated` flag is not resolved on the search path

### What happens

`mergeChain` inherits `deprecated` from the parent, so `load_artifact` reports a child of a deprecated parent as deprecated. The `search_artifacts` default-exclusion filter reads the stored column (`pkg/registry/core/core.go`), which the child never set, so the child keeps appearing in default results. The two surfaces disagree for that field.

### Scope is narrower than it appears

Deprecation is a per-version frontmatter field with no in-place toggle: `stampDeprecation` (`pkg/store/store.go`) records that deprecating an artifact means ingesting a new version with `deprecated: true`, which is a distinct record. §4.7.6 pins a child to an exact parent version at the child's ingest time.

A parent deprecated *after* a child is ingested therefore does not reach that child at all, because the child still pins the undeprecated version it resolved against. The disagreement arises only when a child is ingested against a parent version that was already deprecated at that moment.

### Why the obvious fix is unsafe

Folding the flag into the child's stored column is what proposal 0009 does for `description`, and it cannot be done here. `PutManifest` calls `stampDeprecation` on every backend, which anchors `DeprecatedAt` to `IngestedAt`, and `PurgeDeprecatedManifests` hard-deletes rows whose `DeprecatedAt` predates the §8.4 window. A folded child would therefore be hard-deleted ninety days after **its own** publication, with its author never having written `deprecated:`.

The existing guard does not cover it. `PurgeDeprecatedManifests` (`pkg/store/memory.go`) collects the versions pinned as an extends parent and skips those, which protects the pinned parent from being purged out from under a live child. A folded child is not itself pinned, so nothing shields it.

### The decisions

1. Whether ingest refuses an `extends:` against an already-deprecated parent version, which closes the case rather than resolving it, needs no store change, and matches how ingest already refuses an absent parent with `ingest.invalid_artifact`. §4.6 and §4.7.6 are silent, so this is a spec question first.
2. If it is resolved rather than refused, whether the search filter resolves the flag per candidate, which reintroduces a store read the 0009 design removed, or whether the flag is persisted alongside a way for the purge to tell inherited deprecation from authored deprecation. The second needs a new column or convention and a §8.4 amendment.
3. Whether a child already stored against a deprecated parent is repaired, or left as proposal 0009 leaves its own affected rows.

## Non-goals

- Any change to the ingest fold, the search descriptor, or the §4.7 projection collapse. Those are proposal 0009 and land independently.
- Any change to the §8.4 window itself, or to the purge's existing extends-pin guard for parents.

## Relationship to proposal 0009

Proposal 0009 closes the indexing gap for an inherited `description` and states in §4.6 that an omitted or empty child scalar inherits. This proposal takes the two questions 0009 removed from its open list. Neither blocks the other: 0009 changes the ingest write path and the search result loop, and this proposal changes the `load_artifact` merge path and, depending on decision 1 of defect 2, the ingest extends-validation path.

Sequencing matters in one direction only. If 0009 lands first, the §4.6 omitted-field sentence it stages is already in the spec, and defect 1's decision 3 is a question about `SKILL.md`-derived fields against that sentence rather than against the older text.

## Next step

The decisions above are the input to a `change-proposal` run in review mode over this file, which stages the edits and converges them. Running it before the decisions are taken would spend the run rediscovering them.
