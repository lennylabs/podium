# Proposal 0011: The served extends record is built from the root parent's row

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-19

This document stages no changes yet. It records a family of defects with one root cause, the evidence for each, and which of them need a spec sentence rather than only a code fix. It has not been through the adversarial review loop.

## The root cause

`mergeChain` (`pkg/registry/core/core.go`) assembles the record `load_artifact` serves for an artifact that declares `extends:`. It starts from `chain[0]`, the **root parent's stored row**, and then repairs a hand-written list of fields from the child. Every field absent from that list ships the root parent's value.

The list is incomplete. What follows is each reachable consequence, established by tracing every assignment in the function to the consumer that reads it.

## Defect 1: an extends child is served under its parent's signature

`out.Signature` is never assigned from the child, so it stays the root parent's envelope, while `out.ContentHash` is assigned the child's. `resultFromRecord` copies `Signature` onto the served result, and it reaches the wire.

Ingest signs each record over its own content hash (`pkg/registry/ingest/ingest.go`), and `sign.EnforceVerification` verifies the served signature against the served content hash (`pkg/sign/sign.go`). The two no longer correspond for any extends child.

On a registry with a signer configured, every extends child is therefore served with a signature that cannot verify against its own content hash. §4.7.9 aborts materialization with `materialize.signature_invalid` on a verification failure, so this fails closed rather than open, and it propagates to every consumer that verifies: the MCP server on materialization, and `podium sync`.

No test in the tree pairs signing with `extends:`, which is why it went unnoticed.

## Defect 2: a child with an empty body is served the parent's prose

The body carry-over is guarded: the child's body replaces the parent's only when it is non-empty. `manifest.MergeExtends` has no such guard and assigns the child's body unconditionally, with a comment stating that extends inherits structured fields rather than the markdown body.

So a child whose body is empty is served the root parent's prose in the record's `Body`, while the body inside the same record's re-serialized frontmatter is the child's. One returned record disagrees with itself, and the parent's prose reaches a requester who may hold no access to the parent's layer. That is the §4.6 "Hidden parents" violation, which scopes its guarantee to the parent's existence and ID being unsurfaced.

## Defect 3: the served merged frontmatter drops every undeclared key

The merged frontmatter is produced by `manifest.SerializeArtifact`, which marshals the closed `manifest.Artifact` struct. Every frontmatter key the struct does not declare is dropped, which is exactly the extension-type fields §4.6 names as inheritable and the §9 `TypeProvider` SPI registers.

Proposal 0009 refused a typed round-trip on the **search** path for this precise reason and landed a node-level strip instead, recording it as a fixed decision. The load path still does the thing 0009 rejected, so `search_artifacts` now preserves an extension key that `load_artifact` destroys for the same artifact. That is a genuine search-versus-load disagreement.

## Defect 4: an inherited `audit_redact` never reaches the read event

§4.6's omitted-field rule makes `audit_redact` inheritable and `MergeExtends` implements it, but the §8.2 read-audit emitter derives its redaction keys from the **pre-merge** leaf record, falling back to parsing that record's own frontmatter. A child that omits `audit_redact` and inherits its parent's directive emits an unredacted read event.

This one is security-relevant and needs confirming against the §8 audit path before it is staged, because the emitter's fallback behavior for a merged record has not been traced end to end.

## Dead assignments

`Description`, `Tags`, and `SearchVisibility` are assigned onto the merged record and read by nothing: `LoadArtifactResult` declares no such fields. `Name` and `WhenToUse` are never assigned and are likewise unread. The in-function comment claims the assignments exist so search descriptors and sensitivity gating agree with the served frontmatter, which stopped being true when proposal 0009 removed the search path's use of this record.

These are not defects today. They are why the function's real defects are hard to see: a reader cannot tell which assignments matter without tracing each to its consumer, and half of them have none. An earlier draft of this proposal reported one of these dead assignments as the headline defect, on a reproduction that observed the function's return value rather than any served surface. That is the failure mode this proposal is written to avoid.

## The structural fix

Each defect above is an omission from the hand-written carry-over list, so repairing them one at a time reproduces the shape that caused them. The alternative is to build the served record from the **leaf** and copy down only what §4.6 says is inherited, which makes the inherited set explicit and the default correct.

That is a larger change than four targeted assignments, and it touches the function every extends read passes through. Which of the two to take is the proposal's central decision.

## Decisions for the reviewer

1. Whether to restructure `mergeChain` to build from the leaf, or to add the missing carry-overs to the existing list.
2. Whether the signature defect needs a spec sentence. §4.7.9 says signatures are stored alongside content and verified on materialization, and does not state in as many words that a served signature covers the served content hash. The code is incoherent under any reading, so this may be a code defect alone.
3. Whether the merged frontmatter is serialized at the YAML node level, as proposal 0009 does on the search path, or whether `manifest.Artifact` gains a catch-all for undeclared keys. The first is consistent with a landed decision; the second changes a type every package uses.
4. Whether defect 4 survives verification against the §8.2 emitter.
5. Whether the dead assignments are deleted in the same change. Deleting them is behavior-preserving and removes the ambiguity that hid the rest.

## Testing that must exist whatever is chosen

Every claim above needs a test asserting on a consumer-visible surface, meaning `LoadArtifactResult` fields or the HTTP JSON, rather than on the record an unexported function returns. The signature defect specifically needs a case that boots with a signer, ingests a parent and an extends child, loads the child, and runs `sign.EnforceVerification` over the served pair. That test fails against the current code.

## Non-goals

- Any change to the ingest fold, the search descriptor, or the §4.7 projection, which proposal 0009 landed.
- Any change to the inherited `deprecated` flag on the search path, which [proposal 0010](0010-refuse-an-extends-pin-onto-a-deprecated-parent.md) carries.

## Relationship to proposals 0009 and 0010

0009 landed and touches neither `mergeChain` nor this defect family, though defect 3 is a direct contradiction of one of its fixed decisions, and its `indexedArtifact` helper is the precedent for reading a record's indexed columns rather than its stored frontmatter. 0010 is independent: it changes the ingest extends resolver, and this proposal changes the load-path merge.
