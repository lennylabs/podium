# Proposal 0010: Refuse an extends pin onto a deprecated parent version

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-19

This document stages no changes yet. It records one defect, its reachability, and the resolution options, so a review run stages the edits rather than rediscovering the analysis. It has not been through the adversarial review loop.

An earlier draft of this file bundled a second defect on the same read path. That defect was misdiagnosed and the bundle is dropped; [proposal 0011](0011-merge-chain-builds-the-served-record-from-the-root-parent.md) carries what survived of it.

## The defect

`mergeChain` inherits `deprecated` from the parent (`pkg/manifest/merge.go`, `pkg/registry/core/core.go`), so `load_artifact` reports a child of a deprecated parent as deprecated and attaches the §4.7.4 warning. Four other surfaces read the stored column, which the child never set, and therefore disagree:

- the `search_artifacts` default-exclusion filter (`pkg/registry/core/core.go`), so the child stays in default results,
- the search descriptor's frontmatter block, which is the child's authored bytes with only `extends:` removed,
- `EffectiveView`, which `podium sync` consumes (`pkg/registry/core/effective_view.go`),
- impact analysis (`pkg/registry/core/dependents.go`).

§4.6 settles which side is right. Its "Omitted fields" paragraph states that a child omitting a frontmatter field inherits the parent's value, and it names `deprecated` among the fields a child overrides only by setting one. The `load_artifact` behavior conforms. The four column-reading surfaces are the non-conforming ones.

## Reachability

An earlier reading of this defect called it narrow, on the reasoning that a parent deprecated after the child was ingested cannot reach the child, because §4.7.6 pins an exact parent version at the child's ingest time. The pin claim is true and the conclusion drawn from it was wrong.

`resolveExtendsPin` (`pkg/registry/ingest/ingest.go`) builds its candidate set by filtering `ListManifests` on `ArtifactID` alone. It captures each candidate's version, content hash, type, and frontmatter, and never reads `Deprecated`. `version.Resolve` receives version strings and cannot filter on deprecation either. So the ordinary workflow reaches the divergence:

1. `shared/parent@1.0.0` is live. `finance/child@1.0.0` declares `extends: shared/parent@1.x`, or no pin at all, and is ingested.
2. The parent team deprecates by publishing `shared/parent@1.0.1` with `deprecated: true`, which is the only way to deprecate: the flag is per-version and there is no in-place toggle (`pkg/store/store.go`).
3. The child is re-ingested for any reason, which every layer sync does. `resolveExtendsPin` selects `1.0.1`, the deprecated version, and pins it.
4. `load_artifact` now reports the child as deprecated. Search still lists it, and `podium sync` still reports it live.

Nothing unusual happens at any step.

## A second divergence in the same resolver

§4.7.6 defines `latest` as "the most recently ingested non-deprecated version". An unpinned `extends: shared/parent` parses to the latest pin, and the resolver selects the highest semver among all candidates, blind to both deprecation and ingest order. It implements neither half of the spec's definition. This is a spec-versus-code divergence independent of the merge behavior above, in the same function, and any resolution that touches candidate selection should close it in the same edit.

## Resolutions

**Refuse or re-resolve at ingest.** `resolveExtendsPin` gains a deprecation map alongside the three it already builds. For a range or unpinned reference, deprecated candidates are filtered out of the selection set before resolution, which repairs the §4.7.6 `latest` divergence at the same time and keeps the step-3 workflow working by re-pinning onto the live version. Refusal is reserved for an exact or content-hash pin naming a deprecated version, where the author asked for that version explicitly, and for a range with no non-deprecated candidate. The existing `ingest.invalid_artifact` covers both, and reusing it creates no §6.10 matrix obligation, where a new code would require a spec entry, a matrix axis, and an annotated test.

This closes the case rather than snapshotting it. `(tenant, artifact_id, version)` is immutable and `deprecated` is per-version, so a pinned parent's flag cannot change after the check, and by induction over the chain no newly ingested record can carry a deprecated ancestor.

The failure mode to weigh: an organization that deprecates a parent line while children remain in maintenance can no longer re-ingest those children against an exact pin. The range-skip half avoids this for the common case.

**Persist the inherited flag.** Rejected as the leading option, and recorded because the reasoning is not obvious. Folding `deprecated` into the child's column arms the §8.4 hard purge against the child ninety days after its own publication, because `stampDeprecation` anchors `DeprecatedAt` to `IngestedAt` and the purge's extends-pin guard protects a row that is pinned rather than a row that pins. A variant that folds the flag while leaving `DeprecatedAt` unset makes the row permanently unpurgeable with no migration, but it requires a non-persisted signal so `PutManifest` skips the stamp, which is an SPI-contract change across three backends. Either variant also has to suppress the `artifact.deprecated` webhook event, because `deprecatedFlip` reads the folded value and would announce a deprecation nobody performed, and has to answer §4.7.4, because `replacedByOf` recovers the upgrade target from the child's stored frontmatter and would report a deprecation with no target.

**Resolve on the search path.** Rejected. It reinstates the per-candidate chain walk proposal 0009 deleted, whose absence 0009 records as a fixed decision, and it repairs one of the four diverging surfaces while leaving the other three.

## Decisions for the reviewer

1. Whether the range-skip and the exact-pin refusal are both taken, or only the refusal.
2. Whether the §4.7.6 `latest` divergence is closed in this proposal or split out. It is in the same function and the same candidate-selection code.
3. Whether children already stored against a deprecated parent are repaired. A repair means rewriting `extends_pin` on an immutable row, so the answer is likely no, matching how proposal 0009 left its own affected rows.

## What the spec says today

§4.6 answers the merge question and mandates the inheritance. §4.6's extends paragraph is silent on whether extending a deprecated parent is permitted, so a resolution that refuses needs a new sentence there and in §4.7.6. §8.4 is one table row that names neither whose flag starts the window nor the pinned-parent guard every backend already implements, so it is under-specified relative to the code independently of this change.

## Non-goals

- Any change to the §8.4 window, or to the purge's existing extends-pin guard for parents.
- Any change to `mergeChain`, which proposal 0011 carries.

## Relationship to proposal 0009

Proposal 0009 has landed. Its ingest fold deliberately excludes `Deprecated`, and its inline comment names the `stampDeprecation` hazard as the reason, so this defect is a recorded deferral rather than a discovery. 0009 also deleted the search path's only per-candidate store read and records that as a fixed decision, which is what makes the search-path resolution above a reversal rather than an addition.
