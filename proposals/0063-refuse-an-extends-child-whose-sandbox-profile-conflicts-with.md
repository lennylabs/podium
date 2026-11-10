# Proposal 0063: Refuse an extends: child whose sandbox_profile conflicts with its parent's instead of ranking the profiles

- Issue: (to be filed)
- Status: Implemented (2026-11-09). Verified on 2026-11-09 after 5 adversarial review rounds (3 findings fixed).
- Date: 2026-11-09

This document stages spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change is backward-incompatible: ingest and a filesystem-source `podium sync` refuse an `extends:` child that ingests or syncs today. It ships in 0.6.0.

## Summary

**What changes.**

- §4.6 drops "most-restrictive" for `sandbox_profile` and defines the sandbox-profile rule. The merge keeps the parent's value unless that value is unset or `unrestricted`. Ingest rejects a child whose own restricting profile differs from the parent's resolved restricting profile, on every `extends:` edge, with `ingest.invalid_artifact` and `details.constraint: "sandbox_profile_conflict"`. §7.3.1 lists the new drop, and §13.11.3 makes a filesystem-source `podium sync` drop such a child on every edge through the existing recompose-and-cascade path (SPEC-1, SPEC-2, SPEC-3).
- `pkg/manifest` replaces `sandboxRank` and `mostRestrictiveSandbox` with `mergeSandboxProfile` and the exported predicate `SandboxProfilesConflict`. `pkg/layer` gains the constraint, the reason, and the sentinel `ErrSandboxProfileConflict` (CODE-1, CODE-2).
- `pkg/registry/ingest` renames `executableInheritance` to `extendsEdgeRejection`, walks the parent's stored chain when the edge crosses layers or the child authors a restricting profile, and checks the sandbox-profile rule before the executable-field rule (CODE-3).
- `pkg/registry/filesystem` checks the rule in `mergeRecord` on every edge, before the cross-layer guard, and refuses through `refusedRecordError` (CODE-4).
- Unit, integration, and end-to-end tests pin the rule in both modes and their equivalence, and two fixtures that encode the removed ranking change first (TEST-5, TEST-1 to TEST-4). A `podium-mcp` unit test pins the warm-cache behavior of a stored conflicting version (TEST-4 (e)). Scenario S96 joins `test/manual-validation.md` (TEST-4). `docs/authoring/extends.md` gains a *Sandbox profile* section that defines the rule, and the docs mirrors of the merge table, the consumer composition table, the reference pages, and `CHANGELOG.md` follow (DOC-1, DOC-2).

**Fixed decisions.**

- D-1: the merge rule, and the deletion of the rank.
- D-2: what a conflict is, and the error code and `details` member.
- D-3: the parent's chain-resolved value against the child's authored value.
- D-4: an unknown value is a restricting value; values compare byte-wise.
- D-5: `network-isolated` against `seccomp-strict` is refused.
- D-6: every edge is tested, and the rejection names no profile value and no parent.
- D-7: stored versions keep serving with the parent's value from the registry, and a `podium-mcp` content cache that already holds one keeps its earlier value until it is pruned, and an `offline-only` cache until it is pruned and refilled in `always-revalidate` or `offline-first`. Q-1 asks whether a stored conflicting version fails closed on read instead.
- D-8: the server check sits on the insert path, with the walk gated on a cross-layer edge or a restricting child, and the sandbox rule first.
- D-9: the filesystem check sits on every edge, before the cross-layer guard.
- D-10: one shared predicate, reason, and constraint for both modes.
- D-11: no new surface.
- D-12: spec-first ordering, the fixture step before CODE-1, and CODE-3 with CODE-4 in one step.

**Watch out for.**

- **Proposal 0062 lands first and edits the same §4.6 text.** The implementation order is 0062, then 0063, then 0058, and 0062 changes the §4.6 same-ID overlay and parent-pin wording that SPEC-1 cross-references ("including a same-ID overlay", "as the executable-field rule above defines it"). Rebase SPEC-1 onto the §4.6 text 0062 leaves before landing S1, and cite 0062's definition of the version a same-ID overlay pins rather than restating it. The same applies to the CODE-3 call site and to `resolveExtendsPin` if 0062 moves them.
- **Two fixtures encode the removed ranking.** `TestMergeExtends_FieldSemanticsTable` (`pkg/manifest/merge_test.go:22`, `:44`, `:91`) and `TestExtends_SandboxChildTightens` (`test/e2e/artifact_extends_test.go:237-245`) pair a `read-only-fs` parent with a `seccomp-strict` child and expect `seccomp-strict`. Under D-1 that pair conflicts. TEST-5 moves both parents to `unrestricted` in a step that lands before CODE-1, and each edit passes before and after the change.
- **Do not walk the chain for every same-layer child.** An unconditional walk adds store reads per insert and newly rejects a same-layer child that never sets `sandbox_profile` when its stored chain is missing an ancestor. CODE-3 gates the walk on `crossLayer || childRestricts`, and TEST-2 pins the gate.
- **Keep the single post-idempotency call site.** The server check runs at `pkg/registry/ingest/ingest.go:807-816`, after the idempotency switch. A second check beside the `extends:` block at `:676` would re-judge an unchanged stored child on every reingest.
- **The filesystem check goes before the cross-layer guard.** In `mergeRecord`, the check sits after the `extends.type_mismatch` check and before `if parentRec.Layer.ID != rec.Layer.ID` (`pkg/registry/filesystem/extends.go:347`). Inside the guard it misses every same-layer edge.
- **The rewrite pass needs no special case.** `resolveExtends` re-runs `mergeRecord` on records whose parents it has rewritten, and `SandboxProfilesConflict(g, mergeSandboxProfile(g, p))` is false for every `g` and `p`, so the rewrite raises no refusal. Adding a skip for rewritten records hides a real defect if the property ever breaks; TEST-1 pins the property.
- **The reason contains the word `unrestricted`.** Non-disclosure assertions check that the reason names none of `read-only-fs`, `network-isolated`, and `seccomp-strict`. An assertion over every §4.4.1 value fails on the reason's own text.
- **A same-layer fixture must order the parent first.** Server ingest processes one layer's records in canonical-ID order (`pkg/registry/ingest/ingest.go:1052`) and resolves a pin only against stored rows, so a child that sorts before its same-layer parent is rejected for an unrelated reason.
- **The problem-statement pair does not tell the old build from the new one on a stored version.** For a child `read-only-fs` under a parent `network-isolated` or `seccomp-strict`, both builds serve the parent's value. S96 uses child `seccomp-strict` over parent `read-only-fs`, which the old build serves as `seccomp-strict` and the new build as `read-only-fs`.
- **A warm `podium-mcp` cache keeps a stored conflicting version's earlier profile.** The merged record keeps the child's content hash, and the load validator covers only that hash and the `extends_pin`, so a `podium-mcp` that cached the version before the upgrade keeps delivering the child's earlier profile (D-7). This is accepted and documented with the `podium cache prune --days 0` remedy, which refetches in `always-revalidate` and `offline-first`; an `offline-only` consumer keeps the earlier profile indefinitely and needs its cache cleared and refilled in one of those modes, because a pruned `offline-only` cache answers every load with `network.offline_cache_miss`. Do not change the load ETag or `X-Podium-Content-Hash` inside this proposal; TEST-4 (e) and S96 step 5 pin the accepted behavior.
- **Scenario and port numbers can collide with 0062 and 0058.** If an earlier proposal in the 0.6.0 sequence has taken S96 or port 8198, take the next free number and port, and use the same number in the index row, the heading, and every reference.
- **Parts of `test/e2e` skip silently on macOS.** Run the end-to-end steps with `-v` and grep the output for `SKIP` before claiming they passed.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3. The §4.6 sandbox-profile rule, the §7.3.1 drop, and the §13.11.3 sync paragraph land in one commit, because SPEC-2 and SPEC-3 cite the rule SPEC-1 defines and §4.6 would point at a §13.11.3 paragraph that does not exist alone.
      Levels: unit. Depends on: —
- [x] **S2 · test** — TEST-5. The merge-table unit fixture and the e2e tightening fixture move their parents to `unrestricted`, and the `TestExtends_SandboxChildCannotWiden` doc comment cites the *Sandbox profile* section of `docs/authoring/extends.md`.
      Levels: unit, e2e. Depends on: S1
      Interleave: this test step precedes the code steps because each edit passes before and after CODE-1, so every later commit stays green (D-12). The doc comment cites the section S10 adds, and D-12 places the docs after the code, so that citation resolves at S10 in the same pull request.
- [x] **S3 · code** — CODE-1. `mergeSandboxProfile` and `SandboxProfilesConflict` replace the rank in `pkg/manifest/merge.go`.
      Levels: unit. Depends on: S2
- [x] **S4 · code** — CODE-2. `SandboxProfileConflictConstraint`, `SandboxProfileConflictReason`, and `ErrSandboxProfileConflict` in `pkg/layer/sandbox.go`.
      Levels: unit. Depends on: S1
- [x] **S5 · code** — CODE-3, CODE-4. The server insert-path check and the filesystem drop. Bundled because landing one mode's refusal without the other leaves the deployment modes diverging at that commit (§2.2), and one reviewer checks that both call the same predicate in the same order.
      Levels: unit, integration, materialization, e2e. Depends on: S3, S4
- [x] **S6 · test** — TEST-1. The 6x6 merge and conflict table, the idempotence properties, and the `pkg/layer` constant tests.
      Levels: unit. Depends on: S3, S4
- [x] **S7 · test** — TEST-2. Server ingest cases and the reingest wire case.
      Levels: integration. Depends on: S5
- [x] **S8 · test** — TEST-3. Filesystem resolver cases and the mode-equivalence cases.
      Levels: unit, integration, materialization. Depends on: S5
- [x] **S9 · test** — TEST-4. End-to-end server and sync cases, the warm-cache `podium-mcp` case, and scenario S96.
      Levels: unit, e2e, manual. Depends on: S5
- [x] **S10 · docs** — DOC-1. `docs/authoring/extends.md`, the frontmatter pages, the reference pages, and the consumer composition table.
      Levels: —. Depends on: S1
- [x] **S11 · docs** — DOC-2. The CHANGELOG bullet.
      Levels: —. Depends on: S1–S10

**Ordering constraints.** S1 lands first and passes `make coverage-gate` before any later step (D-12). S2 lands before S3. S3 and S4 are independent of each other. The test steps S6 to S9 and the docs step S10 are independent of one another once their dependencies land. S11 describes the whole change and lands last. Land every step in one pull request.

## Current state and the gap

### The spec names no order

The §4.6 field-semantics table merges `sandbox_profile` as "Scalar; most-restrictive" and names no ordering (`spec/04-artifact-model.md:704`). Its neighbours do name one: `sensitivity` is high > medium > low (`:700`), and `search_visibility` is `direct-only` > `indexed` (`:708`). §4.4.1 defines each restricted profile by the resource it restricts (`:367-374`): `read-only-fs` restricts the filesystem, `network-isolated` restricts outbound network, and `seccomp-strict` sets a syscall allowlist. No row says one profile includes another, and the only cross-profile statement contrasts `unrestricted` with every other value (`:376`). The profiles therefore have no total order, and `unrestricted` is the only value below all the others.

### The code invents one

`MergeExtends` sets `out.SandboxProfile` from `mostRestrictiveSandbox(parent, child)` (`pkg/manifest/merge.go:57-58`). `sandboxRank` ranks `seccomp-strict` 4, `network-isolated` 3, `read-only-fs` 2, `unrestricted` 1, and an empty or unknown value 0 (`:233-245`), and the higher rank wins, with ties going to the parent (`:247-252`). A child that declares `read-only-fs` and extends a parent that resolves to `network-isolated` or `seccomp-strict` therefore merges to the parent's value. The child's filesystem restriction is dropped, and restating the field cannot prevent it.

The merged value is the only one any consumer sees:

- `mergeChain` re-serializes the merged artifact into the served frontmatter (`pkg/registry/core/core.go:1875-1960`).
- The MCP §4.4.1 host gate parses that frontmatter (`cmd/podium-mcp/main.go:2335-2341`), and so does the seccomp baseline writer (`:2010`, `:2368-2374`).
- Adapters build the `SKILL.md` `compatibility` value from the merged `art.SandboxProfile` (`pkg/adapter/compatibility.go:35`, `:62`).
- The filesystem resolver serves the same merged bytes (`pkg/registry/filesystem/extends.go:281-285`, `:353`).

The loss therefore reaches the host end to end.

### No existing rule catches it

- §4.6 places `sandbox_profile` outside the executable-field rule (`spec/04-artifact-model.md:722`).
- The §13.10 ingest gate is off by default, and it checks only whether the host can enforce the child's authored value (`pkg/registry/ingest/ingest.go:604-616`).
- No lint rule covers the case.
- Proposal 0057 recorded the defect as Q-2 and as a non-goal (`proposals/0057-require-an-extends-child-to-restate-every-executable-field-a.md:120`, `:409`, `:666`, `:673`).

The partial containment in the shipped baseline does not rescue the order. `seccomp-strict` blocks `socket` and `connect` (`pkg/materialize/seccomp_test.go:50-55`) but allows `openat`, `write`, `rename`, and `unlink` (`pkg/materialize/seccomp-strict.json:68-109`). `read-only-fs` therefore cannot be compared with either other profile, even in the implementation.

### The decided fix

The maintainer decided to fix this in 0.6.0. `sandbox_profile` stays a single enum value. When a child and the parent it extends both set different values and neither is `unrestricted`, ingest rejects the child with the existing `ingest.invalid_artifact` and a new `details.constraint` value, and a filesystem-source `podium sync` drops it through the drop path 0057 added. Otherwise the merge only tightens.

## Decisions

- **D-1. Merge rule.** The merged `sandbox_profile` is the child's value when the parent's value is unset or `unrestricted`, and the parent's value otherwise. That one sentence covers every case. An unset or `unrestricted` side yields the other side's value: a child tightens an unrestricted parent (the `docs/authoring/extends.md:306-322` example keeps working), and a child's `unrestricted` under a restricting parent yields the parent's value, as `pkg/manifest/merge_test.go:165-178` pins today. Equal values yield that value. For a conflicting pair the merge yields the parent's value, which ingest refuses for a new version (D-2) and which only a stored version can reach (D-7). The field stays a single enum (`spec/04-artifact-model.md:175`; `pkg/manifest/types.go:49-56`), and `sandboxRank` and `mostRestrictiveSandbox` (`pkg/manifest/merge.go:233-252`) are deleted. The rule never loosens a restriction the parent sets, so the §1 summary "Most-restrictive-wins for security fields" (`spec/01-overview.md:19`) stays accurate and is not edited.
- **D-2. Conflict and refusal.** A conflict exists when the child and the parent both set `sandbox_profile`, the values differ byte-wise, and neither is `unrestricted`. Ingest rejects such a child with the existing `ingest.invalid_artifact` (`pkg/layer/executable.go:14`) and `details.constraint: "sandbox_profile_conflict"`, with no further `details` members. §6.10 gains no code and the matrix axis is unchanged. The `RejectedArtifact.Details` field (`pkg/registry/ingest/ingest.go:414-424`) and the reingest serializer (`pkg/registry/server/layers.go:2014-2015`) already carry an arbitrary `details` map, so the wire needs no code change.
- **D-3. Values compared.** The parent's value is its chain-resolved value: the result of the §4.6 fold over the parent's own `extends:` chain, as 0057 D-3 defines "the parent sets" (`core.ExtendsChain` and `core.ResolvedArtifact`, `pkg/registry/core/core.go:1836-1890`; `parentMerged` in `pkg/registry/filesystem/extends.go:332-339`). The child's value is the one its own frontmatter authors (`rec.Artifact.SandboxProfile`, parsed from authored bytes on the server and in the filesystem validation pass, `pkg/registry/filesystem/extends.go:14-35`). A value inherited from a grandparent therefore counts.
- **D-4. Unknown values.** Nothing validates `sandbox_profile`: only `pkg/manifest/types.go:51-56` declares the enum, and `pkg/lint` has no rule for it. 0057 deferred validation (`proposals/0057-require-an-extends-child-to-restate-every-executable-field-a.md:673`). This proposal treats every non-empty value other than `unrestricted` as a restricting value and compares values byte-wise. Two consequences follow. An unknown value conflicts with any different restricting value and is refused. An unknown value over an unset or unrestricted side is kept rather than dropped; today it ranks 0 and loses to an unset value in the parent's favour (`pkg/manifest/merge.go:233-251`). Keeping it fails closed, because the MCP host gate refuses any profile that is neither `unrestricted` nor listed in the host's sandboxes (`cmd/podium-mcp/main.go:2335-2364`), as does the opt-in ingest gate (`pkg/registry/ingest/ingest.go:1788-1798`). The spec text says only that a value other than `unrestricted` restricts. Enum validation stays a non-goal.
- **D-5. `network-isolated` against `seccomp-strict` is refused on purpose.** The shipped baseline blocks network syscalls (`pkg/materialize/seccomp_test.go:50-55`), but the spec does not guarantee that containment. §4.4.1 defines `seccomp-strict` only as a "strict syscall allowlist" (`spec/04-artifact-model.md:374`). The baseline's own comment lets a host extend the allowlist for an artifact (`pkg/materialize/seccomp-strict.json:2`). A process can still `write()` to an inherited socket descriptor, because `write` is allowed. The rule therefore rejects every differing pair of restricting values, and the spec says so, so that a reader does not take the shipped baseline as a spec-level ordering.
- **D-6. Every edge.** The rule tests every `extends:` edge, same-layer and cross-layer, including a same-ID overlay, wherever the parent comes from. It therefore reveals nothing about the parent's layer, and the §4.6 resolution-disclosure paragraph (`spec/04-artifact-model.md:690`) needs no edit. The reason and `details` name neither profile value and no parent coordinate. The parent's resolved profile is already observable through the merge under *Served* (`:687`), because a child that omits the field is served the parent's value. Naming no value keeps the rejection outside the *Withheld* reference test (`:685`) without a per-value check, which an unvalidated parent value would otherwise need (`pkg/manifest/merge_serialize.go:403-410`). The reason tells the author to omit `sandbox_profile` to inherit the parent's profile. Both modes give the byte-identical reason from one constant in `pkg/layer`.
- **D-7. Stored versions keep serving.** As 0057 D-6 does for executable fields, the server check runs on the insert path after the idempotency switch (`pkg/registry/ingest/ingest.go:785-806`), so an unchanged stored child is classified unchanged and is never re-judged. A stored pin is immutable (§4.7.6), so after this change a conflict can exist on the server only in a version stored before the upgrade. The read path folds that chain with the D-1 merge and serves the parent's value. For the case the problem names (child `read-only-fs` under a parent `network-isolated` or `seccomp-strict`), the served value is unchanged. It does change for the three pairs where the child out-ranked the parent under `sandboxRank`: child `network-isolated` over parent `read-only-fs`, child `seccomp-strict` over parent `read-only-fs`, and child `seccomp-strict` over parent `network-isolated`. Those pairs now serve the parent's value where they served the child's. Each served value of a conflicting pair drops one of the two restrictions, both today and after the change. Keeping today's rank as a fallback for stored data alone would be the dual code path the pre-1.0 rule forbids (`.claude/rules/code-best-practices.md`, Configuration and compatibility). The spec states the stored-version behavior explicitly. Q-1 asks the reviewer whether to fail closed on read instead.

  The registry serves that change under an unchanged validator, and a warm consumer cache keeps the earlier value. `mergeChain` keeps the child's content hash (`pkg/registry/core/core.go:1919`). The load ETag is computed from the content hash and the `extends_pin` alone (`pkg/registry/server/server.go:1238-1254`; `spec/07-external-integration.md:45`), and a HEAD reports the content hash in `X-Podium-Content-Hash` (`server.go:1186`). A `podium-mcp` in the default `always-revalidate` mode delivers its cached record when the HEAD hash matches the cached one (`cmd/podium-mcp/main.go:256`, `:1378-1391`), `offline-first` delivers it without asking the registry (`:1350`), the §7.4 degraded-network fallback delivers it after the §6.6 checks (`spec/06-mcp-server.md:249`), and a pinned resolution never expires (`cmd/podium-mcp/resolution_cache.go:350-351`). The cached record carries the frontmatter the registry merged when the record was cached (`cmd/podium-mcp/resolution_cache.go:440-455`), and the §4.4.1 host gate and the seccomp baseline writer read that frontmatter (`cmd/podium-mcp/main.go:2010`, `:2335-2341`). A `podium-mcp` whose content cache holds such a version from before the upgrade therefore keeps applying the child's earlier profile, while a `podium-mcp` with a cold cache, `podium sync`, and a direct HTTP load receive the parent's. The cached value is the value every consumer received before the upgrade, and each value of a conflicting pair drops one restriction, so the stale entry loosens nothing relative to the pre-upgrade state. A `podium-mcp` in `offline-only` never calls the registry (`spec/06-mcp-server.md:247`), so it keeps applying the child's earlier profile indefinitely. This proposal accepts the transition and documents the remedy. `podium cache prune --days 0` against a consumer's cache directory removes every content bucket and keeps the dot-prefixed resolution index (`cmd/podium/cache.go:47`, `:95`, `docs/reference/cli.md:810-822`). In `always-revalidate` and `offline-first`, `podium-mcp` then finds no cached content for the hash (`cmd/podium-mcp/main.go:2930-2935`, `:1574-1578`) and fetches the merged record on the next load that reaches the registry. A pruned `offline-only` consumer fetches nothing: its resolution index still names the hash, the bucket is gone, and every load from that cache returns `network.offline_cache_miss` (`cmd/podium-mcp/main.go:1373-1374`, `cmd/podium-mcp/resolution_cache.go:546`). Its remedy is to clear the cache directory and refill it in `always-revalidate` or `offline-first`, the remedy the operator docs already give an `offline-only` consumer after a key rotation (`docs/deployment/operator-guide.md:134`, `docs/deployment/clustered.md:443`). A new non-conflicting version of the child carries a new content hash, so an `always-revalidate` load of `latest` that reaches the registry stops matching the cached hash and fetches the new version. It does not reach a pinned load, whose resolution to the old hash never expires, nor `offline-first` or the degraded-network fallback, which serve a cached `latest` resolution without asking the registry (`cmd/podium-mcp/main.go:1350-1366`, `cmd/podium-mcp/resolution_cache.go:384-390`), nor `offline-only`, so the prune is the remedy for `always-revalidate` and `offline-first`, and the prune followed by a refill in one of those modes is the remedy for `offline-only`. Covering the merged record in the load validator would change the §7.2 entity tag and the `X-Podium-Content-Hash` header, whose value `podium-mcp` also uses as its content-bucket key, to repair a transition that affects only versions stored before the upgrade, so this proposal does not change the validator. §6.5 already defines the content cache as keyed by content hash and revalidated by content hash (`spec/06-mcp-server.md:240-246`), so the warm-cache behavior follows from unchanged spec text. SPEC-1 names the registry as the party that serves the merged value, and the transition note lives in the docs and the CHANGELOG (DOC-1 item 2, DOC-2). TEST-4 (e) and S96 step 5 pin the behavior.
- **D-8. Server placement and walk.** When `mr.ExtendsPin != ""`, the insert-path check (`pkg/registry/ingest/ingest.go:807`) walks the parent's stored chain when the edge crosses layers or the child authors a restricting `sandbox_profile`. Today it walks only for a cross-layer edge (`:1529-1531`). It applies the sandbox-profile rule to every edge it walks, and then applies the executable-field rule when the edge crosses layers. A child that both rules refuse reports the sandbox-profile rejection, and the filesystem resolver checks in the same order. A same-layer child that authors a restricting profile is the only case that previously skipped the walk and now takes it. For such a child, a missing stored ancestor or a looping chain now rejects it with the existing `chainUnresolvedReason` (`pkg/registry/ingest/ingest.go:1510-1517`), and a store fault aborts the cycle with `*chainReadError`, as on a cross-layer edge. The read path already fails for such a chain (`pkg/registry/core/core.go:1762-1772`), so no servable child is lost. A same-layer child that omits `sandbox_profile` or sets `unrestricted` keeps today's path, because `SandboxProfilesConflict` is false for it whatever the parent resolves to.
- **D-9. Filesystem source.** `mergeRecord` runs the check on every edge directly after the type-match check (`pkg/registry/filesystem/extends.go:343`) and outside the cross-layer guard (`:347`). A refusal returns `*refusedRecordError` wrapping `layer.ErrSandboxProfileConflict`, so `validateExtends` and `refusalOf` pass it through. `composeExtends` then drops it, recomposes, and cascades to stranded references, and `OnDrop` reports it as `layer.Drop{Code: ingest.invalid_artifact}` (`pkg/registry/filesystem/extends.go:37-72`, `:176-192`). The rule applies on every run, so a conflict that predates the upgrade is dropped in sync while the server keeps serving the stored version. 0057 accepted the same mode difference for executable fields (`spec/04-artifact-model.md:728`). The rewrite pass `resolveExtends` re-runs `mergeRecord` on records whose parents it has already rewritten. It raises no spurious refusal, because the merged value of a resolved parent never conflicts with the value the validation pass compared: `SandboxProfilesConflict(g, mergeSandboxProfile(g, p))` is false and `mergeSandboxProfile(g, mergeSandboxProfile(g, p)) = mergeSandboxProfile(g, p)` for every `g` and `p`.
- **D-10. Shared implementation (§2.2).** `manifest.SandboxProfilesConflict` and the merge helper live in `pkg/manifest` beside `MergeExtends`. The constraint, reason, and sentinel error live in `pkg/layer` beside `ExecutableInheritance`. Server ingest and the filesystem resolver both call them, so they refuse the same children with the same code and reason wherever they select the same parent record. Where they select different parents (the §13.11.3 same-ID-overlay parent-selection difference, `spec/13-deployment.md:341`), they test different edges, as 0057 D-5 already states.
- **D-11. No new surface.** The change adds no environment variable, flag, `registry.yaml` key, opt-out, §6.10 code, matrix cell, meta-tool, or SPI method. The rule is fixed by the spec, so §13.12 does not apply. The change is backward-incompatible because ingest and sync refuse children they accept today. It is recorded under `[Unreleased]` `### Changed` and ships in 0.6.0, as the maintainer decided.
- **D-12. Ordering.** SPEC-1 to SPEC-3 land in one commit, verified with `make coverage-gate`, before any code (`.claude/rules/spec-driven-development.md`). TEST-5 follows, because its fixture edits pass before and after the merge change. The code then lands in the order CODE-1, CODE-2, CODE-3 with CODE-4, followed by the tests, docs, and CHANGELOG, all in one pull request.

## Spec amendment: §4.6 Layers and Visibility, sandbox profile across extends

**SPEC-1.** `spec/04-artifact-model.md`, §4.6. Three edits, all in one commit with SPEC-2 and SPEC-3 (D-12).

**Rationale.** The table row names no ordering, and the code supplies a total order that §4.4.1 contradicts (D-1). The merge, the refusal, the values compared, the edges tested, the rejection's contents, and the stored-version terms need one normative definition next to the executable-field rule, which already defines the parent's resolved manifest and how a chain that cannot be read is handled (D-1 to D-8). The resolution-disclosure paragraph needs no edit, because the rule tests every edge and reveals nothing about the parent's layer, and the parent's resolved profile already reaches the author through the merge (D-6). The stored-version sentence names the registry as the party that serves the merged value, because a §6.5 content cache revalidates by content hash and can keep delivering a record it cached before the upgrade (D-7).

**Rebase before landing.** Rebase the phrase "including a same-ID overlay" and the cross-reference "as the executable-field rule above defines it" onto the §4.6 overlay and parent-pin text as proposal 0062 leaves it. The implementation plan orders 0062 before this change, and 0062 edits that text. If 0062 redefines which version a same-ID overlay pins, cite that definition rather than restating it.

**Edit 1, field-semantics table.** Anchor: the `sandbox_profile` row of the table under `### Field semantics table`.

Current text:

> | `sandbox_profile`                      | Scalar; most-restrictive                                   |

Replacement (pad to the table's existing column width):

> | `sandbox_profile`                      | Scalar; see the sandbox-profile rule below                 |

**Edit 2, executable fields across layers.** Anchor: the paragraph that begins "The restated value may equal the parent's or differ from it." Replace one sentence.

Current text:

> `sandbox_profile` constrains execution and supplies nothing to run.

Replacement:

> `sandbox_profile` constrains execution and supplies nothing to run, and the sandbox-profile rule below governs its merge.

The rest of the paragraph is unchanged.

**Edit 3, new paragraphs.** Anchor: directly after the paragraph that ends "A filesystem-source `podium sync` applies the rule on every run (§13.11.3)." and before the heading `## 4.7 Registry as a Service`. Insert:

> **Sandbox profile.** §4.4.1 defines each profile other than `unrestricted` by the resource it restricts and states no inclusion between them, so `sandbox_profile` has no order among those profiles. Under the sandbox-profile rule, every value other than `unrestricted` is a restricting value, including a value §4.4.1 does not list, and two values are the same only when they are byte-identical. The merged `sandbox_profile` is the child's value when the child sets one and the parent's resolved value is unset or `unrestricted`, and it is the parent's resolved value otherwise. The parent's resolved value is the one the parent's resolved manifest carries, as the executable-field rule above defines it, so a value the parent inherited from its ancestors counts. A child can therefore tighten a parent that sets no restriction, and it cannot loosen a restriction the parent sets.
>
> Ingest rejects a child with `ingest.invalid_artifact` (§6.10) when the child's own frontmatter sets a restricting `sandbox_profile`, the parent's resolved value is a restricting value, and the two values differ. It rejects any two different restricting profiles, including a pair where the baseline that §4.4.1 names for one profile also blocks the resource the other restricts. The rule tests every `extends:` edge, whether the pinned parent version (§4.7.6) comes from the child's layer or from another layer, including a same-ID overlay. The rejection carries `details.constraint: "sandbox_profile_conflict"` and no other `details` member. Neither the reason nor `details` names either profile value or the parent's canonical ID, version, layer, content hash, or signature, and the reason directs the author to omit `sandbox_profile` so that the child inherits the parent's profile. When the registry needs the parent's chain to evaluate the rule and cannot read it, it rejects the child as the executable-field rule above does. A child that both this rule and the executable-field rule refuse is rejected under this rule.
>
> The rule applies to the version being ingested and never invalidates a version already stored. A stored version whose chain pairs two conflicting values keeps its pin, and the registry keeps serving it with the merged value the first paragraph of this rule defines, which is the parent's value when the two values conflict, and a cycle that presents it with unchanged content classifies it unchanged (§7.3.1). A filesystem-source `podium sync` applies the rule on every run (§13.11.3).

No other edits to `spec/04-artifact-model.md` are needed. The §1 overview summary is not edited (D-1).

## Spec amendment: §7.3.1 Authoring and Ingestion, ingest outcome

**SPEC-2.** `spec/07-external-integration.md`, §7.3.1, the paragraph that begins "**Ingest outcome.**". Two edits.

**Rationale.** §7.3.1 enumerates every ingest drop and states when a reingest `rejected` entry carries `details`. Today it names only the executable-field rule (D-2). The new drop is its own list item, because joining it to the executable-field item with "and" reads as the parent "sets … a child", and because the item points to the §4.6 definition (the parent's chain-resolved value, neither side `unrestricted`) instead of restating it loosely.

**Edit 1, drop list.**

Current text:

> a child that does not restate an executable field its parent in another layer sets (`ingest.invalid_artifact`, §4.6), an unresolved `extends` pin (`ingest.invalid_artifact`),

Replacement:

> a child that does not restate an executable field its parent in another layer sets (`ingest.invalid_artifact`, §4.6), a child whose `sandbox_profile` the §4.6 sandbox-profile rule refuses (`ingest.invalid_artifact`, §4.6), an unresolved `extends` pin (`ingest.invalid_artifact`),

**Edit 2, details sentence.**

Current text:

> and with a `details` object when the rule that rejected it defines one, as the §4.6 executable-field rule does.

Replacement:

> and with a `details` object when the rule that rejected it defines one, as the §4.6 executable-field and sandbox-profile rules do.

The rest of the paragraph is unchanged.

## Spec amendment: §13.11.3 What's Available, sandbox profile conflicts

**SPEC-3.** `spec/13-deployment.md`, §13.11.3. Insert a new paragraph directly after the paragraph that begins "**Executable inheritance.**" (it ends "Exit status, `--dry-run`, `--config` targets, and `podium sync override` treat these drops as they treat a collision drop.") and before "What's **not available** in filesystem source:".

**Rationale.** The filesystem source keeps no ingest state and re-resolves on every run (`pkg/sync/sync.go:578-583`). The existing "Executable inheritance." paragraph covers drops only for cross-layer edges, and the collision paragraph covers only `ingest.collision` (`spec/13-deployment.md:339-341`). The sandbox-profile rule also drops on same-layer edges, so it needs its own paragraph. The paragraph delegates the drop, the standard-error line, the code, the recomposition, the cascade, and the exit-status, `--dry-run`, `--config`, and `podium sync override` terms to the executable-inheritance paragraph, and it states the edge scope of each rule after a recomposition so that the executable-field rule is not read as applying to same-layer references (`spec/04-artifact-model.md:724`; `spec/13-deployment.md:341`).

Insert:

> **Sandbox profile conflicts.** Sync applies the §4.6 sandbox-profile rule on every run to each artifact that declares `extends:`, including one whose parent comes from its own layer subdirectory. The parent is the artifact that the executable-inheritance paragraph names. Sync drops and reports an artifact this rule refuses, composes the layers again, and repeats until no further artifact is dropped, as the executable-inheritance paragraph states for that rule. After each recomposition, the sandbox-profile rule applies to every reference that is resolved again, and the executable-field rule applies only to such a reference that crosses a layer. When an artifact fails both rules, sync reports the sandbox-profile refusal.

## Proposed solution

### CODE-1. `pkg/manifest/merge.go`: tightening-only merge and the shared conflict predicate

`MergeExtends` is the single §4.6 fold that every path uses (`pkg/registry/core/core.go:1881`, `pkg/registry/ingest/ingest.go:1466`, `pkg/registry/filesystem/extends.go:353`, `pkg/manifest/executable.go:60`). It returns no error, so it yields a value for a conflicting pair, while the refusal lives in a separate predicate both modes call (D-1, D-7, D-10).

Replace `:57-58` with:

```go
// sandbox_profile: the parent's value unless it is unset or unrestricted.
out.SandboxProfile = mergeSandboxProfile(parent.SandboxProfile, child.SandboxProfile)
```

Delete `sandboxRank` and `mostRestrictiveSandbox` (`:231-252`), and add:

```go
// mergeSandboxProfile returns the §4.6 merged sandbox_profile: the child's
// value when the parent's is unset or unrestricted, and the parent's
// otherwise. The §4.4.1 profiles restrict different resources, so they carry
// no rank. Ingest and a filesystem sync refuse a pair SandboxProfilesConflict
// reports; only a version stored before that rule reaches this function with
// one, and it is served the parent's value.
//
// Spec: §4.6
func mergeSandboxProfile(parent, child SandboxProfile) SandboxProfile {
	if child != "" && (parent == "" || parent == SandboxUnrestricted) {
		return child
	}
	return parent
}

// SandboxProfilesConflict reports whether the parent's resolved and the
// child's authored sandbox_profile both restrict and differ. Any value other
// than unrestricted restricts, including one §4.4.1 does not list, and values
// compare byte-wise.
//
// Spec: §4.6
func SandboxProfilesConflict(parent, child SandboxProfile) bool {
	if parent == "" || child == "" || parent == child {
		return false
	}
	return parent != SandboxUnrestricted && child != SandboxUnrestricted
}
```

Update the `MergeExtends` doc comment (`:17-19`) so that `sandbox_profile` reads "the parent's value unless it is unset or unrestricted" rather than most-restrictive, and `sensitivity` keeps its most-restrictive wording.

### CODE-2. `pkg/layer/sandbox.go`: constraint, reason, and sentinel error

Both modes need byte-identical reason text and a matchable error. The refusal carries no varying data, so it is two constants and a sentinel error with no struct type. `ExecutableInheritance` needs a struct because `Reason()` and `Details()` format its `Fields` slice (`pkg/layer/executable.go:52-70`), and `UnreadableExecutableFieldsReason` is the precedent for a fixed-text refusal (`:22-29`).

New file `pkg/layer/sandbox.go`:

```go
// SandboxProfileConflictConstraint is the details.constraint value of a §4.6
// sandbox-profile rejection.
//
// Spec: §4.6, §7.3.1
const SandboxProfileConflictConstraint = "sandbox_profile_conflict"

// SandboxProfileConflictReason is the reason both modes give a child whose
// sandbox_profile and its parent's resolved sandbox_profile both restrict and
// differ. It names neither value and no parent, so it discloses nothing the
// §4.6 hidden-parent rule withholds.
//
// Spec: §4.6
const SandboxProfileConflictReason = extendsReasonPrefix + "sandbox_profile differs from the profile the parent sets, and neither is unrestricted; omit sandbox_profile to inherit the parent's profile"

// ErrSandboxProfileConflict is the error a filesystem walk with no drop
// callback fails with for a §4.6 sandbox-profile refusal, matched with errors.Is.
//
// Spec: §4.6, §13.11.3
var ErrSandboxProfileConflict = errors.New(SandboxProfileConflictReason)
```

Extend the `InvalidArtifactCode` doc comment (`pkg/layer/executable.go:8-14`) to name the §4.6 sandbox-profile rule beside the executable-field rule.

Server ingest (CODE-3) builds the rejection inline, in the style of `pkg/registry/server/layers.go:1271`:

```go
&RejectedArtifact{
	ArtifactID: rec.ID,
	Reason:     layer.SandboxProfileConflictReason,
	Code:       layer.InvalidArtifactCode,
	Details:    map[string]any{"constraint": layer.SandboxProfileConflictConstraint},
}
```

The filesystem resolver (CODE-4) returns `&refusedRecordError{id: rec.ID, layerID: rec.Layer.ID, reason: layer.SandboxProfileConflictReason, err: layer.ErrSandboxProfileConflict}`.

### CODE-3. `pkg/registry/ingest/ingest.go`: walk the chain when either rule can fire, and check the sandbox rule first

The current check returns early for a same-layer parent (`:1529-1531`) and is called only on the insert path (`:808`). `foldExtendsParent` folds no sandbox column (`:1465-1485`), so the parent's resolved value comes from `core.ResolvedArtifact` over the walked chain (D-3). Placing the check at the existing post-idempotency call site preserves stored-version serving (D-7).

Rename `executableInheritance` to `extendsEdgeRejection` and keep it at the single insert call site. The body:

```go
// extendsEdgeRejection applies the §4.6 sandbox-profile rule to every
// extends: edge and the §4.6 executable-field rule to an edge whose pinned
// parent comes from another layer, to a child about to be inserted. It reads
// the parent's stored chain only when one of the rules can refuse the edge:
// the edge crosses layers, or the child authors a restricting
// sandbox_profile. A child both rules refuse reports the sandbox-profile
// rejection. ...
//
// Spec: §4.6
func extendsEdgeRejection(ctx context.Context, st store.Store, tenantID, childLayer string,
	rec filesystem.ArtifactRecord, mr, parent store.ManifestRecord) (*RejectedArtifact, error) {
	childProfile := rec.Artifact.SandboxProfile
	childRestricts := childProfile != "" && childProfile != manifest.SandboxUnrestricted
	crossLayer := parent.Layer != childLayer
	if !crossLayer && !childRestricts {
		return nil, nil // neither rule can refuse this edge; skip the chain read
	}
	chain, err := core.ExtendsChain(ctx, st, tenantID, parent)
	// existing arms, unchanged: ErrNotFound / ErrInvalidArgument ->
	// chainUnresolvedReason rejection; any other error -> log and *chainReadError.
	p, blocks := core.ResolvedArtifact(chain)
	if manifest.SandboxProfilesConflict(p.SandboxProfile, childProfile) {
		log.Printf("ingest: tenant=%s layer=%s artifact=%s extends_pin=%s parent_layer=%s rejected: sandbox_profile_conflict",
			tenantID, childLayer, rec.ID, mr.ExtendsPin, parent.Layer)
		return &RejectedArtifact{ /* CODE-2 inline form */ }, nil
	}
	if !crossLayer {
		return nil, nil
	}
	// existing executable-field comparison, unchanged
}
```

`rec.Artifact` is non-nil on this path, because `mr.ExtendsPin` is set from `rec.Artifact.Extends` (`:676-711`), and it holds the child's authored values, because server ingest walks one layer and never rewrites a record with its parent's values. The log line names the parent's layer for the operator, as the executable-field line does; the rejection names nothing.

Comment updates in the same file:

- The `chainUnresolvedReason` doc comment (`:1503-1508`) names both the cross-layer edge and the same-layer edge whose child authors a restricting profile, in place of "cross-layer parent's stored chain".
- The function doc comment states both rules, the gate, and the precedence.
- The insert-site comment (`:801-806`) names both §4.6 rules.

### CODE-4. `pkg/registry/filesystem/extends.go`: refuse a sandbox conflict on every edge through the existing drop path

0057's refusal-and-cascade machinery (`refusedRecordError`, `refusalOf`, the `composeExtends` rounds, and `layer.Drop` through `OnDrop`) fits a second constraint unchanged. In `mergeRecord`, add the check directly after the `extends.type_mismatch` check and before `if parentRec.Layer.ID != rec.Layer.ID`:

```go
	// Spec: §4.6 sandbox profile. Every edge, same-layer included, before
	// the cross-layer executable-field check, so a child both rules refuse
	// reports this refusal as the server does.
	if manifest.SandboxProfilesConflict(parentMerged.SandboxProfile, rec.Artifact.SandboxProfile) {
		return nil, &refusedRecordError{id: rec.ID, layerID: rec.Layer.ID,
			reason: layer.SandboxProfileConflictReason, err: layer.ErrSandboxProfileConflict}
	}
```

During `validateExtends`, `rec.Artifact` is parsed from the authored bytes, because the rewrite runs only after the composition settles, and `parentMerged` is the chain-resolved parent (D-3). The rewrite pass raises no refusal (D-9).

Doc comments that enumerate the composition's drop causes name the sandbox-profile rule:

- (a) `pkg/registry/filesystem/extends.go`: `composeExtends` (`:13-36`), `validateExtends` (`:112-122`), `refusalOf` (`:147-149`: "An executable-field or sandbox-profile refusal passes through"), `refusedRecordError` (`:167-176`: `errors.As` reaches `*layer.ExecutableInheritance` for an executable-field refusal, and `errors.Is` reaches `layer.ErrSandboxProfileConflict` for a sandbox-profile refusal), and `mergeRecord` (`:290-298`: every edge is held to the §4.6 sandbox-profile rule, and an edge whose parent comes from another layer is also held to the executable-field rule, checked second).
- (b) `pkg/registry/filesystem/walk.go`: the `CollisionPolicyDrop` comment (`:161-166`) and the `WalkOptions.OnDrop` comment (`:179-183`) name a record the sandbox-profile rule refuses beside the executable-field refusal.
- (c) `pkg/sync/sync.go:576-577`: the comment names the sandbox-profile rule.

No code change is needed in `pkg/sync` or `cmd/podium`; only the comment at `pkg/sync/sync.go:576-577` changes. `podium sync` already prints each `layer.Drop` as `rejected: <id> (<code>): <reason>` and exits 1 (`cmd/podium/layer.go:607`).

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| A child sets `read-only-fs` and its parent in another layer resolves to `network-isolated` or `seccomp-strict` | Ingest rejects the child with `ingest.invalid_artifact`, `details` `{"constraint": "sandbox_profile_conflict"}`, and a reason naming neither value nor the parent; the child is not stored | §4.6 "Ingest rejects a child with `ingest.invalid_artifact` (§6.10) when the child's own frontmatter sets a restricting `sandbox_profile`" (SPEC-1); `docs/authoring/extends.md` *Sandbox profile*, `docs/reference/error-codes.md`, `docs/reference/http-api.md` (DOC-1) |
| The same pair within one layer | Rejected the same way on the server; dropped by a filesystem sync | §4.6 "The rule tests every `extends:` edge, whether the pinned parent version (§4.7.6) comes from the child's layer or from another layer" (SPEC-1); §13.11.3 *Sandbox profile conflicts* (SPEC-3); `docs/authoring/extends.md`, `docs/reference/cli.md` (DOC-1) |
| Parent unset or `unrestricted`, child sets a restricting value | Accepted; the child's value is served | §4.6 "the child's value when the child sets one and the parent's resolved value is unset or `unrestricted`" (SPEC-1); `docs/authoring/extends.md` *Tightening sandbox profile* (DOC-1 item 6) |
| Child unset or `unrestricted`, parent restricting | Accepted; the parent's value is served | §4.6 "it cannot loosen a restriction the parent sets" (SPEC-1); `docs/authoring/extends.md:120` (DOC-1 item 3) |
| Child and parent set the same restricting value | Accepted; that value is served | §4.6 "the two values differ" (SPEC-1); `docs/authoring/extends.md` *Sandbox profile* (DOC-1 item 2) |
| A grandparent sets `network-isolated`, the parent omits the field, and the child sets `read-only-fs` | The child is rejected | §4.6 "a value the parent inherited from its ancestors counts" (SPEC-1); `docs/authoring/extends.md` *Sandbox profile* (DOC-1 item 2) |
| `network-isolated` against `seccomp-strict` | Rejected, although the shipped baseline blocks network syscalls (D-5) | §4.6 "including a pair where the baseline that §4.4.1 names for one profile also blocks the resource the other restricts" (SPEC-1); `docs/authoring/extends.md` *Sandbox profile* (DOC-1 item 2) |
| A value §4.4.1 does not list, against a different restricting value | Rejected | §4.6 "including a value §4.4.1 does not list" (SPEC-1); `CHANGELOG.md` (DOC-2) |
| A value §4.4.1 does not list, under an unset or `unrestricted` parent | Accepted and served as authored; a host that does not list the value refuses the load (D-4). Enum validation is deferred | §4.6 (SPEC-1); §4.4.1 "hosts without it MUST refuse to materialize" (unchanged); `CHANGELOG.md` (DOC-2) |
| A version stored before the upgrade whose chain pairs two conflicting values | Keeps its pin, and the registry serves the parent's value. For child `network-isolated` or `seccomp-strict` over parent `read-only-fs`, and child `seccomp-strict` over parent `network-isolated`, the served profile changes from the child's to the parent's. Accepted (D-7, Q-1) | §4.6 "the registry keeps serving it with the merged value the first paragraph of this rule defines" (SPEC-1); `docs/authoring/extends.md` *Sandbox profile*, `CHANGELOG.md` (DOC-1, DOC-2) |
| A `podium-mcp` whose content cache holds that stored version from before the upgrade | The HEAD revalidation in `always-revalidate` reports the unchanged content hash, so `podium-mcp` delivers the cached frontmatter and applies the child's earlier profile; `offline-first` and the degraded-network fallback deliver the same record, and `offline-only` delivers it indefinitely because it never calls the registry. A cold cache, `podium sync`, and a direct HTTP load receive the parent's value. `podium cache prune --days 0` against the consumer's cache directory ends it in `always-revalidate` and `offline-first`, where the next load that reaches the registry fetches the merged record. A pruned `offline-only` consumer returns `network.offline_cache_miss` for every load from that cache until the cache is refilled in `always-revalidate` or `offline-first`. A new non-conflicting child version ends it only for an `always-revalidate` load of `latest` that reaches the registry, and leaves pinned loads, `offline-first`, `offline-only`, and the degraded-network fallback on the cached record. Accepted (D-7) | §6.5 "Content cache. Maps `content_hash` to manifest bytes + bundled resources. Forever (immutable by definition)." (unchanged); `docs/authoring/extends.md` *Sandbox profile*, `CHANGELOG.md` (DOC-1 item 2, DOC-2) |
| That stored version presented again with unchanged bytes | Classified unchanged; no rejection | §4.6 "a cycle that presents it with unchanged content classifies it unchanged (§7.3.1)" (SPEC-1); `docs/authoring/extends.md` (DOC-1 item 2) |
| A new version of that child that still conflicts | Rejected; the stored version keeps serving | §4.6 "applies to the version being ingested" (SPEC-1); `docs/authoring/extends.md` (DOC-1 item 2) |
| A filesystem sync over a directory whose conflict a server stored before the upgrade | Sync drops the child on every run while the server keeps serving the stored version. This mode difference is accepted, as 0057 accepted it for executable fields | §4.6 "A filesystem-source `podium sync` applies the rule on every run (§13.11.3)" (SPEC-1); §13.11.3 (SPEC-3); `docs/authoring/extends.md` (DOC-1 item 2) |
| A filesystem drop strands a same-layer grandchild whose parent ID no other layer contributes | The grandchild is dropped with `ingest.invalid_artifact` and the unresolved-parent reason | §13.11.3 "as the executable-inheritance paragraph states for that rule" (SPEC-3); `docs/reference/cli.md` (DOC-1 item 9) |
| A child that both the sandbox-profile and the executable-field rule refuse | Reported once, under the sandbox-profile rule, in both modes | §4.6 "A child that both this rule and the executable-field rule refuse is rejected under this rule" (SPEC-1); §13.11.3 "When an artifact fails both rules, sync reports the sandbox-profile refusal" (SPEC-3); no docs page states the precedence |
| A same-layer child that sets a restricting profile, whose stored parent chain is missing an ancestor or loops | Rejected with `ingest.invalid_artifact` and `extends: parent chain does not resolve`, with no `details`. This rejection is new on a same-layer edge | §4.6 "When the registry needs the parent's chain to evaluate the rule and cannot read it" (SPEC-1); `CHANGELOG.md` (DOC-2) |
| The same layout with a child that omits `sandbox_profile` or sets `unrestricted` | Accepted as today; the chain is not read on this edge | §4.6 (SPEC-1, the rule needs no chain for this child); `CHANGELOG.md` (DOC-2) |
| A store fault while reading a same-layer restricting child's parent chain | The reingest call fails with `registry.unavailable`, and the message `ingest: resolve extends chain for <child>: store read failed` names no ancestor | §7.3.1 "A cycle the registry answers with a §6.10 error reports that error envelope instead" (unchanged); `docs/reference/cli.md` (unchanged) |
| The filesystem resolver and the server select different parent records for a same-ID overlay | The two modes test the rule on different edges. Accepted (D-10) | §13.11.3 "The parent is the artifact that the executable-inheritance paragraph names" (SPEC-3); `docs/reference/cli.md` (unchanged) |
| A filesystem sync under `--dry-run`, `--config`, or `podium sync override` drops a child under the rule | Exit 1 and a failed target, as for a collision drop | §13.11.3 (SPEC-3 delegates to the executable-inheritance paragraph); `docs/reference/cli.md` (DOC-1 item 9) |
| With `PODIUM_ENFORCE_SANDBOX_PROFILE=true`, a child that omits `sandbox_profile` inherits a profile the host cannot enforce | Ingest accepts it, because the §13.10 gate judges the child's authored value; the MCP host gate refuses the load. This case is deferred as a non-goal | §13.10 "the registry does not refuse to ingest artifacts whose profiles can't be enforced locally" (unchanged); `docs/authoring/extends.md` *Sandbox profile* (DOC-1 item 2 stages the sentence) |
| A consumer loads several artifacts with different profiles in one session | Each artifact's profile applies when its bundled scripts run; no profile stands in for another | No spec text defines a session composition; `docs/consuming/handling-artifact-responses.md` (DOC-1 item 10) |
| `podium lint` against a child the rule would refuse | Reports nothing for the rule | `docs/authoring/extends.md` Lint behavior (DOC-1 item 5) |

## Testing

**New and extended tests.**

- **TEST-5, fixture updates ahead of the merge change** (unit, e2e). Each edit passes before and after CODE-1.
  - `pkg/manifest/merge_test.go` `TestMergeExtends_FieldSemanticsTable`: set the parent's `SandboxProfile` (`:22`) to `SandboxUnrestricted`, keep the `seccomp-strict` child (`:44`) and expectation (`:91`), and split the `:87` comment so that `sandbox_profile` reads "the parent's value unless it is unset or unrestricted" and `sensitivity` stays most-restrictive.
  - `test/e2e/artifact_extends_test.go` `TestExtends_SandboxChildTightens`: change the parent to `sandbox_profile: unrestricted` and keep the child as `seccomp-strict`, so the test mirrors `docs/authoring/extends.md:306-322`. Rewrite its doc comment so it says the child's profile applies because the parent's is `unrestricted`, and drop "most-restrictive wins".
  - The doc comment of `TestExtends_SandboxChildCannotWiden` cites `docs/authoring/extends.md` § "Sandbox profile" in place of § "The most-restrictive rules". The anchor exists after DOC-1, which lands in the same pull request.
  - Keep `TestMergeExtends_MostRestrictiveCannotRelax` (`pkg/manifest/merge_test.go:164-178`) unchanged; it passes under D-1.
- **TEST-1, `pkg/manifest/merge_test.go` and `pkg/layer/sandbox_test.go`** (unit).
  - `TestMergeSandboxProfile_Pairs` (`// Spec: §4.6`): a table over `{"", unrestricted, read-only-fs, network-isolated, seccomp-strict, "custom"}`² asserting `mergeSandboxProfile` and `SandboxProfilesConflict` per cell, with conflicting cells expecting the parent's value. In the same loop, assert `mergeSandboxProfile(p, mergeSandboxProfile(p, c)) == mergeSandboxProfile(p, c)` and `!SandboxProfilesConflict(p, mergeSandboxProfile(p, c))`, the D-9 properties CODE-4's rewrite pass relies on.
  - One conflicting pair driven through `MergeExtends` (parent `network-isolated`, child `read-only-fs`, expect `network-isolated`), so the public fold is pinned and not only the helper.
  - `pkg/layer/sandbox_test.go` (`// Spec: §4.6, §7.3.1`): assert the exact text of `SandboxProfileConflictReason`, that it begins `extends: `, and that it contains none of `read-only-fs`, `network-isolated`, and `seccomp-strict`, which pins the D-6 non-disclosure property as `pkg/layer/executable_test.go:27-29` does for the executable reason. Assert `SandboxProfileConflictConstraint == "sandbox_profile_conflict"`, that `ErrSandboxProfileConflict.Error()` equals the reason, and that `errors.Is(fmt.Errorf("x: %w", ErrSandboxProfileConflict), ErrSandboxProfileConflict)` is true, which CODE-4 relies on. Do not re-assert `InvalidArtifactCode`; `TestExecutableSharedReasons` already pins it.
- **TEST-2, `pkg/registry/ingest/sandbox_conflict_test.go` and `test/integration/reingest_collision_reason_test.go`** (integration).
  - Ingest cases (`// Spec: §4.6`, package `ingest_test`). Reuse `execAgent`, `seedChain`, `chainFaultStore`, `ingestSnapshot`, `assertNotStored`, and `loadMerged` from `executable_inheritance_test.go`.
    - A same-layer conflict and a cross-layer conflict are each rejected with code `ingest.invalid_artifact`, `Reason` equal to `layer.SandboxProfileConflictReason` and containing neither restricting profile string nor the parent ID, `Details` equal to exactly `{"constraint": "sandbox_profile_conflict"}`, and no stored child (`assertNotStored`).
    - Each of these is accepted: an unrestricted or unset parent with a restricting child, an unset or `unrestricted` child under a restricting parent, and equal values. The served value matches D-1 in each case.
    - A grandparent's `network-isolated`, inherited through an unset parent, conflicts with a child's `read-only-fs`.
    - An unknown child value under a known restricting parent value is rejected.
    - A cross-layer child that fails both rules reports `sandbox_profile_conflict`.
    - A same-layer child that sets a restricting profile, whose stored parent chain is missing an ancestor (`chainFaultStore` with `store.ErrNotFound`), is rejected with the reason `extends: parent chain does not resolve` and nil `Details`. A same-layer child whose stored chain loops gets the same rejection.
    - Gate case: a same-layer child with an unset or `unrestricted` profile over a parent whose stored chain is missing an ancestor is accepted, which pins the CODE-3 gate.
    - A store fault during a same-layer restricting child's walk aborts `Ingest`. The test checks `errors.Is(err, boom)` and the exact message `ingest: resolve extends chain for <id>: store read failed`, because `chainReadError` is unexported.
    - A conflicting child written straight into the store, with the scratch-store `PutManifest` pattern from `TestIngest_ExecutableInheritanceStoredViolatorIsNotRejudged`, re-ingests as `Idempotent` with no rejection, and `loadMerged` serves the parent's `sandbox_profile` in `Frontmatter`.
    - Same-layer fixtures name the parent so it sorts before the child, and the fixture comment states why.
  - Reingest wire case (`// Spec: §4.6, §7.3.1`): add `TestReingest_SandboxProfileConflictDetails` to `test/integration/reingest_collision_reason_test.go`. It drives a real reingest through `rcReingest`, with a child whose `sandbox_profile` conflicts with a parent in `rcHiddenLayer`. It asserts code `ingest.invalid_artifact`, `details` exactly `{"constraint": "sandbox_profile_conflict"}`, and that neither the reason, the `details`, nor the response body names the parent ID, `rcHiddenLayer`, or either profile value.
- **TEST-3, `pkg/registry/filesystem/extends_sandbox_test.go` and `test/integration/extends_equivalence_test.go`**.
  - Filesystem cases (unit, `// Spec: §4.6, §13.11.3`). Reuse `walkDrops` and the fixture helpers from `extends_executable_test.go`.
    - (a) A same-layer conflict (parent `read-only-fs`, child `network-isolated`, both in `l1`) is dropped. The test asserts `layer.Drop{ArtifactID: <child>, Code: layer.InvalidArtifactCode, Reason: layer.SandboxProfileConflictReason}`, and that the parent is kept.
    - (b) A same-layer cascade: a grandchild in the same layer extends the dropped ID, and no other layer holds a copy. The grandchild is dropped with `layer.UnresolvedParentReason(<child>)`, and the drops are listed in ID order. The cross-layer cascade and the retracted-collision cases are already pinned by the `TestWalk_ExecutableInheritance*` tests through the same machinery.
    - (c) A cross-layer conflict that also fails the executable-field rule reports the sandbox-profile drop, which pins the D-8 and D-9 precedence.
    - (d) With `OnDrop` nil, the walk fails and `errors.Is(err, layer.ErrSandboxProfileConflict)` is true.
    - (e) A three-level chain settles with no refusal and serves `read-only-fs`: the root is `unrestricted`, the parent is `read-only-fs`, and the child leaves the field unset. A second chain does the same and serves `network-isolated`: the root is `network-isolated`, the parent leaves the field unset, and the child is `network-isolated`. In both chains the parent comes before the child in walk order (for example IDs `a/parent` and `b/child` in one layer), so `resolveExtends` reads the rewritten parent. A comment in the test names this order as the reason.
  - Equivalence cases (integration, materialization, `// Spec: §11, §2.2, §4.6, §13.11.3`): add cases to the `TestSyncEquivalence_ExecutableInheritanceDropIsByteIdentical` table, or a sibling table, using the existing `syncBothModes` harness (`server.NewFromFilesystem`) and no standalone binary.
    - Cover a same-layer conflict and a cross-layer conflict. Each case asserts byte-identical materialized trees and lock entries, that `fsRes.Dropped` equals the expected sandbox drop, and that `srvRes.Dropped` is empty, as the existing cases do.
    - Name same-layer IDs so the parent sorts before the child, because server ingest resolves pins in canonical-ID order (`pkg/registry/ingest/ingest.go:1052`), and state that constraint in the fixture comment.
    - Do not assert the server's rejection reason here. TEST-2 asserts the server `Reason` and `Details`, and both modes take the reason from `layer.SandboxProfileConflictReason`.
- **TEST-4, end-to-end, the `podium-mcp` warm cache, and the manual scenario** (unit, e2e, manual).
  - (b) `test/e2e/artifact_extends_test.go`, new `TestExtends_SandboxConflictRejected` (`// Spec: §4.6`). `extendsBoot` with a parent `read-only-fs` at 1.0.0 and a same-ID child `network-isolated` at 2.0.0. Before this change `sandboxRank` served `network-isolated` at 2.0.0. The test asserts:
    - `exLoad` returns version 1.0.0 and a frontmatter that contains `read-only-fs` and does not contain `network-isolated`.
    - `srv.log()` contains `rejected=1`.
    - `podium layer reingest --registry <url> team-foo` exits 1 and prints `rejected: finance/ap/pay-invoice (ingest.invalid_artifact): extends: sandbox_profile differs`.
    - `cliNotContains` confirms that standard error holds none of `read-only-fs`, `network-isolated`, `org-defaults`, `1.0.0`, or `@1.` (D-6).
  - (c) `test/e2e/filesystem_sync_test.go`, new `TestFilesystemSync_SandboxProfileConflictDropsChild` (`// Spec: §13.11.3`), modelled on `TestFilesystemSync_ExecutableInheritanceDropsChild`. Its fixture is a same-layer conflict, the new edge class: one layer holds a parent with `read-only-fs`, a child that extends it with `network-isolated`, and an unrelated sibling. Run the one-shot arm with `--harness none`. It asserts exit 1, the standard-error line `rejected: <child> (ingest.invalid_artifact): extends: sandbox_profile differs`, no restricting profile value in standard error, the parent and the sibling materialized, the child absent from the target, and the child absent from `sync.lock`. The dry-run and `--config` arms need no repeat, because they share the drop path the existing test pins. Run with `-v` and grep for `SKIP` before claiming verification.
  - (d) Scenario S96 in `test/manual-validation.md`, staged under *Manual validation* below.
  - (e) `cmd/podium-mcp/head_revalidate_test.go`, new `TestLoadArtifact_HeadMatchServesCachedSandboxProfile` (`// Spec: §6.5, §4.6`), modelled on `TestLoadArtifact_AlwaysRevalidate_HeadMatchServesCache` (`:20`). It pins the D-7 warm-cache behavior. `primeCachedRecord` caches a record for `ops/tool` whose frontmatter carries `extends: ops/base` and `sandbox_profile: seccomp-strict`, the merge the removed ranking produced for a `seccomp-strict` child over a `read-only-fs` parent, and `PutVersion` records its content hash for `1.0.0`. The stub registry answers HEAD with that content hash and fails any GET. The server runs in `always-revalidate` with `verifyPolicy: sign.PolicyNever` as the model test does, `hostSandboxes` listing `unrestricted` and `seccomp-strict`, and a `t.TempDir()` materialize root. The test asserts that the load succeeds with no GET, that `materialized_at` lists `<root>/.podium/seccomp-strict.json`, and that `<root>/ops/tool/ARTIFACT.md` carries `sandbox_profile: seccomp-strict`. It passes before and after this change, because the change edits no `podium-mcp` code. A later change that makes the load validator cover the merged record fails it, and that change updates D-7's documented behavior in the docs and the CHANGELOG with it.

**Existing tests to confirm unchanged.** Confirm each of the following passes unchanged: `pkg/registry/ingest/executable_inheritance_test.go` (including the same-layer accepts at `:101` and `:325`, which the CODE-3 gate keeps on today's path), `pkg/registry/ingest/extends_conformance_test.go`, `pkg/registry/filesystem/extends_executable_test.go`, `pkg/registry/filesystem/extends_test.go`, `test/integration/sandbox_profile_ingest_test.go`, `test/e2e/sandbox_ingest_gate_test.go`, `test/integration/extends_equivalence_test.go`, and the golden files under `test/materialization/testdata/golden/` (no regeneration; no golden fixture pairs `extends:` with `sandbox_profile`).

**Commands per step.**

- S1: `make coverage-gate`.
- S2: `go test ./pkg/manifest/` and `go test -v ./test/e2e/ -run 'Extends_Sandbox'`, with no `SKIP` on the edited tests.
- S3, S4: `gofmt -l pkg/manifest pkg/layer` prints nothing, then `go test ./pkg/manifest/ ./pkg/layer/`.
- S5: `go build ./...`, `go test ./pkg/registry/... ./pkg/sync/ ./cmd/podium/ ./test/integration/`, and `go test -v ./test/e2e/ -run 'Extends|FilesystemSync'`.
- S6 to S9: `go test -coverpkg=./... -coverprofile=cover.out ./pkg/manifest/ ./pkg/layer/ ./pkg/registry/... ./pkg/sync/ ./test/integration/`, then `go tool cover -func=cover.out`, and confirm `mergeSandboxProfile`, `SandboxProfilesConflict`, `extendsEdgeRejection`, and `mergeRecord` reach 85%. For S9, `go test ./cmd/podium-mcp/ -run HeadMatchServesCachedSandboxProfile`, then `GOCOVERDIR=$(mktemp -d) go test -v ./test/e2e/ -run 'SandboxConflict|SandboxProfileConflict'`, then `go tool covdata textfmt` and `go tool cover -func`.
- Closing step: `make lint` and `make coverage-gate`.

## Manual validation

TEST-4 (d) appends scenario S96 to `test/manual-validation.md`, after S95, and adds its index row after the S95 row (about `:246`):

```markdown
| S96 | An extends: child whose sandbox_profile conflicts with its parent's is refused in both modes, and a stored version keeps serving | solo then standalone | none | none | none |
```

The scenario uses the S91 format for the refusals and the S95 method for the pre-rule state. Port 8198 is unused by other scenarios (Watch out for: renumber on a collision).

````markdown
## S96: An extends: child whose sandbox_profile conflicts with its parent's is refused in both modes, and a stored version keeps serving

**Goal.** Validate that a filesystem-source `podium sync` drops an `extends:`
child whose `sandbox_profile` conflicts with its parent's, within one layer
and across layers, that a reingest refuses a new conflicting version with
`details.constraint: "sandbox_profile_conflict"`, that neither reason names a
profile value, that a conflicting child an earlier release stored keeps
serving, now with the parent's profile, and that a `podium-mcp` whose cache
holds that child from before the upgrade keeps its earlier profile until
`podium cache prune` clears the cache.

**Covers.** §4.6 sandbox profile, §7.3.1 ingest outcome, §13.11.3 sandbox
profile conflicts, §4.7.6 pin immutability, and the §6.5 content cache,
through the compiled binaries.

**Why by hand.** An operator reads the served frontmatter and the `rejected:`
lines directly and gates CI on `$?`. A served `seccomp-strict` for
`ops/tool` after the upgrade, a `rejected:` line on the unchanged reingest, a
reason that names `read-only-fs`, `network-isolated`, or `seccomp-strict`, or
`exit=0` from the filesystem sync is the defect this scenario catches. The
warm-cache loads confirm the upgrade note the CHANGELOG gives operators.

**Prerequisites.** Built `podium` and `podium-mcp` binaries on `PATH`, a Go
toolchain, and a checkout whose history holds the `v0.5.2` tag, a release
without the rule.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then build
   the `v0.5.2` `podium` binary into `$WORK/old`.

   ```bash
   mkdir -p "$WORK/src" "$WORK/old"
   git -C "$REAL_HOME/projects/podium" archive v0.5.2 | tar -x -C "$WORK/src"
   go -C "$WORK/src" build -o "$WORK/old/" ./cmd/podium
   "$WORK/old/podium" version
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and the old binary
   prints `podium 0.5.2`.

2. Build a two-layer registry. `org` holds `ops/base` with `read-only-fs`.
   `team` holds `ops/tool`, which extends `ops/base` with `seccomp-strict`, and
   the same-layer pair `ops/a-parent` (`read-only-fs`) and `ops/b-child`
   (`network-isolated`, extends `ops/a-parent`). The parent's ID sorts before
   the child's.

   ```bash
   mkdir -p "$WORK/reg/org/ops/base" "$WORK/reg/team/ops/tool" \
     "$WORK/reg/team/ops/a-parent" "$WORK/reg/team/ops/b-child"
   printf 'multi_layer: true\nlayer_order:\n  - org\n  - team\n' > "$WORK/reg/.registry-config"
   for L in org team; do
     printf 'visibility:\n  public: true\n' > "$WORK/reg/$L/.layer-config"
   done
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: Org base.\nsandbox_profile: read-only-fs\n---\n\nOrg base.\n' \
     > "$WORK/reg/org/ops/base/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: Team tool.\nsandbox_profile: seccomp-strict\nextends: ops/base\n---\n\nTeam tool.\n' \
     > "$WORK/reg/team/ops/tool/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: Team parent.\nsandbox_profile: read-only-fs\n---\n\nTeam parent.\n' \
     > "$WORK/reg/team/ops/a-parent/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: Team child.\nsandbox_profile: network-isolated\nextends: ops/a-parent\n---\n\nTeam child.\n' \
     > "$WORK/reg/team/ops/b-child/ARTIFACT.md"
   ```

   **Expect.** The four `ARTIFACT.md` files exist.

3. Sync through the filesystem source with the build under test.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none 2> "$WORK/err.txt"; echo "exit=$?"
   cat "$WORK/err.txt"
   grep -c -e read-only-fs -e network-isolated -e seccomp-strict "$WORK/err.txt"
   ls "$WORK/fs/ops"
   ```

   **Expect.** The sync lists `ops/base [org]` and `ops/a-parent [team]` and
   prints `exit=1`. `$WORK/err.txt` holds two lines, `ops/b-child` first:
   `rejected: ops/b-child (ingest.invalid_artifact): extends: sandbox_profile differs from the profile the parent sets, and neither is unrestricted; omit sandbox_profile to inherit the parent's profile`,
   then the same line for `ops/tool`. `grep -c` prints `0`. `ls` prints
   `a-parent` and `base` alone. A missing `ops/b-child` line means the
   same-layer edge is not tested.

4. Serve the directory with the old binary, reingest `team`, load both
   children, warm a `podium-mcp` cache with `ops/tool`, and stop the server.

   ```bash
   "$WORK/old/podium" serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8198 > "$WORK/old.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8198/healthz
   server_alive "$SRV" "$WORK/old.log"
   export URL=http://127.0.0.1:8198
   "$WORK/old/podium" layer reingest --registry "$URL" team; echo "exit=$?"
   for ID in ops/tool ops/b-child; do
     curl -s "$URL/v1/load_artifact?id=$ID" \
       | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["version"], [l for l in d["frontmatter"].splitlines() if l.startswith("sandbox_profile")])'
   done
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"ops/tool"}}}'
   mcp_load() {
     printf '%s\n%s\n' "$INIT" "$LOAD" | PODIUM_REGISTRY="$URL" PODIUM_CACHE_DIR="$1" \
       PODIUM_HARNESS=none PODIUM_MATERIALIZE_ROOT="$2" \
       PODIUM_HOST_SANDBOXES=unrestricted,read-only-fs,network-isolated,seccomp-strict \
       podium-mcp > /dev/null 2> "$2.err"
     grep '^sandbox_profile' "$2/ops/tool/ARTIFACT.md"; ls "$2/.podium" 2>/dev/null
   }
   mcp_load "$WORK/mcp-warm" "$WORK/out-old"
   kill "$SRV"; wait "$SRV" 2>/dev/null
   ```

   **Expect.** The reingest prints no `rejected:` line and `exit=0`. The loads
   print `1.0.0 ['sandbox_profile: seccomp-strict']` for `ops/tool` and
   `1.0.0 ['sandbox_profile: network-isolated']` for `ops/b-child`: the old
   release ranked the child's profile above the parent's `read-only-fs`. A
   `rejected:` line here means the old binary already carries the rule, and the
   later steps prove nothing. `mcp_load` prints `sandbox_profile: seccomp-strict`
   and `seccomp-strict.json`, and `$WORK/mcp-warm` now holds the record the old
   release merged. A missing `ARTIFACT.md` means the load failed; read
   `$WORK/out-old.err`.

5. Start the build under test on the same store and directory, reingest the
   unchanged `team` layer, load both children, and load `ops/tool` through
   `podium-mcp` with the warm cache, with a cold cache, and with the warm cache
   after a prune.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8198 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8198/healthz
   server_alive "$SRV" "$WORK/srv.log"
   grep 'ingested layer' "$WORK/srv.log"
   podium layer reingest --registry "$URL" team; echo "exit=$?"
   for ID in ops/tool ops/b-child; do
     curl -s "$URL/v1/load_artifact?id=$ID" \
       | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["version"], [l for l in d["frontmatter"].splitlines() if l.startswith("sandbox_profile")])'
   done
   mcp_load "$WORK/mcp-warm" "$WORK/out-warm"
   mcp_load "$WORK/mcp-cold" "$WORK/out-cold"
   podium cache prune --dir "$WORK/mcp-warm" --days 0
   mcp_load "$WORK/mcp-warm" "$WORK/out-pruned"
   ```

   **Expect.** The `ingested layer` lines report `rejected=0` for `org` and
   for `team`. The reingest prints no `rejected:` line and `exit=0`. Both loads
   print `1.0.0 ['sandbox_profile: read-only-fs']`. The stored versions keep
   serving, and the merge now yields the parent's profile for a conflicting
   pair. `seccomp-strict` or `network-isolated` here means the build under test
   still ranks the profiles.

   The warm-cache `mcp_load` prints `sandbox_profile: seccomp-strict` and
   `seccomp-strict.json`: the HEAD revalidation matches the unchanged content
   hash, and `podium-mcp` delivers the record it cached in step 4, as D-7
   accepts and the CHANGELOG states. The cold-cache load and the load after the
   prune each print `sandbox_profile: read-only-fs` and no `.podium` entry. A
   warm-cache `read-only-fs` means the load validator now covers the merged
   record, and the docs and the CHANGELOG need the matching update.

6. Publish a new version of `ops/tool` that still sets `seccomp-strict`,
   reingest, and read the reingest body.

   ```bash
   printf -- '---\ntype: context\nversion: 1.1.0\ndescription: Team tool.\nsandbox_profile: seccomp-strict\nextends: ops/base\n---\n\nTeam tool.\n' \
     > "$WORK/reg/team/ops/tool/ARTIFACT.md"
   podium layer reingest --registry "$URL" team 2> "$WORK/re.txt"; echo "exit=$?"
   cat "$WORK/re.txt"
   curl -s -X POST "$URL/v1/layers/reingest?id=team" > "$WORK/body.json"
   python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1]))["rejected"]))' "$WORK/body.json"
   grep -c -e read-only-fs -e network-isolated -e seccomp-strict "$WORK/body.json"
   curl -s "$URL/v1/load_artifact?id=ops/tool" | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])'
   ```

   **Expect.** `$WORK/re.txt` holds
   `rejected: ops/tool (ingest.invalid_artifact): extends: sandbox_profile differs from the profile the parent sets, and neither is unrestricted; omit sandbox_profile to inherit the parent's profile`,
   and the reingest prints `exit=1`. The `rejected` entry carries
   `"details": {"constraint": "sandbox_profile_conflict"}` and no other
   `details` member, `grep -c` prints `0`, and the load prints `1.0.0`. The
   refusal is the control: the build under test runs the rule, and only the
   versions stored before it are spared.

7. Follow the reason: omit `sandbox_profile` from `ops/tool` 1.1.0, reingest,
   and load it.

   ```bash
   printf -- '---\ntype: context\nversion: 1.1.0\ndescription: Team tool.\nextends: ops/base\n---\n\nTeam tool.\n' \
     > "$WORK/reg/team/ops/tool/ARTIFACT.md"
   podium layer reingest --registry "$URL" team; echo "exit=$?"
   curl -s "$URL/v1/load_artifact?id=ops/tool" \
     | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["version"], [l for l in d["frontmatter"].splitlines() if l.startswith("sandbox_profile")])'
   ```

   **Expect.** The reingest prints no `rejected:` line for `ops/tool` and
   `exit=0`, and the load prints `1.1.0 ['sandbox_profile: read-only-fs']`.

8. Clean up.

   ```bash
   kill "$SRV"; rm -rf "$WORK"
   ```
````

## Documentation changes

### DOC-1. Authoring and reference docs

Make the minimal set of edits below. Every other target stays as written.

1. The merge-table rows at `docs/authoring/extends.md:110`, `docs/authoring/frontmatter-reference.md:286`, and `docs/reference/frontmatter-schema.md:186` become: "Scalar; the parent's value unless it is unset or `unrestricted`. A child that sets a different restricting profile is rejected; see [Sandbox profile](...#sandbox-profile)." In `frontmatter-reference.md` and `frontmatter-schema.md`, the link targets `../authoring/extends.md#sandbox-profile`.
2. In `docs/authoring/extends.md`, add one new section, `## Sandbox profile`, after `## Executable fields across layers` and before `## Bundled files under extends`. It is the only place in the docs that defines the rule. It covers:
   - the merge;
   - the refusal on every `extends:` edge, including the same layer and a same-ID overlay, and that a value the parent inherited from its own chain counts;
   - `ingest.invalid_artifact` with `details.constraint: "sandbox_profile_conflict"`, where neither the reason nor `details` names a profile value or the parent;
   - the remedy, which is to omit the field to inherit the parent's profile;
   - the deliberate refusal of `network-isolated` against `seccomp-strict`;
   - stored versions that the registry keeps serving with the parent's value, and `podium sync` dropping on every run;
   - that a `podium-mcp` whose content cache holds such a stored version from before the upgrade keeps delivering the profile it cached, because the content hash does not change, until `podium cache prune --days 0` clears that cache directory, and that publishing a new version of the child ends it only for an `always-revalidate` load of `latest` that reaches the registry, while a pinned load, `offline-first`, and the degraded-network fallback keep the cached record until the prune, and that a consumer in `PODIUM_CACHE_MODE=offline-only` keeps the cached record indefinitely and, once pruned, returns `network.offline_cache_miss` for every load from that cache, so the operator clears its cache directory and refills it in `always-revalidate` or `offline-first` (D-7); and
   - that the opt-in `PODIUM_ENFORCE_SANDBOX_PROFILE` ingest gate judges the profile the child's own frontmatter sets, and a profile the child inherits is checked when a host loads the artifact.
3. Reword `docs/authoring/extends.md:120` as a whole: "The most-restrictive rules for `sensitivity` and `search_visibility`, and the [sandbox-profile rule](#sandbox-profile), keep a child from loosening a security-relevant field. A parent at `sensitivity: medium` cannot be relaxed to `low`, and a parent with `sandbox_profile: read-only-fs` cannot be widened to `unrestricted`." Do not add a separate conflict sentence here.
4. At `docs/authoring/extends.md:140`, change "`sandbox_profile` constrains execution." to "`sandbox_profile` constrains execution, and the [sandbox-profile rule](#sandbox-profile) governs its merge."
5. In the "Lint behavior" list at `docs/authoring/extends.md:328-337`, add: "Child whose `sandbox_profile` differs from the profile its parent resolves to, neither `unrestricted`: ingest error (`ingest.invalid_artifact`). Ingest and `podium sync` evaluate this rule, and `podium lint` does not."
6. At `docs/authoring/extends.md:322`, replace "(most-restrictive wins)" with "because the parent's profile is `unrestricted`".
7. Extend the `ingest.invalid_artifact` row at `docs/reference/error-codes.md:113` by one clause naming the sandbox-profile conflict, its `details.constraint: "sandbox_profile_conflict"`, and the matching filesystem-sync drop.
8. In `docs/reference/http-api.md`, after the sentence "The reason and `details` name no parent ID, version, or layer." (about `:524`), add one sentence: "A child rejected under the §4.6 sandbox-profile rule carries `details: {\"constraint\": \"sandbox_profile_conflict\"}` and names no profile value." Do not add a second JSON example.
9. At `docs/reference/cli.md:241` and `:288`, name the sandbox-profile drop beside the executable-field drop, and state that it applies within one layer as well as across layers. The existing paragraph frames its rule as cross-layer only.
10. Replace the `sandbox_profile` row at `docs/consuming/handling-artifact-responses.md:195` with: "Keep each artifact's profile and apply it when that artifact's bundled scripts run (see the per-artifact rule below). The profiles restrict different resources, so no profile stands in for another." Drop the invented total order and do not suggest enforcing a session-wide combination of profiles. The per-artifact rule is at `:272`.

Leave the ASCII fallback at `docs/authoring/extends.md:94` unchanged: it sits inside an HTML comment, and its statement that security fields never loosen stays accurate under D-1. Follow `.claude/rules/doc-style.md` throughout. No new runnable block is added, so `tools/doccov` needs no manifest entry.

### DOC-2. CHANGELOG

Add under `## [Unreleased]` `### Changed`, as a separate bullet directly after the 0057 executable-field entry:

```markdown
- **An `extends:` child and its parent cannot set two different restricting sandbox profiles** (§4.6, §7.3.1, §13.11.3): the merge no longer ranks the §4.4.1 profiles, because each one restricts a different resource. A child's `sandbox_profile` applies when the parent's is unset or `unrestricted`, and the parent's applies otherwise. Ingest rejects a new version whose `sandbox_profile` differs from the profile its parent resolves to when neither is `unrestricted`, on every `extends:` edge including a same-layer one. The rejection uses `ingest.invalid_artifact`, and its reingest `rejected` entry carries `details.constraint: "sandbox_profile_conflict"`. Neither the reason nor `details` names either profile. Ingest now also reads the stored parent chain of a same-layer `extends:` child that sets a restricting `sandbox_profile`, so such a child whose chain is missing an ancestor or loops is rejected with the unresolved-chain reason. A value that §4.4.1 does not list now counts as a restricting profile. Under an unset or `unrestricted` parent it is served as authored, where it used to be served as the parent's value, and a host that does not list it refuses the load. A filesystem-source `podium sync` applies the rule on every run, drops each child the rule refuses, and reports the drop as `rejected: <id> (ingest.invalid_artifact): <reason>`. A version stored before this change keeps its pin, and the registry serves it with the parent's profile. Before this change a stored child that set `network-isolated` or `seccomp-strict` over a parent's `read-only-fs`, or `seccomp-strict` over a parent's `network-isolated`, was served its own profile. A `podium-mcp` whose content cache already holds such a version keeps delivering the profile it cached, because the version's content hash does not change; run `podium cache prune --days 0` against that cache directory so the next load fetches the parent's profile. For a consumer that runs with `PODIUM_CACHE_MODE=offline-only`, clear its cache directory and refill it in `always-revalidate` or `offline-first`, because an `offline-only` consumer never calls the registry and answers every load from a pruned cache with `network.offline_cache_miss`. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the ranking.
```

## Open questions

- **Q-1. Serving a stored conflicting version.** Should a version stored before the upgrade, whose chain pairs two conflicting profiles, keep serving with the parent's value (recommended; it matches the maintainer's default and 0057 D-6), or should its read fail closed? Failing closed would reuse the existing `registry.invalid_argument` path that `mergeChain` takes for an unhidable parent (`pkg/registry/core/core.go:1950-1960`). Keeping it serving changes the served profile for three pairs where the child out-ranked the parent under the removed `sandboxRank`: child `network-isolated` over parent `read-only-fs`, child `seccomp-strict` over parent `read-only-fs`, and child `seccomp-strict` over parent `network-isolated`. Each served value of a conflicting pair drops one restriction, both today and after the change. Failing closed serves no conflicting pair, and it makes those artifacts unreadable until their authors publish a non-conflicting version. The filesystem source drops them on every run either way. Neither answer reaches a `podium-mcp` whose content cache already holds such a version, because its HEAD revalidation compares only the content hash and the merged record keeps the child's (D-7). Failing closed for those consumers as well would also need the load validator and the `X-Podium-Content-Hash` header to cover the merged record (`spec/07-external-integration.md:45`; `pkg/registry/server/server.go:1186`, `:1238-1254`), and that header's value is the hash `podium-mcp` uses as its §6.5 content-bucket key (`cmd/podium-mcp/main.go:2930-2935`). Resolved at sign-off: stored conflicting versions keep serving with the parent's value, as D-7 states.

## Non-goals

- A set-valued sandbox profile whose values merge by union. It would represent `read-only-fs` plus `network-isolated` faithfully, but it changes the frontmatter type from a scalar enum (`spec/04-artifact-model.md:175`) to a list. That change reaches every consumer: the §4.4.1 host gate and its `PODIUM_HOST_SANDBOXES` membership test (`cmd/podium-mcp/main.go:2335-2364`), the seccomp baseline writer (`:2010`), the `SKILL.md` `compatibility` derivation (`pkg/adapter/compatibility.go:35-62`), the §6.7.1 capability-matrix row (`pkg/adapter/capability.go:97`), the §13.10 ingest gate (`pkg/registry/ingest/ingest.go:1788-1798`), and the SDKs. Hosts would also need to enforce combinations of profiles that §4.4.1 never defines. The refusal closes the silent loosening with no new surface, and a later proposal can introduce composition if authors need it.
- Validating `sandbox_profile` against the §4.4.1 enum in lint or at ingest. The host gate already fails closed on an unknown value (D-4), and 0057 deferred validation to a separate proposal.
- Stating in the spec that `seccomp-strict` contains `network-isolated`, or changing the shipped seccomp baseline (`pkg/materialize/seccomp-strict.json`).
- Running the §13.10 `ingest.sandbox_profile_unenforceable` gate on the merged value rather than the child's authored value (`pkg/registry/ingest/ingest.go:604-616`).
- Re-judging versions already stored, a migration report, a grandfathering flag, or keeping the old ranking as a fallback for stored data (D-7, D-11).
- Changing the load ETag or the `X-Podium-Content-Hash` header so that a `podium-mcp` cache drops a record cached before the upgrade (D-7).
- Changing the merge of `sensitivity` or `search_visibility`, which have spec-named total orders (`spec/04-artifact-model.md:700`, `:708`), or editing the §1 overview summary.
- Making `podium lint` evaluate the rule. Lint resolves no `extends:` chain, as 0057 recorded for the executable-field rule.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-09)

- CODE-2's revision replaced the `layer.SandboxProfileConflict` struct with two constants and the sentinel `ErrSandboxProfileConflict`, but the CODE-4, TEST-1, TEST-2, and TEST-3 sketches still called `Reason()`, `Details()`, and `errors.As` on the struct. CODE-4 now wraps the sentinel, TEST-1 asserts the constants and `errors.Is`, TEST-2 compares `Reason` with `layer.SandboxProfileConflictReason` and asserts `Details` on the server rejection, and TEST-3 (d) uses `errors.Is`.
- CODE-3's revision gated the chain walk on a cross-layer edge or a restricting child, but D-8, the Summary, the edge-case table, and the DOC-2 CHANGELOG bullet still said every same-layer `extends:` child walks. All four now scope the new same-layer walk, and its new rejections, to a child that authors a restricting `sandbox_profile`. TEST-2 carries the gate case.
- TEST-1 asked for the reason to contain none of the §4.4.1 profile values, but the reason's own text contains `unrestricted`. TEST-1, TEST-2, TEST-4, and S96 assert the absence of the three restricting values.
- The merge-table fixture edit in TEST-1 and TEST-4 (a) would have landed with or after CODE-1, leaving `TestMergeExtends_FieldSemanticsTable` and `TestExtends_SandboxChildTightens` red at the CODE-1 commit. Both edits, and the `TestExtends_SandboxChildCannotWiden` comment update, moved to TEST-5, which lands before CODE-1 and passes before and after it. D-12 records the order.
- D-12 bundles CODE-3 with CODE-4, and the checklist carries them in one step so that no commit has one mode refusing what the other merges.
- The edge-case table required a staged sentence for the deferred §13.10 gate behavior, and no docs page stated it. DOC-1 item 2 adds the sentence to the new *Sandbox profile* section.
- SPEC-1's draft text for Edit 3 carried two defects the challenge named: the claim that no profile includes another, and "this paragraph" pointing at the wrong paragraph. Both are corrected in the staged text, and the rebase onto 0062 is recorded as a pre-landing step and a Watch-out.

### Pass 1 (2026-11-09, automated)

- A stored conflicting chain changes its merged `sandbox_profile` under an unchanged validator: `mergeChain` keeps the child's content hash (`pkg/registry/core/core.go:1919`), the load ETag and `X-Podium-Content-Hash` derive only from that hash and the `extends_pin` (`pkg/registry/server/server.go:1186`, `:1238-1254`), and `podium-mcp` delivers a cached record on a HEAD hash match, in `offline-first`, and from the degraded-network fallback, so a warm cache kept the pre-upgrade profile while D-7, SPEC-1, the edge-case table, Q-1, and the CHANGELOG described only the registry's new value. The fix accepts the transition with a documented remedy rather than changing the §7.2 validator: D-7 gains a paragraph on the warm-cache behavior, its evidence, the `podium cache prune --days 0` remedy, and the reason the validator is unchanged; SPEC-1's stored-version sentence names the registry as the party that serves the merged value, and its rationale says why; the edge-case table scopes the stored-version row to the registry and adds a warm-cache row; Q-1 records that neither answer reaches a warm cache without a validator change; DOC-1 item 2 and the DOC-2 CHANGELOG bullet carry the operator note; TEST-4 gains (e), a `podium-mcp` unit test that pins delivery of the cached `seccomp-strict`; S96 steps 4 and 5 warm a `podium-mcp` cache under the old release and load through it after the upgrade, cold, and after a prune; the Summary, the D-7 line, a new Watch-out, the S9 checklist step, the S9 commands, and Non-goals follow.
- Correction to this pass: D-7, the warm-cache edge-case row, and DOC-1 item 2 named publishing a new non-conflicting child version as a remedy beside the prune, and the DOC-1 bullet carried no `latest` qualifier. A new content hash changes only what a HEAD revalidation reports. `offline-first` resolves `latest` with `allowStale` set and serves the cached hash without a registry call (`cmd/podium-mcp/main.go:1350-1366`, `cmd/podium-mcp/resolution_cache.go:384-390`), the degraded-network fallback serves the cached record, and a pinned resolution never expires (`cmd/podium-mcp/resolution_cache.go:350-353`). The three statements now scope the new-version remedy to an `always-revalidate` load of `latest` that reaches the registry and name `podium cache prune --days 0` as the remedy for every mode, which agrees with the DOC-2 CHANGELOG bullet. After a prune, `cachedOrRefetch` finds no record and the load falls through to a fetch (`cmd/podium-mcp/main.go:1574-1578`).

### Pass 2 (2026-11-09, automated)

- The warm-cache remedy claimed `podium cache prune --days 0` makes the next load fetch the parent's profile in every mode, and the stale-mode lists omitted `offline-only`. An `offline-only` `podium-mcp` never calls the registry (`spec/06-mcp-server.md:247`), so it keeps the child's earlier profile indefinitely. After a prune its resolution index survives (`cmd/podium/cache.go:95`), the content bucket is gone, and the load returns `network.offline_cache_miss` (`cmd/podium-mcp/main.go:1373-1374`, `cmd/podium-mcp/resolution_cache.go:546`) for every artifact in that cache. D-7, the warm-cache edge-case row, DOC-1 item 2, the DOC-2 CHANGELOG bullet, the Summary D-7 line, and the Summary watch-out now name `offline-only` as a stale mode and scope the prune remedy to `always-revalidate` and `offline-first`. For `offline-only` they give the remedy `docs/deployment/operator-guide.md:134` and `docs/deployment/clustered.md:443` give for key rotation: clear the cache directory and refill it in `always-revalidate` or `offline-first`.
